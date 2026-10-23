package server

import (
	"math/rand/v2"
	"slices"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

// lookupTyped returns a live value of kind, or the WRONGTYPE body when the key
// holds another type. Missing keys return ok=false with no error.
func lookupTyped(st *store.Store, key string, kind store.Kind) (store.Value, bool, string) {
	v, ok := st.Lookup(key)
	if !ok {
		return store.Value{}, false, ""
	}
	if v.Kind != kind {
		return store.Value{}, true, protocol.WrongType
	}
	return v, true, ""
}

func buildHashIndex(hash []store.HashEntry) map[string]int {
	return store.BuildHashIndex(hash)
}

func buildSetIndex(members [][]byte) map[string]int {
	return store.BuildSetIndex(members)
}

// rightSize releases an oversized backing array after a collection shrinks.
func rightSize[T any](s []T) []T {
	if len(s) > 0 && cap(s) > 2*len(s) {
		return slices.Clone(s)
	}
	return s
}

// eachDistinctIndex visits take distinct indices in random order without
// shuffling the whole collection when take is small.
func eachDistinctIndex(n, take int, emit func(int)) {
	if take > n/2 {
		for _, index := range rand.Perm(n)[:take] {
			emit(index)
		}
		return
	}
	seen := make(map[int]struct{}, take)
	for len(seen) < take {
		index := rand.IntN(n)
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		emit(index)
	}
}
