package server

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
)

func startServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	return startServerHandler(t, cfg, nil)
}

func startServerHandler(t *testing.T, cfg Config, handler Handler) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	srv := New(cfg, handler)
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
	extra.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(extra)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read refusal: %v", err)
	}
	if want := "-ERR max number of clients reached\r\n"; line != want {
		t.Fatalf("got %q, want %q", line, want)
	}
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

func TestShutdownDoesNotWaitForStuckHandler(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	srv := startServerHandler(t, Config{}, func(_ protocol.Request, out *protocol.Encoder) {
		close(entered)
		<-release
		out.Simple("OK")
	})
	conn := dial(t, srv)
	if _, err := conn.Write([]byte("PING\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	shutdownErr := make(chan error, 1)
	go func() { shutdownErr <- srv.Shutdown(ctx) }()
	select {
	case err := <-shutdownErr:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown waited for a handler that ignored its deadline")
	}

	second := make(chan error, 1)
	go func() { second <- srv.Shutdown(context.Background()) }()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Shutdown = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Shutdown blocked after forced shutdown")
	}
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

func TestPipelineFlushesWithoutFurtherInput(t *testing.T) {
	srv := startServerHandler(t, Config{}, func(req protocol.Request, out *protocol.Encoder) {
		out.Simple("PONG")
	})
	conn := dial(t, srv)
	if _, err := conn.Write([]byte("*1\r\n$4\r\nPING\r\n*1\r\n$4\r\nPING\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	want := "+PONG\r\n+PONG\r\n"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("replies not flushed: %v", err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPipelineTenThousandCommands(t *testing.T) {
	srv := startServerHandler(t, Config{}, func(req protocol.Request, out *protocol.Encoder) {
		out.Bulk(req[1])
	})
	conn := dial(t, srv)

	const n = 10000
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		fmt.Fprintf(&buf, "*2\r\n$4\r\nECHO\r\n$5\r\n%05d\r\n", i)
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		t.Fatalf("write pipeline: %v", err)
	}

	br := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	for i := 0; i < n; i++ {
		hdr, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reply %d header: %v", i, err)
		}
		if hdr != "$5\r\n" {
			t.Fatalf("reply %d header = %q, want $5", i, hdr)
		}
		payload := make([]byte, 7)
		if _, err := io.ReadFull(br, payload); err != nil {
			t.Fatalf("reply %d payload: %v", i, err)
		}
		if want := fmt.Sprintf("%05d\r\n", i); string(payload) != want {
			t.Fatalf("reply %d = %q, want %q (order broken)", i, payload, want)
		}
	}
	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if b, err := br.ReadByte(); err == nil {
		t.Fatalf("unexpected extra byte %q after %d replies", b, n)
	} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("post-pipeline read: %v, want timeout (idle, no extra replies)", err)
	}
}

// A complete command followed by an incomplete one must still get its reply:
// captured from redis 7.4.11, "PING\r\nECH" gets +PONG while the partial
// inline command waits for the rest of its bytes, and the reply survives a
// half-close instead of being discarded with the connection.
func TestRepliesFlushBeforeWaitingOnPartialCommand(t *testing.T) {
	srv := startServerHandler(t, Config{}, func(req protocol.Request, out *protocol.Encoder) {
		out.Simple("PONG")
	})
	const input = "PING\r\nECH"
	const want = "+PONG\r\n"

	conn := dial(t, srv)
	if _, err := conn.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("reply withheld behind partial command: %v", err)
	}
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	conn2 := dial(t, srv)
	if _, err := conn2.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn2.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("half-close: %v", err)
	}
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	got2, err := io.ReadAll(conn2)
	if err != nil {
		t.Fatalf("read after half-close: %v", err)
	}
	if string(got2) != want {
		t.Fatalf("after half-close got %q, want %q", got2, want)
	}
}

func TestProtocolErrorRepliesAndCloses(t *testing.T) {
	srv := startServerHandler(t, Config{}, func(req protocol.Request, out *protocol.Encoder) {
		out.Simple("PONG")
	})
	conn := dial(t, srv)
	if _, err := conn.Write([]byte("*abc\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	br := bufio.NewReader(conn)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read error reply: %v", err)
	}
	if want := "-ERR Protocol error: invalid multibulk length\r\n"; line != want {
		t.Fatalf("got %q, want %q", line, want)
	}
	conn.SetReadDeadline(time.Time{})
	expectClosed(t, conn)
}
