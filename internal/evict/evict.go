package evict

// Policy is a maxmemory-eviction policy. The LFU, random, and TTL names Redis
// also accepts are stored by CONFIG SET but fall back to NoEviction until they
// are implemented.
type Policy string

const (
	NoEviction  Policy = "noeviction"
	AllKeysLRU  Policy = "allkeys-lru"
	VolatileLRU Policy = "volatile-lru"
)

// Sample size and pool bound mirror Redis: sample 5 keys per round and keep the
// 16 most-idle candidates seen so far (ADR-006).
const (
	Samples  = 5
	PoolSize = 16
)

// ParsePolicy maps a CONFIG value to a policy. Any name outside the supported
// set yields NoEviction, so an unimplemented policy rejects rather than evicts.
func ParsePolicy(s string) Policy {
	switch Policy(s) {
	case AllKeysLRU, VolatileLRU:
		return Policy(s)
	default:
		return NoEviction
	}
}

// Evicts reports whether the policy removes keys under memory pressure.
func (p Policy) Evicts() bool { return p == AllKeysLRU || p == VolatileLRU }

// Volatile reports whether the policy samples only keys that carry an expiry.
func (p Policy) Volatile() bool { return p == VolatileLRU }

type entry struct {
	key  string
	idle uint32
}

// Pool holds the most-idle candidates seen during an eviction round, bounded to
// PoolSize. Keeping the worst-seen keys across samples improves victim quality
// beyond a single sample-of-5 without a global ordering, as Redis does.
type Pool struct {
	entries []entry
}

// Insert offers a candidate. A full pool drops its least-idle entry, so the
// survivors are always the most-idle keys seen.
func (p *Pool) Insert(key string, idle uint32) {
	if len(p.entries) < PoolSize {
		p.entries = append(p.entries, entry{key: key, idle: idle})
		return
	}
	worst := 0
	for i := 1; i < len(p.entries); i++ {
		if p.entries[i].idle < p.entries[worst].idle {
			worst = i
		}
	}
	if idle <= p.entries[worst].idle {
		return
	}
	p.entries[worst] = entry{key: key, idle: idle}
}

// PopBest removes and returns the most-idle candidate.
func (p *Pool) PopBest() (key string, idle uint32, ok bool) {
	if len(p.entries) == 0 {
		return "", 0, false
	}
	best := 0
	for i := 1; i < len(p.entries); i++ {
		if p.entries[i].idle > p.entries[best].idle {
			best = i
		}
	}
	e := p.entries[best]
	p.entries = append(p.entries[:best], p.entries[best+1:]...)
	return e.key, e.idle, true
}

// Len returns the number of pooled candidates.
func (p *Pool) Len() int { return len(p.entries) }
