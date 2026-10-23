package server

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
)

// RunReplicaApplyLoop is the continuous command consumer for a connected replica (S1.4, S1.5).
// It reads live commands from the master stream using a TrackedReader, increments
// slave_repl_offset by the exact bytes consumed, executes writes using the replica's
// own client state (bypassing read-only gating), handles keepalive PINGs, sends periodic
// REPLCONF ACKs every 1 second, and replies to master REPLCONF GETACK requests.
func (e *Executor) RunReplicaApplyLoop(ctx context.Context, conn net.Conn, tr *repl.TrackedReader) error {
	return e.runReplicaApplyLoop(ctx, conn, tr, 1*time.Second, 0)
}

// RunReplicaApplyLoopWithInterval runs the apply loop with a custom periodic ACK interval (for tests).
func (e *Executor) RunReplicaApplyLoopWithInterval(ctx context.Context, conn net.Conn, tr *repl.TrackedReader, ackInterval time.Duration) error {
	return e.runReplicaApplyLoop(ctx, conn, tr, ackInterval, 0)
}

func resetReplicaReadDeadline(conn net.Conn, timeout time.Duration) {
	if conn != nil && timeout > 0 {
		_ = conn.SetReadDeadline(clock.Real{}.Now().Add(timeout))
	}
}

func (e *Executor) runReplicaApplyLoop(ctx context.Context, conn net.Conn, tr *repl.TrackedReader, ackInterval time.Duration, syncGen uint64) error {
	defer conn.Close()

	replConn := NewReplicaConnState()
	enc := protocol.GetEncoder()
	defer enc.Release()

	var writeMu sync.Mutex
	sendAck := func(off int64) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		ack := fmt.Sprintf("*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$%d\r\n%d\r\n", len(strconv.FormatInt(off, 10)), off)
		_ = conn.SetWriteDeadline(clock.Real{}.Now().Add(5 * time.Second))
		_, err := conn.Write([]byte(ack))
		return err
	}
	advance := func(offset int64) bool {
		if syncGen == 0 {
			e.repl.SetSlaveReplOffset(offset)
			return true
		}
		return e.repl.SetSlaveReplOffsetForSync(offset, syncGen)
	}

	// Periodic ACK every interval (S1.5: REPLCONF ACK every 1s)
	if ackInterval > 0 {
		ackCtx, cancelAck := context.WithCancel(ctx)
		defer cancelAck()
		go func() {
			ticker := time.NewTicker(ackInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ackCtx.Done():
					return
				case <-ticker.C:
					if err := sendAck(e.repl.SlaveReplOffset()); err != nil {
						_ = conn.Close()
						return
					}
				}
			}
		}()
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if syncGen != 0 && e.repl.SyncGeneration() != syncGen {
			return errSyncSuperseded
		}

		resetReplicaReadDeadline(conn, e.replicaReadTimeout)
		before := tr.Consumed()
		req, err := protocol.ReadRequest(tr.Reader())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}

		bytesRead := tr.Consumed() - before
		if bytesRead <= 0 {
			continue
		}
		nextOffset := e.repl.SlaveReplOffset() + bytesRead

		if len(req) == 0 {
			if !advance(nextOffset) {
				return errSyncSuperseded
			}
			continue
		}

		name := strings.ToLower(string(req[0]))
		switch name {
		case "ping":
			if !advance(nextOffset) {
				return errSyncSuperseded
			}
		case "replconf":
			// Advance before replying so the ACK and the stored offset agree.
			if !advance(nextOffset) {
				return errSyncSuperseded
			}
			if len(req) >= 2 && strings.EqualFold(string(req[1]), "getack") {
				// Master requests offset acknowledgement (S1.5)
				if err := sendAck(nextOffset); err != nil {
					return err
				}
			}
		default:
			// Execute mutation on the replica using its replication client state.
			// The executor advances the offset only after a successful apply.
			enc.Reset()
			e.HandleReplicated(ctx, req, enc, replConn, syncGen, bytesRead)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if syncGen != 0 && e.repl.SyncGeneration() != syncGen {
				return errSyncSuperseded
			}
			if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
				if syncGen != 0 {
					e.repl.BeginFullResync(syncGen)
				}
				return fmt.Errorf("replica command %q failed: %s", req[0], strings.TrimSpace(string(enc.Bytes()[1:])))
			}
		}
	}
}
