package server

import (
	"fmt"
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

func TestCollectionShrinkReleasesStorage(t *testing.T) {
	st := store.New()
	exec := NewExecutorWithClock(st, nil, clock.Real{})
	t.Cleanup(exec.Close)

	args := []string{"RPUSH", "list"}
	for i := 0; i < 1000; i++ {
		args = append(args, "v")
	}
	runStrings(exec, args...)
	runStrings(exec, "LTRIM", "list", "0", "9")
	v, _ := st.Lookup("list")
	if got := len(v.List) - v.ListHead; got != 10 {
		t.Fatalf("list length after LTRIM = %d, want 10", got)
	}
	if cap(v.List) > 2*len(v.List) {
		t.Fatalf("LTRIM retained cap %d for len %d", cap(v.List), len(v.List))
	}

	for i := 0; i < 100; i++ {
		runStrings(exec, "LPUSH", "left", "x")
	}
	v, _ = st.Lookup("left")
	if got := len(v.List) - v.ListHead; got != 100 {
		t.Fatalf("list length after LPUSH = %d, want 100", got)
	}
	if cap(v.List) > 2*len(v.List) {
		t.Fatalf("LPUSH used cap %d for len %d", cap(v.List), len(v.List))
	}

	hashArgs := []string{"HSET", "hash"}
	for i := 0; i < 100; i++ {
		hashArgs = append(hashArgs, string(rune('a'+i%26))+string(rune('a'+i/26)), "v")
	}
	runStrings(exec, hashArgs...)
	delArgs := []string{"HDEL", "hash"}
	for i := 0; i < 98; i++ {
		delArgs = append(delArgs, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	runStrings(exec, delArgs...)
	v, _ = st.Lookup("hash")
	if len(v.Hash) != 2 || cap(v.Hash) > 2*len(v.Hash) {
		t.Fatalf("HDEL retained %d/%d hash entries", cap(v.Hash), len(v.Hash))
	}

	setArgs := []string{"SADD", "set"}
	for i := 0; i < 100; i++ {
		setArgs = append(setArgs, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	runStrings(exec, setArgs...)
	setDelArgs := []string{"SREM", "set"}
	for i := 0; i < 98; i++ {
		setDelArgs = append(setDelArgs, string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	runStrings(exec, setDelArgs...)
	v, _ = st.Lookup("set")
	if len(v.Set) != 2 || cap(v.Set) > 2*len(v.Set) {
		t.Fatalf("SREM retained %d/%d set members", cap(v.Set), len(v.Set))
	}
}

func TestLargeHashAndSetDeletesKeepIndexes(t *testing.T) {
	st := store.New()
	exec := NewExecutorWithClock(st, nil, clock.Real{})
	t.Cleanup(exec.Close)

	hashArgs := []string{"HSET", "h"}
	for i := 0; i < 600; i++ {
		hashArgs = append(hashArgs, fmt.Sprintf("f%d", i), "v")
	}
	runStrings(exec, hashArgs...)
	runStrings(exec, "HDEL", "h", "f0", "f300", "f599")
	if got := runStrings(exec, "HLEN", "h"); got != ":597\r\n" {
		t.Fatalf("HLEN after large HDEL = %q", got)
	}
	for _, field := range []string{"f1", "f299", "f301", "f598"} {
		if got := runStrings(exec, "HGET", "h", field); got != "$1\r\nv\r\n" {
			t.Fatalf("HGET %s after large HDEL = %q", field, got)
		}
	}
	for i := 1; i < 600; i++ {
		runStrings(exec, "HDEL", "h", fmt.Sprintf("f%d", i))
	}
	if got := runStrings(exec, "EXISTS", "h"); got != ":0\r\n" {
		t.Fatalf("hash survived deleting every field: EXISTS = %q", got)
	}

	setArgs := []string{"SADD", "s"}
	for i := 0; i < 600; i++ {
		setArgs = append(setArgs, fmt.Sprintf("m%d", i))
	}
	runStrings(exec, setArgs...)
	runStrings(exec, "SREM", "s", "m0", "m300", "m599", "m0")
	if got := runStrings(exec, "SCARD", "s"); got != ":597\r\n" {
		t.Fatalf("SCARD after large SREM = %q", got)
	}
	for _, member := range []string{"m1", "m299", "m301", "m598"} {
		if got := runStrings(exec, "SISMEMBER", "s", member); got != ":1\r\n" {
			t.Fatalf("SISMEMBER %s after large SREM = %q", member, got)
		}
	}
	for i := 1; i < 600; i++ {
		runStrings(exec, "SREM", "s", fmt.Sprintf("m%d", i))
	}
	if got := runStrings(exec, "EXISTS", "s"); got != ":0\r\n" {
		t.Fatalf("set survived deleting every member: EXISTS = %q", got)
	}
}
