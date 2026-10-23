package expire

import (
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/store"
)

const (
	sampleSize     = 20
	expiredPercent = 25
)

// Cycle runs Redis-style active expiry: sample volatile keys, stop once at
// most 25% of a sample is expired, and repeat while the sample is mostly
// expired until the per-cycle budget is spent.
type Cycle struct {
	store *store.Store
	clock clock.Clock
}

// New returns a cycle over st using c for its time budget.
func New(st *store.Store, c clock.Clock) *Cycle {
	if c == nil {
		c = clock.Real{}
	}
	return &Cycle{store: st, clock: c}
}

// RunKeys performs one active-expiry cycle as of nowMs and returns all deleted key names.
func (c *Cycle) RunKeys(nowMs int64, budget time.Duration) (expiredKeys []string, sampled int) {
	start := c.clock.Now()
	for {
		keys, s := c.store.ExpireSampleKeys(nowMs, sampleSize)
		expiredKeys = append(expiredKeys, keys...)
		sampled += s
		if s == 0 || len(keys)*100 <= s*expiredPercent {
			return expiredKeys, sampled
		}
		if budget > 0 && c.clock.Since(start) >= budget {
			return expiredKeys, sampled
		}
	}
}

// Run performs one active-expiry cycle as of nowMs. It returns the number of
// sampled and expired keys for tests and observability.
func (c *Cycle) Run(nowMs int64, budget time.Duration) (expired, sampled int) {
	keys, sampled := c.RunKeys(nowMs, budget)
	return len(keys), sampled
}
