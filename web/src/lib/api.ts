import type {
  BenchmarksResponse,
  ClaimsResponse,
  DurabilitySimResponse,
  EvictionPoolResponse,
  ExecResponse,
  HealthResponse,
  InfoResponse,
  KeysResponse,
  LiveBenchResult,
  ReplicationActionResponse,
  ReplicationLabResponse,
} from './types';

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

export function fetchClaims(): Promise<ClaimsResponse> {
  return request<ClaimsResponse>('/api/claims');
}

export function fetchBenchmarks(): Promise<BenchmarksResponse> {
  return request<BenchmarksResponse>('/api/benchmarks');
}

export function runLiveBenchmark(params: {
  command?: string;
  pipeline?: number;
  count?: number;
  target?: string;
} = {}): Promise<LiveBenchResult> {
  return request<LiveBenchResult>('/api/benchmarks/run', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(params),
  });
}

export function simulateDurability(count = 30): Promise<DurabilitySimResponse> {
  return request<DurabilitySimResponse>('/api/lab/durability/simulate', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ count }),
  });
}

export function fetchEvictionPool(): Promise<EvictionPoolResponse> {
  return request<EvictionPoolResponse>('/api/lab/eviction-pool');
}

export function fetchReplicationLab(): Promise<ReplicationLabResponse> {
  return request<ReplicationLabResponse>('/api/lab/replication');
}

export function performReplicationAction(params: {
  action: 'pump_writes' | 'simulate_partial_resync' | 'simulate_full_resync';
  count?: number;
}): Promise<ReplicationActionResponse> {
  return request<ReplicationActionResponse>('/api/lab/replication/action', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(params),
  });
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
