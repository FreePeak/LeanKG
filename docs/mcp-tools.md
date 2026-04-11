# MCP Tools

LeanKG exposes a comprehensive set of MCP tools for AI tools to query the knowledge graph.

## Core Tools

| Tool | Description |
|------|-------------|
| `mcp_init` | Initialize LeanKG project (creates .leankg/, leankg.yaml) |
| `mcp_index` | Index codebase (path, incremental, lang, exclude options) |
| `mcp_install` | Create .mcp.json for MCP client configuration |
| `mcp_status` | Show index statistics and status |
| `mcp_impact` | Calculate blast radius for a file |

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
| `get_doc_for_file` | Get documentation files referencing a code element |
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
