package protocol

import (
	"strconv"
	"sync"
)

// Encoder appends RESP2 replies to an internal byte buffer. Zero value is
// ready to use; GetEncoder returns pooled instances and Release returns them.
type Encoder struct {
	buf []byte
}

var encoderPool = sync.Pool{New: func() any { return new(Encoder) }}

// encoderRetainCap bounds what a released encoder keeps for reuse, so one
// huge reply does not pin 512 MB inside the pool.
const encoderRetainCap = 64 << 10

// GetEncoder returns an empty encoder from the pool.
func GetEncoder() *Encoder { return encoderPool.Get().(*Encoder) }

// Release returns the encoder to the pool for reuse.
func (e *Encoder) Release() {
	if cap(e.buf) > encoderRetainCap {
		e.buf = nil
	} else {
		e.buf = e.buf[:0]
	}
	encoderPool.Put(e)
}

// Bytes returns the encoded bytes so far; valid until the next mutation.
func (e *Encoder) Bytes() []byte { return e.buf }

// Reset drops the encoded bytes, keeping the buffer for reuse.
func (e *Encoder) Reset() { e.buf = e.buf[:0] }

// Simple writes +<s>\r\n. Callers pass fixed strings without CR/LF, as
// Redis's addReplyStatus does.
func (e *Encoder) Simple(s string) {
	if s == "PONG" {
		e.buf = append(e.buf, '+', 'P', 'O', 'N', 'G', '\r', '\n')
		return
	}
	if s == "OK" {
		e.buf = append(e.buf, '+', 'O', 'K', '\r', '\n')
		return
	}
	e.buf = append(e.buf, '+')
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, '\r', '\n')
}

// Error writes -<msg>\r\n, mapping CR and LF to spaces exactly like Redis's
// addReplyErrorFormat, so user-derived bytes (command names, arguments)
// can never corrupt the reply stream.
func (e *Encoder) Error(msg string) {
	e.buf = append(e.buf, '-')
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		if c == '\r' || c == '\n' {
			c = ' '
		}
		e.buf = append(e.buf, c)
	}
	e.buf = append(e.buf, '\r', '\n')
}

// Int writes :<n>\r\n.
func (e *Encoder) Int(n int64) {
	e.buf = append(e.buf, ':')
	e.buf = strconv.AppendInt(e.buf, n, 10)
	e.buf = append(e.buf, '\r', '\n')
}

// Bulk writes $<len>\r\n<bytes>\r\n. A nil slice writes the null bulk
// $-1\r\n, exactly like addReplyNull; an empty non-nil slice writes $0.
func (e *Encoder) Bulk(b []byte) {
	if b == nil {
		e.Null()
		return
	}
	e.buf = append(e.buf, '$')
	e.buf = strconv.AppendInt(e.buf, int64(len(b)), 10)
	e.buf = append(e.buf, '\r', '\n')
	e.buf = append(e.buf, b...)
	e.buf = append(e.buf, '\r', '\n')
}

// Null writes the RESP2 null bulk $-1\r\n.
func (e *Encoder) Null() { e.buf = append(e.buf, '$', '-', '1', '\r', '\n') }

// ArrayHeader writes *<n>\r\n; follow with n element encodings.
func (e *Encoder) ArrayHeader(n int) {
	e.buf = append(e.buf, '*')
	e.buf = strconv.AppendInt(e.buf, int64(n), 10)
	e.buf = append(e.buf, '\r', '\n')
}

// NullArray writes the RESP2 null array *-1\r\n.
func (e *Encoder) NullArray() { e.buf = append(e.buf, '*', '-', '1', '\r', '\n') }
