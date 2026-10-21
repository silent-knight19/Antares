#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
CACHE=${TCL_CACHE_DIR:-"$ROOT/tests/tcl/.cache"}
SUITE=${1:-expire}
case "$SUITE" in
    expire) SUITE_PATH=tests/unit/expire.tcl; ONLY_FILE=expire_tests.txt ;;
    incr) SUITE_PATH=tests/unit/type/incr.tcl; ONLY_FILE=incr_tests.txt ;;
    *) echo "unknown Tcl suite: $SUITE" >&2; exit 1 ;;
esac
SRC="$CACHE/redis-7.4.11"
URL="https://download.redis.io/releases/redis-7.4.11.tar.gz"
SHA="3c266ece0abd54ed3b1c912c6eb86b7508cf382cb690ee6649d3843f018f6357"

TCLSH=${TCLSH:-tclsh}
command -v "$TCLSH" >/dev/null 2>&1 || { echo "tclsh (8.5+) is required" >&2; exit 1; }

VERIFIED="$CACHE/redis-7.4.11.verified"
TARBALL="$CACHE/redis-7.4.11.tar.gz"
if [ ! -d "$SRC/tests/unit" ] || [ ! -f "$VERIFIED" ]; then
    mkdir -p "$CACHE"
    if [ ! -f "$TARBALL" ]; then
        echo "Downloading Redis 7.4.11 tests..."
        curl -fsSL "$URL" -o "$TARBALL"
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        echo "$SHA  $TARBALL" | sha256sum -c - >/dev/null
    else
        echo "$SHA  $TARBALL" | shasum -a 256 -c - >/dev/null
    fi
    rm -rf "$SRC"
    tar -xzf "$TARBALL" -C "$CACHE"
    : >"$VERIFIED"
fi

TMP=$(mktemp -d)
PID=
cleanup() {
    if [ -n "$PID" ]; then
        kill "$PID" 2>/dev/null || true
        wait "$PID" 2>/dev/null || true
    fi
    rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

PORT=${TCL_PORT:-}
if [ -z "$PORT" ]; then
    PORT=$("$TCLSH" <<'TCL'
for {set p 20000} {$p < 30000} {incr p} {
    if {[catch {socket -server {} -myaddr 127.0.0.1 $p} s]} {continue}
    close $s
    puts $p
    exit 0
}
exit 1
TCL
)
fi

(cd "$ROOT" && go build -o "$TMP/antares" ./cmd/antares)
"$TMP/antares" -port "$PORT" >"$TMP/antares.log" 2>&1 &
PID=$!

ready=
for _ in $(seq 1 50); do
    if "$TCLSH" <<TCL 2>/dev/null
if {[catch {socket 127.0.0.1 $PORT} s]} {exit 1}
close \$s
TCL
    then
        ready=1
        break
    fi
    sleep 0.1
done
if [ -z "$ready" ]; then
    cat "$TMP/antares.log" >&2
    echo "antares did not start on port $PORT" >&2
    exit 1
fi

"$TCLSH" "$ROOT/tests/tcl/harness.tcl" "$SRC" "$PORT" "$ROOT/tests/tcl/$ONLY_FILE" "$SUITE_PATH"
