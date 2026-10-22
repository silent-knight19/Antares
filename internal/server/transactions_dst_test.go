package server

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"
)

// TestTransactionDSTShadowModel drives two transaction clients through 10k
// random interleavings while an in-process map/snapshot model produces the
// expected reply for every step (P8.3). Divergences are only legal where
// ADR-008 documents them, and both sides compare values so they agree.
func TestTransactionDSTShadowModel(t *testing.T) {
	const steps = 10000
	exec := newStringExec(t)
	model := &dstModel{keys: make(map[string]string)}
	modelConns := []*dstConn{{}, {}}
	conns := []*ConnState{{}, {}}
	rng := rand.New(rand.NewSource(7))

	for i := 0; i < steps; i++ {
		n := rng.Intn(len(conns))
		args, want := nextDSTOp(rng, model, modelConns[n])
		if got := txRun(exec, conns[n], args...); got != want {
			t.Fatalf("step %d client %d %v = %q, want %q (multi=%v dirty=%v watched=%d)",
				i, n, args, got, want, modelConns[n].multi, modelConns[n].dirty, len(modelConns[n].watch))
		}
	}
}

type dstModel struct {
	keys map[string]string
}

type dstConn struct {
	multi bool
	dirty bool
	queue [][]string
	watch map[string]dsWatch
}

type dsWatch struct {
	exists bool
	value  string
}

func nextDSTOp(rng *rand.Rand, m *dstModel, c *dstConn) ([]string, string) {
	key := "k" + strconv.Itoa(rng.Intn(8))
	roll := rng.Intn(100)

	if c.multi {
		switch {
		case roll < 42:
			return dstExec(m, c)
		case roll < 52:
			return dstDiscard(c)
		case roll < 57:
			return []string{"MULTI"}, "-ERR MULTI calls can not be nested\r\n"
		case roll < 62:
			return []string{"WATCH", key}, "-ERR WATCH inside MULTI is not allowed\r\n"
		case roll < 72:
			c.queue = append(c.queue, []string{"UNWATCH"})
			return []string{"UNWATCH"}, "+QUEUED\r\n"
		default:
			return dstQueue(rng, m, c, key)
		}
	}

	switch {
	case roll < 10:
		c.multi = true
		return []string{"MULTI"}, "+OK\r\n"
	case roll < 20:
		if c.watch == nil {
			c.watch = make(map[string]dsWatch)
		}
		if _, ok := c.watch[key]; !ok {
			value, exists := m.keys[key]
			c.watch[key] = dsWatch{exists: exists, value: value}
		}
		return []string{"WATCH", key}, "+OK\r\n"
	case roll < 26:
		c.watch = nil
		return []string{"UNWATCH"}, "+OK\r\n"
	case roll < 31:
		return []string{"DISCARD"}, "-ERR DISCARD without MULTI\r\n"
	default:
		return dstQueue(rng, m, c, key)
	}
}

func dstQueue(rng *rand.Rand, m *dstModel, c *dstConn, key string) ([]string, string) {
	var args []string
	switch rng.Intn(10) {
	case 0, 1, 2, 3, 4, 5:
		args = []string{"SET", key, "v" + strconv.Itoa(rng.Intn(5))}
	case 6, 7:
		args = []string{"GET", key}
	case 8:
		args = []string{"DEL", key}
	default:
		args = []string{"GET"}
	}
	invalid := len(args) == 1 && args[0] == "GET"
	if !c.multi {
		if invalid {
			return args, "-ERR wrong number of arguments for 'get' command\r\n"
		}
		return args, dstApply(m, c, args)
	}
	if invalid {
		c.dirty = true
		return args, "-ERR wrong number of arguments for 'get' command\r\n"
	}
	c.queue = append(c.queue, args)
	return args, "+QUEUED\r\n"
}

func dstApply(m *dstModel, c *dstConn, args []string) string {
	switch args[0] {
	case "SET":
		m.keys[args[1]] = args[2]
		return "+OK\r\n"
	case "GET":
		value, ok := m.keys[args[1]]
		if !ok {
			return "$-1\r\n"
		}
		return fmt.Sprintf("$%d\r\n%s\r\n", len(value), value)
	case "DEL":
		if _, ok := m.keys[args[1]]; ok {
			delete(m.keys, args[1])
			return ":1\r\n"
		}
		return ":0\r\n"
	case "UNWATCH":
		c.watch = nil
		return "+OK\r\n"
	}
	panic("dst: unmodelled command " + args[0])
}

func dstExec(m *dstModel, c *dstConn) ([]string, string) {
	if c.dirty {
		c.multi, c.dirty, c.queue, c.watch = false, false, nil, nil
		return []string{"EXEC"}, "-EXECABORT Transaction discarded because of previous errors.\r\n"
	}
	aborted := false
	for key, snap := range c.watch {
		value, exists := m.keys[key]
		if exists != snap.exists || value != snap.value {
			aborted = true
			break
		}
	}
	queue := c.queue
	c.multi, c.dirty, c.queue, c.watch = false, false, nil, nil
	if aborted {
		return []string{"EXEC"}, "*-1\r\n"
	}
	reply := "*" + strconv.Itoa(len(queue)) + "\r\n"
	for _, args := range queue {
		reply += dstApply(m, c, args)
	}
	return []string{"EXEC"}, reply
}

func dstDiscard(c *dstConn) ([]string, string) {
	c.multi, c.dirty, c.queue, c.watch = false, false, nil, nil
	return []string{"DISCARD"}, "+OK\r\n"
}
