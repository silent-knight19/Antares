package persist

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FsyncPolicy controls when appended frames are flushed to stable storage.
type FsyncPolicy string

const (
	FsyncAlways   FsyncPolicy = "always"
	FsyncEverySec FsyncPolicy = "everysec"
	FsyncNo       FsyncPolicy = "no"
)

// RewriteMinSize is the smallest AOF that triggers an automatic rewrite.
const RewriteMinSize = 64 << 20

// RewriteBufferMax caps the write tail buffered in memory during a rewrite. A
// larger tail aborts the rewrite instead of growing without bound.
const RewriteBufferMax = 64 << 20

var (
	ErrClosed            = errors.New("persist: writer closed")
	ErrRewriteInProgress = errors.New("persist: rewrite already in progress")
	ErrNoRewrite         = errors.New("persist: no rewrite in progress")
)

// ParseFsyncPolicy validates an appendfsync value.
func ParseFsyncPolicy(s string) (FsyncPolicy, error) {
	switch FsyncPolicy(s) {
	case FsyncAlways, FsyncEverySec, FsyncNo:
		return FsyncPolicy(s), nil
	default:
		return "", errors.New("persist: appendfsync must be always, everysec, or no")
	}
}

// Stats is a point-in-time snapshot of the writer's counters.
type Stats struct {
	CurrentSize int64
	BaseSize    int64
	Appends     uint64
	Fsyncs      uint64
	Rewrites    uint64
	LastWriteOK bool
}

type Writer struct {
	path          string
	policy        FsyncPolicy
	queue         chan writerOp
	done          chan struct{}
	closeOnce     sync.Once
	closeErr      error
	syncFn        func(*os.File) error
	fsyncInterval time.Duration

	mu           sync.Mutex
	file         *os.File
	stats        Stats
	rewriting    bool
	rewriteBuf   []byte
	pendingFsync bool
	closed       bool
	writeErr     error
}

type writerOp struct {
	frame   []byte
	ack     chan error
	control *writerControl
}

type controlKind uint8

const (
	controlBeginRewrite controlKind = iota
	controlFinishRewrite
	controlAbortRewrite
	controlClose
)

type writerControl struct {
	kind     controlKind
	tempPath string
	ack      chan error
}

// Open opens or creates path and starts the writer goroutine.
func Open(path string, policy FsyncPolicy) (*Writer, error) {
	if _, err := ParseFsyncPolicy(string(policy)); err != nil {
		return nil, err
	}
	return openWriter(path, policy, nil, time.Second)
}

func openWriter(path string, policy FsyncPolicy, syncFn func(*os.File) error, interval time.Duration) (*Writer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if syncFn == nil {
		syncFn = func(f *os.File) error { return f.Sync() }
	}
	if interval <= 0 {
		interval = time.Second
	}
	w := &Writer{
		path:          path,
		policy:        policy,
		queue:         make(chan writerOp, 1024),
		done:          make(chan struct{}),
		syncFn:        syncFn,
		fsyncInterval: interval,
		file:          f,
		stats: Stats{
			CurrentSize: info.Size(),
			BaseSize:    info.Size(),
			LastWriteOK: true,
		},
	}
	go w.loop()
	return w, nil
}

// Path returns the AOF path passed to Open.
func (w *Writer) Path() string { return w.path }

// Policy returns the configured fsync policy.
func (w *Writer) Policy() FsyncPolicy { return w.policy }

// Failed returns the sticky append or fsync failure, if any, or ErrClosed when
// the writer is closed. Once set, later appends are refused so a torn frame
// cannot be followed by new data.
func (w *Writer) Failed() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return w.writeErr
	}
	if w.closed {
		return ErrClosed
	}
	return nil
}

// RemoveStaleRewriteTemps deletes rewrite temp files left next to path by a
// crash during a previous rewrite.
func RemoveStaleRewriteTemps(path string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".antares-aof-*.tmp"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// Append frames and writes one RESP payload, returning after the write (and
// fsync for "always") completes. A returned error means the write may not be
// durable.
func (w *Writer) Append(payload []byte) error {
	return w.enqueue(writerOp{frame: AppendFrame(nil, payload), ack: make(chan error, 1)})
}

func (w *Writer) enqueue(op writerOp) error {
	select {
	case w.queue <- op:
	case <-w.done:
		return ErrClosed
	}
	select {
	case err := <-op.ack:
		return err
	case <-w.done:
		select {
		case err := <-op.ack:
			return err
		default:
			return ErrClosed
		}
	}
}

func (w *Writer) control(kind controlKind, tempPath string) error {
	ack := make(chan error, 1)
	return w.enqueue(writerOp{
		ack:     ack,
		control: &writerControl{kind: kind, tempPath: tempPath, ack: ack},
	})
}

// BeginRewrite starts buffering every appended frame. The caller must take
// the dataset snapshot after this returns, so no write can fall between the
// snapshot and the buffer.
func (w *Writer) BeginRewrite() error { return w.control(controlBeginRewrite, "") }

// FinishRewrite writes the buffered frames to tempPath, fsyncs it, renames it
// over the AOF, and resumes appends on the new file.
func (w *Writer) FinishRewrite(tempPath string) error {
	return w.control(controlFinishRewrite, tempPath)
}

// AbortRewrite discards the buffered frames and removes tempPath if set.
func (w *Writer) AbortRewrite(tempPath string) error {
	return w.control(controlAbortRewrite, tempPath)
}

// Close flushes, fsyncs, and closes the active file. Later appends fail with
// ErrClosed.
func (w *Writer) Close() error {
	w.closeOnce.Do(func() { w.closeErr = w.control(controlClose, "") })
	return w.closeErr
}

// Stats returns a consistent snapshot for INFO and tests.
func (w *Writer) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stats
}

// ShouldRewrite reports whether the log has grown enough to compact.
func (w *Writer) ShouldRewrite() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rewriting || w.stats.CurrentSize < RewriteMinSize {
		return false
	}
	return w.stats.BaseSize == 0 || w.stats.CurrentSize >= 2*w.stats.BaseSize
}

func (w *Writer) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.fsyncInterval)
	defer ticker.Stop()
	var batch []writerOp
	for {
		select {
		case op := <-w.queue:
			batch = append(batch, op)
		case <-ticker.C:
			w.syncPending()
			continue
		}
	drain:
		for len(batch) < 1024 {
			select {
			case op := <-w.queue:
				batch = append(batch, op)
			default:
				break drain
			}
		}
		if w.process(batch) {
			return
		}
		batch = batch[:0]
	}
}

func (w *Writer) process(batch []writerOp) bool {
	for i := 0; i < len(batch); {
		if c := batch[i].control; c != nil {
			err := w.handleControl(c)
			c.ack <- err
			i++
			if c.kind == controlClose {
				return true
			}
			continue
		}
		j := i
		var data []byte
		for j < len(batch) && batch[j].control == nil {
			data = append(data, batch[j].frame...)
			j++
		}
		err := w.writeBatch(data, j-i)
		for k := i; k < j; k++ {
			batch[k].ack <- err
		}
		i = j
	}
	return false
}

func (w *Writer) writeBatch(data []byte, records int) error {
	w.mu.Lock()
	failed := w.writeErr
	w.mu.Unlock()
	if failed != nil {
		return failed
	}
	if err := writeFull(w.file, data); err != nil {
		w.failWrite(err)
		return err
	}
	w.mu.Lock()
	w.stats.CurrentSize += int64(len(data))
	w.stats.Appends += uint64(records)
	if w.rewriting {
		if len(w.rewriteBuf)+len(data) > RewriteBufferMax {
			w.rewriting = false
			w.rewriteBuf = nil
		} else {
			w.rewriteBuf = append(w.rewriteBuf, data...)
		}
	}
	w.mu.Unlock()

	if w.policy == FsyncAlways {
		return w.syncFile()
	}
	if w.policy == FsyncEverySec {
		w.mu.Lock()
		w.pendingFsync = true
		w.mu.Unlock()
	}
	return nil
}

// failWrite records a sticky failure and clamps the file back to the last
// complete frame, so a partial write can never sit in the middle of the log.
func (w *Writer) failWrite(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr == nil {
		w.writeErr = err
		w.stats.LastWriteOK = false
	}
	_ = w.file.Truncate(w.stats.CurrentSize)
}

func (w *Writer) handleControl(c *writerControl) error {
	switch c.kind {
	case controlBeginRewrite:
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.rewriting {
			return ErrRewriteInProgress
		}
		w.rewriting = true
		w.rewriteBuf = nil
		return nil
	case controlFinishRewrite:
		return w.finishRewrite(c.tempPath)
	case controlAbortRewrite:
		w.mu.Lock()
		w.rewriting = false
		w.rewriteBuf = nil
		w.mu.Unlock()
		if c.tempPath != "" {
			_ = os.Remove(c.tempPath)
		}
		return nil
	case controlClose:
		syncErr := w.syncFile()
		closeErr := w.file.Close()
		w.mu.Lock()
		w.closed = true
		w.mu.Unlock()
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	default:
		return errors.New("persist: unknown control operation")
	}
}

func (w *Writer) finishRewrite(tempPath string) error {
	if tempPath == "" {
		return errors.New("persist: rewrite temp path is empty")
	}
	w.mu.Lock()
	if !w.rewriting {
		w.mu.Unlock()
		return ErrNoRewrite
	}
	failed := w.writeErr
	if failed != nil {
		w.rewriting = false
		w.rewriteBuf = nil
		w.mu.Unlock()
		return failed
	}
	buffered := append([]byte(nil), w.rewriteBuf...)
	w.mu.Unlock()

	tmp, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := writeFull(tmp, buffered); err != nil {
		tmp.Close()
		return err
	}
	if err := w.syncFn(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	if err := w.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, w.path); err != nil {
		w.reopenAfterFailedRewrite()
		return err
	}
	syncDir(w.path)
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.markFailed(err)
		w.mu.Lock()
		w.rewriting = false
		w.rewriteBuf = nil
		w.mu.Unlock()
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		w.markFailed(err)
		w.mu.Lock()
		w.rewriting = false
		w.rewriteBuf = nil
		w.mu.Unlock()
		return err
	}
	w.mu.Lock()
	w.file = f
	w.rewriting = false
	w.rewriteBuf = nil
	w.pendingFsync = false
	w.writeErr = nil
	w.stats.CurrentSize = info.Size()
	w.stats.BaseSize = info.Size()
	w.stats.Rewrites++
	w.stats.LastWriteOK = true
	w.mu.Unlock()
	return nil
}

// syncDir fsyncs the directory holding path so a rename survives power loss.
func syncDir(path string) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return
	}
	_ = dir.Sync()
	_ = dir.Close()
}

func (w *Writer) reopenAfterFailedRewrite() {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		w.markFailed(err)
		return
	}
	w.mu.Lock()
	w.file = f
	w.rewriting = false
	w.rewriteBuf = nil
	w.mu.Unlock()
}

func (w *Writer) syncPending() {
	w.mu.Lock()
	pending := w.pendingFsync
	w.mu.Unlock()
	if !pending {
		return
	}
	if err := w.syncFile(); err != nil {
		return
	}
	w.mu.Lock()
	w.pendingFsync = false
	w.mu.Unlock()
}

func (w *Writer) syncFile() error {
	if err := w.syncFn(w.file); err != nil {
		w.markFailed(err)
		return err
	}
	w.mu.Lock()
	w.stats.Fsyncs++
	if w.writeErr == nil {
		w.stats.LastWriteOK = true
	}
	w.mu.Unlock()
	return nil
}

// markFailed records a sticky failure without touching the file.
func (w *Writer) markFailed(err error) {
	w.mu.Lock()
	if w.writeErr == nil {
		w.writeErr = err
		w.stats.LastWriteOK = false
	}
	w.mu.Unlock()
}

func writeFull(f *os.File, b []byte) error {
	for len(b) > 0 {
		n, err := f.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
