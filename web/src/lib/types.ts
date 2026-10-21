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
