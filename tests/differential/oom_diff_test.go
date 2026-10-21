package differential

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestDiffOOM pins the maxmemory noeviction error: both servers are filled past
// a 1 MiB limit, then a memory-growing command must fail with byte-identical
// OOM wording while reads, deletes, and CONFIG still work.
func TestDiffOOM(t *testing.T) {
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

	fill := func(t *testing.T, rdb *redis.Client) {
		t.Helper()
		if err := rdb.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory 0: %v", err)
		}
		if err := rdb.ConfigSet(ctx, "maxmemory-policy", "noeviction").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory-policy: %v", err)
		}
		if err := rdb.FlushAll(ctx).Err(); err != nil {
			t.Fatalf("FLUSHALL: %v", err)
		}
		value := strings.Repeat("x", 4096)
		for i := 0; i < 512; i++ {
			if err := rdb.Set(ctx, fmt.Sprintf("oom:%d", i), value, 0).Err(); err != nil {
				t.Fatalf("fill SET oom:%d: %v", i, err)
			}
		}
		if err := rdb.ConfigSet(ctx, "maxmemory", "1mb").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory 1mb: %v", err)
		}
	}
	fill(t, ref)
	fill(t, ant)

	checkOOM := func(name string, refErr, antErr error) {
		t.Helper()
		if refErr == nil || antErr == nil {
			t.Fatalf("%s: reference err=%v, antares err=%v; both should be OOM", name, refErr, antErr)
		}
		if refErr.Error() != antErr.Error() {
			t.Fatalf("%s: reference=%q antares=%q", name, refErr.Error(), antErr.Error())
		}
		if !strings.HasPrefix(refErr.Error(), "OOM ") {
			t.Fatalf("%s: reference error = %q, want OOM", name, refErr.Error())
		}
	}

	checkOOM("SET", ref.Set(ctx, "oom:new", "v", 0).Err(), ant.Set(ctx, "oom:new", "v", 0).Err())
	checkOOM("MSET", ref.MSet(ctx, "a", "1", "b", "2").Err(), ant.MSet(ctx, "a", "1", "b", "2").Err())
	checkOOM("SADD", ref.SAdd(ctx, "oom:set", "m").Err(), ant.SAdd(ctx, "oom:set", "m").Err())
	checkOOM("APPEND", ref.Append(ctx, "oom:0", "x").Err(), ant.Append(ctx, "oom:0", "x").Err())

	if err := ref.Get(ctx, "oom:0").Err(); err != nil {
		t.Fatalf("reference GET over limit: %v", err)
	}
	if err := ant.Get(ctx, "oom:0").Err(); err != nil {
		t.Fatalf("antares GET over limit: %v", err)
	}
	if err := ref.Del(ctx, "oom:0").Err(); err != nil {
		t.Fatalf("reference DEL over limit: %v", err)
	}
	if err := ant.Del(ctx, "oom:0").Err(); err != nil {
		t.Fatalf("antares DEL over limit: %v", err)
	}
	if err := ref.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
		t.Fatalf("reference CONFIG SET over limit: %v", err)
	}
	if err := ant.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
		t.Fatalf("antares CONFIG SET over limit: %v", err)
	}
}
