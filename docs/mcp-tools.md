# MCP Tools

LeanKG exposes a comprehensive set of MCP tools for AI tools to query the knowledge graph.

**Total: 36 MCP tools**

## Core Tools

| Tool | Description |
|------|-------------|
| `mcp_init` | Initialize LeanKG project (creates .leankg/ and leankg.yaml) |
| `mcp_index` | Index codebase (path, incremental, lang, exclude options) |
| `mcp_index_docs` | Index documentation directory for code-doc traceability |
| `mcp_install` | Create .mcp.json for MCP client configuration |
| `mcp_status` | Show index statistics and status |
| `mcp_impact` | Calculate blast radius for a file |
| `mcp_hello` | Returns 'Hello, World!' (connectivity test) |

## Query Tools

| Tool | Description |
|------|-------------|
| `query_file` | Find file by name or pattern |
| `find_function` | Locate function definition |
| `search_code` | Search code elements by name/type |
| `get_call_graph` | Get function call chain (full depth) |
| `get_callers` | Find all functions that call a given function |
| `find_large_functions` | Find oversized functions by line count |

## Dependency Analysis

| Tool | Description |
|------|-------------|
| `get_dependencies` | Get file dependencies (direct imports) |
| `get_dependents` | Get files depending on target |
| `get_impact_radius` | Get all files affected by change within N hops |
| `detect_changes` | Pre-commit risk analysis (critical/high/medium/low) |
| `get_review_context` | Generate focused subgraph + structured review prompt |

## Context Tools

| Tool | Description |
|------|-------------|
| `get_context` | Get AI context for file (minimal, token-optimized) |
| `get_tested_by` | Get test coverage for a function/file |
| `orchestrate` | Smart context orchestration with caching |
| `ctx_read` | Read file with compression modes (adaptive/full/map/signatures/diff/aggressive/entropy/lines) |

## Documentation Tools

| Tool | Description |
|------|-------------|
| `generate_doc` | Generate documentation for file |
| `get_doc_for_file` | Get documentation files referencing a code element |
| `get_files_for_doc` | Get code elements referenced in a documentation file |
| `get_doc_structure` | Get documentation directory structure |
| `get_doc_tree` | Get documentation tree structure with hierarchy |
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
| `get_clusters` | Get all clusters (functional communities) |
| `get_cluster_context` | Get all symbols in a cluster with entry points |

## Export Tools

| Tool | Description |
|------|-------------|
| `generate_graph_report` | Generate comprehensive graph report |
| `export_graph` | Export the knowledge graph (json/html/svg/graphml/neo4j) |

## Auto-Initialization

When the MCP server starts without an existing LeanKG project, it automatically initializes and indexes the current directory. This provides a "plug and play" experience for AI tools.

## Auto-Indexing

When the MCP server starts with an existing LeanKG project, it checks if the index is stale (by comparing git HEAD commit time vs database file modification time). If stale, it automatically runs incremental indexing to ensure AI tools have up-to-date context.

## Fallback

If the MCP server reports "LeanKG not initialized", manually run `leankg init` in your project directory, then restart the AI tool.

## Path Normalization

LeanKG automatically handles path formats with or without `./` prefix. For example, these are equivalent:
- `src/main.rs`
- `./src/main.rs`

This applies to all query tools: `get_dependencies`, `get_dependents`, `get_impact_radius`, `get_call_graph`, etc.

## Compression Modes

For `ctx_read` and `orchestrate` tools:

| Mode | Description |
|------|-------------|
| `adaptive` | Auto-select based on file size |
| `full` | Full file content |
| `map` | Map/signature view |
| `signatures` | Function signatures only |
| `diff` | Diff-like view |
| `aggressive` | Maximum compression |
| `entropy` | High-entropy regions |
| `lines` | Specific lines (requires `lines` parameter) |

## Impact Radius Severity Classification

When using `get_impact_radius`, results are classified by severity:

| Severity | Criteria |
|----------|----------|
| `WILL BREAK` | >= 10 dependents at depth 1, or depth 0 changes |
| `LIKELY AFFECTED` | >= 5 dependents, or public API changed |
| `MAY BE AFFECTED` | 2-4 dependents, or cross-module dependency |
| `LOW RISK` | <= 1 dependent within single cluster |

## Pre-commit Risk Analysis

The `detect_changes` tool analyzes git diffs:

- **critical**: >= 10 dependents at depth 1
- **high**: >= 5 dependents or public API changed
- **medium**: 2-4 dependents or cross-module dependency
- **low**: <= 1 dependent within single cluster
