# Decision Log (ADRs) — Antares + Observatory

> Purpose: every non-trivial decision recorded in interview shape — context, options, why chosen, why rejected, trade-offs, revisit conditions, the interview question it answers, and the proof that backs it.
> Rule: a decision made in a session is logged in the same session. Superseded decisions get `Status: Superseded by ADR-XXX` — never deleted.

## Template (copy for new entries)

```
## ADR-XXX — <Title>
- Date: YYYY-MM-DD · Phase: <P#/S#/U#> · Status: Accepted | Superseded by ADR-YYY | Proposed
- Context: <what forced this decision now>
- Options: A. <opt> — <pros/cons> · B. ... · C. ...
- Decision: <what we chose, one line>
- Why chosen: <3–5 bullets, mechanism-level reasons>
- Why rejected: <per-option, one line each — the interviewers' favorite part>
- Trade-offs accepted: <what we lose, measured if possible>
- Revisit if: <falsifiable trigger>
- Interview Q&A: <the questions this pre-answers + the 3–5 line spoken answer outline>
- Proof: <test names / benchmarks / doc references that back the claims>
```

---

## ADR-001 — Language: Go
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Implementation language for a Redis-compatible server under a fixed budget (~3h/day, AI harnesses). Must support: high-concurrency network I/O, rigorous testing (race detection, fuzzing), fast iteration, and strong resume signal for SDE-1 roles in India.
- Options:
  - A. Go — goroutines, stdlib networking/testing/fuzzing, race detector, static binaries, large backend job market.
  - B. Rust — strongest systems signal, no GC; slower iteration under fixed budget.
  - C. C — maximum fidelity to Redis, manual memory management.
  - D. Java — mature runtime and tooling; GC behavior obscures latency stories.
- Decision: Go.
- Why chosen:
  - Goroutine-per-connection maps naturally onto a Redis-style architecture without async coloring.
  - Stdlib gives us `testing`, `-race`, native fuzzing, `pprof`, `net`, `bufio` — the exact tools our verification pyramid needs (no dependency sprawl).
  - Iteration speed matters more than peak runtime for finishing v1+v2+v3 in budget.
  - Race detector makes "no data races" a machine-checkable claim, not a promise.
  - Hiring signal: Go is a common backend stack at Indian product companies (Uber, Zomato, ShareChat, Swiggy).
- Why rejected:
  - Rust: borrow-checker friction + rewrite cycles risk not finishing replication; our systems depth shows through architecture regardless of language.
  - C: memory-safety burden shifts effort from distributed-systems learning (expiry/eviction/replication) to pointer management; tooling for testing is weaker.
  - Java: GC pauses make latency benchmarking murky; heavier runtime for a single-binary demo; less idiomatic for this TCP-server shape.
- Trade-offs accepted: GC pauses vs C/Rust (measured and reported honestly in BENCHMARKS.md); no `fork()` (see ADR-010).
- Revisit if: benchmark targets are unreachable and profiling shows GC-pause-dominated p99.
- Interview Q&A:
  - "Why Go for a Redis clone?" → goroutines for I/O + single-executor for semantics; stdlib race detector/fuzzing directly power my verification claims; iteration speed under a time budget; Go is also the stack of most teams I'm applying to.
  - "Why not Rust?" → I evaluated it; correctness tooling is superb, but my bottleneck was finishing replication, not memory safety. Honest trade-off, logged in ADR-001.
- Proof: ADR-002 (concurrency model), race-clean CI job, `make verify`.

---

## ADR-002 — Concurrency model: actor-style single executor
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Redis executes commands sequentially on one thread. We need the same observable semantics (atomic commands, MULTI/EXEC, consistent expiry/eviction) in a concurrent Go server without data races.
- Options:
  - A. Global `sync.RWMutex` around the keyspace.
  - B. Actor model: connection goroutines send to a single executor goroutine owning the keyspace.
  - C. Per-key / sharded locks.
  - D. Lock-free structures with CAS.
- Decision: B — one executor goroutine owns all keyspace state; connections only do I/O + protocol.
- Why chosen:
  - Exactly reproduces Redis's sequential semantics → differential tests against real Redis become meaningful; no interleaving surprises.
  - Multi-key atomicity (MSET, MULTI/EXEC, WATCH checks) falls out for free because all mutations serialize through one queue.
  - Race detector clean by construction — the shared-state boundary is one channel.
  - Single choke point is the natural hook for: AOF append ordering (ADR-004), event tap (ADR-014), eviction/expiry cycles, replication offset ordering.
  - DST shadow-model tests (P8.3) are only meaningful if there's a linear execution order — this model provides it.
- Why rejected:
  - A: workable but lock discipline across 35+ commands invites subtle bugs; transaction atomicity and expiry-vs-command interleavings get hairy; no natural event ordering hook.
  - C: per-key locks cannot express multi-key atomicity and reintroduce deadlock analysis; complexity without observable benefit.
  - D: enormous complexity; no observable advantage over a serialized executor for our targets.
- Trade-offs accepted: command execution bounded by one core (~Redis-level throughput; matches benchmark targets); executor backpressure is a bounded request queue (1024) with blocking sends — connections wait rather than having commands rejected, preserving semantics under load.
- Revisit if: benchmarks show executor saturation and sharding becomes desirable — explicitly a non-goal (ADR-012).
- Interview Q&A:
  - "Redis is single-threaded — how did you model that in Go?" → one executor goroutine owns the keyspace; connection goroutines only parse/forward; atomicity and ordering for free; race detector proves it.
  - "What's your throughput ceiling?" → one core for execution, same as Redis; I/O and parsing are concurrent; measured in BENCHMARKS.md.
- Proof: P2.2 serialization test; `-race` CI; AOF/appended order = execution order tests.

---

## ADR-003 — Protocol: RESP2-first with HELLO downgrade stub
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Wire protocol scope. Modern redis-cli sends `HELLO` on connect; RESP3 adds types (maps, sets, push, big numbers) and negotiation.
- Options:
  - A. RESP2 only, no HELLO — redis-cli and many clients fail/warn.
  - B. RESP2 full + `HELLO` response in RESP2 form advertising `proto: 2`.
  - C. Full RESP3.
- Decision: B.
- Why chosen:
  - `HELLO` replying with a RESP2-formatted map (`proto:2`) keeps redis-cli, go-redis, Jedis and others working with zero RESP3 types.
  - Every command we implement (v1–v3, replication) is expressible in RESP2.
  - RESP3 breadth (push messages, verbatim strings, big numbers, attribute types) buys little interview value and costs 2–3 weeks.
- Why rejected:
  - A: breaks real clients; "works with redis-cli" is a core demo claim.
  - C: large surface, pushes v2/v3 out; some RESP3 features only matter with pub/sub & client-side caching — both non-goals.
- Trade-offs accepted: RESP3-only client behaviors unavailable (documented in COMPATIBILITY.md); rare clients demanding proto 3 will differ.
- Revisit if: targeting RESP3 parity becomes a stated goal (non-goal today).
- Interview Q&A:
  - "RESP2 vs RESP3?" → inline/bulk/array/int/error vs maps/sets/doubles/push/big numbers; HELLO negotiation; I implemented RESP2 fully and a downgrade stub so modern clients just work.
  - "How do you know clients work?" → differential tests with go-redis + redis-cli runs in CI.
- Proof: P1.5 error-string equality tests; COMPATIBILITY.md; go-redis-based diff harness (ADR-017).

---

## ADR-004 — AOF frame format: `[u32 len][u32 CRC32][RESP payload]` with group commit
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Durability design. Redis's own AOF is a raw RESP command log relying on heuristics to truncate partial writes on recovery; we want per-record integrity and a format we can reuse for replication.
- Options:
  - A. Raw RESP command log (Redis-style).
  - B. Binary op-log (`Key + Value + Op`, type-tagged).
  - C. Framed: 4-byte length, 4-byte CRC32, then RESP command payload; fsync policies `always` / `everysec` / `no`; group commit (batch fsync for concurrent writers).
- Decision: C.
- Why chosen:
  - Per-frame CRC localizes corruption: recovery trusts the valid prefix instead of rejecting the whole file (option A's failure mode).
  - An incomplete final frame is a torn write, so recovery truncates it. A malformed or checksum-bad frame is not a torn write, so recovery refuses to start with `ErrCorrupt` unless `-aof-repair` is passed — this stops one bad byte in the middle from silently deleting every record after it (refined at P6).
  - RESP payload means replay goes through the same execution path as live commands — one code path; the differential harness already tests that path.
  - The same framing doubles as the replication stream format (ADR-007) — one serializer, two consumers.
  - Group commit amortizes fsync across concurrent writes, making `always` survive our throughput targets.
- Why rejected:
  - A: silent corruption risk; fragile recovery heuristics; no integrity story to discuss in interviews.
  - B: requires per-type serializers + versioning; replay diverges from command path; unusable for replication stream reuse.
- Trade-offs accepted: ~8 bytes/frame overhead (~10–15% on small commands — measured in BENCHMARKS); buffering adds write latency vs direct append (mitigated by group commit; quantified per policy).
- Revisit if: rewrite/replay time exceeds target for 1M commands, or write amplification shows in benchmarks.
- Interview Q&A:
  - "Why CRC per frame, not one checksum per file?" → tail corruption is the realistic failure; per-frame CRC lets us keep N-1 valid records and report truncation; whole-file CRC forces rejecting the valid prefix.
  - "Why keep commands instead of binary state?" → one replay path = the path my differential tests already cover; also it's the same bytes the replica consumes.
  - "Always vs everysec?" → always: ack only after fsync → zero loss (proven in crash tests); everysec: ≤1s window, 10× cheaper fsyncs; both shipped and benchmarked.
- Proof: `TestRecoverTornTailMatrix`, `TestRecoverCorruptFrameRefused`, `TestWriterGroupCommit`, `TestWriterRefusesAfterWriteFailure`, `make crash-test` (100× SIGKILL), BENCHMARKS.md fsync-policy table.

---

## ADR-005 — Expiration: lazy + active cycle (hz=10, sample 20, >25% repeat)
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Expired keys must disappear without per-key timers. Naive approaches leak memory or burn CPU. Must be deterministic to test.
- Options:
  - A. Per-key timers (`time.AfterFunc`).
  - B. Lazy-only: delete on access.
  - C. Redis-style: lazy on access + active sampling cycle (hz=10, sample 20 keys per iteration, repeat while >25% sampled keys expired, per-cycle time budget).
- Decision: C, with Redis's exact tuning parameters.
- Why chosen:
  - Lazy alone leaks keys that are never touched (canonical interview trap) — active cycle bounds memory.
  - Sampling is amortized O(1): no global expiry index to maintain; performance stays flat as expired-set grows.
  - Matching Redis parameters (hz, sample size, 25% threshold) keeps differential tests and the Tcl `unit/expire` suite meaningful; behavior parity is the project's core claim.
  - `CONFIG SET hz` supported → question "how would you tune it?" has a real answer.
- Why rejected:
  - A: unbounded timer goroutines, timer-heap thundering herd, non-deterministic under test, memory per TTL key.
  - B: correct-looking but leaks; RCU/touch patterns in real apps make leak permanent, not transient.
- Trade-offs accepted: expired-but-unsampled keys consume memory transiently (same as Redis; documented); active cycle competes with commands for executor time (bounded by per-cycle budget).
- Revisit if: cycle insertion latency appears in p99 → consider timing wheel (documented scale-up answer).
- Interview Q&A:
  - "How are expired keys actually removed?" → lazy + active sampling; explain the 25% loop like you built it because you did.
  - "Lazy-only — what breaks?" → memory leak of cold keys.
  - "How do you test expiry without sleeping?" → injected virtual clock (ADR-009).
- Proof: `make tcl-expire` (official suite), fake-clock cycle tests, Observatory Story 2 renders sampled/expired keys live.

---

## ADR-006 — Eviction: approximated LRU (24-bit clock, sample 5, eviction pool)
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: `maxmemory` needs a victim-selection policy under pressure. Exact LRU needs a global data structure; Redis deliberately approximates.
- Options:
  - A. Exact LRU: doubly-linked list + hash map of all keys.
  - B. Random eviction (`allkeys-random`-style only).
  - C. Redis-style approximation: 24-bit access clock per key (seconds resolution), sample 5 keys, eviction pool ranked by idle time, policies `noeviction` / `allkeys-lru` / `volatile-lru`.
- Decision: C.
- Why chosen:
  - Memory overhead of exact LRU (~2 pointers/key) is real at scale and not what Redis does — parity matters for differential behavior and for the interview story.
  - 5-sample approximation is documented (Redis) to be near-exact in hit-rate; we prove >90% hot-key retention in tests.
  - 24-bit seconds clock is the known Redis trick: cheap, and wraps safely (~194 days) — great depth signal.
  - Eviction pool improves quality beyond pure sampling-of-5 without global ordering.
- Why rejected:
  - A: per-key list overhead + maintenance; diverges from Redis behavior; buys nothing loyal to our targets.
  - B: poor hit rate under skew; no interesting decision to defend.
- Trade-offs accepted: approximation ≠ exact (documented); `volatile-lru` can thrash when volatile set is small (same as Redis; documented); victim choice is non-deterministic by design → tests assert quality bounds (retention %), not exact victims.
- Revisit if: retention test <90% → raise sample size (parameter, one-line change) and re-measure.
- Interview Q&A:
  - "How does Redis pick a victim?" → sample 5, compare idle time, eviction pool; explain wrap-safe clock.
  - "Why not exact LRU?" → memory per key + maintenance; 5-sample gets ~90%+ of the benefit — and I measured retention.
  - "What happens when nothing is evictable?" → `-OOM` error path on writes (noeviction / all-volatile-exhausted), diff-tested.
- Proof: retention-quality test (>90%), eviction event stream in tap, Observatory Story 3 visualizes sample→pool→victim.

---

## ADR-007 — Snapshot format shared by AOF rewrite and full resync
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Two features need "serialize the current dataset": AOF compaction (rewrite) and full resync to a replica. Two implementations would diverge and double the testing burden.
- Options:
  - A. Separate mechanisms: rewrite writes its own file format; resync streams raw AOF/appends.
  - B. Shared snapshot: executor produces a point-in-time copy; a single serializer emits framed (ADR-004) command-sequence snapshot; consumers: rewrite writes it to a temp file; replication streams it over the socket.
- Decision: B.
- Why chosen:
  - One serializer = one correctness surface: recovery-from-rewrite and resync-replay both validate the same bytes; differential replay proves both.
  - Full resync conceptually becomes "AOF written to a socket" — same framing, same replay path on the replica; sync and persistence bugs can't diverge.
  - Snapshot taken from the executor's point-in-time copy (ADR-010), so consistency is structural, not heuristic.
- Why rejected:
  - A: two serializers drift; replay bugs get fixed in one path and not the other; double tests for the same property (dataset equality).
- Trade-offs accepted: resync payload is commands, not a compact binary state dump — larger transfer than RDB for big values (documented; acceptable at target dataset sizes); replica apply time proportional to dataset op count (measured in S3.2).
- Revisit if: resync size/time becomes a benchmark failure → add compact binary variant, keep framing.
- Interview Q&A:
  - "How does a fresh replica get the dataset?" → `+FULLRESYNC`: snapshot stream in framed command form + concurrent-write buffer, then backlog replay; then `+CONTINUE` on reconnects.
  - "Why no RDB-equivalent file?" → I reused the rewrite format; one format, two consumers, one replay path — fewer bugs.
- Proof: dataset-equality test after full resync; rewrite state-equality test; `TestReplay_EqualsDiffBehavior`.

---

## ADR-008 — WATCH semantics: value-snapshot compare (documented divergence)
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Redis WATCH invalidates on any touch of a watched key (internal dirty-flag). Replicating touch-tracking exactly needs per-key watcher bookkeeping and subtle edge semantics.
- Options:
  - A. Exact Redis semantics: per-key version/touch counters; any write to watched key invalidates at EXEC.
  - B. Value-snapshot: capture watched key's serialized value/hash at WATCH time; at EXEC, abort if current value differs.
- Decision: B, divergence documented in COMPATIBILITY.md.
- Why chosen:
  - Same observable outcome for all realistic patterns (concurrent writer changes the key → EXEC aborts).
  - State per connection is bounded and simple: snapshot size × watched keys; no per-key watcher lists to maintain/prune on disconnect.
  - Deterministic comparison makes the DST shadow-model trivial to write (model = HashMap of snapshots + compare).
- Why rejected:
  - A: correct-but-expensive bookkeeping (watchers cleaned up on client death, touch propagation across expiry/eviction/flush paths — many footguns for marginal parity); its only observably different case is write-then-restore-identical-value.
- Trade-offs accepted: divergence case — key written and restored to identical bytes: Redis aborts EXEC, we allow it. Documented; Tcl transaction subset runs with documented skips.
- Revisit if: full Tcl transaction parity becomes a goal (non-goal today).
- Interview Q&A:
  - "How does WATCH/EXEC work in your server?" → snapshot value at WATCH, compare at EXEC, abort with nil reply on change.
  - "How does real Redis do it?" → dirty/touch flags per watched key; why I diverged + which corner differs. Honesty + precision is the point.
- Proof: WATCH test suite incl. divergence cases; COMPATIBILITY.md entry; DST interleavings (P8.3).

---

## ADR-009 — Injected virtual clock for all time-dependent logic
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: TTL expiry, active cycle, fsync intervals, replication timeouts, lag metrics, eviction idle-time — all wall-clock dependent. Sleep-based tests flake and can't exercise long horizons.
- Options:
  - A. Real time + `time.Sleep` in tests.
  - B. Injected clock interface (`Now()`, `Since()`) with real implementation + fake implementation advanced manually in tests.
  - C. Monkey-patched/global time functions.
- Decision: B; one clock instance flows from `main` through executor, store, expiry, repl.
- Why chosen:
  - Deterministic: "advance 100ms → assert exactly these keys expired" instead of sleeps that flake on CI load.
  - Fast: whole expire suites run in milliseconds; long-horizon behaviors (24h TTL rounding, clock wrap) testable.
  - Enables DST time control and precise active-cycle assertions (sample set, >25% loop).
- Why rejected:
  - A: flaky under load, slow, and the 25% repeat loop is nearly untestable deterministically.
  - C: global mutable time breaks parallel tests and hides dependencies.
- Trade-offs accepted: plumbing overhead (pass clock everywhere); review rule: no direct `time.Now()` in non-boot code (checked in code review + grep in CI).
- Revisit if: never — foundational.
- Interview Q&A:
  - "How do you test TTL expiry without waiting hours?" → virtual clock; show a test advancing time.
  - "Why not sleep-based tests?" → flake + non-determinism; this is the same reason FAANG infra teams avoid real time in unit tests.
- Proof: fake-clock usage across P3–P8 tests; CI grep for stray `time.Now()`.

---

## ADR-010 — No fork() in Go: snapshot via executor copy + write buffering
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Redis forks for RDB/BGSAVE so the child gets a copy-on-write snapshot while the parent serves writes. Go cannot `fork()` safely (runtime threads). AOF rewrite (P6.6) and full resync (S1.3) both need a consistent dataset snapshot without multi-second write stalls.
- Options:
  - A. Block writes for the entire snapshot duration.
  - B. Eager deep copy of dataset under executor, then serialize outside the executor.
  - C. B + per-consumer write buffer: copy under executor (bounded pause), serialize the copy outside, buffer concurrent writes during serialization, drain buffer at the end (rewrite: append to new AOF; resync: stream after snapshot). Overflow policy: abort rewrite / disconnect replica → retry full resync.
- Decision: C.
- Why chosen:
  - Executor pause = copy time only (no disk/socket I/O inside the critical section) — bounded and measured.
  - Write availability preserved; replicas converge via snapshot→buffer-drain ordering (offset-exact).
  - Overflow policy is explicit and testable (slow replica gets dropped, retries, still correct).
  - No OS-fork semantics to reason about; deterministic in tests; honest, interesting divergence story.
  - Memory cost ≈ dataset size × active snapshots — acceptable at target sizes; documented.
- Why rejected:
  - A: multi-second stalls violate the "keeps serving writes" design claim.
  - Real fork: unsafe with the Go runtime; cgo gymnastics not worth it.
- Trade-offs accepted: memory ≈ dataset × copies during rewrite/resync; large-dataset resync slower than COW fork (documented); copy pause grows linearly with dataset (measured; bounded at target sizes).
- Revisit if: copy pause visible in p99 at realistic dataset sizes → sharded/incremental snapshots (documented scale-up path).
- Interview Q&A:
  - "Redis forks for snapshots — what about Go?" → can't fork safely; copy under executor + write buffer; pause is copy-only; overflow policy drops slow replicas and retries — correctness preserved.
  - "Why is this acceptable?" → bounded pause, no data loss, and I measured the pause; trade-off table in ARCHITECTURE.md.
- Proof: rewrite concurrency test (`TestRewrite_ConcurrentWrites`), slow-replica disconnect + retry test (S2.3), pause measurement in BENCHMARKS.md.

---

## ADR-011 — Tracking system: 4 living docs + session protocol
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Interview deep-dives probe: why decisions, hardest bugs, testing method, ownership ("which part did you write?"), and boundaries ("what does it NOT do?"). We need artifacts that answer these probes and counter AI-assisted "can't defend my code" risk.
- Options:
  - A. README + code comments only.
  - B. Code comments + occasional blog post.
  - C. Four living docs, each mapped to a probe class + a 10/100/30 session protocol.
- Decision: C (spec in PLAN.md §8).
- Why chosen:
  - Each doc maps to a probe class: DECISIONS → "why X over Y"; PROJECT_LOG → "hardest bug / STAR stories"; FILE_MANIFEST → "explain this file / which part did you write"; INTERVIEW_QBANK → rehearsal + boundary list.
  - Full protocol every session (~30 min, ~8% overhead) keeps artifacts current; retroactive writing is where truthfulness dies.
- Why rejected:
  - A/B: no decision trail, no bug narratives, nothing to rehearse, no defense against "AI wrote it" skepticism.
- Trade-offs accepted: ~30 min/session overhead; discipline cost (enforced by phase gates).
- Revisit if: overhead exceeds ~20% of session time over a month → trim to DECISIONS + QBank.
- Interview Q&A:
  - "How do you track engineering decisions?" → show DECISIONS.md live.
  - "What was hard here?" → PROJECT_LOG has the bug, diagnosis, fix, prevention — tell it as STAR.
- Proof: this repo's docs/ directory.

---

## ADR-012 — Scope: v1+v2+v3 incl. replication; explicit non-goals
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: Fixed 3h/day budget; must finish to application-ready state; stakeholder is interview differentiability.
- Options:
  - A. v1 only (~5–6 weeks): KV + TTL + collections.
  - B. v1+v2 (~10–12 weeks): + AOF, eviction, transactions, verification.
  - C. v1+v2+v3 incl. PSYNC replication (~13 wks server + Observatory interleaved).
- Decision: C, with a timebox: if partial resync on reconnect (S2.2) is not working by end of week 19, freeze at full-resync-only and redirect sessions to UI polish + interview prep.
- Why chosen:
  - Replication with partial resync (backlog, offsets, `+CONTINUE`) is the single most differentiated SDE-1 project feature; it generates the best interview conversations.
  - v2 (AOF/eviction/txn) is table stakes for "systems project" credibility and feeds the Observatory stories.
  - Timebox makes the risk bounded: worst case is a polished v2.5, which still beats most resumes.
- Why rejected:
  - A: under-differentiated; many candidates have a KV store.
  - B: partial resync is where the deep understanding (offset math, backlog window) lives; skipping it loses the moat.
- Explicit non-goals (each is an interview "boundary" answer): Lua (VM complexity, no differentiation for SDE-1) · cluster/slots (multi-node product complexity) · Sentinel (needs consensus; huge) · Streams (independent subsystem, consumer groups) · RESP3 beyond HELLO downgrade (ADR-003) · modules (ABI/runtime design irrelevant to our claim) · listpack/intset encodings (memory micro-optimization, dilutes narrative) · keyspace notifications · pub/sub (easy but non-core; skipped to protect budget) · GEO/HLL/Bitmaps · sharded keyspace (ADR-002 revisit note) · auth/ACL.
- Trade-offs accepted: longer total project (~20–22 wks incl. UI); some "easy wins" (pub/sub) skipped deliberately.
- Revisit if: timebox triggers → execute freeze rule (non-negotiable).
- Interview Q&A:
  - "What does it NOT do?" → the non-goals list with a reason per item — this is a strength answer.
  - "Why replication for an SDE-1 project?" → backlog/offsets/backpressure; almost nobody at this level has built it.
- Proof: PLAN.md §5–§7; S2.2 timebox rule; FAILURE_MODES.md.

---

## ADR-013 — Observatory as a separate binary (not embedded HTTP in the server)
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: The showcase UI needs data (commands, keys, INFO, internals events). Options range from embedding an HTTP admin API in Antares to a fully separate bridge process.
- Options:
  - A. Embed HTTP admin server in `cmd/antares` (`--http` flag).
  - B. Separate `cmd/observatory` process that talks RESP/INFO/event-tap to the server.
  - C. Static SPA with no backend (impossible — needs live data).
- Decision: B.
- Why chosen:
  - The server stays a faithful Redis-compatible artifact: zero HTTP surface, zero protocol pollution — compat claims stay clean.
  - Failure isolation: bridge crash cannot affect data; restarting the UI never touches the server.
  - Side-by-side capability: the bridge can hold connections to Antares AND real Redis in one process — the differential view is only possible here.
  - `internal/client` gets dogfooded by the bridge daily; one more well-scoped distributed component in the architecture story.
- Why rejected:
  - A: pollutes the "drop-in Redis" positioning, shares failure domain, blocks the side-by-side feature, mixes tooling into the server's test surface.
  - C: no live internals.
- Trade-offs accepted: two processes (solved: docker compose demo profile + `make showcase`); negligible demo-path latency.
- Revisit if: single-binary demo becomes a hard requirement (e.g., WASM demo) — then reconsider.
- Interview Q&A:
  - "Why not just add an HTTP endpoint to your server?" → separation of concerns, zero compat pollution, isolated failure domain, and it enabled the side-by-side differential view.
- Proof: cmd/observatory tests; COMPATIBILITY.md shows server surface unchanged; demo profile.

---

## ADR-014 — Event tap: opt-in JSONL file (`--event-log`)
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: The Observatory must show internals (AOF frames, fsync events, eviction sampling decisions, expiry, replication sync/ack, recovery) that RESP/INFO cannot expose. The Durability Lab even needs events around a SIGKILL.
- Options:
  - A. Extension RESP command (`XREDIS.EVENTS` subscription).
  - B. Unix-socket event stream from the server.
  - C. Opt-in JSONL append-only file with rotation; bridge tails it.
  - D. INFO polling only.
- Decision: C.
- Why chosen:
  - Zero RESP-surface pollution: off by default; when off, a nil sink costs one branch; compat claim intact.
  - Crash-surviving: events are on disk before the kill — needed for Story 4 (SIGKILL → recovery narrative).
  - Replayable in tests (event-assertion integration tests); trivially inspectable (`tail -f`).
  - Rotation (64MB × 2) bounds disk; bridge handles inode changes.
- Why rejected:
  - A: pollutes RESP compat, dies with the process at the exact moment of crash (worst timing for the durability story).
  - B: sockets die with the server; lifecycle complexity.
  - D: no per-event granularity — cannot show eviction's sample→pool→victim reasoning, which is the point.
- Trade-offs accepted: file I/O cost when enabled (demo/dev only; documented); bridge must handle rotation.
- Revisit if: bidirectional interactive debugging is needed → that's REST control endpoints, orthogonal.
- Interview Q&A:
  - "How does the UI see internal events without changing your protocol?" → opt-in JSONL tap; disabled by default; survives crashes; zero compat impact.
- Proof: versioned event schema in internal/events; event-assertion tests; Story 3–5 consume it.

---

## ADR-015 — Transport: SSE + REST (not WebSocket)
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: SPA needs live streams (metrics at ~4Hz, events, recovery logs, benchmark results) and command RPC (exec, run bench, lab triggers).
- Options:
  - A. WebSocket for everything.
  - B. SSE (`EventSource`) for server→client streams + REST for client→server commands.
  - C. HTTP polling only.
- Decision: B.
- Why chosen:
  - `EventSource` has native auto-reconnect + `Last-Event-ID` resume — robustness by default, not code we hand-roll and test.
  - Streams are genuinely unidirectional; commands are request/response — the two transports match the two shapes.
  - Every endpoint curl-testable → E2E/CI can exercise the API without a browser; bridge tests stay simple.
  - HTTP/1.1 6-connection concern is irrelevant on localhost with one stream.
- Why rejected:
  - A: manual reconnect/backoff state machine to write and test, bidirectional complexity unused, harder to curl.
  - C: latency/jitter and load — wrong for a live internals view.
- Trade-offs accepted: no binary frames (unneeded); one transport distinction to explain (an interview plus, not a minus).
- Revisit if: a genuinely bidirectional high-frequency channel appears (e.g., stepped debugger) → add WS for that endpoint only.
- Interview Q&A:
  - "Why SSE over WebSockets?" → auto-reconnect + resume, fits unidirectional shape, curl-testable, simpler failure modes; I'd use WS for bidirectional control planes.
- Proof: bridge transport tests; Playwright E2E exercises reconnect path.

---

## ADR-016 — Frontend stack: React + TS + Vite + uPlot + Tailwind
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: SPA with 7 sections, 10Hz data ingest, canvas-grade charts, 100k+ key virtualization, and hiring-signal value in the Indian market — on a 3h/day budget.
- Options:
  - A. React + TypeScript + Vite + Tailwind + uPlot + TanStack (Query, Virtual) + Playwright.
  - B. Svelte + TS + Vite + uPlot.
  - C. Vanilla TS + micro-libraries.
  - D. Next.js (SSR).
- Decision: A.
- Why chosen:
  - React is the dominant JD requirement in target companies → portfolio signal beyond the backend story.
  - Ecosystem fit: TanStack Query (server-state), TanStack Virtual (100k keys), uPlot (canvas, 10Hz-safe, ~40KB).
  - Vite static build is served directly by the bridge; zero runtime complexity.
  - Playwright integration is first-class for CI story tests.
- Why rejected:
  - B: leaner but rarer in JDs; smaller data-grid ecosystem; less portfolio ROI.
  - C: too much manual work for 6 stories on this budget; no hiring signal.
  - D: SSR irrelevant for a localhost tool; adds runtime complexity with no benefit.
- Trade-offs accepted: bundle > Svelte's (budget enforced: <250KB gz initial JS, CI check); requires render discipline (decouple 10Hz ingest from 4Hz paint, memoization, canvas charts, virtualization).
- Revisit if: budget cannot be met after code-splitting → reassess chart lib first.
- Interview Q&A:
  - "Why React on a systems project?" → hiring signal + ecosystem fit + honest scope call; "how do you keep 10Hz dashboards smooth?" → decoupled render cadence, canvas charts, virtualization.
- Proof: CI bundle-budget check; smooth-scroll/step benchmark in UI tests.

---

## ADR-017 — Verification clients: independent go-redis for differential; own `internal/client` for bridge/replica
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: We need a client for (a) differential/verify tests, (b) the Observatory bridge, (c) the replica apply loop. Using our own client for verification is circular.
- Options:
  - A. go-redis everywhere.
  - B. Own client everywhere.
  - C. Independent go-redis for verify/diff; own minimal client for bridge + replica apply.
- Decision: C.
- Why chosen:
  - Differential testing is only meaningful with an independent implementation — otherwise a shared bug produces false confidence.
  - go-redis in CI also proves "our server works with a widely-used real-world client" (bidirectional compat evidence).
  - Own client for the bridge = dogfooding: our protocol code is exercised by a real internal consumer daily.
  - Replica apply loop needs raw connection control (offsets, backlog, `REPLCONF`); own client keeps it dependency-light and fully instrumentable.
- Why rejected:
  - A: bridge would be fine, but replica needs raw access anyway; we'd lose dogfooding.
  - B: circular verification — disqualifying.
- Trade-offs accepted: two clients to maintain; own client kept deliberately minimal (dial, encode, parse, deadlines — no pool/cluster/pubsub).
- Revisit if: own client exceeds ~500 LOC → stop, reconsider scope.
- Interview Q&A:
  - "How do you test a server without testing yourself?" → independent client principle; "which client does your UI use?" → mine, deliberately, for dogfooding.
- Proof: diff harness uses go-redis (make diff-test); bridge uses internal/client; both exercised in CI.

---

## ADR-018 — CI-verified claims file (no hand-typed numbers in the showcase)
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: The showcase displays metrics (commands, test counts, throughput %, crash-test passes). Hand-typed numbers rot and are unverifiable — and interviewers are trained to verify claims.
- Options:
  - A. Hardcode numbers in the UI.
  - B. `make verify` / CI generates `claims.json` {value, source command, timestamp, commit hash, variance}; UI renders only from it.
- Decision: B.
- Why chosen:
  - Trust mechanism: every on-screen number carries provenance; unverified claims don't render (UI shows "not yet verified").
  - Turns "trust me" into "reproduce it" — the exact move for interviews.
  - Forces the verify pipeline to always run before demos (documented flow: `make verify && make showcase`).
- Why rejected:
  - A: rot, unverifiable, misses the narrative opportunity.
- Trade-offs accepted: claims pipeline plumbing (bridge `/api/claims`, claims generation in Makefile); demo requires fresh verify run for full data.
- Revisit if: never — signature trust feature.
- Interview Q&A:
  - "Are these numbers real?" → provenance: command, timestamp, commit, variance — show it live.
- Proof: claims.json schema; UI snapshot tests render "unverified" state; CI verifies claims freshness.

---

## ADR-019 — Observatory non-goals
- Date: 2026-10-18 · Phase: U0 · Status: Accepted
- Context: Scope discipline for ~7–8 weeks of UI work, mirroring ADR-012's approach.
- Options: full product ambitions vs explicit cuts.
- Decision — explicit non-goals (each becomes a clean interview answer):
  - No authentication / multi-tenant: localhost demo; destructive lab endpoints gated by `DEMO_MODE=1`; documented in README.
  - Not a general-purpose Redis GUI: no arbitrary type editing in v1, no cluster views — RedisInsight exists and is better at that; ours visualizes *this implementation's internals*.
  - Not mobile-first: responsive collapse only; interviewer/recruiter demos happen on laptops.
  - No AI copilot / chat: no differentiation, real cost.
  - No WebSocket/binary channels (ADR-015), no user accounts, no telemetry/analytics.
  - No live multi-user collaboration (shared cursors/sessions).
- Why: keeps UI at ~36–40 sessions; every cut is deliberate and documented.
- Revisit if: a specific feature maps to an interview probe that nothing else covers.
- Interview Q&A:
  - "Why doesn't the UI have X?" → deliberate scope decision with reason; boundary-of-knowledge answer.

---

## ADR-020 — Tooling & licensing policy: OSS or free-to-use only, auditable by default
- Date: 2026-10-18 · Phase: P0 · Status: Accepted
- Context: During P0.1/P0.2 scaffolding we installed a container runtime (initially OrbStack — proprietary, free for personal use only, paid for commercial use) and host redis-cli. Owner constraint: every tool in the stack must be open source or free for unrestricted use, and safe/secure. A proprietary privileged VM/helper is also unauditable.
- Options:
  - A. Best-of-breed regardless of license (Docker Desktop / OrbStack) — better UX, license exposure and unauditable privileged components.
  - B. Policy: all tooling must be OSS or free-for-unrestricted-use; container runtime = colima (MIT) + lima (Apache-2.0) + docker CLI/compose (Apache-2.0). One documented exception: Redis 7.4.11 test reference (RSALv2/SSPLv1 — source-available, free to use, never redistributed) because differential parity with real Redis is the core claim (ADR-017).
- Decision: B — migrated OrbStack → colima during P0.2; exception recorded for the Redis reference image only.
- Why chosen:
  - colima/lima/docker CLI are MIT/Apache-2.0, fully auditable, and expose the same `docker compose` interface — migration was config-only (no Makefile/docs changes).
  - Zero commercial-license exposure if the repo is demoed or used at an employer (Docker Desktop requires paid subscription for larger orgs; OrbStack paid for commercial use).
  - Closed-source privileged VM/helper removed from the host attack surface; OSS stack keeps it inspectable.
  - Supply-chain hygiene continues: images digest-pinned (`redis:7.4.11@sha256:4fa2…`), ports bound to 127.0.0.1 only.
- Why rejected:
  - A: license risk + unauditable privileged components; reviewers/demo viewers should never need paid software to reproduce claims.
- Trade-offs accepted: colima has no GUI/dashboard and needs explicit `colima start` (documented in runbook); slower first boot than OrbStack — irrelevant for the reference-Redis workload. Host redis-cli (AGPLv3, Homebrew) is free-to-use and never the reference server — the pinned container always is.
- Revisit if: colima blocks a required workflow (e.g., host networking needs) → evaluate Podman (Apache-2.0) or log a new exception here.
- Interview Q&A:
  - "Why not Docker Desktop?" → licensing + auditability; colima is MIT, same CLI, and the swap was a config-only change — logged, with the one necessary exception (Redis reference) recorded.
  - "How do you keep the dependency surface safe?" → OSS-only policy, digest-pinned images, localhost binds, pinned versions everywhere.
- Proof: this ADR; `docker-compose.yml` (digest pin + 127.0.0.1 binding); P0.2 verification `redis-cli -p 6379 PING → PONG` on colima stack (redis 7.4.11); COMPATIBILITY.md license note arrives in P0.3.

---

## ADR-021 — INCRBYFLOAT matches the reference Redis long-double ABI per GOARCH
- Date: 2026-10-20 · amended 2026-10-21 · Phase: P2.4/P5.3 · Status: Accepted
- Context: Redis implements INCRBYFLOAT with C `long double` parsing (`strtold`), addition, and `%.17Lf` output with trailing-zero trimming. C `long double` is platform-dependent: 80-bit x87 extended (64-bit significand) on amd64/386, but IEEE binary128 (113-bit significand) on arm64 Linux. Go's native `float64` has a 53-bit mantissa and diverges within the 17 printed digits. CI runs the pinned Redis image on amd64; the original binary128-only implementation matched the aarch64 oracle but failed `TestDiffStrings/float_huge` on amd64.
- Options:
  - A. `float64` arithmetic and `strconv.FormatFloat`.
  - B. `math/big.Float` with 113-bit precision, binary128 only.
  - C. `math/big.Float` with precision, exponent range, and rounding selected from the reference's C `long double` ABI for the build architecture.
- Decision: C — 64-bit x87 precision/range on amd64/386, 113-bit binary128 on arm64. The reference is the pinned Linux Redis container, so the selection follows `GOARCH`, not the host OS; a macOS-arm64 development build still targets the aarch64 reference.
- Why chosen:
  - The differential oracle is the Linux container for the same architecture: x87 on amd64 CI, binary128 on aarch64. `make diff-test` therefore behaves like CI on both Apple Silicon and x86 hosts.
  - `big.Float` with the matching precision reproduces `strtold` rounding and the 4933-digit `1e4932` expansion byte-for-byte on both architectures (checked directly against `redis:7.4.11` amd64 and arm64).
  - The previous binary128-only decision broke CI as soon as the reference ran on amd64; selecting the ABI removes the architecture assumption instead of hiding the mismatch.
- Why rejected:
  - A: fails parity at the 17th decimal on values the reference prints exactly.
  - B: correct on aarch64 but wrong on the amd64 reference (the committed CI failure).
- Trade-offs accepted: INCRBYFLOAT output follows the build architecture, just as Redis does. A non-Linux host Redis (for example macOS, whose `long double` is double) can differ; the documented oracle is the Linux container. Range constants must stay in sync with the selected ABI.
- Revisit if: the reference image architecture set changes, or compatibility is claimed against a non-Linux Redis.
- Interview Q&A:
  - "Why not just use float64?" → Redis prints 17 decimals from an 80/128-bit C long double; float64 drifts, so the diff suite catches it. I match the reference ABI with `big.Float` and verify the huge expansion on each architecture.
  - "Why not always binary128?" → CI runs amd64 Redis with x87 extended precision; binary128 failed the huge-float differential there.
- Proof: `internal/server/string_commands.go` (`newLongDoubleFormat`, `parseLongDouble`, `formatLongDouble`); P2.4/P5.3 differential evidence in PROJECT_LOG; COMPATIBILITY precision row.


---

## ADR-022 — INFO identity, CONFIG allowlist, and connection stubs are honest compatibility surfaces
- Date: 2026-10-21 · Phase: P5.3 · Status: Accepted
- Context: redis-cli and go-redis call `INFO`, `HELLO`, `CLIENT`, and `CONFIG` during normal operation. `redis-cli --stat` in particular asserts on `CONFIG GET databases` and then polls INFO fields; without these surfaces the server is usable per command but not through standard tooling. Persistence, eviction, and per-client state do not exist yet, so a full Redis INFO/CONFIG/CLIENT implementation would report values the server cannot honor.
- Options:
  - A. Report only a minimal `redis_version`, skip INFO/CONFIG/CLIENT until later phases, and keep redis-cli interactive only.
  - B. Mirror Redis's full INFO fields and 404-parameter CONFIG matrix, with placeholder values for unimplemented subsystems.
  - C. Implement a truthful compatibility surface: pinned version identity, real sections with zeros for future subsystems, a documented CONFIG allowlist, a RESP2-only HELLO handshake, and CLIENT stubs that accept common redis-cli/go-redis calls without pretending to track per-client state.
- Decision: C.
  - `INFO` reports `redis_version:7.4.11`, `redis_mode:standalone`, role `master`, and Redis's section order. Memory comes from Go's runtime until P7.1; persistence/CPU/command/latency stats stay zero/empty until their phases; only `db0` can contain keys.
  - `CONFIG GET` covers nine parameters; `CONFIG SET` covers `hz`, `maxmemory`, `maxmemory-policy`, and `maxclients`. `save` reports Redis's default schedule but persistence does not land until P6. `maxmemory` and policy are stored but not enforced until P7.2/P7.3; `maxclients` takes effect immediately; the remaining five are GET-only.
  - `HELLO 2` returns the RESP2 array; every other protocol version returns `NOPROTO`. RESP3 remains out of scope.
  - CLIENT ID/GETNAME/SETNAME/SETINFO/INFO/LIST/GETREDIR/NO-EVICT/NO-TOUCH/UNPAUSE are a transport-safe stub. SETNAME/SETINFO validate, but no name or library metadata is stored; ID is 1; LIST/INFO are synthetic.
  - `CONFIG GET databases` reports 16 to satisfy redis-cli, while SELECT is not implemented; this is documented rather than silently exposed.
- Why chosen:
  - `redis-cli --stat` now runs clean, and go-redis's identity handshake succeeds, without claiming persistence, eviction, or per-client tracking that does not exist.
  - A fixed allowlist makes every mutable value reviewable and avoids a fake 404-parameter config tree.
  - Reporting 7.4.11 lets clients enable features that Antares actually implements, while zero/empty sections are an honest signal for the rest.
- Why rejected:
  - A: breaks standard tooling and the P5.3 exit criterion.
  - B: would require placeholders for subsystems and per-client state, turning INFO into a compatibility lie and multiplying test surface.
- Trade-offs accepted: INFO memory will change source at P7.1; CLIENT state is absent until the client table lands with output buffering/replication; maxmemory is visible before it is enforced; `databases=16` is a deliberate redis-cli accommodation, not a claim that 16 databases work.
- Revisit if: a client depends on CLIENT name round-trips, RESP3, or CONFIG parameters outside the allowlist; replace the stubs rather than widening the allowlist without an owner.
- Interview Q&A:
  - "Why not implement all of INFO/CONFIG?" → clients need the surface, not the lie; I exposed real sections and a small allowlist, and pushed enforcement to the phases that own it.
  - "How do you keep compatibility honest?" → the pinned version, zeroed future stats, documented divergences in COMPATIBILITY, and differential tests on the fields that are actually stable.
- Proof: `internal/server/info_commands.go`, `config_commands.go`, `connection_commands.go`, `internal/metrics/stats.go`; `tests/differential/info_diff_test.go`; clean live `redis-cli --stat` capture; COMPATIBILITY rows for INFO/CLIENT/CONFIG.


---

## ADR-023 — AOF propagation: executor-level mutation observation with effect rewrites
- Date: 2026-10-21 · Phase: P6.1–P6.6 · Status: Accepted
- Context: AOF needs to record exactly the commands that changed the keyspace, in execution order, without touching all ~45 handlers. Several commands cannot be replayed verbatim: SPOP picks a random member, relative EXPIRE/PEXPIRE would replay against a different wall clock, and INCRBYFLOAT's result depends on the reference long-double ABI. The AOF rewrite also needs one format that doubles as the future full-resync snapshot (ADR-007).
- Options:
  - A. Instrument every handler to emit its own AOF command.
  - B. Add a `Write` flag to the registry and log every flagged command verbatim.
  - C. Observe a store mutation counter at the executor's single choke point, replay deterministic commands verbatim, and rewrite nondeterministic ones to their final effect.
- Decision: C — the executor snapshots `Store.Mutations()` before dispatch; if it changed, it appends the command's effect, rewriting SET options/INCRBYFLOAT to a final `SET` (+`PEXPIREAT`), the EXPIRE family to `PEXPIREAT`, SPOP to `SREM` with the actually-removed members, and PERSIST to `PERSIST`; everything else is appended as received.
- Why chosen:
  - No-op writes (`SET … NX` that loses, `DEL` of a missing key) do not move the counter, so they never reach the log — the log is effect-based by construction.
  - One hook in the already-serialized executor (ADR-002) means append order equals execution order and handlers stay unaware of persistence.
  - `PEXPIREAT` plus a between-command `SET`+`PEXPIREAT` makes replay independent of when it runs, so relative expiries survive restart.
  - SPOP's removed members come from the before/after set difference, so replay `SREM` deletes exactly what the client observed.
  - Rewrite order is `BeginRewrite` → snapshot copy → serialize temp → append buffered tail → fsync → rename, so no write can fall between snapshot and buffer.
- Why rejected:
  - A: repeats persistence logic across every mutator; easy to forget one and silently lose durability.
  - B: a write flag is static metadata; it cannot tell a no-op write from a real one, and it still logs random/time-relative commands verbatim.
- Trade-offs accepted: the counter moves on some harmless overwrites (for example HSET writing the same field value), so a few redundant frames are logged; SPOP effect is derived by value rather than by index; reads are never logged. Automatic rewrite uses a fixed 64 MiB / 2× threshold instead of Redis's configurable `auto-aof-rewrite-*` knobs.
- Revisit if: replication needs the raw command stream rather than effects, or per-command `Write` metadata is needed for ACL/keyspace notifications; a command that mutates outside `Store` would also need its own effect mapping.
- Interview Q&A:
  - "How do you know what to append to the AOF?" → the executor notices that the store's mutation counter moved after dispatch; if it moved, it appends the effect. No-op writes and reads never move it, so they never log.
  - "Why rewrite SPOP/EXPIRE instead of logging them?" → replay must be deterministic: SPOP would pop a different random member and relative EXPIRE would race the restart clock, so I log the concrete members removed and an absolute PEXPIREAT.
  - "How do you avoid losing writes during a rewrite?" → BeginRewrite first, then copy; writes after the copy land in the buffer and are appended to the new file before the rename.
- Proof: `internal/server/persistence.go` (`propagate`, `aofEffect`, `startRewrite`), `internal/persist/writer.go` (`BeginRewrite`/`FinishRewrite`), `internal/server/persistence_test.go` (propagation effects, replay state equality, rewrite state equality), `internal/persist/writer_test.go` (`TestWriterRewriteSwap`).


---

## ADR-024 — AOF failure policy: stop appending, refuse writes, bound the rewrite tail
- Date: 2026-10-21 · Phase: P6 (review fix) · Status: Accepted
- Context: A durability log must fail safe. If a write to the AOF fails partway (for example the disk fills), continuing to append leaves a torn frame in the middle of the file; the next recovery would truncate there and silently drop every later record. Recovery also could not tell a torn tail from mid-file corruption. And the rewrite tail was buffered in memory without a cap.
- Options:
  - A. Keep appending and log the error (the pre-review behavior).
  - B. Stop appending on the first write failure, clamp the file back to the last complete frame, refuse further writes until a rewrite or restart, fsync the directory after the swap rename, and abort a rewrite whose buffered tail exceeds a cap.
- Decision: B.
  - `Writer` keeps a sticky failure. A failed `Write` truncates the file back to the last good size, then every later `Append` returns that error without touching the file.
  - The executor refuses write commands with `-MISCONF Errors writing to the AOF file: …` while the writer is failed or closed; reads still work.
  - `finishRewrite` fsyncs the parent directory after the rename, so the swap survives power loss.
  - A rewrite whose buffered tail exceeds `RewriteBufferMax` (64 MiB) aborts instead of growing without bound; the old file still holds every write, so nothing is lost.
- Why chosen:
  - Bounds loss to the single unacknowledged batch and keeps a torn frame out of the middle of the log, so the torn-tail rule stays true.
  - MISCONF is how Redis signals a broken AOF; clients that check errors see it, and INFO `aof_last_write_status` is `err`.
  - The directory fsync is what makes the atomic rename actually durable across a power loss.
- Why rejected:
  - A: a single failed write can delete every later acknowledged record on restart, and hides the failure from clients.
- Trade-offs accepted: a failed writer stays failed until `BGREWRITEAOF` succeeds (which resets the error) or the process restarts; reads continue but writes are rejected, so the operator must clear the disk and rewrite or restart.
- Revisit if: the disk error is transient and self-heals, in which case a retry/backoff that clears the error on a successful write could be added.
- Interview Q&A:
  - "What happens if the disk fills mid-write?" → the writer clamps the file to the last complete frame, refuses further appends, and the executor answers writes with MISCONF; reads keep working.
  - "How do you tell a torn tail from corruption?" → an incomplete final frame is a torn write and is truncated; a bad length or checksum is corruption and refuses startup unless `-aof-repair` discards the tail.
- Proof: `internal/persist/writer.go` (`failWrite`, `Failed`, `syncDir`), `internal/server/executor.go` (`aofWriteBlocked`); `TestWriterRefusesAfterWriteFailure`, `TestWriterRefusesAfterSyncFailure`, `TestRecoverCorruptFrameRefused`, `TestAOFWriteBlockedAfterFailure`.

## ADR-025 — Memory accounting: a logical keyspace model feeds INFO and maxmemory
- Date: 2026-10-21 · Phase: P7.1 · Status: Accepted
- Context: `INFO memory` reported `used_memory` from Go's `runtime.MemStats.Alloc`. That number is process-wide and moves with GC and unrelated allocations, so it is not a dataset size. The next subphase enforces `maxmemory` against it, which needs a stable figure that reflects the keyspace only.
- Options:
  - A. Keep reading Go runtime counters.
  - B. Maintain an incremental per-key byte counter updated on every mutation.
  - C. Scan the keyspace on demand and model the retained heap from the shapes we allocate.
- Decision: C. `store.MemoryUsage()` sums a per-entry map cost plus exact payload lengths, collection slice backing, and the hash/set index maps, and returns `Usage{Total, Dataset}`. `INFO` reports `used_memory`/`used_memory_overhead`/`used_memory_dataset` from it; `used_memory_rss` and `allocator_*` stay Go runtime values.
- Why chosen:
  - A is the wrong concept for `maxmemory`: it swings with the Go heap, so eviction would fire on unrelated allocations.
  - B is O(1) but unsafe here: collection handlers mutate a `Value`'s slices in place and re-store it, so an old-size/new-size delta can read the already-mutated backing (e.g. an `LSET` that changes element length) and miss the change. Scanning the current state is correct by construction.
  - The model lands within 10% of Go's retained heap for payload-sized datasets across all four value types (`TestMemoryUsageTracksRuntime`).
- Why rejected:
  - A: not a dataset size; it was only ever a placeholder until accounting landed (ADR-022 anticipated this).
  - B: in-place mutation footguns for a few bytes saved; would also need a touch on every expiry/flush path.
- Trade-offs accepted: `MemoryUsage` is O(n) over physical keys (a `shortcut:` note in `memory.go` marks where P7.2 must cache it); the estimate models Go map/slice overhead, not jemalloc size classes, so absolute bytes and the fragmentation ratio differ from real Redis (COMPATIBILITY).
- Revisit if: `maxmemory` must include non-keyspace overhead (clients, buffers, AOF), or the per-command scan becomes measurable.
- Interview Q&A:
  - "You have no C allocator — how do you know the keyspace's memory?" → a logical model: amortized per-entry map cost plus exact payload, slice backing, and index maps, calibrated to within 10% of the Go heap.
  - "Why not just read the Go heap?" → it is process-wide and GC-dependent; `maxmemory` needs a dataset number.
- Proof: `internal/store/memory.go` (`MemoryUsage`, `Usage`, `add`, `addIndex`), `internal/server/info_commands.go` (`memoryInfo`); `TestMemoryUsageTracksRuntime`, `TestMemoryUsageEmptyAndPerKey`, `TestInfoMemoryAccounting`.

## ADR-026 — maxmemory enforcement: a DenyOOM gate on the command path, no eviction yet
- Date: 2026-10-21 · Phase: P7.2 · Status: Accepted
- Context: `CONFIG SET maxmemory` stored a limit that nothing enforced, so an over-limit server kept growing. Redis refuses memory-growing commands with `-OOM ...` once the keyspace is over `maxmemory` and eviction cannot free anything (`noeviction`). Eviction itself is P7.3.
- Options:
  - A. Reuse `Command.Write` as the refuse flag.
  - B. Add a narrow `Command.DenyOOM` flag mirroring Redis's `CMD_DENYOOM`.
  - C. Maintain an incremental memory counter for the check.
- Decision: B, evaluated in `Executor.oomBlocked` after the AOF MISCONF gate and before dispatch: when `maxmemory > 0`, the named command exists, is `DenyOOM`, and its arity is valid, `store.MemoryUsage().Total > maxmemory` answers `-OOM command not allowed when used memory > 'maxmemory'.` Reads, deletes, `CONFIG`, and non-denyoom writes still run. Until eviction lands (P7.3), every `maxmemory-policy` behaves like `noeviction`.
- Why chosen:
  - A would refuse `DEL`/`SREM`/`SPOP`, which do not grow memory and are the way out of an over-limit keyspace — Redis marks them non-denyoom for exactly that reason.
  - B matches Redis's own flag split (denyoom vs write) and confines the check to the 15 growing commands (`append`, the INCR family, `getset`, `hset`, `lpush`/`rpush`/`lset`, `mset`, `sadd`, `set`, `setnx`).
  - C is unsafe here for the same in-place-mutation reason as ADR-025's accounting; the scan is correct by construction and only runs while `maxmemory` is set (the default 0 skips it).
  - The gate runs after command resolution so unknown-command and arity errors keep Redis's precedence over `-OOM`.
- Why rejected:
  - A: blocks the recovery paths.
  - C: unsafe size deltas, per ADR-025.
- Trade-offs accepted: enforcement is an O(n) scan per denyoom command while `maxmemory > 0` (the `shortcut:` note in `memory.go` now points at P7.3); the accounting is Go-heap-shaped, not jemalloc, so the threshold trips at a different absolute byte count than Redis; with no eviction yet, `allkeys-lru`/`volatile-lru` reject rather than free.
- Revisit if: eviction lands (P7.3) — free from the eviction pool before refusing, and replace the scan with its own accounting.
- Interview Q&A:
  - "Why a separate denyoom flag instead of reusing your write flag?" → writes and memory-growing commands are different sets; `DEL` is a write but must stay allowed over the limit, so the gate keys on Redis's `CMD_DENYOOM`.
  - "What happens over maxmemory with no eviction?" → denyoom commands get `-OOM ...`; reads, deletes, and CONFIG keep working so the operator can recover.
- Proof: `internal/server/executor.go` (`oomBlocked`), `internal/server/registry.go` (`Command.DenyOOM`), `internal/protocol/errors.go` (`OutOfMemory`); `TestOOMGate`, `TestDiffOOM`.

## ADR-027 — Approximated LRU eviction: 24-bit access clock, sample 5, bounded pool
- Date: 2026-10-21 · Phase: P7.3 · Status: Accepted
- Context: P7.2 enforced `maxmemory` by rejecting denyoom writes under every policy, so `allkeys-lru`/`volatile-lru` behaved like `noeviction`. Redis instead frees the least-recently-used keys first, which needs a per-key recency signal and a victim choice.
- Options:
  - A. Exact LRU (a global recency structure over all keys).
  - B. Approximated LRU: a 24-bit seconds clock per key, sample 5, bounded eviction pool (the plan in ADR-006).
  - C. Keep rejecting (no eviction).
- Decision: B. `store.Value` gains a 24-bit `Lru` stamped on every lookup and write; `store.SampleKeys` returns up to n keys with `(now - lru) mod 2^24` idle seconds; `internal/evict.Pool` keeps the 16 most-idle candidates and `PopBest` removes the victim; `Executor.performEvictions` loops until the keyspace fits or nothing is evictable, emitting an `events.Event` per victim. Only `allkeys-lru` and `volatile-lru` evict; the other Redis policy names parse as `noeviction` and still reject.
- Why chosen:
  - A costs ~2 pointers plus list maintenance per key and diverges from Redis; the 5-sample approximation recovers most of the hit rate, and P7.4 proves retention.
  - The 24-bit seconds clock is the Redis trick: cheap, and idle time is taken modulo the mask so a wrap (~194 days) never reads as a huge age.
  - The pool improves victim quality beyond a single sample-of-5 without a global ordering.
- Why rejected:
  - A: overhead and divergence for a bounded benefit; B is the compatibility target.
  - C: leaves the LRU policies accepted but inert, which is exactly what P7.3 exists to fix.
- Trade-offs accepted: victim choice is non-deterministic, so tests assert quality bounds and a hot-key invariant, not exact victims; `volatile-lru` can thrash when the volatile set is small (as in Redis); eviction scans the keyspace and samples per victim (a `shortcut:` in `performEvictions`), acceptable until measured; the JSONL event sink is Observatory (U-phase) work, so P7.3 ships the versioned schema and the emit hook against an in-process sink.
- Revisit if: P7.4 retention is below 90% → raise the sample size (a parameter, one-line change) and re-measure.
- Interview Q&A:
  - "How do you pick a victim?" → stamp a 24-bit seconds clock on access, sample 5 keys, keep the 16 most idle in a bounded pool, evict the most idle, and repeat until the keyspace fits or nothing is evictable.
  - "Why not exact LRU?" → per-key list overhead and divergence from Redis; the sample approximates it and I measure retention.
  - "What if nothing is evictable?" → eviction fails and a denyoom command gets `-OOM` (noeviction, or `volatile-lru` with no volatile keys).
- Proof: `internal/store/store.go` (`Value.Lru`, `lruClock`, `SampleKeys`, the `live` touch), `internal/evict/evict.go` (`Policy`, `Pool`), `internal/events/events.go` (`Event`, `Sink`), `internal/server/executor.go` (`maxmemoryGate`, `performEvictions`); `TestSampleKeysIdleAndVolatile`, `TestLookupTouchesLRU`, `TestParsePolicy`, `TestPoolPopsMostIdle`, `TestPoolBoundedToMostIdle`, `TestEvictAllKeysLRU`, `TestEvictVolatileLRUOnly`. Meeting the ADR-006 retention target, `TestEvictionRetainsHotKeys` (P7.4) measured hot-set retention at 0.98–1.00 across 1000 rounds of cold churn (gate >0.90), and `INFO stats` now reports live `evicted_keys`.

## ADR-028 — Transactions: per-connection queue state, one executor turn and one AOF frame per EXEC
- Date: 2026-10-22 · Phase: P8 · Status: Accepted
- Context: Redis executes a transaction as a unit only because its single-threaded event loop means an EXEC's queued commands cannot interleave with another client's commands. Antares has the same single-key-owner property (ADR-002), but the executor's `Handler` was stateless per request, and AOF effects were appended one command at a time with a `Mutations` counter diff observed before and after each dispatch.
- Options:
  - A. Global transaction registry keyed by client ID in the executor.
  - B. Per-connection `ConnState` created by the network goroutine and passed through the `Handler`; the executor still owns all command execution.
  - C. Execute queued commands directly on the store without going through the normal dispatch path.
- Decision: B. `ConnState` holds `txState{active, dirty, queued, watched}`; `Handler` becomes `func(Request, *Encoder, *ConnState)`; `Handle` substitutes a fresh state for nil (tests). While active, `execute` validates arity/unknown command and appends to the queue with `+QUEUED`; an invalid queued command sets `dirty` so EXEC returns `-EXECABORT Transaction discarded because of previous errors.` At EXEC, a dirty queue aborts, a mismatching WATCH snapshot returns a null array, otherwise the queue is dispatched in order into the caller's encoder (one `ArrayHeader`), each command's AOF effects are collected and appended together as one frame, and the state resets. `DISCARD` and a completed/aborted EXEC both clear the watched set.
- Why chosen:
  - A duplicates connection identity in the executor and needs cleanup on disconnect; B keeps ownership in the goroutine that closes the socket, and the executor sees the same `ConnState` pointer only from that connection's serialized requests.
  - Reusing the normal handler dispatch for queued commands keeps validation, dispatch, expiry, metrics, and AOF effect logic in one place; C would fork every command path.
  - One frame per EXEC means a crash replays all of a transaction or none of it, matching "EXEC is atomic" at the persistence boundary too.
- Why rejected:
  - A: extra identity table and disconnect lifecycle for no execution benefit.
  - C: a second implementation of every command path, guaranteed to drift.
- Trade-offs accepted: `WATCH` compares values, not touches (ADR-008), so a write followed by restoring identical bytes does not abort; transaction state is memory-only and dies with the connection, as in Redis; the MULTI queue is unbounded, as in Redis, so a client can queue until memory runs out (a known limit, not a hidden one); the notification/TTL-change path is covered by comparing the absolute deadline, but no per-key dirty flags exist.
- Revisit if: pub/sub or keyspace notifications need per-key change hooks, or transaction queues need a memory cap.
- Interview Q&A:
  - "How is EXEC atomic if the executor is one goroutine?" → the queue is dispatched inside one executor turn; clients cannot interleave commands between the queued ones, and the AOF appends the effects as a single frame.
  - "Why per-connection state instead of a client ID table?" → the connection goroutine owns the lifetime, the executor owns execution, and no extra registry has to be cleaned up on disconnect.
  - "What makes a transaction dirty?" → a command that fails at queue time: unknown command or wrong arity (EXEC itself has a special abort wording); control errors like nested MULTI or WATCH-inside-MULTI do not dirty it, and runtime errors inside EXEC stay in the reply array.
- Proof: `internal/server/transactions.go`, `internal/store/watch.go`, `internal/server/executor.go` (`execute`, `run` gates dirty open transactions), `internal/server/persistence.go` (`collectEffects`/`appendEffects`); `TestTransactionBasics`, `TestTransactionQueueErrorsAbort`, `TestExecArityAbortsTransaction`, `TestTransactionDSTShadowModel`, `TestExecPersistsEffectsAsOneFrame`, `TestDiffTransactions` against Redis 7.4.11.

## ADR-029 — Differential testing: independent clients, narrow canonicalization, seeded generator
- Date: 2026-10-22 · Phase: P9.1 · Status: Accepted
- Context: The differential harness had grown from a string corpus (P2.6) into ten hand-written suites covering ~1,000 steps. Hand-picked cases reach the edges we thought of, but they do not explore command interactions at scale, and comparison rules lived beside whichever suite needed them (sorted replies in the set test, exact TTL elsewhere). P9.1 must make the "0 mismatches across all commands" claim systematic without turning canonicalization into a way to hide differences.
- Options:
  - A. Keep growing hand-written corpora only.
  - B. Generate commands and compare against a local shadow model of Redis semantics.
  - C. Keep the curated corpora, add a seeded generator that runs the same command stream against both servers, and centralize canonicalization in one audited file.
- Decision: C. `tests/differential/generator_test.go` drives a deterministic PCG stream (`DIFF_SEED`, default 1; `DIFF_ITERS`, default 500) across strings, lists, hashes, sets, TTL, and keyspace operations, mixing missing keys, shared keys, wrong-type use, binary/empty/long values, and overflow inputs. Every step applies to the reference first and then to Antares, and replies are compared exactly after the rules in `canonical.go`: default exact (values, order, errors); sort only replies Redis does not order (`SMEMBERS`, `KEYS`, `HKEYS`, `HVALS`); sort `HGETALL` field/value pairs by field; reduce a live `TTL`/`PTTL` duration to "positive" because the two calls are milliseconds apart while keeping `-1`/`-2` exact. The curated corpora stay for hand-picked edges, and `edge_diff_test.go` adds deterministic empty/NUL/1 KiB/non-ASCII key coverage. CI runs the same suite against the digest-pinned Redis 7.4.11 service container.
- Why chosen:
  - C needs no second implementation of Redis semantics: a shadow model would share assumptions with the server and could agree while both are wrong, the circularity ADR-017 rejects for clients.
  - A seed makes any failure reproducible and shrinking by hand practical; printing `DIFF_SEED`/`DIFF_ITERS` turns a red CI run into one command.
  - Canonicalization is the dangerous part of differential testing, so it is one small file with its own unit test rather than helpers spread across suites.
- Why rejected:
  - A: coverage stops at the author's imagination and does not exercise state-machine interactions.
  - B: a model bug produces false confidence (both sides wrong) or false failures, and maintaining it duplicates the server.
- Trade-offs accepted: the generator only covers deterministic semantics — `SPOP`/`SRANDMEMBER`/`HRANDFIELD`/`RANDOMKEY`, SCAN cursor batches, INFO counters, and `COMMAND` stay in property or curated tests; documented divergences are excluded from the stream (the first 30-seed sweep immediately found `LSET 9223372036854775807`, which Redis truncates to a C `int` and Antares deliberately rejects, so extreme list indices were removed from the generator and remain covered by unit tests); normalizing live TTL durations means duration bugs rely on the curated expiry corpus; the generator is not coverage-guided.
- Revisit if: a red seed proves hard to shrink manually (add a shrinker), or the generator needs to cover random-selection commands by comparing invariants instead of bytes.
- Interview Q&A:
  - "How do you know a generated mismatch is real?" → the seed reproduces the exact stream; canonicalization only sorts unordered replies and buckets live TTL, so values, errors, and order still fail the diff.
  - "Why not model Redis locally?" → a model is a second set of assumptions; diffing two running servers is the independent oracle.
  - "What did the generator catch?" → it surfaced the known C-`int` LSET divergence on the first sweep, which validated the documented boundary and forced the exclusion list to be explicit.
- Proof: `tests/differential/generator_test.go`, `canonical.go`, `canonical_test.go`, `edge_diff_test.go`, `surface_diff_test.go`; 30 seeds × 1,000 steps green against the pinned container; `make diff-test` green on the corpus + generator + edge + connection surface.
