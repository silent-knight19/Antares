import type { Reply } from '../lib/types';

function binary(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (code < 0x20 && code !== 0x09 && code !== 0x0a && code !== 0x0d) return true;
  }
  return false;
}

function escaped(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/\r/g, '\\r')
    .replace(/\n/g, '\\n')
    .replace(/\t/g, '\\t');
}

function ReplyNode({ reply }: { reply: Reply }) {
  switch (reply.type) {
    case 'simple':
      return <span className="text-ok">+{reply.value}</span>;
    case 'error':
      return <span className="text-crit">-{reply.value}</span>;
    case 'integer':
      return <span className="text-info">:{reply.value}</span>;
    case 'null':
      return <span className="text-text-faint">(nil)</span>;
    case 'bulk':
      return binary(reply.value) ? (
        <span>
          <span className="mr-2 rounded-sm border border-warn/30 px-1 py-0.5 text-xs text-warn">binary</span>
          <span className="text-text-muted">{escaped(reply.value)}</span>
        </span>
      ) : (
        <span className="whitespace-pre-wrap break-words text-text">"{reply.value}"</span>
      );
    case 'array':
      return (
        <div>
          <div className="text-text-faint">(array) {reply.value.length}</div>
          <ol className="mt-1 space-y-1 border-l border-border pl-3">
            {reply.value.map((item, index) => (
              <li key={index} className="flex gap-3">
                <span className="select-none text-text-faint">{index}</span>
                <ReplyNode reply={item} />
              </li>
            ))}
          </ol>
        </div>
      );
  }
}

export function ReplyView({ reply }: { reply: Reply }) {
  return (
    <div className="font-mono text-sm leading-relaxed">
      <ReplyNode reply={reply} />
    </div>
  );
}
