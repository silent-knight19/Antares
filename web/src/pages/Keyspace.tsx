import { useMemo, useRef, useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { useVirtualizer } from '@tanstack/react-virtual';
import { KeyDrawer } from '../components/KeyDrawer';
import { Badge, EmptyState, ErrorState, LoadingRows, Panel } from '../components/ui';
import { fetchKeys } from '../lib/api';
import { formatTTL } from '../lib/format';
import type { KeyInfo } from '../lib/types';

const columns = 'grid grid-cols-[minmax(0,2.2fr)_5.5rem_6.5rem_minmax(0,1fr)] items-center gap-3';

export function Keyspace() {
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

  const keys = useMemo(() => query.data?.pages.flatMap((page) => page.keys) ?? [], [query.data]);
  const virtualizer = useVirtualizer({
    count: keys.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 36,
    overscan: 12,
  });

  return (
    <div className="space-y-4">
      <Panel
        title="Keyspace"
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
        title="Keys"
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
          <span>ttl</span>
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
                    <span className="font-mono text-xs text-text-muted">{formatTTL(info.ttl_ms)}</span>
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

      {selected && <KeyDrawer info={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
