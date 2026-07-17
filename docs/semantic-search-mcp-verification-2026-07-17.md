# Semantic Search MCP Verification — 2026-07-17

End-to-end probe of the LeanKG semantic-search and ontology MCP tools against the
Docker HTTP backend (`/workspace` mount). This complements the automated test
suite — it is **not** a substitute for `cargo test --release --features embeddings`.

## Environment

| Item | Value |
|------|-------|
| Branch | `feature/vector-engine-gate` |
| Database | `/workspace/.leankg` (RocksDB) |
| Storage backend | `/data/leankg-rocksdb/projects/workspace-c52ddf65534b` |
| MCP transport | HTTP/SSE on `:9699` (container `leankg-leankg-1`) |
| `project` arg | `/workspace` (container path — see AGENTS.md) |
| `mcp_status` | `initialized: true`, `index_populated: true` |

## Tools Verified

| Tool | Role | Status |
|------|------|--------|
| `mcp_hello` | Connectivity probe | OK |
| `mcp_status` | Index health | OK, populated |
| `semantic_search` | HNSW ANN + cross-encoder rerank | OK (`method: hnsw+rerank`) |
| `concept_search` | Ontology-gated discovery | OK, 7 concepts / 40 code refs |
| `kg_semantic_context` | Vector retrieval + graph traversal | OK on retry (one socket drop) |
| `kg_self_test` | Schema + ontology drift check | `all_ok: true` |
| `explain_node` | Node dossier | OK |
| `search_code` (no ontology) | Name-fallback path | OK |

## Probes and Outcomes

### 1. Tool registration / dispatch

- **Query:** "how does the MCP server handle tool registration and dispatch"
- **Pipeline:** `ann_candidate_count: 50`, `reranker_active: true`
- **Top hit:** `src/mcp/server.rs::handle_mcp_request` (ann_dist `0.274`, rerank `-0.126`)
- **Top-10 spread:** 8/10 hits land in `src/mcp/server.rs` — `serve_http`,
  `process_jsonrpc_request`, `register_session`, `list_tools`,
  `call_tool`, `execute_tool`, `should_resolve_tool_paths`. Rerank scores
  decrease monotonically with ANN distance, so the two signals agree.

### 2. Vector embedding store

- **Query:** "vector embedding store for code retrieval"
- **Top hit:** `src/embeddings/build.rs::run` (ann_dist `0.325`)
- **Top-10 spread:** Hits cluster around
  `src/embeddings/build.rs::{run,count_vectors}` and
  `src/retrieval/pipeline.rs::{retrieve,fetch_elements_batch,hnsw_retrieve,retrieve_options_default_embeddings_stale_false}` —
  the expected surface for the embedding build + retrieval pipeline.

### 3. Concept ontology

- **Query:** "tree-sitter code parser indexer"
- **Matched concepts (7):** Android Code Indexing, Code Indexing, Knowledge
  Graph, Cluster Detection, Semantic Search, CozoDB Integration, Call Graph
  Resolution — `match_score` range 0.5–0.8.
- **Linked code refs:** 40 across the index.
- **Workflow:** `extract_keywords → scan_concept_ontology → load_concept → query_db`.

### 4. Graph-enriched semantic context

- **Query:** "how is impact radius calculated across the dependency graph"
- **Seed:** `src/graph/traversal.rs::calculate_impact_radius` (also
  `calculate_impact_radius_with_confidence`).
- **Traversal:** 1 hop, 22 related symbols across `src/graph/`, `src/db/`,
  `src/mcp/`, `src/main.rs`, and tests. Edge types dominated by `calls`.

### 5. Ontology / schema drift check

`kg_self_test` against the live CozoDB schema:

| Relation | Arity | Canonical | Columns |
|----------|-------|-----------|---------|
| `code_elements` | 13 | yes | `qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer` |
| `relationships` | 6 | yes | `source_qualified, target_qualified, rel_type, confidence, metadata, env` |

All `kg_*` tools (`kg_concept_map`, `kg_context`, `kg_ontology_status`,
`kg_trace_workflow`) report `ok: true`.

### 6. Node explainer contrast

`explain_node("semantic_search")` returned the **graph shape** of the MCP tool
handler: in-degree 2, out-degree 6, top neighbor types `calls` (6),
`<-calls` (1), `<-contains` (1) at `src/mcp/handler.rs:1534–1548`.
`search_code("handle_mcp_request")` (with `use_ontology=false`) returned a flat
single-result name hit at `src/mcp/server.rs:1949`. Useful contrast:
**graph-shaped output** vs. **name-shaped output** on the same codebase.

## Token Usage Summary

| # | Tool | Delivered `tokens` | Notes |
|---|------|--------------------|-------|
| 1 | `mcp_hello` | 6 | Heartbeat |
| 2 | `mcp_status` | 77 | Index state |
| 3 | `semantic_search` #1 | 378 | MCP dispatch query |
| 4 | `semantic_search` #2 | 401 | Embeddings query |
| 5 | `concept_search` | 759 | `_token_budget.actual: 3053`, **truncated** |
| 6 | `kg_semantic_context` | 672 | `_token_budget.actual: 2428`, **truncated** |
| 7 | `kg_self_test` | 155 | Schema check |
| 8 | `explain_node` | 93 | Node dossier |
| 9 | `search_code` | 67 | Name path |
| | **Sum delivered** | **2,608** | |

**Truncation gotcha.** The `tokens` field reports *delivered* payload.
`concept_search` and `kg_semantic_context` both capped output at the configured
`max: 1000` budget — their true upstream cost was **3,053** and **2,428**
respectively. Budget 3–4× the displayed figure whenever a tool returns
`truncated: true`.

## Issues Observed

- **One transient socket drop.** The first `kg_semantic_context` call returned
  `The socket connection was closed unexpectedly`. A clean retry succeeded with
  the same arguments. Same pattern is documented in
  `docs/analysis/mcp-http-stability-analysis-2026-05-05.md` — kill stale
  `:9699` listeners (`lsof -ti :9699 | xargs kill -9`) and relaunch the
  `com.leankg.mcp-http` launchctl job if it recurs.

## Cross-Reference: Real Automated Tests

The MCP probes above are smoke checks, not a substitute for the real test
suite. The ground-truth tests for the underlying machinery:

| File | What it covers | Why it matters |
|------|----------------|----------------|
| `tests/hnsw_recall_e2e.rs` | CozoDB HNSW `recall@k` against brute-force ground truth | The actual correctness gate for ANN. `cargo test --release --features embeddings --test hnsw_recall_e2e` |
| `tests/embeddings_state_e2e.rs` | Embedding lifecycle state machine (stale-false semantics, orphans) | Ensures `retrieve_options_default_embeddings_stale_false` stays in sync with state |
| `tests/vector_engine_e2e.rs` | Vector-engine integration under the vector-engine gate | The `feature/vector-engine-gate` branch's own probe |
| `tests/ontology_e2e.rs` | Ontology layer + concept_search engine | Mirrors what `concept_search` exercises live |
| `tests/mcp_tools_full_tests.rs` | Full MCP tool surface, including `kg_semantic_context` and `search_knowledge` | Tool-contract regressions |
| `tests/test_all_tools_return_data.rs` | Every tool returns data on a real index | Catches "tool returns nothing" regressions |
| `src/mcp/tools.rs:1202` `test_semantic_search_tool_exists` | Schema registration of `semantic_search` | Catches missing tool definitions |

Run the lot (release-only per project rules):

```bash
cargo test --release --features embeddings \
  --test hnsw_recall_e2e \
  --test embeddings_state_e2e \
  --test vector_engine_e2e \
  --test ontology_e2e \
  --test mcp_tools_full_tests \
  --test test_all_tools_return_data
```

## Conclusion

- The semantic-search pipeline (**HNSW ANN → cross-encoder rerank → optional
  graph traversal**) functions end-to-end on the `/workspace` Docker mount.
- The concept ontology and `kg_*` tools report healthy schema/arities.
- ANN distance and rerank score agree on ranking direction across all queries.
- One transient socket drop recovered on retry; matches the documented
  stability pattern.
- **No code changes required.** All signals are GREEN.
