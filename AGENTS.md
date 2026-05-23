# LeanKG - AI Agent Context

## Project Overview

LeanKG is a lightweight knowledge graph for codebase understanding. It indexes code, builds dependency graphs, calculates impact radius, and exposes everything via MCP for AI tool integration.

**Tech Stack:** Rust + CozoDB + tree-sitter + MCP

## Quick Start

```bash
# Index a codebase
cargo run -- init
cargo run -- index ./src

# Calculate impact radius
cargo run -- impact src/main.rs 3

# Start MCP server (stdio transport -- for local AI tool integration)
cargo run -- mcp-stdio --watch

# Start MCP server (HTTP/SSE transport -- for remote clients)
cargo run -- mcp-http --port 9699
```

## Development Workflow

**When implementing features, follow:** `docs/workflow-opencode-agent.md`

### Pattern: Update Docs -> Implement -> Test -> Commit -> Push -> Bump Version -> Tag

1. **Update docs first** - PRD (`docs/requirement/prd-leankg.md`) -> HLD (`docs/design/hld-leankg.md`) -> README
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

## MCP Server Transport Modes

LeanKG supports two MCP transport modes:

### Stdio Transport (Local AI Tools)

For local AI tools (Cursor, Claude Code, opencode, etc.):

```bash
cargo run -- mcp-stdio --watch
```

The stdio transport uses the per-project SQLite-backed CozoDB file at `<project>/.leankg/leankg.db`. This is the default mode for local development.

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

## RocksDB Docker Deployment

The HTTP/SSE MCP server supports optional centralized RocksDB storage, useful when a single long-running server handles multiple projects:

```bash
# Start with RocksDB in Docker
docker compose -f docker-compose.rocksdb.yml up --build

# Stop
docker compose -f docker-compose.rocksdb.yml down

# Clean up RocksDB volume
docker volume rm leankg_leankg-rocksdb
```

Environment variables for RocksDB:
- `LEANKG_DB_ENGINE=rocksdb` -- Switch from SQLite to RocksDB
- `LEANKG_ROCKSDB_ROOT` -- Centralized storage root (default: `$HOME/.leankg-rocksdb`)

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

Doc/Traceability tools: `get_doc_for_file`, `get_files_for_doc`, `get_doc_structure`, `get_traceability`, `search_by_requirement`, `get_doc_tree`, `get_code_tree`, `find_related_docs`

Cluster tools: `get_clusters`, `get_cluster_context`

Risk tools: `detect_changes`

## Known Limitations

- **Android/Kotlin/XML search** - Search for Android-related code elements (Kotlin, XML layouts, AndroidManifest.xml) may return incomplete results. The indexer finds these files but search indexing has gaps.

## Verification Status

See `docs/implementation-feature-verification-2026-03-25.md` for test results.

---

*Last updated: 2026-05-22*
