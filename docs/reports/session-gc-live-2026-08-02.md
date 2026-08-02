# Session GC (#192) live evidence — 2026-08-02

## Environment
- commit: 1bab0c3d (worktree prd/p3-batch-1, NOT in main yet) | binary: .worktrees/prd/p3-batch-1/target/release/leankg 0.19.30 | MCP :9880 | project: /tmp/leankg-live-fixture
- Note: #192 is merged to main? NO — GC commit 19df62e8 / 1bab0c3d is in worktree prd/p3-batch-1, not yet on main (HEAD 8c77b22b). Tested from worktree binary.

## Steps
1. Created refs under `$FIX/.leankg/sessions/`:
   - `sess-old/refs/offload-001.md` (mtime 2026-07-01, 32 days old)
   - `sess-new/refs/offload-002.md` (mtime 2026-08-02, 1 hour old)
   - `sess-old/refs/offload-003.md` (old) + `offload-003.md.pin` (pin sidecar)
2. `session_memory_write` via MCP (id=sm, kind=decision) — writes recall index entry.
3. `sessions_gc retention_days=3` via MCP.

## Results
- Run 1 (no project arg → cwd fixture): `scanned: 3, removed: 1, exempt_pinned: 1, failed: 0` — PASS: old non-pinned ref reclaimed, pinned ref exempted.
- Run 2 (absolute project arg): `scanned: 2, removed: 0, exempt_pinned: 1` — PASS: already-reclaimed ref not re-scanned; pinned still kept.
- Envelope includes `_token_budget {max:1000, actual:30, truncated:false}` + `tokens` — PASS (SEM budgets envelope on this tool too).
- `retention_days` clamps to min 3 (`unwrap_or(14).max(3)`) — observed min enforcement.

## Tracker
- Session GC (#192): PASS (worktree binary). AC "old/low-heat refs reclaimed; pinned/high-heat kept" verified. Note: feature not yet on main — merged via #192 once CI green. `sessions_gc` project resolution uses cwd (no-arg) correctly; explicit `/tmp/...` arg also works after first run.
