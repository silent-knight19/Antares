// Package events defines the opt-in event-tap types and the rotated JSONL
// sink consumed by the Observatory (ADR-014).
package events

// Version is the schema version stamped on every event, so the bridge can
// evolve the JSONL contract without guessing.
const Version = 1

// Stable event type names.
const (
	// Evict is one approximated-LRU victim choice.
	Evict = "evict"
)

// Event is one internal decision exposed on the tap. It is a union: only the
// fields meaningful for Type are set.
type Event struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Key     string `json:"key,omitempty"`
	Policy  string `json:"policy,omitempty"`
	Idle    uint32 `json:"idle_seconds,omitempty"`
	Sampled int    `json:"sampled,omitempty"`
}

// Sink receives events as they occur. The production JSONL file sink (ADR-014)
// is the Observatory implementation; a nil Sink disables the tap for the cost
// of one branch in the executor.
type Sink interface {
	Emit(Event)
}
