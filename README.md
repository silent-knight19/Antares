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

Antares is a Redis-compatible server written from scratch in Go: the RESP2 protocol, 55 commands covering strings, lists, hashes, sets, TTL, DBSIZE, CONFIG hz, and PING/ECHO/COMMAND, plus lazy and active expiry. Persistence, eviction, transactions, and replication are planned, not yet implemented. The **Antares Observatory** — a separate showcase and tooling service plus web UI — is being built alongside it; its bridge currently ships as a stub.

Target shape:

```text
redis-cli / clients ──RESP──▶ Antares ──▶ event tap (JSONL, opt-in)
go-redis (verify)   ──RESP──▶ Antares + real Redis (differential)
Observatory ──own client + INFO + event tap + claims──▶ SPA (SSE + REST)
```

The event tap and Observatory UI are planned; the server currently exposes no tap.

Every claim the project makes is meant to be machine-verifiable — differential tests against real Redis, crash tests, benchmarks, and CI-verified numbers displayed in the Observatory UI.

> **Why the name?** Antares — α Scorpii — is the red star called "the rival of Mars": a red rival to Redis.

## Project status

> **P1–P3 and P4.1–P4.5 complete — the wire path is done and fuzz-tested (15-min fuzz clean, 166M executions), commands run through a single keyspace-owning executor, and the edge-hardened string, list, hash, and set surfaces are live: SET/GET/DEL/EXISTS/TYPE, MSET/MGET, the INCR family including a bit-exact INCRBYFLOAT, APPEND/STRLEN/GETSET/SETNX, LPUSH/RPUSH/LPOP/RPOP/LRANGE/LLEN/LINDEX/LSET/LREM/LTRIM, HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD, SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER, lazy and active expiry, DBSIZE, CONFIG hz, and exact wrong-type/overflow/limit errors.** TTL state sits in a separate expires dict, with TTL/PTTL/PERSIST and the full EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT family (NX/XX/GT/LT). Domain time now comes from one injected clock (ADR-009), so expiry tests advance time instead of sleeping. A committed differential harness (`make diff-test`) runs 198 string/TTL steps, 91 list steps, 71 hash steps, 99 set steps, a 171-case cross-type matrix, and 240 generated collection-sequence steps plus expiry/lazy-read scenarios against the pinned Redis 7.4.11 with zero mismatches; 1056 one-off checks also passed. The selected official Tcl subsets are green via `make tcl-expire` (39 `unit/expire` tests) and `make tcl-incr` (26 `unit/type/incr` tests), which download the pinned 7.4.11 test source at run time. PING/ECHO plus the `COMMAND` stub work. The project is under active construction.

| Works today | Arrives later |
|---|---|
| `make build` (both binaries), `make test`, `make test-race`, `make vet`, `make fmt`, `make tidy`, `make fuzz`, `make diff-test` (strings + lists + hashes + sets + TTL), `make tcl-expire` + `make tcl-incr` (selected official expiry/incr tests) | Full Tcl suite wiring (P9.2), persistence, eviction, transactions, replication |
| TCP server speaking RESP2 — parser with a 31-case corpus captured from real Redis + fuzz harness, pooled reply writer, capture-exact error strings, pipeline-aware flushing; PING/ECHO/COMMAND answered through the registry and single keyspace-owning executor (case-insensitive lookup, Redis arity checks, `COMMAND` stub for redis-cli) | `make verify`, `make crash-test`, `make tcl`, `make bench`, `make showcase` |
| Edge-hardened string core — SET (EX/PX/NX/XX/GET/KEEPTTL), GET, DEL, EXISTS, TYPE, MSET/MGET, INCR/DECR/INCRBY/DECRBY/INCRBYFLOAT, APPEND, STRLEN, GETSET, SETNX; lists — LPUSH/RPUSH/LPOP/RPOP (with counts), LRANGE, LLEN, LINDEX, LSET, LREM, LTRIM, negative ranges, error-order parity, TTL preservation, auto-delete; hashes — HSET/HGET/HMGET/HDEL/HLEN/HGETALL/HKEYS/HVALS/HEXISTS/HRANDFIELD with insertion-order iteration; sets — SADD/SREM/SMEMBERS/SISMEMBER/SCARD/SPOP/SRANDMEMBER with unordered iteration and random pop/rand semantics; separate expires dict with lazy delete-on-access, TTL/PTTL/PERSIST, EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT incl. NX/XX/GT/LT, physical DBSIZE, active expiry (hz=10), CONFIG GET/SET hz; empty/binary/1 MiB values, wide batches, overflow parity, 512 MiB guard, pinned WRONGTYPE body; `make diff-test` green (198 string/TTL + 91 list + 71 hash + 99 set + 171 cross-type + 240 sequence steps + expiry + lazy reads), 1056 one-off live checks clean, and 65 selected official Tcl expiry/incr tests green | Keyspace commands (P5.1); full Tcl suite at P9.2 |
| CI: gofmt check + `vet` + `build` + race-tested tests + differential harness | Observatory bridge and web UI (U2 onward) |
| `docker compose up` — pinned reference Redis 7.4.11 | |

The Observatory bridge (`cmd/observatory`) still ships as a version-printing stub and fails loudly with a pointer to its phase; `cmd/antares` is the real server.

## Architecture

**Server** (from `docs/PLAN.md` §3.1). Only the wire path, strings, lists, hashes, sets, TTL, and PING/ECHO/COMMAND exist today; the rest is the target design:

- **Concurrency** — goroutine per connection for I/O → command channel → a single executor goroutine owning the keyspace, reproducing Redis-like sequential semantics with race-detector-clean guarantees.
- **Protocol** — RESP2 (inline, bulk strings, arrays, integers); `HELLO`/RESP3 is not implemented yet (planned P5.3).
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
- `tclsh` 8.5+ and `curl` only for `make tcl-expire`/`make tcl-incr`; the pinned Redis test source is downloaded into a git-ignored cache, never vendored.

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
make run-observatory  # run the observatory bridge (stub until U2)
```

Antares listens on port 6380 by default (`-port`, `-bind`, `-max-conns`, `-idle-timeout` flags available), so it can run alongside the reference Redis on 6379.

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
| `make tcl-expire` | Download the pinned Redis 7.4.11 tests and run the selected official expiry subset |
| `make tcl-incr` | Run the selected official `unit/type/incr` subset |
| `make run` | Run the Antares server |
| `make run-observatory` | Run the Observatory bridge |
| `make clean` | Remove build artifacts |
| `make help` | List all targets |

Phase-gated targets — `verify`, `crash-test`, `bench`, `tcl`, `showcase`, `showcase-e2e`, `ui-dev` — fail loudly with a pointer until the phase that implements them arrives. `make tcl-expire` and `make tcl-incr` are already live; `make tcl` remains the full-suite P9.2 target.

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
