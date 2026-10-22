package repl

import (
	"bytes"
	"io"
	"testing"

	"github.com/silent-knight19/antares/internal/protocol"
)

func TestTrackedReaderByteAccuracy(t *testing.T) {
	cmd1 := []byte("*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n") // 31 bytes
	cmd2 := []byte("*1\r\n$4\r\nPING\r\n")                        // 14 bytes
	cmd3 := []byte("*2\r\n$3\r\nDEL\r\n$3\r\nfoo\r\n")            // 21 bytes

	var stream bytes.Buffer
	stream.Write(cmd1)
	stream.Write(cmd2)
	stream.Write(cmd3)

	// Use small buffer (16 bytes) to force multiple underlying refills
	tr := NewTrackedReader(&stream, 16)
	if tr.Consumed() != 0 {
		t.Fatalf("initial consumed = %d, want 0", tr.Consumed())
	}

	// Read cmd1
	before := tr.Consumed()
	req1, err := protocol.ReadRequest(tr.Reader())
	if err != nil {
		t.Fatalf("ReadRequest cmd1: %v", err)
	}
	consumed1 := tr.Consumed() - before
	if consumed1 != int64(len(cmd1)) {
		t.Fatalf("consumed1 = %d, want %d", consumed1, len(cmd1))
	}
	if string(req1[0]) != "SET" {
		t.Fatalf("req1 = %v", req1)
	}

	// Read cmd2
	before = tr.Consumed()
	req2, err := protocol.ReadRequest(tr.Reader())
	if err != nil {
		t.Fatalf("ReadRequest cmd2: %v", err)
	}
	consumed2 := tr.Consumed() - before
	if consumed2 != int64(len(cmd2)) {
		t.Fatalf("consumed2 = %d, want %d", consumed2, len(cmd2))
	}
	if string(req2[0]) != "PING" {
		t.Fatalf("req2 = %v", req2)
	}

	// Peek without consuming
	_, _ = tr.Reader().Peek(4)
	if tr.Consumed() != int64(len(cmd1)+len(cmd2)) {
		t.Fatalf("peek changed consumed count: got %d, want %d", tr.Consumed(), len(cmd1)+len(cmd2))
	}

	// Read cmd3
	before = tr.Consumed()
	req3, err := protocol.ReadRequest(tr.Reader())
	if err != nil {
		t.Fatalf("ReadRequest cmd3: %v", err)
	}
	consumed3 := tr.Consumed() - before
	if consumed3 != int64(len(cmd3)) {
		t.Fatalf("consumed3 = %d, want %d", consumed3, len(cmd3))
	}
	if string(req3[0]) != "DEL" {
		t.Fatalf("req3 = %v", req3)
	}

	totalConsumed := tr.Consumed()
	wantTotal := int64(len(cmd1) + len(cmd2) + len(cmd3))
	if totalConsumed != wantTotal {
		t.Fatalf("total consumed = %d, want %d", totalConsumed, wantTotal)
	}

	// Read at EOF
	_, err = protocol.ReadRequest(tr.Reader())
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}
