package store

import (
	"bytes"
	"hash/maphash"

	"github.com/silent-knight19/antares/internal/clock"
)

// Store is the keyspace owned by the executor goroutine. It has no locks:
// ADR-002 makes that goroutine the only accessor. Expiries live in a separate
// dict, as in Redis, so the active cycle can sample only volatile keys.
type Kind string

const (
	KindString Kind = "string"
	KindList   Kind = "list"
	KindHash   Kind = "hash"
	KindSet    Kind = "set"
)

// Value is a typed keyspace entry. String values use Str; list values use
// List, where the live elements are List[ListHead:]. The unused prefix is
// spare room for LPUSH. HashIndex/SetIndex map field/member bytes to their
// slice position so bulk mutations do not rescan the whole collection. Lru is
// the 24-bit access clock (seconds) used by approximated-LRU eviction; it is
// refreshed on every lookup and write.
type Value struct {
	Kind      Kind
	Str       []byte
	List      [][]byte
	ListHead  int
	Hash      []HashEntry
	HashIndex map[string]int
	Set       [][]byte
	SetIndex  map[string]int
	Lru       uint32
	Usage     Usage
}

// LRU clock: 24 bits of seconds, as in Redis. It wraps every ~194 days, and the
// idle time is computed modulo the mask so a wrap never reads as a huge age.
const (
	lruBits      = 24
	lruClockMask = 1<<lruBits - 1
)

// Sample is one key offered to the eviction sampler: its name and seconds since
// last access on the 24-bit clock.
type Sample struct {
	Key  string
	Idle uint32
}

// HashEntry preserves Redis's small-hash insertion order.
type HashEntry struct {
	Field []byte
	Value []byte
}

type Store struct {
	data        map[string]Value
	expires     map[string]int64
	clock       clock.Clock
	scanSeed    maphash.Seed
	expiredKeys uint64
	mutations   uint64
	usage       Usage
}

func New() *Store { return NewWithClock(clock.Real{}) }

func NewWithClock(c clock.Clock) *Store {
	if c == nil {
		c = clock.Real{}
	}
	return &Store{
		data:     make(map[string]Value),
		expires:  make(map[string]int64),
		clock:    c,
		scanSeed: maphash.MakeSeed(),
	}
}

// NowMs returns the current time from the store's injected clock.
func (s *Store) NowMs() int64 { return s.clock.Now().UnixMilli() }

// lruClock is the current time on the 24-bit seconds clock.
func (s *Store) lruClock() uint32 { return uint32(s.clock.Now().Unix()) & lruClockMask }

// SampleKeys returns up to n live-or-expired keys with their idle seconds,
// choosing from all keys or only volatile (expiring) ones. Go's randomized map
// iteration provides the sample, as approximated LRU intends. An idle time
// that wrapped is reported modulo the clock mask.
func (s *Store) SampleKeys(volatileOnly bool, n int) []Sample {
	if n <= 0 {
		return nil
	}
	now := s.lruClock()
	out := make([]Sample, 0, n)
	if volatileOnly {
		for key := range s.expires {
			v, ok := s.data[key]
			if !ok {
				continue
			}
			if len(out) == n {
				break
			}
			out = append(out, Sample{Key: key, Idle: (now - v.Lru) & lruClockMask})
		}
		return out
	}
	for key, v := range s.data {
		if len(out) == n {
			break
		}
		out = append(out, Sample{Key: key, Idle: (now - v.Lru) & lruClockMask})
	}
	return out
}

// Mutations counts explicit keyspace writes. Expiry (lazy or active) is not a
// command effect, so the executor can use this to decide whether to append.
func (s *Store) Mutations() uint64 { return s.mutations }

// Copy returns an independent point-in-time keyspace for AOF rewrite. It must
// only be called by the executor goroutine; collection payloads are cloned
// because later commands mutate their backing slices in place.
func (s *Store) Copy() *Store {
	cp := NewWithClock(s.clock)
	cp.expiredKeys = s.expiredKeys
	cp.mutations = s.mutations
	cp.usage = s.usage
	for key, value := range s.data {
		cp.data[key] = cloneValue(value)
	}
	for key, at := range s.expires {
		cp.expires[key] = at
	}
	return cp
}

func cloneValue(v Value) Value {
	out := Value{
		Kind:     v.Kind,
		Str:      bytes.Clone(v.Str),
		ListHead: v.ListHead,
		Lru:      v.Lru,
		Usage:    v.Usage,
	}
	if v.List != nil {
		out.List = make([][]byte, len(v.List))
		for i, element := range v.List {
			out.List[i] = bytes.Clone(element)
		}
	}
	if v.Hash != nil {
		out.Hash = make([]HashEntry, len(v.Hash))
		for i, entry := range v.Hash {
			out.Hash[i] = HashEntry{Field: bytes.Clone(entry.Field), Value: bytes.Clone(entry.Value)}
		}
	}
	if v.Set != nil {
		out.Set = make([][]byte, len(v.Set))
		for i, member := range v.Set {
			out.Set[i] = bytes.Clone(member)
		}
	}
	return out
}

// Lookup returns the typed value and whether the key is live. An expired key
// is deleted on access (lazy expiry).
func (s *Store) Lookup(key string) (Value, bool) {
	return s.live(key)
}

// Type returns the Redis type name for a live key, or "none".
func (s *Store) Type(key string) string {
	v, ok := s.live(key)
	if !ok {
		return "none"
	}
	return string(v.Kind)
}

// Set stores value under key and clears any previous expiry.
func (s *Store) Set(key string, value []byte) {
	v := Value{Kind: KindString, Str: value, Lru: s.lruClock()}
	s.storeValue(key, v)
	delete(s.expires, key)
	s.mutations++
}

// SetWithTTL stores value under key with an absolute Unix-millisecond expiry.
func (s *Store) SetWithTTL(key string, value []byte, expiresAtMs int64) {
	v := Value{Kind: KindString, Str: value, Lru: s.lruClock()}
	s.storeValue(key, v)
	s.expires[key] = expiresAtMs
	s.mutations++
}

// storeValue replaces key's value and updates the incremental memory total.
func (s *Store) storeValue(key string, value Value) {
	s.removeValue(key)
	value.Usage = usageOf(key, value)
	s.data[key] = value
	s.usage.Total += value.Usage.Total
	s.usage.Dataset += value.Usage.Dataset
}

// removeValue subtracts key's accounted bytes without touching the maps.
func (s *Store) removeValue(key string) {
	if old, ok := s.data[key]; ok {
		s.usage.Total -= old.Usage.Total
		s.usage.Dataset -= old.Usage.Dataset
	}
}

// SetPreservingTTL overwrites key without changing its expiry. Callers must
// have observed the key at command start; this deliberately skips the live
// check so a key expiring mid-command cannot be resurrected without a TTL.
func (s *Store) SetPreservingTTL(key string, value Value) {
	if value.Kind == KindHash && value.HashIndex == nil {
		value.HashIndex = buildHashIndex(value.Hash)
	}
	if value.Kind == KindSet && value.SetIndex == nil {
		value.SetIndex = buildSetIndex(value.Set)
	}
	// Stamp the access clock here so a freshly created value never reads as
	// maximally idle (handlers build Value literals with Lru unset).
	value.Lru = s.lruClock()
	s.storeValue(key, value)
	s.mutations++
}

func buildHashIndex(hash []HashEntry) map[string]int {
	index := make(map[string]int, len(hash))
	for i, entry := range hash {
		index[string(entry.Field)] = i
	}
	return index
}

func buildSetIndex(members [][]byte) map[string]int {
	index := make(map[string]int, len(members))
	for i, member := range members {
		index[string(member)] = i
	}
	return index
}

// Delete removes key and reports whether a live key was removed.
func (s *Store) Delete(key string) bool {
	if _, ok := s.live(key); !ok {
		return false
	}
	s.removeValue(key)
	delete(s.data, key)
	delete(s.expires, key)
	s.mutations++
	return true
}

// Rename moves src to dst, overwriting dst and replacing its expiry with src's.
// Callers must have observed src at command start; this deliberately skips the
// live check so a key expiring mid-command is moved with its original deadline.
func (s *Store) Rename(src, dst string) {
	value := s.data[src]
	expiresAt, hasExpiry := s.expires[src]
	s.removeValue(src)
	s.removeValue(dst)
	delete(s.data, src)
	delete(s.expires, src)
	delete(s.data, dst)
	delete(s.expires, dst)
	s.storeValue(dst, value)
	if hasExpiry {
		s.expires[dst] = expiresAt
	}
	s.mutations++
}

func (s *Store) Exists(key string) bool {
	_, ok := s.live(key)
	return ok
}

// VolatileLen returns the physical number of keys with an expiry.
func (s *Store) VolatileLen() int { return len(s.expires) }

// ExpiredCount returns the number of keys removed by lazy or active expiry.
func (s *Store) ExpiredCount() uint64 { return s.expiredKeys }

// Len returns the physical key count, including expired keys not yet removed
// by lazy access or the active cycle. This is Redis's DBSIZE semantics.
func (s *Store) Len() int {
	return len(s.data)
}

// Keys returns live key names. It does not delete expired keys, matching
// Redis's KEYS, which skips them without touching the expires dict.
func (s *Store) Keys() []string {
	now := s.NowMs()
	keys := make([]string, 0, len(s.data))
	for key := range s.data {
		if at, ok := s.expires[key]; ok && now > at {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// RandomKey returns a live key, deleting expired keys it encounters. Redis
// retries until it finds a live key or the keyspace is empty.
func (s *Store) RandomKey() (string, bool) {
	for len(s.data) > 0 {
		for key := range s.data {
			if _, ok := s.live(key); ok {
				return key, true
			}
		}
	}
	return "", false
}

// Flush empties the keyspace.
func (s *Store) Flush() {
	clear(s.data)
	clear(s.expires)
	s.usage = Usage{}
	s.mutations++
}

// ExpireSample samples up to limit volatile keys and deletes those whose
// deadline is before nowMs. It returns the sampled and expired counts.
func (s *Store) ExpireSample(nowMs int64, limit int) (expired, sampled int) {
	if limit <= 0 {
		return 0, 0
	}
	for key, at := range s.expires {
		if sampled == limit {
			break
		}
		sampled++
		if nowMs > at {
			s.removeValue(key)
			delete(s.data, key)
			delete(s.expires, key)
			s.expiredKeys++
			expired++
		}
	}
	return expired, sampled
}

// ExpiresAt returns the absolute expiry of a live key and whether the key
// exists; 0 means the key has no expiry.
func (s *Store) ExpiresAt(key string) (int64, bool) {
	if _, ok := s.live(key); !ok {
		return 0, false
	}
	return s.expires[key], true
}

// TTLms returns the remaining milliseconds for a live key, -1 when it has no
// expiry, and false when the key does not exist.
func (s *Store) TTLms(key string) (int64, bool) {
	at, ok := s.ExpiresAt(key)
	if !ok {
		return 0, false
	}
	if at == 0 {
		return -1, true
	}
	ttl := at - s.NowMs()
	if ttl < 0 {
		ttl = 0
	}
	return ttl, true
}

// SetExpire sets the absolute expiry of an existing key, deleting it when the
// deadline is already past.
func (s *Store) SetExpire(key string, atMs int64) {
	if atMs <= s.NowMs() {
		s.Delete(key)
		return
	}
	s.expires[key] = atMs
	s.mutations++
}

// Persist removes a key's expiry and reports whether one was removed.
func (s *Store) Persist(key string) bool {
	if _, ok := s.live(key); !ok {
		return false
	}
	if _, ok := s.expires[key]; !ok {
		return false
	}
	delete(s.expires, key)
	s.mutations++
	return true
}

func (s *Store) live(key string) (Value, bool) {
	value, ok := s.data[key]
	if !ok {
		return Value{}, false
	}
	if at, ok := s.expires[key]; ok && s.NowMs() > at {
		s.removeValue(key)
		delete(s.data, key)
		delete(s.expires, key)
		s.expiredKeys++
		return Value{}, false
	}
	value.Lru = s.lruClock()
	s.data[key] = value
	return value, true
}
