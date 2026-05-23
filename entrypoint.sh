#!/bin/bash
set -e

ROCKSDB_ROOT="${LEANKG_ROCKSDB_ROOT:-$HOME/.leankg-rocksdb}"
PROJECTS_DIR="$ROCKSDB_ROOT/projects"
MCP_PORT="${MCP_HTTP_PORT:-9699}"

echo "=== LeanKG Entrypoint ==="
echo "RocksDB root: $ROCKSDB_ROOT"

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
    if [ -f "$rdb_dir/manifest" ] || [ -f "$rdb_dir/data/CURRENT" ]; then
        echo "  RocksDB data exists at $rdb_dir, skip index."
        return
    fi

    echo "  Indexing $project_dir (RocksDB: $rdb_dir)..."
    ( cd "$project_dir" && leankg index . --verbose )
    echo "  Index done."
}

if [ "${LEANKG_AUTO_INDEX:-1}" = "1" ]; then
    echo "=== Scanning for projects ==="
    if [ -n "$LEANKG_PROJECT_DIRS" ]; then
        IFS=',' read -ra DIRS <<< "$LEANKG_PROJECT_DIRS"
        for dir in "${DIRS[@]}"; do
            if [ -d "$dir" ]; then
                index_if_needed "$dir"
            fi
        done
    else
        for dir in /workspace* /test-project*; do
            if [ -d "$dir" ]; then
                index_if_needed "$dir"
            fi
        done
    fi
fi

echo "=== Starting MCP HTTP on port $MCP_PORT ==="
cd /workspace
exec leankg mcp-http --port "$MCP_PORT" "$@"
