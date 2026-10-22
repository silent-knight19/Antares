package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readEvents(t *testing.T, path string) []Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, event)
	}
	return out
}

func TestJSONLSinkRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := newJSONLSink(path, 1)
	if err != nil {
		t.Fatalf("newJSONLSink: %v", err)
	}
	s.Emit(Event{Version: Version, Type: Evict, Key: "first"})
	s.Emit(Event{Version: Version, Type: Evict, Key: "second"})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := readEvents(t, path+".1"); len(got) != 1 || got[0].Key != "first" {
		t.Fatalf("backup events = %+v", got)
	}
	if got := readEvents(t, path); len(got) != 1 || got[0].Key != "second" {
		t.Fatalf("current events = %+v", got)
	}
	s.Emit(Event{Version: Version, Type: Evict, Key: "after-close"})
}
