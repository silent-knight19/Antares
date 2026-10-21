package server

import (
	"strings"
	"testing"
)

func TestConfigHz(t *testing.T) {
	exec := newStringExec(t)

	const get10 = "*2\r\n$2\r\nhz\r\n$2\r\n10\r\n"
	if got := runStrings(exec, "CONFIG", "GET", "hz"); got != get10 {
		t.Fatalf("CONFIG GET hz = %q, want %q", got, get10)
	}
	if got := runStrings(exec, "CONFIG", "SET", "hz", "20"); got != "+OK\r\n" {
		t.Fatalf("CONFIG SET hz 20 = %q", got)
	}
	if got := runStrings(exec, "CONFIG", "GET", "HZ"); got != "*2\r\n$2\r\nhz\r\n$2\r\n20\r\n" {
		t.Fatalf("CONFIG GET HZ after set = %q", got)
	}

	for _, tc := range []struct{ value, want string }{
		{"0", "*2\r\n$2\r\nhz\r\n$1\r\n1\r\n"},
		{"501", "*2\r\n$2\r\nhz\r\n$3\r\n500\r\n"},
	} {
		if got := runStrings(exec, "CONFIG", "SET", "hz", tc.value); got != "+OK\r\n" {
			t.Fatalf("CONFIG SET hz %s = %q", tc.value, got)
		}
		if got := runStrings(exec, "CONFIG", "GET", "hz"); got != tc.want {
			t.Fatalf("CONFIG GET hz after %s = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"CONFIG", "SET", "hz", "abc"}, "-ERR CONFIG SET failed (possibly related to argument 'hz') - argument couldn't be parsed into an integer\r\n"},
		{[]string{"CONFIG", "SET", "hz", "+10"}, "-ERR CONFIG SET failed (possibly related to argument 'hz') - argument couldn't be parsed into an integer\r\n"},
		{[]string{"CONFIG", "SET", "hz", "010"}, "-ERR CONFIG SET failed (possibly related to argument 'hz') - argument couldn't be parsed into an integer\r\n"},
		{[]string{"CONFIG", "SET", "hz", "-1"}, "-ERR CONFIG SET failed (possibly related to argument 'hz') - argument must be between 0 and 2147483647 inclusive\r\n"},
		{[]string{"CONFIG", "SET", "hz", "2147483648"}, "-ERR CONFIG SET failed (possibly related to argument 'hz') - argument must be between 0 and 2147483647 inclusive\r\n"},
		{[]string{"CONFIG", "SET", "hz", "10", "extra"}, "-ERR syntax error\r\n"},
		{[]string{"CONFIG", "SET", "nosuch", "1"}, "-ERR Unknown option or number of arguments for CONFIG SET - 'nosuch'\r\n"},
		{[]string{"CONFIG", "GET"}, "-ERR wrong number of arguments for 'config|get' command\r\n"},
		{[]string{"CONFIG", "SET", "hz"}, "-ERR wrong number of arguments for 'config|set' command\r\n"},
		{[]string{"CONFIG", "FOO"}, "-ERR unknown subcommand 'FOO'. Try CONFIG HELP.\r\n"},
	} {
		if got := runStrings(exec, tc.args...); got != tc.want {
			t.Fatalf("%v = %q, want %q", tc.args, got, tc.want)
		}
	}
}

func TestConfigAllowlist(t *testing.T) {
	exec := newStringExec(t)

	for _, tc := range []struct{ args, want string }{
		{"CONFIG GET maxmemory", "*2\r\n$9\r\nmaxmemory\r\n$1\r\n0\r\n"},
		{"CONFIG GET MAXMEMORY", "*2\r\n$9\r\nmaxmemory\r\n$1\r\n0\r\n"},
		{"CONFIG GET maxmemory-policy", "*2\r\n$16\r\nmaxmemory-policy\r\n$10\r\nnoeviction\r\n"},
		{"CONFIG GET maxclients", "*2\r\n$10\r\nmaxclients\r\n$5\r\n10000\r\n"},
		{"CONFIG GET proto-max-bulk-len", "*2\r\n$18\r\nproto-max-bulk-len\r\n$9\r\n536870912\r\n"},
		{"CONFIG GET client-query-buffer-limit", "*2\r\n$25\r\nclient-query-buffer-limit\r\n$10\r\n1073741824\r\n"},
		{"CONFIG GET databases", "*2\r\n$9\r\ndatabases\r\n$2\r\n16\r\n"},
		{"CONFIG GET appendonly", "*2\r\n$10\r\nappendonly\r\n$2\r\nno\r\n"},
		{"CONFIG GET save", "*2\r\n$4\r\nsave\r\n$23\r\n3600 1 300 100 60 10000\r\n"},
		{"CONFIG GET nosuch", "*0\r\n"},
		{"CONFIG SET maxmemory 1mb", "+OK\r\n"},
		{"CONFIG GET maxmemory", "*2\r\n$9\r\nmaxmemory\r\n$7\r\n1048576\r\n"},
		{"CONFIG SET maxmemory 1k", "+OK\r\n"},
		{"CONFIG SET maxmemory 1kb", "+OK\r\n"},
		{"CONFIG GET maxmemory", "*2\r\n$9\r\nmaxmemory\r\n$4\r\n1024\r\n"},
		{"CONFIG SET maxmemory 0", "+OK\r\n"},
		{"CONFIG SET maxmemory-policy allkeys-lru", "+OK\r\n"},
		{"CONFIG GET maxmemory-policy", "*2\r\n$16\r\nmaxmemory-policy\r\n$11\r\nallkeys-lru\r\n"},
		{"CONFIG SET maxclients 25", "+OK\r\n"},
		{"CONFIG GET maxclients", "*2\r\n$10\r\nmaxclients\r\n$2\r\n25\r\n"},
		{"CONFIG SET hz 20 maxmemory 1gb", "+OK\r\n"},
		{"CONFIG GET hz maxmemory", "*4\r\n$2\r\nhz\r\n$2\r\n20\r\n$9\r\nmaxmemory\r\n$10\r\n1073741824\r\n"},
	} {
		args := strings.Split(tc.args, " ")
		if got := runStrings(exec, args...); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.args, got, tc.want)
		}
	}
	if exec.stats.MaxClients.Load() != 25 {
		t.Fatalf("maxclients counter = %d, want 25", exec.stats.MaxClients.Load())
	}

	globbed := runStrings(exec, "CONFIG", "GET", "max*")
	if !strings.Contains(globbed, "maxmemory") || !strings.Contains(globbed, "maxmemory-policy") || !strings.Contains(globbed, "maxclients") {
		t.Fatalf("CONFIG GET max* = %q", globbed)
	}
}

func TestConfigAllowlistErrors(t *testing.T) {
	exec := newStringExec(t)
	for _, tc := range []struct{ args, want string }{
		{"CONFIG SET maxmemory -1", "-ERR CONFIG SET failed (possibly related to argument 'maxmemory') - argument must be a memory value\r\n"},
		{"CONFIG SET maxmemory abc", "-ERR CONFIG SET failed (possibly related to argument 'maxmemory') - argument must be a memory value\r\n"},
		{"CONFIG SET maxmemory 1tb", "-ERR CONFIG SET failed (possibly related to argument 'maxmemory') - argument must be a memory value\r\n"},
		{"CONFIG SET maxmemory-policy bogus", "-ERR CONFIG SET failed (possibly related to argument 'maxmemory-policy') - argument(s) must be one of the following: volatile-lru, volatile-lfu, volatile-random, volatile-ttl, allkeys-lru, allkeys-lfu, allkeys-random, noeviction\r\n"},
		{"CONFIG SET maxclients 0", "-ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument must be between 1 and 4294967295 inclusive\r\n"},
		{"CONFIG SET maxclients -1", "-ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument must be between 1 and 4294967295 inclusive\r\n"},
		{"CONFIG SET maxclients abc", "-ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument couldn't be parsed into an integer\r\n"},
		{"CONFIG SET maxclients 4294967296", "-ERR CONFIG SET failed (possibly related to argument 'maxclients') - argument must be between 1 and 4294967295 inclusive\r\n"},
		{"CONFIG SET maxmemory 1mb maxmemory-policy bogus", "-ERR CONFIG SET failed (possibly related to argument 'maxmemory-policy') - argument(s) must be one of the following: volatile-lru, volatile-lfu, volatile-random, volatile-ttl, allkeys-lru, allkeys-lfu, allkeys-random, noeviction\r\n"},
	} {
		args := strings.Split(tc.args, " ")
		if got := runStrings(exec, args...); got != tc.want {
			t.Fatalf("%s = %q, want %q", tc.args, got, tc.want)
		}
	}
	if got := runStrings(exec, "CONFIG", "GET", "maxmemory"); got != "*2\r\n$9\r\nmaxmemory\r\n$1\r\n0\r\n" {
		t.Fatalf("failed CONFIG SET partially applied: %q", got)
	}
}
