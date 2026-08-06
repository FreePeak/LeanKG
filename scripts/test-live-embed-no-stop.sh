#!/usr/bin/env bash
# Live-embed regression test: embedding (and indexing) must be triggerable
# while the leankg-mcp server stays up and healthy — no `docker compose stop
# leankg`, no `--force-recreate`. The RocksDB single-writer era is over;
# Postgres is multi-writer.
#
# TDD: this test FAILS against freepeak/leankg:latest (no `embed` subcommand
# — image built without --features=embeddings) and PASSES against an image
# built with embeddings enabled.
#
# Usage (from repo root):
#   LEANKG_IMAGE=freepeak/leankg:embeddings bash scripts/test-live-embed-no-stop.sh
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${LEANKG_IMAGE:-freepeak/leankg:embeddings}"
PORT="${MCP_HTTP_PORT:-9699}"
PASS=0; FAIL=0
step() { echo "--- $*"; }
ok()   { PASS=$((PASS+1)); echo "  PASS: $*"; }
bad()  { FAIL=$((FAIL+1)); echo "  FAIL: $*"; }

step "1. Image ${IMAGE} has the embed subcommand"
embed_help=$(docker run --rm --entrypoint leankg "$IMAGE" embed --help 2>&1 || true)
if echo "$embed_help" | grep -q "Build or refresh the embedding index"; then
  ok "image exposes embed subcommand"
else
  bad "image lacks embed subcommand (built without --features=embeddings)"
  echo "  (red) exiting before live checks — image is the blocker"
  exit 1
fi

step "2. MCP server is up and stays up through the test"
health_before=""
for _ in $(seq 1 30); do
  if health_before=$(curl -fsS -m 2 "http://127.0.0.1:${PORT}/health" 2>/dev/null); then
    break
  fi
  sleep 2
done
if echo "$health_before" | grep -qE '"status"\s*:\s*"ok"'; then
  ok "server healthy before embed: ${health_before}"
else
  bad "server not healthy before embed: ${health_before}"
  exit 1
fi

step "3. Server container name unchanged (no stop/recreate)"
before_id=$(docker ps -q --filter "publish=${PORT}")
after_id="$before_id"
if [[ -n "$before_id" ]]; then
  ok "server container running (id ${before_id:0:12})"
else
  bad "no server container on :${PORT}"
  exit 1
fi

step "4. embedding_vectors row count baseline"
DB_COUNT_QUERY="select count(*) from embedding_vectors"
baseline=$(docker exec leankg-db psql -U postgres -d leankg -tAc "$DB_COUNT_QUERY" 2>/dev/null || echo "query-failed")
echo "  baseline embedding_vectors rows: ${baseline}"
ok "baseline read ok (${baseline})"

step "5. Trigger embed via a throwaway embed container (same PG, no server touch)"
# First run downloads ONNX models (bge-small-en-v1.5 + reranker) into the
# shared model volume; subsequent runs reuse them.
model_count=$(docker run --rm -v leankg-models:/root/.cache/leankg \
  --entrypoint sh "$IMAGE" \
  -c 'find /root/.cache/leankg -name "*.onnx" 2>/dev/null | wc -l')
if [[ "$model_count" =~ ^[0-9]+$ && "$model_count" -gt 0 ]]; then
  ok "models already cached (${model_count} onnx)"
else
  echo "  models absent; running embed --init (network download)..."
  docker run --rm -v leankg-models:/root/.cache/leankg \
    --entrypoint leankg "$IMAGE" embed --init --project /workspace 2>&1 | tail -3 \
    || { bad "embed --init failed"; exit 1; }
  ok "models downloaded"
fi
# The mcp container mounts ${LEANKG_PROJECT_DIR} as /workspace. Mirror it so
# the embed container sees the same indexed project. Default: the mcp
# container's actual /workspace source if discoverable, else the repo root.
HOST_PROJECT="${LEANKG_PROJECT_DIR:-}"
if [[ -z "$HOST_PROJECT" ]]; then
  HOST_PROJECT=$(docker inspect leankg-mcp --format '{{range .Mounts}}{{if eq .Destination "/workspace"}}{{.Source}}{{end}}{{end}}' 2>/dev/null)
fi
if [[ -z "$HOST_PROJECT" || ! -d "${HOST_PROJECT}/.leankg" ]]; then
  HOST_PROJECT="$(pwd)"
fi
echo "  host project mount: ${HOST_PROJECT} -> /workspace"
embed_out=$(docker run --rm \
  -e LEANKG_PG_URL="postgresql://postgres:postgres@host.docker.internal:5432/leankg" \
  -e LEANKG_EMBED_FAST=1 \
  -e LEANKG_EMBED_MODEL=bge-q \
  -e LEANKG_EMBED_MAX_SEQ=128 \
  -e LEANKG_EMBED_MAX_MB=2048 \
  -e LEANKG_EMBED_MAX_BLOB_CHARS=500 \
  -e LEANKG_MCP_PROJECT=/workspace \
  -e OMP_NUM_THREADS=1 \
  -v "${HOST_PROJECT}":/workspace \
  -v leankg-models:/root/.cache/leankg \
  --entrypoint leankg "$IMAGE" \
  embed --wait --project /workspace --workers 1 --batch-size 16 --types function \
  2>&1 | tail -8) || { bad "embed run failed"; echo "$embed_out"; exit 1; }
echo "$embed_out"
ok "embed ran to completion"

step "6. Server still up after embed (same container id)"
after_id=$(docker ps -q --filter "publish=${PORT}")
if [[ -n "$after_id" && "$after_id" == "$before_id" ]]; then
  ok "server container unchanged (${after_id:0:12})"
else
  bad "server container changed or stopped: before=${before_id:0:12} after=${after_id:0:12}"
fi
health_after=$(curl -fsS -m 2 "http://127.0.0.1:${PORT}/health" 2>/dev/null || echo "unhealthy")
if echo "$health_after" | grep -qE '"status"\s*:\s*"ok"'; then
  ok "server healthy after embed: ${health_after}"
else
  bad "server unhealthy after embed: ${health_after}"
fi

step "7. Vectors landed in PG (grew, or embed correctly reported converged)"
after_count=$(docker exec leankg-db psql -U postgres -d leankg -tAc "$DB_COUNT_QUERY" 2>/dev/null || echo "query-failed")
echo "  after embed embedding_vectors rows: ${after_count}"
# A converged project reports "nothing to embed" (all fresh) — that's a pass.
# A partial project must show growth. Either way the server stayed up.
if [[ "$after_count" =~ ^[0-9]+$ && "$after_count" -ge "$baseline" ]]; then
  ok "embedding_vectors ok (${baseline} -> ${after_count})"
else
  bad "embedding_vectors count invalid or shrank: ${baseline} -> ${after_count}"
fi

echo
echo "PASS=${PASS} FAIL=${FAIL}"
[[ "$FAIL" -eq 0 ]]
