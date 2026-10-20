package server

import (
	"bytes"
	"math"
	"math/rand/v2"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

// shortcut: random replies are capped at 1M elements; raise this when
// client-output-buffer limits make larger replies safe to build.
const maxRandomReplyElements = 1_000_000

func registerHashCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "hset", Arity: -4, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		hsetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hget", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		hgetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hmget", Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		hmgetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hdel", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		hdelCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hlen", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		hlenCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hgetall", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		hgetallCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hkeys", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		hkeysCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hvals", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		hvalsCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hexists", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		hexistsCommand(st, args, out)
	}})
	reg.Register(Command{Name: "hrandfield", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		hrandfieldCommand(st, args, out)
	}})
}

func hashValue(st *store.Store, key string) (store.Value, bool, string) {
	v, ok := st.Lookup(key)
	if !ok {
		return store.Value{}, false, ""
	}
	if v.Kind != store.KindHash {
		return store.Value{}, true, protocol.WrongType
	}
	return v, true, ""
}

func hashIndex(hash []store.HashEntry, field []byte) int {
	for i := range hash {
		if bytes.Equal(hash[i].Field, field) {
			return i
		}
	}
	return -1
}

func hashFieldIndex(v store.Value, field []byte) int {
	if v.HashIndex == nil {
		return hashIndex(v.Hash, field)
	}
	index, ok := v.HashIndex[string(field)]
	if !ok {
		return -1
	}
	return index
}

func hsetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	if len(args)%2 == 0 {
		out.Error(protocol.WrongArity("hset"))
		return
	}
	v, exists, errMsg := hashValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	hash := v.Hash
	index := v.HashIndex
	if index == nil {
		index = make(map[string]int, len(hash))
		for i, entry := range hash {
			index[string(entry.Field)] = i
		}
	}
	added := int64(0)
	for i := 1; i < len(args); i += 2 {
		field := string(args[i])
		if pos, ok := index[field]; ok {
			hash[pos].Value = args[i+1]
			continue
		}
		index[field] = len(hash)
		hash = append(hash, store.HashEntry{Field: args[i], Value: args[i+1]})
		added++
	}
	if exists || len(hash) > 0 {
		st.SetPreservingTTL(key, store.Value{Kind: store.KindHash, Hash: hash, HashIndex: index})
	}
	out.Int(added)
}

func hgetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Null()
		return
	}
	if index := hashFieldIndex(v, args[1]); index >= 0 {
		out.Bulk(v.Hash[index].Value)
		return
	}
	out.Null()
}

func hmgetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	out.ArrayHeader(len(args) - 1)
	for _, field := range args[1:] {
		if !exists {
			out.Null()
			continue
		}
		if index := hashFieldIndex(v, field); index >= 0 {
			out.Bulk(v.Hash[index].Value)
		} else {
			out.Null()
		}
	}
}

func hdelCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	v, exists, errMsg := hashValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	hash := v.Hash
	oldLen := len(hash)
	next := hash[:0]
	removed := int64(0)
	for _, entry := range hash {
		deleted := false
		for _, field := range args[1:] {
			if bytes.Equal(entry.Field, field) {
				deleted = true
				break
			}
		}
		if deleted {
			removed++
			continue
		}
		next = append(next, entry)
	}
	if removed > 0 {
		if len(next) == 0 {
			st.Delete(key)
		} else {
			for i := len(next); i < oldLen; i++ {
				hash[i] = store.HashEntry{}
			}
			index := make(map[string]int, len(next))
			for i, entry := range next {
				index[string(entry.Field)] = i
			}
			st.SetPreservingTTL(key, store.Value{Kind: store.KindHash, Hash: next, HashIndex: index})
		}
	}
	out.Int(removed)
}

func hlenCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	out.Int(int64(len(v.Hash)))
}

func hgetallCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
	out.ArrayHeader(2 * len(v.Hash))
	for _, entry := range v.Hash {
		out.Bulk(entry.Field)
		out.Bulk(entry.Value)
	}
}

func hkeysCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
	out.ArrayHeader(len(v.Hash))
	for _, entry := range v.Hash {
		out.Bulk(entry.Field)
	}
}

func hvalsCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
	out.ArrayHeader(len(v.Hash))
	for _, entry := range v.Hash {
		out.Bulk(entry.Value)
	}
}

func hexistsCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists || hashFieldIndex(v, args[1]) < 0 {
		out.Int(0)
		return
	}
	out.Int(1)
}

func hrandfieldCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	hasCount := len(args) >= 2
	withValues := false
	var count int64
	if hasCount {
		n, ok := protocol.ParseInt(args[1])
		if !ok {
			out.Error(protocol.NotInteger)
			return
		}
		if n == math.MinInt64 {
			out.Error(protocol.CountRange)
			return
		}
		count = n
		if len(args) > 3 {
			out.Error(protocol.SyntaxError)
			return
		}
		if len(args) == 3 {
			if !strings.EqualFold(string(args[2]), "WITHVALUES") {
				out.Error(protocol.SyntaxError)
				return
			}
			withValues = true
		}
	}

	v, exists, errMsg := hashValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		if hasCount {
			out.ArrayHeader(0)
		} else {
			out.Null()
		}
		return
	}
	hash := v.Hash
	if !hasCount {
		out.Bulk(hash[rand.IntN(len(hash))].Field)
		return
	}
	if count == 0 {
		out.ArrayHeader(0)
		return
	}

	mult := 1
	if withValues {
		mult = 2
	}
	emit := func(entry store.HashEntry) {
		out.Bulk(entry.Field)
		if withValues {
			out.Bulk(entry.Value)
		}
	}

	if count > 0 {
		take := len(hash)
		if count < int64(take) {
			take = int(count)
		}
		if take > maxRandomReplyElements/mult {
			out.Error(protocol.RandomCountTooLarge)
			return
		}
		out.ArrayHeader(take * mult)
		if take == 1 {
			emit(hash[rand.IntN(len(hash))])
			return
		}
		for _, index := range rand.Perm(len(hash))[:take] {
			emit(hash[index])
		}
		return
	}

	take := -count
	if take > int64(maxRandomReplyElements/mult) {
		out.Error(protocol.RandomCountTooLarge)
		return
	}
	out.ArrayHeader(int(take) * mult)
	for i := int64(0); i < take; i++ {
		emit(hash[rand.IntN(len(hash))])
	}
}
