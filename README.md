<div align="center">

# Antares

**A Redis-compatible server, built from scratch in Go — with an Observatory on the way.**

*α Scorpii — the red star called "the rival of Mars": a red rival to Redis, built to be observed through its Observatory.*

[![ci](https://github.com/silent-knight19/Antares/actions/workflows/ci.yml/badge.svg)](https://github.com/silent-knight19/Antares/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.27.1-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](#license)

</div>

---

## Contents

- [What is Antares?](#what-is-antares)
- [Project status](#project-status)
- [Architecture](#architecture)
- [Repository layout](#repository-layout)
- [Getting started](#getting-started)
- [Development commands](#development-commands)
- [Roadmap](#roadmap)
- [Scope and non-goals](#scope-and-non-goals)
- [Documentation](#documentation)
- [License](#license)

## What is Antares?

Antares is a Redis-compatible server written from scratch in Go: the RESP2 protocol, 71 commands covering strings, lists, hashes, sets, transactions, TTL, DBSIZE, CONFIG, INFO, HELLO 2, CLIENT stubs, and PING/ECHO/COMMAND, plus lazy and active expiry. Framed AOF persistence, approximated-LRU eviction, and transactions are implemented; replication is planned. The **Antares Observatory** — a separate showcase and tooling service plus web UI — is being built alongside it; its M1 dev console (bridge + Playground + Keyspace SPA) runs today via `make showcase-dev`.

Target shape:

```text
redis-cli / clients ──RESP──▶ Antares ──▶ event tap (JSONL, opt-in)
go-redis (verify)   ──RESP──▶ Antares + real Redis (differential)
Observatory ──own client + INFO + event tap + claims──▶ SPA (SSE + REST)
```

The M1 dev console uses the client + `INFO` + REST/SSE bridge. The opt-in JSONL event tap is live in the server (`-event-log path.jsonl`); the Observatory views that consume it are still planned (M2).

Every claim the project makes is meant to be machine-verifiable — differential tests against real Redis, crash tests, benchmarks, and CI-verified numbers displayed in the Observatory UI.

> **Why the name?** Antares — α Scorpii — is the red star called "the rival of Mars": a red rival to Redis.

## Project status

> **P1–P8, P9.1, and P9.2 complete — the wire path is done and fuzz-tested (15-min fuzz clean, 166M executions), commands run through a single keyspace-owning executor, and the edge-hardened string, list, hash, set, and keyspace surfaces are live: SET/GET/DEL/EXISTS/TYPE, MSET/MGET, the INCR family including a bit-exact INCRBYFLOAT, APPEND/STRLEN/GETSET/SETNX, LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM, HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD, SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER, KEYS with a Redis-compatible glob matcher, SCAN with reverse-binary cursors and MATCH/COUNT/TYPE, RENAME/RENAMENX, RANDOMKEY, FLUSHDB/FLUSHALL, lazy and active expiry, DBSIZE, an INFO section surface, a CONFIG GET/SET allowlist, HELLO 2, CLIENT stubs, and exact wrong-type/overflow/limit errors.** TTL state sits in a separate expires dict, with TTL/PTTL/PERSIST and the full EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT family (NX/XX/GT/LT). Domain time now comes from one injected clock (ADR-009), so expiry tests advance time instead of sleeping. A committed differential harness (`make diff-test`) runs 198 string/TTL steps, 91 list steps, 71 hash steps, 99 set steps, a 171-case cross-type matrix, 240 generated collection-sequence steps, a 47-step keyspace corpus, a 32-check SCAN corpus, and an INFO/CONFIG/CLIENT field diff, plus expiry/lazy-read scenarios against the pinned Redis 7.4.11 with zero mismatches; 1056 one-off checks also passed. `make tcl` runs the selected official Tcl subsets (39 `unit/expire` + 26 `unit/type/incr`, 65/65 green) with a per-command report; the pinned 7.4.11 test source is downloaded and SHA-256 verified at run time, never vendored. PING/ECHO plus the `COMMAND` stub work. **The Observatory M1 dev console (U0–U4, U6a) is now live**: a `internal/client` RESP2 client, the `cmd/observatory` REST/SSE bridge, and a React/Vite/Tailwind SPA whose Playground executes commands with a wire trace and latency, and whose Keyspace browses SCAN results with type/TTL/size previews and a read-only detail drawer (`make showcase-dev`). The project is under active construction. **P6 adds framed AOF persistence**: `make crash-test` runs 100× SIGKILL and proves zero acked-write loss under `-appendfsync always`, and the server supports `-appendonly`/`-aof-file`/`-appendfsync`, startup replay with torn-tail truncation, corrupt-frame refusal (opt out with `-aof-repair`), MISCONF write rejection after an AOF failure, and `BGREWRITEAOF`. **P7.1 adds logical keyspace memory accounting**: `INFO memory` now reports `used_memory`/`used_memory_overhead`/`used_memory_dataset` from a model of the retained keyspace (amortized map/slice overhead plus payload) that sits within 10% of Go's heap, replacing the process-wide runtime number as the input `maxmemory` enforces. **P7.2 enforces `maxmemory`**: memory-growing commands (Redis `CMD_DENYOOM` — SET/MSET/SETNX/GETSET/APPEND, the INCR family, HSET, LPUSH/RPUSH/LSET, SADD) reply with Redis's exact `-OOM command not allowed when used memory > 'maxmemory'.` once the keyspace exceeds the limit, while reads, deletes, `CONFIG`, and non-denyoom writes still run. **P7.3 adds approximated LRU eviction**: under `allkeys-lru`/`volatile-lru` every key carries a 24-bit access clock refreshed on lookup and write, the executor samples 5 keys into a bounded most-idle pool and frees victims before rejecting (appending a DEL to the AOF so a restart cannot resurrect them, and emitting a versioned event when `-event-log` is enabled), so an over-limit write is admitted instead of refused; the LFU/random/TTL policy names are rejected by `CONFIG SET` because only `noeviction`/`allkeys-lru`/`volatile-lru` are implemented. **P7.4 validates the approximation**: a hot set under 1000 rounds of cold churn is retained at 0.98–1.00 (gate >0.90), and `INFO stats` reports live `evicted_keys`. **P8 adds transactions:** `MULTI`/`EXEC`/`DISCARD`, `WATCH`/`UNWATCH`, queue-time errors and `EXECABORT`, per-connection state, one executor turn and one AOF frame per `EXEC`, and `WATCH` invalidation by value snapshot (ADR-008/028); verified by unit, DST (10k interleavings), race, and differential runs against Redis 7.4.11. **P9.1 hardens the differential harness**: a reproducible seeded generator (`DIFF_SEED`/`DIFF_ITERS`) mixes string, list, hash, set, TTL, and keyspace operations over missing/wrong-type/binary/long/overflow inputs, deterministic empty/NUL/1 KiB/non-ASCII edge keys and the PING/ECHO/HELLO/CLIENT surfaces are covered, and all relaxation rules live in one audited `canonical.go` (exact by default; sort unordered replies and hash pairs; bucket live TTL durations).

| Works today | Arrives later |
|---|---|
| `make build` (both binaries), `make test`, `make test-race`, `make vet`, `make fmt`, `make tidy`, `make fuzz`, `make diff-test` (strings + lists + hashes + sets + transactions + TTL + keyspace + SCAN + INFO + seeded generator + edge/connection surface), `make tcl` (65 selected official expiry/incr tests + per-command report), `make crash-test` (100× SIGKILL AOF durability) | Broader Tcl subsets (documented), replication |
| TCP server speaking RESP2 — parser with a 31-case corpus captured from real Redis + fuzz harness, pooled reply writer, capture-exact error strings, pipeline-aware flushing; PING/ECHO/COMMAND answered through the registry and single keyspace-owning executor (case-insensitive lookup, Redis arity checks, `COMMAND` stub for redis-cli) | `make verify`, `make crash-test`, `make tcl`, `make bench`, `make showcase` |
| Edge-hardened string core — SET (EX/PX/NX/XX/GET/KEEPTTL), GET, DEL, EXISTS, TYPE, MSET/MGET, INCR/DECR/INCRBY/DECRBY/INCRBYFLOAT, APPEND, STRLEN, GETSET, SETNX; lists — LPUSH/RPUSH/LPOP/RPOP (with counts), LRANGE, LLEN, LINDEX, LSET, LREM, LTRIM, negative ranges, error-order parity, TTL preservation, auto-delete; hashes — HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD with insertion-order iteration; sets — SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER with unordered iteration and random pop/rand semantics; keyspace — KEYS glob matching, SCAN with MATCH/COUNT/TYPE and Redis's reverse-binary cursor, RENAME/RENAMENX with TTL transfer, RANDOMKEY, FLUSHDB/FLUSHALL; separate expires dict with lazy delete-on-access, TTL/PTTL/PERSIST, EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT incl. NX/XX/GT/LT, physical DBSIZE, active expiry (hz=10), INFO sections, CONFIG GET/SET allowlist, HELLO 2, and CLIENT stubs; empty/binary/1 MiB values, wide batches, overflow parity, 512 MiB bulk + 1 GiB request guards, pinned WRONGTYPE body; `make diff-test` green (198 string/TTL + 91 list + 71 hash + 99 set + 171 cross-type + 240 sequence + 47 keyspace steps + 32 SCAN checks + INFO diff + transactions + P9.1 generator/edge/surface + expiry + lazy reads), 1056 one-off live checks clean, and 65 selected official Tcl expiry/incr tests green via `make tcl` with per-command reports | Broader Tcl subsets, replication |
| CI: gofmt check + `vet` + `build` + race-tested tests + differential harness | Full Observatory showcase (M2) and Playwright E2E (U12) |
| Observatory M1 dev console — `internal/client` RESP2 client, `cmd/observatory` REST/SSE bridge, React/Vite/Tailwind SPA with Playground (history, presets, wire trace, latency) and Keyspace (SCAN table, type/TTL/size preview, read-only detail drawer); `make ui-build`, `make showcase-dev`, `make seed` | Event-tap showcase views, metrics charts, durability lab, replication lab (M2/M3) |
| `docker compose up` — pinned reference Redis 7.4.11 | |

The Observatory bridge (`cmd/observatory`) is live as the M1 dev console and serves the built SPA from `web/dist` (or a placeholder page when it has not been built); `cmd/antares` is the real server.

## Architecture

**Server** (from `docs/PLAN.md` §3.1). The wire path, strings, lists, hashes, sets, keyspace commands, TTL, PING/ECHO/COMMAND, framed AOF persistence, and LRU eviction exist today; the rest is the target design:

- **Concurrency** — goroutine per connection for I/O → command channel → a single executor goroutine owning the keyspace, reproducing Redis-like sequential semantics with race-detector-clean guarantees.
- **Protocol** — RESP2 (inline, bulk strings, arrays, integers); `HELLO 2` answers the modern handshake, while RESP3 remains unavailable.
- **Expiration** — lazy on access plus active sampling (hz=10, sample 20 keys, repeat while >25% expired, per-cycle budget); `CONFIG SET hz` adjusts the cycle rate.
- **Persistence** — append-only AOF with framed records `[u32 len][u32 CRC32][RESP payload]`, group commit, fsync policies `always` / `everysec` / `no`; recovery replays the valid prefix and truncates at the first bad frame.
- **Eviction** — approximated LRU under `maxmemory`: 24-bit access clock, sample 5, eviction pool; policies `noeviction` (default), `allkeys-lru`, `volatile-lru`.
- **Transactions** — `MULTI` / `EXEC` / `DISCARD` with dirty + `EXECABORT` semantics; `WATCH` via value-snapshot compare (a documented divergence).
- **Replication** — `REPLICAOF`, PSYNC handshake, `master_repl_offset`, 1MB backlog ring, `+FULLRESYNC` and `+CONTINUE` partial resync, `REPLCONF ACK`, read-only replicas, slow-replica protection.

**Antares Observatory** (from `docs/PLAN.md` §3.2):

- A separate `cmd/observatory` bridge process — never embedded in the server — that talks to Antares (and real Redis, side by side) through its own RESP client, tails the opt-in event log, polls INFO, and serves a static SPA over REST + SSE.
- The SPA is React + TypeScript + Vite + Tailwind + uPlot, with sections for Overview, Playground, Keyspace, Replication Lab, Durability Lab, Metrics/Bench, Decision Journal, and Interview Mode.
- Hero metrics render only from `claims.json` generated by verification runs — no hand-typed numbers.

The design rationale behind every decision above is recorded in [`docs/DECISIONS.md`](docs/DECISIONS.md).

## Repository layout

```text
antares/
├── cmd/
│   ├── antares/        Redis-compatible server entry point
│   └── observatory/    Observatory bridge: REST + SSE + SPA
├── internal/
│   ├── clock/          injected time source + fake clock (ADR-009)
│   ├── protocol/       RESP2 parsing/encoding, fuzz corpus
│   ├── server/         listener, connection goroutines, executor loop
│   ├── store/          keyspace, string/list/hash/set values, WRONGTYPE
│   ├── persist/        AOF frames, writer, recovery, rewrite
│   ├── expire/         lazy + active expiration cycle
│   ├── evict/          approximated LRU, eviction pool, policies
│   ├── repl/           PSYNC, backlog, replica apply
│   ├── events/         event tap types + JSONL sink
│   ├── client/         minimal RESP client (bridge + replica)
│   └── metrics/        counters, INFO assembly
├── web/                SPA source, end-to-end tests, design tokens
├── tests/              unit · differential · chaos · fuzz · tcl
├── bench/              benchmark scripts + commit-keyed results
├── deploy/demo/        docker-compose demo profile, seed scripts
├── docs/               PLAN.md, DECISIONS.md, and the living docs
├── docker-compose.yml  pinned reference Redis 7.4.11
├── Makefile            build/test/run entry points
└── .github/workflows/  CI
```

## Getting started

**Prerequisites**

- [Go 1.27.1](https://go.dev) (per `go.mod`)
- A Docker-compatible runtime — [colima](https://github.com/abiosoft/colima) recommended (MIT; matches the OSS-only tooling policy, ADR-020). Needed only for the pinned reference Redis, not for build or tests.
- `tclsh` 8.5+ and `curl` only for `make tcl`; the pinned Redis test source is downloaded into a git-ignored cache, never vendored.

**Clone, build, test**

```bash
git clone https://github.com/silent-knight19/Antares.git
cd Antares

make build       # build server + observatory binaries into bin/
make test        # go test ./...
make test-race   # go test -race ./...
```

**Run**

```bash
make run              # run the antares server on 127.0.0.1:6380
make run-observatory  # run the observatory bridge on 127.0.0.1:6390 (serves web/dist or a placeholder)
make showcase-dev     # build server + UI, then run Antares + Observatory together
make seed             # load ~10k demo keys into the running server
```

Antares listens on port 6380 by default (`-port`, `-bind`, `-max-conns`, `-idle-timeout`, and the AOF flags `-appendonly`, `-aof-file`, `-appendfsync`, `-aof-repair`), so it can run alongside the reference Redis on 6379.

**Reference Redis**

The repo pins real Redis 7.4.11 (by image digest) for development and differential verification:

```bash
colima start           # once per boot, if using colima
docker compose up -d   # Redis on 127.0.0.1:6379
```

## Development commands

| Command | Description |
|---|---|
| `make build` | Build the server and Observatory binaries into `bin/` |
| `make test` | Run all Go tests |
| `make test-race` | Run all Go tests with the race detector |
| `make vet` | Run `go vet ./...` |
| `make fmt` | Format the tree with gofmt |
| `make tidy` | Run `go mod tidy` |
| `make fuzz` | Run the RESP parser fuzzer for `FUZZTIME` (default 30s) |
| `make diff-test` | Start the pinned Redis and run the command differential harness |
| `make clock-check` | Enforce ADR-009: no direct wall-clock reads outside clock/server I/O |
| `make crash-test` | Build the server and run 100× SIGKILL AOF durability iterations (`CRASH_ITERATIONS` to override) |
| `make tcl` | Download the pinned Redis 7.4.11 tests, run the selected official expiry and incr subsets, and print a per-command pass report |
| `make tcl-expire` | Run only the selected official expiry subset |
| `make tcl-incr` | Run only the selected official `unit/type/incr` subset |
| `make run` | Run the Antares server |
| `make run-observatory` | Run the Observatory bridge |
| `make ui-build` | `npm install` + Vite production build of `web/` into `web/dist` |
| `make showcase-dev` | Build the binaries and UI, then run Antares + the Observatory locally |
| `make seed` | Seed the running demo server with ~10k keys |
| `make clean` | Remove build artifacts |
| `make help` | List all targets |

Phase-gated targets — `verify`, `bench`, `showcase`, `showcase-e2e`, `ui-dev` — fail loudly with a pointer until the phase that implements them arrives. `make diff-test`, `make crash-test`, and `make tcl` are live.

## Roadmap

From [`docs/PLAN.md`](docs/PLAN.md) §7 — roughly 20–22 weeks at 3 hours/day, 5 sessions/week:

| Weeks | Focus | Demo-able milestone |
|---|---|---|
| W1–W5 | Server core (P0–P5) | redis-cli works; TTL, collections, SCAN live; differential + Tcl green |
| W6–W8 | Observatory dev console (M1) | Console in daily use |
| W9–W13 | AOF, eviction, transactions, verification (P6–P10) | `make verify` green; v2 complete |
| W14–W16 | Observatory showcase (M2) | Stories 1–4 live |
| W17–W18 | Replication core + partial resync (S1–S2) | `+CONTINUE` proven; chaos green |
| W19–W21 | Replication verification + Lab + Interview Mode (S3, M3) | Story 5; full Definition of Done |
| W22 | Buffer + interview prep | Application-ready |

## Scope and non-goals

**In scope:** v1 core (RESP2, ~55 commands, TTL + expiry), v2 differentiators (framed AOF, approximated LRU eviction, transactions, differential/Tcl/chaos/fuzz verification), v3 replication (full + partial resync), and the Observatory (bridge + SPA, demo stories, claims pipeline).

**Explicit non-goals** — each with a recorded reason in [`docs/DECISIONS.md`](docs/DECISIONS.md): Lua scripting, cluster/slots, Sentinel, Streams, RESP3 beyond the `HELLO` downgrade, modules, listpack/intset encodings, keyspace notifications, pub/sub, GEO/HLL/Bitmaps, a sharded keyspace, and auth/ACL.

## Documentation

| Document | What's inside |
|---|---|
| [`docs/PLAN.md`](docs/PLAN.md) | Master plan: objective, definition of done, architecture, phases, verification strategy, roadmap |
| [`docs/DECISIONS.md`](docs/DECISIONS.md) | Decision log (ADRs): context, options, why chosen, why rejected, trade-offs, and proof for every non-trivial decision |
| [`docs/PROJECT_LOG.md`](docs/PROJECT_LOG.md) | Session log: what was built, issues in symptom → diagnosis → fix → prevention form, exit evidence |
| [`docs/FILE_MANIFEST.md`](docs/FILE_MANIFEST.md) | Every tracked file, its purpose, and the hand-own commitments for the critical path |
| [`docs/INTERVIEW_QBANK.md`](docs/INTERVIEW_QBANK.md) | Rehearsal bank: narrative, decisions, verification, and boundary questions with evidence pointers |
| [`docs/COMPATIBILITY.md`](docs/COMPATIBILITY.md) | Pinned versions, verification claims, and deliberate divergences from real Redis |

## License

[MIT](LICENSE) © 2026 Sachin Kumar Singh
