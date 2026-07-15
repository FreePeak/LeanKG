# LeanKG PRD Integration — Status 2026-07-15

Snapshot of where the integration branch (`integration/prd-pending`) stands. All paths in this document refer to the LeanKG source tree — the large workspace used for runtime smoke-testing is **not** named in this document and was used only as a non-committed sandbox.

## CI gate (final)

| Check | Status |
|-------|--------|
| `cargo fmt --all -- --check` | PASS |
| `cargo clippy --release --all-targets -- -D warnings` | PASS |
| `cargo test --release --lib` | 601 passed (5× stable runs) |
| `cargo test --release --bin leankg` | 599 passed (5× stable runs) |
| `cargo test --release --tests` (46 test files) | 1687 passed, 0 failed |
| `cargo test --release --test budget_lsp_e2e` | 12 passed |
| `cargo test --release --test ontology_e2e` | 16 passed |
| `cargo test --release --test cli_tests` | passes |

PR #72 — Format, Clippy, and all test suites green.

## Test coverage

| Surface | Tests | Status |
|---|---|---|
| Lib unit tests (modules under `src/`) | 601 | pass |
| Bin unit tests (CLI / handler unit tests) | 599 | pass |
| Integration tests (46 files under `tests/`) | 1687 | pass |
| `tests/budget_lsp_e2e.rs` (new) | 12 | pass |
| `tests/ontology_e2e.rs` (new) | 16 | pass |
| `tests/clippy` | 0 warnings | pass |

## Performance / RAM measurements (627k-element / 1M-relationship workspace)

| Tool | Before | After | Delta |
|---|---|---|---|
| `leankg export --format json` | 5.43 GB peak | **2.50 GB peak** | **-54%** |
| `leankg clones` (cross-file, O(n²)) | hours | O(n) hash + LSH candidate set | huge |
| `leankg clones` (default same-file) | 944 MB | bounded by `max_functions` (default 50k) | - |
| `leankg impact --depth 2` | unbounded | `--max-affected 10k` cap + budget guard | - |
| `leankg check-consistency` | 264k+ findings flood | `--limit` flag (default 50) | - |

## OOM safety guarantees

- **Process budget guard** (`src/budget.rs`): every heavy tool aborts early on wall-clock, RSS, or iteration cap breach. Defaults 60 s / 4 GB RSS / 1M iters. Tunable via `LEANKG_TOOL_TIMEOUT_SECS` / `LEANKG_MAX_RSS_MB` / `LEANKG_TOOL_BUDGET_OFF=1`.
- **Streaming API** (`graph::GraphEngine::for_each_element` / `for_each_relationship` / `for_each_element_of_type`): yields one element at a time, never materializes the full Vec. Migrated callers: `find_clones`, `export_snapshot`, `export_json_streaming`.
- **Long-running daemon GC** (`src/gc.rs` + `MemoryGuard`): polls RSS every 10 s, runs the release callback on idle (default 60 s) and force-runs on RSS over cap (default 4 GB). `malloc_trim(0)` on Linux releases pages to the OS. MCP handlers call `MemoryGuard::touch()` per request so the idle detector only fires when the daemon is truly quiet.
- **Streaming JSON export**: writes one element at a time via `BufWriter`, no 470 MB intermediate string.

## LSP support for all languages

`src/lsp/registry.rs` covers 44 languages (Go, TS, JS, Python, Rust, Java, Kotlin, C, C++, C#, Zig, Crystal, Scala, Clojure, Vue, Svelte, HTML, CSS, JSON, YAML, XML, Ruby, PHP, Lua, Bash, PowerShell, Haskell, Elm, OCaml, F#, Elixir, Erlang, SQL, R, Swift, Dart, Markdown, TOML, GraphQL, Terraform, Dockerfile, Protobuf, Solidity + more). Each entry carries npm / pip / cargo / brew / go / gem / opam / dotnet install hints.

CLI:
- `leankg lsp-list` — prints catalog with on-path check (✓ / ✗)
- `leankg lsp-install <lang|all> [--dry-run]` — runs the best install method on the host
- `leankg lsp-resolve <file>` — `--language` is optional; auto-detected from file extension. Falls back with a helpful hint pointing at `lsp-install`.

## MCP server end-to-end (verified on big workspace)

```
$ leankg mcp-http --port 19699 --project .

POST /mcp initialize -> 200, protocolVersion 2025-06-18, serverInfo { name: "leankg", version: "0.17.9" }
POST /mcp tools/list  -> 200, returns the full tool catalog
POST /mcp tools/call name=mcp_status -> 200, returns:
   { "status": "ok", "tool": "mcp_status", "data": { "database": "...", "index_populated": true, ... } }
```

The `mcp-stdio` daemon also starts cleanly and exits gracefully on stdin close (verified exit 0 with no input).

## Runtime smoke test summary

| Tool | Result |
|------|--------|
| `leankg init` | Detected languages: go, typescript, javascript, python |
| `leankg index .` | 21,762 files → 598,447 elements, 1,077,849 relationships |
| `leankg status` | 369,158 functions, 30,407 classes indexed |
| `leankg query` (name + content + type) | Returns relevant matches |
| `leankg explain` | Reports `in_degree` / `out_degree` for chosen nodes |
| `leankg impact` (bounded) | Reports within budget, no OOM |
| `leankg gods` | Top god-nodes by degree |
| `leankg tunnels` | Cross-cluster tunnels |
| `leankg check-consistency` | With `--limit 20` returns 20 + "264645 more" hint |
| `leankg export --format json` | Streams to disk, ~2.5 GB peak (was 5.4 GB) |
| `leankg clones` (with `--max-functions 400000`) | 3 high-similarity pairs in 6.7 s |
| `leankg lsp-list` | 44 entries, ✓ / ✗ on-path check |
| `leankg lsp-resolve` | Detects "go" from `.go` extension, suggests `leankg lsp-install go` |
| `leankg mcp-http` | Real initialize + tools/list + mcp_status round-trip works |

No crashes, no OOM, no runaway processes.

## Commits added to PR #72 branch

```
0d0f005 perf: stream heavy callers + add MemoryGuard for daemons
f265335 perf: bound heavy tools + LSH-based clones + LSP catalog for all languages
64b0fa6 feat(cbm-b1): LSP MCP/CLI wiring + typed_resolve aliases + e2e tests
534cd7f feat(cbm-b1): LSP bridge for typed resolve (multi-repo + nested dirs)
7d811a4 docs(status): add 2026-07-14 PRD integration status snapshot
6f0a235 fix(lint): resolve clippy + fmt warnings across crates for CI gate
+ 25 earlier commits covering the full PRD feature set
```

(Plus 25 earlier commits for the Graphify / MemPalace / CBM / GitNexus / language breadth / team infra / distribution backlog.)

## Open follow-ups

- Per-file LSH streaming for `find_clones` (currently clones stays around 1.1 GB peak on a 369k-fn graph; LSH file-by-file would drop it further).
- Migrate remaining `all_elements()` / `all_relationships()` call sites in MCP handlers (currently 16 of them) to the streaming API.
- Default `LEANKG_MAX_RSS_MB` is 4 GB which suits 32 GB hosts; should be auto-derived from `sysconf(_SC_PHYS_PAGES) * page_size` on first run.
