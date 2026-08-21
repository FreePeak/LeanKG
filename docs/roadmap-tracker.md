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
| W5 | Fix red `tests/redundant_tools_matrix.rs` (P0-a) | 🚧 IN PROGRESS | worktree `refactor/cozo-cleanup` | test-only fix, matrix asserts ==76 |
| W6 | Purge cozo from generated docs + ontology YAMLs (P0-c) | 🚧 IN PROGRESS | worktree `refactor/cozo-cleanup` | generator.rs/wiki.rs → Postgres+pgvector; concepts.yaml cleanup |
| W7 | Delete orphaned db/mod.rs fns + dead arms + broken script (P1) | ⬜ PENDING | worktree `refactor/cozo-cleanup` | see audit §4 list |
| W8 | SQL-first seam adoption (adopt worktree plan P0) | ⬜ PENDING | branch `feat/remove-cozo-datalog` | existing WIP at orca workspace; land sql.rs seam |
| W9 | Known finding: qualified_name collision (UNIQUE) | ⬜ PENDING | worktree | 52% dup QNs breaks embed on real data |
| W10 | Known finding: `leankg index` EEXIST bug | ⬜ PENDING | worktree | re-index fails after wipe |
| W11 | Tool consolidation round 2 (76→~70) | ⬜ PENDING | after W5 | candidates: get_graph_report, orchestrate, traceability quartet |
| W12 | npm wrapper version sync automation | ⬜ PENDING | quick win | npm/leankg at 0.17.9 vs crate 0.26.0 |
| W13 | Phase-1 enterprise features (see PRD) | ⬜ PENDING | per-quarter | starts with ENT-1 observability/audit-log foundation |

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
| 2026-08-22 | W5+W6+W7 refactor wave 1 | worktree refactor/cozo-cleanup: matrix test fix, doc-lies fix, dead-code purge |
| 2026-08-22 | W9+W10 known-findings fixes | worktrees: QN UNIQUE dedup + index EEXIST fix |
| 2026-08-22 | W12 npm sync | release-please/npm version automation |

---

## 5. Next Actions (pick up here)

1. Land W5–W7 PR → merge main → update this file.
2. Start W8 (SQL-first seam) — follow worktree plan doc phases; parity harness gates each wave.
3. W9/W10 fixes with TDD (repro tests first).
4. Then begin PRD ENT-1 (audit log foundation) per roadmap Q3'26 theme.
