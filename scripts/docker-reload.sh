#!/usr/bin/env bash
# Reload LeanKG Docker container from Hub image — no local rebuild.
# Pulls the configured image tag and recreates the container.
# Keeps RocksDB / model volumes intact.
#
# Usage:
#   LEANKG_IMAGE=freepeak/leankg:latest scripts/docker-reload.sh
#   LEANKG_IMAGE=freepeak/leankg:0.19.4 scripts/docker-reload.sh
#
# Env overrides (also in .dockerfile / docker-compose.override.yml):
#   LEANKG_IMAGE        Hub image tag (default: freepeak/leankg:latest)
#   LEANKG_PULL_POLICY  Compose pull_policy (default: always — force fresh pull)
#   MCP_HTTP_PORT       Host MCP port (default: 9699)
#   LEANKG_SERVE_PORT   Host REST/UI port (default: 8080)

set -euo pipefail

IMAGE="${LEANKG_IMAGE:-freepeak/leankg:latest}"
PULL_POLICY="${LEANKG_PULL_POLICY:-always}"
PORT="${MCP_HTTP_PORT:-9699}"
SERVE_PORT="${LEANKG_SERVE_PORT:-8080}"
SERVE_HTTP="${LEANKG_SERVE_HTTP:-1}"
NAME="leankg"

echo "=== LeanKG Docker Reload (no rebuild) ==="
echo "  image:          $IMAGE"
echo "  pull_policy:    $PULL_POLICY"
echo "  mcp port:       $PORT"
if [[ "$SERVE_HTTP" == "1" || "$SERVE_HTTP" == "true" ]]; then
  echo "  serve port:     $SERVE_PORT"
fi
echo ""

# Compose files to merge
COMPOSE_BASE="docker-compose.rocksdb.yml"
COMPOSE_OVERRIDE="docker-compose.override.yml"
ENV_FILE=".dockerfile"

# Resolve full paths (script may be run from anywhere)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

if [[ ! -f "$COMPOSE_BASE" ]]; then
  echo "ERROR: $COMPOSE_BASE not found in $REPO_ROOT" >&2
  exit 1
fi

# Verify Hub image exists (pull)
echo "=== Pulling image: $IMAGE ==="
docker pull "$IMAGE"

# Build compose command
COMPOSE_CMD=(docker compose -f "$COMPOSE_BASE")
if [[ -f "$COMPOSE_OVERRIDE" ]]; then
  COMPOSE_CMD+=(-f "$COMPOSE_OVERRIDE")
fi
if [[ -f "$ENV_FILE" ]]; then
  COMPOSE_CMD+=(--env-file "$ENV_FILE")
fi

# Override pull_policy for this run to force a fresh pull
COMPOSE_CMD+=(-p leankg up -d --no-build --force-recreate)
# Note: pull_policy is set via env LEANKG_PULL_POLICY in .dockerfile / env
# The main compose file uses ${LEANKG_PULL_POLICY:-missing}, so we pass it via env

export LEANKG_IMAGE="$IMAGE"
export LEANKG_PULL_POLICY="$PULL_POLICY"

echo "=== Recreating container (no build) ==="
"${COMPOSE_CMD[@]}"

echo "=== Waiting for health on :$PORT ==="
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
echo "LeanKG MCP reloaded — no rebuild."
echo "  Health:  curl http://127.0.0.1:${PORT}/health"
echo "  MCP URL: http://127.0.0.1:${PORT}/mcp"
if [[ "$SERVE_HTTP" == "1" || "$SERVE_HTTP" == "true" ]]; then
  echo "  REST UI: http://127.0.0.1:${SERVE_PORT}/  (ui-v2 proxies /api here)"
fi
echo "  Stop:    docker rm -f ${NAME}"