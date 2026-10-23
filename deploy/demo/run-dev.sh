#!/usr/bin/env bash
# Run the M1 dev console stack locally: Antares + Observatory bridge.
# The bridge serves web/dist if it was built; otherwise it serves a pointer page.
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

antares_pid=""
if (exec 3<>"/dev/tcp/127.0.0.1/$antares_port") 2>/dev/null; then
	exec 3>&-
	echo "Antares is already running on port $antares_port"
else
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

cleanup() {
	[[ -n "$observatory_pid" ]] && kill "$observatory_pid" 2>/dev/null || true
	[[ -n "$antares_pid" ]] && kill "$antares_pid" 2>/dev/null || true
	wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

./bin/observatory \
	-addr "127.0.0.1:$observatory_port" \
	-antares "127.0.0.1:$antares_port" \
	-redis "$redis_addr" \
	-web web/dist &
observatory_pid=$!

echo "Observatory: http://127.0.0.1:$observatory_port  (Antares 127.0.0.1:$antares_port)"
wait "$observatory_pid"
