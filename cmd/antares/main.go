// Command antares is the Redis-compatible server (docs/PLAN.md §3.1).
//
// P1.5: error strings are capture-exact (unknown command, arity, protocol
// errors). The handler wired here is server.StopgapHandler: PING and ECHO
// until the real executor lands at P2.2.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/silent-knight19/antares/internal/server"
)

var version = "dev"

const shutdownGrace = 5 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	bind := flag.String("bind", "127.0.0.1", "address to bind (localhost only by default, ADR-020)")
	port := flag.Int("port", 6380, "port to listen on (reference Redis uses 6379)")
	maxConns := flag.Int("max-conns", server.DefaultMaxConns, "maximum concurrent connections (negative = unlimited)")
	idleTimeout := flag.Duration("idle-timeout", 0, "close connections idle longer than this (0 = never)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("antares %s\n", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))
	srv := server.New(server.Config{
		Addr:        addr,
		MaxConns:    *maxConns,
		IdleTimeout: *idleTimeout,
	}, server.StopgapHandler)
	if err := srv.Start(); err != nil {
		logger.Error("listen failed", "addr", addr, "err", err)
		os.Exit(1)
	}
	logger.Info("antares listening", "addr", srv.Addr().String(), "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	<-ctx.Done()
	stop()

	logger.Info("shutting down", "grace", shutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("grace period expired; forced remaining connections closed", "err", err)
	}
	logger.Info("stopped")
}
