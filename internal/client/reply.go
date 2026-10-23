package client

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/silent-knight19/antares/internal/protocol"
)

const (
	maxReplyLine = 1 << 20 // status/error/header lines; Redis replies are far smaller
	maxArrayLen  = 1 << 20 // guard against a malicious cyclic-length array
	maxDepth     = 64
)

// Kind is one RESP2 reply type.
type Kind string

const (
	KindSimple    Kind = "simple"
	KindError     Kind = "error"
	KindInteger   Kind = "integer"
	KindBulk      Kind = "bulk"
	KindArray     Kind = "array"
	KindNull      Kind = "null"
	KindNullArray Kind = "null_array"
)

// Reply is a parsed RESP2 reply. Text holds simple, error, and bulk payloads;
// Int holds integers; Array holds nested elements.
type Reply struct {
	Kind  Kind
	Text  string
	Int   int64
	Array []Reply
}

func parseReply(r *bufio.Reader, depth int) (Reply, error) {
	if depth > maxDepth {
		return Reply{}, errors.New("client: reply nested too deeply")
	}
	prefix, err := r.ReadByte()
	if err != nil {
		return Reply{}, fmt.Errorf("client: read reply: %w", err)
	}
	switch prefix {
	case '+':
		line, err := readLine(r)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Kind: KindSimple, Text: line}, nil
	case '-':
		line, err := readLine(r)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Kind: KindError, Text: line}, nil
	case ':':
		line, err := readLine(r)
		if err != nil {
			return Reply{}, err
		}
		n, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return Reply{}, fmt.Errorf("client: invalid integer reply %q", line)
		}
		return Reply{Kind: KindInteger, Int: n}, nil
	case '$':
		return readBulk(r)
	case '*':
		return readArray(r, depth)
	default:
		return Reply{}, fmt.Errorf("client: unsupported reply prefix %q", string(prefix))
	}
}

func readLine(r *bufio.Reader) (string, error) {
	var line []byte
	for {
		frag, err := r.ReadSlice('\n')
		line = append(line, frag...)
		if len(line) > maxReplyLine {
			return "", errors.New("client: reply line too long")
		}
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return "", fmt.Errorf("client: read line: %w", err)
	}
	line = line[:len(line)-1]
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return string(line), nil
}

func readBulk(r *bufio.Reader) (Reply, error) {
	line, err := readLine(r)
	if err != nil {
		return Reply{}, err
	}
	n, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return Reply{}, fmt.Errorf("client: invalid bulk length %q", line)
	}
	if n == -1 {
		return Reply{Kind: KindNull}, nil
	}
	if n < 0 || n > protocol.MaxBulkLen {
		return Reply{}, fmt.Errorf("client: invalid bulk length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Reply{}, fmt.Errorf("client: read bulk: %w", err)
	}
	var trailer [2]byte
	if _, err := io.ReadFull(r, trailer[:]); err != nil {
		return Reply{}, fmt.Errorf("client: read bulk trailer: %w", err)
	}
	if trailer[0] != '\r' || trailer[1] != '\n' {
		return Reply{}, errors.New("client: invalid bulk CRLF trailer")
	}
	return Reply{Kind: KindBulk, Text: string(buf)}, nil
}

func readArray(r *bufio.Reader, depth int) (Reply, error) {
	line, err := readLine(r)
	if err != nil {
		return Reply{}, err
	}
	n, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return Reply{}, fmt.Errorf("client: invalid array length %q", line)
	}
	if n == -1 {
		return Reply{Kind: KindNullArray}, nil
	}
	if n < 0 || n > maxArrayLen {
		return Reply{}, fmt.Errorf("client: invalid array length %d", n)
	}
	items := make([]Reply, 0, n)
	for i := int64(0); i < n; i++ {
		item, err := parseReply(r, depth+1)
		if err != nil {
			return Reply{}, err
		}
		items = append(items, item)
	}
	return Reply{Kind: KindArray, Array: items}, nil
}

// encode renders the reply as canonical RESP2, used for the wire-trace panel.
func (r Reply) encode() []byte {
	e := protocol.GetEncoder()
	defer e.Release()
	r.appendTo(e)
	out := make([]byte, len(e.Bytes()))
	copy(out, e.Bytes())
	return out
}

func (r Reply) appendTo(e *protocol.Encoder) {
	switch r.Kind {
	case KindSimple:
		e.Simple(r.Text)
	case KindError:
		e.Error(r.Text)
	case KindInteger:
		e.Int(r.Int)
	case KindBulk:
		e.Bulk([]byte(r.Text))
	case KindArray:
		e.ArrayHeader(len(r.Array))
		for _, item := range r.Array {
			item.appendTo(e)
		}
	case KindNull:
		e.Null()
	case KindNullArray:
		e.NullArray()
	}
}
