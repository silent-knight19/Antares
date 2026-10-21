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
| `Makefile` | Canonical commands (`build test test-race vet fmt tidy run clean`, `fuzz` with `FUZZTIME`); `diff-test` starts the pinned reference and runs the command differential harness; `clock-check` enforces ADR-009 wall-clock boundaries, `tcl-expire` runs the selected official expiry subset, and `tcl-incr` the selected official incr subset; remaining gated targets (`verify`, `crash-test`, `showcase`, …) fail loudly until their phase; CI calls these same targets | scaffold |
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
| `cmd/antares/main.go` | Server entry point: flags (`-bind`, `-port`, `-max-conns`, `-idle-timeout`), slog logs, SIGINT/SIGTERM → graceful shutdown; creates the store + executor and wires `executor.Handle`, closing the executor after the server drains | scaffold |
| `cmd/observatory/main.go` | Observatory bridge entry point; P0 stub → U2 | stub |

## Packages (`internal/`)

Each package carries a `doc.go` charter referencing its ADR; real files land in their phases. `internal/server` received its listener at P1.1, its parse → handle → flush loop at P1.4, and the single executor at P2.2; `internal/protocol` its parser, encoder, and error strings at P1.2–P1.5; `internal/store` its keyspace map at P2.2, the separate expires dict at P3.1, and typed string/list/hash/set values at P4.1–P4.3; `internal/clock` its injected time source at P3.4; critical-path packages are re-listed in "Hand-own commitments" below.

| Path | What it will contain | Ownership |
|---|---|---|
| `internal/clock/clock.go` | Injected time source for all domain logic: `Clock` interface, real implementation, and mutex-protected fake with `Advance`; network deadlines/backoff stay real (ADR-009) | scaffold |
| `internal/clock/clock_test.go` | Fake-clock advance/since coverage | scaffold |
| `internal/protocol/doc.go` | Package charter: RESP2 parse/encode + fuzz corpus (ADR-003) | scaffold |
| `internal/protocol/parse.go` | RESP2 request reader: multibulk arrays + inline commands, sdssplitargs quoting, string2ll counts (also exported as `ParseInt` for command arguments); limits and error wording pinned by live capture; one exact allocation for payloads ≤ 64 KiB, larger ones grow with received bytes clamped to the declared size (a declared 512 MB length can never force the allocation); a 1 GiB aggregate request budget (payload + 32 B/arg) mirrors Redis's default `client-query-buffer-limit` and closes oversized requests without a reply (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/parse_test.go` | Edge corpus from live Redis 7.4.11 captures: multibulk, inline quoting/escapes, length limits, skips, lax CRLF handling, truncation, pipelining; plus a generated 200 KiB size-path case | scaffold |
| `internal/protocol/fuzz_test.go` | `FuzzReadRequest` — capture-shaped seeds + round-trip invariant (parse → encode → parse byte-identical); no-crash/no-hang on arbitrary bytes; huge-declared-length regression | scaffold |
| `internal/protocol/encode.go` | RESP2 reply encoder on pooled buffers: `+simple`, `-error`, `:int`, `$bulk`/`$-1`, `*array`/`*-1` (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/encode_test.go` | Byte-exact framing tests, parser round-trips, pool reuse | scaffold |
| `internal/protocol/errors.go` | Error bodies in captured Redis wording: `ProtocolErrorReply`, `WrongArity`, `UnknownCommand` (128-byte name/args truncation), `SyntaxError`, `NotInteger`, `InvalidExpireTime`, `NotFloat`, `IncrementOverflow`, `DecrementOverflow`, `FloatResult`, `StringTooLong`, `WrongType`, `CountPositive`, `CountRange`, `RandomCountTooLarge`, `NoSuchKey`, `IndexOutOfRange` | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/errors_test.go` | String-equality table vs Redis 7.4.11 captures (incl. WRONGTYPE and string-too-long) + CRLF sanitization | scaffold |
| `internal/server/doc.go` | Package charter: listener, conn goroutines, executor loop (ADR-002) | scaffold |
| `internal/server/server.go` | TCP listener: accept loop, conn registry, max-conns cap (over-cap connections get the captured refusal error), graceful/forced `Shutdown(ctx)` with a terminal forced error for later calls; per-conn loop parses RESP requests, dispatches in arrival order to the `Handler`; `connIO` flushes replies before the parser blocks and resets the idle deadline per underlying read (writes bounded when the timeout is set) (ADR-002) | scaffold |
| `internal/server/server_test.go` | Listener + pipeline tests: accept/close, max-conns, idle timeout incl. reset-on-partial-activity, graceful/forced shutdown, 10k-command pipeline order/flush, protocol-error reply+close | scaffold |
| `internal/server/executor.go` | Single executor goroutine owning the keyspace: bounded request queue (1024, blocking backpressure), per-request reply signal into the connection's encoder, active-expiry timer at configurable hz driven by the injected clock, registers string, list, hash, set, TTL, keyspace (KEYS/SCAN/RENAME/RENAMENX/RANDOMKEY/FLUSHDB/FLUSHALL), DBSIZE, and CONFIG commands on the default registry, idempotent `Close()`/bounded `CloseContext(ctx)` after connection drain (ADR-002/005/009) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/server/executor_test.go` | Serialization test (200 concurrent callers, unguarded counter + store writes, race-clean), executor dispatch/arity/unknown replies, and live active-expiry removal without a key read | scaffold |
| `internal/server/registry.go` | Command registry: case-insensitive name → `Command{Name, Arity, Handler}`; Redis arity rules (positive exact, negative minimum); dispatch replies with captured unknown-command/arity wording; P2.1 built-ins PING/ECHO/COMMAND (the COMMAND forms return an empty array for redis-cli hints) | scaffold |
| `internal/server/registry_test.go` | Registry tests: captured wire replies, case-insensitive lookup, positive/negative arity, COMMAND stub forms | scaffold |
| `internal/server/collection_helpers.go` | Shared collection helpers: `lookupTyped` (missing vs WRONGTYPE), hash/set index builders, `rightSize` shrink release, and `eachDistinctIndex` random distinct selection | scaffold |
| `internal/server/glob.go` | Redis-compatible glob matcher (`stringmatchlen` semantics): `*`, `?`, classes/ranges/negation, backslash escapes, malformed-class behavior, and the early-exit guard against abusive patterns | scaffold |
| `internal/server/scan_commands.go` | SCAN P5.2: cursor parsing matching Redis's `string2ull` quirks, `COUNT`/`MATCH`/`TYPE` parsing, and the two-element cursor-plus-keys reply; delegates bucket walking to the store | scaffold |
| `internal/server/scan_commands_test.go` | SCAN option/error table, cursor forms, MATCH/TYPE filtering, empty-key matching, pattern-before-expiry deletion, and growth/shrink coverage/termination invariants | scaffold |
| `internal/server/keyspace_commands.go` | Keyspace commands P5.1: KEYS (all-keys fast path plus the glob matcher), RENAME/RENAMENX (TTL transfer, destination overwrite, same-key forms, `ERR no such key`), RANDOMKEY, and FLUSHDB/FLUSHALL (`SYNC`/`ASYNC` parsing); mutation handlers share the store's keyspace primitives | scaffold |
| `internal/server/keyspace_commands_test.go` | Glob matcher edge table (classes, ranges, escapes, malformed patterns, binary bytes) plus KEYS/RENAME/RENAMENX/RANDOMKEY/FLUSHDB/FLUSHALL replies, TTL transfer/clearing, same-key rules, and syntax errors | scaffold |
| `internal/server/string_commands.go` | String commands P2.3–P2.4: SET (EX/PX/NX/XX/GET/KEEPTTL with Redis validation order), GET, DEL, EXISTS, TYPE, MSET/MGET, INCR/DECR/INCRBY/DECRBY, INCRBYFLOAT (binary128 via `math/big`), APPEND/STRLEN/GETSET/SETNX; absolute-ms deadlines via `setExpiry` using the store clock; TTL-preserving in-place writes; APPEND ceiling and capacity reuse via `joinAppend`; `stringValue` shares `lookupTyped` for the missing-vs-WRONGTYPE contract | scaffold |
| `internal/server/string_commands_test.go` | Captured string behaviour: SET options/errors, MSET/MGET, integer overflow and DECRBY negation, INCRBYFLOAT parsing/formatting/bounds, APPEND empty-key creation and limit guard, STRLEN/GETSET/SETNX, TTL preservation/clearing, empty-key and binary values, wide argument batches, arity | scaffold |
| `internal/server/list_commands.go` | Lists: LPUSH/RPUSH, LPOP/RPOP with optional counts, LRANGE, LLEN, LINDEX, LSET, LREM, LTRIM; negative indices/ranges, Redis error-order parity, TTL-preserving mutations, auto-delete on empty, front headroom for O(1) amortized LPUSH, RPUSH in-place growth, and cleared/released removed slots | scaffold |
| `internal/server/list_commands_test.go` | List semantics: push/range/index, counted pops and null replies, LSET/LREM/LTRIM edge cases, wrong-type ordering, TTL preservation and lazy expiry | scaffold |
| `internal/server/hash_commands.go` | Hashes: HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD; insertion-order iteration up to 512 fields with a field index; above that, HDEL swap-removes in O(1) (order becomes unspecified, as in Redis hashtable encoding); overwrite counts, duplicate-field deletion, TTL-preserving mutations, auto-delete on empty, count/WITHVALUES validation and a bounded random-reply cap | scaffold |
| `internal/server/hash_commands_test.go` | Hash semantics: get/set/delete/all/keys/values, missing-key replies, wrong-type matrix, arity/error precedence, deterministic HRANDFIELD properties, TTL preservation and expiry | scaffold |
| `internal/server/set_commands.go` | Sets: SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER; member index, O(1) swap-remove SREM/SPOP, positive/negative random counts with a bounded reply cap, TTL-preserving mutations, auto-delete on empty, wrong-type and error-order parity | scaffold |
| `internal/server/set_commands_test.go` | Set semantics: add/remove/membership/cardinality, missing-key replies, wrong-type matrix, SPOP/SRANDMEMBER count parsing and random properties, TTL preservation and expiry | scaffold |
| `internal/server/collection_memory_test.go` | Regression tests that collection edits clear removed backing slots, release oversized storage, and keep hash/set indexes valid after delete/re-add/pop, including >512-field/member delete sweeps | scaffold |
| `internal/server/collection_bench_test.go` | APPEND growth plus single-field HDEL/SREM benchmarks over 10k-element collections (guards against reintroducing full scans/index rebuilds) | scaffold |
| `internal/server/ttl_commands.go` | TTL/PTTL (nearest-second rounding), PERSIST, EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT (option parsing order, NX/XX/GT/LT conditions, non-volatile infinite-TTL rules, second/millisecond and relative/absolute overflow boundaries, delete-on-past deadline) using the store clock, and DBSIZE (O(1) physical key count) | scaffold |
| `internal/server/config_commands.go` | `CONFIG GET hz` / `CONFIG SET hz`: Redis 1–500 clamp, parse/unknown/arity errors, and timer-reset notification to the executor | scaffold |
| `internal/server/config_commands_test.go` | CONFIG hz get/set, clamping, case-insensitive parameter, and captured error bodies | scaffold |
| `internal/server/ttl_commands_test.go` | Captured TTL behaviour: -2/-1 replies, rounding buckets, fake-clock lazy expiry after PX, relative/absolute variants, condition matrix incl. past deadlines, option conflicts and precedence over malformed integers, overflow per command, KEEPTTL/SET interplay; lazy-delete coverage for every read and read-modify-write command; DBSIZE physical-count consistency | scaffold |
| `internal/store/store.go` | Keyspace owned by the executor (no locks by construction): typed `Value` entries (`KindString`/`KindList`/`KindHash`/`KindSet`) with hash/set indexes in `data`, a separate `expires` dict, an injected clock (`NewWithClock`, `NowMs`), `Lookup/Type/Set/SetWithTTL/SetPreservingTTL/Delete/Exists/Len`, `Keys` (live names without deleting expired entries), `RandomKey` (deletes expired candidates), `Rename` (value plus TTL transfer), `Flush`, `Scan` (delegated to `scan.go`), `ExpiresAt/TTLms/SetExpire/Persist`, and `ExpireSample` for the active cycle; strict `now > deadline` lazy expiry; `Len` is the O(1) physical key count used by DBSIZE | scaffold (hand-own as value types land) |
| `internal/store/scan.go` | Virtual power-of-two SCAN buckets with Redis's reverse-binary cursor, resizing by physical key count between calls, pattern-before-expiry filtering, and type filtering after expiry | scaffold |
| `internal/store/scan_test.go` | Reverse-binary cursor sequence, full keyspace coverage, growth/shrink stable-key invariants, pattern-before-expiry behavior, type filtering, bucket sizing, and empty scans | scaffold |
| `internal/store/store_test.go` | Keyspace operations incl. overwrite/delete/physical len; typed string/list/hash/set lookup and overwrite; fake-clock expires-dict preservation, lazy delete from both dicts on every accessor, `SetPreservingTTL` semantics, TTL clearing on overwrite, active-sample deletion/live-key safety, KEYS exclusion of expired keys without deletion, RANDOMKEY expiry cleanup, rename TTL transfer, and flush | scaffold |
| `internal/store/doc.go` | Keyspace, value types, WRONGTYPE, empty-collection auto-delete | scaffold → hand-own |
| `internal/persist/doc.go` | Framed AOF, group commit, recovery, rewrite (ADR-004/007/010) | scaffold → hand-own at P6 |
| `internal/expire/doc.go` | Active expiry charter: sample 20, repeat >25%, per-cycle budget (ADR-005) | scaffold → hand-own at P3 |
| `internal/expire/cycle.go` | Active-expiry cycle using the injected clock for its budget; calls the store's volatile-key sampler | scaffold |
| `internal/expire/cycle_test.go` | Fake-clock cycle tests: deletion/repeat, 25% stop, budget exhaustion, live-key safety | scaffold |
| `internal/evict/doc.go` | Approximated LRU, eviction pool, policies (ADR-006) | scaffold → hand-own at P7 |
| `internal/repl/doc.go` | PSYNC, backlog ring, replica apply (ADR-007/010) | scaffold → hand-own at S1 |
| `internal/events/doc.go` | Event-tap types + rotated JSONL sink (ADR-014) | scaffold |
| `internal/client/doc.go` | Minimal RESP2 client for bridge + replica (ADR-017) | scaffold |
| `internal/metrics/doc.go` | Counters + INFO assembly | scaffold |

## Documentation (`docs/`)

| Path | What it is | Ownership |
|---|---|---|
| `docs/PLAN.md` | Single source of truth: scope, phases, exit criteria, protocol, DoD | scaffold |
| `docs/DECISIONS.md` | ADR log (ADR-001…020): context, options, why chosen/rejected, trade-offs, proof | scaffold |
| `docs/PROJECT_LOG.md` | Session log — deliverables, issues (STAR shape), exit evidence, next task | scaffold |
| `docs/FILE_MANIFEST.md` | This file | scaffold |
| `docs/INTERVIEW_QBANK.md` | Rehearsal Q&A + boundary-of-knowledge list | scaffold |
| `docs/COMPATIBILITY.md` | Pinned versions, compat claims, deliberate divergences, licensing note (stub until P9.1) | scaffold |

Planned docs from `PLAN.md` §13, arriving at their phases: `UI-DESIGN.md` (U0), `ARCHITECTURE.md` (P10.2), `BENCHMARKS.md` (P9.4), `FAILURE_MODES.md` (S3.3).

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
| `tests/chaos/.gitkeep` | P6.4 (crash), S2.4 (replica chaos) |
| `tests/fuzz/.gitkeep` | P9.3 (soak + committed regression corpus; the P1.6 harness itself is `internal/protocol/fuzz_test.go`) |
| `tests/tcl/run-expire.sh` | Suite-aware runner: downloads the pinned Redis 7.4.11 tarball into a git-ignored cache, verifies SHA-256, builds/starts Antares, and runs the selected `expire` (default) or `incr` tests |
| `tests/tcl/run-incr.sh` | Thin wrapper that runs the selected official `unit/type/incr` suite through `run-expire.sh` |
| `tests/tcl/harness.tcl` | Thin Tcl glue (`start_server`, `tags`, `test`, `start_cluster`) over the official Redis Tcl client/assertion helpers, sourcing the selected suite (`unit/expire.tcl` or `unit/type/incr.tcl`); no Redis code copied into the repo |
| `tests/tcl/expire_tests.txt` | Allowlist of 39 official expiry tests supported by the current command surface |
| `tests/tcl/incr_tests.txt` | Allowlist of 26 official `unit/type/incr` tests; OBJECT/DEBUG/SETRANGE-dependent tests are excluded |
| `bench/scripts/.gitkeep` | P9.4 |
| `bench/results/.gitkeep` | P9.4 (commit-keyed raw outputs) |
| `web/src/.gitkeep` | U3 (SPA scaffold) |
| `web/e2e/.gitkeep` | U12 (Playwright) |
| `web/tokens/.gitkeep` | U0/U3 (design tokens) |
| `deploy/demo/.gitkeep` | U2/M2 (demo compose profile + seeds) |

## Hand-own commitments (PLAN §8)

When these files exist, they carry the `hand-own` marker here and nothing is "done" on this path until the owner has reviewed every line and can defend it:

- `internal/protocol/` — RESP parser (`parse.go`), encoder (`encode.go`), error strings (`errors.go`); owner line-by-line review pending
- `internal/persist/` — frame format, writer, recovery, rewrite
- `internal/expire/` — lazy + active cycle
- `internal/evict/` — approximation, pool, policies
- `internal/server/` — executor (`executor.go`; owner line-by-line review pending) + transaction semantics
- `internal/repl/` — handshake, backlog, offsets, replica apply
