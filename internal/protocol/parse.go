package protocol

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

const (
	inlineMaxSize   = 64 * 1024         // PROTO_INLINE_MAX_SIZE
	maxMultibulkLen = 1<<31 - 1         // Redis rejects multibulk counts past INT_MAX
	MaxBulkLen      = 512 * 1024 * 1024 // proto-max-bulk-len default
	bulkReadCap     = 64 * 1024         // chunk size/growth start for payloads above this

	// maxRequestBytes mirrors Redis's client-query-buffer-limit default.
	// requestArgOverhead accounts for the slice/argument bookkeeping that
	// survives after the payload is read.
	MaxRequestBytes    = 1 << 30
	requestArgOverhead = 32
)

var errRequestTooLarge = errors.New("request too large")

// Request is one client command: the verb followed by binary-safe arguments.
// Pipelined requests are consumed one ReadRequest call each.
type Request [][]byte

// ProtocolError reports malformed client input. Msg holds the Redis wording
// after the "Protocol error: " prefix (e.g. "invalid multibulk length");
// P1.5 turns it into the exact wire reply.
type ProtocolError struct{ Msg string }

func (e *ProtocolError) Error() string { return "protocol error: " + e.Msg }

// ReadRequest decodes one request from br: a RESP array of bulk strings or an
// inline command. Incomplete input blocks until more bytes arrive; a closed
// stream surfaces io.EOF (clean boundary) or io.ErrUnexpectedEOF (mid-request).
//
// Behavior mirrors redis 7.4.11, including the quirks parse_test.go pins from
// live captures: zero/negative multibulk counts are skipped, the byte after a
// count line's '\r' is skipped blindly (never checked to be '\n'), bulk
// payload trailers are not validated, and inline needs only '\n' (bare LF).
func ReadRequest(br *bufio.Reader) (Request, error) {
	for {
		first, err := br.Peek(1)
		if err != nil {
			return nil, err
		}
		var req Request
		var skip bool
		if first[0] == '*' {
			req, skip, err = readMultibulk(br)
		} else {
			req, skip, err = readInline(br)
		}
		if err != nil {
			return nil, err
		}
		if !skip {
			return req, nil
		}
	}
}

// readMultibulk parses "*<count>\r\n" plus count bulk strings. skip mirrors
// Redis: count <= 0 consumes the header and nothing else.
func readMultibulk(br *bufio.Reader) (Request, bool, error) {
	return readMultibulkLimit(br, MaxRequestBytes)
}

func readMultibulkLimit(br *bufio.Reader, budget int64) (Request, bool, error) {
	if _, err := br.ReadByte(); err != nil {
		return nil, false, unexpectedEOF(err)
	}
	var scratch [32]byte
	line, err := readCountLine(br, scratch[:], "too big mbulk count string")
	if err != nil {
		return nil, false, err
	}
	n, ok := parseCount(line)
	if !ok || n > maxMultibulkLen {
		return nil, false, &ProtocolError{Msg: "invalid multibulk length"}
	}
	if n <= 0 {
		return nil, true, nil
	}
	capHint := n
	if capHint > 1024 {
		capHint = 1024 // grow on demand; INT_MAX would preallocate a 16 GiB slice
	}
	req := make(Request, 0, capHint)
	for i := int64(0); i < n; i++ {
		if budget <= requestArgOverhead {
			return nil, false, errRequestTooLarge
		}
		arg, err := readBulk(br, budget-requestArgOverhead)
		if err != nil {
			return nil, false, err
		}
		budget -= int64(len(arg)) + requestArgOverhead
		req = append(req, arg)
	}
	return req, false, nil
}

func readBulk(br *bufio.Reader, budget int64) ([]byte, error) {
	var scratch [32]byte
	line, err := readCountLine(br, scratch[:], "too big bulk count string")
	if err != nil {
		return nil, err
	}
	got := byte('\r') // empty header line: Redis reports the bare '\r' it matched
	if len(line) > 0 {
		got = line[0]
	}
	if got != '$' {
		// The raw byte goes into the reply, as Redis does: %c would encode
		// bytes >= 0x80 as UTF-8, which would not match the capture.
		return nil, &ProtocolError{Msg: "expected '$', got '" + string([]byte{got}) + "'"}
	}
	n, ok := parseCount(line[1:])
	if !ok || n < 0 || n > MaxBulkLen {
		return nil, &ProtocolError{Msg: "invalid bulk length"}
	}
	if n > budget {
		return nil, errRequestTooLarge
	}
	need := n + 2
	if need <= bulkReadCap {
		buf := make([]byte, need)
		if _, err := io.ReadFull(br, buf); err != nil {
			return nil, unexpectedEOF(err)
		}
		return buf[:n:n], nil // trailer bytes discarded unvalidated, as Redis does
	}
	// Large payloads grow only as bytes actually arrive, so a declared-but-
	// unsent length (up to 512 MB) cannot force the allocation upfront (Redis
	// sizes its structures from received bytes too). Growth doubles but is
	// clamped to the declared size, so no capacity beyond it is ever held.
	buf := make([]byte, bulkReadCap)
	filled := int64(0)
	for filled < need {
		if filled == int64(len(buf)) {
			grown := make([]byte, min(2*int64(len(buf)), need))
			copy(grown, buf)
			buf = grown
		}
		m, err := br.Read(buf[filled:])
		filled += int64(m)
		if err != nil {
			return nil, unexpectedEOF(err)
		}
	}
	return buf[:n:n], nil // trailer bytes discarded unvalidated, as Redis does
}

// readInline parses one inline command line. skip reports an empty or
// blank-only line: Redis consumes it and keeps reading.
func readInline(br *bufio.Reader) (Request, bool, error) {
	line, err := readLine(br, '\n', "too big inline request")
	if err != nil {
		return nil, false, err
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	args, ok := splitInline(line)
	if !ok {
		return nil, false, &ProtocolError{Msg: "unbalanced quotes in request"}
	}
	if len(args) == 0 {
		return nil, true, nil
	}
	return args, false, nil
}

func readLine(br *bufio.Reader, delim byte, tooBigMsg string) ([]byte, error) {
	slice, err := br.ReadSlice(delim)
	if err == nil {
		line := slice[:len(slice)-1]
		if len(line) > inlineMaxSize {
			return nil, &ProtocolError{Msg: tooBigMsg}
		}
		res := make([]byte, len(line))
		copy(res, line)
		return res, nil
	}
	if err != bufio.ErrBufferFull {
		return nil, unexpectedEOF(err)
	}
	line := append([]byte(nil), slice...)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, unexpectedEOF(err)
		}
		if b == delim {
			return line, nil
		}
		line = append(line, b)
		if len(line) > inlineMaxSize {
			return nil, &ProtocolError{Msg: tooBigMsg}
		}
	}
}

// readCountLine reads a multibulk or bulk header line, then skips the byte
// after its terminating '\r' blindly — redis 7.4 never checks that it is
// '\n' (captured: "*1\rX$4\r\nPING\r\n" answers +PONG).
func readCountLine(br *bufio.Reader, scratch []byte, tooBigMsg string) ([]byte, error) {
	slice, err := br.ReadSlice('\r')
	if err == nil {
		line := slice[:len(slice)-1]
		if len(line) > inlineMaxSize {
			return nil, &ProtocolError{Msg: tooBigMsg}
		}
		var res []byte
		if len(line) <= len(scratch) {
			copy(scratch, line)
			res = scratch[:len(line)]
		} else {
			res = make([]byte, len(line))
			copy(res, line)
		}
		if _, err := br.ReadByte(); err != nil {
			return nil, unexpectedEOF(err)
		}
		return res, nil
	}
	if err != bufio.ErrBufferFull {
		return nil, unexpectedEOF(err)
	}
	line := append([]byte(nil), slice...)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, unexpectedEOF(err)
		}
		if b == '\r' {
			break
		}
		line = append(line, b)
		if len(line) > inlineMaxSize {
			return nil, &ProtocolError{Msg: tooBigMsg}
		}
	}
	if _, err := br.ReadByte(); err != nil {
		return nil, unexpectedEOF(err)
	}
	return line, nil
}

// parseCount is string2ll from Redis util.c: optional '-', no leading zeroes,
// no '+', digits only, int64 range. Anything else is invalid.
func parseCount(b []byte) (int64, bool) {
	if len(b) == 0 || len(b) > 20 {
		return 0, false
	}
	i := 0
	neg := false
	if b[0] == '-' {
		neg = true
		i = 1
		if i == len(b) {
			return 0, false
		}
	} else if b[0] == '0' {
		return 0, len(b) == 1
	}
	if b[i] < '1' || b[i] > '9' {
		return 0, false
	}
	var v uint64
	for ; i < len(b); i++ {
		d := b[i]
		if d < '0' || d > '9' {
			return 0, false
		}
		if v > (^uint64(0)-uint64(d-'0'))/10 {
			return 0, false
		}
		v = v*10 + uint64(d-'0')
	}
	if neg {
		if v > uint64(1)<<63 {
			return 0, false
		}
		return -int64(v), true // v == 1<<63 wraps to MinInt64, matching string2ll
	}
	if v > uint64(1)<<63-1 {
		return 0, false
	}
	return int64(v), true
}

// ParseInt decodes a Redis string2ll integer: no sign other than a leading
// '-', no leading zeros, no spaces, no suffixes.
func ParseInt(b []byte) (int64, bool) { return parseCount(b) }

func unexpectedEOF(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

// splitInline is sdssplitargs from Redis sds.c: blanks collapse, double
// quotes decode \xHH \n \r \t \b \a \\ \" and reject a close quote followed
// by a non-blank, single quotes keep everything literal except \' with the
// same close-quote rule, and a closing quote must end the argument. ok=false
// is Redis's NULL return → "unbalanced quotes in request".
func splitInline(line []byte) ([][]byte, bool) {
	// sdssplitargs walks a NUL-terminated string: bytes after a NUL are invisible.
	for i, b := range line {
		if b == 0 {
			line = line[:i]
			break
		}
	}
	if bytes.IndexAny(line, "\"'\\") == -1 {
		var args [][]byte
		p := 0
		for {
			for p < len(line) && isBlank(line[p]) {
				p++
			}
			if p == len(line) {
				return args, true
			}
			start := p
			for p < len(line) && !isBlank(line[p]) {
				p++
			}
			args = append(args, line[start:p])
		}
	}
	var args [][]byte
	p := 0
	for {
		for p < len(line) && isBlank(line[p]) {
			p++
		}
		if p == len(line) {
			return args, true
		}
		var cur []byte
		inq, insq, done := false, false, false
		for !done {
			var c byte
			if p < len(line) {
				c = line[p]
			}
			switch {
			case inq && c == '\\' && p+3 < len(line) && line[p+1] == 'x' && isHex(line[p+2]) && isHex(line[p+3]):
				cur = append(cur, hexVal(line[p+2])<<4|hexVal(line[p+3]))
				p += 3
			case inq && c == '\\' && p+1 < len(line):
				p++
				var e byte
				switch line[p] {
				case 'n':
					e = '\n'
				case 'r':
					e = '\r'
				case 't':
					e = '\t'
				case 'b':
					e = '\b'
				case 'a':
					e = '\a'
				default:
					e = line[p]
				}
				cur = append(cur, e)
			case inq && c == '"':
				if p+1 < len(line) && !isBlank(line[p+1]) {
					return nil, false
				}
				done = true
			case inq && p >= len(line):
				return nil, false
			case inq:
				cur = append(cur, c)
			case insq && c == '\\' && p+1 < len(line) && line[p+1] == '\'':
				p++
				cur = append(cur, '\'')
			case insq && c == '\'':
				if p+1 < len(line) && !isBlank(line[p+1]) {
					return nil, false
				}
				done = true
			case insq && p >= len(line):
				return nil, false
			case insq:
				cur = append(cur, c)
			case c == ' ' || c == '\n' || c == '\r' || c == '\t' || p >= len(line):
				done = true
			case c == '"':
				inq = true
			case c == '\'':
				insq = true
			default:
				cur = append(cur, c)
			}
			if p < len(line) {
				p++
			}
		}
		if cur == nil {
			cur = []byte{} // arguments are never nil (Redis argv entries always exist)
		}
		args = append(args, cur)
	}
}

func isBlank(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

func isHex(b byte) bool { return hexVal(b) != 0xff }

func hexVal(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	}
	return 0xff
}
