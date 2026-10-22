import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { loadKeyDetail } from '../lib/keyDetail';
import { exec } from '../lib/api';
import type { KeyInfo } from '../lib/types';
import { TTLCountdownRing } from '../pages/Keyspace';
import { Badge, ErrorState, LoadingRows } from './ui';

export function KeyDrawer({ info, onClose }: { info: KeyInfo; onClose: () => void }) {
  const [isDeleting, setIsDeleting] = useState(false);
  const detail = useQuery({ queryKey: ['key-detail', info.key], queryFn: () => loadKeyDetail(info.key) });

  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [onClose]);

  const handleDelete = async () => {
    if (!window.confirm(`Delete key "${info.key}"?`)) return;
    setIsDeleting(true);
    try {
      await exec(['DEL', info.key]);
      onClose();
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setIsDeleting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-20 flex justify-end">
      <button type="button" aria-label="Close details" onClick={onClose} className="absolute inset-0 bg-black/50" />
      <aside
        role="dialog"
        aria-modal="true"
        aria-label={`Key ${info.key}`}
        className="relative z-10 flex h-full w-full max-w-md flex-col border-l border-border bg-surface"
      >
        <header className="flex items-start justify-between gap-3 border-b border-border p-4">
          <div className="min-w-0">
            <h2 className="break-all font-mono text-sm text-text">{info.key}</h2>
            <div className="mt-2 flex items-center gap-3">
              <Badge tone="accent">{detail.data?.type ?? info.type}</Badge>
              <TTLCountdownRing ttlMs={info.ttl_ms} />
            </div>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-border px-2 py-1 text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            Close
          </button>
        </header>

        <div className="min-h-0 flex-1 overflow-auto p-4">
          {detail.isPending && <LoadingRows rows={4} />}
          {detail.isError && (
            <ErrorState
              message={detail.error instanceof Error ? detail.error.message : 'failed to load key'}
              onRetry={() => void detail.refetch()}
            />
          )}
          {detail.data &&
            (detail.data.rows.length === 0 ? (
              <p className="text-sm text-text-faint">Key expired or empty.</p>
            ) : (
              <dl className="space-y-3">
                {detail.data.rows.map((row, index) => (
                  <div key={`${row.label}-${index}`}>
                    <dt className="text-xs uppercase tracking-wider text-text-faint">{row.label}</dt>
                    <dd className="mt-1 whitespace-pre-wrap break-all font-mono text-sm text-text">{row.value}</dd>
                  </div>
                ))}
              </dl>
            ))}
        </div>

        <footer className="border-t border-border p-3 flex items-center justify-between">
          <span className="text-xs text-text-faint">U6b Active Mutations</span>
          <button
            type="button"
            onClick={handleDelete}
            disabled={isDeleting}
            className="rounded bg-crit/10 border border-crit/30 px-3 py-1 text-xs font-semibold text-crit hover:bg-crit/20 disabled:opacity-50"
          >
            {isDeleting ? 'Deleting...' : 'Delete Key (DEL)'}
          </button>
        </footer>
      </aside>
    </div>
  );
}
