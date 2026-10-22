package store

import "bytes"

// Snapshot is a decoupled point-in-time copy of one key used by WATCH
// value-compare (ADR-008). Access recency is ignored: reads do not invalidate a
// watch, only writes do.
type Snapshot struct {
	value     Value
	expiresAt int64
	exists    bool
}

// CaptureSnapshot copies key's current value and absolute expiry. An expired
// key is captured as absent (lazy expiry runs here, as on any access).
func (s *Store) CaptureSnapshot(key string) Snapshot {
	v, ok := s.live(key)
	if !ok {
		return Snapshot{}
	}
	return Snapshot{value: cloneValue(v), expiresAt: s.expires[key], exists: true}
}

// MatchesSnapshot reports whether key is still observably what snap captured.
// A changed value, a changed expiry, or an existence change means a writer
// touched the key. Serialized bytes compare order-sensitively, so the only
// accepted divergence is a write that restores identical bytes and TTL.
func (s *Store) MatchesSnapshot(key string, snap Snapshot) bool {
	v, ok := s.live(key)
	if ok != snap.exists {
		return false
	}
	if !ok {
		return true
	}
	if s.expires[key] != snap.expiresAt {
		return false
	}
	return equalValues(v, snap.value)
}

func equalValues(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case KindString:
		return bytes.Equal(a.Str, b.Str)
	case KindList:
		al, bl := a.List[a.ListHead:], b.List[b.ListHead:]
		if len(al) != len(bl) {
			return false
		}
		for i := range al {
			if !bytes.Equal(al[i], bl[i]) {
				return false
			}
		}
		return true
	case KindHash:
		if len(a.Hash) != len(b.Hash) {
			return false
		}
		for i := range a.Hash {
			if !bytes.Equal(a.Hash[i].Field, b.Hash[i].Field) ||
				!bytes.Equal(a.Hash[i].Value, b.Hash[i].Value) {
				return false
			}
		}
		return true
	case KindSet:
		if len(a.Set) != len(b.Set) {
			return false
		}
		for i := range a.Set {
			if !bytes.Equal(a.Set[i], b.Set[i]) {
				return false
			}
		}
		return true
	}
	return false
}
