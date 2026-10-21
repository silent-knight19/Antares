package store

// The keyspace lives in Go maps and slices, so there is no allocator counter to
// read. This file models the retained heap from the shapes we allocate: a
// per-entry map cost plus the exact payload lengths, which INFO reports and
// maxmemory will enforce. The model is calibrated within 10% of the runtime
// heap for payload-sized datasets (TestMemoryUsageTracksRuntime); it is a
// logical estimate, not the allocator's byte count.
const (
	// entryOverhead is the amortized cost of one data-map entry at Go's load
	// factor, including the inline Value struct (144 bytes after the 24-bit Lru
	// field) and the key's string header.
	entryOverhead = 248

	// indexOverhead is the amortized cost of one field/member index entry in
	// map[string]int, excluding the key bytes (counted separately).
	indexOverhead = 48

	sliceHeaderBytes = 24
	hashEntryBytes   = 48
)

// Usage splits accounted keyspace bytes into the total (the number maxmemory
// compares against) and the dataset share (payload only; the rest is overhead,
// Total-Dataset).
type Usage struct {
	Total   int64
	Dataset int64
}

// MemoryUsage estimates the heap bytes held by every key, including expired
// keys not yet collected by lazy access or the active cycle.
//
// shortcut: a full scan per call; maxmemory enforcement (P7.2) calls this per
// denyoom command, so the scan only runs while maxmemory is set (default 0
// skips it). Replace with an incrementally maintained total when eviction
// sampling lands (P7.3).
func (s *Store) MemoryUsage() Usage {
	var u Usage
	for key, value := range s.data {
		u.Total += entryOverhead + int64(len(key))
		u.add(value)
	}
	u.Total += u.Dataset
	return u
}

func (u *Usage) add(v Value) {
	switch v.Kind {
	case KindString:
		u.Dataset += int64(len(v.Str))
	case KindList:
		u.Total += int64(cap(v.List)) * sliceHeaderBytes
		for _, element := range v.List[v.ListHead:] {
			u.Dataset += int64(len(element))
		}
	case KindHash:
		u.Total += int64(cap(v.Hash)) * hashEntryBytes
		for _, entry := range v.Hash {
			u.Dataset += int64(len(entry.Field) + len(entry.Value))
		}
		u.addIndex(v.HashIndex)
	case KindSet:
		u.Total += int64(cap(v.Set)) * sliceHeaderBytes
		for _, member := range v.Set {
			u.Dataset += int64(len(member))
		}
		u.addIndex(v.SetIndex)
	}
}

// addIndex counts an index map's bucket cost plus the field/member bytes it
// duplicates as map keys.
func (u *Usage) addIndex(index map[string]int) {
	if len(index) == 0 {
		return
	}
	u.Total += int64(len(index)) * indexOverhead
	for key := range index {
		u.Total += int64(len(key))
	}
}
