import { useQuery } from '@tanstack/react-query';
import { fetchHealth } from '../lib/api';
import { useInfoStream } from '../lib/useInfoStream';

function uptime(seconds: string | undefined): string | null {
  if (!seconds) return null;
  const total = Number(seconds);
  if (!Number.isFinite(total) || total < 0) return null;
  if (total < 60) return `${Math.floor(total)}s`;
  if (total < 3600) return `${Math.floor(total / 60)}m ${Math.floor(total % 60)}s`;
  return `${Math.floor(total / 3600)}h ${Math.floor((total % 3600) / 60)}m`;
}

function StatusDot({ tone, label }: { tone: 'ok' | 'warn' | 'crit'; label: string }) {
  const glyph = tone === 'ok' ? '✓' : tone === 'warn' ? '!' : '×';
  const color = tone === 'ok' ? 'text-ok' : tone === 'warn' ? 'text-warn' : 'text-crit';
  return (
    <span className="inline-flex items-center gap-1.5 text-text-muted">
      <span aria-hidden className={`font-mono ${color}`}>{glyph}</span>
      <span>{label}</span>
    </span>
  );
}

export function StatusBar() {
  const health = useQuery({ queryKey: ['health'], queryFn: fetchHealth, refetchInterval: 4000 });
  const { fields, state } = useInfoStream();
  const up = health.data?.antares.ok;
  const redis = health.data?.redis;
  const redisConfigured = Boolean(redis && redis.error !== 'not configured');
  const streaming = state === 'live';
  const liveUptime = uptime(fields.uptime_in_seconds);

  return (
    <header className="flex min-h-11 shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-b border-border bg-surface px-4 py-2 text-xs">
      <div className="flex items-center gap-2 font-medium text-text">
        <span aria-hidden className="text-accent">●</span>
        <span>Antares Observatory</span>
      </div>

      <div aria-live="polite" className="ml-auto flex flex-wrap items-center gap-x-4 gap-y-1">
        <span className="font-mono text-text-faint">{health.data?.antares_addr ?? 'connecting…'}</span>
        {health.isError ? (
          <StatusDot tone="crit" label="bridge down" />
        ) : up === undefined ? (
          <span className="text-text-faint">checking…</span>
        ) : up ? (
          <StatusDot tone="ok" label={`up ${health.data?.antares.latency_ms?.toFixed(1) ?? '0.0'} ms`} />
        ) : (
          <StatusDot tone="crit" label="antares down" />
        )}
        {redisConfigured ? (
          redis?.ok ? <StatusDot tone="ok" label="redis up" /> : <StatusDot tone="crit" label="redis down" />
        ) : (
          <StatusDot tone="warn" label="redis off" />
        )}
        {liveUptime && <span className="text-text-muted">up {liveUptime}</span>}
        <span className={streaming ? 'text-text-faint' : 'text-warn'}>
          {streaming ? 'live' : 'reconnecting'}
        </span>
        <span className="text-text-faint">{health.data?.version ?? 'dev'}</span>
      </div>
    </header>
  );
}
