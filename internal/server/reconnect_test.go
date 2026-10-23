package server

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReplicaReconnectUsesPartialResync is the S2.2 exit criterion: cut the
// link, keep writing on the master, and prove the replica reconnects with
// +CONTINUE (not +FULLRESYNC) and catches up without a data gap.
func TestReplicaReconnectUsesPartialResync(t *testing.T) {
	masterExec := NewExecutor(nil, nil)
	defer masterExec.Close()

	master := New(Config{Addr: "127.0.0.1:0"}, masterExec.Handle)
	if err := master.Start(); err != nil {
		t.Fatalf("start master: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = master.Shutdown(ctx)
	}()

	proxy := newCutProxy(t, master.Addr().String())
	defer proxy.Close()

	replicaExec := NewExecutor(nil, nil)
	defer replicaExec.Close()

	if got := runStrings(masterExec, "SET", "before", "sync"); got != "+OK\r\n" {
		t.Fatalf("master SET before = %q", got)
	}

	host, port, err := net.SplitHostPort(proxy.Addr().String())
	if err != nil {
		t.Fatalf("split proxy addr: %v", err)
	}
	if got := runStrings(replicaExec, "REPLICAOF", host, port); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF = %q", got)
	}

	waitFor(t, "initial full resync", func() bool {
		return runStrings(replicaExec, "GET", "before") == "$4\r\nsync\r\n" &&
			strings.Contains(runStrings(replicaExec, "INFO", "replication"), "master_link_status:up")
	})

	proxy.Cut()

	// Mutations made while the replica is disconnected must come from backlog.
	for _, key := range []string{"gap1", "gap2"} {
		if got := runStrings(masterExec, "SET", key, "during"); got != "+OK\r\n" {
			t.Fatalf("master SET %s = %q", key, got)
		}
	}

	waitFor(t, "replica catches up after reconnect", func() bool {
		return runStrings(replicaExec, "GET", "gap1") == "$6\r\nduring\r\n" &&
			runStrings(replicaExec, "GET", "gap2") == "$6\r\nduring\r\n" &&
			runStrings(replicaExec, "GET", "before") == "$4\r\nsync\r\n"
	})

	if got := masterExec.Stats().SyncFull.Load(); got != 1 {
		t.Fatalf("sync_full = %d, want 1 (reconnect must not full-resync)", got)
	}
	if got := masterExec.Stats().SyncPartialOK.Load(); got < 1 {
		t.Fatalf("sync_partial_ok = %d, want >= 1", got)
	}

	waitFor(t, "offset continuity", func() bool {
		return replicaExec.Repl().SlaveReplOffset() == masterExec.Repl().MasterReplOffset()
	})
}

// cutProxy forwards TCP and can sever every live connection without stopping
// its listener, so the replica can reconnect.
type cutProxy struct {
	ln     net.Listener
	target string

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

func newCutProxy(t *testing.T, target string) *cutProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("proxy listen: %v", err)
	}
	p := &cutProxy{ln: ln, target: target, conns: make(map[net.Conn]struct{})}
	go p.acceptLoop()
	return p
}

func (p *cutProxy) Addr() net.Addr { return p.ln.Addr() }

func (p *cutProxy) acceptLoop() {
	for {
		client, err := p.ln.Accept()
		if err != nil {
			return
		}
		go p.handle(client)
	}
}

func (p *cutProxy) handle(client net.Conn) {
	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	p.track(client, upstream)
	defer func() {
		p.untrack(client, upstream)
		_ = client.Close()
		_ = upstream.Close()
	}()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}

func (p *cutProxy) track(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		p.conns[c] = struct{}{}
	}
}

func (p *cutProxy) untrack(conns ...net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range conns {
		delete(p.conns, c)
	}
}

func (p *cutProxy) Cut() {
	p.mu.Lock()
	conns := make([]net.Conn, 0, len(p.conns))
	for c := range p.conns {
		conns = append(conns, c)
	}
	p.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (p *cutProxy) Close() {
	_ = p.ln.Close()
	p.Cut()
}

func TestMasterPingsIdleReplica(t *testing.T) {
	masterExec := newExecutorWithPing(nil, nil, true, nil, nil, 20*time.Millisecond)
	defer masterExec.Close()
	master := startServerHandler(t, Config{Addr: "127.0.0.1:0"}, masterExec.Handle)

	replicaExec := NewExecutor(nil, nil)
	defer replicaExec.Close()

	host, port, err := net.SplitHostPort(master.Addr().String())
	if err != nil {
		t.Fatalf("split master addr: %v", err)
	}
	if got := runStrings(replicaExec, "REPLICAOF", host, port); got != "+OK\r\n" {
		t.Fatalf("REPLICAOF = %q", got)
	}
	waitFor(t, "replica link up", func() bool { return replicaLinkUp(replicaExec) })

	start := masterExec.Repl().MasterReplOffset()
	waitFor(t, "master keepalive ping", func() bool {
		return masterExec.Repl().MasterReplOffset() > start
	})
	waitFor(t, "replica applies keepalive", func() bool {
		return replicaExec.Repl().SlaveReplOffset() == masterExec.Repl().MasterReplOffset()
	})
}

func TestNextReplicaReconnectDelay(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want time.Duration
	}{
		{0, replicaReconnectMinDelay},
		{replicaReconnectMinDelay, 2 * replicaReconnectMinDelay},
		{time.Second, replicaReconnectMaxDelay},
		{replicaReconnectMaxDelay, replicaReconnectMaxDelay},
		{3 * time.Second, replicaReconnectMaxDelay},
	}
	for _, tc := range cases {
		if got := nextReplicaReconnectDelay(tc.in); got != tc.want {
			t.Fatalf("nextReplicaReconnectDelay(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
