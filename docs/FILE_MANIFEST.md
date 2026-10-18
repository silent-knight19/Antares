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
| `Makefile` | Canonical commands (`build test test-race vet fmt tidy run clean`); phase-gated targets (`verify`, `crash-test`, `showcase`, …) exist now and fail loudly until their phase; CI calls these same targets | scaffold |
| `README.md` | Public landing page: what/why, status, architecture, layout, getting started, roadmap, non-goals | scaffold |
| `go.mod` | Module `github.com/silent-knight19/antares`; Go 1.27.1 pin; dependency-free | scaffold |
| `docker-compose.yml` | Reference Redis 7.4.11 pinned by image digest; 127.0.0.1-only; healthchecked | scaffold |

## CI

| Path | What it is | Ownership |
|---|---|---|
| `.github/workflows/ci.yml` | CI: gofmt check, `vet`, `build`, race-tested test run; Go version read from `go.mod` | scaffold |
| `.github/workflows/.gitkeep` | placeholder from the initial layout; obsolete now that the directory has `ci.yml` | placeholder |

## Binaries (`cmd/`)

| Path | What it is | Ownership |
|---|---|---|
| `cmd/antares/main.go` | Server entry point: flags (`-bind`, `-port`, `-max-conns`, `-idle-timeout`), slog logs, SIGINT/SIGTERM → graceful shutdown; P1.4 stopgap handler (PING/ECHO) until the executor | scaffold |
| `cmd/observatory/main.go` | Observatory bridge entry point; P0 stub → U2 | stub |

## Packages (`internal/`)

Each package carries a `doc.go` charter referencing its ADR; real files land in their phases. `internal/server` received its listener at P1.1 and its parse → handle → flush loop at P1.4; `internal/protocol` its parser and encoder at P1.2–P1.3; critical-path packages are re-listed in "Hand-own commitments" below.

| Path | What it will contain | Ownership |
|---|---|---|
| `internal/protocol/doc.go` | Package charter: RESP2 parse/encode + fuzz corpus (ADR-003) | scaffold |
| `internal/protocol/parse.go` | RESP2 request reader: multibulk arrays + inline commands, sdssplitargs quoting, string2ll counts; limits and error wording pinned by live capture (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/parse_test.go` | Edge corpus, expectations captured live from Redis 7.4.11: multibulk, inline quoting/escapes, length limits, skips, lax CRLF handling, truncation, pipelining | scaffold |
| `internal/protocol/encode.go` | RESP2 reply encoder on pooled buffers: `+simple`, `-error`, `:int`, `$bulk`/`$-1`, `*array`/`*-1` (ADR-003) | **hand-own** — owner line-by-line review pending (PLAN §8) |
| `internal/protocol/encode_test.go` | Byte-exact framing tests, parser round-trips, pool reuse | scaffold |
| `internal/server/doc.go` | Package charter: listener, conn goroutines, executor loop (ADR-002) | scaffold |
| `internal/server/server.go` | TCP listener: accept loop, conn registry, max-conns cap, graceful `Shutdown(ctx)`; per-conn loop parses RESP requests, dispatches in arrival order to the `Handler`, flushes replies pipeline-aware; idle deadline resets per underlying read (ADR-002) | scaffold (executor flips to hand-own at P2.2) |
| `internal/server/server_test.go` | Listener + pipeline tests: accept/close, max-conns, idle timeout incl. reset-on-partial-activity, graceful/forced shutdown, 10k-command pipeline order/flush, protocol-error reply+close | scaffold |
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

All entries below are `placeholder` `.gitkeep` files — directories reserved per `PLAN.md` §13, populated at the phase noted.

| Path | Arrives |
|---|---|
| `tests/unit/.gitkeep` | P1.1 onward |
| `tests/differential/.gitkeep` | P2.6 (harness v0), P9.1 (full) |
| `tests/chaos/.gitkeep` | P6.4 (crash), S2.4 (replica chaos) |
| `tests/fuzz/.gitkeep` | P1.6, P9.3 (soak + corpus) |
| `tests/tcl/.gitkeep` | P3.4, P9.2 (vendored Tcl subset) |
| `bench/scripts/.gitkeep` | P9.4 |
| `bench/results/.gitkeep` | P9.4 (commit-keyed raw outputs) |
| `web/src/.gitkeep` | U3 (SPA scaffold) |
| `web/e2e/.gitkeep` | U12 (Playwright) |
| `web/tokens/.gitkeep` | U0/U3 (design tokens) |
| `deploy/demo/.gitkeep` | U2/M2 (demo compose profile + seeds) |

## Hand-own commitments (PLAN §8)

When these files exist, they carry the `hand-own` marker here and nothing is "done" on this path until the owner has reviewed every line and can defend it:

- `internal/protocol/` — RESP parser (`parse.go`) + encoder (`encode.go`); owner line-by-line review pending
- `internal/persist/` — frame format, writer, recovery, rewrite
- `internal/expire/` — lazy + active cycle
- `internal/evict/` — approximation, pool, policies
- `internal/server/` — executor + transaction semantics
- `internal/repl/` — handshake, backlog, offsets, replica apply
