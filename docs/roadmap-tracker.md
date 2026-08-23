# LeanKG Central Progress Tracker

> **THIS IS THE SINGLE SOURCE OF TRUTH for the 2026–2027 roadmap execution.**
> Update this file after every completed step. Last updated: 2026-08-22.

## 0. Mission

Position LeanKG as the **deterministic, self-hostable, MCP-native code knowledge graph** for enterprises and startups — "explainable context" that embedding-only incumbents (Cursor, Augment, Sourcegraph) cannot offer.

**Companion documents:**
| Document | Purpose |
|---|---|
| [`docs/roadmap-2027.md`](roadmap-2027.md) | 1-year roadmap (Q3'26 → Q2'27), quarterly themes |
| [`docs/prd-enterprise.md`](prd-enterprise.md) | PRD: enterprise + startup feature requirements (FR-ENT-*, FR-PLG-*) |
| [`docs/plan-remove-cozo-datalog-sql-migration.md`](plan-remove-cozo-datalog-sql-migration.md) | CozoDB/Datalog full removal plan (in worktree `feat/remove-cozo-datalog`) |
| [`docs/pg-migration-kanban.md`](pg-migration-kanban.md) | Historical: Cozo→Postgres migration (Phases 0–9 DONE) |

---

## 1. Status Dashboard

| # | Workstream | Status | Owner | Evidence / Notes |
|---|-----------|--------|-------|------------------|
| W1 | Discovery: codebase audit + architecture | ✅ DONE | agent | §3.1 findings below; 118k LOC, 79 MCP tools, v0.26.0 |
| W2 | Research: competitors + market + monetization | ✅ DONE | agents | §3.2 summary; full reports in roadmap-2027.md §1–2 |
| W3 | Roadmap 1-year doc | ✅ DONE | primary | docs/roadmap-2027.md |
| W4 | Enterprise/startup PRD | ✅ DONE | primary | docs/prd-enterprise.md |
| W5 | Fix red `tests/redundant_tools_matrix.rs` (P0-a) | ✅ DONE | PR #242 | matrix 6/6 green; REMOVED_TOOLS+11; assert ==76 |
| W6 | Purge cozo from generated docs + ontology YAMLs (P0-c) | ✅ DONE | PR #242 | generator.rs/wiki.rs truthful; 3 YAMLs purged; rg CozoDB src/doc/ = 0 |
| W7 | Delete orphaned db/mod.rs fns + dead arms + broken script (P1) | ✅ DONE | PR #242 | −413 lines; pg-cli-sweep.sh deleted; token_budget arms pruned |
| W8 | SQL-first seam adoption (adopt worktree plan P0) | ⬜ PENDING | branch `feat/remove-cozo-datalog` | existing WIP at orca workspace; land sql.rs seam |
| W9 | Known finding: qualified_name collision | ✅ DONE | PR #243 | extraction-time disambiguation; live: 2711/2711 distinct QNs; regression_qn_collision.rs added |
| W10 | Known finding: `leankg index` EEXIST bug | ✅ DONE | PR #243 | already fixed by 56a0a86b; locked with tests/regression_index_eexist.rs (CLI e2e double-index exit 0) |
| W11 | Tool consolidation round 2 (76→~70) | ⬜ PENDING | hackathon backlog | candidates: get_graph_report, orchestrate, traceability quartet |
| W12 | npm wrapper version sync automation | ⬜ PENDING | quick win | npm/leankg at 0.17.9 vs crate 0.26.0 |
| W13 | Phase-1 enterprise features (see PRD) | ⬜ PENDING | **hackathon** | starts with ENT-1 observability/audit-log foundation |

Status legend: ⬜ pending · 🚧 in progress · ✅ done · ⛔ blocked · ❌ cancelled

---

## 2. Execution Rules (per user mandate)

1. **Workflow per step:** plan → review → execute → test → update docs → merge to origin main.
2. **TDD mandatory:** failing test first, then implementation.
3. **Git worktrees** for every feature/refactor branch; merge to main after green.
4. **Live local testing** against the **remote Postgres in `.env` (`LEANKG_PG_URL`)**. NEVER create a Postgres docker container.
5. **Fan out subagents** wherever steps are independent.
6. **No approvals needed** — pick recommended/best-practice options autonomously.

---

## 3. Key Findings (discovery, 2026-08-22)

### 3.1 Codebase state
- v0.26.0, ~118k LOC Rust, 1,219 unit tests (~4s), ~600 integration tests (NOT in CI gate), CI = lib tests + fmt + clippy + ui-v2 build.
- Storage: **Postgres+pgvector only** (cozo crate removed from Cargo.toml). BUT Datalog survives as internal query IR: 238 `run_script()` call sites → runtime translator `src/db/pg/translate.rs` (4,301 LOC) + test fake `src/db/fake.rs` (1,402 LOC).
- MCP registry: **79 tools** (commit 541ff626 removed 11 thin wrappers but broke `tests/redundant_tools_matrix.rs` — 4/6 RED on main).
- Monolith hotspots: extractor.rs 7.5k, graph/query.rs 7.5k, main.rs 6.9k, handler.rs 5.1k, web/handlers.rs 4.8k.
- npm wrapper lags crate by 9 minors (0.17.9 vs 0.26.0).

### 3.2 Market / competitive
- KG market ~$2B growing 20–33%/yr; code-intel budgets far larger ($400–600K/yr per eng org on AI tools).
- MCP won (Linux Foundation, 97M monthly SDK downloads); enterprise layer forming: private registries, gateways, OAuth 2.1 remote servers.
- Empty quadrant: **local-first × deterministic-graph × hybrid-local-semantics × permissive license**. GitNexus (45k★) is PolyForm Noncommercial TS; Stack Graphs archived; Bloop archived; Sourcegraph killed free tiers.
- Pricing benchmarks: Team tier $20–40/seat/mo band; enterprise floor $16K+/yr proven; metering backlash → flat predictable pricing is a differentiator.
- Recommended model: **open core (permissive/AGPL decision documented in roadmap) + Team cloud $25/seat/mo + Enterprise self-hosted $15–25K/yr + air-gap custom**.
- Datadog check: NO datadog/APM telemetry exists anywhere in the codebase (only a competitor mention in one analysis doc). Nothing to remove.

### 3.3 Removal plan (from audit)
| Priority | Action |
|---|---|
| P0-a | Fix `tests/redundant_tools_matrix.rs` to current 76-tool reality |
| P0-b | Adopt/land worktree `feat/remove-cozo-datalog` sql.rs seam (owns the machinery for full Datalog removal) |
| P0-c | Fix generated-doc lies ("stores data in CozoDB") in doc/generator.rs + wiki.rs; purge `cozo_integration`/`rocksdb_backend` from ontology YAMLs |
| P1-a | Delete orphaned db/mod.rs fns/structs (traceability quartet etc.), drop stale `#[allow(dead_code)]`, fix unused_mut warning |
| P1-b | Delete broken scripts/pg-cli-sweep.sh, prune .gitignore cozo entries, token_budget dead arms, delete stale branch feat/v2-cozo-deprecation |
| P1-c | Sweep ~217 cozo/~57 datalog comment refs (ride along with waves) |
| P2-a | Wave-by-wave: convert 238 run_script sites → parameterized SQL via seam; then DELETE translate.rs, mutability.rs, fake.rs, escape_datalog, preprocess_datalog_query |
| P2-b | Decide run_raw_query fate (last user-facing Datalog surface): deprecate → NL query_graph only |
| P2-c | Tool consolidation round 2 (76→~70) |

---

## 4. Session Log

| Date | Step | Result |
|------|------|--------|
| 2026-08-22 | Goal created, decomposed into 10 todos | goal persisted ses_fdad784c8ffeoX8syoqqUBMlQ8 |
| 2026-08-22 | W1+W2 discovery (4 parallel subagents) | Audit + architecture + market + competitor reports done |
| 2026-08-22 | W3+W4 planning docs | roadmap-2027.md + prd-enterprise.md written |
| 2026-08-22 | W5+W6+W7 refactor wave 1 → **PR #242 merged** | 12 files +52/−413; all gates green |
| 2026-08-22 | W9+W10 known-findings fixes → **PR #243 merged** | QN disambiguation live-verified vs remote PG; migrate TLS fix bonus |
| 2026-08-22 | Final verification on main @ deb8e92e | build 0 warnings · lib 1043✅ · matrix 6/6✅ · fmt✅ · clippy✅ |

> **Process note:** origin/main is now ruleset-protected (squash-only, PR-required). All future work lands via PR from worktree branches. Local `main` tracks `origin/main`.

---

## 5. Next Actions (pick up here)

**ACTIVE: HACKATHON phase** — see §6 below. Branch `feature/hackathon`, worktree `.worktrees/hackathon`.
After hackathon: W8 (SQL-first seam waves) per `plan-remove-cozo-datalog-sql-migration.md`; then ENT-1.

---

## 6. Hackathon (7-day continuous loop, branch `feature/hackathon`)

**Charter:** 24/7 build loop — brainstorm → plan → implement → test → live test → fix → validate, repeating until the worktree is fully green and every MCP tool has been live-exercised against the remote PG (`.env LEANKG_PG_URL`, never Docker).
**Rolling PR:** [#247](https://github.com/FreePeak/LeanKG/pull/247) · **Handoff:** `feature/hackathon` → `docs/cycles/HANDOFF.md` · **Cycle reports:** below.

| Cycle | Scope | Status | Report |
|-------|-------|--------|--------|
| C1 / R0-R1 | Baseline gates + full live sweep of 76 tools (128 calls): 51 PASS, **7 FAIL found**, p50 4.9s/p95 90s N+1 | ✅ | `feature/hackathon:docs/cycles/cycle-01.md`, sweep report in `docs/analysis/hackathon-sweep-R1.md` |
| C1 / R2 | **7 bugs fixed TDD-first**: update_knowledge upsert · index_docs watchdog · project-key canonicalization · export path anchoring · ontology schema adoption · hang-trio batching (get_context **72s→2.5s**) · agent_focus wedge kill | ✅ | same |
| C1 / R3 | **6 features landed** (backlog H1-H11): connect · ENT-1 audit log · npm parity · quickstart smoke (88s) · export --markdown · doctor --deep (+3 more real bugs found & fixed during live verification) | ✅ | HACKATHON.md R3 |
| C2 / R1 | Full live re-sweep: **0 FAIL_ERROR**, p50 −44%, p95 −50%, audit chain verified (107 entries); regression matrix 4/2/1 → all fixed in R2a-c | ✅ | `docs/analysis/hackathon-sweep-R2.md` |
| C2 / R2 | Identity cluster (yaml anchor preservation, legacy-adoption precedence, --project canonicalization) · O(n²) token-budget fix (**consistency 211s→5.8s**, temporal 12min→7s) · data quality (**orphans 432/1000→0/72,699; dup QNs 10→0/14,091**) | ✅ | banked via #247 squash `cf357ad8` |
| C3 | H7 tool-contract doc+CI drift guard · H12 README quickstart refresh · H4 provenance labels on all graph surfaces (36k edges labeled live) · H6 consolidation 76→73 with deprecation history | ✅ | banked via #249 squash `63c37714`; rolling log HACKATHON.md |
| C4+ | H10 usage dashboard · H8 benchmark regression gate · W8 SQL-first seam waves; loop continues | ⬜ | backlog: hackathon-backlog.md |

State @ cycle-3 close: lib **1183✅** · tools **73 stable-tiered** · fmt/clippy/build clean.

Exit criteria: zero red gates · full tool sweep clean (or documented known-issues) · features landed as commits on `feature/hackathon` with TDD evidence · HACKATHON.md log complete.

