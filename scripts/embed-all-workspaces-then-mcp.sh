#!/usr/bin/env bash
# Offline INT8 embed for every LEANKG_PROJECT_DIRS entry, then start MCP.
# One-liner from repo root:
#   bash scripts/embed-all-workspaces-then-mcp.sh
set -euo pipefail
cd "$(dirname "$0")/.."

ENV_FILE="${ENV_FILE:-.dockerfile}"
# Always include embed.yml so override can attach side mounts to leankg-embed
# (profile keeps the embed service from starting with `up`).
COMPOSE=(docker compose
  -f docker-compose.rocksdb.yml
  -f docker-compose.override.yml
  -f docker-compose.embed.yml
  --env-file "$ENV_FILE")

# shellcheck disable=SC1090
set -a; source "$ENV_FILE"; set +a
DIRS="${LEANKG_PROJECT_DIRS:-/workspace}"
IFS=',' read -ra PROJECTS <<< "$DIRS"

echo "=== Stop MCP (RocksDB single-writer) ==="
"${COMPOSE[@]}" stop leankg 2>/dev/null || true

echo "=== Ensure ONNX model cache ==="
HOST_CACHE=""
if [[ -d "${HOME}/Library/Caches/leankg/models" ]]; then
  HOST_CACHE="${HOME}/Library/Caches/leankg"
elif [[ -d "${HOME}/.cache/leankg/models" ]]; then
  HOST_CACHE="${HOME}/.cache/leankg"
fi
if [[ -n "$HOST_CACHE" ]]; then
  echo "Seeding model cache from $HOST_CACHE into compose volume..."
  "${COMPOSE[@]}" --profile embed run --rm --entrypoint sh \
    -v "${HOST_CACHE}:/src:ro" \
    leankg-embed \
    -c 'mkdir -p /root/.cache/leankg && cp -a /src/. /root/.cache/leankg/ && ls /root/.cache/leankg/models/models--Xenova--bge-small-en-v1.5/snapshots/*/onnx/model_quantized.onnx'
else
  echo "No host model cache; running embed --init (needs network)..."
  "${COMPOSE[@]}" --profile embed run --rm --entrypoint leankg leankg-embed \
    embed --init --project "${LEANKG_MCP_PROJECT:-/workspace}"
fi

for p in "${PROJECTS[@]}"; do
  p="$(echo "$p" | xargs)"
  [[ -z "$p" ]] && continue
  echo "=== Clear embed lock + offline embed: $p ==="
  LEANKG_MCP_PROJECT="$p" "${COMPOSE[@]}" --profile embed run --rm --entrypoint sh \
    -e "LEANKG_MCP_PROJECT=$p" \
    leankg-embed \
    -c "rm -f '${p}/.leankg/embed.lock' '${p}/.leankg/embed_status.json' 2>/dev/null; true"
  set +e
  LEANKG_MCP_PROJECT="$p" "${COMPOSE[@]}" --profile embed run --rm \
    -e "LEANKG_MCP_PROJECT=$p" \
    leankg-embed
  rc=$?
  set -e
  if [[ "$rc" -ne 0 ]]; then
    echo "ERROR: embed failed for $p (exit $rc)"
    exit "$rc"
  fi
  # Ensure HNSW exists (create if missing) before MCP starts.
  echo "=== HNSW warmup: $p ==="
  LEANKG_MCP_PROJECT="$p" "${COMPOSE[@]}" --profile embed run --rm --entrypoint leankg \
    -e "LEANKG_MCP_PROJECT=$p" \
    leankg-embed \
    semantic-context "warmup" --project "$p" --top-k 1 --no-traverse >/dev/null
done

echo "=== Start MCP HTTP (BACKGROUND must stay 0 — otherwise it drops HNSW) ==="
LEANKG_EMBED_BACKGROUND=0 LEANKG_EMBED_ON_BOOT=0 \
  "${COMPOSE[@]}" up -d --force-recreate leankg

echo "=== Wait for /health ==="
ok=0
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${MCP_HTTP_PORT:-9699}/health" >/dev/null 2>&1; then
    echo "healthy"; ok=1; break
  fi
  sleep 2
done
[[ "$ok" -eq 1 ]] || { echo "health check failed"; docker logs leankg-leankg-1 2>&1 | tail -40; exit 1; }

echo "=== Verify semantic_search (hnsw) per project ==="
fail=0
for p in "${PROJECTS[@]}"; do
  p="$(echo "$p" | xargs)"
  [[ -z "$p" ]] && continue
  body=$(curl -fsS -X POST "http://127.0.0.1:${MCP_HTTP_PORT:-9699}/mcp?project=${p}" \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"semantic_search","arguments":{"query":"authentication handler","limit":2}}}' \
    2>/dev/null || echo 'request_failed')
  echo "--- $p ---"
  if echo "$body" | grep -q 'hnsw+rerank\|method: hnsw'; then
    echo "OK hnsw+rerank"
  else
    echo "$body" | head -c 500
    echo
    fail=1
  fi
done

if [[ "$fail" -ne 0 ]]; then
  echo "VERIFY FAILED — docker logs leankg-leankg-1"
  exit 1
fi
echo "=== All projects semantic_search OK ==="
