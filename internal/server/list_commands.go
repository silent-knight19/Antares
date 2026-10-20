package server

import (
	"bytes"
	"math"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerListCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "lpush", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		pushList(st, args, true, out)
	}})
	reg.Register(Command{Name: "rpush", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		pushList(st, args, false, out)
	}})
	reg.Register(Command{Name: "lpop", Arity: -2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		popList(st, args, true, "lpop", out)
	}})
	reg.Register(Command{Name: "rpop", Arity: -2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
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
	reg.Register(Command{Name: "lset", Arity: 4, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		lsetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "lrem", Arity: 4, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		lremCommand(st, args, out)
	}})
	reg.Register(Command{Name: "ltrim", Arity: 4, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		ltrimCommand(st, args, out)
	}})
}

func listValue(st *store.Store, key string) ([][]byte, bool, string) {
	v, ok := st.Lookup(key)
	if !ok {
		return nil, false, ""
	}
	if v.Kind != store.KindList {
		return nil, true, protocol.WrongType
	}
	return v.List, true, ""
}

func saveList(st *store.Store, key string, list [][]byte) {
	if len(list) == 0 {
		st.Delete(key)
		return
	}
	st.SetPreservingTTL(key, store.Value{Kind: store.KindList, List: list})
}

func pushList(st *store.Store, args [][]byte, left bool, out *protocol.Encoder) {
	key := string(args[0])
	elements := args[1:]
	list, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	var next [][]byte
	if !left && exists && cap(list)-len(list) >= len(elements) {
		next = list[:len(list)+len(elements)]
		copy(next[len(list):], elements)
	} else {
		next = make([][]byte, 0, len(list)+len(elements))
		if left {
			for i := len(elements) - 1; i >= 0; i-- {
				next = append(next, elements[i])
			}
			next = append(next, list...)
		} else {
			next = append(next, list...)
			next = append(next, elements...)
		}
	}
	if exists {
		st.SetPreservingTTL(key, store.Value{Kind: store.KindList, List: next})
	} else {
		st.SetList(key, next)
	}
	out.Int(int64(len(next)))
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

	list, exists, errMsg := listValue(st, string(args[0]))
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

	if !hasCount {
		if left {
			popped := list[0]
			list[0] = nil
			out.Bulk(popped)
			saveList(st, string(args[0]), list[1:])
		} else {
			last := len(list) - 1
			popped := list[last]
			list[last] = nil
			out.Bulk(popped)
			saveList(st, string(args[0]), list[:last])
		}
		return
	}
	if count == 0 {
		out.ArrayHeader(0)
		return
	}
	take := len(list)
	if count < int64(take) {
		take = int(count)
	}
	out.ArrayHeader(take)
	if left {
		for i := 0; i < take; i++ {
			out.Bulk(list[i])
			list[i] = nil
		}
		saveList(st, string(args[0]), list[take:])
		return
	}
	for i := len(list) - 1; i >= len(list)-take; i-- {
		out.Bulk(list[i])
		list[i] = nil
	}
	saveList(st, string(args[0]), list[:len(list)-take])
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
	list, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.ArrayHeader(0)
		return
	}
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
	list, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}
	out.Int(int64(len(list)))
}

func lindexCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	list, exists, errMsg := listValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Null()
		return
	}
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
	list, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Error(protocol.NoSuchKey)
		return
	}
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
	st.SetPreservingTTL(key, store.Value{Kind: store.KindList, List: list})
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
	list, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Int(0)
		return
	}

	limit := count
	if limit < 0 {
		limit = -limit
	}
	if limit == 0 {
		limit = math.MaxInt64
	}
	var removed int64
	if count >= 0 {
		oldLen := len(list)
		next := list[:0]
		for _, element := range list {
			if removed < limit && bytes.Equal(element, args[2]) {
				removed++
				continue
			}
			next = append(next, element)
		}
		for i := len(next); i < oldLen; i++ {
			list[i] = nil
		}
		list = next
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
		list = next
	}
	if removed > 0 {
		saveList(st, key, list)
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
	list, exists, errMsg := listValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !exists {
		out.Simple("OK")
		return
	}
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
	for i := int64(0); i < start; i++ {
		list[i] = nil
	}
	for i := stop + 1; i < int64(len(list)); i++ {
		list[i] = nil
	}
	st.SetPreservingTTL(key, store.Value{Kind: store.KindList, List: list[start : stop+1]})
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
