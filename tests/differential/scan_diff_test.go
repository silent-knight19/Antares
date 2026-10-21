package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestDiffScan(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:scan:%d:", time.Now().UnixNano())
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

	setup := [][]any{
		{"RPUSH", k("list:one"), "a", "b"},
		{"RPUSH", k("list:two"), "c"},
		{"HSET", k("hash:one"), "f", "v"},
		{"HSET", k("hash:two"), "g", "w"},
		{"SADD", k("set:one"), "m"},
	}
	for i := range 60 {
		setup = append(setup, []any{"SET", k(fmt.Sprintf("s:%02d", i)), "v"})
	}
	for _, rdb := range []*redis.Client{ref, ant} {
		for _, args := range setup {
			if err := rdb.Do(ctx, args...).Err(); err != nil {
				t.Fatalf("setup %v: %v", args, err)
			}
		}
	}

	for _, tc := range []struct {
		name  string
		count int64
		match string
		typ   string
	}{
		{"default count", 10, prefix + "*", ""},
		{"count one", 1, prefix + "*", ""},
		{"count five", 5, prefix + "*", ""},
		{"count large", 1000, prefix + "*", ""},
		{"match question", 7, prefix + "s:1?", ""},
		{"match prefix", 1, prefix + "s:0*", ""},
		{"type string", 3, prefix + "*", "string"},
		{"type list", 2, prefix + "*", "list"},
		{"type hash", 2, prefix + "*", "hash"},
		{"type set", 2, prefix + "*", "set"},
		{"unknown type", 10, prefix + "*", "nope"},
		{"match and type", 10, prefix + "s:*", "list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := diffScanKeys(t, ctx, ref, prefix, tc.count, tc.match, tc.typ)
			got := diffScanKeys(t, ctx, ant, prefix, tc.count, tc.match, tc.typ)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("count=%d match=%q type=%q\nreference: %v\nantares:   %v", tc.count, tc.match, tc.typ, want, got)
			}
		})
	}

	for _, tc := range []struct {
		name string
		args []any
	}{
		{"missing cursor", []any{"SCAN"}},
		{"invalid cursor", []any{"SCAN", "abc"}},
		{"negative cursor", []any{"SCAN", "-1"}},
		{"overflow cursor", []any{"SCAN", "18446744073709551616"}},
		{"count missing", []any{"SCAN", "0", "COUNT"}},
		{"count zero", []any{"SCAN", "0", "COUNT", "0"}},
		{"count negative", []any{"SCAN", "0", "COUNT", "-1"}},
		{"count not integer", []any{"SCAN", "0", "COUNT", "abc"}},
		{"count leading zero", []any{"SCAN", "0", "COUNT", "01"}},
		{"match missing", []any{"SCAN", "0", "MATCH"}},
		{"type missing", []any{"SCAN", "0", "TYPE"}},
		{"unknown option", []any{"SCAN", "0", "FOO"}},
		{"option after extra", []any{"SCAN", "0", "COUNT", "1", "EXTRA"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := outcome(ctx, ref, tc.args...)
			got := outcome(ctx, ant, tc.args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("reference=%#v antares=%#v", want, got)
			}
		})
	}

	t.Run("unknown type", func(t *testing.T) {
		for _, rdb := range []*redis.Client{ref, ant} {
			v, err := rdb.Do(ctx, "SCAN", "0", "TYPE", "nope").Result()
			if err != nil {
				t.Fatalf("SCAN TYPE nope: %v", err)
			}
			reply, ok := v.([]any)
			if !ok || len(reply) != 2 {
				t.Fatalf("SCAN TYPE nope reply = %#v", v)
			}
			if keys, ok := reply[1].([]any); !ok || len(keys) != 0 {
				t.Fatalf("SCAN TYPE nope keys = %#v", reply[1])
			}
		}
	})

	for _, cursor := range []string{"00", "+0", " 0", "-0", "-01", "18446744073709551615"} {
		t.Run("accepted cursor "+cursor, func(t *testing.T) {
			for _, rdb := range []*redis.Client{ref, ant} {
				if _, err := rdb.Do(ctx, "SCAN", cursor, "COUNT", "1000").Result(); err != nil {
					t.Fatalf("SCAN %q: %v", cursor, err)
				}
			}
		})
	}
}

func diffScanKeys(t *testing.T, ctx context.Context, rdb *redis.Client, prefix string, count int64, match, typ string) []string {
	t.Helper()
	seen := make(map[string]bool)
	cursor := uint64(0)
	for steps := 0; steps < 10000; steps++ {
		var (
			keys []string
			next uint64
			err  error
		)
		if typ != "" {
			keys, next, err = rdb.ScanType(ctx, cursor, match, count, typ).Result()
		} else {
			keys, next, err = rdb.Scan(ctx, cursor, match, count).Result()
		}
		if err != nil {
			t.Fatalf("SCAN cursor=%d count=%d match=%q type=%q: %v", cursor, count, match, typ, err)
		}
		for _, key := range keys {
			if strings.HasPrefix(key, prefix) {
				seen[key] = true
			}
		}
		cursor = next
		if cursor == 0 {
			out := make([]string, 0, len(seen))
			for key := range seen {
				out = append(out, key)
			}
			sort.Strings(out)
			return out
		}
	}
	t.Fatal("SCAN did not terminate")
	return nil
}
