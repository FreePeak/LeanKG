#!/usr/bin/env bash
# One-command Docker setup: index → offline INT8 embed → MCP HTTP.
# No Rust / cargo required — only Docker (or OrbStack).
#
#   curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/docker-up.sh | bash
#
# Or from a clone:  bash scripts/docker-up.sh
#
# Env overrides:
#   LEANKG_HOST_DIR       host project to mount (default: $PWD)
#   LEANKG_IMAGE          image tag (default: freepeak/leankg:latest)
#   LEANKG_CONTAINER_NAME container name (default: leankg)
#   MCP_HTTP_PORT         host MCP port (default: 9699)
#   LEANKG_SERVE_PORT     host REST port for UI v2 (default: 8080; set LEANKG_SERVE_HTTP=0 to skip)
#   LEANKG_MCP_MEMORY     MCP container memory limit (default: 2g — Local survival)
#   LEANKG_EMBED_MEMORY   embed job memory limit (default: 10g)
#   LEANKG_EMBED_CPUS     embed job CPUs (default: 6)
#   LEANKG_SKIP_EMBED=1   start MCP only (skip cold embed)
set -euo pipefail

IMAGE="${LEANKG_IMAGE:-freepeak/leankg:latest}"
HOST_DIR="${LEANKG_HOST_DIR:-$PWD}"
NAME="${LEANKG_CONTAINER_NAME:-leankg}"
PORT="${MCP_HTTP_PORT:-9699}"
SERVE_PORT="${LEANKG_SERVE_PORT:-8080}"
SERVE_HTTP="${LEANKG_SERVE_HTTP:-1}"
MCP_MEM="${LEANKG_MCP_MEMORY:-2g}"
MEM="${LEANKG_EMBED_MEMORY:-10g}"
CPUS="${LEANKG_EMBED_CPUS:-6}"

if ! command -v docker >/dev/null 2>&1; then
  echo "ERROR: docker not found. Install Docker or OrbStack first." >&2
  exit 1
fi

if [[ ! -d "$HOST_DIR" ]]; then
  echo "ERROR: host directory does not exist: $HOST_DIR" >&2
  exit 1
fi

echo "=== LeanKG Docker setup ==="
echo "  image:     $IMAGE"
echo "  host dir:  $HOST_DIR → /workspace"
echo "  container: $NAME  mcp:$PORT  serve:$SERVE_PORT (LEANKG_SERVE_HTTP=$SERVE_HTTP)"
echo "  embed:     memory=$MEM cpus=$CPUS"
echo "  mcp:       memory=$MCP_MEM (Local 2GB survival)"

echo "=== Pull image ==="
docker pull "$IMAGE"

docker volume create leankg-rocksdb >/dev/null
docker volume create leankg-models >/dev/null

VOLUMES=(
  -v "${HOST_DIR}:/workspace"
  -v leankg-rocksdb:/data/leankg-rocksdb
  -v leankg-models:/root/.cache/leankg
)
ENV_COMMON=(
  -e LEANKG_DB_ENGINE=rocksdb
  -e LEANKG_ROCKSDB_ROOT=/data/leankg-rocksdb
  -e LEANKG_MCP_PROJECT=/workspace
  -e LEANKG_AUTO_INDEX=1
  -e LEANKG_EMBED_ON_BOOT=0
  -e LEANKG_EMBED_BACKGROUND=0
  -e LEANKG_EMBED_FAST=1
  -e LEANKG_EMBED_MODEL=bge-q
  -e LEANKG_EMBED_MAX_SEQ=128
  -e LEANKG_EMBED_MAX_BLOB_CHARS=500
  -e OMP_NUM_THREADS=1
  -e RUST_LOG=leankg=info
)

# Bypass entrypoint so this works on Hub images that always start mcp-http.
# Newer images also accept `docker run … IMAGE embed …` via entrypoint passthrough.
run_leankg() {
  docker run --rm \
    "${VOLUMES[@]}" \
    "${ENV_COMMON[@]}" \
    --entrypoint leankg \
    "$IMAGE" \
    "$@"
}

echo "=== Stop existing MCP container (RocksDB single-writer) ==="
docker rm -f "$NAME" 2>/dev/null || true

echo "=== Index /workspace ==="
run_leankg index /workspace

if [[ "${LEANKG_SKIP_EMBED:-0}" != "1" ]]; then
  echo "=== Download embedding models (first run) ==="
  run_leankg embed --init --project /workspace || \
    echo "WARN: embed --init failed; continuing (cache may already exist)"

  echo "=== Offline INT8 embed --wait ==="
  echo "    Mega-graphs can take 10–40+ minutes."
  docker run --rm \
    --memory="$MEM" --cpus="$CPUS" \
    "${VOLUMES[@]}" \
    "${ENV_COMMON[@]}" \
    -e LEANKG_EMBED_MAX_MB=0 \
    --entrypoint leankg \
    "$IMAGE" \
    embed --wait --project /workspace --workers 8 --batch-size 128 --types function,method
else
  echo "=== Skipping embed (LEANKG_SKIP_EMBED=1) ==="
fi

echo "=== Start MCP HTTP (+ optional REST serve for UI v2) ==="
RUN_PORTS=(-p "${PORT}:9699")
if [[ "$SERVE_HTTP" == "1" || "$SERVE_HTTP" == "true" ]]; then
  RUN_PORTS+=(-p "${SERVE_PORT}:8080")
fi
docker run -d --name "$NAME" \
  "${RUN_PORTS[@]}" \
  --memory="$MCP_MEM" \
  --restart unless-stopped \
  "${VOLUMES[@]}" \
  "${ENV_COMMON[@]}" \
  -e LEANKG_EMBED_MAX_MB=512 \
  -e LEANKG_SERVE_HTTP="$SERVE_HTTP" \
  -e LEANKG_SERVE_PORT=8080 \
  "$IMAGE"

echo "=== Wait for /health ==="
ok=0
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
    echo "healthy"
    ok=1
    break
  fi
  sleep 2
done
if [[ "$ok" -ne 1 ]]; then
  echo "ERROR: health check failed. Logs:" >&2
  docker logs "$NAME" 2>&1 | tail -40 >&2
  exit 1
fi

echo ""
echo "LeanKG MCP ready — no Rust required."
echo "  Health:  curl http://127.0.0.1:${PORT}/health"
echo "  MCP URL: http://127.0.0.1:${PORT}/mcp"
if [[ "$SERVE_HTTP" == "1" || "$SERVE_HTTP" == "true" ]]; then
  echo "  REST UI: http://127.0.0.1:${SERVE_PORT}/  (ui-v2 proxies /api here)"
fi
echo "  Stop:    docker rm -f ${NAME}"
