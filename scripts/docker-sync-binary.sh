#!/usr/bin/env bash
# Build LeanKG Linux binary in a cached Docker builder, then bind-mount it
# onto a Hub runtime image and recreate the MCP container.
# No multi-stage image rebuild — only the binary changes.
#
# Use when you have local unpublished source changes (e.g., bumped to a
# version not yet on Docker Hub) and want a fast reload.
#
# Usage:
#   scripts/docker-sync-binary.sh
#
# Env overrides:
#   LEANKG_IMAGE        Hub runtime base (default: freepeak/leankg:latest)
#   LEANKG_BUILDER      Build image (default: rust:1-bookworm)
#   LEANKG_BUILD_JOBS   Parallel cargo jobs (default: 4 — safe for Docker Desktop)
#   LEANKG_BINARY_HOST  Host-side path for the built binary (default: target/release-linux/leankg)
#   LEANKG_SKIP_BUILD   Set to 1 to reuse existing binary at LEANKG_BINARY_HOST

set -euo pipefail

IMAGE="${LEANKG_IMAGE:-freepeak/leankg:latest}"
BUILDER="${LEANKG_BUILDER:-rust:1-bookworm}"
BUILD_JOBS="${LEANKG_BUILD_JOBS:-4}"
BINARY_HOST="${LEANKG_BINARY_HOST:-target/release-linux/leankg}"
SKIP_BUILD="${LEANKG_SKIP_BUILD:-0}"
PORT="${MCP_HTTP_PORT:-9699}"
SERVE_PORT="${LEANKG_SERVE_PORT:-8080}"
SERVE_HTTP="${LEANKG_SERVE_HTTP:-1}"
NAME="leankg"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

echo "=== LeanKG Binary Sync (no image rebuild) ==="
echo "  runtime image:  $IMAGE"
echo "  binary host:    $BINARY_HOST"
echo "  builder:        $BUILDER"
echo ""

BINARY_DIR="$(dirname "$BINARY_HOST")"
BINARY_FILE="$(basename "$BINARY_HOST")"

# ========== Step 1: Build the Linux binary ==========
if [[ "$SKIP_BUILD" == "1" ]]; then
  if [[ ! -f "$BINARY_HOST" ]]; then
    echo "ERROR: LEANKG_SKIP_BUILD=1 but $BINARY_HOST not found." >&2
    exit 1
  fi
  echo "=== Skipping build, reusing: $BINARY_HOST ==="
else
  echo "=== Building Linux binary (cargo, incremental) ==="
  mkdir -p "$BINARY_DIR"

  # One-shot builder with cached registry + incremental target.
  # Registry volume avoids re-downloading crates; target volume keeps
  # incremental build artifacts so only changed source files recompile.
  docker run --rm \
    --platform linux/arm64 \
    -v "$REPO_ROOT:/app" \
    -v cargo-registry:/root/.cargo/registry \
    -v cargo-target-linux:/app/target \
    -w /app \
    -e CARGO_BUILD_JOBS="$BUILD_JOBS" \
    -e CARGO_PROFILE_RELEASE_LTO=false \
    -e CARGO_TERM_COLOR=always \
    "$BUILDER" \
    bash -c "
      apt-get update -qq && apt-get install -y -qq clang libclang-dev && rm -rf /var/lib/apt/lists/* && \
      cargo build --release --features embeddings && \
      strip target/release/leankg
    "

  # Copy the Linux binary to the host so we can bind-mount it
  docker run --rm \
    --platform linux/arm64 \
    -v cargo-target-linux:/app/target \
    -v "$(realpath "$BINARY_DIR"):/out" \
    --entrypoint cp \
    "$BUILDER" \
    /app/target/release/leankg "/out/$BINARY_FILE"

  echo "  Built: $BINARY_HOST"
fi

file "$BINARY_HOST" 2>/dev/null | head -1

# ========== Step 2: Recreate MCP container with binary bind ==========
echo ""
echo "=== Recreating MCP with bind-mounted binary ==="

COMPOSE_BASE="docker-compose.rocksdb.yml"
COMPOSE_OVERRIDE="docker-compose.override.yml"
ENV_FILE=".dockerfile"

COMPOSE_CMD=(docker compose -f "$COMPOSE_BASE")
if [[ -f "$COMPOSE_OVERRIDE" ]]; then
  COMPOSE_CMD+=(-f "$COMPOSE_OVERRIDE")
fi
if [[ -f "$ENV_FILE" ]]; then
  COMPOSE_CMD+=(--env-file "$ENV_FILE")
fi

export LEANKG_IMAGE="$IMAGE"

# Merge: use the base + override compose files, but add a temporary
# override for the binary bind-mount. We do this by writing a one-shot
# override fragment that Compose merges last.
BINARY_OVERRIDE="/tmp/leankg-binary-override.yml"
cat > "$BINARY_OVERRIDE" <<YAML
services:
  leankg:
    volumes:
      - $(realpath "$BINARY_HOST"):/usr/local/bin/leankg:ro
YAML

COMPOSE_CMD+=(-f "$BINARY_OVERRIDE")
COMPOSE_CMD+=(-p leankg up -d --no-build --force-recreate)

"${COMPOSE_CMD[@]}"
rm -f "$BINARY_OVERRIDE"

# ========== Step 3: Health check ==========
echo ""
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
echo "LeanKG MCP running with bind-mounted local binary."
echo "  binary:   $(realpath "$BINARY_HOST") → /usr/local/bin/leankg:ro"
echo "  Health:   curl http://127.0.0.1:${PORT}/health"
echo "  MCP URL:  http://127.0.0.1:${PORT}/mcp"
if [[ "$SERVE_HTTP" == "1" || "$SERVE_HTTP" == "true" ]]; then
  echo "  REST UI:  http://127.0.0.1:${SERVE_PORT}/  (ui-v2 proxies /api here)"
fi
echo "  Stop:     docker compose -f $COMPOSE_BASE down"
echo ""
echo "To revert to the baked Hub binary (after publishing the new tag):"
echo "  LEANKG_IMAGE=freepeak/leankg:latest scripts/docker-reload.sh"