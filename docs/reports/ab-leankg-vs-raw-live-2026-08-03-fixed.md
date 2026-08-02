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

## Results

[FILL FROM RUN — expected changes vs 2026-08-02:]

| Axis | 2026-08-02 (broken) | 2026-08-03 (fixed) |
|---|---|---|
| `semantic_search` on fresh boot | hangs 30s+, never returns | returns within budget |
| DB tools after embed auto-arm | `lock hold by current process` | no lock error (RC-02 single handle) |
| `find_function` correctness | stale foreign paths | correct project-only result |
| `/health` during heavy tool | container `(unhealthy)` | stays `ok` (RC-03 timeout+semaphore) |

## Verdict

[FILL — semantic_search functional; no lock poison; /health stable.]

## Evidence

- Full 88-tool re-validation: `LEANKG_SMOKE_PROJECT=/workspace-be python3 scripts/mcp-smoke-tools.py`
- P0 AC harness: `scripts/mcp-p0-fix-smoke.sh`
