package server

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func scanCommand(st *store.Store, args [][]byte, out *protocol.Encoder) {
	cursor, ok := parseScanCursor(args[0])
	if !ok {
		out.Error("ERR invalid cursor")
		return
	}

	count := 10
	var match func([]byte) bool
	var kind store.Kind
	filterKind := false
	for i := 1; i < len(args); {
		switch strings.ToUpper(string(args[i])) {
		case "COUNT":
			if i+1 >= len(args) {
				out.Error(protocol.SyntaxError)
				return
			}
			n, ok := protocol.ParseInt(args[i+1])
			if !ok {
				out.Error(protocol.NotInteger)
				return
			}
			if n < 1 {
				out.Error(protocol.SyntaxError)
				return
			}
			count = int(n)
			i += 2
		case "MATCH":
			if i+1 >= len(args) {
				out.Error(protocol.SyntaxError)
				return
			}
			pattern := args[i+1]
			if len(pattern) == 1 && pattern[0] == '*' {
				match = nil
			} else {
				match = func(key []byte) bool { return globMatch(pattern, key) }
			}
			i += 2
		case "TYPE":
			if i+1 >= len(args) {
				out.Error(protocol.SyntaxError)
				return
			}
			filterKind = true
			kind = store.Kind(strings.ToLower(string(args[i+1])))
			i += 2
		default:
			out.Error(protocol.SyntaxError)
			return
		}
	}

	next, keys := st.Scan(cursor, count, match, kind, filterKind)
	out.ArrayHeader(2)
	out.Bulk([]byte(strconv.FormatUint(next, 10)))
	out.ArrayHeader(len(keys))
	for _, key := range keys {
		out.Bulk([]byte(key))
	}
}

// parseScanCursor mirrors Redis's string2ull: string2ll first (so ordinary
// negative cursors are rejected), then strtoull for forms it allows, such as
// leading zeroes, '+', or leading whitespace. Embedded NULs truncate because
// Redis passes a C string to strlen.
func parseScanCursor(b []byte) (uint64, bool) {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	if n, ok := protocol.ParseInt(b); ok {
		if n < 0 {
			return 0, false
		}
		return uint64(n), true
	}
	return parseStrtoull(b)
}

func parseStrtoull(b []byte) (uint64, bool) {
	i := 0
	for i < len(b) && isCSpace(b[i]) {
		i++
	}
	if i == len(b) {
		return 0, false
	}
	neg := false
	if b[i] == '+' || b[i] == '-' {
		neg = b[i] == '-'
		i++
	}
	start := i
	var v uint64
	for ; i < len(b); i++ {
		c := b[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		d := uint64(c - '0')
		if v > (^uint64(0)-d)/10 {
			return 0, false
		}
		v = v*10 + d
	}
	if i == start {
		return 0, false
	}
	if neg {
		v = -v
	}
	return v, true
}

func isCSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}
