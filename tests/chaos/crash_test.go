// Package chaos exercises the crash-durability contract: every write the
// client acknowledged before SIGKILL must be present after restart.
package chaos

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestCrashRecovery(t *testing.T) {
	if os.Getenv("ANTARES_CRASH_TEST") == "" {
		t.Skip("set ANTARES_CRASH_TEST=1 (make crash-test) to run the SIGKILL harness")
	}
	bin := os.Getenv("ANTARES_BIN")
	if bin == "" {
		bin = filepath.Join("..", "..", "bin", "antares")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("antares binary %s not found: %v", bin, err)
	}
	iterations := 100
	if v := os.Getenv("CRASH_ITERATIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("CRASH_ITERATIONS = %q", v)
		}
		iterations = n
	}
	for i := 0; i < iterations; i++ {
		if i%10 == 0 {
			t.Logf("iteration %d/%d", i, iterations)
		}
		runIteration(t, bin, i)
	}
}

func runIteration(t *testing.T, bin string, seed int) {
	t.Helper()
	dir := t.TempDir()
	aofPath := filepath.Join(dir, "appendonly.aof")
	port, err := freePort()
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	start := func() (*exec.Cmd, *bytes.Buffer) {
		var output bytes.Buffer
		cmd := exec.Command(bin, "-bind", "127.0.0.1", "-port", strconv.Itoa(port), "-appendonly", "-aof-file", aofPath, "-appendfsync", "always")
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err := cmd.Start(); err != nil {
			t.Fatalf("start antares: %v", err)
		}
		waitReady(t, addr, cmd, &output)
		return cmd, &output
	}

	cmd, _ := start()
	client := redisClient(addr)

	acked := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("key:%d", i)
			value := strings.Repeat("x", 1+i%16)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			err := client.Set(ctx, key, value, 0).Err()
			cancel()
			if err != nil {
				return
			}
			mu.Lock()
			acked[key] = value
			mu.Unlock()
		}
	}()

	time.Sleep(time.Duration(10+seed%30) * time.Millisecond)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait()
	close(stop)
	wg.Wait()
	client.Close()

	mu.Lock()
	snapshot := make(map[string]string, len(acked))
	for key, value := range acked {
		snapshot[key] = value
	}
	mu.Unlock()
	if len(snapshot) == 0 {
		t.Fatal("no acknowledged writes before SIGKILL; nothing to verify")
	}

	restarted, _ := start()
	verify := redisClient(addr)
	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	values, err := verify.MGet(ctx, keys...).Result()
	cancel()
	if err != nil {
		signalAndWait(t, restarted)
		t.Fatalf("MGET after restart: %v", err)
	}
	for i, key := range keys {
		if values[i] != snapshot[key] {
			signalAndWait(t, restarted)
			t.Fatalf("acked key %q = %v after restart, want %q", key, values[i], snapshot[key])
		}
	}
	verify.Close()
	signalAndWait(t, restarted)
}

func redisClient(addr string) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:            addr,
		Protocol:        2,
		DisableIdentity: true,
		MaxRetries:      -1,
		PoolSize:        1,
		DialTimeout:     100 * time.Millisecond,
		ReadTimeout:     250 * time.Millisecond,
		WriteTimeout:    250 * time.Millisecond,
	})
}

func waitReady(t *testing.T, addr string, cmd *exec.Cmd, output *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	t.Fatalf("server did not start on %s: %s", addr, output.String())
}

func signalAndWait(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
