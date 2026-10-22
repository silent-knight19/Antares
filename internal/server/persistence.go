package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/silent-knight19/antares/internal/persist"
	"github.com/silent-knight19/antares/internal/protocol"
	"github.com/silent-knight19/antares/internal/store"
)

var errRewriteInProgress = errors.New("AOF rewrite already in progress")

// registerDataCommands registers only commands that read or write the
// keyspace. Replay uses this subset so a hand-edited AOF cannot reach CONFIG.
func registerDataCommands(reg *Registry, st *store.Store) {
	registerStringCommands(reg, st)
	registerListCommands(reg, st)
	registerHashCommands(reg, st)
	registerSetCommands(reg, st)
	registerTTLCommands(reg, st)
	registerKeyspaceCommands(reg, st)
}

// NewDataRegistry returns the replay registry for an offline store.
func NewDataRegistry(st *store.Store) *Registry {
	reg := NewRegistry()
	registerDataCommands(reg, st)
	return reg
}

// registerPersistenceCommands adds BGREWRITEAOF to the serving registry.
func registerPersistenceCommands(reg *Registry, e *Executor) {
	reg.Register(Command{Name: "bgrewriteaof", Arity: 1, Handler: func(_ [][]byte, out *protocol.Encoder) {
		if e.aof == nil {
			out.Error("ERR AOF is not enabled")
			return
		}
		if err := e.startRewrite(); err != nil {
			if errors.Is(err, errRewriteInProgress) {
				out.Error("ERR Background append only file rewriting already in progress")
			} else {
				out.Error("ERR " + err.Error())
			}
			return
		}
		out.Simple("Background append only file rewriting started")
	}})
}

// RecoverAOF replays valid frames in order into st, refusing a corrupt frame
// rather than silently discarding committed data.
func RecoverAOF(path string, st *store.Store) (persist.RecoveryReport, error) {
	return RecoverAOFRepair(path, st, false)
}

// RecoverAOFRepair is RecoverAOF with an explicit choice to discard a corrupt
// AOF tail instead of refusing to start. A command that replies with an error
// still aborts startup, so a write is never silently dropped.
func RecoverAOFRepair(path string, st *store.Store, repair bool) (persist.RecoveryReport, error) {
	reg := NewDataRegistry(st)
	enc := protocol.GetEncoder()
	defer enc.Release()

	return persist.RecoverFile(path, repair, func(payload []byte) error {
		br := bufio.NewReader(bytes.NewReader(payload))
		for first := true; ; first = false {
			req, err := protocol.ReadRequest(br)
			if errors.Is(err, io.EOF) {
				if first {
					return errors.New("empty command")
				}
				return nil
			}
			if err != nil {
				return err
			}
			if len(req) == 0 {
				return errors.New("empty command")
			}
			enc.Reset()
			reg.Dispatch(req, enc)
			if bytes.HasPrefix(enc.Bytes(), []byte("-")) {
				return fmt.Errorf("command %q failed: %s", req[0], strings.TrimSpace(string(enc.Bytes()[1:])))
			}
		}
	})
}

func (e *Executor) appendonlyValue() string {
	if e.aof != nil {
		return "yes"
	}
	return "no"
}

func (e *Executor) appendfsyncValue() string {
	if e.aof != nil {
		return string(e.aof.Policy())
	}
	return string(persist.FsyncEverySec)
}

// AOFStats returns the writer counters for INFO and tests.
func (e *Executor) AOFStats() (persist.Stats, bool) {
	if e.aof == nil {
		return persist.Stats{}, false
	}
	return e.aof.Stats(), true
}

// Rewriting reports whether a rewrite is in flight.
func (e *Executor) Rewriting() bool { return e.rewriteInFlight.Load() }

func (e *Executor) captureSpopBefore(req protocol.Request) map[string]struct{} {
	if (e.aof == nil && (e.repl == nil || !e.repl.IsMaster())) || len(req) < 2 || !strings.EqualFold(string(req[0]), "spop") {
		return nil
	}
	v, ok := e.store.Lookup(string(req[1]))
	if !ok {
		return nil
	}
	members := make(map[string]struct{}, len(v.Set))
	for _, member := range v.Set {
		members[string(member)] = struct{}{}
	}
	return members
}

// propagate appends the command's effects when the store's mutation counter
// moved, then opportunistically compacts.
func (e *Executor) propagate(req protocol.Request, mutations uint64, spopBefore map[string]struct{}) error {
	return e.appendEffects(e.collectEffects(req, mutations, spopBefore))
}

// collectEffects returns the deterministic effect commands for one request, or
// nil when it did not mutate the keyspace. Raw commands are safe to replay;
// random, relative-time, and precision-sensitive ones are rewritten.
func (e *Executor) collectEffects(req protocol.Request, mutations uint64, spopBefore map[string]struct{}) [][][]byte {
	if (e.aof == nil && (e.repl == nil || !e.repl.IsMaster())) || len(req) == 0 || e.store.Mutations() == mutations {
		return nil
	}
	return e.aofEffect(req, spopBefore)
}

// appendEffects writes every effect into one frame so replay applies a
// multi-command effect (a transaction) atomically, and feeds the replication backlog.
func (e *Executor) appendEffects(effects [][][]byte) error {
	if len(effects) == 0 {
		return nil
	}
	var payload []byte
	for _, args := range effects {
		cmdBytes := persist.EncodeCommand(args...)
		if e.aof != nil {
			payload = append(payload, cmdBytes...)
		}
		if e.repl != nil && e.repl.IsMaster() {
			e.repl.FeedBacklog(cmdBytes)
		}
	}
	if e.aof != nil {
		if err := e.aof.Append(payload); err != nil {
			slog.Error("AOF append failed", "err", err)
			return err
		}
		if e.aof.ShouldRewrite() {
			if err := e.startRewrite(); err != nil && !errors.Is(err, errRewriteInProgress) {
				slog.Error("AOF automatic rewrite failed", "err", err)
			}
		}
	}
	return nil
}

func (e *Executor) aofEffect(req protocol.Request, spopBefore map[string]struct{}) [][][]byte {
	switch strings.ToLower(string(req[0])) {
	case "set", "incrbyfloat":
		return e.stringEffect(string(req[1]))
	case "expire", "pexpire", "expireat", "pexpireat":
		return e.expiryEffect(string(req[1]))
	case "persist":
		return [][][]byte{{[]byte("PERSIST"), req[1]}}
	case "spop":
		return e.spopEffect(string(req[1]), spopBefore)
	default:
		return [][][]byte{req}
	}
}

func (e *Executor) stringEffect(key string) [][][]byte {
	v, ok := e.store.Lookup(key)
	if !ok {
		return [][][]byte{{[]byte("DEL"), []byte(key)}}
	}
	effect := [][][]byte{{[]byte("SET"), []byte(key), v.Str}}
	if at, ok := e.store.ExpiresAt(key); ok && at != 0 {
		effect = append(effect, pexpireat(key, at))
	}
	return effect
}

func (e *Executor) expiryEffect(key string) [][][]byte {
	if _, ok := e.store.Lookup(key); !ok {
		return [][][]byte{{[]byte("DEL"), []byte(key)}}
	}
	at, ok := e.store.ExpiresAt(key)
	if !ok || at == 0 {
		return [][][]byte{{[]byte("PERSIST"), []byte(key)}}
	}
	return [][][]byte{pexpireat(key, at)}
}

func (e *Executor) spopEffect(key string, before map[string]struct{}) [][][]byte {
	if len(before) == 0 {
		return nil
	}
	after := make(map[string]struct{})
	if v, ok := e.store.Lookup(key); ok {
		after = make(map[string]struct{}, len(v.Set))
		for _, member := range v.Set {
			after[string(member)] = struct{}{}
		}
	}
	removed := make([][]byte, 0, len(before))
	for member := range before {
		if _, still := after[member]; !still {
			removed = append(removed, []byte(member))
		}
	}
	if len(removed) == 0 {
		return nil
	}
	args := make([][]byte, 0, 2+len(removed))
	args = append(args, []byte("SREM"), []byte(key))
	args = append(args, removed...)
	return [][][]byte{args}
}

func pexpireat(key string, at int64) [][]byte {
	return [][]byte{[]byte("PEXPIREAT"), []byte(key), strconv.AppendInt(nil, at, 10)}
}

// startRewrite begins buffering, snapshots the store while the executor still
// owns it, then compacts and swaps on a background goroutine.
func (e *Executor) startRewrite() error {
	if e.aof == nil {
		return errors.New("AOF is not enabled")
	}
	if !e.rewriteInFlight.CompareAndSwap(false, true) {
		return errRewriteInProgress
	}
	if err := e.aof.BeginRewrite(); err != nil {
		e.rewriteInFlight.Store(false)
		return err
	}
	snapshot := e.store.Copy()
	go func() {
		defer e.rewriteInFlight.Store(false)
		if err := e.writeRewrite(snapshot); err != nil {
			slog.Error("AOF rewrite failed", "err", err)
		}
	}()
	return nil
}

func (e *Executor) writeRewrite(snapshot *store.Store) error {
	tmp, err := os.CreateTemp(filepath.Dir(e.aof.Path()), ".antares-aof-*.tmp")
	if err != nil {
		_ = e.aof.AbortRewrite("")
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := persist.WriteSnapshot(tmp, snapshot); err != nil {
		tmp.Close()
		_ = e.aof.AbortRewrite(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		_ = e.aof.AbortRewrite(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = e.aof.AbortRewrite(tmpPath)
		return err
	}
	if err := e.aof.FinishRewrite(tmpPath); err != nil {
		_ = e.aof.AbortRewrite(tmpPath)
		return err
	}
	return nil
}
