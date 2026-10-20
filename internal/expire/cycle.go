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

// Run performs one active-expiry cycle as of nowMs. It returns the number of
// sampled and expired keys for tests and observability.
func (c *Cycle) Run(nowMs int64, budget time.Duration) (expired, sampled int) {
	start := c.clock.Now()
	for {
		e, s := c.store.ExpireSample(nowMs, sampleSize)
		expired += e
		sampled += s
		if s == 0 || e*100 <= s*expiredPercent {
			return expired, sampled
		}
		if budget > 0 && c.clock.Since(start) >= budget {
			return expired, sampled
		}
	}
}
