package linearizability

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"

	"github.com/silent-knight19/antares/internal/client"
	"github.com/silent-knight19/antares/internal/server"
)

type registerInput struct {
	write bool
	value string
}

type registerOutput struct {
	value string
	found bool
}

type registerState struct {
	value string
	found bool
}

func registerModel() porcupine.Model {
	return porcupine.Model{
		Init: func() interface{} { return registerState{} },
		Step: func(state, input, output interface{}) (bool, interface{}) {
			s := state.(registerState)
			in := input.(registerInput)
			out := output.(registerOutput)
			if in.write {
				return true, registerState{value: in.value, found: true}
			}
			return out.found == s.found && out.value == s.value, s
		},
		Equal: func(a, b interface{}) bool {
			return a.(registerState) == b.(registerState)
		},
		DescribeOperation: func(input, output interface{}) string {
			in := input.(registerInput)
			out := output.(registerOutput)
			if in.write {
				return fmt.Sprintf("SET %q", in.value)
			}
			if !out.found {
				return "GET -> nil"
			}
			return fmt.Sprintf("GET -> %q", out.value)
		},
	}
}

func startServer(t *testing.T) string {
	t.Helper()
	exec := server.NewExecutor(nil, nil)
	t.Cleanup(exec.Close)
	srv := server.New(server.Config{Addr: "127.0.0.1:0", Stats: exec.Stats()}, exec.Handle)
	if err := srv.Start(); err != nil {
		t.Fatalf("start Antares: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv.Addr().String()
}

// TestConcurrentRegisterLinearizable records concurrent SET/GET operations on
// one key and asks Porcupine for a total order consistent with real time.
func TestConcurrentRegisterLinearizable(t *testing.T) {
	addr := startServer(t)
	const (
		clients      = 6
		opsPerClient = 60
		key          = "lin:register"
	)

	start := time.Now()
	ready := make(chan struct{})
	errCh := make(chan error, clients)
	var historyMu sync.Mutex
	var history []porcupine.Operation
	var wg sync.WaitGroup

	for id := 0; id < clients; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			conn, err := client.Dial(context.Background(), addr)
			if err != nil {
				errCh <- fmt.Errorf("client %d dial: %w", id, err)
				return
			}
			defer conn.Close()

			rng := rand.New(rand.NewSource(int64(id + 1)))
			<-ready
			for seq := 0; seq < opsPerClient; seq++ {
				in := registerInput{write: rng.Intn(100) < 55}
				if in.write {
					in.value = fmt.Sprintf("c%d-%d", id, seq)
				}

				call := time.Since(start).Nanoseconds()
				var res client.Result
				if in.write {
					res, err = conn.Do(context.Background(), "SET", key, in.value)
				} else {
					res, err = conn.Do(context.Background(), "GET", key)
				}
				ret := time.Since(start).Nanoseconds()
				if err != nil {
					errCh <- fmt.Errorf("client %d op %d: %w", id, seq, err)
					return
				}

				out := registerOutput{}
				if in.write {
					if res.Reply.Kind != client.KindSimple || res.Reply.Text != "OK" {
						errCh <- fmt.Errorf("client %d SET %q: unexpected reply %+v", id, in.value, res.Reply)
						return
					}
				} else {
					switch res.Reply.Kind {
					case client.KindBulk:
						out.found = true
						out.value = res.Reply.Text
					case client.KindNull:
					default:
						errCh <- fmt.Errorf("client %d GET: unexpected reply %+v", id, res.Reply)
						return
					}
				}

				historyMu.Lock()
				history = append(history, porcupine.Operation{
					ClientId: id,
					Input:    in,
					Call:     call,
					Output:   out,
					Return:   ret,
				})
				historyMu.Unlock()
			}
		}(id)
	}
	close(ready)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(history) != clients*opsPerClient {
		t.Fatalf("history has %d operations, want %d", len(history), clients*opsPerClient)
	}
	writes, reads := 0, 0
	for _, op := range history {
		if op.Input.(registerInput).write {
			writes++
		} else {
			reads++
		}
	}
	if writes == 0 || reads == 0 {
		t.Fatalf("history needs both reads and writes: %d writes, %d reads", writes, reads)
	}

	result := porcupine.CheckOperationsTimeout(registerModel(), history, 10*time.Second)
	if result != porcupine.Ok {
		t.Fatalf("linearizability check: %s (%d operations)", result, len(history))
	}
}
