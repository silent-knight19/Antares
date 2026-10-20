package expire

import (
	"fmt"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/store"
)

func TestCycleDeletesExpiredAndRepeats(t *testing.T) {
	st := store.New()
	for i := 0; i < 45; i++ {
		st.SetWithTTL(fmt.Sprintf("dead:%d", i), []byte("value"), 999)
	}
	cycle := New(st, clock.Real{})

	expired, sampled := cycle.Run(1000, time.Hour)
	if expired != 45 || sampled != 45 {
		t.Fatalf("Run = %d expired, %d sampled; want 45, 45", expired, sampled)
	}
	if got := st.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
}

func TestCycleStopsAtQuarterExpired(t *testing.T) {
	st := store.New()
	st.SetWithTTL("dead", []byte("value"), 999)
	for i := 0; i < 3; i++ {
		st.SetWithTTL(fmt.Sprintf("live:%d", i), []byte("value"), 1000)
	}
	cycle := New(st, clock.Real{})

	expired, sampled := cycle.Run(1000, time.Hour)
	if expired != 1 || sampled != 4 {
		t.Fatalf("Run = %d expired, %d sampled; want 1, 4", expired, sampled)
	}
	if got := st.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3 live keys", got)
	}
}

func TestCycleHonorsTimeBudget(t *testing.T) {
	st := store.New()
	for i := 0; i < 1000; i++ {
		st.SetWithTTL(fmt.Sprintf("dead:%d", i), []byte("value"), 999)
	}
	base := time.Unix(0, 0)
	step := &stepClock{base: base}
	cycle := New(st, step)

	expired, sampled := cycle.Run(1000, 5*time.Millisecond)
	if expired == 0 || expired >= 1000 || expired != sampled {
		t.Fatalf("Run = %d expired, %d sampled; want a budget-limited partial run", expired, sampled)
	}
	if got := st.Len(); got != 1000-expired {
		t.Fatalf("Len = %d, want %d", got, 1000-expired)
	}
}

func TestCycleIgnoresLiveKeys(t *testing.T) {
	st := store.New()
	st.Set("persistent", []byte("value"))
	st.SetWithTTL("future", []byte("value"), 1001)
	cycle := New(st, clock.Real{})

	expired, sampled := cycle.Run(1000, time.Hour)
	if expired != 0 || sampled != 1 {
		t.Fatalf("Run = %d expired, %d sampled; want 0, 1", expired, sampled)
	}
	if got := st.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2", got)
	}
}

type stepClock struct {
	base time.Time
	n    int
}

func (c *stepClock) Now() time.Time {
	c.n++
	return c.base.Add(time.Duration(c.n) * time.Millisecond)
}

func (c *stepClock) Since(t time.Time) time.Duration { return c.Now().Sub(t) }
