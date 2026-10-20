package store

import (
	"testing"
	"time"
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

func TestStoreExpiryAndKeepTTL(t *testing.T) {
	s := New()
	expiresAt := time.Now().Add(time.Minute).UnixMilli()
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

	s.SetWithTTL("dead", []byte("value"), time.Now().Add(-time.Millisecond).UnixMilli())
	if _, ok := s.Get("dead"); ok {
		t.Fatal("Get(dead) found an expired value")
	}
	if s.Exists("dead") || s.Len() != 1 {
		t.Fatalf("expired key still counted: Exists=%v Len=%d", s.Exists("dead"), s.Len())
	}
}
