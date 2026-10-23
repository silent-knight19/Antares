package server

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/metrics"
	"github.com/silent-knight19/antares/internal/protocol"
)

const (
	DefaultAddr     = "127.0.0.1:6380"
	DefaultMaxConns = 10000

	readBufferSize   = 16 * 1024 // PROTO_IOBUF_LEN: batches pipeline reads, fewer flushes
	acceptRetryDelay = 100 * time.Millisecond

	// errMaxClients is Redis's refusal, captured from redis 7.4.11: an
	// over-cap connection gets this reply, then the connection closes.
	errMaxClients   = "-ERR max number of clients reached\r\n"
	refusalDeadline = 5 * time.Second
)

// Handler executes one parsed request and writes its reply to out. The
// executor replaces this at P2.2; P1.4 hands requests to it in arrival order.
// conn carries per-connection state (transaction queue and WATCH set) and is
// unique for the lifetime of the TCP connection.
type Handler func(req protocol.Request, out *protocol.Encoder, conn *ConnState)

type Config struct {
	Addr        string
	MaxConns    int
	IdleTimeout time.Duration
	Stats       *metrics.Stats
}

type Server struct {
	cfg     Config
	handler Handler

	mu          sync.Mutex
	ln          net.Listener
	conns       map[net.Conn]struct{}
	stopped     bool
	forced      bool
	shutdownErr error

	wg        sync.WaitGroup
	stopOnce  sync.Once
	drainOnce sync.Once
	drained   chan struct{}
	forcedCh  chan struct{}
}

// New builds a server. A nil handler drops every request (tests); the real
// binary always supplies one.
func New(cfg Config, handler Handler) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = DefaultMaxConns
	}
	if cfg.Stats != nil {
		cfg.Stats.MaxClients.Store(int64(cfg.MaxConns))
	}
	return &Server{
		cfg:      cfg,
		handler:  handler,
		conns:    make(map[net.Conn]struct{}),
		drained:  make(chan struct{}),
		forcedCh: make(chan struct{}),
	}
}

func (s *Server) Start() error {
	s.mu.Lock()
	if s.ln != nil || s.stopped {
		s.mu.Unlock()
		return errors.New("server: already started or stopped")
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.ln = ln
	s.mu.Unlock()
	if s.cfg.Stats != nil {
		s.cfg.Stats.SetAddr(ln.Addr().String())
	}

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

func (s *Server) Conns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	if s.forced {
		err := s.shutdownErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		ln := s.ln
		s.mu.Unlock()
		if ln != nil {
			ln.Close()
		}
	})
	s.drainOnce.Do(func() {
		go func() {
			s.wg.Wait()
			close(s.drained)
		}()
	})

	select {
	case <-s.drained:
		return nil
	case <-s.forcedCh:
		return s.forceError()
	case <-ctx.Done():
		if !s.forceCloseConns() {
			<-s.drained
			return nil
		}
		return s.forceShutdown(ctx.Err())
	}
}

func (s *Server) forceShutdown(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.forced {
		s.forced = true
		s.shutdownErr = err
		close(s.forcedCh)
	}
	return s.shutdownErr
}

func (s *Server) forceError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdownErr
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if !retryAccept(err) {
				return
			}
			time.Sleep(acceptRetryDelay)
			continue
		}

		maxConns := s.cfg.MaxConns
		if s.cfg.Stats != nil {
			maxConns = int(s.cfg.Stats.MaxClients.Load())
		}

		s.mu.Lock()
		if s.stopped || (maxConns > 0 && len(s.conns) >= maxConns) {
			stopped := s.stopped
			s.mu.Unlock()
			if !stopped {
				conn.SetWriteDeadline(time.Now().Add(refusalDeadline))
				conn.Write([]byte(errMaxClients))
			}
			conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		if s.cfg.Stats != nil {
			s.cfg.Stats.ConnectionOpened()
		}

		s.wg.Add(1)
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		if s.cfg.Stats != nil {
			s.cfg.Stats.ConnectionClosed()
		}
		conn.Close()
	}()

	ic := &connIO{Conn: conn, timeout: s.cfg.IdleTimeout, stats: s.cfg.Stats}
	bw := bufio.NewWriterSize(ic, readBufferSize)
	ic.bw = bw
	br := bufio.NewReaderSize(ic, readBufferSize)
	enc := protocol.GetEncoder()
	defer enc.Release()

	remoteIP := "127.0.0.1"
	remotePort := 0
	if rAddr := conn.RemoteAddr(); rAddr != nil {
		if host, pStr, err := net.SplitHostPort(rAddr.String()); err == nil {
			remoteIP = host
			if p, err := strconv.Atoi(pStr); err == nil {
				remotePort = p
			}
		}
	}

	connState := &ConnState{
		conn:       conn,
		remoteIP:   remoteIP,
		remotePort: remotePort,
		flush:      bw.Flush,
	}
	defer connState.Close()

	for {
		req, err := protocol.ReadRequest(br)
		if err != nil {
			// Protocol errors get Redis's reply before the close; I/O errors
			// (EOF, idle timeout, reset) close silently, as Redis does.
			var pe *protocol.ProtocolError
			if errors.As(err, &pe) && !connState.streaming {
				enc.Reset()
				enc.Error(protocol.ProtocolErrorReply(pe.Msg))
				bw.Write(enc.Bytes())
				bw.Flush()
			}
			return
		}
		if s.handler != nil {
			enc.Reset()
			s.handler(req, enc, connState)
			if !connState.streaming {
				bw.Write(enc.Bytes())
			}
		}
		// No explicit flush here: connIO.Read flushes pending replies right
		// before the parser blocks on the socket, so a burst of fully
		// buffered pipelined commands still goes out in one batch, and
		// replies are never withheld behind a partial trailing command
		// (captured: real Redis answers PING in "PING\r\nECH" immediately).
	}
}

// connIO is the per-connection I/O policy: it resets the read deadline on
// every underlying read so any byte of activity keeps the connection alive
// (Redis lastinteraction semantics), bounds writes by the same idle timeout
// when one is set, and flushes pending replies before the parser blocks
// waiting for more input.
type connIO struct {
	net.Conn
	timeout time.Duration
	bw      *bufio.Writer
	stats   *metrics.Stats
}

func (c *connIO) Read(p []byte) (int, error) {
	if c.bw.Buffered() > 0 {
		if err := c.bw.Flush(); err != nil {
			return 0, err
		}
	}
	if c.timeout > 0 {
		c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	n, err := c.Conn.Read(p)
	if c.stats != nil {
		c.stats.AddNetInput(n)
	}
	return n, err
}

func (c *connIO) Write(p []byte) (int, error) {
	if c.timeout > 0 {
		c.Conn.SetWriteDeadline(time.Now().Add(c.timeout))
	}
	n, err := c.Conn.Write(p)
	if c.stats != nil {
		c.stats.AddNetOutput(n)
	}
	return n, err
}

func (s *Server) forceCloseConns() bool {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		conns = append(conns, conn)
	}
	s.mu.Unlock()

	for _, conn := range conns {
		conn.Close()
	}
	return len(conns) > 0
}

func retryAccept(err error) bool {
	return !errors.Is(err, net.ErrClosed)
}
