package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestDiffHashes(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:hash:%d:", time.Now().UnixNano())
	k := func(s string) string { return prefix + s }
	t.Cleanup(func() {
		keys, err := ref.Keys(ctx, prefix+"*").Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = ref.Del(ctx, keys...).Err()
		}
	})

	corpus := []struct {
		name string
		args []any
	}{
		{"hset", []any{"HSET", k("h"), "f1", "v1", "f2", "v2"}},
		{"type hash", []any{"TYPE", k("h")}},
		{"hget", []any{"HGET", k("h"), "f1"}},
		{"hget missing field", []any{"HGET", k("h"), "missing"}},
		{"hget missing key", []any{"HGET", k("none"), "f"}},
		{"hmget", []any{"HMGET", k("h"), "f1", "missing", "f2"}},
		{"hmget missing key", []any{"HMGET", k("none"), "f1", "f2"}},
		{"hgetall", []any{"HGETALL", k("h")}},
		{"hkeys", []any{"HKEYS", k("h")}},
		{"hvals", []any{"HVALS", k("h")}},
		{"hlen", []any{"HLEN", k("h")}},
		{"hexists yes", []any{"HEXISTS", k("h"), "f1"}},
		{"hexists no", []any{"HEXISTS", k("h"), "missing"}},
		{"hset update and add", []any{"HSET", k("h"), "f1", "V1", "f3", "v3"}},
		{"hgetall after update", []any{"HGETALL", k("h")}},
		{"hdel duplicate", []any{"HDEL", k("h"), "f2", "missing", "f2"}},
		{"hgetall after hdel", []any{"HGETALL", k("h")}},
		{"hdel remaining", []any{"HDEL", k("h"), "f1", "f3"}},
		{"exists after hdel all", []any{"EXISTS", k("h")}},
		{"hdel missing key", []any{"HDEL", k("none"), "f"}},
		{"hlen missing", []any{"HLEN", k("none")}},
		{"hgetall missing", []any{"HGETALL", k("none")}},
		{"hkeys missing", []any{"HKEYS", k("none")}},
		{"hvals missing", []any{"HVALS", k("none")}},
		{"hexists missing", []any{"HEXISTS", k("none"), "f"}},
		{"hset odd", []any{"HSET", k("h"), "f", "v", "f2"}},
		{"hset arity", []any{"HSET", k("h"), "f"}},
		{"hget arity", []any{"HGET", k("h"), "f", "extra"}},
		{"hmget arity", []any{"HMGET", k("h")}},
		{"hdel arity", []any{"HDEL", k("h")}},
		{"hgetall arity", []any{"HGETALL", k("h"), "extra"}},
		{"hkeys arity", []any{"HKEYS", k("h"), "extra"}},
		{"hvals arity", []any{"HVALS", k("h"), "extra"}},
		{"hlen arity", []any{"HLEN", k("h"), "extra"}},
		{"hexists arity", []any{"HEXISTS", k("h"), "f", "extra"}},
		{"hrandfield arity", []any{"HRANDFIELD"}},
		{"string setup", []any{"SET", k("s"), "v"}},
		{"hget string", []any{"HGET", k("s"), "f"}},
		{"hmget string", []any{"HMGET", k("s"), "f"}},
		{"hgetall string", []any{"HGETALL", k("s")}},
		{"hkeys string", []any{"HKEYS", k("s")}},
		{"hvals string", []any{"HVALS", k("s")}},
		{"hlen string", []any{"HLEN", k("s")}},
		{"hexists string", []any{"HEXISTS", k("s"), "f"}},
		{"hset string", []any{"HSET", k("s"), "f", "v"}},
		{"hdel string", []any{"HDEL", k("s"), "f"}},
		{"hrandfield string", []any{"HRANDFIELD", k("s")}},
		{"hrandfield string count", []any{"HRANDFIELD", k("s"), "1"}},
		{"hrandfield string bad count", []any{"HRANDFIELD", k("s"), "abc"}},
		{"hrandfield empty count", []any{"HRANDFIELD", k("h2"), "0"}},
		{"hrandfield missing", []any{"HRANDFIELD", k("none")}},
		{"hrandfield missing count", []any{"HRANDFIELD", k("none"), "2"}},
		{"hrandfield missing zero", []any{"HRANDFIELD", k("none"), "0"}},
		{"hrandfield missing negative", []any{"HRANDFIELD", k("none"), "-2"}},
		{"hrandfield missing withvalues", []any{"HRANDFIELD", k("none"), "2", "WITHVALUES"}},
		{"hrandfield bad count", []any{"HRANDFIELD", k("h"), "abc"}},
		{"hrandfield withvalues count", []any{"HRANDFIELD", k("h"), "WITHVALUES"}},
		{"hrandfield min count", []any{"HRANDFIELD", k("h"), "-9223372036854775808"}},
		{"hrandfield syntax", []any{"HRANDFIELD", k("h"), "1", "FOO"}},
		{"hrandfield extra", []any{"HRANDFIELD", k("h"), "1", "WITHVALUES", "extra"}},
		{"hash setup for string ops", []any{"HSET", k("hs"), "f", "v"}},
		{"get hash", []any{"GET", k("hs")}},
		{"mget hash", []any{"MGET", k("hs"), k("missing")}},
		{"set get hash", []any{"SET", k("hs"), "x", "GET"}},
		{"setnx hash", []any{"SETNX", k("hs"), "x"}},
		{"incr hash", []any{"INCR", k("hs")}},
		{"lpush hash", []any{"LPUSH", k("hs"), "x"}},
		{"type hash after string ops", []any{"TYPE", k("hs")}},
		{"binary hash", []any{"HSET", k("bin"), "f\x00\r\n", "v\x00\r\n", "", ""}},
		{"binary hget", []any{"HGET", k("bin"), "f\x00\r\n"}},
		{"binary hgetall", []any{"HGETALL", k("bin")}},
	}

	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			want := outcome(ctx, ref, tc.args...)
			got := outcome(ctx, ant, tc.args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("args %v\nreference: %#v\nantares:   %#v", tc.args, want, got)
			}
		})
	}
}
