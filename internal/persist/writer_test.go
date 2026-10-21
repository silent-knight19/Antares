package persist

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readRecords(t *testing.T, path string) []string {
	t.Helper()
	var records []string
	report, err := RecoverFile(path, false, func(payload []byte) error {
		records = append(records, string(payload))
		return nil
	})
	if err != nil {
		t.Fatalf("recover %s: %v", path, err)
	}
	if report.Truncated {
		t.Fatalf("unexpected truncation: %+v", report)
	}
	return records
}

func TestWriterAppendAndRecover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "append.aof")
	w, err := openWriter(path, FsyncAlways, nil, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	for _, payload := range []string{"A", "B", "C"} {
		if err := w.Append([]byte(payload)); err != nil {
			t.Fatalf("Append(%s): %v", payload, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := readRecords(t, path); len(got) != 3 || got[0] != "A" || got[2] != "C" {
		t.Fatalf("records = %v", got)
	}
	stats := w.Stats()
	if stats.Appends != 3 || stats.Fsyncs < 3 || !stats.LastWriteOK {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestWriterGroupCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "group.aof")
	var calls atomic.Int64
	started := make(chan struct{})
	gate := make(chan struct{})
	var once sync.Once
	syncFn := func(f *os.File) error {
		if calls.Add(1) == 1 {
			once.Do(func() { close(started) })
			<-gate
		}
		return f.Sync()
	}
	w, err := openWriter(path, FsyncAlways, syncFn, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	defer w.Close()

	first := make(chan error, 1)
	go func() { first <- w.Append([]byte("first")) }()
	<-started

	const n = 32
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- w.Append([]byte("batch")) }()
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(w.queue) < n && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("batched Append: %v", err)
		}
	}
	if err := <-first; err != nil {
		t.Fatalf("first Append: %v", err)
	}
	if got := calls.Load(); got > 3 {
		t.Fatalf("fsync calls = %d; group commit did not batch", got)
	}
}

func TestWriterEverySecFlusher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "everysec.aof")
	var calls atomic.Int64
	w, err := openWriter(path, FsyncEverySec, func(f *os.File) error {
		calls.Add(1)
		return f.Sync()
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	if err := w.Append([]byte("A")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("everysec flusher did not fsync")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWriterNoFsyncUntilClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no.aof")
	var calls atomic.Int64
	w, err := openWriter(path, FsyncNo, func(f *os.File) error {
		calls.Add(1)
		return f.Sync()
	}, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	if err := w.Append([]byte("A")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("fsync called %d times before close", calls.Load())
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("close fsync count = %d, want 1", calls.Load())
	}
}

func TestWriterCloseAndAppendAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.aof")
	w, err := openWriter(path, FsyncNo, nil, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := w.Append([]byte("late")); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append after Close = %v, want ErrClosed", err)
	}
}

func TestWriterRewriteSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rewrite.aof")
	w, err := openWriter(path, FsyncAlways, nil, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	if err := w.Append([]byte("pre")); err != nil {
		t.Fatalf("append pre: %v", err)
	}
	if err := w.BeginRewrite(); err != nil {
		t.Fatalf("BeginRewrite: %v", err)
	}
	if err := w.Append([]byte("during")); err != nil {
		t.Fatalf("append during: %v", err)
	}
	tmp := filepath.Join(dir, "snapshot.tmp")
	if err := os.WriteFile(tmp, AppendFrame(nil, []byte("snapshot")), 0o644); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	if err := w.FinishRewrite(tmp); err != nil {
		t.Fatalf("FinishRewrite: %v", err)
	}
	if err := w.Append([]byte("post")); err != nil {
		t.Fatalf("append post: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := readRecords(t, path)
	want := []string{"snapshot", "during", "post"}
	if len(got) != len(want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("records = %v, want %v", got, want)
		}
	}
	if stats := w.Stats(); stats.Rewrites != 1 {
		t.Fatalf("rewrites = %d, want 1", stats.Rewrites)
	}
}

func TestWriterRefusesAfterWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fail.aof")
	w, err := openWriter(path, FsyncNo, nil, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	defer w.Close()

	w.mu.Lock()
	_ = w.file.Close()
	w.mu.Unlock()

	if err := w.Append([]byte("A")); err == nil {
		t.Fatal("append on a broken file did not fail")
	}
	sticky := w.Failed()
	if sticky == nil {
		t.Fatal("Failed() = nil after a write error")
	}
	if err := w.Append([]byte("B")); !errors.Is(err, sticky) {
		t.Fatalf("append after failure = %v, want sticky %v", err, sticky)
	}

	var records []string
	if _, err := RecoverFile(path, false, func(payload []byte) error {
		records = append(records, string(payload))
		return nil
	}); err != nil {
		t.Fatalf("recover after failure: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("records after failed write = %q, want none", records)
	}
}

func TestWriterRefusesAfterSyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syncfail.aof")
	wantErr := errors.New("fsync refused")
	w, err := openWriter(path, FsyncAlways, func(*os.File) error { return wantErr }, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	defer w.Close()

	if err := w.Append([]byte("A")); !errors.Is(err, wantErr) {
		t.Fatalf("Append = %v, want %v", err, wantErr)
	}
	if !errors.Is(w.Failed(), wantErr) {
		t.Fatalf("Failed() = %v, want %v", w.Failed(), wantErr)
	}
	if err := w.Append([]byte("B")); !errors.Is(err, wantErr) {
		t.Fatalf("Append after sync failure = %v, want %v", err, wantErr)
	}
	if stats := w.Stats(); stats.LastWriteOK {
		t.Fatalf("LastWriteOK = true after a sync failure: %+v", stats)
	}
}

func TestWriterShouldRewrite(t *testing.T) {
	w, err := openWriter(filepath.Join(t.TempDir(), "grow.aof"), FsyncNo, nil, time.Hour)
	if err != nil {
		t.Fatalf("openWriter: %v", err)
	}
	defer w.Close()

	w.mu.Lock()
	w.stats.CurrentSize = RewriteMinSize - 1
	w.stats.BaseSize = 0
	w.mu.Unlock()
	if w.ShouldRewrite() {
		t.Fatal("rewrite triggered below minimum size")
	}

	w.mu.Lock()
	w.stats.CurrentSize = RewriteMinSize
	w.mu.Unlock()
	if !w.ShouldRewrite() {
		t.Fatal("rewrite did not trigger at minimum size with empty base")
	}

	w.mu.Lock()
	w.stats.CurrentSize = 2 * RewriteMinSize
	w.stats.BaseSize = RewriteMinSize * 3 / 2
	w.mu.Unlock()
	if w.ShouldRewrite() {
		t.Fatal("rewrite triggered below twice the base size")
	}

	w.mu.Lock()
	w.stats.CurrentSize = 3 * RewriteMinSize
	w.mu.Unlock()
	if !w.ShouldRewrite() {
		t.Fatal("rewrite did not trigger at twice the base size")
	}
}
