package store

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestNextScanCursor(t *testing.T) {
	cursor := uint64(0)
	var got []uint64
	for range 8 {
		got = append(got, cursor)
		cursor = nextScanCursor(cursor, 7)
	}
	want := []uint64{0, 4, 2, 6, 1, 5, 3, 7}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cursor sequence = %v, want %v", got, want)
		}
	}
	if cursor != 0 {
		t.Fatalf("cursor after a full cycle = %d, want 0", cursor)
	}
}

func TestStoreScanCoversAllKeys(t *testing.T) {
	s := New()
	want := make(map[string]bool)
	for i := range 500 {
		key := "key:" + string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260))
		s.Set(key, []byte("v"))
		want[key] = true
	}

	seen := make(map[string]bool)
	cursor := uint64(0)
	for steps := 0; steps < 10000; steps++ {
		next, keys := s.Scan(cursor, 7, nil, "", false)
		for _, key := range keys {
			seen[key] = true
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if cursor != 0 {
		t.Fatal("SCAN did not terminate")
	}
	for key := range want {
		if !seen[key] {
			t.Fatalf("SCAN missed %q", key)
		}
	}
}

func TestStoreScanGuaranteesAcrossGrowthAndShrink(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)
	stable := make(map[string]bool)
	for i := range 100 {
		key := "stable:" + string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260))
		s.Set(key, []byte("v"))
		stable[key] = true
	}
	extra := make([]string, 0, 900)
	for i := range 900 {
		key := "extra:" + string(rune('a'+i%26)) + string(rune('0'+i/26%10)) + string(rune('0'+i/260))
		s.Set(key, []byte("v"))
		extra = append(extra, key)
	}

	seen := make(map[string]bool)
	cursor := uint64(0)
	for steps := 0; steps < 10000; steps++ {
		next, keys := s.Scan(cursor, 10, nil, "", false)
		for _, key := range keys {
			seen[key] = true
		}
		if steps == 0 {
			for i := range 500 {
				s.Set("grown:"+string(rune('a'+i%26))+string(rune('0'+i/26%10)), []byte("v"))
			}
		}
		if steps == 5 {
			for _, key := range extra {
				s.Delete(key)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if cursor != 0 {
		t.Fatal("SCAN did not terminate")
	}
	for key := range stable {
		if !seen[key] {
			t.Fatalf("SCAN missed stable key %q", key)
		}
	}
}

func TestStoreScanFilterBeforeExpiry(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)
	s.SetWithTTL("dead:match", []byte("v"), 1100)
	s.SetWithTTL("dead:other", []byte("v"), 1100)
	s.Set("live", []byte("v"))
	f.Advance(200 * time.Millisecond)

	match := func(key []byte) bool { return string(key) == "dead:match" }
	_, keys := s.Scan(0, 100, match, "", false)
	if len(keys) != 0 {
		t.Fatalf("SCAN returned expired keys: %v", keys)
	}
	if _, ok := s.data["dead:match"]; ok {
		t.Fatal("matching expired key was not deleted")
	}
	if _, ok := s.data["dead:other"]; !ok {
		t.Fatal("non-matching expired key was deleted")
	}
	if n := len(s.Keys()); n != 1 {
		t.Fatalf("live keys = %d, want 1", n)
	}

	s.Scan(0, 100, nil, "", false)
	if _, ok := s.data["dead:other"]; ok {
		t.Fatal("expired key was not deleted without a pattern")
	}
}

func TestStoreScanTypeFilterExpiresFirst(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)
	s.SetWithTTL("dead:list", []byte("v"), 1100)
	f.Advance(200 * time.Millisecond)

	_, keys := s.Scan(0, 100, nil, KindString, true)
	if len(keys) != 0 {
		t.Fatalf("SCAN TYPE returned %v", keys)
	}
	if len(s.data) != 0 {
		t.Fatal("SCAN TYPE did not expire a matching-pattern key of another type")
	}
}

func TestScanTableSize(t *testing.T) {
	for n, want := range map[int]uint64{0: 4, 1: 4, 4: 4, 5: 8, 8: 8, 9: 16, 1000: 1024} {
		if got := scanTableSize(n); got != want {
			t.Errorf("scanTableSize(%d) = %d, want %d", n, got, want)
		}
	}
}

func TestStoreScanEmpty(t *testing.T) {
	if cursor, keys := New().Scan(0, 10, nil, "", false); cursor != 0 || len(keys) != 0 {
		t.Fatalf("empty SCAN = %d, %v", cursor, keys)
	}
}

func TestStoreScanSortIsStableForTestNames(t *testing.T) {
	// The synthetic names above rely on ASCII ordering only for readability.
	names := []string{"stable:a0", "stable:b0"}
	sort.Strings(names)
	if names[0] != "stable:a0" {
		t.Fatal("unexpected sort order")
	}
}

func TestStoreScanHugeCountCompletes(t *testing.T) {
	s := New()
	for i := range 2000 {
		s.Set(fmt.Sprintf("key:%d", i), []byte("v"))
	}
	cursor, keys := s.Scan(0, int(^uint(0)>>1), nil, "", false)
	if cursor != 0 || len(keys) != 2000 {
		t.Fatalf("huge COUNT = cursor %d, %d keys; want one full cycle", cursor, len(keys))
	}
}
