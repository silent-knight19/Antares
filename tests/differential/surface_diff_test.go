package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
)

// TestDiffConnectionSurface covers the connection/keyspace commands that the
// family corpora do not touch: PING, ECHO, HELLO, and CLIENT error paths.
// COMMAND is a documented divergence (empty array) and BGREWRITEAOF depends on
// AOF configuration, so neither is byte-diffed here.
func TestDiffConnectionSurface(t *testing.T) {
	refAddr := os.Getenv("REDIS_REF_ADDR")
	if refAddr == "" {
		t.Skip("REDIS_REF_ADDR not set; run make diff-test")
	}
	ctx := context.Background()
	ref := newClient(t, refAddr)
	ant := newClient(t, startAntares(t))
	if err := ref.Ping(ctx).Err(); err != nil {
		t.Fatalf("reference Redis at %s is unreachable: %v", refAddr, err)
	}

	for _, tc := range []struct {
		name string
		args []any
	}{
		{"ping", []any{"PING"}},
		{"ping message", []any{"PING", "hello"}},
		{"ping binary", []any{"PING", "a\x00\r\nb"}},
		{"ping arity", []any{"PING", "a", "b"}},
		{"echo", []any{"ECHO", "hello"}},
		{"echo empty", []any{"ECHO", ""}},
		{"echo binary", []any{"ECHO", "a\x00\r\nb"}},
		{"echo arity", []any{"ECHO"}},
		{"echo extra", []any{"ECHO", "a", "b"}},
		{"hello default", []any{"HELLO"}},
		{"hello 2", []any{"HELLO", "2"}},
		{"hello 1", []any{"HELLO", "1"}},
		{"hello 0", []any{"HELLO", "0"}},
		{"hello 4", []any{"HELLO", "4"}},
		{"hello leading zero", []any{"HELLO", "02"}},
		{"hello bad version", []any{"HELLO", "abc"}},
		{"hello setname", []any{"HELLO", "2", "SETNAME", "diff"}},
		{"hello bad name", []any{"HELLO", "2", "SETNAME", "a b"}},
		{"hello unknown option", []any{"HELLO", "2", "FOO"}},
		{"hello auth", []any{"HELLO", "2", "AUTH", "user", "pass"}},
		{"client arity", []any{"CLIENT"}},
		{"client id extra", []any{"CLIENT", "ID", "extra"}},
		{"client setname ok", []any{"CLIENT", "SETNAME", "diff"}},
		{"client setname bad", []any{"CLIENT", "SETNAME", "a b"}},
		{"client setname arity", []any{"CLIENT", "SETNAME"}},
		{"client setinfo ok", []any{"CLIENT", "SETINFO", "lib-name", "diff"}},
		{"client setinfo unknown", []any{"CLIENT", "SETINFO", "FOO", "bar"}},
		{"client setinfo arity", []any{"CLIENT", "SETINFO", "lib-name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := outcome(ctx, ref, tc.args...)
			got := outcome(ctx, ant, tc.args...)
			if canon := helloCanonical; tc.args[0] == "HELLO" {
				want = canon(want)
				got = canon(got)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("args %v\nreference: %#v\nantares:   %#v", tc.args, want, got)
			}
		})
	}
}

// helloCanonical drops the per-connection id, which is allocated differently by
// the two servers, and compares the remaining handshake fields.
func helloCanonical(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr)%2 != 0 {
		return v
	}
	m := make(map[string]any, len(arr)/2)
	for i := 0; i < len(arr); i += 2 {
		m[fmt.Sprint(arr[i])] = arr[i+1]
	}
	if _, ok := m["id"]; ok {
		m["id"] = int64(0)
	}
	return m
}
