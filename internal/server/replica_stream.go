package server

import (
	"net"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/repl"
)

// replicaWriteTimeout bounds each network write so a stalled replica cannot pin
// a stream goroutine or grow its buffer forever (S2.3).
var replicaWriteTimeout = 5 * time.Second

// attachReplicaStream makes sure the live write buffer is unregistered and
// closed when the replica connection ends, even if no further writes arrive.
func (e *Executor) attachReplicaStream(conn *ConnState, buf *repl.SyncBuffer) {
	if conn == nil {
		return
	}
	conn.addCloseHook(func() {
		e.repl.UnregisterSyncBuffer(buf)
		buf.Close()
	})
}

// streamPartialResync writes +CONTINUE, the backlog tail after the replica's
// offset, then live mutations until the link closes (S2.2).
func (e *Executor) streamPartialResync(conn net.Conn, buf *repl.SyncBuffer, backlog []byte, replID string) {
	defer func() {
		e.repl.UnregisterSyncBuffer(buf)
		buf.Close()
	}()

	if err := writeReplicaStream(conn, []byte("+CONTINUE "+replID+"\r\n")); err != nil {
		_ = conn.Close()
		return
	}
	if err := writeReplicaStream(conn, backlog); err != nil {
		_ = conn.Close()
		return
	}
	e.pumpReplicaStream(conn, buf)
}

// streamFullResync writes +FULLRESYNC and the snapshot, then live mutations
// accumulated while the snapshot streamed (S2.2).
func (e *Executor) streamFullResync(conn net.Conn, session *FullResyncSession) {
	buf := session.SyncBuffer
	defer func() {
		e.repl.UnregisterSyncBuffer(buf)
		buf.Close()
		e.endFullResync()
	}()

	if err := session.StreamOpen(deadlineWriter{conn: conn}); err != nil {
		_ = conn.Close()
		return
	}
	e.pumpReplicaStream(conn, buf)
}

// pumpReplicaStream forwards buffered mutations until the buffer closes or a
// write fails.
func (e *Executor) pumpReplicaStream(conn net.Conn, buf *repl.SyncBuffer) {
	for {
		chunks, err := buf.DrainChunks()
		if err != nil {
			_ = conn.Close()
			return
		}
		for _, chunk := range chunks {
			if err := writeReplicaStream(conn, chunk); err != nil {
				_ = conn.Close()
				return
			}
		}
		select {
		case <-buf.Wait():
		case <-buf.Done():
			return
		}
	}
}

// deadlineWriter applies the slow-replica write deadline to snapshot writes too.
type deadlineWriter struct {
	conn net.Conn
}

func (w deadlineWriter) Write(p []byte) (int, error) {
	_ = w.conn.SetWriteDeadline(clock.Real{}.Now().Add(replicaWriteTimeout))
	return w.conn.Write(p)
}

func writeReplicaStream(conn net.Conn, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	_ = conn.SetWriteDeadline(clock.Real{}.Now().Add(replicaWriteTimeout))
	for len(p) > 0 {
		n, err := conn.Write(p)
		if err != nil {
			return err
		}
		p = p[n:]
	}
	return nil
}
