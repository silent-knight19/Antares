import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchBenchmarks, runLiveBenchmark } from '../lib/api';
import type { LiveBenchResult } from '../lib/types';

export function Benchmarks() {
  const [selectedPipeline, setSelectedPipeline] = useState<number>(1);
  const [liveCmd, setLiveCmd] = useState<string>('SET');
  const [livePipe, setLivePipe] = useState<number>(1);
  const [liveCount, setLiveCount] = useState<number>(500);
  const [isRunning, setIsRunning] = useState<boolean>(false);
  const [liveResult, setLiveResult] = useState<LiveBenchResult | null>(null);

  const { data: benchData } = useQuery({
    queryKey: ['benchmarks'],
    queryFn: fetchBenchmarks,
  });

  const handleRunLive = async () => {
    setIsRunning(true);
    setLiveResult(null);
    try {
      const res = await runLiveBenchmark({
        command: liveCmd,
        pipeline: livePipe,
        count: liveCount,
        target: 'both',
      });
      setLiveResult(res);
    } catch (err) {
      console.error('Live benchmark failed', err);
    } finally {
      setIsRunning(false);
    }
  };

  const report = benchData?.report;
  const isVerified = benchData?.status === 'verified';
  const benchmarks = report?.benchmarks?.filter((b) => b.pipeline === selectedPipeline) ?? [];

  return (
    <div className="space-y-6">
      {/* Header & Provenance Status */}
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border pb-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-text">Benchmarks & Redis Comparison (U8)</h1>
          <p className="text-xs text-text-muted">
            Independent differential benchmark claims verified against reference Redis 7.4.11 on Apple Silicon.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${
            isVerified ? 'bg-ok/10 text-ok border border-ok/20' : 'bg-warn/10 text-warn border border-warn/20'
          }`}>
            <span className={`h-1.5 w-1.5 rounded-full ${isVerified ? 'bg-ok' : 'bg-warn'}`} />
            {isVerified ? `Provenance: Verified (${report?.commit || 'claims.json'})` : 'Unverified (Run make verify)'}
          </span>
        </div>
      </div>

      {/* Live Benchmark Execution Panel */}
      <div className="rounded-lg border border-border bg-surface shadow-sm">
        <div className="border-b border-border p-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-text">Live Benchmark Runner</h2>
              <p className="text-xs text-text-muted">Trigger real in-process RESP pipeline runs against Antares right now.</p>
            </div>
            <div className="flex items-center gap-3">
              <label className="flex items-center gap-1.5 text-xs text-text-muted">
                Command:
                <select
                  value={liveCmd}
                  onChange={(e) => setLiveCmd(e.target.value)}
                  className="rounded border border-border bg-bg px-2 py-1 text-xs text-text font-mono"
                >
                  <option value="SET">SET</option>
                  <option value="GET">GET</option>
                  <option value="INCR">INCR</option>
                  <option value="PING">PING</option>
                </select>
              </label>

              <label className="flex items-center gap-1.5 text-xs text-text-muted">
                Pipeline:
                <select
                  value={livePipe}
                  onChange={(e) => setLivePipe(Number(e.target.value))}
                  className="rounded border border-border bg-bg px-2 py-1 text-xs text-text font-mono"
                >
                  <option value={1}>P=1 (Sequential)</option>
                  <option value={16}>P=16 (Batched)</option>
                  <option value={64}>P=64 (Saturated)</option>
                </select>
              </label>

              <label className="flex items-center gap-1.5 text-xs text-text-muted">
                Count:
                <select
                  value={liveCount}
                  onChange={(e) => setLiveCount(Number(e.target.value))}
                  className="rounded border border-border bg-bg px-2 py-1 text-xs text-text font-mono"
                >
                  <option value={200}>200 requests</option>
                  <option value={500}>500 requests</option>
                  <option value={1000}>1,000 requests</option>
                </select>
              </label>

              <button
                type="button"
                onClick={handleRunLive}
                disabled={isRunning}
                className="rounded-md bg-accent px-3 py-1.5 text-xs font-semibold text-bg hover:opacity-90 disabled:opacity-50"
              >
                {isRunning ? 'Benchmarking...' : 'Execute Live Run'}
              </button>
            </div>
          </div>
        </div>

        {liveResult && (
          <div className="p-4 bg-bg/40 border-b border-border">
            <div className="flex flex-wrap items-center justify-between gap-4 font-mono text-xs">
              <div>
                <span className="text-text-muted">Command: </span>
                <span className="font-bold text-accent">{liveResult.command} (P={liveResult.pipeline})</span>
                <span className="text-text-faint ml-2">({liveResult.count} iterations)</span>
              </div>
              <div className="flex items-center gap-4">
                <div>
                  <span className="text-text-muted">Antares: </span>
                  <span className="font-bold text-ok">{Math.round(liveResult.antares.rps).toLocaleString()} rps</span>
                  <span className="text-text-faint ml-1">(p50: {liveResult.antares.p50_ms.toFixed(2)}ms)</span>
                </div>
                {liveResult.redis && (
                  <div>
                    <span className="text-text-muted">Redis 7.4: </span>
                    <span className="font-bold text-text">{Math.round(liveResult.redis.rps).toLocaleString()} rps</span>
                    <span className="text-text-faint ml-1">(p50: {liveResult.redis.p50_ms.toFixed(2)}ms)</span>
                  </div>
                )}
                {liveResult.ratio_percent && (
                  <span className="rounded bg-ok/10 text-ok px-2 py-0.5 font-bold border border-ok/20">
                    {liveResult.ratio_percent.toFixed(1)}% of Redis
                  </span>
                )}
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Pipeline Filter Bar */}
      <div className="flex items-center justify-between border-b border-border pb-2">
        <div className="flex gap-2">
          <button
            type="button"
            onClick={() => setSelectedPipeline(1)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              selectedPipeline === 1
                ? 'bg-accent text-bg font-semibold'
                : 'bg-surface border border-border text-text-muted hover:text-text'
            }`}
          >
            Sequential (P=1) · Low Latency &gt;300% Redis
          </button>
          <button
            type="button"
            onClick={() => setSelectedPipeline(16)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              selectedPipeline === 16
                ? 'bg-accent text-bg font-semibold'
                : 'bg-surface border border-border text-text-muted hover:text-text'
            }`}
          >
            Pipelined (P=16) · High Throughput 750k+ rps
          </button>
        </div>
        <div className="text-xs text-text-faint font-mono">
          50 clients · 20,000 reqs/run · 3 runs median
        </div>
      </div>

      {/* Differential Comparison Cards */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        {benchmarks.map((b) => {
          const antaresRps = b.antares.median_rps;
          const redisRps = b.redis_ref?.median_rps ?? 0;
          const maxRps = Math.max(antaresRps, redisRps, 1);
          const antaresWidth = (antaresRps / maxRps) * 100;
          const redisWidth = (redisRps / maxRps) * 100;
          const ratio = b.ratio_percent ?? (antaresRps / redisRps) * 100;

          return (
            <div key={b.command} className="rounded-lg border border-border bg-surface p-4 shadow-sm">
              <div className="flex items-center justify-between">
                <div>
                  <span className="font-mono text-xs font-bold uppercase tracking-wider text-accent">
                    {b.command} (P={b.pipeline})
                  </span>
                  <div className="text-xs text-text-muted mt-0.5">
                    Antares: {b.antares.p50_ms.toFixed(2)}ms p50 · Redis: {b.redis_ref?.p50_ms.toFixed(2)}ms p50
                  </div>
                </div>
                <span className={`rounded px-2 py-0.5 font-mono text-xs font-bold ${
                  ratio >= 100 ? 'bg-ok/10 text-ok border border-ok/20' : 'bg-info/10 text-info border border-info/20'
                }`}>
                  {ratio.toFixed(1)}% of Redis
                </span>
              </div>

              {/* Differential Comparison Bar Visualizer */}
              <div className="mt-4 space-y-2">
                <div>
                  <div className="flex justify-between text-xs font-mono mb-1">
                    <span className="text-text font-medium">Antares</span>
                    <span className="font-bold text-ok">{Math.round(antaresRps).toLocaleString()} rps</span>
                  </div>
                  <div className="h-4 w-full rounded bg-surface-2 overflow-hidden">
                    <div
                      className="h-full bg-accent transition-all duration-500 rounded"
                      style={{ width: `${antaresWidth}%` }}
                    />
                  </div>
                </div>

                <div>
                  <div className="flex justify-between text-xs font-mono mb-1">
                    <span className="text-text-muted">Redis 7.4.11</span>
                    <span className="text-text-muted">{Math.round(redisRps).toLocaleString()} rps</span>
                  </div>
                  <div className="h-4 w-full rounded bg-surface-2 overflow-hidden">
                    <div
                      className="h-full bg-text-faint/50 transition-all duration-500 rounded"
                      style={{ width: `${redisWidth}%` }}
                    />
                  </div>
                </div>
              </div>

              <div className="mt-3 flex justify-between border-t border-border pt-2 text-[11px] font-mono text-text-faint">
                <span>p99: {b.antares.p99_ms.toFixed(2)}ms vs {b.redis_ref?.p99_ms.toFixed(2)}ms</span>
                <span>Runs: {b.antares.all_rps.map((r) => Math.round(r / 1000) + 'k').join(', ')}</span>
              </div>
            </div>
          );
        })}
      </div>

      {/* Latency Percentiles Detailed Breakdown Table */}
      <div className="rounded-lg border border-border bg-surface overflow-hidden shadow-sm">
        <div className="border-b border-border p-4">
          <h3 className="text-sm font-semibold text-text">Full Latency Percentiles Matrix</h3>
        </div>
        <table className="w-full text-left font-mono text-xs">
          <thead className="border-b border-border bg-bg/50 text-text-muted">
            <tr>
              <th className="p-3">Command</th>
              <th className="p-3">Pipeline</th>
              <th className="p-3">Antares Median RPS</th>
              <th className="p-3">Redis Median RPS</th>
              <th className="p-3">Antares p50</th>
              <th className="p-3">Redis p50</th>
              <th className="p-3">Differential Ratio</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-border">
            {(report?.benchmarks ?? []).map((b, i) => (
              <tr key={i} className="hover:bg-surface-2">
                <td className="p-3 font-bold text-accent">{b.command}</td>
                <td className="p-3 text-text-muted">P={b.pipeline}</td>
                <td className="p-3 font-bold text-text">{Math.round(b.antares.median_rps).toLocaleString()}</td>
                <td className="p-3 text-text-muted">{Math.round(b.redis_ref?.median_rps ?? 0).toLocaleString()}</td>
                <td className="p-3 text-ok">{b.antares.p50_ms.toFixed(3)}ms</td>
                <td className="p-3 text-text-muted">{b.redis_ref?.p50_ms.toFixed(3)}ms</td>
                <td className="p-3 font-bold text-ok">{b.ratio_percent?.toFixed(1)}%</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Environment & Hardware Manifest */}
      <div className="rounded-lg border border-border bg-surface p-4 text-xs font-mono text-text-faint flex flex-wrap justify-between gap-2">
        <span>Platform: {report?.platform || 'darwin/arm64'}</span>
        <span>Compiler: {report?.go_version || 'go1.27.1'}</span>
        <span>Reference Engine: Redis 7.4.11 (Official Container)</span>
        <span>Suite Source: bench/scripts/run-bench.sh</span>
      </div>
    </div>
  );
}
