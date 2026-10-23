package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func newAOFExec(t *testing.T, c clock.Clock) (*Executor, *persist.Writer, *store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.aof")
	st := store.NewWithClock(c)
	aof, err := persist.Open(path, persist.FsyncAlways)
	if err != nil {
		t.Fatalf("persist.Open: %v", err)
	}
	exec := NewExecutorWithAOF(st, nil, c, aof)
	t.Cleanup(func() {
		exec.Close()
		if err := aof.Close(); err != nil {
			t.Errorf("aof.Close: %v", err)
		}
	})
	return exec, aof, st, path
}

func readAOFFrames(t *testing.T, path string) [][]byte {
	t.Helper()
	var frames [][]byte
	report, err := persist.RecoverFile(path, false, func(payload []byte) error {
		frames = append(frames, append([]byte(nil), payload...))
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverFile: %v", err)
	}
	if report.Truncated {
		t.Fatalf("unexpected truncation: %+v", report)
	}
	return frames
}

func readAOFCommands(t *testing.T, path string) [][][]byte {
	t.Helper()
	var commands [][][]byte
	for _, payload := range readAOFFrames(t, path) {
		br := bufio.NewReader(bytes.NewReader(payload))
		for {
			req, err := protocol.ReadRequest(br)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("parse AOF frame: %v", err)
			}
			commands = append(commands, req)
		}
	}
	return commands
}

// findCommand returns the last matching command so relative-expiry rewrites
// can be checked after earlier expiry effects for the same key.
func findCommand(commands [][][]byte, name string, key string) [][]byte {
	var found [][]byte
	for _, cmd := range commands {
		if len(cmd) >= 2 && string(cmd[0]) == name && string(cmd[1]) == key {
			found = cmd
		}
	}
	return found
}

func TestAOFPropagationEffects(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, _, path := newAOFExec(t, clk)

	runStrings(exec, "SADD", "s", "a", "b", "c")
	popped := runStrings(exec, "SPOP", "s")
	if len(popped) < 5 || popped[0] != '$' {
		t.Fatalf("SPOP reply = %q", popped)
	}
	member := popped[4 : len(popped)-2]

	runStrings(exec, "SET", "k", "v", "EX", "100")
	runStrings(exec, "PEXPIRE", "k", "5000")
	runStrings(exec, "INCRBYFLOAT", "f", "1.5")

	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}
	commands := readAOFCommands(t, path)

	if srem := findCommand(commands, "SREM", "s"); string(srem[2]) != member {
		t.Fatalf("SREM = %q, want member %q", srem, member)
	}
	if set := findCommand(commands, "SET", "k"); set == nil || string(set[2]) != "v" {
		t.Fatalf("SET k = %q", set)
	}
	if at := findCommand(commands, "PEXPIREAT", "k"); string(at[2]) != "1005000" {
		t.Fatalf("PEXPIREAT k = %q, want 1005000", at)
	}
	if set := findCommand(commands, "SET", "f"); set == nil || string(set[2]) != "1.5" {
		t.Fatalf("INCRBYFLOAT effect = %q", set)
	}

	for _, cmd := range commands {
		switch string(cmd[0]) {
		case "SPOP", "PEXPIRE", "INCRBYFLOAT", "EXPIRE", "PEXPIREAT":
			if string(cmd[0]) == "PEXPIREAT" {
				continue
			}
			t.Fatalf("nondeterministic command leaked into AOF: %q", cmd)
		}
	}
}

func TestAOFReplayStateEquality(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, want, path := newAOFExec(t, clk)

	for _, args := range [][]string{
		{"SET", "plain", "value"},
		{"SET", "exp", "v", "EX", "1000"},
		{"INCRBYFLOAT", "float", "2.5"},
		{"LPUSH", "list", "a", "b", "c"},
		{"RPOP", "list"},
		{"HSET", "hash", "f1", "v1", "f2", "v2"},
		{"HDEL", "hash", "f1"},
		{"SADD", "set", "x", "y", "z"},
		{"SREM", "set", "y"},
		{"SPOP", "set"},
		{"PEXPIRE", "plain", "100000"},
		{"RENAME", "float", "renamed"},
	} {
		runStrings(exec, args...)
	}
	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}

	got := store.NewWithClock(clk)
	report, err := RecoverAOF(path, got)
	if err != nil {
		t.Fatalf("RecoverAOF: %v", err)
	}
	if report.Records == 0 || report.Truncated {
		t.Fatalf("report = %+v", report)
	}
	assertStoresEqual(t, want, got)
}

func TestAOFRewriteStateEquality(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, want, path := newAOFExec(t, clk)

	for i := 0; i < 200; i++ {
		runStrings(exec, "SET", "before:"+string(rune('a'+i%26))+string(rune('0'+i%10)), "v")
	}
	if got := runStrings(exec, "BGREWRITEAOF"); got != "+Background append only file rewriting started\r\n" {
		t.Fatalf("BGREWRITEAOF = %q", got)
	}
	for i := 0; i < 50; i++ {
		runStrings(exec, "SET", "during:"+string(rune('a'+i%26)), "w")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		stats, _ := exec.AOFStats()
		if stats.Rewrites == 1 && !exec.Rewriting() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rewrite did not finish: %+v", stats)
		}
		time.Sleep(2 * time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		runStrings(exec, "SET", "after:"+string(rune('a'+i)), "z")
	}
	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}

	got := store.NewWithClock(clk)
	if _, err := RecoverAOF(path, got); err != nil {
		t.Fatalf("RecoverAOF after rewrite: %v", err)
	}
	assertStoresEqual(t, want, got)
}

func TestBGREWRITEAOFWithoutAOF(t *testing.T) {
	exec := NewExecutor(nil, nil)
	t.Cleanup(exec.Close)
	if got := runStrings(exec, "BGREWRITEAOF"); got != "-ERR AOF is not enabled\r\n" {
		t.Fatalf("BGREWRITEAOF without AOF = %q", got)
	}
}

func TestAOFAppendFailureDoesNotFeedReplication(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, _, _ := newAOFExec(t, clk)
	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}

	before := exec.Repl().MasterReplOffset()
	if err := exec.appendEffects([][][]byte{{[]byte("SET"), []byte("k"), []byte("v")}}); err == nil {
		t.Fatal("appendEffects succeeded after AOF close")
	}
	if got := exec.Repl().MasterReplOffset(); got != before {
		t.Fatalf("replication offset after failed append = %d, want %d", got, before)
	}
	if err := exec.persistEvictions([]string{"k"}); err == nil {
		t.Fatal("persistEvictions succeeded after AOF close")
	}
	if got := exec.Repl().MasterReplOffset(); got != before {
		t.Fatalf("replication offset after failed eviction append = %d, want %d", got, before)
	}
}

func TestAOFWriteBlockedAfterFailure(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, _, _ := newAOFExec(t, clk)
	runStrings(exec, "SET", "before", "ok")

	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}
	if got := runStrings(exec, "SET", "k", "v"); !strings.HasPrefix(got, "-MISCONF") {
		t.Fatalf("write after AOF failure = %q, want -MISCONF", got)
	}
	if got := runStrings(exec, "GET", "before"); got != "$2\r\nok\r\n" {
		t.Fatalf("read after AOF failure = %q, want the value", got)
	}
}

func TestAOFEffectIsOneFrame(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, _, path := newAOFExec(t, clk)
	runStrings(exec, "SET", "k", "v", "EX", "100")
	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}

	if frames := readAOFFrames(t, path); len(frames) != 1 {
		t.Fatalf("SET EX wrote %d frames, want one atomic frame", len(frames))
	}
	commands := readAOFCommands(t, path)
	if set := findCommand(commands, "SET", "k"); set == nil {
		t.Fatalf("missing SET effect in %q", commands)
	}
	if at := findCommand(commands, "PEXPIREAT", "k"); at == nil {
		t.Fatalf("missing PEXPIREAT effect in %q", commands)
	}
}

func TestAOFEvictionPersistsDelete(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	exec, aof, want, path := newAOFExec(t, clk)

	value := strings.Repeat("x", 1024)
	for i := 0; i < 20; i++ {
		runStrings(exec, "SET", fmt.Sprintf("k%d", i), value)
	}
	runStrings(exec, "CONFIG", "SET", "maxmemory-policy", "allkeys-lru")
	runStrings(exec, "CONFIG", "SET", "maxmemory", "20000")
	if got := runStrings(exec, "SET", "new", value); got != "+OK\r\n" {
		t.Fatalf("SET under allkeys-lru = %q, want OK", got)
	}
	if got := runStrings(exec, "DBSIZE"); got == ":21\r\n" {
		t.Fatalf("DBSIZE after eviction = %q, want a smaller keyspace", got)
	}
	if err := aof.Close(); err != nil {
		t.Fatalf("aof.Close: %v", err)
	}

	got := store.NewWithClock(clk)
	if _, err := RecoverAOF(path, got); err != nil {
		t.Fatalf("RecoverAOF: %v", err)
	}
	assertStoresEqual(t, want, got)
}

func TestStoreCopyIsIndependent(t *testing.T) {
	st := store.New()
	st.Set("k", []byte("v"))
	st.SetPreservingTTL("list", store.Value{Kind: store.KindList, List: [][]byte{[]byte("a")}})
	copy := st.Copy()
	st.Set("k", []byte("changed"))
	st.SetPreservingTTL("list", store.Value{Kind: store.KindList, List: [][]byte{[]byte("b")}})

	if v, _ := copy.Lookup("k"); string(v.Str) != "v" {
		t.Fatalf("copy key = %q", v.Str)
	}
	if v, _ := copy.Lookup("list"); string(v.List[0]) != "a" {
		t.Fatalf("copy list = %q", v.List)
	}
}

func assertStoresEqual(t *testing.T, want, got *store.Store) {
	t.Helper()
	wantKeys := want.Keys()
	gotKeys := got.Keys()
	sort.Strings(wantKeys)
	sort.Strings(gotKeys)
	if !slices.Equal(wantKeys, gotKeys) {
		t.Fatalf("keys = %v, want %v", gotKeys, wantKeys)
	}
	for _, key := range wantKeys {
		wv, _ := want.Lookup(key)
		gv, _ := got.Lookup(key)
		if wv.Kind != gv.Kind {
			t.Fatalf("type of %q = %q, want %q", key, gv.Kind, wv.Kind)
		}
		wantAt, wantOK := want.ExpiresAt(key)
		gotAt, gotOK := got.ExpiresAt(key)
		if wantAt != gotAt || wantOK != gotOK {
			t.Fatalf("TTL of %q = (%d,%v), want (%d,%v)", key, gotAt, gotOK, wantAt, wantOK)
		}
		switch wv.Kind {
		case store.KindString:
			if !bytes.Equal(wv.Str, gv.Str) {
				t.Fatalf("string %q = %q, want %q", key, gv.Str, wv.Str)
			}
		case store.KindList:
			wantList := wv.List[wv.ListHead:]
			gotList := gv.List[gv.ListHead:]
			if len(wantList) != len(gotList) {
				t.Fatalf("list %q length = %d, want %d", key, len(gotList), len(wantList))
			}
			for i := range wantList {
				if !bytes.Equal(wantList[i], gotList[i]) {
					t.Fatalf("list %q[%d] = %q, want %q", key, i, gotList[i], wantList[i])
				}
			}
		case store.KindHash:
			assertHashEqual(t, key, wv.Hash, gv.Hash)
		case store.KindSet:
			assertSetEqual(t, key, wv.Set, gv.Set)
		}
	}
}

func assertHashEqual(t *testing.T, key string, want, got []store.HashEntry) {
	t.Helper()
	wantMap := make(map[string]string, len(want))
	for _, entry := range want {
		wantMap[string(entry.Field)] = string(entry.Value)
	}
	gotMap := make(map[string]string, len(got))
	for _, entry := range got {
		gotMap[string(entry.Field)] = string(entry.Value)
	}
	if len(gotMap) != len(wantMap) {
		t.Fatalf("hash %q = %v, want %v", key, gotMap, wantMap)
	}
	for field, value := range wantMap {
		if gotMap[field] != value {
			t.Fatalf("hash %q field %q = %q, want %q", key, field, gotMap[field], value)
		}
	}
}

func assertSetEqual(t *testing.T, key string, want, got [][]byte) {
	t.Helper()
	wantSet := make(map[string]struct{}, len(want))
	for _, member := range want {
		wantSet[string(member)] = struct{}{}
	}
	gotSet := make(map[string]struct{}, len(got))
	for _, member := range got {
		gotSet[string(member)] = struct{}{}
	}
	if len(gotSet) != len(wantSet) {
		t.Fatalf("set %q = %v, want %v", key, gotSet, wantSet)
	}
	for member := range wantSet {
		if _, ok := gotSet[member]; !ok {
			t.Fatalf("set %q missing %q", key, member)
		}
	}
}
