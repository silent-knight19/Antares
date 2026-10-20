package server

import "testing"

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
