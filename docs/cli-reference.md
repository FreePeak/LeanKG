# LeanKG CLI Reference

Complete reference for all LeanKG CLI commands.

## CLI Commands

| Command | Description |
|---------|-------------|
| `leankg version` | Show LeanKG version |
| `leankg init` | Initialize LeanKG in the current directory |
| `leankg init --with-lsp` | Initialize and write a prefab `lsp:` block (gopls / typescript-language-server / pyright / …) plus `indexer.typed_resolve: go,ts` |
| `leankg lsp-resolve <file> <line> <col>` | Resolve definition/references via LSP bridge (or hybrid fallback) |
| `leankg lsp-list` | List catalogued LSP servers |
| `leankg lsp-install <lang>` | Install the preferred LSP server for a language |
| `leankg index [path]` | Index source files at the given path |
| `leankg index` with `typed_resolve: go,ts` | Produce `resolution_method=typed` CALLS edges via in-process hybrid resolver (Go/TS MVP) |

| `leankg index --incremental` | Only index changed files (git-based) |
| `leankg index --lang go,ts,py,rs,java,kotlin` | Filter by language |
| `leankg index --exclude vendor,node_modules` | Exclude patterns |
| `leankg serve` | Start the MCP server (WebSocket) |
| `leankg serve --mcp-port 3000` | Custom MCP server port |
| `leankg mcp-stdio` | Start MCP server with stdio transport |
| `leankg impact <file> --depth N` | Compute blast radius for a file |
| `leankg status` | Show index statistics and status |
| `leankg generate` | Generate documentation from the graph |
| `leankg install` | Auto-install MCP config for AI tools |
| `leankg watch` | Start file watcher for auto-indexing |
| `leankg quality --min-lines N` | Find oversized functions by line count |
| `leankg query <text> --kind name` | Query the knowledge graph by name/type/rel/pattern/content |
| `leankg query "<question>" --kind subgraph` | US-GF-03 / FR-GF-06: NL scoped subgraph (same as `graph-query`) |
| `leankg graph-query "<question>"` | US-GF-03: seed → expand → budget trim subgraph with provenance labels |
| `leankg path <a> <b>` | US-GF-01: shortest path between two symbols |
| `leankg explain <symbol>` | US-GF-02: node dossier (degree, cluster, neighbors) |
| `leankg gods` | US-GF-05: top-degree god nodes |
| `leankg annotate <element> -d <desc>` | Add business logic annotation |
| `leankg link <element> <id>` | Link element to feature |
| `leankg search-annotations <query>` | Search business logic annotations |
| `leankg show-annotations <element>` | Show annotations for a specific element |
| `leankg trace --feature <id>` | Show feature-to-code traceability |
| `leankg find-by-domain <domain>` | Find code by business domain |
| `leankg export` | Export graph data as JSON |
| `leankg docs --tree` | Show documentation directory structure |
| `leankg docs --for <file>` | Show docs referencing a code file |
| `leankg docs --link <doc> <element>` | Link documentation to code element |
| `leankg trace <element>` | Show traceability chain for element |
| `leankg trace --requirement <id>` | Trace code for a requirement |

## Quick Start

```bash
# 1. Initialize LeanKG in your project
leankg init
# Optional: prefab LSP servers + typed_resolve=go,ts (FR-LSP-B / REL-039)
leankg init --with-lsp

# 2. Index your codebase
leankg index ./src

# 3. Start the MCP server (for AI tools)
leankg serve

# 4. Compute impact radius for a file
leankg impact src/main.rs --depth 3

# 5. Check index status
leankg status
```

## Auto-Indexing

```bash
# Start file watcher -- indexes changes automatically in background
leankg watch

# Incremental indexing -- only re-index changed files (git-based)
leankg index --incremental

# Filter by language
leankg index --lang go,ts,py,rs,java,kotlin

# Exclude patterns
leankg index --exclude vendor,node_modules,dist
```

## Multi-Project Setup (Docker Compose)

The containerized MCP server (RocksDB-backed, see `docker-compose.rocksdb.yml`) can serve multiple repositories side-by-side. Each repo gets its own auto-detected `?project=` route.

**Required layout:**

| What | Where | Why |
|------|-------|-----|
| `.dockerfile` | repo root (gitignored) | Holds host paths and per-project env vars |
| `docker-compose.override.yml` | repo root (gitignored) | Adds bind mounts for side repos |
| `LEANKG_PROJECT_DIRS` | inside `.dockerfile` | Comma-separated list of container paths to scan |

**Start command (multi-project):**

```bash
docker compose \
  -f docker-compose.rocksdb.yml \
  -f docker-compose.override.yml \
  --env-file .dockerfile \
  up -d
```

**`.dockerfile` template:**

```bash
HOST_PROJECT_PATH=/path/to/leankg
CONTAINER_PROJECT_PATH=/workspace
LEANKG_MCP_PROJECT=/workspace              # default project the MCP server serves
LEANKG_PROJECT_DIRS=/workspace,/workspace-other  # comma-separated!
```

**`docker-compose.override.yml` template:**

```yaml
services:
  leankg:
    volumes:
      - /host/path/to/other-repo:/workspace-other
```

The override is **required** for any side repo to be mounted -- `docker-compose.rocksdb.yml` only mounts the primary `HOST_PROJECT_PATH`.

Compose also publishes **8080** for `leankg serve` (UI v2 REST) alongside **9699** (MCP). Set `LEANKG_SERVE_HTTP=0` in `.dockerfile` for MCP-only.

If `LEANKG_PROJECT_DIRS` is unset, the entrypoint falls back to scanning `/workspace*`, `/test-project*` globs automatically.

## MCP Project Routing

When the HTTP server is started, every URL supports an optional `?project=` query parameter:

| URL | Routes to |
|-----|-----------|
| `http://host:9699/mcp` | `LEANKG_MCP_PROJECT` (or default) |
| `http://host:9699/mcp?project=/workspace-other` | `.leankg` DB inside `/workspace-other` |

AI tool MCP configs must include the `?project=` param so each project queries the correct database. See `docs/agentic-instructions.md` for examples.
