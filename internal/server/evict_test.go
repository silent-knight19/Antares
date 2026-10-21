package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/events"
	"github.com/silent-knight19/antares/internal/evict"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

type recordingSink struct{ events []events.Event }

func (r *recordingSink) Emit(e events.Event) { r.events = append(r.events, e) }

func TestEvictAllKeysLRU(t *testing.T) {
	fake := clock.NewFake(time.Unix(1_000_000, 0))
	exec := NewExecutorWithClock(store.NewWithClock(fake), nil, fake)
	defer exec.Close()
	rec := &recordingSink{}
	exec.events = rec

	value := strings.Repeat("x", 1024)
	for i := 0; i < 20; i++ {
		if got := runStrings(exec, "SET", fmt.Sprintf("k%d", i), value); got != "+OK\r\n" {
			t.Fatalf("fill SET k%d = %q", i, got)
		}
	}
	fake.Advance(time.Hour)
	runStrings(exec, "GET", "k0")

	if got := runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "allkeys-lru"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET policy = %q", got)
	}
	if got := runStrings(exec, "CONFIG", "SET", "maxmemory", "20000"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET maxmemory = %q", got)
	}

	// Over the limit, an LRU policy evicts to make room rather than rejecting.
	if got := runStrings(exec, "SET", "new", value); got != "+OK\r\n" {
		t.Fatalf("SET under allkeys-lru = %q, want OK", got)
	}
	if got := runStrings(exec, "EXISTS", "k0"); got != ":1\r\n" {
		t.Fatalf("recently used key evicted: EXISTS k0 = %q", got)
	}
	if len(rec.events) == 0 {
		t.Fatal("no eviction events emitted")
	}
	for _, e := range rec.events {
		if e.Type != events.Evict || e.Policy != "allkeys-lru" || e.Sampled != evict.Samples || e.Version != events.Version {
			t.Fatalf("unexpected event %+v", e)
		}
	}
}

func TestEvictVolatileLRUOnly(t *testing.T) {
	fake := clock.NewFake(time.Unix(2_000_000, 0))
	exec := NewExecutorWithClock(store.NewWithClock(fake), nil, fake)
	defer exec.Close()

	value := strings.Repeat("x", 1024)
	runStrings(exec, "SET", "plain", value)
	runStrings(exec, "SET", "vol", value, "EX", "1000")
	fake.Advance(time.Minute)

	if got := runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "volatile-lru"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET policy = %q", got)
	}
	if got := runStrings(exec, "CONFIG", "SET", "maxmemory", "300"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET maxmemory = %q", got)
	}

	oom := "-" + protocol.OutOfMemory + "\r\n"
	if got := runStrings(exec, "SET", "new", value); got != oom {
		t.Fatalf("SET under volatile-lru = %q, want %q", got, oom)
	}
	if got := runStrings(exec, "EXISTS", "vol"); got != ":0\r\n" {
		t.Fatalf("volatile key should be evicted: EXISTS vol = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "plain"); got != ":1\r\n" {
		t.Fatalf("non-volatile key should survive: EXISTS plain = %q", got)
	}
}
