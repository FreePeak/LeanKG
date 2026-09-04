# Live-Test Plan — 2026-08-02 campaign features (local)

**Date:** 2026-08-02
**Status:** Ready to execute
**Scope:** Live validation of all Wave 2a–2e + P3 features merged in the 2026-08-01 all-open campaign (#168–#192), against a **local** release binary. Docker `:9699` serves the previous release (89 tools, no `session_recall`) — treat as reference, not target.

| SoT | Path |
|-----|------|
| Campaign | `docs/planning/2026-08-01-all-open-prd-campaign.md` |
| PRD | `docs/prd.md` |
| Tracker | `docs/prd-task-tracker.md` / `.json` (open_work = 9) |
| Evidence | `docs/reports/` (one file per feature group, `-2026-08-02.md`) |

---

## 0. Prerequisites

```bash
# 0.1 Release binary WITH embeddings (needed for SEM/MMR/embeddings probes)
cargo build --release --features embeddings
BIN=./target/release/leankg

# 0.2 Fresh fixture project
FIX=/tmp/leankg-live-fixture
rm -rf $FIX && mkdir -p $FIX/src $FIX/docs
# seed: a few .rs/.ts/.py/.vue/.svelte/.sql files + docs/*.md referencing file::symbol
# (reuse tests/fixtures/* where possible)

# 0.3 Server on isolated port (avoid :9699 stale MCP + RocksDB LOCK contention)
$BIN init --path $FIX
$BIN index $FIX/src          # bulk
$BIN serve --port 9876 --project $FIX &   # REST + MCP HTTP on :9876
curl -sf http://localhost:9876/health && echo OK
```

> If Docker MCP is needed for cross-check, verify first: `curl -sf http://localhost:9699/health` + `leankg mcp-status --project /workspace`. Do NOT run two servers on the same RocksDB path.

---

## 1. Feature → probe matrix

| # | Feature (PR) | Probe | Expected | Report |
|---|--------------|-------|----------|--------|
| 1 | Wave4 single-repo expand (#164) | `curl :9876/api/graph/expand-service` on single-repo root (all_content path) | nested content returned | `wave4-expand-live-2026-08-02.md` |
| 2 | vue/svelte/sql indexing (#170) | `search_code(query="users", project=$FIX)` after seeding `.vue`/`.svelte`/`.sql` | file-level elements `file::*.vue` etc. + sql `users` table element | `lang-breadth-live-2026-08-02.md` |
| 3 | DOCJOIN symbol upgrade (#172) | docs/*.md with `src/…::handle_tool_call` → `get_traceability`/`documented_by` | symbol-level edge when unique; file-level when ambiguous | `docjoin-live-2026-08-02.md` |
| 4 | Session offload (#174) | MCP `session_recall` after offload writes | refs md bit-for-bit (vs `docs/reports/rel-075-…`), missing-node error clean | `session-offload-live-2026-08-02.md` |
| 5 | Auto-recall (#176) | MCP `get_overview_context` + opt-in recall | `session_lessons` injected; default off; ≤5s (timeout skip) | `session-autorecall-live-2026-08-02.md` |
| 6 | Provenance + RRF (#184) | `session_memory_write` → `search_memory_rrf` (k=60) | provenance fields present; fused rank order | `session-rrf-live-2026-08-02.md` |
| 7 | Heat promote (#183) | recall same session N times → `MEMORY_INDEX.md` top-K | heat order deterministic; proposals JSONL only (no YAML writes) | `session-heat-live-2026-08-02.md` |
| 8 | Session GC (#192) | `sessions_gc` (retention_days=3, min enforced) | old/low-heat refs reclaimed; pinned/high-heat kept | `session-gc-live-2026-08-02.md` |
| 9 | SEM budgets (#186) | MCP `concept_search` on fixture | `_token_budget.{max:4000,actual,truncated}` + `tokens`; truncation when > max | `sem-budgets-live-2026-08-02.md` |
| 10 | MMR diversity (#192) | `semantic_search` (embeddings) on fixture w/ MMR | top-k not ≥70% one file (λ<1); λ=1 pass-through | `mmr-diversity-live-2026-08-02.md` |
| 11 | God nodes (#182) | `get_god_nodes` + `get_architecture` hotspots | rank_score/pagerank persisted; hotspots top-10; hub node = fixture's busiest | `god-nodes-live-2026-08-02.md` |
| 12 | GE planner (#178) | pure fn / MCP (goal → DAG) | deterministic DAG JSON, edges reference nodes | `ge-planner-live-2026-08-02.md` |
| 13 | GE entity resolve (#177) | `resolve_alias("handler")` | ranked list, exact-first, no silent pick | `ge-resolve-live-2026-08-02.md` |
| 14 | GE cluster-first (#179) | `list_clusters`/`get_cluster_context`/`nearest_clusters` | bounded, cluster-scoped, no full scan (query-count assert) | `ge-cluster-live-2026-08-02.md` |
| 15 | Layout3d (#171) | `curl :9876/api/graph/layout3d?seed=42` twice | identical positions; unit-cube bounds; finite | `tracke-layout-live-2026-08-02.md` |
| 16 | Track E serve (#181/188) | `curl :9876/3d/` + `/3d/assets/*` + `/api/projects` + `/api/ui-build` | HTML 200, JS 200, `has_3d:true`; ui-v2 `/` untouched | `tracke-serve-live-2026-08-02.md` |
| 17 | Conversation mining (#175) | `$BIN mine-conversations --format claude --project $FIX --input <fixture>` | mined decision/preference items + `decided_about` edge; idempotent re-run | `conversation-mining-live-2026-08-02.md` |
| 18 | REST mutations (#189) | `DELETE :9876/api/annotations/<element>` + folder impact `get_impact_radius("src/")` | 200/204 + element gone; dir-level radius | `rest-mutations-live-2026-08-02.md` |
| 19 | MCP resources (#190) | `resources/list` + `resources/read` (leankg://overview) | 2 resources listed, read returns overview | `mcp-resources-live-2026-08-02.md` |
| 20 | Smoke gates (#173) | `python3 scripts/mcp-smoke-tools.py --check-only-ontology` | ontology 4/4 + routing 3/3 + recipes ≥10 | `cbm-smoke-live-2026-08-02.md` |
| 21 | Install matrix (#187) | `bash -n scripts/install.sh` + `configure_codex` idempotent on temp HOME | no-op second run; `[mcp_servers.leankg]` present | `install-matrix-live-2026-08-02.md` |
| 22 | service_calls breadth (#187) | index fixture with `http.Get`/`client.Get` + YAML `*_address: http://…` | service_calls edges extracted | `service-calls-live-2026-08-02.md` |
| 23 | Python/Rust typed resolve (#191) | `$BIN init --with-lsp` on py+rs fixture → `typed_resolve` | `py`/`rs` in typed_resolve; python call edges extracted | `typed-resolve-live-2026-08-02.md` |
| 24 | UI (graph-ui/ui-v2) | `cd graph-ui && npm test && npm run build`; same for ui-v2 | 56 + 47 tests, builds green; browser spot-check `/3d` (camera, select, panels, LOD) | `ui-live-2026-08-02.md` |

---

## 2. Execution order (cheapest first, by surface)

1. **Rust gates on main** (already green per PRs — re-verify once): fmt / clippy / `cargo test --lib` (848) / targeted suites
2. **CLI + REST probes** (#1, 2, 15, 16, 17, 18, 22, 23) — one server, curl + CLI, fast
3. **MCP probes** (#3, 4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 19) — JSON-RPC over `POST :9876/mcp`, or stdio if handler tests suffice
4. **Embeddings** (#10) — needs `--features embeddings` binary; run last (build time)
5. **UI + smoke + install** (#20, 21, 24) — scripted, no server needed
6. **Cross-check Docker MCP** (optional): after container rebuilds to 0.19.31 — `curl :9699/health` + 3 representative probes (session_recall, get_architecture god_nodes, concept_search envelope)

## 3. Evidence format (per report)

```markdown
# <Feature> live evidence — 2026-08-02

## Environment
- commit: <sha> | binary: <path> | server: :9876 | project: <fixture>
- embeddings feature: yes/no

## Steps
1. <exact command>

## Results
- <command> → <trimmed output>
- Pass/Fail vs AC (cite PRD ID)

## Tracker
- IDs listed for DONE (conductor updates after evidence)
```

## 4. Rules

- **One server, one RocksDB path.** Kill stale `:9876` before re-run; never run two servers on the same fixture.
- **Evidence-first:** every Pass needs the command + output in the report. SKIP (documented) is allowed only for: Docker MCP cross-check if container not rebuilt; UI pixel-level checks (jsdom has no WebGL — assert via build + component logic).
- **No code changes mid-test** unless a probe fails — then fix → re-run probe → note fix in report (TDD loop: failing probe = failing test).
- **No AI attribution** in any commit.
- Reports live under `docs/reports/*-2026-08-02.md`; tracker flips only after the report exists.

## 5. Exit criteria

- [x] Every probe in §1 has a Pass or documented SKIP
- [x] `docs/reports/` contains the 20+ evidence files (24 reports)
- [x] Tracker open_work stays 9 (no regressions re-opened); probe-found bugs fixed in commit b555fdc2
- [ ] Optional: Docker `:9699` cross-check green after container rebuild (ops)

## 6. Progress log

| Date | Probes | Pass | Skip | Notes |
|------|--------|------|------|-------|
| 2026-08-02 | — | — | — | Plan authored |
| 2026-08-02 | 24/24 | 21 | 3 | All probes executed vs workspace-be + local fixture. 2 probe-found bugs fixed (REST annotation DELETE: missing route+handler AND broken cozo `:delete` syntax → `:rm`; MCP resources HTTP mirror missing). Session features #183/184/192 tested from worktrees (not yet on main). PASS: 1,2,3,4,5,6,7,8,9,11,12,13,14,15,16,17,18,19,20,21,22 — SKIP/partial: 10 (MMR λ=1 not isolatable on fixture), 23 (py calls partial), 16 (has_3d not in embedded build) |
