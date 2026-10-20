package server

import (
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestHashBasics(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"HSET", "h", "f1", "v1", "f2", "v2"}, ":2\r\n"},
		{[]string{"TYPE", "h"}, "+hash\r\n"},
		{[]string{"HGET", "h", "f1"}, "$2\r\nv1\r\n"},
		{[]string{"HGET", "h", "missing"}, "$-1\r\n"},
		{[]string{"HMGET", "h", "f1", "missing", "f2"}, "*3\r\n$2\r\nv1\r\n$-1\r\n$2\r\nv2\r\n"},
		{[]string{"HLEN", "h"}, ":2\r\n"},
		{[]string{"HEXISTS", "h", "f1"}, ":1\r\n"},
		{[]string{"HEXISTS", "h", "missing"}, ":0\r\n"},
		{[]string{"HGETALL", "h"}, "*4\r\n$2\r\nf1\r\n$2\r\nv1\r\n$2\r\nf2\r\n$2\r\nv2\r\n"},
		{[]string{"HKEYS", "h"}, "*2\r\n$2\r\nf1\r\n$2\r\nf2\r\n"},
		{[]string{"HVALS", "h"}, "*2\r\n$2\r\nv1\r\n$2\r\nv2\r\n"},
		{[]string{"HSET", "h", "f1", "V1", "f3", "v3"}, ":1\r\n"},
		{[]string{"HGETALL", "h"}, "*6\r\n$2\r\nf1\r\n$2\r\nV1\r\n$2\r\nf2\r\n$2\r\nv2\r\n$2\r\nf3\r\n$2\r\nv3\r\n"},
		{[]string{"HDEL", "h", "f2", "missing", "f2"}, ":1\r\n"},
		{[]string{"HGETALL", "h"}, "*4\r\n$2\r\nf1\r\n$2\r\nV1\r\n$2\r\nf3\r\n$2\r\nv3\r\n"},
		{[]string{"HDEL", "h", "f1", "f3"}, ":2\r\n"},
		{[]string{"EXISTS", "h"}, ":0\r\n"},
		{[]string{"HGET", "missing", "f"}, "$-1\r\n"},
		{[]string{"HMGET", "missing", "f1", "f2"}, "*2\r\n$-1\r\n$-1\r\n"},
		{[]string{"HLEN", "missing"}, ":0\r\n"},
		{[]string{"HEXISTS", "missing", "f"}, ":0\r\n"},
		{[]string{"HGETALL", "missing"}, "*0\r\n"},
		{[]string{"HKEYS", "missing"}, "*0\r\n"},
		{[]string{"HVALS", "missing"}, "*0\r\n"},
		{[]string{"HDEL", "missing", "f"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestHashArityAndWrongType(t *testing.T) {
	exec := newStringExec(t)
	const wrong = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
	runStrings(exec, "SET", "s", "v")
	for _, args := range [][]string{
		{"HSET", "s", "f", "v"},
		{"HGET", "s", "f"},
		{"HMGET", "s", "f"},
		{"HDEL", "s", "f"},
		{"HLEN", "s"},
		{"HGETALL", "s"},
		{"HKEYS", "s"},
		{"HVALS", "s"},
		{"HEXISTS", "s", "f"},
		{"HRANDFIELD", "s"},
		{"HRANDFIELD", "s", "1"},
	} {
		if got := runStrings(exec, args...); got != wrong {
			t.Fatalf("%v = %q, want wrongtype", args, got)
		}
	}
	for _, args := range [][]string{
		{"HSET", "h", "f"},
		{"HSET", "h", "f", "v", "f2"},
		{"HGET", "h", "f", "extra"},
		{"HMGET", "h"},
		{"HDEL", "h"},
		{"HLEN", "h", "extra"},
		{"HGETALL", "h", "extra"},
		{"HKEYS", "h", "extra"},
		{"HVALS", "h", "extra"},
		{"HEXISTS", "h", "f", "extra"},
		{"HRANDFIELD"},
	} {
		name := strings.Fields(args[0])[0]
		got := runStrings(exec, args...)
		if !strings.HasPrefix(got, "-ERR wrong number of arguments for '") || !strings.Contains(got, strings.ToLower(name)) {
			t.Fatalf("%v = %q, want arity error", args, got)
		}
	}
	if got := runStrings(exec, "HRANDFIELD", "s", "abc"); got != "-ERR value is not an integer or out of range\r\n" {
		t.Fatalf("HRANDFIELD wrongtype bad count = %q", got)
	}
}

func TestHashRandField(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "HSET", "h", "a", "1", "b", "2", "c", "3")

	if got := runStrings(exec, "HRANDFIELD", "h"); got != "$1\r\na\r\n" && got != "$1\r\nb\r\n" && got != "$1\r\nc\r\n" {
		t.Fatalf("HRANDFIELD single = %q", got)
	}
	got := runStrings(exec, "HRANDFIELD", "h", "2")
	if !strings.HasPrefix(got, "*2\r\n") {
		t.Fatalf("HRANDFIELD 2 = %q", got)
	}
	if strings.Count(got, "$1\r\n") != 2 {
		t.Fatalf("HRANDFIELD 2 fields = %q", got)
	}
	for _, field := range []string{"a", "b", "c"} {
		if strings.Count(got, "$1\r\n"+field+"\r\n") > 1 {
			t.Fatalf("HRANDFIELD 2 repeated %s: %q", field, got)
		}
	}
	got = runStrings(exec, "HRANDFIELD", "h", "-5")
	if !strings.HasPrefix(got, "*5\r\n") {
		t.Fatalf("HRANDFIELD -5 = %q", got)
	}
	got = runStrings(exec, "HRANDFIELD", "h", "2", "WITHVALUES")
	if !strings.HasPrefix(got, "*4\r\n") {
		t.Fatalf("HRANDFIELD withvalues = %q", got)
	}
	for _, value := range []string{"$1\r\n1\r\n", "$1\r\n2\r\n", "$1\r\n3\r\n"} {
		if strings.Count(got, value) > 1 {
			t.Fatalf("HRANDFIELD withvalues repeated %q: %q", value, got)
		}
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"HRANDFIELD", "h", "0"}, "*0\r\n"},
		{[]string{"HRANDFIELD", "missing"}, "$-1\r\n"},
		{[]string{"HRANDFIELD", "missing", "2"}, "*0\r\n"},
		{[]string{"HRANDFIELD", "missing", "0"}, "*0\r\n"},
		{[]string{"HRANDFIELD", "missing", "-2"}, "*0\r\n"},
		{[]string{"HRANDFIELD", "h", "WITHVALUES"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"HRANDFIELD", "h", "abc"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"HRANDFIELD", "h", "-0"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"HRANDFIELD", "h", "-9223372036854775808"}, "-ERR value is out of range, value must between -9223372036854775807 and 9223372036854775807\r\n"},
		{[]string{"HRANDFIELD", "h", "1", "FOO"}, "-ERR syntax error\r\n"},
		{[]string{"HRANDFIELD", "h", "1", "WITHVALUES", "extra"}, "-ERR syntax error\r\n"},
		{[]string{"HRANDFIELD", "h", "-1000001"}, "-ERR count is out of range\r\n"},
		{[]string{"HRANDFIELD", "h", "-500001", "WITHVALUES"}, "-ERR count is out of range\r\n"},
		{[]string{"HRANDFIELD", "h", "-9223372036854775807"}, "-ERR count is out of range\r\n"},
		{[]string{"HRANDFIELD", "missing", "-1000001"}, "*0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
	for _, args := range [][]string{
		{"HRANDFIELD", "h", "1", "withvalues"},
		{"HRANDFIELD", "h", "-1", "WithValues"},
	} {
		if got := runStrings(exec, args...); !strings.HasPrefix(got, "*2\r\n") {
			t.Fatalf("%v = %q, want two bulk replies", args, got)
		}
	}
}

func TestHashPreservesTTL(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	runStrings(exec, "HSET", "h", "f", "v")
	runStrings(exec, "PEXPIRE", "h", "1000")
	runStrings(exec, "HSET", "h", "f2", "v2")
	if got := runStrings(exec, "PTTL", "h"); got != ":1000\r\n" {
		t.Fatalf("PTTL after HSET = %q, want 1000", got)
	}
	runStrings(exec, "HDEL", "h", "f")
	if got := runStrings(exec, "PTTL", "h"); got != ":1000\r\n" {
		t.Fatalf("PTTL after HDEL = %q, want 1000", got)
	}
	clk.Advance(1001 * time.Millisecond)
	if got := runStrings(exec, "HLEN", "h"); got != ":0\r\n" {
		t.Fatalf("HLEN after expiry = %q", got)
	}
}
