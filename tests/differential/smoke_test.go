// Package differential holds cross-implementation checks: an independent
// Redis client (go-redis, ADR-017) speaking to Antares. This file is the P1.6
// smoke; P2.6 grows it into the full command-diff harness, P9.1 the report.
package differential

import (
	"context"
	"testing"
)

// TestPingEchoSmoke drives a real client against an in-process Antares
// (executor + P2.1 registry) to prove the wire path end to end.
func TestPingEchoSmoke(t *testing.T) {
	rdb := newClient(t, startAntares(t))
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
