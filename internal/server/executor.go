package server

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/expire"
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

	closeOnce sync.Once
}

type execRequest struct {
	req  protocol.Request
	out  *protocol.Encoder
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

func newExecutor(st *store.Store, reg *Registry, active bool, c clock.Clock) *Executor {
	if c == nil {
		c = clock.Real{}
	}
	if st == nil {
		st = store.NewWithClock(c)
	}
	e := &Executor{
		requests:  make(chan execRequest, requestQueueSize),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
		active:    active,
		hz:        defaultHz,
		hzChanged: make(chan struct{}, 1),
		clock:     c,
		expiry:    expire.New(st, c),
	}
	if reg == nil {
		reg = NewRegistry()
		registerStringCommands(reg, st)
		registerListCommands(reg, st)
		registerHashCommands(reg, st)
		registerSetCommands(reg, st)
		registerTTLCommands(reg, st)
		registerConfigCommands(reg, e)
	}
	e.commands = reg
	go e.loop()
	return e
}

// Handle implements server.Handler: it queues the request and waits for the
// executor to write the reply into out. A closed executor drops the request
// rather than blocking the connection goroutine forever.
func (e *Executor) Handle(req protocol.Request, out *protocol.Encoder) {
	done := make(chan struct{})
	select {
	case e.requests <- execRequest{req: req, out: out, done: done}:
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
	e.commands.Dispatch(req.req, req.out)
}
