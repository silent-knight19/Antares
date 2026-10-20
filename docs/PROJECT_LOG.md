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

---

### 2026-10-18 — P1.4: pipelining — parse → handle → flush per connection

- Built: `internal/server/server.go` — the conn goroutine now parses requests with `protocol.ReadRequest` and dispatches each to a new `Handler` type (`func(protocol.Request, *protocol.Encoder)`) in arrival order; replies batch in a per-conn `bufio.Writer` and flush only when no buffered input remains (classic Redis pipeline behavior); protocol errors get `-ERR Protocol error: …` then close, I/O errors (EOF, idle timeout, reset) close silently; reader buffer 4 KB → 16 KB (PROTO_IOBUF_LEN) to batch pipeline reads; idle deadline moved into an `idleConn` reader wrapper that resets it on every underlying read, so any byte of activity keeps the connection alive (Redis `lastinteraction` semantics). `cmd/antares/main.go` — P1.4 stopgap handler: PING/ECHO with the captured `wrong number of arguments for 'echo' command` string; unknown-command wording is refined at P1.5.
- Files touched: `internal/server/server.go`, `internal/server/server_test.go`, `cmd/antares/main.go`, `README.md` (status), `docs/PLAN.md` (current position), `docs/FILE_MANIFEST.md`, `docs/INTERVIEW_QBANK.md` (Q11 evidence), `docs/PROJECT_LOG.md`.
- Issues: one design trap caught before it shipped — resetting the read deadline per request (instead of per read) would have broken idle-timeout semantics for slow partial commands; `TestIdleTimeoutResetsOnActivity` guards it.
- ADRs written: none.
- QBank updated: Q11 refreshed with the current strongest evidence; note 4 wording fixed.
- Exit evidence: `make test-race` green — server 14/14 including a 10 000-command single-write ECHO pipeline (distinct payloads read back in order, no extra replies), flush-without-further-input, and protocol-error reply+close; live smoke: mixed multibulk/inline pipeline against `bin/antares -port 6381` → `+PONG +PONG $2 hi -ERR unknown command 'FOO'`, SIGTERM → clean stop.
- Next session first task: P1.5 — capture exact Redis error strings (unknown command incl. arg quoting/limits, protocol errors, wrong arity) and pin them with string-equality tests.

---

### 2026-10-18 — P1.5: error framework, captured from Redis 7.4.11

- Built: `internal/protocol/errors.go` — `ProtocolErrorReply`, `WrongArity`, `UnknownCommand` with Redis's 128-byte quoted-args budget; `Encoder.Error` now sanitizes CR/LF to spaces like `addReplyErrorFormat`, so user-derived bytes can never corrupt the reply stream; `internal/server/server.go` uses the shared protocol-error body; `cmd/antares` stopgap handler now replies capture-exact (PING message → bulk, PING/ECHO arity, unknown command). Tests: `internal/protocol/errors_test.go` (string-equality table + sanitization) and `cmd/antares/main_test.go` (handler replies).
- Files touched: `internal/protocol/errors.go`, `internal/protocol/errors_test.go`, `internal/protocol/encode.go`, `internal/server/server.go`, `cmd/antares/main.go`, `cmd/antares/main_test.go`, `docs/COMPATIBILITY.md`, `docs/PLAN.md`, `docs/FILE_MANIFEST.md`, `docs/PROJECT_LOG.md`.
- Issues: two more capture-vs-memory corrections: (1) Redis 7.4 quotes unknown-command arguments space-separated (`'bar' 'baz' `) with no commas, group capped at 128 bytes, each argument cut to the remaining budget; (2) formatted error replies map CR/LF to spaces — visible both in an argument containing CRLF and in the empty-bulk-header protocol error (`got ' '`).
- ADRs written: none.
- QBank updated: none.
- Exit evidence: `make test-race` green across all packages; live 12-case differential battery (unknown command incl. truncation edges, arity, PING forms, protocol errors, CRLF sanitization) byte-identical between `bin/antares` and the digest-pinned reference Redis; COMPATIBILITY open question updated (only OOM wording remains, P7.2).
- Next session first task: P1.6 — fuzz harness (`go test -fuzz`) with 15-minute clean run, then differential smoke via go-redis (PING/ECHO).

---

### 2026-10-18 — P1.6: parser fuzzing + differential smoke; P1 complete

- Built: `FuzzReadRequest` (`internal/protocol/fuzz_test.go`) — 39 capture-shaped seeds from the P1.2 corpus, invariants: never crash or hang; returned args never nil; and parse → encode → re-parse is byte-identical (parser/encoder agreement). Hardening the fuzzer design surfaced: `readBulk` no longer preallocates the declared payload length — it grows with received bytes, so a client declaring 512 MB and sending nothing no longer forces a 512 MB allocation (Redis sizes structures from received bytes too; regression test included). `make fuzz FUZZTIME=…` is now a real target (was a phase-gated stub). `server.StopgapHandler` moved out of `cmd/antares` so tests and the differential harness can run a full in-process server. `tests/differential/smoke_test.go` — go-redis v9 (first direct dependency, ADR-017) against in-process Antares: PING, ECHO, pipelined PING+ECHO, and byte-exact unknown-command error.
- Files touched: `internal/protocol/parse.go`, `internal/protocol/fuzz_test.go`, `internal/server/stopgap.go`, `internal/server/stopgap_test.go`, `cmd/antares/main.go` (removed `main_test.go` — handler moved), `tests/differential/smoke_test.go` (removed `.gitkeep`), `Makefile`, `go.mod`, `go.sum`, docs.
- Issues: designing the fuzz round-trip invariant exposed a real bug — an inline `""` argument parsed to nil, which the encoder would have written as the null bulk `$-1` (wrong: it is an empty string); arguments are now guaranteed non-nil. Also noted for P2.6: go-redis v9 sends `HELLO`/`CLIENT SETINFO` on connect; the smoke pins `Protocol: 2` + `DisableIdentity` until P5.3 negotiates properly.
- ADRs written: none.
- QBank updated: Q11.
- Exit evidence: `make fuzz FUZZTIME=15m` clean — **165,807,798 executions in 15m0s, no failures** (143 interesting inputs found; committed regression corpus is P9.3); full race suite green including the new differential package; `make build` clean.
- Next session first task: P2.1 — command registry (name → handler, arity, flags) with the `COMMAND` stub for redis-cli.

---

### 2026-10-18 — P1.6 review fixes (owner-requested review of the uncommitted diff)

- Built: review found no functional bug in the P1.6 changes; all flagged items fixed. `readBulk` keeps a single exact allocation for payloads ≤ 64 KiB (the interim `bytes.Buffer` version paid an extra allocation and indirection per argument) and above that grows with a manually clamped loop — capacity never exceeds the declared size, so a large value no longer retains up to 2× its size while it lives. Dead nil-guard and hand-written `min64` helper deleted (Go builtin `min`).
- Issues: two test gaps closed. A 200 KiB `ECHO` corpus case now drives the growth path and asserts exact content and length. `TestReadBulkTruncatedHugeLength` now enforces the security property it claimed: a `runtime.MemStats` allocation delta (< 8 MB) around an unsent 512 MB declared payload — the previous error-only assertion would also have passed with the old preallocating implementation.
- Files touched: `internal/protocol/parse.go`, `internal/protocol/parse_test.go`, `internal/protocol/fuzz_test.go`, `docs/FILE_MANIFEST.md`, `docs/PLAN.md`, `docs/PROJECT_LOG.md`.
- ADRs written: none (no decision changed; parser internals only).
- QBank updated: none.
- Exit evidence: race suite green across all packages; `make fuzz FUZZTIME=60s` clean on the rewritten `readBulk`.
- Next session first task: P2.1 — command registry (name → handler, arity, flags) with the `COMMAND` stub for redis-cli.

---

### 2026-10-18 — P1 audit fixes (whole-repo audit → every finding fixed)

- Built: whole-repo audit of the P1 codebase (README/build/deps/entry points/tests + full read of `internal/protocol`, `internal/server`, docs) found 5 code issues and 4 tidy-ups; all fixed this session. Server: replies now flush from `connIO.Read` right before the parser blocks on the socket — a reply can no longer be withheld (or discarded on close) behind a partial trailing command; the deadline wrapper now also bounds writes when `-idle-timeout` is set; over-cap connections get Redis's captured `-ERR max number of clients reached` before the close (was silent). Protocol: `UnknownCommand` cuts the command name at 128 bytes like Redis; the `expected '$'` error carries the raw non-ASCII byte instead of a UTF-8 rendering (Go `%c` bug).
- Issues (symptom → diagnosis → fix → prevention): (1) `PING\r\nECH` got no `+PONG` while reference Redis answered instantly — the P1.4 flush gate waited for a drained read buffer, so any partial trailing command held earlier replies; fixed by flushing at the read boundary, regression test covers hold-open and half-close. (2) a 200-byte unknown command name produced a 253-byte reply vs Redis's 181 — the P1.5 capture set never included a long name; fixed at 128 bytes, capture-backed test added. (3) `*1\r\n\xff\r\n` echoed the byte as UTF-8 — `fmt.Sprintf("%c")` treats a byte as a rune; fixed to raw-byte formatting. (4) max-conns closed silently — capture shows Redis answers first; now matched. (5) stalled writers were unbounded — the opt-in idle timeout now also covers writes (documented in COMPATIBILITY, S2.3 output caps still the long-term answer). Prevention: each fixed case is pinned by a capture-backed test; fuzz seed added for the 0xff header.
- Tidy-ups: dead `.github/workflows/.gitkeep` deleted; corpus claims corrected (31-case corpus, one generated size case) in README/QBANK/parse_test comment; PLAN §9/§13 reworded — unit tests are colocated with packages, `tests/unit/` reserved for cross-package suites.
- Files touched: `internal/server/server.go`, `internal/server/server_test.go`, `internal/protocol/parse.go`, `internal/protocol/parse_test.go`, `internal/protocol/errors.go`, `internal/protocol/errors_test.go`, `internal/protocol/fuzz_test.go`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,INTERVIEW_QBANK,PROJECT_LOG}.md`, `README.md`, removed `.github/workflows/.gitkeep`.
- ADRs written: none (no new trade-off; the slow-client write bound is recorded in COMPATIBILITY's divergences).
- QBank updated: Q11 (count + late captures).
- Exit evidence: race suite green; live byte checks against the pinned reference Redis — `PING\r\nECH` → `+PONG` (was withheld), 200-byte name → 181-byte reply identical, `*1\r\n\xff\r\n` → raw `\377` identical, `-max-conns 2` refusal identical; `make fuzz FUZZTIME=60s` clean.
- Next session first task: P2.1 — command registry (name → handler, arity, flags) with the `COMMAND` stub for redis-cli.

---

### 2026-10-20 — P2.1: command registry + COMMAND stub

- Built: `internal/server/registry.go` — `Command{Name, Arity, Flags, Handler}` table with case-insensitive lookup; Redis arity rules (positive = exact argc including the command name, negative = minimum); dispatch replies with the captured `WrongArity`/`UnknownCommand` wording. P2.1 built-ins: PING (arity -1, message form still answered as a bulk), ECHO (arity 2), COMMAND (arity -1). Every COMMAND form (`COMMAND`, `COMMAND DOCS`, `COMMAND INFO …`) returns an empty array — enough for redis-cli 8.10.2's interactive `COMMAND DOCS` hint request. `FlagWrite` is the first command flag, ready for SET at P2.3. `stopgap.go` is now a one-line registry delegate; `stopgap_test.go` was replaced by `registry_test.go` (captured replies + lookup/flags + positive/negative arity).
- Files touched: `internal/server/registry.go`, `internal/server/registry_test.go`, `internal/server/stopgap.go`, removed `internal/server/stopgap_test.go`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,PROJECT_LOG}.md`, `README.md`.
- Issues: none. Micro-decision logged: COMMAND introspection is deliberately a stub (empty array), not full Redis metadata; redis-cli interactive mode was captured sending `COMMAND DOCS` on startup, and an empty array is accepted without breaking the prompt or hints path.
- ADRs written: none.
- QBank updated: none.
- Exit evidence: `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` all clean; live byte comparison against the pinned Redis 7.4.11 — `PING`, `ECHO hi`, `PING a b`, `ECHO`, `FOO bar baz` identical; interactive redis-cli against a fresh `go build` of `cmd/antares` on port 6381 printed `PONG`, `"hi"`, and the exact unknown-command error after its startup `COMMAND DOCS`.
- Next session first task: P2.2 — executor goroutine + request/response channels + `internal/store`; replace `StopgapHandler` and delete `stopgap.go`.

---

### 2026-10-20 — P2.2: single executor goroutine + keyspace store

- Built: `internal/server/executor.go` — one executor goroutine owns the keyspace; connection goroutines queue `{request, encoder}` over a bounded (1024) channel and wait for a per-request done signal; the executor dispatches through the P2.1 registry, writes the reply into the connection's encoder, then signals. `Close()` is idempotent and is called only after the server has drained connections, so no request can be stranded. Blocking sends are the backpressure policy: under load a connection waits instead of having its command rejected, preserving Redis-like semantics. `internal/store/store.go` — keyspace map with `Get/Set/Delete/Exists/Len`, deliberately lock-free because access is single-goroutine by construction. `cmd/antares` now creates the store + executor and wires `executor.Handle`; `stopgap.go` is deleted. Differential smoke and binaries run through the real executor.
- Files touched: `internal/server/executor.go`, `internal/server/executor_test.go`, `internal/store/store.go`, `internal/store/store_test.go`, `cmd/antares/main.go`, `tests/differential/smoke_test.go`, removed `internal/server/stopgap.go`, `docs/{PLAN,DECISIONS,FILE_MANIFEST,PROJECT_LOG}.md`, `README.md`.
- Issues: none. Micro-decision logged: the ADR-002 backpressure note now says bounded queue + blocking sends (no rejection policy), because rejecting a parsed command would break Redis semantics; memory is bounded by 1024 queued requests plus one blocked connection goroutine per connection. Also avoided a subtle shutdown deadlock: the executor stays alive while connections drain, and is closed only after `Server.Shutdown` returns.
- ADRs written: none (ADR-002 trade-off line updated to the implemented policy).
- QBank updated: none.
- Exit evidence: `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` all clean; serialization test runs 200 concurrent callers against an unguarded counter and store, observes exactly 1..200 with no race; live `bin/antares -port 6381` served PING/ECHO/arity/unknown/`COMMAND DOCS` and shut down cleanly on SIGTERM.
- Next session first task: P2.3 — SET (EX/PX/NX/XX/GET/KEEPTTL), GET, DEL, EXISTS, TYPE; differential batch 1 green.

---

### 2026-10-20 — P2.3: string core (SET/GET/DEL/EXISTS/TYPE)

- Built: `internal/server/string_commands.go` — SET with EX/PX/NX/XX/GET/KEEPTTL (options case-insensitive; duplicate NX/XX/GET/KEEPTTL and same-unit EX/PX allowed; NX+XX, KEEPTTL+expiry, and EX↔PX conflicts are `ERR syntax error`; expiry values use Redis string2ll semantics, so `+10`/`010`/`1.5` are `ERR value is not an integer or out of range`, and non-positive/overflowing times are `ERR invalid expire time in 'set' command`). SET validates expiry before the NX/XX condition, returns the old value for GET (nil when absent), and preserves TTL only for KEEPTTL. GET, DEL (duplicates count once), EXISTS (duplicates count each time), and TYPE. `internal/store/store.go` now holds absolute Unix-millisecond expiries with lazy delete-on-access, `Set` clearing TTL, `SetWithTTL`, and `SetKeepTTL`; `Len` skips expired keys. `protocol.ParseInt` exposes the existing string2ll-faithful parser; `protocol.SyntaxError`, `protocol.NotInteger`, and `InvalidExpireTime` pin the captured error bodies.
- Files touched: `internal/server/string_commands.go`, `internal/server/string_commands_test.go`, `internal/server/executor.go` (register string commands by default), `internal/store/store.go`, `internal/store/store_test.go`, `internal/protocol/parse.go`, `internal/protocol/errors.go`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,PROJECT_LOG}.md`, `README.md`.
- Issues: none. Design trap avoided: expiries are stored as int64 Unix milliseconds, not `time.Time`/`time.Duration` — Redis accepts PX values beyond Go's ~292-year `time.Duration` range (captured: `PX 99999999999999999` is valid), so duration arithmetic would have wrapped. Redis option-validation order also differs from the obvious implementation: NX+XX and KEEPTTL+expiry conflicts are rejected before the expire value is parsed, which is why `EX abc NX XX` is a syntax error rather than an integer error; the parser now follows that order. Lazy expiry landed here (rather than P3.2) because SET EX/PX is otherwise observably wrong; the active cycle and clock injection remain P3.
- ADRs written: none.
- QBank updated: none.
- Exit evidence: `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` all clean; live differential battery against digest-pinned Redis 7.4.11 — 67 command/expiry checks plus 30 option/edge checks, **0 mismatches** (only SET EXAT/PXAT differ by design: out of the EX/PX plan scope, returns `ERR syntax error`).
- Next session first task: P2.4 — MSET/MGET (atomic), INCR/DECR/INCRBY/DECRBY/INCRBYFLOAT (+ errors), APPEND, STRLEN, GETSET, SETNX; differential batch 2 green.

---

### 2026-10-20 — P2.4: string mutations and INCR family

- Built: MSET/MGET; INCR/DECR/INCRBY/DECRBY with Redis's string2ll semantics, overflow wording (`ERR increment or decrement would overflow`), and the special `DECRBY ... -9223372036854775808` rejection (`ERR decrement would overflow`); INCRBYFLOAT; APPEND/STRLEN; GETSET; SETNX. In-place writes preserve TTL via `Store.SetKeepTTL`; MSET and GETSET clear it. MSET validates pair parity before writing, so an odd argument list is a wrong-arity error and no partial write happens. APPEND enforces the 512 MiB `proto-max-bulk-len` ceiling (`ERR string exceeds maximum allowed size (proto-max-bulk-len)`) before allocating; a missing key appended with an empty value still creates a live empty string (encoded as `$0`, never `$-1`). `internal/protocol/errors.go` pins the five new error bodies; `internal/server/string_commands.go` holds the handlers.
- INCRBYFLOAT fidelity: Redis formats with C `long double` and `%.17Lf`, trimming zeros; the pinned container is aarch64, where `long double` is IEEE binary128 (113-bit mantissa), so Go float64 drifts in the 17th decimal. Implemented with `math/big.Float` at 113 bits: correctly rounded decimal/hex parsing, binary128 overflow and half-min-subnormal bounds, explicit `inf`/`infinity` handling, `nan` rejection, Redis's 5119-byte value cap, `.17f` output trimming, and `-0` normalization. Capture also showed Redis accepts hex floats without `p` (`0x1.8`), so the parser appends `p0`.
- Files touched: `internal/server/string_commands.go`, `internal/server/string_commands_test.go`, `internal/protocol/errors.go`, `docs/{PLAN,DECISIONS,COMPATIBILITY,FILE_MANIFEST,INTERVIEW_QBANK,PROJECT_LOG}.md`, `README.md`.
- Issues: none. Two traps caught by capture rather than assumption: `DECRBY k -9223372036854775808` is not an overflow of the value but of the negation, and it is checked before the stored value is parsed; INCRBYFLOAT on `1e-4950` succeeds and prints `0` (subnormal, no ERANGE) whereas `1e-4966` is `ERR value is not a valid float`; APPEND allows exactly 512 MiB and rejects only the next byte. `INCRBYFLOAT` result range checks enforce overflow but deliberately allow arithmetic underflow to print `0`, matching Redis.
- ADRs written: ADR-021 — INCRBYFLOAT uses binary128 to match the pinned aarch64 oracle; the platform dependency is recorded in COMPATIBILITY.
- QBank updated: Q11 evidence now includes the 934-check string differential; new Q14 covers bit-exact INCRBYFLOAT; Q13 lists the precision divergence.
- Exit evidence: `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` all clean; live `bin/antares -port 6381` vs pinned Redis 7.4.11 — 178 structured P2.4 checks and 756 randomized INCRBYFLOAT checks (including 5119-byte values, hex floats, subnormals, and overflow boundaries), **0 mismatches**; TTL-preservation covered by unit tests with expiry waits; clean SIGTERM shutdown.
- Next session first task: P2.5 — WRONGTYPE path (needs the minimal type tag before collections), empty-value edge differential, large-payload guard (536MB proto limit), numeric overflow edges; then P2.6 differential harness v0.

---

### 2026-10-20 — P2.5: string edge-hardening (empty values, limits, overflow, WRONGTYPE contract)

- Built: edge tests for empty key names and empty values across every string command; binary values containing NUL/CRLF through SET/GET/APPEND/MGET; case-insensitive command/option spelling; 1 MiB values and 2000-key MSET/MGET/DEL/EXISTS batches; integer boundaries (`INCRBY -9223372036854775808` on 0/-1, `DECRBY 9223372036854775807` twice, `DECRBY -9223372036854775808` precedence over a non-integer stored value, unchanged values after failed mutations); float error precedence and overflow; expired-key interactions for GET/TYPE/EXISTS/DEL/MGET/INCR/APPEND/GETSET/SETNX/MSET/SET NX/XX/KEEPTTL; TTL preservation for INCR/APPEND/INCRBYFLOAT/failed SETNX/failed INCRBY, and TTL clearing for GETSET/MSET.
- Limits: verified the protocol parser's `$536870913` rejection from P1 already covers the wire ceiling; added `joinAppend(old, add, limit)` so the APPEND 512 MiB branch is unit-tested without allocating 512 MiB (at-limit accepted, one byte over rejected, empty append stays non-nil); pinned `protocol.StringTooLong`. The runtime limit is fixed until P5.3 (`CONFIG SET`), now recorded in COMPATIBILITY.
- WRONGTYPE: captured the exact body from a list-valued key (`WRONGTYPE Operation against a key holding the wrong kind of value`) and pinned it as `protocol.WrongType`. The live matrix is deliberately deferred to P4.4: with only a string type in the store there is no reachable wrong-type path, and adding a speculative kind/union now would be replaced by P4.1's real value model.
- Files touched: `internal/server/string_commands.go` (append helper), `internal/server/string_commands_test.go`, `internal/protocol/errors.go`, `internal/protocol/errors_test.go`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,INTERVIEW_QBANK,PROJECT_LOG}.md`, `README.md`.
- Issues: none. Deliberate scope deviation logged: P2.5's WRONGTYPE item is contract-only; P4.4 owns the cross-type matrix. A full 512 MiB APPEND runtime test remains skipped (memory/CI cost, and the reference boundary was captured at a reduced `proto-max-bulk-len`); the branch is covered by the limit-parameterized unit test.
- ADRs written: none.
- QBank updated: Q11 evidence now includes 1056 checks; Q13 adds the fixed runtime bulk limit.
- Exit evidence: `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` all clean; live `bin/antares -port 6381` vs pinned Redis 7.4.11 — 122 edge checks including expiry waits and 1 MiB/2000-argument batches, **0 mismatches**; combined string differential stands at 1056 checks, zero mismatches; clean SIGTERM shutdown.
- Next session first task: P2.6 — differential harness v0: spawn the pinned reference, encode a reusable string command corpus, canonicalize replies, and wire `make diff-test` to run it against Antares with zero mismatches.

---

### 2026-10-20 — P2.6: differential harness v0

- Built: `tests/differential/harness_test.go` starts an in-process Antares on an ephemeral port and builds RESP2 go-redis clients with identity commands disabled; `tests/differential/diff_test.go` runs a 93-step string corpus (SET options, MSET/MGET, INCR family, INCRBYFLOAT edges, APPEND/STRLEN/GETSET/SETNX, empty/binary/1 MiB values, 1000-key batches, syntax/arity/unknown errors) plus an expiry/TTL scenario, comparing every reply after canonicalization (nil, error string, scalar, array). `outcome` maps go-redis's `redis.Nil` to nil, and errors compare by exact server message. The suite skips unless `REDIS_REF_ADDR` is set, so `make test` stays Docker-free; `make diff-test` starts the pinned compose service and sets the address. The P1 smoke now shares the harness helpers. Corpus keys are namespaced with a nanosecond prefix so repeated runs cannot collide with manual reference data.
- Files touched: `tests/differential/{harness_test,diff_test,smoke_test}.go`, `Makefile`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,INTERVIEW_QBANK,PROJECT_LOG}.md`, `README.md`.
- Bug: the first harness run failed the expiry scenario with `GET = <nil>, redis: nil`. Diagnosis: `Cmd.Result()` surfaces a null bulk as `redis.Nil`, and the assertion treated any error as failure, unlike the canonicalizer. Fix: route those checks through the same `outcome` helper. Prevention: all corpus and scenario comparisons now use one canonical path; raw `Do(...).Result()` is no longer interpreted inline.
- ADRs written: none (implements ADR-017; no new trade-off).
- QBank updated: Q11 now cites the repeatable harness.
- Exit evidence: `make diff-test` green — 94 subtests (93 corpus steps + expiry/TTL), zero mismatches; `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` clean; reference container already healthy and idempotently reused.
- Next session first task: P3.1 — implement the expires dictionary and TTL/PTTL/PERSIST, EXPIRE/PEXPIRE/EXPIREAT/PEXPIREAT with NX/XX/GT/LT; extend the differential harness with the TTL batch.

---

### 2026-10-20 — P3.1: expires dict and the TTL family

- Built: refactored the store from an inline `entry{value, expiresAt}` to separate `data` and `expires` dicts, matching Redis and letting the P3.3 active cycle sample only volatile keys. Added `ExpiresAt`, `TTLms`, `SetExpire`, and `Persist`; lazy expiry now deletes from both dicts. Added `internal/server/ttl_commands.go`: TTL/PTTL (`-2` missing, `-1` persistent, nearest-second rounding `(ms+500)/1000`), PERSIST, and the four EXPIRE variants with NX/XX/GT/LT, including Redis's non-volatile-is-infinite rules and delete-on-past-deadline behaviour. Registered them from the default executor.
- Capture findings that changed the implementation:
  - Redis validates options before the TTL integer, and reports the first unsupported option immediately; option conflicts are checked only after the full option list, and both fire before the missing-key lookup. `EXPIRE missing abc FOO` is `ERR Unsupported option FOO`, not an integer error.
  - TTL rounds to the nearest second, not floor/ceil: PX 400 → TTL 0, PX 1400 → 1, PX 1600 → 2.
  - Expiry is strict: a key is dead only when `now > deadline`, so a deadline equal to the current millisecond still yields PTTL 0 rather than a missing key. The lazy check was changed from `>=` to `>`.
  - GT on a non-volatile key always fails; LT always succeeds, even with a past deadline (which deletes the key). XX requires an existing TTL; NX requires none; repeated options are allowed; XX may combine with GT/LT.
  - Overflow is unit- and base-specific: EXPIRE/EXPIREAT reject seconds that would overflow `seconds*1000` (so EXPIREAT MinInt64 is invalid), relative millisecond commands reject only upper addition overflow (PEXPIRE MaxInt64 invalid), PEXPIRE MinInt64 deletes immediately, and PEXPIREAT MaxInt64 is a valid far-future deadline.
- Files touched: `internal/store/{store.go,store_test.go}`, `internal/server/{executor.go,ttl_commands.go,ttl_commands_test.go}`, `tests/differential/diff_test.go`, `docs/{PLAN,COMPATIBILITY,FILE_MANIFEST,INTERVIEW_QBANK,PROJECT_LOG}.md`, `README.md`.
- Issues: the first TTL corpus draft exercised PEXPIRE on a missing key and lowercase XX on a persistent key, so both compared `0` instead of the success path; added the missing setup steps before claiming coverage. No product bug.
- ADRs written: none (implements ADR-005/ADR-009 groundwork).
- QBank updated: Q11 cites the 199-subtest harness and TTL family.
- Exit evidence: `make diff-test` green — 199 subtests (198 corpus steps + expiry scenario), zero mismatches; new unit tests cover rounding, lazy expiry, relative/absolute variants, the condition matrix, and every captured error; `go test ./...`, `go test -race ./...`, `go vet ./...`, `gofmt -l .` clean.
- Next session first task: P3.2 — audit every read path for lazy expiry, pin delete-on-access semantics, and add DBSIZE consistency coverage where it lands.
