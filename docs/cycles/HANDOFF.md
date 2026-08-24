# SESSION HANDOFF — LeanKG Hackathon (resume here in a new session)

**Written:** 2026-08-23 · **Goal file:** `~/.local/share/opencode/goals/ses_fdad784c8ffeoX8syoqqUBMlQ8.json` (active, infinite loop)
**Central SoT:** `docs/roadmap-tracker.md` §6 (on main) · **Running log:** `.worktrees/hackathon/HACKATHON.md` · **This file:** `docs/cycles/HANDOFF.md`

---

## 1. Mission (unchanged)

Infinite autonomous loop on branch `feature/hackathon`: brainstorm → plan → implement → test → live-test → fix → validate, repeating. Remote Postgres ONLY (`LEANKG_PG_URL` in repo `.env`), NEVER Docker. TDD mandatory. Main lands via squash PRs only (ruleset-protected). Conventional commits, NO AI attribution. Always `--release`.

## 2. Delivered so far (all squash-merged to main)

| Cycle | PR | Content |
|---|---|---|
| Pre-hack | #242–244 | Cozo purge, QN-collision + EEXIST fixes, roadmap-2027 + prd-enterprise + tracker docs |
| C1 | #247 (squash `cf357ad8`) | R1 sweep 128 calls: 7 bugs fixed (update_knowledge upsert; index_docs watchdog; project-key canonicalization; export anchoring; ontology schema adoption+sslmode URL; hang-trio batching get_context **72s→2.5s**; agent_focus wedge kill). Features: H1 connect, H2 ENT-1 audit log (hash chain), H3 npm parity, H5 quickstart smoke 88s |
| C2 | same PR | Re-sweep 0 FAIL_ERROR; identity cluster fixes; **O(n²) token-budget fix — consistency 211s→5.8s**, temporal 12min→7s; data quality **orphans 432/1000→0/72,699, dup QNs 10→0/14,091** |
| C3 | #249 (`63c37714`) | H7 tool-contract doc + CI drift guard; H12 README refresh; H4 provenance labels all graph surfaces (36k edges live); H6 consolidation 76→73 tools with deprecation history |
| C4 | #251 (`542a15e8`) | H10 usage dashboard (`leankg dashboard`, grouped-SQL, bars+JSON); H8 perf regression gate (perf_gate.sh + CI workflow; baseline index 18.3s/boot 19ms/search 13ms) |
| C5 | #253 (`d01d9295`) | **W8 begins**: SQL-first seam adopted from dormant WIP (`src/db/sql.rs`); wave-1a converted db/keys.rs (7 sites) + content_hash.rs (2); caught NULL-rendering + validate_key data-loss bugs |

State at last green: lib **1213✅** · fmt/clippy/build clean · tools **73 tiered** · trackers #248/#250/#252 merged.

## 3. ⚠️ IN-FLIGHT RIGHT NOW (W8 wave-1b, UNCOMMITTED)

Worktree: `.worktrees/w8-wave1b` (branch `hackathon/w8-wave1b` @ d01d9295).
`git status`: **`M src/db/backend.rs` only** — a partial edit that does NOT compile yet.

Done in that edit:
- Trait default methods added to `DbBackend` (after `touch_api_key_last_used`): `upsert_knowledge_entry`, `find_knowledge_entry`, `delete_knowledge_entry_by_id`, `search_knowledge_entries`, `list_knowledge_by_element/feature/environment` (default-Err pattern like api_keys).

Still TODO to make it compile & pass:
1. Add helper fns near the keys PG impls in backend.rs: `fn opt_text(Option<&str>) -> SqlParam` and `fn knowledge_entry_from_row(&crate::db::sql::SqlRow) -> crate::db::models::KnowledgeEntry` (mirror `row_to_knowledge_entry` field-for-field; created_at/updated_at via `r.int(...)`) and `fn escape_like(&str) -> String` (escape `%_\`).
2. Rewrite bodies of the 8 knowledge fns in `src/db/mod.rs` (~lines 669–905) to delegate to the trait methods; DELETE their Datalog strings + `row_to_knowledge_entry` if now orphaned.
3. RED-first integration test `tests/pg_sql_wave1b_test.rs` (pattern = tests/pg_sql_wave1_test.rs): upsert/find/delete parity round-trips incl. NULL optional cols; search ILIKE semantics vs legacy regex; by_element/by_feature/by_environment; limit ordering by updated_at DESC.
4. Gates: `CARGO_TARGET_DIR=/tmp/opencode/t-w8b cargo build --release` (0 warnings), lib tests, fmt --check, clippy --all -- -D warnings, wave-1b test vs remote PG (`set -a; source ../../.env; set +a`).
5. Live proof: boot `$BIN mcp-http :9872 --project <abs worktree>` after indexing small fixture; curl add_knowledge→search_knowledge→update_knowledge→delete_knowledge; logs show `kind="sql"` and zero `cozo=` lines for those ops.
6. Update plan doc execution log (wave-1b done + counts). Commits: "refactor(db): knowledge_entries SQL conversion (W8 wave-1b)".

Then push branch → rolling cycle-6 PR (same pattern as before).

## 4. Branch / worktree map

| Path | Branch | State |
|---|---|---|
| parent repo | main | tracks origin/main `03b969c4`… check latest (#253 merged as d01d9295) |
| `.worktrees/hackathon` | feature/hackathon | pushed 5d2e8c29 (C5 docs); PR #253 MERGED — reset to origin/main when resuming |
| `.worktrees/w8-wave1b` | hackathon/w8-wave1b | ⚠️ IN-FLIGHT partial edit (see §3) |
| `/Users/linh.doan/orca/workspaces/cozo-removal` | feat/remove-cozo-datalog (+wip/cozo-sql-seam-backup dd8018fa) | reference-only; seam already adopted |
| misc `.worktrees/feat-*`, `fix-*` | merged branches | deletable |

## 5. Resume queue (in order)

1. **Finish wave-1b** per §3 checklist → commit → push → cycle-6 rolling PR → merge on green.
2. **Wave-2 conversions** (disjoint files, fan-out one agent at a time): graph/query.rs (~105 sites), embeddings/state.rs (15), ontology/query.rs (8), auth/accounts.rs (9), tokens.rs (6), handler.rs (4), server.rs (3), clustering/inventory/pipeline/write_bus/schema leftovers.
3. **P3 deletion sweep**: remove `translate.rs` (4.3k LOC), `mutability.rs`, `fake.rs` (1.4k), `escape_datalog`, `preprocess_datalog_query`; decide `run_raw_query` fate (deprecate → NL-only).
4. **Dependabot**: 18 vulnerabilities flagged (9 high) — triage `cargo update` / bump PRs (noted 2026-08-23).
5. Carried minors: index_prd zero-work bug; get_cluster_skill parent-repo path bleed.
6. Keep looping: re-sweeps each cycle, HACKATHON.md + tracker §6 updates, squash-merge stable slices.

## 6. Infra quirks (learned the hard way)

- Subagent `task` dispatches fail randomly ("Endpoint unavailable", "Failed to execute statement") → retry once, then implement directly.
- Parallel multi-task calls get cancelled → ONE agent at a time.
- Shared cargo target dir (`~/.cache/cargo-target/leankg-target`) gets lock-contended → use isolated `CARGO_TARGET_DIR=/tmp/opencode/t-*` per workstream; builds take 10–20 min under load → always background nohup+poll.
- Full `index ./src` over remote PG ≈ 45 min (~350ms/statement) — prefer fixtures/background.
- psql needs `sslmode=verify-full`→`require` URL rewrite; leankg handles TLS natively.
- macOS /tmp→/private/tmp symlink affects path identity (canonical_project_root handles it).
- `git stash` is repo-global across worktrees — avoid.
- tracing logs now go to stderr (stdout machine-readable); JSON-RPC pattern: POST /mcp initialize → tools/list → tools/call.

## 7. Verify cheatsheet

```bash
cd .worktrees/hackathon   # or w8-wave1b
cargo build --release && cargo test --release --lib    # expect ≥1213 green
cargo fmt --all -- --check && cargo clippy --all -- -D warnings
set -a; source ../.env; set +a                          # LEANKG_PG_URL
bash scripts/perf_gate.sh --baseline benchmarks/baseline.json --metrics '{"index":18315,"server_boot":19,"search_code":13,"get_impact_radius":13}'
scripts/quickstart_smoke.sh                             # ~90s e2e
leankg doctor --deep --project "$PWD"                   # exit ≤1
```
