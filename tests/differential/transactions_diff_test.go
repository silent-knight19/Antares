package differential

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/client"
)

type connPair struct {
	ref *client.Client
	ant *client.Client
}

func rawClient(t *testing.T, addr string) *client.Client {
	t.Helper()
	c, err := client.Dial(context.Background(), addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func cleanupTxnKeys(c *client.Client, prefix string) {
	ctx := context.Background()
	res, err := c.Execute(ctx, []byte("KEYS"), []byte(prefix+"*"))
	if err != nil || res.Reply.Kind != client.KindArray || len(res.Reply.Array) == 0 {
		return
	}
	del := [][]byte{[]byte("DEL")}
	for _, key := range res.Reply.Array {
		del = append(del, []byte(key.Text))
	}
	_, _ = c.Execute(ctx, del...)
}

func (p connPair) step(t *testing.T, args ...string) {
	t.Helper()
	bb := make([][]byte, len(args))
	for i, arg := range args {
		bb[i] = []byte(arg)
	}
	want, err := p.ref.Execute(context.Background(), bb...)
	if err != nil {
		t.Fatalf("reference %v: %v", args, err)
	}
	got, err := p.ant.Execute(context.Background(), bb...)
	if err != nil {
		t.Fatalf("antares %v: %v", args, err)
	}
	if !reflect.DeepEqual(want.Reply, got.Reply) {
		t.Fatalf("%v\nreference: %#v\nantares:   %#v", args, want.Reply, got.Reply)
	}
}

func TestDiffTransactions(t *testing.T) {
	refAddr := os.Getenv("REDIS_REF_ADDR")
	if refAddr == "" {
		t.Skip("REDIS_REF_ADDR not set; run make diff-test")
	}
	antAddr := startAntares(t)
	refB := rawClient(t, refAddr)
	antB := rawClient(t, antAddr)
	if _, err := refB.Execute(context.Background(), []byte("PING")); err != nil {
		t.Fatalf("reference Redis at %s is unreachable: %v", refAddr, err)
	}

	prefix := fmt.Sprintf("antares:txn:%d:", time.Now().UnixNano())
	k := func(name string) string { return prefix + name }
	a := connPair{ref: rawClient(t, refAddr), ant: rawClient(t, antAddr)}
	b := connPair{ref: refB, ant: antB}
	t.Cleanup(func() {
		for _, c := range []*client.Client{a.ref, a.ant} {
			cleanupTxnKeys(c, prefix)
		}
	})

	a.step(t, "EXEC")
	a.step(t, "DISCARD")
	a.step(t, "MULTI")
	a.step(t, "MULTI")
	a.step(t, "SET", k("basic"), "v")
	a.step(t, "GET", k("basic"))
	a.step(t, "EXEC")
	a.step(t, "GET", k("basic"))
	a.step(t, "MULTI")
	a.step(t, "EXEC")

	a.step(t, "MULTI")
	a.step(t, "SET", k("str"), "str")
	a.step(t, "INCR", k("str"))
	a.step(t, "GET", k("str"))
	a.step(t, "EXEC")

	a.step(t, "MULTI")
	a.step(t, "NOSUCHCMD", "arg")
	a.step(t, "SET", k("aborted"), "1")
	a.step(t, "EXEC")
	a.step(t, "EXISTS", k("aborted"))

	a.step(t, "MULTI")
	a.step(t, "SET", k("arity"))
	a.step(t, "EXEC")

	a.step(t, "MULTI")
	a.step(t, "SET", k("discarded"), "1")
	a.step(t, "DISCARD")
	a.step(t, "EXISTS", k("discarded"))

	a.step(t, "EXEC", "extra")
	a.step(t, "MULTI")
	a.step(t, "EXEC", "extra")

	a.step(t, "MULTI")
	a.step(t, "WATCH", k("inside"))
	a.step(t, "SET", k("inside"), "v")
	a.step(t, "EXEC")
	a.step(t, "GET", k("inside"))

	a.step(t, "MULTI")
	a.step(t, "UNWATCH")
	a.step(t, "EXEC")

	a.step(t, "WATCH", k("watched"))
	b.step(t, "SET", k("watched"), "one")
	a.step(t, "MULTI")
	a.step(t, "GET", k("watched"))
	a.step(t, "EXEC")
	a.step(t, "GET", k("watched"))

	a.step(t, "WATCH", k("watched"))
	a.step(t, "UNWATCH")
	b.step(t, "SET", k("watched"), "two")
	a.step(t, "MULTI")
	a.step(t, "GET", k("watched"))
	a.step(t, "EXEC")

	a.step(t, "WATCH", k("watched"))
	b.step(t, "DEL", k("watched"))
	a.step(t, "MULTI")
	a.step(t, "EXISTS", k("watched"))
	a.step(t, "EXEC")

	a.step(t, "WATCH", k("ttl"))
	b.step(t, "SET", k("ttl"), "v")
	b.step(t, "PEXPIRE", k("ttl"), "100000")
	a.step(t, "MULTI")
	a.step(t, "GET", k("ttl"))
	a.step(t, "EXEC")

	a.step(t, "WATCH", k("absent"))
	b.step(t, "SET", k("absent"), "v")
	a.step(t, "MULTI")
	a.step(t, "GET", k("absent"))
	a.step(t, "EXEC")

	a.step(t, "WATCH", k("list"))
	b.step(t, "RPUSH", k("list"), "a")
	a.step(t, "MULTI")
	a.step(t, "LLEN", k("list"))
	a.step(t, "EXEC")
}
