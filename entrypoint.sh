#!/bin/bash
set -e

ROCKSDB_ROOT="${LEANKG_ROCKSDB_ROOT:-$HOME/.leankg-rocksdb}"
PROJECTS_DIR="$ROCKSDB_ROOT/projects"
MCP_PORT="${MCP_HTTP_PORT:-9699}"

echo "=== LeanKG Entrypoint ==="
echo "RocksDB root: $ROCKSDB_ROOT"

# Determine which project to serve via MCP
# LEANKG_MCP_PROJECT takes precedence; fall back to /workspace
MCP_PROJECT="${LEANKG_MCP_PROJECT:-/workspace}"

# Plan §"Part B Option 3" defaults: never block MCP on embed. Operators
# can opt-in to in-process background embed by setting
# LEANKG_EMBED_BACKGROUND=1; the MCP server picks it up after bind.
# LEANKG_EMBED_ON_BOOT=0 keeps the legacy synchronous path off (the
# legacy path blocks `mcp-http` start for hours on mega-graphs).
export LEANKG_EMBED_ON_BOOT="${LEANKG_EMBED_ON_BOOT:-0}"
# Fast-path defaults for Docker (INT8). Background workers stay low via
# compose/Dockerfile; ensure_quantized runs after cache warm in-process.
export LEANKG_EMBED_FAST="${LEANKG_EMBED_FAST:-1}"
export LEANKG_EMBED_MODEL="${LEANKG_EMBED_MODEL:-bge-q}"
export LEANKG_EMBED_MAX_SEQ="${LEANKG_EMBED_MAX_SEQ:-128}"
export LEANKG_EMBED_MAX_BLOB_CHARS="${LEANKG_EMBED_MAX_BLOB_CHARS:-500}"
export LEANKG_EMBED_MAX_MB="${LEANKG_EMBED_MAX_MB:-3072}"

rocksdb_dir_for() {
    local project_dir="$1"
    local canonical=$(realpath "$project_dir" 2>/dev/null || readlink -f "$project_dir" 2>/dev/null || echo "$project_dir")
    local name=$(basename "$canonical" | sed 's/[^a-zA-Z0-9_-]/-/g')
    local hash=$(echo -n "$canonical" | sha256sum | cut -c1-12)
    echo "$PROJECTS_DIR/${name}-${hash}"
}

index_if_needed() {
    local project_dir="$1"
    local leankg_dir="$project_dir/.leankg"

    echo "--- Project: $project_dir ---"

    if [ ! -d "$leankg_dir" ]; then
        echo "  Initializing .leankg..."
        mkdir -p "$leankg_dir"
        cat > "$leankg_dir/leankg.yaml" <<YAML
project:
  name: "$(basename "$project_dir")"
  root: .
  project_path: "$project_dir"
  languages:
    - go
    - typescript
    - python
    - java
    - kotlin
mcp:
  enabled: true
  port: $MCP_PORT
  auth_token: ""
  auto_index_on_start: false
  auto_index_threshold_minutes: 60
  auto_index_on_db_write: false
  require_git_for_auto_index: false
indexer:
  exclude:
    - "**/node_modules/**"
    - "**/vendor/**"
  include:
    - "*.go"
    - "*.ts"
    - "*.js"
    - "*.py"
    - "*.java"
    - "*.kt"
YAML
    fi

    local rdb_dir=$(rocksdb_dir_for "$project_dir")

    # Force re-index if LEANKG_FORCE_REINDEX is set
    if [ "${LEANKG_FORCE_REINDEX:-0}" = "1" ]; then
        echo "  LEANKG_FORCE_REINDEX=1, removing old RocksDB data at $rdb_dir..."
        rm -rf "$rdb_dir"
    fi

    # Skip index if RocksDB data already exists (unless forced above)
    if [ -f "$rdb_dir/manifest" ] || [ -f "$rdb_dir/data/CURRENT" ]; then
        echo "  RocksDB data exists at $rdb_dir, skip index."
        return
    fi

    echo "  Indexing $project_dir (RocksDB: $rdb_dir)..."
    ( cd "$project_dir" && leankg index . --verbose )
    echo "  Index done."
}

# Plan §"Part B" — by default we DO NOT block MCP on embed. The legacy
# `embed_if_needed` path that used to run synchronously here is replaced
# by two layered mechanisms:
#
#   1. `LEANKG_EMBED_BACKGROUND=1` (recommended) — `mcp-http` spawns
#      an in-process worker thread that shares the CozoDb handle.
#      MCP stays healthy while HNSW catches up.
#   2. Legacy foreground embed — only runs when explicitly opted in
#      via `LEANKG_EMBED_ON_BOOT=1`. This was the default before the
#      Plan was implemented; we keep it as an escape hatch for users
#      who want a deterministic "embed complete before mcp-http" flow.
embed_if_needed() {
    local project_dir="$1"

    if [ "${LEANKG_EMBED_ON_BOOT:-1}" != "1" ]; then
        echo "  LEANKG_EMBED_ON_BOOT!=1, skipping legacy foreground embed."
        return
    fi

    if [ ! -x "$(command -v leankg)" ]; then
        echo "  leankg binary not found on PATH, skipping embed build."
        return
    fi

    local cache_dir="${LEANKG_FASTEMBED_CACHE:-$HOME/.cache/leankg}"
    local marker="$cache_dir/.embed_init_done"

    if [ ! -f "$marker" ]; then
        echo "  Downloading embedding + reranker models (first run only)..."
        if ( cd "$project_dir" && leankg embed --init --project "$project_dir" ); then
            mkdir -p "$cache_dir"
            touch "$marker"
        else
            echo "  WARN: leankg embed --init failed; semantic tools may be unavailable."
            return
        fi
    fi

    echo "  Building embedding index (foreground; will block mcp-http)..."
    ( cd "$project_dir" && leankg embed --wait --project "$project_dir" ) || \
        echo "  WARN: leankg embed failed; semantic tools may be unavailable."
}

if [ "${LEANKG_AUTO_INDEX:-1}" = "1" ]; then
    echo "=== Scanning for projects ==="
    if [ -n "$LEANKG_PROJECT_DIRS" ]; then
        IFS=',' read -ra DIRS <<< "$LEANKG_PROJECT_DIRS"
        for dir in "${DIRS[@]}"; do
            if [ -d "$dir" ]; then
                index_if_needed "$dir"
                embed_if_needed "$dir"
            fi
        done
    else
        for dir in /workspace* /test-project*; do
            if [ -d "$dir" ]; then
                index_if_needed "$dir"
                embed_if_needed "$dir"
            fi
        done
    fi
fi

# Default SQLite mmap to 64 MiB instead of the upstream 256 MiB.
# The previous default consumed hundreds of MiB of RSS per leankg process and
# pushed containers past their memory limit (exit 137 = OOM kill).
export LEANKG_MMAP_SIZE="${LEANKG_MMAP_SIZE:-67108864}"

# Sync ontology into the MCP project's database, using ontology files from
# the MCP project if present, otherwise falling back to /workspace/ontology
# (the LeanKG source tree ships concepts.yaml + workflows.yaml there).
# The sync target is always $MCP_PROJECT so the served DB has the ontology.
#
# CRITICAL (search availability): never block mcp-http forever on sync.
# On mega-graphs, opening RocksDB + sync has been observed to hang for
# minutes with /health failing (empty reply / connection reset) so
# search_code / find_function appear completely broken.
#
# LEANKG_ONTOLOGY_SYNC_ON_BOOT:
#   skip | 0     — do not sync (MCP starts immediately)
#   force | 1    — blocking sync (legacy; can delay health for a long time)
#   timeout|auto — default: sync with timeout, then start MCP either way
ONTOLOGY_SOURCE_DIR=""
for candidate in "$MCP_PROJECT" "/workspace"; do
    if [ -d "$candidate/ontology" ] && [ -f "$candidate/ontology/concepts.yaml" ]; then
        ONTOLOGY_SOURCE_DIR="$candidate/ontology"
        break
    fi
done
ONTOLOGY_SYNC_MODE="${LEANKG_ONTOLOGY_SYNC_ON_BOOT:-timeout}"
ONTOLOGY_SYNC_TIMEOUT="${LEANKG_ONTOLOGY_SYNC_TIMEOUT_SECS:-45}"
ONTOLOGY_MARKER="$MCP_PROJECT/.leankg/ontology_synced"
if [ -n "$ONTOLOGY_SOURCE_DIR" ]; then
    case "$ONTOLOGY_SYNC_MODE" in
        skip|0|false|FALSE)
            echo "=== Skipping ontology sync (LEANKG_ONTOLOGY_SYNC_ON_BOOT=$ONTOLOGY_SYNC_MODE) ==="
            ;;
        force|1|true|TRUE|foreground)
            echo "=== Syncing ontology from $ONTOLOGY_SOURCE_DIR into $MCP_PROJECT (blocking) ==="
            ( cd "$MCP_PROJECT" && leankg ontology sync --path "$ONTOLOGY_SOURCE_DIR" ) \
                && mkdir -p "$(dirname "$ONTOLOGY_MARKER")" && touch "$ONTOLOGY_MARKER" \
                || echo "WARN: ontology sync failed; continuing to mcp-http"
            echo "=== Ontology sync done ==="
            ;;
        *)
            # Default: skip if marker is newer than concepts.yaml (already synced).
            CONCEPTS_FILE="$ONTOLOGY_SOURCE_DIR/concepts.yaml"
            if [ -f "$ONTOLOGY_MARKER" ] && [ "$ONTOLOGY_MARKER" -nt "$CONCEPTS_FILE" ]; then
                echo "=== Ontology already synced (marker newer than concepts.yaml); skipping ==="
            else
                echo "=== Syncing ontology from $ONTOLOGY_SOURCE_DIR (timeout ${ONTOLOGY_SYNC_TIMEOUT}s) ==="
                set +e
                if command -v timeout >/dev/null 2>&1; then
                    ( cd "$MCP_PROJECT" && timeout "$ONTOLOGY_SYNC_TIMEOUT" leankg ontology sync --path "$ONTOLOGY_SOURCE_DIR" )
                    sync_rc=$?
                else
                    ( cd "$MCP_PROJECT" && leankg ontology sync --path "$ONTOLOGY_SOURCE_DIR" )
                    sync_rc=$?
                fi
                set -e
                if [ "$sync_rc" -eq 0 ]; then
                    mkdir -p "$(dirname "$ONTOLOGY_MARKER")" && touch "$ONTOLOGY_MARKER"
                    echo "=== Ontology sync done ==="
                elif [ "$sync_rc" -eq 124 ]; then
                    echo "WARN: ontology sync timed out after ${ONTOLOGY_SYNC_TIMEOUT}s; starting mcp-http anyway (search stays available)"
                else
                    echo "WARN: ontology sync failed (rc=$sync_rc); starting mcp-http anyway"
                fi
            fi
            ;;
    esac
else
    echo "No ontology directory found in $MCP_PROJECT or /workspace, skipping ontology sync"
fi

# One-shot CLI after auto-index (used by scripts/docker-up.sh).
# Example: docker run --rm ... freepeak/leankg:latest embed --wait --project /workspace
if [ $# -gt 0 ]; then
    case "$1" in
        embed|index|semantic-context|ontology|status|doctor|smoke-test|query|find)
            echo "=== Running one-shot: leankg $* ==="
            cd "$MCP_PROJECT" || { echo "FATAL: cannot cd to $MCP_PROJECT"; exit 1; }
            exec leankg "$@"
            ;;
    esac
fi

# Optional first-boot path: offline embed every scanned project, then MCP.
# Prefer scripts/docker-up.sh so health stays green (embed finishes first).
if [ "${LEANKG_DOCKER_SETUP:-0}" = "1" ]; then
    echo "=== LEANKG_DOCKER_SETUP=1: offline INT8 embed before mcp-http ==="
    export LEANKG_EMBED_MAX_MB="${LEANKG_EMBED_MAX_MB:-0}"
    if [ -n "$LEANKG_PROJECT_DIRS" ]; then
        IFS=',' read -ra SETUP_DIRS <<< "$LEANKG_PROJECT_DIRS"
    else
        SETUP_DIRS=()
        for dir in /workspace* /test-project*; do
            [ -d "$dir" ] && SETUP_DIRS+=("$dir")
        done
    fi
    for dir in "${SETUP_DIRS[@]}"; do
        dir="$(echo "$dir" | xargs)"
        [ -z "$dir" ] || [ ! -d "$dir" ] && continue
        echo "  embed --wait: $dir"
        ( cd "$dir" && leankg embed --wait --project "$dir" \
            --workers "${LEANKG_EMBED_SETUP_WORKERS:-8}" \
            --batch-size "${LEANKG_EMBED_SETUP_BATCH:-128}" \
            --types function,method ) \
            || echo "  WARN: embed failed for $dir; semantic tools may be unavailable."
    done
fi

cd "$MCP_PROJECT" || { echo "FATAL: cannot cd to $MCP_PROJECT"; exit 1; }

# Option A: same container serves MCP (:9699) + REST UI API (:8080).
# UI v2 (Vite) proxies /api → host :8080; agents keep using MCP :9699.
# Disable with LEANKG_SERVE_HTTP=0 when you only need MCP (saves a process).
#
# IMPORTANT: do NOT start serve with cwd=$MCP_PROJECT when that mount is a
# multi-repo tree. Default UI project is LeanKG at /workspace (override with
# LEANKG_SERVE_PROJECT). MCP keeps using $MCP_PROJECT independently.
SERVE_PORT="${LEANKG_SERVE_PORT:-8080}"
SERVE_PROJECT="${LEANKG_SERVE_PROJECT:-/workspace}"
case "${LEANKG_SERVE_HTTP:-1}" in
    0|false|FALSE|no|NO|off|OFF)
        echo "=== Skipping leankg serve (LEANKG_SERVE_HTTP=${LEANKG_SERVE_HTTP}) ==="
        ;;
    *)
        echo "=== Starting web REST API (leankg serve) on port $SERVE_PORT (project=$SERVE_PROJECT) ==="
        # Explicit --project so UI opens LeanKG RocksDB, not the MCP multi-repo cwd.
        leankg serve --port "$SERVE_PORT" --project "$SERVE_PROJECT" &
        SERVE_PID=$!
        sleep 0.5
        if kill -0 "$SERVE_PID" 2>/dev/null; then
            echo "  serve PID $SERVE_PID — UI REST http://0.0.0.0:$SERVE_PORT project=$SERVE_PROJECT"
        else
            echo "  WARN: leankg serve failed to start; UI REST :$SERVE_PORT unavailable (MCP still starting)"
        fi
        ;;
esac

echo "=== Starting MCP HTTP on port $MCP_PORT for project $MCP_PROJECT ==="
exec leankg mcp-http --port "$MCP_PORT" --project "$MCP_PROJECT" "$@"
