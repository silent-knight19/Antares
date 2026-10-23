// Package repl implements REPLICAOF, PSYNC full/partial resync, the backlog
// ring buffer, and the replica apply loop (ADR-007, ADR-010).
package repl

import (
	"errors"
	"sync"
)

// DefaultSyncBufferCap is the 64 MiB maximum write tail buffered in memory
// during a full resync snapshot, matching RewriteBufferMax (ADR-010).
const DefaultSyncBufferCap = 64 << 20

// ErrSyncBufferOverflow is returned when concurrent writes during full resync
// exceed the buffer's configured memory ceiling.
var ErrSyncBufferOverflow = errors.New("replication: sync buffer overflow")

// ErrSyncBufferClosed is returned when appending to a closed sync buffer.
var ErrSyncBufferClosed = errors.New("replication: sync buffer closed")

// SyncBuffer is a thread-safe, bounded per-replica write buffer that accumulates
// concurrent mutations while a snapshot is being serialized or streamed to a replica.
// If concurrent writes exceed maxBytes, the buffer enters an overflow state and
// drops accumulated chunks to protect the master's memory (ADR-010).
type SyncBuffer struct {
	mu       sync.Mutex
	chunks   [][]byte
	buffered int64
	maxBytes int64
	overflow bool
	closed   bool
	notify   chan struct{}
	done     chan struct{}
}

// NewSyncBuffer creates a SyncBuffer with the specified byte capacity limit.
// If maxBytes <= 0, DefaultSyncBufferCap (64 MiB) is used.
func NewSyncBuffer(maxBytes int64) *SyncBuffer {
	if maxBytes <= 0 {
		maxBytes = DefaultSyncBufferCap
	}
	return &SyncBuffer{
		maxBytes: maxBytes,
		chunks:   make([][]byte, 0, 16),
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// Append adds payload bytes to the buffer. If adding payload would exceed maxBytes,
// the buffer enters the overflow state and returns ErrSyncBufferOverflow.
func (b *SyncBuffer) Append(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrSyncBufferClosed
	}
	if b.overflow {
		b.notifyLocked()
		return ErrSyncBufferOverflow
	}
	if b.buffered+int64(len(p)) > b.maxBytes {
		b.overflow = true
		b.chunks = nil // free memory immediately on overflow
		b.notifyLocked()
		return ErrSyncBufferOverflow
	}

	chunk := make([]byte, len(p))
	copy(chunk, p)
	b.chunks = append(b.chunks, chunk)
	b.buffered += int64(len(p))
	b.notifyLocked()
	return nil
}

func (b *SyncBuffer) notifyLocked() {
	select {
	case b.notify <- struct{}{}:
	default:
	}
}

// DrainChunks returns all accumulated chunks without flattening into a single slice,
// resetting the buffer.
func (b *SyncBuffer) DrainChunks() ([][]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.overflow {
		return nil, ErrSyncBufferOverflow
	}
	if len(b.chunks) == 0 {
		return nil, nil
	}

	out := b.chunks
	b.chunks = make([][]byte, 0, 16)
	b.buffered = 0
	return out, nil
}

// Buffered returns the total bytes currently buffered.
func (b *SyncBuffer) Buffered() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffered
}

// Overflow reports whether the buffer exceeded its capacity.
func (b *SyncBuffer) Overflow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

// Wait returns a channel that receives a token when bytes are appended.
func (b *SyncBuffer) Wait() <-chan struct{} { return b.notify }

// Done returns a channel closed when the buffer is closed.
func (b *SyncBuffer) Done() <-chan struct{} { return b.done }

// Close marks the buffer as closed and frees all buffered chunks.
func (b *SyncBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.chunks = nil
	b.buffered = 0
	close(b.done)
}
