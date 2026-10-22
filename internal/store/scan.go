package store

import (
	"hash/maphash"
	"math/bits"
)

const (
	minScanBuckets = 4

	// maxScanBuckets caps the work one SCAN call may do regardless of COUNT.
	maxScanBuckets = 1 << 16
)

// Scan advances one SCAN step over a virtual power-of-two bucket table. Buckets
// are keyed by a seeded hash and visited with the reverse-binary cursor from
// Redis's dictScan, so a resized table (grow or shrink) can continue from an
// old cursor. Elements present for the whole iteration are returned at least
// once; duplicates are possible. match runs before expiry, mirroring SCAN's
// pattern filter. Expired keys are deleted when the pattern matches; a type
// filter is applied only after expiry, as in Redis.
//
// One pass over the keyspace collects every bucket this call may visit, so the
// cost is O(n) per call instead of O(n) per bucket.
//
// shortcut: a full walk still costs O(n²/COUNT) map lookups; add an ordered
// key index if scanning millions of keys becomes a real workload.
func (s *Store) Scan(cursor uint64, count int, match func([]byte) bool, kind Kind, filterKind bool) (uint64, []string) {
	if len(s.data) == 0 || count < 1 {
		return 0, nil
	}
	size := scanTableSize(len(s.data))
	mask := size - 1
	steps := uint64(count) * 10
	if steps < uint64(count) {
		steps = ^uint64(0)
	}
	if steps > size {
		steps = size
	}
	if steps > maxScanBuckets {
		steps = maxScanBuckets
	}

	bucketKeys := make([][]string, steps)
	visited := make(map[uint64]uint64, steps)
	for step, c := uint64(0), cursor; step < steps; step++ {
		visited[c&mask] = step
		c = nextScanCursor(c, mask)
	}
	for key := range s.data {
		if step, ok := visited[maphash.String(s.scanSeed, key)&mask]; ok {
			bucketKeys[step] = append(bucketKeys[step], key)
		}
	}

	now := s.NowMs()
	keys := make([]string, 0)
	sampled := 0
	for _, bucket := range bucketKeys {
		for _, key := range bucket {
			sampled++
			if match != nil && !match([]byte(key)) {
				continue
			}
			if at, ok := s.expires[key]; ok && now > at {
				s.removeValue(key)
				delete(s.data, key)
				delete(s.expires, key)
				s.expiredKeys++
				continue
			}
			if filterKind {
				if value, ok := s.data[key]; !ok || value.Kind != kind {
					continue
				}
			}
			keys = append(keys, key)
		}
		cursor = nextScanCursor(cursor, mask)
		if cursor == 0 || sampled >= count {
			break
		}
	}
	return cursor, keys
}

func scanTableSize(n int) uint64 {
	size := uint64(minScanBuckets)
	for size < uint64(n) {
		size <<= 1
	}
	return size
}

func nextScanCursor(cursor, mask uint64) uint64 {
	cursor |= ^mask
	cursor = bits.Reverse64(cursor)
	cursor++
	return bits.Reverse64(cursor)
}
