package evict

import (
	"fmt"
	"testing"
)

func TestParsePolicy(t *testing.T) {
	cases := map[string]Policy{
		"allkeys-lru":    AllKeysLRU,
		"volatile-lru":   VolatileLRU,
		"noeviction":     NoEviction,
		"allkeys-random": NoEviction,
		"volatile-lfu":   NoEviction,
	}
	for in, want := range cases {
		if got := ParsePolicy(in); got != want {
			t.Fatalf("ParsePolicy(%q) = %q, want %q", in, got, want)
		}
	}
	if !AllKeysLRU.Evicts() || AllKeysLRU.Volatile() || !VolatileLRU.Volatile() || NoEviction.Evicts() {
		t.Fatal("policy predicates disagree")
	}
}

func TestPoolPopsMostIdle(t *testing.T) {
	var p Pool
	p.Insert("a", 1)
	p.Insert("b", 5)
	p.Insert("c", 3)

	for _, want := range []struct {
		key  string
		idle uint32
	}{{"b", 5}, {"c", 3}, {"a", 1}} {
		key, idle, ok := p.PopBest()
		if !ok || key != want.key || idle != want.idle {
			t.Fatalf("PopBest = (%q,%d,%v), want (%q,%d)", key, idle, ok, want.key, want.idle)
		}
	}
	if _, _, ok := p.PopBest(); ok {
		t.Fatal("PopBest on empty pool returned ok")
	}
}

func TestPoolBoundedToMostIdle(t *testing.T) {
	var p Pool
	for i := 0; i < 100; i++ {
		p.Insert(fmt.Sprintf("k%d", i), uint32(i))
	}
	if p.Len() != PoolSize {
		t.Fatalf("pool len = %d, want %d", p.Len(), PoolSize)
	}
	for i := 99; i > 99-PoolSize; i-- {
		key, idle, ok := p.PopBest()
		if !ok || key != fmt.Sprintf("k%d", i) || idle != uint32(i) {
			t.Fatalf("PopBest = (%q,%d,%v), want (k%d,%d)", key, idle, ok, i, i)
		}
	}
}
