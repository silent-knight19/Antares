package differential

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDiffGenerated is the P9.1 seeded generator: it drives a deterministic
// pseudo-random stream of commands from every implemented family against the
// reference and Antares, comparing canonicalized replies at each step. A bug
// reproduces exactly with the printed seed.
//
// DIFF_SEED and DIFF_ITERS override the defaults (1 and 500).
func TestDiffGenerated(t *testing.T) {
	refAddr := os.Getenv("REDIS_REF_ADDR")
	if refAddr == "" {
		t.Skip("REDIS_REF_ADDR not set; run make diff-test")
	}
	seed := envUint(t, "DIFF_SEED", 1)
	iters := envInt(t, "DIFF_ITERS", 500)
	ctx := context.Background()
	ref := newClient(t, refAddr)
	ant := newClient(t, startAntares(t))
	if err := ref.Ping(ctx).Err(); err != nil {
		t.Fatalf("reference Redis at %s is unreachable: %v", refAddr, err)
	}

	prefix := fmt.Sprintf("antares:gen:%d:%d:", seed, time.Now().UnixNano())
	t.Cleanup(func() {
		keys, err := ref.Keys(ctx, prefix+"*").Result()
		if err != nil || len(keys) == 0 {
			return
		}
		_ = ref.Del(ctx, keys...).Err()
	})

	g := &generator{
		rng:     rand.New(rand.NewPCG(seed, 0x9e3779b97f4a7c15)),
		pattern: prefix + "*",
		keys: []string{
			prefix + "s", prefix + "l", prefix + "h", prefix + "set",
			prefix + "ttl", prefix + "x", prefix + "bin\x00\r\nkey",
			prefix + strings.Repeat("long", 64), prefix + "",
		},
		vals: []string{
			"", "v", "0", "1", "-1", "42", "abc", "1.5", "-0.25", "1e3",
			"9223372036854775807", "-9223372036854775808",
			"a\x00\r\nb", strings.Repeat("x", 1024),
		},
		arith: []string{
			"0", "1", "-1", "10", "-10", "9223372036854775807",
			"-9223372036854775808", "abc", "+1", "01", "1.5", "-0.25",
			"inf", "nan",
		},
		floats: []string{
			"0.1", "-0.1", "1.5", "1e3", "1e-3", "inf", "nan", "abc", "1e4932",
		},
		fields:  []string{"f0", "f1", "f2", "f3", "f4", "", "f\x00\r\n"},
		members: []string{"m0", "m1", "m2", "m3", "m4", "", "m\x00\r\n"},
	}

	for i := 0; i < iters; i++ {
		step := g.next()
		want := outcome(ctx, ref, step.args...)
		got := outcome(ctx, ant, step.args...)
		if step.canon != nil {
			want = step.canon(want)
			got = step.canon(got)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("seed=%d step=%d args=%v\nreference: %#v\nantares:   %#v\nrerun: DIFF_SEED=%d DIFF_ITERS=%d",
				seed, i, step.args, want, got, seed, i+1)
		}
	}
	t.Logf("generated %d steps with seed %d", iters, seed)
}

type genStep struct {
	args  []any
	canon func(any) any
}

type generator struct {
	rng     *rand.Rand
	pattern string
	keys    []string
	vals    []string
	arith   []string
	floats  []string
	fields  []string
	members []string
}

func (g *generator) pick(xs []string) string { return xs[g.rng.IntN(len(xs))] }
func (g *generator) key() string             { return g.pick(g.keys) }
func (g *generator) value() string           { return g.pick(g.vals) }

func (g *generator) keysN(n int) []any {
	out := make([]any, 0, n*2)
	for range n {
		out = append(out, g.key())
	}
	return out
}

func (g *generator) next() genStep {
	switch n := g.rng.IntN(100); {
	case n < 32:
		return g.stringStep()
	case n < 52:
		return g.listStep()
	case n < 70:
		return g.hashStep()
	case n < 85:
		return g.setStep()
	default:
		return g.keyspaceStep()
	}
}

func (g *generator) stringStep() genStep {
	switch n := g.rng.IntN(100); {
	case n < 20:
		args := []any{"SET", g.key(), g.value()}
		switch g.rng.IntN(9) {
		case 0:
			args = append(args, "NX")
		case 1:
			args = append(args, "XX")
		case 2:
			args = append(args, "GET")
		case 3:
			args = append(args, "NX", "GET")
		case 4:
			args = append(args, "XX", "GET")
		case 5:
			args = append(args, "EX", strconv.Itoa(100+g.rng.IntN(100000)))
		case 6:
			args = append(args, "PX", strconv.Itoa(100000+g.rng.IntN(10000000)))
		}
		return genStep{args: args}
	case n < 30:
		return genStep{args: []any{"GET", g.key()}}
	case n < 35:
		return genStep{args: []any{"GETSET", g.key(), g.value()}}
	case n < 38:
		return genStep{args: []any{"SETNX", g.key(), g.value()}}
	case n < 43:
		return genStep{args: []any{"APPEND", g.key(), g.value()}}
	case n < 47:
		return genStep{args: []any{"STRLEN", g.key()}}
	case n < 62:
		switch g.rng.IntN(10) {
		case 0:
			return genStep{args: []any{"INCR", g.key()}}
		case 1:
			return genStep{args: []any{"DECR", g.key()}}
		case 2, 3:
			return genStep{args: []any{"INCRBY", g.key(), g.pick(g.arith)}}
		case 4, 5:
			return genStep{args: []any{"DECRBY", g.key(), g.pick(g.arith)}}
		default:
			return genStep{args: []any{"INCRBYFLOAT", g.key(), g.pick(g.floats)}}
		}
	case n < 68:
		args := []any{"MSET"}
		for range 1 + g.rng.IntN(3) {
			args = append(args, g.key(), g.value())
		}
		return genStep{args: args}
	case n < 74:
		return genStep{args: append([]any{"MGET"}, g.keysN(1+g.rng.IntN(3))...)}
	case n < 80:
		return genStep{args: append([]any{"DEL"}, g.keysN(1+g.rng.IntN(3))...)}
	case n < 84:
		return genStep{args: append([]any{"EXISTS"}, g.keysN(1+g.rng.IntN(3))...)}
	case n < 88:
		return genStep{args: []any{"TYPE", g.key()}}
	case n < 91:
		op := "RENAME"
		if g.rng.IntN(2) == 0 {
			op = "RENAMENX"
		}
		return genStep{args: []any{op, g.key(), g.key()}}
	case n < 94:
		args := []any{"PING"}
		if g.rng.IntN(3) == 0 {
			args = append(args, g.value())
		}
		return genStep{args: args}
	case n < 97:
		return genStep{args: []any{"ECHO", g.value()}}
	default:
		return g.ttlStep()
	}
}

func (g *generator) ttlStep() genStep {
	switch g.rng.IntN(5) {
	case 0:
		return genStep{args: []any{"PERSIST", g.key()}}
	case 1, 2:
		return genStep{args: []any{"TTL", g.key()}, canon: ttlCanonical}
	case 3:
		return genStep{args: []any{"PTTL", g.key()}, canon: ttlCanonical}
	default:
		seconds := []string{"0", "-1", "100", "3600", "100000"}
		return genStep{args: []any{"EXPIRE", g.key(), g.pick(seconds)}}
	}
}

func (g *generator) listStep() genStep {
	switch n := g.rng.IntN(100); {
	case n < 30:
		op := "RPUSH"
		if g.rng.IntN(2) == 0 {
			op = "LPUSH"
		}
		args := []any{op, g.key()}
		for range 1 + g.rng.IntN(3) {
			args = append(args, g.value())
		}
		return genStep{args: args}
	case n < 45:
		op := "RPOP"
		if g.rng.IntN(2) == 0 {
			op = "LPOP"
		}
		args := []any{op, g.key()}
		if g.rng.IntN(2) == 0 {
			args = append(args, strconv.Itoa(g.rng.IntN(6)))
		}
		return genStep{args: args}
	case n < 55:
		return genStep{args: []any{"LLEN", g.key()}}
	case n < 65:
		return genStep{args: []any{"LINDEX", g.key(), g.index()}}
	case n < 75:
		return genStep{args: []any{"LSET", g.key(), g.index(), g.value()}}
	case n < 90:
		return genStep{args: []any{"LRANGE", g.key(), g.index(), g.index()}}
	case n < 95:
		count := []string{"0", "1", "-1", "2", "-2", "abc"}
		return genStep{args: []any{"LREM", g.key(), g.pick(count), g.value()}}
	default:
		return genStep{args: []any{"LTRIM", g.key(), g.index(), g.index()}}
	}
}

func (g *generator) index() string {
	return g.pick([]string{"0", "1", "2", "-1", "-2", "abc"})
}

func (g *generator) hashStep() genStep {
	switch n := g.rng.IntN(100); {
	case n < 25:
		args := []any{"HSET", g.key()}
		for range 1 + g.rng.IntN(2) {
			args = append(args, g.pick(g.fields), g.value())
		}
		return genStep{args: args}
	case n < 40:
		return genStep{args: []any{"HGET", g.key(), g.pick(g.fields)}}
	case n < 50:
		return genStep{args: []any{"HMGET", g.key(), g.pick(g.fields), g.pick(g.fields)}}
	case n < 60:
		return genStep{args: []any{"HDEL", g.key(), g.pick(g.fields), g.pick(g.fields)}}
	case n < 70:
		return genStep{args: []any{"HLEN", g.key()}}
	case n < 80:
		return genStep{args: []any{"HEXISTS", g.key(), g.pick(g.fields)}}
	case n < 90:
		return genStep{args: []any{"HGETALL", g.key()}, canon: hashCanonical}
	case n < 95:
		return genStep{args: []any{"HKEYS", g.key()}, canon: unorderedCanonical}
	default:
		return genStep{args: []any{"HVALS", g.key()}, canon: unorderedCanonical}
	}
}

func (g *generator) setStep() genStep {
	switch n := g.rng.IntN(100); {
	case n < 30:
		args := []any{"SADD", g.key()}
		for range 1 + g.rng.IntN(3) {
			args = append(args, g.pick(g.members))
		}
		return genStep{args: args}
	case n < 45:
		return genStep{args: []any{"SREM", g.key(), g.pick(g.members), g.pick(g.members)}}
	case n < 55:
		return genStep{args: []any{"SCARD", g.key()}}
	case n < 70:
		return genStep{args: []any{"SISMEMBER", g.key(), g.pick(g.members)}}
	default:
		return genStep{args: []any{"SMEMBERS", g.key()}, canon: unorderedCanonical}
	}
}

func (g *generator) keyspaceStep() genStep {
	switch g.rng.IntN(10) {
	case 0:
		return genStep{args: append([]any{"DEL"}, g.keysN(1+g.rng.IntN(3))...)}
	case 1:
		return genStep{args: append([]any{"EXISTS"}, g.keysN(1+g.rng.IntN(3))...)}
	case 2:
		return genStep{args: []any{"TYPE", g.key()}}
	case 3:
		op := "RENAME"
		if g.rng.IntN(2) == 0 {
			op = "RENAMENX"
		}
		return genStep{args: []any{op, g.key(), g.key()}}
	case 4:
		return g.ttlStep()
	case 5:
		return genStep{args: []any{"KEYS", g.pattern}, canon: unorderedCanonical}
	case 6:
		args := []any{"PING"}
		if g.rng.IntN(2) == 0 {
			args = append(args, g.value())
		}
		return genStep{args: args}
	case 7:
		return genStep{args: []any{"ECHO", g.value()}}
	case 8:
		return genStep{args: []any{"PEXPIRE", g.key(), g.pick([]string{"0", "100000", "3600000"})}}
	default:
		return genStep{args: []any{"GET", g.key()}}
	}
}

func envUint(t *testing.T, name string, def uint64) uint64 {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		t.Fatalf("%s=%q: %v", name, raw, err)
	}
	return n
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		t.Fatalf("%s=%q: want a positive integer", name, raw)
	}
	return n
}
