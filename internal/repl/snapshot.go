package repl

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/store"
)

// DelimiterLength is the 40-character hex delimiter length used for Redis diskless
// replication EOF framing (ADR-007, capa eof).
const DelimiterLength = 40

// ErrInvalidSnapshotStream reports a snapshot stream with malformed framing.
var ErrInvalidSnapshotStream = errors.New("replication: invalid snapshot stream framing")

// GenerateDelimiter generates a random 40-character hex delimiter for EOF-framed snapshots.
func GenerateDelimiter() string {
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback deterministic pattern if rand.Read fails
		return strings.Repeat("a", DelimiterLength)
	}
	return hex.EncodeToString(b[:])
}

// WriteSnapshotStream serializes the store snapshot in shared framed format (ADR-007)
// using Redis diskless EOF delimiter framing:
//
//	$EOF:<40-hex-delimiter>\r\n
//	<framed snapshot commands from persist.WriteSnapshot>
//	<40-hex-delimiter>
//
// The delimiter is returned upon successful serialization. If delimiter is empty,
// a random 40-hex delimiter is generated.
func WriteSnapshotStream(w io.Writer, snap *store.Store, delimiter string) (string, error) {
	if delimiter == "" {
		delimiter = GenerateDelimiter()
	}

	bw := bufio.NewWriterSize(w, 64<<10)

	// 1. Write $EOF:<delimiter>\r\n header
	if _, err := fmt.Fprintf(bw, "$EOF:%s\r\n", delimiter); err != nil {
		return "", err
	}

	// 2. Stream framed commands via shared serializer (ADR-007)
	if err := persist.WriteSnapshot(bw, snap); err != nil {
		return "", err
	}

	// 3. Write closing delimiter (no trailing newline per Redis capa eof protocol)
	if _, err := bw.WriteString(delimiter); err != nil {
		return "", err
	}

	if err := bw.Flush(); err != nil {
		return "", err
	}
	return delimiter, nil
}

// WriteSnapshotLengthPrefixed serializes the store snapshot using length-prefixed framing:
//
//	$<length>\r\n
//	<framed snapshot commands>
func WriteSnapshotLengthPrefixed(w io.Writer, snap *store.Store) error {
	var buf bytes.Buffer
	if err := persist.WriteSnapshot(&buf, snap); err != nil {
		return err
	}

	bw := bufio.NewWriterSize(w, 64<<10)
	if _, err := fmt.Fprintf(bw, "$%d\r\n", buf.Len()); err != nil {
		return err
	}
	if _, err := bw.Write(buf.Bytes()); err != nil {
		return err
	}
	return bw.Flush()
}

// ReadSnapshotStream decodes a snapshot stream from r and applies each frame
// payload through apply. It transparently handles both:
//  1. Diskless EOF framing: $EOF:<40-byte-delimiter>\r\n ... <40-byte-delimiter>
//  2. Length-prefixed framing: $<length>\r\n ...
//
// It skips any leading keepalive newlines (\r\n or \n).
func ReadSnapshotStream(r io.Reader, apply func(payload []byte) error) error {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReaderSize(r, 64<<10)
	}

	// Skip any leading keepalive empty lines
	var line string
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading snapshot header: %w", err)
		}
		trimmed := strings.TrimRight(l, "\r\n")
		if trimmed == "" {
			continue // keepalive newline
		}
		line = trimmed
		break
	}

	var payloadReader io.Reader
	var streamSize int64 = -1

	if strings.HasPrefix(line, "$EOF:") {
		delimiter := strings.TrimPrefix(line, "$EOF:")
		if len(delimiter) == 0 {
			return fmt.Errorf("%w: empty EOF delimiter", ErrInvalidSnapshotStream)
		}
		payloadReader = NewDelimiterReader(br, []byte(delimiter))
	} else if strings.HasPrefix(line, "$") {
		lengthStr := strings.TrimPrefix(line, "$")
		length, err := strconv.ParseInt(lengthStr, 10, 64)
		if err != nil || length < 0 {
			return fmt.Errorf("%w: invalid length %q", ErrInvalidSnapshotStream, lengthStr)
		}
		streamSize = length
		payloadReader = io.LimitReader(br, length)
	} else {
		return fmt.Errorf("%w: unexpected header %q", ErrInvalidSnapshotStream, line)
	}

	report, err := persist.RecoverReader(payloadReader, streamSize, apply)
	if err != nil {
		return err
	}
	if report.Corrupt {
		return fmt.Errorf("snapshot stream corrupt: %s", report.Reason)
	}
	return nil
}

// DelimiterReader reads from br until the exact delimiter byte slice is matched.
// When delimiter is encountered, DelimiterReader discards the delimiter from br
// and returns io.EOF, leaving br positioned immediately at the next byte following
// the delimiter.
type DelimiterReader struct {
	br    *bufio.Reader
	delim []byte
	done  bool
}

// NewDelimiterReader creates a DelimiterReader wrapping br.
func NewDelimiterReader(br *bufio.Reader, delim []byte) *DelimiterReader {
	return &DelimiterReader{
		br:    br,
		delim: delim,
	}
}

// Read reads unconsumed bytes up to the delimiter.
func (d *DelimiterReader) Read(p []byte) (int, error) {
	if d.done {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	// Ensure we have at least len(delim) bytes buffered to inspect.
	for d.br.Buffered() < len(d.delim) {
		_, err := d.br.Peek(len(d.delim))
		if err != nil {
			// Underlying reader hit EOF without providing full delimiter
			if d.br.Buffered() > 0 {
				toRead := min(len(p), d.br.Buffered())
				return d.br.Read(p[:toRead])
			}
			return 0, io.ErrUnexpectedEOF
		}
	}

	buffered := d.br.Buffered()
	peek, _ := d.br.Peek(buffered)
	idx := bytes.Index(peek, d.delim)

	if idx == 0 {
		// Exactly at the delimiter!
		_, _ = d.br.Discard(len(d.delim))
		d.done = true
		return 0, io.EOF
	}

	if idx > 0 {
		// Delimiter is present in the buffered window; read up to its start
		toRead := min(len(p), idx)
		return d.br.Read(p[:toRead])
	}

	// Delimiter not in current peek. Suffix of peek could match prefix of delim.
	// We can safely read up to len(peek) - len(delim) + 1 bytes.
	safe := len(peek) - len(d.delim) + 1
	if safe > 0 {
		toRead := min(len(p), safe)
		return d.br.Read(p[:toRead])
	}

	// If safe <= 0, read at least 1 byte
	return d.br.Read(p[:1])
}
