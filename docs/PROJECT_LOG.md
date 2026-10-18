# Project Log — Antares + Observatory

> Purpose: session-by-session record — what was built, what broke, how it was diagnosed and fixed, what happens next.
> Answers the interview probes: "What was the hardest bug? How did you find it? What did you do last month?" — the STAR-story source.
> Rule: one entry per session, written at session close. Never backfill retroactively; if something was missed, log the catch-up as its own entry.
> Bugs are written in symptom → diagnosis → fix → prevention shape.

## Entry template (copy for new sessions)

```
### YYYY-MM-DD — <Phase ID>: <title>
- Built: <deliverables, phase exit criterion status>
- Files touched: <paths>
- Issues: <symptom → diagnosis → fix → prevention, or "none">
- ADRs written: <ADR-NNN or none>
- QBank updated: <questions added/changed>
- Exit evidence: <commands run + observed output, CI run IDs>
- Next session first task: <the very first action of next session>
```

---

### 2026-10-18 — P0.1: Repo scaffold

- Built: module init `github.com/silent-knight19/antares` (Go 1.27.1 pinned in `go.mod`); full layout per `PLAN.md` §13 (`cmd/`, 10 `internal/` packages with ADR-referencing package docs, `web/`, `tests/`, `bench/`, `deploy/`, `.github/workflows/`); minimal binaries that print `--version` and fail loudly with a pointer to the phase that implements them; `Makefile` with working core targets and phase-gated stubs that exit non-zero with a plan pointer; MIT LICENSE.
- Files touched: `go.mod`, `Makefile`, `LICENSE`, `cmd/antares/main.go`, `cmd/observatory/main.go`, `internal/*/doc.go` (×10), `.gitkeep` placeholders under `web/`, `tests/`, `bench/`, `deploy/`, `.github/`.
- Issues: none blocking.
  1. `git describe --always` fails in a repo with no commits → Makefile falls back to `version=dev`; after the first commit, ldflags inject the short hash (verified: `antares bdc0586`).
  2. macOS ships GNU Make 3.81 → avoided `.ONESHELL` and grouped-target features; the build recipe is two explicit `go build` lines.
- ADRs written: none (ADR-001…019 pre-seeded before code).
- QBank updated: n/a (QBank not yet seeded).
- Exit evidence: `make build` green; `make test`, `make vet`, `gofmt -l .` clean; both binaries print version; `make verify` fails loudly by design (phase-gated).
- Next session first task: P0.2 — docker-compose with pinned real Redis 7.4 + CI stub.

---

### 2026-10-18 — P0.2: Pinned reference Redis + CI + OSS tooling policy

- Built: `docker-compose.yml` — reference Redis 7.4.11 pinned by image-index digest `sha256:4fa24486…`, healthcheck, bound to 127.0.0.1 only; `.github/workflows/ci.yml` — gofmt check + `vet` + `build` + race-tested test run, Go version read from `go.mod`; container runtime migrated from OrbStack to the fully-OSS colima stack.
- Files touched: `docker-compose.yml`, `.github/workflows/ci.yml`, `docs/DECISIONS.md` (ADR-020), host `~/.docker/config.json`.
- Issues:
  1. OrbStack violated the "OSS or free-to-use + auditable" policy (proprietary; free for personal use only; unauditable privileged helper). Symptom: worked, failed the policy. Diagnosis: license + auditability, flagged during policy review. Fix: `docker compose down` → uninstall `--zap` → install colima (MIT) + lima + docker CLI/compose → cleaned stale `currentContext: orbstack`, added `cliPluginsExtraDirs` → recreated container. Prevention: ADR-020 makes OSS-or-free a selection rule for every future tool installation.
  2. YAML validation friction while verifying the new config files: no PyYAML on the machine; Ruby 2.6 psych rejects the `aliases:` keyword. Fix: plain `YAML.load_file`. Prevention: planned CI step `docker compose config` at P9.1 will validate compose on every push.
  3. Open (owner action, harmless): root-owned leftover `/usr/local/bin/docker` symlink and protected `Group Containers/HUAQ24HBR6.dev.orbstack` need `sudo rm`.
- ADRs written: ADR-020 (tooling & licensing policy).
- QBank updated: n/a (seeded in P0.3).
- Exit evidence: `redis-cli -p 6379 PING` → `PONG`; `redis_version:7.4.11`; container healthy on `127.0.0.1:6379`; CI run #1 success — [39138833199](https://github.com/silent-knight19/Antares/actions/runs/39138833199) on commit `80427de`.
- Next session first task: P0.3 — seed the tracking docs.

---

### 2026-10-18 — P0.3: Tracking system seeded

- Built: the four living docs — `PROJECT_LOG.md` (this file, first entries), `FILE_MANIFEST.md` (every tracked file mapped + hand-own commitments for the critical path), `INTERVIEW_QBANK.md` (seeded with 12 questions + boundary list), `COMPATIBILITY.md` (stub: pinned versions, planned divergences, licensing note); README status and documentation table refreshed.
- Files touched: `docs/PROJECT_LOG.md`, `docs/FILE_MANIFEST.md`, `docs/INTERVIEW_QBANK.md`, `docs/COMPATIBILITY.md`, `README.md`, `docs/PLAN.md` (current-position line).
- Issues: none.
- ADRs written: none.
- QBank updated: seeded with Q1–Q12 (narrative, decisions, verification, boundaries).
- Exit evidence: first PROJECT_LOG entry written (this file); `FILE_MANIFEST.md` covers every tracked file — `git ls-files` count matches (37 after this commit), no orphans; all cross-references resolve.
- Next session first task: P1.1 — TCP listener with graceful shutdown, connection limits/timeouts, and accept/close/shutdown unit tests.

---

### 2026-10-18 — P1.1: TCP listener with graceful shutdown

- Built: `internal/server/server.go` — accept loop, connection registry (`Conns()`), `MaxConns` cap (excess connections accepted-then-closed; the exact Redis `-ERR max number of clients reached` string lands with the RESP writer at P1.5), per-read idle-timeout deadlines, `Shutdown(ctx)`: stop accepting → drain → force-close remaining at deadline, idempotent, force path returns `ctx.Err()`; connection goroutines read-and-discard until the RESP parser arrives (P1.2/P1.4). `cmd/antares` promoted from stub to a real server: `-bind`/`-port`/`-max-conns`/`-idle-timeout` flags (Redis-compatible defaults: 10 000 clients, idle timeout off), structured slog lifecycle logs, SIGINT/SIGTERM → 5 s graceful shutdown, clean exit 0.
- Files touched: `internal/server/server.go`, `internal/server/server_test.go`, `cmd/antares/main.go`, `Makefile` (help), `README.md` (run section), `docs/PROJECT_LOG.md`, `docs/FILE_MANIFEST.md`.
- Issues: none. Micro-decision logged: default port **6380** so `make run` coexists with the reference Redis on 6379 (Redis parity available via `-port 6379`; revisit for the demo stack at U2).
- Review pass (self-review, pre-commit): accept loop now retries transient errors (EMFILE/ENFILE) instead of silently dying; read buffer 16 KB → 4 KB per connection; `Shutdown` returns an error only when connections were actually force-closed; `Start` refused after `Shutdown`; second SIGINT/SIGTERM force-quits immediately; 4 new tests cover all of it.
- ADRs written: none (micro-decision above).
- QBank updated: none.
- Exit evidence: 11 tests green under `make test-race` (accept/close, max-conns rejection, idle timeout close + reset-on-activity, graceful drain, forced shutdown, expired-context shutdown, idempotent shutdown, double-`Start` error, restart-after-shutdown refusal, accept-error classification); `gofmt -l .` and `go vet` clean; live smoke: `bin/antares -port 6380` → port open, SIGTERM → `shutting down` → `stopped`, exit 0.
- Next session first task: P1.2 — RESP parser (hand-own begins): arrays of bulk strings + inline commands, edge-case corpus.

---

### 2026-10-18 — P1.2: RESP parser, captured against Redis 7.4.11

- Built: `internal/protocol/parse.go` — RESP2 request reader: multibulk arrays + inline commands behind `ReadRequest(*bufio.Reader) (Request, error)`; `Request [][]byte` binary-safe args; `ProtocolError{Msg}` carries the raw Redis wording (P1.5 frames it as `-ERR Protocol error: …`); Redis string2ll-faithful count parsing; sdssplitargs-faithful inline tokenizer (quote grouping; `\xHH` `\n` `\r` `\t` `\b` `\a` `\\` `\"` escapes; `\'` in single quotes; close quote must be followed by a blank); limits: INT_MAX multibulk count, 512 MB bulk, 64 KB count/inline lines; streaming: incomplete input blocks, EOF surfaces as `io.ErrUnexpectedEOF` (clean boundary: `io.EOF`). `internal/protocol/parse_test.go` — 30-case edge corpus + 5 truncation inputs, every expectation captured live from the pinned reference Redis.
- Files touched: `internal/protocol/parse.go`, `internal/protocol/parse_test.go`, `docs/PLAN.md` (§8 working-agreement amendment + current position), `docs/FILE_MANIFEST.md`, `docs/INTERVIEW_QBANK.md`, `docs/PROJECT_LOG.md`.
- Issues: four Redis quirks contradicted the assumed source behavior and were captured before coding (the reason the corpus captures instead of trusting memory): (1) multibulk counts are accepted up to INT_MAX — `*1048577` waits for its args, the real bound is `*2147483648` → `invalid multibulk length`; (2) counts ≤ 0 (`*0`, `*-1`, `*-2`) are silently skipped, not errors; (3) the byte after a header `\r` is skipped blindly — `*1\rX…` and `$4\rX…` both execute; `expected '$', got 'X'` fires only on a wrong first byte (empty bulk header reports `got '\r'`); (4) bulk payload trailers are never validated (`$4` + `PINGXX` still executes `PING`) and inline commands accept a bare LF.
- Working agreement update (PLAN §8, amended in place): harness writes all code; the critical path keeps a mandatory line-by-line owner review before it ships — the defensibility rule survives, authoring does not. Owner review of `parse.go` is the standing open item.
- ADRs written: none.
- QBank updated: §5 note 4 — protocol fidelity comes from capture, not memory, listing the Redis 7.4 quirks.
- Exit evidence: `make test-race` green — protocol 30 corpus cases + 5 truncation inputs, server 11/11; `gofmt -l .` clean; vet/build green.
- Next session first task: P1.3 — RESP writer (`+OK`, `-ERR`, integers, bulk, arrays; buffer pooling; parse↔encode round-trip tests).

---

### 2026-10-18 — P1.3: RESP writer with pooled buffers

- Built: `internal/protocol/encode.go` — `Encoder` appending RESP2 replies to a reused byte buffer: `Simple` (`+OK`), `Error` (`-ERR …`), `Int` (`:n`), `Bulk` (`$len\r\n…`; nil → `$-1`, empty → `$0`), `Null`, `ArrayHeader`, `NullArray`; `GetEncoder`/`Release` over a `sync.Pool` that drops buffers grown past 64 KiB so one 512 MB reply cannot pin pool memory. `internal/protocol/encode_test.go` — byte-exact framing table (17 cases incl. int64 extremes, binary payloads with NUL/CR/LF/0xFF, nested arrays), request round-trips through `ReadRequest` (single + pipelined), pool-reuse reset.
- Files touched: `internal/protocol/encode.go`, `internal/protocol/encode_test.go`, `docs/PLAN.md` (current position), `docs/FILE_MANIFEST.md`, `docs/PROJECT_LOG.md`.
- Issues: none. Micro-decision logged: `Bulk(nil)` writes `$-1` (Redis addReplyNull semantics) while an empty non-nil slice writes `$0` — the distinction string commands depend on from P2.4.
- ADRs written: none.
- QBank updated: none.
- Exit evidence: `make test-race` green — encode suite (17 framing + 3 round-trip/pool tests) plus full protocol and server packages; `gofmt -l .` clean; vet/build green.
- Next session first task: P1.4 — pipelining: read and handle multiple commands per packet in order; exit test streams 10 000 commands in one write.
