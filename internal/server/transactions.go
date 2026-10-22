package server

import (
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

// ConnState is the per-connection state the server hands to the executor. It
// lives for the lifetime of the TCP connection, so a disconnect drops an open
// MULTI queue and any WATCH snapshots with no server-side bookkeeping.
type ConnState struct {
	tx              txState
	isReplicaClient bool
	slaveID         uint64
	remoteIP        string
	remotePort      int
	onClose         func()
}

// Close runs the optional close cleanup hook.
func (c *ConnState) Close() {
	if c != nil && c.onClose != nil {
		fn := c.onClose
		c.onClose = nil
		fn()
	}
}

// NewReplicaConnState creates connection state for the internal replication apply loop,
// granting it write permissions on read-only replicas (ADR-017).
func NewReplicaConnState() *ConnState {
	return &ConnState{isReplicaClient: true}
}

// txState is the MULTI queue plus WATCH value snapshots (ADR-008). The executor
// goroutine is its only accessor.
type txState struct {
	active  bool
	dirty   bool
	queued  []protocol.Request
	watched map[string]store.Snapshot
}

// reset ends the transaction, clears the queue, and unwatches every key.
func (t *txState) reset() {
	t.active = false
	t.dirty = false
	t.queued = nil
	t.watched = nil
}

func registerTransactionCommands(reg *Registry) {
	reg.Register(Command{Name: "multi", Arity: 1, Handler: transactionControl})
	reg.Register(Command{Name: "exec", Arity: 1, Write: true, Handler: transactionControl})
	reg.Register(Command{Name: "discard", Arity: 1, Handler: transactionControl})
	reg.Register(Command{Name: "watch", Arity: -2, Handler: transactionControl})
	// UNWATCH can be queued inside MULTI, so its reply is written from EXEC.
	reg.Register(Command{Name: "unwatch", Arity: 1, Handler: func(_ [][]byte, out *protocol.Encoder) {
		out.Simple("OK")
	}})
}

// transactionControl is unreachable: run intercepts these commands before
// dispatch. It stays explicit so a future path that bypasses the executor
// fails loudly instead of silently ignoring a transaction command.
func transactionControl(_ [][]byte, out *protocol.Encoder) {
	out.Error("ERR internal error: transaction command reached dispatch")
}

// execute runs one already-gated request: transaction routing, queueing, or
// normal dispatch plus AOF propagation.
func (e *Executor) execute(req execRequest, tx *txState) {
	e.stats.Commands.Add(1)
	cmd, ok := e.commands.Lookup(req.req[0])
	if !ok || !cmd.accepts(len(req.req)) {
		if ok && cmd.Name == "exec" {
			// Redis wraps an EXEC rejection in EXECABORT and discards the
			// transaction, even outside MULTI (captured from 7.4.11).
			tx.reset()
			reason := strings.TrimPrefix(protocol.WrongArity("exec"), "ERR ")
			req.out.Error("EXECABORT Transaction discarded because of: " + reason)
			return
		}
		if tx.active {
			tx.dirty = true
		}
		e.commands.DispatchWithConn(req.req, req.out, req.conn)
		return
	}
	name := cmd.Name

	if tx.active {
		switch name {
		case "multi":
			req.out.Error("ERR MULTI calls can not be nested")
		case "watch":
			req.out.Error("ERR WATCH inside MULTI is not allowed")
		case "discard":
			tx.reset()
			req.out.Simple("OK")
		case "exec":
			e.execTransaction(req, tx)
		default:
			tx.queued = append(tx.queued, req.req)
			req.out.Simple("QUEUED")
		}
		return
	}

	switch name {
	case "multi":
		tx.active = true
		tx.dirty = false
		tx.queued = nil
		req.out.Simple("OK")
		return
	case "discard":
		req.out.Error("ERR DISCARD without MULTI")
		return
	case "exec":
		req.out.Error("ERR EXEC without MULTI")
		return
	case "watch":
		e.watch(tx, req.req[1:], req.out)
		return
	case "unwatch":
		tx.watched = nil
		req.out.Simple("OK")
		return
	}

	mutations := e.store.Mutations()
	spopBefore := e.captureSpopBefore(req.req)
	e.commands.DispatchWithConn(req.req, req.out, req.conn)
	if err := e.propagate(req.req, mutations, spopBefore); err != nil {
		req.out.Reset()
		req.out.Error("MISCONF Errors writing to the AOF file: " + err.Error())
	}
	if used := e.store.MemoryUsage().Total; used > 0 {
		e.stats.ObserveMemory(uint64(used))
	}
}

// watch snapshots every key so EXEC can detect a concurrent write. Watching an
// already-watched key keeps the first snapshot, as Redis keeps the first touch.
func (e *Executor) watch(tx *txState, keys [][]byte, out *protocol.Encoder) {
	if tx.watched == nil {
		tx.watched = make(map[string]store.Snapshot, len(keys))
	}
	for _, key := range keys {
		name := string(key)
		if _, ok := tx.watched[name]; ok {
			continue
		}
		tx.watched[name] = e.store.CaptureSnapshot(name)
	}
	out.Simple("OK")
}

// execTransaction runs the queued commands as one executor turn, so no other
// client observes an intermediate state. Durable effects are appended as one
// frame for the same reason. A dirty queue or a changed watched value discards
// the transaction; runtime command errors stay in the reply array.
func (e *Executor) execTransaction(req execRequest, tx *txState) {
	aborted := false
	for key, snap := range tx.watched {
		if !e.store.MatchesSnapshot(key, snap) {
			aborted = true
			break
		}
	}
	queue := tx.queued
	dirty := tx.dirty
	tx.reset()

	if dirty {
		req.out.Error("EXECABORT Transaction discarded because of previous errors.")
		return
	}
	if aborted {
		req.out.NullArray()
		return
	}

	req.out.ArrayHeader(len(queue))
	var effects [][][]byte
	for _, queued := range queue {
		mutations := e.store.Mutations()
		spopBefore := e.captureSpopBefore(queued)
		e.commands.DispatchWithConn(queued, req.out, req.conn)
		effects = append(effects, e.collectEffects(queued, mutations, spopBefore)...)
	}
	if err := e.appendEffects(effects); err != nil {
		req.out.Reset()
		req.out.Error("MISCONF Errors writing to the AOF file: " + err.Error())
		return
	}
	if used := e.store.MemoryUsage().Total; used > 0 {
		e.stats.ObserveMemory(uint64(used))
	}
}
