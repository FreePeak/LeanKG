# SESSION HANDOFF — LeanKG Hackathon (resume from here)

**Updated:** 2026-08-22 (post-H11 merge) · **Session goal file:** `~/.local/share/opencode/goals/ses_fdad784c8ffeoX8syoqqUBMlQ8.json` (status: active, cycle 4/∞)
**Central SoT:** [`docs/roadmap-tracker.md`](../roadmap-tracker.md) §6 · **Running log:** [`HACKATHON.md`](../../HACKATHON.md) · **Cycle reports:** `docs/cycles/cycle-01.md`

---

## 1. One-paragraph state

Hackathon runs on branch `feature/hackathon` (worktree `.worktrees/hackathon`), rolling PR **#247 → main: OPEN, MERGEABLE, ALL CI CHECKS GREEN** (incl. new npm-parity gate). Cycle 1 complete: R1 live sweep of all 76 MCP tools found 7 real bugs (all fixed TDD-first), then 5 features landed and merged to the branch: connect, audit log, npm parity, quickstart smoke, export --markdown. Branch head `ed424591`, pushed. lib tests **1126 green**. Only open work item: **H9 doctor --deep** (subagent cancelled 3× — implement directly or retry single dispatch).

## 2. Branch / worktree map

| Worktree | Branch | SHA | State |
|---|---|---|---|
| `.worktrees/hackathon` | `feature/hackathon` | e5433676 | **MAINLINE — pushed to origin, = PR #247 head minus cycle docs? NO: pushed at e5433676; HANDOFF/cycle-docs commits land on top** |
| `.worktrees/feat-exportmd` | `hackathon/feat-exportmd` | 9073782d | ✅ H11 DONE — **needs merge back into feature/hackathon** (already contains merge of e5433676 as parent 9f35436b) |
| `.worktrees/feat-doctor` | `hackathon/feat-doctor` | c1d0b7d0 | ⏳ H9 NOT STARTED (only synced with hackathon HEAD; agent cancelled 2×) |
| `.worktrees/feat-audit` | `hackathon/feat-audit` | 4b4f966d | merged (e5433676) — deletable |
| `.worktrees/feat-connect` | `hackathon/feat-connect` | a7b17d0d | merged (cbd5c44e) — deletable |
| `.worktrees/feat-npm` | `hackathon/feat-npm` | 20869eec | merged (8dadc1fe) — deletable |
| `.worktrees/feat-quickstart` | `hackathon/feat-quickstart` | dfac6b8c | merged (f48df7f0) — deletable |
| `.worktrees/fix-mcp`, `fix-engine` | `hackathon/fix-*-layer` | cd60aff2/3f070c8f | merged (b7cda6c5/0d4715aa) — deletable |
| `/Users/linh.doan/orca/workspaces/cozo-removal` | `feat/remove-cozo-datalog` | d14045ff | PRE-HACKATHON WIP: SQL-first seam + removal plan doc — future W8 track, untouched |
| parent repo | `main` | 0b2ee2cc | local behind origin by tracker commit; origin/main has PRs #242–244 |

## 3. What shipped (all on PR #247 unless noted)

**Pre-hackathon (already squash-merged to main):**
- #242 cozo purge (matrix test 6/6, doc truth, −413 LOC dead code)
- #243 QN-collision disambiguation + EEXIST regression guards + migrate TLS fix
- #244 roadmap-2027.md + prd-enterprise.md + roadmap-tracker.md

**Hackathon cycle 1:**
- R1 sweep: 128 live calls, 51 PASS / 18 PASS_EMPTY / 7 FAIL → report `docs/analysis/hackathon-sweep-R1.md`
- 7 bug fixes (RED→GREEN each): update_knowledge upsert · mcp_index_docs watchdog yield · project-key canonicalization · export path anchoring · dynamic-ontology schema adoption (+readonly sslmode URL fix) · hang-trio N+1 batching (get_context 72s→2.5s) · agent_focus wedge kill (+bounded pool wait). get_context/temporal/check_consistency latency collapsed.
- Features: **ENT-1 audit log** (migration 006, hash chain, <2ms recorder, MCP+REST hooks, `leankg audit export/verify` — live chain verified) · **PLG-1 connect** (4 clients, idempotent, --remote/--remove; fake-HOME live proof) · **npm parity** (CI guard, wrapper 0.17.9→0.26.0, release publish wiring) · **quickstart smoke** (88s total vs 300s budget; weekly CI cron w/ pgvector service) · **export --markdown** (12,864-line deterministic graph docs; only generated_at differs between runs)

## 4. Verification cheatsheet

```bash
cd .worktrees/hackathon
cargo build --release                      # ~4-5 min cold, 0 warnings required
cargo test --release --lib                 # expect ≥1126 passed, 0 failed
cargo fmt --all -- --check && cargo clippy --all -- -D warnings
set -a; source ../.env; set +a             # LEANKG_PG_URL — REMOTE PG ONLY, NEVER DOCKER
target/release/leankg mcp-http --port 9701 --project "$PWD" &
curl -s localhost:9701/health              # then JSON-RPC POST /mcp tools/list, tools/call
scripts/quickstart_smoke.sh                # full timed e2e (~90s)
bash tests/sync_npm_version_test.sh        # npm parity script tests
```

4. When PR #247 stable and CI green → `gh pr merge 247 --squash` (or keep rolling if more cycles stack on it).
5. Then next backlog items: H4 provenance labels surfacing, H6 tool consolidation 76→~70 (update redundant_tools_matrix + instructions/leankg-tools.md together), H7 stable-tool-contract doc+CI guard, H8 benchmark regression gate, H10 usage dashboard, H12 README quickstart refresh.
6. Longer-term tracks: W8 SQL-first seam adoption per `docs/plan-remove-cozo-datalog-sql-migration.md` (worktree at orca/cozo-removal); ENT-3 SSO after audit/RBAC.

## 6. Hard rules (do not violate)

- Remote Postgres ONLY (`source ../.env` → LEANKG_PG_URL). NEVER create Docker containers/local PG.
- Main is protected: changes land via squash PRs (`gh pr create` → `--squash`). Direct push rejected.
- Conventional commits, NO AI attribution / Co-Authored-By. Pre-commit hook auto-runs fmt+clippy.
- Always `--release` (debug profile has debug=false).
- TDD mandatory: failing test first, then fix/feature.
- Never paste personal host paths into committed files.
- Goal loop: infinite until user cancels; keep HACKATHON.md + docs/cycles/* updated every round.

## 7. Known quirks learned this session

- Parallel multi-`task` dispatches were cancelled repeatedly → use ONE subagent at a time.
- Cargo.lock merges: take either side, `cargo build --release` regenerates, re-add.
- macOS /var symlink breaks naive canonicalize for nonexistent paths — canonical_project_root_in now lexically resolves `..` (backend.rs).
- `git stash` is repo-global across worktrees — avoid stashing while agents share the repo.
- Indexing ./src over remote PG ≈ 350ms/statement → ~45min for full src corpus; prefer small fixtures or background runs.
