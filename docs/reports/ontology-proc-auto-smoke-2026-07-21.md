# REL-059 — Procedural ontology auto-update smoke (2026-07-21)

**Branch:** `feature/ontology-proc-auto`  
**PRD:** §3.18 / §5.21 — `US-ONT-PROC-01` / `FR-ONT-PROC-01..03` / `REL-059`  
**Codebase:** LeanKG 0.19.2

## Acceptance matrix

| # | AC | Evidence | Result |
|--:|----|----------|--------|
| 1 | Edit workflow YAML → `kg_trace_workflow` / `trace_workflow` updates without process restart | Unit: `ontology::sync::tests::sync_from_dir_loads_workflows_and_touches_marker` loads YAML into temp DB and `trace_workflow("Test Flow")` returns step; watcher module filters `workflows.yaml` / `concepts.yaml` and debounces ≥1s (`LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS`, default 1500). Watcher wired in `serve_http` / `serve_stdio` / `leankg serve`. | **PASS** |
| 2 | Boot with marker older than `workflows.yaml` triggers sync | `entrypoint.sh`: skip only when marker is newer than **both** `concepts.yaml` and `workflows.yaml` (FR-ONT-PROC-02). | **PASS** (logic review + code change) |
| 3 | Sync never blocks `/health` beyond existing ontology timeout policy | Boot path still uses `LEANKG_ONTOLOGY_SYNC_ON_BOOT` (`skip` / `force` / `timeout`, default timeout 45s). In-process watcher sync runs on a background thread and does not bind-block HTTP. | **PASS** |

## Triggers implemented

1. **YAML watch** — `ontology::spawn_ontology_yaml_watcher` during MCP HTTP/stdio and `leankg serve`
2. **Boot marker** — `.leankg/ontology_synced` vs both YAML mtimes
3. **Post-index** — CLI `index` / incremental, MCP `mcp_index`, auto-index, web `set_indexing_complete`
4. **Explicit** — MCP `ontology_control(action=sync|status)` (Admin)

## Commands run

```bash
cargo test --release --lib ontology::
cargo test --release --lib test_ontology_control
cargo test --release auth::tests::test_required_role_mapping
```

All listed tests passed (39 ontology::* + tool/auth checks).

## Notes

- Flow **search** is unchanged (`search_workflows` / `kg_trace_workflow`); this release keeps those reads fresh.
- `code_refs` remain YAML strings at sync time (no symbol rebind from indexer in P0).
- Won't Do: LLM workflow extraction.

## Follow-up (optional live Docker)

With MCP up: edit `ontology/workflows.yaml` → wait debounce → `ontology_control(status)` then `kg_trace_workflow` and confirm step text; touch only `workflows.yaml` older than marker and restart container to confirm boot sync log line.
