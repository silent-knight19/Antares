import type { ExecResponse, HealthResponse, InfoResponse, KeysResponse } from './types';

export class ApiError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch (err) {
    throw new ApiError(err instanceof Error ? err.message : 'network error', 0);
  }
  const body = (await res.json().catch(() => null)) as { error?: string } | null;
  if (!res.ok) {
    throw new ApiError(body?.error ?? `HTTP ${res.status}`, res.status);
  }
  return body as T;
}

export function fetchHealth(): Promise<HealthResponse> {
  return request<HealthResponse>('/api/health');
}

export function exec(args: string[], target = 'antares'): Promise<ExecResponse> {
  return request<ExecResponse>('/api/exec', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ args, target }),
  });
}

export function fetchInfo(section?: string): Promise<InfoResponse> {
  const query = section ? `?section=${encodeURIComponent(section)}` : '';
  return request<InfoResponse>(`/api/info${query}`);
}

export interface KeysQuery {
  cursor?: string;
  count?: number;
  match?: string;
  type?: string;
}

export function fetchKeys(query: KeysQuery = {}): Promise<KeysResponse> {
  const params = new URLSearchParams();
  if (query.cursor) params.set('cursor', query.cursor);
  if (query.count) params.set('count', String(query.count));
  if (query.match) params.set('match', query.match);
  if (query.type) params.set('type', query.type);
  const suffix = params.toString();
  return request<KeysResponse>(`/api/keys${suffix ? `?${suffix}` : ''}`);
}
