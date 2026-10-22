package server

import (
	"strings"
	"testing"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/persist"
)

func TestReplicationPropagationStreamAndOffsets(t *testing.T) {
	clk := clock.Real{}
	exec := NewExecutorWithClock(nil, nil, clk)
	defer exec.Close()

	repl := exec.Repl()
	if repl == nil {
		t.Fatalf("expected non-nil repl.State")
	}

	// 1. Initial master state
	if repl.MasterReplOffset() != 0 {
		t.Fatalf("initial offset = %d, want 0", repl.MasterReplOffset())
	}

	info := runStrings(exec, "INFO", "replication")
	if !strings.Contains(info, "master_repl_offset:0") ||
		!strings.Contains(info, "repl_backlog_active:1") ||
		!strings.Contains(info, "repl_backlog_first_byte_offset:0") ||
		!strings.Contains(info, "repl_backlog_histlen:0") {
		t.Fatalf("initial INFO mismatch:\n%s", info)
	}

	// 2. Read commands do NOT increment replication offset
	runStrings(exec, "PING")
	runStrings(exec, "GET", "nonexistent")
	if repl.MasterReplOffset() != 0 {
		t.Fatalf("offset after reads = %d, want 0", repl.MasterReplOffset())
	}

	// 3. Non-mutating write does NOT increment offset
	runStrings(exec, "DEL", "nonexistent")
	if repl.MasterReplOffset() != 0 {
		t.Fatalf("offset after no-op DEL = %d, want 0", repl.MasterReplOffset())
	}

	// 4. Mutating SET command increments offset by exact encoded length
	expectedCmd := persist.EncodeCommand([]byte("SET"), []byte("foo"), []byte("bar"))
	cmdLen := int64(len(expectedCmd))
	if cmdLen != 31 {
		t.Fatalf("expected 31 bytes for SET foo bar, got %d", cmdLen)
	}

	if got := runStrings(exec, "SET", "foo", "bar"); got != "+OK\r\n" {
		t.Fatalf("SET = %q, want +OK", got)
	}

	if repl.MasterReplOffset() != cmdLen {
		t.Fatalf("offset after SET = %d, want %d", repl.MasterReplOffset(), cmdLen)
	}

	// Check backlog content
	slice, err := repl.BacklogSlice(0)
	if err != nil {
		t.Fatalf("BacklogSlice(0) err: %v", err)
	}
	if string(slice) != string(expectedCmd) {
		t.Fatalf("backlog slice = %q, want %q", slice, expectedCmd)
	}

	// INFO reflection
	info = runStrings(exec, "INFO", "replication")
	if !strings.Contains(info, "master_repl_offset:31") ||
		!strings.Contains(info, "repl_backlog_first_byte_offset:1") ||
		!strings.Contains(info, "repl_backlog_histlen:31") {
		t.Fatalf("INFO after SET mismatch:\n%s", info)
	}

	// 5. Mutating HSET increments offset further
	hsetExpected := persist.EncodeCommand([]byte("HSET"), []byte("myhash"), []byte("field1"), []byte("val1"))
	hsetLen := int64(len(hsetExpected))

	runStrings(exec, "HSET", "myhash", "field1", "val1")
	expectedTotal := cmdLen + hsetLen
	if repl.MasterReplOffset() != expectedTotal {
		t.Fatalf("offset after HSET = %d, want %d", repl.MasterReplOffset(), expectedTotal)
	}

	// Slicing from offset 31 yields only HSET
	hsetSlice, err := repl.BacklogSlice(cmdLen)
	if err != nil {
		t.Fatalf("BacklogSlice(31) err: %v", err)
	}
	if string(hsetSlice) != string(hsetExpected) {
		t.Fatalf("hset slice = %q, want %q", hsetSlice, hsetExpected)
	}

	// Slicing from 0 yields both commands
	fullSlice, err := repl.BacklogSlice(0)
	if err != nil {
		t.Fatalf("BacklogSlice(0) err: %v", err)
	}
	if string(fullSlice) != string(expectedCmd)+string(hsetExpected) {
		t.Fatalf("full slice length = %d, want %d", len(fullSlice), len(expectedCmd)+len(hsetExpected))
	}

	// 6. MULTI/EXEC transaction propagation
	runStrings(exec, "MULTI")
	runStrings(exec, "SET", "tx1", "val1")
	runStrings(exec, "SET", "tx2", "val2")
	runStrings(exec, "EXEC")

	tx1Expected := persist.EncodeCommand([]byte("SET"), []byte("tx1"), []byte("val1"))
	tx2Expected := persist.EncodeCommand([]byte("SET"), []byte("tx2"), []byte("val2"))
	expectedTxTotal := expectedTotal + int64(len(tx1Expected)) + int64(len(tx2Expected))

	if repl.MasterReplOffset() != expectedTxTotal {
		t.Fatalf("offset after TX = %d, want %d", repl.MasterReplOffset(), expectedTxTotal)
	}

	// CanPartialResync
	if !repl.CanPartialResync(0) {
		t.Fatalf("CanPartialResync(0) should be true")
	}
	if !repl.CanPartialResync(expectedTotal) {
		t.Fatalf("CanPartialResync at intermediate offset should be true")
	}
	if !repl.CanPartialResync(expectedTxTotal) {
		t.Fatalf("CanPartialResync at current offset should be true")
	}
	if repl.CanPartialResync(expectedTxTotal + 10) {
		t.Fatalf("CanPartialResync ahead of offset should be false")
	}
}

func TestSpopPropagationWithoutAOF(t *testing.T) {
	// AOF is nil here by using NewExecutor(nil, nil)
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	runStrings(exec, "SADD", "myset", "a", "b", "c")
	offsetBefore := exec.Repl().MasterReplOffset()

	runStrings(exec, "SPOP", "myset")
	offsetAfter := exec.Repl().MasterReplOffset()

	if offsetAfter <= offsetBefore {
		t.Fatalf("expected SPOP to advance offset without AOF, before=%d, after=%d", offsetBefore, offsetAfter)
	}

	slice, err := exec.Repl().BacklogSlice(offsetBefore)
	if err != nil {
		t.Fatalf("BacklogSlice err: %v", err)
	}
	if !strings.Contains(string(slice), "SREM") {
		t.Fatalf("expected SREM in backlog stream for SPOP, got %q", string(slice))
	}
}
