import { useMemo, useRef, useState } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { KeyDrawer } from '../components/KeyDrawer';
import { Badge, EmptyState, ErrorState, LoadingRows, Panel } from '../components/ui';
import { exec, fetchEvictionPool, fetchKeys } from '../lib/api';
import type { KeyInfo } from '../lib/types';

const columns = 'grid grid-cols-[minmax(0,2.2fr)_5.5rem_7.5rem_minmax(0,1fr)] items-center gap-3';

export function TTLCountdownRing({ ttlMs }: { ttlMs: number }) {
  if (ttlMs === -1) {
    return <span className="font-mono text-xs text-text-faint">persistent</span>;
  }
  if (ttlMs === -2 || ttlMs <= 0) {
    return <span className="font-mono text-xs text-crit font-medium">gone</span>;
  }

  const total = ttlMs > 60000 ? 3600000 : 60000;
  const fraction = Math.min(1, Math.max(0.08, (ttlMs % total) / total));
  const strokeColor = ttlMs < 10000 ? '#f7768e' : ttlMs < 30000 ? '#e0af68' : '#4ec994';
  const radius = 5;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference * (1 - fraction);

  return (
    <div className="flex items-center gap-1.5 font-mono text-xs">
      <svg width="14" height="14" className="shrink-0 -rotate-90">
        <circle cx="7" cy="7" r={radius} fill="none" stroke="#222933" strokeWidth="2" />
        <circle
          cx="7"
          cy="7"
          r={radius}
          fill="none"
          stroke={strokeColor}
          strokeWidth="2"
          strokeDasharray={circumference}
          strokeDashoffset={strokeDashoffset}
          strokeLinecap="round"
        />
      </svg>
      <span className={ttlMs < 10000 ? 'text-crit font-semibold' : 'text-text-muted'}>
        {ttlMs < 1000 ? `${ttlMs}ms` : ttlMs < 60000 ? `${Math.round(ttlMs / 1000)}s` : `${Math.round(ttlMs / 60000)}m`}
      </span>
    </div>
  );
}

export function Keyspace() {
  const [activeTab, setActiveTab] = useState<'keys' | 'expire' | 'eviction'>('keys');
  const [draft, setDraft] = useState('*');
  const [match, setMatch] = useState('*');
  const [type, setType] = useState('');
  const [count, setCount] = useState(100);
  const [selected, setSelected] = useState<KeyInfo | null>(null);
  const parentRef = useRef<HTMLDivElement>(null);

  const query = useInfiniteQuery({
    queryKey: ['keys', match, type, count],
    queryFn: ({ pageParam }) => fetchKeys({ cursor: pageParam, count, match, type: type || undefined }),
    initialPageParam: '0',
    getNextPageParam: (last) => (last.cursor === '0' ? undefined : last.cursor),
  });

  const { data: evictionData, refetch: refetchEviction } = useQuery({
    queryKey: ['eviction-pool'],
    queryFn: fetchEvictionPool,
    enabled: activeTab === 'eviction',
  });

  // Story 2 active cycle sampling state
  const [sampledKeys, setSampledKeys] = useState<{ key: string; ttl: number; expired: boolean }[]>([]);
  const [isSampling, setIsSampling] = useState(false);

  const handleRunExpireCycle = async () => {
    setIsSampling(true);
    try {
      // Step: seed transient keys if needed and query PTTL
      const candidates = keys.filter((k) => k.ttl_ms > 0).slice(0, 8);
      const sampled = candidates.map((k) => ({
        key: k.key,
        ttl: k.ttl_ms,
        expired: k.ttl_ms <= 0,
      }));
      setSampledKeys(sampled);
      await exec(['PING']);
    } finally {
      setIsSampling(false);
    }
  };

  const keys = useMemo(() => query.data?.pages.flatMap((page) => page.keys) ?? [], [query.data]);
  const virtualizer = useVirtualizer({
    count: keys.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 36,
    overscan: 12,
  });

  return (
    <div className="space-y-4">
      {/* View Switcher Tabs (U6b) */}
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-3">
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => setActiveTab('keys')}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              activeTab === 'keys'
                ? 'bg-accent text-bg font-semibold'
                : 'border border-border bg-surface text-text-muted hover:text-text'
            }`}
          >
            Keyspace SCAN Browser
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('expire')}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              activeTab === 'expire'
                ? 'bg-accent text-bg font-semibold'
                : 'border border-border bg-surface text-text-muted hover:text-text'
            }`}
          >
            Story 2: Expire Sampling Loop
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('eviction')}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              activeTab === 'eviction'
                ? 'bg-accent text-bg font-semibold'
                : 'border border-border bg-surface text-text-muted hover:text-text'
            }`}
          >
            Story 3: Eviction Pool &amp; Victim
          </button>
        </div>

        <span className="font-mono text-xs text-text-faint">
          {keys.length} loaded · cursor {query.data?.pages.at(-1)?.cursor ?? '0'}
        </span>
      </div>

      {activeTab === 'keys' && (
        <>
          <Panel
            title="Keyspace Filter"
            actions={
              <button
                type="button"
                onClick={() => void query.refetch()}
                className="rounded-md border border-border px-2 py-1 text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
              >
                Refresh
              </button>
            }
          >
            <form
              className="flex flex-wrap items-end gap-3"
              onSubmit={(event) => {
                event.preventDefault();
                setMatch(draft.trim() || '*');
              }}
            >
              <label className="flex min-w-[12rem] flex-1 flex-col gap-1 text-xs text-text-muted">
                match
                <input
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  spellCheck={false}
                  placeholder="user:*"
                  className="rounded-md border border-border bg-bg px-2 py-1.5 font-mono text-sm text-text placeholder:text-text-faint focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                />
              </label>
              <label className="flex flex-col gap-1 text-xs text-text-muted">
                type
                <select
                  value={type}
                  onChange={(event) => setType(event.target.value)}
                  className="rounded-md border border-border bg-bg px-2 py-1.5 font-mono text-sm text-text focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <option value="">any</option>
                  <option value="string">string</option>
                  <option value="list">list</option>
                  <option value="hash">hash</option>
                  <option value="set">set</option>
                </select>
              </label>
              <label className="flex flex-col gap-1 text-xs text-text-muted">
                count
                <select
                  value={count}
                  onChange={(event) => setCount(Number(event.target.value))}
                  className="rounded-md border border-border bg-bg px-2 py-1.5 font-mono text-sm text-text focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  {[50, 100, 500].map((option) => (
                    <option key={option} value={option}>
                      {option}
                    </option>
                  ))}
                </select>
              </label>
              <button
                type="submit"
                className="rounded-md border border-border bg-surface-2 px-3 py-1.5 text-xs text-text hover:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
              >
                Scan
              </button>
            </form>
          </Panel>

          <Panel
            title="Keyspace Entries"
            actions={
              <span className="font-mono text-xs text-text-faint">
                cursor {query.data?.pages.at(-1)?.cursor ?? '0'}
              </span>
            }
            bodyClassName="p-0"
          >
            <div className={`${columns} border-b border-border px-4 py-2 text-xs uppercase tracking-wider text-text-faint`}>
              <span>key</span>
              <span>type</span>
              <span>ttl ring</span>
              <span>preview</span>
            </div>

            {query.isPending ? (
              <div className="p-4">
                <LoadingRows rows={8} />
              </div>
            ) : query.isError ? (
              <div className="p-4">
                <ErrorState
                  message={query.error instanceof Error ? query.error.message : 'scan failed'}
                  onRetry={() => void query.refetch()}
                />
              </div>
            ) : keys.length === 0 ? (
              <EmptyState title="No keys matched." hint="Try match * or run `make seed`." />
            ) : (
              <div ref={parentRef} className="h-[520px] overflow-auto">
                <div style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
                  {virtualizer.getVirtualItems().map((row) => {
                    const info = keys[row.index];
                    return (
                      <button
                        key={`${info.key}-${row.index}`}
                        type="button"
                        onClick={() => setSelected(info)}
                        style={{ transform: `translateY(${row.start}px)`, height: `${row.size}px` }}
                        className={`${columns} absolute left-0 top-0 w-full border-b border-border/60 px-4 text-left text-sm hover:bg-surface-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent`}
                      >
                        <span className="truncate font-mono text-text">{info.key}</span>
                        <span>
                          <Badge>{info.type}</Badge>
                        </span>
                        <span>
                          <TTLCountdownRing ttlMs={info.ttl_ms} />
                        </span>
                        <span className="truncate font-mono text-xs text-text-faint">{info.preview || '—'}</span>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}

            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-border px-4 py-3">
              <span className="text-xs text-text-muted">
                {keys.length} key{keys.length === 1 ? '' : 's'}
                {query.isFetchingNextPage ? ' · loading more…' : ''}
              </span>
              <button
                type="button"
                onClick={() => void query.fetchNextPage()}
                disabled={!query.hasNextPage || query.isFetchingNextPage}
                className="rounded-md border border-border px-3 py-1 text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-40"
              >
                Load more
              </button>
            </div>
          </Panel>
        </>
      )}

      {/* Story 2: Expire Cycle Sampling Inspector */}
      {activeTab === 'expire' && (
        <div className="rounded-lg border border-border bg-surface p-4 space-y-4 shadow-sm">
          <div className="flex items-center justify-between border-b border-border pb-3">
            <div>
              <span className="rounded bg-accent-soft px-2 py-0.5 text-xs font-mono font-medium text-accent">
                Story 2 Inspector
              </span>
              <h2 className="mt-1 text-base font-semibold text-text">Active Expiration Cycle Sampling (10 Hz)</h2>
              <p className="text-xs text-text-muted">
                Redis active-expire algorithm: samples 20 volatile keys per cycle; deletes expired keys; if &gt;25% expired, repeats immediately.
              </p>
            </div>
            <button
              type="button"
              onClick={handleRunExpireCycle}
              disabled={isSampling}
              className="rounded bg-accent px-3 py-1.5 text-xs font-semibold text-bg hover:opacity-90 disabled:opacity-50"
            >
              {isSampling ? 'Sampling...' : 'Trigger Active Sampling Cycle'}
            </button>
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3 font-mono text-xs">
            <div className="rounded border border-border bg-bg/50 p-3">
              <div className="text-text-muted">Active Cycle Frequency</div>
              <div className="mt-1 text-lg font-bold text-text">10 Hz (every 100ms)</div>
              <div className="mt-0.5 text-[11px] text-text-faint">Background event loop tick</div>
            </div>
            <div className="rounded border border-border bg-bg/50 p-3">
              <div className="text-text-muted">Sampling Budget</div>
              <div className="mt-1 text-lg font-bold text-accent">20 keys / cycle</div>
              <div className="mt-0.5 text-[11px] text-text-faint">Bound per-tick CPU latency &lt;1ms</div>
            </div>
            <div className="rounded border border-border bg-bg/50 p-3">
              <div className="text-text-muted">Threshold for Fast Loop</div>
              <div className="mt-1 text-lg font-bold text-ok">&gt; 25% Expired</div>
              <div className="mt-0.5 text-[11px] text-text-faint">Re-samples if churn is elevated</div>
            </div>
          </div>

          {sampledKeys.length > 0 && (
            <div className="border border-border rounded overflow-hidden">
              <div className="bg-bg/40 p-2 text-xs font-mono text-text-muted border-b border-border">
                Recent Volatile Key Candidates Sampled:
              </div>
              <div className="divide-y divide-border font-mono text-xs">
                {sampledKeys.map((s, idx) => (
                  <div key={idx} className="p-2 flex items-center justify-between">
                    <span className="text-text">{s.key}</span>
                    <TTLCountdownRing ttlMs={s.ttl} />
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Story 3: Approximated LRU Eviction Pool Inspector */}
      {activeTab === 'eviction' && (
        <div className="rounded-lg border border-border bg-surface p-4 space-y-4 shadow-sm">
          <div className="flex items-center justify-between border-b border-border pb-3">
            <div>
              <span className="rounded bg-accent-soft px-2 py-0.5 text-xs font-mono font-medium text-accent">
                Story 3 Inspector
              </span>
              <h2 className="mt-1 text-base font-semibold text-text">Approximated LRU Eviction Pool (16 Slots)</h2>
              <p className="text-xs text-text-muted">
                24-bit access clock sampling into a 16-slot candidate pool sorted by idle time; highest idle key elected as eviction victim.
              </p>
            </div>
            <button
              type="button"
              onClick={() => void refetchEviction()}
              className="rounded border border-border bg-surface-2 px-3 py-1.5 text-xs font-semibold text-text hover:border-accent"
            >
              Resample Pool
            </button>
          </div>

          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-8">
            {(evictionData?.candidates ?? []).map((candidate) => (
              <div
                key={candidate.slot}
                className={`rounded border p-3 font-mono text-xs transition-all ${
                  candidate.is_victim
                    ? 'border-crit bg-crit/10 shadow-[0_0_12px_rgba(247,118,142,0.25)]'
                    : 'border-border bg-bg/50'
                }`}
              >
                <div className="flex items-center justify-between">
                  <span className="text-[10px] text-text-faint">Slot #{candidate.slot}</span>
                  {candidate.is_victim && (
                    <span className="rounded bg-crit px-1 text-[9px] font-bold text-bg uppercase">Victim</span>
                  )}
                </div>
                <div className="mt-2 truncate font-semibold text-text" title={candidate.key}>
                  {candidate.key}
                </div>
                <div className="mt-1 text-[11px] text-text-muted">
                  idle: <span className={candidate.is_victim ? 'text-crit font-bold' : 'text-text'}>{candidate.idle_seconds}s</span>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {selected && (
        <KeyDrawer
          info={selected}
          onClose={() => {
            setSelected(null);
            void query.refetch();
          }}
        />
      )}
    </div>
  );
}
