package server

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
)

func parseScanReply(t *testing.T, resp string) (uint64, []string) {
	t.Helper()
	lines := strings.Split(resp, "\r\n")
	if len(lines) < 5 || lines[0] != "*2" || !strings.HasPrefix(lines[1], "$") {
		t.Fatalf("malformed SCAN reply: %q", resp)
	}
	cursor, err := strconv.ParseUint(lines[2], 10, 64)
	if err != nil {
		t.Fatalf("cursor %q: %v", lines[2], err)
	}
	if !strings.HasPrefix(lines[3], "*") {
		t.Fatalf("malformed SCAN key array: %q", resp)
	}
	n, err := strconv.Atoi(lines[3][1:])
	if err != nil {
		t.Fatalf("key count %q: %v", lines[3], err)
	}
	keys := make([]string, 0, n)
	idx := 4
	for range n {
		if idx+1 >= len(lines) || !strings.HasPrefix(lines[idx], "$") {
			t.Fatalf("malformed SCAN key at line %d: %q", idx, resp)
		}
		keys = append(keys, lines[idx+1])
		idx += 2
	}
	return cursor, keys
}

func scanAll(t *testing.T, exec *Executor, opts ...string) map[string]bool {
	t.Helper()
	seen := make(map[string]bool)
	cursor := uint64(0)
	for steps := 0; steps < 10000; steps++ {
		args := append([]string{"SCAN", strconv.FormatUint(cursor, 10)}, opts...)
		next, keys := parseScanReply(t, runStrings(exec, args...))
		for _, key := range keys {
			seen[key] = true
		}
		cursor = next
		if cursor == 0 {
			return seen
		}
	}
	t.Fatal("SCAN did not terminate")
	return nil
}

func requireKeys(t *testing.T, got map[string]bool, want ...string) {
	t.Helper()
	have := make([]string, 0, len(got))
	for key := range got {
		have = append(have, key)
	}
	sort.Strings(have)
	sort.Strings(want)
	if len(have) != len(want) || strings.Join(have, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("keys = %v, want %v", have, want)
	}
}

func TestScanCommandOptionsAndErrors(t *testing.T) {
	exec := newStringExec(t)
	runStrings(exec, "SET", "a", "1")

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"SCAN"}, "-ERR wrong number of arguments for 'scan' command\r\n"},
		{[]string{"SCAN", "abc"}, "-ERR invalid cursor\r\n"},
		{[]string{"SCAN", "-1"}, "-ERR invalid cursor\r\n"},
		{[]string{"SCAN", ""}, "-ERR invalid cursor\r\n"},
		{[]string{"SCAN", "1x"}, "-ERR invalid cursor\r\n"},
		{[]string{"SCAN", "18446744073709551616"}, "-ERR invalid cursor\r\n"},
		{[]string{"SCAN", "0", "COUNT"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "COUNT", "0"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "COUNT", "-1"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "COUNT", "abc"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"SCAN", "0", "COUNT", "01"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"SCAN", "0", "COUNT", "9223372036854775808"}, "-ERR value is not an integer or out of range\r\n"},
		{[]string{"SCAN", "0", "MATCH"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "TYPE"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "FOO"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "COUNT", "1", "EXTRA"}, "-ERR syntax error\r\n"},
		{[]string{"SCAN", "0", "COUNT", "1", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", "00", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", "+0", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", " 0", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", "-0", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", "-01", "COUNT", "1000"}, "*2\r\n"},
		{[]string{"SCAN", "18446744073709551615", "COUNT", "1000"}, "*2\r\n"},
	} {
		got := runStrings(exec, tc.args...)
		if !strings.HasPrefix(got, tc.want) {
			t.Fatalf("%v = %q, want prefix %q", tc.args, got, tc.want)
		}
	}
}

func TestScanCommandMatchTypeAndEmptyKey(t *testing.T) {
	exec := newStringExec(t)
	for _, args := range [][]string{
		{"SET", "a", "1"},
		{"SET", "ab", "2"},
		{"SET", "b", "3"},
		{"SET", "", "empty"},
		{"RPUSH", "list", "x"},
		{"HSET", "hash", "f", "v"},
		{"SADD", "set", "m"},
	} {
		runStrings(exec, args...)
	}

	requireKeys(t, scanAll(t, exec, "MATCH", "a*", "COUNT", "1000"), "a", "ab")
	requireKeys(t, scanAll(t, exec, "MATCH", "*", "COUNT", "1000"), "", "a", "ab", "b", "hash", "list", "set")
	requireKeys(t, scanAll(t, exec, "TYPE", "string", "COUNT", "1000"), "", "a", "ab", "b")
	requireKeys(t, scanAll(t, exec, "TYPE", "LIST", "COUNT", "1000"), "list")
	requireKeys(t, scanAll(t, exec, "TYPE", "hash", "COUNT", "1000"), "hash")
	requireKeys(t, scanAll(t, exec, "TYPE", "set", "COUNT", "1000"), "set")
	requireKeys(t, scanAll(t, exec, "TYPE", "nope", "COUNT", "1000"))
	requireKeys(t, scanAll(t, exec, "MATCH", "a*", "TYPE", "string", "COUNT", "1000"), "a", "ab")
}

func TestScanCommandExpiryFiltering(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newExecutor(nil, nil, false, f)
	t.Cleanup(exec.Close)

	for _, args := range [][]string{
		{"SET", "dead:match", "v"},
		{"SET", "dead:other", "v"},
		{"SET", "live", "v"},
		{"PEXPIRE", "dead:match", "100"},
		{"PEXPIRE", "dead:other", "100"},
	} {
		runStrings(exec, args...)
	}
	f.Advance(200 * time.Millisecond)

	requireKeys(t, scanAll(t, exec, "MATCH", "dead:match", "COUNT", "1000"))
	if got := runStrings(exec, "DBSIZE"); got != ":2\r\n" {
		t.Fatalf("DBSIZE after filtered SCAN = %q, want 2", got)
	}
	requireKeys(t, scanAll(t, exec, "COUNT", "1000"), "live")
	if got := runStrings(exec, "DBSIZE"); got != ":1\r\n" {
		t.Fatalf("DBSIZE after unfiltered SCAN = %q, want 1", got)
	}
}

func TestScanCommandTypeExpiresFirst(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newExecutor(nil, nil, false, f)
	t.Cleanup(exec.Close)

	runStrings(exec, "RPUSH", "dead:list", "x")
	runStrings(exec, "PEXPIRE", "dead:list", "100")
	f.Advance(200 * time.Millisecond)

	requireKeys(t, scanAll(t, exec, "TYPE", "string", "COUNT", "1000"))
	if got := runStrings(exec, "DBSIZE"); got != ":0\r\n" {
		t.Fatalf("DBSIZE after SCAN TYPE = %q, want 0", got)
	}
}

func TestScanCommandGuaranteesUnderGrowthAndShrink(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1_000_000))
	exec := newExecutor(nil, nil, false, f)
	t.Cleanup(exec.Close)

	stable := make(map[string]bool)
	for i := range 100 {
		key := "stable:" + strconv.Itoa(i)
		runStrings(exec, "SET", key, "v")
		stable[key] = true
	}
	extra := make([]string, 0, 900)
	for i := range 900 {
		key := "extra:" + strconv.Itoa(i)
		runStrings(exec, "SET", key, "v")
		extra = append(extra, key)
	}

	seen := make(map[string]bool)
	cursor := uint64(0)
	for steps := 0; steps < 10000; steps++ {
		args := []string{"SCAN", strconv.FormatUint(cursor, 10)}
		next, keys := parseScanReply(t, runStrings(exec, args...))
		for _, key := range keys {
			seen[key] = true
		}
		if steps == 0 {
			for i := range 500 {
				runStrings(exec, "SET", "grown:"+strconv.Itoa(i), "v")
			}
		}
		if steps == 5 {
			del := append([]string{"DEL"}, extra...)
			runStrings(exec, del...)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if cursor != 0 {
		t.Fatal("SCAN did not terminate")
	}
	for key := range stable {
		if !seen[key] {
			t.Fatalf("SCAN missed stable key %q", key)
		}
	}
}
