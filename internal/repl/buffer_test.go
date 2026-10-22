package repl

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSyncBufferAppendAndDrain(t *testing.T) {
	buf := NewSyncBuffer(1024)
	if buf.Buffered() != 0 {
		t.Fatalf("expected 0 buffered bytes, got %d", buf.Buffered())
	}

	// 1. Drain on empty buffer returns nil, nil
	drained, err := buf.Drain()
	if err != nil || drained != nil {
		t.Fatalf("expected nil on empty drain, got %v, %v", drained, err)
	}

	// 2. Append multiple payloads
	p1 := []byte("*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n")
	p2 := []byte("*3\r\n$3\r\nSET\r\n$1\r\nb\r\n$1\r\n2\r\n")
	if err := buf.Append(p1); err != nil {
		t.Fatalf("append p1: %v", err)
	}
	if err := buf.Append(p2); err != nil {
		t.Fatalf("append p2: %v", err)
	}

	expectedLen := int64(len(p1) + len(p2))
	if buf.Buffered() != expectedLen {
		t.Fatalf("buffered = %d, want %d", buf.Buffered(), expectedLen)
	}

	// 3. Drain and verify content
	drained, err = buf.Drain()
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want := append(append([]byte(nil), p1...), p2...)
	if !bytes.Equal(drained, want) {
		t.Fatalf("drained = %q, want %q", drained, want)
	}

	// 4. Verify buffer reset after drain
	if buf.Buffered() != 0 {
		t.Fatalf("buffered after drain = %d, want 0", buf.Buffered())
	}
}

func TestSyncBufferDrainChunks(t *testing.T) {
	buf := NewSyncBuffer(1024)
	c1 := []byte("chunk1")
	c2 := []byte("chunk2")
	_ = buf.Append(c1)
	_ = buf.Append(c2)

	chunks, err := buf.DrainChunks()
	if err != nil {
		t.Fatalf("drain chunks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if !bytes.Equal(chunks[0], c1) || !bytes.Equal(chunks[1], c2) {
		t.Fatalf("chunks mismatch: got %v, want [%q, %q]", chunks, c1, c2)
	}
	if buf.Buffered() != 0 {
		t.Fatalf("buffered = %d after DrainChunks, want 0", buf.Buffered())
	}
}

func TestSyncBufferOverflow(t *testing.T) {
	capBytes := int64(64)
	buf := NewSyncBuffer(capBytes)

	// Fill right up to limit: 40 bytes
	p1 := make([]byte, 40)
	if err := buf.Append(p1); err != nil {
		t.Fatalf("append 40 bytes: %v", err)
	}
	if buf.Overflow() {
		t.Fatalf("unexpected overflow at 40/%d bytes", capBytes)
	}

	// Append that exceeds remaining 24 bytes -> triggers overflow
	p2 := make([]byte, 30)
	err := buf.Append(p2)
	if !errors.Is(err, ErrSyncBufferOverflow) {
		t.Fatalf("expected ErrSyncBufferOverflow, got %v", err)
	}

	if !buf.Overflow() {
		t.Fatal("expected Overflow() to be true")
	}

	// Further appends should be rejected with ErrSyncBufferOverflow
	if err := buf.Append([]byte("x")); !errors.Is(err, ErrSyncBufferOverflow) {
		t.Fatalf("append after overflow = %v, want ErrSyncBufferOverflow", err)
	}

	// Drain after overflow should return ErrSyncBufferOverflow
	if _, err := buf.Drain(); !errors.Is(err, ErrSyncBufferOverflow) {
		t.Fatalf("drain after overflow = %v, want ErrSyncBufferOverflow", err)
	}
	if _, err := buf.DrainChunks(); !errors.Is(err, ErrSyncBufferOverflow) {
		t.Fatalf("drain chunks after overflow = %v, want ErrSyncBufferOverflow", err)
	}
}

func TestSyncBufferClose(t *testing.T) {
	buf := NewSyncBuffer(1024)
	_ = buf.Append([]byte("data"))
	buf.Close()

	if err := buf.Append([]byte("more")); !errors.Is(err, ErrSyncBufferClosed) {
		t.Fatalf("append to closed buffer = %v, want ErrSyncBufferClosed", err)
	}
	if buf.Buffered() != 0 {
		t.Fatalf("buffered after close = %d, want 0", buf.Buffered())
	}
}

func TestSyncBufferConcurrent(t *testing.T) {
	buf := NewSyncBuffer(10 << 20) // 10MB
	var wg sync.WaitGroup

	numWriters := 8
	itemsPerWriter := 200
	payload := []byte("*3\r\n$3\r\nSET\r\n$3\r\nfoo\r\n$3\r\nbar\r\n")

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < itemsPerWriter; i++ {
				if err := buf.Append(payload); err != nil {
					t.Errorf("concurrent append: %v", err)
					return
				}
			}
		}()
	}

	// Concurrent reader draining periodically
	var drainedTotal atomic.Int64
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				d, _ := buf.Drain()
				drainedTotal.Add(int64(len(d)))
				return
			default:
				d, _ := buf.Drain()
				drainedTotal.Add(int64(len(d)))
			}
		}
	}()

	wg.Wait()
	close(done)

	wantTotal := int64(numWriters * itemsPerWriter * len(payload))
	finalDrain, _ := buf.Drain()
	drainedTotal.Add(int64(len(finalDrain)))

	if drainedTotal.Load() != wantTotal {
		t.Fatalf("drained total = %d, want %d", drainedTotal.Load(), wantTotal)
	}
}
