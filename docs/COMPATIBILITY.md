# Compatibility — Antares vs Redis 7.4

> Purpose: the compatibility contract in one place — what is pinned, what is verified, what deliberately diverges, and how to reproduce each claim.
> Status: living document — verified rows accumulate as phases complete; final pass at P10.2. Nothing in this file is a claim until it has a verification run behind it.

---

## Pinned versions

| Component | Version | Where pinned |
|---|---|---|
| Reference Redis (test-time only, never shipped) | 7.4.11 — digest `sha256:4fa24486b8bcca8eec45ee0eb166edc674795e53a2b53d1a9ef263eecebaac85` | `docker-compose.yml` |
| Go toolchain | 1.27.1 | `go.mod` (`ci.yml` reads it via `go-version-file`) |
| Host redis-cli (client only — never the reference server) | 8.10.2 | local install |
| Tcl test suite | selected official `unit/expire` and `unit/type/incr` subsets from Redis 7.4.11; downloaded at run time, SHA-256 verified, never vendored | `tests/tcl/run-expire.sh` |

Rationale for pinning strategy: ADR-020 (tooling policy, digest pinning) and PLAN §12 (version-drift risk).

## Compatibility claims (verified rows expand by phase)

| Claim | Verified by | Status |
|---|---|---|
| redis-cli works interactively and `redis-cli --stat` is clean | P5.3 + diff suite | pending |
| go-redis differential: 0 mismatches across implemented commands | `make diff-test` (P2.6 → P9.1) | verified for the string, list, hash, set, TTL, keyspace, and SCAN surface (P5.2, 198 string/TTL + 91 list + 71 hash + 99 set + 171 cross-type + 240 sequence + 47 keyspace steps + 32 SCAN checks + expiry and lazy-read scenarios); expands each phase |
| DBSIZE | unit tests (P3.2) | verified: O(1) physical key count, matching Redis `dictSize`; expired keys are counted until lazy access or the active cycle removes them |
| SCAN | differential + invariant tests (P5.2) | verified: `COUNT` (>0, last option wins), case-insensitive `MATCH`/`TYPE`, unknown types returning no keys, cursor parsing including Redis's `string2ull` quirks, and pattern-before-expiry deletion. Full-iteration key sets match the reference for count/match/type combinations; growth/shrink tests prove termination and that keys present for the whole iteration are seen at least once. Cursor values and per-call batch composition are intentionally not byte-diffed because they depend on the hash table |
| KEYS / RENAME / RENAMENX / RANDOMKEY / FLUSHDB / FLUSHALL | differential + unit tests (P5.1) | verified: Redis's glob matcher (`*`, `?`, classes/ranges/negation/escapes and malformed-class edge cases), expired keys excluded from KEYS, rename moving values and TTLs across strings/lists/hashes/sets with destination overwrite/TTL clearing, missing-key errors, RENAMENX destination rules and same-key forms, random live-key/null replies, and FLUSHDB/FLUSHALL `SYNC`/`ASYNC` parsing with syntax errors (47 differential steps) |
| Lists | differential + unit tests (P4.1) | verified: push/pop with counts, ranges, indexed access/update, LREM/LTRIM, negative indices, TTL preservation, empty-list auto-delete, wrong-type and error-order parity (91 differential steps) |
| Hashes | differential + unit tests (P4.2) | verified: HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS, insertion-order iteration, overwrite/add counts, duplicate-field HDEL, TTL preservation, empty-hash auto-delete, wrong-type and error-order parity (71 differential steps); randomized HRANDFIELD forms are property-tested for cardinality, distinctness, and membership rather than exact-diffed |
| Sets | differential + unit tests (P4.3) | verified: SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER, add/remove counts, positive/negative rand counts, TTL preservation, empty-set auto-delete, wrong-type and error-order parity (99 differential steps; unordered replies sorted before comparison); random member selection is property-tested rather than exact-diffed |
| Cross-type WRONGTYPE and empty collections | differential matrix (P4.4) | verified: 171 cases apply every list/hash/set command to strings and the other collection types, string reads/writes to every collection, and every mutator that can empty a collection; replies, TYPE transitions, EXISTS, and TTL deletion match the reference |
| Active expiry | fake-clock cycle tests (P3.3) | verified: samples 20 volatile keys, repeats while >25% expired, respects a per-cycle time budget, and never deletes live keys |
| CONFIG hz | unit tests (P3.3) | verified: `CONFIG GET hz` and `CONFIG SET hz` match Redis's 32-bit range validation, 1–500 clamp, parse errors, and timer reset |
| Selected official Tcl `unit/expire` subset passes | `make tcl-expire` (P3.4, extended P4.1) | verified: 39 tests pass, 28 unselected tests skipped (commands not yet implemented) |
| Selected official Tcl `unit/type/incr` subset passes | `make tcl-incr` (P4.5) | verified: 26 tests pass, 7 skipped (OBJECT/DEBUG/SETRANGE-dependent tests not selected) |
| Official Tcl `unit/expire` and `unit/type/incr` fully pass | `make tcl` (P9.2) | pending |
| Crash test: zero acked-write loss with `fsync=always` (≤1s window `everysec`) | `make crash-test` (P6.4) | pending |
| Replica dataset equality after full resync + `+CONTINUE` continuity | S1.3/S2.2 tests | pending |
| Benchmarks ≥50–80% of real Redis on the same machine | `make bench` (P9.4) | pending |

## Deliberate divergences (by design, each with a decision record)

| Divergence | Observable behavior | Decision |
|---|---|---|
| WATCH semantics | Value-snapshot compare: a write followed by restoring identical bytes does **not** abort EXEC (real Redis aborts). All realistic patterns behave the same | ADR-008 |
| RESP3 | `HELLO` is not implemented yet (returns the standard unknown-command error); RESP3-only client behaviors unavailable | ADR-003; stub planned for P5.3 |
| COMMAND introspection | `COMMAND`, `COMMAND DOCS`, and `COMMAND INFO` return an empty array; redis-cli connects and runs interactively, but command hints/completion are empty | P2.1 stub (PLAN §5); full metadata is out of scope |
| CONFIG surface | Only `CONFIG GET hz` and `CONFIG SET hz` are implemented; other parameters return an empty GET result or `Unknown option`, rather than Redis's full config matrix | P3.3 partial; full allowlist at P5.3 |
| SET EXAT/PXAT | Redis accepts the absolute-expiry SET options; Antares returns `ERR syntax error` because P2.3 scope is EX/PX | PLAN §5 P2.3; revisit at P5.3 |
| LSET index truncation | Indices outside the C `int` range are rejected with `ERR index out of range`; Redis truncates them to a 32-bit `int` (for example `4294967295` becomes `-1` and updates the tail) | Intentional safety choice: Antares keeps `int64` indices and treats C truncation as a Redis bug rather than compatibility |
| INCRBYFLOAT precision | Antares always computes with IEEE binary128 (113-bit mantissa), matching C `long double` on the pinned aarch64 reference. On x86-64 Redis uses 80-bit `long double`, so its last printed digits can differ from this build | ADR-021; required for bit-exact parity with the pinned oracle |
| Runtime request limit | `proto-max-bulk-len` is fixed at 512 MiB, and each request has a 1 GiB aggregate budget (payload plus 32 bytes per argument, Redis's default `client-query-buffer-limit`); a larger request closes the connection without a reply. `CONFIG SET` for either limit is unavailable until the P5.3 CONFIG allowlist | PLAN §5 P5.3; revisit with CONFIG support |
| Tcl coverage | `make tcl-expire` runs a 39-test allowlist and `make tcl-incr` a 26-test allowlist; tests requiring commands beyond the current surface (SETEX/PSETEX/GETEX, SETRANGE, EXPIRETIME, DEBUG/OBJECT, HSCAN/SSCAN/ZSCAN, AOF/repl, cluster) are skipped until their phases | PLAN §5 P3.4/P4.5/P9.2 |
| Persistence file formats | Framed AOF and framed snapshot stream are **not** Redis-compatible files: Antares cannot load existing `.rdb`/`.aof`, and Redis cannot load ours | ADR-004, ADR-007 |
| Value encodings | No listpack/intset/embstr encodings (non-goal); the exact `OBJECT ENCODING` surface will be decided at P2/P5 and documented here when it lands | ADR-012 |
| Missing command families | Lua, cluster, Sentinel, Streams, pub/sub, GEO/HLL/Bitmaps, ACL — commands return the standard unknown-command error | ADR-012, ADR-019 |
| Sharded/striped keyspace | Single executor, one keyspace — no slot model | ADR-002, ADR-012 |
| Full-resync payload | Snapshot transferred as a framed command stream (reuses AOF rewrite format) — larger than RDB for big values | ADR-007 |
| Slow clients | When `-idle-timeout` is set, a connection blocked writing to a client that stopped reading is closed after that period (default 0 = no bound); real Redis's default never closes a slow client and buffers output unboundedly | ADR-002 goroutine-per-conn model; revisit with output-buffer caps at S2.3 |
| Random reply size | `HRANDFIELD`/`SRANDMEMBER` counts that would build more than 1,000,000 reply elements return `ERR count is out of range`; Redis builds the full reply | Bounded random-reply guard (P4.2/P4.3); revisit with client-output-buffer limits |
| FLUSHDB/FLUSHALL ASYNC | Both `ASYNC` and `SYNC` are accepted and reply `OK`, but Antares frees the keyspace synchronously (no lazy-free worker); replies and post-flush visibility match Redis | P5.1: single keyspace and no lazy-free thread yet; revisit if flush latency becomes observable |

## Known implementation limits

- Lists are slice-backed with front headroom: `LPUSH`/`RPUSH`/`LPOP`/`RPOP` amortize to O(1) while capacity lasts, and `LTRIM`/`LREM` clear and release removed slots. A few shifts still copy when headroom is exhausted; a deque remains a later optimization, not a compatibility difference.
- Hash field order is insertion order for hashes up to 512 fields. Above that, `HDEL` uses swap-remove, so `HGETALL`/`HKEYS`/`HVALS` order after a delete is no longer insertion order; Redis also gives no order guarantee for hashtable-encoded hashes.
- `SCAN` walks a virtual power-of-two bucket table over the Go map with Redis's reverse-binary cursor. Cursor values and batch sizes therefore differ from Redis, but the contract is the same: a returned cursor of 0 means termination, every key present throughout the iteration is returned at least once, duplicates are possible, and keys added or removed mid-iteration are not guaranteed. The table size follows the physical key count between calls, so growth and shrink are safe; cursors are not portable across restarts.

## Open questions (decide at the phase noted)

- What should `INFO server` report as `redis_version`? A 7.4-compatible string (some clients gate features on it) vs a suffixed identifier. Decide at P5.3 with go-redis behavior evidence.
- OOM error strings must match Redis captures — P7.2 (protocol and command error strings were pinned by live capture at P1.5 and extended at the P1 audit: unknown command incl. 128-byte name and args truncation, arity, `-ERR Protocol error: …` incl. raw non-ASCII byte passthrough, CRLF sanitization, over-cap connection refusal).
- `HELLO` reply shape vs client expectations (go-redis, Jedis) — P5.3 differential.

## Licensing & safety note

- Antares server code: MIT (`LICENSE`).
- Reference Redis image: RSALv2/SSPLv1 (source-available, free to use) — used locally for development and differential testing **only**; never redistributed; no Redis code is linked or copied. The Tcl runner downloads the pinned Redis test source into a git-ignored cache at run time and does not redistribute it. See ADR-020.
- Tooling policy: OSS or free-to-use only (ADR-020); container images digest-pinned; ports bound to `127.0.0.1` only.

## How claims will be reproduced

- `make verify` (umbrella, P9.4) runs diff + Tcl + crash + fuzz + bench and writes `claims.json` `{value, source command, timestamp, commit, variance}` (ADR-018).
- Every number shown in the Observatory renders only from `claims.json` — a number without provenance does not render.
