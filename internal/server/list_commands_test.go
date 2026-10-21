package server

import (
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestListPushRangeAndIndex(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"LPUSH", "l", "a", "b", "c"}, ":3\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*3\r\n$1\r\nc\r\n$1\r\nb\r\n$1\r\na\r\n"},
		{[]string{"LLEN", "l"}, ":3\r\n"},
		{[]string{"TYPE", "l"}, "+list\r\n"},
		{[]string{"LINDEX", "l", "0"}, "$1\r\nc\r\n"},
		{[]string{"LINDEX", "l", "-1"}, "$1\r\na\r\n"},
		{[]string{"LINDEX", "l", "-4"}, "$-1\r\n"},
		{[]string{"RPUSH", "l", "d", "e"}, ":5\r\n"},
		{[]string{"LRANGE", "l", "2", "3"}, "*2\r\n$1\r\na\r\n$1\r\nd\r\n"},
		{[]string{"LRANGE", "l", "-2", "-1"}, "*2\r\n$1\r\nd\r\n$1\r\ne\r\n"},
		{[]string{"LRANGE", "l", "9", "10"}, "*0\r\n"},
		{[]string{"LRANGE", "missing", "0", "-1"}, "*0\r\n"},
		{[]string{"LLEN", "missing"}, ":0\r\n"},
		{[]string{"LINDEX", "missing", "0"}, "$-1\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestListPopCountsAndAutoDelete(t *testing.T) {
	exec := newStringExec(t)
	if got := runStrings(exec, "RPUSH", "l", "a", "b", "c", "d"); got != ":4\r\n" {
		t.Fatalf("RPUSH = %q", got)
	}
	if got := runStrings(exec, "LPOP", "l"); got != "$1\r\na\r\n" {
		t.Fatalf("LPOP = %q", got)
	}
	if got := runStrings(exec, "RPOP", "l", "2"); got != "*2\r\n$1\r\nd\r\n$1\r\nc\r\n" {
		t.Fatalf("RPOP count = %q", got)
	}
	if got := runStrings(exec, "LPOP", "l", "0"); got != "*0\r\n" {
		t.Fatalf("LPOP 0 = %q", got)
	}
	if got := runStrings(exec, "LPOP", "l", "9"); got != "*1\r\n$1\r\nb\r\n" {
		t.Fatalf("LPOP all = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "l"); got != ":0\r\n" {
		t.Fatalf("EXISTS after pop = %q", got)
	}
	if got := runStrings(exec, "LPOP", "missing"); got != "$-1\r\n" {
		t.Fatalf("LPOP missing = %q", got)
	}
	if got := runStrings(exec, "LPOP", "missing", "2"); got != "*-1\r\n" {
		t.Fatalf("LPOP missing count = %q", got)
	}
	if got := runStrings(exec, "RPOP", "missing", "0"); got != "*-1\r\n" {
		t.Fatalf("RPOP missing zero = %q", got)
	}
	for _, args := range [][]string{
		{"LPOP", "l", "-1"},
		{"LPOP", "l", "abc"},
		{"RPOP", "l", "+1"},
		{"RPOP", "l", "01"},
		{"LPOP", "l", "1", "2"},
	} {
		got := runStrings(exec, args...)
		want := "-ERR value is out of range, must be positive\r\n"
		if len(args) == 4 {
			want = "-ERR wrong number of arguments for 'lpop' command\r\n"
		}
		if got != want {
			t.Fatalf("%v = %q, want %q", args, got, want)
		}
	}
}

func TestListSetAndTrim(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "RPUSH", "l", "a", "b", "c", "d", "e")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"LSET", "l", "0", "A"}, "+OK\r\n"},
		{[]string{"LSET", "l", "-1", "E"}, "+OK\r\n"},
		{[]string{"LINDEX", "l", "0"}, "$1\r\nA\r\n"},
		{[]string{"LINDEX", "l", "-1"}, "$1\r\nE\r\n"},
		{[]string{"LSET", "l", "5", "X"}, "-ERR index out of range\r\n"},
		{[]string{"LSET", "l", "-6", "X"}, "-ERR index out of range\r\n"},
		{[]string{"LSET", "missing", "0", "X"}, "-ERR no such key\r\n"},
		{[]string{"LSET", "l", "abc", "X"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"LTRIM", "l", "1", "-2"}, "+OK\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*3\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\nd\r\n"},
		{[]string{"LTRIM", "l", "-2", "-1"}, "+OK\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*2\r\n$1\r\nc\r\n$1\r\nd\r\n"},
		{[]string{"LTRIM", "l", "5", "10"}, "+OK\r\n"},
		{[]string{"EXISTS", "l"}, ":0\r\n"},
		{[]string{"LTRIM", "missing", "0", "1"}, "+OK\r\n"},
		{[]string{"LTRIM", "l", "abc", "1"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"LRANGE", "l", "abc", "1"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"LTRIM", "l", "0", "1"}, "+OK\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestListSetHugeIndexDivergesFromRedis(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "RPUSH", "l", "a", "b", "c")
	for _, index := range []string{"4294967295", "4294967296", "9223372036854775807"} {
		if got := runStrings(exec, "LSET", "l", index, "z"); got != "-ERR index out of range\r\n" {
			t.Fatalf("LSET index %s = %q", index, got)
		}
	}
}

func TestListRem(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "RPUSH", "l", "a", "b", "a", "c", "a", "b", "a")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"LREM", "l", "2", "a"}, ":2\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*5\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\na\r\n"},
		{[]string{"LREM", "l", "-2", "a"}, ":2\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*3\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\nb\r\n"},
		{[]string{"LREM", "l", "0", "b"}, ":2\r\n"},
		{[]string{"LRANGE", "l", "0", "-1"}, "*1\r\n$1\r\nc\r\n"},
		{[]string{"LREM", "l", "0", "c"}, ":1\r\n"},
		{[]string{"EXISTS", "l"}, ":0\r\n"},
		{[]string{"LREM", "missing", "0", "a"}, ":0\r\n"},
		{[]string{"LREM", "l", "abc", "a"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"LREM", "l", "-9223372036854775808", "a"}, "-ERR value is out of range, value must between -9223372036854775807 and 9223372036854775807\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestListWrongTypeMatrix(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "s", "v")
	const wrong = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
	for _, args := range [][]string{
		{"LPUSH", "s", "x"},
		{"RPUSH", "s", "x"},
		{"LPOP", "s"},
		{"RPOP", "s", "2"},
		{"LRANGE", "s", "0", "-1"},
		{"LLEN", "s"},
		{"LINDEX", "s", "abc"},
		{"LSET", "s", "abc", "x"},
		{"LREM", "s", "0", "x"},
		{"LTRIM", "s", "0", "1"},
	} {
		if got := runStrings(exec, args...); got != wrong {
			t.Fatalf("%v = %q, want wrongtype", args, got)
		}
	}
	runStrings(exec, "RPUSH", "l", "x")
	for _, args := range [][]string{
		{"GET", "l"},
		{"APPEND", "l", "y"},
		{"STRLEN", "l"},
		{"INCR", "l"},
		{"GETSET", "l", "y"},
		{"SET", "l", "y", "GET"},
		{"SET", "l", "y", "NX", "GET"},
	} {
		if got := runStrings(exec, args...); got != wrong {
			t.Fatalf("%v = %q, want wrongtype", args, got)
		}
	}
	if got := runStrings(exec, "MGET", "l"); got != "*1\r\n$-1\r\n" {
		t.Fatalf("MGET list = %q", got)
	}
	if got := runStrings(exec, "SETNX", "l", "x"); got != ":0\r\n" {
		t.Fatalf("SETNX list = %q", got)
	}
	if got := runStrings(exec, "SET", "l", "x"); got != "+OK\r\n" {
		t.Fatalf("SET overwrite list = %q", got)
	}
	if got := runStrings(exec, "TYPE", "l"); got != "+string\r\n" {
		t.Fatalf("TYPE after SET = %q", got)
	}
	if got := runStrings(exec, "LPUSH", "l", "x"); got != wrong {
		t.Fatalf("LPUSH string = %q", got)
	}
}

func TestListPreservesTTL(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	runStrings(exec, "RPUSH", "l", "a")
	runStrings(exec, "PEXPIRE", "l", "1000")
	runStrings(exec, "LPUSH", "l", "b")
	runStrings(exec, "LTRIM", "l", "0", "0")
	if got := runStrings(exec, "PTTL", "l"); got != ":1000\r\n" {
		t.Fatalf("PTTL after mutators = %q, want 1000", got)
	}
	clk.Advance(1001 * time.Millisecond)
	if got := runStrings(exec, "LLEN", "l"); got != ":0\r\n" {
		t.Fatalf("LLEN after expiry = %q", got)
	}
	if got := runStrings(exec, "TYPE", "l"); got != "+none\r\n" {
		t.Fatalf("TYPE after expiry = %q", got)
	}
}
