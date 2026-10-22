package server

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func txRequest(args ...string) protocol.Request {
	req := make(protocol.Request, len(args))
	for i, arg := range args {
		req[i] = []byte(arg)
	}
	return req
}

func txRun(exec *Executor, conn *ConnState, args ...string) string {
	enc := protocol.GetEncoder()
	defer enc.Release()
	exec.Handle(txRequest(args...), enc, conn)
	return string(enc.Bytes())
}

func TestTransactionBasics(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"EXEC"}, "-ERR EXEC without MULTI\r\n"},
		{[]string{"DISCARD"}, "-ERR DISCARD without MULTI\r\n"},
		{[]string{"UNWATCH"}, "+OK\r\n"},
		{[]string{"MULTI"}, "+OK\r\n"},
		{[]string{"MULTI"}, "-ERR MULTI calls can not be nested\r\n"},
		{[]string{"SET", "k", "v"}, "+QUEUED\r\n"},
		{[]string{"GET", "k"}, "+QUEUED\r\n"},
		{[]string{"EXEC"}, "*2\r\n+OK\r\n$1\r\nv\r\n"},
		{[]string{"GET", "k"}, "$1\r\nv\r\n"},
		{[]string{"MULTI"}, "+OK\r\n"},
		{[]string{"EXEC"}, "*0\r\n"},
		{[]string{"MULTI"}, "+OK\r\n"},
		{[]string{"SET", "k", "other"}, "+QUEUED\r\n"},
		{[]string{"DISCARD"}, "+OK\r\n"},
		{[]string{"EXEC"}, "-ERR EXEC without MULTI\r\n"},
		{[]string{"GET", "k"}, "$1\r\nv\r\n"},
	} {
		if got := txRun(exec, conn, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestTransactionQueueErrorsAbort(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	if got := txRun(exec, conn, "MULTI"); got != "+OK\r\n" {
		t.Fatalf("MULTI = %q", got)
	}
	if got := txRun(exec, conn, "NOSUCHCMD", "x"); got != "-ERR unknown command 'NOSUCHCMD', with args beginning with: 'x' \r\n" {
		t.Fatalf("unknown = %q", got)
	}
	if got := txRun(exec, conn, "SET", "aborted", "v"); got != "+QUEUED\r\n" {
		t.Fatalf("SET after error = %q", got)
	}
	if got := txRun(exec, conn, "EXEC"); got != "-EXECABORT Transaction discarded because of previous errors.\r\n" {
		t.Fatalf("EXEC = %q", got)
	}
	if got := txRun(exec, conn, "EXISTS", "aborted"); got != ":0\r\n" {
		t.Fatalf("dirty transaction wrote a key: %q", got)
	}
	if got := txRun(exec, conn, "EXEC"); got != "-ERR EXEC without MULTI\r\n" {
		t.Fatalf("EXEC after abort = %q", got)
	}

	if got := txRun(exec, conn, "MULTI"); got != "+OK\r\n" {
		t.Fatalf("MULTI = %q", got)
	}
	if got := txRun(exec, conn, "SET", "onlykey"); got != "-ERR wrong number of arguments for 'set' command\r\n" {
		t.Fatalf("arity = %q", got)
	}
	if got := txRun(exec, conn, "EXEC"); got != "-EXECABORT Transaction discarded because of previous errors.\r\n" {
		t.Fatalf("EXEC after arity = %q", got)
	}
}

func TestTransactionControlErrorsDoNotDirty(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	txRun(exec, conn, "MULTI")
	if got := txRun(exec, conn, "MULTI"); got != "-ERR MULTI calls can not be nested\r\n" {
		t.Fatalf("nested MULTI = %q", got)
	}
	if got := txRun(exec, conn, "WATCH", "k"); got != "-ERR WATCH inside MULTI is not allowed\r\n" {
		t.Fatalf("WATCH inside MULTI = %q", got)
	}
	if got := txRun(exec, conn, "SET", "k", "v"); got != "+QUEUED\r\n" {
		t.Fatalf("SET = %q", got)
	}
	if got := txRun(exec, conn, "EXEC"); got != "*1\r\n+OK\r\n" {
		t.Fatalf("EXEC = %q", got)
	}
}

func TestTransactionRuntimeErrorsStayInReply(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "SET", "k", "str")
	txRun(exec, conn, "INCR", "k")
	txRun(exec, conn, "GET", "k")
	want := "*3\r\n+OK\r\n-ERR value is not an integer or out of range\r\n$3\r\nstr\r\n"
	if got := txRun(exec, conn, "EXEC"); got != want {
		t.Fatalf("EXEC = %q, want %q", got, want)
	}

	txRun(exec, conn, "SET", "n", "1")
	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "INCR", "n")
	txRun(exec, conn, "GET", "n")
	if got := txRun(exec, conn, "EXEC"); got != "*2\r\n:2\r\n$1\r\n2\r\n" {
		t.Fatalf("EXEC mixed = %q", got)
	}
}

func TestUnwatchQueuedInsideMulti(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	txRun(exec, conn, "MULTI")
	if got := txRun(exec, conn, "UNWATCH"); got != "+QUEUED\r\n" {
		t.Fatalf("UNWATCH = %q", got)
	}
	if got := txRun(exec, conn, "EXEC"); got != "*1\r\n+OK\r\n" {
		t.Fatalf("EXEC = %q", got)
	}
}

func TestWatchAbortsOnValueOrExpiryChange(t *testing.T) {
	exec := newStringExec(t)
	a, b := &ConnState{}, &ConnState{}

	setup := func() {
		txRun(exec, a, "SET", "w", "one")
	}

	setup()
	txRun(exec, a, "WATCH", "w")
	txRun(exec, b, "SET", "w", "two")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "w")
	if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
		t.Fatalf("EXEC after change = %q", got)
	}

	// A failed EXEC unwatches: a new transaction runs even though w changed.
	txRun(exec, a, "MULTI")
	txRun(exec, a, "SET", "after", "1")
	if got := txRun(exec, a, "EXEC"); got != "*1\r\n+OK\r\n" {
		t.Fatalf("EXEC after abort = %q", got)
	}

	txRun(exec, a, "WATCH", "w")
	txRun(exec, a, "UNWATCH")
	txRun(exec, b, "SET", "w", "three")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "w")
	if got := txRun(exec, a, "EXEC"); got != "*1\r\n$5\r\nthree\r\n" {
		t.Fatalf("EXEC after UNWATCH = %q", got)
	}

	txRun(exec, a, "WATCH", "w")
	txRun(exec, b, "EXPIRE", "w", "100")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "w")
	if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
		t.Fatalf("EXEC after EXPIRE = %q", got)
	}

	txRun(exec, a, "WATCH", "missing")
	txRun(exec, b, "SET", "missing", "x")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "missing")
	if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
		t.Fatalf("EXEC after creating watched key = %q", got)
	}
}

func TestWatchSameValueWriteMatchesSnapshot(t *testing.T) {
	exec := newStringExec(t)
	a, b := &ConnState{}, &ConnState{}

	txRun(exec, a, "SET", "w", "same")
	txRun(exec, a, "WATCH", "w")
	txRun(exec, b, "SET", "w", "same")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "w")
	if got := txRun(exec, a, "EXEC"); got != "*1\r\n$4\r\nsame\r\n" {
		t.Fatalf("EXEC = %q; value-snapshot should allow a same-bytes rewrite", got)
	}

	txRun(exec, a, "WATCH", "absent")
	txRun(exec, b, "SET", "absent", "x")
	txRun(exec, b, "DEL", "absent")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "absent")
	if got := txRun(exec, a, "EXEC"); got != "*1\r\n$-1\r\n" {
		t.Fatalf("EXEC = %q; value-snapshot should allow write-then-restore", got)
	}
}

func TestWatchAbortsOnCollectionMutations(t *testing.T) {
	exec := newStringExec(t)
	a, b := &ConnState{}, &ConnState{}

	for _, setup := range []struct {
		name string
		key  string
		cmds [][]string
	}{
		{"list", "l", [][]string{{"RPUSH", "l", "a"}, {"RPUSH", "l", "b"}}},
		{"hash", "h", [][]string{{"HSET", "h", "f", "v"}}},
		{"set", "s", [][]string{{"SADD", "s", "m"}}},
	} {
		for _, cmd := range setup.cmds {
			txRun(exec, a, cmd...)
		}
		txRun(exec, a, "WATCH", setup.key)
		txRun(exec, b, map[string][]string{
			"list": {"LPUSH", "l", "x"},
			"hash": {"HSET", "h", "f", "changed"},
			"set":  {"SADD", "s", "n"},
		}[setup.name]...)
		txRun(exec, a, "MULTI")
		txRun(exec, a, "GET", setup.key)
		if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
			t.Fatalf("%s: EXEC after mutation = %q", setup.name, got)
		}
	}
}

func TestWatchExpiredKeyAborts(t *testing.T) {
	fake := clock.NewFake(clock.Real{}.Now())
	exec := NewExecutorWithClock(store.NewWithClock(fake), nil, fake)
	t.Cleanup(exec.Close)
	a := &ConnState{}

	txRun(exec, a, "SET", "exp", "v", "PX", "50")
	txRun(exec, a, "WATCH", "exp")
	fake.Advance(2 * time.Second)
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "exp")
	if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
		t.Fatalf("EXEC after expiry = %q", got)
	}
}

func TestWatchAbortsOnFlushOnlyForExistingKeys(t *testing.T) {
	exec := newStringExec(t)
	a, b := &ConnState{}, &ConnState{}

	txRun(exec, a, "SET", "present", "v")
	txRun(exec, a, "WATCH", "present", "absent")
	txRun(exec, b, "FLUSHALL")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "present")
	if got := txRun(exec, a, "EXEC"); got != "*-1\r\n" {
		t.Fatalf("EXEC after FLUSHALL = %q", got)
	}

	txRun(exec, a, "WATCH", "absent")
	txRun(exec, b, "FLUSHALL")
	txRun(exec, a, "MULTI")
	txRun(exec, a, "GET", "absent")
	if got := txRun(exec, a, "EXEC"); got != "*1\r\n$-1\r\n" {
		t.Fatalf("EXEC after flushing an absent watched key = %q", got)
	}
}

func TestExecPersistsEffectsAsOneFrame(t *testing.T) {
	exec, _, _, path := newAOFExec(t, clock.Real{})
	conn := &ConnState{}

	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "SET", "a", "1")
	txRun(exec, conn, "SET", "b", "2")
	txRun(exec, conn, "EXPIRE", "a", "100")
	if got := txRun(exec, conn, "EXEC"); got != "*3\r\n+OK\r\n+OK\r\n:1\r\n" {
		t.Fatalf("EXEC = %q", got)
	}

	frames := readAOFFrames(t, path)
	if len(frames) != 1 {
		t.Fatalf("AOF frames = %d, want 1 (transaction is atomic)", len(frames))
	}
	commands := readAOFCommands(t, path)
	if len(commands) != 3 {
		t.Fatalf("AOF commands = %d, want 3: %q", len(commands), commands)
	}

	restored := store.New()
	if _, err := RecoverAOF(path, restored); err != nil {
		t.Fatalf("RecoverAOF: %v", err)
	}
	if v, ok := restored.Lookup("a"); !ok || string(v.Str) != "1" {
		t.Fatalf("restored a = %q, %v", v.Str, ok)
	}
	if got, _ := restored.TTLms("a"); got <= 0 {
		t.Fatalf("restored a has no TTL")
	}
	if v, ok := restored.Lookup("b"); !ok || string(v.Str) != "2" {
		t.Fatalf("restored b = %q, %v", v.Str, ok)
	}
}

func TestExecDirtyWritesNothing(t *testing.T) {
	exec, _, _, path := newAOFExec(t, clock.Real{})
	conn := &ConnState{}

	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "SET", "a", "1")
	txRun(exec, conn, "BOGUS")
	txRun(exec, conn, "SET", "b", "2")
	if got := txRun(exec, conn, "EXEC"); got != "-EXECABORT Transaction discarded because of previous errors.\r\n" {
		t.Fatalf("EXEC = %q", got)
	}
	if frames := readAOFFrames(t, path); len(frames) != 0 {
		t.Fatalf("dirty transaction wrote %d AOF frames", len(frames))
	}
}

// Two keys written by one EXEC are never observed half-written: a concurrent
// transaction reading both either sees the old pair or the new pair.
func TestExecArityAbortsTransaction(t *testing.T) {
	exec := newStringExec(t)
	conn := &ConnState{}

	want := "-EXECABORT Transaction discarded because of: wrong number of arguments for 'exec' command\r\n"
	if got := txRun(exec, conn, "EXEC", "extra"); got != want {
		t.Fatalf("EXEC extra = %q, want %q", got, want)
	}

	txRun(exec, conn, "MULTI")
	txRun(exec, conn, "SET", "k", "v")
	if got := txRun(exec, conn, "EXEC", "extra"); got != want {
		t.Fatalf("EXEC extra in MULTI = %q, want %q", got, want)
	}
	if got := txRun(exec, conn, "EXISTS", "k"); got != ":0\r\n" {
		t.Fatalf("EXEC with wrong arity wrote a key: %q", got)
	}
}

func TestExecAtomicVisibility(t *testing.T) {
	exec := newStringExec(t)
	reader, writer := &ConnState{}, &ConnState{}
	txRun(exec, writer, "SET", "k1", "-1")
	txRun(exec, writer, "SET", "k2", "-1")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			v := strconv.Itoa(i)
			txRun(exec, writer, "MULTI")
			txRun(exec, writer, "SET", "k1", v)
			txRun(exec, writer, "SET", "k2", v)
			txRun(exec, writer, "EXEC")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			txRun(exec, reader, "MULTI")
			txRun(exec, reader, "GET", "k1")
			txRun(exec, reader, "GET", "k2")
			if got := txRun(exec, reader, "EXEC"); !consistentPair(got) {
				t.Errorf("observed torn transaction: %q", got)
				return
			}
		}
	}()
	wg.Wait()
}

// Transactions, WATCH snapshots, and competing clients run through the shared
// executor from many goroutines; the race-detector gate covers this path.
func TestTransactionRaceSoak(t *testing.T) {
	exec := newStringExec(t)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		conn := &ConnState{}
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				key := "race:" + strconv.Itoa((w+i)%4)
				txRun(exec, conn, "WATCH", key)
				txRun(exec, conn, "MULTI")
				txRun(exec, conn, "SET", key, strconv.Itoa(i))
				txRun(exec, conn, "GET", key)
				txRun(exec, conn, "EXEC")
			}
		}(w)
	}
	wg.Wait()
}

func consistentPair(reply string) bool {
	const header = "*2\r\n"
	if !strings.HasPrefix(reply, header) {
		return false
	}
	rest := reply[len(header):]
	var values []string
	for i := 0; i < 2; i++ {
		if len(rest) == 0 || rest[0] != '$' {
			return false
		}
		end := strings.Index(rest, "\r\n")
		if end < 0 {
			return false
		}
		n, err := strconv.Atoi(rest[1:end])
		if err != nil || n < 0 || end+2+n+2 > len(rest) {
			return false
		}
		values = append(values, rest[end+2:end+2+n])
		rest = rest[end+2+n+2:]
	}
	return len(rest) == 0 && values[0] == values[1]
}
