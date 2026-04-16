# LeanKG - AI Agent Context

## Project Overview

LeanKG is a local-first knowledge graph for codebase understanding. It indexes code with tree-sitter, stores data in CozoDB, and exposes functionality via CLI and MCP.

**Tech Stack:** Rust 1.70+ | CozoDB | tree-sitter | Axum | Clap | Tokio

## Build & Test Commands

```bash
cargo build                    # Debug build
cargo build --release          # Release build (LTO thin)
cargo test                     # Unit tests (16 pass, 3 integration fail without db)
cargo fmt                      # Format code
cargo fmt -- --check           # Check formatting
cargo clippy                   # Lint
```

## CLI Commands

```bash
cargo run -- init              # Initialize LeanKG project
cargo run -- index ./src       # Index codebase
cargo run -- serve             # Start MCP server
cargo run -- impact <file> -d 3 # Calculate blast radius
cargo run -- status            # Check index status
cargo run -- web               # Start Web UI (http://localhost:8080)
```

## Key Modules

| Module | Purpose |
|--------|---------|
| `src/lib.rs` | Module exports |
| `src/main.rs` | CLI entry point (68k, uses clap) |
| `src/db/` | CozoDB models & schema |
| `src/graph/` | Query engine, traversal, clustering |
| `src/indexer/` | tree-sitter extraction, git analysis |
| `src/mcp/` | MCP protocol server & tools |
| `src/cli/` | CLI commands |
| `src/web/` | Axum web server |
| `src/compress/` | Response compression modes |
| `src/obsidian/` | Obsidian vault sync |

## Important Files

- `src/db/models.rs` - CodeElement, Relationship, BusinessLogic
- `src/graph/query.rs` - GraphEngine (get_dependencies, get_dependents, etc.)
- `src/mcp/tools.rs` - MCP tool definitions
- `src/mcp/handler.rs` - Tool execution handlers
- `config/microservice-extractor.yaml` - Microservice extraction rules

## Data Model

- **CodeElement** - Files, functions, classes with `qualified_name` (e.g., `src/main.rs::main`)
- **Relationship** - `imports`, `calls`, `tested_by`, `references`, `documented_by`, `service_calls`
- **BusinessLogic** - Annotations linking code to requirements

## Release Workflow

1. Update version in `Cargo.toml`
2. `cargo build` (updates Cargo.lock)
3. Commit: `git add -A && git commit -m "release: vX.Y.Z"`
4. Push: `git push`
5. Tag: `git tag -a vX.Y.Z -m "Release vX.Y.Z" && git push origin vX.Y.Z`
6. CI auto-publishes to crates.io and builds binaries

## Development Workflow

**When implementing features, follow:** `docs/workflow-opencode-agent.md`

Pattern: Update Docs (PRD → HLD) → Implement → Build → Test → Commit → PR → Merge → Release

## MCP Tools (via LeanKG)

Core: `search_code`, `find_function`, `query_file`, `get_impact_radius`, `get_dependencies`, `get_dependents`, `get_call_graph`, `get_context`, `get_tested_by`, `find_large_functions`

Doc/Traceability: `get_doc_for_file`, `get_files_for_doc`, `get_traceability`, `search_by_requirement`

Cluster: `get_clusters`, `get_cluster_context`

Risk: `detect_changes`

## Architecture Notes

- CozoDB embedded database (not a separate service)
- tree-sitter parsers: Go, TypeScript, Python, Rust, Java, Kotlin
- MCP server uses rmcp crate
- Web UI built with Vite, embedded in binary via `src/embed/`

## Testing Notes

- Unit tests in `#[cfg(test)]` modules per source file
- Integration tests in `tests/` directory
- Integration tests require indexed database state; fail without it
- Use `tempfile::TempDir` for filesystem test fixtures
- Use `tokio::test` for async tests

## Verification Status

See `docs/implementation-feature-verification-2026-03-25.md` for test results.

---

*Last updated: 2026-04-15*
