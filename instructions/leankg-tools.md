# LeanKG MCP Tools - Agent Guide

## Core Principle

LeanKG is a **pre-built knowledge graph** of the codebase. Always query it first — never grep/ripgrep unless the tool returns no results.

---

## Semantic Discovery (v3.6.2 — CozoDB HNSW preferred)

When the binary was built with `--features embeddings` AND the embedding
index has been built (`leankg embed` after `leankg index`), prefer the
**HNSW-backed** vector retrieval tools. They return semantically similar
code, ontology nodes, and graph context, ranked by cross-encoder rerank:

1. `kg_semantic_context(query="...", env="local")` — best for natural-language questions ("where do we validate access rights", "how does the refund flow work"). Returns ranked seed nodes + 1-2 hop graph context.
2. `semantic_search(query="...", limit=20, offset=0)` — paginated ontology+HNSW fallback; pagination is the safe default on mega-graphs.

If `kg_semantic_context` returns "No embedded vectors found", fall back
to the ontology layer:

3. `kg_context(query="...")` — ontology-aware concept expansion (no embeddings required).
4. `search_code(query="...")` — bounded name search.

---

## Tool Selection Flowchart

```
User asks about codebase → mcp_status (check initialized)
  │
  ├─ "Where is X?" / "Find Y" (fuzzy / NL / domain) ─► concept_search → semantic_search
  │   └─ by name/type ─────────────────────────► search_code(query="X")
  │
  ├─ "What breaks if I change X?" ────────────► get_impact_radius(file="X", depth=2)
  │   └─ use depth<=2 for token budgets (depth=3 returns hundreds of nodes)
  │
  ├─ "How does X work?" / call chain ─────────► get_call_graph(function="X")
  │   └─ keep depth≤2, avoid depth>3 (neighbor explosion)
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
  ├─ "What connects X to Y?" (NL subgraph) ───► query_graph(question="...", token_budget=2000)
  │
  ├─ "What docs reference X?" ─────────────────► find_related_docs(file="X")
  ├─ "What code is in this doc?" ─────────────► get_files_for_doc(doc="docs/X.md")
  │
  └─ Pre-commit risk check ───────────────────► detect_changes(scope="staged"|"all")
```

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

---

## Path Formats (All Equivalent)

```
src/main.rs      ./src/main.rs      src/lib.rs::parse_config
```

Works across all tools. No need to worry about `./` prefix or absolute paths.

---

## Multi-Project Setup (HTTP/SSE Server)

LeanKG supports multiple projects through a single Docker-based HTTP server.

### How Routing Works

The server identifies which project database to use via the `?project=` URL query parameter:

| URL | Project |
|-----|---------|
| `http://host:9699/mcp` | Default project (where server started) |
| `http://host:9699/mcp?project=/workspace-foo` | Side-by-side project mounted at `/workspace-foo` |
| `http://host:9699/mcp?project=/workspace-new` | Custom project |

The side-by-side project path is whatever the user configured in their
local `.dockerfile` (see `.dockerfile.example`); the canonical example
used in this repo's docker-compose is `/workspace`.

### Registering a New Project Directory

**Option A: Docker volume mount**
1. Add volume mount to `docker-compose.yml`:
   ```yaml
   volumes:
     - /host/path/to/project:/workspace-new
   ```
2. Restart: `docker compose restart`
3. Auto-discovery entrypoint detects the new `.leankg` directory and indexes it.

**Option B: Via MCP tools (from AI agent)**
1. Call `mcp_init(path="/workspace-new")` to create `.leankg/leankg.yaml`
2. Call `mcp_index(path="/workspace-new")` to index all files
3. All subsequent queries use `?project=/workspace-new` for that project

**Option C: Via CLI (Docker exec)**
```bash
docker exec leankg-leankg-1 leankg index /workspace-new
```

### Adding MCP Config for a New Project Tool

Each AI tool (opencode, Claude, Cursor) needs the `?project=` param in its MCP URL:

```json
// .mcp.json or equivalent config
{
  "mcpServers": {
    "leankg": {
      "url": "http://localhost:9699/mcp?project=/workspace-new"
    }
  }
}
```

Without the param, the server defaults to the project it was started in (`/workspace`).
