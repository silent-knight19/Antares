package metrics

import (
	"crypto/rand"
	"fmt"
	"sync/atomic"
)

// Stats holds the process-wide counters INFO reports. Connections and network
// byte counts are updated by the server; command and memory counters by the
// executor. Atomics keep them race-free without joining the keyspace's single
// goroutine.
type Stats struct {
	RunID  string
	ReplID string

	MaxClients  atomic.Int64
	Connected   atomic.Int64
	Connections atomic.Uint64

	Commands atomic.Uint64
	NetInput atomic.Uint64
	NetOut   atomic.Uint64

	PeakMemory  atomic.Uint64
	EvictedKeys atomic.Uint64

	addr atomic.Value
}

func New(maxClients int) *Stats {
	s := &Stats{RunID: randomID(), ReplID: randomID()}
	s.MaxClients.Store(int64(maxClients))
	s.addr.Store("")
	return s
}

func (s *Stats) SetAddr(addr string) { s.addr.Store(addr) }

func (s *Stats) Addr() string {
	addr, _ := s.addr.Load().(string)
	return addr
}

func (s *Stats) ConnectionOpened() {
	s.Connections.Add(1)
	s.Connected.Add(1)
}

func (s *Stats) ConnectionClosed() { s.Connected.Add(-1) }

func (s *Stats) AddNetInput(n int) {
	if n > 0 {
		s.NetInput.Add(uint64(n))
	}
}

func (s *Stats) AddNetOutput(n int) {
	if n > 0 {
		s.NetOut.Add(uint64(n))
	}
}

func (s *Stats) ObserveMemory(n uint64) {
	for {
		peak := s.PeakMemory.Load()
		if n <= peak || s.PeakMemory.CompareAndSwap(peak, n) {
			return
		}
	}
}

func randomID() string {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%040d", 0)
	}
	return fmt.Sprintf("%x", b[:])
}
