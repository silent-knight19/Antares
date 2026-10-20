package server

import (
	"fmt"
	"sync"
	"testing"

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
		Flags: FlagWrite,
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
