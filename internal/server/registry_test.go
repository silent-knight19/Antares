package server

import (
	"testing"

	"github.com/silent-knight19/antares/internal/protocol"
)

// Wants are byte-identical to captures from the pinned redis:7.4.11
// (PROJECT_LOG P1.5/P2.1).
func TestRegistryDispatch(t *testing.T) {
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
		{"command", protocol.Request{[]byte("COMMAND")}, "*0\r\n"},
		{"command docs", protocol.Request{[]byte("COMMAND"), []byte("DOCS")}, "*0\r\n"},
		{"command info", protocol.Request{[]byte("COMMAND"), []byte("INFO"), []byte("ping")}, "*0\r\n"},
		{"unknown command", protocol.Request{[]byte("FOO"), []byte("bar"), []byte("baz")}, "-ERR unknown command 'FOO', with args beginning with: 'bar' 'baz' \r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := protocol.GetEncoder()
			defer enc.Release()
			NewRegistry().Dispatch(tc.req, enc)
			if got := string(enc.Bytes()); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestRegistryLookupCaseInsensitiveAndFlags(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Command{
		Name:  "SET",
		Arity: 3,
		Flags: FlagWrite,
		Handler: func(args [][]byte, out *protocol.Encoder) {
			out.Simple("OK")
		},
	})

	cmd, ok := reg.Lookup([]byte("sEt"))
	if !ok {
		t.Fatal("lookup set: not found")
	}
	if cmd.Name != "set" || cmd.Arity != 3 || cmd.Flags != FlagWrite {
		t.Fatalf("lookup set = %+v", cmd)
	}
	if _, ok := reg.Lookup([]byte("nosuchcmd")); ok {
		t.Fatal("lookup nosuchcmd: unexpectedly found")
	}
}

func TestRegistryNegativeArity(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Command{
		Name:  "pair",
		Arity: -2,
		Handler: func(args [][]byte, out *protocol.Encoder) {
			out.Int(int64(len(args)))
		},
	})

	for _, tc := range []struct {
		req  protocol.Request
		want string
	}{
		{protocol.Request{[]byte("pair")}, "-ERR wrong number of arguments for 'pair' command\r\n"},
		{protocol.Request{[]byte("pair"), []byte("a")}, ":1\r\n"},
		{protocol.Request{[]byte("pair"), []byte("a"), []byte("b")}, ":2\r\n"},
	} {
		enc := protocol.GetEncoder()
		reg.Dispatch(tc.req, enc)
		if got := string(enc.Bytes()); got != tc.want {
			enc.Release()
			t.Fatalf("%v: got %q, want %q", tc.req, got, tc.want)
		}
		enc.Release()
	}
}
