package server

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/events"
	"github.com/silent-knight19/antares/internal/evict"
	"github.com/silent-knight19/antares/internal/expire"
	"github.com/silent-knight19/antares/internal/metrics"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
	"github.com/silent-knight19/antares/internal/store"
)

// requestQueueSize bounds requests waiting for the executor. Connections block
// when it fills, so TCP backpressure replaces an unbounded queue.
const requestQueueSize = 1024

const defaultHz = 10

type Executor struct {
	commands *Registry
	requests chan execRequest
	quit     chan struct{}
	done     chan struct{}

	active bool
	hz     int
	// replicaReadTimeout bounds how long a replica waits for master traffic.
	replicaReadTimeout time.Duration
	hzChanged          chan struct{}
	clock              clock.Clock
	expiry             *expire.Cycle
	store              *store.Store
	stats              *metrics.Stats
	start              time.Time

	maxmemory       int64
	maxmemoryPolicy string

	// events is the opt-in internal tap (ADR-014); nil disables it. evictPool
	// carries candidates within one eviction round.
	events    events.Sink
	evictPool evict.Pool

	aof             *persist.Writer
	rewriteInFlight atomic.Bool

	replMu     sync.Mutex
	repl       *repl.State
	replCancel context.CancelFunc
	replDone   chan struct{}

	replicaWriteTimeout atomic.Int64
	fullResyncs         atomic.Int32

	closeOnce sync.Once
}

type execRequest struct {
	req       protocol.Request
	batch     []protocol.Request
	out       *protocol.Encoder
	conn      *ConnState
	done      chan struct{}
	ctx       context.Context
	syncGen   uint64
	replBytes int64
}

// NewExecutor starts the single command-execution goroutine. A nil store or
// registry gets fresh defaults. Close must be called after server shutdown
// has drained the connections.
func NewExecutor(st *store.Store, reg *Registry) *Executor {
	return newExecutor(st, reg, true, clock.Real{})
}

// NewExecutorWithClock is the real entry point: one clock instance flows
// through the executor, store, and active cycle (ADR-009).
func NewExecutorWithClock(st *store.Store, reg *Registry, c clock.Clock) *Executor {
	return newExecutor(st, reg, true, c)
}

// NewExecutorWithAOF enables append-only persistence: mutations are framed and
// appended in execution order after each successful command.
func NewExecutorWithAOF(st *store.Store, reg *Registry, c clock.Clock, aof *persist.Writer) *Executor {
	return newExecutorWithAOF(st, reg, true, c, aof)
}

func newExecutor(st *store.Store, reg *Registry, active bool, c clock.Clock) *Executor {
	return newExecutorWithAOF(st, reg, active, c, nil)
}

func newExecutorWithAOF(st *store.Store, reg *Registry, active bool, c clock.Clock, aof *persist.Writer) *Executor {
	return newExecutorWithPing(st, reg, active, c, aof, defaultReplicaPingInterval)
}

// newExecutorWithPing lets tests shorten the keepalive without racing the
// production default.
func newExecutorWithPing(st *store.Store, reg *Registry, active bool, c clock.Clock, aof *persist.Writer, pingInterval time.Duration) *Executor {
	if c == nil {
		c = clock.Real{}
	}
	if st == nil {
		st = store.NewWithClock(c)
	}
	e := &Executor{
		requests:           make(chan execRequest, requestQueueSize),
		quit:               make(chan struct{}),
		done:               make(chan struct{}),
		active:             active,
		hz:                 defaultHz,
		hzChanged:          make(chan struct{}, 1),
		replicaReadTimeout: 30 * time.Second,
		clock:              c,
		expiry:             expire.New(st, c),
		store:              st,
		stats:              metrics.New(DefaultMaxConns),
		start:              c.Now(),
		maxmemoryPolicy:    "noeviction",
		aof:                aof,
	}
	e.repl = repl.NewState(e.stats.ReplID, c)
	e.store.SetOnLazyExpire(func(key string) {
		_ = e.persistEvictions([]string{key})
	})
	if reg == nil {
		reg = NewRegistry()
		registerDataCommands(reg, st)
		registerConfigCommands(reg, e)
		registerInfoCommands(reg, e)
		registerConnectionCommands(reg, e)
		registerPersistenceCommands(reg, e)
		registerTransactionCommands(reg)
		registerReplicationCommands(reg, e)
	}
	e.replicaWriteTimeout.Store(int64(defaultReplicaWriteTimeout))
	e.commands = reg
	go e.loop()
	if active {
		go e.replicationPingLoop(pingInterval)
	}
	return e
}

// defaultReplicaPingInterval is the master keepalive period (Redis defaults to 10s).
const defaultReplicaPingInterval = 10 * time.Second

// defaultReplicaWriteTimeout bounds network writes to replicas (S2.3).
const defaultReplicaWriteTimeout = 5 * time.Second

// ReplicaWriteTimeout returns the per-replica stream write deadline.
func (e *Executor) ReplicaWriteTimeout() time.Duration {
	if e == nil {
		return defaultReplicaWriteTimeout
	}
	d := e.replicaWriteTimeout.Load()
	if d <= 0 {
		return defaultReplicaWriteTimeout
	}
	return time.Duration(d)
}

// SetReplicaWriteTimeout configures the per-replica stream write deadline.
func (e *Executor) SetReplicaWriteTimeout(d time.Duration) {
	if e != nil {
		e.replicaWriteTimeout.Store(int64(d))
	}
}

func (e *Executor) replicationPingLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.repl.FeedReplicationPing()
		case <-e.quit:
			return
		}
	}
}

const maxConcurrentFullResyncs = 1

func (e *Executor) beginFullResync() bool {
	if e.fullResyncs.Add(1) > maxConcurrentFullResyncs {
		e.fullResyncs.Add(-1)
		return false
	}
	return true
}

func (e *Executor) endFullResync() {
	e.fullResyncs.Add(-1)
}

// Stats exposes the process counters INFO reports.
func (e *Executor) Stats() *metrics.Stats { return e.stats }

// SetEventSink installs the opt-in event tap. Call it before serving.
func (e *Executor) SetEventSink(sink events.Sink) { e.events = sink }

// Handle implements server.Handler: it queues the request and waits for the
// executor to write the reply into out. A nil conn (direct tests) gets a fresh
// transaction state. A closed executor drops the request rather than blocking
// the connection goroutine forever.
func (e *Executor) Handle(req protocol.Request, out *protocol.Encoder, conn *ConnState) {
	e.HandleContext(context.Background(), req, out, conn)
}

// HandleContext queues a request that is dropped if ctx is canceled before the
// executor runs it.
func (e *Executor) HandleContext(ctx context.Context, req protocol.Request, out *protocol.Encoder, conn *ConnState) {
	e.handle(ctx, req, out, conn, 0, 0)
}

// HandleBatch executes a slice of pipelined requests sequentially and returns
// once all replies are written to out.
func (e *Executor) HandleBatch(batch []protocol.Request, out *protocol.Encoder, conn *ConnState) {
	if len(batch) == 0 {
		return
	}
	if len(batch) == 1 {
		e.Handle(batch[0], out, conn)
		return
	}
	if conn == nil {
		conn = &ConnState{}
	}
	done := donePool.Get().(chan struct{})
	select {
	case e.requests <- execRequest{batch: batch, out: out, conn: conn, done: done, ctx: context.Background()}:
	case <-e.quit:
		donePool.Put(done)
		return
	case <-e.done:
		donePool.Put(done)
		return
	}
	select {
	case <-done:
		donePool.Put(done)
	case <-e.done:
	}
}

// HandleReplicated applies one command from the master stream. The offset is
// advanced on the executor turn only after the command succeeds, so a failed
// apply cannot move the replica past a command it did not execute.
func (e *Executor) HandleReplicated(ctx context.Context, req protocol.Request, out *protocol.Encoder, conn *ConnState, syncGen uint64, bytesRead int64) {
	e.handle(ctx, req, out, conn, syncGen, bytesRead)
}

var donePool = sync.Pool{
	New: func() any {
		return make(chan struct{}, 1)
	},
}

func (e *Executor) handle(ctx context.Context, req protocol.Request, out *protocol.Encoder, conn *ConnState, syncGen uint64, bytesRead int64) {
	if conn == nil {
		conn = &ConnState{}
	}
	done := donePool.Get().(chan struct{})
	select {
	case e.requests <- execRequest{req: req, out: out, conn: conn, done: done, ctx: ctx, syncGen: syncGen, replBytes: bytesRead}:
	case <-ctx.Done():
		donePool.Put(done)
		return
	case <-e.quit:
		donePool.Put(done)
		return
	case <-e.done:
		donePool.Put(done)
		return
	}
	// Never return while the executor could still write through out.
	select {
	case <-done:
		donePool.Put(done)
	case <-e.done:
	}
}

// Close stops the executor and waits indefinitely for the current command.
func (e *Executor) Close() {
	_ = e.CloseContext(context.Background())
}

// CloseContext stops accepting commands and waits for the executor to exit or
// ctx to expire. A command that ignores cancellation can outlive ctx; callers
// must not reuse its encoder after that happens.
func (e *Executor) CloseContext(ctx context.Context) error {
	e.closeOnce.Do(func() {
		e.cancelReplica()
		close(e.quit)
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Executor) cancelReplica() {
	e.replMu.Lock()
	defer e.replMu.Unlock()
	if e.replCancel != nil {
		e.replCancel()
		e.replCancel = nil
	}
}

func (e *Executor) setReplCancel(cancel context.CancelFunc) {
	e.replMu.Lock()
	defer e.replMu.Unlock()
	e.replCancel = cancel
}

// Repl exposes the replication state.
func (e *Executor) Repl() *repl.State { return e.repl }

func (e *Executor) loop() {
	defer close(e.done)
	if !e.active {
		for {
			select {
			case req := <-e.requests:
				e.run(req)
				for i := 0; i < 256; i++ {
					select {
					case nextReq := <-e.requests:
						e.run(nextReq)
					default:
						i = 256
					}
				}
			case <-e.quit:
				return
			}
		}
	}

	timer := time.NewTimer(e.cycleInterval())
	defer timer.Stop()
	for {
		select {
		case req := <-e.requests:
			e.run(req)
			for i := 0; i < 256; i++ {
				select {
				case nextReq := <-e.requests:
					e.run(nextReq)
				default:
					i = 256
				}
			}
		case <-timer.C:
			expiredKeys, _ := e.expiry.RunKeys(e.clock.Now().UnixMilli(), e.cycleBudget())
			if len(expiredKeys) > 0 {
				_ = e.persistEvictions(expiredKeys)
			}
			timer.Reset(e.cycleInterval())
		case <-e.hzChanged:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(e.cycleInterval())
		case <-e.quit:
			return
		}
	}
}

func (e *Executor) setHz(hz int) {
	e.hz = hz
	select {
	case e.hzChanged <- struct{}{}:
	default:
	}
}

func (e *Executor) cycleInterval() time.Duration {
	return time.Second / time.Duration(e.hz)
}

func (e *Executor) cycleBudget() time.Duration {
	return e.cycleInterval() / 4
}

func (e *Executor) run(req execRequest) {
	defer func() {
		if r := recover(); r != nil {
			cmd := ""
			if len(req.req) > 0 {
				cmd = string(req.req[0])
			} else if len(req.batch) > 0 && len(req.batch[0]) > 0 {
				cmd = string(req.batch[0][0])
			}
			slog.Error("command handler panicked", "command", cmd, "panic", r)
			req.out.Reset()
			req.out.Error("ERR internal error")
		}
		select {
		case req.done <- struct{}{}:
		default:
		}
	}()
	if req.ctx != nil && req.ctx.Err() != nil {
		return
	}
	if len(req.batch) > 0 {
		for _, single := range req.batch {
			subReq := execRequest{
				req:  single,
				out:  req.out,
				conn: req.conn,
				ctx:  req.ctx,
			}
			e.runSingle(subReq)
		}
		return
	}
	e.runSingle(req)
}

func (e *Executor) runSingle(req execRequest) {
	tx := &req.conn.tx
	if e.aofWriteBlocked(req.req, req.out) {
		tx.dirty = true
		return
	}
	if e.readOnlyBlocked(req.req, req.out, req.conn) {
		tx.dirty = true
		return
	}
	blocked, evicted := e.maxmemoryGate(req.req, req.out, req.conn)
	if err := e.persistEvictions(evicted); err != nil {
		slog.Error("AOF eviction append failed", "keys", len(evicted), "err", err)
		req.out.Reset()
		req.out.Error("MISCONF Errors writing to the AOF file: " + err.Error())
		tx.dirty = true
		return
	}
	if blocked {
		tx.dirty = true
		return
	}
	e.execute(req, tx)
	if req.replBytes <= 0 || (len(req.out.Bytes()) > 0 && req.out.Bytes()[0] == '-') {
		return
	}
	offset := e.repl.SlaveReplOffset() + req.replBytes
	if req.syncGen != 0 {
		e.repl.SetSlaveReplOffsetForSync(offset, req.syncGen)
		return
	}
	e.repl.SetSlaveReplOffset(offset)
}

// readOnlyBlocked rejects write commands from regular clients when node is a read-only replica.
// Replicated writes from the master stream (isReplicaClient) are permitted (ADR-017).
func (e *Executor) readOnlyBlocked(req protocol.Request, out *protocol.Encoder, conn *ConnState) bool {
	if conn != nil && conn.isReplicaClient {
		return false
	}
	if e.repl == nil || !e.repl.IsSlave() || !e.repl.SlaveReadOnly() || len(req) == 0 {
		return false
	}
	cmd, ok := e.commands.Lookup(req[0])
	if !ok || !cmd.Write || cmd.Name == "exec" {
		return false
	}
	out.Error(protocol.ReadOnlyReplica)
	return true
}

// persistEvictions records one DEL for every key evicted while preparing this
// command, so a restart cannot resurrect them and replicas remove them.
func (e *Executor) persistEvictions(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	args := make([][]byte, 0, len(keys)+1)
	args = append(args, []byte("DEL"))
	for _, key := range keys {
		args = append(args, []byte(key))
	}
	cmdBytes := persist.EncodeCommand(args...)
	if e.aof != nil {
		if err := e.aof.Append(cmdBytes); err != nil {
			return err
		}
	}
	if e.repl != nil && e.repl.IsMaster() {
		e.repl.FeedBacklog(cmdBytes)
	}
	return nil
}

// aofWriteBlocked refuses a write command once the AOF has failed, so the
// server does not accept mutations it cannot persist. Reads still work.
func (e *Executor) aofWriteBlocked(req protocol.Request, out *protocol.Encoder) bool {
	if e.aof == nil || len(req) == 0 {
		return false
	}
	err := e.aof.Failed()
	if err == nil {
		return false
	}
	cmd, ok := e.commands.Lookup(req[0])
	if !ok || !cmd.Write {
		return false
	}
	out.Error("MISCONF Errors writing to the AOF file: " + err.Error())
	return true
}

// maxmemoryGate runs before dispatch. While over maxmemory under an LRU policy
// it evicts victims, then refuses a still-over memory-growing command (DenyOOM)
// with Redis's reply. Unknown-command and arity failures fall through so they
// keep their own errors, as in Redis. CONFIG SET rejects unimplemented
// policies (LFU/random/ttl), so a stored policy always has real behavior.
func (e *Executor) maxmemoryGate(req protocol.Request, out *protocol.Encoder, conn *ConnState) (blocked bool, evicted []string) {
	if (conn != nil && conn.isReplicaClient) || e.maxmemory <= 0 || len(req) == 0 {
		return false, nil
	}
	cmd, ok := e.commands.Lookup(req[0])
	if !ok || !cmd.accepts(len(req)) {
		return false, nil
	}
	if e.store.MemoryUsage().Total <= e.maxmemory {
		return false, nil
	}
	policy := evict.ParsePolicy(e.maxmemoryPolicy)
	if policy.Evicts() && (e.aof == nil || e.aof.Failed() == nil) {
		evicted = e.performEvictions(policy)
		if e.store.MemoryUsage().Total <= e.maxmemory {
			return false, evicted
		}
	}
	if !cmd.DenyOOM {
		return false, evicted
	}
	out.Error(protocol.OutOfMemory)
	return true, evicted
}

// performEvictions removes least-recently-used keys until the keyspace fits
// maxmemory or nothing is evictable, returning the evicted keys. Each round
// samples evict.Samples keys into a bounded pool and evicts the most idle, as
// Redis's approximated LRU does.
func (e *Executor) performEvictions(policy evict.Policy) []string {
	volatileOnly := policy.Volatile()
	e.evictPool = evict.Pool{}
	var evicted []string
	for e.store.MemoryUsage().Total > e.maxmemory {
		for _, s := range e.store.SampleKeys(volatileOnly, evict.Samples) {
			e.evictPool.Insert(s.Key, s.Idle)
		}
		key, idle, ok := e.evictPool.PopBest()
		if !ok {
			return evicted
		}
		if !e.store.Delete(key) {
			continue
		}
		evicted = append(evicted, key)
		e.stats.EvictedKeys.Add(1)
		if e.events != nil {
			e.events.Emit(events.Event{
				Version: events.Version,
				Type:    events.Evict,
				Key:     key,
				Policy:  string(policy),
				Idle:    idle,
				Sampled: evict.Samples,
			})
		}
	}
	return evicted
}
