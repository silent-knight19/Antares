package store

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestMemoryUsageEmptyAndPerKey(t *testing.T) {
	s := New()
	if got := s.MemoryUsage(); got.Total != 0 || got.Dataset != 0 {
		t.Fatalf("empty MemoryUsage = %+v, want zero", got)
	}

	s.Set("key", []byte("value"))
	u := s.MemoryUsage()
	if want := int64(entryOverhead + len("key")); u.Dataset != int64(len("value")) || u.Total != want+int64(len("value")) {
		t.Fatalf("string MemoryUsage = %+v, want total %d dataset %d", u, want+int64(len("value")), len("value"))
	}

	s.Delete("key")
	if got := s.MemoryUsage(); got.Total != 0 || got.Dataset != 0 {
		t.Fatalf("MemoryUsage after Delete = %+v, want zero", got)
	}
}

// TestMemoryUsageTracksRuntime is the P7.1 accuracy gate: the logical model
// must land within 10% of the bytes the Go heap actually retains for a
// payload-sized dataset, across all four value types.
func TestMemoryUsageTracksRuntime(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	s := New()
	for i := 0; i < 400; i++ {
		s.Set(fmt.Sprintf("str:%04d", i), bytes.Repeat([]byte("s"), 4096))
	}
	for i := 0; i < 40; i++ {
		list := make([][]byte, 100)
		for j := range list {
			list[j] = bytes.Repeat([]byte("l"), 128)
		}
		s.SetPreservingTTL(fmt.Sprintf("list:%02d", i), Value{Kind: KindList, List: list})
	}
	for i := 0; i < 40; i++ {
		hash := make([]HashEntry, 100)
		for j := range hash {
			hash[j] = HashEntry{Field: []byte(fmt.Sprintf("field:%03d", j)), Value: bytes.Repeat([]byte("h"), 58)}
		}
		s.SetPreservingTTL(fmt.Sprintf("hash:%02d", i), Value{Kind: KindHash, Hash: hash})
	}
	for i := 0; i < 40; i++ {
		set := make([][]byte, 100)
		for j := range set {
			set[j] = []byte(fmt.Sprintf("member:%03d%s", j, bytes.Repeat([]byte("e"), 52)))
		}
		s.SetPreservingTTL(fmt.Sprintf("set:%02d", i), Value{Kind: KindSet, Set: set})
	}

	runtime.GC()
	runtime.ReadMemStats(&after)
	actual := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if actual <= 0 {
		t.Fatalf("measured heap growth = %d, want > 0", actual)
	}
	got := s.MemoryUsage().Total
	diff := float64(got-actual) / float64(actual)
	if diff > 0.10 || diff < -0.10 {
		t.Fatalf("MemoryUsage = %d, runtime heap = %d (%.1f%%, want within 10%%)", got, actual, diff*100)
	}
	runtime.KeepAlive(s)
}

func TestMemoryUsageTracksMutations(t *testing.T) {
	fake := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(fake)
	check := func() {
		t.Helper()
		want := Usage{}
		for key, value := range s.data {
			u := usageOf(key, value)
			want.Total += u.Total
			want.Dataset += u.Dataset
		}
		if got := s.MemoryUsage(); got != want {
			t.Fatalf("MemoryUsage = %+v, want %+v", got, want)
		}
	}

	s.Set("a", []byte("1"))
	check()
	s.SetWithTTL("b", []byte("22"), 1100)
	check()
	s.Set("a", []byte("333"))
	check()
	s.SetPreservingTTL("list", Value{Kind: KindList, List: [][]byte{[]byte("x")}})
	check()
	v, _ := s.Lookup("list")
	v.List = append(v.List, []byte("yy"))
	s.SetPreservingTTL("list", v)
	check()
	s.Rename("a", "b")
	check()
	s.Delete("list")
	check()

	fake.Advance(200 * time.Millisecond)
	s.Lookup("b")
	check()
	s.SetWithTTL("c", []byte("3333"), 1100)
	check()
	s.ExpireSample(fake.Now().UnixMilli(), 10)
	check()

	if cp := s.Copy(); cp.MemoryUsage() != s.MemoryUsage() {
		t.Fatalf("copy MemoryUsage = %+v, want %+v", cp.MemoryUsage(), s.MemoryUsage())
	}
	s.Flush()
	check()
}
