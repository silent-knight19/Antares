package store

// The keyspace lives in Go maps and slices, so there is no allocator counter to
// read. This file models the retained heap from the shapes we allocate: a
// per-entry map cost plus the exact payload lengths, which INFO reports and
// maxmemory will enforce. The model is calibrated within 10% of the runtime
// heap for payload-sized datasets (TestMemoryUsageTracksRuntime); it is a
// logical estimate, not the allocator's byte count.
const (
	EntryOverhead    = 264
	IndexOverhead    = 48
	SliceHeaderBytes = 24
	HashEntryBytes   = 48

	entryOverhead    = EntryOverhead
	indexOverhead    = IndexOverhead
	sliceHeaderBytes = SliceHeaderBytes
	hashEntryBytes   = HashEntryBytes
)

// Usage splits accounted keyspace bytes into the total (the number maxmemory
// compares against) and the dataset share (payload only; the rest is overhead,
// Total-Dataset).
type Usage struct {
	Total   int64
	Dataset int64
}

// MemoryUsage estimates the heap bytes held by every key, including expired
// keys not yet collected by lazy access or the active cycle. The total is
// maintained on every mutation, so this is O(1).
func (s *Store) MemoryUsage() Usage {
	return s.usage
}

// usageOf computes one key's accounted bytes. Values are stored with this
// cached so replacing or deleting a key can subtract its old cost exactly.
func usageOf(key string, v Value) Usage {
	u := Usage{Total: entryOverhead + int64(len(key))}
	u.add(v)
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
