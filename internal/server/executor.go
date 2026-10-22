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

	active    bool
	hz        int
	hzChanged chan struct{}
	clock     clock.Clock
	expiry    *expire.Cycle
	store     *store.Store
	stats     *metrics.Stats
	start     time.Time

	maxmemory       int64
	maxmemoryPolicy string

	// events is the opt-in internal tap (ADR-014); nil disables it. evictPool
	// carries candidates within one eviction round.
	events    events.Sink
	evictPool evict.Pool

	aof             *persist.Writer
	rewriteInFlight atomic.Bool

	closeOnce sync.Once
}

type execRequest struct {
	req  protocol.Request
	out  *protocol.Encoder
	conn *ConnState
	done chan struct{}
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
	if c == nil {
		c = clock.Real{}
	}
	if st == nil {
		st = store.NewWithClock(c)
	}
	e := &Executor{
		requests:        make(chan execRequest, requestQueueSize),
		quit:            make(chan struct{}),
		done:            make(chan struct{}),
		active:          active,
		hz:              defaultHz,
		hzChanged:       make(chan struct{}, 1),
		clock:           c,
		expiry:          expire.New(st, c),
		store:           st,
		stats:           metrics.New(DefaultMaxConns),
		start:           c.Now(),
		maxmemoryPolicy: "noeviction",
		aof:             aof,
	}
	if reg == nil {
		reg = NewRegistry()
		registerDataCommands(reg, st)
		registerConfigCommands(reg, e)
		registerInfoCommands(reg, e)
		registerConnectionCommands(reg, e)
		registerPersistenceCommands(reg, e)
		registerTransactionCommands(reg)
	}
	e.commands = reg
	go e.loop()
	return e
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
	if conn == nil {
		conn = &ConnState{}
	}
	done := make(chan struct{})
	select {
	case e.requests <- execRequest{req: req, out: out, conn: conn, done: done}:
	case <-e.quit:
		return
	case <-e.done:
		return
	}
	// Never return while the executor could still write through out.
	select {
	case <-done:
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
		close(e.quit)
	})
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Executor) loop() {
	defer close(e.done)
	if !e.active {
		for {
			select {
			case req := <-e.requests:
				e.run(req)
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
		case <-timer.C:
			e.expiry.Run(e.clock.Now().UnixMilli(), e.cycleBudget())
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
			}
			slog.Error("command handler panicked", "command", cmd, "panic", r)
			req.out.Reset()
			req.out.Error("ERR internal error")
		}
		close(req.done)
	}()
	tx := &req.conn.tx
	if e.aofWriteBlocked(req.req, req.out) {
		tx.dirty = true
		return
	}
	blocked, evicted := e.maxmemoryGate(req.req, req.out)
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
}

// persistEvictions records one DEL for every key evicted while preparing this
// command, so a restart cannot resurrect them.
func (e *Executor) persistEvictions(keys []string) error {
	if e.aof == nil || len(keys) == 0 {
		return nil
	}
	args := make([][]byte, 0, len(keys)+1)
	args = append(args, []byte("DEL"))
	for _, key := range keys {
		args = append(args, []byte(key))
	}
	return e.aof.Append(persist.EncodeCommand(args...))
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
func (e *Executor) maxmemoryGate(req protocol.Request, out *protocol.Encoder) (blocked bool, evicted []string) {
	if e.maxmemory <= 0 || len(req) == 0 {
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
