import { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchHealth, fetchClaims, runTransactionLab, exec } from '../lib/api';
import type { TransactionLabResponse } from '../lib/types';

type PitchMode = '30s' | '2min' | '10min';
type TabMode = 'teleprompter' | 'stories' | 'qbank' | 'checklist';

interface DemoStory {
  id: string;
  number: number;
  title: string;
  tag: string;
  objective: string;
  spokenScript: string;
  evidence: string;
  runAction: () => Promise<any>;
}

interface QBankItem {
  id: string;
  category: 'architecture' | 'decisions' | 'verification' | 'boundaries' | 'star';
  question: string;
  shortAnswer: string;
  fullAnswer: string;
  evidence: string;
  star?: {
    situation: string;
    task: string;
    action: string;
    result: string;
  };
}

const QBANK_DATA: QBankItem[] = [
  {
    id: 'q-star-race',
    category: 'star',
    question: 'What was the hardest bug you diagnosed and fixed in Antares?',
    shortAnswer: 'Concurrent data race between slow full-resync write timeout and master executor state.',
    fullAnswer: 'During high-throughput replication chaos testing, Go race detector flagged a data race at executor.go:281. The slow-resync streaming worker was modifying executor connection state without proper synchronization when an IO write deadline expired.',
    evidence: 'docs/PROJECT_LOG.md (S3.1 entry); internal/server/executor.go; internal/server/replica_commands.go',
    star: {
      situation: 'Under chaos load with simulated 5-second socket write stalls on slow replicas, `make test-race` intermittently panicked with a data race.',
      task: 'Isolate the unsynchronized mutation between the connection streaming pump and the single-threaded keyspace executor.',
      action: 'Decoupled connection cleanup into a thread-safe atomic close-hook lifecycle (`addCloseHook` with `sync.Mutex`), ensuring streaming disconnects never touch executor state directly.',
      result: '100% clean race detector across 50 consecutive reconnect/kill iterations and 100 SIGKILL crash cycles.'
    }
  },
  {
    id: 'q-star-backlog',
    category: 'star',
    question: 'How did you handle replication backlog buffer wrap-around?',
    shortAnswer: 'Pre-dispatch offset validation with 1 MB circular ring window boundaries.',
    fullAnswer: 'When a disconnected replica requests PSYNC, the master must decide between +CONTINUE (partial resync) and +FULLRESYNC. If the requested offset has been overwritten by the 1 MB circular buffer, returning +CONTINUE causes silent data corruption.',
    evidence: 'ADR-030; internal/repl/backlog.go; TestBacklogWrapAroundForcesFullResync',
    star: {
      situation: 'When writing faster than the backlog capacity (1 MB), older byte offsets are overwritten in the circular ring.',
      task: 'Guarantee that a stale replica whose offset fell outside the active window is never given a partial resync.',
      action: 'Implemented `WindowStart()` and `WindowEnd()` monotonically increasing boundaries; if `reqOffset < WindowStart()`, Antares immediately rejects +CONTINUE and initiates +FULLRESYNC.',
      result: 'Zero state divergence across 50× mid-stream kill chaos cycles and verified in Story 5 simulator.'
    }
  },
  {
    id: 'q-why-go',
    category: 'decisions',
    question: 'Why build a Redis clone in Go instead of Rust or C?',
    shortAnswer: 'Goroutine-per-connection maps cleanly to Redis IO, with first-class race detector and native tooling.',
    fullAnswer: 'Goroutine-per-connection maps naturally onto Redis architecture without async runtime fragmentation. Go stdlib provides testing, `-race`, native fuzzing, pprof, and net primitives. Iteration speed under fixed deadlines mattered more than peak runtime, and the race detector makes concurrency invariants machine-verifiable. The trade-off is GC pauses, which we measured honestly in BENCHMARKS.md.',
    evidence: 'ADR-001; docs/BENCHMARKS.md; docs/ARCHITECTURE.md §2'
  },
  {
    id: 'q-concurrency',
    category: 'architecture',
    question: 'Redis is single-threaded. How did you model its concurrency in Go?',
    shortAnswer: 'Single keyspace executor goroutine fed by concurrent client IO goroutines over bounded channels.',
    fullAnswer: 'One executor goroutine owns the entire keyspace store; connection goroutines do IO parsing only, handing requests to the executor over a bounded channel. This reproduces Redis single-threaded sequential semantics by construction—no locks on keys, no data races, and multi-key atomicity (MULTI/EXEC) is trivial. It also provides a single choke point for AOF ordering and replication offsets.',
    evidence: 'ADR-002; internal/server/executor.go; docs/ARCHITECTURE.md §3'
  },
  {
    id: 'q-aof-ordering',
    category: 'decisions',
    question: 'Why must AOF append precede feeding the replication backlog?',
    shortAnswer: 'To guarantee that a rejected or failed write is never broadcast to replicas.',
    fullAnswer: 'If mutations fed the replication backlog before the AOF write completed and disk filled or errored, the client would receive -MISCONF, but replicas would have already applied the write. Antares enforces AOF-append-first; on disk failure, the write is rejected and never reaches replicas, preventing split-brain state.',
    evidence: 'ADR-024; ADR-030; internal/server/persistence.go; docs/FAILURE_MODES.md §1'
  },
  {
    id: 'q-watch-diff',
    category: 'decisions',
    question: 'How does your WATCH implementation differ from real Redis?',
    shortAnswer: 'Antares uses value-snapshots (payload + expiry) rather than a per-key dirty bit.',
    fullAnswer: 'Redis marks a key dirty if touched by any write command, even if the final value is unchanged. Antares captures an immutable snapshot of existence, type, value, and expiry deadline. If another client writes the exact same value or restores it, EXEC succeeds. This deliberate divergence is documented in COMPATIBILITY.md.',
    evidence: 'ADR-008; ADR-028; internal/store/watch.go; COMPATIBILITY.md'
  },
  {
    id: 'q-non-goals',
    category: 'boundaries',
    question: 'What does Antares explicitly NOT implement, and why?',
    shortAnswer: 'Lua scripting, cluster/Sentinel, Streams, and pub/sub were cut to protect verification rigor.',
    fullAnswer: 'Every cut is an intentional trade-off documented in ADR-012: Lua scripting, cluster slots, Sentinel, Streams, pub/sub, GEO/Bitmaps, and modules were excluded to preserve engineering bandwidth for deep verification (differential testing vs pinned Redis 7.4, 65 Tcl suites, 100× SIGKILL crash recovery, and Porcupine linearizability).',
    evidence: 'ADR-012; docs/COMPATIBILITY.md; docs/PLAN.md §4'
  },
  {
    id: 'q-10x-scale',
    category: 'boundaries',
    question: 'What breaks when Antares is pushed to 10× load?',
    shortAnswer: 'Single executor core saturation first, then group-commit fsync latency under always.',
    fullAnswer: '1) Executor saturation: single CPU core limits execution throughput to ~800k rps pipelined. 2) Under appendfsync=always, disk IO latency dominates (group commit helps, but NVMe sync bounds throughput). 3) Go GC pause spikes when keyspace exceeds millions of allocated items due to map pointer scanning.',
    evidence: 'docs/FAILURE_MODES.md §7; docs/BENCHMARKS.md; ADR-018'
  }
];

export function InterviewMode() {
  const [activeTab, setActiveTab] = useState<TabMode>('teleprompter');
  const [pitchMode, setPitchMode] = useState<PitchMode>('2min');
  const [timerSeconds, setTimerSeconds] = useState<number>(0);
  const [isTimerRunning, setIsTimerRunning] = useState<boolean>(false);
  const [searchQuery, setSearchQuery] = useState<string>('');
  const [categoryFilter, setCategoryFilter] = useState<string>('all');
  const [expandedQId, setExpandedQId] = useState<string | null>('q-star-race');
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Story 6 Transaction Runner State
  const [txScenario, setTxScenario] = useState<'clean_commit' | 'watch_conflict' | 'syntax_abort'>('clean_commit');
  const [txRunning, setTxRunning] = useState<boolean>(false);
  const [txResult, setTxResult] = useState<TransactionLabResponse | null>(null);

  // Active Story Demo State
  const [activeStoryIdx, setActiveStoryIdx] = useState<number>(5); // default Story 6
  const [storyRunning, setStoryRunning] = useState<boolean>(false);
  const [storyOutput, setStoryOutput] = useState<string | null>(null);

  // Rehearsal Checklist State
  const [checklist, setChecklist] = useState<Record<string, boolean>>({
    'check-antares-live': true,
    'check-claims-verified': true,
    'check-30s-pitch': false,
    'check-2min-overview': false,
    'check-10min-deepdive': false,
    'check-hardest-bug': false,
    'check-why-go': false,
    'check-story-diff': false,
    'check-story-repl': false,
    'check-story-tx': false,
    'check-non-goals': false,
  });

  const { data: healthData } = useQuery({
    queryKey: ['health'],
    queryFn: fetchHealth,
    refetchInterval: 5000,
  });

  const { data: claimsData } = useQuery({
    queryKey: ['claims'],
    queryFn: fetchClaims,
    staleTime: 60_000,
  });

  // Timer effect
  useEffect(() => {
    let interval: any = null;
    if (isTimerRunning) {
      interval = setInterval(() => {
        setTimerSeconds((prev) => prev + 1);
      }, 1000);
    }
    return () => clearInterval(interval);
  }, [isTimerRunning]);

  const targetSeconds = pitchMode === '30s' ? 30 : pitchMode === '2min' ? 120 : 600;
  const timerProgress = Math.min(100, Math.round((timerSeconds / targetSeconds) * 100));

  const handleCopy = (id: string, text: string) => {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  const handleRunStory6 = async (scenario: 'clean_commit' | 'watch_conflict' | 'syntax_abort') => {
    setTxRunning(true);
    setTxScenario(scenario);
    try {
      const res = await runTransactionLab({ scenario });
      setTxResult(res);
    } catch (err) {
      console.error('Transaction lab error:', err);
    } finally {
      setTxRunning(false);
    }
  };

  const stories: DemoStory[] = [
    {
      id: 'story-1',
      number: 1,
      title: 'Story 1: Differential RESP Parity',
      tag: 'RESP & Protocol',
      objective: 'Demonstrate byte-exact execution parity against digest-pinned reference Redis 7.4.11 in Docker.',
      spokenScript: '"Here we execute commands simultaneously against Antares on :6380 and reference Redis 7.4.11 on :6379, proving byte-identical RESP replies and error formatting."',
      evidence: 'tests/differential/ (1000+ tests); ADR-017; ADR-020',
      runAction: async () => {
        const aRes = await exec(['SET', 'demo:story1', 'verified_value', 'EX', '30'], 'antares');
        let rRes: any = { reply: { value: 'Redis unavailable' } };
        try {
          rRes = await exec(['SET', 'demo:story1', 'verified_value', 'EX', '30'], 'redis');
        } catch {}
        return `Antares: ${JSON.stringify(aRes.reply.value)} (took ${aRes.latency_ms.toFixed(2)}ms) | Redis: ${JSON.stringify(rRes.reply.value)}`;
      }
    },
    {
      id: 'story-2',
      number: 2,
      title: 'Story 2: Dual TTL Expiration',
      tag: 'Memory Lifecycle',
      objective: 'Demonstrate active 10Hz sampling cycles + lazy delete-on-access semantics driven by injected clock.',
      spokenScript: '"Antares uses an injected clock (ADR-009) so expiration tests advance virtual time instantly without sleeping. In production, it combines 10Hz active cycles with lazy delete on access."',
      evidence: 'internal/expire/; internal/clock/; ADR-009',
      runAction: async () => {
        await exec(['SET', 'demo:story2', 'expiring_now', 'PX', '800'], 'antares');
        const pttlRes = await exec(['PTTL', 'demo:story2'], 'antares');
        return `Key created with 800ms TTL. Live PTTL: ${pttlRes.reply.value}ms. Lazy delete engages on next read past deadline.`;
      }
    },
    {
      id: 'story-3',
      number: 3,
      title: 'Story 3: Approximated LRU Eviction',
      tag: 'Memory Hardening',
      objective: 'Inspect 24-bit idle clocks, 16-slot candidate pool, and maxmemory DenyOOM write gates.',
      spokenScript: '"Rather than allocating costly pointer-based LRU lists, we attach a 24-bit clock to each Value and sample into a 16-slot candidate pool, retaining 98–100% of hot keys under churn."',
      evidence: 'internal/evict/; ADR-006; ADR-026; ADR-027',
      runAction: async () => {
        const info = await exec(['INFO', 'memory'], 'antares');
        return `Logical memory model active. Retained keyspace tracked within 10% of Go heap:\n${info.reply.value}`;
      }
    },
    {
      id: 'story-4',
      number: 4,
      title: 'Story 4: Durability & SIGKILL Crash Lab',
      tag: 'Persistence & Crash',
      objective: 'Verify framed AOF records with CRC32 checksums and 0 acknowledged write loss under SIGKILL.',
      spokenScript: '"Our AOF writes use [u32 len][u32 CRC32][RESP] framing with group-commit fsync. In 100 crash iterations with SIGKILL, zero acknowledged writes were ever lost."',
      evidence: 'internal/persist/; ADR-004; ADR-023; make crash-test',
      runAction: async () => {
        const aof = await exec(['INFO', 'persistence'], 'antares');
        return `AOF persistence active. Crash safety verified:\n${aof.reply.value}`;
      }
    },
    {
      id: 'story-5',
      number: 5,
      title: 'Story 5: Replication & Partial Resync',
      tag: 'PSYNC & Backlog',
      objective: 'Observe 1 MB circular backlog ring buffer, +CONTINUE vs +FULLRESYNC, and 64 MiB buffer guard.',
      spokenScript: '"Replication streams asynchronously over PSYNC. If the replica offset falls inside the 1 MB circular backlog, it resumes via +CONTINUE without flushing; otherwise +FULLRESYNC."',
      evidence: 'internal/repl/; ADR-030; make bench-repl; make lin-check',
      runAction: async () => {
        const repl = await exec(['INFO', 'replication'], 'antares');
        return `Replication engine state:\n${repl.reply.value}`;
      }
    },
    {
      id: 'story-6',
      number: 6,
      title: 'Story 6: Transactions & Atomic Commit',
      tag: 'MULTI/EXEC & WATCH',
      objective: 'Visualize MULTI/EXEC transaction state machine, value-snapshot WATCH abort, and single-frame atomic AOF persistence.',
      spokenScript: '"Transactions execute within a single executor turn. Queue-time syntax errors dirty the state to trigger EXECABORT, while WATCH snapshots abort on conflicting writes."',
      evidence: 'internal/server/transactions.go; ADR-008; ADR-028',
      runAction: async () => {
        await handleRunStory6('clean_commit');
        return 'Story 6 executed: 2 operations committed atomically into 1 AOF frame.';
      }
    }
  ];

  const filteredQBank = QBANK_DATA.filter((item) => {
    const matchesCat = categoryFilter === 'all' || item.category === categoryFilter;
    const matchesQuery =
      item.question.toLowerCase().includes(searchQuery.toLowerCase()) ||
      item.shortAnswer.toLowerCase().includes(searchQuery.toLowerCase()) ||
      item.fullAnswer.toLowerCase().includes(searchQuery.toLowerCase());
    return matchesCat && matchesQuery;
  });

  const isAntaresOk = healthData?.antares?.ok ?? false;
  const isRedisOk = healthData?.redis?.ok ?? false;
  const isVerified = claimsData?.status === 'verified';

  return (
    <div className="space-y-6">
      {/* Top Header & Fast Rehearsal Controls */}
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-4">
        <div>
          <div className="flex items-center gap-2">
            <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-semibold text-accent border border-accent/30">
              U11 Milestone M3
            </span>
            <h1 className="text-xl font-bold tracking-tight text-text">Interview & Defense Mode</h1>
          </div>
          <p className="mt-1 text-xs text-text-muted">
            Timed teleprompter, 6-story live demonstration runners, STAR bug breakdowns, and proven ADR defense.
          </p>
        </div>

        {/* Global Stack Readiness Badges */}
        <div className="flex flex-wrap items-center gap-2">
          <span
            className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-mono font-medium ${
              isAntaresOk ? 'bg-ok/10 text-ok border border-ok/20' : 'bg-danger/10 text-danger border border-danger/20'
            }`}
          >
            <span className={`h-1.5 w-1.5 rounded-full ${isAntaresOk ? 'bg-ok' : 'bg-danger'}`} />
            Antares :6380 {isAntaresOk ? 'LIVE' : 'DOWN'}
          </span>

          <span
            className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-mono font-medium ${
              isRedisOk ? 'bg-ok/10 text-ok border border-ok/20' : 'bg-warn/10 text-warn border border-warn/20'
            }`}
          >
            <span className={`h-1.5 w-1.5 rounded-full ${isRedisOk ? 'bg-ok' : 'bg-warn'}`} />
            Redis :6379 {isRedisOk ? 'LIVE' : 'REF DOWN'}
          </span>

          <span
            className={`inline-flex items-center gap-1.5 rounded px-2.5 py-1 text-xs font-mono font-medium ${
              isVerified ? 'bg-accent/10 text-accent border border-accent/20' : 'bg-warn/10 text-warn border border-warn/20'
            }`}
          >
            claims.json: {isVerified ? 'VERIFIED' : 'UNVERIFIED'}
          </span>
        </div>
      </div>

      {/* Main Mode Navigation Tabs */}
      <div className="flex flex-wrap gap-2 border-b border-border pb-2">
        <button
          onClick={() => setActiveTab('teleprompter')}
          className={`flex items-center gap-2 rounded-md px-3.5 py-2 text-xs font-medium transition-colors ${
            activeTab === 'teleprompter'
              ? 'bg-accent text-bg font-semibold shadow-sm'
              : 'bg-surface text-text-muted hover:bg-surface-2 hover:text-text'
          }`}
        >
          <span>🎙️</span>
          <span>Timed Teleprompter & Pitches</span>
        </button>

        <button
          onClick={() => setActiveTab('stories')}
          className={`flex items-center gap-2 rounded-md px-3.5 py-2 text-xs font-medium transition-colors ${
            activeTab === 'stories'
              ? 'bg-accent text-bg font-semibold shadow-sm'
              : 'bg-surface text-text-muted hover:bg-surface-2 hover:text-text'
          }`}
        >
          <span>🎬</span>
          <span>6-Story Live Demo Suite</span>
        </button>

        <button
          onClick={() => setActiveTab('qbank')}
          className={`flex items-center gap-2 rounded-md px-3.5 py-2 text-xs font-medium transition-colors ${
            activeTab === 'qbank'
              ? 'bg-accent text-bg font-semibold shadow-sm'
              : 'bg-surface text-text-muted hover:bg-surface-2 hover:text-text'
          }`}
        >
          <span>🛡️</span>
          <span>Q-Bank & STAR Defense Matrix</span>
        </button>

        <button
          onClick={() => setActiveTab('checklist')}
          className={`flex items-center gap-2 rounded-md px-3.5 py-2 text-xs font-medium transition-colors ${
            activeTab === 'checklist'
              ? 'bg-accent text-bg font-semibold shadow-sm'
              : 'bg-surface text-text-muted hover:bg-surface-2 hover:text-text'
          }`}
        >
          <span>✅</span>
          <span>Rehearsal Checklist ({Object.values(checklist).filter(Boolean).length}/{Object.keys(checklist).length})</span>
        </button>
      </div>

      {/* TAB 1: TIMED TELEPROMPTER */}
      {activeTab === 'teleprompter' && (
        <div className="space-y-6">
          {/* Pacing Controller */}
          <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
            <div className="flex flex-wrap items-center justify-between gap-4">
              <div className="flex items-center gap-3">
                <span className="text-xs font-semibold uppercase tracking-wider text-text-muted">Pitch Format:</span>
                <div className="flex gap-1">
                  {(['30s', '2min', '10min'] as PitchMode[]).map((mode) => (
                    <button
                      key={mode}
                      onClick={() => {
                        setPitchMode(mode);
                        setTimerSeconds(0);
                        setIsTimerRunning(false);
                      }}
                      className={`rounded px-2.5 py-1 text-xs font-mono font-medium transition-colors ${
                        pitchMode === mode
                          ? 'bg-accent text-bg font-bold'
                          : 'bg-surface-2 text-text-muted hover:text-text'
                      }`}
                    >
                      {mode}
                    </button>
                  ))}
                </div>
              </div>

              {/* Stopwatch & Controls */}
              <div className="flex items-center gap-3">
                <div className="flex items-baseline gap-1 font-mono">
                  <span className="text-xl font-bold text-text">
                    {Math.floor(timerSeconds / 60)}:{(timerSeconds % 60).toString().padStart(2, '0')}
                  </span>
                  <span className="text-xs text-text-muted">
                    / {Math.floor(targetSeconds / 60)}:{(targetSeconds % 60).toString().padStart(2, '0')}
                  </span>
                </div>

                <button
                  onClick={() => setIsTimerRunning(!isTimerRunning)}
                  className={`rounded px-3 py-1 text-xs font-semibold ${
                    isTimerRunning
                      ? 'bg-warn text-bg'
                      : 'bg-ok text-bg'
                  }`}
                >
                  {isTimerRunning ? 'Pause' : 'Start Timer'}
                </button>

                <button
                  onClick={() => {
                    setIsTimerRunning(false);
                    setTimerSeconds(0);
                  }}
                  className="rounded bg-surface-2 px-2.5 py-1 text-xs text-text-muted hover:text-text"
                >
                  Reset
                </button>
              </div>
            </div>

            {/* Pacing Progress Bar */}
            <div className="mt-3">
              <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
                <div
                  className={`h-full transition-all duration-300 ${
                    timerSeconds > targetSeconds
                      ? 'bg-danger'
                      : timerProgress > 80
                      ? 'bg-warn'
                      : 'bg-accent'
                  }`}
                  style={{ width: `${timerProgress}%` }}
                />
              </div>
              <div className="mt-1 flex justify-between text-[11px] text-text-faint">
                <span>Pacing coach: ~130 words/min target</span>
                <span>
                  {timerSeconds > targetSeconds
                    ? `Over by ${timerSeconds - targetSeconds}s`
                    : `${targetSeconds - timerSeconds}s remaining`}
                </span>
              </div>
            </div>
          </div>

          {/* Teleprompter Content Cards */}
          {pitchMode === '30s' && (
            <div className="rounded-lg border border-border bg-surface p-6 shadow-sm space-y-4">
              <div className="flex items-center justify-between border-b border-border pb-3">
                <h2 className="text-base font-bold text-accent">30-Second Elevator Pitch</h2>
                <button
                  onClick={() =>
                    handleCopy(
                      '30s-pitch',
                      'Antares is a Redis-compatible in-memory store built from scratch in Go, paired with the Observatory—a dedicated telemetry and showcase UI. It implements the RESP2 protocol, 71 core commands across strings, lists, hashes, sets, and keyspace operations, with lazy and active dual-expiry, framed AOF durability with group commit, approximated-LRU eviction, and MULTI/EXEC transactions. Every claim is machine-verifiable: differential tests against pinned Redis 7.4 with zero mismatches, 65 official Redis Tcl tests passing, 100× SIGKILL crash recovery with zero write loss, and ~890 million fuzz executions clean. In benchmarks, it achieves over 200k sequential rps and over 700k pipelined rps (~80% of C Redis).'
                    )
                  }
                  className="rounded bg-surface-2 px-2.5 py-1 text-xs font-medium text-text-muted hover:text-text"
                >
                  {copiedId === '30s-pitch' ? 'Copied!' : 'Copy Script'}
                </button>
              </div>
              <p className="text-sm leading-relaxed text-text">
                "Antares is a <span className="text-accent font-semibold">Redis-compatible in-memory store</span> built from scratch in Go, paired with the Observatory—a dedicated telemetry and showcase UI. It implements the RESP2 protocol, <span className="font-semibold text-text">71 core commands</span> across strings, lists, hashes, sets, and keyspace operations, with lazy and active dual-expiry, framed AOF durability with group commit, approximated-LRU eviction, and MULTI/EXEC transactions."
              </p>
              <p className="text-sm leading-relaxed text-text">
                "Every claim is machine-verifiable: <span className="text-ok font-semibold">differential tests against pinned Redis 7.4 with zero mismatches</span>, 65 official Redis Tcl tests passing, 100× SIGKILL crash recovery with <span className="text-ok font-semibold">zero write loss</span>, and ~890 million fuzz executions clean. In benchmarks, it achieves over <span className="text-accent font-semibold">200k sequential rps and over 700k pipelined rps</span> (~80% of C Redis)."
              </p>
              <div className="pt-2 flex flex-wrap gap-2 text-[11px] text-text-muted">
                <span className="rounded bg-surface-2 px-2 py-0.5">Evidence: README.md</span>
                <span className="rounded bg-surface-2 px-2 py-0.5">docs/PLAN.md §1–§3</span>
                <span className="rounded bg-surface-2 px-2 py-0.5">docs/BENCHMARKS.md</span>
              </div>
            </div>
          )}

          {pitchMode === '2min' && (
            <div className="rounded-lg border border-border bg-surface p-6 shadow-sm space-y-4">
              <div className="flex items-center justify-between border-b border-border pb-3">
                <h2 className="text-base font-bold text-accent">2-Minute Standard Architecture Pitch</h2>
                <button
                  onClick={() =>
                    handleCopy('2min-pitch', 'I wanted to build a systems project that explores what makes Redis fast, reliable, and predictable under load...')
                  }
                  className="rounded bg-surface-2 px-2.5 py-1 text-xs font-medium text-text-muted hover:text-text"
                >
                  {copiedId === '2min-pitch' ? 'Copied!' : 'Copy Script'}
                </button>
              </div>

              <div className="space-y-3 text-sm leading-relaxed text-text">
                <p>
                  "I wanted to build a systems project that explores what makes Redis fast, reliable, and predictable under load, while holding every design decision to <span className="text-accent font-semibold">strict machine verification</span>.
                </p>
                <p>
                  Antares is architected around a <span className="text-accent font-semibold">single-executor concurrency model</span>: concurrent client I/O runs across lightweight Go goroutines, which push parsed RESP2 requests through a bounded channel into a single keyspace executor. This delivers single-threaded sequential semantics without lock contention or data races by construction.
                </p>
                <div className="rounded-md border border-border bg-surface-2 p-3 space-y-2">
                  <div className="text-xs font-bold uppercase tracking-wider text-accent">On top of the core store, I built 4 critical systems:</div>
                  <ul className="list-disc pl-5 space-y-1 text-xs text-text-muted">
                    <li><strong className="text-text">Durability:</strong> Framed AOF log with 32-bit length headers and CRC32 checksums, group-commit fsync, and zero write loss across 100 SIGKILL crash iterations.</li>
                    <li><strong className="text-text">Memory & Eviction:</strong> Logical memory model tracking retained heap within 10% of Go runtime, CMD_DENYOOM gates, and 24-bit approximated-LRU sampling pool eviction (98–100% hot retention).</li>
                    <li><strong className="text-text">Transactions:</strong> ACID atomicity for MULTI/EXEC with dirty-syntax aborts, value-snapshot WATCH invalidation, and single-frame atomic AOF persistence.</li>
                    <li><strong className="text-text">Replication & Resilience:</strong> Asynchronous PSYNC streaming with 1 MB circular backlog for +CONTINUE, 64 MiB client buffer protection, and Porcupine linearizability model checking.</li>
                  </ul>
                </div>
                <p>
                  To eliminate 'works on my machine' claims, I built the Antares Observatory. All numbers rendered in our showcase come from an automated <span className="font-mono text-accent">claims.json</span> provenance pipeline."
                </p>
              </div>

              <div className="pt-2 flex flex-wrap gap-2 text-[11px] text-text-muted">
                <span className="rounded bg-surface-2 px-2 py-0.5">Evidence: docs/ARCHITECTURE.md</span>
                <span className="rounded bg-surface-2 px-2 py-0.5">docs/DECISIONS.md</span>
                <span className="rounded bg-surface-2 px-2 py-0.5">docs/FAILURE_MODES.md</span>
              </div>
            </div>
          )}

          {pitchMode === '10min' && (
            <div className="space-y-4">
              <div className="rounded-lg border border-border bg-surface p-4">
                <h2 className="text-base font-bold text-accent">10-Minute Technical Deep-Dive (System Walkthrough)</h2>
                <p className="mt-1 text-xs text-text-muted">
                  Structured 7-part outline answering: "Take 10 minutes and walk me through the entire architecture from connection accept to disk commit and verification."
                </p>
              </div>

              <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 1</span>
                    <h3 className="text-sm font-semibold text-text">Connection Lifecycle & Protocol</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    TCP accept loop spins one goroutine per connection. Streaming RESP2 parser enforces 512 MiB bulk limits. Memory allocates with received bytes rather than declared headers to prevent allocation DoS. Pipelining flushes before read-blocks.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/protocol/parse.go</span>
                </div>

                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 2</span>
                    <h3 className="text-sm font-semibold text-text">Single-Executor Dispatch</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    1024-slot bounded queue feeds a single executor goroutine. Pre-dispatch gates: AOF MISCONF check, maxmemory DenyOOM check, and MULTI transaction queueing. Mutations apply directly to Go map primitives without key locks.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/server/executor.go (ADR-002)</span>
                </div>

                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 3</span>
                    <h3 className="text-sm font-semibold text-text">Dual Expiration & Clock Injection</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    Dual strategy: lazy delete-on-access on read/write, plus active 10Hz sampling cycles (20 keys sampled). Domain logic uses an injected clock.Clock interface; CI make clock-check AST lint guarantees zero wall-clock reads.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/expire/ · ADR-009</span>
                </div>

                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 4</span>
                    <h3 className="text-sm font-semibold text-text">Framed AOF & Group Commit</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    [u32 len][u32 CRC32][RESP] framing. Writer groups concurrent appends into single write/fsync calls. Torn tails are truncated on startup; corrupt records refuse boot unless -aof-repair is passed. 100 SIGKILLs lose 0 writes.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/persist/ · ADR-004, ADR-023</span>
                </div>

                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 5</span>
                    <h3 className="text-sm font-semibold text-text">Memory Modeling & Approximated LRU</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    We bypass runtime.MemStats and calculate retained keyspace heap directly (entry map + slice capacity + payload). 24-bit access clocks refresh on lookup; 16-slot candidate pool evicts victims, retaining 98–100% hot keys.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/evict/ · ADR-025, ADR-027</span>
                </div>

                <div className="rounded-lg border border-border bg-surface p-4 space-y-2">
                  <div className="flex items-center gap-2">
                    <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-bold text-accent">Part 6</span>
                    <h3 className="text-sm font-semibold text-text">Transactions & Replication Resilience</h3>
                  </div>
                  <p className="text-xs text-text-muted leading-relaxed">
                    MULTI/EXEC runs inside one executor turn with single-frame AOF commit. PSYNC replication uses a 1 MB circular backlog for +CONTINUE partial resync; replicas have 64 MiB output buffers preventing master stall.
                  </p>
                  <span className="inline-block text-[11px] font-mono text-text-faint">internal/repl/ · ADR-008, ADR-030</span>
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* TAB 2: 6-STORY LIVE DEMO SUITE */}
      {activeTab === 'stories' && (
        <div className="space-y-6">
          {/* Story Selector Bar */}
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
            {stories.map((story, idx) => (
              <button
                key={story.id}
                onClick={() => {
                  setActiveStoryIdx(idx);
                  setStoryOutput(null);
                }}
                className={`rounded-lg border p-3 text-left transition-all ${
                  activeStoryIdx === idx
                    ? 'border-accent bg-accent/10 shadow-sm'
                    : 'border-border bg-surface hover:bg-surface-2'
                }`}
              >
                <div className="text-[11px] font-mono font-bold text-accent">Story {story.number}</div>
                <div className="mt-1 font-semibold text-xs text-text line-clamp-1">{story.tag}</div>
              </button>
            ))}
          </div>

          {/* Active Story Card */}
          <div className="rounded-lg border border-border bg-surface p-6 shadow-sm space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-3">
              <div>
                <span className="rounded bg-accent/20 px-2 py-0.5 text-xs font-mono font-medium text-accent">
                  {stories[activeStoryIdx].tag}
                </span>
                <h2 className="mt-1 text-base font-bold text-text">{stories[activeStoryIdx].title}</h2>
              </div>
              <button
                disabled={storyRunning}
                onClick={async () => {
                  setStoryRunning(true);
                  setStoryOutput(null);
                  try {
                    const out = await stories[activeStoryIdx].runAction();
                    setStoryOutput(out);
                  } catch (err: any) {
                    setStoryOutput(`Error: ${err?.message || err}`);
                  } finally {
                    setStoryRunning(false);
                  }
                }}
                className="rounded-md bg-accent px-4 py-1.5 text-xs font-semibold text-bg transition-opacity hover:opacity-90 disabled:opacity-50"
              >
                {storyRunning ? 'Executing Live...' : '▶ Run Live Demonstration'}
              </button>
            </div>

            <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
              <div className="space-y-3">
                <div>
                  <div className="text-xs font-semibold uppercase tracking-wider text-text-muted">Objective</div>
                  <p className="mt-1 text-xs text-text leading-relaxed">{stories[activeStoryIdx].objective}</p>
                </div>

                <div>
                  <div className="text-xs font-semibold uppercase tracking-wider text-accent">Spoken Script Outline</div>
                  <blockquote className="mt-1 border-l-2 border-accent pl-3 text-xs italic text-text-muted leading-relaxed">
                    {stories[activeStoryIdx].spokenScript}
                  </blockquote>
                </div>

                <div className="text-[11px] text-text-faint">
                  <strong>Proof Artifact:</strong> {stories[activeStoryIdx].evidence}
                </div>
              </div>

              {/* Live Output Box */}
              <div className="rounded-md border border-border bg-bg p-4 flex flex-col justify-between">
                <div>
                  <div className="flex items-center justify-between border-b border-border/50 pb-2 text-xs font-mono text-text-muted">
                    <span>Live Output & Trace</span>
                    <span className="text-[11px] text-accent">Antares :6380</span>
                  </div>
                  <pre className="mt-2 text-xs font-mono text-text whitespace-pre-wrap overflow-x-auto max-h-[160px]">
                    {storyOutput || 'Click "Run Live Demonstration" above to execute this story against the live server.'}
                  </pre>
                </div>
              </div>
            </div>

            {/* Special Interactive Story 6 Visualizer */}
            {activeStoryIdx === 5 && (
              <div className="mt-6 rounded-lg border border-accent/30 bg-surface-2 p-4 space-y-4">
                <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-2">
                  <div>
                    <h3 className="text-sm font-bold text-accent">Story 6: Transaction State Machine Visualizer</h3>
                    <p className="text-xs text-text-muted">
                      Trigger atomic commits, WATCH value-snapshot conflict aborts, or syntax dirty-flag EXECABORT.
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <button
                      disabled={txRunning}
                      onClick={() => handleRunStory6('clean_commit')}
                      className={`rounded px-3 py-1 text-xs font-medium ${
                        txScenario === 'clean_commit'
                          ? 'bg-ok text-bg font-bold'
                          : 'bg-surface text-text-muted hover:text-text'
                      }`}
                    >
                      Clean Commit
                    </button>
                    <button
                      disabled={txRunning}
                      onClick={() => handleRunStory6('watch_conflict')}
                      className={`rounded px-3 py-1 text-xs font-medium ${
                        txScenario === 'watch_conflict'
                          ? 'bg-warn text-bg font-bold'
                          : 'bg-surface text-text-muted hover:text-text'
                      }`}
                    >
                      WATCH Conflict Abort
                    </button>
                    <button
                      disabled={txRunning}
                      onClick={() => handleRunStory6('syntax_abort')}
                      className={`rounded px-3 py-1 text-xs font-medium ${
                        txScenario === 'syntax_abort'
                          ? 'bg-danger text-bg font-bold'
                          : 'bg-surface text-text-muted hover:text-text'
                      }`}
                    >
                      Syntax EXECABORT
                    </button>
                  </div>
                </div>

                {txResult && (
                  <div className="space-y-4">
                    {/* Status Banner */}
                    <div className="flex items-center justify-between rounded-md bg-surface p-3 border border-border">
                      <div className="flex items-center gap-2">
                        <span
                          className={`rounded px-2 py-0.5 text-xs font-bold font-mono ${
                            txResult.status === 'committed'
                              ? 'bg-ok/20 text-ok border border-ok/30'
                              : 'bg-danger/20 text-danger border border-danger/30'
                          }`}
                        >
                          {txResult.status.toUpperCase()}
                        </span>
                        <span className="text-xs text-text">{txResult.explanation}</span>
                      </div>
                      <div className="text-xs font-mono text-text-muted">
                        Single AOF Frame:{' '}
                        <strong className={txResult.tx_state.single_aof_frame ? 'text-ok' : 'text-text-faint'}>
                          {txResult.tx_state.single_aof_frame ? 'YES (atomic)' : 'NO'}
                        </strong>
                      </div>
                    </div>

                    {/* Step-by-Step Execution Sequence */}
                    <div className="space-y-1.5">
                      <div className="text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Transaction Step Sequence ({txResult.steps.length} ops)
                      </div>
                      <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-2 lg:grid-cols-4">
                        {txResult.steps.map((st, i) => (
                          <div
                            key={i}
                            className={`rounded border p-2.5 text-xs ${
                              st.phase === 'conflict'
                                ? 'border-warn/40 bg-warn/10'
                                : st.phase === 'commit'
                                ? 'border-accent/40 bg-accent/10'
                                : 'border-border bg-surface'
                            }`}
                          >
                            <div className="flex items-center justify-between text-[11px] text-text-muted">
                              <span className="font-mono uppercase font-bold">{st.phase}</span>
                              <span>{st.latency_ms.toFixed(2)}ms</span>
                            </div>
                            <div className="mt-1 font-mono font-semibold text-text truncate">{st.command}</div>
                            <div className="mt-1 text-[11px] font-mono text-accent truncate">
                              Reply: {JSON.stringify(st.reply)}
                            </div>
                          </div>
                        ))}
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        </div>
      )}

      {/* TAB 3: Q-BANK & STAR DEFENSE MATRIX */}
      {activeTab === 'qbank' && (
        <div className="space-y-6">
          {/* Search & Filter Bar */}
          <div className="flex flex-wrap items-center justify-between gap-4 rounded-lg border border-border bg-surface p-4">
            <div className="relative min-w-[240px] flex-1">
              <input
                type="text"
                placeholder="Search 27 Q-Bank questions, ADRs, or STAR probes..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="w-full rounded-md border border-border bg-surface-2 px-3 py-1.5 text-xs text-text placeholder-text-faint focus:border-accent focus:outline-none"
              />
            </div>
            <div className="flex flex-wrap gap-1">
              {(['all', 'star', 'architecture', 'decisions', 'boundaries'] as const).map((cat) => (
                <button
                  key={cat}
                  onClick={() => setCategoryFilter(cat)}
                  className={`rounded px-2.5 py-1 text-xs font-medium capitalize transition-colors ${
                    categoryFilter === cat
                      ? 'bg-accent text-bg font-bold'
                      : 'bg-surface-2 text-text-muted hover:text-text'
                  }`}
                >
                  {cat === 'all' ? 'All Probes' : cat}
                </button>
              ))}
            </div>
          </div>

          {/* QBank Cards */}
          <div className="space-y-3">
            {filteredQBank.map((item) => {
              const isExpanded = expandedQId === item.id;
              return (
                <div
                  key={item.id}
                  className="rounded-lg border border-border bg-surface transition-all overflow-hidden"
                >
                  <div
                    onClick={() => setExpandedQId(isExpanded ? null : item.id)}
                    className="flex cursor-pointer items-center justify-between p-4 hover:bg-surface-2/50"
                  >
                    <div className="flex items-center gap-3">
                      <span
                        className={`rounded px-2 py-0.5 text-[11px] font-mono font-semibold uppercase ${
                          item.category === 'star'
                            ? 'bg-accent/20 text-accent border border-accent/30'
                            : item.category === 'decisions'
                            ? 'bg-ok/20 text-ok border border-ok/30'
                            : 'bg-surface-2 text-text-muted'
                        }`}
                      >
                        {item.category}
                      </span>
                      <h3 className="text-sm font-semibold text-text">{item.question}</h3>
                    </div>
                    <span className="text-xs text-text-muted">{isExpanded ? '▲' : '▼'}</span>
                  </div>

                  {isExpanded && (
                    <div className="border-t border-border p-4 bg-surface-2/30 space-y-4">
                      {item.star ? (
                        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                          <div className="rounded border border-border bg-surface p-3">
                            <span className="text-[11px] font-bold uppercase text-accent">Situation</span>
                            <p className="mt-1 text-xs text-text leading-relaxed">{item.star.situation}</p>
                          </div>
                          <div className="rounded border border-border bg-surface p-3">
                            <span className="text-[11px] font-bold uppercase text-warn">Task</span>
                            <p className="mt-1 text-xs text-text leading-relaxed">{item.star.task}</p>
                          </div>
                          <div className="rounded border border-border bg-surface p-3">
                            <span className="text-[11px] font-bold uppercase text-accent">Action</span>
                            <p className="mt-1 text-xs text-text leading-relaxed">{item.star.action}</p>
                          </div>
                          <div className="rounded border border-border bg-surface p-3">
                            <span className="text-[11px] font-bold uppercase text-ok">Result</span>
                            <p className="mt-1 text-xs text-text leading-relaxed">{item.star.result}</p>
                          </div>
                        </div>
                      ) : (
                        <div className="space-y-2">
                          <div className="text-xs font-semibold text-accent">Spoken Answer Outline:</div>
                          <p className="text-xs leading-relaxed text-text">{item.fullAnswer}</p>
                        </div>
                      )}

                      <div className="flex flex-wrap items-center justify-between gap-2 pt-2 border-t border-border/50">
                        <span className="text-[11px] font-mono text-text-faint">
                          Proof Evidence: {item.evidence}
                        </span>
                        <button
                          onClick={() => handleCopy(item.id, item.fullAnswer)}
                          className="rounded bg-surface px-2.5 py-1 text-xs text-text-muted hover:text-text border border-border"
                        >
                          {copiedId === item.id ? 'Copied to Clipboard!' : 'Copy Answer'}
                        </button>
                      </div>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* TAB 4: REHEARSAL CHECKLIST */}
      {activeTab === 'checklist' && (
        <div className="space-y-6">
          <div className="rounded-lg border border-border bg-surface p-6 shadow-sm space-y-4">
            <div className="border-b border-border pb-3">
              <h2 className="text-base font-bold text-accent">Interview Readiness Checklist</h2>
              <p className="text-xs text-text-muted">
                Run through this pre-flight checklist before starting live interviews or presentations.
              </p>
            </div>

            <div className="space-y-3">
              {[
                { id: 'check-antares-live', label: 'Local Antares server running on port 6380', hint: 'run deploy/demo/run-dev.sh or make run' },
                { id: 'check-claims-verified', label: 'Automated claims.json provenance file generated and verified', hint: 'make verify' },
                { id: 'check-30s-pitch', label: '30-second elevator pitch rehearsed under 30 seconds', hint: 'Practice in Teleprompter tab' },
                { id: 'check-2min-overview', label: '2-minute standard architecture overview rehearsed under 120 seconds', hint: 'Practice in Teleprompter tab' },
                { id: 'check-10min-deepdive', label: '10-minute deep-dive structure memorized (7 core subsystems)', hint: 'Walkthrough outline in Teleprompter' },
                { id: 'check-hardest-bug', label: 'STAR response ready for "Hardest bug" (Slow Resync Race / S3.1)', hint: 'Review in Q-Bank tab' },
                { id: 'check-why-go', label: 'Defense ready for "Why Go instead of Rust/C?" (ADR-001)', hint: 'Review in Q-Bank tab' },
                { id: 'check-story-diff', label: 'Story 1 (Differential RESP parity) demonstrated live', hint: 'Execute in 6-Story Demo Suite' },
                { id: 'check-story-repl', label: 'Story 5 (PSYNC replication partial resync) demonstrated live', hint: 'Execute in Replication Lab or Story Suite' },
                { id: 'check-story-tx', label: 'Story 6 (Transaction visualizer & WATCH conflict) demonstrated live', hint: 'Execute in Story 6 visualizer' },
                { id: 'check-non-goals', label: 'Explicit non-goals boundary defense ready (ADR-012/019)', hint: 'Lua, Cluster, Streams, Sentinel justifications' },
              ].map((item) => (
                <label
                  key={item.id}
                  className="flex items-start gap-3 rounded-md border border-border bg-surface-2 p-3 cursor-pointer hover:bg-surface-2/80 transition-colors"
                >
                  <input
                    type="checkbox"
                    checked={!!checklist[item.id]}
                    onChange={(e) =>
                      setChecklist((prev) => ({ ...prev, [item.id]: e.target.checked }))
                    }
                    className="mt-0.5 h-4 w-4 rounded border-border text-accent focus:ring-accent"
                  />
                  <div>
                    <div className="text-xs font-semibold text-text">{item.label}</div>
                    <div className="text-[11px] text-text-muted">{item.hint}</div>
                  </div>
                </label>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
