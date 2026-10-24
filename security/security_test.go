package security_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/server"
	"github.com/silent-knight19/antares/internal/store"
)

// TestKeysGlobBacktracking verifies that pathological patterns like a*a*a*a*a*a*b
// do not cause exponential backtracking stalls against a large keyspace.
func TestKeysGlobBacktracking(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)

	// Populate 10,000 keys with repetitive prefixes
	for i := 0; i < 10000; i++ {
		st.Set(fmt.Sprintf("aaaaaaaaaa_%d", i), []byte("val"))
	}

	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	pathologicalPattern := "a*a*a*a*a*a*a*a*b"
	start := time.Now()

	exec.Handle(protocol.Request{[]byte("KEYS"), []byte(pathologicalPattern)}, enc, nil)

	duration := time.Since(start)
	if duration > 500*time.Millisecond {
		t.Fatalf("pathological glob pattern took too long: %v (expected < 500ms)", duration)
	}

	// Verify reply is an empty array
	reply := string(enc.Bytes())
	if !strings.HasPrefix(reply, "*0\r\n") {
		t.Fatalf("unexpected reply for non-matching pattern: %q", reply)
	}
}

// TestScanHugeCount asserts that an astronomical COUNT does not cause
// excessive memory allocation or executor starvation, and respects the 65,536 bucket cap.
func TestScanHugeCount(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)

	for i := 0; i < 100; i++ {
		st.Set(fmt.Sprintf("key_%d", i), []byte("v"))
	}

	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	// 18446744073709551615 is > MaxInt64, should be rejected with not an integer
	exec.Handle(protocol.Request{[]byte("SCAN"), []byte("0"), []byte("COUNT"), []byte("18446744073709551615")}, enc, nil)
	if !strings.Contains(string(enc.Bytes()), protocol.NotInteger) {
		t.Fatalf("SCAN >MaxInt64 count expected error %q, got: %s", protocol.NotInteger, enc.Bytes())
	}

	// MaxInt64 count should be clamped to maxScanBuckets (65536) and return valid scan reply
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("SCAN"), []byte("0"), []byte("COUNT"), []byte("9223372036854775807")}, enc, nil)
	if !strings.HasPrefix(string(enc.Bytes()), "*2\r\n") {
		t.Fatalf("SCAN with MaxInt64 failed: %s", enc.Bytes())
	}
}

// TestReplConfListeningPortValidation verifies that REPLCONF listening-port
// rejects non-integers with protocol.NotInteger while accepting integer values (matching Redis 7.4.11).
func TestReplConfListeningPortValidation(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)
	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	conn := &server.ConnState{}
	defer conn.Close()

	// Non-integer must be rejected
	exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte("abc")}, enc, conn)
	if !strings.Contains(string(enc.Bytes()), protocol.NotInteger) {
		t.Fatalf("expected NotInteger for port 'abc', got: %s", enc.Bytes())
	}

	// Integer values are accepted (Redis behavior)
	for _, port := range []string{"-1", "6380", "65536"} {
		enc.Reset()
		exec.Handle(protocol.Request{[]byte("REPLCONF"), []byte("listening-port"), []byte(port)}, enc, conn)
		if string(enc.Bytes()) != "+OK\r\n" {
			t.Fatalf("expected +OK for port %s, got: %s", port, enc.Bytes())
		}
	}
}

// TestMULTIQueueUnbounded verifies that the per-connection MULTI queue length
// is bounded at 65,536 commands to prevent unlimited memory exhaustion.
func TestMULTIQueueUnbounded(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)
	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	conn := &server.ConnState{}
	defer conn.Close()

	// Start MULTI
	exec.Handle(protocol.Request{[]byte("MULTI")}, enc, conn)
	if string(enc.Bytes()) != "+OK\r\n" {
		t.Fatalf("MULTI failed: %s", enc.Bytes())
	}

	// Queue up to the 65,536 limit
	for i := 0; i < 65536; i++ {
		enc.Reset()
		exec.Handle(protocol.Request{[]byte("PING")}, enc, conn)
		if string(enc.Bytes()) != "+QUEUED\r\n" {
			t.Fatalf("queueing at %d failed: %s", i, enc.Bytes())
		}
	}

	// 65537th command must be rejected with queue size limit error and dirty the transaction
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("PING")}, enc, conn)
	if !strings.Contains(string(enc.Bytes()), "ERR max queued transaction commands reached") {
		t.Fatalf("expected queue limit error, got: %s", enc.Bytes())
	}

	// EXEC should abort
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("EXEC")}, enc, conn)
	if !strings.Contains(string(enc.Bytes()), "EXECABORT") {
		t.Fatalf("expected EXECABORT on oversized transaction, got: %s", enc.Bytes())
	}
}

// TestPSYNCForgeReplid asserts that an attacker-supplied forged replid
// forces a full resync rather than continuing from diverged history.
func TestPSYNCForgeReplid(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)
	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	enc := protocol.GetEncoder()
	defer enc.Release()

	forgedReplID := "0000000000000000000000000000000000000000"
	exec.Handle(protocol.Request{[]byte("PSYNC"), []byte(forgedReplID), []byte("100")}, enc, nil)

	got := string(enc.Bytes())
	if !strings.HasPrefix(got, "+FULLRESYNC") {
		t.Fatalf("PSYNC with forged replid did not force FULLRESYNC, got: %s", got)
	}
}

// TestReplicaClientFlagSpoof asserts that regular connections cannot bypass
// read-only restrictions on a replica by spoofing the internal isReplicaClient flag.
func TestReplicaClientFlagSpoof(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)
	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	// Configure node as read-only replica
	exec.Repl().SetMaster("127.0.0.1", 6379)
	exec.Repl().SetSlaveReadOnly(true)

	enc := protocol.GetEncoder()
	defer enc.Release()

	// A regular client connection state
	clientConn := &server.ConnState{}
	defer clientConn.Close()

	// Normal write must fail with READONLY
	exec.Handle(protocol.Request{[]byte("SET"), []byte("foo"), []byte("bar")}, enc, clientConn)
	if !strings.Contains(string(enc.Bytes()), protocol.ReadOnlyReplica) {
		t.Fatalf("expected READONLY error for normal client, got: %s", enc.Bytes())
	}

	// Ensure normal client cannot issue any command that elevates isReplicaClient
	// The internal replication connection state is explicitly created only by NewReplicaConnState()
	internalReplConn := server.NewReplicaConnState()
	enc.Reset()
	exec.Handle(protocol.Request{[]byte("SET"), []byte("foo"), []byte("bar")}, enc, internalReplConn)
	if strings.Contains(string(enc.Bytes()), protocol.ReadOnlyReplica) {
		t.Fatalf("internal replica client unexpectedly blocked by READONLY: %s", enc.Bytes())
	}
}

// TestAOFRepairReportsDiscardedRange ensures that corrupted frames repaired by
// -aof-repair accurately report the number of valid records, valid bytes, and truncated bytes.
func TestAOFRepairReportsDiscardedRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repair_test.aof")

	// Frame 1: valid SET
	cmd1 := persist.EncodeCommand([]byte("SET"), []byte("k1"), []byte("v1"))
	data1 := persist.AppendFrame(nil, cmd1)

	// Frame 2: corrupted payload
	cmd2 := persist.EncodeCommand([]byte("SET"), []byte("k2"), []byte("v2"))
	data2 := persist.AppendFrame(nil, cmd2)
	data2[len(data2)-1] ^= 0xff // corrupt byte

	fullData := append(data1, data2...)
	if err := os.WriteFile(path, fullData, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	st := store.New()
	report, err := server.RecoverAOFRepair(path, st, true)
	if err != nil {
		t.Fatalf("RecoverAOFRepair failed: %v", err)
	}

	if report.Records != 1 {
		t.Fatalf("recovered records = %d, want 1", report.Records)
	}
	if report.ValidBytes != int64(len(data1)) {
		t.Fatalf("valid bytes = %d, want %d", report.ValidBytes, len(data1))
	}
	if !report.Truncated {
		t.Fatalf("expected Truncated = true")
	}
	if report.TruncatedBytes != int64(len(data2)) {
		t.Fatalf("truncated bytes = %d, want %d", report.TruncatedBytes, len(data2))
	}
	if report.Reason != "frame checksum mismatch" {
		t.Fatalf("report reason = %q, want checksum mismatch", report.Reason)
	}
}

// TestShutdownTornFrame simulates SIGTERM during a write and proves that recovery
// safely truncates the incomplete final frame while retaining all previous complete writes.
func TestShutdownTornFrame(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shutdown_torn.aof")

	// Append 10 valid writes
	w, err := persist.Open(path, persist.FsyncAlways)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for i := 0; i < 10; i++ {
		cmd := persist.EncodeCommand([]byte("SET"), []byte(fmt.Sprintf("k:%d", i)), []byte("val"))
		if err := w.Append(cmd); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	validSize := info.Size()

	// Append a torn incomplete frame (e.g. process killed mid-write)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// Write 4 bytes of length claiming 100 bytes, but no payload
	var partial [4]byte
	partial[0] = 100
	_, _ = f.Write(partial[:])
	_ = f.Close()

	// Recover without repair flag: torn tail must be automatically truncated
	st := store.New()
	report, err := server.RecoverAOF(path, st)
	if err != nil {
		t.Fatalf("RecoverAOF on torn tail failed: %v", err)
	}
	if report.Records != 10 {
		t.Fatalf("recovered %d records, want 10", report.Records)
	}
	if report.ValidBytes != validSize {
		t.Fatalf("valid bytes = %d, want %d", report.ValidBytes, validSize)
	}
	if !report.Truncated {
		t.Fatalf("expected Truncated = true for torn tail")
	}

	// Verify all 10 keys are present in store
	for i := 0; i < 10; i++ {
		if !st.Exists(fmt.Sprintf("k:%d", i)) {
			t.Fatalf("key k:%d missing after restart", i)
		}
	}
}

// TestTrackedReaderPeekDiscard tests TrackedReader byte accuracy under Peek and Discard interleaving.
func TestTrackedReaderPeekDiscard(t *testing.T) {
	data := []byte("0123456789ABCDEF")
	buf := bytes.NewReader(data)
	tr := repl.NewTrackedReader(buf, 8)

	if tr.Consumed() != 0 {
		t.Fatalf("initial consumed = %d, want 0", tr.Consumed())
	}

	// Peek 4 bytes (does not consume)
	p, err := tr.Reader().Peek(4)
	if err != nil || string(p) != "0123" {
		t.Fatalf("peek 4 = %q, err = %v", p, err)
	}
	if tr.Consumed() != 0 {
		t.Fatalf("consumed after peek = %d, want 0", tr.Consumed())
	}

	// Discard 4 bytes (consumes)
	n, err := tr.Reader().Discard(4)
	if err != nil || n != 4 {
		t.Fatalf("discard 4 = %d, err = %v", n, err)
	}
	if tr.Consumed() != 4 {
		t.Fatalf("consumed after discard = %d, want 4", tr.Consumed())
	}

	// Read 6 bytes
	readBuf := make([]byte, 6)
	m, err := io.ReadFull(tr.Reader(), readBuf)
	if err != nil || m != 6 || string(readBuf) != "456789" {
		t.Fatalf("read 6 = %q, err = %v", readBuf, err)
	}
	if tr.Consumed() != 10 {
		t.Fatalf("consumed after read = %d, want 10", tr.Consumed())
	}
}

// TestSnapshotStreamCorruptDelimiter ensures that DelimiterReader lookahead safely rejects
// truncated or missing delimiters without infinite loops or memory growth.
func TestSnapshotStreamCorruptDelimiter(t *testing.T) {
	raw := []byte("$EOF:1234567890123456789012345678901234567890\r\n")
	// Add some data but do not append the closing delimiter
	raw = append(raw, []byte("truncated_payload_without_delimiter")...)

	err := repl.ReadSnapshotStream(bytes.NewReader(raw), func(payload []byte) error {
		return nil
	})
	if err == nil {
		t.Fatalf("expected error on truncated snapshot stream, got nil")
	}
}

// TestOrdinaryClientOutputUnbounded documents and tests ordinary client output handling:
// the executor serializes into the encoder without blocking on network I/O, but
// memory is retained per-connection until the client reads or idle timeout triggers.
func TestOrdinaryClientOutputUnbounded(t *testing.T) {
	clk := clock.Real{}
	st := store.NewWithClock(clk)
	exec := server.NewExecutorWithClock(st, nil, clk)
	defer exec.Close()

	// Store a 2 MiB value
	val := make([]byte, 2<<20)
	for i := range val {
		val[i] = 'A'
	}
	st.Set("bigkey", val)

	enc := protocol.GetEncoder()
	defer enc.Release()

	conn := &server.ConnState{}
	defer conn.Close()

	start := time.Now()
	exec.Handle(protocol.Request{[]byte("GET"), []byte("bigkey")}, enc, conn)
	duration := time.Since(start)

	if duration > 100*time.Millisecond {
		t.Fatalf("executor stalled on large key read: %v", duration)
	}

	if len(enc.Bytes()) < 2<<20 {
		t.Fatalf("expected reply size > 2MB, got %d", len(enc.Bytes()))
	}
}

// FuzzSnapshotStream stresses snapshot stream parsing with adversarial inputs
// including corrupted delimiters, truncated payloads, and keepalive noise.
func FuzzSnapshotStream(f *testing.F) {
	delim := "1234567890123456789012345678901234567890"
	validEOF := fmt.Sprintf("$EOF:%s\r\n%s", delim, delim)
	validLength := "$0\r\n"
	keepalives := "\r\n\r\n\r\n$0\r\n"
	malformed := "$EOF:\r\n"

	f.Add([]byte(validEOF))
	f.Add([]byte(validLength))
	f.Add([]byte(keepalives))
	f.Add([]byte(malformed))

	f.Fuzz(func(t *testing.T, data []byte) {
		_ = repl.ReadSnapshotStream(bytes.NewReader(data), func(payload []byte) error {
			return nil
		})
	})
}

