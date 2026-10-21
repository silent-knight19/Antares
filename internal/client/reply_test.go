package client

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func parse(t *testing.T, wire string) Reply {
	t.Helper()
	r, err := parseReply(bufio.NewReader(strings.NewReader(wire)), 0)
	if err != nil {
		t.Fatalf("parseReply(%q): %v", wire, err)
	}
	return r
}

func TestParseReplyTypes(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want Reply
	}{
		{"simple", "+OK\r\n", Reply{Kind: KindSimple, Text: "OK"}},
		{"error", "-ERR no such key\r\n", Reply{Kind: KindError, Text: "ERR no such key"}},
		{"integer", ":-42\r\n", Reply{Kind: KindInteger, Int: -42}},
		{"bulk", "$5\r\nAlice\r\n", Reply{Kind: KindBulk, Text: "Alice"}},
		{"empty bulk", "$0\r\n\r\n", Reply{Kind: KindBulk, Text: ""}},
		{"binary bulk", "$4\r\n\x00\r\n\xff\r\n", Reply{Kind: KindBulk, Text: "\x00\r\n\xff"}},
		{"null bulk", "$-1\r\n", Reply{Kind: KindNull}},
		{"null array", "*-1\r\n", Reply{Kind: KindNullArray}},
		{"empty array", "*0\r\n", Reply{Kind: KindArray, Array: []Reply{}}},
		{"array", "*3\r\n$1\r\na\r\n$-1\r\n:2\r\n", Reply{Kind: KindArray, Array: []Reply{
			{Kind: KindBulk, Text: "a"},
			{Kind: KindNull},
			{Kind: KindInteger, Int: 2},
		}}},
		{"nested array", "*1\r\n*2\r\n+a\r\n:1\r\n", Reply{Kind: KindArray, Array: []Reply{
			{Kind: KindArray, Array: []Reply{
				{Kind: KindSimple, Text: "a"},
				{Kind: KindInteger, Int: 1},
			}},
		}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parse(t, tc.wire)
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text || got.Int != tc.want.Int || len(got.Array) != len(tc.want.Array) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if len(tc.want.Array) > 0 {
				for i := range tc.want.Array {
					if got.Array[i].Kind != tc.want.Array[i].Kind {
						t.Fatalf("array[%d] = %+v, want %+v", i, got.Array[i], tc.want.Array[i])
					}
				}
			}
		})
	}
}

func TestReplyEncodeRoundTrip(t *testing.T) {
	inputs := []string{
		"+OK\r\n",
		"-ERR bad\r\n",
		":7\r\n",
		"$5\r\nAlice\r\n",
		"$-1\r\n",
		"*-1\r\n",
		"*2\r\n$1\r\na\r\n*1\r\n:1\r\n",
	}
	for _, wire := range inputs {
		reply := parse(t, wire)
		got := reply.encode()
		if !bytes.Equal(got, []byte(wire)) {
			t.Fatalf("encode(%q) = %q", wire, got)
		}
	}
}

func TestParseReplyRejectsMalformed(t *testing.T) {
	inputs := []string{
		"!nope\r\n",
		"$x\r\n",
		"$-2\r\n",
		"*x\r\n",
		"*-2\r\n",
		":nope\r\n",
		"$3\r\nab", // truncated payload
	}
	for _, wire := range inputs {
		if _, err := parseReply(bufio.NewReader(strings.NewReader(wire)), 0); err == nil {
			t.Fatalf("parseReply(%q) succeeded, want error", wire)
		}
	}
}

func TestParseReplyDepthLimit(t *testing.T) {
	wire := strings.Repeat("*1\r\n", maxDepth+2) + ":1\r\n"
	if _, err := parseReply(bufio.NewReader(strings.NewReader(wire)), 0); err == nil {
		t.Fatal("deeply nested reply succeeded, want error")
	}
}
