package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func setOutcome(ctx context.Context, rdb *redis.Client, args ...any) any {
	v := outcome(ctx, rdb, args...)
	if arr, ok := v.([]any); ok {
		sorted := append([]any(nil), arr...)
		sort.Slice(sorted, func(i, j int) bool {
			return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j])
		})
		return sorted
	}
	return v
}

func TestDiffSets(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:set:%d:", time.Now().UnixNano())
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
		{"sadd", []any{"SADD", k("s"), "a", "b", "c"}},
		{"sadd duplicate", []any{"SADD", k("s"), "b", "d", "d"}},
		{"type set", []any{"TYPE", k("s")}},
		{"smembers", []any{"SMEMBERS", k("s")}},
		{"sismember yes", []any{"SISMEMBER", k("s"), "a"}},
		{"sismember no", []any{"SISMEMBER", k("s"), "z"}},
		{"scard", []any{"SCARD", k("s")}},
		{"srem", []any{"SREM", k("s"), "b", "z", "b"}},
		{"smembers after srem", []any{"SMEMBERS", k("s")}},
		{"sismember after srem", []any{"SISMEMBER", k("s"), "b"}},
		{"scard after srem", []any{"SCARD", k("s")}},
		{"srem remaining", []any{"SREM", k("s"), "a", "c", "d"}},
		{"exists after srem all", []any{"EXISTS", k("s")}},
		{"smembers missing", []any{"SMEMBERS", k("none")}},
		{"sismember missing", []any{"SISMEMBER", k("none"), "a"}},
		{"scard missing", []any{"SCARD", k("none")}},
		{"srem missing", []any{"SREM", k("none"), "a"}},
		{"sadd arity", []any{"SADD", k("s")}},
		{"srem arity", []any{"SREM", k("s")}},
		{"smembers arity", []any{"SMEMBERS", k("s"), "extra"}},
		{"sismember arity", []any{"SISMEMBER", k("s"), "a", "extra"}},
		{"scard arity", []any{"SCARD", k("s"), "extra"}},
		{"spop arity", []any{"SPOP"}},
		{"srandmember arity", []any{"SRANDMEMBER"}},
		{"sadd duplicate only", []any{"SADD", k("dup"), "a", "a"}},
		{"scard duplicate", []any{"SCARD", k("dup")}},
		{"srem duplicate only", []any{"SREM", k("dup"), "a", "a"}},
		{"exists duplicate", []any{"EXISTS", k("dup")}},

		{"spop missing", []any{"SPOP", k("none")}},
		{"spop missing count", []any{"SPOP", k("none"), "2"}},
		{"spop missing zero", []any{"SPOP", k("none"), "0"}},
		{"spop missing negative", []any{"SPOP", k("none"), "-2"}},
		{"spop missing bad count", []any{"SPOP", k("none"), "abc"}},
		{"spop missing min", []any{"SPOP", k("none"), "-9223372036854775808"}},
		{"spop missing plus", []any{"SPOP", k("none"), "+1"}},
		{"spop missing leading zero", []any{"SPOP", k("none"), "01"}},
		{"spop missing extra", []any{"SPOP", k("none"), "1", "2"}},
		{"srand missing", []any{"SRANDMEMBER", k("none")}},
		{"srand missing count", []any{"SRANDMEMBER", k("none"), "2"}},
		{"srand missing zero", []any{"SRANDMEMBER", k("none"), "0"}},
		{"srand missing negative", []any{"SRANDMEMBER", k("none"), "-2"}},
		{"srand missing bad count", []any{"SRANDMEMBER", k("none"), "abc"}},
		{"srand missing min", []any{"SRANDMEMBER", k("none"), "-9223372036854775808"}},
		{"srand missing extra", []any{"SRANDMEMBER", k("none"), "1", "2"}},
		{"srand missing withvalues", []any{"SRANDMEMBER", k("none"), "WITHVALUES"}},

		{"spop one setup", []any{"SADD", k("one"), "only"}},
		{"spop one", []any{"SPOP", k("one")}},
		{"exists after spop one", []any{"EXISTS", k("one")}},
		{"spop all setup", []any{"SADD", k("all"), "a", "b", "c"}},
		{"spop zero existing", []any{"SPOP", k("all"), "0"}},
		{"scard after spop zero", []any{"SCARD", k("all")}},
		{"spop nine", []any{"SPOP", k("all"), "9"}},
		{"exists after spop nine", []any{"EXISTS", k("all")}},
		{"srand zero", []any{"SRANDMEMBER", k("all"), "0"}},
		{"srand all setup", []any{"SADD", k("rand"), "a", "b", "c"}},
		{"srand count all", []any{"SRANDMEMBER", k("rand"), "9"}},
		{"srand count zero", []any{"SRANDMEMBER", k("rand"), "0"}},
		{"scard after srand", []any{"SCARD", k("rand")}},
		{"srand single setup", []any{"SADD", k("rand1"), "b"}},
		{"srand single", []any{"SRANDMEMBER", k("rand1")}},
		{"srand single negative", []any{"SRANDMEMBER", k("rand1"), "-3"}},
		{"srand bad count", []any{"SRANDMEMBER", k("rand1"), "abc"}},
		{"srand min", []any{"SRANDMEMBER", k("rand1"), "-9223372036854775808"}},
		{"srand withvalues", []any{"SRANDMEMBER", k("rand1"), "WITHVALUES"}},
		{"srand extra", []any{"SRANDMEMBER", k("rand1"), "1", "2"}},

		{"string setup", []any{"SET", k("str"), "v"}},
		{"sadd string", []any{"SADD", k("str"), "a"}},
		{"srem string", []any{"SREM", k("str"), "a"}},
		{"smembers string", []any{"SMEMBERS", k("str")}},
		{"sismember string", []any{"SISMEMBER", k("str"), "a"}},
		{"scard string", []any{"SCARD", k("str")}},
		{"spop string", []any{"SPOP", k("str")}},
		{"spop string count", []any{"SPOP", k("str"), "1"}},
		{"spop string zero", []any{"SPOP", k("str"), "0"}},
		{"spop string bad count", []any{"SPOP", k("str"), "abc"}},
		{"srand string", []any{"SRANDMEMBER", k("str")}},
		{"srand string count", []any{"SRANDMEMBER", k("str"), "1"}},
		{"srand string zero", []any{"SRANDMEMBER", k("str"), "0"}},
		{"srand string bad count", []any{"SRANDMEMBER", k("str"), "abc"}},

		{"set setup for string ops", []any{"SADD", k("ss"), "a"}},
		{"get set", []any{"GET", k("ss")}},
		{"mget set", []any{"MGET", k("ss"), k("none")}},
		{"set get set", []any{"SET", k("ss"), "x", "GET"}},
		{"setnx set", []any{"SETNX", k("ss"), "x"}},
		{"incr set", []any{"INCR", k("ss")}},
		{"lpush set", []any{"LPUSH", k("ss"), "x"}},
		{"hget set", []any{"HGET", k("ss"), "f"}},
		{"type set after string ops", []any{"TYPE", k("ss")}},

		{"list setup for set ops", []any{"RPUSH", k("ls"), "a"}},
		{"sadd list", []any{"SADD", k("ls"), "a"}},
		{"srem list", []any{"SREM", k("ls"), "a"}},
		{"smembers list", []any{"SMEMBERS", k("ls")}},
		{"sismember list", []any{"SISMEMBER", k("ls"), "a"}},
		{"scard list", []any{"SCARD", k("ls")}},
		{"spop list", []any{"SPOP", k("ls")}},
		{"srand list", []any{"SRANDMEMBER", k("ls")}},

		{"binary set", []any{"SADD", k("bin"), "a\x00\r\nb", ""}},
		{"binary sismember", []any{"SISMEMBER", k("bin"), "a\x00\r\nb"}},
		{"binary smembers", []any{"SMEMBERS", k("bin")}},
	}

	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			want := setOutcome(ctx, ref, tc.args...)
			got := setOutcome(ctx, ant, tc.args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("args %v\nreference: %#v\nantares:   %#v", tc.args, want, got)
			}
		})
	}
}
