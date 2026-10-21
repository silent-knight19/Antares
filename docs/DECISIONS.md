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
  - Per-frame CRC localizes corruption: recovery can trust the valid prefix and truncate at the first bad frame, instead of rejecting the whole file (option A's failure mode).
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
- Proof: `TestRecovery_TornTail`, `TestAOF_GroupCommit`, `make crash-test` (100× SIGKILL), BENCHMARKS.md fsync-policy table.

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
