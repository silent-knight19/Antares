package server

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func infoBody(t *testing.T, resp string) string {
	t.Helper()
	end := strings.Index(resp, "\r\n")
	if !strings.HasPrefix(resp, "$") || end < 0 {
		t.Fatalf("malformed INFO reply: %q", resp)
	}
	n, err := strconv.Atoi(resp[1:end])
	if err != nil || len(resp) < end+2+n {
		t.Fatalf("malformed INFO bulk length: %q", resp)
	}
	return resp[end+2 : end+2+n]
}

func infoField(t *testing.T, body, name string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\r\n") {
		if strings.HasPrefix(line, name+":") {
			return strings.TrimPrefix(line, name+":")
		}
	}
	t.Fatalf("INFO field %q missing from:\n%s", name, body)
	return ""
}

func TestInfoSections(t *testing.T) {
	exec := newStringExec(t)
	body := infoBody(t, runStrings(exec, "INFO"))
	for _, want := range []string{
		"# Server\r\n", "redis_version:7.4.11", "redis_mode:standalone",
		"process_supervised:no", "io_threads_active:0", "hz:10",
		"# Clients\r\n", "connected_clients:0", "maxclients:10000",
		"blocked_clients:0", "# Memory\r\n", "used_memory:",
		"used_memory_rss:", "maxmemory:0", "maxmemory_policy:noeviction",
		"# Persistence\r\n", "loading:0", "rdb_bgsave_in_progress:0",
		"aof_rewrite_in_progress:0", "# Stats\r\n",
		"total_connections_received:0", "total_commands_processed:",
		"# Replication\r\n", "role:master", "connected_slaves:0",
		"# CPU\r\n", "# Modules\r\n", "# Errorstats\r\n",
		"# Cluster\r\n", "cluster_enabled:0", "# Keyspace\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("default INFO missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "# Commandstats") {
		t.Fatalf("default INFO unexpectedly contains commandstats:\n%s", body)
	}

	if got := infoBody(t, runStrings(exec, "INFO", "server")); !strings.HasPrefix(got, "# Server\r\n") || strings.Contains(got, "# Clients") {
		t.Fatalf("INFO server = %q", got)
	}
	if got := infoBody(t, runStrings(exec, "INFO", "MEMORY")); !strings.HasPrefix(got, "# Memory\r\n") || strings.Contains(got, "# Server") {
		t.Fatalf("INFO MEMORY = %q", got)
	}
	if got := infoBody(t, runStrings(exec, "INFO", "nosuchsection")); got != "" {
		t.Fatalf("INFO nosuchsection = %q, want empty", got)
	}
	multi := infoBody(t, runStrings(exec, "INFO", "server", "clients"))
	if strings.Index(multi, "# Server") > strings.Index(multi, "# Clients") {
		t.Fatalf("INFO server clients order wrong:\n%s", multi)
	}
	if all := infoBody(t, runStrings(exec, "INFO", "all")); !strings.Contains(all, "# Commandstats") || !strings.Contains(all, "# Latencystats") {
		t.Fatalf("INFO all missing optional sections:\n%s", all)
	}
	if everything := infoBody(t, runStrings(exec, "INFO", "everything")); !strings.Contains(everything, "# Commandstats") {
		t.Fatalf("INFO everything missing commandstats:\n%s", everything)
	}
}

func TestInfoStatsAndKeyspace(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "a", "1")
	runStrings(exec, "SET", "b", "2")
	runStrings(exec, "PEXPIRE", "b", "100000")

	body := infoBody(t, runStrings(exec, "INFO", "keyspace"))
	wantKeyspace := "# Keyspace\r\ndb0:keys=2,expires=1,avg_ttl=0,subexpiry=0\r\n"
	if body != wantKeyspace {
		t.Fatalf("INFO keyspace = %q, want %q", body, wantKeyspace)
	}

	body = infoBody(t, runStrings(exec, "INFO", "stats"))
	if got := infoField(t, body, "total_commands_processed"); got != "4" {
		t.Fatalf("total_commands_processed = %q, want 4", got)
	}

	runStrings(exec, "DEL", "a", "b")
	body = infoBody(t, runStrings(exec, "INFO", "keyspace"))
	if body != "# Keyspace\r\n" {
		t.Fatalf("empty INFO keyspace = %q", body)
	}
}

func TestInfoExpiredKeys(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newStringExecWithClock(t, f)
	runStrings(exec, "SET", "gone", "v")
	runStrings(exec, "PEXPIRE", "gone", "10")
	f.Advance(20 * time.Millisecond)
	runStrings(exec, "GET", "gone")

	body := infoBody(t, runStrings(exec, "INFO", "stats"))
	if got := infoField(t, body, "expired_keys"); got != "1" {
		t.Fatalf("expired_keys = %q, want 1", got)
	}
}

func TestInfoMemoryAccounting(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "key", "value")
	body := infoBody(t, runStrings(exec, "INFO", "memory"))
	if got := infoField(t, body, "used_memory_dataset"); got != "5" {
		t.Fatalf("used_memory_dataset = %q, want 5", got)
	}
	used, _ := strconv.Atoi(infoField(t, body, "used_memory"))
	overhead, _ := strconv.Atoi(infoField(t, body, "used_memory_overhead"))
	if used != overhead+5 {
		t.Fatalf("used_memory = %d, overhead = %d; want used = overhead+5", used, overhead)
	}

	runStrings(exec, "DEL", "key")
	body = infoBody(t, runStrings(exec, "INFO", "memory"))
	if got := infoField(t, body, "used_memory"); got != "0" {
		t.Fatalf("used_memory after DEL = %q, want 0", got)
	}
}

func TestInfoMemoryPeakTracksCommands(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "big", strings.Repeat("x", 100000))
	runStrings(exec, "SET", "big", "small")
	body := infoBody(t, runStrings(exec, "INFO", "memory"))
	if peak, _ := strconv.Atoi(infoField(t, body, "used_memory_peak")); peak < 100000 {
		t.Fatalf("used_memory_peak = %d, want at least the transient 100000-byte value", peak)
	}
}

func TestInfoHzAndMaxmemory(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "CONFIG", "SET", "hz", "20")
	runStrings(exec, "CONFIG", "SET", "maxmemory", "1mb")
	runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "allkeys-lru")

	serverBody := infoBody(t, runStrings(exec, "INFO", "server"))
	if got := infoField(t, serverBody, "hz"); got != "20" {
		t.Fatalf("INFO hz = %q, want 20", got)
	}
	memoryBody := infoBody(t, runStrings(exec, "INFO", "memory"))
	if got := infoField(t, memoryBody, "maxmemory"); got != "1048576" {
		t.Fatalf("INFO maxmemory = %q, want 1048576", got)
	}
	if got := infoField(t, memoryBody, "maxmemory_policy"); got != "allkeys-lru" {
		t.Fatalf("INFO maxmemory_policy = %q, want allkeys-lru", got)
	}
	clientsBody := infoBody(t, runStrings(exec, "INFO", "clients"))
	if got := infoField(t, clientsBody, "maxclients"); got != "10000" {
		t.Fatalf("INFO maxclients = %q, want 10000", got)
	}
}
