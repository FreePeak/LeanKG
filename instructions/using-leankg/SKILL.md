---
name: using-leankg
description: >-
  Code search via LeanKG MCP when HTTP :9699 is healthy; otherwise skip LeanKG
  and use default Grep/Glob/Read. Invoke before code navigation when LeanKG may apply.
---

# LeanKG Code Search (HTTP-gated)

**LeanKG is preferred when the MCP HTTP server is up.** If health fails, exit this skill immediately and use default Cursor/editor tools.

## Gate (ALWAYS FIRST)

```bash
curl -sf --max-time 2 http://localhost:9699/health
```

| Result | Next step |
|--------|-----------|
| Success (2xx) | Continue with LeanKG MCP below |
| Fail / timeout / connection refused | **Exit skill.** Use `Grep`, `Glob`, `Read`. Do **not** call LeanKG MCP, `mcp_init`, or leankg CLI |

Re-check health only if the user asks or you have reason to believe the server came back.

---

## When HTTP is healthy: LeanKG MCP

### Project path (Docker vs host)

When talking to Docker MCP on `:9699`, pass the **container mount** as `project=`:

| Target | `project=` |
|--------|------------|
| This LeanKG repo | `/workspace` |
| Extra bind (compose override) | `/workspace-other` (or the container side of the bind) |

Do **not** pass a Mac host path (e.g. `/Users/.../leankg`) as `project` against Docker RocksDB.

### Prefer-order (discover → exact)

Natural-language / domain questions:

```
1. mcp_status(project=…)
2. concept_search(query=…)     # domain concepts first
3. semantic_search(query=…)    # HNSW ANN if embeddings exist
4. search_code / find_function # name/type fallback
5. get_context / get_impact_radius / get_dependencies / …
   on the returned qualified_name or file — never full-graph dumps
```

Exact symbol / file known:

```
mcp_status → find_function / query_file → get_context → impact/deps tools
```

### Finding Code

| Task | Tool | Example |
|------|------|---------|
| Domain / NL search | `concept_search` | `concept_search(query="authentication", project="/workspace")` |
| Semantic / meaning | `semantic_search` | `semantic_search(query="payment refund", project="/workspace")` |
| Name / type search | `search_code` | `search_code(query="Handler", project="/workspace")` |
| Function definition | `find_function` | `find_function(name="ProcessOrder", project="/workspace")` |
| File by pattern | `query_file` | `query_file(pattern="auth", project="/workspace")` |
| Callers / call graph | `get_callers` / `get_call_graph` | pass `project=` |

### Reading & Context (after discovery)

| Task | Tool |
|------|------|
| File / symbol context | `get_context` |
| Blast radius | `get_impact_radius` |
| Imports / dependents | `get_dependencies` / `get_dependents` |
| Tests | `get_tested_by` |
| NL subgraph | `query_graph` (frontier-local; mega-safe) |

### If mcp_status is not ready (but HTTP health was OK)

Try other known container mounts from `LEANKG_PROJECT_DIRS`, then fall back to default Grep/Glob/Read.

**Do not** run `mcp_init` or local CLI indexing as a substitute when the preferred path is Docker HTTP MCP.

### If LeanKG returns EMPTY results

Fall back to default mode: `Grep`, `Glob`, `Read`.

---

## Default mode (HTTP down OR LeanKG empty)

```
Grep / Glob / Read
```

No Tier 2 leankg CLI. No forced `mcp_init`. Default editor tools are enough.

**BAN:** Do not call LeanKG tools when `:9699` health failed.
**BAN:** Do not materialize full element/relationship tables on mega graphs — use keyed / ANN / frontier tools only.
