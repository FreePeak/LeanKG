# P0 MCP Root-Cause Fixes — Unit + Live Evidence — 2026-08-03

## Tickets closed

| ID | Status | PR | Unit tests | Live smoke |
|----|--------|-----|-----------|-----------|
| FR-P0-MCP-RC-01 | DONE | #198 | `resolve_db_route_*` (5) | mcp-p0-fix-smoke.sh RC-01 |
| FR-P0-MCP-RC-02 | DONE | #195 | `same_handle_reused_across_write_tool`, `index_tool_keeps_shared_handle`; `write_bus::*` (5) | mcp-p0-fix-smoke.sh RC-02 |
| FR-P0-MCP-RC-03 | DONE | #199 | `tool_timeout_*`, `tool_semaphore_keeps_one_worker_free`, `timeout_wraps_slow_future`, `cozo_db_is_send` | mcp-p0-fix-smoke.sh RC-03 |
| FR-P0-MCP-RC-04 | DONE | #196 | `full_scan_tools_refuse_on_mega_graph`, `cluster_skill_mega_path_uses_precomputed` | mcp-p0-fix-smoke.sh RC-04 |
| FR-P0-EMBED-LOCK | DONE | #200 | `auto_arm_default_disabled`, `dockerfile_sets_embed_auto_arm_zero_default` | mcp-p0-fix-smoke.sh EMBED-LOCK |

## Unit suite (cargo test --release)

- `cargo test --release --lib`: PASS (796)
- `cargo test --release --test mcp_tests`: PASS (30 incl. handle-reuse)
- `cargo test --release --test mcp_tools_full_tests`: PASS (49 incl. mega-guard)
- `cargo clippy --release --all-targets`: clean (my files)
- `cargo check --release --features embeddings`: clean

## Live smoke (Docker MCP :9699, project=/workspace-be)

[FILL FROM mcp-p0-fix-smoke.sh OUTPUT]

- RC-01: get_dependencies(project=/workspace-be, file) → real edges, no /workspace lock
- RC-02: add_knowledge → find_related_docs → no `lock hold by current process`
- RC-03: export_graph_snapshot concurrent with /health → /health ok
- RC-04: get_graph_report refuses on mega; get_cluster_skill precomputed/refused
- EMBED-LOCK: semantic_search completes, no lock

## 88-tool re-validation

[FILL FROM LEANKG_SMOKE_PROJECT=/workspace-be python3 scripts/mcp-smoke-tools.py]

- Expected: full-scan tools refuse within budget; empty only where data absent
  (find_tunnels / get_service_* / get_traceability* / query_incidents /
  get_files_for_doc are data-absent on code-only /workspace-be — correct empty).

## 5× parallel storm

[FILL: N/5 curl ticks ok during parallel semantic_search storm; /health stays ok]
