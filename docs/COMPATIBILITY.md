# Compatibility — Antares vs Redis 7.4

> Purpose: the compatibility contract in one place — what is pinned, what is verified, what deliberately diverges, and how to reproduce each claim.
> Status: stub — claims table stays empty until their verifying phases run; final pass at P10.2. Nothing in this file is a claim until it has a verification run behind it.

---

## Pinned versions

| Component | Version | Where pinned |
|---|---|---|
| Reference Redis (test-time only, never shipped) | 7.4.11 — digest `sha256:4fa24486b8bcca8eec45ee0eb166edc674795e53a2b53d1a9ef263eecebaac85` | `docker-compose.yml` |
| Go toolchain | 1.27.1 | `go.mod` (`ci.yml` reads it via `go-version-file`) |
| Host redis-cli (client only — never the reference server) | 8.10.2 | local install |
| Tcl test suite | vendored subset, pinned at P9.2 | `tests/tcl/` |

Rationale for pinning strategy: ADR-020 (tooling policy, digest pinning) and PLAN §12 (version-drift risk).

## Compatibility claims (verified rows expand by phase)

| Claim | Verified by | Status |
|---|---|---|
| redis-cli works interactively and `redis-cli --stat` is clean | P5.3 + diff suite | pending |
| go-redis differential: 0 mismatches across implemented commands | `make diff-test` (P2.6 → P9.1) | verified for the string and TTL surface (P3.1, 198 steps + expiry); expands each phase |
| Official Tcl `unit/expire` and `unit/type/incr` fully pass | `make tcl` (P3.4, P9.2) | pending |
| Crash test: zero acked-write loss with `fsync=always` (≤1s window `everysec`) | `make crash-test` (P6.4) | pending |
| Replica dataset equality after full resync + `+CONTINUE` continuity | S1.3/S2.2 tests | pending |
| Benchmarks ≥50–80% of real Redis on the same machine | `make bench` (P9.4) | pending |

## Deliberate divergences (by design, each with a decision record)

| Divergence | Observable behavior | Decision |
|---|---|---|
| WATCH semantics | Value-snapshot compare: a write followed by restoring identical bytes does **not** abort EXEC (real Redis aborts). All realistic patterns behave the same | ADR-008 |
| RESP3 | Not implemented beyond a `HELLO` downgrade stub advertising `proto: 2`; RESP3-only client behaviors unavailable | ADR-003 |
| COMMAND introspection | `COMMAND`, `COMMAND DOCS`, and `COMMAND INFO` return an empty array; redis-cli connects and runs interactively, but command hints/completion are empty | P2.1 stub (PLAN §5); full metadata is out of scope |
| SET EXAT/PXAT | Redis accepts the absolute-expiry SET options; Antares returns `ERR syntax error` because P2.3 scope is EX/PX | PLAN §5 P2.3; revisit alongside the P3 absolute-expiry commands |
| INCRBYFLOAT precision | Antares always computes with IEEE binary128 (113-bit mantissa), matching C `long double` on the pinned aarch64 reference. On x86-64 Redis uses 80-bit `long double`, so its last printed digits can differ from this build | ADR-021; required for bit-exact parity with the pinned oracle |
| Runtime bulk limit | `proto-max-bulk-len` is fixed at 512 MiB in both the parser and APPEND; `CONFIG SET proto-max-bulk-len` is unavailable until the P5.3 CONFIG allowlist | PLAN §5 P5.3; revisit with CONFIG support |
| Persistence file formats | Framed AOF and framed snapshot stream are **not** Redis-compatible files: Antares cannot load existing `.rdb`/`.aof`, and Redis cannot load ours | ADR-004, ADR-007 |
| Value encodings | No listpack/intset/embstr encodings (non-goal); the exact `OBJECT ENCODING` surface will be decided at P2/P5 and documented here when it lands | ADR-012 |
| Missing command families | Lua, cluster, Sentinel, Streams, pub/sub, GEO/HLL/Bitmaps, ACL — commands return the standard unknown-command error | ADR-012, ADR-019 |
| Sharded/striped keyspace | Single executor, one keyspace — no slot model | ADR-002, ADR-012 |
| Full-resync payload | Snapshot transferred as a framed command stream (reuses AOF rewrite format) — larger than RDB for big values | ADR-007 |
| Slow clients | When `-idle-timeout` is set, a connection blocked writing to a client that stopped reading is closed after that period (default 0 = no bound); real Redis's default never closes a slow client and buffers output unboundedly | ADR-002 goroutine-per-conn model; revisit with output-buffer caps at S2.3 |

## Open questions (decide at the phase noted)

- What should `INFO server` report as `redis_version`? A 7.4-compatible string (some clients gate features on it) vs a suffixed identifier. Decide at P5.3 with go-redis behavior evidence.
- OOM error strings must match Redis captures — P7.2 (protocol and command error strings were pinned by live capture at P1.5 and extended at the P1 audit: unknown command incl. 128-byte name and args truncation, arity, `-ERR Protocol error: …` incl. raw non-ASCII byte passthrough, CRLF sanitization, over-cap connection refusal).
- SCAN cursor semantics (reverse-binary) — document observed ordering guarantees at P5.2.
- `HELLO` reply shape vs client expectations (go-redis, Jedis) — P5.3 differential.

## Licensing & safety note

- Antares server code: MIT (`LICENSE`).
- Reference Redis image: RSALv2/SSPLv1 (source-available, free to use) — used locally for development and differential testing **only**; never redistributed; no Redis code is linked or copied. See ADR-020.
- Tooling policy: OSS or free-to-use only (ADR-020); container images digest-pinned; ports bound to `127.0.0.1` only.

## How claims will be reproduced

- `make verify` (umbrella, P9.4) runs diff + Tcl + crash + fuzz + bench and writes `claims.json` `{value, source command, timestamp, commit, variance}` (ADR-018).
- Every number shown in the Observatory renders only from `claims.json` — a number without provenance does not render.
