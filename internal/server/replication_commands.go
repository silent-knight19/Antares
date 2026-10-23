package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
)

func registerReplicationCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "replicaof", Arity: 3, Handler: e.replicaofCommand})
	reg.Register(Command{Name: "slaveof", Arity: 3, Handler: e.slaveofCommand})
	reg.Register(Command{Name: "replconf", Arity: -1, HandlerWithConn: e.replconfCommand})
	reg.Register(Command{Name: "psync", Arity: 3, HandlerWithConn: e.psyncCommand})
}

func (e *Executor) replicaofCommand(args [][]byte, out *protocol.Encoder) {
	e.handleReplicaof("replicaof", args, out)
}

func (e *Executor) slaveofCommand(args [][]byte, out *protocol.Encoder) {
	e.handleReplicaof("slaveof", args, out)
}

func (e *Executor) handleReplicaof(cmdName string, args [][]byte, out *protocol.Encoder) {
	host := string(args[0])
	portStr := string(args[1])

	// REPLICAOF NO ONE
	if strings.EqualFold(host, "NO") && strings.EqualFold(portStr, "ONE") {
		e.cancelReplica()
		e.repl.PromoteToMaster()
		out.Simple("OK")
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		out.Error(protocol.InvalidMasterPort)
		return
	}

	e.repl.SetMaster(host, port)
	if port > 0 {
		e.StartReplicaHandshake(host, port)
	} else {
		e.cancelReplica()
		e.repl.SetLinkStatus(repl.LinkStatusDown)
	}

	out.Simple("OK")
}

// Replica reconnect backoff: fast first retry, capped exponential growth.
const (
	replicaReconnectMinDelay = 100 * time.Millisecond
	replicaReconnectMaxDelay = 2 * time.Second
)

var errSyncSuperseded = errors.New("replication sync superseded")

func nextReplicaReconnectDelay(current time.Duration) time.Duration {
	if current <= 0 {
		return replicaReconnectMinDelay
	}
	if current >= replicaReconnectMaxDelay {
		return replicaReconnectMaxDelay
	}
	next := current * 2
	if next > replicaReconnectMaxDelay {
		return replicaReconnectMaxDelay
	}
	return next
}

// StartReplicaHandshake connects to the master, retrying with partial resync
// from the last processed offset after a link break (S2.2).
func (e *Executor) StartReplicaHandshake(host string, port int) {
	e.cancelReplica()

	ctx, cancel := context.WithCancel(context.Background())
	e.setReplCancel(cancel)

	e.replMu.Lock()
	oldDone := e.replDone
	done := make(chan struct{})
	e.replDone = done
	e.replMu.Unlock()

	listeningPort := 6380
	if addr := e.stats.Addr(); addr != "" {
		if _, p, err := net.SplitHostPort(addr); err == nil {
			if lp, pErr := strconv.Atoi(p); pErr == nil {
				listeningPort = lp
			}
		}
	}

	masterAddr := net.JoinHostPort(host, strconv.Itoa(port))

	go func() {
		defer close(done)
		defer cancel()
		if oldDone != nil {
			<-oldDone
		}
		delay := replicaReconnectMinDelay
		for {
			if ctx.Err() != nil {
				return
			}
			_ = e.replicaSyncOnce(ctx, masterAddr, listeningPort)
			if ctx.Err() != nil {
				return
			}
			if e.repl.LinkStatus() == repl.LinkStatusUp {
				delay = replicaReconnectMinDelay
			}
			e.repl.SetLinkStatus(repl.LinkStatusDown)
			if !sleepContext(ctx, delay) {
				return
			}
			delay = nextReplicaReconnectDelay(delay)
		}
	}()
}

// replicaSyncOnce runs one handshake and, on success, the apply loop until the
// link breaks. It returns the link error so the caller can retry.
func (e *Executor) replicaSyncOnce(ctx context.Context, masterAddr string, listeningPort int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	syncGen := e.repl.BeginSync()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	reqReplID, reqOffset := e.repl.PSyncArgs()
	if !e.repl.SetLinkStatusForSync(repl.LinkStatusConnecting, syncGen) {
		return errSyncSuperseded
	}

	cfg := repl.HandshakeConfig{
		MasterAddr:    masterAddr,
		ListeningPort: listeningPort,
		ReplID:        reqReplID,
		Offset:        reqOffset,
		Timeout:       3 * time.Second,
	}

	res, err := repl.PerformHandshake(ctx, cfg)
	if err != nil {
		return err
	}
	defer res.Close()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Cancellation (REPLICAOF NO ONE or shutdown) must unblock a silent read.
	stopClose := context.AfterFunc(ctx, func() { _ = res.Conn.Close() })
	defer stopClose()

	tr := repl.NewTrackedReader(res.Reader, 64<<10)

	if res.IsFullResync {
		if !e.repl.SetLinkStatusForSync(repl.LinkStatusSync, syncGen) {
			return errSyncSuperseded
		}
		if err := e.applySnapshotStream(ctx, res.Conn, tr, syncGen); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if res.MasterReplID != "" {
			if !e.repl.SetMasterReplIDForSync(res.MasterReplID, syncGen) {
				return errSyncSuperseded
			}
		}
		if !e.repl.SetSlaveReplOffsetForSync(res.MasterReplOffset, syncGen) {
			return errSyncSuperseded
		}
	} else if res.MasterReplID != "" {
		if !e.repl.SetMasterReplIDForSync(res.MasterReplID, syncGen) {
			return errSyncSuperseded
		}
	}

	if !e.repl.SetLinkStatusForSync(repl.LinkStatusUp, syncGen) {
		return errSyncSuperseded
	}
	return e.runReplicaApplyLoop(ctx, res.Conn, tr, time.Second, syncGen)
}

// applySnapshotStream replays a FULLRESYNC snapshot through the executor. The
// resume point is invalidated before FLUSHALL, so a failed snapshot cannot be
// followed by a partial resync against a flushed dataset.
func (e *Executor) applySnapshotStream(ctx context.Context, conn net.Conn, tr *repl.TrackedReader, syncGen uint64) error {
	replConn := NewReplicaConnState()
	enc := protocol.GetEncoder()
	defer enc.Release()

	if !e.repl.BeginFullResync(syncGen) {
		return errSyncSuperseded
	}
	resetReplicaReadDeadline(conn, e.replicaReadTimeout)

	// A full resync replaces the dataset; stale keys from an older link must go.
	enc.Reset()
	e.HandleContext(ctx, protocol.Request{[]byte("FLUSHALL")}, enc, replConn)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
		return fmt.Errorf("FLUSHALL before snapshot failed: %s", strings.TrimSpace(string(enc.Bytes()[1:])))
	}

	return repl.ReadSnapshotStream(tr.Reader(), func(payload []byte) error {
		resetReplicaReadDeadline(conn, e.replicaReadTimeout)
		br := bufio.NewReader(bytes.NewReader(payload))
		for first := true; ; first = false {
			req, pErr := protocol.ReadRequest(br)
			if errors.Is(pErr, io.EOF) {
				if first {
					return errors.New("empty command in snapshot frame")
				}
				return nil
			}
			if pErr != nil {
				return pErr
			}
			if len(req) == 0 {
				continue
			}
			enc.Reset()
			e.HandleContext(ctx, req, enc, replConn)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
				return fmt.Errorf("snapshot command %q failed: %s", req[0], strings.TrimSpace(string(enc.Bytes()[1:])))
			}
		}
	})
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Executor) ensureSlave(conn *ConnState) uint64 {
	if conn == nil || e.repl == nil {
		return 0
	}
	if conn.slaveID != 0 {
		return conn.slaveID
	}
	ip := conn.remoteIP
	if ip == "" {
		ip = "127.0.0.1"
	}
	slID := e.repl.AddSlave(ip, conn.remotePort)
	conn.slaveID = slID
	conn.addCloseHook(func() {
		if e.repl != nil {
			e.repl.RemoveSlave(slID)
		}
	})
	return slID
}

func (e *Executor) replconfCommand(args [][]byte, out *protocol.Encoder, conn *ConnState) {
	if len(args) == 0 {
		out.Simple("OK")
		return
	}

	sub := strings.ToLower(string(args[0]))
	switch sub {
	case "listening-port":
		if len(args) >= 2 {
			if port, err := strconv.Atoi(string(args[1])); err == nil && port >= 0 && port <= 65535 {
				slaveID := e.ensureSlave(conn)
				if slaveID != 0 && e.repl != nil {
					e.repl.UpdateSlavePort(slaveID, port)
				}
			}
		}
		out.Simple("OK")
	case "ip-address":
		if len(args) >= 2 {
			slaveID := e.ensureSlave(conn)
			if slaveID != 0 && e.repl != nil {
				e.repl.UpdateSlaveIP(slaveID, string(args[1]))
			}
		}
		out.Simple("OK")
	case "capa":
		out.Simple("OK")
	case "ack":
		// Replica offset confirmation (S1.5)
		if len(args) >= 2 {
			if off, err := strconv.ParseInt(string(args[1]), 10, 64); err == nil && off >= 0 {
				slaveID := e.ensureSlave(conn)
				if slaveID != 0 && e.repl != nil {
					e.repl.UpdateSlaveAck(slaveID, off)
				}
			}
		}
		// Master doesn't reply to REPLCONF ACK in Redis protocol
	default:
		out.Simple("OK")
	}
}

func (e *Executor) psyncCommand(args [][]byte, out *protocol.Encoder, conn *ConnState) {
	if len(args) != 2 {
		out.Error(protocol.WrongArity("psync"))
		return
	}

	slaveID := e.ensureSlave(conn)
	if slaveID != 0 && e.repl != nil {
		e.repl.UpdateSlaveState(slaveID, "online")
	}

	reqReplID := string(args[0])
	reqOffset, err := strconv.ParseInt(string(args[1]), 10, 64)

	// Real connections get the streamed protocol; direct unit tests without a
	// socket fall back to the decision header only.
	if conn != nil && conn.conn != nil {
		if err == nil {
			if backlog, buf, ok := e.repl.StartPartialResync(reqReplID, reqOffset); ok {
				e.stats.SyncPartialOK.Add(1)
				e.attachReplicaStream(conn, buf)
				conn.flushPending()
				conn.streaming = true
				out.Reset()
				go e.streamPartialResync(conn.conn, buf, backlog, e.repl.MasterReplID())
				return
			}
		}

		if !e.beginFullResync() {
			out.Error("ERR full resync already in progress")
			return
		}
		e.stats.SyncFull.Add(1)
		session := e.PrepareFullResync()
		e.attachReplicaStream(conn, session.SyncBuffer)
		conn.flushPending()
		conn.streaming = true
		out.Reset()
		go e.streamFullResync(conn.conn, session)
		return
	}

	if err != nil || !e.repl.CanPartialResyncWithID(reqReplID, reqOffset) {
		e.stats.SyncFull.Add(1)
		out.Simple(fmt.Sprintf("FULLRESYNC %s %d", e.repl.MasterReplID(), e.repl.MasterReplOffset()))
		return
	}

	e.stats.SyncPartialOK.Add(1)
	out.Simple(fmt.Sprintf("CONTINUE %s", e.repl.MasterReplID()))
}
