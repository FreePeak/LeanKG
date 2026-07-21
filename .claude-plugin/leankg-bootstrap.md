# LeanKG - Lightweight Knowledge Graph

LeanKG is a lightweight knowledge graph for codebase understanding. It indexes code, builds dependency graphs, calculates impact radius, and exposes everything via MCP for AI tool integration.

## Multi-Project Support

LeanKG uses a single HTTP server supporting multiple projects. Each tool accepts a `project` parameter to route queries to the correct `.leankg` database.

**Always pass `project="/path/to/project/root"`** to ensure the server queries the correct project database.

## MCP Tools (all accept `project` parameter)

| Tool | Purpose |
|------|---------|
| `mcp_status` | Check if LeanKG is initialized and ready |
| `mcp_init` | Initialize LeanKG for a project |
| `mcp_index` | Index codebase |
| `search_code` | Search code elements by name/type |
| `find_function` | Locate function definitions |
| `query_file` | Find files by name/pattern |
| `get_impact_radius` | Calculate blast radius of changes (N hops) |
| `get_dependencies` | Get direct imports of a file |
| `get_dependents` | Get files depending on target |
| `get_context` | Get AI-optimized context for a file |
| `get_call_graph` | Get function call chains |
| `find_large_functions` | Find oversized functions |
| `get_tested_by` | Get test coverage for a function/file |
| `get_overview_context` | Session-start L0+L1 overview |
| `find_related_docs` | Find documentation related to a code change |
| `get_traceability` | Get full traceability chain |
| `get_code_tree` | Get codebase structure |
| `get_doc_tree` | Get documentation tree |
| `get_clusters` | Get functional clusters |
| `detect_changes` | Pre-commit risk analysis |

## Workflow: LeanKG First, Grep Fallback

**MANDATORY: Use LeanKG First**

Before ANY codebase search/navigation, you MUST:

1. Check if LeanKG is available via `mcp_status(project="/project/root")`
2. If LeanKG is not initialized, run `mcp_init(path="/project/root/.leankg")` first
3. Use the appropriate LeanKG tool with `project="/project/root"` for the task
4. **ONLY after LeanKG is exhausted (returns empty) may you fall back to grep/ripgrep**

| Instead of | Use LeanKG | Grep Fallback |
|------------|------------|---------------|
| grep/ripgrep for "where is X?" | `search_code(query="X", project="/path")` | `grep -rn "X" --include="*.rs"` |
| glob + content search for tests | `get_tested_by(file="X", project="/path")` | `grep -rn "X" tests/` |
| Manual dependency tracing | `get_impact_radius(file="X", project="/path")` | N/A |
| Reading entire files | `get_context(file="X", project="/path")` | `cat file.rs` |

## Auto-Init Behavior

LeanKG automatically initializes on first use:
- If `.leankg` does not exist, it creates one automatically
- If index is stale (>5 min since last git commit), it re-indexes automatically
- Set `auto_index_on_start: false` in `leankg.yaml` to disable
