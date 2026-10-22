package server

import (
	"context"
	"fmt"
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
	reg.Register(Command{Name: "replconf", Arity: -1, Handler: e.replconfCommand})
	reg.Register(Command{Name: "psync", Arity: 3, Handler: e.psyncCommand})
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

		e.repl.SetLinkStatus(repl.LinkStatusUp)
		if res.MasterReplID != "" {
			e.repl.SetMasterReplID(res.MasterReplID)
			e.repl.SetMasterReplOffset(res.MasterReplOffset)
		}
		_ = res.Close()
	}()
}

func (e *Executor) replconfCommand(args [][]byte, out *protocol.Encoder) {
	if len(args) == 0 {
		out.Simple("OK")
		return
	}

	sub := strings.ToLower(string(args[0]))
	switch sub {
	case "listening-port", "capa", "ip-address":
		out.Simple("OK")
	case "ack":
		// Replica offset confirmation (S1.5)
		if len(args) >= 2 {
			if off, err := strconv.ParseInt(string(args[1]), 10, 64); err == nil {
				e.repl.SetSlaveReplOffset(off)
			}
		}
		// Master doesn't reply to REPLCONF ACK in Redis protocol
	default:
		out.Simple("OK")
	}
}

func (e *Executor) psyncCommand(args [][]byte, out *protocol.Encoder) {
	if len(args) != 2 {
		out.Error(protocol.WrongArity("psync"))
		return
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
