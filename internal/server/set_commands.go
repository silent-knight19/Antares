package server

import (
	"bytes"
	"math"
	"math/rand/v2"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerSetCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "sadd", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		saddCommand(st, args, out)
	}})
	reg.Register(Command{Name: "srem", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		sremCommand(st, args, out)
	}})
	reg.Register(Command{Name: "smembers", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		smembersCommand(st, args, out)
	}})
	reg.Register(Command{Name: "sismember", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		sismemberCommand(st, args, out)
	}})
	reg.Register(Command{Name: "scard", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		scardCommand(st, args, out)
	}})
	reg.Register(Command{Name: "spop", Arity: -2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		spopCommand(st, args, out)
	}})
	reg.Register(Command{Name: "srandmember", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		srandmemberCommand(st, args, out)
	}})
}

func setValue(st *store.Store, key string) (store.Value, bool, string) {
	v, ok := st.Lookup(key)
	if !ok {
		return store.Value{}, false, ""
	}
	if v.Kind != store.KindSet {
		return store.Value{}, true, protocol.WrongType
	}
	return v, true, ""
}

func setIndex(members [][]byte, member []byte) int {
	for i := range members {
		if bytes.Equal(members[i], member) {
			return i
		}
	}
	return -1
}

func setMemberIndex(v store.Value, member []byte) int {
	if v.SetIndex == nil {
		return setIndex(v.Set, member)
	}
	index, ok := v.SetIndex[string(member)]
	if !ok {
		return -1
	}
	return index
}

func saddCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	v, exists, errMsg := setValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	members := v.Set
	index := v.SetIndex
	if index == nil {
		index = make(map[string]int, len(members))
		for i, member := range members {
			index[string(member)] = i
		}
	}
	added := int64(0)
	for _, member := range args[1:] {
		name := string(member)
		if _, ok := index[name]; ok {
			continue
		}
		index[name] = len(members)
		members = append(members, member)
		added++
	}
	if added > 0 || !exists {
		st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: members, SetIndex: index})
	}
	out.Int(added)
}

func sremCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	v, exists, errMsg := setValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	members := v.Set
	oldLen := len(members)
	next := members[:0]
	removed := int64(0)
	for _, member := range members {
		drop := false
		for _, target := range args[1:] {
			if bytes.Equal(member, target) {
				drop = true
				break
			}
		}
		if drop {
			removed++
			continue
		}
		next = append(next, member)
	}
	if removed > 0 {
		if len(next) == 0 {
			st.Delete(key)
		} else {
			for i := len(next); i < oldLen; i++ {
				members[i] = nil
			}
			index := make(map[string]int, len(next))
			for i, member := range next {
				index[string(member)] = i
			}
			st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: next, SetIndex: index})
		}
	}
	out.Int(removed)
}

func smembersCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := setValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
	out.ArrayHeader(len(v.Set))
	for _, member := range v.Set {
		out.Bulk(member)
	}
}

func sismemberCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := setValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists || setMemberIndex(v, args[1]) < 0 {
		out.Int(0)
		return
	}
	out.Int(1)
}

func scardCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := setValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	out.Int(int64(len(v.Set)))
}

func spopCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	if len(args) > 2 {
		out.Error(protocol.SyntaxError)
		return
	}
	hasCount := len(args) == 2
	count := int64(0)
	if hasCount {
		n, ok := protocol.ParseInt(args[1])
		if !ok || n < 0 {
			out.Error(protocol.CountPositive)
			return
		}
		count = n
	}

	key := string(args[0])
	v, exists, errMsg := setValue(st, key)
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
	members := v.Set
	if !hasCount {
		out.Bulk(popMember(st, key, v, rand.IntN(len(members))))
		return
	}
	if count == 0 {
		out.ArrayHeader(0)
		return
	}
	if count == 1 {
		out.ArrayHeader(1)
		out.Bulk(popMember(st, key, v, rand.IntN(len(members))))
		return
	}

	take := len(members)
	if count < int64(take) {
		take = int(count)
	}
	order := rand.Perm(len(members))[:take]
	selected := make([]bool, len(members))
	out.ArrayHeader(take)
	for _, index := range order {
		out.Bulk(members[index])
		selected[index] = true
	}
	next := members[:0]
	for i, member := range members {
		if !selected[i] {
			next = append(next, member)
		}
	}
	if len(next) == 0 {
		st.Delete(key)
	} else {
		for i := len(next); i < len(members); i++ {
			members[i] = nil
		}
		index := make(map[string]int, len(next))
		for i, member := range next {
			index[string(member)] = i
		}
		st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: next, SetIndex: index})
	}
}

func popMember(st *store.Store, key string, v store.Value, index int) []byte {
	members := v.Set
	last := len(members) - 1
	popped := members[index]
	if index != last {
		members[index] = members[last]
	}
	members[last] = nil
	next := members[:last]
	if len(next) == 0 {
		st.Delete(key)
		return popped
	}
	indexMap := v.SetIndex
	if indexMap == nil {
		indexMap = make(map[string]int, len(next))
		for i, member := range next {
			indexMap[string(member)] = i
		}
	} else {
		delete(indexMap, string(popped))
		if index != last {
			indexMap[string(members[index])] = index
		}
	}
	st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: next, SetIndex: indexMap})
	return popped
}

func srandmemberCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	if len(args) > 2 {
		out.Error(protocol.SyntaxError)
		return
	}
	hasCount := len(args) == 2
	count := int64(0)
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
	}

	v, exists, errMsg := setValue(st, string(args[0]))
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
	members := v.Set
	if !hasCount {
		out.Bulk(members[rand.IntN(len(members))])
		return
	}
	if count == 0 {
		out.ArrayHeader(0)
		return
	}
	if count > 0 {
		take := len(members)
		if count < int64(take) {
			take = int(count)
		}
		if take > maxRandomReplyElements {
			out.Error(protocol.RandomCountTooLarge)
			return
		}
		out.ArrayHeader(take)
		if take == 1 {
			out.Bulk(members[rand.IntN(len(members))])
			return
		}
		for _, index := range rand.Perm(len(members))[:take] {
			out.Bulk(members[index])
		}
		return
	}

	take := -count
	if take > maxRandomReplyElements {
		out.Error(protocol.RandomCountTooLarge)
		return
	}
	out.ArrayHeader(int(take))
	for i := int64(0); i < take; i++ {
		out.Bulk(members[rand.IntN(len(members))])
	}
}
