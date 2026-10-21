package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

// One executor goroutine means a deliberately unguarded counter stays correct
// and the race detector stays quiet under concurrent clients.
func TestExecutorSerializesCommands(t *testing.T) {
	st := store.New()
	reg := NewRegistry()

	var seq int
	var seen []int
	reg.Register(Command{
		Name:  "seq",
		Arity: 3,
		Handler: func(args [][]byte, out *protocol.Encoder) {
			seq++
			seen = append(seen, seq)
			st.Set(string(args[0]), args[1])
			out.Int(int64(seq))
		},
	})

	exec := NewExecutor(st, reg)
	defer exec.Close()

	const n = 200
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			enc := protocol.GetEncoder()
			defer enc.Release()
			exec.Handle(protocol.Request{
				[]byte("SEQ"),
				[]byte(fmt.Sprintf("key:%d", i)),
				[]byte("value"),
			}, enc)
		}(i)
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("executed %d commands, want %d", len(seen), n)
	}
	for i, v := range seen {
		if v != i+1 {
			t.Fatalf("execution %d observed seq %d; commands interleaved", i, v)
		}
	}
	if st.Len() != n {
		t.Fatalf("store has %d keys, want %d", st.Len(), n)
	}
}

func TestExecutorCloseContextDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	reg := NewRegistry()
	reg.Register(Command{
		Name:  "stuck",
		Arity: -1,
		Handler: func([][]byte, *protocol.Encoder) {
			close(entered)
			<-release
		},
	})
	exec := NewExecutor(nil, reg)
	handled := make(chan struct{})
	go func() {
		enc := protocol.GetEncoder()
		defer enc.Release()
		exec.Handle(protocol.Request{[]byte("STUCK")}, enc)
		close(handled)
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := exec.CloseContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CloseContext = %v, want context.DeadlineExceeded", err)
	}

	close(release)
	if err := exec.CloseContext(context.Background()); err != nil {
		t.Fatalf("CloseContext after release = %v", err)
	}
	select {
	case <-handled:
	case <-time.After(time.Second):
		t.Fatal("Handle did not return after executor stopped")
	}
}

func TestExecutorHandleAfterCloseReturns(t *testing.T) {
	exec := NewExecutor(nil, nil)
	exec.Close()

	done := make(chan struct{})
	go func() {
		runStrings(exec, "PING")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Handle blocked after Close")
	}
}

func TestExecutorRunsActiveExpiry(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	if got := runStrings(exec, "SET", "cold", "v"); got != "+OK\r\n" {
		t.Fatalf("SET cold = %q", got)
	}
	if got := runStrings(exec, "PEXPIRE", "cold", "1"); got != ":1\r\n" {
		t.Fatalf("PEXPIRE cold = %q", got)
	}
	waitFor(t, "active expiry to remove cold key", func() bool {
		return runStrings(exec, "DBSIZE") == ":0\r\n"
	})
}

func TestExecutorRecoversFromPanic(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Command{
		Name:  "panic",
		Arity: -1,
		Handler: func([][]byte, *protocol.Encoder) {
			panic("boom")
		},
	})
	exec := NewExecutor(nil, reg)
	defer exec.Close()

	if got := runCommand(exec, []byte("PANIC")); got != "-ERR internal error\r\n" {
		t.Fatalf("panic reply = %q", got)
	}
	if got := runCommand(exec, []byte("PING")); got != "+PONG\r\n" {
		t.Fatalf("executor did not survive panic: %q", got)
	}
}

func TestExecutorDispatch(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	tests := []struct {
		name string
		req  protocol.Request
		want string
	}{
		{"ping", protocol.Request{[]byte("PING")}, "+PONG\r\n"},
		{"wrong arity", protocol.Request{[]byte("ECHO")}, "-ERR wrong number of arguments for 'echo' command\r\n"},
		{"unknown", protocol.Request{[]byte("FOO")}, "-ERR unknown command 'FOO', with args beginning with: \r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := protocol.GetEncoder()
			defer enc.Release()
			exec.Handle(tc.req, enc)
			if got := string(enc.Bytes()); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
