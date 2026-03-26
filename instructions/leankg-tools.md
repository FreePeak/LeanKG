# LeanKG Tools - Usage Instructions

## For AI Coding Agents (Cursor, OpenCode, etc.)

Use LeanKG tools **first** before performing any codebase search, navigation, or impact analysis.

---

## When to Use Each Tool

### Code Discovery & Search

| Task | Use This Tool |
|------|--------------|
| Find a file by name | `query_file` |
| Find a function definition | `find_function` |
| Search code by name/type | `search_code` |
| Get full codebase structure | `get_code_tree` |

### Dependency Analysis

| Task | Use This Tool |
|------|--------------|
| Get direct imports of a file | `get_dependencies` |
| Get files that import/use a file | `get_dependents` |
| Get function call chain (full depth) | `get_call_graph` |
| Calculate what breaks if file changes | `get_impact_radius` |

### Review & Context

| Task | Use This Tool |
|------|--------------|
| Generate focused review context | `get_review_context` |
| Get minimal AI context (token-optimized) | `get_context` |
| Find oversized functions | `find_large_functions` |

### Testing & Documentation

| Task | Use This Tool |
|------|--------------|
| Get test coverage for a function | `get_tested_by` |
| Get docs that reference a file | `get_doc_for_file` |
| Get code elements in a doc | `get_files_for_doc` |
| Get doc directory structure | `get_doc_structure` |
| Find docs related to a change | `find_related_docs` |

### Traceability & Requirements

| Task | Use This Tool |
|------|--------------|
| Get full traceability chain | `get_traceability` |
| Find code for a requirement | `search_by_requirement` |
| Get doc tree with hierarchy | `get_doc_tree` |

---

## Decision Flow

```
User asks about codebase →
  First check if LeanKG is initialized (mcp_status) →
    If not, use mcp_init first →
    Then use appropriate LeanKG tool →
      NEVER fall back to naive grep/search until LeanKG is exhausted
```

---

## Example Usage Patterns

**"Where is the auth function?"**
```
search_code("auth") or find_function("auth")
```

**"What tests cover this file?"**
```
get_tested_by({ file: "src/auth.rs" })
```

**"What would break if I change this file?"**
```
get_impact_radius({ file: "src/main.rs", depth: 3 })
```

**"How does X work end-to-end?"**
```
get_call_graph({ function: "src/auth.rs::authenticate" })
```

---

## Important Notes

- LeanKG maintains a **knowledge graph** of your codebase - use it instead of text search
- `get_impact_radius` calculates blast radius - always check before making changes
- `get_context` returns token-optimized output - use it for AI prompts
- Tools are pre-indexed and **much faster** than runtime grep/search
