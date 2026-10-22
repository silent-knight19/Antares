package repl

import (
	"bufio"
	"io"
)

// TrackedReader wraps an io.Reader and a bufio.Reader, accurately tracking
// the exact number of bytes consumed by the consumer from the buffer.
type TrackedReader struct {
	cr *countingReader
	br *bufio.Reader
}

type countingReader struct {
	r     io.Reader
	total int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.total += int64(n)
	return n, err
}

// NewTrackedReader creates a TrackedReader wrapping r.
func NewTrackedReader(r io.Reader, bufSize int) *TrackedReader {
	if bufSize <= 0 {
		bufSize = 64 << 10
	}
	cr := &countingReader{r: r}
	br := bufio.NewReaderSize(cr, bufSize)
	return &TrackedReader{
		cr: cr,
		br: br,
	}
}

// Reader returns the *bufio.Reader for parsing.
func (t *TrackedReader) Reader() *bufio.Reader {
	return t.br
}

// Consumed returns the exact cumulative count of bytes consumed by the caller.
// It accounts for buffered lookahead: total bytes read from underlying reader
// minus unread bytes still sitting in bufio's buffer.
func (t *TrackedReader) Consumed() int64 {
	return t.cr.total - int64(t.br.Buffered())
}
