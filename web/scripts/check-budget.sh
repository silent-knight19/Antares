#!/usr/bin/env bash
set -euo pipefail

DIST_DIR="${1:-web/dist}"

if [ ! -d "$DIST_DIR/assets" ]; then
  echo "Error: $DIST_DIR/assets not found. Run npm run build first." >&2
  exit 1
fi

TOTAL_GZIP_JS=0
for js in "$DIST_DIR"/assets/*.js; do
  if [ -f "$js" ]; then
    SZ=$(gzip -c "$js" | wc -c | tr -d ' ')
    TOTAL_GZIP_JS=$((TOTAL_GZIP_JS + SZ))
  fi
done

BUDGET=256000 # 250 KB = 256,000 bytes
TOTAL_KB=$((TOTAL_GZIP_JS / 1024))

echo "==> Production JS gzip size: ${TOTAL_GZIP_JS} bytes (~${TOTAL_KB} KB)"
echo "==> Budget limit: ${BUDGET} bytes (~250 KB)"

if [ "$TOTAL_GZIP_JS" -gt "$BUDGET" ]; then
  echo "FAIL: JS bundle exceeds 250 KB budget by $((TOTAL_GZIP_JS - BUDGET)) bytes!" >&2
  exit 1
fi

echo "PASS: JS bundle is within 250 KB budget (${TOTAL_KB} KB / 250 KB)."
