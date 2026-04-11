# Graphify-Inspired Features for LeanKG

**Date:** 2026-04-09
**Status:** Approved
**Author:** linh.doan
**Inspiration:** [Graphify](https://github.com/safishamsi/graphify) by safishamsi

---

## 1. Background

Graphify is a Python-based AI coding assistant skill that turns any folder of code, docs, papers, or images into a queryable knowledge graph. It features rich output (god nodes, surprising connections, interactive HTML), natural language graph queries, git hooks for auto-rebuild, and wiki generation.

LeanKG already has a strong foundation: tree-sitter indexing, CozoDB storage, MCP server, impact radius analysis, clustering, call graphs, and context compression. What LeanKG lacks is the **rich output layer** and **continuous sync** that make Graphify's graph immediately useful and always current.

This spec adapts Graphify's best features to LeanKG's Rust + CozoDB architecture in 4 phases.

## 2. Design Principles

1. **Each phase delivers standalone value** -- no phase is just groundwork for a later phase
2. **Build on existing LeanKG infrastructure** -- CozoDB queries, MCP handlers, cluster detection
3. **No new dependencies** unless absolutely necessary
4. **MCP-first** -- every feature exposed as MCP tool for AI tool integration
5. **Token-efficient** -- output designed for LLM context windows, not human browsing

## 3. Phase 1: Graph Report + Interactive HTML Export

### 3.1 Graph Report

Generate a `GRAPH_REPORT.md` file with structural insights about the codebase knowledge graph.

**New module:** `src/graph/report.rs`

#### God Nodes

Top N elements with highest degree (most relationships). Calculated by counting inbound + outbound edges per element.

```
Query:
  ?[element, degree] := *relationships{source: element}
  UNION
  ?[element, degree] := *relationships{target: element}
  :sort -degree
  :limit 10
```

Output per god node:
- Qualified name
- Element type (file/function/class)
- Total degree (in + out)
- Top relationship types (e.g., "called by 42 functions, imports 12 files")
- Cluster membership

#### Surprising Connections

Rank edges by a composite "surprise" score. Cross-module edges (source and target in different clusters) rank higher than intra-module. Cross-type edges (e.g., function-to-document) rank higher than same-type (function-to-function).

```
surprise_score = (
  0.4 * is_cross_cluster +
  0.3 * is_cross_type +
  0.2 * is_infrequent_relation_type +
  0.1 * (1.0 - confidence)
)
```

Output per connection:
- Source element + target element
- Relationship type
- Surprise score
- Plain-English explanation (e.g., "auth.rs::validate_token is called by 5 files outside the auth cluster")

#### Suggested Questions

Analyze graph structure to propose 4-5 questions the graph is uniquely positioned to answer. Based on:
- God nodes (high-degree elements worth exploring)
- Cross-cluster bridges (elements connecting functional areas)
- Untested critical paths (functions with high fan-out but no tested_by edges)

#### Token Benchmark

Compare total size of indexed source files (bytes) vs. graph query output size. Report compression ratio.

**Example output:**

```markdown
# LeanKG Graph Report

## God Nodes (Top 10)

| # | Element | Type | Degree | Cluster |
|---|---------|------|--------|---------|
| 1 | src/mcp/handler.rs::handle_tool_call | function | 47 | mcp |
| 2 | src/graph/query.rs::GraphEngine | class | 38 | graph |
| ... |

## Surprising Connections

1. **src/indexer/extractor.rs::extract_function** --calls--> **src/mcp/handler.rs::handle_tool_call**
   - Cross-cluster bridge (indexer -> mcp)
   - Score: 0.87

## Suggested Questions

1. "What functions in the indexer cluster are called by the MCP cluster?"
2. "Which critical functions lack test coverage?"
3. ...

## Token Benchmark

- Raw source: 847,312 bytes (52 files)
- Graph query output: 11,846 bytes
- Compression: 71.5x
```

### 3.2 Interactive HTML Export

Generate a self-contained `graph.html` with interactive visualization.

**New module:** `src/web/export_html.rs`

Features:
- vis.js network graph (embedded, no external dependencies)
- Node color by element type (file=blue, function=green, class=orange, import=gray)
- Node size by degree (god nodes are larger)
- Edge color by relationship type (calls=red, imports=blue, tested_by=green, references=purple)
- Filter by cluster/community
- Search by element name
- Click node to see details (qualified name, type, relationships)
- Clustering visualization (group nodes by cluster with background shading)

The HTML file is self-contained (inline CSS + JS + data) and can be opened in any browser.

### 3.3 CLI Commands

| Command | Description |
|---------|-------------|
| `leankg report` | Generate GRAPH_REPORT.md in project root |
| `leankg report --output <path>` | Generate report to custom path |
| `leankg export --format html` | Generate interactive graph.html |
| `leankg export --format json` | Export graph as JSON (existing behavior, formalized) |

### 3.4 MCP Tools

| Tool | Parameters | Returns |
|------|-----------|---------|
| `get_graph_report` | `include: ["god_nodes", "surprising_connections", "suggested_questions", "token_benchmark"]` (optional filters) | Structured JSON with all report sections |
| `export_graph` | `format: "html" \| "json"`, `output_path: string` (optional) | Path to generated file |

### 3.5 Data Model Changes

None. Uses existing `code_elements` and `relationships` tables.

## 4. Phase 2: Natural Language Graph Queries

### 4.1 Concept Query

`query "what connects X to Y?"` -- Finds the shortest path between two concepts in the graph.

**New module:** `src/graph/nl_query.rs`

Algorithm:
1. Find candidate nodes for X and Y using fuzzy name matching (existing `search_code` logic)
2. Run BFS from X candidates, track path
3. Return shortest path with all intermediate nodes and edge metadata
4. Include relation types, confidence scores, source locations

Output:
```
Path: auth.rs::validate_token -> session.rs::create_session -> db/query.rs::execute_query
  - auth.rs::validate_token --calls--> session.rs::create_session (confidence: 1.0)
  - session.rs::create_session --calls--> db/query.rs::execute_query (confidence: 0.95)
  Distance: 2 hops
  Cross-cluster: yes (auth -> session -> database)
```

### 4.2 Explain Element

`explain "function_name"` -- 360-degree context for any element.

Returns:
- All callers (who calls this)
- All callees (what this calls)
- All imports (what this imports)
- All dependents (who imports this)
- Test coverage (tested_by)
- Documentation (documented_by)
- Business logic annotations
- Cluster membership
- Related elements (same cluster)

### 4.3 Path Query

`path "NodeA" "NodeB"` -- Explicit shortest path between two named nodes.

Parameters:
- `source`: Element name or qualified name
- `target`: Element name or qualified name
- `max_depth`: Maximum search depth (default: 5, max: 10)
- `algorithm`: "bfs" (default) or "dfs"

### 4.4 CLI Commands

| Command | Description |
|---------|-------------|
| `leankg query --nl "what connects X to Y?"` | Natural language graph query |
| `leankg explain <element>` | 360-degree element context |
| `leankg path <source> <target>` | Shortest path between elements |

### 4.5 MCP Tools

| Tool | Parameters | Returns |
|------|-----------|---------|
| `nl_query` | `query: string`, `max_depth: int` (default 5) | Nodes and edges along the path |
| `explain_element` | `element: string` | 360-degree context JSON |
| `find_path` | `source: string`, `target: string`, `max_depth: int`, `algorithm: string` | Path with nodes and edges |

### 4.6 Data Model Changes

None. Builds on existing graph traversal infrastructure (`src/graph/traversal.rs`).

## 5. Phase 3: Git Hooks + Auto-Sync

### 5.1 Git Hooks

Install git hooks that auto-rebuild the graph on commit and branch switch.

**New module:** `src/cli/hooks.rs`

Hook scripts:
- **post-commit**: Runs `leankg index --incremental` after every commit
- **post-checkout**: Runs `leankg index --incremental` after every branch switch
- Both hooks exit with non-zero code on rebuild failure so git surfaces the error
- Hook scripts are shell scripts that call the `leankg` binary

```
#!/bin/sh
# LeanKG post-commit hook
leankg index --incremental --quiet
if [ $? -ne 0 ]; then
  echo "LeanKG: WARNING - incremental reindex failed" >&2
fi
```

### 5.2 Enhanced Watch Mode

Enhance existing file watcher (`src/watcher/`) for auto-sync.

Behavior:
- Code file saves trigger instant rebuild (AST extraction only -- LeanKG is already tree-sitter based, no LLM needed)
- Detects file changes via `notify` crate (already a dependency)
- Debounces rapid changes (100ms window)
- Prints status to terminal: "Reindexed: src/graph/report.rs (3 elements updated)"

### 5.3 CLI Commands

| Command | Description |
|---------|-------------|
| `leankg hooks install` | Install post-commit and post-checkout git hooks |
| `leankg hooks uninstall` | Remove installed hooks |
| `leankg hooks status` | Check if hooks are installed and their status |
| `leankg watch` | Start file watcher for auto-sync (enhanced) |

### 5.4 MCP Tools

None. Hooks and watch are local developer workflow, not exposed via MCP.

### 5.5 Data Model Changes

None.

## 6. Phase 4: Wiki Generation + Multi-format Export

### 6.1 Wiki Generation

Generate agent-crawlable wiki from the knowledge graph.

**New module:** `src/doc/wiki.rs`

Output structure:
```
wiki/
  index.md              # Overview of all clusters, god nodes, suggested questions
  cluster-mcp.md        # Article for the "mcp" cluster
  cluster-graph.md      # Article for the "graph" cluster
  cluster-indexer.md    # Article for the "indexer" cluster
  ...
```

**index.md** contains:
- Project overview (from leankg.yaml project name)
- Table of clusters with member count and description
- Top god nodes
- Suggested questions

**Per-cluster article** contains:
- Cluster label and member count
- Key elements (files, functions, classes)
- Internal relationships (calls, imports within cluster)
- External connections (bridges to other clusters)
- Test coverage summary
- Suggested questions specific to this cluster

### 6.2 Multi-format Export

**New module:** `src/graph/export.rs`

Formats:
- **SVG**: Static graph layout via petgraph layout algorithms
- **GraphML**: XML format for Gephi, yEd, and other graph analysis tools
- **Neo4j Cypher**: `.cypher` file with CREATE statements for Neo4j import
- **JSON**: Structured graph data (existing format, formalized)

### 6.3 CLI Commands

| Command | Description |
|---------|-------------|
| `leankg wiki` | Generate agent-crawlable wiki in `wiki/` directory |
| `leankg wiki --output <path>` | Generate wiki to custom directory |
| `leankg export --format svg` | Export graph as SVG |
| `leankg export --format graphml` | Export graph as GraphML |
| `leankg export --format neo4j` | Export as Neo4j Cypher |
| `leankg export --format json` | Export as JSON |

### 6.4 MCP Tools

| Tool | Parameters | Returns |
|------|-----------|---------|
| `generate_wiki` | `output_path: string` (optional) | Path to generated wiki directory |
| `export_graph` | `format: "html" \| "json" \| "svg" \| "graphml" \| "neo4j"`, `output_path: string` (optional) | Path to generated file |

### 6.5 Data Model Changes

None.

## 7. Implementation Priority

| Phase | Features | Estimated Scope | Dependencies |
|-------|----------|----------------|--------------|
| Phase 1 | Graph Report + HTML Export | 2 new modules, ~800 lines | None |
| Phase 2 | NL Queries (query, explain, path) | 1 new module, ~600 lines | Phase 1 (god nodes for NL queries) |
| Phase 3 | Git Hooks + Watch Enhancement | 1 new module, ~400 lines | None (independent of Phase 1-2) |
| Phase 4 | Wiki + Multi-format Export | 2 new modules, ~700 lines | Phase 1 (report data for wiki) |

## 8. New Files Summary

| File | Phase | Purpose |
|------|-------|---------|
| `src/graph/report.rs` | 1 | GraphReportGenerator (god nodes, surprising connections, suggested questions, token benchmark) |
| `src/web/export_html.rs` | 1 | Interactive HTML graph export with vis.js |
| `src/graph/nl_query.rs` | 2 | NLQueryEngine (concept query, explain, path) |
| `src/cli/hooks.rs` | 3 | Git hook installation and management |
| `src/doc/wiki.rs` | 4 | WikiGenerator (index.md + per-cluster articles) |
| `src/graph/export.rs` | 4 | MultiFormatExporter (SVG, GraphML, Neo4j, JSON) |

## 9. New MCP Tools Summary

| Tool | Phase | Description |
|------|-------|-------------|
| `get_graph_report` | 1 | Get god nodes, surprising connections, suggested questions |
| `export_graph` | 1,4 | Export graph in various formats |
| `nl_query` | 2 | Natural language graph traversal |
| `explain_element` | 2 | 360-degree element context |
| `find_path` | 2 | Shortest path between two elements |
| `generate_wiki` | 4 | Generate agent-crawlable wiki |

## 10. Testing Strategy

Each phase follows LeanKG's existing testing approach:

- **Unit tests**: `#[cfg(test)]` modules within each new file
- **Integration tests**: `tests/` directory for end-to-end flows
- **Test fixtures**: Use `tempfile::TempDir` for filesystem tests
- **Arrange-Act-Assert** pattern throughout

Phase-specific test focus:
- Phase 1: Verify god node ranking, surprise score calculation, HTML generation validity
- Phase 2: Verify path finding correctness, fuzzy matching, output formatting
- Phase 3: Verify hook script generation, hook install/uninstall idempotency
- Phase 4: Verify wiki structure, export format validity (GraphML XML, Cypher syntax)

## 11. Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| vis.js bundle size in HTML export | Embed minified vis.js inline (~200KB min+gzip); no CDN dependency, works offline |
| NL query ambiguity (multiple matching nodes) | Return ranked candidates, let caller choose |
| Git hooks conflicting with existing hooks | Append to existing hooks, don't overwrite |
| Large graph performance in HTML | Limit to top N nodes by degree, add pagination |
| Neo4j export syntax correctness | Validate generated Cypher with syntax checker |
