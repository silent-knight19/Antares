package repl

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestBacklogSmallBufferWrapping(t *testing.T) {
	// 10-byte buffer
	b := NewBacklog(10)

	if b.Offset() != 0 || b.FirstByteOffset() != 0 || b.Histlen() != 0 {
		t.Fatalf("unexpected initial state: offset=%d first=%d hist=%d",
			b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// 1. Write 4 bytes: "abcd"
	n, err := b.Write([]byte("abcd"))
	if err != nil || n != 4 {
		t.Fatalf("write 4: n=%d err=%v", n, err)
	}
	if b.Offset() != 4 || b.FirstByteOffset() != 1 || b.Histlen() != 4 {
		t.Fatalf("after abcd: offset=%d first=%d hist=%d",
			b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// 2. Write 4 bytes: "efgh"
	n, err = b.Write([]byte("efgh"))
	if err != nil || n != 4 {
		t.Fatalf("write 4: n=%d err=%v", n, err)
	}
	if b.Offset() != 8 || b.FirstByteOffset() != 1 || b.Histlen() != 8 {
		t.Fatalf("after efgh: offset=%d first=%d hist=%d",
			b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// Slicing from offset 4 (bytes 5..8 = "efgh")
	slice, err := b.Slice(4)
	if err != nil || string(slice) != "efgh" {
		t.Fatalf("slice from 4 = %q (err=%v), want efgh", slice, err)
	}

	// Slicing from offset 0 (bytes 1..8 = "abcdefgh")
	slice, err = b.Slice(0)
	if err != nil || string(slice) != "abcdefgh" {
		t.Fatalf("slice from 0 = %q (err=%v), want abcdefgh", slice, err)
	}

	// 3. Write 4 bytes: "ijkl" -> wraps around 10-byte buffer!
	// Total bytes written: 12. Buffer size: 10.
	// Retained bytes: 3..12 = "cdefghijkl"
	n, err = b.Write([]byte("ijkl"))
	if err != nil || n != 4 {
		t.Fatalf("write 4: n=%d err=%v", n, err)
	}
	if b.Offset() != 12 || b.FirstByteOffset() != 3 || b.Histlen() != 10 {
		t.Fatalf("after ijkl: offset=%d first=%d hist=%d (want 12, 3, 10)",
			b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// Slicing from offset 2 (startOffset=3, all 10 bytes = "cdefghijkl")
	slice, err = b.Slice(2)
	if err != nil || string(slice) != "cdefghijkl" {
		t.Fatalf("slice from 2 = %q (err=%v), want cdefghijkl", slice, err)
	}

	// Slicing from offset 5 (startOffset=6, 7 bytes = "fghijkl")
	slice, err = b.Slice(5)
	if err != nil || string(slice) != "fghijkl" {
		t.Fatalf("slice from 5 = %q (err=%v), want fghijkl", slice, err)
	}

	// Slicing from offset 12 (already up to date) -> empty slice
	slice, err = b.Slice(12)
	if err != nil || len(slice) != 0 {
		t.Fatalf("slice from 12 = %q (err=%v), want empty", slice, err)
	}

	// Slicing from offset 1 (byte 2 has expired from backlog) -> error
	if b.CanPartialResync(1) {
		t.Fatalf("CanPartialResync(1) should be false (firstByteOffset=3)")
	}
	if _, err := b.Slice(1); err == nil {
		t.Fatalf("slice from 1 should error")
	}
}

func TestBacklogLargeWrite(t *testing.T) {
	b := NewBacklog(8)

	// Write 15 bytes in one call (larger than capacity 8)
	payload := []byte("0123456789ABCDE")
	n, err := b.Write(payload)
	if err != nil || n != 15 {
		t.Fatalf("write 15: n=%d err=%v", n, err)
	}

	if b.Offset() != 15 || b.FirstByteOffset() != 8 || b.Histlen() != 8 {
		t.Fatalf("after 15B write: offset=%d first=%d hist=%d (want 15, 8, 8)",
			b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// Buffer holds bytes 8..15 = "789ABCDE"
	slice, err := b.Slice(7)
	if err != nil || string(slice) != "789ABCDE" {
		t.Fatalf("slice from 7 = %q (err=%v), want 789ABCDE", slice, err)
	}
}

func TestBacklog1MBCapacity(t *testing.T) {
	b := NewBacklog(DefaultBacklogSize) // 1MB
	if b.Size() != 1048576 {
		t.Fatalf("size = %d, want 1048576", b.Size())
	}

	// Fill with 500KB
	chunk := bytes.Repeat([]byte("A"), 500000)
	_, _ = b.Write(chunk)
	if b.Offset() != 500000 || b.FirstByteOffset() != 1 || b.Histlen() != 500000 {
		t.Fatalf("unexpected state after 500KB: %d, %d, %d", b.Offset(), b.FirstByteOffset(), b.Histlen())
	}

	// Fill another 600KB -> total 1.1MB, wrapping past 1MB
	chunk2 := bytes.Repeat([]byte("B"), 600000)
	_, _ = b.Write(chunk2)
	totalOffset := int64(1100000)
	if b.Offset() != totalOffset {
		t.Fatalf("offset = %d, want %d", b.Offset(), totalOffset)
	}
	wantFirst := totalOffset - DefaultBacklogSize + 1
	if b.FirstByteOffset() != wantFirst {
		t.Fatalf("firstByteOffset = %d, want %d", b.FirstByteOffset(), wantFirst)
	}
	if b.Histlen() != DefaultBacklogSize {
		t.Fatalf("histlen = %d, want %d", b.Histlen(), DefaultBacklogSize)
	}

	// Can partial resync inside window
	if !b.CanPartialResync(wantFirst) {
		t.Fatalf("CanPartialResync at window boundary should be true")
	}
	if b.CanPartialResync(wantFirst - 2) {
		t.Fatalf("CanPartialResync before window should be false")
	}
	if !b.CanPartialResync(totalOffset) {
		t.Fatalf("CanPartialResync at current offset should be true")
	}
	if b.CanPartialResync(totalOffset + 1) {
		t.Fatalf("CanPartialResync ahead of offset should be false")
	}
}

func TestBacklogConcurrentWritesAndReads(t *testing.T) {
	b := NewBacklog(1024)
	var wg sync.WaitGroup

	// 4 concurrent writers
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				data := []byte(fmt.Sprintf("w%d_step%03d\n", workerID, i))
				_, _ = b.Write(data)
			}
		}(w)
	}

	// 2 concurrent readers
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				off := b.Offset()
				if b.CanPartialResync(off) {
					_, _ = b.Slice(off)
				}
			}
		}()
	}

	wg.Wait()

	if b.Histlen() == 0 || b.Offset() == 0 {
		t.Fatalf("expected non-zero backlog state after concurrent workers")
	}
}
