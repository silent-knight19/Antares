package server

import (
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestSetBasics(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SADD", "s", "a", "b", "c"}, ":3\r\n"},
		{[]string{"SADD", "s", "b", "d", "d"}, ":1\r\n"},
		{[]string{"TYPE", "s"}, "+set\r\n"},
		{[]string{"SMEMBERS", "s"}, "*4\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\nd\r\n"},
		{[]string{"SISMEMBER", "s", "a"}, ":1\r\n"},
		{[]string{"SISMEMBER", "s", "z"}, ":0\r\n"},
		{[]string{"SCARD", "s"}, ":4\r\n"},
		{[]string{"SREM", "s", "b", "z", "b"}, ":1\r\n"},
		{[]string{"SMEMBERS", "s"}, "*3\r\n$1\r\na\r\n$1\r\nc\r\n$1\r\nd\r\n"},
		{[]string{"SREM", "s", "a", "c", "d"}, ":3\r\n"},
		{[]string{"EXISTS", "s"}, ":0\r\n"},
		{[]string{"SMEMBERS", "missing"}, "*0\r\n"},
		{[]string{"SISMEMBER", "missing", "a"}, ":0\r\n"},
		{[]string{"SCARD", "missing"}, ":0\r\n"},
		{[]string{"SREM", "missing", "a"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetArityAndWrongType(t *testing.T) {
	exec := newStringExec(t)
	const wrong = "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
	runStrings(exec, "SET", "s", "v")
	for _, args := range [][]string{
		{"SADD", "s", "a"},
		{"SREM", "s", "a"},
		{"SMEMBERS", "s"},
		{"SISMEMBER", "s", "a"},
		{"SCARD", "s"},
		{"SPOP", "s"},
		{"SPOP", "s", "1"},
		{"SPOP", "s", "0"},
		{"SRANDMEMBER", "s"},
		{"SRANDMEMBER", "s", "1"},
	} {
		if got := runStrings(exec, args...); got != wrong {
			t.Fatalf("%v = %q, want wrongtype", args, got)
		}
	}
	for _, args := range [][]string{
		{"SADD", "s"},
		{"SREM", "s"},
		{"SMEMBERS", "s", "extra"},
		{"SISMEMBER", "s", "a", "extra"},
		{"SCARD", "s", "extra"},
		{"SPOP"},
		{"SRANDMEMBER"},
	} {
		name := strings.ToLower(args[0])
		got := runStrings(exec, args...)
		if !strings.HasPrefix(got, "-ERR wrong number of arguments for '") || !strings.Contains(got, name) {
			t.Fatalf("%v = %q, want arity error", args, got)
		}
	}
	if got := runStrings(exec, "SPOP", "s", "abc"); got != "-ERR value is out of range, must be positive\r\n" {
		t.Fatalf("SPOP string bad count = %q", got)
	}
	if got := runStrings(exec, "SRANDMEMBER", "s", "abc"); got != "-ERR value is not an integer or out of range\r\n" {
		t.Fatalf("SRANDMEMBER string bad count = %q", got)
	}
}

func TestSetPopAndRand(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SADD", "p", "only")
	if got := runStrings(exec, "SPOP", "p"); got != "$4\r\nonly\r\n" {
		t.Fatalf("SPOP single = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "p"); got != ":0\r\n" {
		t.Fatalf("EXISTS after SPOP = %q", got)
	}

	runStrings(exec, "SADD", "p", "a", "b", "c")
	got := runStrings(exec, "SPOP", "p", "2")
	if !strings.HasPrefix(got, "*2\r\n") || strings.Count(got, "$1\r\n") != 2 {
		t.Fatalf("SPOP 2 = %q", got)
	}
	if got := runStrings(exec, "SCARD", "p"); got != ":1\r\n" {
		t.Fatalf("SCARD after SPOP 2 = %q", got)
	}
	if got := runStrings(exec, "SPOP", "p", "0"); got != "*0\r\n" {
		t.Fatalf("SPOP 0 = %q", got)
	}
	if got := runStrings(exec, "SCARD", "p"); got != ":1\r\n" {
		t.Fatalf("SCARD after SPOP 0 = %q", got)
	}
	if got := runStrings(exec, "SPOP", "p", "9"); !strings.HasPrefix(got, "*1\r\n$1\r\n") {
		t.Fatalf("SPOP 9 = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "p"); got != ":0\r\n" {
		t.Fatalf("EXISTS after SPOP all = %q", got)
	}

	runStrings(exec, "SADD", "q", "b")
	if got := runStrings(exec, "SRANDMEMBER", "q"); got != "$1\r\nb\r\n" {
		t.Fatalf("SRANDMEMBER single = %q", got)
	}
	if got := runStrings(exec, "SCARD", "q"); got != ":1\r\n" {
		t.Fatalf("SCARD after SRANDMEMBER = %q", got)
	}
	if got := runStrings(exec, "SRANDMEMBER", "q", "-3"); got != "*3\r\n$1\r\nb\r\n$1\r\nb\r\n$1\r\nb\r\n" {
		t.Fatalf("SRANDMEMBER -3 = %q", got)
	}
	if got := runStrings(exec, "SRANDMEMBER", "q", "1"); got != "*1\r\n$1\r\nb\r\n" {
		t.Fatalf("SRANDMEMBER 1 = %q", got)
	}

	runStrings(exec, "SADD", "r", "a", "b", "c")
	got = runStrings(exec, "SRANDMEMBER", "r", "2")
	if !strings.HasPrefix(got, "*2\r\n") || strings.Count(got, "$1\r\n") != 2 {
		t.Fatalf("SRANDMEMBER 2 = %q", got)
	}
	for _, member := range []string{"a", "b", "c"} {
		if strings.Count(got, "$1\r\n"+member+"\r\n") > 1 {
			t.Fatalf("SRANDMEMBER 2 repeated %s: %q", member, got)
		}
	}
	if got := runStrings(exec, "SRANDMEMBER", "r", "9"); got != "*3\r\n$1\r\na\r\n$1\r\nb\r\n$1\r\nc\r\n" && got != "*3\r\n$1\r\na\r\n$1\r\nc\r\n$1\r\nb\r\n" && got != "*3\r\n$1\r\nb\r\n$1\r\na\r\n$1\r\nc\r\n" && got != "*3\r\n$1\r\nb\r\n$1\r\nc\r\n$1\r\na\r\n" && got != "*3\r\n$1\r\nc\r\n$1\r\na\r\n$1\r\nb\r\n" && got != "*3\r\n$1\r\nc\r\n$1\r\nb\r\n$1\r\na\r\n" {
		t.Fatalf("SRANDMEMBER 9 = %q", got)
	}
}

func TestSetRandErrorsAndMissing(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SADD", "s", "a")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SPOP", "missing"}, "$-1\r\n"},
		{[]string{"SPOP", "missing", "2"}, "*0\r\n"},
		{[]string{"SPOP", "missing", "0"}, "*0\r\n"},
		{[]string{"SPOP", "s", "-1"}, "-ERR value is out of range, must be positive\r\n"},
		{[]string{"SPOP", "s", "+1"}, "-ERR value is out of range, must be positive\r\n"},
		{[]string{"SPOP", "s", "01"}, "-ERR value is out of range, must be positive\r\n"},
		{[]string{"SPOP", "s", "-9223372036854775808"}, "-ERR value is out of range, must be positive\r\n"},
		{[]string{"SPOP", "s", "1", "2"}, "-ERR syntax error\r\n"},
		{[]string{"SRANDMEMBER", "missing"}, "$-1\r\n"},
		{[]string{"SRANDMEMBER", "missing", "2"}, "*0\r\n"},
		{[]string{"SRANDMEMBER", "missing", "-2"}, "*0\r\n"},
		{[]string{"SRANDMEMBER", "s", "0"}, "*0\r\n"},
		{[]string{"SRANDMEMBER", "s", "abc"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"SRANDMEMBER", "s", "-9223372036854775808"}, "-ERR value is out of range, value must between -9223372036854775807 and 9223372036854775807\r\n"},
		{[]string{"SRANDMEMBER", "s", "1", "2"}, "-ERR syntax error\r\n"},
		{[]string{"SRANDMEMBER", "s", "WITHVALUES"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"SRANDMEMBER", "s", "-1000001"}, "-ERR count is out of range\r\n"},
		{[]string{"SRANDMEMBER", "s", "-9223372036854775807"}, "-ERR count is out of range\r\n"},
		{[]string{"SRANDMEMBER", "missing", "-1000001"}, "*0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetPreservesTTL(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	runStrings(exec, "SADD", "s", "a")
	runStrings(exec, "PEXPIRE", "s", "1000")
	runStrings(exec, "SADD", "s", "b")
	if got := runStrings(exec, "PTTL", "s"); got != ":1000\r\n" {
		t.Fatalf("PTTL after SADD = %q, want 1000", got)
	}
	runStrings(exec, "SREM", "s", "a")
	if got := runStrings(exec, "PTTL", "s"); got != ":1000\r\n" {
		t.Fatalf("PTTL after SREM = %q, want 1000", got)
	}
	runStrings(exec, "SPOP", "s", "0")
	if got := runStrings(exec, "PTTL", "s"); got != ":1000\r\n" {
		t.Fatalf("PTTL after SPOP 0 = %q, want 1000", got)
	}
	clk.Advance(1001 * time.Millisecond)
	if got := runStrings(exec, "SCARD", "s"); got != ":0\r\n" {
		t.Fatalf("SCARD after expiry = %q", got)
	}
}

func TestSetBinaryMembers(t *testing.T) {
	exec := newStringExec(t)
	if got := runStrings(exec, "SADD", "bin", "a\x00\r\nb", ""); got != ":2\r\n" {
		t.Fatalf("SADD binary = %q", got)
	}
	if got := runStrings(exec, "SISMEMBER", "bin", "a\x00\r\nb"); got != ":1\r\n" {
		t.Fatalf("SISMEMBER binary = %q", got)
	}
	if got := runStrings(exec, "SMEMBERS", "bin"); !strings.Contains(got, "a\x00\r\nb") || !strings.Contains(got, "$0\r\n") {
		t.Fatalf("SMEMBERS binary = %q", got)
	}
}
