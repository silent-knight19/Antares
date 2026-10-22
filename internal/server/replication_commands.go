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

// StartReplicaHandshake initiates an asynchronous handshake with the master.
func (e *Executor) StartReplicaHandshake(host string, port int) {
	e.cancelReplica()

	ctx, cancel := context.WithCancel(context.Background())
	e.setReplCancel(cancel)

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
		defer cancel()
		cfg := repl.HandshakeConfig{
			MasterAddr:    masterAddr,
			ListeningPort: listeningPort,
			ReplID:        "?",
			Offset:        -1,
			Timeout:       3 * time.Second,
		}

		res, err := repl.PerformHandshake(ctx, cfg)
		if err != nil {
			if ctx.Err() == nil {
				e.repl.SetLinkStatus(repl.LinkStatusDown)
			}
			return
		}

		if ctx.Err() != nil {
			_ = res.Close()
			return
		}

		if res.MasterReplID != "" {
			e.repl.SetMasterReplID(res.MasterReplID)
		}

		tr := repl.NewTrackedReader(res.Conn, 64<<10)

		if res.IsFullResync {
			e.repl.SetLinkStatus(repl.LinkStatusSync)
			e.repl.SetSlaveReplOffset(res.MasterReplOffset)

			// Decode and apply snapshot stream safely under the executor (ADR-002, ADR-017)
			replConn := NewReplicaConnState()
			enc := protocol.GetEncoder()
			defer enc.Release()

			if sErr := repl.ReadSnapshotStream(tr.Reader(), func(payload []byte) error {
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
					e.Handle(req, enc, replConn)
					if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
						return fmt.Errorf("snapshot command %q failed: %s", req[0], strings.TrimSpace(string(enc.Bytes()[1:])))
					}
				}
			}); sErr != nil {
				if ctx.Err() == nil {
					e.repl.SetLinkStatus(repl.LinkStatusDown)
				}
				_ = res.Close()
				return
			}
		}

		e.repl.SetLinkStatus(repl.LinkStatusUp)
		if aErr := e.RunReplicaApplyLoop(ctx, res.Conn, tr); aErr != nil {
			if ctx.Err() == nil {
				e.repl.SetLinkStatus(repl.LinkStatusDown)
			}
		}
	}()
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
	conn.onClose = func() {
		if e.repl != nil {
			e.repl.RemoveSlave(slID)
		}
	}
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
	masterReplID := e.repl.MasterReplID()
	masterOffset := e.repl.MasterReplOffset()

	if reqReplID == "?" || reqReplID != masterReplID {
		out.Simple(fmt.Sprintf("FULLRESYNC %s %d", masterReplID, masterOffset))
		return
	}

	reqOffset, err := strconv.ParseInt(string(args[1]), 10, 64)
	if err != nil || !e.repl.CanPartialResync(reqOffset) {
		out.Simple(fmt.Sprintf("FULLRESYNC %s %d", masterReplID, masterOffset))
		return
	}

	out.Simple(fmt.Sprintf("CONTINUE %s", masterReplID))
}
