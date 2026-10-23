#!/usr/bin/env bash
# run-bench.sh — benchmark Antares vs reference Redis 7.4.11 (PLAN §10, ADR-018)
#
# Runs redis-benchmark across workloads (SET, GET, INCR, MSET) at P=1 and P=16
# for 3 iterations, computing median throughput (rps), latencies (p50, p99),
# and throughput ratio vs real Redis.
#
# Output:
#   1. Formatted markdown summary
#   2. JSON payload saved to bench/results/<commit>.json (and claims.json if requested)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

REQUESTS="${BENCH_REQUESTS:-20000}"
CLIENTS="${BENCH_CLIENTS:-50}"
RUNS="${BENCH_RUNS:-3}"
WORKLOADS="${BENCH_WORKLOADS:-ping_inline,ping_mbulk,set,get,incr,mset,lpush,rpush,lpop,rpop,sadd,hset,spop}"
PIPELINES="${BENCH_PIPELINES:-1 16}"
WRITE_CLAIMS="${WRITE_CLAIMS:-0}"
OUTPUT_DIR="${REPO_ROOT}/bench/results"
ANTARES_PORT="${ANTARES_PORT:-6382}"
REDIS_PORT="${REDIS_PORT:-6379}"
ANTARES_HOST="127.0.0.1"
REDIS_HOST="127.0.0.1"

mkdir -p "${OUTPUT_DIR}"

if ! command -v redis-benchmark >/dev/null 2>&1; then
  echo "error: redis-benchmark is required in PATH" >&2
  exit 1
fi

COMMIT="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo "dev")"
TIMESTAMP="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
GO_VERSION="$(go env GOVERSION)"
OS_ARCH="$(go env GOOS)/$(go env GOARCH)"

# Ensure reference Redis is healthy
if ! redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" PING >/dev/null 2>&1; then
  echo "starting reference Redis via docker compose..."
  (cd "${REPO_ROOT}" && docker compose up -d --wait redis-ref)
fi

# Build antares binary
echo "building Antares server..."
(cd "${REPO_ROOT}" && make build)

# Start Antares server instance
echo "starting Antares on port ${ANTARES_PORT}..."
ANTARES_PID_FILE="$(mktemp /tmp/antares-bench.XXXXXX.pid)"
"${REPO_ROOT}/bin/antares" -port "${ANTARES_PORT}" >/dev/null 2>&1 &
ANTARES_PID=$!
echo "${ANTARES_PID}" > "${ANTARES_PID_FILE}"

cleanup() {
  if [ -n "${ANTARES_PID:-}" ] && kill -0 "${ANTARES_PID}" 2>/dev/null; then
    kill "${ANTARES_PID}" 2>/dev/null || true
    wait "${ANTARES_PID}" 2>/dev/null || true
  fi
  rm -f "${ANTARES_PID_FILE:-}"
}
trap cleanup EXIT

# Wait for Antares to accept connections
for i in {1..30}; do
  if redis-cli -h "${ANTARES_HOST}" -p "${ANTARES_PORT}" PING >/dev/null 2>&1; then
    break
  fi
  sleep 0.1
done

if ! redis-cli -h "${ANTARES_HOST}" -p "${ANTARES_PORT}" PING >/dev/null 2>&1; then
  echo "error: Antares failed to start on port ${ANTARES_PORT}" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d /tmp/antares-bench-run.XXXXXX)"
trap 'cleanup; rm -rf "${TMP_DIR}"' EXIT

echo "=================================================================="
echo " Antares Benchmark Suite (PLAN §10)"
echo " Workloads: ${WORKLOADS} | Pipelines: ${PIPELINES} | Runs: ${RUNS}"
echo " Requests/run: ${REQUESTS} | Clients: ${CLIENTS}"
echo " Date: ${TIMESTAMP} | Commit: ${COMMIT} | Platform: ${OS_ARCH} (${GO_VERSION})"
echo "=================================================================="

# Function to run benchmark and output CSV
run_target_bench() {
  local target_name="$1"
  local host="$2"
  local port="$3"
  local pipe="$4"
  local run_idx="$5"
  local out_file="$6"

  # Flush before run for consistency
  redis-cli -h "${host}" -p "${port}" FLUSHDB >/dev/null 2>&1 || true

  redis-benchmark -h "${host}" -p "${port}" \
    -t "${WORKLOADS}" \
    -n "${REQUESTS}" \
    -c "${CLIENTS}" \
    -P "${pipe}" \
    --csv > "${out_file}" 2>/dev/null
}

# Run runs and collect CSVs
for pipe in ${PIPELINES}; do
  for r in $(seq 1 "${RUNS}"); do
    echo "Running iteration ${r}/${RUNS} (P=${pipe})..."
    run_target_bench "redis" "${REDIS_HOST}" "${REDIS_PORT}" "${pipe}" "${r}" "${TMP_DIR}/redis_p${pipe}_r${r}.csv"
    run_target_bench "antares" "${ANTARES_HOST}" "${ANTARES_PORT}" "${pipe}" "${r}" "${TMP_DIR}/antares_p${pipe}_r${r}.csv"
  done
done

# Analyze results with python
ANALYZER_SCRIPT='
import sys, os, csv, json, statistics

tmp_dir = sys.argv[1]
runs = int(sys.argv[2])
pipelines = [int(p) for p in sys.argv[3].split()]
workloads_req = sys.argv[4].split(",")
commit = sys.argv[5]
timestamp = sys.argv[6]
os_arch = sys.argv[7]
go_ver = sys.argv[8]
reqs = int(sys.argv[9])
clients = int(sys.argv[10])

def parse_csv(path):
    # returns dict of test_name -> {rps, p50, p99}
    data = {}
    if not os.path.exists(path):
        return data
    with open(path, "r", encoding="utf-8") as f:
        reader = csv.DictReader(f)
        for row in reader:
            raw_test = row["test"].strip()
            # Normalize e.g. "MSET (10 keys)" -> "MSET"
            test_key = raw_test.split()[0].upper()
            try:
                data[test_key] = {
                    "rps": float(row["rps"]),
                    "p50_ms": float(row.get("p50_latency_ms", 0.0)),
                    "p99_ms": float(row.get("p99_latency_ms", 0.0)),
                }
            except (ValueError, KeyError):
                continue
    return data

results = {
    "commit": commit,
    "timestamp": timestamp,
    "platform": os_arch,
    "go_version": go_ver,
    "requests_per_run": reqs,
    "clients": clients,
    "runs_count": runs,
    "benchmarks": []
}

summary_table = []

# Collect all command names present across files preserving order
command_order = []
for pipe in pipelines:
    for r in range(1, runs + 1):
        for prefix in ("antares", "redis"):
            fpath = os.path.join(tmp_dir, f"{prefix}_p{pipe}_r{r}.csv")
            for k in parse_csv(fpath).keys():
                if k not in command_order:
                    command_order.append(k)

for pipe in pipelines:
    for cmd in command_order:
        # Collect runs
        antares_rps_list = []
        antares_p50_list = []
        antares_p99_list = []
        redis_rps_list = []
        redis_p50_list = []
        redis_p99_list = []

        for r in range(1, runs + 1):
            a_file = os.path.join(tmp_dir, f"antares_p{pipe}_r{r}.csv")
            r_file = os.path.join(tmp_dir, f"redis_p{pipe}_r{r}.csv")
            a_data = parse_csv(a_file)
            r_data = parse_csv(r_file)

            if cmd in a_data:
                antares_rps_list.append(a_data[cmd]["rps"])
                antares_p50_list.append(a_data[cmd]["p50_ms"])
                antares_p99_list.append(a_data[cmd]["p99_ms"])
            if cmd in r_data:
                redis_rps_list.append(r_data[cmd]["rps"])
                redis_p50_list.append(r_data[cmd]["p50_ms"])
                redis_p99_list.append(r_data[cmd]["p99_ms"])

        if not antares_rps_list or not redis_rps_list:
            continue

        a_rps_med = statistics.median(antares_rps_list)
        a_p50_med = statistics.median(antares_p50_list)
        a_p99_med = statistics.median(antares_p99_list)

        r_rps_med = statistics.median(redis_rps_list)
        r_p50_med = statistics.median(redis_p50_list)
        r_p99_med = statistics.median(redis_p99_list)

        ratio = (a_rps_med / r_rps_med * 100.0) if r_rps_med > 0 else 0.0

        item = {
            "pipeline": pipe,
            "command": cmd,
            "antares": {
                "median_rps": round(a_rps_med, 1),
                "p50_ms": round(a_p50_med, 3),
                "p99_ms": round(a_p99_med, 3),
                "all_rps": [round(x, 1) for x in antares_rps_list]
            },
            "redis_ref": {
                "median_rps": round(r_rps_med, 1),
                "p50_ms": round(r_p50_med, 3),
                "p99_ms": round(r_p99_med, 3),
                "all_rps": [round(x, 1) for x in redis_rps_list]
            },
            "ratio_percent": round(ratio, 1)
        }
        results["benchmarks"].append(item)
        summary_table.append((f"P={pipe}", cmd, f"{a_rps_med:,.0f}", f"{r_rps_med:,.0f}", f"{ratio:.1f}%", f"{a_p50_med:.2f}ms", f"{a_p99_med:.2f}ms"))

# Print table
print("\n### Benchmark Results (Median of 3 runs)")
print("| Pipeline | Command | Antares (rps) | Redis 7.4 (rps) | Ratio (% of Redis) | Antares p50 | Antares p99 |")
print("| :--- | :--- | :---: | :---: | :---: | :---: | :---: |")
for row in summary_table:
    print(f"| {row[0]} | {row[1]} | **{row[2]}** | {row[3]} | **{row[4]}** | {row[5]} | {row[6]} |")

# Check targets for core commands
for b in results["benchmarks"]:
    p = b["pipeline"]
    cmd = b["command"]
    rps = b["antares"]["median_rps"]
    ratio = b["ratio_percent"]

    # Target criteria:
    # P=1: 60-100k or >=50% Redis
    # P=16: 400-800k or >=50% Redis
    if cmd in ("SET", "GET", "INCR", "MSET") and ratio < 50.0 and rps < (50000 if p == 1 else 300000):
        print(f"WARNING: {cmd} (P={p}) below 50% target ratio ({ratio}%)", file=sys.stderr)

out_json = json.dumps(results, indent=2)
results_path = os.path.join(sys.argv[11], f"{commit}.json")
with open(results_path, "w", encoding="utf-8") as f:
    f.write(out_json)
print(f"\nSaved raw benchmark metrics to: {results_path}")

if sys.argv[12] == "1":
    claims_path = os.path.join(sys.argv[13], "claims.json")
    # Convert benchmark data to claims schema: { key: { value, command, timestamp, commit, variance } }
    claims_data = {}
    if os.path.exists(claims_path):
        try:
            with open(claims_path, "r", encoding="utf-8") as cf:
                claims_data = json.load(cf)
        except Exception:
            claims_data = {}

    for b in results["benchmarks"]:
        p = b["pipeline"]
        cmd = b["command"].lower()
        key_rps = f"bench_{cmd}_p{p}_rps"
        key_ratio = f"bench_{cmd}_p{p}_ratio"
        claims_data[key_rps] = {
            "value": b["antares"]["median_rps"],
            "unit": "rps",
            "source": f"redis-benchmark -t {cmd} -P {p}",
            "timestamp": timestamp,
            "commit": commit,
            "variance": round(statistics.stdev(b["antares"]["all_rps"]) if len(b["antares"]["all_rps"]) > 1 else 0.0, 1)
        }
        claims_data[key_ratio] = {
            "value": b["ratio_percent"],
            "unit": "%",
            "source": f"Antares vs Redis 7.4.11 ({cmd.upper()} P={p})",
            "timestamp": timestamp,
            "commit": commit,
            "variance": 0.0
        }

    with open(claims_path, "w", encoding="utf-8") as cf:
        json.dump(claims_data, cf, indent=2)
    print(f"Updated claims file: {claims_path}")
'

python3 -c "${ANALYZER_SCRIPT}" \
  "${TMP_DIR}" \
  "${RUNS}" \
  "${PIPELINES}" \
  "${WORKLOADS}" \
  "${COMMIT}" \
  "${TIMESTAMP}" \
  "${OS_ARCH}" \
  "${GO_VERSION}" \
  "${REQUESTS}" \
  "${CLIENTS}" \
  "${OUTPUT_DIR}" \
  "${WRITE_CLAIMS}" \
  "${REPO_ROOT}"
