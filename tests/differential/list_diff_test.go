package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestDiffLists(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:list:%d:", time.Now().UnixNano())
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
		{"push left", []any{"LPUSH", k("l"), "a", "b", "c"}},
		{"range all", []any{"LRANGE", k("l"), "0", "-1"}},
		{"type list", []any{"TYPE", k("l")}},
		{"llen", []any{"LLEN", k("l")}},
		{"index head", []any{"LINDEX", k("l"), "0"}},
		{"index tail", []any{"LINDEX", k("l"), "-1"}},
		{"index too negative", []any{"LINDEX", k("l"), "-4"}},
		{"index too large", []any{"LINDEX", k("l"), "3"}},
		{"push right", []any{"RPUSH", k("l"), "d", "e"}},
		{"range middle", []any{"LRANGE", k("l"), "2", "3"}},
		{"range negative", []any{"LRANGE", k("l"), "-2", "-1"}},
		{"range reversed", []any{"LRANGE", k("l"), "3", "1"}},
		{"range extremes", []any{"LRANGE", k("l"), "-9223372036854775808", "9223372036854775807"}},
		{"range huge start", []any{"LRANGE", k("l"), "9223372036854775807", "9223372036854775807"}},
		{"lset head", []any{"LSET", k("l"), "0", "A"}},
		{"lset tail", []any{"LSET", k("l"), "-1", "E"}},
		{"lset index too large", []any{"LSET", k("l"), "5", "X"}},
		{"lset index too negative", []any{"LSET", k("l"), "-6", "X"}},
		{"lset missing", []any{"LSET", k("missing"), "0", "X"}},
		{"lset missing bad index", []any{"LSET", k("missing"), "abc", "X"}},
		{"lset bad index", []any{"LSET", k("l"), "abc", "X"}},
		{"lset plus index", []any{"LSET", k("l"), "+1", "X"}},
		{"lset leading zero", []any{"LSET", k("l"), "01", "X"}},
		{"lindex bad index", []any{"LINDEX", k("l"), "abc"}},
		{"lindex missing bad index", []any{"LINDEX", k("missing"), "abc"}},
		{"lrange bad start", []any{"LRANGE", k("l"), "abc", "1"}},
		{"lrange bad stop", []any{"LRANGE", k("l"), "0", "abc"}},
		{"ltrim missing", []any{"LTRIM", k("missing"), "0", "1"}},
		{"ltrim bad index", []any{"LTRIM", k("l"), "abc", "1"}},
		{"ltrim keep middle", []any{"LTRIM", k("l"), "1", "-2"}},
		{"range after trim", []any{"LRANGE", k("l"), "0", "-1"}},
		{"ltrim keep tail", []any{"LTRIM", k("l"), "-2", "-1"}},
		{"range after tail trim", []any{"LRANGE", k("l"), "0", "-1"}},
		{"ltrim empty", []any{"LTRIM", k("l"), "5", "10"}},
		{"exists after trim", []any{"EXISTS", k("l")}},
		{"lpop missing", []any{"LPOP", k("missing")}},
		{"lpop missing count", []any{"LPOP", k("missing"), "2"}},
		{"rpop missing count", []any{"RPOP", k("missing"), "0"}},
		{"rpush for pop", []any{"RPUSH", k("p"), "a", "b", "c", "d"}},
		{"lpop", []any{"LPOP", k("p")}},
		{"rpop count", []any{"RPOP", k("p"), "2"}},
		{"lpop zero", []any{"LPOP", k("p"), "0"}},
		{"lpop rest", []any{"LPOP", k("p"), "9"}},
		{"exists after pops", []any{"EXISTS", k("p")}},
		{"lpop negative", []any{"LPOP", k("missing"), "-1"}},
		{"lpop plus count", []any{"LPOP", k("missing"), "+1"}},
		{"rpop leading zero", []any{"RPOP", k("missing"), "01"}},
		{"lpop too many args", []any{"LPOP", k("missing"), "1", "2"}},
		{"rpop too many args", []any{"RPOP", k("missing"), "1", "2"}},
		{"push arity", []any{"LPUSH", k("l")}},
		{"lrange arity", []any{"LRANGE", k("l"), "0"}},
		{"llen arity", []any{"LLEN"}},
		{"lrem setup", []any{"RPUSH", k("r"), "a", "b", "a", "c", "a", "b", "a"}},
		{"lrem positive", []any{"LREM", k("r"), "2", "a"}},
		{"range after lrem positive", []any{"LRANGE", k("r"), "0", "-1"}},
		{"lrem negative", []any{"LREM", k("r"), "-2", "a"}},
		{"range after lrem negative", []any{"LRANGE", k("r"), "0", "-1"}},
		{"lrem all", []any{"LREM", k("r"), "0", "b"}},
		{"range after lrem all", []any{"LRANGE", k("r"), "0", "-1"}},
		{"lrem delete", []any{"LREM", k("r"), "0", "c"}},
		{"exists after lrem", []any{"EXISTS", k("r")}},
		{"lrem missing", []any{"LREM", k("missing"), "0", "a"}},
		{"lrem bad count", []any{"LREM", k("missing"), "abc", "a"}},
		{"lrem min count", []any{"LREM", k("missing"), "-9223372036854775808", "a"}},
		{"lrem negative zero", []any{"LREM", k("missing"), "-0", "a"}},
		{"string setup", []any{"SET", k("s"), "v"}},
		{"list op on string", []any{"LPUSH", k("s"), "x"}},
		{"lpop count on string", []any{"LPOP", k("s"), "abc"}},
		{"lindex bad index on string", []any{"LINDEX", k("s"), "abc"}},
		{"lset bad index on string", []any{"LSET", k("s"), "abc", "x"}},
		{"lrem bad count on string", []any{"LREM", k("s"), "abc", "x"}},
		{"ltrim bad index on string", []any{"LTRIM", k("s"), "abc", "1"}},
		{"lrange bad index on string", []any{"LRANGE", k("s"), "abc", "1"}},
		{"list setup for string ops", []any{"RPUSH", k("ls"), "a"}},
		{"get list", []any{"GET", k("ls")}},
		{"mget list", []any{"MGET", k("ls"), k("missing")}},
		{"set get list", []any{"SET", k("ls"), "x", "GET"}},
		{"set nx get list", []any{"SET", k("ls"), "x", "NX", "GET"}},
		{"setnx list", []any{"SETNX", k("ls"), "x"}},
		{"set nx list", []any{"SET", k("ls"), "x", "NX"}},
		{"append list", []any{"APPEND", k("ls"), "x"}},
		{"strlen list", []any{"STRLEN", k("ls")}},
		{"incr list", []any{"INCR", k("ls")}},
		{"getset list", []any{"GETSET", k("ls"), "x"}},
		{"set xx overwrite list", []any{"SET", k("ls"), "x", "XX"}},
		{"type after overwrite", []any{"TYPE", k("ls")}},
		{"get after overwrite", []any{"GET", k("ls")}},
		{"push string after overwrite", []any{"LPUSH", k("ls"), "x"}},
		{"binary list", []any{"RPUSH", k("bin"), "a\x00\r\nb", ""}},
		{"binary range", []any{"LRANGE", k("bin"), "0", "-1"}},
		{"binary lindex", []any{"LINDEX", k("bin"), "0"}},
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
