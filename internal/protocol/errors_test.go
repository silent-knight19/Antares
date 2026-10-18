package protocol

import (
	"bytes"
	"strings"
	"testing"
)

// Every want below is byte-identical to a capture from the digest-pinned
// redis:7.4.11 (PROJECT_LOG P1.5).
func TestErrorBodies(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"protocol error", ProtocolErrorReply("invalid multibulk length"), "ERR Protocol error: invalid multibulk length"},
		{"protocol error big mbulk", ProtocolErrorReply("too big mbulk count string"), "ERR Protocol error: too big mbulk count string"},
		{"protocol error big bulk", ProtocolErrorReply("too big bulk count string"), "ERR Protocol error: too big bulk count string"},
		{"ping arity", WrongArity("ping"), "ERR wrong number of arguments for 'ping' command"},
		{"echo arity", WrongArity("echo"), "ERR wrong number of arguments for 'echo' command"},
		{"unknown bare", UnknownCommand("FOO"), "ERR unknown command 'FOO', with args beginning with: "},
		{"unknown two args", UnknownCommand("FOO", []byte("bar"), []byte("baz")), "ERR unknown command 'FOO', with args beginning with: 'bar' 'baz' "},
		{"unknown empty arg", UnknownCommand("FOO", []byte{}), "ERR unknown command 'FOO', with args beginning with: '' "},
		{"unknown long arg truncated at 128", UnknownCommand("FOO", bytes.Repeat([]byte("a"), 200)), "ERR unknown command 'FOO', with args beginning with: '" + strings.Repeat("a", 128) + "' "},
		{"unknown exact 128-char arg", UnknownCommand("FOO", bytes.Repeat([]byte("x"), 128)), "ERR unknown command 'FOO', with args beginning with: '" + strings.Repeat("x", 128) + "' "},
		{"unknown budget cuts second arg", UnknownCommand("FOO", bytes.Repeat([]byte("x"), 124), []byte("abcdef")), "ERR unknown command 'FOO', with args beginning with: '" + strings.Repeat("x", 124) + "' 'a' "},
		{"unknown long name truncated at 128", UnknownCommand(strings.Repeat("c", 200)), "ERR unknown command '" + strings.Repeat("c", 128) + "', with args beginning with: "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got  %q\nwant %q", tc.got, tc.want)
			}
		})
	}
}

// Redis sanitizes CR/LF to spaces in formatted error replies; the captures
// for an argument containing CRLF and for the empty-bulk-header protocol
// error ("got '<CR>'") both show the space substitution.
func TestErrorReplySanitizesCRLF(t *testing.T) {
	e := GetEncoder()
	defer e.Release()

	e.Error(UnknownCommand("FOO", []byte("a\r\nb")))
	if got, want := string(e.Bytes()), "-ERR unknown command 'FOO', with args beginning with: 'a  b' \r\n"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}

	e.Reset()
	e.Error(ProtocolErrorReply("expected '$', got '\r'"))
	if got, want := string(e.Bytes()), "-ERR Protocol error: expected '$', got ' '\r\n"; got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
