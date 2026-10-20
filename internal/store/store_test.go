package store

import "testing"

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
