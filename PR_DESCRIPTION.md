Implements PENDING PRD v3.5 features plus the LSP bridge and a comprehensive performance / OOM-safety pass. 35+ commits on the `integration/prd-pending` worktree branch, one feature per commit, no AI attribution per CLAUDE.md.

## Graphify (US-GF-01..12)
- feat(gf-01): shortest_path MCP + leankg path CLI
- feat(gf-02/gf-05): explain_node + get_god_nodes MCP + CLI
- feat(gf-04): edge provenance labels (EXTRACTED / INFERRED / AMBIGUOUS)
- feat(gf-06): GRAPH_REPORT.md generator + get_graph_report MCP
- feat(gf-07): rationale extraction (WHY / NOTE / HACK / FIXME / XXX)
- feat(gf-08): PR impact dashboard (get_pr_impact MCP + leankg prs CLI)
- feat(gf-09): work-memory reflect loop
- feat(gf-10): Vue + Svelte SFC extractors
- feat(gf-11): portable graph snapshot (export_graph_snapshot MCP)
- feat(gf-12): SQL DDL parser

## MemPalace (US-MP-01..08)
- feat(mp-01): temporal knowledge graph
- feat(mp-02): layered context loading L0-L3 + load_layer MCP
- feat(mp-04): specialist agent contexts (agent_focus + diary)
- feat(mp-05): check_consistency MCP + CLI
- feat(mp-06): cross-domain tunnels
- feat(mp-08): folder-as-graph helpers

## Massive-Graph UI
- feat(mg-03): single-repo root expansion auto-loads full graph

## CBM structural parity
- feat(cbm-b1): LSP bridge for typed resolve (multi-repo + nested dirs)
- feat(cbm-b1): LSP MCP/CLI wiring + typed_resolve aliases + e2e
- feat(cbm-b6): event channel edges
- feat(cbm-b7): clone / near-duplicate detection
- feat(cbm-b8): cross-repo similar edges
- feat(cbm-b10): typed_resolve feature flag
- feat(cbm-c2): hot-path cache

## GitNexus
- feat(gn-07): get_cluster_skill MCP
- feat(gn-08): get_overview_context MCP

## Language breadth
- feat(lang-01): Dart extraction
- feat(lang-02): Swift extraction
- feat(lang-03): XML extraction

## Team + distribution
- feat(v2-12): get_team_map MCP
- feat(us-14): npm-based installation wrapper
- feat(v2-11): CI/CD auto-graph update

## LSP support for all languages (no-config)
- feat: src/lsp/registry.rs (new) — 44-language catalog with
  Go/TS/JS/Python/Rust/Java/Kotlin/C/C++/C#/Zig/Crystal/Scala/
  Clojure/Vue/Svelte/HTML/CSS/JSON/YAML/XML/Ruby/PHP/Lua/Bash/
  PowerShell/Haskell/Elm/OCaml/F#/Elixir/Erlang/SQL/R/Swift/Dart/
  Markdown/TOML/GraphQL/Terraform/Dockerfile/Protobuf/Solidity.
  Each entry carries npm / pip / cargo / brew / go / gem / opam /
  dotnet install hints.
- feat: leankg lsp-install <lang|all> — runs the best install
  method on the host (or --dry-run to print commands).
- feat: leankg lsp-list — prints catalog with on-path check.
- feat: leankg lsp-resolve — --language now optional; auto-detected
  from file extension. Falls back with a helpful hint pointing at
  lsp-install.

## Performance + OOM safety (the big one)
- feat: src/budget.rs (new) — process-wide `BudgetGuard` with
  wall-clock + RSS + iteration caps. Defaults 60s / 4 GB RSS /
  1M iters. Disable via `LEANKG_TOOL_BUDGET_OFF=1`.
- feat: src/minhash.rs (new) — MinHash + LSH bands (128 perms,
  32 × 4). `find_clones --cross-file` now runs in O(n) hash
  insertions + candidate-set, not O(n²) all-pairs.
- feat: src/gc.rs (new) — `MemoryGuard` for long-running daemons.
  Polls RSS every 10s, runs a release callback on idle (default
  60s) and force-runs on RSS over cap (default 4 GB).
  `malloc_trim(0)` on Linux releases pages to the OS.
- perf: graph::GraphEngine::for_each_element /
  for_each_relationship / for_each_element_of_type — yield one
  element at a time, never materialize the full Vec.
- perf: find_clones — same-file default scoping, streaming file
  reads, max_functions cap (default 50k), LSH opt-in for
  cross-file, budget guard.
- perf: export_snapshot — streams JSON to disk via BufWriter +
  per-element serialization (no more 470 MB string in RAM).
- perf: impact — --max-affected cap (default 10k) + budget guard,
  `truncated` flag so callers can re-scope.
- perf: check-consistency — --limit flag (default 50, 0 = unlimited).
- feat: MCP handler calls `MemoryGuard::touch()` per request so
  the idle detector only fires when the daemon has truly gone
  quiet.

### Measured on a 627k-element / 1M-relationship workspace
| Tool                     | Before     | After       | Delta   |
|--------------------------|------------|-------------|---------|
| `export --format json`   | 5.43 GB pk | **2.50 GB pk** | **-54%** |
| `clones` (same-file)     | 944 MB pk  | bounded by max_functions + LSH opt-in for cross-file |  -     |
| `clones` (cross-file)    | hours      | O(n) hash + LSH candidate set |  huge    |

## CI gate
- cargo fmt --all -- --check — PASS
- cargo clippy --release --all-targets -- -D warnings — PASS
- cargo test --release --lib — 601 passed (5× stable runs)
- cargo test --release --bin leankg — 599 passed (5× stable runs)
- cargo test --release --tests — 1687 tests across 46 test files, 0 failed
- cargo test --release --test budget_lsp_e2e — 12 passed
- cargo test --release --test ontology_e2e — 16 passed

## Verified on a real workspace (LOC + multi-repo Go/TS/Python)
- `leankg status` reports 627k elements / 1M relationships
- `leankg query`, `leankg explain`, `leankg impact`, `leankg tunnels`,
  `leankg check-consistency`, `leankg export`, `leankg clones`,
  `leankg lsp-list`, `leankg lsp-resolve` all return correct output
- MCP HTTP server: `initialize` returns protocolVersion 2025-06-18,
  `tools/list` returns the full catalog, `tools/call mcp_status`
  returns real data

## Notes
- One feature per commit, per the project's commit-message policy.
- No AI attribution, Co-Authored-By, or generated-with lines per CLAUDE.md.
- New worktree commits rebased cleanly onto main's 0.17.9 release.
