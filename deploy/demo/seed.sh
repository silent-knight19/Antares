#!/usr/bin/env bash
# Seed a dev keyspace for the Observatory Keyspace view (U6a).
# Usage: deploy/demo/seed.sh [port]   (default 6380)
set -euo pipefail

port=${1:-${PORT:-6380}}
host=${HOST:-127.0.0.1}
count=${COUNT:-10000}
ttl=${TTL_MS:-3600000}

if ! command -v redis-cli >/dev/null 2>&1; then
	echo "seed: redis-cli is required (it speaks the same RESP wire)" >&2
	exit 1
fi

resp_set() {
	local key=$1 value=$2
	printf '*3\r\n$3\r\nSET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n' "${#key}" "$key" "${#value}" "$value"
}

resp_set_ttl() {
	local key=$1 value=$2 ttl=$3
	printf '*5\r\n$3\r\nSET\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n$2\r\nPX\r\n$%d\r\n%s\r\n' \
		"${#key}" "$key" "${#value}" "$value" "${#ttl}" "$ttl"
}

resp_list() {
	local key=$1
	printf '*4\r\n$5\r\nRPUSH\r\n$%d\r\n%s\r\n$1\r\na\r\n$1\r\nb\r\n' "${#key}" "$key"
}

resp_hash() {
	local key=$1
	printf '*4\r\n$4\r\nHSET\r\n$%d\r\n%s\r\n$2\r\nid\r\n$%d\r\n%s\r\n' "${#key}" "$key" "${#key}" "$key"
}

resp_set_member() {
	local key=$1
	printf '*3\r\n$4\r\nSADD\r\n$%d\r\n%s\r\n$2\r\ngo\r\n' "${#key}" "$key"
}

{
	for i in $(seq 1 "$count"); do
		key="user:$i"
		if (( i % 10 == 0 )); then
			resp_set_ttl "$key" "value-$i" "$ttl"
		else
			resp_set "$key" "value-$i"
		fi
		if (( i <= 500 )); then
			resp_list "queue:$i"
			resp_hash "session:$i"
			resp_set_member "tags:$i"
		fi
	done
} | redis-cli -h "$host" -p "$port" --pipe

echo "seeded ~$count keys on $host:$port"
