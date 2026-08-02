# Root-Cause Analysis: 88 MCP Tools on `/workspace-be` Mega-Graph (2026-08-02)

Source analyzed: `b555fdc2` (origin main). All paths relative to repo root. Live evidence: [`mcp-88-tool-validation-workspace-be-2026-08-02.md`](mcp-88-tool-validation-workspace-be-2026-08-02.md). This is the deep-dive companion — every failure is traced to a `file:line` root cause, verified by subagent code-tracing and live DB probes.

## Executive summary

44 of 88 tools failed on the `/workspace-be` mega-graph (662,378 elements / 2,259,855 relationships). The failures collapse into **4 real code defects + 1 data-absence class**:

| # | Defect | Code location | Tools broken |
|--:|--------|---------------|--------------|
| D1 | **File-arg project routing shadows `project`** | `src/mcp/server.rs:2640-2682` | ~8 empty + 4 lock = ~12 |
| D2 | **No single-handle-per-DB invariant → RocksDB double-open lock** | `src/db/schema.rs:193-204`; `src/ontology/watcher.rs:50` | 4 lock |
| D3 | **Blocking sync CozoDB on async workers + no request timeout → whole-server stall** | `src/mcp/server.rs:2979`; `src/db/schema.rs:25` | 16 timeout + server down |
| D4 | **Mega-graph guard is opt-in (7 of ~90 tools) + guard's own check is a full COUNT** | `src/ontology/safe_discover.rs:68`; `src/graph/query.rs:3483` | 15 timeout |
| A | **Data genuinely absent in `/workspace-be` index** (no PRD, incidents, service metadata, clusters, docs) | n/a (index content) | ~8 empty (tool correct) |

---

## Defect D1 — File-arg project routing shadows `project` (worst cross-cutting)

**Files:** `src/mcp/server.rs:2640-2682`, `src/mcp/server.rs:371-433`, `src/db/schema.rs:278-322`

**Mechanism.** `execute_tool` picks the DB by `file_path` priority chain (`server.rs:2640-2662`): `file` → `path` → `project`. For any tool whose probe supplied a `file`/`path` arg (container-relative like `./platform-core/...` or `/workspace/src`), that arg wins over the `project=/workspace-be` argument. `resolve_project_db_path` (`:2664-2675`) then calls `find_leankg_for_path` which walks **ancestors from container cwd `/workspace`** (`:318-335`) → finds `/workspace/.leankg` → key `workspace-c52ddf65534b`. The `project` param is injected into args (`:3070-3075`) but the routing already happened in `execute_tool` before the handler runs.

**Consequences.**
- Tools that take `file` and got a relative path → DB routed to `/workspace` (wrong project) → queries against the wrong graph → empty results (`get_dependencies`, `get_dependents`, `get_impact_radius`, `get_context`).
- Tools that take `path` → `/workspace/src` or `/workspace/docs` → same wrong routing → **RocksDB lock error** because `/workspace` is already open (see D2): `mcp_index`, `mcp_index_docs`, `add_documentation`, `find_related_docs`.
- Function-arg tools (`get_callers`, `get_call_graph`, `explain_node`) route via `project` → correct DB — which is why `get_callers("NewAcceptRideLogs")` returned 4 but the file tools returned empty.

**Evidence:** live probe `get_dependencies` with `project=/workspace-be` → `-32603 lock hold by current process: workspace-c52ddf65534b` (the `/workspace` hash), not `workspace-be-6917453a1780`.

**Fix:** make `project` the authoritative routing key; resolve `file`/`path` **relative to `project`**, not cwd. Or require absolute container paths from clients and document them.

---

## Defect D2 — No single-handle-per-DB-path invariant → RocksDB double-open lock

**Files:** `src/db/schema.rs:193-227`, `src/mcp/server.rs:418-432`, `src/mcp/server.rs:2734-2737`, `src/ontology/watcher.rs:34-127`

**Mechanism.** RocksDB (via `cozo::DbInstance::new("rocksdb", ...)`, `schema.rs:227`) enforces one writer handle per process per DB directory. `schema.rs:193-204` documents the invariant ("Use get_graph_engine_for_path() to share handles") but it's only a **debug-build assert** — the release binary has no guard. There are two independent handle sources:
1. `graph_engine_cache` (`server.rs:123`) — request-path handles, shared when cache key matches (`:418-432`), but **cleared on every write** (`:2734-2737`).
2. `self.graph_engine` / the **ontology YAML watcher** which holds a `GraphEngine` in `Arc` for the process lifetime (`watcher.rs:50`, loop at `:89-127`).

So `/workspace` RocksDB stays open forever (watcher clone). After a cache-clear, the next doc-tool request re-opens the same path via `init_db` → RocksDB `LOCK` rejection: *"lock hold by current process"*.

**Tools hit:** `add_documentation`, `find_related_docs`, `mcp_index`, `mcp_index_docs` (all FAIL-LOCK). Note the existing P0 `FR-P0-EMBED-LOCK` (embed scheduler holds the lock) is a **different** trigger of the same RocksDB single-writer constraint — this D2 is the cache/watcher double-open variant.

**Fix:** a process-wide `HashMap<PathBuf, GraphEngine>` behind one mutex with no cache-clear (drop the handle on write, but re-share not re-open); or read-only RocksDB open for mcp-http.

---

## Defect D3 — Blocking sync CozoDB on async workers + no request timeout → whole-server stall

**Files:** `src/mcp/server.rs:2979` (`handle_mcp_request`), `:3254` (`process_jsonrpc_request`), `src/mcp/handler.rs:231` (`execute_tool`), `src/db/schema.rs:25` (`run_script`), `src/main.rs:51` (`#[tokio::main]`)

**Mechanism.** `ToolHandler` tool fns are synchronous and call `run_script` → `cozo::Db::run_script` (blocking, holds relation locks, runs on calling thread). There is **no `spawn_blocking`** anywhere in `src/mcp/` (only `src/web/handlers.rs:1723` uses it). The Tokio multi-thread runtime has `num_cpus` workers. One heavy full-scan occupies a worker; several concurrent ones occupy all → `/health` (pure async, `server.rs:3421`) starves → Docker 500ms healthcheck flips the container `(unhealthy)`. **No `tokio::time::timeout` exists around tool execution** — a tool runs until it returns; the MCP client gives up at 30s but the server keeps burning CPU.

**This is the mechanism behind the whole-server collapse observed live** (`/health` timing out, container unhealthy, needing `docker restart`).

**Fix:** (a) `tokio::time::timeout` around handler execution in `process_jsonrpc_request`; (b) move heavy `GraphEngine` calls to `spawn_blocking`.

---

## Defect D4 — Mega-graph guard is opt-in + its own check is a full COUNT

**Files:** `src/ontology/safe_discover.rs:23-77`, `src/graph/query.rs:3483` (`count_elements`), `query.rs:3508` (`is_mega_graph` cached probe)

**Mechanism.** `refuse_full_scan_if_mega` (threshold default 50,000 from `LEANKG_MAX_CACHE_ELEMENTS`) is wired into only **7** of ~90 tools (`search_annotations`, `get_doc_tree`, `get_nav_graph`, `find_route`, `get_screen_args`, `get_nav_callers`, `get_cluster_context`). The 15 remaining timeout tools full-scan without a guard:
- `find_dead_code` (`handler.rs:4572`, `query.rs:4199`) — 4 full-table scans (1× code_elements + 3× relationships).
- `get_graph_report` (`handler.rs:1514`, `query.rs:5246`) — 2× all_elements + 2× all_relationships.
- `export_html` (`handler.rs:1870`, `export_select.rs:99`) — loads 662k+2.2M before truncating to 5000 nodes.
- `export_graph_snapshot` (`handler.rs:1854`, `query.rs:5906/797/836`) — full materialization, 60s BudgetGuard.
- `check_consistency` (`handler.rs:1573`, `query.rs:5449`) — full load + O(rels) findings loop.
- `get_tested_by` (`handler.rs:2311`, `query.rs:383`) — OR filter over 2.2M.
- `temporal_query` (`handler.rs:1549`, `query.rs:5400`) — full 2.2M load.
- `timeline` (`handler.rs:1562`, `query.rs:5418`) — full 2.2M load for a single node.
- `query_graph` (`handler.rs:1476`, `nl_query.rs:357`) — regex full-scan seed resolution over 662k.
- `get_cluster_skill` (`handler.rs:3063`, `clustering.rs:22`) — **live Louvain on 662k nodes**; bypasses the guard its siblings `get_clusters`/`get_cluster_context` use.
- `kg_semantic_context` (`handler.rs:4295`, `pipeline.rs:234`) — HNSW + cross-encoder + per-hop OR queries.
- `resolve_with_lsp` (`handler.rs:1784`, `lsp/client.rs:99-321`) — blocking child-process I/O on async worker (bounded by 5s LSP timeout, still a wedge victim).

**Even `search_annotations` (the guarded one) timed out**: the guard's `count_elements()` (`query.rs:3483`) is itself a full COUNT over 662k rows on the async worker — there's a cheaper cached probe (`is_mega_graph`, `query.rs:3508`) the guard doesn't use.

**Ontology write tools** (`add_ontology_concept` `handler.rs:3413`, `add_ontology_workflow` `:3473`, `delete_ontology_concept` `:3660`) are individually fast but **not in `WRITE_TOOLS`** (`server.rs:42-59`) — no write_lock serialization, and they can't be scheduled when full scans occupy every worker. Wedge victims.

**Fix:** wire `refuse_full_scan_if_mega` into all unguarded full-scan tools; fix `get_cluster_skill` to skip live Louvain on mega; use the cached `is_mega_graph` probe in the guard; add ontology writes to `WRITE_TOOLS`.

---

## Cluster A — Empty results from missing data (tool correct)

`/workspace-be` was indexed **code-only**: no PRD, no incidents, no service metadata, no clusters, no docs, sparse ontology. Tools returning empty for this are correct — the data doesn't exist:

| Tool | Missing data (verified via live `run_raw_query`) |
|------|--------------------------------------------------|
| `find_tunnels` | `cluster_id` NULL for all 662k elements (clustering never run — only `main.rs:3057` CLI) |
| `get_traceability` / `get_traceability_matrix` | `knowledge_entries` = 0 |
| `get_feature_flow` | `feature_workflow_links` = 0 |
| `get_files_for_doc` | `element_type="document"` = 0 |
| `get_upcoming_changes` | `env="upcoming"` = 0 |
| `query_incidents` | `incidents` table = 0 |
| `get_service_context` / `get_team_map` | `service_metadata` empty (no caller of `upsert_service_metadata`) |
| `get_service_graph` | `service_calls` = 0 |
| `detect_changes` | git runs in cwd `/workspace` (wrong repo, fresh clone → no changes) |
| `kg_trace_workflow` | 3 workflows exist but are YAML stubs: no `code_refs`, no `failure_modes` |

**Still real bugs among the empties:**
- `get_call_graph` (`query.rs:3111-3150`) — `find_element_by_name` returns **first match**; `NewAcceptRideLogs` exists in both `stores/` (leaf) and `schema/` (has callers). Short name resolves to the leaf → `calls: []`. Also `normalize_path` strips `./` (`query.rs:23-33`) while DB stores `./`-prefixed — exact-match on stripped path misses.
- `shortest_path` (`query.rs:1394-1396`) — mega-graph **skips the incoming-edge query** (`if mega { continue; }`), so bidirectional traversal fails.
- `agent_diary_read` — **works** when `project` passed; validation's empty was a missing-arg default to cwd.
- `kg_context` / `kg_concept_map` — **work** when queried with a matching concept (live: `"deployment"` returned 15 nodes); empty only for unmatched queries.

---

## Cluster B — Path / PRD errors (3 tools)

| Tool | Root cause | Evidence |
|------|-----------|----------|
| `orchestrate` | `read_file` → `FileReader::read` → `fs::read_to_string(path)` with path resolved to process cwd, never joined to `project` | `handler.rs:471-493`, `orchestrator/mod.rs:121-151`, `compress/reader.rs:39` |
| `ctx_read` | Same — `fs::read_to_string(file)` at `handler.rs:437` | `handler.rs:425-469` |
| `index_prd` | `/workspace-be/docs/prd.md` not found is **correct** (no PRD there). Lock error with `/workspace` is D1+D2. | `handler.rs:3726-3741` |

Note: `query_file`, `generate_doc`, `get_context` work because they query the **graph DB** (stored paths), never the filesystem — which is why they don't hit the cwd bug.

---

## Recommended fix priorities

| Priority | Fix | Defect | Impact |
|---------:|-----|--------|--------|
| P0 | `project` as authoritative routing key; resolve file/path relative to project | D1 | ~12 tools |
| P0 | Process-wide single GraphEngine per path, no cache-clear re-open | D2 | 4 lock tools |
| P0 | `tokio::time::timeout` + `spawn_blocking` in MCP path | D3 | whole-server stalls |
| P1 | Wire mega-guard into all unguarded full-scan tools; cached `is_mega_graph` in guard; `get_cluster_skill` skip Louvain on mega | D4 | 15 timeout tools |
| P1 | Add ontology writes to `WRITE_TOOLS` | D4 | 3 write tools |
| P2 | Run clustering in the index pipeline (`CommunityDetector::assign_clusters_to_elements`) | A | find_tunnels, get_pr_impact, get_god_nodes clusters |
| P2 | Fix `find_element_by_name` ambiguity (require qualified name) + `normalize_path` `./` handling; remove mega incoming-edge skip | B | get_call_graph, shortest_path |

---

## Evidence cross-references

- Live validation: [`mcp-88-tool-validation-workspace-be-2026-08-02.md`](mcp-88-tool-validation-workspace-be-2026-08-02.md)
- Key derivation: `src/db/schema.rs:278-322`
- Routing: `src/mcp/server.rs:2640-2706`, `:371-433`, `:2734-2737`
- Guard: `src/ontology/safe_discover.rs:23-77`; cached probe `src/graph/query.rs:3508`
- Watcher long-lived handle: `src/ontology/watcher.rs:34-127`
- Mega incoming-edge skip: `src/graph/query.rs:1394-1396`
- Name resolution: `src/graph/query.rs:3111-3150`, `:3230-3275`

— Auto-recorded by Claude session, 2026-08-02. Subagent-verified; no code modified.
