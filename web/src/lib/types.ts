export type ReplyKind = 'simple' | 'error' | 'integer' | 'bulk' | 'array' | 'null';

export type Reply =
  | { type: 'simple' | 'error' | 'bulk'; value: string }
  | { type: 'integer'; value: number }
  | { type: 'null'; value: null }
  | { type: 'array'; value: Reply[] };

export interface ExecResponse {
  target: string;
  reply: Reply;
  request: string;
  raw: string;
  latency_ms: number;
}

export interface Probe {
  ok: boolean;
  latency_ms?: number;
  error?: string;
}

export interface HealthResponse {
  status: string;
  version: string;
  antares_addr: string;
  antares: Probe;
  redis: Probe;
  at_ms: number;
}

export interface KeyInfo {
  key: string;
  type: string;
  ttl_ms: number;
  preview: string;
}

export interface KeysResponse {
  cursor: string;
  keys: KeyInfo[];
}

export interface InfoResponse {
  raw: string;
  fields: Record<string, string>;
}

export interface InfoEvent {
  fields: Record<string, string>;
  at_ms: number;
}

export interface ClaimEntry {
  value: number;
  unit: string;
  source: string;
  timestamp: string;
  commit: string;
  variance: number;
}

export interface ClaimsResponse {
  claims: Record<string, ClaimEntry>;
  status: 'verified' | 'unverified';
  message?: string;
}

export interface BenchmarkItem {
  pipeline: number;
  command: string;
  antares: {
    median_rps: number;
    p50_ms: number;
    p99_ms: number;
    all_rps: number[];
  };
  redis_ref?: {
    median_rps: number;
    p50_ms: number;
    p99_ms: number;
    all_rps: number[];
  };
  ratio_percent?: number;
}

export interface BenchmarkReport {
  commit: string;
  timestamp: string;
  platform: string;
  go_version: string;
  requests_per_run: number;
  clients: number;
  runs_count: number;
  benchmarks: BenchmarkItem[];
}

export interface BenchmarksResponse {
  status: 'verified' | 'unverified';
  report?: BenchmarkReport;
  claims?: Record<string, ClaimEntry>;
  source_file?: string;
}

export interface LiveBenchResult {
  command: string;
  pipeline: number;
  count: number;
  antares: {
    rps: number;
    p50_ms: number;
    p99_ms: number;
  };
  redis?: {
    rps: number;
    p50_ms: number;
    p99_ms: number;
  };
  ratio_percent?: number;
}

export interface DurabilityTimelineEntry {
  seq: number;
  key: string;
  status: string;
  offset: number;
  latency_ms: number;
}

export interface DurabilitySimResponse {
  status: string;
  writes_sent: number;
  writes_acked: number;
  recovered_keys: number;
  lost_acked_writes: number;
  badge: string;
  final_aof_offset: number;
  timeline: DurabilityTimelineEntry[];
  persistence?: Record<string, string>;
}

export interface EvictionCandidate {
  slot: number;
  key: string;
  idle_seconds: number;
  ttl_ms: number;
  is_victim: boolean;
}

export interface EvictionPoolResponse {
  policy: string;
  pool_size: number;
  sampled_count: number;
  victim_slot: number;
  candidates: EvictionCandidate[];
}

export interface ReplicationSlave {
  id: number;
  ip: string;
  port: number;
  state: string;
  offset: number;
  lag: number;
}

export interface ReplicationLabResponse {
  status: string;
  master: {
    role: string;
    replid: string;
    replid2: string;
    offset: number;
    second_offset: number;
    connected_slaves: number;
    addr: string;
  };
  backlog: {
    active: boolean;
    size: number;
    first_byte_offset: number;
    histlen: number;
    window_start: number;
    window_end: number;
  };
  slaves: ReplicationSlave[];
  claims?: Record<string, ClaimEntry>;
}

export interface ReplicationActionResponse {
  status: string;
  action: string;
  decision?: string;
  replid?: string;
  req_offset?: number;
  master_offset?: number;
  delta_bytes?: number;
  in_backlog?: boolean;
  snapshot_streaming?: boolean;
  explanation?: string;
  writes_sent?: number;
  writes_acked?: number;
  new_offset?: number;
  backlog_histlen?: number;
  window_start?: number;
  window_end?: number;
}

export interface TxStepResult {
  command: string;
  reply: any;
  reply_type: string;
  phase: string;
  latency_ms: number;
}

export interface TransactionLabResponse {
  scenario: string;
  status: 'committed' | 'aborted_by_watch' | 'execabort' | string;
  explanation: string;
  steps: TxStepResult[];
  tx_state: {
    active: boolean;
    dirty: boolean;
    queued_count: number;
    watched_count: number;
    committed_items?: number;
    conflict_detected?: boolean;
    syntax_error?: boolean;
    single_aof_frame?: boolean;
  };
}
