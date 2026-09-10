# LeanKG Task Tracker

**Last synced:** 2026-09-10 — everything through v0.30.0 merged to main (v4.4.3 PRD window: #326 sqlite-default conversion, #331 doc-join batching + bounded temporal_query [#257/#256], #333 sweep path-form normalization [#332], #336/#337/#339 semantic-release pipeline healed + auto releases live, #341 temporal PG translator fix, #342 006_audit_log PG migration restored, #343 integration suites re-pinned to the 3-tool surface). Open bugs: #286 (evidence-gated), #321 (evidence-gated, instrumentation live). User-held PRs: #328/#329/#330 (green), #299 (green), #295 superseded by v0.28.1, #262/#265/#266 need `@dependabot rebase`.
**SoT pairing:** narrative + ACs live in [`docs/prd.md`](prd.md); statuses live here.
**Status legend:** `IN_PROGRESS` (being worked now) · `TODO` (backlog, ordered) · `DONE` (implemented + verified) · `BLOCKED` (needs external input) · `WONT_DO` (explicitly cancelled).

---

## Summary

| Status | Count |
|--------|------:|
| IN_PROGRESS | 1 |
| TODO | 35 (9 live + 26 carry-forward) |
| DONE | 9 |
| Open work | 36 |

**Inventory note (ID-level accounting):** the archived tracker holds **40 open inventory items** (35 master-table `NOT_DONE`/`PENDING`/`PARTIAL`/`OPEN` IDs + 5 `FR-HEA-*` section-table rows). All 40 are accounted for below: FR IDs appear as named rows; each paired `US-*` tracks with its FR (the archive itself pairs them `US-X / FR-X` as one work item); `FR-ZG-01..05` + `US-ZG-01..05` + `FR-B05` are superseded inside the live `FR-ZCP-*` rows (Supersedes column); `FR-HEA-05` is DONE (v4.0.0 §1 cutover). `FR-ZCP-09/10/11/12/13` are **new in v4.1.x–v4.3.0** (no archive IDs). Row-level open work = 1 IN_PROGRESS + 9 live + 26 carry-forward = 36. (The 26 carry-forward rows cover 35 archived open IDs: 3 rows pair multiple US stories with their FR; the inventory not…

| Milestone | Live items | Carry-forward items | Status |
|---|---|---|---|
| M1 — Zero-config attach | 3 | — | **IN_PROGRESS** (FR-ZCP-01 clause-2 roots/list DONE 87e18287; FR-ZCP-02 DONE 0aba41ad+d9ccd8b5; FR-ZCP-13 DONE b251046c) |
| M2 — One-tool surface | 2 | — | **IN_PROGRESS** (FR-ZCP-03 router+ladder DONE 4231d256; **v4.3.1 hard cutover DONE** — registry 1 tool + verb envelope; FR-ZCP-04 install --target outstanding) |
| M3 — Honest search | 2 | FR-HEA-02, FR-HEA-04 | **IN_PROGRESS** (FR-ZCP-06 freshness contract DONE #347; FR-ZCP-05 bridge tier DONE 7d902461+3a68d571 — tsvector FTS + RRF outstanding) |
| M4 — Harness memory | 1 | FR-SMA-01..03, FR-SM-04/05, US-SM-02 | **IN_PROGRESS** (surface DONE #357: mnemopi bank naming/scoping/cursor, session_retain/recall + memory_* verbs; outstanding: hindsight-shaped HTTP API + OMP end-to-end injection AC) |
| M5 — Defensible evidence | 1 | FR-HEA-01, FR-HEA-03 (FR-ZCP-08 harness DONE — pinned/≥3-trial/judge-blind) | TODO (health checks) |
| M6 — Org-scale portfolio | 2 | — | TODO |
| M7 — Embedding correctness | 1 | — | **IN_PROGRESS** (core DONE #351/#353/#355: pinned revisions, model-stamped collections, hard rebuild guard, query-side degrade to L2, entry-point guards, chunker_version hash coupling; outstanding §3.8: query/document prefixes in catalog, 3-signal size+mtime fast-path, per-file atomic replace + truncation accounting, watcher-miss insurance, single-flight leases) |
| M8 — Measured simplicity | 1 | — | **IN_PROGRESS** (FR-ZCP-12 T1 DONE c5b4b991; T3 re-scoped to one-tool CI invariant — landed with v4.3.1; T2 TTFV outstanding) |
| M9 — Three tools + dual backend | 4 | — | **IN_PROGRESS** (FR-3T-01/02/03 DONE; FR-3T-04 live validation complete on this repo — 581 files, 9522 vectors, L1/L2/L3 verified; PR #284 merged (v4.3.x)) |
| Unmilestoned (P3) | — | FR-B16, FR-B51, FR-SURF-06, US-SURF-05, US-GF-10, US-GF-12, FR-EMBED-R4, FR-SMA-05/06, US-SMA-05/06, FR-ZG-06 | TODO |

---

## In Progress

| ID | Title | Started | Notes |
|----|-------|---------|-------|
| FR-ZCP-01 | Contextual project resolution — connection→project mapping (cwd / server-initiated `roots/list` / session registration); `?project=` demoted to escape hatch | 2026-09-03 | **Clause 2 (HTTP roots/list) DONE 2026-09-04** — commit 87e18287: probe rides the initialize SSE response as a second `event: message` frame, answer via POST /mcp with Mcp-Session-Id (mechanism chosen because LeanKG's custom axum dispatcher has no server-to-client channel; streamable-HTTP spec allows request frames in POST response bodies — matches OMP's TS-SDK client behavior); per-connection SessionRootCache; capability-gated (roots object) + list_changed invalidation; 24 tests. Remaining clauses: stdio cwd (clause 1, already works), session registration table (clause 3, partially via leankg install --register-cwd follow-up). Resolution order + cache design in prd.md §3.1; verified anchors: `find_leankg_for_path` `src/mcp/server.rs:588-605`, `resolve_project_db_path` `:637-661`, silent default-schema fallback `:2987-2989` (KILLED by FR-ZCP-02), no `X-LeanKG-Project` header in `src/`, identity = canonical root via `project_identity_keys_in` `src/db/backend.rs:2613-2675` |

## Done — 2026-09-04 implementation sprint (v4.3.0 wave 1–3)

| ID | Title | Evidence |
|----|-------|----------|
| FR-ZCP-02 | Lazy auto-attach + background first index; silent fallback killed; `freshness: cold` | 0aba41ad + d9ccd8b5 — 13 fr_zcp02 tests; auto-attach default-ON, LEANKG_AUTO_ATTACH=0 opt-out, inline ensure_project_indexed removed from request path, mcp_status carries indexing state |
| FR-ZCP-03 | `leankg_context` capability router with L0–L3 degradation ladder | 4231d256 + a86a771c (kick wiring) — src/mcp/router.rs 1142 lines, 23 unit tests; Tier markers on all 77 tool descriptions; safe_discover de-rotted; kg_semantic_context no-vector degrade |
| FR-ZCP-05 (bridge tier) | pg_trgm fuzzy baseline for the L2 rung | 7d902461 + 3a68d571 — migration 007, fuzzy_find_elements/suggest_element_names seams, trgm_available probe, live tests on throwaway DBs |
| FR-ZCP-12 (T1) | Error catalog + claim hygiene | c5b4b991 + 8c5b9cce — src/errors.rs 14 codes, 43 migrated sites, 4-test CI lint (coverage + dead-entry + completeness, enforcement proven), README tool-count fix |
| FR-ZCP-13 | First-run setup contract + `leankg add` | b251046c — src/setup_config.rs (10 tests), precedence flag>env>stored>TTY-prompt>manual-default, detached background index <2s return, status --json, setup --reset |
| FR-ZCP-01 (clause 2) | Server-initiated roots/list HTTP resolution | 87e18287 — see In Progress note; 24 tests |

## Todo — live v4.1.x–v4.3.0 items (ordered)

| ID | Title | Priority | Milestone | Supersedes |
|----|-------|----------|-----------|------------|
| FR-ZCP-04 | `leankg install --target opencode\|claude\|codex\|cursor\|omp` — project-less URLs + `--register-cwd` hook — scope: extends existing `connect` writers with opencode+omp targets; `--register-cwd` = session-start hook running `leankg add <cwd>` (persistent cwd→project table stays FR-ZCP-01 clause 3, out of scope); Docker `?project=` is the documented exception; env inventory table + byte-identical config-block snapshot tests per PRD §3.4 | P1 | M2 | FR-ZG-04, US-ZG-04 — **DONE** (opencode + omp writers, projectless URL contract, --register-cwd via install --target/connect; integration suite covers all six clients) |
| FR-ZCP-06 | Freshness contract: `freshness: fresh\|possibly_stale\|cold` on every index-backed response; reconciliation off the query path | P1 | M3 | FR-ZG-03, US-ZG-03 — **DONE** (#347: 30s TTL cache off the query path, write-invalidated; is_index_backed_tool covers 38 graph-reading verbs) |
| FR-ZCP-07 | Memory-backend adjacency: mnemopi-compatible bank naming (`<basename>-<wyhash36(cwd)>`, cwd-only), 3-mode scoping, `retained_through_user_turn` cursor, `session_retain`/`session_recall` + `<memories>`-equivalent injection; hindsight-shaped HTTP memory API as upstream `memory.backend: "mcp"` evidence | P1 | M4 | FR-SMA-04, US-SMA-04, US-SM-02 — **slice 1 DONE** (#357; hindsight HTTP API + OMP e2e outstanding) |
| FR-ZCP-08 | Cross-tool harness hardening: pinned SHAs/prompts, ≥3 trials/arm, judge-blind scorer, zg pitfalls checklist | P2 | M5 | FR-ZG-05, US-ZG-05, FR-B05 — **DONE** (lock-file SHAs, prompt_version+SHA, >=3-trials gate, judge-blind score.py, computed pitfalls checklist) |

| FR-ZCP-09 | Project registry (`public.leankg_projects`) + portfolio scope (T0 manifest inventory, per-child freshness) + cross-schema portfolio queries + memory federation; one indexer slot, hot-set cap, LRU detach-to-cold | **P1** | M6 | — |
| FR-ZCP-10 | Per-schema migration fleet reconciliation + `doctor --deep` drift check (per-schema ledgers today, nothing fleet-wide) | P2 | M6 | — |
| FR-ZCP-11 | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **P1** | M7 | FR-EMBED-R4 (supersedes the aspirational perf-only goal with a correctness contract) — **IN_PROGRESS** (part 1 DONE #351: model stamp + hard rebuild guard; 3-signal detection + chunker_version outstanding). Part 2 DONE #353: query-side degrade guard in semantic_search + entry-point guards in run/build_index_parallel; AC amended (mismatch degrades to L2, never errors). Part 3 DONE #355: chunker_version coupled into content_hash_for — bump invalidates all hashes → full re-embed; query-guard + entry-point guards compile under the embeddings feature (#355). FR-ZCP-07 slice 1 DONE #357: mnemopi-compatible bank naming (wyhash36 + canonicalize), scoping matrix, session_retain/session_recall cursor contract, memory mirrors — file-backed JSONL banks) |
| FR-ZCP-05 (remainder) | Postgres FTS: `tsvector` + GIN, `websearch_to_tsquery`, RRF fusion in `semantic_search` — the L2 rung's FTS half beyond the landed trgm bridge | P1 | M3 | FR-ZG-02, US-ZG-02 |
| FR-3T-01 | Registry: exactly 3 tools — `set` (import repo / nested dir of repos), `get` (query, multi-layer L0–L3 ladder + legacy actions), `status` (health/inventory/freshness); legacy verbs remain valid as per-tool actions | **P0** | M9 | Supersedes FR-ZCP-03 one-tool end-state (verb namespace preserved as actions) |
| FR-3T-02 | SQLite storage backend: cozo-sqlite engine (storage-sqlite feature), per-project `.leankg/leankg.db`, HNSW vectors (cosine dim 384), audit ledger on Cozo relations, migrations standalone | **P0** | M9 | — |
| FR-3T-03 | Session default: SQLite engaged when `LEANKG_DB_ENGINE=sqlite` or `LEANKG_PG_URL` unset; migrate/index/serve run on SQLite; PG reachable via `LEANKG_PG_URL` | **P0** | M9 | — |
| FR-3T-04 | Live validation: this repo indexed into SQLite; 3-tool smoke (search/status/router) over MCP HTTP; persistence across restart | **P1** | M9 | DONE 2026-09-07 (581 files / 9522 vectors; L1/L2/L3 verified live; follow-up bugs #286 crash-on-concurrent-embed, #287 fuzzy regex escaping, #288 positional-rule audit) |
| FR-ZCP-12 (remainder) | Measured-simplicity T2 (CI-timed published TTFV ≤ 5 min) — T1 error catalog DONE; T3 superseded by v4.3.1's CI-enforced one-tool invariant (landed) | P1 | M8 | — |

## Todo — carry-forward from archive (original IDs preserved)

| ID | Title (from archive) | Priority | Archive PRD § | Live mapping |
|----|----------------------|----------|---------------|--------------|
| US-SMA-01 / US-SMA-02 / US-SMA-03 | Stories paired with FR-SMA-01..03 (write path / decay / feedback) | P2 | 3.32 | Close via FR-SMA-01..03 |
| FR-HEA-01 | `kg_ontology_status` alias accounting self-consistent — `nodes_missing_aliases ≤ sum(domain_entity_counts)` invariant; backfill or fix formula | **P1** | 5.36 | None — direct TODO (M5 hygiene) |
| FR-HEA-02 | Empty / below-floor `semantic_search` / `kg_semantic_context` carry structured `search_code` fallback hint (no bare dead ends) | **P1** | 5.36 | Extends FR-ZCP-05/06 (M3) |
| FR-HEA-04 | Per-tool `tokio::time::timeout` floors below the 30s client budget; structured timeout response; local-PG / materialised-view docs | **P1** | 5.36 | Extends FR-ZCP-06 (M3) |
| FR-HEA-03 | Mega-graph 50k full-scan banner + guarded-tool list in `get_architecture` / `mcp_status` output | P2 | 5.36 | None — direct TODO (M5 hygiene) |
| FR-HEA-05 | README lead + §1 + agent-surface docs lead with org-memory substrate positioning | P1 | 5.36 | **DONE by this revision** — v4.0.0 prd.md §1 is the cutover |
| FR-SMA-01 | `report_query_outcome` / `agent_diary_write` / `add_knowledge` push into `RecallStore::push_dedup` (outcome-weighted rank seed); module doc corrected | P2 | 5.37 | Prerequisite of FR-ZCP-07 (M4) |
| FR-SMA-02 | `Lesson.created_at` + recency decay in `recall_for_overview` scoring (≈30-day half-life) | P2 | 5.37 | Prerequisite of FR-ZCP-07 (M4) |
| FR-SMA-03 | `report_query_outcome` lesson_id: useful bumps / dead_end decays / corrected rewrites | P2 | 5.37 | Prerequisite of FR-ZCP-07 (M4) |
| FR-SMA-04 | `session_retain(project, session_id, transcript)` — idempotent `documentId=session_id`, chunking, Stop-hook recipe | P2 | 5.37 | Landed inside FR-ZCP-07 (M4) |
| FR-SM-04 | Ranked lessons index from outcomes/diary/knowledge with dedup — write path never wired; rework as FR-SMA-01 | P2 | 5.32 | Closes via FR-SMA-01 |
| FR-SM-05 | Opt-in `get_overview_context` enrichment with top-K lessons — read path exists, default OFF, A/B unmeasured | P2 | 5.32 | Closes via FR-SMA-01..03 + FR-ZCP-07 |
| US-SM-02 | Auto-recall lessons/diary at session start (closes US-GE-05) | P2 | 3.28 | Closes via FR-SMA-01..04 + FR-ZCP-07 |
| FR-B16 | Runtime trace ingestion (Could) | P2 | 5.10 | None — direct TODO (unmilestoned) |
| FR-B51 | Optional openCypher→Cozo subset (Could) | P2 | 5.10 | None — direct TODO (unmilestoned) |
| FR-SURF-06 | Mega-safe `get_doc_structure`/tree; optional merge format tree\|list after safety | P3 | 5.18 | None — direct TODO (unmilestoned) |
| US-SURF-05 | Optional unify get_doc_tree + get_doc_structure (mega-safe first) | P3 | 3.16 | Closes via FR-SURF-06 |
| US-GF-10 | Expand language extractors toward Graphify breadth (Vue/Svelte done; Scala/Lua/Zig/shell/AppX open) | P3 | 3.10 | None — direct TODO (unmilestoned) |
| US-GF-12 | Live SQL / Postgres schema introspection into the same graph | P3 | 3.10 | None — direct TODO (unmilestoned) |
| FR-EMBED-R4 | (aspirational) Cold functions-only < 20 min on ~371k elements on reference M2 Pro 10c | P3 | 5.12 | Superseded by FR-ZCP-11 (correctness contract first; perf target rides M7's rebuild paths) |
| FR-SMA-06 | Secret redaction before diary/lesson writes; truncation markers on injected lessons | P3 | 5.37 | None — direct TODO (unmilestoned) |
| US-SMA-05 | Worktree sessions share one memory scope | P3 | 3.32 | Closes via FR-SMA-05 |
| US-SMA-06 | Memory writes never leak tokens; injection budgets auditable | P3 | 3.32 | Closes via FR-SMA-06 |
| FR-ZG-06 | Second in-catalog embedding model (Model2Vec-class) + model-switch/rebuild docs | P3 | 5.38 | None — direct TODO (unmilestoned) |
| US-ZG-06 | Quick constrained-hardware indexes without the ONNX stack | P3 | 3.33 | Closes via FR-ZG-06 |

## Done

| ID | Title | Evidence |
|----|-------|----------|
| DOC-ARCHIVE-01 | Move all 66 historical docs to `docs/archive/`; README + AGENTS.md links updated | `docs/` now contains only `prd.md` + `prd-task-tracker.md` (+ `archive/`) |
| OMP-ENABLE-01 | LeanKG MCP enabled in OMP `~/.omp/agent/mcp.json` (draft FR-OMP-01) | OMP draft §6 Phase 0, 2026-09-03 |
| FR-HEA-05 | Positioning cutover — docs lead with org-memory substrate | v4.0.0 `docs/prd.md` §1 |

*Last updated: 2026-09-09 (v0.29.0 released — FR-ZCP-06 freshness contract DONE #347+#350, FR-ZCP-07 slice 1 DONE #357, FR-ZCP-11 core DONE #351/#353/#355/#356, semantic-release pipeline healed and self-running)*

## Repo hygiene (non-PRD)

| Item | Detail |
|------|--------|
| Dependabot backlog | 22 vulnerabilities flagged on default branch (14 high, 7 moderate, 1 low) as of 2026-09-05 — https://github.com/FreePeak/LeanKG/security/dependabot; PR #264 remediated the Cargo-side set, the remainder are npm/ui-v2-side; not tied to a FR |
| Pre-existing integration-test hangs | `test_mcp_index`, `test_mcp_index_docs` (tests/mcp_tools_full_tests.rs) and `handle_reuse_tests::index_tool_keeps_shared_handle` (tests/mcp_tests.rs) hang at baseline HEAD too (verified via clean worktree 2026-09-05) — full-index-in-test family; CI gates on `cargo test --lib` so they never block CI; separate fix (TempDir-seeded rewrite or `#[ignore]`) queued outside the cutover |


