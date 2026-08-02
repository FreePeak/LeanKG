# Install matrix (#187) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b | scripts/install.sh

## Steps
1. `bash -n scripts/install.sh` — syntax check
2. `configure_opencode` fn extracted + run twice on temp HOME (no network download; INSTALL_DIR stubbed)

## Results
- `bash -n` → OK (no syntax errors). PASS.
- Run 1: "Configured LeanKG plugin and MCP for OpenCode at ..." — config written.
- Run 2: "LeanKG MCP already configured in OpenCode" + "LeanKG plugin already in OpenCode" — **idempotent no-op** (jq guards `.mcp.leankg` + `.plugin contains ["leankg@git"]`). PASS.
- Config: `mcp.leankg = {"type":"local","command":["<bin>","mcp-stdio","--watch"],"enabled":true}` + plugin entry. PASS (AC: `[mcp_servers.leankg]` equivalent present).
- Full `bash install.sh opencode` timed out (network download of binary) — SKIP (documented); idempotency verified via isolated fn test.

## Tracker
- Install matrix (#187): PASS (syntax + idempotent configure + config shape). Full network install not exercised (download timeout) — SKIP documented.
