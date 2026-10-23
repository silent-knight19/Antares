package server

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"
)

// TestReplicaChaos50Kills is the S2.4 exit criterion: cut the replica link 50
// times mid-stream, keep writing on the master, and verify full dataset
// equality and offset parity after every reconnect.
func TestReplicaChaos50Kills(t *testing.T) {
	const iterations = 50

	masterExec := NewExecutor(nil, nil)
	t.Cleanup(masterExec.Close)
	master := startServerHandler(t, Config{Addr: "127.0.0.1:0"}, masterExec.Handle)
	proxy := newCutProxy(t, master.Addr().String())
	defer proxy.Close()

	replicaExec := NewExecutor(nil, nil)
	defer replicaExec.Close()

	rng := rand.New(rand.NewSource(42))
	keys := make([]string, 0, iterations*10)
	values := make(map[string]string)
	set := func(key, value string) {
		t.Helper()
		if got := runStrings(masterExec, "SET", key, value); got != "+OK\r\n" {
			t.Fatalf("master SET %s = %q", key, got)
		}
		keys = append(keys, key)
		values[key] = value
	}

	for i := 0; i < 5; i++ {
		set(fmt.Sprintf("seed:%d", i), fmt.Sprintf("seed-value-%d", i))
	}

	host, port, err := net.SplitHostPort(proxy.Addr().String())
	if err != nil {
		t.Fatalf("split proxy addr: %v", err)
	}
	if got := runStrings(replicaExec, "REPLICAOF", host, port); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF = %q", got)
	}
	waitForReplication(t, "initial sync", func() bool {
		return replicaLinkUp(replicaExec) && runStrings(replicaExec, "GET", "seed:0") == "$12\r\nseed-value-0\r\n"
	})

	for i := 0; i < iterations; i++ {
		for j := 0; j < 5; j++ {
			value := fmt.Sprintf("chaos-%d-%d-", i, j) + strings.Repeat("c", 1+rng.Intn(64))
			set(fmt.Sprintf("chaos:%d:%d", i, j), value)
		}
		set(fmt.Sprintf("chaos:%d:0", i), fmt.Sprintf("overwrite-%d", i))
		if got := runStrings(masterExec, "DEL", fmt.Sprintf("chaos:%d:1", i)); got != ":1\r\n" {
			t.Fatalf("master DEL chaos:%d:1 = %q, want :1", i, got)
		}
		delete(values, fmt.Sprintf("chaos:%d:1", i))
		set(fmt.Sprintf("large:%d", i), strings.Repeat("L", 32<<10))

		proxy.Cut()

		if got := runStrings(masterExec, "DEL", fmt.Sprintf("chaos:%d:2", i)); got != ":1\r\n" {
			t.Fatalf("master DEL chaos:%d:2 = %q, want :1", i, got)
		}
		delete(values, fmt.Sprintf("chaos:%d:2", i))
		set(fmt.Sprintf("chaos:%d:3", i), fmt.Sprintf("backlog-overwrite-%d", i))

		for j := 0; j < 5; j++ {
			value := fmt.Sprintf("gap-%d-%d-", i, j) + strings.Repeat("g", 1+rng.Intn(64))
			set(fmt.Sprintf("gap:%d:%d", i, j), value)
		}

		marker := keys[len(keys)-1]
		waitForReplication(t, fmt.Sprintf("reconnect %d", i), func() bool {
			return replicaLinkUp(replicaExec) &&
				runStrings(replicaExec, "GET", marker) == bulkString(values[marker]) &&
				replicaExec.Repl().SlaveReplOffset() == masterExec.Repl().MasterReplOffset()
		})

		assertReplicaDataset(t, replicaExec, keys, values)
	}

	if got := masterExec.Stats().SyncFull.Load(); got != 1 {
		t.Fatalf("sync_full = %d, want 1 (all reconnects must be partial)", got)
	}
	if got := masterExec.Stats().SyncPartialOK.Load(); got < iterations {
		t.Fatalf("sync_partial_ok = %d, want >= %d", got, iterations)
	}
}

// TestSplitBrainPromotionForcesFullResync proves a promoted replica cannot
// resume its old master's history: it shifts replid, so reconnecting forces a
// full resync that discards divergent writes (S2.4).
func TestSplitBrainPromotionForcesFullResync(t *testing.T) {
	masterExec := NewExecutor(nil, nil)
	t.Cleanup(masterExec.Close)
	master := startServerHandler(t, Config{Addr: "127.0.0.1:0"}, masterExec.Handle)

	replicaExec := NewExecutor(nil, nil)
	defer replicaExec.Close()

	mustSet(t, masterExec, "shared", "master")
	host, port, err := net.SplitHostPort(master.Addr().String())
	if err != nil {
		t.Fatalf("split master addr: %v", err)
	}
	if got := runStrings(replicaExec, "REPLICAOF", host, port); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF = %q", got)
	}
	waitForReplication(t, "initial sync", func() bool {
		return replicaLinkUp(replicaExec) && runStrings(replicaExec, "GET", "shared") == "$6\r\nmaster\r\n"
	})

	oldID := replicaExec.Repl().MasterReplID()
	if got := runStrings(replicaExec, "REPLICAOF", "NO", "ONE"); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF NO ONE = %q", got)
	}
	if newID := replicaExec.Repl().MasterReplID(); newID == oldID {
		t.Fatal("promoted replica kept its old master replid")
	}

	// Diverge on both sides of the split.
	mustSet(t, replicaExec, "divergent", "replica-only")
	mustSet(t, masterExec, "shared", "master2")
	mustSet(t, masterExec, "master-only", "A")

	if got := runStrings(replicaExec, "REPLICAOF", host, port); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF after promotion = %q", got)
	}
	waitForReplication(t, "full resync after split-brain", func() bool {
		return replicaLinkUp(replicaExec) &&
			runStrings(replicaExec, "GET", "shared") == "$7\r\nmaster2\r\n" &&
			runStrings(replicaExec, "GET", "master-only") == "$1\r\nA\r\n"
	})

	if got := runStrings(replicaExec, "GET", "divergent"); got != "$-1\r\n" {
		t.Fatalf("divergent split-brain key survived resync: %q", got)
	}
	if got := masterExec.Stats().SyncFull.Load(); got != 2 {
		t.Fatalf("sync_full = %d, want 2 (initial + post-split resync)", got)
	}
	if got := masterExec.Stats().SyncPartialOK.Load(); got != 0 {
		t.Fatalf("sync_partial_ok = %d, want 0 (new replid must force full resync)", got)
	}
	if replicaExec.Repl().MasterReplID() != masterExec.Repl().MasterReplID() {
		t.Fatal("replica did not adopt the master replid after full resync")
	}
}

func replicaLinkUp(exec *Executor) bool {
	return strings.Contains(runStrings(exec, "INFO", "replication"), "master_link_status:up")
}

func mustSet(t *testing.T, exec *Executor, key, value string) {
	t.Helper()
	if got := runStrings(exec, "SET", key, value); got != "+OK\r\n" {
		t.Fatalf("SET %s = %q", key, got)
	}
}

func bulkString(value string) string {
	return fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
}

func assertReplicaDataset(t *testing.T, replica *Executor, keys []string, values map[string]string) {
	t.Helper()
	args := make([]string, 0, len(keys)+1)
	args = append(args, "MGET")
	args = append(args, keys...)

	var want strings.Builder
	fmt.Fprintf(&want, "*%d\r\n", len(keys))
	for _, key := range keys {
		if value, ok := values[key]; ok {
			want.WriteString(bulkString(value))
		} else {
			want.WriteString("$-1\r\n")
		}
	}

	if got := runStrings(replica, args...); got != want.String() {
		t.Fatalf("replica dataset mismatch after reconnect: got %d bytes, want %d", len(got), want.Len())
	}
	if got := runStrings(replica, "DBSIZE"); got != fmt.Sprintf(":%d\r\n", len(values)) {
		t.Fatalf("replica DBSIZE = %q, want :%d (stale or missing keys)", got, len(values))
	}
}

func waitForReplication(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
