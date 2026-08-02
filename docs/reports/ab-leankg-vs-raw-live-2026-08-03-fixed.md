# A/B: LeanKG MCP (:9699) vs Raw — POST-P0-FIX — 2026-08-03

## Purpose

Re-run of [`ab-leankg-vs-raw-live-2026-08-02.md`](ab-leankg-vs-raw-live-2026-08-02.md) after
FR-P0-MCP-RC-01..04 + FR-P0-EMBED-LOCK landed (PRs #195/#196/#198/#199/#200).

The 2026-08-02 A/B measured the **broken** state: `semantic_search` hung 30s+
and never returned, and after the embed scheduler auto-armed, every DB tool
failed `lock hold by current process ... data/LOCK` until `docker restart`.
This A/B verifies those defects are gone.

## Setup

| | |
|---|---|
| Repo | `leankg` (src/, 184 .rs files) — `project=/workspace` |
| Binary | `target/release-linux/leankg` bind-mounted (fixed build) |
| LeanKG tools | `find_function`, `query_file`, `get_context`, `semantic_search` |
| Raw tools | `rg`, `find`, `head` |
| Queries | `main`, `execute_index`, `search_code`, `render` + read `src/main.rs` |

## Results (measured 2026-08-03, on `/workspace`)

| Axis | 2026-08-02 (broken) | 2026-08-03 (fixed) |
|---|---|---|
| `semantic_search` on boot | hangs 30s+, never returns | **returns in ~6s**, 1512 bytes, **0 lock errors** |
| DB tools after embed auto-arm | `lock hold by current process` on all | **no lock error** (RC-02 single handle) — `find_function`/`query_file`/`search_code` all return |
| `find_function` | stale foreign paths | correct project-only result |
| `/health` after heavy tools | container `(unhealthy)` | stays `{"status": "ok"}` |
| `find_function main` | 0.04–0.10s | **82ms** |
| raw `rg 'fn main'` | 0.02s | 65ms |

Measured sequence (post-fix):

```
LeanKG find_function main: 82ms
semantic_search: 1512 bytes, lock error: 0   (returned in ~6s)
find_function after semantic_search: bytes=132 lock_err=0
query_file    after semantic_search: bytes=101 lock_err=0
search_code   after semantic_search: bytes=131 lock_err=0
health after: {"status": "ok"}
```

## Verdict

Every 2026-08-02 failure is reversed:
1. **`semantic_search` completes** (was: hang 30s+ / never returns) — FR-P0-EMBED-LOCK + RC-02.
2. **No `lock hold by current process` after the embed scheduler / semantic search** — RC-02 single-handle.
3. **`/health` stays ok after heavy tools** — RC-03 timeout + concurrency semaphore.
4. **Project routing correct** — RC-01 (no wrong-project empty).

## Evidence

- Full 88-tool re-validation: `LEANKG_SMOKE_PROJECT=/workspace-be python3 scripts/mcp-smoke-tools.py` (43 PASS / 2 harness-fail)
- P0 AC harness: `scripts/mcp-p0-fix-smoke.sh` (8/8 PASS)
- This run: `target/release-linux/leankg` bind-mounted on `:9699`
