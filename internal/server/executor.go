package server

import (
	"sync"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

// requestQueueSize bounds requests waiting for the executor. Connections block
// when it fills, so TCP backpressure replaces an unbounded queue.
const requestQueueSize = 1024

type Executor struct {
	store    *store.Store
	commands *Registry
	requests chan execRequest
	quit     chan struct{}
	done     chan struct{}

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
	if st == nil {
		st = store.New()
	}
	if reg == nil {
		reg = NewRegistry()
	}
	e := &Executor{
		store:    st,
		commands: reg,
		requests: make(chan execRequest, requestQueueSize),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go e.loop()
	return e
}

// Handle implements server.Handler: it queues the request and waits for the
// executor to write the reply into out.
func (e *Executor) Handle(req protocol.Request, out *protocol.Encoder) {
	done := make(chan struct{})
	e.requests <- execRequest{req: req, out: out, done: done}
	<-done
}

func (e *Executor) Close() {
	e.closeOnce.Do(func() {
		close(e.quit)
		<-e.done
	})
}

func (e *Executor) loop() {
	defer close(e.done)
	for {
		select {
		case req := <-e.requests:
			e.run(req)
		case <-e.quit:
			return
		}
	}
}

func (e *Executor) run(req execRequest) {
	e.commands.Dispatch(req.req, req.out)
	close(req.done)
}
