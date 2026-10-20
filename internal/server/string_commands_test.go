package server

import (
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
)

func newStringExec(t *testing.T) *Executor {
	t.Helper()
	return newStringExecWithClock(t, clock.Real{})
}

func newStringExecWithClock(t *testing.T, c clock.Clock) *Executor {
	t.Helper()
	exec := NewExecutorWithClock(nil, nil, c)
	t.Cleanup(exec.Close)
	return exec
}

func runCommand(exec *Executor, args ...[]byte) string {
	enc := protocol.GetEncoder()
	defer enc.Release()
	exec.Handle(protocol.Request(args), enc)
	return string(enc.Bytes())
}

func runStrings(exec *Executor, args ...string) string {
	req := make([][]byte, len(args))
	for i, arg := range args {
		req[i] = []byte(arg)
	}
	return runCommand(exec, req...)
}

func TestStringCommandsBasics(t *testing.T) {
	exec := newStringExec(t)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "k", "v"}, "+OK\r\n"},
		{[]string{"GET", "k"}, "$1\r\nv\r\n"},
		{[]string{"TYPE", "k"}, "+string\r\n"},
		{[]string{"EXISTS", "k", "k", "missing"}, ":2\r\n"},
		{[]string{"DEL", "k", "k"}, ":1\r\n"},
		{[]string{"GET", "k"}, "$-1\r\n"},
		{[]string{"TYPE", "k"}, "+none\r\n"},
		{[]string{"EXISTS", "k"}, ":0\r\n"},
		{[]string{"DEL", "missing"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetConditionsAndGet(t *testing.T) {
	exec := newStringExec(t)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "k", "v"}, "+OK\r\n"},
		{[]string{"SET", "k", "v2", "NX"}, "$-1\r\n"},
		{[]string{"GET", "k"}, "$1\r\nv\r\n"},
		{[]string{"SET", "missing", "v2", "XX"}, "$-1\r\n"},
		{[]string{"EXISTS", "missing"}, ":0\r\n"},
		{[]string{"SET", "missing", "v", "NX"}, "+OK\r\n"},
		{[]string{"SET", "missing", "v2", "XX"}, "+OK\r\n"},
		{[]string{"GET", "missing"}, "$2\r\nv2\r\n"},
		{[]string{"SET", "k", "new", "GET"}, "$1\r\nv\r\n"},
		{[]string{"SET", "k", "other", "GET"}, "$3\r\nnew\r\n"},
		{[]string{"SET", "k", "v2", "NX", "GET"}, "$5\r\nother\r\n"},
		{[]string{"GET", "k"}, "$5\r\nother\r\n"},
		{[]string{"SET", "missing2", "v", "XX", "GET"}, "$-1\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetOptionErrors(t *testing.T) {
	exec := newStringExec(t)
	const (
		syntax    = "-ERR syntax error\r\n"
		notInt    = "-ERR value is not an integer or out of range\r\n"
		badExpiry = "-ERR invalid expire time in 'set' command\r\n"
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "k", "v", "NX", "XX"}, syntax},
		{[]string{"SET", "k", "v", "NX", "XX", "EX", "abc"}, syntax},
		{[]string{"SET", "k", "v", "EX", "abc", "NX", "XX"}, syntax},
		{[]string{"SET", "k", "v", "EX", "10", "KEEPTTL"}, syntax},
		{[]string{"SET", "k", "v", "KEEPTTL", "EX", "10"}, syntax},
		{[]string{"SET", "k", "v", "PX", "100", "EX", "5"}, syntax},
		{[]string{"SET", "k", "v", "FOO"}, syntax},
		{[]string{"SET", "k", "v", "EX"}, syntax},
		{[]string{"SET", "k", "v", "EX", "0"}, badExpiry},
		{[]string{"SET", "k", "v", "EX", "-1"}, badExpiry},
		{[]string{"SET", "k", "v", "PX", "0"}, badExpiry},
		{[]string{"SET", "k", "v", "EX", "abc"}, notInt},
		{[]string{"SET", "k", "v", "EX", "1.5"}, notInt},
		{[]string{"SET", "k", "v", "EX", "+10"}, notInt},
		{[]string{"SET", "k", "v", "EX", "010"}, notInt},
		{[]string{"SET", "k", "v", "EX", "1e3"}, notInt},
		{[]string{"SET", "k", "v", "EX", "9223372036854775808"}, notInt},
		{[]string{"SET", "k", "v", "EX", "9999999999999999"}, badExpiry},
		{[]string{"SET", "k", "v", "EX", "9223372036854775807"}, badExpiry},
		{[]string{"SET", "k", "v", "PX", "9223372036854775807"}, badExpiry},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetDuplicateOptionsAllowed(t *testing.T) {
	exec := newStringExec(t)
	for _, args := range [][]string{
		{"SET", "k", "v", "NX", "NX"},
		{"SET", "k", "v", "XX", "XX"},
		{"SET", "k", "v", "GET", "GET"},
		{"SET", "k", "v", "KEEPTTL", "KEEPTTL"},
		{"SET", "k", "v", "EX", "10", "EX", "20"},
		{"SET", "k", "v", "PX", "100", "PX", "200"},
	} {
		if got := runStrings(exec, args...); strings.HasPrefix(got, "-") {
			t.Fatalf("%v = %q, want a success reply", args, got)
		}
	}
}

func TestSetGetReturnsEmptyValue(t *testing.T) {
	exec := newStringExec(t)
	if got := runCommand(exec, []byte("SET"), []byte("k"), []byte{}); got != "+OK\r\n" {
		t.Fatalf("SET empty = %q, want +OK", got)
	}
	if got := runStrings(exec, "SET", "k", "new", "GET"); got != "$0\r\n\r\n" {
		t.Fatalf("SET GET over empty = %q, want $0", got)
	}
}

func TestKeepttlAndExpiry(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)

	if got := runStrings(exec, "SET", "k", "v", "PX", "200"); got != "+OK\r\n" {
		t.Fatalf("SET PX = %q", got)
	}
	if got := runStrings(exec, "SET", "k", "kept", "KEEPTTL"); got != "+OK\r\n" {
		t.Fatalf("SET KEEPTTL = %q", got)
	}
	clk.Advance(250 * time.Millisecond)
	if got := runStrings(exec, "GET", "k"); got != "$-1\r\n" {
		t.Fatalf("KEEPTTL did not preserve expiry: GET = %q", got)
	}

	if got := runStrings(exec, "SET", "k", "v", "PX", "200"); got != "+OK\r\n" {
		t.Fatalf("SET PX = %q", got)
	}
	if got := runStrings(exec, "SET", "k", "plain"); got != "+OK\r\n" {
		t.Fatalf("SET plain = %q", got)
	}
	clk.Advance(250 * time.Millisecond)
	if got := runStrings(exec, "GET", "k"); got != "$5\r\nplain\r\n" {
		t.Fatalf("plain SET did not clear TTL: GET = %q", got)
	}
}

func TestExpiredKeysAreInvisible(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	if got := runStrings(exec, "SET", "k", "v", "PX", "100"); got != "+OK\r\n" {
		t.Fatalf("SET PX = %q", got)
	}
	clk.Advance(150 * time.Millisecond)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"GET", "k"}, "$-1\r\n"},
		{[]string{"EXISTS", "k"}, ":0\r\n"},
		{[]string{"TYPE", "k"}, "+none\r\n"},
		{[]string{"DEL", "k"}, ":0\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestStringCommandArity(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "k"}, "-ERR wrong number of arguments for 'set' command\r\n"},
		{[]string{"GET"}, "-ERR wrong number of arguments for 'get' command\r\n"},
		{[]string{"DEL"}, "-ERR wrong number of arguments for 'del' command\r\n"},
		{[]string{"EXISTS"}, "-ERR wrong number of arguments for 'exists' command\r\n"},
		{[]string{"TYPE"}, "-ERR wrong number of arguments for 'type' command\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestSetOptionCaseInsensitive(t *testing.T) {
	exec := newStringExec(t)
	if got := runStrings(exec, "set", "k", "v", "ex", "10", "nx"); got != "+OK\r\n" {
		t.Fatalf("lowercase options = %q", got)
	}
	if got := runStrings(exec, "GET", "k"); got != "$1\r\nv\r\n" {
		t.Fatalf("GET after lowercase options = %q", got)
	}
}

func TestMSetMGet(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"MSET", "a", "1", "b", "2"}, "+OK\r\n"},
		{[]string{"MSET", "a", "3", "a", "4"}, "+OK\r\n"},
		{[]string{"GET", "a"}, "$1\r\n4\r\n"},
		{[]string{"MGET", "a", "b", "nope"}, "*3\r\n$1\r\n4\r\n$1\r\n2\r\n$-1\r\n"},
		{[]string{"MGET", "a", "a"}, "*2\r\n$1\r\n4\r\n$1\r\n4\r\n"},
		{[]string{"MSET", "empty", ""}, "+OK\r\n"},
		{[]string{"MGET", "empty"}, "*1\r\n$0\r\n\r\n"},
		{[]string{"MSET", "x"}, "-ERR wrong number of arguments for 'mset' command\r\n"},
		{[]string{"MSET", "x", "1", "y"}, "-ERR wrong number of arguments for 'mset' command\r\n"},
		{[]string{"MGET"}, "-ERR wrong number of arguments for 'mget' command\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestIncrDecr(t *testing.T) {
	exec := newStringExec(t)
	const (
		notInt   = "-ERR value is not an integer or out of range\r\n"
		overflow = "-ERR increment or decrement would overflow\r\n"
		decrOver = "-ERR decrement would overflow\r\n"
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"INCR", "n"}, ":1\r\n"},
		{[]string{"INCR", "n"}, ":2\r\n"},
		{[]string{"DECR", "n"}, ":1\r\n"},
		{[]string{"INCRBY", "n", "10"}, ":11\r\n"},
		{[]string{"DECRBY", "n", "3"}, ":8\r\n"},
		{[]string{"INCRBY", "n", "abc"}, notInt},
		{[]string{"INCRBY", "n", "+1"}, notInt},
		{[]string{"INCRBY", "n", "01"}, notInt},
		{[]string{"INCRBY", "n", "1.0"}, notInt},
		{[]string{"INCRBY", "n", "9223372036854775807"}, overflow},
		{[]string{"GET", "n"}, "$1\r\n8\r\n"},
		{[]string{"SET", "big", "9223372036854775807"}, "+OK\r\n"},
		{[]string{"INCR", "big"}, overflow},
		{[]string{"DECR", "big"}, ":9223372036854775806\r\n"},
		{[]string{"SET", "min", "-9223372036854775808"}, "+OK\r\n"},
		{[]string{"DECR", "min"}, overflow},
		{[]string{"INCR", "min"}, ":-9223372036854775807\r\n"},
		{[]string{"DECRBY", "n", "-9223372036854775808"}, decrOver},
		{[]string{"SET", "pre", "abc"}, "+OK\r\n"},
		{[]string{"DECRBY", "pre", "-9223372036854775808"}, decrOver},
		{[]string{"SET", "s", "abc"}, "+OK\r\n"},
		{[]string{"INCR", "s"}, notInt},
		{[]string{"GET", "s"}, "$3\r\nabc\r\n"},
		{[]string{"SET", "empty", ""}, "+OK\r\n"},
		{[]string{"INCR", "empty"}, notInt},
		{[]string{"INCRBY", "fresh", "-9223372036854775808"}, ":-9223372036854775808\r\n"},
		{[]string{"INCRBY", "fresh", "0"}, ":-9223372036854775808\r\n"},
		{[]string{"INCRBY", "under", "-1"}, ":-1\r\n"},
		{[]string{"INCRBY", "under", "-9223372036854775808"}, overflow},
		{[]string{"DECRBY", "over", "9223372036854775807"}, ":-9223372036854775807\r\n"},
		{[]string{"DECRBY", "over", "9223372036854775807"}, overflow},
		{[]string{"DECRBY", "negincr", "-9223372036854775807"}, ":9223372036854775807\r\n"},
		{[]string{"INCRBY", "n", "0x10"}, notInt},
		{[]string{"SET", "space", " 1"}, "+OK\r\n"},
		{[]string{"INCR", "space"}, notInt},
		{[]string{"INCR", "missing"}, ":1\r\n"},
		{[]string{"INCR", "n", "extra"}, "-ERR wrong number of arguments for 'incr' command\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestIncrByFloat(t *testing.T) {
	exec := newStringExec(t)
	const (
		notFloat = "-ERR value is not a valid float\r\n"
		floatRes = "-ERR increment would produce NaN or Infinity\r\n"
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"INCRBYFLOAT", "f", "1.5"}, "$3\r\n1.5\r\n"},
		{[]string{"INCRBYFLOAT", "f", "0.1"}, "$3\r\n1.6\r\n"},
		{[]string{"INCRBYFLOAT", "g", "0.1"}, "$3\r\n0.1\r\n"},
		{[]string{"INCRBYFLOAT", "g", "0.2"}, "$3\r\n0.3\r\n"},
		{[]string{"SET", "h", "1.234567890123456789"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "h", "0"}, "$19\r\n1.23456789012345679\r\n"},
		{[]string{"INCRBYFLOAT", "tiny", "1e-18"}, "$1\r\n0\r\n"},
		{[]string{"INCRBYFLOAT", "tiny2", "1e-17"}, "$19\r\n0.00000000000000001\r\n"},
		{[]string{"INCRBYFLOAT", "tiny3", "1e-4950"}, "$1\r\n0\r\n"},
		{[]string{"INCRBYFLOAT", "plus", "+1.5"}, "$3\r\n1.5\r\n"},
		{[]string{"INCRBYFLOAT", "dot", ".5"}, "$3\r\n0.5\r\n"},
		{[]string{"INCRBYFLOAT", "trail", "5."}, "$1\r\n5\r\n"},
		{[]string{"INCRBYFLOAT", "zeros", "01"}, "$1\r\n1\r\n"},
		{[]string{"INCRBYFLOAT", "exp", "1e2"}, "$3\r\n100\r\n"},
		{[]string{"INCRBYFLOAT", "hex", "0x1.8"}, "$3\r\n1.5\r\n"},
		{[]string{"INCRBYFLOAT", "hex2", "0x1p2"}, "$1\r\n4\r\n"},
		{[]string{"SET", "negzero", "-1e-18"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "negzero", "0"}, "$1\r\n0\r\n"},
		{[]string{"INCRBYFLOAT", "bad1", "1e4933"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad1b", "1.2e4932"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad2", "0x1p-20000"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad3", "1e-4966"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad4", "nan"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad5", "abc"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad6", ""}, notFloat},
		{[]string{"INCRBYFLOAT", "bad7", " 1"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad8", "1 "}, notFloat},
		{[]string{"INCRBYFLOAT", "bad9", "1_000"}, notFloat},
		{[]string{"INCRBYFLOAT", "bad10", "0x1p"}, notFloat},
		{[]string{"INCRBYFLOAT", "inf1", "inf"}, floatRes},
		{[]string{"INCRBYFLOAT", "inf2", "-inf"}, floatRes},
		{[]string{"INCRBYFLOAT", "inf3", "Infinity"}, floatRes},
		{[]string{"INCRBYFLOAT", "inf4", "+Infinity"}, floatRes},
		{[]string{"SET", "isf", "1"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "isf", "inf"}, floatRes},
		{[]string{"SET", "isf", "inf"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "isf", "1"}, floatRes},
		{[]string{"SET", "isf", "1.189731495357231765e4932"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "isf", "1.189731495357231765e4932"}, floatRes},
		{[]string{"SET", "crash", "inf"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "crash", "-inf"}, floatRes},
		{[]string{"GET", "crash"}, "$3\r\ninf\r\n"},
		{[]string{"SET", "crash", "-inf"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "crash", "inf"}, floatRes},
		{[]string{"INCRBYFLOAT", "under", "1e-999999999"}, notFloat},
		{[]string{"INCRBYFLOAT", "underhex", "0x1p-999999999"}, notFloat},
		{[]string{"INCRBYFLOAT", "zeroexp", "0e-999999999"}, "$1\r\n0\r\n"},
		{[]string{"INCRBYFLOAT", "zerohex", "0x0p-999999999"}, "$1\r\n0\r\n"},
		{[]string{"SET", "storedunder", "1e-999999999"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "storedunder", "0"}, notFloat},
		{[]string{"GET", "storedunder"}, "$12\r\n1e-999999999\r\n"},
		{[]string{"SET", "storedbad", "abc"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "storedbad", "1"}, notFloat},
		{[]string{"GET", "storedbad"}, "$3\r\nabc\r\n"},
		{[]string{"SET", "toobig", "1e4933"}, "+OK\r\n"},
		{[]string{"INCRBYFLOAT", "toobig", "0"}, notFloat},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}

	longOK := strings.Repeat("0", 5116) + "1.5"
	if got := runStrings(exec, "INCRBYFLOAT", "longok", longOK); got != "$3\r\n1.5\r\n" {
		t.Fatalf("INCRBYFLOAT 5119-byte value = %q", got)
	}
	if got := runStrings(exec, "INCRBYFLOAT", "longbad", longOK+"0"); got != notFloat {
		t.Fatalf("INCRBYFLOAT 5120-byte value = %q", got)
	}

	got := runStrings(exec, "INCRBYFLOAT", "huge", "1.189731495357231765e4932")
	if len(got) != len("$4933\r\n")+4933+2 || got[len("$4933\r\n"):len("$4933\r\n")+19] != "1189731495357231765" {
		t.Fatalf("INCRBYFLOAT huge = %q... (len %d)", got[:40], len(got))
	}
}

func TestAppendStrlenGetsetSetnx(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"APPEND", "a", "foo"}, ":3\r\n"},
		{[]string{"APPEND", "a", "bar"}, ":6\r\n"},
		{[]string{"GET", "a"}, "$6\r\nfoobar\r\n"},
		{[]string{"STRLEN", "a"}, ":6\r\n"},
		{[]string{"STRLEN", "nope"}, ":0\r\n"},
		{[]string{"APPEND", "empty", ""}, ":0\r\n"},
		{[]string{"EXISTS", "empty"}, ":1\r\n"},
		{[]string{"GET", "empty"}, "$0\r\n\r\n"},
		{[]string{"STRLEN", "empty"}, ":0\r\n"},
		{[]string{"APPEND", "empty", "x"}, ":1\r\n"},
		{[]string{"GETSET", "a", "new"}, "$6\r\nfoobar\r\n"},
		{[]string{"GET", "a"}, "$3\r\nnew\r\n"},
		{[]string{"GETSET", "nope", "v"}, "$-1\r\n"},
		{[]string{"GET", "nope"}, "$1\r\nv\r\n"},
		{[]string{"GETSET", "nope", ""}, "$1\r\nv\r\n"},
		{[]string{"GET", "nope"}, "$0\r\n\r\n"},
		{[]string{"SETNX", "a", "z"}, ":0\r\n"},
		{[]string{"GET", "a"}, "$3\r\nnew\r\n"},
		{[]string{"SETNX", "nope2", "v"}, ":1\r\n"},
		{[]string{"GET", "nope2"}, "$1\r\nv\r\n"},
		{[]string{"APPEND"}, "-ERR wrong number of arguments for 'append' command\r\n"},
		{[]string{"APPEND", "k"}, "-ERR wrong number of arguments for 'append' command\r\n"},
		{[]string{"STRLEN"}, "-ERR wrong number of arguments for 'strlen' command\r\n"},
		{[]string{"GETSET", "k"}, "-ERR wrong number of arguments for 'getset' command\r\n"},
		{[]string{"SETNX", "k"}, "-ERR wrong number of arguments for 'setnx' command\r\n"},
		{[]string{"SETNX", "k", "v", "x"}, "-ERR wrong number of arguments for 'setnx' command\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestStringTTLBehavior(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, clk)
	for _, args := range [][]string{
		{"SET", "incr", "1", "PX", "300"},
		{"SET", "append", "x", "PX", "300"},
		{"SET", "float", "1.5", "PX", "300"},
		{"SET", "setnx", "v", "PX", "300"},
		{"SET", "getset", "old", "PX", "300"},
		{"SET", "mset", "old", "PX", "300"},
	} {
		if got := runStrings(exec, args...); got != "+OK\r\n" {
			t.Fatalf("%v = %q", args, got)
		}
	}
	for _, args := range [][]string{
		{"INCR", "incr"},
		{"APPEND", "append", "y"},
		{"INCRBYFLOAT", "float", "0"},
		{"SETNX", "setnx", "z"},
		{"GETSET", "getset", "new"},
		{"MSET", "mset", "new"},
	} {
		if got := runStrings(exec, args...); got == "" || got[0] == '-' {
			t.Fatalf("%v = %q", args, got)
		}
	}
	clk.Advance(400 * time.Millisecond)
	for _, key := range []string{"incr", "append", "float", "setnx"} {
		if got := runStrings(exec, "GET", key); got != "$-1\r\n" {
			t.Fatalf("GET %s after TTL = %q, want nil", key, got)
		}
	}
	for key, want := range map[string]string{"getset": "$3\r\nnew\r\n", "mset": "$3\r\nnew\r\n"} {
		if got := runStrings(exec, "GET", key); got != want {
			t.Fatalf("GET %s = %q, want %q", key, got, want)
		}
	}
}

func TestEmptyKeyAndBinaryValues(t *testing.T) {
	exec := newStringExec(t)
	binary := "a\x00\r\nb"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SET", "", ""}, "+OK\r\n"},
		{[]string{"GET", ""}, "$0\r\n\r\n"},
		{[]string{"EXISTS", ""}, ":1\r\n"},
		{[]string{"TYPE", ""}, "+string\r\n"},
		{[]string{"STRLEN", ""}, ":0\r\n"},
		{[]string{"MSET", "", "x"}, "+OK\r\n"},
		{[]string{"GET", ""}, "$1\r\nx\r\n"},
		{[]string{"APPEND", "", ""}, ":1\r\n"},
		{[]string{"GETSET", "", ""}, "$1\r\nx\r\n"},
		{[]string{"GET", ""}, "$0\r\n\r\n"},
		{[]string{"SETNX", "", "y"}, ":0\r\n"},
		{[]string{"GET", ""}, "$0\r\n\r\n"},
		{[]string{"MGET", "", ""}, "*2\r\n$0\r\n\r\n$0\r\n\r\n"},
		{[]string{"DEL", "", ""}, ":1\r\n"},
		{[]string{"EXISTS", ""}, ":0\r\n"},
		{[]string{"SET", "bin", binary}, "+OK\r\n"},
		{[]string{"GET", "bin"}, "$5\r\na\x00\r\nb\r\n"},
		{[]string{"STRLEN", "bin"}, ":5\r\n"},
		{[]string{"APPEND", "bin", "\x00"}, ":6\r\n"},
		{[]string{"MGET", "bin"}, "*1\r\n$6\r\na\x00\r\nb\x00\r\n"},
		{[]string{"set", "lower", "v"}, "+OK\r\n"},
		{[]string{"GeT", "lower"}, "$1\r\nv\r\n"},
		{[]string{"mSeT", "lower", "w"}, "+OK\r\n"},
		{[]string{"get", "lower"}, "$1\r\nw\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%q = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestAppendLengthGuard(t *testing.T) {
	if got, ok := joinAppend(nil, []byte("abc"), 3); !ok || string(got) != "abc" {
		t.Fatalf("at-limit append = %q, %v; want abc, true", got, ok)
	}
	if got, ok := joinAppend([]byte("ab"), []byte("cd"), 4); !ok || string(got) != "abcd" {
		t.Fatalf("at-limit join = %q, %v; want abcd, true", got, ok)
	}
	if _, ok := joinAppend([]byte("ab"), []byte("cd"), 3); ok {
		t.Fatal("over-limit append was accepted")
	}
	if got, ok := joinAppend(nil, nil, 0); !ok || got == nil || len(got) != 0 {
		t.Fatalf("empty append = %#v, %v; want non-nil empty, true", got, ok)
	}
}
