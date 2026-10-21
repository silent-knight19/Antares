package server

import (
	"fmt"
	"testing"

	"github.com/silent-knight19/antares/internal/clock"
)

func newBenchExecutor(b *testing.B) *Executor {
	b.Helper()
	exec := NewExecutorWithClock(nil, nil, clock.Real{})
	b.Cleanup(exec.Close)
	return exec
}

func BenchmarkAppend(b *testing.B) {
	exec := newBenchExecutor(b)
	b.SetBytes(1)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runCommand(exec, []byte("APPEND"), []byte("k"), []byte("x"))
	}
}

func BenchmarkHashDeleteSingle(b *testing.B) {
	exec := newBenchExecutor(b)
	for i := 0; i < 10_000; i++ {
		runCommand(exec, []byte("HSET"), []byte("h"), []byte(fmt.Sprintf("f%d", i)), []byte("v"))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		field := []byte(fmt.Sprintf("f%d", i%10_000))
		runCommand(exec, []byte("HDEL"), []byte("h"), field)
		runCommand(exec, []byte("HSET"), []byte("h"), field, []byte("v"))
	}
}

func BenchmarkSetRemoveSingle(b *testing.B) {
	exec := newBenchExecutor(b)
	for i := 0; i < 10_000; i++ {
		runCommand(exec, []byte("SADD"), []byte("s"), []byte(fmt.Sprintf("m%d", i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		member := []byte(fmt.Sprintf("m%d", i%10_000))
		runCommand(exec, []byte("SREM"), []byte("s"), member)
		runCommand(exec, []byte("SADD"), []byte("s"), member)
	}
}
