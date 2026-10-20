package clock

import (
	"testing"
	"time"
)

func TestFakeAdvances(t *testing.T) {
	base := time.UnixMilli(1000)
	f := NewFake(base)
	if got := f.Now(); !got.Equal(base) {
		t.Fatalf("Now = %v, want %v", got, base)
	}
	f.Advance(250 * time.Millisecond)
	if got := f.Since(base); got != 250*time.Millisecond {
		t.Fatalf("Since = %v, want 250ms", got)
	}
}
