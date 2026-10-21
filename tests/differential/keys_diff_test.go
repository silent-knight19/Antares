package differential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDiffKeys(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:keys:%d:", time.Now().UnixNano())
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

	for _, rdb := range []*redis.Client{ref, ant} {
		for _, args := range [][]any{
			{"SET", k("k:a"), "va"},
			{"SET", k("k:b"), "vb"},
			{"SET", k("k:c"), "vc"},
			{"SET", k("k:z"), "vz"},
			{"SET", k("other"), "vo"},
			{"SET", k("star*"), "vs"},
			{"SET", k("q?"), "vq"},
			{"SET", k("br[a]"), "vb"},
			{"RPUSH", k("list"), "a", "b"},
			{"HSET", k("hash"), "f", "v"},
			{"SADD", k("set"), "m"},
			{"SET", k("ttl:src"), "v"},
			{"PEXPIRE", k("ttl:src"), "100000"},
			{"SET", k("ttl:dst"), "old"},
			{"PEXPIRE", k("ttl:dst"), "200000"},
		} {
			if err := rdb.Do(ctx, args...).Err(); err != nil {
				t.Fatalf("setup %v: %v", args, err)
			}
		}
	}

	for _, tc := range []struct {
		name string
		args []any
	}{
		{"all prefix keys", []any{"KEYS", prefix + "*"}},
		{"question mark", []any{"KEYS", prefix + "k:?"}},
		{"character class", []any{"KEYS", prefix + "k:[ab]"}},
		{"range", []any{"KEYS", prefix + "k:[a-c]"}},
		{"reversed range", []any{"KEYS", prefix + "k:[c-a]"}},
		{"negated class", []any{"KEYS", prefix + "k:[^a]"}},
		{"unterminated class", []any{"KEYS", prefix + "k:[a"}},
		{"unterminated range", []any{"KEYS", prefix + "k:[a-c"}},
		{"escaped star", []any{"KEYS", prefix + "star\\*"}},
		{"star wildcard", []any{"KEYS", prefix + "star*"}},
		{"escaped question", []any{"KEYS", prefix + "q\\?"}},
		{"escaped brackets", []any{"KEYS", prefix + "br\\[a\\]"}},
		{"brackets as class", []any{"KEYS", prefix + "br[a]"}},
		{"literal prefix", []any{"KEYS", prefix}},
		{"empty pattern", []any{"KEYS", ""}},
		{"keys arity", []any{"KEYS"}},
		{"keys extra arg", []any{"KEYS", "*", "extra"}},

		{"rename missing", []any{"RENAME", k("missing"), k("dest")}},
		{"renamenx missing", []any{"RENAMENX", k("missing"), k("dest")}},
		{"ttl before rename", []any{"TTL", k("ttl:src")}},
		{"rename with ttl", []any{"RENAME", k("ttl:src"), k("ttl:moved")}},
		{"ttl moved", []any{"TTL", k("ttl:moved")}},
		{"source gone", []any{"EXISTS", k("ttl:src")}},
		{"move unknown after rename", []any{"RENAME", k("ttl:src"), k("other")}},
		{"rename list", []any{"RENAME", k("list"), k("list2")}},
		{"list contents", []any{"LRANGE", k("list2"), "0", "-1"}},
		{"rename hash", []any{"RENAME", k("hash"), k("hash2")}},
		{"hash contents", []any{"HGETALL", k("hash2")}},
		{"rename set", []any{"RENAME", k("set"), k("set2")}},
		{"set contents", []any{"SMEMBERS", k("set2")}},
		{"set plain source", []any{"SET", k("plain"), "new"}},
		{"rename onto volatile", []any{"RENAME", k("plain"), k("ttl:dst")}},
		{"destination ttl cleared", []any{"TTL", k("ttl:dst")}},
		{"destination value", []any{"GET", k("ttl:dst")}},
		{"rename same key", []any{"RENAME", k("other"), k("other")}},
		{"renamenx same key", []any{"RENAMENX", k("other"), k("other")}},
		{"set nx source", []any{"SET", k("nxsrc"), "x"}},
		{"set nx dest", []any{"SET", k("nxdst"), "y"}},
		{"renamenx existing dest", []any{"RENAMENX", k("nxsrc"), k("nxdst")}},
		{"nx source kept", []any{"GET", k("nxsrc")}},
		{"nx dest kept", []any{"GET", k("nxdst")}},
		{"renamenx free dest", []any{"RENAMENX", k("nxsrc"), k("nxfree")}},
		{"nx moved", []any{"GET", k("nxfree")}},
		{"nx source gone", []any{"EXISTS", k("nxsrc")}},
		{"keys after mutations", []any{"KEYS", prefix + "*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := setOutcome(ctx, ref, tc.args...)
			got := setOutcome(ctx, ant, tc.args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("args %v\nreference: %#v\nantares:   %#v", tc.args, want, got)
			}
		})
	}

	t.Run("randomkey", func(t *testing.T) {
		for _, rdb := range []*redis.Client{ref, ant} {
			key, err := rdb.RandomKey(ctx).Result()
			if err != nil {
				t.Fatalf("RANDOMKEY: %v", err)
			}
			if !strings.HasPrefix(key, prefix) {
				t.Fatalf("RANDOMKEY = %q, want a key with prefix %q", key, prefix)
			}
			if n := rdb.Exists(ctx, key).Val(); n != 1 {
				t.Fatalf("RANDOMKEY returned %q, EXISTS = %d", key, n)
			}
		}
	})

	t.Run("flush", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			args []any
		}{
			{"flushdb bad option", []any{"FLUSHDB", "FOO"}},
			{"flushdb extra option", []any{"FLUSHDB", "ASYNC", "SYNC"}},
			{"flushall bad option", []any{"FLUSHALL", "FOO"}},
		} {
			want := outcome(ctx, ref, tc.args...)
			got := outcome(ctx, ant, tc.args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s: reference=%#v antares=%#v", tc.name, want, got)
			}
		}

		for _, rdb := range []*redis.Client{ref, ant} {
			if err := rdb.Do(ctx, "SET", k("flush-me"), "v").Err(); err != nil {
				t.Fatalf("flush setup: %v", err)
			}
		}
		want := outcome(ctx, ref, "FLUSHDB", "ASYNC")
		got := outcome(ctx, ant, "FLUSHDB", "ASYNC")
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("FLUSHDB ASYNC: reference=%#v antares=%#v", want, got)
		}
		for _, rdb := range []*redis.Client{ref, ant} {
			if n := rdb.DBSize(ctx).Val(); n != 0 {
				t.Fatalf("DBSIZE after FLUSHDB = %d, want 0", n)
			}
			if keys := rdb.Keys(ctx, "*").Val(); len(keys) != 0 {
				t.Fatalf("KEYS * after FLUSHDB = %v, want empty", keys)
			}
			if key, err := rdb.RandomKey(ctx).Result(); !errors.Is(err, redis.Nil) {
				t.Fatalf("RANDOMKEY after FLUSHDB = %q, %v; want redis.Nil", key, err)
			}
		}

		for _, rdb := range []*redis.Client{ref, ant} {
			if err := rdb.Do(ctx, "SET", k("flush-all"), "v").Err(); err != nil {
				t.Fatalf("flushall setup: %v", err)
			}
		}
		want = outcome(ctx, ref, "FLUSHALL", "SYNC")
		got = outcome(ctx, ant, "FLUSHALL", "SYNC")
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("FLUSHALL SYNC: reference=%#v antares=%#v", want, got)
		}
		for _, rdb := range []*redis.Client{ref, ant} {
			if n := rdb.DBSize(ctx).Val(); n != 0 {
				t.Fatalf("DBSIZE after FLUSHALL = %d, want 0", n)
			}
		}
	})
}
