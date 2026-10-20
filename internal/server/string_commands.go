package server

import (
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerStringCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "set", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		setCommand(st, args, out)
	}})
	reg.Register(Command{Name: "get", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		getCommand(st, args, out)
	}})
	reg.Register(Command{Name: "del", Arity: -2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		delCommand(st, args, out)
	}})
	reg.Register(Command{Name: "exists", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		existsCommand(st, args, out)
	}})
	reg.Register(Command{Name: "type", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		typeCommand(st, args, out)
	}})
	reg.Register(Command{Name: "mset", Arity: -3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		msetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "mget", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		mgetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "incr", Arity: 2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrDecrCommand(st, args[0], 1, out)
	}})
	reg.Register(Command{Name: "decr", Arity: 2, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrDecrCommand(st, args[0], -1, out)
	}})
	reg.Register(Command{Name: "incrby", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrByCommand(st, args, out)
	}})
	reg.Register(Command{Name: "decrby", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		decrByCommand(st, args, out)
	}})
	reg.Register(Command{Name: "incrbyfloat", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrByFloatCommand(st, args, out)
	}})
	reg.Register(Command{Name: "append", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		appendCommand(st, args, out)
	}})
	reg.Register(Command{Name: "strlen", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		strlenCommand(st, args, out)
	}})
	reg.Register(Command{Name: "getset", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		getsetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "setnx", Arity: 3, Flags: FlagWrite, Handler: func(args [][]byte, out *protocol.Encoder) {
		setnxCommand(st, args, out)
	}})
}

const maxStringLen = 512 << 20

type setOptions struct {
	nx, xx, get, keepTTL bool
	expireArgs           []byte
	expireUnit           time.Duration
	hasExpire            bool
}

func setCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	opts, errMsg := parseSetOptions(args[2:])
	if errMsg != "" {
		out.Error(errMsg)
		return
	}

	var expiresAt int64
	if opts.hasExpire {
		seconds, ok := protocol.ParseInt(opts.expireArgs)
		if !ok {
			out.Error(protocol.NotInteger)
			return
		}
		expiresAt, ok = setExpiry(time.Now().UnixMilli(), seconds, opts.expireUnit)
		if !ok {
			out.Error(protocol.InvalidExpireTime("set"))
			return
		}
	}

	key := string(args[0])
	old, exists := st.Get(key)
	if (opts.nx && exists) || (opts.xx && !exists) {
		if opts.get {
			out.Bulk(old)
		} else {
			out.Null()
		}
		return
	}

	switch {
	case opts.keepTTL:
		st.SetKeepTTL(key, args[1])
	case opts.hasExpire:
		st.SetWithTTL(key, args[1], expiresAt)
	default:
		st.Set(key, args[1])
	}

	if opts.get {
		out.Bulk(old)
	} else {
		out.Simple("OK")
	}
}

func parseSetOptions(args [][]byte) (setOptions, string) {
	var opts setOptions
	for i := 0; i < len(args); i++ {
		option := strings.ToUpper(string(args[i]))
		switch option {
		case "NX":
			opts.nx = true
		case "XX":
			opts.xx = true
		case "GET":
			opts.get = true
		case "KEEPTTL":
			if opts.hasExpire {
				return opts, protocol.SyntaxError
			}
			opts.keepTTL = true
		case "EX", "PX":
			if opts.keepTTL || i+1 == len(args) {
				return opts, protocol.SyntaxError
			}
			unit := time.Second
			if option == "PX" {
				unit = time.Millisecond
			}
			if opts.hasExpire && opts.expireUnit != unit {
				return opts, protocol.SyntaxError
			}
			opts.expireArgs = args[i+1]
			opts.expireUnit = unit
			opts.hasExpire = true
			i++
		default:
			return opts, protocol.SyntaxError
		}
	}
	if opts.nx && opts.xx {
		return opts, protocol.SyntaxError
	}
	return opts, ""
}

func setExpiry(nowMs, n int64, unit time.Duration) (int64, bool) {
	if n <= 0 {
		return 0, false
	}
	if unit == time.Second {
		if n > (math.MaxInt64-nowMs)/1000 {
			return 0, false
		}
		n *= 1000
	}
	if n > math.MaxInt64-nowMs {
		return 0, false
	}
	return nowMs + n, true
}

func getCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	value, ok := st.Get(string(args[0]))
	if !ok {
		out.Null()
		return
	}
	out.Bulk(value)
}

func delCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	var n int64
	for _, key := range args {
		if st.Delete(string(key)) {
			n++
		}
	}
	out.Int(n)
}

func existsCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	var n int64
	for _, key := range args {
		if st.Exists(string(key)) {
			n++
		}
	}
	out.Int(n)
}

func typeCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	if st.Exists(string(args[0])) {
		out.Simple("string")
		return
	}
	out.Simple("none")
}

func msetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	if len(args)%2 != 0 {
		out.Error(protocol.WrongArity("mset"))
		return
	}
	for i := 0; i < len(args); i += 2 {
		st.Set(string(args[i]), args[i+1])
	}
	out.Simple("OK")
}

func mgetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	out.ArrayHeader(len(args))
	for _, key := range args {
		value, ok := st.Get(string(key))
		if !ok {
			out.Null()
			continue
		}
		out.Bulk(value)
	}
}

func incrDecrCommand(st *store.Store, keyBytes []byte, incr int64, out *protocol.Encoder) {
	key := string(keyBytes)
	current := int64(0)
	value, exists := st.Get(key)
	if exists {
		n, ok := protocol.ParseInt(value)
		if !ok {
			out.Error(protocol.NotInteger)
			return
		}
		current = n
	}
	if (incr > 0 && current > math.MaxInt64-incr) || (incr < 0 && current < math.MinInt64-incr) {
		out.Error(protocol.IncrementOverflow)
		return
	}
	current += incr
	newValue := strconv.AppendInt(nil, current, 10)
	if exists {
		st.SetKeepTTL(key, newValue)
	} else {
		st.Set(key, newValue)
	}
	out.Int(current)
}

func incrByCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	incr, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	incrDecrCommand(st, args[0], incr, out)
}

func decrByCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	incr, ok := protocol.ParseInt(args[1])
	if !ok {
		out.Error(protocol.NotInteger)
		return
	}
	if incr == math.MinInt64 {
		out.Error(protocol.DecrementOverflow)
		return
	}
	incrDecrCommand(st, args[0], -incr, out)
}

const (
	longDoublePrec     = 113
	maxLongDoubleChars = 5 * 1024
)

var (
	longDoubleMax = func() *big.Float {
		n := new(big.Int).Lsh(big.NewInt(1), 16384)
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), 16384-longDoublePrec))
		return new(big.Float).SetPrec(longDoublePrec).SetInt(n)
	}()
	longDoubleHalfMin = new(big.Float).SetPrec(longDoublePrec).SetMantExp(big.NewFloat(0.5), -16494)
)

// parseLongDouble mirrors Redis's string2ld: C long double is binary128 on
// aarch64, so 113 bits reproduce its parsing and rounding here.
func parseLongDouble(b []byte) (*big.Float, bool) {
	if len(b) == 0 || len(b) >= maxLongDoubleChars {
		return nil, false
	}
	s := string(b)
	if strings.IndexByte(s, '_') >= 0 {
		return nil, false
	}
	body := s
	negative := false
	if body[0] == '+' || body[0] == '-' {
		negative = body[0] == '-'
		body = body[1:]
	}
	if body == "" {
		return nil, false
	}
	lower := strings.ToLower(body)
	if lower == "inf" || lower == "infinity" {
		return new(big.Float).SetPrec(longDoublePrec).SetInf(negative), true
	}
	if strings.HasPrefix(lower, "nan") {
		return nil, false
	}
	base := 10
	if strings.HasPrefix(lower, "0x") {
		base = 0
		if !strings.ContainsRune(lower, 'p') {
			s += "p0"
		}
	}
	f, _, err := big.ParseFloat(s, base, longDoublePrec, big.ToNearestEven)
	if err != nil || !inLongDoubleRange(f) {
		return nil, false
	}
	return f, true
}

func inLongDoubleRange(f *big.Float) bool {
	if f.Sign() == 0 {
		return true
	}
	abs := new(big.Float).SetPrec(f.Prec()).Abs(f)
	return abs.Cmp(longDoubleMax) <= 0 && abs.Cmp(longDoubleHalfMin) > 0
}

func longDoubleOverflows(f *big.Float) bool {
	if f.IsInf() {
		return true
	}
	if f.Sign() == 0 {
		return false
	}
	abs := new(big.Float).SetPrec(f.Prec()).Abs(f)
	return abs.Cmp(longDoubleMax) > 0
}

func formatLongDouble(f *big.Float) string {
	s := f.Text('f', 17)
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

func incrByFloatCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	incr, ok := parseLongDouble(args[1])
	if !ok {
		out.Error(protocol.NotFloat)
		return
	}
	key := string(args[0])
	current := new(big.Float).SetPrec(longDoublePrec)
	value, exists := st.Get(key)
	if exists {
		current, ok = parseLongDouble(value)
		if !ok {
			out.Error(protocol.NotFloat)
			return
		}
	}
	result := new(big.Float).SetPrec(longDoublePrec).Add(current, incr)
	if longDoubleOverflows(result) {
		out.Error(protocol.FloatResult)
		return
	}
	encoded := []byte(formatLongDouble(result))
	if exists {
		st.SetKeepTTL(key, encoded)
	} else {
		st.Set(key, encoded)
	}
	out.Bulk(encoded)
}

func appendCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	value, exists := st.Get(key)
	joined, ok := joinAppend(value, args[1], maxStringLen)
	if !ok {
		out.Error(protocol.StringTooLong)
		return
	}
	if exists {
		st.SetKeepTTL(key, joined)
	} else {
		st.Set(key, joined)
	}
	out.Int(int64(len(joined)))
}

func joinAppend(old, add []byte, limit int) ([]byte, bool) {
	total := len(old) + len(add)
	if total > limit {
		return nil, false
	}
	joined := make([]byte, total)
	copy(joined, old)
	copy(joined[len(old):], add)
	return joined, true
}

func strlenCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	value, ok := st.Get(string(args[0]))
	if !ok {
		out.Int(0)
		return
	}
	out.Int(int64(len(value)))
}

func getsetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	old, _ := st.Get(key)
	st.Set(key, args[1])
	out.Bulk(old)
}

func setnxCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	if _, ok := st.Get(key); ok {
		out.Int(0)
		return
	}
	st.Set(key, args[1])
	out.Int(1)
}
