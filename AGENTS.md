# LeanKG - Agent Instructions

**Tech Stack:** Rust 1.70+, CozoDB (embedded), tree-sitter, Axum, Clap, Tokio, MCP

**Repo:** https://github.com/FreePeak/LeanKG

---

## Build & Test

```bash
cargo build                # Debug build
cargo build --release     # Release build
cargo test                # Run all tests
cargo test <name>         # Run test matching <name>
cargo test -- --nocapture # Show println output
cargo fmt -- --check      # Check formatting
cargo clippy -- -D warnings  # Lint (warnings as errors)
```

## CLI Commands

```bash
cargo run -- init              # Initialize .leankg in current dir
cargo run -- index ./src      # Index a codebase
cargo run -- serve            # Start MCP server (stdio transport)
cargo run -- impact <file> <depth>  # Calculate blast radius
cargo run -- status           # Show index status
leankg mcp-stdio --watch     # MCP mode for AI tool integration
```

## Module Map

```
src/
├── cli/       # Clap commands (init, index, serve, impact, status)
├── config/    # ProjectConfig, IndexerConfig, DocConfig
├── db/        # CozoDB models + schema init
├── doc/       # DocGenerator, template rendering
├── graph/     # GraphEngine, ImpactAnalyzer, query cache
├── indexer/   # tree-sitter extractors (EntityExtractor)
├── mcp/       # MCP tools + handler (tools.rs, handler.rs)
├── watcher/  # notify-based file watcher
└── web/       # Axum REST API for graph visualization
```

**Key files:** `src/lib.rs` (exports), `src/db/models.rs` (CodeElement, Relationship, BusinessLogic), `src/mcp/tools.rs` (tool defs), `src/mcp/handler.rs` (tool exec)

## Data Model

- **qualified_name** format: `path/to/file.rs::function_name` (e.g., `src/main.rs::main`)
- **Relationship** types: `imports`, `calls`, `tested_by`, `references`, `documented_by`

## Workflow (Feature Per Branch)

1. Update docs first (PRD → HLD → README)
2. Implement on a `feature/<name>` branch
3. Commit: `git commit -m "feat: description"` (one feature per commit)
4. Push and create PR via `gh pr create`
5. After merge: bump version in `Cargo.toml`, tag as `vX.Y.Z`

**Commit rules:**
- NEVER add `Co-Authored-By:` or AI attribution to commits
- NEVER add "Generated with AI" to PR descriptions

## LeanKG MCP Tools (for codebase queries)

Use LeanKG tools BEFORE grep/read when navigating code:

| Task | Use |
|------|-----|
| Where is X? | `search_code`, `find_function` |
| What breaks if I change Y? | `get_impact_radius` |
| What tests cover Y? | `get_tested_by` |
| How does X work? | `get_context`, `get_review_context` |
| Dependencies | `get_dependencies`, `get_dependents` |
| Call graph | `get_call_graph` |

Doc/Traceability: `get_doc_for_file`, `get_traceability`, `search_by_requirement`, `get_doc_tree`, `get_code_tree`

## Testing Notes

- Unit tests in `#[cfg(test)]` modules within each `.rs` file
- Integration tests in `tests/` directory
- Use `tempfile::TempDir` for filesystem tests
- Use `tokio::test` for async tests
- Follow Arrange-Act-Assert pattern

## Adding New Features

When adding a new MCP tool:
1. Define in `src/mcp/tools.rs` with input schema
2. Add handler in `src/mcp/handler.rs`
3. Add match arm in `execute_tool`

When adding a new data model:
1. Add struct to `src/db/models.rs`
2. Add DB operations to `src/db/mod.rs`
3. Add query methods to `src/graph/query.rs`