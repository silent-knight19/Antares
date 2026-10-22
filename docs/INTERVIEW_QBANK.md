# Interview Q-Bank — Antares + Observatory

> Purpose: the rehearsal bank. Every probe a real interviewer might ask, with a spoken-answer outline and the artifact that proves it.
> Rules: maintained by the AI harness whenever a decision lands or a correction surfaces; every answer points at evidence (ADR / test / doc) — no hand-waving.
> Sections: Narrative · Decisions · Verification · Boundaries · Answer notes.

---

## 1. Narrative

### Q1. Walk me through this project. (30–60s)

Antares is a Redis-compatible server I'm building from scratch in Go — RESP2, ~70 commands, TTL expiry, framed AOF persistence, approximated-LRU eviction, transactions, and replication with both full and partial resync. It ships with the Observatory: a separate process and web UI that visualizes the server's internals — fsync events, eviction decisions, expiry sampling, replication offsets — and renders only numbers produced by a verification pipeline, never hand-typed ones. Every claim in the repo is machine-verifiable: differential tests against a pinned real Redis, crash tests, fuzzing, and benchmarks in CI.
Evidence: `README.md`, `docs/PLAN.md` §1–§3.

### Q2. Why the name "Antares"?

α Scorpii is the red star called "the rival of Mars" — a red rival to Redis, built to be observed through its Observatory. It's a memorable hook, and every demo section (Replication Lab, Durability Lab) is named after what it actually does.
Evidence: `README.md`; PLAN §name.

---

## 2. Decisions (the "why X over Y" probes)

### Q3. Why Go for a Redis clone?

Goroutine-per-connection maps naturally onto the Redis architecture without async coloring; the stdlib gives `testing`, `-race`, native fuzzing, `pprof`, and `net` — exactly the tools the verification plan needs. Iteration speed under a fixed time budget mattered more than peak runtime, and the race detector makes "no data races" machine-checkable instead of a promise.
Evidence: ADR-001. Trade-off: GC pauses vs Rust/C — measured honestly in `BENCHMARKS.md` (P9.4).

### Q4. Redis is single-threaded. How did you model its concurrency in Go?

One executor goroutine owns the keyspace; connection goroutines do I/O and protocol only, handing commands to the executor over a channel. That reproduces Redis's sequential semantics exactly, so multi-key atomicity and MULTI/EXEC fall out for free, and the race detector is clean by construction. It also gives one choke point for AOF append ordering, the event tap, and replication offsets.
Evidence: ADR-002. Ceiling: one core for execution, same as Redis — measured in `BENCHMARKS.md`.

### Q5. Why RESP2 instead of RESP3?

Full RESP3 buys push messages, maps, and client-side caching — all features tied to non-goals like pub/sub. RESP2 covers every command in scope including replication, and a `HELLO` downgrade stub keeps modern redis-cli and go-redis working with `proto: 2`. RESP3 breadth would cost weeks for zero added verification value.
Evidence: ADR-003; compatibility kept honest in `COMPATIBILITY.md`.

### Q6. Why not Docker Desktop or OrbStack?

Licensing and auditability. Docker Desktop requires paid subscriptions for larger companies; OrbStack is proprietary and free for personal use only, with an unauditable privileged helper. colima is MIT, runs on lima, and exposes the identical `docker compose` interface — the migration was a config-only change. I logged the policy as an ADR: OSS or free-to-use only, with exactly one documented exception (the Redis test reference, which is never redistributed).
Evidence: ADR-020; `docker-compose.yml`.

### Q7. Why pin the Redis image by digest?

A tag like `7.4.11` can be re-pushed or drift; a digest pins the exact image-index bytes. Since differential tests treat this image as ground truth, its identity must be immutable — same reasoning as pinning Tcl suite versions (P9.2).
Evidence: ADR-020; `docker-compose.yml` digest `sha256:4fa24486…`.

### Q8. Why is the Observatory a separate process instead of HTTP endpoints in the server?

Three reasons: the server stays a faithful Redis-compatible artifact with zero HTTP surface; failure isolation — restarting the UI never touches data; and the bridge can hold connections to Antares and real Redis simultaneously, which is what makes the side-by-side differential view possible.
Evidence: ADR-013.

### Q9. Redis forks for snapshots. You can't fork a Go program safely — what's your plan?

The executor takes a point-in-time copy (bounded pause, no I/O inside the critical section), serializes it outside the executor, buffers concurrent writes during serialization, then drains the buffer. Overflow policy: abort the rewrite / disconnect the slow replica and retry. Correctness is preserved and the pause is measured; the memory cost is dataset × active snapshots.
Evidence: ADR-010; the rewrite half shipped at P6.6 (`internal/server/persistence.go`, `TestAOFRewriteStateEquality`, `TestWriterRewriteSwap` runs a store copy, serializes it outside the executor, buffers concurrent writes, fsyncs a temp file, and atomically renames); the replica half arrives at S1.3.

---

## 3. Verification ("how do you know it works?")

### Q10. How do you verify a Redis clone?

A six-layer pyramid, all CI-enforced: unit tests; differential tests against a pinned real Redis using an independent client (go-redis) — using my own client for verification would be circular; the official Tcl suite subset (`unit/expire`, `unit/type/incr`); chaos tests (100× SIGKILL, torn writes, replica kill/reconnect); a DST shadow-model for random interleavings; and fuzzing on the parser and frame recovery. The Tcl layer is live for 39 selected `unit/expire` tests (38 at P3.4, plus one list-backed expiry test after P4.1) and 26 selected `unit/type/incr` tests (P4.5), and expands as wider subsets are wired. Benchmark and test numbers are emitted into `claims.json`, and the Observatory renders only those.
Evidence: PLAN §9; ADR-017; ADR-018.

### Q11. What's the strongest verified evidence in the repo right now?

Honest answer: the wire path and the string/list/hash/set/keyspace core are machine-verified against real Redis. Evidence: the RESP parser's 31-case edge corpus (30 live captures plus a generated 200 KiB size case) with expectations captured live from the digest-pinned Redis 7.4.11 (several captures overturned assumed Redis behavior); error strings additionally verified by a live differential battery byte-identical to reference Redis, extended at the P1 audit with two late captures (128-byte command-name truncation, raw non-ASCII byte in the `expected '$'` error) and the over-cap refusal wording; a 15-minute parser fuzz clean (165.8M executions, zero failures) with a parse→encode→re-parse invariant; a 10 000-command single-write pipeline test asserting reply order; a go-redis (independent client, ADR-017) smoke; and the P2.3–P2.5 string surface at 1056 live differential checks (SET options, MSET/MGET, empty/binary/1 MiB values, 2000-key batches, integer overflow, expiry-edge interactions, APPEND/STRLEN/GETSET/SETNX, TTL/PERSIST and the full EXPIRE family with NX/XX/GT/LT, the P3.2 lazy-delete audit across every read/read-modify-write path with DBSIZE physical-count consistency, the P3.3 fake-clock active-cycle tests plus CONFIG hz clamp/error coverage, the P3.4 clock injection that lets store/command expiry tests advance time instead of sleeping, the P4.1 typed value model and 91-step list differential corpus, the P4.2 hash value type and 71-step hash differential corpus, the P4.3 set type and 99-step set differential corpus (with unordered replies canonicalized), the P4.4 171-case cross-type WRONGTYPE/empty-deletion matrix, the P4.5 240-step generated collection sequence, the P5.1 47-step keyspace corpus (Redis glob classes/ranges/escapes incl. malformed patterns, rename TTL transfer across all four types, RANDOMKEY properties, and flush parsing/visibility), the P5.2 32-check SCAN corpus (full-iteration set equality for COUNT/MATCH/TYPE, cursor parsing quirks, error wording, plus growth/shrink invariants proving termination and no loss of stable keys), the P5.3 INFO/CONFIG/CLIENT differential (key INFO fields and section filtering, allowlist CONFIG GET and maxmemory units, and CLIENT/HELLO behavior), and 756 randomized INCRBYFLOAT cases) with zero mismatches, now repeatable through the committed `make diff-test` harness (198 string/TTL + 91 list + 71 hash + 99 set + 171 cross-type + 240 sequence + 47 keyspace steps + 32 SCAN checks + INFO/CONFIG/CLIENT diff plus expiry/lazy-read scenarios against the pinned container). P6 adds durability evidence on the same independent-client path: frame/recovery/writer/snapshot unit tests, a corruption matrix (partial header, partial payload, checksum mismatch, bad length, trailing garbage), propagation-effect tests (SPOP→SREM, EXPIRE→PEXPIREAT, SET options/INCRBYFLOAT→final SET), replay and post-rewrite state equality, and `make crash-test` with 100 SIGKILL iterations where every write acknowledged before the kill is present after restart under `-appendfsync always`. On top of that, `make tcl-expire` runs 39 selected official Redis 7.4.11 `unit/expire` tests and `make tcl-incr` 26 `unit/type/incr` tests with zero failures, using a pinned download rather than vendored Redis code. CI (gofmt + vet + clock check + build + race tests) is green for the committed baseline. The M1 Observatory dev console is verified the same way it runs: `internal/client` and `cmd/observatory` unit/integration tests, a live `curl` smoke over every bridge endpoint (health/exec/keys/info/SSE/static), and a Vite production build at 112.5 kB gzip JS. Redis itself is the oracle for every protocol and command claim.
Evidence: `PROJECT_LOG.md` P1.2–M1 entries; GitHub Actions run [39138833199](https://github.com/silent-knight19/Antares/actions/runs/39138833199) (pre-P3.4).

### Q14. How do you make INCRBYFLOAT bit-exact?

Redis uses C `long double` and prints `%.17Lf` with trailing zeros trimmed. That type is x87 extended (64-bit significand) on amd64 and binary128 on arm64, so Go's float64 drifts within those 17 digits on both. I implemented parsing, addition, range checks, and formatting with `math/big.Float` selected from the reference ABI, matched Redis's 5119-byte input cap, hex floats without `p`, `inf`/`infinity` handling, `nan` rejection, architecture-specific overflow/subnormal boundaries, and `-0` normalization. 756 randomized differential checks plus edge cases produced zero mismatches on the original aarch64 oracle; the amd64 CI failure led to the ABI switch and a byte-identical huge-float check against `redis:7.4.11` on both architectures. The selected Tcl `unit/type/incr` subset passes 26/26.
Evidence: ADR-021; `internal/server/string_commands.go`; COMPATIBILITY precision row.

### Q15. How does the AOF stay deterministic and crash-safe?

The executor logs effects, not raw user commands. After each dispatch it compares `Store.Mutations()` to the value captured before the command; if it moved, it appends a frame. Deterministic commands replay verbatim; nondeterministic ones are rewritten: SPOP logs the actual removed members as `SREM` (computed from the before/after set difference), the EXPIRE family logs an absolute `PEXPIREAT`, SET options and INCRBYFLOAT log the resulting value as a final `SET` plus `PEXPIREAT`, and PERSIST logs `PERSIST`. Each frame carries a length and CRC32, so recovery replays the valid prefix; an incomplete final frame is a torn write and is truncated, while a malformed or checksum-bad frame refuses startup unless `-aof-repair` discards the tail. If a write or fsync fails, the writer clamps the file to the last complete frame and stops appending, and the executor answers writes with `-MISCONF` while reads continue. The writer groups concurrent appends into one write/fsync, and `always` returns from the append only after fsync — so a reply is sent only after the write is durable. Rewrite takes `BeginRewrite` first, then copies the store, serializes a temp file outside the executor, appends the buffered tail, fsyncs, and renames atomically; 100× SIGKILL with `always` loses zero acknowledged writes; the rewrite rename is followed by a parent-directory fsync so the swap survives power loss, and the rewrite's in-memory tail is capped so a slow rewrite cannot grow without bound.
Evidence: ADR-004, ADR-023, ADR-024; `internal/server/persistence.go`; `internal/persist/`; `make crash-test`; COMPATIBILITY AOF rows.

### Q16. How does the keyspace report its memory without a C allocator?

Go gives no per-dataset allocator counter, and `runtime.MemStats` is process-wide and moves with GC, so it is useless as a `maxmemory` input. I model the retained heap from the shapes the keyspace allocates: an amortized per-entry cost for the `map[string]Value` (bucket + inline `Value` + key header), the exact payload lengths, the outer slice backing for lists/hashes/sets, and the hash/set field-member index maps. `Store.MemoryUsage()` scans physical keys and returns `Total` plus the payload `Dataset`, and `INFO memory` reports them as `used_memory`/`used_memory_dataset`/`used_memory_overhead`. I measured the model against `runtime.ReadMemStats` and it lands within 1% of the Go heap on a payload-sized four-type dataset (the test gate is 10%). I chose the on-demand scan over an incremental per-key counter because collection handlers mutate slices in place and re-store them, so an old/new delta can read the already-mutated backing and miss the change. The trade-off is an O(n) scan per `INFO`, marked with a `shortcut:` note to cache when `maxmemory` enforcement moves it onto the command path at P7.2.
Evidence: ADR-025; `internal/store/memory.go`; `internal/server/info_commands.go`; `TestMemoryUsageTracksRuntime`; COMPATIBILITY memory-accounting row.

### Q17. How does `maxmemory` refuse a write without locking the client out of the keyspace?

Redis splits "may grow memory" from "is a write": only commands flagged `CMD_DENYOOM` are refused over the limit, so `DEL`/`SREM`/`SPOP` still run and the operator can recover. I mirrored that with a `Command.DenyOOM` field on the 15 growing commands, checked in `Executor.oomBlocked` on the command path after the AOF MISCONF gate and before dispatch. The gate only engages when `maxmemory > 0`, the command resolves and its arity is valid, and `store.MemoryUsage().Total` exceeds the limit; then it answers Redis's exact `-OOM command not allowed when used memory > 'maxmemory'.`. Placing it after resolution keeps unknown-command and arity errors first, and because eviction is not built yet, every policy — including `allkeys-lru` — behaves like `noeviction` and rejects rather than frees (P7.3).

Evidence: ADR-026; `internal/server/executor.go`; `internal/protocol/errors.go`; `TestOOMGate`; `TestDiffOOM`.

### Q18. How does approximated LRU pick a victim, and what if nothing can be evicted?

Each key carries a 24-bit access clock in seconds, refreshed on every lookup and write; idle time is `(now - lru) mod 2^24`, so the ~194-day wrap never reads as an ancient key. Under `allkeys-lru` the executor samples five keys (Go's randomized map iteration is the sample), inserts them into a 16-slot pool that keeps the most-idle candidates, evicts the most idle, and repeats until the keyspace fits `maxmemory` or nothing is evictable — emitting a versioned event per victim. `volatile-lru` samples only keys with a TTL. I rejected exact LRU: it costs about two pointers plus list maintenance per key and diverges from Redis, while the five-sample approximation keeps most of the hit rate (P7.4 measures retention). When eviction cannot free enough — `noeviction`, or `volatile-lru` with no volatile keys left — the denyoom command gets `-OOM`, while reads and deletes stay allowed. I validated the approximation: a hot set refreshed each round against 1000 rounds of cold churn retained 0.98–1.00 of its keys (the gate is 0.90), and `INFO stats` reports live `evicted_keys`.

Evidence: ADR-006, ADR-027; `internal/store/store.go`; `internal/evict/evict.go`; `internal/server/executor.go`; `TestEvictAllKeysLRU`; `TestEvictVolatileLRUOnly`; `TestEvictionRetainsHotKeys`.

### Q19. How do MULTI/EXEC and WATCH work on top of a single-goroutine executor?

The connection goroutine owns a `ConnState` whose `txState` holds the active flag, dirty flag, command queue, and watched snapshots; the executor owns all keyspace access. Before dispatch, `execute` checks the command name and arity: while a transaction is active, a valid command is appended and answered `+QUEUED`, an unknown or wrong-arity command sets `dirty`, and `EXEC` then returns Redis's `-EXECABORT Transaction discarded because of previous errors.` A runtime error (wrong type, overflow) is not a queue-time error, so it stays inside the EXEC reply array while the other commands still run. `EXEC` dispatches the whole queue in one executor turn — no other connection's requests can interleave — writes an `ArrayHeader`, and collects the queued commands' AOF effects into a single frame, so a restart replays all of the transaction or none of it. `WATCH` captures existence, kind, payload, and the absolute expiry deadline rather than registering a per-key dirty flag; at EXEC the snapshot is compared, and a mismatch returns a null array. That value-snapshot choice is the documented divergence: same-value writes and write-then-restore do not abort, while TTL changes and expiry do; `FLUSHALL` matches Redis by aborting only for watched keys that exist.

Evidence: ADR-008, ADR-028; `internal/server/transactions.go`; `internal/store/watch.go`; `TestTransactionBasics`; `TestTransactionDSTShadowModel`; `TestDiffTransactions`.

### Q20. How do you differential-test 71 commands without flaky tests?

Three layers: curated corpora for edges I chose (arity/option ordering, overflow, WRONGTYPE, binary and empty keys), a seeded generator for interactions I did not enumerate, and property tests for replies that are random by contract. Every comparison uses `go-redis` (an independent implementation, ADR-017) against the digest-pinned Redis 7.4.11 container, and the generator prints its seed so `DIFF_SEED=n DIFF_ITERS=m go test ./tests/differential -run TestDiffGenerated` reproduces a failure exactly. The part that usually rots is canonicalization, so it lives in one small file with its own unit test: values, errors, and order are exact by default; only `SMEMBERS`/`KEYS`/`HKEYS`/`HVALS` are sorted, `HGETALL` pairs are sorted by field, and live `TTL`/`PTTL` durations are bucketed to positive because the reference and Antares are called milliseconds apart (`-1`/`-2` stay exact). Random commands and documented divergences are excluded deliberately and covered elsewhere, so a green run means behaviour matches rather than "the harness ignored it." The first 30-seed sweep still found a real boundary: `LSET` with a max-int64 index, where Redis truncates to a C `int` and Antares rejects by design.

Evidence: ADR-029; `tests/differential/generator_test.go`, `canonical.go`, `edge_diff_test.go`, `surface_diff_test.go`; `make diff-test`; CI service container.

### Q21. How do you run Redis's own Tcl tests against Antares without copying Redis code?

`make tcl` downloads the pinned Redis 7.4.11 tarball at run time into a git-ignored cache, verifies its SHA-256, builds Antares, starts it on an ephemeral port, and runs the selected official `unit/expire` and `unit/type/incr` tests. I do not use the upstream `test_helper.tcl` runner: its external mode sends setup commands Antares does not implement (`SELECT`, `FLUSHALL`, `FUNCTION FLUSH`, `CONFIG GET *`). Instead `tests/tcl/harness.tcl` supplies only the `start_server`/`tags`/`test` glue over the official Redis Tcl client and assertion helpers, and supports `start_cluster` as a no-op so cluster-only tests can be skipped. Two allowlists name the tests whose commands exist; everything else is skipped explicitly. The harness classifies each selected test by its command token and prints `REPORT <command> passed=… failed=… skipped=…`, so a green run is a per-command claim, not just one aggregate line. Nothing from Redis is vendored or linked, which keeps the MIT repo clean.

Evidence: ADR-020; `tests/tcl/{run-expire.sh,run-incr.sh,harness.tcl,expire_tests.txt,incr_tests.txt}`; `make tcl` (65/65 green); COMPATIBILITY Tcl rows.

### Q22. How do you fuzz a file format without the filesystem dominating the run?

Recovery was file-only, so the first `FuzzRecoverFile` run through real temp files managed about 1.3k executions in 30 seconds; most of the time went to creating files and fsyncing directories on truncated tails. I split `RecoverFile` into a thin file wrapper (open, stat, close, truncate, sync) and an in-memory `recoverFrames(io.Reader, size, apply, repair)` core. The fuzzer drives the core directly, which raised throughput by about three orders of magnitude without weakening the assertions: for arbitrary bytes, a non-repair pass either returns a clean or torn report whose valid prefix is exactly the input (or a prefix), or refuses with `ErrCorrupt` and leaves the file untouched; a repair pass must keep the same prefix, report the discarded tail, and a second pass over `data[:ValidBytes]` must be clean and return identical payloads. The `size` argument is also the allocation guard: a four-byte header claiming more bytes than the file holds is treated as a torn write instead of reserving the declared length. The corpus is committed in `internal/persist/testdata/fuzz/FuzzRecoverFile` alongside inline seeds, and the nightly workflow runs both this target and the parser for an hour each.

Evidence: `internal/persist/fuzz_test.go`; `internal/persist/recovery.go`; `internal/persist/testdata/fuzz/FuzzRecoverFile/`; `Makefile`; `.github/workflows/fuzz-nightly.yml`; COMPATIBILITY fuzz row.

---

## 4. Boundaries ("what does it NOT do?")

### Q12. What does Antares not implement, and why?

Deliberate non-goals, each with a recorded reason: Lua scripting, cluster/slots, Sentinel, Streams, RESP3 beyond the HELLO downgrade, modules, listpack/intset encodings, keyspace notifications, pub/sub, GEO/HLL/Bitmaps, sharded keyspace, and auth/ACL. They were cut to protect the verification depth and the replication story — and each cut is an interview answer with a reason, not a gap I haven't thought about.
Evidence: ADR-012, ADR-019 (Observatory non-goals).

### Q13. Where is Antares weaker than real Redis?

Known, documented divergences: WATCH uses value-snapshot comparison (a write-then-restore-identical-value does not abort EXEC, while Redis aborts); persistence formats are intentionally not RDB/AOF-file-compatible (AOF is a single framed file with no `appendonlydir`/manifest, is enabled only by `-appendonly`/`-aof-file`/`-appendfsync` flags rather than a config file or `CONFIG SET`, uses fixed 64 MiB/2× rewrite thresholds instead of `auto-aof-rewrite-*`, and logs command effects rather than a raw command stream, so it cannot be replayed by Redis); no compact encodings (listpack/intset); snapshot transfer for full resync is a framed command stream, larger than RDB for big values; INCRBYFLOAT selects the reference's C `long double` ABI by `GOARCH` (64-bit x87 on amd64, binary128 on arm64), so a non-Linux host Redis can differ; `proto-max-bulk-len` is fixed at 512 MiB and the aggregate request budget at 1 GiB (both visible but GET-only through CONFIG); `INFO` memory is a logical model of the retained keyspace (amortized map/slice overhead plus payload, calibrated within 10% of Go's heap) rather than an allocator count, so its absolute bytes and `mem_fragmentation_ratio` differ from Redis; future-phase counters are zeroed, CLIENT name/id state is stubbed, and `maxmemory` evicts approximated-LRU victims under `allkeys-lru`/`volatile-lru` and otherwise refuses memory-growing commands with `-OOM`, while `CONFIG SET` rejects the unimplemented LFU/random/TTL policy names; `databases` reports 16 while only db0 exists; FLUSHDB/FLUSHALL accept ASYNC but free synchronously because there is no lazy-free worker yet; SCAN uses a virtual bucket table over the Go map, so cursor values and per-call batch composition differ from Redis while the terminate/at-least-once/duplicate guarantees match.
Evidence: `COMPATIBILITY.md`; ADR-004, ADR-007, ADR-008, ADR-022, ADR-023.

---

## 5. Answer notes (folded into answers as they stabilize)

1. `claims.json` is the provenance store the Observatory renders from (ADR-018) — never describe it as scratch output.
2. 10× bottlenecks: executor saturation first; group commit (`everysec`) decouples fsync from the executor, which stalls only under `fsync=always`; second-order: parser allocations / GC pressure.
3. Digest-bump process: pull the new release → resolve the index digest → update `docker-compose.yml` + `COMPATIBILITY.md` → log the why (CVE/version) in PROJECT_LOG → re-run differential + Tcl suites before accepting.
4. Protocol fidelity comes from capture, not memory: Redis 7.4 accepts multibulk counts up to INT_MAX, silently skips counts ≤ 0, blinds through the byte after a header `\r`, never validates bulk trailers, and takes bare-LF inline. Each of those contradicted an initial assumption in P1.2 — verify against the reference binary, then pin it in tests.
