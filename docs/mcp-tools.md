# MCP Tools

LeanKG exposes a comprehensive set of MCP tools for AI tools to query the knowledge graph.

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
| `get_context` | Get AI context for file (minimal, token-optimized) |
| `get_tested_by` | Get test coverage for a function/file |
| `find_large_functions` | Find oversized functions by line count |

## Documentation Tools

| Tool | Description |
|------|-------------|
| `generate_doc` | Generate documentation for file |
| `get_files_for_doc` | Get code elements referenced in a documentation file |
| `get_doc_structure` | Get documentation directory structure |
| `get_doc_tree` | Get documentation tree structure |
| `find_related_docs` | Find documentation related to a code change |

## Traceability Tools

| Tool | Description |
|------|-------------|
| `get_traceability` | Get full traceability chain for a code element |
| `search_by_requirement` | Find code elements related to a requirement |

## Structure Tools

| Tool | Description |
|------|-------------|
| `get_code_tree` | Get codebase structure |
| `get_architecture` | Single-call architecture overview: languages, entry points, routes, clusters, hotspots, relationship summary, knowledge count, total element/file counts. Replaces running 5+ individual queries. Optional `max_items` argument caps each array section for token budget control; `truncated_sections` reports which sections were trimmed. |
| `get_graph_schema` | Single-call graph schema overview: element type counts and relationship type counts. Use to discover available patterns before running targeted queries. Optional `max_items` argument caps each array section for token budget control; `truncated_sections` reports which sections were trimmed. |
| `find_dead_code` | Find functions with zero callers and no `tested_by` edge, excluding common entry-point names (`main`, `Main`, `start`, `serve`, `Start`) and trivial bodies. Returns `dead_functions[]`, `count`, and the `min_lines` threshold that was applied. Argument: `min_lines` (default 10). |

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

## Semantic Retrieval (optional, `embeddings` feature)

These tools ship only when LeanKG is built with `--features embeddings`. They add vector retrieval + cross-encoder rerank + adaptive graph traversal on top of the existing keyword/graph search.

| Tool | Description |
|------|-------------|
| `kg_semantic_context` | Vector retrieve → rerank → traverse. Best for natural-language questions where keyword search misses (e.g., 'where do we validate access rights'). Returns ranked seed nodes plus 1-2 hop graph context. |

Setup (one-time):

```bash
cargo run --release --features embeddings -- embed --init        # pre-download models (~700MB)
cargo run --release --features embeddings -- embed               # build the embedding index
```

Then call from any MCP client:

```json
{ "tool": "kg_semantic_context", "arguments": { "query": "where is refund failure handled" } }
```

Optional arguments: `env` (default `local`), `top_k` (default 50), `rerank_top_n` (default 10), `traverse` (default true), `include_worktrees` (default false), `debug` (default false).

Response shape (debug=false): `{ query, env, seeds[], traversed[] }`. With `debug=true`: adds `diagnostics` with reranker status, candidate counts, per-stage latency, and the edges traversed.

Behavior notes:
- If the reranker fails to load, the tool silently falls back to ANN-order top-N (Q4 option A). `diagnostics.reranker = "fallback_ann"` surfaces this.
- If the embedding index is older than the last `index` run, `diagnostics.embeddings_stale = true` (still serves, just warns).
- Worktree scratch copies (`.worktrees/`, `.claude/worktrees/`, `.opencode/worktrees/`) are filtered out by default to avoid duplicate-noise results.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Fallback

If the MCP server reports "LeanKG not initialized", manually run `leankg init` in your project directory, then restart the AI tool.

## Path Normalization

LeanKG automatically handles path formats with or without `./` prefix. For example, these are equivalent:
- `src/main.rs`
- `./src/main.rs`

This applies to all query tools: `get_dependencies`, `get_dependents`, `get_impact_radius`, `get_call_graph`, etc.
