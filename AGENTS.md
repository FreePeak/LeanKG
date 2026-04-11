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
cargo run -- init [--path <path>]     # Initialize .leankg in current dir
cargo run -- index ./src [--incremental] [--lang <lang>] [--exclude <patterns>]
cargo run -- mcp-stdio [--watch]     # Start MCP server with stdio transport
cargo run -- web [--port <port>]     # Start embedded web UI server
cargo run -- impact <file> [--depth <depth>]  # Calculate blast radius
cargo run -- status                  # Show index status
cargo run -- watch [--path <path>]   # Start file watcher for auto-indexing
cargo run -- export [--format <json|dot|mermaid|html|svg|graphml|neo4j>] # Export graph
cargo run -- annotate <element> --description <desc>  # Business logic annotation
cargo run -- trace [--feature <id>]  # Traceability chain
cargo run -- detect-clusters         # Community detection
cargo run -- benchmark [--category <cat>] [--cli <opencode|gemini|kilo>]
cargo run -- api-serve [--port <port>] [--auth]  # REST API server
cargo run -- metrics [--since <period>] [--json]  # Context metrics
cargo run -- wiki [--output <dir>]  # Generate wiki
cargo run -- hooks install           # Install git hooks
```

## Module Map

```
src/
├── main.rs              # CLI entry point (28+ commands)
├── lib.rs               # Library exports
├── cli/                 # Clap commands enum + ShellRunner
├── config/              # ProjectConfig, IndexerConfig, DocConfig, McpConfig
├── db/                  # CozoDB models, schema, operations, API key store
├── doc/                 # DocGenerator, template rendering, wiki generation
├── doc_indexer/         # Documentation indexing (docs/ -> documented_by edges)
├── graph/               # GraphEngine, queries, context, traversal, clustering, cache, export
├── indexer/             # tree-sitter parsers (13), extractors, git analysis, Terraform, CI/CD
├── mcp/                 # MCP tools (35), handler, server (rmcp), auth, write tracker
├── orchestrator/        # Query orchestration with intent parsing and persistent cache
├── compress/            # RTK-style compression: 8 read modes, response/shell/cargo/git, entropy
├── web/                 # Axum web UI (20+ routes, embedded HTML/CSS/JS)
├── api/                 # REST API handlers, auth middleware
├── watcher/             # notify-based file watcher for auto-indexing
├── hooks/               # Git hooks (pre-commit, post-commit, post-checkout, GitWatcher)
├── benchmark/           # Benchmark runner (LeanKG vs OpenCode/Gemini/Kilo)
├── registry.rs          # Global repository registry (multi-repo management)
└── runtime.rs           # Tokio runtime utilities
```

**Key files:** `src/lib.rs` (exports), `src/db/models.rs` (CodeElement, Relationship, BusinessLogic, ContextMetric), `src/mcp/tools.rs` (35 tool defs), `src/mcp/handler.rs` (tool execution)

## Data Model

- **qualified_name** format: `path/to/file.rs::function_name` (e.g., `src/main.rs::main`)
- **Relationship** types (10): `imports`, `calls`, `references`, `documented_by`, `tested_by`, `tests`, `contains`, `defines`, `implements`, `implementations`

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
| What breaks if I change Y? | `get_impact_radius`, `detect_changes` |
| What tests cover Y? | `get_tested_by` |
| How does X work? | `get_context`, `get_review_context`, `orchestrate` |
| Dependencies | `get_dependencies`, `get_dependents` |
| Call graph | `get_call_graph`, `get_callers` |
| Read file efficiently | `ctx_read` (8 compression modes) |
| Smart routing | `orchestrate` (cache-graph-compress) |

Doc/Traceability: `get_doc_for_file`, `get_traceability`, `search_by_requirement`, `get_doc_tree`, `get_code_tree`

Clustering: `get_clusters`, `get_cluster_context`, `generate_graph_report`

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