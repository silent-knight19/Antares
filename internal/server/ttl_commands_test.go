package server

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/store"
)

func intReply(t *testing.T, got string) int64 {
	t.Helper()
	if len(got) < 4 || got[0] != ':' || !strings.HasSuffix(got, "\r\n") {
		t.Fatalf("reply %q is not an integer", got)
	}
	n, err := strconv.ParseInt(got[1:len(got)-2], 10, 64)
	if err != nil {
		t.Fatalf("reply %q: %v", got, err)
	}
	return n
}

func TestTTLBasics(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"TTL", "missing"}, ":-2\r\n"},
		{[]string{"PTTL", "missing"}, ":-2\r\n"},
		{[]string{"PERSIST", "missing"}, ":0\r\n"},
		{[]string{"SET", "k", "v"}, "+OK\r\n"},
		{[]string{"TTL", "k"}, ":-1\r\n"},
		{[]string{"PTTL", "k"}, ":-1\r\n"},
		{[]string{"PERSIST", "k"}, ":0\r\n"},
		{[]string{"EXPIRE", "k", "100"}, ":1\r\n"},
		{[]string{"TTL", "k"}, ":100\r\n"},
		{[]string{"GET", "k"}, "$1\r\nv\r\n"},
		{[]string{"TTL", "k"}, ":100\r\n"},
		{[]string{"PERSIST", "k"}, ":1\r\n"},
		{[]string{"TTL", "k"}, ":-1\r\n"},
		{[]string{"PERSIST", "k"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
	if pttl := intReply(t, runStrings(exec, "EXPIRE", "k", "100")); pttl != 1 {
		t.Fatalf("EXPIRE k 100 = %d, want 1", pttl)
	}
	got := intReply(t, runStrings(exec, "PTTL", "k"))
	if got <= 99000 || got > 100000 {
		t.Fatalf("PTTL after EXPIRE 100 = %d, want 99000..100000", got)
	}
}

func TestTTLRounding(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		px   string
		want string
	}{
		{"1600", ":2\r\n"},
		{"1400", ":1\r\n"},
		{"400", ":0\r\n"},
	} {
		if got := runStrings(exec, "SET", "r", "v", "PX", tc.px); got != "+OK\r\n" {
			t.Fatalf("SET PX %s = %q", tc.px, got)
		}
		if got := runStrings(exec, "TTL", "r"); got != tc.want {
			t.Fatalf("TTL after PX %s = %q, want %q", tc.px, got, tc.want)
		}
	}
}

func TestExpireDeletesAndLazyExpiry(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "zero", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "zero", "0"}, ":1\r\n"},
		{[]string{"EXISTS", "zero"}, ":0\r\n"},
		{[]string{"SET", "neg", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "neg", "-1"}, ":1\r\n"},
		{[]string{"EXISTS", "neg"}, ":0\r\n"},
		{[]string{"SET", "at0", "v"}, "+OK\r\n"},
		{[]string{"PEXPIREAT", "at0", "0"}, ":1\r\n"},
		{[]string{"EXISTS", "at0"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}

	if got := runStrings(exec, "SET", "dead", "v", "PX", "20"); got != "+OK\r\n" {
		t.Fatalf("SET dead = %q", got)
	}
	clk.Advance(21 * time.Millisecond)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"GET", "dead"}, "$-1\r\n"},
		{[]string{"TTL", "dead"}, ":-2\r\n"},
		{[]string{"PTTL", "dead"}, ":-2\r\n"},
		{[]string{"PERSIST", "dead"}, ":0\r\n"},
		{[]string{"EXISTS", "dead"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestExpireRelativeAndAbsolute(t *testing.T) {
	exec := newStringExec(t)
	if got := runStrings(exec, "SET", "ms", "v"); got != "+OK\r\n" {
		t.Fatalf("SET ms = %q", got)
	}
	if got := runStrings(exec, "PEXPIRE", "ms", "10000"); got != ":1\r\n" {
		t.Fatalf("PEXPIRE ms = %q", got)
	}
	if got := runStrings(exec, "TTL", "ms"); got != ":10\r\n" {
		t.Fatalf("TTL after PEXPIRE 10000 = %q", got)
	}
	got := intReply(t, runStrings(exec, "PTTL", "ms"))
	if got <= 9000 || got > 10000 {
		t.Fatalf("PTTL after PEXPIRE 10000 = %d, want 9000..10000", got)
	}

	sec := time.Now().Add(100 * time.Second).Unix()
	if got := runStrings(exec, "SET", "at", "v"); got != "+OK\r\n" {
		t.Fatalf("SET at = %q", got)
	}
	if got := runStrings(exec, "EXPIREAT", "at", strconv.FormatInt(sec, 10)); got != ":1\r\n" {
		t.Fatalf("EXPIREAT at = %q", got)
	}
	if ttl := intReply(t, runStrings(exec, "TTL", "at")); ttl < 99 || ttl > 100 {
		t.Fatalf("TTL after EXPIREAT = %d, want 99..100", ttl)
	}
	if got := runStrings(exec, "PERSIST", "at"); got != ":1\r\n" {
		t.Fatalf("PERSIST at = %q", got)
	}

	ms := time.Now().Add(100 * time.Second).UnixMilli()
	if got := runStrings(exec, "SET", "atms", "v"); got != "+OK\r\n" {
		t.Fatalf("SET atms = %q", got)
	}
	if got := runStrings(exec, "PEXPIREAT", "atms", strconv.FormatInt(ms, 10)); got != ":1\r\n" {
		t.Fatalf("PEXPIREAT atms = %q", got)
	}
	if pttl := intReply(t, runStrings(exec, "PTTL", "atms")); pttl < 99000 || pttl > 100000 {
		t.Fatalf("PTTL after PEXPIREAT = %d, want 99000..100000", pttl)
	}
}

func TestExpireConditions(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "vol", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "vol", "10", "GT"}, ":0\r\n"},
		{[]string{"TTL", "vol"}, ":-1\r\n"},
		{[]string{"EXPIRE", "vol", "10", "LT"}, ":1\r\n"},
		{[]string{"TTL", "vol"}, ":10\r\n"},
		{[]string{"EXPIRE", "vol", "20", "NX"}, ":0\r\n"},
		{[]string{"EXPIRE", "vol", "20", "XX"}, ":1\r\n"},
		{[]string{"TTL", "vol"}, ":20\r\n"},
		{[]string{"EXPIRE", "vol", "5", "GT"}, ":0\r\n"},
		{[]string{"EXPIRE", "vol", "30", "LT"}, ":0\r\n"},
		{[]string{"EXPIRE", "vol", "30", "GT"}, ":1\r\n"},
		{[]string{"EXPIRE", "vol", "10", "LT"}, ":1\r\n"},
		{[]string{"TTL", "vol"}, ":10\r\n"},
		{[]string{"EXPIRE", "vol", "10", "XX", "GT"}, ":0\r\n"},
		{[]string{"EXPIRE", "vol", "20", "XX", "GT"}, ":1\r\n"},
		{[]string{"EXPIRE", "vol", "30", "XX", "LT"}, ":0\r\n"},
		{[]string{"EXPIRE", "vol", "10", "XX", "LT"}, ":1\r\n"},
		{[]string{"TTL", "vol"}, ":10\r\n"},
		{[]string{"SET", "nx", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "nx", "10", "NX"}, ":1\r\n"},
		{[]string{"EXPIRE", "nx", "10", "NX", "NX"}, ":0\r\n"},
		{[]string{"SET", "xx", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "xx", "10", "XX"}, ":0\r\n"},
		{[]string{"TTL", "xx"}, ":-1\r\n"},
		{[]string{"SET", "pastgt", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "pastgt", "-5", "GT"}, ":0\r\n"},
		{[]string{"EXISTS", "pastgt"}, ":1\r\n"},
		{[]string{"SET", "pastlt", "v"}, "+OK\r\n"},
		{[]string{"EXPIRE", "pastlt", "-5", "LT"}, ":1\r\n"},
		{[]string{"EXISTS", "pastlt"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestExpireConditionsEquality(t *testing.T) {
	st := store.New()
	exec := newExecutor(st, nil, false, clock.Real{})
	t.Cleanup(exec.Close)
	st.Set("key", []byte("value"))
	at := time.Now().Add(time.Minute).UnixMilli()
	st.SetExpire("key", at)
	when := strconv.FormatInt(at, 10)

	for _, cond := range []string{"GT", "LT"} {
		if got := runStrings(exec, "PEXPIREAT", "key", when, cond); got != ":0\r\n" {
			t.Fatalf("PEXPIREAT equal %s = %q, want :0", cond, got)
		}
	}
}

func TestLazyExpiryOnReadPaths(t *testing.T) {
	past := time.Now().Add(-time.Second).UnixMilli()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"get", []string{"GET", "dead"}, "$-1\r\n"},
		{"mget", []string{"MGET", "dead"}, "*1\r\n$-1\r\n"},
		{"strlen", []string{"STRLEN", "dead"}, ":0\r\n"},
		{"exists", []string{"EXISTS", "dead"}, ":0\r\n"},
		{"type", []string{"TYPE", "dead"}, "+none\r\n"},
		{"ttl", []string{"TTL", "dead"}, ":-2\r\n"},
		{"pttl", []string{"PTTL", "dead"}, ":-2\r\n"},
		{"del", []string{"DEL", "dead"}, ":0\r\n"},
		{"persist", []string{"PERSIST", "dead"}, ":0\r\n"},
		{"expire", []string{"EXPIRE", "dead", "10"}, ":0\r\n"},
		{"pexpire", []string{"PEXPIRE", "dead", "10000"}, ":0\r\n"},
		{"expireat", []string{"EXPIREAT", "dead", "9999999999"}, ":0\r\n"},
		{"pexpireat", []string{"PEXPIREAT", "dead", "9999999999999"}, ":0\r\n"},
		{"set xx", []string{"SET", "dead", "new", "XX"}, "$-1\r\n"},
		{"set xx get", []string{"SET", "dead", "new", "XX", "GET"}, "$-1\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			exec := newExecutor(st, nil, false, clock.Real{})
			t.Cleanup(exec.Close)
			st.SetWithTTL("dead", []byte("old"), past)

			if got := runStrings(exec, tc.args...); got != tc.want {
				t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
			}
			if got := runStrings(exec, "DBSIZE"); got != ":0\r\n" {
				t.Fatalf("DBSIZE after %v = %q, want 0 (lazy delete)", tc.args, got)
			}
		})
	}
}

func TestLazyExpiryOnReadModifyWritePaths(t *testing.T) {
	past := time.Now().Add(-time.Second).UnixMilli()
	cases := []struct {
		name string
		args []string
		want string
		val  string
	}{
		{"set nx", []string{"SET", "dead", "new", "NX"}, "+OK\r\n", "new"},
		{"set nx get", []string{"SET", "dead", "new", "NX", "GET"}, "$-1\r\n", "new"},
		{"set get", []string{"SET", "dead", "new", "GET"}, "$-1\r\n", "new"},
		{"set keepttl", []string{"SET", "dead", "new", "KEEPTTL"}, "+OK\r\n", "new"},
		{"setnx", []string{"SETNX", "dead", "new"}, ":1\r\n", "new"},
		{"getset", []string{"GETSET", "dead", "new"}, "$-1\r\n", "new"},
		{"incr", []string{"INCR", "dead"}, ":1\r\n", "1"},
		{"incrbyfloat", []string{"INCRBYFLOAT", "dead", "1.5"}, "$3\r\n1.5\r\n", "1.5"},
		{"append", []string{"APPEND", "dead", "x"}, ":1\r\n", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			exec := newExecutor(st, nil, false, clock.Real{})
			t.Cleanup(exec.Close)
			st.SetWithTTL("dead", []byte("old"), past)

			if got := runStrings(exec, tc.args...); got != tc.want {
				t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
			}
			if got := runStrings(exec, "GET", "dead"); got != "$"+strconv.Itoa(len(tc.val))+"\r\n"+tc.val+"\r\n" {
				t.Fatalf("GET dead after %v = %q, want %q", tc.args, got, tc.val)
			}
			if got := runStrings(exec, "TTL", "dead"); got != ":-1\r\n" {
				t.Fatalf("TTL dead after %v = %q, want no expiry", tc.args, got)
			}
			if got := runStrings(exec, "DBSIZE"); got != ":1\r\n" {
				t.Fatalf("DBSIZE after %v = %q, want 1", tc.args, got)
			}
		})
	}
}

func TestDBSizeCountsPhysicalKeys(t *testing.T) {
	st := store.New()
	exec := newExecutor(st, nil, false, clock.Real{})
	t.Cleanup(exec.Close)
	st.Set("live", []byte("value"))
	st.SetWithTTL("dead", []byte("value"), time.Now().Add(-time.Second).UnixMilli())

	if got := runStrings(exec, "DBSIZE"); got != ":2\r\n" {
		t.Fatalf("DBSIZE before access = %q, want 2 (Redis counts physical keys)", got)
	}
	if got := runStrings(exec, "GET", "dead"); got != "$-1\r\n" {
		t.Fatalf("GET dead = %q, want nil", got)
	}
	if got := runStrings(exec, "DBSIZE"); got != ":1\r\n" {
		t.Fatalf("DBSIZE after lazy delete = %q, want 1", got)
	}
	if got := runStrings(exec, "DEL", "live"); got != ":1\r\n" {
		t.Fatalf("DEL live = %q, want 1", got)
	}
	if got := runStrings(exec, "DBSIZE"); got != ":0\r\n" {
		t.Fatalf("DBSIZE after DEL = %q, want 0", got)
	}
}

func TestExpireErrors(t *testing.T) {
	exec := newStringExec(t)
	const (
		notInt     = "-ERR value is not an integer or out of range\r\n"
		nxConflict = "-ERR NX and XX, GT or LT options at the same time are not compatible\r\n"
		gtlt       = "-ERR GT and LT options at the same time are not compatible\r\n"
	)
	if got := runStrings(exec, "SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("SET k = %q", got)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"EXPIRE", "k", "abc"}, notInt},
		{[]string{"EXPIRE", "k", "10", "FOO"}, "-ERR Unsupported option FOO\r\n"},
		{[]string{"EXPIRE", "k", "10", "foo"}, "-ERR Unsupported option foo\r\n"},
		{[]string{"EXPIRE", "k", "10", "NX", "XX"}, nxConflict},
		{[]string{"EXPIRE", "k", "10", "NX", "GT"}, nxConflict},
		{[]string{"EXPIRE", "k", "10", "NX", "LT"}, nxConflict},
		{[]string{"EXPIRE", "k", "10", "GT", "LT"}, gtlt},
		{[]string{"EXPIRE", "k", "9223372036854775807"}, "-ERR invalid expire time in 'expire' command\r\n"},
		{[]string{"PEXPIRE", "k", "9223372036854775807"}, "-ERR invalid expire time in 'pexpire' command\r\n"},
		{[]string{"EXPIREAT", "k", "9223372036854775807"}, "-ERR invalid expire time in 'expireat' command\r\n"},
		{[]string{"EXPIREAT", "k", "-9223372036854775808"}, "-ERR invalid expire time in 'expireat' command\r\n"},
		{[]string{"EXPIRE", "missing", "abc", "FOO"}, "-ERR Unsupported option FOO\r\n"},
		{[]string{"EXPIRE", "missing", "abc", "NX", "XX"}, nxConflict},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
	if got := runStrings(exec, "TTL", "k"); got != ":-1\r\n" {
		t.Fatalf("TTL changed by failed EXPIRE: %q", got)
	}

	if got := runStrings(exec, "PEXPIRE", "k", "-9223372036854775808"); got != ":1\r\n" {
		t.Fatalf("PEXPIRE MinInt64 = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "k"); got != ":0\r\n" {
		t.Fatalf("key survived PEXPIRE MinInt64: %q", got)
	}
	if got := runStrings(exec, "SET", "k", "v"); got != "+OK\r\n" {
		t.Fatalf("SET k = %q", got)
	}
	if got := runStrings(exec, "PEXPIREAT", "k", "-9223372036854775808"); got != ":1\r\n" {
		t.Fatalf("PEXPIREAT MinInt64 = %q", got)
	}
	if got := runStrings(exec, "EXISTS", "k"); got != ":0\r\n" {
		t.Fatalf("key survived PEXPIREAT MinInt64: %q", got)
	}
}

func TestTTLPreservedByKeepTTLAndClearedBySet(t *testing.T) {
	exec := newStringExec(t)
	if got := runStrings(exec, "SET", "k", "v", "PX", "10000"); got != "+OK\r\n" {
		t.Fatalf("SET = %q", got)
	}
	if got := runStrings(exec, "SET", "k", "v2", "KEEPTTL"); got != "+OK\r\n" {
		t.Fatalf("SET KEEPTTL = %q", got)
	}
	if got := runStrings(exec, "TTL", "k"); got != ":10\r\n" {
		t.Fatalf("TTL after KEEPTTL = %q", got)
	}
	if got := runStrings(exec, "SET", "k", "v3"); got != "+OK\r\n" {
		t.Fatalf("SET = %q", got)
	}
	if got := runStrings(exec, "TTL", "k"); got != ":-1\r\n" {
		t.Fatalf("TTL after plain SET = %q", got)
	}
}
