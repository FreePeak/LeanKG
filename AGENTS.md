# LeanKG — Agent Context

**Tech stack:** Rust + PostgreSQL/pgvector + tree-sitter + MCP

## Build & Test

```bash
cargo build --release          # always --release; debug profile has debug=false
cargo test --lib                # quick unit tests only (CI does this)
cargo test                      # full suite including integration/e2e
make lint                       # = cargo clippy --all-targets --all-features -- -D warnings
cargo fmt --all -- --check      # formatting check
```

`.opencode.json` auto-loads `instructions/leankg-tools.md` — detailed MCP tool reference.

## CLI Quick Reference

| Command | Purpose |
|---------|---------|
| `cargo run --release -- init` | Init project |
| `cargo run --release -- index ./src` | Index codebase |
| `cargo run --release -- mcp-stdio --watch` | MCP stdio (local AI tools) |
| `cargo run --release -- mcp-http --port 9699` | MCP HTTP (remote clients) |
| `cargo run --release -- embed` | Build embedding vectors (after index) |
| `cargo run --release -- embed --dry-run` | Export embed queries to `.leankg/embed_export.jsonl` (offsite/GPU batch — pair with `scripts/embed_batch.py` + `embed --import`) |
| `cargo run --release -- embed --import <file>` | Import vectors produced from a `--dry-run` export (resumable; `--no-verify` skips drift check) |
| `cargo run --release -- serve` | REST API + embedded UI v2 on :8080 |
| `cargo run --release -- impact <file> <depth>` | Blast radius calc |
| `cargo run --release -- doctor` | Stale-process / mmap diagnostics |
| `cargo run --release -- doctor --deep [--format json] [--project PATH]` | Deployment self-diagnosis (H9): PG latency, migrations, index freshness, embeddings coverage, pool env, orphan edges, duplicate names. Exit 0 pass / 1 warn / 2 fail |

Embeddings require `--features embeddings` build flag (off by default). Without them, `semantic_search` / `kg_semantic_context` return "no vectors".

## MANDATORY: Docker MCP project paths

When MCP talks to Docker HTTP on `:9699`, **always** pass container mount paths as `project=`. Host paths return "not initialized".

```rust
mcp_status(project="/workspace")              // OK
search_code(query="fn main", project="/workspace")  // OK
mcp_status(project="/Users/.../leankg")        // FAILS
```

| Mount | `project=` |
|-------|-----------|
| This repo | `/workspace` |
| Side repo | `/workspace-other` (per local `.dockerfile`) |

Health check: `curl http://localhost:9699/health`. If healthy → use Docker MCP. Else → fall back to stdio + host-path `mcp_init`.

## Tool discovery prefer-order

Do **not** open with `query_graph`. Discover first:

`concept_search` → `semantic_search` → `search_code` / `find_function` → connection verbs (impact, deps, context).

| Question | First tools |
|----------|-------------|
| Fuzzy / NL / domain | `concept_search` → `semantic_search` → `search_code` |
| Exact symbol / file | `find_function` / `search_code` / `query_file` |
| How A↔B? | `shortest_path` |
| What is symbol? | `explain_node` |
| Expand subgraph | `query_graph` (after seeds known) |

**Dynamic ontology**: `add_ontology_concept` / `add_ontology_workflow` persist insights across sessions. `add_knowledge` for free-form notes. After YAML edits in `ontology/`, use `kg_trace_workflow` (auto-synced; no manual `leankg ontology sync` needed).

## Development workflow

1. Update `docs/prd.md` (narrative + ACs) + `docs/prd-task-tracker.md` (task list)
2. Implement per `docs/workflow-opencode-agent.md`
3. `cargo build --release && cargo test`
4. `git commit -m "feat: description"` (one feature per commit; **no** `Co-Authored-By` or AI attribution)
5. `git pull --rebase && git push`
6. Bump `version` in `Cargo.toml`
7. `git tag -a v<version> -m "Release v<version>" && git push origin v<version>`

## Key source files

| File | Purpose |
|------|---------|
| `src/main.rs` | CLI entrypoint |
| `src/lib.rs` | Module exports |
| `src/cli/mod.rs` | Subcommand definitions |
| `src/mcp/tools.rs` | MCP tool definitions |
| `src/mcp/handler.rs` | MCP tool handlers |
| `src/db/models.rs` | Data models |
| `src/graph/query.rs` | Graph query engine |
| `src/indexer/extractor.rs` | tree-sitter code parsing |
| `src/embed.rs` | Embedding pipeline CLI |

## Multi-project setup (side-by-side repos)

Gitignored local files:
- `.dockerfile` — copy from `.dockerfile.example`; set `LEANKG_PROJECT_DIRS=/workspace,/workspace-other`
- `docker-compose.override.yml` — add bind mounts for side repos

Never paste personal host paths into commits.

## Parallel subagent workflow

For 3+ independent tasks: dispatch to `.worktree/<feature>/` worktrees with feature branches. Verify isolation (`.gitignore` covers `.worktrees/`). Merge all feature branches after completion.

## Cursor Cloud specific instructions

Single Rust binary (`leankg`); all modes are subcommands. Storage is PostgreSQL-only — set `LEANKG_PG_URL` (remote managed Postgres works; see `.env`). The VM snapshot already has the toolchain and system libs below; the startup update script only runs `cargo fetch`.

- **Toolchain**: build requires Rust **stable ≥ 1.85** (transitive deps use edition2024). The base image's 1.83 is too old; the snapshot ships `rustup default stable`. README's "Rust 1.75+" badge is outdated for building from source.
- **Native build deps**: native extensions compiled via the `cxx`/C++ toolchain need C++ stdlib headers. `clang`/`cc` select GCC 14, so `libstdc++-14-dev` (plus `g++`) must be present or the build fails with `fatal error: 'algorithm' file not found`. These are installed in the snapshot.
- **Always `--release`**: the debug profile sets `debug=false`; use `cargo build --release` / `cargo run --release --` per `Makefile`. First release build ≈ 4–5 min; `cargo clippy --all -- -D warnings` ≈ 3 min.
- **Verify commands** (all pass): `cargo fmt --all -- --check`, `cargo clippy --all -- -D warnings` (CI gate; `make lint` adds `--all-features` which pulls the heavy `embeddings`/ONNX stack), `cargo test --lib` (734 tests, ~4s). See `AGENTS.md` Build & Test and `.github/workflows/ci.yml`.
- **Index step is slow**: `leankg index ./src` inserts ~8k elements / ~50k relationships into SQLite and takes ~4–5 min; it is not hung. Run `leankg init` first.
- **CLI quirk**: `impact` takes `--depth N` (a flag), not a positional depth arg as some docs show, e.g. `leankg impact src/main.rs --depth 2`.
- **MCP HTTP**: `leankg mcp-http --port 9699 --project /workspace`; health `GET /health`, JSON-RPC `POST /mcp?project=/workspace`. Pass the container path `/workspace` as `project` (see MANDATORY section above).
- **Embeddings/semantic search** need `--features embeddings` (downloads ONNX models at runtime); off by default — `semantic_search` returns "no vectors" without them.

---

*Last updated: 2026-08-01*
