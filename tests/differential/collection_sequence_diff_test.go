package differential

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestDiffCollectionSequences(t *testing.T) {
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

	prefix := fmt.Sprintf("antares:seq:%d:", time.Now().UnixNano())
	keys := []string{prefix + "list", prefix + "hash", prefix + "set"}
	t.Cleanup(func() {
		if err := ref.Del(ctx, keys...).Err(); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	values := []string{"a", "b", "c", "1", "2", "", "x\x00\r\n"}
	pick := func(rng *rand.Rand) string { return values[rng.IntN(len(values))] }
	rng := rand.New(rand.NewPCG(20261020, 4))
	const steps = 240
	for i := 0; i < steps; i++ {
		var args []any
		unordered := false
		switch family := rng.IntN(10); {
		case family < 3:
			key := keys[0]
			switch rng.IntN(8) {
			case 0:
				args = []any{"LPUSH", key, pick(rng), pick(rng)}
			case 1:
				args = []any{"RPUSH", key, pick(rng)}
			case 2:
				if rng.IntN(2) == 0 {
					args = []any{"LPOP", key}
				} else {
					args = []any{"RPOP", key}
				}
			case 3:
				args = []any{"LLEN", key}
			case 4:
				args = []any{"LINDEX", key, fmt.Sprint(rng.IntN(9) - 4)}
			case 5:
				args = []any{"LRANGE", key, fmt.Sprint(rng.IntN(11) - 5), fmt.Sprint(rng.IntN(11) - 5)}
			case 6:
				args = []any{"LSET", key, fmt.Sprint(rng.IntN(9) - 4), pick(rng)}
			default:
				switch rng.IntN(2) {
				case 0:
					args = []any{"LREM", key, fmt.Sprint(rng.IntN(5) - 2), pick(rng)}
				default:
					args = []any{"LTRIM", key, fmt.Sprint(rng.IntN(7) - 3), fmt.Sprint(rng.IntN(7) - 3)}
				}
			}
		case family < 6:
			key := keys[1]
			switch rng.IntN(9) {
			case 0:
				args = []any{"HSET", key, pick(rng), pick(rng), pick(rng), pick(rng)}
			case 1:
				args = []any{"HGET", key, pick(rng)}
			case 2:
				args = []any{"HMGET", key, pick(rng), pick(rng)}
			case 3:
				args = []any{"HDEL", key, pick(rng), pick(rng)}
			case 4:
				args = []any{"HLEN", key}
			case 5:
				args = []any{"HGETALL", key}
			case 6:
				args = []any{"HKEYS", key}
			case 7:
				args = []any{"HVALS", key}
			default:
				args = []any{"HEXISTS", key, pick(rng)}
			}
		case family < 9:
			key := keys[2]
			switch rng.IntN(6) {
			case 0:
				args = []any{"SADD", key, pick(rng), pick(rng)}
			case 1:
				args = []any{"SREM", key, pick(rng), pick(rng)}
			case 2:
				args = []any{"SISMEMBER", key, pick(rng)}
			case 3:
				args = []any{"SCARD", key}
			case 4:
				args = []any{"SMEMBERS", key}
				unordered = true
			default:
				args = []any{"SADD", key, pick(rng)}
			}
		default:
			key := keys[rng.IntN(len(keys))]
			if rng.IntN(2) == 0 {
				args = []any{"DEL", key}
			} else {
				args = []any{"TYPE", key}
			}
		}

		var want, got any
		if unordered {
			want = setOutcome(ctx, ref, args...)
			got = setOutcome(ctx, ant, args...)
		} else {
			want = outcome(ctx, ref, args...)
			got = outcome(ctx, ant, args...)
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("step %d args %v\nreference: %#v\nantares:   %#v", i, args, want, got)
		}
	}
}
