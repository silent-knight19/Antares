package repl

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func startMockMaster(t *testing.T, fullResync bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen mock master: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		for {
			// Read array length
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if !strings.HasPrefix(line, "*") {
				continue
			}

			// Read first command bulk header + command name
			_, _ = reader.ReadString('\n') // $len
			cmdLine, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(cmdLine))

			switch cmd {
			case "PING":
				_, _ = conn.Write([]byte("+PONG\r\n"))
			case "REPLCONF":
				// Drain remaining args
				// For mock simplicity, reply +OK
				_, _ = conn.Write([]byte("+OK\r\n"))
			case "PSYNC":
				// Drain remaining args
				if fullResync {
					_, _ = conn.Write([]byte("+FULLRESYNC aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa 0\r\n"))
				} else {
					_, _ = conn.Write([]byte("+CONTINUE aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\r\n"))
				}
				return
			}
		}
	}()

	return ln.Addr().String()
}

func TestHandshakeMockMaster(t *testing.T) {
	masterAddr := startMockMaster(t, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := PerformHandshake(ctx, HandshakeConfig{
		MasterAddr:    masterAddr,
		ListeningPort: 6380,
		Timeout:       time.Second,
	})
	if err != nil {
		t.Fatalf("PerformHandshake failed: %v", err)
	}
	defer res.Close()

	if !res.IsFullResync {
		t.Fatalf("expected full resync, got partial")
	}
	if res.MasterReplID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("replID = %s", res.MasterReplID)
	}
	if res.MasterReplOffset != 0 {
		t.Fatalf("offset = %d, want 0", res.MasterReplOffset)
	}
	if len(res.Trace) != 4 {
		t.Fatalf("trace length = %d, want 4", len(res.Trace))
	}

	if err := ReplayHandshake(res.Trace); err != nil {
		t.Fatalf("ReplayHandshake trace validation failed: %v", err)
	}
}

func TestHandshakeUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err := PerformHandshake(ctx, HandshakeConfig{
		MasterAddr: "127.0.0.1:59999", // closed port
		Timeout:    200 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error connecting to unreachable port, got nil")
	}
}

func TestHandshakeLiveRedis(t *testing.T) {
	// Attempt connect to reference Redis if available
	conn, err := net.DialTimeout("tcp", "127.0.0.1:6379", 200*time.Millisecond)
	if err != nil {
		t.Skip("local Redis on 127.0.0.1:6379 not running, skipping live handshake test")
	}
	_ = conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := PerformHandshake(ctx, HandshakeConfig{
		MasterAddr:    "127.0.0.1:6379",
		ListeningPort: 6380,
		Timeout:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("live handshake against Redis 7.4.11 failed: %v", err)
	}
	defer res.Close()

	if !res.IsFullResync {
		t.Fatalf("expected full resync from fresh Redis, got %v", res.IsFullResync)
	}
	if len(res.MasterReplID) != 40 {
		t.Fatalf("invalid replid from real Redis: %q", res.MasterReplID)
	}
	if err := ReplayHandshake(res.Trace); err != nil {
		t.Fatalf("live handshake trace failed validation: %v", err)
	}

	for _, step := range res.Trace {
		fmt.Printf("HANDSHAKE STEP %s: sent %d B, recv %s\n", step.Name, len(step.Sent), step.Recv)
	}
}
