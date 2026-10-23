package persist

import (
	"bufio"
	"io"
	"strconv"

	"github.com/silent-knight19/antares/internal/store"
)

const (
	snapshotListChunk = 512
	snapshotSetChunk  = 512
	snapshotHashChunk = 256
)

// WriteSnapshot emits the current dataset as framed Redis commands: SET,
// RPUSH, HSET, SADD, then PEXPIREAT for volatile keys. It reads the store
// synchronously, so callers must either own the store or serialize a copy
// first (ADR-010).
func WriteSnapshot(w io.Writer, st *store.Store) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	err := st.ForEach(func(key string, v store.Value, expiresAt int64) error {
		switch v.Kind {
		case store.KindString:
			if err := writeSnapshotCommand(bw, []byte("SET"), []byte(key), v.Str); err != nil {
				return err
			}
		case store.KindList:
			items := v.List[v.ListHead:]
			for start := 0; start < len(items); start += snapshotListChunk {
				end := min(start+snapshotListChunk, len(items))
				if err := writeSnapshotChunk(bw, "RPUSH", key, items[start:end]); err != nil {
					return err
				}
			}
		case store.KindHash:
			for start := 0; start < len(v.Hash); start += snapshotHashChunk {
				end := min(start+snapshotHashChunk, len(v.Hash))
				args := make([][]byte, 0, 2+2*(end-start))
				args = append(args, []byte("HSET"), []byte(key))
				for _, entry := range v.Hash[start:end] {
					args = append(args, entry.Field, entry.Value)
				}
				if err := writeSnapshotCommand(bw, args...); err != nil {
					return err
				}
			}
		case store.KindSet:
			for start := 0; start < len(v.Set); start += snapshotSetChunk {
				end := min(start+snapshotSetChunk, len(v.Set))
				if err := writeSnapshotChunk(bw, "SADD", key, v.Set[start:end]); err != nil {
					return err
				}
			}
		}
		if expiresAt != 0 {
			deadline := strconv.AppendInt(nil, expiresAt, 10)
			if err := writeSnapshotCommand(bw, []byte("PEXPIREAT"), []byte(key), deadline); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return bw.Flush()
}

func writeSnapshotChunk(bw *bufio.Writer, name, key string, items [][]byte) error {
	args := make([][]byte, 0, 2+len(items))
	args = append(args, []byte(name), []byte(key))
	args = append(args, items...)
	return writeSnapshotCommand(bw, args...)
}

func writeSnapshotCommand(bw *bufio.Writer, args ...[]byte) error {
	_, err := bw.Write(AppendFrame(nil, EncodeCommand(args...)))
	return err
}
