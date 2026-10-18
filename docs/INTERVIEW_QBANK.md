# Interview Q-Bank — Antares + Observatory

> Purpose: the rehearsal bank. Every probe a real interviewer might ask, with a spoken-answer outline and the artifact that proves it.
> Rules: maintained by the AI harness whenever a decision lands or a correction surfaces; every answer points at evidence (ADR / test / doc) — no hand-waving.
> Sections: Narrative · Decisions · Verification · Boundaries · Answer notes.

---

## 1. Narrative

### Q1. Walk me through this project. (30–60s)

Antares is a Redis-compatible server I'm building from scratch in Go — RESP2, ~40 commands, TTL expiry, framed AOF persistence, approximated-LRU eviction, transactions, and replication with both full and partial resync. It ships with the Observatory: a separate process and web UI that visualizes the server's internals — fsync events, eviction decisions, expiry sampling, replication offsets — and renders only numbers produced by a verification pipeline, never hand-typed ones. Every claim in the repo is machine-verifiable: differential tests against a pinned real Redis, crash tests, fuzzing, and benchmarks in CI.
Evidence: `README.md`, `docs/PLAN.md` §1–§3.

### Q2. Why the name "Antares"?

α Scorpii is the red star called "the rival of Mars" — a red rival to Redis, built to be observed through its Observatory. It's a memorable hook, and every demo section (Replication Lab, Durability Lab) is named after what it actually does.
Evidence: `README.md`; PLAN §name.

---

## 2. Decisions (the "why X over Y" probes)

### Q3. Why Go for a Redis clone?

Goroutine-per-connection maps naturally onto the Redis architecture without async coloring; the stdlib gives `testing`, `-race`, native fuzzing, `pprof`, and `net` — exactly the tools the verification plan needs. Iteration speed under a fixed time budget mattered more than peak runtime, and the race detector makes "no data races" machine-checkable instead of a promise.
Evidence: ADR-001. Trade-off: GC pauses vs Rust/C — measured honestly in `BENCHMARKS.md` (P9.4).

### Q4. Redis is single-threaded. How did you model its concurrency in Go?

One executor goroutine owns the keyspace; connection goroutines do I/O and protocol only, handing commands to the executor over a channel. That reproduces Redis's sequential semantics exactly, so multi-key atomicity and MULTI/EXEC fall out for free, and the race detector is clean by construction. It also gives one choke point for AOF append ordering, the event tap, and replication offsets.
Evidence: ADR-002. Ceiling: one core for execution, same as Redis — measured in `BENCHMARKS.md`.

### Q5. Why RESP2 instead of RESP3?

Full RESP3 buys push messages, maps, and client-side caching — all features tied to non-goals like pub/sub. RESP2 covers every command in scope including replication, and a `HELLO` downgrade stub keeps modern redis-cli and go-redis working with `proto: 2`. RESP3 breadth would cost weeks for zero added verification value.
Evidence: ADR-003; compatibility kept honest in `COMPATIBILITY.md`.

### Q6. Why not Docker Desktop or OrbStack?

Licensing and auditability. Docker Desktop requires paid subscriptions for larger companies; OrbStack is proprietary and free for personal use only, with an unauditable privileged helper. colima is MIT, runs on lima, and exposes the identical `docker compose` interface — the migration was a config-only change. I logged the policy as an ADR: OSS or free-to-use only, with exactly one documented exception (the Redis test reference, which is never redistributed).
Evidence: ADR-020; `docker-compose.yml`.

### Q7. Why pin the Redis image by digest?

A tag like `7.4.11` can be re-pushed or drift; a digest pins the exact image-index bytes. Since differential tests treat this image as ground truth, its identity must be immutable — same reasoning as pinning Tcl suite versions (P9.2).
Evidence: ADR-020; `docker-compose.yml` digest `sha256:4fa24486…`.

### Q8. Why is the Observatory a separate process instead of HTTP endpoints in the server?

Three reasons: the server stays a faithful Redis-compatible artifact with zero HTTP surface; failure isolation — restarting the UI never touches data; and the bridge can hold connections to Antares and real Redis simultaneously, which is what makes the side-by-side differential view possible.
Evidence: ADR-013.

### Q9. Redis forks for snapshots. You can't fork a Go program safely — what's your plan?

The executor takes a point-in-time copy (bounded pause, no I/O inside the critical section), serializes it outside the executor, buffers concurrent writes during serialization, then drains the buffer. Overflow policy: abort the rewrite / disconnect the slow replica and retry. Correctness is preserved and the pause is measured; the memory cost is dataset × active snapshots.
Evidence: ADR-010 (proof tests arrive P6.6/S1.3).

---

## 3. Verification ("how do you know it works?")

### Q10. How do you verify a Redis clone?

A six-layer pyramid, all CI-enforced: unit tests; differential tests against a pinned real Redis using an independent client (go-redis) — using my own client for verification would be circular; the official Tcl suite subset (`unit/expire`, `unit/type/incr`); chaos tests (100× SIGKILL, torn writes, replica kill/reconnect); a DST shadow-model for random interleavings; and fuzzing on the parser and frame recovery. Benchmark and test numbers are emitted into `claims.json`, and the Observatory renders only those.
Evidence: PLAN §9; ADR-017; ADR-018.

### Q11. What's the strongest verified evidence in the repo right now?

Honest answer: it's early — P0 is foundations. What is already machine-verified: CI (gofmt + vet + build + race tests) is green on every push (run #1, commit `80427de`), and `redis-cli -p 6379 PING` returns PONG against the digest-pinned 7.4.11 reference that all future differential tests will use. The first real server evidence arrives at P1.x/P2.x with parser and command tests.
Evidence: GitHub Actions run [39138833199](https://github.com/silent-knight19/Antares/actions/runs/39138833199); `PROJECT_LOG.md` P0.2 entry.

---

## 4. Boundaries ("what does it NOT do?")

### Q12. What does Antares not implement, and why?

Deliberate non-goals, each with a recorded reason: Lua scripting, cluster/slots, Sentinel, Streams, RESP3 beyond the HELLO downgrade, modules, listpack/intset encodings, keyspace notifications, pub/sub, GEO/HLL/Bitmaps, sharded keyspace, and auth/ACL. They were cut to protect the verification depth and the replication story — and each cut is an interview answer with a reason, not a gap I haven't thought about.
Evidence: ADR-012, ADR-019 (Observatory non-goals).

### Q13. Where is Antares weaker than real Redis?

Known, documented divergences: WATCH uses value-snapshot comparison (a write-then-restore-identical-value does not abort EXEC, while Redis aborts); persistence formats are intentionally not RDB/AOF-file-compatible; no compact encodings (listpack/intset); snapshot transfer for full resync is a framed command stream, larger than RDB for big values.
Evidence: `COMPATIBILITY.md`; ADR-004, ADR-007, ADR-008.

---

## 5. Answer notes (folded into answers as they stabilize)

1. `claims.json` is the provenance store the Observatory renders from (ADR-018) — never describe it as scratch output.
2. 10× bottlenecks: executor saturation first; group commit (`everysec`) decouples fsync from the executor, which stalls only under `fsync=always`; second-order: parser allocations / GC pressure.
3. Digest-bump process: pull the new release → resolve the index digest → update `docker-compose.yml` + `COMPATIBILITY.md` → log the why (CVE/version) in PROJECT_LOG → re-run differential + Tcl suites before accepting.
4. Protocol fidelity comes from capture, not memory: Redis 7.4 accepts multibulk counts up to INT_MAX, silently skips counts ≤ 0, blinds through the byte after a header `\r`, never validates bulk trailers, and takes bare-LF inline. Three of those overturned assumptions in P1.2 — verify against the reference binary, then pin it in tests.
