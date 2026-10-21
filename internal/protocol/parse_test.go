package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type step struct {
	args   [][]byte
	errMsg string
}

type requestCase struct {
	name  string
	in    string
	steps []step
}

func expected(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func argsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func checkSteps(t *testing.T, in string, steps []step) {
	t.Helper()
	br := bufio.NewReader(strings.NewReader(in))
	for i, st := range steps {
		got, err := ReadRequest(br)
		if st.errMsg != "" {
			var pe *ProtocolError
			if !errors.As(err, &pe) {
				t.Fatalf("step %d: err = %v, want ProtocolError(%q)", i, err, st.errMsg)
			}
			if pe.Msg != st.errMsg {
				t.Fatalf("step %d: ProtocolError.Msg = %q, want %q", i, pe.Msg, st.errMsg)
			}
			return
		}
		if err != nil {
			t.Fatalf("step %d: unexpected error: %v", i, err)
		}
		if !argsEqual(got, st.args) {
			t.Fatalf("step %d: args = %q, want %q", i, got, st.args)
		}
	}
}

// Edge cases captured from the pinned reference Redis
// (redis:7.4.11@sha256:4fa24486...) on a raw TCP connection — see the
// PROJECT_LOG P1.2 entry for the capture set. The 200 KiB payload case is
// generated (size path, not a capture).
func TestReadRequest(t *testing.T) {
	bigArg := strings.Repeat("a", 200<<10)
	tests := []requestCase{
		{
			name:  "multibulk ping",
			in:    "*1\r\n$4\r\nPING\r\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name:  "inline ping",
			in:    "PING\r\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name:  "multibulk set",
			in:    "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n",
			steps: []step{{args: expected("SET", "k", "v")}},
		},
		{
			name:  "empty bulk argument",
			in:    "*2\r\n$4\r\nECHO\r\n$0\r\n\r\n",
			steps: []step{{args: expected("ECHO", "")}},
		},
		{
			name:  "bulk payload may contain CRLF",
			in:    "*3\r\n$3\r\nSET\r\n$2\r\n\r\n\r\n$1\r\nv\r\n",
			steps: []step{{args: expected("SET", "\r\n", "v")}},
		},
		{
			name:  "empty inline line is skipped",
			in:    "\r\nPING\r\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name:  "zero and negative multibulk counts are skipped",
			in:    "*0\r\n*-1\r\n*-2\r\n*1\r\n$4\r\nPING\r\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name: "pipelined multibulk",
			in:   "*1\r\n$4\r\nPING\r\n*1\r\n$4\r\nPING\r\n",
			steps: []step{
				{args: expected("PING")},
				{args: expected("PING")},
			},
		},
		{
			name: "pipeline mixing inline and multibulk",
			in:   "PING\r\n*1\r\n$4\r\nPING\r\nPING\r\n",
			steps: []step{
				{args: expected("PING")},
				{args: expected("PING")},
				{args: expected("PING")},
			},
		},
		{
			name:  "inline double quotes group one argument",
			in:    "GET \"a b\"\r\n",
			steps: []step{{args: expected("GET", "a b")}},
		},
		{
			name:  "inline single quotes group one argument",
			in:    "GET 'a b'\r\n",
			steps: []step{{args: expected("GET", "a b")}},
		},
		{
			name:  "double quotes decode hex escapes",
			in:    "ECHO \"a\\x42\"\r\n",
			steps: []step{{args: expected("ECHO", "aB")}},
		},
		{
			name:  "double quotes decode backslash-n",
			in:    "ECHO \"a\\nb\"\r\n",
			steps: []step{{args: expected("ECHO", "a\nb")}},
		},
		{
			name:  "double quotes decode escaped backslash",
			in:    "ECHO \"a\\\\b\"\r\n",
			steps: []step{{args: expected("ECHO", `a\b`)}},
		},
		{
			name:  "single quotes are literal",
			in:    "ECHO 'a\\nb'\r\n",
			steps: []step{{args: expected("ECHO", `a\nb`)}},
		},
		{
			name:  "inline collapses space runs",
			in:    "ECHO   a    b\r\n",
			steps: []step{{args: expected("ECHO", "a", "b")}},
		},
		{
			name:  "leading non-asterisk byte means inline",
			in:    "$4\r\n",
			steps: []step{{args: expected("$4")}},
		},
		{
			name:  "multibulk header CR is skipped blindly",
			in:    "*1\rX$4\r\nPING\r\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name: "bulk header CR is skipped blindly",
			in:   "*1\r\n$4\rXPING\r\nPING\r\n",
			steps: []step{
				{args: expected("PING")},
				{args: expected("PING")},
			},
		},
		{
			name:  "bulk payload trailer is not validated",
			in:    "*1\r\n$4\r\nPINGXX",
			steps: []step{{args: expected("PING")}},
		},
		{
			name:  "inline accepts bare LF",
			in:    "PING\n",
			steps: []step{{args: expected("PING")}},
		},
		{
			name:  "empty multibulk count",
			in:    "*\r\n",
			steps: []step{{errMsg: "invalid multibulk length"}},
		},
		{
			name:  "empty bulk count",
			in:    "*1\r\n$\r\n",
			steps: []step{{errMsg: "invalid bulk length"}},
		},
		{
			name:  "invalid multibulk length text",
			in:    "*abc\r\n",
			steps: []step{{errMsg: "invalid multibulk length"}},
		},
		{
			name:  "multibulk count above INT_MAX",
			in:    "*2147483648\r\n",
			steps: []step{{errMsg: "invalid multibulk length"}},
		},
		{
			name:  "negative bulk length",
			in:    "*3\r\n$3\r\nSET\r\n$-1\r\n$1\r\nv\r\n",
			steps: []step{{errMsg: "invalid bulk length"}},
		},
		{
			name:  "invalid bulk length text",
			in:    "*1\r\n$abc\r\n",
			steps: []step{{errMsg: "invalid bulk length"}},
		},
		{
			// Captured from redis 7.4.11: the raw byte goes on the wire, not
			// its UTF-8 rendering.
			name:  "expected-dollar error keeps the raw non-ASCII byte",
			in:    "*1\r\n\xff\r\n",
			steps: []step{{errMsg: "expected '$', got '\xff'"}},
		},
		{
			name:  "bulk length above 512MB",
			in:    "*2\r\n$4\r\nECHO\r\n$536870913\r\n",
			steps: []step{{errMsg: "invalid bulk length"}},
		},
		{
			name:  "unbalanced quotes in inline request",
			in:    "GET \"a b\r\n",
			steps: []step{{errMsg: "unbalanced quotes in request"}},
		},
		{
			name:  "inline request above 64KB without newline",
			in:    strings.Repeat("a", 70000),
			steps: []step{{errMsg: "too big inline request"}},
		},
		{
			name:  "bulk payload larger than the initial read buffer",
			in:    "*2\r\n$4\r\nECHO\r\n$204800\r\n" + bigArg + "\r\n",
			steps: []step{{args: expected("ECHO", bigArg)}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkSteps(t, tc.in, tc.steps)
		})
	}
}

func TestReadMultibulkRequestBudget(t *testing.T) {
	req, skip, err := readMultibulkLimit(
		bufio.NewReader(strings.NewReader("*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n")), 1024)
	if err != nil || skip {
		t.Fatalf("under-budget request: req=%q skip=%v err=%v", req, skip, err)
	}
	if len(req) != 2 || string(req[0]) != "foo" || string(req[1]) != "bar" {
		t.Fatalf("under-budget request = %q", req)
	}

	var br = bufio.NewReader(strings.NewReader("*1\r\n$100\r\n"))
	_, _, err = readMultibulkLimit(br, 50)
	if !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("oversized bulk header err = %v, want errRequestTooLarge", err)
	}

	_, _, err = readMultibulkLimit(
		bufio.NewReader(strings.NewReader("*2\r\n$0\r\n\r\n$0\r\n\r\n")), 40)
	if !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("per-argument overhead err = %v, want errRequestTooLarge", err)
	}
}

func TestReadBulkOverBudgetDoesNotReadPayload(t *testing.T) {
	// The header alone is enough to reject this, so an absent 512 MiB payload
	// must not change the error or force an allocation.
	_, err := readBulk(bufio.NewReader(strings.NewReader("$536870912\r\n")), 1024)
	if !errors.Is(err, errRequestTooLarge) {
		t.Fatalf("err = %v, want errRequestTooLarge", err)
	}
}

// Incomplete input must surface as an I/O error, never as a protocol error:
// real Redis simply keeps waiting for the rest of the bytes.
func TestReadRequestIncomplete(t *testing.T) {
	inputs := []string{
		"*1\r\n$4\r\nPI",
		"*1\r\n$4\r\nPINGX",
		"*1",
		"*1\r",
		"*1\n$4\nPING\n",
	}
	for _, in := range inputs {
		br := bufio.NewReader(strings.NewReader(in))
		_, err := ReadRequest(br)
		var pe *ProtocolError
		if errors.As(err, &pe) {
			t.Fatalf("input %q: got protocol error %q; incomplete input must wait, not fail", in, pe.Msg)
		}
		if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("input %q: err = %v, want io.EOF or io.ErrUnexpectedEOF", in, err)
		}
	}
}
