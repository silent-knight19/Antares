# Failure Modes — Antares

> Purpose: what Antares does when a client, the network, the master, a replica, or the disk fails; what is guaranteed; how to detect it; and what the operator must do.
> Scope: one Antares master with zero or more Antares replicas, RESP2 clients, and the framed AOF. There is no Sentinel, cluster mode, quorum, automatic failover, or cross-datacenter replication.
> Status: S3.3, reviewed against the chaos, replication, persistence, and crash tests listed in §8. This document describes behavior that exists today, including known gaps.

---

## 1. The write contract

A mutation follows one path inside the executor:

```
execute command -> append AOF frame (if enabled) -> feed replication backlog -> reply
```

The reply is produced only after the AOF append returns. With replication, "feed" means the command is copied into the master's backlog and per-replica buffers; it does **not** wait for a replica to apply or acknowledge it.

| `-appendfsync` | A reply means | Process crash (`SIGKILL`) | Machine power loss | Verified by |
|---|---|---|---|---|
| `always` | Frame written and `fsync` completed | No acknowledged write loss | No acknowledged write loss | `make crash-test` (100 iterations) |
| `everysec` | Frame written to the OS page cache | Acknowledged writes survive a process crash (the OS keeps the cache) | Up to about 1 second of acknowledged writes can be lost | Not covered by the crash harness, which uses `always` |
| `no` | Frame written to the OS page cache | Acknowledged writes survive a process crash | Arbitrary recent writes can be lost; the OS decides when to flush | Not covered |

Two consequences matter operationally:

- Replication is asynchronous. A master can reply `+OK` while a replica is down, lagging, or has not applied the write. There is no `WAIT` command.
- A failed AOF append returns an error to the client, but the command may already have changed the master's in-memory keyspace. Treat a `-MISCONF` reply as "outcome unknown", not as "definitely not applied". Replicas are fed only after a successful append, so they do not apply that rejected write.

---

## 2. Failure matrix

| Failure | Client-visible effect | Durable / replicated state | Automatic recovery |
|---|---|---|---|
| Client TCP disconnect | Connection closes; in-flight reply may be lost | Commands already executed remain; `MULTI` queue and `WATCH` set are discarded with the connection | Client reconnects; no exactly-once guarantee |
| Client disconnects during `EXEC` | Reply may be lost | `EXEC` runs atomically in one executor turn; it either completed or never started | Client must re-read/reconcile |
| Master process crash | All connections drop | Acked writes survive per the fsync policy; AOF replay on restart; replicas reconnect | Restart; replicas full-resync because the master gets a new replid |
| Master restart without AOF | All connections drop | Dataset starts empty; replicas are full-resynced to the empty dataset | Restart; no data recovery |
| Master link/network partition | Writes on the replica return `-READONLY`; reads serve stale data | Master keeps accepting writes; replica catches up after reconnect | Partial resync if the gap is inside the 1 MB backlog and the replid matches, otherwise full resync |
| Master hangs (paused process) | Clients on the master block; replicas stop receiving data | No new writes; acked writes remain per fsync policy | Replica read timeout (30 s) triggers reconnect; master must be resumed/restarted |
| Replica process crash | Master continues; `connected_slaves` drops | Master data unaffected; replica's in-memory replid/offset are lost | Replica restarts and full-resyncs; if the master is down, it serves its recovered AOF or an empty dataset read-only |
| Replica link cut | Replica reads stale data; writes return `-READONLY` | Master continues; replica keeps its last applied dataset | Reconnect with 100 ms → 2 s backoff; partial resync when possible |
| Slow replica | Master and other clients unaffected | Master's per-replica buffer grows to 64 MiB, then the replica is disconnected | Reconnect and resync; the master never blocks on a replica |
| Replica disk/AOF failure | Replica reads stale data; replicated writes fail to apply | Replica stops advancing; resume point is invalidated and a full resync is requested | Fix the disk and restart; the replica full-resyncs |
| Master disk write/fsync failure | Writes return `-MISCONF`; reads continue | Sticky failure; no further writes are persisted or replicated; AOF clamped to the last complete frame on a write error | Free space/fix the disk, then restart; a failed writer is not cleared online |
| Master AOF corruption | Startup refuses to serve | Valid prefix is readable; a corrupt tail is refused unless `-aof-repair` is passed | Repair with `-aof-repair` (discards the tail) or restore from backup |
| Full resync interrupted | Replica serves a partial dataset to readers during sync | Resume point invalidated before `FLUSHALL`; the next attempt is a full resync | Reconnect and retry; a successful full resync replaces the partial state |
| Split brain / manual promotion | Both sides may accept writes | `REPLICAOF NO ONE` starts a new replid; the rejoining side is full-resynced and loses divergent writes | Operator chooses the surviving side; there is no automatic fencing |

---

## 3. Client connection failures

- **Disconnect before a command is read:** nothing happens.
- **Disconnect after a command is sent but before its reply:** the command may have executed and been persisted. Retrying a non-idempotent command can apply it twice. Use idempotent writes or application-level reconciliation when this matters.
- **Disconnect during `MULTI`:** the queued commands are discarded with the connection; `WATCH` registrations are released. `EXEC` itself is atomic: the executor runs the whole transaction in one turn, so a lost reply means "unknown", never "half applied".
- **Client stalls:** ordinary clients have no output-buffer cap. A client that stops reading blocks only its own connection goroutine; the executor and other clients continue. `-idle-timeout` (default off) closes idle connections.
- **Protocol violation:** the connection is closed with the Redis-compatible protocol error; no partial command is executed.

---

## 4. Master failures

### 4.1 Process crash or kill

- All client and replica connections drop.
- On restart, the AOF is replayed in frame order. An incomplete final frame (torn tail) is truncated automatically; a malformed or checksum-bad frame stops startup with `ErrCorrupt` unless `-aof-repair` is passed.
- Every master process gets a fresh random replid. Replicas therefore cannot `+CONTINUE` after a master restart; they receive `+FULLRESYNC` and replace their dataset.
- **No AOF:** the master starts empty. Replicas are full-resynced to the empty dataset. This is data loss by configuration, not a replication bug.
- **AOF with a lost tail** (`everysec`/`no`, or an `-aof-repair` truncation): replicas receive the recovered state, so the same tail loss propagates to them.

Evidence: `tests/chaos/crash_test.go` (`make crash-test`), `internal/persist/recovery_test.go` (`TestRecoverTornTailMatrix`, `TestRecoverCorruptFrameRefused`).

### 4.2 Master restart with an AOF

The master replays the valid AOF prefix and then serves. If the AOF was truncated to repair corruption, only the prefix survives. A rewrite in progress at crash time leaves either the old AOF or the fully written replacement; a stale `.antares-aof-*.tmp` is removed at startup.

### 4.3 Network partition or link cut

- The master keeps serving its clients and writing to its backlog.
- The replica detects the broken link, stays read-only, and serves its last applied dataset.
- On reconnect, the replica sends its replid and last offset. The master answers:
  - `+CONTINUE` when the replid matches and the offset is still inside the 1 MB backlog. The replica applies the backlog tail and resumes live streaming.
  - `+FULLRESYNC` when the replid differs (including after a master restart) or the offset has fallen out of the backlog. The replica flushes and reloads.
- A partition longer than the time needed to write 1 MB of replication traffic forces a full resync.

Evidence: `TestReplicaReconnectUsesPartialResync`, `TestPSyncDecisionPath`, `TestReplicaChaos50Kills` (50 cuts with dataset equality and offset parity after every reconnect).

### 4.4 Master hang

A paused or wedged master stops sending stream data. The replica's read deadline is 30 seconds; when it expires, the replica closes the link and retries with capped backoff. The master injects a PING every 10 seconds while a replica or sync buffer exists, so a healthy but idle link stays alive. A master that resumes before the deadline can keep the link; a master that resumes later is reconnected to, with partial or full resync depending on the backlog window.

Clients connected to a hung master block until the process resumes or the connection is closed; there is no server-side command timeout.

### 4.5 Split brain and promotion

There is no automatic failover. An operator promotes a replica with `REPLICAOF NO ONE`:

- Promotion generates a new replid, keeps the old one as `master_replid2`, and resets the master offset and backlog to zero.
- The promoted node accepts writes immediately.
- If the old master reconnects to the promoted node, its old replid is rejected, so it full-resyncs and **discards its divergent writes**.
- If the promoted node reconnects to the old master instead, the old master also rejects the new replid and full-resyncs the promoted node, discarding the promoted node's divergent writes.

The operator chooses which history survives. Until one side is resynced, both sides can accept writes and answer reads.

Evidence: `TestSplitBrainPromotionForcesFullResync`.

---

## 5. Replica failures

### 5.1 Link cut

The replica stays in `slave` role and remains read-only. Reads continue from the last applied dataset; writes from normal clients return `-READONLY`. `INFO replication` shows `master_link_status:down`; the replica reconnects with a 100 ms first delay, doubling to a 2 s cap, and resets the delay after a link reaches `up`.

### 5.2 Replica process crash or restart

- The master removes the replica from `connected_slaves` when the connection closes; master writes continue.
- A restarted replica has no persisted replid or offset, so it asks for `PSYNC ? -1` and receives a full resync. This is true even when the replica has its own AOF: it recovers the AOF first, then flushes and replaces the dataset during the full resync.
- If the master is unavailable, the restarted replica serves its recovered AOF (or an empty dataset) read-only until the link returns. That data is stale by definition.

### 5.3 Slow replica

Each replica has a 64 MiB pending-output cap and every stream write has a 5 second deadline. A replica that stops reading fills its buffer or misses a deadline and is disconnected; the master and other replicas continue. A full resync is capped at one in flight per master; a second `PSYNC` receives `ERR full resync already in progress`. A replica that cannot keep up with a full resync reconnects and may repeat it until the write rate drops.

Evidence: `TestSlowReplicaDoesNotBlockMaster`, `TestSlowFullResyncDisconnectsOnWriteTimeout`, `TestPumpReplicaStreamDisconnectsOnOverflow`, `TestFullResyncConcurrencyLimit`.

### 5.4 Replica disk or AOF failure

If the replica has `-appendonly` and its AOF fails, replicated writes are rejected by the same MISCONF gate as client writes. The apply loop treats the error as a failed apply: it invalidates the resume point and requests a full resync. The full resync's `FLUSHALL` is itself a write, so it is also blocked while the AOF is failed; the replica retries and keeps serving reads from its last good state. Fix the disk and restart the replica, or restart it after freeing space.

A failed apply never advances the replica offset past the command it did not execute (`TestReplicaApplyFailureDoesNotAdvanceOffset`).

### 5.5 During a full resync

A full resync flushes the replica first, then applies the snapshot stream in place. Readers on the replica can therefore observe a partial dataset while `master_sync_in_progress:1`; there is no read blocking or staging swap. If the stream breaks, the resume point has already been invalidated, so the next attempt is another full resync. A failed full resync leaves the replica serving whatever partial state was applied until a retry succeeds or the replica is restarted.

Evidence: `TestFailedFullResyncInvalidatesPSyncState`, `TestReplicaDatasetEqualityAfterFullResync`, `TestSnapshotStreamRejectsTruncated`.

---

## 6. Disk failures (master AOF)

### 6.1 Write or fsync failure

- The append fails; the writer records a sticky failure (`Failed()`).
- A write error clamps the file back to the last complete frame. A failed `fsync` does not truncate; the frame may be present in the OS cache but is not considered durable.
- The command that triggered the failure may already have changed the in-memory keyspace. The client receives `-MISCONF Errors writing to the AOF file: ...`.
- Subsequent writes, including writes inside `MULTI`, are rejected with MISCONF. Reads continue.
- The failing effect is not fed to replicas (AOF append precedes backlog feed). The master and replicas can therefore diverge on that one command until the master restarts or a full resync replaces the replica.
- `INFO persistence` reports `aof_last_write_status:err`. The sticky failure is cleared only by restarting the process. `BGREWRITEAOF` cannot clear it: a rewrite started against a failed writer aborts and keeps the old AOF (`TestFinishRewriteKeepsWriteFailureSticky`).

Operator action: free disk space or replace the failed volume, then restart the master with the same `-aof-file`. Do not pass `-aof-repair` for a full disk; that flag is for corrupt frames.

An eviction that races the failing append is a special case: the keys are already removed from memory, but the `DEL` frame may not reach the AOF. A restart can resurrect those evicted keys. They are cache victims, not client writes, and the next eviction cycle removes them again.

### 6.2 Torn tail versus corruption

| Damage | Behavior |
|---|---|
| Incomplete final frame (partial header or payload) | Truncated automatically on startup; the valid prefix is served |
| Bad checksum or invalid frame length | Startup refuses with `ErrCorrupt`; the valid prefix is not served |
| `-aof-repair` with any of the above | The tail from the first bad frame is discarded; later valid frames are discarded too |

Evidence: `internal/persist/recovery_test.go`, `TestAOFWriteBlockedAfterFailure`, `TestWriterRefusesAfterSyncFailure`.

### 6.3 Rewrite failure or crash during rewrite

A rewrite writes a snapshot plus the buffered concurrent-write tail to a temp file, fsyncs it, atomically renames it over the AOF, and fsyncs the parent directory. If the rewrite fails, the old AOF is kept and the temp file is removed; the server continues appending to the old AOF. A crash leaves either the old AOF or the completed replacement, both consistent; stale temp files are removed at startup. Rewrite failures are logged; `INFO persistence aof_last_bgrewrite_status` is currently hardcoded to `ok` and is not a reliable signal yet.

### 6.4 Disk full

Same path as §6.1: writes fail, the AOF is clamped to the last complete frame, and the server rejects mutations with MISCONF while serving reads. Free space and restart; there is no online recovery.

---

## 7. What is not covered

- **No automatic failover, quorum, or fencing.** Promotion is a manual `REPLICAOF NO ONE`; split brain is possible until one side is resynced.
- **No synchronous replication or `WAIT`.** A master reply does not imply replica durability.
- **No cross-node durability.** A replica with AOF is a copy, not a backup; a full resync can replace its dataset.
- **No persisted replid/offset.** Every process restart forces a full resync for that node's replicas, even with AOF.
- **No replica read blocking during full resync.** Readers can see a partial dataset while the snapshot applies.
- **No online AOF recovery from a failed writer.** A restart is required after a sticky write/fsync failure.
- **No RDB snapshots, backup tooling, disk health checks, or checksummed backups.**
- **No client output-buffer cap for ordinary clients.** A stalled normal client blocks only its own connection.
- **No command timeout.** A hung master leaves clients blocked until the process resumes or the connection closes.

---

## 8. Evidence and reproduction

| Area | Command / test |
|---|---|
| Crash durability (`always`, 100× SIGKILL) | `make crash-test`; `tests/chaos/crash_test.go` |
| AOF failure gate | `go test -count=1 -run 'TestAOF' ./internal/server/` |
| AOF recovery, torn tail, corruption | `go test -count=1 ./internal/persist/` |
| Rewrite failure keeps old AOF | `go test -count=1 -run 'TestFinishRewriteKeepsWriteFailureSticky' ./internal/persist/` |
| Partial resync after link cut | `go test -count=1 -run 'TestReplicaReconnectUsesPartialResync' ./internal/server/` |
| 50 mid-stream cuts, dataset and offset parity | `go test -count=1 -run 'TestReplicaChaos50Kills' ./internal/server/` |
| Split-brain promotion forces full resync | `go test -count=1 -run 'TestSplitBrainPromotionForcesFullResync' ./internal/server/` |
| Slow replica and full-resync timeout | `go test -count=1 -run 'TestSlow' ./internal/server/` |
| Replica output buffer overflow | `go test -count=1 -run 'TestPumpReplicaStreamDisconnectsOnOverflow' ./internal/server/` |
| Silent master timeout | `go test -count=1 -run 'TestReplicaApplyLoopTimesOutSilentMaster' ./internal/server/` |
| Failed apply does not advance offset | `go test -count=1 -run 'TestReplicaApplyFailureDoesNotAdvanceOffset' ./internal/server/` |
| Failed full resync invalidates resume point | `go test -count=1 -run 'TestFailedFullResyncInvalidatesPSyncState' ./internal/server/` |
| Full resync dataset equality | `go test -count=1 -run 'TestReplicaDatasetEqualityAfterFullResync' ./internal/server/` |
| Concurrent register linearizability | `make lin-check` |

The replication chaos suite runs without Docker; the crash harness builds `bin/antares` and kills it with `SIGKILL`. Run `make test-race` after changing any of these paths.
