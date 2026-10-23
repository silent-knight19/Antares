#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

antares_port=${ANTARES_PORT:-6380}
observatory_port=${OBS_PORT:-6390}
redis_addr=${REDIS_ADDR:-}

if [[ -z "$redis_addr" ]] && (exec 3<>"/dev/tcp/127.0.0.1/6379") 2>/dev/null; then
  exec 3>&-
  redis_addr="127.0.0.1:6379"
fi

echo "==> Checking web bundle budget..."
./web/scripts/check-budget.sh

echo "==> Checking if Antares is running on port $antares_port..."
antares_pid=""
if (exec 3<>"/dev/tcp/127.0.0.1/$antares_port") 2>/dev/null; then
  exec 3>&-
  echo "==> Antares already running on $antares_port"
else
  echo "==> Starting local Antares on $antares_port..."
  ./bin/antares -port "$antares_port" &
  antares_pid=$!
  for _ in $(seq 1 50); do
    if (exec 3<>"/dev/tcp/127.0.0.1/$antares_port") 2>/dev/null; then
      exec 3>&-
      break
    fi
    sleep 0.1
  done
fi

observatory_pid=""
if curl -s -f "http://127.0.0.1:$observatory_port/api/health" >/dev/null 2>&1; then
  echo "==> Observatory already running on $observatory_port"
else
  echo "==> Starting local Observatory on $observatory_port..."
  ./bin/observatory \
    -addr "127.0.0.1:$observatory_port" \
    -antares "127.0.0.1:$antares_port" \
    -redis "$redis_addr" \
    -web web/dist &
  observatory_pid=$!
  for _ in $(seq 1 50); do
    if curl -s -f "http://127.0.0.1:$observatory_port/api/health" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
fi

cleanup() {
  echo "==> Cleaning up spawned test services..."
  [[ -n "$observatory_pid" ]] && kill "$observatory_pid" 2>/dev/null || true
  [[ -n "$antares_pid" ]] && kill "$antares_pid" 2>/dev/null || true
  wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "==> Seeding initial keys..."
./deploy/demo/seed.sh "$antares_port" || true

echo "==> Running Playwright E2E test suite..."
cd web
OBSERVATORY_URL="http://127.0.0.1:$observatory_port" npx playwright test

echo "==> Playwright E2E tests completed successfully!"
