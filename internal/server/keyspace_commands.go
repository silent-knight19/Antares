package server

import (
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerKeyspaceCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "keys", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		keysCommand(st, args, out)
	}})
	reg.Register(Command{Name: "scan", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		scanCommand(st, args, out)
	}})
	reg.Register(Command{Name: "rename", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		renameCommand(st, args, out, false)
	}})
	reg.Register(Command{Name: "renamenx", Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		renameCommand(st, args, out, true)
	}})
	reg.Register(Command{Name: "randomkey", Arity: 1, Handler: func(_ [][]byte, out *protocol.Encoder) {
		randomKeyCommand(st, out)
	}})
	reg.Register(Command{Name: "flushdb", Arity: -1, Handler: func(args [][]byte, out *protocol.Encoder) {
		flushCommand(st, args, out)
	}})
	reg.Register(Command{Name: "flushall", Arity: -1, Handler: func(args [][]byte, out *protocol.Encoder) {
		flushCommand(st, args, out)
	}})
}

func keysCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	pattern := args[0]
	allKeys := len(pattern) == 1 && pattern[0] == '*'
	matched := make([]string, 0)
	for _, key := range st.Keys() {
		if allKeys || globMatch(pattern, []byte(key)) {
			matched = append(matched, key)
		}
	}
	out.ArrayHeader(len(matched))
	for _, key := range matched {
		out.Bulk([]byte(key))
	}
}

func renameCommand(st *store.Store, args [][]byte, out *protocol.Encoder, nx bool) {
	src, dst := string(args[0]), string(args[1])
	if _, ok := st.Lookup(src); !ok {
		out.Error(protocol.NoSuchKey)
		return
	}
	if src == dst {
		if nx {
			out.Int(0)
		} else {
			out.Simple("OK")
		}
		return
	}
	if nx && st.Exists(dst) {
		out.Int(0)
		return
	}
	st.Rename(src, dst)
	if nx {
		out.Int(1)
	} else {
		out.Simple("OK")
	}
}

func randomKeyCommand(st *store.Store, out *protocol.Encoder) {
	key, ok := st.RandomKey()
	if !ok {
		out.Null()
		return
	}
	out.Bulk([]byte(key))
}

func flushCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	if len(args) > 1 {
		out.Error(protocol.SyntaxError)
		return
	}
	if len(args) == 1 {
		switch strings.ToUpper(string(args[0])) {
		case "ASYNC", "SYNC":
		default:
			out.Error(protocol.SyntaxError)
			return
		}
	}
	st.Flush()
	out.Simple("OK")
}
