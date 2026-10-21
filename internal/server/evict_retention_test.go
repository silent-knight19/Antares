package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/store"
)

// TestEvictionRetainsHotKeys proves the approximated LRU keeps a hot working
// set under cold churn: over 90% of the repeatedly-read keys survive a workload
// that inserts many times more keys than maxmemory can hold. The hot keys are
// refreshed each round while fresh cold keys keep arriving, so only sampling
// quality decides whether they survive.
func TestEvictionRetainsHotKeys(t *testing.T) {
	fake := clock.NewFake(time.Unix(4_000_000, 0))
	exec := NewExecutorWithClock(store.NewWithClock(fake), nil, fake)
	defer exec.Close()

	value := strings.Repeat("x", 32)
	const hotCount = 50
	hot := make([]string, hotCount)
	for i := range hot {
		hot[i] = fmt.Sprintf("hot:%d", i)
		runStrings(exec, "SET", hot[i], value)
	}
	if got := runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "allkeys-lru"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET policy = %q", got)
	}
	if got := runStrings(exec, "CONFIG", "SET", "maxmemory", "300000"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET maxmemory = %q", got)
	}

	const rounds = 1000
	const coldPerRound = 5
	for round := 0; round < rounds; round++ {
		fake.Advance(20 * time.Second)
		for _, key := range hot {
			runStrings(exec, "GET", key)
		}
		for i := 0; i < coldPerRound; i++ {
			runStrings(exec, "SET", fmt.Sprintf("cold:%d:%d", round, i), value)
		}
	}

	// Check the keyspace directly so the measurement itself does not trigger
	// eviction through the command gate.
	survived := 0
	for _, key := range hot {
		if exec.store.Exists(key) {
			survived++
		}
	}
	retained := float64(survived) / float64(hotCount)
	t.Logf("hot retention = %.3f (%d/%d)", retained, survived, hotCount)
	if retained <= 0.9 {
		t.Fatalf("hot retention = %.2f (%d/%d), want > 0.90", retained, survived, hotCount)
	}
}
