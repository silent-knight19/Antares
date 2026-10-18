package server

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

const (
	DefaultAddr     = "127.0.0.1:6380"
	DefaultMaxConns = 10000

	readBufferSize   = 4 * 1024
	acceptRetryDelay = 100 * time.Millisecond
)

type Config struct {
	Addr        string
	MaxConns    int
	IdleTimeout time.Duration
}

type Server struct {
	cfg Config

	mu      sync.Mutex
	ln      net.Listener
	conns   map[net.Conn]struct{}
	stopped bool

	wg       sync.WaitGroup
	stopOnce sync.Once
}

func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = DefaultMaxConns
	}
	return &Server{
		cfg:   cfg,
		conns: make(map[net.Conn]struct{}),
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

	buf := make([]byte, readBufferSize)
	for {
		if s.cfg.IdleTimeout > 0 {
			conn.SetReadDeadline(time.Now().Add(s.cfg.IdleTimeout))
		}
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
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
