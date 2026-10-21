package server

import (
	"strings"
	"testing"
)

const helloReply = "*14\r\n$6\r\nserver\r\n$5\r\nredis\r\n$7\r\nversion\r\n$6\r\n7.4.11\r\n$5\r\nproto\r\n:2\r\n$2\r\nid\r\n:1\r\n$4\r\nmode\r\n$10\r\nstandalone\r\n$4\r\nrole\r\n$6\r\nmaster\r\n$7\r\nmodules\r\n*0\r\n"

func TestHello(t *testing.T) {
	exec := newStringExec(t)
	for _, args := range [][]string{
		{"HELLO"},
		{"HELLO", "2"},
		{"HELLO", "2", "SETNAME", "worker"},
	} {
		if got := runStrings(exec, args...); got != helloReply {
			t.Fatalf("%v = %q, want %q", args, got, helloReply)
		}
	}

	for _, tc := range []struct{ args, want string }{
		{"HELLO 3", "-NOPROTO unsupported protocol version\r\n"},
		{"HELLO 4", "-NOPROTO unsupported protocol version\r\n"},
		{"HELLO -1", "-NOPROTO unsupported protocol version\r\n"},
		{"HELLO abc", "-ERR Protocol version is not an integer or out of range\r\n"},
		{"HELLO 02", "-ERR Protocol version is not an integer or out of range\r\n"},
		{"HELLO +2", "-ERR Protocol version is not an integer or out of range\r\n"},
		{"HELLO 2 SETNAME", "-ERR Syntax error in HELLO option 'SETNAME'\r\n"},
		{"HELLO 2 SETNAME bad name", "-ERR Client names cannot contain spaces, newlines or special characters.\r\n"},
		{"HELLO 2 AUTH user pass", "-WRONGPASS invalid username-password pair or user is disabled.\r\n"},
		{"HELLO 2 BOGUS", "-ERR Syntax error in HELLO option 'BOGUS'\r\n"},
	} {
		args := strings.Split(tc.args, " ")
		if tc.args == "HELLO 2 SETNAME bad name" {
			args = []string{"HELLO", "2", "SETNAME", "bad name"}
		}
		if got := runStrings(exec, args...); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestClientStubs(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct{ args, want string }{
		{"CLIENT ID", ":1\r\n"},
		{"CLIENT GETNAME", "$-1\r\n"},
		{"CLIENT GETREDIR", ":-1\r\n"},
		{"CLIENT SETNAME worker", "+OK\r\n"},
		{"CLIENT SETNAME", "-ERR wrong number of arguments for 'client|setname' command\r\n"},
		{"CLIENT SETNAME bad name", "-ERR Client names cannot contain spaces, newlines or special characters.\r\n"},
		{"CLIENT SETINFO lib-name go-redis", "+OK\r\n"},
		{"CLIENT SETINFO LIB-VER 9.0.0", "+OK\r\n"},
		{"CLIENT SETINFO lib-name", "-ERR wrong number of arguments for 'client|setinfo' command\r\n"},
		{"CLIENT SETINFO bogus x", "-ERR Unrecognized option 'bogus'\r\n"},
		{"CLIENT SETINFO lib-name bad name", "-ERR lib-name cannot contain spaces, newlines or special characters.\r\n"},
		{"CLIENT NO-EVICT ON", "+OK\r\n"},
		{"CLIENT NO-EVICT off", "+OK\r\n"},
		{"CLIENT NO-EVICT maybe", "-ERR syntax error\r\n"},
		{"CLIENT NO-TOUCH ON", "+OK\r\n"},
		{"CLIENT UNPAUSE", "+OK\r\n"},
		{"CLIENT FOO", "-ERR unknown subcommand 'FOO'. Try CLIENT HELP.\r\n"},
		{"CLIENT INFO x", "-ERR wrong number of arguments for 'client|info' command\r\n"},
	} {
		args := strings.Split(tc.args, " ")
		if tc.args == "CLIENT SETNAME bad name" {
			args = []string{"CLIENT", "SETNAME", "bad name"}
		}
		if tc.args == "CLIENT SETINFO lib-name bad name" {
			args = []string{"CLIENT", "SETINFO", "lib-name", "bad name"}
		}
		if got := runStrings(exec, args...); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.args, got, tc.want)
		}
	}

	info := runStrings(exec, "CLIENT", "INFO")
	if !strings.HasPrefix(info, "$") || !strings.Contains(info, "cmd=client|info") || !strings.HasSuffix(info, "\r\n") {
		t.Fatalf("CLIENT INFO = %q", info)
	}
	list := runStrings(exec, "CLIENT", "LIST")
	if !strings.HasPrefix(list, "$") || !strings.Contains(list, "cmd=client|list") || !strings.Contains(list, "\n") {
		t.Fatalf("CLIENT LIST = %q", list)
	}
}
