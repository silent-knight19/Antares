package repl

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrOffsetOutOfRange is returned when the requested offset has already been overwritten in the backlog.
	ErrOffsetOutOfRange = errors.New("replication offset has fallen out of backlog window")
	// ErrOffsetAhead is returned when the requested offset is greater than the master's current offset.
	ErrOffsetAhead = errors.New("replication offset is ahead of master offset")
	// ErrNegativeOffset is returned when an invalid negative offset is requested.
	ErrNegativeOffset = errors.New("negative replication offset")
	// ErrBacklogEmpty is returned when slicing an empty backlog for non-zero offset.
	ErrBacklogEmpty = errors.New("backlog is empty")
)

// Backlog implements the 1MB replication ring buffer (ADR-007, PLAN §5).
// It maintains the contiguous stream of executed mutation commands, allowing
// disconnected replicas to catch up via partial resynchronization (+CONTINUE)
// without requiring a full dataset transfer.
type Backlog struct {
	mu sync.RWMutex

	buf             []byte
	size            int64 // capacity in bytes (default 1MB = 1048576)
	offset          int64 // master_repl_offset (total bytes written since master start)
	firstByteOffset int64 // absolute stream offset of the oldest byte in buffer
	histlen         int64 // current number of valid bytes in the buffer
}

// NewBacklog creates a replication backlog buffer of the specified size.
// If size <= 0, DefaultBacklogSize (1048576 = 1MB) is used.
func NewBacklog(size int64) *Backlog {
	if size <= 0 {
		size = DefaultBacklogSize
	}
	return &Backlog{
		buf:             make([]byte, size),
		size:            size,
		offset:          0,
		firstByteOffset: 0,
		histlen:         0,
	}
}

// Write appends bytes to the replication backlog ring buffer, advancing the
// master stream offset and updating firstByteOffset / histlen accordingly.
func (b *Backlog) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	pn := int64(n)

	if pn >= b.size {
		// Payload is larger than or equal to total buffer capacity.
		// Retain only the trailing b.size bytes.
		tail := p[pn-b.size:]
		newOffset := b.offset + pn
		writeStart := (newOffset - b.size) % b.size

		part1 := b.size - writeStart
		copy(b.buf[writeStart:], tail[:part1])
		if part1 < b.size {
			copy(b.buf[:writeStart], tail[part1:])
		}

		b.offset = newOffset
		b.firstByteOffset = newOffset - b.size + 1
		b.histlen = b.size
		return n, nil
	}

	// Normal append: payload fits within capacity.
	startIdx := b.offset % b.size
	part1 := min(pn, b.size-startIdx)
	copy(b.buf[startIdx:startIdx+part1], p[:part1])
	if part1 < pn {
		copy(b.buf[:pn-part1], p[part1:])
	}

	b.offset += pn
	if b.offset > b.size {
		b.firstByteOffset = b.offset - b.size + 1
		b.histlen = b.size
	} else {
		b.firstByteOffset = 1
		b.histlen = b.offset
	}

	return n, nil
}

// CanPartialResync checks if the replica's acknowledged offset can be served
// from the current backlog window. In Redis replication, the replica supplies
// the offset of the last byte it processed; the master must provide bytes
// starting at reqOffset + 1.
func (b *Backlog) CanPartialResync(reqOffset int64) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if reqOffset < 0 {
		return false
	}
	if b.histlen == 0 {
		return reqOffset == 0
	}
	if reqOffset > b.offset {
		return false
	}
	if reqOffset == b.offset {
		// Replica is caught up with master.
		return true
	}
	// The next byte needed is reqOffset + 1.
	return reqOffset+1 >= b.firstByteOffset
}

// Slice extracts the contiguous replication stream bytes from reqOffset + 1
// to the master's current stream offset.
// If reqOffset == master offset, an empty slice is returned.
// If reqOffset cannot be served, an error is returned.
func (b *Backlog) Slice(reqOffset int64) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if reqOffset < 0 {
		return nil, ErrNegativeOffset
	}
	if b.histlen == 0 {
		if reqOffset == 0 {
			return []byte{}, nil
		}
		return nil, ErrBacklogEmpty
	}
	if reqOffset > b.offset {
		return nil, fmt.Errorf("%w: req %d > master %d", ErrOffsetAhead, reqOffset, b.offset)
	}
	if reqOffset == b.offset {
		return []byte{}, nil
	}

	startOffset := reqOffset + 1
	if startOffset < b.firstByteOffset {
		return nil, fmt.Errorf("%w: req start %d < first %d", ErrOffsetOutOfRange, startOffset, b.firstByteOffset)
	}

	length := b.offset - startOffset + 1
	out := make([]byte, length)

	startIdx := (startOffset - 1) % b.size
	if startIdx+length <= b.size {
		copy(out, b.buf[startIdx:startIdx+length])
	} else {
		part1 := b.size - startIdx
		copy(out[:part1], b.buf[startIdx:])
		copy(out[part1:], b.buf[:length-part1])
	}

	return out, nil
}

// Offset returns the current master replication offset.
func (b *Backlog) Offset() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.offset
}

// FirstByteOffset returns the offset of the first byte in the backlog.
func (b *Backlog) FirstByteOffset() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.firstByteOffset
}

// Histlen returns the number of valid bytes in the backlog.
func (b *Backlog) Histlen() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.histlen
}

// Size returns the capacity of the backlog ring buffer.
func (b *Backlog) Size() int64 {
	return b.size
}

// Reset clears the backlog, resetting offset and history counters.
func (b *Backlog) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.offset = 0
	b.firstByteOffset = 0
	b.histlen = 0
}
