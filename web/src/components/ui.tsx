import type { ReactNode } from 'react';

type Tone = 'neutral' | 'ok' | 'warn' | 'crit' | 'info' | 'accent';

const toneClass: Record<Tone, string> = {
  neutral: 'border-border bg-surface-2 text-text-muted',
  ok: 'border-ok/30 bg-ok/10 text-ok',
  warn: 'border-warn/30 bg-warn/10 text-warn',
  crit: 'border-crit/30 bg-crit/10 text-crit',
  info: 'border-info/30 bg-info/10 text-info',
  accent: 'border-accent/30 bg-accent-soft text-accent',
};

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span className={`inline-flex items-center rounded-sm border px-1.5 py-0.5 font-mono text-xs ${toneClass[tone]}`}>
      {children}
    </span>
  );
}

export function Panel({
  title,
  actions,
  children,
  className = '',
  bodyClassName = '',
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <section className={`rounded-lg border border-border bg-surface ${className}`}>
      {(title || actions) && (
        <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <h2 className="text-lg font-medium text-text">{title}</h2>
          {actions}
        </header>
      )}
      <div className={`p-4 ${bodyClassName}`}>{children}</div>
    </section>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div role="alert" className="rounded-md border border-crit/40 bg-crit/10 p-4">
      <div className="flex items-start gap-3">
        <span aria-hidden className="mt-0.5 text-crit">×</span>
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium text-crit">Request failed</p>
          <p className="mt-1 break-words font-mono text-xs text-text-muted">{message}</p>
        </div>
        {onRetry && (
          <button
            type="button"
            onClick={onRetry}
            className="rounded-md border border-border px-2 py-1 text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
          >
            Retry
          </button>
        )}
      </div>
    </div>
  );
}

export function EmptyState({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="py-12 text-center">
      <p className="text-sm text-text-muted">{title}</p>
      {hint && <p className="mt-2 font-mono text-xs text-text-faint">{hint}</p>}
    </div>
  );
}

export function LoadingRows({ rows = 5 }: { rows?: number }) {
  return (
    <div aria-busy="true" className="space-y-2">
      {Array.from({ length: rows }, (_, index) => (
        <div key={index} className="h-8 animate-pulse rounded-sm bg-surface-2" />
      ))}
    </div>
  );
}
