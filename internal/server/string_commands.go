package server

import (
	"math"
	"math/big"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func registerStringCommands(reg *Registry, st *store.Store) {
	reg.Register(Command{Name: "set", Write: true, DenyOOM: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		setCommand(st, args, out)
	}})
	reg.Register(Command{Name: "get", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		getCommand(st, args, out)
	}})
	reg.Register(Command{Name: "del", Write: true, Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		delCommand(st, args, out)
	}})
	reg.Register(Command{Name: "exists", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		existsCommand(st, args, out)
	}})
	reg.Register(Command{Name: "type", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		typeCommand(st, args, out)
	}})
	reg.Register(Command{Name: "mset", Write: true, DenyOOM: true, Arity: -3, Handler: func(args [][]byte, out *protocol.Encoder) {
		msetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "mget", Arity: -2, Handler: func(args [][]byte, out *protocol.Encoder) {
		mgetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "incr", Write: true, DenyOOM: true, Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrDecrCommand(st, args[0], 1, out)
	}})
	reg.Register(Command{Name: "decr", Write: true, DenyOOM: true, Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrDecrCommand(st, args[0], -1, out)
	}})
	reg.Register(Command{Name: "incrby", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrByCommand(st, args, out)
	}})
	reg.Register(Command{Name: "decrby", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		decrByCommand(st, args, out)
	}})
	reg.Register(Command{Name: "incrbyfloat", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		incrByFloatCommand(st, args, out)
	}})
	reg.Register(Command{Name: "append", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		appendCommand(st, args, out)
	}})
	reg.Register(Command{Name: "strlen", Arity: 2, Handler: func(args [][]byte, out *protocol.Encoder) {
		strlenCommand(st, args, out)
	}})
	reg.Register(Command{Name: "getset", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		getsetCommand(st, args, out)
	}})
	reg.Register(Command{Name: "setnx", Write: true, DenyOOM: true, Arity: 3, Handler: func(args [][]byte, out *protocol.Encoder) {
		setnxCommand(st, args, out)
	}})
}

const maxStringLen = 512 << 20

// stringValue returns a live string value. The final string is a ready-to-send
// error body when the key exists with another type.
func stringValue(st *store.Store, key string) ([]byte, bool, string) {
	v, ok, errMsg := lookupTyped(st, key, store.KindString)
	return v.Str, ok, errMsg
}

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
		expiresAt, ok = setExpiry(st.NowMs(), seconds, opts.expireUnit)
		if !ok {
			out.Error(protocol.InvalidExpireTime("set"))
			return
		}
	}

	key := string(args[0])
	var old []byte
	var exists bool
	if opts.get {
		var errMsg string
		old, exists, errMsg = stringValue(st, key)
		if errMsg != "" {
			out.Error(errMsg)
			return
		}
	} else if opts.nx || opts.xx || opts.keepTTL {
		exists = st.Exists(key)
	}
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
		st.SetPreservingTTL(key, store.Value{Kind: store.KindString, Str: args[1]})
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
	value, ok, errMsg := stringValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
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
	out.Simple(st.Type(string(args[0])))
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
		v, ok := st.Lookup(string(key))
		if !ok || v.Kind != store.KindString {
			out.Null()
			continue
		}
		out.Bulk(v.Str)
	}
}

func incrDecrCommand(st *store.Store, keyBytes []byte, incr int64, out *protocol.Encoder) {
	key := string(keyBytes)
	current := int64(0)
	value, exists, errMsg := stringValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
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
		st.SetPreservingTTL(key, store.Value{Kind: store.KindString, Str: newValue})
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

const maxLongDoubleChars = 5 * 1024

// longDoubleFormat describes the C long double ABI of the pinned Linux
// reference: x87 extended (64-bit significand) on amd64/386 and IEEE
// binary128 on arm64. The host OS does not matter; make diff-test runs the
// reference container for the same GOARCH.
type longDoubleFormat struct {
	prec    uint
	max     *big.Float
	halfMin *big.Float
}

var hostLongDouble = newLongDoubleFormat()

func newLongDoubleFormat() longDoubleFormat {
	prec, maxExp, minSubExp := uint(113), 16384, -16494
	switch runtime.GOARCH {
	case "amd64", "386":
		prec, maxExp, minSubExp = 64, 16384, -16445
	}
	max := new(big.Int).Lsh(big.NewInt(1), uint(maxExp))
	max.Sub(max, new(big.Int).Lsh(big.NewInt(1), uint(maxExp)-prec))
	return longDoubleFormat{
		prec: prec,
		max:  new(big.Float).SetPrec(prec).SetInt(max),
		// At or below half the minimum subnormal, strtold rounds to zero.
		halfMin: new(big.Float).SetPrec(prec).SetMantExp(big.NewFloat(0.5), minSubExp),
	}
}

// parseLongDouble mirrors Redis's string2ld using the host long double ABI.
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
		return new(big.Float).SetPrec(hostLongDouble.prec).SetInf(negative), true
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
	f, _, err := big.ParseFloat(s, base, hostLongDouble.prec, big.ToNearestEven)
	if err != nil {
		return nil, false
	}
	// big.ParseFloat silently underflows huge negative exponents to zero;
	// string2ld reports the same input as out of range.
	if f.Sign() == 0 && hasNonzeroSignificand(s) {
		return nil, false
	}
	if !inLongDoubleRange(f) {
		return nil, false
	}
	return f, true
}

// hasNonzeroSignificand reports whether a numeric literal has a nonzero digit
// before its exponent. It separates a real zero (0e10) from underflow (1e-999).
func hasNonzeroSignificand(s string) bool {
	if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	hex := strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")
	if hex {
		s = s[2:]
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if hex {
			if c == 'p' || c == 'P' {
				return false
			}
			if (c >= '1' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
				return true
			}
			continue
		}
		if c == 'e' || c == 'E' {
			return false
		}
		if c >= '1' && c <= '9' {
			return true
		}
	}
	return false
}

func inLongDoubleRange(f *big.Float) bool {
	if f.Sign() == 0 {
		return true
	}
	abs := new(big.Float).SetPrec(f.Prec()).Abs(f)
	return abs.Cmp(hostLongDouble.max) <= 0 && abs.Cmp(hostLongDouble.halfMin) > 0
}

func longDoubleOverflows(f *big.Float) bool {
	if f.IsInf() {
		return true
	}
	if f.Sign() == 0 {
		return false
	}
	abs := new(big.Float).SetPrec(f.Prec()).Abs(f)
	return abs.Cmp(hostLongDouble.max) > 0
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
	key := string(args[0])
	current := new(big.Float).SetPrec(hostLongDouble.prec)
	value, exists, errMsg := stringValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if exists {
		var ok bool
		current, ok = parseLongDouble(value)
		if !ok {
			out.Error(protocol.NotFloat)
			return
		}
	}
	incr, ok := parseLongDouble(args[1])
	if !ok {
		out.Error(protocol.NotFloat)
		return
	}
	// big.Float.Add panics on opposite infinities; Redis returns an error for
	// any infinite operand before it computes a result.
	if current.IsInf() || incr.IsInf() {
		out.Error(protocol.FloatResult)
		return
	}
	result := new(big.Float).SetPrec(hostLongDouble.prec).Add(current, incr)
	if longDoubleOverflows(result) {
		out.Error(protocol.FloatResult)
		return
	}
	encoded := []byte(formatLongDouble(result))
	if exists {
		st.SetPreservingTTL(key, store.Value{Kind: store.KindString, Str: encoded})
	} else {
		st.Set(key, encoded)
	}
	out.Bulk(encoded)
}

func appendCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	value, exists, errMsg := stringValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	joined, ok := joinAppend(value, args[1], maxStringLen)
	if !ok {
		out.Error(protocol.StringTooLong)
		return
	}
	if exists {
		st.SetPreservingTTL(key, store.Value{Kind: store.KindString, Str: joined})
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
	if total == 0 && old == nil {
		return []byte{}, true
	}
	if cap(old) >= total {
		joined := old[:total]
		copy(joined[len(old):], add)
		return joined, true
	}
	capacity := total
	if doubled := 2 * len(old); doubled > capacity {
		capacity = doubled
	}
	if capacity > limit {
		capacity = limit
	}
	joined := make([]byte, total, capacity)
	copy(joined, old)
	copy(joined[len(old):], add)
	return joined, true
}

func strlenCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	value, ok, errMsg := stringValue(st, string(args[0]))
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	if !ok {
		out.Int(0)
		return
	}
	out.Int(int64(len(value)))
}

func getsetCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	old, _, errMsg := stringValue(st, key)
	if errMsg != "" {
		out.Error(errMsg)
		return
	}
	st.Set(key, args[1])
	out.Bulk(old)
}

func setnxCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	key := string(args[0])
	if st.Exists(key) {
		out.Int(0)
		return
	}
	st.Set(key, args[1])
	out.Int(1)
}
