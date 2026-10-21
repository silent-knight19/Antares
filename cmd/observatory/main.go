// Command observatory is the Antares Observatory bridge: REST + SSE over the
// event tap, serving the SPA (docs/PLAN.md §3.2, ADR-013/ADR-015).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var version = "dev"

const (
	shutdownGrace     = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
	idleTimeout       = 2 * time.Minute
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	addr := flag.String("addr", "127.0.0.1:6390", "HTTP listen address")
	antares := flag.String("antares", "127.0.0.1:6380", "Antares server address")
	redis := flag.String("redis", "", "optional reference Redis address for side-by-side")
	web := flag.String("web", "web/dist", "built SPA directory")
	poll := flag.Duration("poll", defaultPollInterval, "INFO poll/SSE interval")
	flag.Parse()

	if *showVersion {
		fmt.Printf("observatory %s\n", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bridge := New(Config{
		Addr:         *addr,
		AntaresAddr:  *antares,
		RedisAddr:    *redis,
		WebDir:       *web,
		Version:      version,
		PollInterval: *poll,
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		logger.Error("listen failed", "addr", *addr, "err", err)
		bridge.Close()
		os.Exit(1)
	}
	srv := &http.Server{
		Handler:           bridge.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	srv.RegisterOnShutdown(bridge.Close)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("serve failed", "err", err)
		}
	}()
	logger.Info("observatory listening", "addr", ln.Addr().String(), "antares", *antares, "redis", *redis, "version", version)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	<-ctx.Done()
	stop()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown deadline expired", "err", err)
	}
	logger.Info("stopped")
}
