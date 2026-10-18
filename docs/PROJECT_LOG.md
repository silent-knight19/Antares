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
- AI quiz / grill: <score, weak spots> (daily 5-question quiz; full grill at phase gates)
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
- AI quiz / grill: n/a (P0 gate runs at P0.3 close).
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
- AI quiz / grill: n/a (P0 gate runs at P0.3 close).
- Next session first task: P0.3 — seed the tracking docs.

---

### 2026-10-18 — P0.3: Tracking system seeded

- Built: the four living docs — `PROJECT_LOG.md` (this file, first entries), `FILE_MANIFEST.md` (every tracked file mapped + hand-own commitments for the critical path), `INTERVIEW_QBANK.md` (seeded with 12 questions + boundary list), `COMPATIBILITY.md` (stub: pinned versions, planned divergences, licensing note); README status and documentation table refreshed.
- Files touched: `docs/PROJECT_LOG.md`, `docs/FILE_MANIFEST.md`, `docs/INTERVIEW_QBANK.md`, `docs/COMPATIBILITY.md`, `README.md`, `docs/PLAN.md` (current-position line).
- Issues: none.
- ADRs written: none.
- QBank updated: seeded with Q1–Q12 (narrative, decisions, verification, boundaries) — weak-spots section opens at the P0 grill.
- Exit evidence: first PROJECT_LOG entry written (this file); `FILE_MANIFEST.md` covers every tracked file — `git ls-files` count matches (37 after this commit), no orphans; all cross-references resolve.
- AI quiz / grill: P0 phase-gate grill pending — to be recorded here with score and weak spots before P1.1 starts.
- Next session first task: run the P0 phase-gate AI grill (PLAN §8; pass ≥8/10 required); then P1.1 — TCP listener with graceful shutdown, connection limits/timeouts, and accept/close/shutdown unit tests.
