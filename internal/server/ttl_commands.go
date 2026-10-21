package server

import (
	"math"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

const (
	expireNXConflict   = "ERR NX and XX, GT or LT options at the same time are not compatible"
	expireGTLTConflict = "ERR GT and LT options at the same time are not compatible"
)

func registerTTLCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "ttl", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		ttlCommand(st, args, out, false)
	}})
	reg.Register(Command{Name: "pttl", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		ttlCommand(st, args, out, true)
	}})
	reg.Register(Command{Name: "persist", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		if st.Persist(string(args[0])) {
			out.Int(1)
		} else {
			out.Int(0)
		}
	}})
	reg.Register(Command{Name: "expire", Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		expireCommand(st, args, out, "expire", true, true)
	}})
	reg.Register(Command{Name: "pexpire", Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		expireCommand(st, args, out, "pexpire", true, false)
	}})
	reg.Register(Command{Name: "expireat", Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		expireCommand(st, args, out, "expireat", false, true)
	}})
	reg.Register(Command{Name: "pexpireat", Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		expireCommand(st, args, out, "pexpireat", false, false)
	}})
	reg.Register(Command{Name: "dbsize", Arity: 1, Handler: func(_ [][]byte, out *protocol.Encoder) {
		out.Int(int64(st.Len()))
	}})
}

func ttlCommand(st *store.Store, args [][]byte, out *protocol.Encoder, milliseconds bool) {
	ttl, ok := st.TTLms(string(args[0]))
	if !ok {
		out.Int(-2)
		return
	}
	if ttl < 0 {
		out.Int(-1)
		return
	}
	if milliseconds {
		out.Int(ttl)
		return
	}
	out.Int((ttl + 500) / 1000)
}

// expireCommand implements EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT. Redis parses
// and validates every option before the integer argument, so an unsupported
// option wins over a malformed TTL.
func expireCommand(st *store.Store, args [][]byte, out *protocol.Encoder, name string, relative, seconds bool) {
	flags, errMsg := parseExpireFlags(args[2:])
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	n, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	when, ok := expireTimestamp(st.NowMs(), n, relative, seconds)
	if !ok {
		out.Error(protocol.InvalidExpireTime(name))
		return
	}

	key := string(args[0])
	current, exists := st.ExpiresAt(key)
	if !exists {
		out.Int(0)
		return
	}
	hasTTL := current != 0
	switch {
	case flags.nx && hasTTL:
		out.Int(0)
		return
	case flags.xx && !hasTTL:
		out.Int(0)
		return
	case flags.gt && (!hasTTL || when <= current):
		out.Int(0)
		return
	case flags.lt && hasTTL && when >= current:
		out.Int(0)
		return
	}
	st.SetExpire(key, when)
	out.Int(1)
}

type expireFlags struct {
	nx, xx, gt, lt bool
}

func parseExpireFlags(args [][]byte) (expireFlags, string) {
	var flags expireFlags
	for _, arg := range args {
		switch strings.ToUpper(string(arg)) {
		case "NX":
			flags.nx = true
		case "XX":
			flags.xx = true
		case "GT":
			flags.gt = true
		case "LT":
			flags.lt = true
		default:
			return flags, "ERR Unsupported option " + string(arg)
		}
	}
	if flags.nx && (flags.xx || flags.gt || flags.lt) {
		return flags, expireNXConflict
	}
	if flags.gt && flags.lt {
		return flags, expireGTLTConflict
	}
	return flags, ""
}

func expireTimestamp(base int64, n int64, relative, seconds bool) (int64, bool) {
	if seconds {
		if n > math.MaxInt64/1000 || n < math.MinInt64/1000 {
			return 0, false
		}
		n *= 1000
	}
	if !relative {
		base = 0
	}
	if n > math.MaxInt64-base {
		return 0, false
	}
	return base + n, true
}
