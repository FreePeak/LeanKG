#!/usr/bin/env bash
# Emit a temporary Claude Code MCP config JSON file.
#
# Usage:
#   install_leankg_mcp.sh <output_path> <mode>
# where mode is one of:
#   with     - register LeanKG stdio MCP pointing at the local release binary
#   without  - emit an empty mcpServers object (the "no MCP" baseline arm)
#
# `claude -p --strict-mcp-config <output_path>` reads only this file, so neither
# arm inherits the user's global Claude Code MCP setup.

set -euo pipefail

OUTPUT="${1:?output path required}"
MODE="${2:?mode required (with|without)}"

BIN_PATH="${LEANKG_BIN:-$(command -v leankg || true)}"
if [[ "${MODE}" == "with" ]]; then
  if [[ -z "${BIN_PATH}" || ! -x "${BIN_PATH}" ]]; then
    echo "ERROR: leankg binary not found on PATH. Build with: cargo build --release" >&2
    echo "       or set LEANKG_BIN=/abs/path/to/leankg" >&2
    exit 2
  fi
fi

mkdir -p "$(dirname "${OUTPUT}")"

case "${MODE}" in
  with)
    cat > "${OUTPUT}" <<EOF
{
  "mcpServers": {
    "leankg": {
      "type": "stdio",
      "command": "${BIN_PATH}",
      "args": ["mcp-stdio"]
    }
  }
}
EOF
    ;;
  without)
    cat > "${OUTPUT}" <<EOF
{
  "mcpServers": {}
}
EOF
    ;;
  *)
    echo "ERROR: unknown mode '${MODE}' (expected with|without)" >&2
    exit 2
    ;;
esac

echo "wrote ${MODE} MCP config to ${OUTPUT}" >&2