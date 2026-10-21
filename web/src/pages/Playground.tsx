import { useEffect, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ReplyView } from '../components/ReplyView';
import { Badge, ErrorState, EmptyState, Panel } from '../components/ui';
import { exec, fetchHealth } from '../lib/api';
import { parseCommand } from '../lib/command';
import type { ExecResponse } from '../lib/types';

const HISTORY_KEY = 'antares.playground.history';
const WIRE_KEY = 'antares.playground.wire';

const presets: { label: string; command: string }[] = [
  { label: 'PING', command: 'PING' },
  { label: 'SET', command: 'SET user:1 Alice EX 60' },
  { label: 'GET', command: 'GET user:1' },
  { label: 'INCR', command: 'INCR counter' },
  { label: 'LPUSH', command: 'LPUSH queue:jobs a b c' },
  { label: 'HSET', command: 'HSET user:1 name Alice' },
  { label: 'INFO', command: 'INFO' },
];

function loadHistory(): string[] {
  try {
    const raw = localStorage.getItem(HISTORY_KEY);
    const parsed = raw ? (JSON.parse(raw) as unknown) : [];
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : [];
  } catch {
    return [];
  }
}

function replyTone(type: ExecResponse['reply']['type']) {
  switch (type) {
    case 'error':
      return 'crit' as const;
    case 'simple':
      return 'ok' as const;
    case 'integer':
      return 'info' as const;
    case 'null':
      return 'warn' as const;
    default:
      return 'neutral' as const;
  }
}

function visibleWire(value: string) {
  return value.replace(/\r/g, '\\r').replace(/\n/g, '\\n');
}

function bytes(value: string) {
  return new TextEncoder().encode(value).length;
}

export function Playground() {
  const health = useQuery({ queryKey: ['health'], queryFn: fetchHealth });
  const redisConfigured = Boolean(health.data && health.data.redis.error !== 'not configured');
  const [target, setTarget] = useState<'antares' | 'redis'>('antares');
  const [input, setInput] = useState('');
  const [history, setHistory] = useState<string[]>(loadHistory);
  const [historyIndex, setHistoryIndex] = useState(-1);
  const [result, setResult] = useState<ExecResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [wire, setWire] = useState(() => localStorage.getItem(WIRE_KEY) === '1');
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  useEffect(() => {
    localStorage.setItem(WIRE_KEY, wire ? '1' : '0');
  }, [wire]);

  async function run(raw: string) {
    const command = raw.trim();
    if (!command || pending) return;
    let args: string[];
    try {
      args = parseCommand(command);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'invalid command');
      return;
    }
    if (args.length === 0) return;

    setPending(true);
    setError(null);
    try {
      const response = await exec(args, target);
      setResult(response);
      const next = [...history.filter((item, index) => item !== command || index === history.length - 1), command].slice(-100);
      setHistory(next);
      localStorage.setItem(HISTORY_KEY, JSON.stringify(next));
      setHistoryIndex(-1);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'exec failed');
    } finally {
      setPending(false);
    }
  }

  function onKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'ArrowUp') {
      event.preventDefault();
      if (history.length === 0) return;
      const next = historyIndex < 0 ? history.length - 1 : Math.max(0, historyIndex - 1);
      setHistoryIndex(next);
      setInput(history[next]);
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      if (historyIndex < 0) return;
      const next = historyIndex + 1;
      if (next >= history.length) {
        setHistoryIndex(-1);
        setInput('');
      } else {
        setHistoryIndex(next);
        setInput(history[next]);
      }
    }
  }

  const recent = history.slice(-6).reverse();

  return (
    <div className="space-y-4">
      <Panel
        title="Playground"
        actions={
          <div className="flex items-center gap-3">
            {redisConfigured && (
              <label className="flex items-center gap-1.5 text-xs text-text-muted">
                target
                <select
                  value={target}
                  onChange={(event) => setTarget(event.target.value as 'antares' | 'redis')}
                  className="rounded-md border border-border bg-surface-2 px-2 py-1 font-mono text-xs text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
                >
                  <option value="antares">antares</option>
                  <option value="redis">redis</option>
                </select>
              </label>
            )}
            <label className="flex items-center gap-1.5 text-xs text-text-muted">
              <input
                type="checkbox"
                checked={wire}
                onChange={(event) => setWire(event.target.checked)}
                className="accent-accent"
              />
              wire trace
            </label>
          </div>
        }
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void run(input);
          }}
        >
          <div className="flex items-center gap-2 rounded-md border border-border bg-bg px-3 py-2 focus-within:border-accent">
            <span aria-hidden className="select-none font-mono text-accent">
              &gt;
            </span>
            <input
              ref={inputRef}
              value={input}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={onKeyDown}
              spellCheck={false}
              autoComplete="off"
              aria-label="Command"
              placeholder="GET user:1"
              className="min-w-0 flex-1 bg-transparent font-mono text-sm text-text placeholder:text-text-faint focus:outline-none"
            />
            <button
              type="submit"
              disabled={pending}
              className="rounded-md border border-border px-3 py-1 text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent disabled:opacity-50"
            >
              {pending ? 'Running…' : 'Run'}
            </button>
          </div>
        </form>

        <div className="mt-3 flex flex-wrap items-center gap-2">
          <span className="text-xs uppercase tracking-wider text-text-faint">presets</span>
          {presets.map((preset) => (
            <button
              key={preset.label}
              type="button"
              onClick={() => {
                setInput(preset.command);
                inputRef.current?.focus();
              }}
              className="rounded-md border border-border bg-surface-2 px-2 py-1 font-mono text-xs text-text-muted hover:border-accent hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
            >
              {preset.label}
            </button>
          ))}
        </div>

        {recent.length > 0 && (
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <span className="text-xs uppercase tracking-wider text-text-faint">history</span>
            {recent.map((command, index) => (
              <button
                key={`${command}-${index}`}
                type="button"
                onClick={() => {
                  setInput(command);
                  inputRef.current?.focus();
                }}
                className="max-w-[28ch] truncate rounded-md px-2 py-1 font-mono text-xs text-text-muted hover:bg-surface-2 hover:text-text focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent"
              >
                {command}
              </button>
            ))}
          </div>
        )}
      </Panel>

      {error && <ErrorState message={error} onRetry={() => void run(input)} />}

      {!result && !error && (
        <Panel title="Response">
          <EmptyState title="No response yet." hint="Type a command or pick a preset, then press Enter." />
        </Panel>
      )}

      {result && (
        <div className="grid gap-4 lg:grid-cols-5">
          <Panel
            className="lg:col-span-3"
            title="Response"
            actions={
              <div className="flex items-center gap-2">
                <Badge tone={replyTone(result.reply.type)}>{result.reply.type}</Badge>
                <span className="font-mono text-xs text-text-muted">{result.latency_ms.toFixed(3)} ms</span>
                <span className="font-mono text-xs text-text-faint">{bytes(result.raw)} B</span>
              </div>
            }
            bodyClassName="max-h-[420px] overflow-auto"
          >
            <ReplyView reply={result.reply} />
          </Panel>

          {wire && (
            <Panel className="lg:col-span-2" title="Wire" bodyClassName="max-h-[420px] overflow-auto">
              <dl className="space-y-3 font-mono text-xs">
                <div>
                  <dt className="text-text-faint">request · {result.target}</dt>
                  <dd className="mt-1 whitespace-pre-wrap break-all text-info">{visibleWire(result.request)}</dd>
                </div>
                <div>
                  <dt className="text-text-faint">response</dt>
                  <dd className="mt-1 whitespace-pre-wrap break-all text-text-muted">{visibleWire(result.raw)}</dd>
                </div>
              </dl>
            </Panel>
          )}
        </div>
      )}
    </div>
  );
}
