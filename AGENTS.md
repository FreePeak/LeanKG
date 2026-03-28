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

# Start MCP server
cargo run -- serve
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

## Important Files

| File | Purpose |
|------|---------|
| `src/lib.rs` | Module exports |
| `src/db/models.rs` | Data models (CodeElement, Relationship, BusinessLogic) |
| `src/graph/query.rs` | Graph query engine |
| `src/mcp/tools.rs` | MCP tool definitions |
| `src/mcp/handler.rs` | MCP tool handlers |
| `src/indexer/extractor.rs` | Code parsing with tree-sitter |

## Data Model

- **CodeElement** - Files, functions, classes with `qualified_name` (e.g., `src/main.rs::main`)
- **Relationship** - `imports`, `calls`, `tested_by`, `references`, `documented_by`
- **BusinessLogic** - Annotations linking code to business requirements

## MCP Tools

Core tools: `query_file`, `get_dependencies`, `get_dependents`, `get_impact_radius`, `get_review_context`, `find_function`, `get_call_graph`, `search_code`, `generate_doc`, `find_large_functions`, `get_tested_by`

Doc/Traceability tools: `get_doc_for_file`, `get_files_for_doc`, `get_doc_structure`, `get_traceability`, `search_by_requirement`, `get_doc_tree`, `get_code_tree`, `find_related_docs`

Cluster tools: `get_clusters`, `get_cluster_context`

Risk tools: `detect_changes`

## Verification Status

See `docs/implementation-feature-verification-2026-03-25.md` for test results.

---

## LeanKG Tools Usage

### MANDATORY: LeanKG First, Grep Fallback

**This is NOT optional. LeanKG MUST be used first for ALL codebase searches.**

Before ANY codebase search/navigation, you MUST:

1. `mcp_status` - check if LeanKG is ready
2. If not initialized, run `mcp_init` with the project `.leankg` path
3. Use LeanKG tools FIRST: `search_code`, `find_function`, `query_file`, `get_impact_radius`, `get_dependencies`, `get_dependents`, `get_tested_by`, `get_context`
4. **ONLY if LeanKG returns EMPTY results, fall back to grep/ripgrep**

### Why LeanKG First?

| Instead of | Use LeanKG | Why |
|------------|------------|-----|
| `grep -rn "X" --include="*.rs"` | `search_code("X")` or `find_function("X")` | Token-optimized, semantic results |
| `find . -name "*X*"` | `query_file("*X*")` | Instant file lookup |
| Manual dependency tracing | `get_impact_radius` or `get_dependencies` | Accurate blast radius calculation |
| `grep -rn "X" tests/` | `get_tested_by(file)` | Knows exact test coverage |
| Reading entire files | `get_context(file)` | ~99% token savings |

### Grep Fallback

When LeanKG returns empty, use grep with appropriate language filter:

```bash
# Rust
grep -rn "X" --include="*.rs"

# Go
grep -rn "X" --include="*.go"

# TypeScript
grep -rn "X" --include="*.ts" --include="*.tsx"

# Python
grep -rn "X" --include="*.py"
```

### Auto-Init Behavior

LeanKG automatically initializes on first use:
- If `.leankg` does not exist, it creates one automatically
- If index is stale (>5 min since last git commit), it re-indexes automatically
- Configure via `auto_index_on_start` and `auto_index_threshold_minutes` in `leankg.yaml`

---

*Last updated: 2026-03-28*