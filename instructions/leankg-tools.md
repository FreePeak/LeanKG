# LeanKG MCP Tools - Agent Guide

## Core Principle

LeanKG is a **pre-built knowledge graph** of the codebase. Always query it first — never grep/ripgrep unless the tool returns no results.

---

## Tool Selection Flowchart

```
User asks about codebase → mcp_status (check initialized)
  │
  ├─ "Where is X?" / "Find Y" ───────────────► search_code or find_function
  │   ├─ by name/type ─────────────────────────► search_code(query="X")
  │   └─ exact function ───────────────────────► find_function(name="parseJson")
  │                                              scope to file: find_function(name="foo", file="src/bar.rs")
  │
  ├─ "What breaks if I change X?" ────────────► get_impact_radius(file="X", depth=2)
  │   └─ use depth<=2 for token budgets (depth=3 returns hundreds of nodes)
  │
  ├─ "How does X work?" / call chain ─────────► get_call_graph(function="X")
  │   └─ keep depth≤2, avoid depth>3 (neighbor explosion)
  │
  ├─ "Who calls X?" / callers ────────────────► get_callers(function="X")
  │
  ├─ "What does X import/use?" ───────────────► get_dependencies(file="X")
  ├─ "What uses X?" ──────────────────────────► get_dependents(file="X")
  │
  ├─ "Show me file context" / read large file ─► ctx_read(file="X", mode=adaptive)
  │   └─ modes: adaptive, signatures (smallest), full, map, diff, lines("1-20,30-40")
  │
  ├─ "Get minimal AI context for prompt" ─────► get_context(file="X", signature_only=true)
  │
  ├─ "What tests cover X?" ───────────────────► get_tested_by(file="X")
  │
  ├─ "Show me all files/folders" ─────────────► get_code_tree(limit=50)
  │
  ├─ "Find oversized functions" ──────────────► find_large_functions(min_lines=50, limit=20)
  │
  ├─ Natural language query (any of the above) ─► orchestrate(intent="...")
  │   └─ file param is OPTIONAL — only needed for impact/dependency queries
  │      e.g. orchestrate(intent="show me impact of changing src/lib.rs", file="src/lib.rs")
  │
  ├─ "What docs reference X?" ─────────────────► get_doc_for_file(file="X")
  ├─ "What code is in this doc?" ─────────────► get_files_for_doc(doc="docs/X.md")
  │
  └─ Pre-commit risk check ───────────────────► detect_changes(scope="staged"|"all")
```

---

## Smart Shortcut: `orchestrate`

Use when you want LeanKG to pick the best tool automatically. Only requires `intent`:

| Intent Pattern | What It Does |
|----------------|-------------|
| "show me impact of changing X" | Impact radius analysis |
| "get context for file X" | Token-optimized file context |
| "find function named X" | Function location search |
| "what does module X do?" | Cluster + dependency summary |

**Parameters:** `intent` (required), `file` (optional — only needed when intent references a specific file for impact/dependency queries), `mode` (adaptive/full/map/signatures), `fresh` (bypass cache)

---

## Token Optimization Tips

| Scenario | Tool + Params |
|----------|--------------|
| Read large file (>50 lines) | `ctx_read(file="X", mode=signatures)` — 80-90% token savings |
| Impact analysis | `get_impact_radius(file="X", depth=2, compress_response=true)` |
| Call graph | `get_call_graph(function="X", max_results=30)` |
| File context for prompt | `get_context(file="X", signature_only=true, max_tokens=4000)` |

---

## Anti-Patterns (Don't Do These)

- **grep before LeanKG** — The graph is pre-built and faster
- **depth>2 on get_impact_radius** — Returns hundreds of nodes, wastes tokens
- **depth>3 on get_call_graph** — Neighbor explosion
- **Reading full files with ctx_read mode=full** — Use signatures or adaptive for large files
- **Calling orchestrate without intent** — intent is the only required param

---

## Path Formats (All Equivalent)

```
src/main.rs      ./src/main.rs      src/lib.rs::parse_config
```

Works across all tools. No need to worry about `./` prefix or absolute paths.
