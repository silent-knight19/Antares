package server

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/store"
)

// TestMasterSlaveLagTrackingAndInfo verifies the S1.5 exit criteria:
// 1. REPLCONF handshake (listening-port, ip-address, psync) registers a replica.
// 2. INFO replication includes connected_slaves:N and slave0:ip=...,port=...,state=...,offset=...,lag=...
// 3. Advancing injected virtual clock increments lag deterministically without sleep (ADR-009).
// 4. REPLCONF ACK <offset> produces NO response and resets lag to 0 while updating offset.
// 5. Multiple slaves are indexed consecutively; disconnecting a slave re-indexes the remainder.
func TestMasterSlaveLagTrackingAndInfo(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(1000000, 0))
	exec := NewExecutorWithClock(nil, nil, fakeClk)
	defer exec.Close()

	// Initial master state: 0 slaves
	rawInfo := runStrings(exec, "INFO", "replication")
	if !strings.Contains(rawInfo, "role:master") || !strings.Contains(rawInfo, "connected_slaves:0") {
		t.Fatalf("initial INFO replication mismatch:\n%s", rawInfo)
	}
	if strings.Contains(rawInfo, "slave0:") {
		t.Fatalf("initial INFO unexpectedly contains slave0:\n%s", rawInfo)
	}

	// Connect simulated replica 1 on 127.0.0.1:6381
	conn1 := &ConnState{
		remoteIP:   "127.0.0.1",
		remotePort: 54321,
	}
	defer conn1.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	// 1. REPLCONF listening-port 6381
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte("6381")}, enc, conn1)
	if got := string(enc.Bytes()); got != "+OK\r\n" {
		t.Fatalf("REPLCONF listening-port reply = %q, want +OK", got)
	}

	// 2. REPLCONF ip-address 127.0.0.1
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("ip-address"), []byte("127.0.0.1")}, enc, conn1)
	if got := string(enc.Bytes()); got != "+OK\r\n" {
		t.Fatalf("REPLCONF ip-address reply = %q, want +OK", got)
	}

	// 3. PSYNC ? -1
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("PSYNC"), []byte("?"), []byte("-1")}, enc, conn1)
	if got := string(enc.Bytes()); !strings.HasPrefix(got, "+FULLRESYNC ") {
		t.Fatalf("PSYNC ? -1 reply = %q, want +FULLRESYNC prefix", got)
	}

	// Verify INFO replication has slave0 with lag=0
	info1 := runStrings(exec, "INFO", "replication")
	wantSlave0Initial := "slave0:ip=127.0.0.1,port=6381,state=online,offset=0,lag=0"
	if !strings.Contains(info1, "connected_slaves:1") || !strings.Contains(info1, wantSlave0Initial) {
		t.Fatalf("INFO replication missing slave0 after handshake:\n%s", info1)
	}

	// Advance virtual clock by 3 seconds -> lag must be 3
	fakeClk.Advance(3 * time.Second)
	infoLag3 := runStrings(exec, "INFO", "replication")
	wantSlave0Lag3 := "slave0:ip=127.0.0.1,port=6381,state=online,offset=0,lag=3"
	if !strings.Contains(infoLag3, wantSlave0Lag3) {
		t.Fatalf("INFO replication missing lag=3:\n%s", infoLag3)
	}

	// Replica sends REPLCONF ACK 450
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("ACK"), []byte("450")}, enc, conn1)
	// In Redis protocol, master does not write any reply to REPLCONF ACK
	if got := string(enc.Bytes()); got != "" {
		t.Fatalf("REPLCONF ACK unexpectedly generated reply: %q", got)
	}

	// Verify offset updated to 450 and lag reset to 0
	infoAfterAck := runStrings(exec, "INFO", "replication")
	wantSlave0AfterAck := "slave0:ip=127.0.0.1,port=6381,state=online,offset=450,lag=0"
	if !strings.Contains(infoAfterAck, wantSlave0AfterAck) {
		t.Fatalf("INFO replication missing updated offset=450,lag=0:\n%s", infoAfterAck)
	}

	// Advance virtual clock by 2 seconds -> lag becomes 2
	fakeClk.Advance(2 * time.Second)

	// Connect second replica on 10.0.0.2:6382
	conn2 := &ConnState{
		remoteIP:   "10.0.0.2",
		remotePort: 54322,
	}
	defer conn2.Close()

	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte("6382")}, enc, conn2)
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("ip-address"), []byte("10.0.0.2")}, enc, conn2)
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("PSYNC"), []byte("?"), []byte("-1")}, enc, conn2)

	infoTwoSlaves := runStrings(exec, "INFO", "replication")
	if !strings.Contains(infoTwoSlaves, "connected_slaves:2") ||
		!strings.Contains(infoTwoSlaves, "slave0:ip=127.0.0.1,port=6381,state=online,offset=450,lag=2") ||
		!strings.Contains(infoTwoSlaves, "slave1:ip=10.0.0.2,port=6382,state=online,offset=0,lag=0") {
		t.Fatalf("INFO replication missing two slaves:\n%s", infoTwoSlaves)
	}

	// Disconnect slave 0 (conn1.Close())
	conn1.Close()

	infoAfterDisconnect := runStrings(exec, "INFO", "replication")
	if !strings.Contains(infoAfterDisconnect, "connected_slaves:1") ||
		strings.Contains(infoAfterDisconnect, "slave1:") ||
		!strings.Contains(infoAfterDisconnect, "slave0:ip=10.0.0.2,port=6382,state=online,offset=0,lag=0") {
		t.Fatalf("INFO replication after slave0 disconnect incorrect:\n%s", infoAfterDisconnect)
	}
}

// TestPeriodicReplconfAckInApplyLoop verifies that the replica apply loop
// sends REPLCONF ACK every second (tested with a fast interval), reflects
// consumed mutation stream bytes in the offset, and answers REPLCONF GETACK *.
func TestPeriodicReplconfAckInApplyLoop(t *testing.T) {
	clk := clock.NewFake(time.Unix(1000000, 0))
	replicaStore := store.NewWithClock(clk)
	replicaReg := NewRegistry()
	registerDataCommands(replicaReg, replicaStore)

	replicaExec := NewExecutorWithClock(replicaStore, replicaReg, clk)
	defer replicaExec.Close()

	// Simulate master <-> replica connection via in-memory net.Pipe
	masterConn, replicaConn := net.Pipe()
	defer masterConn.Close()

	tr := repl.NewTrackedReader(replicaConn, 16<<10)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loopErrCh := make(chan error, 1)
	// Run apply loop with 20ms periodic ACK interval for fast testing
	go func() {
		loopErrCh <- replicaExec.RunReplicaApplyLoopWithInterval(ctx, replicaConn, tr, 20*time.Millisecond)
	}()

	masterReader := bufio.NewReader(masterConn)

	readAck := func() int64 {
		req, err := protocol.ReadRequest(masterReader)
		if err != nil {
			t.Fatalf("reading ACK from replica: %v", err)
		}
		if len(req) != 3 || !strings.EqualFold(string(req[0]), "REPLCONF") || !strings.EqualFold(string(req[1]), "ACK") {
			t.Fatalf("unexpected request from replica: %v", req)
		}
		off, err := strconv.ParseInt(string(req[2]), 10, 64)
		if err != nil {
			t.Fatalf("parsing ACK offset %q: %v", string(req[2]), err)
		}
		return off
	}

	// 1. Replica should transmit initial periodic ACK 0
	off1 := readAck()
	if off1 != 0 {
		t.Fatalf("first periodic ACK offset = %d, want 0", off1)
	}

	// 2. Stream a mutation command from master to replica: SET greeting "hello"
	setCmd := persist.EncodeCommand([]byte("SET"), []byte("greeting"), []byte("hello"))
	cmdLen := int64(len(setCmd))
	if _, err := masterConn.Write(setCmd); err != nil {
		t.Fatalf("writing SET to replica: %v", err)
	}

	// Wait for periodic ACK reflecting new offset
	deadline := time.After(2 * time.Second)
	var lastOff int64
	for lastOff < cmdLen {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for ACK offset %d, got %d", cmdLen, lastOff)
		default:
			lastOff = readAck()
		}
	}
	if lastOff != cmdLen {
		t.Fatalf("ACK offset after SET = %d, want %d", lastOff, cmdLen)
	}

	// 3. Master sends REPLCONF GETACK * -> Replica responds immediately
	getackCmd := persist.EncodeCommand([]byte("REPLCONF"), []byte("GETACK"), []byte("*"))
	if _, err := masterConn.Write(getackCmd); err != nil {
		t.Fatalf("writing GETACK to replica: %v", err)
	}

	immediateAck := readAck()
	wantOffset := cmdLen + int64(len(getackCmd))
	if immediateAck != wantOffset {
		t.Fatalf("immediate GETACK reply offset = %d, want %d", immediateAck, wantOffset)
	}

	cancel()
	_ = masterConn.Close()
	_ = <-loopErrCh
}

// TestLiveMasterSlaveLagIntegration launches an actual Antares master server on a TCP port,
// connects a replica via live TCP connection, performs mutations, and verifies INFO replication
// format and lag field on the master live.
func TestLiveMasterSlaveLagIntegration(t *testing.T) {
	masterExec := NewExecutor(nil, nil)
	defer masterExec.Close()

	srv := New(Config{Addr: "127.0.0.1:0"}, masterExec.Handle)
	if err := srv.Start(); err != nil {
		t.Fatalf("starting master server: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	masterAddr := srv.Addr().String()

	// Connect client to master
	clientConn, err := net.Dial("tcp", masterAddr)
	if err != nil {
		t.Fatalf("dialing master: %v", err)
	}
	defer clientConn.Close()

	clientReader := bufio.NewReader(clientConn)

	// Send handshake
	_, _ = clientConn.Write([]byte("*3\r\n$8\r\nREPLCONF\r\n$14\r\nlistening-port\r\n$4\r\n6399\r\n"))
	resp1, _ := clientReader.ReadString('\n')
	if !strings.HasPrefix(resp1, "+OK") {
		t.Fatalf("REPLCONF listening-port response: %q", resp1)
	}

	_, _ = clientConn.Write([]byte("*3\r\n$8\r\nREPLCONF\r\n$4\r\ncapa\r\n$3\r\neof\r\n"))
	resp2, _ := clientReader.ReadString('\n')
	if !strings.HasPrefix(resp2, "+OK") {
		t.Fatalf("REPLCONF capa response: %q", resp2)
	}

	_, _ = clientConn.Write([]byte("*3\r\n$5\r\nPSYNC\r\n$1\r\n?\r\n$2\r\n-1\r\n"))
	resp3, _ := clientReader.ReadString('\n')
	if !strings.HasPrefix(resp3, "+FULLRESYNC") {
		t.Fatalf("PSYNC response: %q", resp3)
	}

	// Read INFO replication from master using a second client
	infoConn, err := net.Dial("tcp", masterAddr)
	if err != nil {
		t.Fatalf("dialing info client: %v", err)
	}
	defer infoConn.Close()

	infoReader := bufio.NewReader(infoConn)

	readBulkString := func() string {
		line, err := infoReader.ReadString('\n')
		if err != nil {
			t.Fatalf("reading bulk header: %v", err)
		}
		if !strings.HasPrefix(line, "$") {
			t.Fatalf("expected bulk string starting with $, got %q", line)
		}
		size, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil {
			t.Fatalf("parsing bulk size: %v", err)
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(infoReader, buf); err != nil {
			t.Fatalf("reading bulk body: %v", err)
		}
		return string(buf[:size])
	}

	_, _ = infoConn.Write([]byte("*2\r\n$4\r\nINFO\r\n$11\r\nreplication\r\n"))
	infoText := readBulkString()

	if !strings.Contains(infoText, "connected_slaves:1") {
		t.Fatalf("INFO replication missing connected_slaves:1:\n%s", infoText)
	}
	wantFields := []string{"slave0:", "ip=", "port=6399", "state=online", "offset=0", "lag=0"}
	for _, wf := range wantFields {
		if !strings.Contains(infoText, wf) {
			t.Fatalf("INFO replication missing field %q:\n%s", wf, infoText)
		}
	}

	// Send REPLCONF ACK 1234
	_, _ = clientConn.Write([]byte("*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$4\r\n1234\r\n"))

	// Small pause for command execution under single-goroutine executor
	time.Sleep(25 * time.Millisecond)

	_, _ = infoConn.Write([]byte("*2\r\n$4\r\nINFO\r\n$11\r\nreplication\r\n"))
	infoText2 := readBulkString()

	if !strings.Contains(infoText2, "offset=1234,lag=0") {
		t.Fatalf("INFO replication missing updated offset=1234,lag=0:\n%s", infoText2)
	}

	// Close replica connection -> verify slave is removed
	_ = clientConn.Close()
	time.Sleep(25 * time.Millisecond)

	_, _ = infoConn.Write([]byte("*2\r\n$4\r\nINFO\r\n$11\r\nreplication\r\n"))
	infoText3 := readBulkString()
	if !strings.Contains(infoText3, "connected_slaves:0") || strings.Contains(infoText3, "slave0:") {
		t.Fatalf("INFO replication should have 0 slaves after disconnect:\n%s", infoText3)
	}
}

// TestReplicationEdgeCasesAndMemorySafety verifies that:
// 1. Replicas bypass the maxmemory OOM gate so master mutations are never dropped.
// 2. REPLCONF listening-port validates port range (0-65535).
// 3. REPLCONF ACK ignores negative offsets.
func TestReplicationEdgeCasesAndMemorySafety(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(1000000, 0))
	exec := NewExecutorWithClock(nil, nil, fakeClk)
	defer exec.Close()

	// 1. Test maxmemory bypass for replica client
	runStrings(exec, "SET", "seed_key", "seed_val") // Seed key so memory usage > 0
	runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "noeviction")
	runStrings(exec, "CONFIG", "SET", "maxmemory", "1") // 1 byte limit -> forces OOM on non-empty store

	regularConn := &ConnState{}
	replicaConn := NewReplicaConnState()
	enc := protocol.GetEncoder()
	defer enc.Release()

	// Regular client write gets rejected with OOM
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("SET"), []byte("key1"), []byte("val1")}, enc, regularConn)
	if !strings.HasPrefix(string(enc.Bytes()), "-OOM") {
		t.Fatalf("expected -OOM for regular client, got: %q", string(enc.Bytes()))
	}

	// Replica client write bypasses OOM gate and succeeds
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("SET"), []byte("key1"), []byte("val1")}, enc, replicaConn)
	if string(enc.Bytes()) != "+OK\r\n" {
		t.Fatalf("expected +OK for replica client under maxmemory, got: %q", string(enc.Bytes()))
	}

	// Reset maxmemory
	runStrings(exec, "CONFIG", "SET", "maxmemory", "0")

	// 2. Test port range validation in REPLCONF
	conn := &ConnState{remoteIP: "127.0.0.1", remotePort: 50000}
	defer conn.Close()

	// Out of range negative port
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte("-1")}, enc, conn)
	info := runStrings(exec, "INFO", "replication")
	if strings.Contains(info, "port=-1") {
		t.Fatalf("invalid negative port was accepted:\n%s", info)
	}

	// Valid port
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte("6385")}, enc, conn)
	info2 := runStrings(exec, "INFO", "replication")
	if !strings.Contains(info2, "port=6385") {
		t.Fatalf("valid port 6385 was not recorded:\n%s", info2)
	}

	// 3. Test negative ACK offset is ignored
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("ACK"), []byte("-100")}, enc, conn)
	info3 := runStrings(exec, "INFO", "replication")
	if strings.Contains(info3, "offset=-100") {
		t.Fatalf("negative ACK offset was accepted:\n%s", info3)
	}
}

