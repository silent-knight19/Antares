# File Manifest — Antares + Observatory

> Purpose: one-line truth for every tracked file — what it is, why it exists, and whether it is scaffold or a critical-path file awaiting owner review.
> Answers the interview probes: "Explain this file." / "Which part did you review?"
> Rule: updated whenever files are added or changed. Phase gates check that no orphan files exist — every tracked path appears here.
> Defend-rule (PLAN §8): the critical path — RESP parser, AOF frames/recovery, expiry, eviction, transactions, and the replication handshake/backlog — carries a `hand-own` marker; the harness implements, then the owner reviews every line and must be able to defend it in a 10-minute grilling before it ships.

## Ownership legend

- `scaffold` — generated scaffolding (P0), owner-reviewed; not on the critical path
- `stub` — placeholder that fails loudly with a phase pointer
- `placeholder` — `.gitkeep` so an empty directory can be tracked; removed when the directory gains real files
- `hand-own` — critical path; owner review pending or done; every line defensible for 10 minutes (PLAN §8)

## Root

| Path | What it is | Ownership |
|---|---|---|
| `.gitignore` | Ignore rules: build artifacts, runtime data (aof/rdb/tap), node_modules, env files, the downloaded Tcl test cache | scaffold |
| `LICENSE` | MIT, © 2026 Sachin Kumar Singh | scaffold |
| `Makefile` | Canonical commands (`build test test-race vet fmt tidy run clean`, `fuzz` with `FUZZTIME`); `diff-test` starts the pinned reference and runs the command differential harness; `clock-check` enforces ADR-009 wall-clock boundaries, `tcl-expire` runs the selected official expiry subset, and `tcl-incr` the selected official incr subset; M1 adds `ui-build` (npm + Vite), `showcase-dev` (build + UI + demo runner), and `seed`, with `PORT ?= 6380`; P6 adds `crash-test` (build + 100× SIGKILL AOF harness); remaining gated targets (`verify`, `showcase`, …) fail loudly until their phase; CI calls these same targets | scaffold |
| `README.md` | Public landing page: what/why, status, architecture, layout, getting started, roadmap, non-goals | scaffold |
| `go.mod` | Module `github.com/silent-knight19/antares`; Go 1.27.1 pin; single direct dependency: go-redis v9 (test-only differential client, ADR-017) | scaffold |
| `go.sum` | Module checksums for go-redis and its indirect deps | scaffold |
| `docker-compose.yml` | Reference Redis 7.4.11 pinned by image digest; 127.0.0.1-only; healthchecked | scaffold |

## CI

| Path | What it is | Ownership |
|---|---|---|
| `.github/workflows/ci.yml` | CI: gofmt check, `vet`, ADR-009 clock check, `build`, race-tested test run, differential harness; Go version read from `go.mod` | scaffold |

## Binaries (`cmd/`)

| Path | What it is | Ownership |
|---|---|---|
| `cmd/antares/main.go` | Server entry point: flags (`-bind`, `-port`, `-max-conns`, `-idle-timeout`, and P6 AOF flags `-appendonly`/`-aof-file`/`-appendfsync`/`-aof-repair`), slog logs, SIGINT/SIGTERM → graceful shutdown; creates the store + executor and wires `executor.Handle`, replays the AOF before serving when enabled, and closes the writer after the executor drains | scaffold |
| `cmd/observatory/main.go` | Observatory bridge entry point (U2): flags (`-version`, `-addr`, `-antares`, `-redis`, `-web`, `-poll`), graceful shutdown, and a placeholder HTML page when `web/dist` has not been built | scaffold |
| `cmd/observatory/bridge.go` | Observatory HTTP bridge (U2): `Config`, `New`/`Close`/`Handler`, routes `/api/health`, `/api/exec`, `/api/keys`, `/api/info`, `/api/events/stream` (SSE) and static SPA serving; lazy-redialing targets, SCAN-backed key rows with type/TTL/size preview, INFO field parsing, and `toReplyJSON` wire rendering | scaffold |
| `cmd/observatory/bridge_test.go` | Bridge tests: health (`antares_addr`, redis unconfigured), exec replies incl. error-at-HTTP-200 and transport failures, keys (types/TTL/preview/type filter/bad count), info version, SSE hello+info events, static placeholder and 404 | scaffold |

## Packages (`internal/`)

Each package carries a `doc.go` charter referencing its ADR; real files land in their phases. `internal/server` received its listener at P1.1, its parse → handle → flush loop at P1.4, and the single executor at P2.2; `internal/protocol` its parser, encoder, and error strings at P1.2–P1.5; `internal/store` its keyspace map at P2.2, the separate expires dict at P3.1, and typed string/list/hash/set values at P4.1–P4.3; `internal/clock` its injected time source at P3.4; critical-path packages are re-listed in "Hand-own commitments" below.

| Path | What it will contain | Ownership |
|---|---|---|
| `internal/clock/clock.go` | Injected time source for all domain logic: `Clock` interface, real implementation, and mutex-protected fake with `Advance`; network deadlines/backoff stay real (ADR-009) | scaffold |
| `internal/clock/clock_test.go` | Fake-clock advance/since coverage | scaffold |
| `internal/protocol/doc.go` | Package charter: RESP2 parse/encode + fuzz corpus (ADR-003) | scaffold |
| `internal/protocol/parse.go` | RESP2 request reader: multibulk arrays + inline commands, sdssplitargs quoting, string2ll counts (also exported as `ParseInt` for command arguments); exported `MaxBulkLen`/`MaxRequestBytes`; limits and error wording pinned by live capture; one exact allocation for payloads ≤ 64 KiB, larger ones grow with received bytes clamped to the declared size (a declared 512 MB length can never force the allocation); a 1 GiB aggregate request budget (payload + 32 B/arg) mirrors Redis's default `client-query-buffer-limit` and closes oversized requests without a reply (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/parse_test.go` | Edge corpus from live Redis 7.4.11 captures: multibulk, inline quoting/escapes, length limits, skips, lax CRLF handling, truncation, pipelining; plus a generated 200 KiB size-path case | scaffold |
| `internal/protocol/fuzz_test.go` | `FuzzReadRequest` — capture-shaped seeds + round-trip invariant (parse → encode → parse byte-identical); no-crash/no-hang on arbitrary bytes; huge-declared-length regression | scaffold |
| `internal/protocol/encode.go` | RESP2 reply encoder on pooled buffers: `+simple`, `-error`, `:int`, `$bulk`/`$-1`, `*array`/`*-1` (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/encode_test.go` | Byte-exact framing tests, parser round-trips, pool reuse | scaffold |
| `internal/protocol/errors.go` | Error bodies in captured Redis wording: `ProtocolErrorReply`, `WrongArity`, `UnknownCommand` (128-byte name/args truncation), `SyntaxError`, `NotInteger`, `InvalidExpireTime`, `NotFloat`, `IncrementOverflow`, `DecrementOverflow`, `FloatResult`, `StringTooLong`, `WrongType`, `CountPositive`, `CountRange`, `RandomCountTooLarge`, `NoSuchKey`, `IndexOutOfRange`, `OutOfMemory` | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/errors_test.go` | String-equality table vs Redis 7.4.11 captures (incl. WRONGTYPE and string-too-long) + CRLF sanitization | scaffold |
| `internal/server/doc.go` | Package charter: listener, conn goroutines, executor loop (ADR-002) | scaffold |
| `internal/server/server.go` | TCP listener: accept loop, conn registry, dynamic max-clients cap from metrics (over-cap connections get the captured refusal error), graceful/forced `Shutdown(ctx)` with a terminal forced error for later calls; per-conn loop parses RESP requests, dispatches in arrival order to the `Handler`; `connIO` flushes replies before the parser blocks, resets the idle deadline per underlying read, and counts network bytes (ADR-002) | scaffold |
| `internal/server/server_test.go` | Listener + pipeline tests: accept/close, max-conns, idle timeout incl. reset-on-partial-activity, graceful/forced shutdown, 10k-command pipeline order/flush, protocol-error reply+close | scaffold |
| `internal/server/executor.go` | Single executor goroutine owning the keyspace: bounded request queue (1024, blocking backpressure), per-request reply signal into the connection's encoder, active-expiry timer at configurable hz driven by the injected clock, process counters, config state (`hz`, `maxmemory`, policy), command counting, the AOF MISCONF gate and the maxmemory gate (`maxmemoryGate`/`performEvictions`: approximated-LRU eviction then `-OOM` for denyoom commands, emitting `events` decisions; ADR-026/027), optional `persist.Writer`, and an atomic rewrite-in-flight flag; registers string, list, hash, set, TTL, keyspace (KEYS/SCAN/RENAME/RENAMENX/RANDOMKEY/FLUSHDB/FLUSHALL), DBSIZE, CONFIG, INFO, HELLO, CLIENT, and BGREWRITEAOF commands on the default registry, idempotent `Close()`/bounded `CloseContext(ctx)` after connection drain (ADR-002/005/009/022/023) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/server/executor_test.go` | Serialization test (200 concurrent callers, unguarded counter + store writes, race-clean), executor dispatch/arity/unknown replies, and live active-expiry removal without a key read | scaffold |
| `internal/server/persistence.go` | P6 AOF integration: data-only replay registry, `RecoverAOF`/`RecoverAOFRepair` (replay or abort on an error reply; refuse a corrupt frame unless repair is set), executor-level effect propagation (`propagate`/`aofEffect`: SET options + INCRBYFLOAT→final SET+PEXPIREAT, EXPIRE family→PEXPIREAT, SPOP→SREM of the actual removed members, PERSIST, raw for deterministic commands), `BGREWRITEAOF`, and the snapshot/temp-fsync/atomic-rename rewrite path with a buffered concurrent-write tail (ADR-004/007/010/023) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/server/persistence_test.go` | P6 propagation effects (SREM/ PEXPIREAT/final SET, no nondeterministic leakage), replay state equality, rewrite state equality with concurrent writes, `BGREWRITEAOF` without AOF, and store-copy independence | scaffold |
| `internal/server/registry.go` | Command registry: case-insensitive name → `Command{Name, Arity, Write, DenyOOM, Handler}`; Redis arity rules (positive exact, negative minimum) and the `DenyOOM` (Redis `CMD_DENYOOM`) flag enforced by the maxmemory gate (ADR-026); dispatch replies with captured unknown-command/arity wording; P2.1 built-ins PING/ECHO/COMMAND (the COMMAND forms return an empty array for redis-cli hints) | scaffold |
| `internal/server/registry_test.go` | Registry tests: captured wire replies, case-insensitive lookup, positive/negative arity, COMMAND stub forms | scaffold |
| `internal/server/collection_helpers.go` | Shared collection helpers: `lookupTyped` (missing vs WRONGTYPE), hash/set index builders, `rightSize` shrink release, and `eachDistinctIndex` random distinct selection | scaffold |
| `internal/server/glob.go` | Redis-compatible glob matcher (`stringmatchlen` semantics): `*`, `?`, classes/ranges/negation, backslash escapes, malformed-class behavior, and the early-exit guard against abusive patterns | scaffold |
| `internal/server/scan_commands.go` | SCAN P5.2: cursor parsing matching Redis's `string2ull` quirks, `COUNT`/`MATCH`/`TYPE` parsing, and the two-element cursor-plus-keys reply; delegates bucket walking to the store | scaffold |
| `internal/server/scan_commands_test.go` | SCAN option/error table, cursor forms, MATCH/TYPE filtering, empty-key matching, pattern-before-expiry deletion, and growth/shrink coverage/termination invariants | scaffold |
| `internal/server/keyspace_commands.go` | Keyspace commands P5.1: KEYS (all-keys fast path plus the glob matcher), RENAME/RENAMENX (TTL transfer, destination overwrite, same-key forms, `ERR no such key`), RANDOMKEY, and FLUSHDB/FLUSHALL (`SYNC`/`ASYNC` parsing); mutation handlers share the store's keyspace primitives | scaffold |
| `internal/server/keyspace_commands_test.go` | Glob matcher edge table (classes, ranges, escapes, malformed patterns, binary bytes) plus KEYS/RENAME/RENAMENX/RANDOMKEY/FLUSHDB/FLUSHALL replies, TTL transfer/clearing, same-key rules, and syntax errors | scaffold |
| `internal/server/string_commands.go` | String commands P2.3–P2.4: SET (EX/PX/NX/XX/GET/KEEPTTL with Redis validation order), GET, DEL, EXISTS, TYPE, MSET/MGET, INCR/DECR/INCRBY/DECRBY, INCRBYFLOAT (reference C `long double` ABI per GOARCH via `math/big`: x87 on amd64/386, binary128 on arm64), APPEND/STRLEN/GETSET/SETNX; absolute-ms deadlines via `setExpiry` using the store clock; TTL-preserving in-place writes; APPEND ceiling and capacity reuse via `joinAppend`; `stringValue` shares `lookupTyped` for the missing-vs-WRONGTYPE contract | scaffold |
| `internal/server/string_commands_test.go` | Captured string behaviour: SET options/errors, MSET/MGET, integer overflow and DECRBY negation, INCRBYFLOAT parsing/formatting/bounds incl. ABI-specific subnormal boundary, APPEND empty-key creation and limit guard, STRLEN/GETSET/SETNX, TTL preservation/clearing, empty-key and binary values, wide argument batches, arity | scaffold |
| `internal/server/list_commands.go` | Lists: LPUSH/RPUSH, LPOP/RPOP with optional counts, LRANGE, LLEN, LINDEX, LSET, LREM, LTRIM; negative indices/ranges, Redis error-order parity, TTL-preserving mutations, auto-delete on empty, front headroom for O(1) amortized LPUSH, RPUSH in-place growth, and cleared/released removed slots | scaffold |
| `internal/server/list_commands_test.go` | List semantics: push/range/index, counted pops and null replies, LSET/LREM/LTRIM edge cases, wrong-type ordering, TTL preservation and lazy expiry | scaffold |
| `internal/server/hash_commands.go` | Hashes: HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD; insertion-order iteration up to 512 fields with a field index; above that, HDEL swap-removes in O(1) (order becomes unspecified, as in Redis hashtable encoding); overwrite counts, duplicate-field deletion, TTL-preserving mutations, auto-delete on empty, count/WITHVALUES validation and a bounded random-reply cap | scaffold |
| `internal/server/hash_commands_test.go` | Hash semantics: get/set/delete/all/keys/values, missing-key replies, wrong-type matrix, arity/error precedence, deterministic HRANDFIELD properties, TTL preservation and expiry | scaffold |
| `internal/server/set_commands.go` | Sets: SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER; member index, O(1) swap-remove SREM/SPOP, positive/negative random counts with a bounded reply cap, TTL-preserving mutations, auto-delete on empty, wrong-type and error-order parity | scaffold |
| `internal/server/set_commands_test.go` | Set semantics: add/remove/membership/cardinality, missing-key replies, wrong-type matrix, SPOP/SRANDMEMBER count parsing and random properties, TTL preservation and expiry | scaffold |
| `internal/server/collection_memory_test.go` | Regression tests that collection edits clear removed backing slots, release oversized storage, and keep hash/set indexes valid after delete/re-add/pop, including >512-field/member delete sweeps | scaffold |
| `internal/server/evict_test.go` | P7.3 eviction: `allkeys-lru` evicts to admit a write while a recently used key survives and an event is emitted; `volatile-lru` evicts only TTL keys and returns `-OOM` when none remain evictable | scaffold |
| `internal/server/collection_bench_test.go` | APPEND growth plus single-field HDEL/SREM benchmarks over 10k-element collections (guards against reintroducing full scans/index rebuilds) | scaffold |
| `internal/server/ttl_commands.go` | TTL/PTTL (nearest-second rounding), PERSIST, EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT (option parsing order, NX/XX/GT/LT conditions, non-volatile infinite-TTL rules, second/millisecond and relative/absolute overflow boundaries, delete-on-past deadline) using the store clock, and DBSIZE (O(1) physical key count) | scaffold |
| `internal/server/config_commands.go` | P5.3 `CONFIG GET/SET`: glob and multi-parameter GET over the 10-item allowlist (including dynamic P6 `appendonly`/`appendfsync`), `hz` (1–500 clamp with timer reset), memory units for `maxmemory`, the 8 eviction policies, dynamic `maxclients`, multi-SET atomic validation, and captured error wording | scaffold |
| `internal/server/config_commands_test.go` | CONFIG allowlist/glob/multi-pattern GET, hz clamping, maxmemory units/rejection, policy and maxclients validation, multi-SET atomicity, and captured error bodies | scaffold |
| `internal/server/info_commands.go` | P5.3/P6/P7.1 INFO: default/all/everything and named section filtering in Redis order, Server/Clients/Memory/Persistence/Stats/Replication/CPU/Modules/Commandstats/Errorstats/Latencystats/Cluster/Keyspace renderers (Memory reports `store.MemoryUsage` logical bytes, Persistence reports live AOF enabled/rewrite/size/status counters), human byte formatting, and live keyspace/expiry lines (ADR-025) | scaffold |
| `internal/server/info_commands_test.go` | INFO section selection/order, required fields, empty keyspace, db0 keys/expires, logical memory accounting (used = overhead + dataset, zero after DEL), maxclients wiring, and unknown-section behavior | scaffold |
| `internal/server/connection_commands.go` | P5.3 connection surface: `HELLO 2` handshake with strict version parsing and options, plus CLIENT ID/GETNAME/SETNAME/SETINFO/INFO/LIST/GETREDIR/NO-EVICT/NO-TOUCH/UNPAUSE stubs with captured validation errors | scaffold |
| `internal/server/connection_commands_test.go` | HELLO reply shape/options/version and NOPROTO errors; CLIENT subcommand replies, SETNAME/SETINFO validation, and unknown-subcommand wording | scaffold |
| `internal/server/ttl_commands_test.go` | Captured TTL behaviour: -2/-1 replies, rounding buckets, fake-clock lazy expiry after PX, relative/absolute variants, condition matrix incl. past deadlines, option conflicts and precedence over malformed integers, overflow per command, KEEPTTL/SET interplay; lazy-delete coverage for every read and read-modify-write command; DBSIZE physical-count consistency | scaffold |
| `internal/store/store.go` | Keyspace owned by the executor (no locks by construction): typed `Value` entries (`KindString`/`KindList`/`KindHash`/`KindSet`) with hash/set indexes in `data`, a separate `expires` dict, an injected clock (`NewWithClock`, `NowMs`), `Lookup/Type/Set/SetWithTTL/SetPreservingTTL/Delete/Exists/Len`, `Keys` (live names without deleting expired entries), `RandomKey` (deletes expired candidates), `Rename` (value plus TTL transfer), `Flush`, `Scan` (delegated to `scan.go`), `ExpiresAt/TTLms/SetExpire/Persist`, and `ExpireSample` for the active cycle; a 24-bit `Lru` access clock stamped on every lookup and write with `SampleKeys` for approximated-LRU eviction (ADR-027); strict `now > deadline` lazy expiry; `Len` is the O(1) physical key count used by DBSIZE; a `Mutations` counter for AOF effect detection and a deep `Copy` for rewrite snapshots (lazy/active expiry deliberately do not count as mutations) | scaffold (hand-own as value types land) |
| `internal/store/memory.go` | P7.1 logical memory accounting: `MemoryUsage()` scans physical keys and returns `Usage{Total, Dataset}` from a calibrated per-entry map cost plus exact payload, collection slice backing, and hash/set index maps (ADR-025); the O(n) scan is marked (`shortcut:`) for replacement when eviction lands (P7.3) | scaffold |
| `internal/store/scan.go` | Virtual power-of-two SCAN buckets with Redis's reverse-binary cursor, resizing by physical key count between calls, pattern-before-expiry filtering, and type filtering after expiry | scaffold |
| `internal/store/scan_test.go` | Reverse-binary cursor sequence, full keyspace coverage, growth/shrink stable-key invariants, pattern-before-expiry behavior, type filtering, bucket sizing, and empty scans | scaffold |
| `internal/store/store_test.go` | Keyspace operations incl. overwrite/delete/physical len; typed string/list/hash/set lookup and overwrite; fake-clock expires-dict preservation, lazy delete from both dicts on every accessor, `SetPreservingTTL` semantics, TTL clearing on overwrite, active-sample deletion/live-key safety, KEYS exclusion of expired keys without deletion, RANDOMKEY expiry cleanup, rename TTL transfer, and flush | scaffold |
| `internal/store/memory_test.go` | P7.1 memory accounting: empty/per-key total-and-dataset figures and a `runtime.MemStats` heap comparison within 10% across string/list/hash/set datasets | scaffold |
| `internal/store/lru_test.go` | P7.3 access clock: sampled idle seconds per key, volatile-only sampling, and lookup refreshing a key's idle time | scaffold |
| `internal/store/doc.go` | Keyspace, value types, WRONGTYPE, empty-collection auto-delete | scaffold → hand-own |
| `internal/persist/doc.go` | Framed AOF, group commit, recovery, rewrite (ADR-004/007/010) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/persist/frame.go` | `[u32 len][u32 CRC32 IEEE]` little-endian frame encode plus RESP command encoding; rejects zero-length and >1 GiB frames when reading (ADR-004) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/persist/frame_test.go` | Frame round-trip and error table (short header/payload, zero length, oversized length, bad CRC) plus command encoding | scaffold |
| `internal/persist/recovery.go` | Streaming recovery: replays checksum-valid frames, truncates an incomplete final frame (torn write), refuses a malformed or checksum-bad frame with `ErrCorrupt` unless `repair` is set, and reports records/valid bytes/truncated bytes/reason/corrupt; a missing file is an empty log (ADR-004/024) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/persist/recovery_test.go` | Clean replay, missing file, corruption matrix (partial header, partial payload, checksum mismatch, zero length, trailing garbage) asserting prefix recovery and truncation, and apply errors leaving the file untouched | scaffold |
| `internal/persist/writer.go` | Group-commit writer: one goroutine batches concurrent appends, fsync policies `always`/`everysec`/`no` with a background flusher, counters, `BeginRewrite`/`FinishRewrite`/`AbortRewrite` buffered-swap protocol with a bounded tail and parent-directory fsync, sticky write-failure handling (`Failed`) that refuses later appends, stale-temp cleanup, and idempotent `Close` (ADR-004/010/024) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/persist/writer_test.go` | Append/close/recover, deterministic group-commit batching under a blocked fsync, everysec flusher, no-fsync-until-close, append-after-close, rewrite-swap ordering (snapshot then buffered tail then post), and the rewrite threshold | scaffold |
| `internal/persist/snapshot.go` | Snapshot serializer shared with the future full-resync path: SET/RPUSH/HSET/SADD in chunks plus PEXPIREAT for volatile keys, emitted as framed commands (ADR-007) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/persist/snapshot_test.go` | Snapshot command/reply shape, collection chunking (600-member set), TTL PEXPIREAT, and list-headroom trimming | scaffold |
| `internal/expire/doc.go` | Active expiry charter: sample 20, repeat >25%, per-cycle budget (ADR-005) | scaffold → hand-own at P3 |
| `internal/expire/cycle.go` | Active-expiry cycle using the injected clock for its budget; calls the store's volatile-key sampler | scaffold |
| `internal/expire/cycle_test.go` | Fake-clock cycle tests: deletion/repeat, 25% stop, budget exhaustion, live-key safety | scaffold |
| `internal/evict/doc.go` | Approximated LRU, eviction pool, policies (ADR-006) | scaffold → hand-own at P7 |
| `internal/evict/evict.go` | P7.3 eviction core: `Policy` (`noeviction`/`allkeys-lru`/`volatile-lru`) with `ParsePolicy`/`Evicts`/`Volatile`, and a bounded `Pool` (16 slots) that keeps the most-idle sampled candidates and pops the victim (ADR-006/027) | scaffold |
| `internal/evict/evict_test.go` | Policy parsing/predicates and pool behavior: most-idle pop order and the 16-entry most-idle bound | scaffold |
| `internal/repl/doc.go` | PSYNC, backlog ring, replica apply (ADR-007/010) | scaffold → hand-own at S1 |
| `internal/events/doc.go` | Event-tap types + rotated JSONL sink (ADR-014) | scaffold |
| `internal/events/events.go` | P7.3 versioned event schema (`Version`, `Evict`, `Event`) and the `Sink` interface the executor emits to; the rotated JSONL file sink is U-phase | scaffold |
| `internal/client/doc.go` | Minimal RESP2 client for bridge + replica (ADR-017) | scaffold |
| `internal/client/client.go` | U1 RESP2 client: `Dial`/`Execute`/`Do`/`Close`, injected clock, per-call deadline, mutex-serialized single connection (no pool), and a `Result` carrying reply, canonical request bytes, raw reply bytes, and latency | scaffold |
| `internal/client/reply.go` | U1 reply model and parser (`simple`/`error`/`integer`/`bulk`/`array`/`null`) reusing `internal/protocol` limits, with canonical RESP re-encoding for the bridge wire trace and bounded line/array/depth guards | scaffold |
| `internal/client/client_test.go` | Client integration tests against an in-process Antares: PING/SET/GET/MGET, error replies, binary-safe values, and closed-connection failure | scaffold |
| `internal/client/reply_test.go` | Reply parser tests: every type, encode round-trip, malformed-input rejection, oversized line/array, and depth limit | scaffold |
| `internal/metrics/doc.go` | Metrics package charter | scaffold |
| `internal/metrics/stats.go` | Process counters shared by server/executor/INFO: run/repl IDs, dynamic max clients, connected/total connections, commands, network bytes, peak memory, and listener address | scaffold |

## Documentation (`docs/`)

| Path | What it is | Ownership |
|---|---|---|
| `docs/PLAN.md` | Single source of truth: scope, phases, exit criteria, protocol, DoD | scaffold |
| `docs/DECISIONS.md` | ADR log (ADR-001…022): context, options, why chosen/rejected, trade-offs, proof | scaffold |
| `docs/PROJECT_LOG.md` | Session log — deliverables, issues (STAR shape), exit evidence, next task | scaffold |
| `docs/FILE_MANIFEST.md` | This file | scaffold |
| `docs/INTERVIEW_QBANK.md` | Rehearsal Q&A + boundary-of-knowledge list | scaffold |
| `docs/COMPATIBILITY.md` | Pinned versions, compat claims, deliberate divergences, licensing note (stub until P9.1) | scaffold |
| `docs/UI-DESIGN.md` | U0 design baseline: dark-theme tokens, ASCII shell/Playground/Keyspace wireframes, render policy, a11y, keyboard map, and the M1 API contract; updated at U3/U4/U6a to match the built console | scaffold |

Planned docs from `PLAN.md` §13, arriving at their phases: `ARCHITECTURE.md` (P10.2), `BENCHMARKS.md` (P9.4), `FAILURE_MODES.md` (S3.3).

## Tests, benchmarks, UI, deploy

Entries below are populated at the phase noted; directories without files remain `placeholder` `.gitkeep` reservations per `PLAN.md` §13.

| Path | Arrives |
|---|---|
| `tests/unit/.gitkeep` | reserved for cross-package suites; unit tests are colocated with their package (Go convention) |
| `tests/differential/harness_test.go` | Shared test harness: starts an in-process Antares on an ephemeral port and builds RESP2 go-redis clients (identity disabled) (P2.6) |
| `tests/differential/smoke_test.go` | P1.6 go-redis PING/ECHO/pipeline/unknown-command smoke against the in-process server, now using the shared harness helpers |
| `tests/differential/diff_test.go` | Differential harness v0: 198-step string/TTL corpus + expiry scenario against the pinned reference, canonicalized reply comparison, namespaced keys, cleaned after the run; skips unless `REDIS_REF_ADDR` is set (`make diff-test`); full report at P9.1 |
| `tests/differential/list_diff_test.go` | 91-step list corpus: push/pop order and counts, ranges/indexing, LSET/LREM/LTRIM, error-order and wrong-type interactions, binary elements, auto-delete |
| `tests/differential/hash_diff_test.go` | 71-step hash corpus: get/set/delete/all/keys/values, overwrite counts, error-order and wrong-type interactions, deterministic HRANDFIELD forms, binary fields/values, auto-delete |
| `tests/differential/set_diff_test.go` | 99-step set corpus: add/remove/cardinality/membership, random-count parsing and errors, auto-delete, cross-type interactions, binary members; unordered replies sorted before comparison |
| `tests/differential/collection_sequence_diff_test.go` | 240 deterministic seeded operations across list/hash/set state, comparing replies against the reference after every step |
| `tests/differential/wrongtype_diff_test.go` | 171-case cross-type matrix: every list/hash/set command against strings and other collections, string reads/writes against every collection, and every mutator that can empty a collection; asserts replies, TYPE, EXISTS, and TTL after each step |
| `tests/differential/keys_diff_test.go` | 47-step keyspace corpus: KEYS patterns (wildcards, classes/ranges/negation, escapes, malformed classes, all-keys and arity), RENAME/RENAMENX across types with TTL transfer/clearing, RANDOMKEY properties, and FLUSHDB/FLUSHALL parsing/visibility against the pinned reference |
| `tests/differential/scan_diff_test.go` | 32-check SCAN corpus: full-iteration set equality for default/COUNT/MATCH/TYPE/unknown-type combinations, option and cursor error wording, Redis's accepted cursor quirks, and termination checks against the pinned reference |
| `tests/differential/info_diff_test.go` | P5.3 INFO/CONFIG/CLIENT differential: key INFO fields, section filtering and empty unknown sections, allowlist CONFIG GET values and maxmemory units, and CLIENT ID/SETINFO behavior against the pinned reference |
| `tests/differential/oom_diff_test.go` | P7.2 maxmemory differential: both servers filled past a 1 MiB limit under `noeviction`, then byte-identical `-OOM ...` for SET/MSET/SADD/APPEND while GET/DEL/CONFIG still succeed |
| `tests/chaos/crash_test.go` | P6.4 SIGKILL durability harness: N acknowledged writes at `-appendfsync always`, SIGKILL, restart, and verify every acked key (default 100 iterations, `CRASH_ITERATIONS` overrides); skipped unless `ANTARES_CRASH_TEST=1` (set by `make crash-test`) | scaffold |
| `tests/fuzz/.gitkeep` | P9.3 (soak + committed regression corpus; the P1.6 harness itself is `internal/protocol/fuzz_test.go`) |
| `tests/tcl/run-expire.sh` | Suite-aware runner: downloads the pinned Redis 7.4.11 tarball into a git-ignored cache, verifies SHA-256, builds/starts Antares, and runs the selected `expire` (default) or `incr` tests |
| `tests/tcl/run-incr.sh` | Thin wrapper that runs the selected official `unit/type/incr` suite through `run-expire.sh` |
| `tests/tcl/harness.tcl` | Thin Tcl glue (`start_server`, `tags`, `test`, `start_cluster`) over the official Redis Tcl client/assertion helpers, sourcing the selected suite (`unit/expire.tcl` or `unit/type/incr.tcl`); no Redis code copied into the repo |
| `tests/tcl/expire_tests.txt` | Allowlist of 39 official expiry tests supported by the current command surface |
| `tests/tcl/incr_tests.txt` | Allowlist of 26 official `unit/type/incr` tests; OBJECT/DEBUG/SETRANGE-dependent tests are excluded |
| `bench/scripts/.gitkeep` | P9.4 |
| `bench/results/.gitkeep` | P9.4 (commit-keyed raw outputs) |
| `web/{package.json,package-lock.json,tsconfig.json,vite.config.ts,postcss.config.js,index.html}` | U3 SPA build setup: Vite + React 19 + TS + Tailwind v3; dev server proxies `/api` to `127.0.0.1:6390`; production build emitted to `web/dist` (git-ignored) |
| `web/tailwind.config.js` | U3 Tailwind config; imports the U0 token theme from `web/tokens/tailwind.config.js` |
| `web/tokens/tailwind.config.js` | U0 design tokens (dark theme, one accent, ok/warn/crit/info, AA contrast) |
| `web/src/main.tsx` | U3 React entry: `QueryClientProvider` + `BrowserRouter` |
| `web/src/App.tsx` | U3 routes: `/playground`, `/keyspace`, index redirect, catch-all |
| `web/src/index.css` | Tailwind layers, base dark styling, selection, reduced-motion |
| `web/src/components/{Layout,StatusBar}.tsx` | U3 shell: left nav with later-milestone entries, status bar polling `/api/health` every 4s and consuming the SSE info stream |
| `web/src/components/{ui,ReplyView,KeyDrawer}.tsx` | Shared states/badges; U4 per-type RESP renderer; U6a read-only key detail drawer (Esc closes, bounded inline reads) |
| `web/src/lib/{api,types,command,format,keyDetail,useInfoStream}.ts` | Typed bridge client, reply types, inline command splitter, TTL formatting, detail loader, SSE hook |
| `web/src/pages/{Playground,Keyspace,NotFound}.tsx` | U4 Playground (input/history/presets/wire trace/latency), U6a virtualized SCAN table, 404 |
| `web/e2e/.gitkeep` | U12 (Playwright) |
| `deploy/demo/run-dev.sh` | U2 local demo runner: starts Antares, optionally the pinned reference Redis, then the Observatory |
| `deploy/demo/seed.sh` | U2 seed data (~10k `user:*` plus `queue:*`/`session:*`/`tags:*`) via `redis-cli --pipe` |
| `deploy/demo/Dockerfile` | U2 demo image (Go + Node build stages, static bridge serving); superseded by the P10.1 packaging profile |
| `deploy/demo/docker-compose.demo.yml` | U2 demo stack: Antares + pinned reference Redis + Observatory |

## Hand-own commitments (PLAN §8)

When these files exist, they carry the `hand-own` marker here and nothing is "done" on this path until the owner has reviewed every line and can defend it:

- `internal/protocol/` — RESP parser (`parse.go`), encoder (`encode.go`), error strings (`errors.go`); owner line-by-line review pending
- `internal/persist/` — frame format, writer, recovery, rewrite
- `internal/expire/` — lazy + active cycle
- `internal/evict/` — approximation, pool, policies
- `internal/server/` — executor (`executor.go`; owner line-by-line review pending) + transaction semantics
- `internal/repl/` — handshake, backlog, offsets, replica apply
