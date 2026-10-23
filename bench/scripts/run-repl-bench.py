#!/usr/bin/env python3
"""run-repl-bench.py — S3.2 replication benchmark for Antares.

Measures SET throughput on one Antares master with no replica and with one
replica attached, then samples master/replica offsets during the loaded run to
report byte lag and catch-up time. Raw output goes to
bench/results/<commit>-repl.json; claims.json is updated when WRITE_CLAIMS=1.

Environment:
  REPL_REQUESTS   requests per run (default 500000)
  REPL_CLIENTS    concurrent clients (default 50)
  REPL_RUNS       iterations per pipeline (default 3)
  REPL_PIPELINES  space-separated pipelines (default "1 16")
  REPL_SAMPLE_MS  lag sampling interval in ms (default 50)
  REPL_MASTER_PORT / REPL_REPLICA_PORT (default 6383 / 6384)
  WRITE_CLAIMS    1 to merge results into claims.json (default 0)
"""

from __future__ import annotations

import csv
import io
import json
import os
import shutil
import socket
import statistics
import subprocess
import sys
import threading
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BIN = ROOT / "bin" / "antares"
HOST = "127.0.0.1"
MASTER_PORT = int(os.environ.get("REPL_MASTER_PORT", "6383"))
REPLICA_PORT = int(os.environ.get("REPL_REPLICA_PORT", "6384"))
REQUESTS = int(os.environ.get("REPL_REQUESTS", "500000"))
CLIENTS = int(os.environ.get("REPL_CLIENTS", "50"))
RUNS = int(os.environ.get("REPL_RUNS", "3"))
PIPELINES = [int(p) for p in os.environ.get("REPL_PIPELINES", "1 16").split()]
SAMPLE_MS = int(os.environ.get("REPL_SAMPLE_MS", "50"))
WRITE_CLAIMS = os.environ.get("WRITE_CLAIMS", "0") == "1"
RESULT_DIR = ROOT / "bench" / "results"


class Resp:
    def __init__(self, port: int, timeout: float = 2.0):
        self.sock = socket.create_connection((HOST, port), timeout=timeout)
        self.sock.settimeout(timeout)
        self.buf = b""

    def close(self) -> None:
        self.sock.close()

    def _read_line(self) -> bytes:
        while b"\r\n" not in self.buf:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("connection closed")
            self.buf += chunk
        line, self.buf = self.buf.split(b"\r\n", 1)
        return line

    def _read_exact(self, n: int) -> bytes:
        while len(self.buf) < n:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise ConnectionError("connection closed")
            self.buf += chunk
        data, self.buf = self.buf[:n], self.buf[n:]
        return data

    def _read_reply(self):
        line = self._read_line()
        kind, body = line[:1], line[1:]
        if kind == b"+":
            return body.decode()
        if kind == b"-":
            raise RuntimeError(body.decode())
        if kind == b":":
            return int(body)
        if kind == b"$":
            n = int(body)
            if n < 0:
                return None
            return self._read_exact(n + 2)[:-2]
        if kind == b"*":
            n = int(body)
            if n < 0:
                return None
            return [self._read_reply() for _ in range(n)]
        raise RuntimeError(f"unexpected RESP reply: {line!r}")

    def cmd(self, *args: str):
        parts = [f"*{len(args)}\r\n".encode()]
        for arg in args:
            data = arg.encode()
            parts.append(f"${len(data)}\r\n".encode())
            parts.append(data + b"\r\n")
        self.sock.sendall(b"".join(parts))
        return self._read_reply()


def command(port: int, *args: str):
    conn = Resp(port)
    try:
        return conn.cmd(*args)
    finally:
        conn.close()


def info(port: int) -> dict[str, str]:
    raw = command(port, "INFO", "replication")
    if not isinstance(raw, bytes):
        raise RuntimeError(f"INFO replication returned {raw!r}")
    fields = {}
    for line in raw.decode(errors="replace").splitlines():
        if ":" in line and not line.startswith("#"):
            key, value = line.split(":", 1)
            fields[key] = value
    return fields


def ping(port: int) -> bool:
    try:
        return command(port, "PING") == "PONG"
    except (OSError, RuntimeError):
        return False


def wait_until(label: str, fn, timeout: float = 30.0, interval: float = 0.05) -> None:
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        try:
            if fn():
                return
        except (OSError, RuntimeError, ValueError) as exc:
            last = exc
        time.sleep(interval)
    suffix = f" (last error: {last})" if last else ""
    raise RuntimeError(f"timed out waiting for {label}{suffix}")


def port_in_use(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        return sock.connect_ex((HOST, port)) == 0


def start_server(port: int) -> subprocess.Popen:
    if port_in_use(port):
        raise RuntimeError(f"port {port} is already in use")
    proc = subprocess.Popen(
        [str(BIN), "-bind", HOST, "-port", str(port)],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        wait_until(f"Antares on port {port}", lambda: proc.poll() is None and ping(port))
    except RuntimeError:
        if proc.poll() is not None:
            raise RuntimeError(f"Antares on port {port} exited with status {proc.returncode}")
        raise
    return proc


def stop_server(proc: subprocess.Popen | None) -> None:
    if proc is None or proc.poll() is not None:
        return
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait(timeout=5)


def master_offset() -> int:
    return int(info(MASTER_PORT)["master_repl_offset"])


def replica_offset() -> int:
    return int(info(REPLICA_PORT)["slave_repl_offset"])


def wait_link_up() -> None:
    wait_until(
        "replica link",
        lambda: info(REPLICA_PORT).get("master_link_status") == "up",
    )
    wait_until(
        "master to see its replica",
        lambda: int(info(MASTER_PORT).get("connected_slaves", "0")) >= 1,
    )


def wait_caught_up(timeout: float = 30.0) -> float:
    target = master_offset()
    started = time.monotonic()
    wait_until(
        "replica catch-up",
        lambda: replica_offset() >= target,
        timeout=timeout,
        interval=0.01,
    )
    return (time.monotonic() - started) * 1000.0


def flush_and_sync(replica: bool) -> None:
    command(MASTER_PORT, "FLUSHALL")
    if replica:
        wait_caught_up()


def benchmark(pipeline: int) -> tuple[float, float, float]:
    proc = subprocess.run(
        [
            "redis-benchmark",
            "-h", HOST,
            "-p", str(MASTER_PORT),
            "-t", "set",
            "-n", str(REQUESTS),
            "-c", str(CLIENTS),
            "-P", str(pipeline),
            "--csv",
        ],
        capture_output=True,
        text=True,
        timeout=max(120, REQUESTS // 1000),
    )
    if proc.returncode != 0:
        raise RuntimeError(f"redis-benchmark failed: {proc.stderr.strip()}")
    rows = list(csv.DictReader(io.StringIO(proc.stdout)))
    for row in rows:
        if row["test"].strip().upper().startswith("SET"):
            return (
                float(row["rps"]),
                float(row.get("p50_latency_ms", 0.0)),
                float(row.get("p99_latency_ms", 0.0)),
            )
    raise RuntimeError(f"no SET row in redis-benchmark output: {proc.stdout!r}")


def sample_lag(stop: object, samples: list[int]) -> None:
    master = replica = None
    try:
        master = Resp(MASTER_PORT)
        replica = Resp(REPLICA_PORT)
        while not stop.is_set():
            try:
                m = int(master.cmd("INFO", "replication").decode().split("master_repl_offset:")[1].split("\r\n")[0])
                s = int(replica.cmd("INFO", "replication").decode().split("slave_repl_offset:")[1].split("\r\n")[0])
                samples.append(max(0, m - s))
            except (OSError, RuntimeError, ValueError, IndexError):
                master.close()
                replica.close()
                master = Resp(MASTER_PORT)
                replica = Resp(REPLICA_PORT)
            time.sleep(SAMPLE_MS / 1000.0)
    finally:
        if master is not None:
            master.close()
        if replica is not None:
            replica.close()


def percentile(values: list[int], pct: float) -> int:
    if not values:
        return 0
    ordered = sorted(values)
    index = max(0, min(len(ordered) - 1, int(round(pct / 100.0 * (len(ordered) - 1)))))
    return ordered[index]


def variance(values: list[float]) -> float:
    return statistics.stdev(values) if len(values) > 1 else 0.0


def run_condition(with_replica: bool) -> list[dict]:
    results = []
    for pipeline in PIPELINES:
        for run in range(1, RUNS + 1):
            print(f"  P={pipeline} run {run}/{RUNS} ({'with' if with_replica else 'without'} replica)...", flush=True)
            flush_and_sync(with_replica)
            samples: list[int] = []
            stop = None
            sampler = None
            if with_replica:
                stop = threading.Event()
                sampler = threading.Thread(target=sample_lag, args=(stop, samples), daemon=True)
                sampler.start()
            try:
                rps, p50, p99 = benchmark(pipeline)
            finally:
                if stop is not None:
                    stop.set()
                if sampler is not None:
                    sampler.join(timeout=2)
            catchup_ms = 0.0
            if with_replica:
                catchup_ms = wait_caught_up()
                if info(REPLICA_PORT).get("master_link_status") != "up":
                    raise RuntimeError("replica link dropped during benchmark")
            results.append({
                "pipeline": pipeline,
                "run": run,
                "rps": rps,
                "p50_ms": p50,
                "p99_ms": p99,
                "lag_samples": len(samples),
                "samples": samples,
                "lag_median_bytes": int(statistics.median(samples)) if samples else 0,
                "lag_p95_bytes": percentile(samples, 95),
                "lag_max_bytes": max(samples) if samples else 0,
                "catchup_ms": round(catchup_ms, 1),
            })
    return results


def summarize(no_replica: list[dict], with_replica: list[dict]) -> tuple[list[dict], list[dict]]:
    throughput = []
    lag = []
    for pipeline in PIPELINES:
        base = [r for r in no_replica if r["pipeline"] == pipeline]
        repl = [r for r in with_replica if r["pipeline"] == pipeline]
        base_rps = [r["rps"] for r in base]
        repl_rps = [r["rps"] for r in repl]
        base_med = statistics.median(base_rps)
        repl_med = statistics.median(repl_rps)
        overhead = (base_med - repl_med) / base_med * 100.0 if base_med else 0.0
        throughput.append({
            "pipeline": pipeline,
            "command": "SET",
            "without_replica": {
                "median_rps": round(base_med, 1),
                "stdev_rps": round(variance(base_rps), 1),
                "all_rps": [round(v, 1) for v in base_rps],
                "p50_ms": round(statistics.median([r["p50_ms"] for r in base]), 3),
                "p99_ms": round(statistics.median([r["p99_ms"] for r in base]), 3),
            },
            "with_replica": {
                "median_rps": round(repl_med, 1),
                "stdev_rps": round(variance(repl_rps), 1),
                "all_rps": [round(v, 1) for v in repl_rps],
                "p50_ms": round(statistics.median([r["p50_ms"] for r in repl]), 3),
                "p99_ms": round(statistics.median([r["p99_ms"] for r in repl]), 3),
            },
            "overhead_percent": round(overhead, 1),
            "throughput_retained_percent": round(100.0 - overhead, 1),
        })
        gaps = [sample for r in repl for sample in r["samples"]]
        lag.append({
            "pipeline": pipeline,
            "runs": repl,
            "sample_count": len(gaps),
            "max_gap_bytes": max(gaps) if gaps else 0,
            "p95_gap_bytes": percentile(gaps, 95),
            "median_gap_bytes": int(statistics.median(gaps)) if gaps else 0,
            "max_catchup_ms": max((r["catchup_ms"] for r in repl), default=0.0),
        })
    return throughput, lag


def write_claims(payload: dict, commit: str, timestamp: str) -> None:
    claims_path = ROOT / "claims.json"
    try:
        claims = json.loads(claims_path.read_text())
    except (OSError, json.JSONDecodeError):
        claims = {}
    for item in payload["throughput"]:
        p = item["pipeline"]
        for condition in ("without_replica", "with_replica"):
            key = f"bench_repl_set_p{p}_{condition}_rps"
            claims[key] = {
                "value": item[condition]["median_rps"],
                "unit": "rps",
                "source": f"redis-benchmark -t set -P {p} ({condition.replace('_', ' ')})",
                "timestamp": timestamp,
                "commit": commit,
                "variance": item[condition]["stdev_rps"],
            }
        key = f"bench_repl_set_p{p}_overhead_percent"
        claims[key] = {
            "value": item["overhead_percent"],
            "unit": "%",
            "source": f"Antares SET P={p} throughput loss with one replica",
            "timestamp": timestamp,
            "commit": commit,
            "variance": 0.0,
        }
    claims["bench_repl_max_offset_lag_bytes"] = {
        "value": max((item["max_gap_bytes"] for item in payload["lag"]), default=0),
        "unit": "bytes",
        "source": "max master_repl_offset - slave_repl_offset during SET load",
        "timestamp": timestamp,
        "commit": commit,
        "variance": 0.0,
    }
    claims["bench_repl_p95_offset_lag_bytes"] = {
        "value": max((item["p95_gap_bytes"] for item in payload["lag"]), default=0),
        "unit": "bytes",
        "source": "p95 master_repl_offset - slave_repl_offset during SET load",
        "timestamp": timestamp,
        "commit": commit,
        "variance": 0.0,
    }
    claims["bench_repl_max_catchup_ms"] = {
        "value": max((item["max_catchup_ms"] for item in payload["lag"]), default=0.0),
        "unit": "ms",
        "source": "time for the replica to match the master offset after SET load",
        "timestamp": timestamp,
        "commit": commit,
        "variance": 0.0,
    }
    claims_path.write_text(json.dumps(claims, indent=2) + "\n")


def main() -> int:
    if not BIN.exists():
        print(f"error: {BIN} not found; run `make build` first", file=sys.stderr)
        return 1
    if shutil.which("redis-benchmark") is None:
        print("error: redis-benchmark is required in PATH", file=sys.stderr)
        return 1
    if REQUESTS <= 0 or CLIENTS <= 0 or RUNS <= 0 or SAMPLE_MS <= 0 or not PIPELINES:
        print("error: REPL_REQUESTS, REPL_CLIENTS, REPL_RUNS, REPL_SAMPLE_MS, and REPL_PIPELINES must be positive", file=sys.stderr)
        return 1

    commit = subprocess.run(
        ["git", "-C", str(ROOT), "rev-parse", "--short", "HEAD"],
        capture_output=True, text=True,
    ).stdout.strip() or "dev"
    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    platform = f"{os.uname().sysname.lower()}/{os.uname().machine}"
    go_version = subprocess.run(["go", "env", "GOVERSION"], capture_output=True, text=True).stdout.strip()

    master = replica = None
    try:
        print(f"starting Antares master on {HOST}:{MASTER_PORT}...", flush=True)
        master = start_server(MASTER_PORT)
        print(f"running baseline: {RUNS} run(s) without a replica...", flush=True)
        no_replica = run_condition(False)

        print(f"starting Antares replica on {HOST}:{REPLICA_PORT}...", flush=True)
        replica = start_server(REPLICA_PORT)
        command(REPLICA_PORT, "REPLICAOF", HOST, str(MASTER_PORT))
        wait_link_up()
        wait_caught_up()
        print(f"running loaded runs: {RUNS} run(s) with one replica...", flush=True)
        with_replica = run_condition(True)

        throughput, lag = summarize(no_replica, with_replica)
        payload = {
            "commit": commit,
            "timestamp": timestamp,
            "platform": platform,
            "go_version": go_version,
            "requests_per_run": REQUESTS,
            "clients": CLIENTS,
            "runs_count": RUNS,
            "pipelines": PIPELINES,
            "sample_interval_ms": SAMPLE_MS,
            "throughput": throughput,
            "lag": lag,
        }

        print("\n### Replication Throughput (median of runs)")
        print("| Pipeline | No replica (rps) | With replica (rps) | Retained | Overhead | No-replica stdev | With-replica stdev |")
        print("| :--- | :---: | :---: | :---: | :---: | :---: | :---: |")
        for item in throughput:
            print(
                f"| P={item['pipeline']} | **{item['without_replica']['median_rps']:,.0f}** | "
                f"**{item['with_replica']['median_rps']:,.0f}** | "
                f"{item['throughput_retained_percent']:.1f}% | {item['overhead_percent']:.1f}% | "
                f"{item['without_replica']['stdev_rps']:,.0f} | {item['with_replica']['stdev_rps']:,.0f} |"
            )

        print("\n### Replication Lag Under Load (SET)")
        print("| Pipeline | Samples | Median gap | p95 gap | Max gap | Max catch-up |")
        print("| :--- | :---: | :---: | :---: | :---: | :---: |")
        for item in lag:
            print(
                f"| P={item['pipeline']} | {item['sample_count']} | {item['median_gap_bytes']:,} B | "
                f"{item['p95_gap_bytes']:,} B | **{item['max_gap_bytes']:,} B** | {item['max_catchup_ms']:.1f} ms |"
            )

        RESULT_DIR.mkdir(parents=True, exist_ok=True)
        suffix = "" if REQUESTS == 500000 and RUNS == 3 else f"-n{REQUESTS}-r{RUNS}"
        result_path = RESULT_DIR / f"{commit}-repl{suffix}.json"
        result_path.write_text(json.dumps(payload, indent=2) + "\n")
        print(f"\nSaved raw replication metrics to: {result_path}")
        if WRITE_CLAIMS:
            write_claims(payload, commit, timestamp)
            print(f"Updated claims file: {ROOT / 'claims.json'}")
        return 0
    finally:
        stop_server(replica)
        stop_server(master)


if __name__ == "__main__":
    sys.exit(main())
