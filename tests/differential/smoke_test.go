// Package differential holds cross-implementation checks: an independent
// Redis client (go-redis, ADR-017) speaking to Antares. This file is the P1.6
// smoke; P2.6 grows it into the full command-diff harness, P9.1 the report.
package differential

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/silent-knight19/antares/internal/server"
)

// TestPingEchoSmoke drives a real client against an in-process Antares
// (StopgapHandler until the P2.2 executor) to prove the wire path end to end.
func TestPingEchoSmoke(t *testing.T) {
	srv := server.New(server.Config{Addr: "127.0.0.1:0"}, server.StopgapHandler)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	rdb := redis.NewClient(&redis.Options{
		Addr:            srv.Addr().String(),
		Protocol:        2,
		DisableIdentity: true,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()

	if got := rdb.Ping(ctx).Val(); got != "PONG" {
		t.Fatalf("PING = %q, want PONG", got)
	}
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("PING err: %v", err)
	}
	if got := rdb.Echo(ctx, "hello").Val(); got != "hello" {
		t.Fatalf("ECHO = %q, want hello", got)
	}

	pipe := rdb.Pipeline()
	p1 := pipe.Ping(ctx)
	p2 := pipe.Echo(ctx, "hi")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	if p1.Val() != "PONG" || p2.Val() != "hi" {
		t.Fatalf("pipeline = %q, %q; want PONG, hi", p1.Val(), p2.Val())
	}

	err := rdb.Do(ctx, "NOSUCHCMD").Err()
	if err == nil {
		t.Fatal("unknown command: want error, got nil")
	}
	want := "ERR unknown command 'NOSUCHCMD', with args beginning with: "
	if err.Error() != want {
		t.Fatalf("unknown command err = %q, want %q", err.Error(), want)
	}
}
