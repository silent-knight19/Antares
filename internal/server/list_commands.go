package server

import (
	"bytes"
	"math"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerListCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "lpush", Write: true, DenyOOM: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		pushList(st, args, true, out)
	}})
	reg.Register(Command{Name: "rpush", Write: true, DenyOOM: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		pushList(st, args, false, out)
	}})
	reg.Register(Command{Name: "lpop", Write: true, Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		popList(st, args, true, "lpop", out)
	}})
	reg.Register(Command{Name: "rpop", Write: true, Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		popList(st, args, false, "rpop", out)
	}})
	reg.Register(Command{Name: "lrange", Arity: 4, Handler: func(args [][]byte, out *protocol.Encoder) {
		lrangeCommand(st, args, out)
	}})
	reg.Register(Command{Name: "llen", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		llenCommand(st, args, out)
	}})
	reg.Register(Command{Name: "lindex", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		lindexCommand(st, args, out)
	}})
	reg.Register(Command{Name: "lset", Write: true, DenyOOM: true, Arity: 4, Handler: func(args [][]byte, out *protocol.Encoder) {
		lsetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "lrem", Write: true, Arity: 4, Handler: func(args [][]byte, out *protocol.Encoder) {
		lremCommand(st, args, out)
	}})
	reg.Register(Command{Name: "ltrim", Write: true, Arity: 4, Handler: func(args [][]byte, out *protocol.Encoder) {
		ltrimCommand(st, args, out)
	}})
}

func listValue(st *store.Store, key string) (store.Value, bool, string) {
	return lookupTyped(st, key, store.KindList)
}

func listView(v store.Value) [][]byte {
	return v.List[v.ListHead:]
}

func saveListValue(st *store.Store, key string, v store.Value) {
	if len(v.List) == v.ListHead {
		st.Delete(key)
		return
	}
	v.Kind = store.KindList
	if v.Usage.Total > 0 {
		st.SetPreservingTTLFast(key, v, v.Usage)
	} else {
		st.SetPreservingTTL(key, v)
	}
}

func listMemoryUsage(key string, v store.Value) store.Usage {
	return store.Usage{
		Dataset: v.Usage.Dataset,
		Total:   store.EntryOverhead + int64(len(key)) + int64(cap(v.List))*store.SliceHeaderBytes + v.Usage.Dataset,
	}
}

func saveListState(st *store.Store, key string, base [][]byte, head int, u store.Usage) {
	if len(base) == head {
		st.Delete(key)
		return
	}
	liveLen := len(base) - head
	if (head > 256 && head > liveLen) || (cap(base) > 4*liveLen && cap(base) > 512) {
		base, head = listStorage(base, head, liveLen)
		u.Total = store.EntryOverhead + int64(len(key)) + int64(cap(base))*store.SliceHeaderBytes + u.Dataset
	}
	saveListValue(st, key, store.Value{Kind: store.KindList, List: base, ListHead: head, Usage: u})
}

// listStorage trims to the live span and keeps front headroom for LPUSH; it
// reallocates when the front gap is too small or the backing array is over 2x.
func listStorage(base [][]byte, head, length int) ([][]byte, int) {
	if length == 0 {
		return nil, 0
	}
	headroom := length / 2
	if headroom < 64 {
		headroom = 64
	} else if headroom > 65536 {
		headroom = 65536
	}
	next := make([][]byte, length+headroom)
	copy(next[headroom:], base[head:head+length])
	return next, headroom
}

func pushList(st *store.Store, args [][]byte, left bool, out *protocol.Encoder) {
	key := string(args[0])
	elements := args[1:]
	v, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	length := len(v.List) - v.ListHead

	var addedBytes int64
	if len(elements) == 1 {
		addedBytes = int64(len(elements[0]))
	} else {
		for _, el := range elements {
			addedBytes += int64(len(el))
		}
	}
	if !exists {
		v.Usage.Dataset = 0
	}
	v.Usage.Dataset += addedBytes

	if !left {
		if !exists {
			v.List = make([][]byte, 0, max(len(elements), 256))
		}
		v.List = append(v.List, elements...)
		v.Usage = listMemoryUsage(key, v)
		saveListValue(st, key, v)
		out.Int(int64(length + len(elements)))
		return
	}

	n := len(elements)
	if v.ListHead >= n {
		oldHead := v.ListHead
		for i, element := range elements {
			v.List[oldHead-1-i] = element
		}
		v.ListHead = oldHead - n
		v.Usage = listMemoryUsage(key, v)
		saveListValue(st, key, v)
		out.Int(int64(length + n))
		return
	}

	headroom := 2 * (length + n)
	if headroom < 256 {
		headroom = 256
	} else if headroom > 65536 {
		headroom = 65536
	}
	base := make([][]byte, length+n+headroom)
	for i, element := range elements {
		base[headroom+n-1-i] = element
	}
	copy(base[headroom+n:], v.List[v.ListHead:])
	v.List = base
	v.ListHead = headroom
	v.Usage = listMemoryUsage(key, v)
	saveListValue(st, key, v)
	out.Int(int64(length + n))
}

func popList(st *store.Store, args [][]byte, left bool, name string, out *protocol.Encoder) {
	if len(args) > 2 {
		out.Error(protocol.WrongArity(name))
		return
	}
	hasCount := len(args) == 2
	var count int64
	if hasCount {
		n, ok := protocol.ParseInt(args[1])
		if !ok || n < 0 {
			out.Error(protocol.CountPositive)
			return
		}
		count = n
	}

	key := string(args[0])
	v, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		if hasCount {
			out.NullArray()
		} else {
			out.Null()
		}
		return
	}
	length := len(v.List) - v.ListHead

	if !hasCount {
		if left {
			popped := v.List[v.ListHead]
			out.Bulk(popped)
			v.List[v.ListHead] = nil
			v.Usage.Dataset -= int64(len(popped))
			v.Usage = listMemoryUsage(key, v)
			saveListState(st, key, v.List, v.ListHead+1, v.Usage)
		} else {
			last := len(v.List) - 1
			popped := v.List[last]
			out.Bulk(popped)
			v.List[last] = nil
			v.Usage.Dataset -= int64(len(popped))
			v.Usage = listMemoryUsage(key, v)
			saveListState(st, key, v.List[:last], v.ListHead, v.Usage)
		}
		return
	}
	if count == 0 {
		out.ArrayHeader(0)
		return
	}
	take := length
	if count < int64(take) {
		take = int(count)
	}
	out.ArrayHeader(take)
	if left {
		for i := 0; i < take; i++ {
			elem := v.List[v.ListHead+i]
			out.Bulk(elem)
			v.Usage.Dataset -= int64(len(elem))
			v.List[v.ListHead+i] = nil
		}
		v.Usage = listMemoryUsage(key, v)
		saveListState(st, key, v.List, v.ListHead+take, v.Usage)
		return
	}
	for i := len(v.List) - 1; i >= len(v.List)-take; i-- {
		elem := v.List[i]
		out.Bulk(elem)
		v.Usage.Dataset -= int64(len(elem))
		v.List[i] = nil
	}
	v.Usage = listMemoryUsage(key, v)
	saveListState(st, key, v.List[:len(v.List)-take], v.ListHead, v.Usage)
}

func lrangeCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	start, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	stop, ok := protocol.ParseInt(args[2])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	v, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
	list := listView(v)
	start, stop, ok = listBounds(start, stop, int64(len(list)))
	if !ok {
		out.ArrayHeader(0)
		return
	}
	out.ArrayHeader(int(stop - start + 1))
	for i := start; i <= stop; i++ {
		out.Bulk(list[i])
	}
}

func llenCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	out.Int(int64(len(v.List) - v.ListHead))
}

func lindexCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	v, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Null()
		return
	}
	list := listView(v)
	index, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	if index < 0 {
		index += int64(len(list))
	}
	if index < 0 || index >= int64(len(list)) {
		out.Null()
		return
	}
	out.Bulk(list[index])
}

func lsetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	v, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Error(protocol.NoSuchKey)
		return
	}
	list := listView(v)
	index, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	if index < 0 {
		index += int64(len(list))
	}
	if index < 0 || index >= int64(len(list)) {
		out.Error(protocol.IndexOutOfRange)
		return
	}
	list[index] = args[2]
	saveListValue(st, key, v)
	out.Simple("OK")
}

func lremCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	count, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	if count == math.MinInt64 {
		out.Error(protocol.CountRange)
		return
	}
	key := string(args[0])
	v, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	list := listView(v)

	limit := count
	if limit < 0 {
		limit = -limit
	}
	if limit == 0 {
		limit = math.MaxInt64
	}
	var removed int64
	if count >= 0 {
		head := v.ListHead
		next := v.List[head:head]
		for _, element := range list {
			if removed < limit && bytes.Equal(element, args[2]) {
				removed++
				continue
			}
			next = append(next, element)
		}
		for i := head + len(next); i < len(v.List); i++ {
			v.List[i] = nil
		}
		if removed > 0 {
			base, remHead := listStorage(v.List[:head+len(next)], head, len(next))
			saveListValue(st, key, store.Value{Kind: store.KindList, List: base, ListHead: remHead})
		}
	} else {
		next := make([][]byte, 0, len(list))
		for i := len(list) - 1; i >= 0; i-- {
			if removed < limit && bytes.Equal(list[i], args[2]) {
				removed++
				continue
			}
			next = append(next, list[i])
		}
		for i, j := 0, len(next)-1; i < j; i, j = i+1, j-1 {
			next[i], next[j] = next[j], next[i]
		}
		if removed > 0 {
			saveListValue(st, key, store.Value{Kind: store.KindList, List: rightSize(next)})
		}
	}
	out.Int(removed)
}

func ltrimCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	start, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	stop, ok := protocol.ParseInt(args[2])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	key := string(args[0])
	v, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Simple("OK")
		return
	}
	list := listView(v)
	start, stop, ok = listBounds(start, stop, int64(len(list)))
	if !ok {
		st.Delete(key)
		out.Simple("OK")
		return
	}
	if start == 0 && stop == int64(len(list))-1 {
		out.Simple("OK")
		return
	}
	head := v.ListHead
	newHead := head + int(start)
	keptEnd := newHead + int(stop-start+1)
	for i := head; i < newHead; i++ {
		v.List[i] = nil
	}
	for i := keptEnd; i < len(v.List); i++ {
		v.List[i] = nil
	}
	base, trimmedHead := listStorage(v.List[:keptEnd], newHead, keptEnd-newHead)
	saveListValue(st, key, store.Value{Kind: store.KindList, List: base, ListHead: trimmedHead})
	out.Simple("OK")
}

// listBounds normalizes Redis's inclusive start/stop list range. It reports
// false for empty and fully out-of-range windows.
func listBounds(start, stop, n int64) (int64, int64, bool) {
	if n == 0 {
		return 0, 0, false
	}
	if start < 0 {
		start += n
		if start < 0 {
			start = 0
		}
	}
	if stop < 0 {
		stop += n
	}
	if stop >= n {
		stop = n - 1
	}
	if stop < 0 || start >= n || start > stop {
		return 0, 0, false
	}
	return start, stop, true
}
