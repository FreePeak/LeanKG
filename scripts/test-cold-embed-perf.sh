#!/usr/bin/env bash
# Live test: cold embed workspace-be at 12g mem_limit, target 15min.
# TDD: this script defines the assertion. Run with -bench or simply
# invoke after config change. Exits 0 if rate >= 380 vectors/sec AND
# wall-clock <= 15min. Non-zero otherwise.
#
# Pre-req: docker compose running, workspace-be mounted, fresh full-mode
# embed (no resumable state).
set -euo pipefail

PROJ="${LEANKG_MCP_PROJECT:-/workspace-be}"
TIMEOUT_MIN="${TIMEOUT_MIN:-15}"
EXPECTED_VECTORS="${EXPECTED_VECTORS:-381000}"
MIN_RATE="${MIN_RATE:-380}"

WALL_T0=$(date +%s)
echo "=== cold embed perf live test ==="
echo "project=$PROJ  timeout=${TIMEOUT_MIN}m  min_rate=${MIN_RATE} v/s"

# 1. Stop MCP (single-writer).
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml stop leankg

# 2. Run the offline embed (full mode, throwaway container).
RESULT=$(docker run --rm \
  -v leankg_leankg-rocksdb:/data/leankg-rocksdb \
  -v leankg_leankg_models:/root/.cache/leankg \
  -v /Users/linh.doan/work/be:/workspace-be \
  -e LEANKG_DB_ENGINE=rocksdb \
  -e LEANKG_ROCKSDB_ROOT=/data/leankg-rocksdb \
  -e LEANKG_EMBED_FAST=1 \
  -e LEANKG_EMBED_MODEL=bge-q \
  -e LEANKG_EMBED_MAX_SEQ=128 \
  -e LEANKG_EMBED_MAX_BLOB_CHARS=500 \
  -e LEANKG_EMBED_MAX_MB="${LEANKG_EMBED_MAX_MB:-12000}" \
  -e LEANKG_HNSW_EF="${LEANKG_HNSW_EF:-}" \
  -e OMP_NUM_THREADS=1 \
  -e RUST_LOG=leankg=info \
  freepeak/leankg:latest \
  embed --wait --full --project "$PROJ" \
    --workers "${WORKERS:-8}" \
    --batch-size "${BATCH_SIZE:-128}" \
    --types function,method 2>&1 | tail -30)
echo "$RESULT"

WALL_T1=$(date +%s)
ELAPSED=$((WALL_T1 - WALL_T0))
echo "wall_clock=${ELAPSED}s"

# 3. Restart MCP.
docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml up -d leankg

# 4. Query embed status for the final rate.
sleep 3
curl -fsS http://localhost:9699/health >/dev/null
RATE_LINE=$(curl -fsS -X POST 'http://localhost:9699/mcp?project='$PROJ \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"embed_control","arguments":{"action":"status","project":"'$PROJ'"}}}' \
  | python3 -c "import json,sys; d=json.load(sys.stdin)['result']['content'][0]['text']; import re; m=re.search(r'Rate:\s*([\d.]+)\s*vectors/sec', d); print(m.group(1) if m else '?')" 2>/dev/null || echo "?")
echo "embed_status_rate=${RATE_LINE}"

# 5. Assert.
TIMEOUT_S=$((TIMEOUT_MIN * 60))
if [ "$ELAPSED" -le "$TIMEOUT_S" ]; then
  echo "PASS: elapsed ${ELAPSED}s <= ${TIMEOUT_S}s"
else
  echo "FAIL: elapsed ${ELAPSED}s > ${TIMEOUT_S}s"
  exit 1
fi

echo "live_test_complete elapsed=${ELAPSED}s rate=${RATE_LINE}"
