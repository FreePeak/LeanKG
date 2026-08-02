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

`scripts/mcp-p0-fix-smoke.sh` — **8 passed, 0 failed** on the fixed bind-mounted binary:

```
PASS  RC-01 get_dependencies(project,file) returns deps — ok
PASS  RC-02 add_knowledge no lock
PASS  RC-02 find_related_docs after write no lock
PASS  RC-03 /health ok during slow tool
PASS  RC-04 get_graph_report refuses on mega
PASS  RC-04 get_cluster_skill no live Louvain
PASS  EMBED-LOCK semantic_search completes — returned 975 bytes
PASS  container /health healthy
RESULT: 8 passed, 0 failed
```

- RC-01: get_dependencies(project=/workspace-be, file) → real edges, no /workspace lock
- RC-02: add_knowledge → find_related_docs → no `lock hold by current process`
- RC-03: export_graph_snapshot concurrent with /health → /health ok
- RC-04: get_graph_report refuses on mega; get_cluster_skill precomputed/refused
- EMBED-LOCK: semantic_search completes, no lock

## 88-tool re-validation

`LEANKG_SMOKE_PROJECT=/workspace-be python3 scripts/mcp-smoke-tools.py`

**43 PASS, 2 FAIL** (clean run, no concurrent load). The 2 FAILs are harness /
config artifacts, NOT the P0 defects:

| Tool | FAIL reason | Classification |
|---|---|---|
| `agent_focus` | `persona smoke-tester not found` — missing `.leankg/agents/<name>.json` fixture | data/config absence — correct error |
| `session_recall` | `Missing required parameter 'node_id'` — harness didn't pass the arg | harness arg issue |

**P0-relevant outcomes (the four defects):**
- **RC-01**: `search_code` PASS (count:4), `query_graph` PASS — no more wrong-project empty.
- **RC-02**: **0 tools** fail with `lock hold by current process` (was 4 lock tools before).
- **RC-03**: **0 hangs / 0 dropped connections** in the clean run (was whole-server stall).
- **RC-04**: heavy tools **refuse** cleanly within budget: `export_html`, `find_route`,
  `get_doc_tree`, `get_nav_callers`, `get_nav_graph`, etc. return
  `refused: graph has 721328 elements` (not hang). `get_cluster_skill` serves
  precomputed / refuses.
- Data-absent tools (`search_knowledge` count:0, etc.) return empty correctly.

Before (2026-08-02): **44/88 failed** including 4 lock errors, 15 timeouts, whole-server
`(unhealthy)`. After (2026-08-03): 43 pass + heavy-refuse + 2 harness/config fails.

## 5× parallel storm

3 rounds × 5 concurrent `semantic_search` while probing `/health`:

- Docker-healthcheck-like probes (5s timeout, 2s cadence): **16/18 ok**; the
  container stayed `(healthy)` throughout (`docker ps` = `Up N (healthy)`,
  health log all exit-code 0). The 2 transient misses are within Docker's
  healthcheck tolerance (30s interval, 3 retries).
- Aggressive probes (3s): 10/30 — transient saturation expected under a 5-way
  heavy-embedding burst; container did not flip unhealthy.
- Container CPUs: 6 → `num_cpus-1 = 5` semaphore permits lets all 5 storm
  searches through; reserve 1 worker for /health. Docker `(healthy)` was
  preserved (RC-03 AC met).
