package server

import (
	"strings"
	"testing"

	"github.com/silent-knight19/antares/internal/clock"
)

func TestReplicaOfRoleTransitionsAndReadOnly(t *testing.T) {
	clk := clock.Real{}
	exec := NewExecutorWithClock(nil, nil, clk)
	defer exec.Close()

	// 1. Initial state is master
	info := runStrings(exec, "INFO", "replication")
	if !strings.Contains(info, "role:master") {
		t.Fatalf("expected role:master, got:\n%s", info)
	}

	// Writes are allowed on master
	if got := runStrings(exec, "SET", "foo", "bar"); got != "+OK\r\n" {
		t.Fatalf("SET on master = %q, want +OK", got)
	}

	// 2. Switch to replica of 127.0.0.1:6379
	if got := runStrings(exec, "REPLICAOF", "127.0.0.1", "6379"); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF = %q, want +OK", got)
	}

	info = runStrings(exec, "INFO", "replication")
	if !strings.Contains(info, "role:slave") ||
		!strings.Contains(info, "master_host:127.0.0.1") ||
		!strings.Contains(info, "master_port:6379") ||
		!strings.Contains(info, "slave_read_only:1") {
		t.Fatalf("expected replica info, got:\n%s", info)
	}

	// Writes are blocked on read-only replica
	wantReadOnly := "-READONLY You can't write against a read only replica.\r\n"
	if got := runStrings(exec, "SET", "newkey", "val"); got != wantReadOnly {
		t.Fatalf("SET on replica = %q, want %q", got, wantReadOnly)
	}
	if got := runStrings(exec, "DEL", "foo"); got != wantReadOnly {
		t.Fatalf("DEL on replica = %q, want %q", got, wantReadOnly)
	}

	// Reads and non-writes still work
	if got := runStrings(exec, "GET", "foo"); got != "$3\r\nbar\r\n" {
		t.Fatalf("GET on replica = %q", got)
	}
	if got := runStrings(exec, "PING"); got != "+PONG\r\n" {
		t.Fatalf("PING on replica = %q", got)
	}

	// EXEC outside MULTI returns -ERR EXEC without MULTI, NOT -READONLY
	if got := runStrings(exec, "EXEC"); got != "-ERR EXEC without MULTI\r\n" {
		t.Fatalf("EXEC outside MULTI on replica = %q, want -ERR EXEC without MULTI", got)
	}

	// Read-only transaction succeeds on replica
	conn := &ConnState{}
	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "GET", "foo")
	if got := txRun(exec, conn, "EXEC"); got != "*1\r\n$3\r\nbar\r\n" {
		t.Fatalf("read-only EXEC on replica = %q, want *1\\r\\n$3\\r\\nbar\\r\\n", got)
	}

	// Mutating transaction is rejected at queue time with -READONLY and aborts at EXEC
	conn2 := &ConnState{}
	txRun(exec, conn2, "MULTI")
	if got := txRun(exec, conn2, "SET", "blocked", "val"); got != wantReadOnly {
		t.Fatalf("queued SET on replica = %q, want %q", got, wantReadOnly)
	}
	if got := txRun(exec, conn2, "EXEC"); !strings.Contains(got, "EXECABORT") {
		t.Fatalf("mutating EXEC on replica = %q, want EXECABORT", got)
	}

	// 3. Promote back to master with REPLICAOF NO ONE
	if got := runStrings(exec, "REPLICAOF", "NO", "ONE"); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF NO ONE = %q, want +OK", got)
	}

	info = runStrings(exec, "INFO", "replication")
	if !strings.Contains(info, "role:master") {
		t.Fatalf("expected promoted role:master, got:\n%s", info)
	}

	// Writes work again
	if got := runStrings(exec, "SET", "promoted", "val"); got != "+OK\r\n" {
		t.Fatalf("SET after promotion = %q, want +OK", got)
	}
}

func TestReplicaOfValidation(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	wantPortErr := "-ERR Invalid master port\r\n"
	if got := runStrings(exec, "REPLICAOF", "127.0.0.1", "badport"); got != wantPortErr {
		t.Fatalf("badport = %q, want %q", got, wantPortErr)
	}
	if got := runStrings(exec, "REPLICAOF", "127.0.0.1", "70000"); got != wantPortErr {
		t.Fatalf("70000 = %q, want %q", got, wantPortErr)
	}
	if got := runStrings(exec, "REPLICAOF", "127.0.0.1", "-5"); got != wantPortErr {
		t.Fatalf("-5 = %q, want %q", got, wantPortErr)
	}

	wantArity := "-ERR wrong number of arguments for 'replicaof' command\r\n"
	if got := runStrings(exec, "REPLICAOF"); got != wantArity {
		t.Fatalf("no args = %q, want %q", got, wantArity)
	}
	if got := runStrings(exec, "REPLICAOF", "127.0.0.1"); got != wantArity {
		t.Fatalf("one arg = %q, want %q", got, wantArity)
	}

	wantSlaveofArity := "-ERR wrong number of arguments for 'slaveof' command\r\n"
	if got := runStrings(exec, "SLAVEOF"); got != wantSlaveofArity {
		t.Fatalf("slaveof arity = %q, want %q", got, wantSlaveofArity)
	}

	// SLAVEOF alias works identically
	if got := runStrings(exec, "SLAVEOF", "127.0.0.1", "6380"); got != "+OK\r\n" {
		t.Fatalf("SLAVEOF = %q, want +OK", got)
	}
	if got := runStrings(exec, "SLAVEOF", "NO", "ONE"); got != "+OK\r\n" {
		t.Fatalf("SLAVEOF NO ONE = %q, want +OK", got)
	}
}

func TestReplconfAndPsyncCommands(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	// REPLCONF forms
	if got := runStrings(exec, "REPLCONF"); got != "+OK\r\n" {
		t.Fatalf("bare REPLCONF = %q, want +OK", got)
	}
	if got := runStrings(exec, "REPLCONF", "listening-port", "6381"); got != "+OK\r\n" {
		t.Fatalf("REPLCONF listening-port = %q", got)
	}
	if got := runStrings(exec, "REPLCONF", "capa", "eof", "capa", "psync2"); got != "+OK\r\n" {
		t.Fatalf("REPLCONF capa = %q", got)
	}
	if got := runStrings(exec, "REPLCONF", "ip-address", "127.0.0.1"); got != "+OK\r\n" {
		t.Fatalf("REPLCONF ip-address = %q", got)
	}

	// PSYNC full resync when "?" is sent
	replID := exec.Repl().MasterReplID()
	got := runStrings(exec, "PSYNC", "?", "-1")
	wantPrefix := "+FULLRESYNC " + replID + " 0\r\n"
	if got != wantPrefix {
		t.Fatalf("PSYNC ? -1 = %q, want %q", got, wantPrefix)
	}

	// PSYNC continue when replID matches and offset is in backlog window
	gotContinue := runStrings(exec, "PSYNC", replID, "0")
	wantContinue := "+CONTINUE " + replID + "\r\n"
	if gotContinue != wantContinue {
		t.Fatalf("PSYNC continue = %q, want %q", gotContinue, wantContinue)
	}

	// PSYNC full resync when offset is out of range or malformed
	gotOutOfRange := runStrings(exec, "PSYNC", replID, "99999")
	if gotOutOfRange != wantPrefix {
		t.Fatalf("PSYNC out-of-range = %q, want %q", gotOutOfRange, wantPrefix)
	}
	gotMalformed := runStrings(exec, "PSYNC", replID, "not-a-number")
	if gotMalformed != wantPrefix {
		t.Fatalf("PSYNC malformed = %q, want %q", gotMalformed, wantPrefix)
	}
}
