package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/store"
)

// TestReplicaDatasetEqualityAfterFullResync is the exit criterion for Phase S1.3:
// It populates a master with all data structures (strings, lists, hashes, sets, volatile keys),
// takes a point-in-time snapshot, streams it over a socket/pipe, executes concurrent writes
// during snapshot streaming that are captured by the per-replica concurrent-write buffer,
// drains the write buffer, and asserts that the replica store is byte-for-byte equal to the master store.
func TestReplicaDatasetEqualityAfterFullResync(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	exec := NewExecutorWithClock(masterStore, nil, clk)
	defer exec.Close()

	// 1. Populate master with initial dataset across all supported data types
	initialCommands := [][]string{
		{"SET", "str1", "hello_world"},
		{"SET", "str2", "binary_\x00\xff_data"},
		{"SET", "str_to_delete", "goodbye"},
		{"SET", "str_expiring", "temporary", "PX", "500000"},
		{"LPUSH", "list1", "alpha", "beta", "gamma"},
		{"RPUSH", "list1", "delta"},
		{"LPOP", "list1"},
		{"HSET", "hash1", "f1", "v1", "f2", "v2", "f3", "v3"},
		{"HDEL", "hash1", "f2"},
		{"SADD", "set1", "apple", "banana", "cherry", "date"},
		{"SREM", "set1", "banana"},
	}
	for _, cmd := range initialCommands {
		res := runStrings(exec, cmd...)
		if strings.HasPrefix(res, "-") {
			t.Fatalf("command %v failed: %s", cmd, res)
		}
	}

	// 2. Prepare full resync under master
	session := exec.PrepareFullResync()

	// Pipe simulating network connection between master and replica
	pr, pw := io.Pipe()

	var wg sync.WaitGroup
	var streamErr error
	var applyErr error
	var replID string
	var replOffset int64
	replicaStore := store.NewWithClock(clk)

	// Goroutine 1: Master streaming thread
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		streamErr = session.Stream(pw)
	}()

	// 3. Concurrently execute writes on master while snapshot streaming is underway (ADR-010)
	concurrentWrites := [][]string{
		{"SET", "concurrent_str", "written_during_resync"},
		{"SET", "str1", "hello_overwritten"},
		{"DEL", "str_to_delete"},
		{"LPUSH", "list1", "concurrent_head"},
		{"RPUSH", "list_new", "item_a", "item_b"},
		{"HSET", "hash1", "f4", "v4_new"},
		{"HSET", "hash_new", "field_x", "value_y"},
		{"SADD", "set1", "elderberry"},
		{"SPOP", "set1"}, // deterministic SREM effect captured
		{"SET", "exp_during", "will_expire", "PX", "200000"},
	}

	for _, cmd := range concurrentWrites {
		res := runStrings(exec, cmd...)
		if strings.HasPrefix(res, "-") {
			t.Fatalf("concurrent command %v failed: %s", cmd, res)
		}
	}

	// Transaction block with shared connection state
	txConn := &ConnState{}
	txRun(exec, txConn, "MULTI")
	txRun(exec, txConn, "SET", "tx_key1", "tx_val1")
	txRun(exec, txConn, "RPUSH", "tx_list", "m1", "m2")
	execRes := txRun(exec, txConn, "EXEC")
	if strings.HasPrefix(execRes, "-") {
		t.Fatalf("EXEC failed: %s", execRes)
	}

	// Goroutine 2: Replica apply thread reading from pipe
	wg.Add(1)
	go func() {
		defer wg.Done()
		br := bufio.NewReader(pr)
		replID, replOffset, applyErr = ApplyFullResync(br, replicaStore, 0)
	}()

	wg.Wait()

	if streamErr != nil {
		t.Fatalf("master Stream failed: %v", streamErr)
	}
	if applyErr != nil {
		t.Fatalf("replica ApplyFullResync failed: %v", applyErr)
	}

	// Verify replication metadata
	if replID != exec.Repl().MasterReplID() {
		t.Fatalf("replica replID = %s, want %s", replID, exec.Repl().MasterReplID())
	}
	if replOffset < 0 {
		t.Fatalf("replica replOffset = %d, expected >= 0", replOffset)
	}

	// 4. EXIT CRITERION: Dataset equality assertion between master and replica
	assertStoresEqual(t, masterStore, replicaStore)
}

// TestFullResyncBufferOverflow tests ADR-010 overflow policy:
// When concurrent mutations exceed the replica's SyncBuffer capacity,
// the buffer enters overflow, drops memory, and Stream returns ErrSyncBufferOverflow.
func TestFullResyncBufferOverflow(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	exec := NewExecutorWithClock(masterStore, nil, clk)
	defer exec.Close()

	runStrings(exec, "SET", "key1", "val1")

	// Set tiny buffer limit (64 bytes)
	exec.Repl().SetSyncBufferLimit(64)
	session := exec.PrepareFullResync()

	// Fire mutations totaling much more than 64 bytes
	for i := 0; i < 10; i++ {
		runStrings(exec, "SET", "overflow_key_"+string(rune('a'+i)), "overflow_value_long_string_payload")
	}

	pr, pw := io.Pipe()
	defer pr.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Stream(pw)
		pw.Close()
	}()

	// Discard whatever is streamed
	_, _ = io.ReadAll(pr)

	err := <-errCh
	if err == nil || !errors.Is(err, repl.ErrSyncBufferOverflow) {
		t.Fatalf("expected ErrSyncBufferOverflow, got %v", err)
	}
}

// TestFullResyncEmptyDataset tests full resync when master has zero keys.
func TestFullResyncEmptyDataset(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	exec := NewExecutorWithClock(masterStore, nil, clk)
	defer exec.Close()

	session := exec.PrepareFullResync()

	pr, pw := io.Pipe()
	go func() {
		_ = session.Stream(pw)
		pw.Close()
	}()

	replicaStore := store.NewWithClock(clk)
	br := bufio.NewReader(pr)
	_, _, err := ApplyFullResync(br, replicaStore, 0)
	if err != nil {
		t.Fatalf("ApplyFullResync on empty store: %v", err)
	}

	assertStoresEqual(t, masterStore, replicaStore)
}

// TestReplicaDatasetEqualityConcurrentFuzz stresses full resync under heavy concurrent writes
// from multiple goroutines, verifying that the replica converges to exact dataset equality.
func TestReplicaDatasetEqualityConcurrentFuzz(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	exec := NewExecutorWithClock(masterStore, nil, clk)
	defer exec.Close()

	// Initial dataset: 50 keys
	for i := 0; i < 50; i++ {
		key := "init_" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		runStrings(exec, "SET", key, "initial_value")
	}

	session := exec.PrepareFullResync()

	pr, pw := io.Pipe()

	var wg sync.WaitGroup
	var streamErr error
	var applyErr error
	replicaStore := store.NewWithClock(clk)

	// Replica reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		br := bufio.NewReader(pr)
		_, _, applyErr = ApplyFullResync(br, replicaStore, 0)
	}()

	// Multiple writers firing concurrent commands
	writersDone := make(chan struct{})
	numWriters := 4
	var writersWg sync.WaitGroup
	for w := 0; w < numWriters; w++ {
		writersWg.Add(1)
		wID := w
		go func() {
			defer writersWg.Done()
			for i := 0; i < 30; i++ {
				k := "writer_" + string(rune('0'+wID)) + "_" + string(rune('a'+i%26))
				runStrings(exec, "SET", k, "concurrent_val")
				runStrings(exec, "LPUSH", "fuzz_list_"+string(rune('0'+wID)), "item")
				runStrings(exec, "HSET", "fuzz_hash_"+string(rune('0'+wID)), "f", "v")
				runStrings(exec, "SADD", "fuzz_set_"+string(rune('0'+wID)), "member_"+string(rune('a'+i%10)))
			}
		}()
	}

	go func() {
		writersWg.Wait()
		close(writersDone)
	}()

	// Wait for writers to finish before master completes Stream
	<-writersDone

	// Master finishes streaming
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer pw.Close()
		streamErr = session.Stream(pw)
	}()

	wg.Wait()

	if streamErr != nil {
		t.Fatalf("master Stream: %v", streamErr)
	}
	if applyErr != nil {
		t.Fatalf("replica ApplyFullResync: %v", applyErr)
	}

	assertStoresEqual(t, masterStore, replicaStore)
}

// TestApplySnapshotStreamClearsStaleKeys guards the reconnect fallback: a full
// resync must replace the replica dataset, not merge into it (S2.2).
func TestApplySnapshotStreamClearsStaleKeys(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	masterExec := NewExecutorWithClock(masterStore, nil, clk)
	defer masterExec.Close()

	if got := runStrings(masterExec, "SET", "fresh", "value"); got != "+OK\r\n" {
		t.Fatalf("master SET fresh = %q", got)
	}

	var snapshot bytes.Buffer
	if _, err := repl.WriteSnapshotStream(&snapshot, masterStore, ""); err != nil {
		t.Fatalf("WriteSnapshotStream: %v", err)
	}

	replicaExec := NewExecutorWithClock(store.NewWithClock(clk), nil, clk)
	defer replicaExec.Close()
	if got := runStrings(replicaExec, "SET", "stale", "old"); got != "+OK\r\n" {
		t.Fatalf("replica SET stale = %q", got)
	}

	tr := repl.NewTrackedReader(bytes.NewReader(snapshot.Bytes()), 64<<10)
	if err := replicaExec.applySnapshotStream(context.Background(), nil, tr, replicaExec.Repl().BeginSync()); err != nil {
		t.Fatalf("applySnapshotStream: %v", err)
	}

	if got := runStrings(replicaExec, "GET", "stale"); got != "$-1\r\n" {
		t.Fatalf("stale key survived full resync: %q", got)
	}
	if got := runStrings(replicaExec, "GET", "fresh"); got != "$5\r\nvalue\r\n" {
		t.Fatalf("fresh key missing after full resync: %q", got)
	}
}

func TestFailedFullResyncInvalidatesPSyncState(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	masterStore := store.NewWithClock(clk)
	masterExec := NewExecutorWithClock(masterStore, nil, clk)
	defer masterExec.Close()
	if got := runStrings(masterExec, "SET", "key", "value"); got != "+OK\r\n" {
		t.Fatalf("master SET = %q", got)
	}
	var snapshot bytes.Buffer
	if _, err := repl.WriteSnapshotStream(&snapshot, masterStore, ""); err != nil {
		t.Fatalf("WriteSnapshotStream: %v", err)
	}
	raw := snapshot.Bytes()
	truncated := raw[:len(raw)-1]

	replicaExec := NewExecutorWithClock(store.NewWithClock(clk), nil, clk)
	defer replicaExec.Close()
	replicaExec.Repl().SetMaster("127.0.0.1", 6379)
	replicaExec.Repl().SetMasterReplID("oldmaster")
	replicaExec.Repl().SetSlaveReplOffset(42)

	gen := replicaExec.Repl().BeginSync()
	tr := repl.NewTrackedReader(bytes.NewReader(truncated), 64<<10)
	if err := replicaExec.applySnapshotStream(context.Background(), nil, tr, gen); err == nil {
		t.Fatal("truncated snapshot was accepted")
	}
	if id, off := replicaExec.Repl().PSyncArgs(); id != "?" || off != -1 {
		t.Fatalf("PSyncArgs after failed full resync = %q %d, want ? -1", id, off)
	}
}

func TestStreamOpenReleasesSnapshot(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()
	session := exec.PrepareFullResync()
	if session.Snapshot == nil {
		t.Fatal("snapshot was not captured")
	}
	if err := session.Stream(io.Discard); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if session.Snapshot != nil {
		t.Fatal("snapshot retained after streaming")
	}
}
