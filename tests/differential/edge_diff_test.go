package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDiffEdgeKeys applies one cross-family sequence to the key shapes most
// likely to break byte handling: the empty key, a key containing NUL/CRLF, a
// 1 KiB key, and a non-ASCII key.
func TestDiffEdgeKeys(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:edge:%d:", time.Now().UnixNano())
	keys := []string{
		"",
		prefix + "bin\x00\r\n",
		prefix + strings.Repeat("k", 1024),
		prefix + "ключ",
	}
	t.Cleanup(func() { _ = ref.Del(ctx, keys...).Err() })

	for i, key := range keys {
		t.Run(fmt.Sprintf("key%d", i), func(t *testing.T) {
			steps := []struct {
				args  []any
				canon func(any) any
			}{
				{args: []any{"DEL", key}},
				{args: []any{"SET", key, "v"}},
				{args: []any{"GET", key}},
				{args: []any{"TYPE", key}},
				{args: []any{"STRLEN", key}},
				{args: []any{"EXISTS", key}},
				{args: []any{"APPEND", key, "\x00\r\n"}},
				{args: []any{"GET", key}},
				{args: []any{"EXPIRE", key, "1000"}},
				{args: []any{"TTL", key}, canon: ttlCanonical},
				{args: []any{"PERSIST", key}},
				{args: []any{"DEL", key}},
				{args: []any{"RPUSH", key, "", "\x00\r\n"}},
				{args: []any{"LRANGE", key, "0", "-1"}},
				{args: []any{"LLEN", key}},
				{args: []any{"LPOP", key}},
				{args: []any{"DEL", key}},
				{args: []any{"HSET", key, "", "", "f\x00\r\n", "v\x00\r\n"}},
				{args: []any{"HGET", key, ""}},
				{args: []any{"HGETALL", key}, canon: hashCanonical},
				{args: []any{"HDEL", key, "", "f\x00\r\n"}},
				{args: []any{"DEL", key}},
				{args: []any{"SADD", key, "", "m\x00\r\n"}},
				{args: []any{"SMEMBERS", key}, canon: unorderedCanonical},
				{args: []any{"SISMEMBER", key, ""}},
				{args: []any{"SREM", key, "", "m\x00\r\n"}},
				{args: []any{"EXISTS", key}},
			}
			for _, step := range steps {
				want := outcome(ctx, ref, step.args...)
				got := outcome(ctx, ant, step.args...)
				if step.canon != nil {
					want = step.canon(want)
					got = step.canon(got)
				}
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("args %v\nreference: %#v\nantares:   %#v", step.args, want, got)
				}
			}
		})
	}
}
