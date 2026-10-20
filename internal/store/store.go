package store

import "time"

// Store is the keyspace owned by the executor goroutine. It has no locks:
// ADR-002 makes that goroutine the only accessor. Expiries live in a separate
// dict, as in Redis, so the active cycle can sample only volatile keys.
type Store struct {
	data    map[string][]byte
	expires map[string]int64
}

func New() *Store {
	return &Store{
		data:    make(map[string][]byte),
		expires: make(map[string]int64),
	}
}

// Get returns the stored bytes and whether the key is live. An expired key is
// deleted on access (lazy expiry).
func (s *Store) Get(key string) ([]byte, bool) {
	return s.live(key)
}

// Set stores value under key and clears any previous expiry.
func (s *Store) Set(key string, value []byte) {
	s.data[key] = value
	delete(s.expires, key)
}

// SetWithTTL stores value under key with an absolute Unix-millisecond expiry.
func (s *Store) SetWithTTL(key string, value []byte, expiresAtMs int64) {
	s.data[key] = value
	s.expires[key] = expiresAtMs
}

// SetKeepTTL overwrites value while preserving an existing live expiry.
func (s *Store) SetKeepTTL(key string, value []byte) {
	if _, ok := s.live(key); !ok {
		s.Set(key, value)
		return
	}
	s.data[key] = value
}

// Delete removes key and reports whether a live key was removed.
func (s *Store) Delete(key string) bool {
	if _, ok := s.live(key); !ok {
		return false
	}
	delete(s.data, key)
	delete(s.expires, key)
	return true
}

func (s *Store) Exists(key string) bool {
	_, ok := s.live(key)
	return ok
}

// Len counts live keys, dropping expired ones it encounters.
func (s *Store) Len() int {
	n := 0
	for key := range s.data {
		if _, ok := s.live(key); ok {
			n++
		}
	}
	return n
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
	ttl := at - nowMs()
	if ttl < 0 {
		ttl = 0
	}
	return ttl, true
}

// SetExpire sets the absolute expiry of an existing key, deleting it when the
// deadline is already past.
func (s *Store) SetExpire(key string, atMs int64) {
	if atMs <= nowMs() {
		s.Delete(key)
		return
	}
	s.expires[key] = atMs
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
	return true
}

func (s *Store) live(key string) ([]byte, bool) {
	value, ok := s.data[key]
	if !ok {
		return nil, false
	}
	if at, ok := s.expires[key]; ok && nowMs() > at {
		delete(s.data, key)
		delete(s.expires, key)
		return nil, false
	}
	return value, true
}

func nowMs() int64 { return time.Now().UnixMilli() }
