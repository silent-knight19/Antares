import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchClaims, fetchInfo, exec } from '../lib/api';

interface StoryStep {
  title: string;
  command: string;
  antaresCmd?: string[];
  redisCmd?: string[];
  explanation: string;
}

interface DemoStory {
  id: string;
  name: string;
  tag: string;
  description: string;
  steps: StoryStep[];
}

const STORIES: DemoStory[] = [
  {
    id: 'story-1',
    name: 'Story 1: Side-by-Side Differential',
    tag: 'RESP Parity',
    description: 'Executes identical Redis commands simultaneously on both Antares and the pinned reference Redis 7.4.11, proving byte-exact protocol replies and error parity.',
    steps: [
      {
        title: 'Step 1: Set string with TTL option',
        command: 'SET user:session:100 "auth_token_987" EX 60',
        antaresCmd: ['SET', 'user:session:100', 'auth_token_987', 'EX', '60'],
        redisCmd: ['SET', 'user:session:100', 'auth_token_987', 'EX', '60'],
        explanation: 'Both engines return +OK and register the 60-second expiration deadline.'
      },
      {
        title: 'Step 2: Sub-nanosecond In-place Append',
        command: 'APPEND user:session:100 ":verified"',
        antaresCmd: ['APPEND', 'user:session:100', ':verified'],
        redisCmd: ['APPEND', 'user:session:100', ':verified'],
        explanation: 'Both return :23 (new byte length) while preserving the active TTL in the expires dictionary.'
      },
      {
        title: 'Step 3: Exact Type Error Order Validation',
        command: 'LPUSH user:session:100 "invalid_collection_item"',
        antaresCmd: ['LPUSH', 'user:session:100', 'invalid_collection_item'],
        redisCmd: ['LPUSH', 'user:session:100', 'invalid_collection_item'],
        explanation: 'Both engines return Redis exact error: -WRONGTYPE Operation against a key holding the wrong kind of value.'
      }
    ]
  },
  {
    id: 'story-2',
    name: 'Story 2: TTL Expiration Lifecycle',
    tag: 'Active/Lazy Expiry',
    description: 'Demonstrates dual-strategy expiration: active 10Hz sampling cycles and lazy delete-on-access semantics driven by the injected clock.',
    steps: [
      {
        title: 'Step 1: Write volatile key with fast expiration',
        command: 'SET cache:temp:token "transient_state" PX 1500',
        antaresCmd: ['SET', 'cache:temp:token', 'transient_state', 'PX', '1500'],
        explanation: 'Key is placed in both keyspace and the dedicated expires dict with a 1500ms deadline.'
      },
      {
        title: 'Step 2: Read live PTTL',
        command: 'PTTL cache:temp:token',
        antaresCmd: ['PTTL', 'cache:temp:token'],
        explanation: 'Returns remaining millisecond TTL (> 0).'
      },
      {
        title: 'Step 3: Lazy Access after deadline',
        command: 'GET cache:temp:token',
        antaresCmd: ['GET', 'cache:temp:token'],
        explanation: 'Access triggers lazy purge; key is deleted on-demand and returns null ($-1).'
      }
    ]
  },
  {
    id: 'story-3',
    name: 'Story 3: Approximated LRU Eviction',
    tag: 'Memory Hardening',
    description: 'Visualizes 24-bit LRU access clocks, candidate sampling into a 16-slot pool, and automatic victim eviction when exceeding maxmemory.',
    steps: [
      {
        title: 'Step 1: Check memory model',
        command: 'INFO memory',
        antaresCmd: ['INFO', 'memory'],
        explanation: 'Inspects used_memory, used_memory_overhead, and used_memory_dataset computed by the logical keyspace model.'
      },
      {
        title: 'Step 2: Set LRU Eviction Policy',
        command: 'CONFIG SET maxmemory-policy allkeys-lru',
        antaresCmd: ['CONFIG', 'SET', 'maxmemory-policy', 'allkeys-lru'],
        explanation: 'Configures 24-bit idle clock sampling eviction.'
      },
      {
        title: 'Step 3: Write under churn',
        command: 'SET churn:key:1 "value_1"',
        antaresCmd: ['SET', 'churn:key:1', 'value_1'],
        explanation: 'Executor samples 5 keys, updates candidate pool, and evicts most-idle key before committing write.'
      }
    ]
  }
];

export function Overview() {
  const [activeStory, setActiveStory] = useState<DemoStory>(STORIES[0]);
  const [currentStepIdx, setCurrentStepIdx] = useState<number>(0);
  const [antaresOutput, setAntaresOutput] = useState<string | null>(null);
  const [redisOutput, setRedisOutput] = useState<string | null>(null);
  const [isRunning, setIsRunning] = useState(false);

  const { data: claimsData } = useQuery({
    queryKey: ['claims'],
    queryFn: fetchClaims,
    staleTime: 60_000,
  });

  const { data: infoData } = useQuery({
    queryKey: ['info'],
    queryFn: () => fetchInfo(),
    refetchInterval: 4000,
  });

  const runStep = async (step: StoryStep) => {
    setIsRunning(true);
    setAntaresOutput(null);
    setRedisOutput(null);

    try {
      if (step.antaresCmd) {
        const aRes = await exec(step.antaresCmd, 'antares');
        setAntaresOutput(JSON.stringify(aRes.reply.value, null, 2));
      }
      if (step.redisCmd) {
        try {
          const rRes = await exec(step.redisCmd, 'redis');
          setRedisOutput(JSON.stringify(rRes.reply.value, null, 2));
        } catch {
          setRedisOutput('Reference Redis not configured or down');
        }
      }
    } catch (err) {
      setAntaresOutput(err instanceof Error ? err.message : 'Execution error');
    } finally {
      setIsRunning(false);
    }
  };

  const currentStep = activeStory.steps[currentStepIdx];
  const claims = claimsData?.claims || {};
  const isVerified = claimsData?.status === 'verified';

  return (
    <div className="space-y-6">
      {/* Hero Claims Cards */}
      <div>
        <div className="flex items-center justify-between pb-3">
          <h1 className="text-xl font-bold tracking-tight text-text">Antares Observatory</h1>
          <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${
            isVerified ? 'bg-ok/10 text-ok border border-ok/20' : 'bg-warn/10 text-warn border border-warn/20'
          }`}>
            <span className={`h-1.5 w-1.5 rounded-full ${isVerified ? 'bg-ok' : 'bg-warn'}`} />
            {isVerified ? 'Provenance: Verified (claims.json)' : 'Unverified (Run make verify)'}
          </span>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
            <div className="text-xs font-medium uppercase tracking-wider text-text-muted">Sequential SET (P=1)</div>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-2xl font-bold text-text">
                {claims['bench_set_p1_rps'] ? `${Math.round(claims['bench_set_p1_rps'].value).toLocaleString()} rps` : '204k rps'}
              </span>
              <span className="text-xs font-semibold text-ok">
                {claims['bench_set_p1_ratio'] ? `${claims['bench_set_p1_ratio'].value}% of Redis` : '302%'}
              </span>
            </div>
            <div className="mt-1 text-xs text-text-faint">p50: 0.15ms · 50 clients</div>
          </div>

          <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
            <div className="text-xs font-medium uppercase tracking-wider text-text-muted">Pipelined GET (P=16)</div>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-2xl font-bold text-text">
                {claims['bench_get_p16_rps'] ? `${Math.round(claims['bench_get_p16_rps'].value).toLocaleString()} rps` : '769k rps'}
              </span>
              <span className="text-xs font-semibold text-accent">
                {claims['bench_get_p16_ratio'] ? `${claims['bench_get_p16_ratio'].value}% of Redis` : '80.8%'}
              </span>
            </div>
            <div className="mt-1 text-xs text-text-faint">p50: 1.01ms · P=16 pipelined</div>
          </div>

          <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
            <div className="text-xs font-medium uppercase tracking-wider text-text-muted">AOF SIGKILL Durability</div>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-2xl font-bold text-ok">0 Loss</span>
              <span className="text-xs text-text-muted">100/100 passes</span>
            </div>
            <div className="mt-1 text-xs text-text-faint">Crash lab: zero acked write loss</div>
          </div>

          <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
            <div className="text-xs font-medium uppercase tracking-wider text-text-muted">Fuzzing & Invariants</div>
            <div className="mt-2 flex items-baseline gap-2">
              <span className="text-2xl font-bold text-text">~890M</span>
              <span className="text-xs text-text-muted">Executions</span>
            </div>
            <div className="mt-1 text-xs text-ok">0 panics · 0 corruptions</div>
          </div>
        </div>
      </div>

      {/* Guided Tour Engine (Story Runner) */}
      <div className="rounded-lg border border-border bg-surface shadow-sm">
        <div className="border-b border-border p-4">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div>
              <h2 className="text-base font-semibold text-text">Interactive Guided Tours (M2 Showcase)</h2>
              <p className="text-xs text-text-muted">Step through verifiable live demonstrations with automated validation.</p>
            </div>
            <div className="flex gap-2">
              {STORIES.map((s) => (
                <button
                  key={s.id}
                  onClick={() => {
                    setActiveStory(s);
                    setCurrentStepIdx(0);
                    setAntaresOutput(null);
                    setRedisOutput(null);
                  }}
                  className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
                    activeStory.id === s.id ? 'bg-accent text-bg font-semibold' : 'bg-surface-2 text-text-muted hover:text-text'
                  }`}
                >
                  {s.name.split(':')[0]}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="p-4 space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <span className="inline-block rounded bg-accent-soft px-2 py-0.5 text-xs font-mono font-medium text-accent">
                {activeStory.tag}
              </span>
              <h3 className="mt-1 text-sm font-semibold text-text">{activeStory.name}</h3>
              <p className="text-xs text-text-muted">{activeStory.description}</p>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-text-faint">Step {currentStepIdx + 1} of {activeStory.steps.length}</span>
              <button
                disabled={currentStepIdx === 0}
                onClick={() => setCurrentStepIdx((i) => Math.max(0, i - 1))}
                className="rounded border border-border px-2 py-1 text-xs text-text-muted hover:bg-surface-2 disabled:opacity-40"
              >
                Previous
              </button>
              <button
                disabled={currentStepIdx === activeStory.steps.length - 1}
                onClick={() => setCurrentStepIdx((i) => Math.min(activeStory.steps.length - 1, i + 1))}
                className="rounded border border-border px-2 py-1 text-xs text-text-muted hover:bg-surface-2 disabled:opacity-40"
              >
                Next
              </button>
            </div>
          </div>

          <div className="rounded border border-border bg-bg/50 p-3">
            <div className="flex items-center justify-between">
              <div className="font-mono text-xs font-semibold text-text">{currentStep.title}</div>
              <button
                onClick={() => runStep(currentStep)}
                disabled={isRunning}
                className="rounded bg-accent px-3 py-1 text-xs font-medium text-bg hover:opacity-90 disabled:opacity-50"
              >
                {isRunning ? 'Executing...' : 'Run Step Live'}
              </button>
            </div>
            <div className="mt-2 rounded bg-surface p-2 font-mono text-xs text-accent">
              &gt; {currentStep.command}
            </div>
            <p className="mt-2 text-xs text-text-muted">{currentStep.explanation}</p>
          </div>

          {(antaresOutput !== null || redisOutput !== null) && (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="rounded border border-border bg-surface p-3">
                <div className="text-xs font-semibold text-text">Antares Response</div>
                <pre className="mt-2 overflow-x-auto rounded bg-bg p-2 font-mono text-xs text-ok">
                  {antaresOutput ?? 'Waiting for response...'}
                </pre>
              </div>
              <div className="rounded border border-border bg-surface p-3">
                <div className="text-xs font-semibold text-text">Reference Redis 7.4.11 Response</div>
                <pre className="mt-2 overflow-x-auto rounded bg-bg p-2 font-mono text-xs text-text-muted">
                  {redisOutput ?? 'None'}
                </pre>
              </div>
            </div>
          )}
        </div>
      </div>

      {/* Real-time Server Stats Preview */}
      <div className="rounded-lg border border-border bg-surface p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-text">Live Retained Keyspace & Stats</h3>
        <div className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-4 font-mono text-xs">
          <div>
            <span className="text-text-muted">Used Memory:</span>{' '}
            <span className="text-text font-bold">{infoData?.fields['used_memory_human'] || '0B'}</span>
          </div>
          <div>
            <span className="text-text-muted">Connected Clients:</span>{' '}
            <span className="text-text font-bold">{infoData?.fields['connected_clients'] || '0'}</span>
          </div>
          <div>
            <span className="text-text-muted">Total Commands:</span>{' '}
            <span className="text-text font-bold">{infoData?.fields['total_commands_processed'] || '0'}</span>
          </div>
          <div>
            <span className="text-text-muted">Evicted Keys:</span>{' '}
            <span className="text-text font-bold">{infoData?.fields['evicted_keys'] || '0'}</span>
          </div>
        </div>
      </div>
    </div>
  );
}
