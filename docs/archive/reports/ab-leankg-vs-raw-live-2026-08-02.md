# A/B: LeanKG MCP (:9699) vs Raw tools — Live 2026-08-02

## Setup

| | |
|---|---|
| Repo | `leankg` (`src/`, 184 `.rs` files, ~96.8k lines, 5.5MB) |
| Env | Docker `leankg-leankg-1`, `mcp-http :9699`, `project=/workspace`, index populated |
| LeanKG tools | `find_function`, `query_file`, `get_context`, `semantic_search` |
| Raw tools | `rg` (exact name / def search), `find` (file locate), `head` (read) |
| Queries | `main`, `execute_index`, `search_code`, `render` + read `src/main.rs` |

## Latency (median of valid runs)

| Task | LeanKG | Raw | Winner |
|---|---|---|---|
| find `main` symbol | 0.04–0.10s | `rg '^fn main' src/main.rs` 0.02s | Raw |
| find `execute_index` | 0.87s (empty, correct) | 0.02s (0 matches) | Raw |
| find `search_code` | 0.86s | 0.01s (22 files) | Raw |
| find `render` | 0.69s | 0.01s (9 files) | Raw |
| locate `main.rs` | `query_file` 3.3s | `find` 1.29s* | Raw |
| read `src/main.rs` context | `get_context` 5.3s | `head` 0.006s | Raw |

\* Raw `find` traversed 27GB `target/`; scoped to `src tests` = 0.57s. LeanKG excludes target natively — raw numbers are biased in LeanKG's favor and it still loses.

## Semantic search: FAILS on this setup

- First `semantic_search` on fresh boot: **hangs 30s+, never returns** (reproduced 4/4 queries, 3 separate boots).
- `LEANKG_EMBED_AUTO_ARM=1` + `LEANKG_EMBED_IDLE_AFTER_SECS=30` → 30s after boot, in-process embed scheduler starts an incremental scan that grabs the RocksDB `data/LOCK`.
- After the scan or any semantic_search, **all** DB tools (`find_function`, `get_context`, `search_code`, `query_file`) fail:
  `RocksDB error: IO error: lock hold by current process ... data/LOCK: No locks available`
  until `docker restart leankg-leankg-1`.
- `embed_control off` clears the armed flag but does **not** release the already-held lock — server stays poisoned.

## Correctness: stale foreign index data

`find_function "main"` returns foreign-workspace artifacts (`./GitNexus/...`, `./OmniRoute/...`) and does **not** return `src/main.rs::main`. The `/workspace` RocksDB index carries stale entries from previously indexed foreign binds.

## Verdict

Raw wins on every measurable axis on this setup. LeanKG's differentiator (semantic search) is non-functional here — hang + lock poison — and its exact-name lookup is slower (0.7–0.9s vs 0.02s) and returns wrong results.

## Root cause (see memory `leankg-embed-lock-poison`)

Reader MCP server and in-process embed scheduler fight over a single-writer RocksDB handle inside PID 1 (`leankg mcp-http`). Compose comment claims a "phase-0 LOCK fix + read-only mode" was intended, but auto-arm still conflicts.

## Fix directions

1. `LEANKG_EMBED_AUTO_ARM=0` on the serving container (separate embed writer from reader).
2. Re-index / prune stale foreign paths from `/workspace` RocksDB.
