package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/store"
)

// TestReplicaReadOnlyMode tests that read-only replicas reject client writes with exact
// -READONLY errors, allow reads, allow read-only transactions, reject mutating transactions,
// and respect CONFIG SET replica-read-only yes/no.
func TestReplicaReadOnlyMode(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec := NewExecutorWithClock(nil, nil, clk)
	defer exec.Close()

	wantReadOnly := "-READONLY You can't write against a read only replica.\r\n"

	// 1. Initially master: writes succeed
	if got := runStrings(exec, "SET", "key1", "val1"); got != "+OK\r\n" {
		t.Fatalf("SET on master = %q, want +OK", got)
	}

	// 2. Transition to slave
	runStrings(exec, "REPLICAOF", "127.0.0.1", "6379")
	if !exec.Repl().IsSlave() {
		t.Fatal("expected role slave after REPLICAOF")
	}

	// 3. Client writes are rejected with -READONLY
	for _, writeCmd := range [][]string{
		{"SET", "key2", "val2"},
		{"DEL", "key1"},
		{"LPUSH", "list", "a"},
		{"RPUSH", "list", "b"},
		{"HSET", "hash", "f", "v"},
		{"SADD", "set", "m"},
		{"PEXPIRE", "key1", "1000"},
		{"FLUSHDB"},
	} {
		if got := runStrings(exec, writeCmd...); got != wantReadOnly {
			t.Fatalf("command %v on replica = %q, want %q", writeCmd, got, wantReadOnly)
		}
	}

	// 4. Client reads succeed on read-only replica
	if got := runStrings(exec, "GET", "key1"); got != "$4\r\nval1\r\n" {
		t.Fatalf("GET on replica = %q, want val1", got)
	}
	if got := runStrings(exec, "PING"); got != "+PONG\r\n" {
		t.Fatalf("PING on replica = %q, want +PONG", got)
	}

	// 5. Read-only transaction succeeds on replica
	txConn := &ConnState{}
	txRun(exec, txConn, "MULTI")
	txRun(exec, txConn, "GET", "key1")
	if got := txRun(exec, txConn, "EXEC"); got != "*1\r\n$4\r\nval1\r\n" {
		t.Fatalf("read-only EXEC = %q, want val1", got)
	}

	// 6. Mutating transaction is rejected at queue time with -READONLY and aborts at EXEC
	txConn2 := &ConnState{}
	txRun(exec, txConn2, "MULTI")
	if got := txRun(exec, txConn2, "SET", "blocked", "v"); got != wantReadOnly {
		t.Fatalf("queued SET on replica = %q, want %q", got, wantReadOnly)
	}
	if got := txRun(exec, txConn2, "EXEC"); !strings.HasPrefix(got, "-EXECABORT") {
		t.Fatalf("EXEC after dirty queue = %q, want -EXECABORT", got)
	}

	// 7. CONFIG GET / SET replica-read-only
	if got := runStrings(exec, "CONFIG", "GET", "replica-read-only"); got != "*2\r\n$17\r\nreplica-read-only\r\n$3\r\nyes\r\n" {
		t.Fatalf("CONFIG GET replica-read-only = %q", got)
	}

	// Toggle read-only to no
	if got := runStrings(exec, "CONFIG", "SET", "replica-read-only", "no"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET replica-read-only no = %q", got)
	}
	if exec.Repl().SlaveReadOnly() {
		t.Fatal("expected SlaveReadOnly() to be false")
	}

	// Client write now succeeds!
	if got := runStrings(exec, "SET", "now_writable", "worked"); got != "+OK\r\n" {
		t.Fatalf("SET on writable replica = %q, want +OK", got)
	}

	// Toggle back to yes
	if got := runStrings(exec, "CONFIG", "SET", "replica-read-only", "yes"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET replica-read-only yes = %q", got)
	}
	if !exec.Repl().SlaveReadOnly() {
		t.Fatal("expected SlaveReadOnly() to be true")
	}
	if got := runStrings(exec, "SET", "blocked_again", "v"); got != wantReadOnly {
		t.Fatalf("SET on read-only replica = %q, want %q", got, wantReadOnly)
	}

	// 8. Promote back to master (REPLICAOF NO ONE)
	if got := runStrings(exec, "REPLICAOF", "NO", "ONE"); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF NO ONE = %q", got)
	}
	if !exec.Repl().IsMaster() {
		t.Fatal("expected role master after promotion")
	}
	if got := runStrings(exec, "SET", "post_promotion", "ok"); got != "+OK\r\n" {
		t.Fatalf("SET after promotion = %q, want +OK", got)
	}
}

// TestReplicaApplyLoopProcessing tests that the replica apply loop:
// 1. Accepts and executes write commands from master using replication client state.
// 2. Accurately advances slave_repl_offset by the byte length of each command.
// 3. Handles keepalive PINGs without mutating keyspace.
// 4. Responds to REPLCONF GETACK * with REPLCONF ACK <offset>.
func TestReplicaApplyLoopProcessing(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	st := store.NewWithClock(clk)
	exec := NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	// Put executor into slave role
	exec.Repl().SetMaster("127.0.0.1", 6379)
	exec.Repl().SetSlaveReplOffset(1000)

	pr, pw := io.Pipe()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := repl.NewTrackedReader(pr, 1024)

	// Mock master connection via net.Pipe
	masterConn, replicaConn := net.Pipe()
	defer masterConn.Close()

	loopErrCh := make(chan error, 1)
	go func() {
		loopErrCh <- exec.RunReplicaApplyLoop(ctx, replicaConn, tr)
	}()

	// 1. Master sends SET foo bar (31 bytes)
	cmd1 := persist.EncodeCommand([]byte("SET"), []byte("foo"), []byte("bar"))
	if len(cmd1) != 31 {
		t.Fatalf("len(cmd1) = %d, want 31", len(cmd1))
	}
	if _, err := pw.Write(cmd1); err != nil {
		t.Fatalf("writing cmd1: %v", err)
	}

	// Give executor a moment to process command
	time.Sleep(10 * time.Millisecond)

	// Verify key in store
	// Verify key in store
	if got := runStrings(exec, "GET", "foo"); got != "$3\r\nbar\r\n" {
		t.Fatalf("GET foo = %q, want bar", got)
	}

	// Verify offset advanced by 31 bytes: 1000 + 31 = 1031
	if exec.Repl().SlaveReplOffset() != 1031 {
		t.Fatalf("slave_repl_offset = %d, want 1031", exec.Repl().SlaveReplOffset())
	}

	// 2. Master sends LPUSH list item1 item2 (40 bytes)
	cmd2 := persist.EncodeCommand([]byte("LPUSH"), []byte("list"), []byte("item1"), []byte("item2"))
	cmd2Len := int64(len(cmd2))
	if _, err := pw.Write(cmd2); err != nil {
		t.Fatalf("writing cmd2: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if got := runStrings(exec, "LLEN", "list"); got != ":2\r\n" {
		t.Fatalf("LLEN list = %q, want 2", got)
	}
	if exec.Repl().SlaveReplOffset() != 1031+cmd2Len {
		t.Fatalf("slave_repl_offset = %d, want %d", exec.Repl().SlaveReplOffset(), 1031+cmd2Len)
	}

	// 3. Master sends keepalive PING (*1\r\n$4\r\nPING\r\n = 14 bytes)
	cmd3 := persist.EncodeCommand([]byte("PING"))
	if len(cmd3) != 14 {
		t.Fatalf("len(cmd3) = %d, want 14", len(cmd3))
	}
	if _, err := pw.Write(cmd3); err != nil {
		t.Fatalf("writing cmd3: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	expectedOffset := 1031 + cmd2Len + 14
	if exec.Repl().SlaveReplOffset() != expectedOffset {
		t.Fatalf("slave_repl_offset after PING = %d, want %d", exec.Repl().SlaveReplOffset(), expectedOffset)
	}

	// 4. Master sends REPLCONF GETACK *
	cmd4 := persist.EncodeCommand([]byte("REPLCONF"), []byte("GETACK"), []byte("*"))
	cmd4Len := int64(len(cmd4))
	if _, err := pw.Write(cmd4); err != nil {
		t.Fatalf("writing cmd4: %v", err)
	}

	// Read ACK reply from masterConn
	masterReader := bufio.NewReader(masterConn)
	ackReply, err := masterReader.ReadString('\n')
	if err != nil {
		t.Fatalf("reading ACK reply: %v", err)
	}
	// REPLCONF ACK is a 3-element RESP array: *3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$<len>\r\n<offset>\r\n
	if !strings.HasPrefix(ackReply, "*3") {
		t.Fatalf("ackReply = %q, expected array header *3", ackReply)
	}
	line2, _ := masterReader.ReadString('\n')
	line3, _ := masterReader.ReadString('\n')
	line4, _ := masterReader.ReadString('\n')
	line5, _ := masterReader.ReadString('\n')
	line6, _ := masterReader.ReadString('\n')
	line7, _ := masterReader.ReadString('\n')
	ackBody := line2 + line3 + line4 + line5 + line6 + line7

	expectedAckOffset := expectedOffset + cmd4Len
	wantAckPart := fmt.Sprintf("%d", expectedAckOffset)
	if !strings.Contains(ackBody, wantAckPart) {
		t.Fatalf("ACK reply body %q does not contain expected offset %d", ackBody, expectedAckOffset)
	}

	if exec.Repl().SlaveReplOffset() != expectedAckOffset {
		t.Fatalf("slave_repl_offset after GETACK = %d, want %d", exec.Repl().SlaveReplOffset(), expectedAckOffset)
	}

	// 5. Clean cancel terminates loop
	cancel()
	_ = pw.Close()
	err = <-loopErrCh
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		t.Fatalf("loop error: %v", err)
	}
}

// TestReplicaFullResyncAndApplyLoop tests full resync followed by live continuous apply loop:
// 1. Master populated with initial dataset (snapshot).
// 2. Replica executes snapshot resync, populating its store.
// 3. Replica transitions link to up and enters continuous apply loop.
// 4. Master streams live mutations (SET, DEL, RPUSH, HSET, PING).
// 5. Replica applies all mutations without -READONLY blocking, tracks byte offsets, and matches master store.
func TestReplicaFullResyncAndApplyLoop(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))

	// Master
	masterStore := store.NewWithClock(clk)
	masterExec := NewExecutorWithClock(masterStore, nil, clk)
	defer masterExec.Close()

	runStrings(masterExec, "SET", "initial_str", "init_val")
	runStrings(masterExec, "LPUSH", "initial_list", "item1", "item2")
	runStrings(masterExec, "HSET", "initial_hash", "f1", "v1")
	runStrings(masterExec, "SADD", "initial_set", "m1", "m2")

	// Replica
	replicaStore := store.NewWithClock(clk)
	replicaExec := NewExecutorWithClock(replicaStore, nil, clk)
	defer replicaExec.Close()

	replicaExec.Repl().SetMaster("127.0.0.1", 6380)

	// Pipe simulating network connection
	pr, pw := io.Pipe()
	defer pw.Close()

	// Master prepares snapshot session
	session := masterExec.PrepareFullResync()

	// Replica net.Pipe for apply loop
	mockMasterNetConn, replicaNetConn := net.Pipe()
	defer mockMasterNetConn.Close()

	tr := repl.NewTrackedReader(pr, 64<<10)

	// Step 1: Replica reads snapshot from stream
	replicaExec.Repl().SetLinkStatus(repl.LinkStatusSync)
	replicaExec.Repl().SetMasterReplID(session.MasterReplID)
	replicaExec.Repl().SetSlaveReplOffset(session.SnapshotOffset)

	// Stream snapshot in goroutine
	streamErrCh := make(chan error, 1)
	go func() {
		streamErrCh <- session.Stream(pw)
	}()

	// Read +FULLRESYNC line from tr.Reader()
	fullResyncLine, err := tr.Reader().ReadString('\n')
	if err != nil {
		t.Fatalf("reading FULLRESYNC line: %v", err)
	}
	if !strings.HasPrefix(fullResyncLine, "+FULLRESYNC") {
		t.Fatalf("expected +FULLRESYNC, got %q", fullResyncLine)
	}

	// Apply snapshot stream into replica store
	reg := NewDataRegistry(replicaStore)
	enc := protocol.GetEncoder()
	defer enc.Release()

	if err := repl.ReadSnapshotStream(tr.Reader(), func(payload []byte) error {
		return ReplayPayload(reg, enc, payload)
	}); err != nil {
		t.Fatalf("ReadSnapshotStream: %v", err)
	}

	if err := <-streamErrCh; err != nil {
		t.Fatalf("session.Stream: %v", err)
	}

	// Verify snapshot keys in replica
	if got := runStrings(replicaExec, "GET", "initial_str"); got != "$8\r\ninit_val\r\n" {
		t.Fatalf("replica missing initial_str: %q", got)
	}

	// Step 2: Transition replica to LinkStatusUp and launch continuous apply loop
	replicaExec.Repl().SetLinkStatus(repl.LinkStatusUp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopErrCh := make(chan error, 1)
	go func() {
		loopErrCh <- replicaExec.RunReplicaApplyLoop(ctx, replicaNetConn, tr)
	}()

	// Step 3: Master sends live mutations over pw
	sendCmd := func(args ...[]byte) {
		cmdBytes := persist.EncodeCommand(args...)
		if _, err := pw.Write(cmdBytes); err != nil {
			t.Fatalf("writing live command: %v", err)
		}
	}

	// Execute on master then stream to replica
	runStrings(masterExec, "SET", "live_key", "live_val")
	sendCmd([]byte("SET"), []byte("live_key"), []byte("live_val"))

	runStrings(masterExec, "DEL", "initial_str")
	sendCmd([]byte("DEL"), []byte("initial_str"))

	runStrings(masterExec, "RPUSH", "initial_list", "item3")
	sendCmd([]byte("RPUSH"), []byte("initial_list"), []byte("item3"))

	runStrings(masterExec, "HSET", "initial_hash", "f2", "v2")
	sendCmd([]byte("HSET"), []byte("initial_hash"), []byte("f2"), []byte("v2"))

	time.Sleep(20 * time.Millisecond)

	// Verify live propagation into replica store
	if got := runStrings(replicaExec, "GET", "live_key"); got != "$8\r\nlive_val\r\n" {
		t.Fatalf("replica GET live_key = %q, want live_val", got)
	}
	if got := runStrings(replicaExec, "GET", "initial_str"); got != "$-1\r\n" {
		t.Fatalf("initial_str was not deleted on replica: %q", got)
	}
	if got := runStrings(replicaExec, "LLEN", "initial_list"); got != ":3\r\n" {
		t.Fatalf("initial_list on replica len = %q, want 3", got)
	}

	// Step 4: Verify client writes on replica return -READONLY
	wantReadOnly := "-READONLY You can't write against a read only replica.\r\n"
	if got := runStrings(replicaExec, "SET", "client_write", "forbidden"); got != wantReadOnly {
		t.Fatalf("replica allowed client write: %q", got)
	}

	// Step 5: Verify byte offsets match between master and replica
	masterOffset := masterExec.Repl().MasterReplOffset()
	replicaOffset := replicaExec.Repl().SlaveReplOffset()
	if replicaOffset != masterOffset {
		t.Fatalf("offsets mismatch: master=%d, replica=%d", masterOffset, replicaOffset)
	}

	// Step 6: Verify replica INFO replication formatting
	info := replicaExec.Repl().FormatInfo()
	if !strings.Contains(info, "role:slave") ||
		!strings.Contains(info, "master_link_status:up") ||
		!strings.Contains(info, fmt.Sprintf("slave_repl_offset:%d", masterOffset)) {
		t.Fatalf("replica INFO replication mismatch:\n%s", info)
	}

	cancel()
	_ = pw.Close()
	_ = <-loopErrCh
}
