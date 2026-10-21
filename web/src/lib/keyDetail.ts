import { exec } from './api';
import { formatTTL } from './format';
import type { Reply } from './types';

export interface DetailRow {
  label: string;
  value: string;
}

export interface KeyDetail {
  type: string;
  rows: DetailRow[];
}

const maxInlineBytes = 65536;
const maxInlineEntries = 200;
const pageSize = 50;

function asInteger(reply: Reply): number | null {
  return reply.type === 'integer' ? reply.value : null;
}

function asText(reply: Reply): string | null {
  return reply.type === 'bulk' || reply.type === 'simple' ? reply.value : null;
}

async function call(args: string[]): Promise<Reply> {
  return (await exec(args)).reply;
}

async function inline(reply: Reply): Promise<DetailRow[]> {
  if (reply.type !== 'array') return [];
  return reply.value.map((item, index) => ({ label: `${index}`, value: asText(item) ?? '' }));
}

export async function loadKeyDetail(key: string): Promise<KeyDetail> {
  const typeReply = await call(['TYPE', key]);
  const type = typeReply.type === 'simple' ? typeReply.value : 'none';
  if (type === 'none') {
    return { type, rows: [] };
  }

  const rows: DetailRow[] = [];
  const ttl = await call(['PTTL', key]);
  const ttlValue = asInteger(ttl);
  if (ttlValue !== null) {
    rows.push({ label: 'TTL', value: formatTTL(ttlValue) });
  }

  if (type === 'string') {
    const length = asInteger(await call(['STRLEN', key])) ?? 0;
    rows.push({ label: 'length', value: `${length} bytes` });
    if (length <= maxInlineBytes) {
      rows.push({ label: 'value', value: asText(await call(['GET', key])) ?? '' });
    } else {
      rows.push({ label: 'value', value: `omitted (${length} bytes)` });
    }
  } else if (type === 'list') {
    const length = asInteger(await call(['LLEN', key])) ?? 0;
    rows.push({ label: 'length', value: `${length} items` });
    const slice = await call(['LRANGE', key, '0', String(pageSize - 1)]);
    if (length > 0) {
      rows.push({ label: `elements 0–${Math.min(length, pageSize) - 1}`, value: '\n' + (await inline(slice)).map((row) => row.value).join('\n') });
    }
  } else if (type === 'hash') {
    const length = asInteger(await call(['HLEN', key])) ?? 0;
    rows.push({ label: 'fields', value: `${length}` });
    if (length <= maxInlineEntries) {
      const all = await call(['HGETALL', key]);
      if (all.type === 'array') {
        const lines: string[] = [];
        for (let i = 0; i + 1 < all.value.length; i += 2) {
          lines.push(`${asText(all.value[i]) ?? ''} = ${asText(all.value[i + 1]) ?? ''}`);
        }
        if (lines.length > 0) rows.push({ label: 'entries', value: '\n' + lines.join('\n') });
      }
    } else {
      rows.push({ label: 'entries', value: `omitted (${length} fields)` });
    }
  } else if (type === 'set') {
    const length = asInteger(await call(['SCARD', key])) ?? 0;
    rows.push({ label: 'members', value: `${length}` });
    if (length <= maxInlineEntries) {
      const members = await call(['SMEMBERS', key]);
      if (members.type === 'array' && members.value.length > 0) {
        rows.push({ label: 'members', value: '\n' + (await inline(members)).map((row) => row.value).join('\n') });
      }
    } else {
      rows.push({ label: 'members', value: `omitted (${length} members)` });
    }
  }

  return { type, rows };
}
