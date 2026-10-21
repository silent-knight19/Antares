package store

import (
	"hash/maphash"
	"math/bits"
)

const minScanBuckets = 4

// Scan advances one SCAN step over a virtual power-of-two bucket table. Buckets
// are keyed by a seeded hash and visited with the reverse-binary cursor from
// Redis's dictScan, so a resized table (grow or shrink) can continue from an
// old cursor. Elements present for the whole iteration are returned at least
// once; duplicates are possible. match runs before expiry, mirroring SCAN's
// pattern filter. Expired keys are deleted when the pattern matches; a type
// filter is applied only after expiry, as in Redis.
func (s *Store) Scan(cursor uint64, count int, match func([]byte) bool, kind Kind, filterKind bool) (uint64, []string) {
	if len(s.data) == 0 || count < 1 {
		return 0, nil
	}
	size := scanTableSize(len(s.data))
	mask := size - 1
	maxIterations := uint64(count)
	if maxIterations > ^uint64(0)/10 {
		maxIterations = ^uint64(0)
	} else {
		maxIterations *= 10
	}
	if maxIterations > size {
		maxIterations = size
	}

	now := s.NowMs()
	keys := make([]string, 0)
	sampled := 0
	for i := uint64(0); i < maxIterations; i++ {
		bucket := cursor & mask
		for key, value := range s.data {
			if maphash.String(s.scanSeed, key)&mask != bucket {
				continue
			}
			sampled++
			if match != nil && !match([]byte(key)) {
				continue
			}
			if at, ok := s.expires[key]; ok && now > at {
				delete(s.data, key)
				delete(s.expires, key)
				s.expiredKeys++
				continue
			}
			if filterKind && value.Kind != kind {
				continue
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
