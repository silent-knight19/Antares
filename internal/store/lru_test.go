package store

import (
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestSampleKeysIdleAndVolatile(t *testing.T) {
	fake := clock.NewFake(time.Unix(1000, 0))
	st := NewWithClock(fake)

	st.Set("cold", []byte("v"))
	fake.Advance(10 * time.Second)
	st.Set("warm", []byte("v"))
	fake.Advance(5 * time.Second)
	st.SetWithTTL("vol", []byte("v"), fake.Now().Add(time.Hour).UnixMilli())

	idles := make(map[string]uint32)
	for _, s := range st.SampleKeys(false, 10) {
		idles[s.Key] = s.Idle
	}
	if idles["cold"] != 15 || idles["warm"] != 5 || idles["vol"] != 0 {
		t.Fatalf("idle seconds = %v, want cold=15 warm=5 vol=0", idles)
	}

	vol := st.SampleKeys(true, 10)
	if len(vol) != 1 || vol[0].Key != "vol" {
		t.Fatalf("volatile sample = %v, want only vol", vol)
	}

	if got := st.SampleKeys(false, 2); len(got) != 2 {
		t.Fatalf("sample limit = %d keys, want 2", len(got))
	}
}

func TestLookupTouchesLRU(t *testing.T) {
	fake := clock.NewFake(time.Unix(2000, 0))
	st := NewWithClock(fake)
	st.Set("k", []byte("v"))
	fake.Advance(time.Hour)
	st.Lookup("k")

	if got := st.SampleKeys(false, 1)[0].Idle; got != 0 {
		t.Fatalf("idle after lookup = %d, want 0", got)
	}
}
