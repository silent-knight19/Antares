package server

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/repl"
)

func TestPumpReplicaStreamDisconnectsOnOverflow(t *testing.T) {
	buf := repl.NewSyncBuffer(8)
	if err := buf.Append([]byte("0123456789")); !errors.Is(err, repl.ErrSyncBufferOverflow) {
		t.Fatalf("append = %v, want ErrSyncBufferOverflow", err)
	}

	server, peer := net.Pipe()
	defer peer.Close()
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	done := make(chan struct{})
	go func() {
		exec.pumpReplicaStream(server, buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pump did not exit after buffer overflow")
	}

	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("slow replica connection was not closed")
	}
}

func TestSlowReplicaDoesNotBlockMaster(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()
	exec.repl.SetSyncBufferLimit(1024)

	serverConn, replicaConn := net.Pipe()
	defer replicaConn.Close()
	state := &ConnState{conn: serverConn, remoteIP: "127.0.0.1", remotePort: 12345}
	defer state.Close()

	enc := protocol.GetEncoder()
	exec.Handle(protocol.Request{[]byte("PSYNC"), []byte("?"), []byte("-1")}, enc, state)
	enc.Release()
	if !state.streaming {
		t.Fatal("PSYNC did not switch connection to streaming")
	}

	done := make(chan error, 1)
	go func() {
		for i := 0; i < 200; i++ {
			if got := runStrings(exec, "SET", "key"+string(rune('a'+i%26))+strings.Repeat("x", i%7), strings.Repeat("v", 512)); got != "+OK\r\n" {
				done <- errors.New("SET failed: " + got)
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("master blocked on a replica that stopped reading")
	}

	_ = replicaConn.Close()
	state.Close()
	waitFor(t, "sync buffer cleanup", func() bool { return exec.repl.ActiveSyncBuffers() == 0 })
}

func TestSlowFullResyncDisconnectsOnWriteTimeout(t *testing.T) {
	oldTimeout := replicaWriteTimeout
	replicaWriteTimeout = 50 * time.Millisecond
	defer func() { replicaWriteTimeout = oldTimeout }()

	exec := NewExecutor(nil, nil)
	defer exec.Close()

	serverConn, replicaConn := net.Pipe()
	defer replicaConn.Close()
	state := &ConnState{conn: serverConn, remoteIP: "127.0.0.1", remotePort: 23456}
	defer state.Close()

	enc := protocol.GetEncoder()
	exec.Handle(protocol.Request{[]byte("PSYNC"), []byte("?"), []byte("-1")}, enc, state)
	enc.Release()

	waitFor(t, "stalled full resync disconnect", func() bool { return exec.repl.ActiveSyncBuffers() == 0 })

	_ = replicaConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := replicaConn.Read(make([]byte, 1)); err == nil {
		t.Fatal("stalled full-resync connection was not closed")
	}
}

func TestFullResyncConcurrencyLimit(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	if !exec.beginFullResync() {
		t.Fatal("first full resync was rejected")
	}
	if exec.beginFullResync() {
		t.Fatal("second concurrent full resync was admitted")
	}
	exec.endFullResync()
	if !exec.beginFullResync() {
		t.Fatal("slot was not released")
	}
	exec.endFullResync()
}

func TestFullResyncSlotReleasedOnStreamFailure(t *testing.T) {
	exec := NewExecutor(nil, nil)
	defer exec.Close()

	serverConn, clientConn := net.Pipe()
	_ = clientConn.Close()
	conn := &ConnState{conn: serverConn, flush: func() error { return nil }}
	enc := protocol.GetEncoder()
	defer enc.Release()

	exec.psyncCommand([][]byte{[]byte("?"), []byte("-1")}, enc, conn)
	waitFor(t, "full-resync slot release", func() bool {
		return exec.fullResyncs.Load() == 0
	})
	if !exec.beginFullResync() {
		t.Fatal("slot was not reusable after stream failure")
	}
	exec.endFullResync()
}
