#!/usr/bin/env bash
# Start LeanKG HTTP MCP server (sqlite default engine).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# .env is optional and only carries non-storage overrides; the storage
# engine defaults to sqlite. Explicit LEANKG_DB_ENGINE=postgres + LEANKG_PG_URL
# still opts into Postgres if a caller really wants it.
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  source "$PROJECT_DIR/.env"
  set +a
fi
export LEANKG_DB_ENGINE="${LEANKG_DB_ENGINE:-sqlite}"
if [ "$LEANKG_DB_ENGINE" != "sqlite" ]; then
  echo "warning: LEANKG_DB_ENGINE=$LEANKG_DB_ENGINE — sqlite is the supported default" >&2
fi

PORT="${MCP_HTTP_PORT:-9699}"
PROJECT="${1:-$PROJECT_DIR}"

echo "Starting LeanKG MCP HTTP server on :${PORT} (project: ${PROJECT})"
exec leankg mcp-http --port "$PORT" --project "$PROJECT" "$@"
