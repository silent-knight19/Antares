package persist

import (
	"bufio"
	"bytes"
	"testing"
	"time"

	"github.com/silent-knight19/antares/internal/clock"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

func snapshotCommands(t *testing.T, data []byte) [][][]byte {
	t.Helper()
	var commands [][][]byte
	for _, payload := range splitFrames(t, data) {
		req, err := protocol.ReadRequest(bufio.NewReader(bytes.NewReader(payload)))
		if err != nil {
			t.Fatalf("ReadRequest(%q): %v", payload, err)
		}
		commands = append(commands, req)
	}
	return commands
}

func TestWriteSnapshotCommands(t *testing.T) {
	clk := clock.NewFake(time.UnixMilli(1_000_000))
	st := store.NewWithClock(clk)
	st.Set("plain", []byte("value"))
	st.SetWithTTL("dying", []byte("x"), 2_000_000)
	st.SetPreservingTTL("list", store.Value{Kind: store.KindList, List: [][]byte{[]byte("a"), []byte("b")}})
	st.SetPreservingTTL("hash", store.Value{Kind: store.KindHash, Hash: []store.HashEntry{{Field: []byte("f"), Value: []byte("v")}}})
	members := make([][]byte, 600)
	for i := range members {
		members[i] = []byte{'m', byte('0' + i%10), byte('0' + (i/10)%10), byte('0' + (i/100)%10)}
	}
	st.SetPreservingTTL("set", store.Value{Kind: store.KindSet, Set: members})

	var buf bytes.Buffer
	if err := WriteSnapshot(&buf, st); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	commands := snapshotCommands(t, buf.Bytes())

	saw := map[string]int{}
	var setMembers int
	var setFrames int
	for _, cmd := range commands {
		name := string(cmd[0])
		saw[name]++
		switch name {
		case "SADD":
			setFrames++
			setMembers += len(cmd) - 2
		case "SET":
			if string(cmd[1]) == "plain" && string(cmd[2]) != "value" {
				t.Fatalf("unexpected SET: %q", cmd)
			}
		case "PEXPIREAT":
			if string(cmd[1]) == "dying" && string(cmd[2]) != "2000000" {
				t.Fatalf("PEXPIREAT = %q", cmd)
			}
		}
	}
	if saw["SET"] == 0 || saw["RPUSH"] == 0 || saw["HSET"] == 0 || saw["PEXPIREAT"] == 0 {
		t.Fatalf("missing snapshot commands: %v", saw)
	}
	if setFrames < 2 || setMembers != 600 {
		t.Fatalf("SADD frames = %d, members = %d; want >=2 and 600", setFrames, setMembers)
	}
}

func TestWriteSnapshotClearsListHeadroom(t *testing.T) {
	st := store.NewWithClock(clock.NewFake(time.UnixMilli(1_000_000)))
	st.SetPreservingTTL("list", store.Value{Kind: store.KindList, List: [][]byte{nil, nil, []byte("a"), []byte("b")}, ListHead: 2})

	var buf bytes.Buffer
	if err := WriteSnapshot(&buf, st); err != nil {
		t.Fatalf("WriteSnapshot: %v", err)
	}
	commands := snapshotCommands(t, buf.Bytes())
	if len(commands) != 1 || string(commands[0][0]) != "RPUSH" {
		t.Fatalf("commands = %q", commands)
	}
	if string(commands[0][2]) != "a" || string(commands[0][3]) != "b" {
		t.Fatalf("RPUSH args = %q", commands[0])
	}
}
