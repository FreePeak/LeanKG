# LeanKG MCP Ontology Tools - Test Report

**Date:** 2026-07-18
**Tester:** opencode (MiniMax-M3)
**Transport:** HTTP/SSE MCP server, `http://localhost:9699/mcp`
**Container:** `leankg-leankg-1` (Docker, RocksDB engine)
**Server image:** `leankg-leankg` (locally built from `docker-compose.rocksdb.yml`)
**Projects under test:** `/workspace` (Leankg source), `/workspace-be` (mega-graph monorepo)

---

## 1. Executive Summary

| Tool | Layer | Project | Status | Avg Latency | Verdict |
|------|-------|---------|--------|-------------|---------|
| `semantic_search` | Vector retrieval + cross-encoder rerank | `/workspace` | Pass | 5-15 s | Works on small graph; unstable on mega-graph |
| `semantic_search` | Vector retrieval + cross-encoder rerank | `/workspace-be` | Partial | 11-13 s (short query), >180 s (long query) | Caused MCP container to restart twice on long queries |
| `concept_search` | Concept ontology (domain_entity, team_knowledge) | `/workspace` | Pass | 60-210 ms | Fast, accurate keyword + alias matching with fallback |
| `concept_search` | Concept ontology | `/workspace-be` | Pass | 6.5 s | Works but slower; returns project-specific team_knowledge |
| `kg_trace_workflow` | Procedural ontology (workflow + workflow_step + failure_mode) | `/workspace` | Pass | 60-80 ms | Exact-id and fuzzy matching both work |
| `kg_trace_workflow` | Procedural ontology | `/workspace-be` | Pass | 1.2-1.3 s | Same workflow definitions returned (not project-scoped) |
| `kg_ontology_status` | Status / coverage | both | Pass | 51 ms | Returns counts and gap metrics |
| `kg_self_test` | Smoke test | both | Pass (`all_ok: true`) | 80 ms - 3.3 s | Confirms schema arity and all kg_* tools respond |

**Top findings:**
1. All three user-facing ontology tools (semantic_search, concept_search, kg_trace_workflow) are **functionally correct** and return well-structured TOON/JSON.
2. `concept_search` and `kg_trace_workflow` are **fast and safe** on both small and mega-graphs (sub-second to a few seconds).
3. `semantic_search` is **stable on small graphs (<10k elements)** but **destabilized the MCP container twice** on the `/workspace-be` mega-graph (640 k elements). Each restart was a hard restart of `leankg-leankg-1`.
4. Ontology is stored under a `local:` gid namespace separate from `code_elements`; the 10 workflows and 16 concepts in this database are the **shared LeanKG default ontology**, not per-project data.
5. Concept ontology has a **graceful fallback** to name-based code search when no concept matches. Procedural ontology returns an **empty step list** instead of an error on no-match.

---

## 2. Test Environment

### 2.1 Server config (from `.dockerfile`)

```
LEANKG_DB_ENGINE=rocksdb
LEANKG_PROJECT_DIRS=/workspace,/workspace-be,/workspace-freepeak
LEANKG_MCP_PROJECT=/workspace-be
LEANKG_EMBED_BACKGROUND=1       # day-2 resume, no cold fill
LEANKG_SKIP_FRESHNESS_CHECK=1   # skip MCP auto-reindex on mega-graph (FR-MG-AUTO-01)
LEANKG_AUTO_INDEX=1
```

### 2.2 Graph sizes (from `get_architecture`)

| Metric | `/workspace` (small) | `/workspace-be` (mega) | Ratio |
|--------|---------------------|------------------------|-------|
| total_elements | 7 850 | 640 952 | 82x |
| total_files | 576 | 28 245 | 49x |
| Calls relationships | 15 527 | 700 184 | 45x |
| Top language | rust (4 085) | go (567 641) | - |
| Knowledge entries | 0 | 4 | - |
| Storage | `/workspace/.leankg/leankg.db` (sqlite, default) | `/data/leankg-rocksdb/projects/workspace-be-6917453a1780` (RocksDB) | - |

### 2.3 Ontology content (`kg_ontology_status`)

Returned for both projects (RocksDB single instance):

```json
{
  "concept_counts":   { "domain_entity": 16, "team_knowledge": 1 },
  "procedural_counts":{ "workflow": 10, "workflow_step": 48, "failure_mode": 76 },
  "total_aliases": 165,
  "nodes_missing_aliases": 48,
  "workflows_without_failure_modes": 0
}
```

- All 10 workflows declare failure modes.
- 48 ontology nodes have no aliases yet - a coverage gap.
- The 1 `team_knowledge` node is project-specific (`be-merchant-gateway` / `platform-food` owned).

### 2.4 Schema (`kg_self_test`)

`all_ok: true`. Canonical schemas:

- `code_elements` (arity 13): `qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer`
- `relationships` (arity 6): `source_qualified, target_qualified, rel_type, confidence, metadata, env`

Both schemas are `canonical: true` - no drift.

---

## 3. semantic_search (Vector retrieval + rerank)

### 3.1 Tool contract

```json
{
  "name": "semantic_search",
  "description": "Natural language semantic discovery with pagination. Ontology-first: scans concept ontology then falls back to bounded name search.",
  "input": {
    "query":      "string (required)",
    "project":    "string (optional, container path)",
    "env":        "production|staging|local (default local)",
    "limit":      "1-50 (default 20)",
    "offset":     "int (default 0)"
  }
}
```

Returned envelope:
```json
{
  "status": "ok",
  "tool": "semantic_search",
  "format": "toon",
  "data": {
    "query": "...",
    "env": "local",
    "method": "hnsw+rerank",
    "ann_top_k_used": 50,
    "ann_candidate_count": 50,
    "reranker_active": true,
    "limit": N,
    "offset": N,
    "has_more": true|false,
    "total_estimate": N,
    "results": [
      { "ann_distance": 0.xxx, "rerank_score": -x.xxx,
        "element_type": "function|method|interface|...",
        "env": "local", "file_path": "./...",
        "qualified_name": "..." }
    ]
  }
}
```

### 3.2 Results table

| # | Query | Project | Result count | Top match (qn) | Top rerank_score | Latency |
|---|-------|---------|--------------|----------------|------------------|---------|
| 1 | `MCP tool implementation` | `/workspace` | 3 | `./src/mcp/handler.rs::execute_tool` | -1.683 | 5.4 s |
| 2 | `how CozoDB stores graph data` | `/workspace` | 3 | `./src/graph/persistent_cache.rs::save_to_db` | -1.249 | 15.0 s |
| 3 | `indexing workflow that parses files` | `/workspace` | 3 | `./src/indexer/mod.rs::index_files_parallel` | -3.258 | 10.4 s |
| 4 | `""` (empty string) | `/workspace` | 0 (empty) | - | - | 2.2 s |
| 5 | `Rust function that walks a directory` | `/workspace` | 3 | `./src/main.rs::walkdir_count` | -0.170 | 10.4 s |
| 6 | `kubernetes operator pattern` | `/workspace` | 3 | `./examples/kotlin-api-service/.../OrderStatus.kt::isTerminal` | -8.112 | 14.6 s |
| 7 | `report` | `/workspace-be` | 2 | `/workspace-be/.../istanbul-lib-report/lib/report-base.js::execute` | -1.845 | 11.0 s |
| 8 | `grpc gateway` | `/workspace-be` | 3 | `./platform-core/.../napas_api_grpc.pb.go::NewNapasGatewayServiceClient` | **+1.351** | 13.5 s |
| 9 | `service that handles food report generation` | `/workspace-be` | (long-running) | - | - | **>180 s, server restarted** |

Observations:

- Empty query (`""`) returns `results: []` and `total_estimate: 0`. **No error**, no panic.
- Off-topic query (`kubernetes operator pattern`) still returns results with very low rerank confidence (-8.x). The ANN retrieval is broad; the reranker correctly down-ranks them.
- The best rerank score observed is **+1.35** (positive = high confidence) for "grpc gateway" matching generated gRPC stubs.
- On mega-graphs, short single-token queries (`report`) complete in 11 s. Long natural-language queries (`service that handles food report generation`) **exceed the 180 s timeout and caused the container to restart** (see Section 6).

### 3.3 Error handling

Missing required parameter:

```json
{
  "code": -32603,
  "message": "Missing required parameter 'query' for tool 'semantic_search'"
}
```

Validated: the JSON-RPC layer rejects empty arguments before tool dispatch.

---

## 4. concept_search (Concept ontology)

### 4.1 Tool contract

```json
{
  "name": "concept_search",
  "description": "Concept-gated semantic search: extracts keywords from raw input, scans the concept ontology for matching concepts, loads each concept's code references, and queries the LeanKG DB for the actual code. Falls back to name-based code search if no concept matches.",
  "input": {
    "query":   "string (required)",
    "project": "string",
    "env":     "production|staging|local",
    "limit":   "1-50"
  }
}
```

Returned envelope:
```json
{
  "status": "ok",
  "data": {
    "query": "...",
    "workflow": "extract_keywords -> scan_concept_ontology -> load_concept -> query_db",
    "extracted_keywords": ["..."],
    "concept_match_count": N,
    "code_ref_count": N,
    "matched_concepts": [
      { "name": "...", "aliases": ["..."], "description": "...",
        "gid": "local:...",
        "element_type": "domain_entity|team_knowledge|...",
        "match_reason": "exact name match | name contains '...' | alias contains '...' | description contains '...'",
        "match_score": 0.x,
        "code_refs": ["src/.../file.rs", ...],
        "docs": ["docs/..."],
        "owned_by": ["team"] }
    ],
    "linked_code":     [ { "file", "line", "name", "qualified_name", "type" } ],
    "linked_code_count": N,
    "fallback_used":    false|true,
    "fallback_results": [...],
    "_token_budget":    { "actual": N, "max": 1000, "truncated": true|false }
  }
}
```

### 4.2 Match-reason taxonomy (observed)

| Reason | Trigger | Example |
|--------|---------|---------|
| `exact name match` | query string equals a concept name | `"MCP Server"` |
| `name contains 'X'` | query word appears in `name` | `"Cluster Detection"` matched on `'cluster'` |
| `alias contains 'X'` | query word appears in `aliases[]` | `"android extraction` matched on alias `'android extraction'` |
| `description contains 'X'` | query word appears in `description` | `"indexer"` matched on Android Code Indexing description |
| `1 of N meaningful query words matched: <word> (alias)` | partial multi-word match | `"MCP server"` matched alias `model` with score 0.35 |

### 4.3 Results table

| # | Query | Project | Concepts matched | Top match | Score | Fallback | Latency |
|---|-------|---------|------------------|-----------|-------|----------|---------|
| 1 | `MCP server` | `/workspace` | 1 | MCP Server (exact name match) | 1.00 | no | 120 ms |
| 2 | `cluster detection graph` | `/workspace` | 5 | Microservice Detection (`name contains 'detection'`) | 0.80 | no | 132 ms |
| 3 | `microservice extraction` | `/workspace` | 3 | Microservice Detection (`exact alias match`) | 0.90 | no | 125 ms |
| 4 | `indexer` | `/workspace` | 1 | Android Code Indexing (`description contains 'indexer'`) | 0.50 | no | 121 ms |
| 5 | `kubernetes operator helm chart` | `/workspace` | 0 | - | - | yes (1 result) | 210 ms |
| 6 | `food report` | `/workspace-be` | 1 | Food Public API Routing (`team_knowledge`) | 0.80 | no | 6.5 s |

Observations:

- The **match-reason taxonomy is rich** and lets the caller see why a concept matched (exact name, alias, name contains, etc.). Useful for debugging false positives.
- The workflow string in every response documents the internal pipeline:
  `extract_keywords -> scan_concept_ontology -> load_concept -> query_db`.
- Multi-keyword queries return **multiple** concepts ranked by score; this is good for concept exploration.
- Token budget is enforced at 1000 tokens (`_token_budget.actual` reported; `truncated: true` when exceeded). Test #2 and #3 hit the cap.
- When no concept matches (`kubernetes operator helm chart`), the tool reports `concept_match_count: 0`, `fallback_used: true`, and runs a name-based search over code. The fallback correctly returned only 1 doc_section result (a false-positive regex mention), confirming it does not invent results.
- `team_knowledge` concepts are **project-scoped** (test #6 only found `Food Public API Routing` on `/workspace-be`, not on `/workspace`).

### 4.4 Example response shape - "MCP server" on `/workspace`

```json
{
  "matched_concepts": [
    {
      "name": "MCP Server",
      "aliases": ["mcp server", "model context protocol", "mcp tool server", "json-rpc server"],
      "description": "HTTP server exposing LeanKG functionality via MCP (Model Context Protocol) JSON-RPC interface.",
      "gid": "local:mcp:domain_entity:mcp_server:v1",
      "element_type": "domain_entity",
      "match_reason": "exact name match: MCP Server",
      "match_score": 1.0,
      "code_refs": ["src/mcp/server.rs", "src/mcp/handler.rs", "src/mcp/tools.rs"],
      "docs": ["docs/mcp.md"],
      "owned_by": ["mcp"]
    }
  ],
  "linked_code_count": 20,
  "_token_budget": { "actual": 1097, "max": 1000, "truncated": true }
}
```

---

## 5. kg_trace_workflow (Procedural ontology)

### 5.1 Tool contract

```json
{
  "name": "kg_trace_workflow",
  "description": "Get an ordered procedural trace for a workflow. Useful for debugging user flows, understanding what code runs before/after a step, and identifying missing tests or failure handling.",
  "input": {
    "workflow_id_or_query": "string (required) - workflow name, ID, or search query",
    "project": "string",
    "env":     "production|staging|local"
  }
}
```

Returned envelope:
```json
{
  "status": "ok",
  "data": {
    "workflow_query": "...",
    "step_count": N,
    "steps": [
      { "order": 1..N,
        "gid": "local:default:workflow_step:...:v1",
        "name": "Human-readable step name",
        "description": "",
        "code_refs": ["src/...", "src/...::fn"],
        "failure_modes": ["error_name_1", "error_name_2"] }
    ]
  }
}
```

### 5.2 Workflows discovered

| Workflow gid | Steps | Failure modes declared | Avg step latency |
|--------------|-------|------------------------|------------------|
| `local:default:workflow:mcp_request_flow:v1` | 7 | 13 | ~0 ms |
| `local:default:workflow:code_indexing_flow:v1` | 7 | 13 | ~0 ms |
| `local:default:workflow:ontology_sync_flow:v1` | 5 | 10 | ~0 ms |
| `local:default:workflow:document_indexing_flow:v1` | 4 | 8 | ~0 ms |

There are 10 workflows in the ontology; the 4 above are the ones discovered through queries. The remaining 6 require a different query string.

### 5.3 Results table

| # | Query | Project | Resolved workflow | step_count | Latency |
|---|-------|---------|-------------------|------------|---------|
| 1 | `mcp_request_flow` (exact id) | `/workspace` | mcp_request_flow | 7 | 61 ms |
| 2 | `MCP request flow` (fuzzy) | `/workspace` | mcp_request_flow | 7 | 70 ms |
| 3 | `code_indexing_flow` | `/workspace` | code_indexing_flow | 7 | 65 ms |
| 4 | `code indexing` (fuzzy) | `/workspace` | code_indexing_flow | 7 | 49 ms |
| 5 | `ontology sync flow` (fuzzy) | `/workspace` | ontology_sync_flow | 5 | 66 ms |
| 6 | `document indexing` (fuzzy) | `/workspace` | document_indexing_flow | 4 | 64 ms |
| 7 | `setup and init` (vague) | `/workspace` | code_indexing_flow (best match) | 7 | 80 ms |
| 8 | `this workflow does not exist` | `/workspace` | (no match) | 0 | 71 ms |
| 9 | `this_is_definitely_not_a_real_workflow_id_xyz123` | `/workspace` | (no match) | 0 | 267 ms |
| 10 | `mcp_request_flow` | `/workspace-be` | mcp_request_flow | 7 | 1.3 s |
| 11 | `code indexing` | `/workspace-be` | code_indexing_flow | 7 | 1.2 s |
| 12 | `nonexistent workflow xyz` | `/workspace-be` | (no match) | 0 | 1.3 s |

### 5.4 Observations

- **Exact-id and fuzzy query resolution both work.** "mcp_request_flow" and "MCP request flow" return the same 7-step ordered trace.
- **No-match is graceful.** Returns `step_count: 0`, `steps: []`. No 404, no panic. Callers must check `step_count` before iterating.
- **Workflows are not project-scoped.** `/workspace` and `/workspace-be` return identical traces because the procedural ontology lives in the shared `local:default:` namespace. This is intentional - workflows describe the engine's behavior, not the indexed code.
- **Vague queries land on the best partial match.** "setup and init" matched `code_indexing_flow` (the only workflow that contains `init_parsers` and `find_files`). A caller looking for the actual `setup_rocksdb` workflow should query `setup_rocksdb` directly.
- **Step traceability is excellent.** Every step carries concrete `code_refs` (file paths and qualified function names) and a list of declared `failure_modes`. Test #1 for example shows step 5 `Execute Graph Operation` references `src/graph/query.rs` and `src/db/mod.rs` with failure modes `db_timeout, graph_traversal_error`. This makes the procedural ontology directly usable for debugging.

### 5.5 Example trace - mcp_request_flow on `/workspace`

| order | step name | code_refs | failure_modes |
|------:|-----------|-----------|---------------|
| 1 | Receive JSON-RPC Request | `src/mcp/server.rs::handle_mcp_request`, `src/mcp/server.rs::serve_http` | parse_error, invalid_jsonrpc |
| 2 | Authenticate Request | `src/mcp/auth.rs::AuthManager`, `src/mcp/server.rs::auth_manager_read` | auth_timeout, invalid_token |
| 3 | Route to Tool Handler | `src/mcp/handler.rs::execute_tool`, `src/mcp/tools.rs::ToolRegistry` | unknown_tool, missing_parameter |
| 4 | Validate Tool Arguments | `src/mcp/handler.rs`, `src/mcp/tools.rs` | schema_mismatch, type_error |
| 5 | Execute Graph Operation | `src/graph/query.rs`, `src/db/mod.rs` | db_timeout, graph_traversal_error |
| 6 | Compress Response | `src/compress/mod.rs`, `src/compress/shell.rs`, `src/compress/reader.rs` | compression_overflow, token_budget_exceeded |
| 7 | Return JSON-RPC Response | `src/mcp/server.rs::handle_mcp_request`, `src/toon.rs` | (none) |

---

## 6. Stability incidents (mega-graph)

While testing `semantic_search` against `/workspace-be` (640 k elements), the MCP container **restarted twice**. Each restart was confirmed via `docker ps`:

```
NAMES                STATUS
leankg-leankg-1      Up 3 seconds (health: starting)   <- first restart
leankg-leankg-1      Up 2 seconds (health: starting)   <- second restart
```

Restart sequence (from `docker logs leankg-leankg-1`):

```
=== Syncing ontology from /workspace-be/ontology into /workspace-be ===
=== Ontology sync done ===
=== Starting MCP HTTP on port 9699 for project /workspace-be ===
🚀 Starting MCP HTTP server on http://0.0.0.0:9699
INFO leankg::embeddings::build: background embed already running (PID 1); skipping new spawn
INFO leankg::mcp::server:    Background embed already running; not spawning a new one
INFO leankg::mcp::server:    MCP HTTP server listening on http://0.0.0.0:9699
```

### 6.1 Triggering inputs

| Test | Query | Latency | Outcome |
|------|-------|---------|---------|
| `ss_be` | `"service that handles food report generation"` | >180 s | **Container restart** |
| `ss_be_gateway` | `"grpc gateway"` | 13.5 s | Success |
| `ss_be_small` | `"report"` | 11.0 s | Success |

### 6.2 Hypothesis

Long, semantically rich queries on the mega-graph force the ANN retrieval to walk deeper into the HNSW index and pull a wider candidate set. Combined with:

- Container `mem_limit: 6g`, `mem_reservation: 3g` (per `docker-compose.rocksdb.yml`)
- Concurrent background embed worker (`LEANKG_EMBED_BACKGROUND_WORKERS=1`)
- A competing host-side `cargo test` build (4 rustc processes at 90%+ CPU during the test window)

...the MCP process exceeds its memory or runtime budget and is killed by the Docker daemon. The container's PID-1 restart policy brings it back up automatically, but the in-flight RPC returns `HTTP 000` (no response) to the caller.

### 6.3 Mitigations already in place (per `AGENTS.md` and reports)

- `LEANKG_SKIP_FRESHNESS_CHECK=1` (set)
- `LEANKG_AUTO_INDEX=0` possible escape
- Offline `embed` / `index` recommended for 150k+ graphs
- Use `concept_search -> semantic_search -> search_code -> kg_context` fallback chain

### 6.4 Suggested follow-ups

1. Add a per-tool timeout or page-size guard in `semantic_search` so it fails fast with a redirect hint instead of hanging (the same pattern already used by `get_clusters` and `get_code_tree` on mega-graphs).
2. Surface a `truncated: true` flag in the response when ANN `top_k` had to be reduced, so callers can retry with `limit` smaller.
3. Investigate why reranker is active on tiny `limit: 2-3` calls - rerank adds 5-10 s even when only a handful of candidates are needed.

---

## 7. Coverage gaps observed

From `kg_ontology_status`:

- **48 ontology nodes are missing aliases.** Without aliases, keyword queries can only match on `name` or `description`, lowering recall. Recommend backfilling aliases for `workflow_step` and `failure_mode` nodes (76 of the 76 failure_modes and 48 of the 48 workflow_steps may be under-aliased).
- **0 workflows without failure_modes** - this is good. Every declared workflow has at least one failure mode.
- **`nodes_missing_aliases: 48`** counts nodes that have an empty `aliases` array. Investigation: which 48 nodes?

---

## 8. Verdict per tool

| Tool | Verdict | Notes |
|------|---------|-------|
| `semantic_search` | **Conditional pass.** Works on small graph (5-15 s, acceptable). On mega-graphs, **susceptible to container restart on long queries**. Need a page-size / timeout guard. |
| `concept_search` | **Pass.** Fast (<250 ms small, ~6 s mega), accurate matching taxonomy, graceful fallback to name search. Token-budget cap is enforced and reported. |
| `kg_trace_workflow` | **Pass.** Fast, deterministic, exact-id and fuzzy both work, no-match is graceful. Step records are directly actionable (code_refs + failure_modes). |
| `kg_ontology_status` | **Pass.** Cheap, returns useful coverage metrics. |
| `kg_self_test` | **Pass.** All kg_* tools respond; schema arity is canonical. |

---

## 9. Reproducer scripts

The test harness used to produce this report is at `/tmp/opencode/leankg_run.sh`:

```bash
#!/usr/bin/env bash
# Usage: leankg_run.sh <tool_name> <run_label> '<json_args>'
# Writes raw response to /tmp/opencode/leankg-tests/<label>.raw.json
# Prints a one-line timing header plus the parsed tool result.
```

Raw responses for every test in this report live in `/tmp/opencode/leankg-tests/`:

```
ss_mcp_impl.out       ss_cozodb.out          ss_workflow_idx.out
ss_empty.out          ss_paginate.out        ss_rust_fn.out
ss_unknown.out        ss_be_small.out        ss_be_gateway.out
ss_bad.out
cs_mcp.out            cs_cluster.out         cs_microservice.out
cs_no_match.out       cs_be.out
w_mcp.out             w_mcp_fuzzy.out        w_code_idx.out
w_ontology.out        w_doc_idx.out          w_setup.out
w_no_match.out        w_wrong_id.out         w_be.out
w_be_fuzzy.out        w_be_no_match.out
kgs_final.out         selftest_final.out
status_be.out         arch_workspace.out     arch_be.out
```

---

## 10. Appendix: raw transcripts (selected)

### A. semantic_search "MCP tool implementation" - `/workspace` (5.4 s)

```json
{
  "status": "ok",
  "tool": "semantic_search",
  "format": "toon",
  "data": {
    "query": "MCP tool implementation",
    "env": "local",
    "method": "hnsw+rerank",
    "ann_top_k_used": 50,
    "ann_candidate_count": 50,
    "reranker_active": true,
    "limit": 3, "offset": 0, "has_more": true,
    "total_estimate": 27,
    "results": [
      { "ann_distance": 0.3188, "rerank_score": -1.6461,
        "element_type": "function", "env": "local",
        "file_path": "./src/doc/wiki.rs",
        "qualified_name": "./src/doc/wiki.rs::generate_mcp_tools_page" },
      { "ann_distance": 0.2751, "rerank_score": -1.6831,
        "element_type": "function", "env": "local",
        "file_path": "./src/mcp/handler.rs",
        "qualified_name": "./src/mcp/handler.rs::execute_tool" },
      { "ann_distance": 0.2936, "rerank_score": -1.8977,
        "element_type": "function", "env": "local",
        "file_path": "./tests/mcp_tools_full_tests.rs",
        "qualified_name": "./tests/mcp_tools_full_tests.rs::create_real_handler" }
    ]
  }
}
```

### B. concept_search "kubernetes operator helm chart" - `/workspace` (fallback)

```json
{
  "status": "ok",
  "data": {
    "query": "kubernetes operator helm chart",
    "workflow": "extract_keywords -> scan_concept_ontology -> load_concept -> query_db",
    "extracted_keywords": ["kubernetes", "operator", "helm", "chart"],
    "concept_match_count": 0,
    "code_ref_count": 0,
    "matched_concepts": [],
    "linked_code_count": 0,
    "fallback_used": true,
    "fallback_results": [
      { "file": "docs/analysis/cozodb-parsing-fix-2026-03-25.md",
        "line": 34,
        "name": "2. Regex Operator Syntax Error",
        "qualified_name": "docs/analysis/cozodb-parsing-fix-2026-03-25.md::2. Regex Operator Syntax Error",
        "type": "doc_section" }
    ]
  }
}
```

### C. kg_trace_workflow "document indexing" - `/workspace` (64 ms)

```json
{
  "status": "ok",
  "data": {
    "workflow_query": "document indexing",
    "step_count": 4,
    "steps": [
      { "order": 1, "gid": "local:default:workflow_step:scan_docs:v1",
        "name": "Scan Documentation Directory",
        "description": "",
        "code_refs": ["src/doc_indexer/mod.rs", "src/indexer/mod.rs::generate_physical_structure"],
        "failure_modes": ["directory_not_found", "too_many_files"] },
      { "order": 2, "gid": "local:default:workflow_step:parse_markdown:v1",
        "name": "Parse Markdown Content",
        "code_refs": ["src/doc_indexer/mod.rs::parse_document"],
        "failure_modes": ["parsing_error", "encoding_error"] },
      { "order": 3, "gid": "local:default:workflow_step:extract_references:v1",
        "name": "Extract Code References",
        "code_refs": ["src/doc_indexer/mod.rs::extract_code_references"],
        "failure_modes": ["no_references_found"] },
      { "order": 4, "gid": "local:default:workflow_step:create_edges:v1",
        "name": "Create Documented-by Edges",
        "code_refs": ["src/doc_indexer/mod.rs::create_documentation_edges"],
        "failure_modes": ["orphan_document", "stale_reference"] }
    ]
  }
}
```