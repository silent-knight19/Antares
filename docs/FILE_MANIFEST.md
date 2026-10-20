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
| `.gitignore` | Ignore rules: build artifacts, runtime data (aof/rdb/tap), node_modules, env files | scaffold |
| `LICENSE` | MIT, © 2026 Sachin Kumar Singh | scaffold |
| `Makefile` | Canonical commands (`build test test-race vet fmt tidy run clean`, `fuzz` with `FUZZTIME`); gated targets (`verify`, `crash-test`, `showcase`, …) exist now and fail loudly until their phase; CI calls these same targets | scaffold |
| `README.md` | Public landing page: what/why, status, architecture, layout, getting started, roadmap, non-goals | scaffold |
| `go.mod` | Module `github.com/silent-knight19/antares`; Go 1.27.1 pin; single direct dependency: go-redis v9 (test-only differential client, ADR-017) | scaffold |
| `go.sum` | Module checksums for go-redis and its indirect deps | scaffold |
| `docker-compose.yml` | Reference Redis 7.4.11 pinned by image digest; 127.0.0.1-only; healthchecked | scaffold |

## CI

| Path | What it is | Ownership |
|---|---|---|
| `.github/workflows/ci.yml` | CI: gofmt check, `vet`, `build`, race-tested test run; Go version read from `go.mod` | scaffold |

## Binaries (`cmd/`)

| Path | What it is | Ownership |
|---|---|---|
| `cmd/antares/main.go` | Server entry point: flags (`-bind`, `-port`, `-max-conns`, `-idle-timeout`), slog logs, SIGINT/SIGTERM → graceful shutdown; creates the store + executor and wires `executor.Handle`, closing the executor after the server drains | scaffold |
| `cmd/observatory/main.go` | Observatory bridge entry point; P0 stub → U2 | stub |

## Packages (`internal/`)

Each package carries a `doc.go` charter referencing its ADR; real files land in their phases. `internal/server` received its listener at P1.1, its parse → handle → flush loop at P1.4, and the single executor at P2.2; `internal/protocol` its parser, encoder, and error strings at P1.2–P1.5; `internal/store` its keyspace map at P2.2; critical-path packages are re-listed in "Hand-own commitments" below.

| Path | What it will contain | Ownership |
|---|---|---|
| `internal/protocol/doc.go` | Package charter: RESP2 parse/encode + fuzz corpus (ADR-003) | scaffold |
| `internal/protocol/parse.go` | RESP2 request reader: multibulk arrays + inline commands, sdssplitargs quoting, string2ll counts; limits and error wording pinned by live capture; one exact allocation for payloads ≤ 64 KiB, larger ones grow with received bytes clamped to the declared size (a declared 512 MB length can never force the allocation) (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/parse_test.go` | Edge corpus from live Redis 7.4.11 captures: multibulk, inline quoting/escapes, length limits, skips, lax CRLF handling, truncation, pipelining; plus a generated 200 KiB size-path case | scaffold |
| `internal/protocol/fuzz_test.go` | `FuzzReadRequest` — capture-shaped seeds + round-trip invariant (parse → encode → parse byte-identical); no-crash/no-hang on arbitrary bytes; huge-declared-length regression | scaffold |
| `internal/protocol/encode.go` | RESP2 reply encoder on pooled buffers: `+simple`, `-error`, `:int`, `$bulk`/`$-1`, `*array`/`*-1` (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/encode_test.go` | Byte-exact framing tests, parser round-trips, pool reuse | scaffold |
| `internal/protocol/errors.go` | Error bodies in captured Redis wording: `ProtocolErrorReply`, `WrongArity`, `UnknownCommand` (128-byte name/args truncation) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/errors_test.go` | String-equality table vs Redis 7.4.11 captures + CRLF sanitization | scaffold |
| `internal/server/doc.go` | Package charter: listener, conn goroutines, executor loop (ADR-002) | scaffold |
| `internal/server/server.go` | TCP listener: accept loop, conn registry, max-conns cap (over-cap connections get the captured refusal error), graceful `Shutdown(ctx)`; per-conn loop parses RESP requests, dispatches in arrival order to the `Handler`; `connIO` flushes replies before the parser blocks and resets the idle deadline per underlying read (writes bounded when the timeout is set) (ADR-002) | scaffold |
| `internal/server/server_test.go` | Listener + pipeline tests: accept/close, max-conns, idle timeout incl. reset-on-partial-activity, graceful/forced shutdown, 10k-command pipeline order/flush, protocol-error reply+close | scaffold |
| `internal/server/executor.go` | Single executor goroutine owning the keyspace: bounded request queue (1024, blocking backpressure), per-request reply signal into the connection's encoder, idempotent `Close()` after connection drain (ADR-002) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/server/executor_test.go` | Serialization test (200 concurrent callers, unguarded counter + store writes, race-clean), executor dispatch/arity/unknown replies | scaffold |
| `internal/server/registry.go` | Command registry: case-insensitive name → `Command{Name, Arity, Flags, Handler}`; Redis arity rules (positive exact, negative minimum); dispatch replies with captured unknown-command/arity wording; P2.1 built-ins PING/ECHO/COMMAND (the COMMAND forms return an empty array for redis-cli hints); `FlagWrite` marker for P2.3 onward | scaffold |
| `internal/server/registry_test.go` | Registry tests: captured wire replies, case-insensitive lookup + flags, positive/negative arity, COMMAND stub forms | scaffold |
| `internal/store/store.go` | Keyspace map owned by the executor (no locks by construction): `Get/Set/Delete/Exists/Len`; value representation grows with the P2.3 string commands | scaffold (hand-own as value types land) |
| `internal/store/store_test.go` | Basic keyspace operations incl. overwrite/delete/len | scaffold |
| `internal/store/doc.go` | Keyspace, value types, WRONGTYPE, empty-collection auto-delete | scaffold → hand-own |
| `internal/persist/doc.go` | Framed AOF, group commit, recovery, rewrite (ADR-004/007/010) | scaffold → hand-own at P6 |
| `internal/expire/doc.go` | Lazy + active expiry cycle (ADR-005) | scaffold → hand-own at P3 |
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

All entries below except the differential smoke are `placeholder` `.gitkeep` files — directories reserved per `PLAN.md` §13, populated at the phase noted.

| Path | Arrives |
|---|---|
| `tests/unit/.gitkeep` | reserved for cross-package suites; unit tests are colocated with their package (Go convention) |
| `tests/differential/smoke_test.go` | P1.6 (go-redis PING/ECHO/pipeline/unknown-command against in-process server); grows into harness v0 at P2.6, full at P9.1 |
| `tests/chaos/.gitkeep` | P6.4 (crash), S2.4 (replica chaos) |
| `tests/fuzz/.gitkeep` | P9.3 (soak + committed regression corpus; the P1.6 harness itself is `internal/protocol/fuzz_test.go`) |
| `tests/tcl/.gitkeep` | P3.4, P9.2 (vendored Tcl subset) |
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
