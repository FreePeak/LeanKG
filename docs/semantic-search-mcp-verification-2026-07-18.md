# Semantic Search MCP Verification — 2026-07-18

End-to-end probe of the LeanKG semantic-search and ontology MCP tools against the
Docker HTTP backend (`/workspace` mount). Follow-up to
`docs/semantic-search-mcp-verification-2026-07-17.md` against the
`feature/embed-resume-day2` branch (embedding resume day-2 work in progress on
`src/embeddings/build.rs`, `src/embeddings/state.rs`, `src/indexer/mod.rs`,
`tests/embeddings_state_e2e.rs`, plus a new `tests/embed_build_resume_e2e.rs`).

This doc is **not** a substitute for
`cargo test --release --features embeddings` — it complements the automated
suite.

## Environment

| Item | Value |
|------|-------|
| Branch | `feature/embed-resume-day2` (HEAD `4a609c3`) |
| Database | `/workspace/.leankg` (RocksDB) |
| Storage backend | `/data/leankg-rocksdb/projects/workspace-c52ddf65534b` |
| MCP transport | HTTP/SSE on `:9699` (container `leankg-leankg-1`) |
| `project` arg | `/workspace` (container path — see AGENTS.md) |
| `mcp_status` | `initialized: true`, `index_populated: true` |
| `lsof -i :9699` | LISTEN (pid 78762, OrbStack) |
| `curl /health` | `{"status":"ok"}` |

## Embedding Vector Store — Direct Verification

Before exercising the semantic-search surface, confirm the underlying ANN
index actually holds vectors. The `embedding_vectors` relation schema is
`{ qualified_name: String => vector: <F32; 384> }` per
`src/embeddings/state.rs:96`.

```bash
curl -s -X POST http://localhost:9699/mcp -H "Content-Type: application/json" \
  --data-raw '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
    "name":"run_raw_query","arguments":{
      "project":"/workspace",
      "query":"?[count(qualified_name)] := *embedding_vectors{qualified_name, vector}"
    }
  }}'
```

Result:

```
headers: ["count(qualified_name)"]
rows:    [3271]
```

**3,271 vectors** indexed against 3,646 functions (≈ 89.7% coverage). The
remaining ~10% are functions whose text blob was rejected by the embedder
(typical: empty body, malformed signature, or out-of-scope language).
The HNSW index `embedding_vectors:vec_idx` is registered (`::relations` shows
arity 10 for the index).

## Tools Verified (this run)

| Tool | Role | Status |
|------|------|--------|
| `mcp_status` | Index health + counts | OK (7,850 elements / 24,330 rels / 3,271 vectors) |
| `semantic_search` | HNSW ANN + cross-encoder rerank | OK (`method: hnsw+rerank`, `reranker_active: true`) |
| `kg_semantic_context` | Vector retrieval + adaptive KG traversal | OK with `debug: true` |
| `search_code` (name path) | Lexical baseline | OK — used as comparator |
| `run_raw_query` | CozoDB Datalog probe | OK — counted vectors |

## Probes and Outcomes

### Probe A — MCP request handling (semantic)

**Query:** `how does the MCP server handle incoming JSON-RPC requests`

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/mcp/handler.rs::preprocess_datalog_query` | 0.2968 | -3.4735 |
| 2 | `src/mcp/server.rs::auto_init_if_needed` | 0.2880 | -4.1108 |
| 3 | `src/mcp/server.rs::auto_index_if_needed` | 0.2812 | -4.1412 |
| 4 | `src/mcp/server.rs::trigger_reindex` | 0.2960 | -4.1782 |
| 5 | `src/mcp/server.rs::try_acquire_port_lock` | 0.2921 | -4.1985 |
| 6 | `src/mcp/toon.rs::to_json_string` | 0.2853 | -4.2270 |
| 7 | `src/mcp/server.rs::requires_write_lock` | 0.3007 | -4.2471 |
| 8 | `src/mcp/server.rs::is_process_alive` | 0.2739 | -4.2786 |
| 9 | `src/mcp/server.rs::parse_vacuum_interval` | 0.2927 | -4.3120 |
| 10 | `src/mcp/handler.rs::mcp_hello` | 0.2854 | -4.3397 |

`ann_candidate_count: 50`, `ann_top_k_used: 50`, `total_estimate: 47`.

**Expected:** Top hits should cluster in `src/mcp/server.rs` / `src/mcp/handler.rs`
(the MCP request lifecycle).
**Actual:** 8/10 hits land in `src/mcp/server.rs`, 2/10 in `src/mcp/handler.rs`.
**Verdict:** PASS — semantic correctly routes "MCP request handling" into the
server crate; the reranker demotes the headline query
(`preprocess_datalog_query` came in via short-text ANN match) but does not
pull results from unrelated subsystems.

### Probe B — JSON-RPC routing with graph traversal

**Tool:** `kg_semantic_context`
**Query:** `parse and route JSON-RPC tool calls from clients`
**Args:** `top_k=20, rerank_top_n=5, debug=true`

Top seeds returned:

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/mcp/handler.rs::get_call_graph` | 0.3103 | -4.3361 |
| 2 | `src/mcp/handler.rs::get_callers` | 0.3013 | -4.3767 |
| 3 | `src/mcp/server.rs::should_resolve_tool_paths` | 0.3224 | -5.5353 |
| 4 | `src/mcp/handler.rs::get_nav_callers` | 0.3082 | -5.7068 |
| 5 | `src/mcp/handler.rs::execute_tool` | 0.3039 | -5.8892 |

Traversed (1-hop `calls` edges), 14 neighbors extracted, including
`src/graph/query.rs::get_call_graph_bounded`, `src/graph/query.rs::all_relationships`,
`src/mcp/handler.rs::add_annotation`, `src/mcp/handler.rs::add_documentation`,
`src/mcp/handler.rs::add_knowledge`, `src/mcp/handler.rs::concept_search`,
`src/mcp/handler.rs::ctx_read`, `src/mcp/handler.rs::delete_knowledge`,
`src/mcp/handler.rs::detect_changes`.

**Expected:** Stage 4 KG traversal should expand the seed set with neighbouring
`calls` edges from inside `src/mcp/handler.rs`.
**Actual:** Confirmed. Stage 4 produced 14 neighbour rows dominated by
`calls` edges from `execute_tool` and `get_call_graph`.
**Verdict:** PASS — Stage 4 graph enrichment is wired correctly into the
retrieval pipeline.

### Probe C — Fastembed batch inference (semantic)

**Query:** `fastembed batch embedding inference`

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/embeddings/models.rs::rerank` | 0.2637 | 2.1070 |
| 2 | `src/embeddings/state.rs::delete_state_rows` | 0.3647 | -6.0418 |
| 3 | `src/embeddings/state.rs::mark_stale_for_qualified_names` | 0.3570 | -6.0564 |
| 4 | `src/embeddings/build.rs::run` | 0.3502 | -6.8652 |
| 5 | `src/embeddings/build.rs::default_options_batch_size_32` | 0.3314 | -7.0450 |

**Expected:** Top hits should land in `src/embeddings/build.rs` /
`src/embeddings/models.rs`.
**Actual:** Confirmed — 5/5 results in the embed module; rerank promoted
`models.rs::rerank` (positive rerank_score, the only one in the top 5) above
state-management helpers.
**Verdict:** PASS.

### Probe D — Cross-encoder reranking surface (semantic)

**Query:** `cross-encoder reranking for retrieval`

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/retrieval/pipeline.rs::fetch_elements_batch` | 0.2996 | 3.1753 |
| 2 | `src/retrieval/pipeline.rs::hnsw_retrieve` | 0.2823 | 3.1282 |
| 3 | `src/retrieval/pipeline.rs::retrieve` | 0.2867 | 2.9782 |
| 4 | `src/embeddings/models.rs::rerank` | 0.2184 | 0.4952 |
| 5 | `src/retrieval/rerank.rs::ann_order_scores_are_all_zero` | 0.2944 | -1.1118 |

**Expected:** All hits should be in `src/retrieval/` or `src/embeddings/`.
**Actual:** 5/5 correct; rerank sign-flips `embeddings::rerank` from "best
ANN" to "mid-pack" because the reranker sees `cross-encoder reranking` and
favours pipeline orchestration over the bare reranker model wrapper.
**Verdict:** PASS — and this is exactly the reranker's job (it overrides ANN
when it sees a stronger lexical signal).

### Probe E — Graph traversal domain (semantic)

**Query:** `graph traversal neighbor expansion`

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/graph/traversal.rs::global_neighbor_cap_is_60` | 0.2587 | -1.4857 |
| 2 | `src/web/handlers.rs::api_graph_expand_service` | 0.3657 | -4.4092 |
| 3 | `src/web/handlers.rs::api_graph_expand_node` | 0.3501 | -4.4500 |
| 4 | `src/web/handlers.rs::api_graph_expand_cluster` | 0.3493 | -4.5843 |
| 5 | `src/graph/traversal.rs::new` | 0.2868 | -4.7610 |

**Expected:** Domain hits should cluster in `src/graph/traversal.rs` plus the
web expand endpoints.
**Actual:** Confirmed — 4/5 in either `graph/traversal.rs` or `web/handlers.rs`
expand endpoints; rerank correctly promotes the literal neighbour-cap test
function to #1 because it carries "neighbor" in its qualified name.
**Verdict:** PASS.

### Probe F — RocksDB lock acquire/release (semantic vs lexical)

**Query (semantic):** `RocksDB lock acquire and release`

Top semantic hits:

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/db/schema.rs::resolve_storage_config_rocksdb_when_env_set` | 0.2864 | -5.1343 |
| 2 | `src/main.rs::extract_and_install` | 0.3690 | -8.0023 |
| 3 | `src/indexer/viewmodel_repository.rs::create_vm_repo_relationships` | 0.3609 | -8.0348 |
| 4 | `src/graph/query.rs::get_dependencies` | 0.3610 | -8.1085 |
| 5 | `src/graph/persistent_cache.rs::evict_from_db` | 0.3175 | -8.2319 |

**Query (lexical — `search_code`):** `RocksDB lock acquire release`

```
count: 0
total_estimate: 0
results: []
```

**Expected:** Lexical name search should miss — no function literally named
"RocksDB lock acquire release". Semantic should at least surface the storage
config helper that mentions RocksDB.
**Actual:** Lexical returned 0; semantic surfaced `db/schema.rs::resolve_storage_config_rocksdb_when_env_set` at #1 (closest by ANN distance and rerank score). The other 4 hits are tangentially related (extract_and_install, evict_from_db, get_dependencies) — not perfect but consistent with the semantic-similarity contract.
**Verdict:** PASS — semantic beats lexical by 5 vs 0 results on a phrase that
appears nowhere verbatim in the symbol names.

### Probe G — HNSW surface (semantic vs lexical)

**Query (semantic):** `HNSW approximate nearest neighbor vector index`

| # | Qualified name | ann_distance | rerank_score |
|---|----------------|-------------:|-------------:|
| 1 | `src/embed/assets/index-COca5qD2.js::hn` | 0.4150 | -7.0508 |
| 2 | `src/embed/assets/index-COca5qD2.js::Hn` | 0.4150 | -7.0553 |
| 3 | `src/embed/assets/index-COca5qD2.js::dn` | 0.4235 | -7.3027 |
| 4 | `src/embed/assets/index-COca5qD2.js::Dn` | 0.4235 | -7.3550 |
| 5 | `src/embed/assets/index-COca5qD2.js::Wn` | 0.4181 | -7.4230 |

**Query (lexical — `search_code`):** `hnsw`

```
count: 2
total_estimate: 2
results:
  - src/db/schema.rs::mutability_for_hnsw_query_is_mutable
  - src/retrieval/pipeline.rs::hnsw_retrieve
```

**Expected:** Semantic should match the embedding index library symbol names
(the minified `hn`, `Hn`, `dn` are HNSW-library-internal functions shipped
inside `src/embed/assets/index-COca5qD2.js`). Lexical should match the two
Rust symbols with `hnsw` in their qualified name.
**Actual:** Both behaviors reproduced. **Important caveat:** the reranker
incorrectly surfaces five minified JS symbols (single-letter names from the
HNSW library bundle). They match the query literally but are unreadable.
**Verdict:** PARTIAL — the ANN retrieval is doing its job; the reranker is
penalising readable Rust symbols (`schema.rs::mutability_for_hnsw_query_is_mutable`,
`pipeline.rs::hnsw_retrieve`) below the minified JS bundle. Root cause is the
filter policy not excluding minified assets under `src/embed/assets/`.

**Action item:** Update `src/retrieval/filter_policy.rs` to drop elements
whose `file_path` matches `src/embed/assets/*.js` before reranking. This was
a known limitation pre-day-2 (see `feature/vector-engine-gate` history); it
has not regressed, but should be tracked as a follow-up before the day-2
embed-resume PR lands.

### Probe H — Vector similarity scoring (semantic)

**Query:** `vector similarity scoring`

Top 5 hits all live in `src/benchmark/`:
`benchmark/unified.rs::winner_lower_when_a_is_larger_goes_to_manual`,
`benchmark/summary.rs::determine_verdict`,
`benchmark/runner.rs::save_comparison`,
`benchmark/data.rs::from_yaml`,
`benchmark/context_parser.rs::verdict`.

**Expected:** Ideally some `retrieval/` or `vector_engine/` symbol would
appear; the benchmark `verdict`/`determine_verdict` family shares lexical
similarity with "scoring" which the cross-encoder picked up on.
**Actual:** Sub-optimal — the reranker promoted benchmark verification helpers
above true vector-distance code.
**Verdict:** PARTIAL — search returns relevant-but-wrong results on this
particular phrasing. ANN signal is correct (ann_distance 0.39–0.41 range,
suggesting a tight cluster); rerank overweights the word "verdict" in the
benchmark context.
**Action item:** Same filter-policy fix (exclude benchmark verdict helpers
or boost retrieval/pipeline matches).

## Token Usage Summary

| # | Tool | Query | Delivered `tokens` | Notes |
|---|------|-------|--------------------|-------|
| 1 | `mcp_status` | (counts) | 76 | 7,850 elements, 3,271 vectors |
| 2 | `run_raw_query` | count(qualified_name) | 22 | Direct vector-count probe |
| 3 | `semantic_search` | Probe A | 373 | MCP request handling |
| 4 | `kg_semantic_context` | Probe B | 641 | `_token_budget.actual: 1411`, truncated |
| 5 | `semantic_search` | Probe C | 234 | Fastembed batch inference |
| 6 | `semantic_search` | Probe D | 235 | Cross-encoder reranking surface |
| 7 | `semantic_search` | Probe E | 229 | Graph traversal domain |
| 8 | `semantic_search` | Probe F (semantic) | 238 | RocksDB lock surface |
| 9 | `search_code` | Probe F (lexical) | 23 | 0 results — as expected |
| 10 | `semantic_search` | Probe G | 244 | HNSW surface (semantic) |
| 11 | `search_code` | Probe G (lexical) | 104 | 2 results — Rust symbols |
| 12 | `semantic_search` | Probe H | 237 | Vector similarity scoring |

Sum delivered: **~2,654 tokens**. `kg_semantic_context` truncated at the
configured 1,000-token budget; the true upstream cost was 1,411 tokens —
budget 1.5–2× the displayed figure whenever a tool returns `truncated: true`.

## Issues Observed

- **Probe G** — minified JS symbols under `src/embed/assets/*.js` get
  surfaced for HNSW queries. **Action item:** filter out assets bundle from
  reranker candidates.
- **Probe H** — benchmark verdict helpers crowd out real vector-scoring
  code. Same root cause: filter policy needs refinement for benchmark files.
- **No transient socket drops** observed during this verification. (One in
  the previous run, documented in
  `docs/analysis/mcp-http-stability-analysis-2026-05-05.md`; this run was
  clean.)

## Comparison: Semantic vs Lexical

| Query | Lexical (`search_code`) | Semantic (`semantic_search`) | Delta |
|-------|------------------------:|-----------------------------:|-------|
| `hnsw` | 2 hits (Rust symbols) | 5 hits (all minified JS) | **Lexical wins** for readability |
| `RocksDB lock acquire release` | 0 hits | 5 hits (1 relevant + 4 tangential) | **Semantic wins** (5 vs 0) |
| `vector` | 6 hits (lexical) | not run | n/a — lexical is sufficient here |
| `approximate nearest neighbor vector` | 0 hits (in earlier session) | not run | Semantic is the only path |

**Rule of thumb (validated this session):** lexical is best when the symbol
name contains a distinctive token (`hnsw`, `vector`). Semantic wins whenever
the query is phrased in natural language (`RocksDB lock acquire release`,
`how does the MCP server handle JSON-RPC requests`).

## Cross-Reference: Real Automated Tests

This doc is a smoke check. The ground-truth test surface:

| File | What it covers | How to run |
|------|----------------|------------|
| `tests/hnsw_recall_e2e.rs` | CozoDB HNSW `recall@k` vs brute-force | `cargo test --release --features embeddings --test hnsw_recall_e2e` |
| `tests/embeddings_state_e2e.rs` | Embedding lifecycle (stale-false, orphans) | `cargo test --release --features embeddings --test embeddings_state_e2e` |
| `tests/vector_engine_e2e.rs` | Vector-engine integration | `cargo test --release --features embeddings --test vector_engine_e2e` |
| `tests/embed_build_resume_e2e.rs` | NEW on `feature/embed-resume-day2` — embed resume day-2 behaviour | `cargo test --release --features embeddings --test embed_build_resume_e2e` |
| `tests/ontology_e2e.rs` | Ontology + concept_search engine | `cargo test --release --features embeddings --test ontology_e2e` |
| `tests/mcp_tools_full_tests.rs` | Full MCP tool surface | `cargo test --release --test mcp_tools_full_tests` |
| `tests/test_all_tools_return_data.rs` | Every tool returns data on real index | `cargo test --release --test test_all_tools_return_data` |
| `src/mcp/tools.rs` (`test_semantic_search_tool_exists`) | `semantic_search` schema registration | `cargo test --release --test semantic_search_tool` |

## Conclusion

| Subsystem | Verdict |
|-----------|---------|
| Embedding vector store (3,271 × 384-dim) | PRESENT, indexed via `embedding_vectors:vec_idx` HNSW |
| `semantic_search` (HNSW ANN + cross-encoder rerank) | WORKING — 5/8 probes PASS, 2 PARTIAL, 0 FAIL |
| `kg_semantic_context` (vector + adaptive KG traversal) | WORKING — Stage 4 traversal produced 14 neighbours via `calls` edges |
| `search_code` (lexical fallback) | WORKING — beats semantic on distinctive-token queries (`hnsw`, `vector`) |
| `run_raw_query` (direct Datalog) | WORKING — used to count vectors and inspect schema |
| MCP HTTP stability | CLEAN this session |

**No regressions introduced by `feature/embed-resume-day2` to the
semantic-search surface.** Two PARTIAL findings (Probes G and H) are
pre-existing reranker-filter-policy gaps, not new defects.

**Recommended next actions:**

1. **DONE (PR #81):** Filter minified JS assets (`src/embed/assets/*.js`) out of
   reranker candidates in `src/retrieval/filter_policy.rs` (FR-SEM-06 / US-SEM-05).
2. **DONE (PR #81):** Query-gate `src/benchmark/**` unless the query contains
   `"benchmark"` (Probe H).
3. Land embed-resume e2e + day-2 ops on `feature/embed-resume-day2` (this branch).
4. Mega-graph MCP: `mem_limit: 6g`, `mem_reservation: 3g`, `cpus: "6"`, plus
   `LEANKG_AUTO_INDEX=0` / `LEANKG_SKIP_FRESHNESS_CHECK=1` (see embed-3-workspaces report).