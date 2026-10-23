package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/store"
)

// FullResyncSession coordinates taking an atomic point-in-time snapshot copy under
// the executor (ADR-010), streaming it in shared framed format (ADR-007), and draining
// the per-replica concurrent-write buffer so writes remain available during resync.
type FullResyncSession struct {
	MasterReplID   string
	SnapshotOffset int64
	Snapshot       *store.Store
	SyncBuffer     *repl.SyncBuffer
	repl           *repl.State
}

// PrepareFullResync takes a point-in-time copy of the keyspace, captures the current
// replication offset, and registers a per-replica concurrent-write buffer.
// Must be called under the executor goroutine or when executor is paused.
func (e *Executor) PrepareFullResync() *FullResyncSession {
	snap := e.store.Copy()
	syncBuf := repl.NewSyncBuffer(e.repl.SyncBufferLimit())
	snapOffset := e.repl.RegisterSyncBufferWithOffset(syncBuf)

	return &FullResyncSession{
		MasterReplID:   e.repl.MasterReplID(),
		SnapshotOffset: snapOffset,
		Snapshot:       snap,
		SyncBuffer:     syncBuf,
		repl:           e.repl,
	}
}

// PrepareFullResyncWithCap is PrepareFullResync with a custom buffer limit for tests.
func (e *Executor) PrepareFullResyncWithCap(maxBytes int64) *FullResyncSession {
	snap := e.store.Copy()
	syncBuf := repl.NewSyncBuffer(maxBytes)
	snapOffset := e.repl.RegisterSyncBufferWithOffset(syncBuf)

	return &FullResyncSession{
		MasterReplID:   e.repl.MasterReplID(),
		SnapshotOffset: snapOffset,
		Snapshot:       snap,
		SyncBuffer:     syncBuf,
		repl:           e.repl,
	}
}

// Stream streams +FULLRESYNC, the framed snapshot, and drains all concurrent
// mutations accumulated during snapshot streaming to w (ADR-007, ADR-010).
// It unregisters the SyncBuffer when done.
func (s *FullResyncSession) Stream(w io.Writer) error {
	defer s.repl.UnregisterSyncBuffer(s.SyncBuffer)
	return s.StreamOpen(w)
}

// StreamOpen writes +FULLRESYNC, the snapshot, and the mutations buffered
// during snapshot streaming, but keeps the SyncBuffer registered so the caller
// can continue streaming live writes through it (S2.2).
func (s *FullResyncSession) StreamOpen(w io.Writer) error {
	bw := bufio.NewWriterSize(w, 64<<10)

	// 1. Emit +FULLRESYNC <replid> <offset>\r\n
	if _, err := fmt.Fprintf(bw, "+FULLRESYNC %s %d\r\n", s.MasterReplID, s.SnapshotOffset); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}

	// 2. Stream dataset snapshot using shared framed format with EOF delimiter (ADR-007)
	if _, err := repl.WriteSnapshotStream(bw, s.Snapshot, ""); err != nil {
		return err
	}
	s.Snapshot = nil
	if err := bw.Flush(); err != nil {
		return err
	}

	// 3. Drain concurrent-write buffer accumulated during streaming (ADR-010)
	chunks, err := s.SyncBuffer.DrainChunks()
	if err != nil {
		return fmt.Errorf("replication sync buffer: %w", err)
	}
	for _, chunk := range chunks {
		if _, err := bw.Write(chunk); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	return nil
}

// ApplyFullResync reads +FULLRESYNC from br, deserializes the snapshot stream
// into st, and replays any trailing concurrent writes until EOF or until
// commandsCount commands are processed (if commandsCount > 0).
func ApplyFullResync(br *bufio.Reader, st *store.Store, commandsCount int) (masterReplID string, offset int64, err error) {
	// 1. Read +FULLRESYNC line (skipping keepalive newlines)
	for {
		line, rErr := br.ReadString('\n')
		if rErr != nil {
			return "", 0, fmt.Errorf("reading FULLRESYNC header: %w", rErr)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "+FULLRESYNC ") {
			return "", 0, fmt.Errorf("expected +FULLRESYNC, got %q", trimmed)
		}
		parts := strings.Split(strings.TrimPrefix(trimmed, "+FULLRESYNC "), " ")
		if len(parts) >= 1 {
			masterReplID = parts[0]
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &offset)
		}
		break
	}

	reg := NewDataRegistry(st)
	enc := protocol.GetEncoder()
	defer enc.Release()

	// 2. Decode and apply snapshot stream
	if err := repl.ReadSnapshotStream(br, func(payload []byte) error {
		return ReplayPayload(reg, enc, payload)
	}); err != nil {
		return "", 0, fmt.Errorf("applying snapshot stream: %w", err)
	}

	// 3. Replay concurrent-write buffer / propagation stream
	replayed := 0
	for commandsCount <= 0 || replayed < commandsCount {
		req, pErr := protocol.ReadRequest(br)
		if errors.Is(pErr, io.EOF) {
			break
		}
		if pErr != nil {
			return masterReplID, offset, fmt.Errorf("reading live stream: %w", pErr)
		}
		enc.Reset()
		reg.Dispatch(req, enc)
		if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
			return masterReplID, offset, fmt.Errorf("live command %q failed: %s", req[0], strings.TrimSpace(string(enc.Bytes()[1:])))
		}
		replayed++
	}

	return masterReplID, offset, nil
}
