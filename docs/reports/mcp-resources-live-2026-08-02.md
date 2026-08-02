# MCP resources (#190) live evidence — 2026-08-02

## Environment
- commit: 8c77b22b + local fix | binary: target/release/leankg 0.19.31 (rebuilt) | MCP :9878 | project: /tmp/leankg-live-fixture

## Steps
1. `resources/list` (JSON-RPC POST)
2. `resources/read` `leankg://overview`
3. `resources/read` `leankg://overview/wake_up`

## Results
- `resources/list` → 2 resources: `leankg://overview` (LeanKG overview, text/markdown) + `leankg://overview/wake_up` (wake-up summary). PASS (AC: 2 resources listed).
- `resources/read leankg://overview` → contents text: `# project` + Languages + Top-level + Critical facts (Elements 36, Hot modules degree-ranked). PASS (read returns overview).
- `resources/read leankg://overview/wake_up` → wake_up summary text. PASS.

## Probe-found bug (fixed in this session)
PR #190 (f707af2d) added rmcp trait `list_resources`/`read_resource` + `.enable_resources()` in `get_info`, but the merge to main left:
1. `get_info` with only `.enable_tools()` (`.enable_resources()` lost) — src/mcp/server.rs:2795.
2. The HTTP JSON-RPC dispatcher (`process_jsonrpc_request`) kept the stub `"resources/list" => Ok({"resources": []})` and had NO `resources/read` handler — the rmcp trait impl only served stdio transport.
Fixed: added `.enable_resources()` (server.rs:2796) + wired HTTP mirror (server.rs:3188-3240) using `get_graph_engine_for_path` + `identity_context`/`critical_facts_context`/`wake_up_summary` (same seam as the tool).

## Tracker
- MCP resources (#190): PASS after fix (was FAIL: empty list + Method not found on main).
