package differential

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/silent-knight19/antares/internal/server"
)

func startAntares(t *testing.T) string {
	t.Helper()
	exec := server.NewExecutor(nil, nil)
	t.Cleanup(exec.Close)
	srv := server.New(server.Config{Addr: "127.0.0.1:0"}, exec.Handle)
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

func newClient(t *testing.T, addr string) *redis.Client {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{
		Addr:            addr,
		Protocol:        2,
		DisableIdentity: true,
	})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}
