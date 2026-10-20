// Package clock provides the injected time source used by all domain logic
// (ADR-009). Network deadlines and accept backoff deliberately stay on real
// time; they are I/O policy, not simulated server time.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) Since(t time.Time) time.Duration { return time.Since(t) }

// Fake is a manually advanced clock for deterministic tests.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(now time.Time) *Fake { return &Fake{now: now} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now.Sub(t)
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
