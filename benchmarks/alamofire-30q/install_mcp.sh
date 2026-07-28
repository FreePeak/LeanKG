#!/usr/bin/env bash
# Emit a temporary Claude Code MCP config JSON file for a 3-arm benchmark.
#
# Usage:
#   install_mcp.sh <output_path> <arm>
#
# Arms:
#   leankg    - LeanKG stdio MCP (local release binary)
#   codegraph - CodeGraph MCP (npm global binary)
#   none      - Empty mcpServers (the "no graph" baseline)
#
# Env:
#   LEANKG_BIN    absolute path to leankg binary
#   CODEGRAPH_BIN absolute path to codegraph binary (default: command -v codegraph)

set -euo pipefail

OUTPUT="${1:?output path required}"
ARM="${2:?arm required (leankg|codegraph|none)}"

LEANKG_BIN="${LEANKG_BIN:-$(command -v leankg || true)}"
CODEGRAPH_BIN="${CODEGRAPH_BIN:-$(command -v codegraph || true)}"

mkdir -p "$(dirname "${OUTPUT}")"

case "${ARM}" in
  leankg)
    if [[ -z "${LEANKG_BIN}" || ! -x "${LEANKG_BIN}" ]]; then
      echo "ERROR: leankg binary not found. Build with: cargo build --release" >&2
      echo "       or set LEANKG_BIN=/abs/path/to/leankg" >&2
      exit 2
    fi
    cat > "${OUTPUT}" <<'EOF'
{
  "mcpServers": {
    "leankg": {
      "type": "stdio",
      "command": "LEANKG_BIN_PLACEHOLDER",
      "args": ["mcp-stdio"]
    }
  }
}
EOF
    # Replace placeholder with actual binary path
    sed -i '' "s|LEANKG_BIN_PLACEHOLDER|${LEANKG_BIN}|g" "${OUTPUT}"
    ;;
  codegraph)
    if [[ -z "${CODEGRAPH_BIN}" || ! -x "${CODEGRAPH_BIN}" ]]; then
      echo "ERROR: codegraph binary not found. Install with: npm i -g @colbymchenry/codegraph" >&2
      exit 2
    fi
    cat > "${OUTPUT}" <<'EOF'
{
  "mcpServers": {
    "codegraph": {
      "type": "stdio",
      "command": "CODEGRAPH_BIN_PLACEHOLDER",
      "args": ["serve", "--mcp"]
    }
  }
}
EOF
    sed -i '' "s|CODEGRAPH_BIN_PLACEHOLDER|${CODEGRAPH_BIN}|g" "${OUTPUT}"
    ;;
  none)
    cat > "${OUTPUT}" <<'EOF'
{
  "mcpServers": {}
}
EOF
    ;;
  *)
    echo "ERROR: unknown arm '${ARM}' (expected leankg|codegraph|none)" >&2
    exit 2
    ;;
esac

echo "wrote ${ARM} MCP config to ${OUTPUT}" >&2
