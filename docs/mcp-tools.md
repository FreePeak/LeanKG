# MCP Tools

LeanKG exposes a comprehensive set of MCP tools for AI tools to query the knowledge graph.

Live registry size is **~81** tools with embeddings (`tools/list`; ~79 without). Prefer-order and redundancy status:

| Prefer-order | Tools |
|--------------|-------|
| Overview | `get_overview_context` → optional `get_architecture` |
| Search | `concept_search` → `semantic_search` → `search_code` |
| Semantic context | `semantic_search` → `kg_semantic_context` → `kg_context` |
| Environment filter | `env=` on search / `kg_*` tools |
| File context | `get_context` (skill default); `ctx_read` for compression modes |
| **Doc↔code join** | `search_by_requirement` / `get_traceability` (FR/US IDs) → `get_files_for_doc` / `find_related_docs` (after `mcp_index_docs`, canonical `docs/…` paths) → `concept_search` / `kg_trace_workflow` → `semantic_search` |

**Hard-removed:** `mcp_hello`, `mcp_impact`, `get_doc_for_file`, `find_clones`, `wake_up`, `search_by_environment`, `load_layer`, `get_doc_structure` — see [Wave 1b report](reports/rel-076-mcp-surf-1b-2026-08-01.md).  
**Machine-checked matrix:** `tests/redundant_tools_matrix.rs` (every tool classified).

## Core Tools

| Tool | Description |
|------|-------------|
| `mcp_init` | Initialize LeanKG project (creates .leankg/, leankg.yaml) |
| `mcp_index` | Index codebase (path, incremental, lang, exclude options) |
| `mcp_install` | Create .mcp.json for MCP client configuration |
| `mcp_status` | Show index statistics and status |

## Query Tools

| Tool | Description |
|------|-------------|
| `query_file` | Find file by name or pattern |
| `find_function` | Locate function definition |
| `search_code` | Search code elements by name/type |
| `get_call_graph` | Get function call chain (full depth) |

## Dependency Analysis

| Tool | Description |
|------|-------------|
| `get_dependencies` | Get file dependencies (direct imports) |
| `get_dependents` | Get files depending on target |
| `get_impact_radius` | Get all files affected by change within N hops |
| `get_review_context` | Generate focused subgraph + structured review prompt |

## Context Tools

| Tool | Description |
|------|-------------|
| `get_overview_context` | Session-start L0+L1 overview (replaces removed `wake_up`) |
| `get_context` | Get AI context for file (minimal, token-optimized) |
| `get_tested_by` | Get test coverage for a function/file |
| `find_large_functions` | Find oversized functions by line count |
| `session_recall` | US-SM-01: recover an offloaded MCP/tool payload bit-for-bit by `node_id` (`.leankg/sessions/<id>/refs/<node_id>.md`) |

## Documentation Tools

| Tool | Description |
|------|-------------|
| `generate_doc` | Generate documentation for file |
| `get_files_for_doc` | Get code files referenced in a markdown doc (`doc` accepts `docs/…`, `./docs/…`, or short name when unique). Returns `resolved_doc`, `files[]`, and `tried[]` on miss. |
| `get_doc_tree` | Get documentation tree structure with hierarchy |
| `find_related_docs` | Find markdown docs citing a code file (`file` accepts `./src/…`, `src/…`, or basename when unique). Returns `resolved_file`, `related_docs[]`, and `tried[]` on miss. |

**Doc↔code join (v3.7.13):** Run `mcp_index_docs` so markdown backtick/link paths resolve to indexed file keys. Prefer annotations (`search_by_requirement`, `get_traceability`, `link_element`) for `FR-*` / `US-*` IDs; use structural tools for file-path neighbors. Author markdown refs as repo-relative paths outside fenced code blocks (e.g. `` `src/mcp/handler.rs` ``).

## Traceability Tools

| Tool | Description |
|------|-------------|
| `get_traceability` | Get full traceability chain for a code element |
| `search_by_requirement` | Find code elements related to a requirement |
| `index_prd` | Parse a PRD markdown document into structured KG entries. Extracts FR-* and US-* into `knowledge_entries`, auto-links features to ontology workflows by matching code paths. Args: `source_doc` (default: `docs/prd.md`), `environment`. |
| `get_feature_flow` | Given a feature requirement ID (e.g. `FR-ONT-PROC-01`), returns the full implementation chain: FR → linked ontology workflows → ordered steps with `code_refs` and `failure_modes` → annotated code elements. Args: `feature_id` (required), `env`. |
| `get_traceability_matrix` | PO-facing traceability matrix: all FR-* with workflow count, annotated element count, and doc coverage. Filter by `feature_id` or `status` tag. Args: `feature_id` (optional), `status` (optional), `limit`. |

## Structure Tools

| Tool | Description |
|------|-------------|
| `get_code_tree` | Get codebase structure |
| `get_architecture` | Single-call architecture overview: languages, entry points, routes, clusters, hotspots, relationship summary, knowledge count, total element/file counts. Replaces running 5+ individual queries. Optional `max_items` argument caps each array section for token budget control; `truncated_sections` reports which sections were trimmed. |
| `get_graph_schema` | Single-call graph schema overview: element type counts and relationship type counts. Use to discover available patterns before running targeted queries. Optional `max_items` argument caps each array section for token budget control; `truncated_sections` reports which sections were trimmed. |
| `find_dead_code` | Find functions with zero callers and no `tested_by` edge, excluding common entry-point names (`main`, `Main`, `start`, `serve`, `Start`) and trivial bodies. Returns `dead_functions[]`, `count`, and the `min_lines` threshold that was applied. Argument: `min_lines` (default 10). |
| `query_graph` | US-GF-03 / FR-GF-05: natural-language scoped subgraph. Pipeline: keyword seed retrieval → bounded BFS expand (or shortest path when the question asks what connects A to B) → trim to `token_budget` → TOON response. Every edge includes `confidence_label` (`EXTRACTED` / `INFERRED` / `AMBIGUOUS`). Distinct from `orchestrate` (routing) and `kg_semantic_context` (embed pipeline). Args: `question` (required), `token_budget` (optional, default 2000), `max_depth` (optional, default 2). |

### Live MCP smoke (optional)

Against a running HTTP MCP (`localhost:9699`):

```bash
python3 scripts/mcp-smoke-tools.py
# ontology + routing gates only (CI-friendly, no full registry walk):
python3 scripts/mcp-smoke-tools.py --check-only-ontology
# mega-graph heavy tools (needs higher mem_limit):
LEANKG_SMOKE_INCLUDE_HEAVY=1 python3 scripts/mcp-smoke-tools.py
```

The script discovers tools from `tools/list`, skips mutators vs mega-graph-heavy with distinct labels, and always exercises `query_graph`. Since the CBM Phase-1 smoke closeout it also runs **hard gates**:

- **FR-A03 ontology gates** — `kg_self_test` (all_ok), `kg_ontology_status`, `kg_trace_workflow` (real workflow id, `LEANKG_SMOKE_WORKFLOW`), `ontology_control(action=status)`.
- **FR-A06 routing gates** — `find_route` / `get_screen_args` / `get_nav_callers` must answer or refuse via the predictable mega-graph guard; a hard tool error fails the gate.
- **FR-B50 raw-query recipes** — ≥ 10 validated `run_raw_query` Datalog recipes; see [guides/run-raw-query-recipes.md](guides/run-raw-query-recipes.md).

## Call-edge Resolution Method

Every `calls` relationship now carries a `resolution_method` value in its metadata:

| Value | Meaning |
|-------|---------|
| `name` | Resolved by exact name match within a known context (same class, same file, or receiver type). |
| `name_file_hint` | Resolved by name within a hint-derived file context. Lower confidence than `name`. |
| `unresolved` | Could not be resolved; stored as `__unresolved__<name>`. |
| `typed` | Resolved via in-process hybrid type registry (Go/TS MVP when `typed_resolve=go,ts`) or external LSP bridge. |

Use `get_architecture` to inspect how many calls fall into each bucket once Phase 3 lands.

## Auto-Initialization

When the MCP server starts without an existing LeanKG project, it automatically initializes and indexes the current directory. This provides a "plug and play" experience for AI tools.

## Procedural ontology (auto-update)

Procedural workflows live in `ontology/workflows.yaml` (plus `concepts.yaml`). Flow search: `kg_trace_workflow` / `kg_ontology_status`.

| Tool | Description |
|------|-------------|
| `kg_trace_workflow` | Ordered procedural steps for a workflow (or match by step name) |
| `kg_ontology_status` | Concept/procedural counts, missing aliases, workflows without failure modes |
| `ontology_control` | Admin: `action=sync` loads YAML into the served DB and touches `.leankg/ontology_synced`; `action=status` reports YAML/marker mtimes and counts |

**When ontology refreshes (no restart):**

1. YAML watch during `mcp-http` / `mcp-stdio` / `leankg serve` (debounce `LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS`, default 1500ms, min 1000)
2. Docker/boot when `.leankg/ontology_synced` is older than `concepts.yaml` **or** `workflows.yaml` (`LEANKG_ONTOLOGY_SYNC_ON_BOOT`)
3. After successful index (CLI / MCP / auto-index / UI)
4. Explicit `ontology_control(action=sync)`

**Sync semantics:** YAML is source of truth. Each sync **clears** the `ontology://` layer then batch-inserts, so renames/removals do not leave duplicate workflow steps. Live correction path (wrong steps → edit YAML → watcher → next `kg_trace_workflow`): [`docs/reports/ontology-proc-auto-smoke-2026-07-21.md`](reports/ontology-proc-auto-smoke-2026-07-21.md).

Env: `LEANKG_ONTOLOGY_DIR` overrides the ontology directory.

## Semantic Retrieval (optional, `embeddings` feature)

These tools ship only when LeanKG is built with `--features embeddings`. They add vector retrieval + cross-encoder rerank + adaptive graph traversal on top of the existing keyword/graph search.

| Tool | Description |
|------|-------------|
| `semantic_search` | Natural-language → functions. HNSW vector retrieve → cross-encoder rerank → **ontology-guided top-down traversal**: high-level seeds (class / doc / workflow / concept) walk *down* to the function nodes that implement the intent, re-ranked by a composite embedding of `"{upper_name}\n{function_blob}"`. Returns a merged, deduplicated function list. Each result carries `source: "direct"` (HNSW + cross-encoder) or `source: "traversed"` (composite-scored), plus `via_upper` / `via_edge` / `hop` for traversed hits. |
| `kg_semantic_context` | Vector retrieve → rerank → traverse. Best for natural-language questions where keyword search misses (e.g., 'where do we validate access rights'). Returns ranked seed nodes, 1-2 hop additive `traversed[]` neighborhood, **and a `functions[]` array** of composite-scored functions discovered by the same ontology-guided top-down traversal as `semantic_search`. |
| `embed_control` | US-EMBED-05: arm/disarm in-process day-2 embed when boot FG is off. Actions: `on` (idle-gated Incremental resume, default `mode=partial`), `off` (cooperative cancel), `status` (`mode`, `vectors_existing`, `skipped_fresh`, `armed`/`waiting_idle`/`running`/`paused_yield`). Does not wipe existing RocksDB vectors. |

Setup (one-time):

```bash
cargo run --release --features embeddings -- embed --init        # pre-download models (~700MB)
cargo run --release --features embeddings -- embed               # build the embedding index
```

Then call from any MCP client:

```json
{ "tool": "semantic_search", "arguments": { "query": "where is refund failure handled", "project": "/workspace" } }
```

`semantic_search` arguments: `query` (required), `env` (default `local`), `limit` (default 20), `offset` (default 0), `project`.
`kg_semantic_context` arguments: `query` (required), `env` (default `local`), `top_k` (default 50), `rerank_top_n` (default 10), `traverse` (default true), `include_worktrees` (default false), `debug` (default false).

Response shape:
- `semantic_search`: `{ query, env, limit, offset, method: "hnsw+ontology-traverse", results[], upper_matches[], total_estimate, has_more, ann_candidate_count, ann_top_k_used, reranker_active, traversal }`. Each `results[]` entry has `qualified_name`, `element_type`, `file_path`, `source`, `rank_score`, and either `rerank_score` (direct) or `composite_score` + `via_upper`/`via_edge`/`hop` (traversed). The two pools are normalized to `[0,1]` and interleaved by `rank_score`.
- `kg_semantic_context` (debug=false): `{ query, env, seeds[], traversed[], functions[] }`. With `debug=true`: adds `diagnostics` with reranker status, candidate counts, per-stage latency (incl. `functions`), and the edges traversed.

Behavior notes:
- If the reranker fails to load, the tool silently falls back to ANN-order top-N (Q4 option A). `diagnostics.reranker = "fallback_ann"` surfaces this.
- If the embedding index is older than the last `index` run, `diagnostics.embeddings_stale = true` (still serves, just warns).
- Worktree scratch copies (`.worktrees/`, `.claude/worktrees/`, `.opencode/worktrees/`) are filtered out by default to avoid duplicate-noise results.
- The top-down traversal reuses the already-embedded query vector (no second embed call per request). For `domain_entity` / `workflow` / `workflow_step` upper seeds whose code links live in `metadata.code_refs` rather than DB edges, it falls back to keyed path-prefix resolution (same path as `concept_search`) — no full-table scan.
- Known limits: `traversal.traversed_function_count` is `0` against indexes where doc↔code edges are file-granular or where `domain_entity` / `workflow` / `workflow_step` nodes carry no `metadata.code_refs`. To activate FR-SEM-08, run `mcp_index_docs` (populates `metadata.code_refs`) or rebuild the index with per-symbol doc↔code edges. Baseline reproduction: `docs/reports/semantic-traversal-vs-main-2026-07-27.md`.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Fallback

If the MCP server reports "LeanKG not initialized", manually run `leankg init` in your project directory, then restart the AI tool.

## Path Normalization

LeanKG automatically handles path formats with or without `./` prefix. For example, these are equivalent:
- `src/main.rs`
- `./src/main.rs`

This applies to all query tools: `get_dependencies`, `get_dependents`, `get_impact_radius`, `get_call_graph`, etc.
