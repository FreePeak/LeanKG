#!/usr/bin/env bash
# Emit a temporary Claude Code MCP config JSON file.
#
# Usage:
#   install_leankg_mcp.sh <output_path> <mode>
# where mode is one of:
#   baseline       - emit an empty mcpServers object (grep/read only, no MCP)
#   context-mode   - register the context-mode MCP server (CONTEXT_MODE_SERVER =
#                    absolute path to server.mjs of the pinned npm package; see RUNNING.md)
#   leankg         - register LeanKG stdio MCP (`leankg serve --stdio`) from LEANKG_BIN
#
# `claude -p --strict-mcp-config <output_path>` reads only this file, so no arm
# inherits the user's global Claude Code MCP setup.

set -euo pipefail

OUTPUT="${1:?output path required}"
MODE="${2:?mode required (baseline|context-mode|leankg)}"

BIN_PATH="${LEANKG_BIN:-$(command -v leankg || true)}"
case "${MODE}" in
  leankg)
    if [[ -z "${BIN_PATH}" || ! -x "${BIN_PATH}" ]]; then
      echo "ERROR: leankg binary not found. Build with: make go-build (repo root)" >&2
      echo "       or set LEANKG_BIN=/abs/path/to/leankg" >&2
      exit 2
    fi
    ;;
  context-mode)
    if [[ -z "${CONTEXT_MODE_SERVER:-}" || ! -f "${CONTEXT_MODE_SERVER}" ]]; then
      echo "ERROR: CONTEXT_MODE_SERVER must name an existing server.mjs (context-mode@<pinned tag>)" >&2
      echo "       see benchmarks/cross_tool/RUNNING.md, section 'context-mode arm'" >&2
      exit 2
    fi
    ;;
esac

mkdir -p "$(dirname "${OUTPUT}")"

case "${MODE}" in
  leankg)
    cat > "${OUTPUT}" <<EOF
{
  "mcpServers": {
    "leankg": {
      "type": "stdio",
      "command": "${BIN_PATH}",
      "args": ["serve", "--stdio"]
    }
  }
}
EOF
    ;;
  context-mode)
    cat > "${OUTPUT}" <<EOF
{
  "mcpServers": {
    "context-mode": {
      "type": "stdio",
      "command": "node",
      "args": ["${CONTEXT_MODE_SERVER}"]
    }
  }
}
EOF
    ;;
  baseline)
    cat > "${OUTPUT}" <<EOF
{
  "mcpServers": {}
}
EOF
    ;;
  *)
    echo "ERROR: unknown mode '${MODE}' (expected baseline|context-mode|leankg)" >&2
    exit 2
    ;;
esac

echo "wrote ${MODE} MCP config to ${OUTPUT}" >&2
