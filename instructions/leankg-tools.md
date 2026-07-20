# LeanKG MCP Tools - Agent Guide

## Core Principle

LeanKG is a **pre-built knowledge graph**. Prefer it when MCP HTTP `:9699` is healthy. If health fails, use Grep/Glob/Read — do **not** call LeanKG or `mcp_init`.

Gate:

```bash
curl -sf --max-time 2 http://localhost:9699/health
```

Docker MCP: pass container `project=` (e.g. `/workspace`), not a host Mac path.

---

## Semantic Discovery (prefer-order — FR-SURF-02)

**Search triple:** `concept_search` → `semantic_search` → `search_code`  
**Semantic triple:** `semantic_search` → `kg_semantic_context` → `kg_context`

1. `concept_search(query="...")` — domain concepts / aliases first.
2. `semantic_search(query="...", limit=20, offset=0)` — HNSW+rerank when embeddings exist, else ontology-first.
3. `search_code(query="...")` / `find_function(name="...")` — name/type filters after the above.
4. Then exact tools on hits: `get_context`, `get_impact_radius`, `get_dependencies`, …
5. `kg_semantic_context(query="...", env="local")` — ranked seeds + hop graph (needs embeddings).
6. `kg_context(query="...")` — ontology expand without vectors.

---

## Tool Selection Flowchart

```
User asks about codebase → curl :9699/health
  │ fail → Grep/Glob/Read
  │ ok → mcp_status(project=…)
  │
  ├─ NL / domain "Where is auth?" ───────────► concept_search → semantic_search
  │                                              then get_context on a hit
  ├─ Exact name "Find ProcessOrder" ─────────► find_function / search_code
  │
  ├─ "What breaks if I change X?" ───────────► get_impact_radius(file="X", depth=2)
  │   └─ use depth<=2 for token budgets (depth=3 returns hundreds of nodes)
  │
  ├─ "How does X work?" / call chain ────────► get_call_graph(function="X")
  │   └─ keep depth≤2, avoid depth>3 (neighbor explosion)
  │
  ├─ "Who calls X?" / callers ───────────────► get_callers(function="X")
  │
  ├─ "What does X import/use?" ──────────────► get_dependencies(file="X")
  ├─ "What uses X?" ─────────────────────────► get_dependents(file="X")
  │
  ├─ "Show me file context" / read large file ► ctx_read(file="X", mode=adaptive)
  │   └─ modes: adaptive, signatures (smallest), full, map, diff, lines("1-20,30-40")
  │
  ├─ "Get minimal AI context for prompt" ────► get_context(file="X", signature_only=true)
  │
  ├─ "What tests cover X?" ──────────────────► get_tested_by(file="X")
  │
  ├─ "Show me all files/folders" ────────────► get_code_tree(limit=50)
  │
  ├─ "Find oversized functions" ─────────────► find_large_functions(min_lines=50, limit=20)
  │
  ├─ Natural language router ────────────────► orchestrate(intent="...")
  │   └─ file param optional — only for impact/dependency intents
  │
  ├─ "What docs reference X?" ───────────────► find_related_docs(file="X")
  ├─ "What code is in this doc?" ────────────► get_files_for_doc(doc="docs/X.md")
  │
  └─ Pre-commit risk check ──────────────────► detect_changes(scope="staged"|"all")
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
1. Add volume mount to `docker-compose.rocksdb.yml`:
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
