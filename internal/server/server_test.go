package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func startServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv := New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	})
	return srv
}

func dial(t *testing.T, srv *Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func expectClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err == nil {
		t.Fatal("read succeeded; want closed connection")
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("connection still open; want closed")
	}
}

func TestRetryAccept(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"closed listener stops the loop", net.ErrClosed, false},
		{"wrapped closed listener stops the loop", fmt.Errorf("accept tcp: %w", net.ErrClosed), false},
		{"transient error is retried", errors.New("too many open files"), true},
	}
	for _, tt := range tests {
		if got := retryAccept(tt.err); got != tt.want {
			t.Errorf("%s: retryAccept(%v) = %v, want %v", tt.name, tt.err, got, tt.want)
		}
	}
}

func TestAcceptAndClose(t *testing.T) {
	srv := startServer(t, Config{})
	conn := dial(t, srv)
	waitFor(t, "connection tracked", func() bool { return srv.Conns() == 1 })
	conn.Close()
	waitFor(t, "connection released", func() bool { return srv.Conns() == 0 })
}

func TestMaxConnsRejectsExtra(t *testing.T) {
	srv := startServer(t, Config{MaxConns: 2})
	dial(t, srv)
	dial(t, srv)
	waitFor(t, "two connections tracked", func() bool { return srv.Conns() == 2 })

	extra := dial(t, srv)
	expectClosed(t, extra)
	if got := srv.Conns(); got != 2 {
		t.Fatalf("Conns() = %d, want 2", got)
	}
}

func TestIdleTimeoutClosesConn(t *testing.T) {
	srv := startServer(t, Config{IdleTimeout: 100 * time.Millisecond})
	conn := dial(t, srv)
	waitFor(t, "connection tracked", func() bool { return srv.Conns() == 1 })
	expectClosed(t, conn)
	waitFor(t, "connection released", func() bool { return srv.Conns() == 0 })
}

func TestIdleTimeoutResetsOnActivity(t *testing.T) {
	srv := startServer(t, Config{IdleTimeout: 500 * time.Millisecond})
	conn := dial(t, srv)
	waitFor(t, "connection tracked", func() bool { return srv.Conns() == 1 })

	for i := 0; i < 8; i++ {
		if _, err := conn.Write([]byte{'x'}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got := srv.Conns(); got != 1 {
		t.Fatalf("connection closed while active: Conns() = %d, want 1", got)
	}
	expectClosed(t, conn)
}

func TestGracefulShutdownDrainsClients(t *testing.T) {
	srv := startServer(t, Config{})
	conn := dial(t, srv)
	waitFor(t, "connection tracked", func() bool { return srv.Conns() == 1 })

	shutdownErr := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownErr <- srv.Shutdown(ctx)
	}()

	waitFor(t, "listener closed", func() bool {
		probe, err := net.DialTimeout("tcp", srv.Addr().String(), 50*time.Millisecond)
		if err != nil {
			return true
		}
		probe.Close()
		return false
	})

	conn.Close()
	if err := <-shutdownErr; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestShutdownForcesRemainingConns(t *testing.T) {
	srv := startServer(t, Config{})
	conn := dial(t, srv)
	waitFor(t, "connection tracked", func() bool { return srv.Conns() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context.DeadlineExceeded", err)
	}
	expectClosed(t, conn)
}

func TestShutdownIdempotent(t *testing.T) {
	srv := startServer(t, Config{})
	ctx := context.Background()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestShutdownExpiredContextReturnsNil(t *testing.T) {
	srv := startServer(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v, want nil (nothing was force-closed)", err)
	}
}

func TestStartTwiceFails(t *testing.T) {
	srv := startServer(t, Config{})
	if err := srv.Start(); err == nil {
		t.Fatal("second Start succeeded; want error")
	}
}

func TestStartAfterShutdownFails(t *testing.T) {
	srv := startServer(t, Config{})
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := srv.Start(); err == nil {
		t.Fatal("Start after Shutdown succeeded; want error")
	}
}
