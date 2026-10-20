package server

import (
	"testing"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/store"
)

func TestListEditsReleaseRemovedSlots(t *testing.T) {
	st := store.New()
	exec := NewExecutorWithClock(st, nil, clock.Real{})
	t.Cleanup(exec.Close)

	runStrings(exec, "RPUSH", "l", "a", "b", "c", "d")
	before, _ := st.Lookup("l")
	runStrings(exec, "LPOP", "l")
	if before.List[0] != nil {
		t.Fatal("LPOP retained the removed head")
	}

	before, _ = st.Lookup("l")
	runStrings(exec, "RPOP", "l")
	if before.List[len(before.List)-1] != nil {
		t.Fatal("RPOP retained the removed tail")
	}

	runStrings(exec, "RPUSH", "m", "a", "b", "c", "d", "e")
	before, _ = st.Lookup("m")
	runStrings(exec, "LTRIM", "m", "1", "-2")
	if before.List[0] != nil || before.List[len(before.List)-1] != nil {
		t.Fatal("LTRIM retained a removed edge")
	}

	runStrings(exec, "RPUSH", "n", "a", "b", "a", "c")
	before, _ = st.Lookup("n")
	runStrings(exec, "LREM", "n", "1", "a")
	if before.List[len(before.List)-1] != nil {
		t.Fatal("LREM retained the compacted tail")
	}
}

func TestCollectionIndexesStayValidAfterRemoval(t *testing.T) {
	st := store.New()
	exec := NewExecutorWithClock(st, nil, clock.Real{})
	t.Cleanup(exec.Close)

	runStrings(exec, "HSET", "h", "a", "1", "b", "2")
	runStrings(exec, "HDEL", "h", "a")
	if got := runStrings(exec, "HGET", "h", "b"); got != "$1\r\n2\r\n" {
		t.Fatalf("HGET after HDEL = %q", got)
	}
	runStrings(exec, "HSET", "h", "a", "3")
	if got := runStrings(exec, "HGET", "h", "a"); got != "$1\r\n3\r\n" {
		t.Fatalf("HGET after re-add = %q", got)
	}

	runStrings(exec, "SADD", "s", "a", "b")
	runStrings(exec, "SREM", "s", "a")
	if got := runStrings(exec, "SISMEMBER", "s", "b"); got != ":1\r\n" {
		t.Fatalf("SISMEMBER after SREM = %q", got)
	}
	runStrings(exec, "SADD", "s", "a")
	if got := runStrings(exec, "SISMEMBER", "s", "a"); got != ":1\r\n" {
		t.Fatalf("SISMEMBER after re-add = %q", got)
	}

	runStrings(exec, "SADD", "t", "a", "b", "c")
	runStrings(exec, "SPOP", "t", "1")
	runStrings(exec, "SADD", "t", "d")
	if got := runStrings(exec, "SISMEMBER", "t", "d"); got != ":1\r\n" {
		t.Fatalf("SISMEMBER after SPOP = %q", got)
	}
}

func TestHashAndSetEditsReleaseRemovedSlots(t *testing.T) {
	st := store.New()
	exec := NewExecutorWithClock(st, nil, clock.Real{})
	t.Cleanup(exec.Close)

	runStrings(exec, "HSET", "h", "f1", "v1", "f2", "v2", "f3", "v3")
	before, _ := st.Lookup("h")
	runStrings(exec, "HDEL", "h", "f1")
	if before.Hash[len(before.Hash)-1].Field != nil {
		t.Fatal("HDEL retained the compacted field")
	}

	runStrings(exec, "SADD", "s", "a", "b", "c")
	before, _ = st.Lookup("s")
	runStrings(exec, "SREM", "s", "a")
	if before.Set[len(before.Set)-1] != nil {
		t.Fatal("SREM retained the compacted member")
	}

	runStrings(exec, "SADD", "p", "a", "b", "c")
	before, _ = st.Lookup("p")
	runStrings(exec, "SPOP", "p")
	if before.Set[len(before.Set)-1] != nil {
		t.Fatal("SPOP retained the swapped-out member")
	}

	runStrings(exec, "SADD", "q", "a", "b", "c", "d")
	before, _ = st.Lookup("q")
	runStrings(exec, "SPOP", "q", "2")
	if before.Set[2] != nil || before.Set[3] != nil {
		t.Fatal("SPOP count retained a removed member")
	}
}
