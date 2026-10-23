package metrics

import (
	"sync"
	"testing"
)

func TestStatsNewAndAddr(t *testing.T) {
	s := New(100)
	if s == nil {
		t.Fatal("New returned nil")
	}
	if len(s.RunID) != 40 {
		t.Fatalf("RunID length = %d, want 40", len(s.RunID))
	}
	if len(s.ReplID) != 40 {
		t.Fatalf("ReplID length = %d, want 40", len(s.ReplID))
	}
	if s.RunID == s.ReplID {
		t.Fatal("RunID and ReplID should be distinct")
	}
	if got := s.MaxClients.Load(); got != 100 {
		t.Fatalf("MaxClients = %d, want 100", got)
	}
	if got := s.Addr(); got != "" {
		t.Fatalf("initial Addr = %q, want empty", got)
	}
	s.SetAddr("127.0.0.1:6379")
	if got := s.Addr(); got != "127.0.0.1:6379" {
		t.Fatalf("Addr = %q, want 127.0.0.1:6379", got)
	}
}

func TestStatsConnectionLifecycle(t *testing.T) {
	s := New(10)
	s.ConnectionOpened()
	s.ConnectionOpened()
	if s.Connections.Load() != 2 {
		t.Fatalf("total connections = %d, want 2", s.Connections.Load())
	}
	if s.Connected.Load() != 2 {
		t.Fatalf("connected clients = %d, want 2", s.Connected.Load())
	}

	s.ConnectionClosed()
	if s.Connections.Load() != 2 {
		t.Fatalf("total connections after close = %d, want 2", s.Connections.Load())
	}
	if s.Connected.Load() != 1 {
		t.Fatalf("connected clients after close = %d, want 1", s.Connected.Load())
	}
}

func TestStatsNetworkCounters(t *testing.T) {
	s := New(10)
	s.AddNetInput(100)
	s.AddNetInput(-50) // negative should be ignored
	s.AddNetInput(50)
	if s.NetInput.Load() != 150 {
		t.Fatalf("NetInput = %d, want 150", s.NetInput.Load())
	}

	s.AddNetOutput(200)
	s.AddNetOutput(0) // 0 should be ignored
	s.AddNetOutput(-10)
	s.AddNetOutput(100)
	if s.NetOut.Load() != 300 {
		t.Fatalf("NetOut = %d, want 300", s.NetOut.Load())
	}
}

func TestStatsObserveMemoryConcurrent(t *testing.T) {
	s := New(10)
	s.ObserveMemory(500)
	if s.PeakMemory.Load() != 500 {
		t.Fatalf("PeakMemory = %d, want 500", s.PeakMemory.Load())
	}

	// Lower value should not decrease peak
	s.ObserveMemory(200)
	if s.PeakMemory.Load() != 500 {
		t.Fatalf("PeakMemory after lower = %d, want 500", s.PeakMemory.Load())
	}

	// Concurrent observe
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		val := uint64(1000 + i*10)
		wg.Add(1)
		go func(v uint64) {
			defer wg.Done()
			s.ObserveMemory(v)
		}(val)
	}
	wg.Wait()

	wantPeak := uint64(1000 + 49*10)
	if s.PeakMemory.Load() != wantPeak {
		t.Fatalf("concurrent PeakMemory = %d, want %d", s.PeakMemory.Load(), wantPeak)
	}
}
