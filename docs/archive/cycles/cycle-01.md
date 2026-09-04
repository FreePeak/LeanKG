# Cycle 01 — Discovery → Live Sweep → Bug Fixes → First Feature

**Branch:** `feature/hackathon` · **Window opened:** 2026-08-22 · **Base:** main @ 0b2ee2cc
**Companion logs:** [`HACKATHON.md`](../../HACKATHON.md) (worktree running log) · [`roadmap-tracker.md`](../roadmap-tracker.md) §6 (SoT)

## Inputs consumed
- R1 live sweep report: [`hackathon-sweep-R1.md`](analysis/hackathon-sweep-R1.md)
- Implementation backlog: [`hackathon-backlog.md`](analysis/hackathon-backlog.md)

## Outcomes

| Phase | Result | Evidence |
|---|---|---|
| Research/audit | 76-tool registry mapped; 7 real bugs + N+1 latency found (p50 4.9s / p95 90s) | sweep-R1 |
| Validate | Every failure reproduced before fixing (TDD RED first) | per-fix tests |
| Plan | Backlog H1–H12 prioritized by value/effort | backlog doc |
| Implement+Test | **7 bug fixes merged** (mcp-layer ×4, engine-layer ×3) + manual identity-logic unification | commits below |
| Feature | **H1 `leankg connect`** shipped: 4 clients, idempotent merge, --remote/--remove; 43 new tests, live-verified with fake HOME | a7b17d0d |

## Bug fixes landed (all RED→GREEN, gates green after each)
| # | Bug → Fix | Commit |
|---|---|---|
| 1 | update_knowledge -32603 db error → pk_for_table upsert | 756b9292 |
| 2 | mcp_index_docs watchdog bypass → spawn_blocking + 300s floor | a53c65fa |
| 3 | CLI/MCP project-key mismatch → canonical_project_root | ea74cd89 |
| 4 | Exports escaping project dir → resolve_out_path anchoring | cd60aff2 |
| 5 | Dynamic ontology invisible cross-boot → schema candidates + legacy adoption (+readonly sslmode URL fix) | 004d6099 |
| 6 | get_context 72s N+1 → batched IN-list hydration (**→2.5s**) | e59b60e5 |
| 7 | agent_focus wedged whole executor → targeted queries + bounded pool wait | 3f070c8f |

Merge commits: b7cda6c5 (mcp), 0d4715aa (engine), cbd5c44e (H1).

## Verification state at cycle close
- lib tests **1089 passed / 0 failed** · fmt ✓ · clippy -D warnings ✓ · build 0 warnings
- Live evidence: update_knowledge round-trip OK on remote PG; index_docs 35.5s success; exports land inside project dir; agent_focus warm 13ms no wedge.

## Carry-over to next cycle
- H2 audit log, H3 npm sync, H5 quickstart timer, H9 doctor --deep, H11 export --markdown queued in dedicated worktrees.
- Re-sweep planned post-R3 to confirm zero FAIL_ERROR and improved p95.
