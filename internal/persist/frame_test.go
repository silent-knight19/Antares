package persist

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// splitFrames is an independent frame reader used by tests, so the tests do
// not lean on the same code that writes the frames.
func splitFrames(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var payloads [][]byte
	for len(data) > 0 {
		if len(data) < FrameHeaderSize {
			t.Fatalf("short frame header: %d bytes left", len(data))
		}
		n := int(binary.LittleEndian.Uint32(data[0:4]))
		if n == 0 || n > MaxFramePayload || len(data) < FrameHeaderSize+n {
			t.Fatalf("bad frame length %d with %d bytes left", n, len(data))
		}
		payload := data[FrameHeaderSize : FrameHeaderSize+n]
		if crc32.ChecksumIEEE(payload) != binary.LittleEndian.Uint32(data[4:8]) {
			t.Fatalf("frame checksum mismatch")
		}
		payloads = append(payloads, payload)
		data = data[FrameHeaderSize+n:]
	}
	return payloads
}

func TestEncodeCommand(t *testing.T) {
	got := string(EncodeCommand([]byte("SET"), []byte("k"), []byte("v")))
	want := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n"
	if got != want {
		t.Fatalf("EncodeCommand = %q, want %q", got, want)
	}
	if got := string(EncodeCommand([]byte("PING"))); got != "*1\r\n$4\r\nPING\r\n" {
		t.Fatalf("EncodeCommand PING = %q", got)
	}
	if !bytes.Equal(EncodeCommand([]byte("SET"), []byte("k"), nil), []byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n")) {
		t.Fatalf("EncodeCommand with empty value = %q", EncodeCommand([]byte("SET"), []byte("k"), nil))
	}
}

func TestFrameRoundTrip(t *testing.T) {
	buf := AppendFrame(nil, []byte("one"))
	buf = AppendFrame(buf, []byte("two"))
	frames := splitFrames(t, buf)
	if len(frames) != 2 || string(frames[0]) != "one" || string(frames[1]) != "two" {
		t.Fatalf("frames = %q", frames)
	}
}
