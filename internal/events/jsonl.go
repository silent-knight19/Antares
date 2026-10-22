package events

import (
	"encoding/json"
	"log/slog"
	"os"
	"sync"
)

// MaxLogBytes is the rotated JSONL size cap (ADR-014: 64MB × 2).
const MaxLogBytes = 64 << 20

// JSONLSink appends one JSON object per event to path. It keeps one rotated
// backup at path+".1". Emit is safe for concurrent callers.
type JSONLSink struct {
	mu     sync.Mutex
	path   string
	max    int64
	file   *os.File
	size   int64
	warned bool
}

func NewJSONLSink(path string) (*JSONLSink, error) {
	return newJSONLSink(path, MaxLogBytes)
}

func newJSONLSink(path string, max int64) (*JSONLSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &JSONLSink{path: path, max: max, file: f, size: info.Size()}, nil
}

func (s *JSONLSink) Emit(event Event) {
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return
	}
	if s.max > 0 && s.size > 0 && s.size+int64(len(line)) > s.max {
		if err := s.rotate(); err != nil {
			s.warn("event log rotation failed", err)
		}
	}
	if s.file == nil {
		return
	}
	n, err := s.file.Write(line)
	s.size += int64(n)
	if err != nil {
		s.warn("event log write failed", err)
		return
	}
	s.warned = false
}

func (s *JSONLSink) rotate() error {
	if err := s.file.Close(); err != nil {
		s.warn("event log close failed", err)
	}
	s.file = nil
	backup := s.path + ".1"
	_ = os.Remove(backup)
	if err := os.Rename(s.path, backup); err != nil {
		f, reopenErr := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if reopenErr != nil {
			s.warn("event log reopen failed", reopenErr)
		} else {
			s.file = f
		}
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	s.file = f
	s.size = 0
	return err
}

func (s *JSONLSink) warn(msg string, err error) {
	if s.warned {
		return
	}
	s.warned = true
	slog.Warn(msg, "path", s.path, "err", err)
}

func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}
