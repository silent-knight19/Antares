package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type matrixCase struct {
	name  string
	setup [][]any
	cmd   []any
}

func keyed(args []any, key string) []any {
	out := make([]any, len(args))
	for i, arg := range args {
		if s, ok := arg.(string); ok && s == "K" {
			out[i] = key
			continue
		}
		out[i] = arg
	}
	return out
}

func TestDiffWrongTypeMatrix(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:matrix:%d:", time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := ref.Keys(ctx, prefix+"*").Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			_ = ref.Del(ctx, keys...).Err()
		}
	})

	listCommands := [][]any{
		{"LPUSH", "K", "x"},
		{"RPUSH", "K", "x"},
		{"LPOP", "K"},
		{"LPOP", "K", "1"},
		{"RPOP", "K"},
		{"RPOP", "K", "1"},
		{"LLEN", "K"},
		{"LINDEX", "K", "0"},
		{"LSET", "K", "0", "x"},
		{"LREM", "K", "0", "a"},
		{"LTRIM", "K", "0", "1"},
		{"LRANGE", "K", "0", "1"},
	}
	hashCommands := [][]any{
		{"HSET", "K", "f", "v"},
		{"HGET", "K", "f"},
		{"HMGET", "K", "f"},
		{"HDEL", "K", "f"},
		{"HLEN", "K"},
		{"HGETALL", "K"},
		{"HKEYS", "K"},
		{"HVALS", "K"},
		{"HEXISTS", "K", "f"},
		{"HRANDFIELD", "K"},
		{"HRANDFIELD", "K", "0"},
		{"HRANDFIELD", "K", "1"},
	}
	setCommands := [][]any{
		{"SADD", "K", "a"},
		{"SREM", "K", "a"},
		{"SMEMBERS", "K"},
		{"SISMEMBER", "K", "a"},
		{"SCARD", "K"},
		{"SPOP", "K"},
		{"SPOP", "K", "0"},
		{"SPOP", "K", "1"},
		{"SRANDMEMBER", "K"},
		{"SRANDMEMBER", "K", "0"},
		{"SRANDMEMBER", "K", "1"},
	}
	stringCommands := [][]any{
		{"GET", "K"},
		{"MGET", "K"},
		{"INCR", "K"},
		{"INCRBY", "K", "1"},
		{"DECR", "K"},
		{"INCRBYFLOAT", "K", "1"},
		{"INCRBYFLOAT", "K", "abc"},
		{"APPEND", "K", "x"},
		{"STRLEN", "K"},
		{"GETSET", "K", "x"},
		{"SETNX", "K", "x"},
		{"SET", "K", "x", "NX"},
		{"SET", "K", "x", "XX"},
		{"SET", "K", "x", "GET"},
		{"SET", "K", "x", "NX", "GET"},
		{"SET", "K", "x", "XX", "GET"},
		{"MSET", "K", "x"},
		{"SET", "K", "x"},
	}

	sources := []struct {
		name  string
		setup []any
	}{
		{"string", []any{"SET", "K", "v"}},
		{"list", []any{"RPUSH", "K", "a", "b"}},
		{"hash", []any{"HSET", "K", "f", "v"}},
		{"set", []any{"SADD", "K", "a", "b"}},
	}

	var cases []matrixCase
	build := func(source string, setup []any, commands [][]any) {
		for i, command := range commands {
			cases = append(cases, matrixCase{
				name:  fmt.Sprintf("%s/%02d", source, i),
				setup: [][]any{setup},
				cmd:   command,
			})
		}
	}
	for _, source := range sources {
		switch source.name {
		case "string":
			build(source.name, source.setup, listCommands)
			build(source.name, source.setup, hashCommands)
			build(source.name, source.setup, setCommands)
		case "list":
			build(source.name, source.setup, hashCommands)
			build(source.name, source.setup, setCommands)
			build(source.name, source.setup, stringCommands)
		case "hash":
			build(source.name, source.setup, listCommands)
			build(source.name, source.setup, setCommands)
			build(source.name, source.setup, stringCommands)
		case "set":
			build(source.name, source.setup, listCommands)
			build(source.name, source.setup, hashCommands)
			build(source.name, source.setup, stringCommands)
		}
	}

	deleteCases := []matrixCase{
		{name: "delete/list-lrem", setup: [][]any{{"RPUSH", "K", "a"}}, cmd: []any{"LREM", "K", "0", "a"}},
		{name: "delete/list-lrem-all", setup: [][]any{{"RPUSH", "K", "a", "a"}}, cmd: []any{"LREM", "K", "0", "a"}},
		{name: "delete/list-ltrim", setup: [][]any{{"RPUSH", "K", "a"}}, cmd: []any{"LTRIM", "K", "5", "10"}},
		{name: "delete/list-lpop", setup: [][]any{{"RPUSH", "K", "a"}}, cmd: []any{"LPOP", "K"}},
		{name: "delete/list-rpop", setup: [][]any{{"RPUSH", "K", "a"}}, cmd: []any{"RPOP", "K"}},
		{name: "delete/list-lpop-count", setup: [][]any{{"RPUSH", "K", "a", "b"}}, cmd: []any{"LPOP", "K", "9"}},
		{name: "delete/list-rpop-count", setup: [][]any{{"RPUSH", "K", "a", "b"}}, cmd: []any{"RPOP", "K", "9"}},
		{name: "delete/list-expiry", setup: [][]any{{"RPUSH", "K", "a"}, {"PEXPIRE", "K", "100000"}}, cmd: []any{"LPOP", "K"}},
		{name: "delete/hash-hdel", setup: [][]any{{"HSET", "K", "f", "v"}}, cmd: []any{"HDEL", "K", "f"}},
		{name: "delete/hash-expiry", setup: [][]any{{"HSET", "K", "f", "v"}, {"PEXPIRE", "K", "100000"}}, cmd: []any{"HDEL", "K", "f"}},
		{name: "delete/set-srem", setup: [][]any{{"SADD", "K", "a"}}, cmd: []any{"SREM", "K", "a"}},
		{name: "delete/set-srem-all", setup: [][]any{{"SADD", "K", "a", "b"}}, cmd: []any{"SREM", "K", "a", "b"}},
		{name: "delete/set-spop", setup: [][]any{{"SADD", "K", "a"}}, cmd: []any{"SPOP", "K"}},
		{name: "delete/set-spop-count", setup: [][]any{{"SADD", "K", "a", "b"}}, cmd: []any{"SPOP", "K", "9"}},
		{name: "delete/set-expiry", setup: [][]any{{"SADD", "K", "a"}, {"PEXPIRE", "K", "100000"}}, cmd: []any{"SREM", "K", "a"}},
	}
	cases = append(cases, deleteCases...)

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := fmt.Sprintf("%s%03d", prefix, i)
			for _, setup := range tc.setup {
				args := keyed(setup, key)
				if len(args) == 0 {
					continue
				}
				for _, rdb := range []*redis.Client{ref, ant} {
					outcome(ctx, rdb, args...)
				}
			}

			args := keyed(tc.cmd, key)
			want := setOutcome(ctx, ref, args...)
			got := setOutcome(ctx, ant, args...)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("args %v\nreference: %#v\nantares:   %#v", args, want, got)
			}
			for _, check := range [][]any{{"TYPE", key}, {"EXISTS", key}, {"TTL", key}} {
				want := outcome(ctx, ref, check...)
				got := outcome(ctx, ant, check...)
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("after %v: reference=%#v antares=%#v", check, want, got)
				}
			}
		})
	}
}
