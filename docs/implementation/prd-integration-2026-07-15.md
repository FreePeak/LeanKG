# LeanKG PRD Integration — Status 2026-07-15 (final)

Snapshot of where the integration branch (`integration/prd-pending`) stands. All paths in this document refer to the LeanKG source tree — the large workspace used for runtime smoke-testing is **not** named in this document and was used only as a non-committed sandbox.

## CI gate (final)

| Check | Status |
|-------|--------|
| `cargo fmt --all -- --check` | PASS |
| `cargo clippy --release --all-targets -- -D warnings` | PASS |
| `cargo test --release --lib` | 496 passed (3× stable runs) |
| `cargo test --release --bin leankg` | 491 passed (3× stable runs) |
| `cargo test --release --tests` (integration suite) | 1687+ passed across 46 test files, 0 failed |
| `cargo test --release --test budget_lsp_e2e` | 12 passed |
| `cargo test --release --test ontology_e2e` | 16 passed |
| `cargo test --release --test cli_tests` | passes |

PR #72 — Format, Clippy, and all test suites green.

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
- **Long-running daemon GC** (`src/gc.rs` + `MemoryGuard`):
  - `mcp-stdio` and `mcp-http` daemons spawn a `MemoryGuard` watchdog that polls RSS every `LEANKG_GC_POLL_SECS` (default 10 s).
  - On idle (default 60 s) the release callback runs **once per idle period** (re-armed only after `touch()`), skips when caches are already empty, and calls `trim_heap()` after a real release.
  - On RSS over cap (default 4 GB) the callback runs every 30 s when there is something to drop, and prints a warning.
  - MCP handlers call `MemoryGuard::touch()` per request so the idle detector only fires when the daemon is truly quiet.
- **Streaming JSON export**: writes one element at a time via `BufWriter`, no 470 MB intermediate string.

## Stale-process hygiene (`leankg doctor`)

`leankg doctor [--kill]` reports (and optionally kills) any stale leankg process holding the DB mmap. Uses `argv[0] == "leankg"` to avoid false positives on cline daemons that happen to have a `--cwd .../leankg` argument. The `--kill` path uses `SIGTERM` first with a 2-second grace, then `SIGKILL` stragglers. Refuses to kill the current process or its parent (the shell that invoked the doctor).

Example:
```
$ leankg doctor
No stale leangk processes detected.

$ leankg mcp-stdio < /dev/null &      # spawn a stale daemon
$ leankg doctor
Stale leankg processes (RSS reported by `ps`):
  PID   12345  RSS     8 MB  /Users/.../target/release/leankg mcp-stdio
Re-run with --kill to terminate these processes.

$ leankg doctor --kill
Stale leankg processes (RSS reported by `ps`):
  PID   12345  RSS     8 MB  /Users/.../target/release/leankg mcp-stdio
Killing 1 stale process(es)...
Done.
```

## LSP support for all languages

`src/lsp/registry.rs` covers 44 languages with npm / pip / cargo / brew / go / gem / opam / dotnet install hints. CLI: `leankg lsp-list`, `leankg lsp-install <lang|all>`, `leankg lsp-resolve` (auto-detects language from file extension).

## MCP server end-to-end (verified on big workspace)

```
$ leankg mcp-http --port 19699 --project .

POST /mcp initialize -> 200, protocolVersion 2025-06-18, serverInfo { name: "leankg", version: "0.17.9" }
POST /mcp tools/list  -> 200, returns the full tool catalog
POST /mcp tools/call name=mcp_status -> 200, returns real data from the workspace
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
| `leankg check-consistency --limit 20` | Returns 20 + "264645 more" hint |
| `leankg export --format json` | Streams to disk, ~2.5 GB peak (was 5.4 GB) |
| `leankg clones --max-functions 400000` | 3 high-similarity pairs in 6.7 s |
| `leankg lsp-list` | 44 entries, ✓ / ✗ on-path check |
| `leankg lsp-resolve ./foo.go` | Detects "go" from extension, suggests `lsp-install go` |
| `leankg doctor` | Reports stale processes; `--kill` terminates them |
| `leankg mcp-http` | Real initialize + tools/list + mcp_status round-trip works |

No crashes, no OOM, no runaway processes.

## Commits added to PR #72 branch

```
9abb57f perf: stream heavy callers + add MemoryGuard for daemons
7d69880 perf: stream heavy callers + add MemoryGuard for daemons
0d0f005 perf: stream heavy callers + add MemoryGuard for daemons
f265335 perf: bound heavy tools + LSH-based clones + LSP catalog for all languages
+ 30 earlier commits covering the full PRD feature set
```

## Open follow-ups

- Per-file LSH streaming for `find_clones` (currently clones stays around 1.1 GB peak on a 369k-fn graph; LSH file-by-file would drop it further).
- Migrate remaining `all_elements()` / `all_relationships()` call sites in MCP handlers (currently 16 of them) to the streaming API.
- Default `LEANKG_MAX_RSS_MB` is 4 GB which suits 32 GB hosts; should be auto-derived from `sysconf(_SC_PHYS_PAGES) * page_size` on first run.
