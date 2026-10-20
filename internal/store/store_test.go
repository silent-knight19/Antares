package store

import (
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestStoreBasics(t *testing.T) {
	s := New()

	if _, ok := s.Get("missing"); ok {
		t.Fatal("Get(missing) found a value")
	}
	if s.Exists("missing") {
		t.Fatal("Exists(missing) = true")
	}
	if s.Delete("missing") {
		t.Fatal("Delete(missing) = true")
	}

	s.Set("key", []byte("value"))
	if got, ok := s.Get("key"); !ok || string(got) != "value" {
		t.Fatalf("Get(key) = %q, %v; want value, true", got, ok)
	}
	if !s.Exists("key") {
		t.Fatal("Exists(key) = false")
	}
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", s.Len())
	}

	s.Set("key", []byte("new"))
	if got, _ := s.Get("key"); string(got) != "new" {
		t.Fatalf("Get(key) after overwrite = %q, want new", got)
	}
	if !s.Delete("key") {
		t.Fatal("Delete(key) = false")
	}
	if s.Exists("key") || s.Len() != 0 {
		t.Fatal("key still present after Delete")
	}
}

func TestStoreExpiryBoundary(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)
	s.SetWithTTL("key", []byte("value"), 2000)

	f.Advance(time.Second)
	if _, ok := s.Get("key"); !ok {
		t.Fatal("key expired at its exact deadline")
	}
	f.Advance(time.Millisecond)
	if _, ok := s.Get("key"); ok {
		t.Fatal("key survived past its deadline")
	}
}

func TestStoreLazyDeleteOnAccess(t *testing.T) {
	accessors := []struct {
		name string
		call func(*Store)
	}{
		{"Get", func(s *Store) { s.Get("dead") }},
		{"Exists", func(s *Store) { s.Exists("dead") }},
		{"Delete", func(s *Store) { s.Delete("dead") }},
		{"ExpiresAt", func(s *Store) { s.ExpiresAt("dead") }},
		{"TTLms", func(s *Store) { s.TTLms("dead") }},
		{"Persist", func(s *Store) { s.Persist("dead") }},
	}
	for _, tc := range accessors {
		t.Run(tc.name, func(t *testing.T) {
			s := NewWithClock(clock.NewFake(time.UnixMilli(1000)))
			s.SetWithTTL("dead", []byte("value"), 999)
			tc.call(s)
			if len(s.data) != 0 || len(s.expires) != 0 {
				t.Fatalf("after %s: data=%d expires=%d, want both 0", tc.name, len(s.data), len(s.expires))
			}
		})
	}
}

func TestStoreSetKeepTTLOnExpiredKeyStartsFresh(t *testing.T) {
	s := NewWithClock(clock.NewFake(time.UnixMilli(1000)))
	s.SetWithTTL("dead", []byte("old"), 999)
	s.SetKeepTTL("dead", []byte("new"))

	if got, ok := s.Get("dead"); !ok || string(got) != "new" {
		t.Fatalf("Get(dead) = %q, %v; want new, true", got, ok)
	}
	if _, ok := s.expires["dead"]; ok {
		t.Fatal("SetKeepTTL preserved an expired deadline")
	}
}

func TestStoreLenCountsPhysicalKeys(t *testing.T) {
	s := NewWithClock(clock.NewFake(time.UnixMilli(1000)))
	s.Set("live", []byte("value"))
	s.SetWithTTL("dead", []byte("value"), 999)

	if got := s.Len(); got != 2 {
		t.Fatalf("Len() before access = %d, want 2 physical keys", got)
	}
	s.Get("dead")
	if got := s.Len(); got != 1 {
		t.Fatalf("Len() after lazy delete = %d, want 1", got)
	}
}

func TestStoreExpireSample(t *testing.T) {
	s := New()
	for i := 0; i < 25; i++ {
		s.SetWithTTL(string(rune('a'+i)), []byte("value"), 999)
	}
	s.Set("future", []byte("value"))

	expired, sampled := s.ExpireSample(1000, 20)
	if expired != 20 || sampled != 20 {
		t.Fatalf("ExpireSample = %d expired, %d sampled; want 20, 20", expired, sampled)
	}
	if got := s.Len(); got != 6 {
		t.Fatalf("Len after first sample = %d, want 6", got)
	}

	expired, sampled = s.ExpireSample(1000, 20)
	if expired != 5 || sampled != 5 {
		t.Fatalf("ExpireSample = %d expired, %d sampled; want 5, 5", expired, sampled)
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("Len after second sample = %d, want 1", got)
	}
	if _, ok := s.Get("future"); !ok {
		t.Fatal("active sample deleted a live key")
	}
}

func TestStoreExpiryAndKeepTTL(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	s := NewWithClock(f)
	expiresAt := f.Now().Add(time.Minute).UnixMilli()
	s.SetWithTTL("key", []byte("value"), expiresAt)

	if got, ok := s.Get("key"); !ok || string(got) != "value" {
		t.Fatalf("Get(key) = %q, %v; want value, true", got, ok)
	}
	s.SetKeepTTL("key", []byte("kept"))
	if got := s.expires["key"]; got != expiresAt {
		t.Fatalf("SetKeepTTL expiry = %d, want %d", got, expiresAt)
	}
	s.Set("key", []byte("plain"))
	if got := s.expires["key"]; got != 0 {
		t.Fatalf("Set expiry = %d, want 0 (cleared)", got)
	}

	s.SetWithTTL("dead", []byte("value"), f.Now().UnixMilli()-1)
	if _, ok := s.Get("dead"); ok {
		t.Fatal("Get(dead) found an expired value")
	}
	if s.Exists("dead") || s.Len() != 1 {
		t.Fatalf("expired key still counted: Exists=%v Len=%d", s.Exists("dead"), s.Len())
	}
}

func TestStoreListValueAndType(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	s := NewWithClock(f)
	s.SetList("l", [][]byte{[]byte("a"), []byte("b")})
	if got := s.Type("l"); got != "list" {
		t.Fatalf("Type(list) = %q", got)
	}
	if _, ok := s.Get("l"); ok {
		t.Fatal("Get(list) returned a string")
	}
	v, ok := s.Lookup("l")
	if !ok || v.Kind != KindList || len(v.List) != 2 {
		t.Fatalf("Lookup(list) = %#v, %v", v, ok)
	}
	s.SetExpire("l", f.Now().UnixMilli()+1000)
	if _, ok := s.expires["l"]; !ok {
		t.Fatal("SetExpire did not set the list expiry")
	}
	s.Set("l", []byte("value"))
	if got := s.Type("l"); got != "string" {
		t.Fatalf("Type after string overwrite = %q", got)
	}
	if _, ok := s.expires["l"]; ok {
		t.Fatal("string overwrite did not clear list expiry")
	}
}

func TestStoreSetPreservingTTL(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := NewWithClock(f)
	s.SetWithTTL("live", []byte("old"), 2000)
	s.SetPreservingTTL("live", Value{Kind: KindString, Str: []byte("new")})
	if got, ok := s.Get("live"); !ok || string(got) != "new" {
		t.Fatalf("Get(live) = %q, %v; want new, true", got, ok)
	}
	if got := s.expires["live"]; got != 2000 {
		t.Fatalf("SetPreservingTTL expiry = %d, want 2000", got)
	}

	s.SetWithTTL("dead", []byte("old"), 999)
	s.SetPreservingTTL("dead", Value{Kind: KindString, Str: []byte("new")})
	if got := s.expires["dead"]; got != 999 {
		t.Fatalf("SetPreservingTTL expired deadline = %d, want 999", got)
	}
	if _, ok := s.Get("dead"); ok {
		t.Fatal("SetPreservingTTL resurrected an expired key")
	}

	s.SetPreservingTTL("fresh", Value{Kind: KindString, Str: []byte("value")})
	if _, ok := s.expires["fresh"]; ok {
		t.Fatal("SetPreservingTTL added an expiry to a fresh key")
	}
}

func TestStoreHashValueAndType(t *testing.T) {
	s := New()
	s.SetHash("h", []HashEntry{{Field: []byte("f"), Value: []byte("v")}})
	if got := s.Type("h"); got != "hash" {
		t.Fatalf("Type(hash) = %q", got)
	}
	if _, ok := s.Get("h"); ok {
		t.Fatal("Get(hash) returned a string")
	}
	v, ok := s.Lookup("h")
	if !ok || v.Kind != KindHash || len(v.Hash) != 1 {
		t.Fatalf("Lookup(hash) = %#v, %v", v, ok)
	}
	s.Set("h", []byte("value"))
	if got := s.Type("h"); got != "string" {
		t.Fatalf("Type after overwrite = %q", got)
	}
}

func TestStoreSetValueAndType(t *testing.T) {
	s := New()
	s.SetMembers("s", [][]byte{[]byte("a"), []byte("b")})
	if got := s.Type("s"); got != "set" {
		t.Fatalf("Type(set) = %q", got)
	}
	if _, ok := s.Get("s"); ok {
		t.Fatal("Get(set) returned a string")
	}
	v, ok := s.Lookup("s")
	if !ok || v.Kind != KindSet || len(v.Set) != 2 {
		t.Fatalf("Lookup(set) = %#v, %v", v, ok)
	}
	s.Set("s", []byte("value"))
	if got := s.Type("s"); got != "string" {
		t.Fatalf("Type after overwrite = %q", got)
	}
}
