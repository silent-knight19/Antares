// Package client is the minimal RESP2 client used by the Observatory bridge
// and the replica apply loop; verification uses independent go-redis (ADR-017).
//
// It deliberately has no pool, no cluster support, and no pub/sub: one
// connection, one command at a time, caller-supplied deadlines.
package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
)

// DefaultTimeout bounds a connect and each command when the caller's context
// has no earlier deadline.
const DefaultTimeout = 5 * time.Second

// Result is one completed command: the parsed reply plus the canonical wire
// bytes for both directions and the measured round trip.
type Result struct {
	Reply   Reply
	Request []byte
	Raw     []byte
	Latency time.Duration
}

type config struct {
	timeout time.Duration
	clock   clock.Clock
}

// Option configures a Client.
type Option func(*config)

// WithTimeout sets the per-command deadline used when ctx has none.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithClock overrides the clock used for deadlines and latency (tests).
func WithClock(c clock.Clock) Option {
	return func(cfg *config) { cfg.clock = c }
}

// Client is a single RESP2 connection. It is safe for concurrent use by
// serializing commands with a mutex.
type Client struct {
	mu      sync.Mutex
	conn    net.Conn
	r       *bufio.Reader
	timeout time.Duration
	clock   clock.Clock
	closed  bool
}

// Dial connects to addr (host:port) and returns a RESP2 client.
func Dial(ctx context.Context, addr string, opts ...Option) (*Client, error) {
	cfg := config{timeout: DefaultTimeout, clock: clock.Real{}}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.timeout <= 0 {
		cfg.timeout = DefaultTimeout
	}
	dialer := net.Dialer{Timeout: cfg.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("client: dial %s: %w", addr, err)
	}
	return &Client{
		conn:    conn,
		r:       bufio.NewReader(conn),
		timeout: cfg.timeout,
		clock:   cfg.clock,
	}, nil
}

// Close closes the connection. It is safe to call more than once.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

// Execute sends one command and reads its reply. A Redis error reply is
// returned as Reply{Kind: Error}; only transport or malformed-reply failures
// return a non-nil error.
func (c *Client) Execute(ctx context.Context, args ...[]byte) (Result, error) {
	if len(args) == 0 {
		return Result{}, errors.New("client: empty command")
	}
	request := encodeRequest(args)
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return Result{}, errors.New("client: closed")
	}

	deadline := c.clock.Now().Add(c.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		return Result{}, fmt.Errorf("client: set deadline: %w", err)
	}
	start := c.clock.Now()
	if _, err := c.conn.Write(request); err != nil {
		return Result{}, fmt.Errorf("client: write: %w", err)
	}
	reply, err := parseReply(c.r, 0)
	if err != nil {
		return Result{}, err
	}
	latency := c.clock.Since(start)
	return Result{
		Reply:   reply,
		Request: request,
		Raw:     reply.encode(),
		Latency: latency,
	}, nil
}

// Do runs one command with string arguments, convenient for HTTP handlers.
func (c *Client) Do(ctx context.Context, args ...string) (Result, error) {
	buf := make([][]byte, len(args))
	for i, arg := range args {
		buf[i] = []byte(arg)
	}
	return c.Execute(ctx, buf...)
}

func encodeRequest(args [][]byte) []byte {
	e := protocol.GetEncoder()
	defer e.Release()
	e.ArrayHeader(len(args))
	for _, arg := range args {
		e.Bulk(arg)
	}
	// Copy out of the pooled encoder before Release recycles the buffer.
	out := make([]byte, len(e.Bytes()))
	copy(out, e.Bytes())
	return out
}
