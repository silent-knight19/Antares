package main

import (
	"testing"

	"github.com/silent-knight19/antares/internal/protocol"
)

// Wants are byte-identical to captures from the pinned redis:7.4.11
// (PROJECT_LOG P1.5).
func TestStopgapHandler(t *testing.T) {
	tests := []struct {
		name string
		req  protocol.Request
		want string
	}{
		{"ping", protocol.Request{[]byte("PING")}, "+PONG\r\n"},
		{"ping message", protocol.Request{[]byte("ping"), []byte("hello")}, "$5\r\nhello\r\n"},
		{"ping arity", protocol.Request{[]byte("PING"), []byte("a"), []byte("b")}, "-ERR wrong number of arguments for 'ping' command\r\n"},
		{"echo", protocol.Request{[]byte("ECHO"), []byte("hi")}, "$2\r\nhi\r\n"},
		{"echo arity", protocol.Request{[]byte("ECHO")}, "-ERR wrong number of arguments for 'echo' command\r\n"},
		{"unknown command", protocol.Request{[]byte("FOO"), []byte("bar"), []byte("baz")}, "-ERR unknown command 'FOO', with args beginning with: 'bar' 'baz' \r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := protocol.GetEncoder()
			defer enc.Release()
			stopgapHandler(tc.req, enc)
			if got := string(enc.Bytes()); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}
