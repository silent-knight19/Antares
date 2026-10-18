package protocol

import (
	"bufio"
	"bytes"
	"testing"
)

func TestEncoder(t *testing.T) {
	tests := []struct {
		name   string
		encode func(*Encoder)
		want   string
	}{
		{"simple string", func(e *Encoder) { e.Simple("OK") }, "+OK\r\n"},
		{"pong", func(e *Encoder) { e.Simple("PONG") }, "+PONG\r\n"},
		{"error", func(e *Encoder) { e.Error("ERR unknown command 'foo'") }, "-ERR unknown command 'foo'\r\n"},
		{"protocol error", func(e *Encoder) { e.Error("ERR Protocol error: invalid multibulk length") }, "-ERR Protocol error: invalid multibulk length\r\n"},
		{"zero int", func(e *Encoder) { e.Int(0) }, ":0\r\n"},
		{"negative int", func(e *Encoder) { e.Int(-5) }, ":-5\r\n"},
		{"max int64", func(e *Encoder) { e.Int(1<<63 - 1) }, ":9223372036854775807\r\n"},
		{"min int64", func(e *Encoder) { e.Int(-1 << 63) }, ":-9223372036854775808\r\n"},
		{"bulk", func(e *Encoder) { e.Bulk([]byte("hey")) }, "$3\r\nhey\r\n"},
		{"empty bulk", func(e *Encoder) { e.Bulk([]byte{}) }, "$0\r\n\r\n"},
		{"nil bulk is null", func(e *Encoder) { e.Bulk(nil) }, "$-1\r\n"},
		{"null", func(e *Encoder) { e.Null() }, "$-1\r\n"},
		{"null array", func(e *Encoder) { e.NullArray() }, "*-1\r\n"},
		{"empty array", func(e *Encoder) { e.ArrayHeader(0) }, "*0\r\n"},
		{"binary bulk", func(e *Encoder) { e.Bulk([]byte{0, '\r', '\n', 0xff}) }, "$4\r\n\x00\r\n\xff\r\n"},
		{"array of bulk and null", func(e *Encoder) {
			e.ArrayHeader(2)
			e.Bulk([]byte("foo"))
			e.Null()
		}, "*2\r\n$3\r\nfoo\r\n$-1\r\n"},
		{"nested array", func(e *Encoder) {
			e.ArrayHeader(2)
			e.Int(1)
			e.ArrayHeader(1)
			e.Simple("OK")
		}, "*2\r\n:1\r\n*1\r\n+OK\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := GetEncoder()
			defer e.Release()
			tc.encode(e)
			if got := string(e.Bytes()); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Multibulk requests are the reply encoder's array + bulk slice; reading
// them back through ReadRequest closes the loop with the parser.
func TestEncoderRequestRoundTrip(t *testing.T) {
	e := GetEncoder()
	e.ArrayHeader(3)
	e.Bulk([]byte("SET"))
	e.Bulk([]byte("k"))
	e.Bulk([]byte("v"))
	want := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"
	if got := string(e.Bytes()); got != want {
		t.Fatalf("encoded %q, want %q", got, want)
	}
	req, err := ReadRequest(bufio.NewReader(bytes.NewReader(e.Bytes())))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(req) != 3 || string(req[0]) != "SET" || string(req[1]) != "k" || string(req[2]) != "v" {
		t.Fatalf("read back %q, want SET k v", req)
	}

	e.Reset()
	e.ArrayHeader(2)
	e.Bulk([]byte("ECHO"))
	e.Bulk([]byte{})
	req, err = ReadRequest(bufio.NewReader(bytes.NewReader(e.Bytes())))
	if err != nil {
		t.Fatalf("read back empty bulk: %v", err)
	}
	if len(req) != 2 || string(req[0]) != "ECHO" || len(req[1]) != 0 {
		t.Fatalf("read back %q, want ECHO + empty arg", req)
	}
	e.Release()
}

func TestEncoderPipelinedRoundTrip(t *testing.T) {
	e := GetEncoder()
	defer e.Release()
	e.ArrayHeader(1)
	e.Bulk([]byte("PING"))
	e.ArrayHeader(2)
	e.Bulk([]byte("ECHO"))
	e.Bulk([]byte("hi"))
	br := bufio.NewReader(bytes.NewReader(e.Bytes()))
	first, err := ReadRequest(br)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := ReadRequest(br)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(first[0]) != "PING" || string(second[0]) != "ECHO" || string(second[1]) != "hi" {
		t.Fatalf("got %q %q", first, second)
	}
}

func TestEncoderPoolReuse(t *testing.T) {
	e := GetEncoder()
	e.Simple("OK")
	if got := string(e.Bytes()); got != "+OK\r\n" {
		t.Fatalf("got %q", got)
	}
	e.Release()

	e2 := GetEncoder()
	if len(e2.Bytes()) != 0 {
		t.Fatalf("released encoder not reset: %q", e2.Bytes())
	}
	e2.Bulk(make([]byte, 128<<10)) // bigger than the retention cap
	e2.Release()

	e3 := GetEncoder()
	if len(e3.Bytes()) != 0 {
		t.Fatalf("reused encoder not empty: %q", e3.Bytes())
	}
	e3.Release()
}
