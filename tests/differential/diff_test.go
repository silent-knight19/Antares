package differential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestDiffStrings is the P2.6 differential harness v0: the same command
// sequence runs against the pinned reference Redis and an in-process Antares,
// and every reply is compared after canonicalization. It is skipped unless
// REDIS_REF_ADDR is set, which `make diff-test` does.
func TestDiffStrings(t *testing.T) {
	refAddr := os.Getenv("REDIS_REF_ADDR")
	if refAddr == "" {
		t.Skip("REDIS_REF_ADDR not set; run make diff-test")
	}
	ctx := context.Background()
	ref := newClient(t, refAddr)
	ant := newClient(t, startAntares(t))
	if err := ref.Ping(ctx).Err(); err != nil {
		t.Fatalf("reference Redis at %s is unreachable: %v (start it with docker compose up -d)", refAddr, err)
	}

	prefix := fmt.Sprintf("antares:diff:%d:", time.Now().UnixNano())
	k := func(s string) string { return prefix + s }
	t.Cleanup(func() {
		keys, err := ref.Keys(ctx, prefix+"*").Result()
		if err != nil {
			t.Logf("cleanup scan: %v", err)
			return
		}
		if len(keys) > 0 {
			if err := ref.Del(ctx, keys...).Err(); err != nil {
				t.Logf("cleanup delete: %v", err)
			}
		}
	})

	big := strings.Repeat("x", 1<<20)
	farSec := strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10)
	farMs := strconv.FormatInt(time.Now().Add(24*time.Hour).UnixMilli(), 10)
	wideKeys := make([]string, 1000)
	msetWide := []any{"MSET"}
	mgetWide := []any{"MGET"}
	existsWide := []any{"EXISTS"}
	delWide := []any{"DEL"}
	for i := range wideKeys {
		wideKeys[i] = k(fmt.Sprintf("w:%d", i))
		msetWide = append(msetWide, wideKeys[i], fmt.Sprintf("v%d", i))
		mgetWide = append(mgetWide, wideKeys[i])
		existsWide = append(existsWide, wideKeys[i])
		delWide = append(delWide, wideKeys[i])
	}

	corpus := []struct {
		name string
		args []any
	}{
		{"set", []any{"SET", k("s"), "v"}},
		{"get", []any{"GET", k("s")}},
		{"type", []any{"TYPE", k("s")}},
		{"exists duplicates", []any{"EXISTS", k("s"), k("s"), k("missing")}},
		{"strlen", []any{"STRLEN", k("s")}},
		{"set nx fails", []any{"SET", k("s"), "v2", "NX"}},
		{"get after nx", []any{"GET", k("s")}},
		{"set xx", []any{"SET", k("s"), "v3", "XX"}},
		{"set xx missing", []any{"SET", k("missing"), "v", "XX"}},
		{"set nx missing", []any{"SET", k("missing"), "v", "NX"}},
		{"set get", []any{"SET", k("s"), "new", "GET"}},
		{"get after set get", []any{"GET", k("s")}},
		{"set empty value", []any{"SET", k("empty"), ""}},
		{"get empty value", []any{"GET", k("empty")}},
		{"set binary", []any{"SET", k("bin"), "a\x00\r\nb"}},
		{"get binary", []any{"GET", k("bin")}},
		{"mset", []any{"MSET", k("a"), "1", k("b"), "2"}},
		{"mset duplicate", []any{"MSET", k("a"), "3", k("a"), "4"}},
		{"get duplicate", []any{"GET", k("a")}},
		{"mget", []any{"MGET", k("a"), k("b"), k("nope")}},
		{"mset odd args", []any{"MSET", k("x"), "1", k("y")}},
		{"mget arity", []any{"MGET"}},
		{"append", []any{"APPEND", k("ap"), "foo"}},
		{"append more", []any{"APPEND", k("ap"), "bar"}},
		{"get appended", []any{"GET", k("ap")}},
		{"strlen appended", []any{"STRLEN", k("ap")}},
		{"append empty to missing", []any{"APPEND", k("emptyapp"), ""}},
		{"exists empty append", []any{"EXISTS", k("emptyapp")}},
		{"get empty append", []any{"GET", k("emptyapp")}},
		{"getset", []any{"GETSET", k("ap"), "new"}},
		{"getset missing", []any{"GETSET", k("nosuch"), "v"}},
		{"setnx existing", []any{"SETNX", k("ap"), "z"}},
		{"setnx missing", []any{"SETNX", k("nx"), "v"}},
		{"incr", []any{"INCR", k("n")}},
		{"incr again", []any{"INCR", k("n")}},
		{"decr", []any{"DECR", k("n")}},
		{"incrby", []any{"INCRBY", k("n"), "10"}},
		{"decrby", []any{"DECRBY", k("n"), "3"}},
		{"get incremented", []any{"GET", k("n")}},
		{"incrby not integer", []any{"INCRBY", k("n"), "abc"}},
		{"incrby plus sign", []any{"INCRBY", k("n"), "+1"}},
		{"incrby leading zero", []any{"INCRBY", k("n"), "01"}},
		{"incrby unchanged", []any{"GET", k("n")}},
		{"set int max", []any{"SET", k("imax"), "9223372036854775807"}},
		{"incr overflow", []any{"INCR", k("imax")}},
		{"decr from max", []any{"DECR", k("imax")}},
		{"set int min", []any{"SET", k("imin"), "-9223372036854775808"}},
		{"decr underflow", []any{"DECR", k("imin")}},
		{"set non integer", []any{"SET", k("nonint"), "abc"}},
		{"incr non integer", []any{"INCR", k("nonint")}},
		{"decrby min", []any{"DECRBY", k("n"), "-9223372036854775808"}},
		{"incrby min", []any{"INCRBY", k("freshmin"), "-9223372036854775808"}},
		{"incrbyfloat", []any{"INCRBYFLOAT", k("f"), "1.5"}},
		{"incrbyfloat more", []any{"INCRBYFLOAT", k("f"), "0.1"}},
		{"get float", []any{"GET", k("f")}},
		{"float tiny", []any{"INCRBYFLOAT", k("tiny"), "1e-18"}},
		{"float 1e-17", []any{"INCRBYFLOAT", k("tiny2"), "1e-17"}},
		{"float hex", []any{"INCRBYFLOAT", k("hex"), "0x1.8"}},
		{"float huge", []any{"INCRBYFLOAT", k("huge"), "1e4932"}},
		{"float nan", []any{"INCRBYFLOAT", k("nan"), "nan"}},
		{"float too large", []any{"INCRBYFLOAT", k("toobig"), "1e4933"}},
		{"float too small", []any{"INCRBYFLOAT", k("toosmall"), "0x1p-20000"}},
		{"float invalid", []any{"INCRBYFLOAT", k("bad"), "abc"}},
		{"float infinity", []any{"INCRBYFLOAT", k("inf"), "inf"}},
		{"set stored invalid float", []any{"SET", k("storedbad"), "abc"}},
		{"incrbyfloat stored invalid", []any{"INCRBYFLOAT", k("storedbad"), "1"}},
		{"set negative zero float", []any{"SET", k("nz"), "-1e-18"}},
		{"negative zero result", []any{"INCRBYFLOAT", k("nz"), "0"}},
		{"set float max", []any{"SET", k("fmax"), "1.189731495357231765e4932"}},
		{"float overflow", []any{"INCRBYFLOAT", k("fmax"), "1.189731495357231765e4932"}},
		{"del duplicates", []any{"DEL", k("s"), k("s"), k("missing")}},
		{"deleted exists", []any{"EXISTS", k("s")}},
		{"set syntax", []any{"SET", k("x"), "v", "NX", "XX"}},
		{"set expiry zero", []any{"SET", k("x"), "v", "EX", "0"}},
		{"set unknown option", []any{"SET", k("x"), "v", "FOO"}},
		{"unknown command", []any{"NOSUCHCMD"}},
		{"get arity", []any{"GET"}},
		{"empty key del", []any{"DEL", ""}},
		{"empty key set", []any{"SET", "", ""}},
		{"empty key get", []any{"GET", ""}},
		{"empty key type", []any{"TYPE", ""}},
		{"empty key strlen", []any{"STRLEN", ""}},
		{"empty key del again", []any{"DEL", ""}},
		{"big set", []any{"SET", k("big"), big}},
		{"big get", []any{"GET", k("big")}},
		{"big strlen", []any{"STRLEN", k("big")}},
		{"big append", []any{"APPEND", k("big"), big}},
		{"big strlen after append", []any{"STRLEN", k("big")}},
		{"wide mset", msetWide},
		{"wide mget", mgetWide},
		{"wide exists", existsWide},
		{"wide del", delWide},
		{"wide exists after del", existsWide},
		{"ttl missing", []any{"TTL", k("ttl:missing")}},
		{"pttl missing", []any{"PTTL", k("ttl:missing")}},
		{"persist missing", []any{"PERSIST", k("ttl:missing")}},
		{"set ttl key", []any{"SET", k("ttl:k"), "v"}},
		{"ttl no expire", []any{"TTL", k("ttl:k")}},
		{"pttl no expire", []any{"PTTL", k("ttl:k")}},
		{"persist no expire", []any{"PERSIST", k("ttl:k")}},
		{"expire", []any{"EXPIRE", k("ttl:k"), "100"}},
		{"ttl after expire", []any{"TTL", k("ttl:k")}},
		{"persist", []any{"PERSIST", k("ttl:k")}},
		{"ttl after persist", []any{"TTL", k("ttl:k")}},
		{"persist again", []any{"PERSIST", k("ttl:k")}},
		{"set ttl pexpire key", []any{"SET", k("ttl:px"), "v"}},
		{"pexpire", []any{"PEXPIRE", k("ttl:px"), "10000"}},
		{"ttl after pexpire", []any{"TTL", k("ttl:px")}},
		{"set ttl conditions", []any{"SET", k("ttl:cond"), "v", "PX", "10000"}},
		{"ttl before gt", []any{"TTL", k("ttl:cond")}},
		{"gt rejects earlier", []any{"EXPIRE", k("ttl:cond"), "5", "GT"}},
		{"ttl unchanged by gt", []any{"TTL", k("ttl:cond")}},
		{"gt accepts later", []any{"EXPIRE", k("ttl:cond"), "20", "GT"}},
		{"lt rejects later", []any{"EXPIRE", k("ttl:cond"), "30", "LT"}},
		{"lt accepts earlier", []any{"EXPIRE", k("ttl:cond"), "10", "LT"}},
		{"nx rejects volatile", []any{"EXPIRE", k("ttl:cond"), "20", "NX"}},
		{"xx accepts volatile", []any{"EXPIRE", k("ttl:cond"), "20", "XX"}},
		{"xx gt accepts later", []any{"EXPIRE", k("ttl:cond"), "30", "XX", "GT"}},
		{"xx gt rejects earlier", []any{"EXPIRE", k("ttl:cond"), "10", "XX", "GT", "GT"}},
		{"xx lt accepts earlier", []any{"EXPIRE", k("ttl:cond"), "5", "XX", "LT"}},
		{"ttl after xx lt", []any{"TTL", k("ttl:cond")}},
		{"nx on no expiry", []any{"SET", k("ttl:nx"), "v"}},
		{"nx sets", []any{"EXPIRE", k("ttl:nx"), "10", "NX"}},
		{"nx rejects second", []any{"EXPIRE", k("ttl:nx"), "20", "NX", "NX"}},
		{"ttl after nx", []any{"TTL", k("ttl:nx")}},
		{"xx on no expiry", []any{"SET", k("ttl:xx"), "v"}},
		{"xx rejects no expiry", []any{"EXPIRE", k("ttl:xx"), "10", "XX"}},
		{"ttl after xx reject", []any{"TTL", k("ttl:xx")}},
		{"gt on no expiry", []any{"SET", k("ttl:gt"), "v"}},
		{"gt rejects no expiry", []any{"EXPIRE", k("ttl:gt"), "10", "GT"}},
		{"lt on no expiry", []any{"EXPIRE", k("ttl:gt"), "10", "LT"}},
		{"ttl after lt", []any{"TTL", k("ttl:gt")}},
		{"past gt keeps key", []any{"SET", k("ttl:pastgt"), "v"}},
		{"past gt rejects", []any{"EXPIRE", k("ttl:pastgt"), "-5", "GT"}},
		{"past gt exists", []any{"EXISTS", k("ttl:pastgt")}},
		{"past lt deletes key", []any{"SET", k("ttl:pastlt"), "v"}},
		{"past lt accepts", []any{"EXPIRE", k("ttl:pastlt"), "-5", "LT"}},
		{"past lt exists", []any{"EXISTS", k("ttl:pastlt")}},
		{"nx past deletes key", []any{"SET", k("ttl:nxpast"), "v"}},
		{"nx past accepts", []any{"EXPIRE", k("ttl:nxpast"), "-5", "NX"}},
		{"nx past exists", []any{"EXISTS", k("ttl:nxpast")}},
		{"xx past deletes key", []any{"SET", k("ttl:xxpast"), "v"}},
		{"xx past sets ttl", []any{"EXPIRE", k("ttl:xxpast"), "10"}},
		{"xx past accepts", []any{"EXPIRE", k("ttl:xxpast"), "-5", "XX"}},
		{"xx past exists", []any{"EXISTS", k("ttl:xxpast")}},
		{"xx gt past keeps key", []any{"SET", k("ttl:xxgtpast"), "v"}},
		{"xx gt past sets ttl", []any{"EXPIRE", k("ttl:xxgtpast"), "10"}},
		{"xx gt past rejects", []any{"EXPIRE", k("ttl:xxgtpast"), "-5", "XX", "GT"}},
		{"xx gt past exists", []any{"EXISTS", k("ttl:xxgtpast")}},
		{"xx lt past deletes key", []any{"SET", k("ttl:xxltpast"), "v"}},
		{"xx lt past sets ttl", []any{"EXPIRE", k("ttl:xxltpast"), "10"}},
		{"xx lt past accepts", []any{"EXPIRE", k("ttl:xxltpast"), "-5", "XX", "LT"}},
		{"xx lt past exists", []any{"EXISTS", k("ttl:xxltpast")}},
		{"nx zero deletes key", []any{"SET", k("ttl:nx0"), "v"}},
		{"nx zero accepts", []any{"EXPIRE", k("ttl:nx0"), "0", "NX"}},
		{"nx zero exists", []any{"EXISTS", k("ttl:nx0")}},
		{"xx zero after persist", []any{"SET", k("ttl:xx0"), "v"}},
		{"xx zero sets ttl", []any{"EXPIRE", k("ttl:xx0"), "10"}},
		{"xx zero persists", []any{"PERSIST", k("ttl:xx0")}},
		{"xx zero rejects", []any{"EXPIRE", k("ttl:xx0"), "0", "XX"}},
		{"xx zero exists", []any{"EXISTS", k("ttl:xx0")}},
		{"expireat", []any{"SET", k("ttl:at"), "v"}},
		{"expireat accepts", []any{"EXPIREAT", k("ttl:at"), farSec}},
		{"persist after expireat", []any{"PERSIST", k("ttl:at")}},
		{"ttl after expireat persist", []any{"TTL", k("ttl:at")}},
		{"pexpireat", []any{"SET", k("ttl:atms"), "v"}},
		{"pexpireat accepts", []any{"PEXPIREAT", k("ttl:atms"), farMs}},
		{"persist after pexpireat", []any{"PERSIST", k("ttl:atms")}},
		{"pexpireat zero deletes", []any{"SET", k("ttl:at0"), "v"}},
		{"pexpireat zero reply", []any{"PEXPIREAT", k("ttl:at0"), "0"}},
		{"pexpireat zero exists", []any{"EXISTS", k("ttl:at0")}},
		{"expire zero deletes", []any{"SET", k("ttl:zero"), "v"}},
		{"expire zero reply", []any{"EXPIRE", k("ttl:zero"), "0"}},
		{"expire zero exists", []any{"EXISTS", k("ttl:zero")}},
		{"expire overflow", []any{"SET", k("ttl:max"), "v"}},
		{"expire overflow seconds", []any{"EXPIRE", k("ttl:max"), "9223372036854775807"}},
		{"pexpire overflow ms", []any{"PEXPIRE", k("ttl:max"), "9223372036854775807"}},
		{"expireat overflow seconds", []any{"EXPIREAT", k("ttl:max"), "9223372036854775807"}},
		{"expireat underflow seconds", []any{"EXPIREAT", k("ttl:max"), "-9223372036854775808"}},
		{"ttl after overflow", []any{"TTL", k("ttl:max")}},
		{"pexpire min deletes", []any{"SET", k("ttl:minpex"), "v"}},
		{"pexpire min reply", []any{"PEXPIRE", k("ttl:minpex"), "-9223372036854775808"}},
		{"pexpire min exists", []any{"EXISTS", k("ttl:minpex")}},
		{"pexpireat min deletes", []any{"SET", k("ttl:minpexat"), "v"}},
		{"pexpireat min reply", []any{"PEXPIREAT", k("ttl:minpexat"), "-9223372036854775808"}},
		{"pexpireat min exists", []any{"EXISTS", k("ttl:minpexat")}},
		{"ttl not integer", []any{"EXPIRE", k("ttl:missing"), "abc"}},
		{"ttl unsupported option", []any{"EXPIRE", k("ttl:missing"), "10", "FOO"}},
		{"ttl lowercase unsupported", []any{"EXPIRE", k("ttl:missing"), "10", "foo"}},
		{"ttl nx xx conflict", []any{"EXPIRE", k("ttl:missing"), "10", "NX", "XX"}},
		{"ttl nx gt conflict", []any{"EXPIRE", k("ttl:missing"), "10", "NX", "GT"}},
		{"ttl gt lt conflict", []any{"EXPIRE", k("ttl:missing"), "10", "GT", "LT"}},
		{"option beats integer", []any{"EXPIRE", k("ttl:missing"), "abc", "FOO"}},
		{"conflict beats integer", []any{"EXPIRE", k("ttl:missing"), "abc", "NX", "XX"}},
		{"expire missing", []any{"EXPIRE", k("ttl:missing"), "10"}},
		{"set ttl lowercase key", []any{"PEXPIRE", k("ttl:max"), "10000"}},
		{"expire lowercase option", []any{"EXPIRE", k("ttl:max"), "20", "xx"}},
		{"ttl after lowercase option", []any{"TTL", k("ttl:max")}},
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

	t.Run("expiry and ttl interactions", func(t *testing.T) {
		expiring := []string{k("exp"), k("ttl:int"), k("ttl:app"), k("ttl:float"), k("ttl:setnx")}
		persistent := []string{k("ttl:getset"), k("ttl:mset")}
		lazy := []string{
			k("lazy:strlen"), k("lazy:exists"), k("lazy:type"), k("lazy:ttl"),
			k("lazy:pttl"), k("lazy:persist"), k("lazy:expire"), k("lazy:setxx"),
		}
		setup := [][]any{
			{"SET", expiring[0], "v", "PX", "300"},
			{"SET", expiring[1], "1", "PX", "300"},
			{"SET", expiring[2], "x", "PX", "300"},
			{"SET", expiring[3], "1.5", "PX", "300"},
			{"SET", expiring[4], "v", "PX", "300"},
			{"SET", persistent[0], "old", "PX", "300"},
			{"SET", persistent[1], "old", "PX", "300"},
		}
		for _, key := range lazy {
			setup = append(setup, []any{"SET", key, "v", "PX", "300"})
		}
		mutations := [][]any{
			{"INCR", expiring[1]},
			{"APPEND", expiring[2], "y"},
			{"INCRBYFLOAT", expiring[3], "1"},
			{"SETNX", expiring[4], "z"},
			{"GETSET", persistent[0], "new"},
			{"MSET", persistent[1], "new"},
		}
		for _, rdb := range []*redis.Client{ref, ant} {
			for _, args := range append(setup, mutations...) {
				if err := rdb.Do(ctx, args...).Err(); err != nil {
					t.Fatalf("setup %v: %v", args, err)
				}
			}
		}
		time.Sleep(600 * time.Millisecond)
		for _, key := range expiring {
			if got := outcome(ctx, ref, "GET", key); got != nil {
				t.Fatalf("reference GET %s after expiry = %#v; want nil", key, got)
			}
			if got := outcome(ctx, ant, "GET", key); got != nil {
				t.Fatalf("antares GET %s after expiry = %#v; want nil", key, got)
			}
		}
		for _, key := range persistent {
			want := outcome(ctx, ref, "GET", key)
			got := outcome(ctx, ant, "GET", key)
			if !reflect.DeepEqual(want, got) || want != "new" {
				t.Fatalf("GET %s after TTL clear = %#v / %#v; want new", key, want, got)
			}
		}
		for _, args := range [][]any{
			{"TYPE", expiring[0]},
			{"EXISTS", expiring[0], expiring[1]},
			{"DEL", expiring[0], expiring[1]},
			{"MGET", expiring[0], expiring[2], expiring[3]},
			{"STRLEN", lazy[0]},
			{"EXISTS", lazy[1]},
			{"TYPE", lazy[2]},
			{"TTL", lazy[3]},
			{"PTTL", lazy[4]},
			{"PERSIST", lazy[5]},
			{"EXPIRE", lazy[6], "10"},
			{"SET", lazy[7], "x", "XX"},
		} {
			want := outcome(ctx, ref, args...)
			got := outcome(ctx, ant, args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("after expiry %v: reference=%#v antares=%#v", args, want, got)
			}
		}
	})
}

func outcome(ctx context.Context, rdb *redis.Client, args ...any) any {
	v, err := rdb.Do(ctx, args...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return "error: " + err.Error()
	}
	return canonical(v)
}

func canonical(v any) any {
	switch x := v.(type) {
	case nil, string, int64:
		return x
	case []byte:
		return string(x)
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = canonical(x[i])
		}
		return out
	default:
		return fmt.Sprintf("%T(%v)", v, v)
	}
}
