package server

import (
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, value string
		want           bool
	}{
		{"", "", true},
		{"", "a", false},
		{"*", "", false},
		{"*", "abc", true},
		{"?", "a", true},
		{"?", "", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"a*c", "abbbc", true},
		{"a*c", "ab", false},
		{"**", "abc", true},
		{"[abc]", "b", true},
		{"[abc]", "d", false},
		{"[^abc]", "d", true},
		{"[^abc]", "a", false},
		{"[a-c]", "b", true},
		{"[c-a]", "b", true},
		{"[a-c]", "B", false},
		{"[\\]]", "]", true},
		{"\\*", "*", true},
		{"\\*", "x", false},
		{"\\", "\\", true},
		{"[a", "a", true},
		{"[", "[", false},
		{"[]", "x", false},
		{"[^]", "x", true},
		{"a\x00b", "a\x00b", true},
	}
	for _, tc := range tests {
		if got := globMatch([]byte(tc.pattern), []byte(tc.value)); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.value, got, tc.want)
		}
	}
}

func TestKeysCommand(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "a", "1")
	runStrings(exec, "SET", "ab", "2")
	runStrings(exec, "RPUSH", "list", "x")
	runStrings(exec, "SET", "", "empty")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"KEYS", "a"}, "*1\r\n$1\r\na\r\n"},
		{[]string{"KEYS", "a?"}, "*1\r\n$2\r\nab\r\n"},
		{[]string{"KEYS", "a*"}, "*2\r\n"},
		{[]string{"KEYS", "[al]*"}, "*3\r\n"},
		{[]string{"KEYS", "*"}, "*4\r\n"},
		{[]string{"KEYS", "**"}, "*3\r\n"},
		{[]string{"KEYS", "missing*"}, "*0\r\n"},
		{[]string{"KEYS", "*", "extra"}, "-ERR wrong number of arguments for 'keys' command\r\n"},
	} {
		got := runStrings(exec, tc.args...)
		if !strings.HasPrefix(got, tc.want) {
			t.Fatalf("%v = %q, want prefix %q", tc.args, got, tc.want)
		}
	}

	got := runStrings(exec, "KEYS", "a*")
	for _, key := range []string{"$1\r\na\r\n", "$2\r\nab\r\n"} {
		if !strings.Contains(got, key) {
			t.Fatalf("KEYS a* = %q, missing %q", got, key)
		}
	}
}

func TestRenameCommands(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, f)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"RENAME", "missing", "dst"}, "-ERR no such key\r\n"},
		{[]string{"RENAMENX", "missing", "dst"}, "-ERR no such key\r\n"},
		{[]string{"SET", "src", "value"}, "+OK\r\n"},
		{[]string{"PEXPIRE", "src", "100000"}, ":1\r\n"},
		{[]string{"RENAME", "src", "dst"}, "+OK\r\n"},
		{[]string{"GET", "dst"}, "$5\r\nvalue\r\n"},
		{[]string{"EXISTS", "src", "dst"}, ":1\r\n"},
		{[]string{"PTTL", "dst"}, ":100000\r\n"},
		{[]string{"RENAME", "dst", "dst"}, "+OK\r\n"},
		{[]string{"RENAMENX", "dst", "dst"}, ":0\r\n"},
		{[]string{"SET", "other", "old"}, "+OK\r\n"},
		{[]string{"RENAMENX", "dst", "other"}, ":0\r\n"},
		{[]string{"GET", "other"}, "$3\r\nold\r\n"},
		{[]string{"RENAMENX", "dst", "free"}, ":1\r\n"},
		{[]string{"GET", "free"}, "$5\r\nvalue\r\n"},
		{[]string{"EXISTS", "dst"}, ":0\r\n"},
		{[]string{"SET", "plain", "new"}, "+OK\r\n"},
		{[]string{"PEXPIRE", "other", "100000"}, ":1\r\n"},
		{[]string{"RENAME", "plain", "other"}, "+OK\r\n"},
		{[]string{"GET", "other"}, "$3\r\nnew\r\n"},
		{[]string{"PTTL", "other"}, ":-1\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}

	for _, args := range [][]string{
		{"RENAME", "onlyone"},
		{"RENAMENX", "onlyone"},
	} {
		if got := runStrings(exec, args...); !strings.Contains(got, "wrong number of arguments") {
			t.Fatalf("%v = %q, want arity error", args, got)
		}
	}
}

func TestRandomKeyAndFlush(t *testing.T) {
	exec := newStringExec(t)

	if got := runStrings(exec, "RANDOMKEY"); got != "$-1\r\n" {
		t.Fatalf("RANDOMKEY empty = %q", got)
	}
	runStrings(exec, "SET", "only", "value")
	if got := runStrings(exec, "RANDOMKEY"); got != "$4\r\nonly\r\n" {
		t.Fatalf("RANDOMKEY = %q", got)
	}

	if got := runStrings(exec, "FLUSHDB", "FOO"); got != "-ERR syntax error\r\n" {
		t.Fatalf("FLUSHDB FOO = %q", got)
	}
	if got := runStrings(exec, "FLUSHDB", "ASYNC", "SYNC"); got != "-ERR syntax error\r\n" {
		t.Fatalf("FLUSHDB ASYNC SYNC = %q", got)
	}
	if got := runStrings(exec, "DBSIZE"); got != ":1\r\n" {
		t.Fatalf("failed FLUSHDB changed the keyspace: DBSIZE = %q", got)
	}

	if got := runStrings(exec, "FLUSHDB", "async"); got != "+OK\r\n" {
		t.Fatalf("FLUSHDB async = %q", got)
	}
	if got := runStrings(exec, "DBSIZE"); got != ":0\r\n" {
		t.Fatalf("DBSIZE after FLUSHDB = %q", got)
	}

	runStrings(exec, "SET", "again", "value")
	if got := runStrings(exec, "FLUSHALL", "SYNC"); got != "+OK\r\n" {
		t.Fatalf("FLUSHALL SYNC = %q", got)
	}
	if got := runStrings(exec, "DBSIZE"); got != ":0\r\n" {
		t.Fatalf("DBSIZE after FLUSHALL = %q", got)
	}
	if got := runStrings(exec, "RANDOMKEY"); got != "$-1\r\n" {
		t.Fatalf("RANDOMKEY after FLUSHALL = %q", got)
	}
}
