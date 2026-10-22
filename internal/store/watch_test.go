package store

import (
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestSnapshotMatchesStringsAndTime(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)

	if snap := s.CaptureSnapshot("k"); !s.MatchesSnapshot("k", snap) {
		t.Fatal("absent key stopped matching while still absent")
	}
	snap := s.CaptureSnapshot("k")
	s.Set("k", []byte("v"))
	if s.MatchesSnapshot("k", snap) {
		t.Fatal("creating a watched key did not invalidate")
	}

	snap = s.CaptureSnapshot("k")
	s.Set("k", []byte("v"))
	if !s.MatchesSnapshot("k", snap) {
		t.Fatal("write restoring identical bytes should match (ADR-008)")
	}
	s.Set("k", []byte("other"))
	if s.MatchesSnapshot("k", snap) {
		t.Fatal("changed value did not invalidate")
	}

	snap = s.CaptureSnapshot("k")
	s.Delete("k")
	if s.MatchesSnapshot("k", snap) {
		t.Fatal("deleted watched key did not invalidate")
	}
	snap = s.CaptureSnapshot("gone")
	s.Set("gone", []byte("x"))
	s.Delete("gone")
	if !s.MatchesSnapshot("gone", snap) {
		t.Fatal("write-then-restore of an absent key should match (ADR-008)")
	}

	s.SetWithTTL("ttl", []byte("v"), 5000)
	snap = s.CaptureSnapshot("ttl")
	s.SetExpire("ttl", 6000)
	if s.MatchesSnapshot("ttl", snap) {
		t.Fatal("deadline change did not invalidate")
	}
	snap = s.CaptureSnapshot("ttl")
	s.SetExpire("ttl", 6000)
	if !s.MatchesSnapshot("ttl", snap) {
		t.Fatal("same deadline should match")
	}
	snap = s.CaptureSnapshot("ttl")
	s.Persist("ttl")
	if s.MatchesSnapshot("ttl", snap) {
		t.Fatal("clearing the deadline did not invalidate")
	}

	s.SetWithTTL("exp", []byte("v"), 1500)
	snap = s.CaptureSnapshot("exp")
	f.Advance(2 * time.Second)
	if s.MatchesSnapshot("exp", snap) {
		t.Fatal("expiry did not invalidate")
	}
}

func TestSnapshotMatchesCollections(t *testing.T) {
	s := New()
	s.SetPreservingTTL("list", Value{Kind: KindList, List: [][]byte{[]byte("a"), []byte("b")}})
	s.SetPreservingTTL("hash", Value{Kind: KindHash, Hash: []HashEntry{{Field: []byte("f"), Value: []byte("v")}}})
	s.SetPreservingTTL("set", Value{Kind: KindSet, Set: [][]byte{[]byte("m")}})

	for _, key := range []string{"list", "hash", "set"} {
		snap := s.CaptureSnapshot(key)
		v, _ := s.Lookup(key)
		s.SetPreservingTTL(key, v)
		if !s.MatchesSnapshot(key, snap) {
			t.Fatalf("%s: unchanged collection did not match", key)
		}
	}

	snap := s.CaptureSnapshot("hash")
	s.SetPreservingTTL("hash", Value{Kind: KindHash, Hash: []HashEntry{
		{Field: []byte("f"), Value: []byte("changed")},
	}})
	if s.MatchesSnapshot("hash", snap) {
		t.Fatal("hash value change did not invalidate")
	}

	snap = s.CaptureSnapshot("set")
	s.SetPreservingTTL("set", Value{Kind: KindSet, Set: [][]byte{[]byte("m"), []byte("n")}})
	if s.MatchesSnapshot("set", snap) {
		t.Fatal("set member addition did not invalidate")
	}

	snap = s.CaptureSnapshot("list")
	v, _ := s.Lookup("list")
	v.List[0] = []byte("mutated")
	s.SetPreservingTTL("list", v)
	if s.MatchesSnapshot("list", snap) {
		t.Fatal("in-place list mutation did not invalidate")
	}
}
