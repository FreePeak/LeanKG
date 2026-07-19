# LeanKG - AI Agent Context

## Project Overview

LeanKG is a lightweight knowledge graph for codebase understanding. It indexes code, builds dependency graphs, calculates impact radius, and exposes everything via MCP for AI tool integration.

**Tech Stack:** Rust + CozoDB + tree-sitter + MCP

## Quick Start

```bash
# Index a codebase
cargo run -- init
# Optional: prefab LSP + typed_resolve=go,ts (FR-LSP-B)
cargo run -- init --with-lsp
cargo run -- index ./src

# Calculate impact radius
cargo run -- impact src/main.rs 3

# Start MCP server (stdio transport -- for local AI tool integration)
cargo run -- mcp-stdio --watch

# Start MCP server (HTTP/SSE transport -- for remote clients)
cargo run -- mcp-http --port 9699
```

### Hybrid typed resolve (Go / TypeScript MVP)

When `indexer.typed_resolve` is `go,ts` (or `all`), indexing builds a cross-file type registry and upgrades CALLS edges to `resolution_method=typed` without spawning language servers. External LSP (`resolve_with_lsp` / `leankg lsp-resolve`) remains available when servers are installed; `leankg init --with-lsp` writes the prefab `lsp:` block from the server catalog.
## Development Workflow

**When implementing features, follow:** `docs/workflow-opencode-agent.md`

### Pattern: Update Docs -> Implement -> Test -> Commit -> Push -> Bump Version -> Tag

1. **Update docs first** - Consolidated PRD+HLD (`docs/prd.md`) for narrative/ACs/HLD. For **all task lists + Done/Pending status**, use [`docs/prd-task-tracker.md`](docs/prd-task-tracker.md) only — do not scan or reintroduce status tables in the PRD.
2. **Implement** - Follow patterns in `docs/workflow-opencode-agent.md`
3. **Build & test** - `cargo build && cargo test`
4. **Commit** - `git commit -m "feat: description"` (one feature per commit)
5. **Push** - `git pull --rebase && git push`
6. **Bump version** - Update `version` in `Cargo.toml`
7. **Tag** - `git tag -a v<version> -m "Release v<version>" && git push origin v<version>` (after version bump)

### Parallel Subagent Workflow

When facing 3+ independent tasks that can work in parallel without shared state:

1. **Dispatch multiple subagents** - One agent per independent problem domain
2. **Each agent works in isolated `.worktree/`** - Prevents interference between agents
3. **Each worktree uses feature branch** - Format: `.worktree/<feature-name>/`
4. **Verify isolation** - Confirm directory is in `.gitignore`
5. **Run baseline tests** - Ensure clean starting point per worktree
6. **Agent completes independently** - Agent returns summary of changes
7. **Merge to main** - After all agents complete, merge each feature branch to main

```
# Example workflow
Agent 1 -> .worktree/feature-a/ (works on tests in file_a.test.ts)
Agent 2 -> .worktree/feature-b/ (works on tests in file_b.test.ts)
Agent 3 -> .worktree/feature-c/ (works on tests in file_c.test.ts)

# After all complete
git checkout main
git merge feature-a
git merge feature-b
git merge feature-c
git push
```

**When to use:**
- 3+ test files failing with different root causes
- Multiple subsystems broken independently
- Each problem can be understood without context from others

**When NOT to use:**
- Failures are related (fix one might fix others)
- Need to understand full system state
- Agents would interfere with each other

## Key Commands

```bash
cargo build      # Build project
cargo test       # Run tests
cargo run -- <cmd>  # Run CLI commands
```

## Phase 1 Structural Parity Tools (v2.0 PRD)

Three new MCP tools are available once a project is indexed:

- `get_architecture` — single-call overview (languages, entry points, routes, clusters, hotspots, relationship summary, knowledge count).
- `get_graph_schema` — element type and relationship type counts.
- `find_dead_code` — functions with no callers and no `tested_by` edge, with a `min_lines` threshold.

See [`docs/mcp-tools.md`](docs/mcp-tools.md) → Structure Tools and [`docs/roadmap.md`](docs/roadmap.md) → Phase 1. Requirements: [`docs/prd.md`](docs/prd.md) Sections 3.11 / 5.10.

## MANDATORY: LeanKG MCP project paths (Docker vs host)

Cursor agents talking to LeanKG MCP over HTTP (`localhost:9699`, Docker RocksDB) **must** pass **container mount paths** as `project=`. Host filesystem paths do not match RocksDB project keys and return "not initialized" even when the graph is indexed.

| Target codebase | Correct `project=` | Wrong (will fail against Docker MCP) |
|-----------------|--------------------|--------------------------------------|
| This LeanKG repo (`$PWD` → `/workspace`) | `/workspace` | `/Users/.../leankg`, `./.leankg`, absolute Mac path |
| Extra monorepo bind (`…:/workspace-other`) | `/workspace-other` | that repo's host path |
| Optional freepeak bind (`…:/workspace-freepeak`) | `/workspace-freepeak` | that tree's host path |

**Required pattern for every MCP call in this repo:**

```
mcp_status(project="/workspace")
search_code(query="…", project="/workspace")
find_function(name="…", project="/workspace")
get_context(file="…", project="/workspace")
```

**Agent checklist (new chat / no prior context):**

1. Prefer Docker MCP when `curl http://localhost:9699/health` is ok
2. Call `mcp_status(project="/workspace")` first for LeanKG source work
3. For other indexed trees, use the container side of binds listed in local `LEANKG_PROJECT_DIRS` (gitignored `.dockerfile`)
4. Never invent or paste personal host bind paths into commits, docs, or chat
5. Host-path `mcp_init` / local SQLite is only for non-Docker stdio workflows

Chat sessions do **not** share memory. This section is the durable source of truth so agents do not re-discover mounts every session.

## MCP Server Transport Modes

LeanKG supports two MCP transport modes:

### Stdio Transport (Local AI Tools)

For local AI tools (Cursor, Claude Code, opencode, etc.):

```bash
cargo run -- mcp-stdio --watch
```

The stdio transport uses the per-project SQLite-backed CozoDB file at `<project>/.leankg/leankg.db`. Use host project paths only with this transport (not with Docker HTTP MCP).

### HTTP/SSE Transport (Remote Clients)

For remote clients or multi-repo setups:

```bash
# Single project
cargo run -- mcp-http --port 9699

# Multi-repo routing with auth
cargo run -- mcp-http --port 9699 --auth "my-secret-token" --project /path/to/project
```

HTTP endpoints:
- `POST /mcp` -- JSON-RPC endpoint
- `GET /mcp/stream` -- SSE (Server-Sent Events) stream
- `GET /health` -- Health check

Environment variables:
- `MCP_HTTP_PORT` -- Override port (default: 9699)
- `MCP_HTTP_AUTH` -- Bearer token for authentication

When the HTTP server runs **inside Docker**, `--project` / MCP `project` must be the **container** path (`/workspace`, `/workspace-other`, …), matching bind mounts and `LEANKG_PROJECT_DIRS`.

## RocksDB Docker Deployment

The HTTP/SSE MCP server supports optional centralized RocksDB storage, useful when a single long-running server handles multiple projects.

### Embed resume rule (P0 — day-2)

**If embedding data already exists** on the named RocksDB volume → **resume** (skip `fresh` vectors; delta only).  
**If no embedding data exists** for that project → **cold/fresh** fill is allowed.

This applies to **every** path that starts embed:

| Path | Env / command |
|------|----------------|
| Standalone | `docker run … embed --wait --project /workspace-other` |
| In-process MCP | `LEANKG_EMBED_BACKGROUND=1` |
| Legacy boot | `LEANKG_EMBED_ON_BOOT=1` |
| Offline setup | `LEANKG_DOCKER_SETUP=1` |
| One-line up | `scripts/docker-up.sh` |

Turning embed on later (or restarting the container with the same volume) must **not** wipe or full-rebuild. Intentional full rebuild only via `--full`, `LEANKG_EMBED_BACKGROUND_FULL=1`, or `LEANKG_FORCE_REINDEX=1`. Product ACs: [`docs/prd.md`](docs/prd.md) §3.15 / §5.16 / §8.5.

**Mega-graph MCP auto-index OOM escape:** set `LEANKG_SKIP_FRESHNESS_CHECK=1` and/or `LEANKG_AUTO_INDEX=0` (or `mcp.auto_index_on_start: false` in that project's `leankg.yaml`) so Docker MCP does not reindex hundreds of thousands of elements on every start. For serving ~150k+ embedding vectors, use MCP **`mem_limit: 6g`**, **`mem_reservation: 3g`**, **`cpus: "6"`** (compose defaults in `docker-compose.rocksdb.yml`; offline embed in `docker-compose.embed.yml`). Prefer offline `embed` / `index` when you choose. See [`docs/reports/embed-3-workspaces-2026-07-17.md`](docs/reports/embed-3-workspaces-2026-07-17.md) and FR-MG-AUTO-01 / FR-OPS-EMBED-CPU.

**Docker PID 1 + `embed.lock`:** MCP runs as PID 1 in the container. A leftover `<project>/.leankg/embed.lock` containing `1` from a killed prior run used to look “alive” forever and skip `LEANKG_EMBED_BACKGROUND` spawn. Current code treats same-PID locks as stale unless an in-process embed is already active in this process (and clears non-`running` status leftovers). Manual escape: `rm -f "$LEANKG_MCP_PROJECT/.leankg/embed.lock"` then recreate the container.

### One-line run (published image — no Rust)

Index + INT8 embed + MCP (recommended):

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/docker-up.sh | bash
```

MCP only (skip cold embed):

```bash
docker run -d --name leankg -p 9699:9699 \
  -v "$PWD:/workspace" \
  -v leankg-rocksdb:/data/leankg-rocksdb \
  -v leankg-models:/root/.cache/leankg \
  freepeak/leankg:latest
```

Hub: https://hub.docker.com/r/freepeak/leankg (`linux/arm64` tags `:latest` / `:0.18.2`).

### Single-project (build from source)

```bash
# Start with RocksDB in Docker
docker compose -f docker-compose.rocksdb.yml --env-file .dockerfile up --build

# Stop
docker compose -f docker-compose.rocksdb.yml down

# Clean up RocksDB volume
docker volume rm leankg_leankg-rocksdb
```

Environment variables for RocksDB (defaults are built into compose):
- `LEANKG_DB_ENGINE=rocksdb` -- Switch from SQLite to RocksDB
- `LEANKG_ROCKSDB_ROOT` -- Centralized storage root (default: `$HOME/.leankg-rocksdb`)

The MCP server selects its project via `LEANKG_MCP_PROJECT`; the entrypoint scans and auto-indexes any directory listed in `LEANKG_PROJECT_DIRS` (comma-separated, e.g. `/workspace,/workspace-other`).

### Multi-repo workspace roots (nested git)

Some mounts (e.g. a polyrepo directory that contains many service repos under `platform-*/*`) are **not** a git repository at the root. MCP auto-index still treats them as git-backed when nested `.git` directories are found (bounded depth scan). Freshness uses the latest `HEAD` commit time across nested repos; incremental indexing unions changed/untracked files from each nested repo with paths prefixed relative to the workspace root.

`require_git_for_auto_index: true` in `leankg.yaml` therefore passes when either:
- the project root itself is a git work tree, or
- nested git repositories exist under that root.

### Mega-graph / OOM-safe querying

Workspaces above `LEANKG_MAX_CACHE_ELEMENTS` (default **50_000** elements) are treated as mega-graphs:

- Discovery tools (`search_code`, `semantic_search`, `concept_search`, `query_file`) use **ontology-first + paginated** paths (`limit`/`offset`, max page 50).
- Full-scan tools (`get_clusters`, `get_code_tree` without query, nav dumps, annotation full scans) **refuse** with a redirect hint instead of loading 600k+ rows.
- Incremental auto-index **skips** full-graph dependent expansion on mega-graphs (override with `LEANKG_INCREMENTAL_SKIP_DEPENDENTS=1` to force skip always).
- Search prefer-order: `concept_search` → `semantic_search` → `search_code`. Semantic context: `semantic_search` → `kg_semantic_context` (embeddings) → `kg_context`.

Env knobs:

| Variable | Default | Purpose |
|----------|---------|---------|
| `LEANKG_MAX_CACHE_ELEMENTS` | 50000 | Mega-graph threshold + in-memory cache gate |
| `LEANKG_MAX_CLUSTER_ELEMENTS` | 50000 | Refuse Louvain clustering above this |
| `LEANKG_CODE_TREE_CAP` | 50000 | Cap for small-graph code tree DB fetch |
| `LEANKG_INCREMENTAL_SKIP_DEPENDENTS` | auto | Force-skip dependents in incremental index |

### Multi-project (side-by-side repos)

To serve additional repos (e.g. another project mounted at `/workspace-other` alongside the LeanKG source tree at `/workspace`):

1. Create `.dockerfile` (local-only, gitignored) — copy from `.dockerfile.example`. Set:
   ```bash
   HOST_PROJECT_PATH=/path/to/leankg
   CONTAINER_PROJECT_PATH=/workspace
   LEANKG_MCP_PROJECT=/workspace-other
   LEANKG_PROJECT_DIRS=/workspace,/workspace-other
   ```
   Note the **comma-separated** `LEANKG_PROJECT_DIRS` -- `entrypoint.sh` uses `IFS=','`.

2. Create `docker-compose.override.yml` (local-only, gitignored). The committed template adds the bind mount for the second repo:
   ```yaml
   services:
     leankg:
       volumes:
         - /Users/you/work/other-repo:/workspace-other
   ```


3. Start with the override file chained in:
   ```bash
   docker compose \
     -f docker-compose.rocksdb.yml \
     -f docker-compose.override.yml \
     --env-file .dockerfile \
     up -d
   ```

The override file's `volumes:` list is appended to the base compose, so the second bind mount appears alongside `/workspace` and the named RocksDB volume.

Without Docker (host machine):

```bash
export LEANKG_DB_ENGINE=rocksdb
export LEANKG_ROCKSDB_ROOT="$HOME/.leankg-rocksdb"
cargo build --release
target/release/leankg mcp-http --port 9699 --project /path/to/project
```

When `LEANKG_DB_ENGINE` is not set, LeanKG uses the default per-project SQLite storage at `<project>/.leankg/leankg.db`.

## UI Development

The UI is a Vite + React app in `<leankg-codebase>/ui/`:

```bash
cd <leankg-codebase>/ui && npm run dev    # Dev server at http://localhost:5173/ (hot reload)
cd <leankg-codebase>/ui && npm run build  # Production build
```

**Workflow after testing:**
```bash
cp -r <leankg-codebase>/ui/dist/* <leankg-codebase>/src/embed/
cargo build  # Rebuild Rust with new UI assets
```

**For full backend testing:**
```bash
cargo run -- serve      # Backend API at http://localhost:8080/
# Then open http://localhost:8080 in browser
```

## Important Files

| File | Purpose |
|------|---------|
| `src/lib.rs` | Module exports |
| `src/db/models.rs` | Data models (CodeElement, Relationship, BusinessLogic, RelationshipType) |
| `src/graph/query.rs` | Graph query engine |
| `src/mcp/tools.rs` | MCP tool definitions |
| `src/mcp/handler.rs` | MCP tool handlers |
| `src/indexer/extractor.rs` | Code parsing with tree-sitter |
| `src/indexer/microservice.rs` | Microservice gRPC call extraction |
| `config/microservice-extractor.yaml` | Default rules for microservice relationship extraction |

## Data Model

- **CodeElement** - Files, functions, classes with `qualified_name` (e.g., `src/main.rs::main`)
- **Relationship** - `imports`, `calls`, `tested_by`, `references`, `documented_by`, `service_calls`
- **ServiceCalls** - Microservice gRPC calls between services via DNS addresses
- **BusinessLogic** - Annotations linking code to business requirements

## MCP Tools

Core tools: `query_file`, `get_dependencies`, `get_dependents`, `get_impact_radius`, `get_review_context`, `find_function`, `get_call_graph`, `search_code`, `generate_doc`, `find_large_functions`, `get_tested_by`

Doc/Traceability tools: `get_files_for_doc`, `get_doc_structure`, `get_traceability`, `search_by_requirement`, `get_doc_tree`, `get_code_tree`, `find_related_docs`

Cluster tools: `get_clusters`, `get_cluster_context`

Risk tools: `detect_changes`

## Known Limitations

- **Android/Kotlin/XML search** - Search for Android-related code elements (Kotlin, XML layouts, AndroidManifest.xml) may return incomplete results. The indexer finds these files but search indexing has gaps.

## Verification Status

See `docs/implementation-feature-verification-2026-03-25.md` for test results.

---

*Last updated: 2026-07-17*
