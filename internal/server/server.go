package server

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
)

const (
	DefaultAddr     = "127.0.0.1:6380"
	DefaultMaxConns = 10000

	readBufferSize   = 16 * 1024 // PROTO_IOBUF_LEN: batches pipeline reads, fewer flushes
	acceptRetryDelay = 100 * time.Millisecond
)

// Handler executes one parsed request and writes its reply to out. The
// executor replaces this at P2.2; P1.4 hands requests to it in arrival order.
type Handler func(req protocol.Request, out *protocol.Encoder)

type Config struct {
	Addr        string
	MaxConns    int
	IdleTimeout time.Duration
}

type Server struct {
	cfg     Config
	handler Handler

	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	stopped bool

	wg       sync.WaitGroup
	stopOnce sync.Once
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
	return &Server{
		cfg:     cfg,
		handler: handler,
		conns:   make(map[net.Conn]struct{}),
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
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		ln := s.ln
		s.mu.Unlock()
		if ln != nil {
			ln.Close()
		}
	})

	drained := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		if !s.forceCloseConns() {
			<-drained
			return nil
		}
		<-drained
		return ctx.Err()
	}
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

		s.mu.Lock()
		if s.stopped || (s.cfg.MaxConns > 0 && len(s.conns) >= s.cfg.MaxConns) {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

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
		conn.Close()
	}()

	br := bufio.NewReaderSize(idleConn{Conn: conn, timeout: s.cfg.IdleTimeout}, readBufferSize)
	bw := bufio.NewWriter(conn)
	enc := protocol.GetEncoder()
	defer enc.Release()

	for {
		req, err := protocol.ReadRequest(br)
		if err != nil {
			// Protocol errors get Redis's reply before the close; I/O errors
			// (EOF, idle timeout, reset) close silently, as Redis does.
			var pe *protocol.ProtocolError
			if errors.As(err, &pe) {
				enc.Reset()
				enc.Error("ERR Protocol error: " + pe.Msg)
				bw.Write(enc.Bytes())
				bw.Flush()
			}
			return
		}
		if s.handler != nil {
			enc.Reset()
			s.handler(req, enc)
			bw.Write(enc.Bytes())
		}
		// Flush once the pipeline is drained — all commands already buffered
		// are executed first, then replies go out in one batch (classic Redis
		// pipeline behavior). The kernel may still hold more bytes, which
		// only costs an extra flush.
		if br.Buffered() == 0 {
			if err := bw.Flush(); err != nil {
				return
			}
		}
	}
}

// idleConn resets the read deadline on every underlying read, so any byte of
// activity keeps the connection alive (Redis lastinteraction semantics) —
// not just completed commands.
type idleConn struct {
	net.Conn
	timeout time.Duration
}

func (c idleConn) Read(p []byte) (int, error) {
	if c.timeout > 0 {
		c.Conn.SetReadDeadline(time.Now().Add(c.timeout))
	}
	return c.Conn.Read(p)
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
