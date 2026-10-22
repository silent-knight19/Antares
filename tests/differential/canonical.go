package differential

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
)

// Canonicalization rules for the differential harness. The reference and
// Antares must agree exactly on values, errors, and ordering; only replies
// whose order Redis itself does not promise, or whose duration must tick
// between the two calls, are normalized here.

// outcome runs one command and reduces the reply to a comparable value.
// Errors are compared as strings, so wording differences fail the diff.
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

// canonical maps go-redis reply types onto the small set the harness compares.
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

// setOutcome sorts a reply whose order Redis does not guarantee (SMEMBERS,
// KEYS, HKEYS, and friends). It assumes the array elements are scalars.
func setOutcome(ctx context.Context, rdb *redis.Client, args ...any) any {
	return unorderedCanonical(outcome(ctx, rdb, args...))
}

func unorderedCanonical(v any) any {
	arr, ok := v.([]any)
	if !ok {
		return v
	}
	sorted := append([]any(nil), arr...)
	sort.Slice(sorted, func(i, j int) bool {
		return fmt.Sprint(sorted[i]) < fmt.Sprint(sorted[j])
	})
	return sorted
}

// hashOutcome sorts the flat field/value pairs of HGETALL by field so hash
// table encoding cannot influence the comparison.
func hashOutcome(ctx context.Context, rdb *redis.Client, args ...any) any {
	return hashCanonical(outcome(ctx, rdb, args...))
}

func hashCanonical(v any) any {
	arr, ok := v.([]any)
	if !ok || len(arr)%2 != 0 {
		return v
	}
	pairs := make([][2]any, len(arr)/2)
	for i := range pairs {
		pairs[i] = [2]any{arr[2*i], arr[2*i+1]}
	}
	sort.Slice(pairs, func(i, j int) bool {
		return fmt.Sprint(pairs[i][0]) < fmt.Sprint(pairs[j][0])
	})
	out := make([]any, 0, len(arr))
	for _, pair := range pairs {
		out = append(out, pair[0], pair[1])
	}
	return out
}

// ttlOutcome keeps TTL/PTTL's sentinel values (-1 no expiry, -2 missing) exact
// but reduces any live duration to "positive": the two calls happen a few
// milliseconds apart, so exact remaining time cannot match.
func ttlOutcome(ctx context.Context, rdb *redis.Client, args ...any) any {
	return ttlCanonical(outcome(ctx, rdb, args...))
}

func ttlCanonical(v any) any {
	if n, ok := v.(int64); ok && n > 0 {
		return int64(1)
	}
	return v
}
