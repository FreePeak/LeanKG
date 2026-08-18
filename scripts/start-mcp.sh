#!/usr/bin/env bash
# Start LeanKG HTTP MCP server with Postgres config from .env
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Load .env for LEANKG_PG_URL
if [ -f "$PROJECT_DIR/.env" ]; then
  set -a
  source "$PROJECT_DIR/.env"
  set +a
fi

PORT="${MCP_HTTP_PORT:-9699}"
PROJECT="${1:-$PROJECT_DIR}"

echo "Starting LeanKG MCP HTTP server on :${PORT} (project: ${PROJECT})"
exec leankg mcp-http --port "$PORT" --project "$PROJECT" "$@"
