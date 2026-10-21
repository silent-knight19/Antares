package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/client"
	"github.com/silent-knight19/antares/internal/server"
)

func startServer(t *testing.T) string {
	t.Helper()
	exec := server.NewExecutor(nil, nil)
	t.Cleanup(exec.Close)
	srv := server.New(server.Config{Addr: "127.0.0.1:0", Stats: exec.Stats()}, exec.Handle)
	if err := srv.Start(); err != nil {
		t.Fatalf("start antares: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv.Addr().String()
}

func TestClientAgainstAntares(t *testing.T) {
	addr := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	res, err := c.Do(ctx, "PING")
	if err != nil {
		t.Fatalf("PING: %v", err)
	}
	if res.Reply.Kind != client.KindSimple || res.Reply.Text != "PONG" {
		t.Fatalf("PING reply = %+v", res.Reply)
	}
	if got, want := string(res.Request), "*1\r\n$4\r\nPING\r\n"; got != want {
		t.Fatalf("request = %q, want %q", got, want)
	}
	if got, want := string(res.Raw), "+PONG\r\n"; got != want {
		t.Fatalf("raw = %q, want %q", got, want)
	}

	if _, err := c.Do(ctx, "SET", "k", "v"); err != nil {
		t.Fatalf("SET: %v", err)
	}
	res, err = c.Do(ctx, "GET", "k")
	if err != nil || res.Reply.Kind != client.KindBulk || res.Reply.Text != "v" {
		t.Fatalf("GET = %+v, %v", res.Reply, err)
	}

	res, err = c.Do(ctx, "GET", "missing")
	if err != nil || res.Reply.Kind != client.KindNull {
		t.Fatalf("GET missing = %+v, %v", res.Reply, err)
	}

	res, err = c.Do(ctx, "MGET", "k", "missing")
	if err != nil {
		t.Fatalf("MGET: %v", err)
	}
	if res.Reply.Kind != client.KindArray || len(res.Reply.Array) != 2 {
		t.Fatalf("MGET reply = %+v", res.Reply)
	}
	if res.Reply.Array[0].Kind != client.KindBulk || res.Reply.Array[1].Kind != client.KindNull {
		t.Fatalf("MGET elements = %+v", res.Reply.Array)
	}

	res, err = c.Do(ctx, "INCR", "k")
	if err != nil {
		t.Fatalf("INCR: %v", err)
	}
	if res.Reply.Kind != client.KindError {
		t.Fatalf("INCR reply = %+v, want error", res.Reply)
	}

	if _, err := c.Do(ctx, "SET", "bin", "\x00\r\n\xff"); err != nil {
		t.Fatalf("SET binary: %v", err)
	}
	res, err = c.Do(ctx, "GET", "bin")
	if err != nil || res.Reply.Text != "\x00\r\n\xff" {
		t.Fatalf("GET binary = %q, %v", res.Reply.Text, err)
	}
	if res.Latency < 0 {
		t.Fatalf("latency = %v", res.Latency)
	}
}

func TestClientReportsClosedConnection(t *testing.T) {
	addr := startServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := client.Dial(ctx, addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := c.Do(ctx, "PING"); err == nil {
		t.Fatal("command after close succeeded")
	}
}
