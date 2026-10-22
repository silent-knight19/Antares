package repl

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func TestDelimiterReader(t *testing.T) {
	delim := []byte("0123456789abcdef0123456789abcdef01234567")
	payload := []byte("hello world! this is a framed snapshot payload.")
	trail := []byte("*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$1\r\nv\r\n")

	var stream bytes.Buffer
	stream.Write(payload)
	stream.Write(delim)
	stream.Write(trail)

	br := bufio.NewReader(&stream)
	dr := NewDelimiterReader(br, delim)

	readPayload, err := io.ReadAll(dr)
	if err != nil {
		t.Fatalf("io.ReadAll(dr): %v", err)
	}
	if !bytes.Equal(readPayload, payload) {
		t.Fatalf("payload = %q, want %q", readPayload, payload)
	}

	// Verify br is positioned exactly at trail
	readTrail, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("io.ReadAll(br) trail: %v", err)
	}
	if !bytes.Equal(readTrail, trail) {
		t.Fatalf("trail = %q, want %q", readTrail, trail)
	}
}

func TestDelimiterReaderEmptyPayload(t *testing.T) {
	delim := []byte("0123456789abcdef0123456789abcdef01234567")
	trail := []byte("next-commands")

	var stream bytes.Buffer
	stream.Write(delim)
	stream.Write(trail)

	br := bufio.NewReader(&stream)
	dr := NewDelimiterReader(br, delim)

	readPayload, err := io.ReadAll(dr)
	if err != nil {
		t.Fatalf("io.ReadAll(dr): %v", err)
	}
	if len(readPayload) != 0 {
		t.Fatalf("expected empty payload, got %q", readPayload)
	}

	readTrail, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("io.ReadAll(br): %v", err)
	}
	if !bytes.Equal(readTrail, trail) {
		t.Fatalf("trail = %q, want %q", readTrail, trail)
	}
}

func TestSnapshotStreamRoundtripEOF(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	st := store.NewWithClock(clk)
	st.Set("foo", []byte("bar"))
	st.SetWithTTL("expiring", []byte("baz"), 2_000_000)
	st.SetPreservingTTL("list", store.Value{Kind: store.KindList, List: [][]byte{[]byte("item1"), []byte("item2")}})
	st.SetPreservingTTL("hash", store.Value{Kind: store.KindHash, Hash: []store.HashEntry{{Field: []byte("k"), Value: []byte("v")}}})
	st.SetPreservingTTL("set", store.Value{Kind: store.KindSet, Set: [][]byte{[]byte("m1"), []byte("m2")}})

	var stream bytes.Buffer
	// Add some keepalive newlines ahead of snapshot
	stream.WriteString("\r\n\r\n\n")

	delim, err := WriteSnapshotStream(&stream, st, "")
	if err != nil {
		t.Fatalf("WriteSnapshotStream: %v", err)
	}
	if len(delim) != DelimiterLength {
		t.Fatalf("delimiter len = %d, want %d", len(delim), DelimiterLength)
	}

	// Add trailing replication command
	trailingCmd := []byte("*3\r\n$3\r\nSET\r\n$4\r\nnext\r\n$3\r\nval\r\n")
	stream.Write(trailingCmd)

	br := bufio.NewReader(&stream)
	var replayedCommands []protocol.Request
	err = ReadSnapshotStream(br, func(payload []byte) error {
		pbr := bufio.NewReader(bytes.NewReader(payload))
		req, pErr := protocol.ReadRequest(pbr)
		if pErr != nil {
			return pErr
		}
		replayedCommands = append(replayedCommands, req)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshotStream: %v", err)
	}

	// Check replayed commands count (> 0)
	if len(replayedCommands) == 0 {
		t.Fatal("expected replayed commands, got 0")
	}

	// Check that the trailing command is immediately readable from br
	trailReq, err := protocol.ReadRequest(br)
	if err != nil {
		t.Fatalf("reading trailing command: %v", err)
	}
	if string(trailReq[0]) != "SET" || string(trailReq[1]) != "next" || string(trailReq[2]) != "val" {
		t.Fatalf("unexpected trailing command: %v", trailReq)
	}
}

func TestSnapshotStreamRoundtripLengthPrefixed(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	st := store.NewWithClock(clk)
	st.Set("hello", []byte("world"))

	var stream bytes.Buffer
	if err := WriteSnapshotLengthPrefixed(&stream, st); err != nil {
		t.Fatalf("WriteSnapshotLengthPrefixed: %v", err)
	}

	var count int
	br := bufio.NewReader(&stream)
	err := ReadSnapshotStream(br, func(payload []byte) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadSnapshotStream: %v", err)
	}
	if count != 1 {
		t.Fatalf("replayed count = %d, want 1", count)
	}
}

func TestSnapshotStreamCorruptHeader(t *testing.T) {
	invalidHeaders := []string{
		"NOT_A_SNAPSHOT\r\n",
		"$EOF:\r\n",
		"$-5\r\n",
		"$invalid\r\n",
	}

	for _, h := range invalidHeaders {
		br := bufio.NewReader(strings.NewReader(h))
		err := ReadSnapshotStream(br, func([]byte) error { return nil })
		if err == nil || !errors.Is(err, ErrInvalidSnapshotStream) {
			t.Fatalf("header %q expected ErrInvalidSnapshotStream, got %v", h, err)
		}
	}
}
