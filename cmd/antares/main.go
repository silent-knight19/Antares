// Command antares is the Redis-compatible server (docs/PLAN.md §3.1).
//
// P3.4: the single executor goroutine owns the keyspace, runs the active
// expiry cycle from the injected clock, and dispatches the command registry
// (PING/ECHO/COMMAND plus string, TTL, DBSIZE, and CONFIG hz).
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
	"strings"
	"syscall"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/events"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/server"
	"github.com/silent-knight19/antares/internal/store"
)

var version = "dev"

const shutdownGrace = 5 * time.Second

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	bind := flag.String("bind", "127.0.0.1", "address to bind (localhost only by default, ADR-020)")
	port := flag.Int("port", 6380, "port to listen on (reference Redis uses 6379)")
	maxConns := flag.Int("max-conns", server.DefaultMaxConns, "maximum concurrent connections (0 = default 10000, negative = unlimited)")
	idleTimeout := flag.Duration("idle-timeout", 0, "close connections idle longer than this (0 = never)")
	appendOnly := flag.Bool("appendonly", false, "enable append-only file persistence")
	aofFile := flag.String("aof-file", "appendonly.aof", "AOF path (used with -appendonly)")
	appendFsync := flag.String("appendfsync", "everysec", "AOF fsync policy: always|everysec|no")
	aofRepair := flag.Bool("aof-repair", false, "on a corrupt AOF frame, discard the tail instead of refusing to start")
	eventLog := flag.String("event-log", "", "write internal events as rotated JSONL to this path (empty = disabled, ADR-014)")
	replicaOf := flag.String("replicaof", "", "master host and port to replicate from, e.g. '127.0.0.1 6379'")
	flag.Parse()

	if *showVersion {
		fmt.Printf("antares %s\n", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	addr := net.JoinHostPort(*bind, strconv.Itoa(*port))
	clk := clock.Real{}
	st := store.NewWithClock(clk)

	var aof *persist.Writer
	if *appendOnly {
		policy, err := persist.ParseFsyncPolicy(*appendFsync)
		if err != nil {
			logger.Error("invalid -appendfsync", "value", *appendFsync, "err", err)
			os.Exit(1)
		}
		persist.RemoveStaleRewriteTemps(*aofFile)
		report, err := server.RecoverAOFRepair(*aofFile, st, *aofRepair)
		if err != nil {
			logger.Error("AOF recovery failed", "path", *aofFile, "err", err, "hint", "pass -aof-repair to discard a corrupt tail")
			os.Exit(1)
		}
		if report.Truncated {
			logger.Warn("AOF tail truncated", "path", *aofFile, "records", report.Records, "bytes", report.TruncatedBytes, "reason", report.Reason, "corrupt", report.Corrupt)
		} else if report.Records > 0 {
			logger.Info("AOF recovered", "path", *aofFile, "records", report.Records, "bytes", report.ValidBytes)
		}
		aof, err = persist.Open(*aofFile, policy)
		if err != nil {
			logger.Error("opening AOF failed", "path", *aofFile, "err", err)
			os.Exit(1)
		}
	}

	exec := server.NewExecutorWithClock(st, nil, clk)
	if aof != nil {
		exec = server.NewExecutorWithAOF(st, nil, clk, aof)
	}
	if *replicaOf != "" {
		host, portStr := "", ""
		if strings.Contains(*replicaOf, ":") {
			if h, p, err := net.SplitHostPort(*replicaOf); err == nil {
				host, portStr = h, p
			}
		} else {
			parts := strings.Fields(*replicaOf)
			if len(parts) == 2 {
				host, portStr = parts[0], parts[1]
			}
		}
		if host != "" && portStr != "" {
			if rPort, pErr := strconv.Atoi(portStr); pErr == nil && rPort > 0 && rPort <= 65535 {
				exec.Repl().SetMaster(host, rPort)
				exec.StartReplicaHandshake(host, rPort)
				logger.Info("configured as replica", "master", host, "port", rPort)
			}
		}
	}
	var eventSink *events.JSONLSink
	if *eventLog != "" {
		sink, err := events.NewJSONLSink(*eventLog)
		if err != nil {
			logger.Error("opening event log failed", "path", *eventLog, "err", err)
			exec.Close()
			if aof != nil {
				_ = aof.Close()
			}
			os.Exit(1)
		}
		eventSink = sink
		exec.SetEventSink(eventSink)
	}
	srv := server.New(server.Config{
		Addr:        addr,
		MaxConns:    *maxConns,
		IdleTimeout: *idleTimeout,
		Stats:       exec.Stats(),
	}, exec.Handle)
	if err := srv.Start(); err != nil {
		logger.Error("listen failed", "addr", addr, "err", err)
		exec.Close()
		if eventSink != nil {
			_ = eventSink.Close()
		}
		if aof != nil {
			_ = aof.Close()
		}
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
	if err := exec.CloseContext(shutdownCtx); err != nil {
		logger.Warn("executor shutdown deadline expired", "err", err)
	}
	if eventSink != nil {
		if err := eventSink.Close(); err != nil {
			logger.Warn("event log close failed", "err", err)
		}
	}
	if aof != nil {
		if err := aof.Close(); err != nil {
			logger.Warn("AOF close failed", "err", err)
		}
	}
	logger.Info("stopped")
}
