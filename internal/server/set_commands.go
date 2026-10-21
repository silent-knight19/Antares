package server

import (
	"bytes"
	"math"
	"math/rand/v2"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerSetCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "sadd", Write: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		saddCommand(st, args, out)
	}})
	reg.Register(Command{Name: "srem", Write: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
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
	reg.Register(Command{Name: "spop", Write: true, Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		spopCommand(st, args, out)
	}})
	reg.Register(Command{Name: "srandmember", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		srandmemberCommand(st, args, out)
	}})
}

func setValue(st *store.Store, key string) (store.Value, bool, string) {
	return lookupTyped(st, key, store.KindSet)
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
		index = buildSetIndex(members)
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
	index := v.SetIndex
	if index == nil {
		index = buildSetIndex(members)
	}
	targets := make(map[string]struct{}, len(args)-1)
	for _, member := range args[1:] {
		targets[string(member)] = struct{}{}
	}
	members, removed := removeSetMembers(members, index, targets)
	if removed > 0 {
		if len(members) == 0 {
			st.Delete(key)
		} else {
			members = rightSize(members)
			st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: members, SetIndex: index})
		}
	}
	out.Int(removed)
}

// removeSetMembers uses the member index and swap-remove: sets are unordered,
// so this is O(1) per member instead of a full scan and index rebuild.
func removeSetMembers(members [][]byte, index map[string]int, targets map[string]struct{}) ([][]byte, int64) {
	removed := int64(0)
	for member := range targets {
		pos, ok := index[string(member)]
		if !ok {
			continue
		}
		last := len(members) - 1
		if pos != last {
			members[pos] = members[last]
			index[string(members[pos])] = pos
		}
		members[last] = nil
		members = members[:last]
		delete(index, string(member))
		removed++
	}
	return members, removed
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
	initial := len(members)
	index := v.SetIndex
	if index == nil {
		index = buildSetIndex(members)
	}
	out.ArrayHeader(take)
	for range take {
		pos := rand.IntN(len(members))
		popped := members[pos]
		out.Bulk(popped)
		last := len(members) - 1
		delete(index, string(popped))
		if pos != last {
			members[pos] = members[last]
			index[string(members[pos])] = pos
		}
		members[last] = nil
		members = members[:last]
	}
	if len(members) == 0 {
		st.Delete(key)
		return
	}
	next := rightSize(members)
	if len(next) < initial/2 {
		index = buildSetIndex(next)
	}
	st.SetPreservingTTL(key, store.Value{Kind: store.KindSet, Set: next, SetIndex: index})
}

func popMember(st *store.Store, key string, v store.Value, index int) []byte {
	members := v.Set
	last := len(members) - 1
	popped := members[index]
	if index != last {
		members[index] = members[last]
	}
	members[last] = nil
	next := rightSize(members[:last])
	if len(next) == 0 {
		st.Delete(key)
		return popped
	}
	indexMap := v.SetIndex
	if indexMap == nil {
		indexMap = buildSetIndex(next)
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
		eachDistinctIndex(len(members), take, func(index int) {
			out.Bulk(members[index])
		})
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
