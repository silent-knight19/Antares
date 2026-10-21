package differential

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestDiffInfo(t *testing.T) {
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

	for _, rdb := range []*redis.Client{ref, ant} {
		if err := rdb.FlushAll(ctx).Err(); err != nil {
			t.Fatalf("FLUSHALL: %v", err)
		}
		if err := rdb.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory: %v", err)
		}
		if err := rdb.ConfigSet(ctx, "maxmemory-policy", "noeviction").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory-policy: %v", err)
		}
		if err := rdb.ConfigSet(ctx, "maxclients", "10000").Err(); err != nil {
			t.Fatalf("CONFIG SET maxclients: %v", err)
		}
		if err := rdb.Set(ctx, "antares:info:key", "value", 0).Err(); err != nil {
			t.Fatalf("SET: %v", err)
		}
		if err := rdb.PExpire(ctx, "antares:info:key", 100000).Err(); err != nil {
			t.Fatalf("PEXPIRE: %v", err)
		}
	}

	refInfo := parseInfoMap(t, ref.Info(ctx).Val())
	antInfo := parseInfoMap(t, ant.Info(ctx).Val())

	for _, name := range []string{
		"redis_version", "redis_mode", "arch_bits", "process_supervised",
		"io_threads_active", "loading", "async_loading", "rdb_bgsave_in_progress",
		"aof_enabled", "aof_rewrite_in_progress", "role", "connected_slaves",
		"cluster_enabled", "maxclients", "maxmemory", "maxmemory_policy", "db0",
	} {
		if refInfo[name] != antInfo[name] {
			t.Errorf("INFO %s: reference=%q antares=%q", name, refInfo[name], antInfo[name])
		}
	}
	for _, name := range []string{
		"used_memory", "used_memory_rss", "total_connections_received",
		"total_commands_processed", "expired_keys", "evicted_keys",
		"keyspace_hits", "keyspace_misses", "total_net_input_bytes",
		"total_net_output_bytes",
	} {
		refValue, refOK := parseUint(refInfo[name])
		antValue, antOK := parseUint(antInfo[name])
		if !refOK || !antOK {
			t.Errorf("INFO %s is not numeric: reference=%q antares=%q", name, refInfo[name], antInfo[name])
			continue
		}
		if refValue == 0 && name == "used_memory" && antValue == 0 {
			t.Errorf("INFO used_memory is zero on both servers")
		}
	}

	refServer := ref.Info(ctx, "server").Val()
	antServer := ant.Info(ctx, "server").Val()
	for _, server := range []string{refServer, antServer} {
		if !strings.Contains(server, "# Server") || strings.Contains(server, "# Clients") {
			t.Fatalf("INFO server returned other sections:\n%s", server)
		}
	}
	if got := ref.Info(ctx, "nosuchsection").Val(); got != "" {
		t.Fatalf("reference INFO nosuchsection = %q", got)
	}
	if got := ant.Info(ctx, "nosuchsection").Val(); got != "" {
		t.Fatalf("antares INFO nosuchsection = %q", got)
	}

	for _, parameter := range []string{
		"hz", "maxmemory", "maxmemory-policy", "maxclients",
		"proto-max-bulk-len", "client-query-buffer-limit", "databases", "appendonly", "save",
	} {
		refValue, err := ref.ConfigGet(ctx, parameter).Result()
		if err != nil {
			t.Fatalf("reference CONFIG GET %s: %v", parameter, err)
		}
		antValue, err := ant.ConfigGet(ctx, parameter).Result()
		if err != nil {
			t.Fatalf("antares CONFIG GET %s: %v", parameter, err)
		}
		if !reflect.DeepEqual(refValue, antValue) {
			t.Errorf("CONFIG GET %s: reference=%#v antares=%#v", parameter, refValue, antValue)
		}
	}

	for _, rdb := range []*redis.Client{ref, ant} {
		if err := rdb.ConfigSet(ctx, "maxmemory", "1mb").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory 1mb: %v", err)
		}
	}
	for _, rdb := range []*redis.Client{ref, ant} {
		value, err := rdb.ConfigGet(ctx, "maxmemory").Result()
		if err != nil {
			t.Fatalf("CONFIG GET maxmemory after set: %v", err)
		}
		if value["maxmemory"] != "1048576" {
			t.Fatalf("CONFIG GET maxmemory = %#v, want 1048576", value)
		}
		if err := rdb.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
			t.Fatalf("CONFIG SET maxmemory 0: %v", err)
		}
	}

	t.Run("client id and setinfo", func(t *testing.T) {
		for _, rdb := range []*redis.Client{ref, ant} {
			id, err := rdb.ClientID(ctx).Result()
			if err != nil || id < 1 {
				t.Fatalf("CLIENT ID = %d, %v", id, err)
			}
			if err := rdb.Do(ctx, "CLIENT", "SETINFO", "LIB-NAME", "antares-diff").Err(); err != nil {
				t.Fatalf("CLIENT SETINFO: %v", err)
			}
			if err := rdb.Do(ctx, "CLIENT", "SETINFO", "LIB-VER", "1").Err(); err != nil {
				t.Fatalf("CLIENT SETINFO: %v", err)
			}
		}
	})
}

func parseInfoMap(t *testing.T, info string) map[string]string {
	t.Helper()
	fields := make(map[string]string)
	for _, line := range strings.Split(info, "\r\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if ok {
			fields[name] = value
		}
	}
	return fields
}

func parseUint(value string) (uint64, bool) {
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}
