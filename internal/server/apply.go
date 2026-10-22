package server

import (
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
	return e.RunReplicaApplyLoopWithInterval(ctx, conn, tr, 1*time.Second)
}

// RunReplicaApplyLoopWithInterval runs the apply loop with a custom periodic ACK interval (for tests).
func (e *Executor) RunReplicaApplyLoopWithInterval(ctx context.Context, conn net.Conn, tr *repl.TrackedReader, ackInterval time.Duration) error {
	defer conn.Close()

	replConn := NewReplicaConnState()
	enc := protocol.GetEncoder()
	defer enc.Release()

	var writeMu sync.Mutex
	sendAck := func() error {
		writeMu.Lock()
		defer writeMu.Unlock()
		off := e.repl.SlaveReplOffset()
		ack := fmt.Sprintf("*3\r\n$8\r\nREPLCONF\r\n$3\r\nACK\r\n$%d\r\n%d\r\n", len(strconv.FormatInt(off, 10)), off)
		_ = conn.SetWriteDeadline(clock.Real{}.Now().Add(5 * time.Second))
		_, err := conn.Write([]byte(ack))
		return err
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
					if err := sendAck(); err != nil {
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

		// Advance replica processed stream offset
		e.repl.SetSlaveReplOffset(e.repl.SlaveReplOffset() + bytesRead)

		if len(req) == 0 {
			continue
		}

		name := strings.ToLower(string(req[0]))
		switch name {
		case "ping":
			// Master replication keepalive; offset already advanced, no store mutation
			continue
		case "replconf":
			if len(req) >= 2 && strings.EqualFold(string(req[1]), "getack") {
				// Master requests offset acknowledgement (S1.5)
				if err := sendAck(); err != nil {
					return err
				}
			}
			continue
		default:
			// Execute mutation on the replica using its replication client state
			enc.Reset()
			e.Handle(req, enc, replConn)
		}
	}
}
