# LeanKG Task Tracker

**Last synced:** 2026-09-05 — v4.3.1-one-tool-envelope hard cutover: registry = exactly 1 tool (`leankg_context`), all ~76 capabilities ride the `{verb}` envelope (verb namespace = legacy tool namespace), envelope resolved before RO-gate/write-lock/audit; FR-ZCP-03 end-state DONE; FR-ZCP-12 T3 re-scoped to the one-tool CI invariant (T2 outstanding). Prior: v4.3.0-one-tool-ladder rewrites FR-ZCP-03 as the capability-probe router with an L0–L3 degradation ladder (vectors → FTS/trigram fuzzy → exact/regex → cold guidance; `retrieval: {rung, reason}` provenance; registers the unregistered `orchestrate` parser) and adds FR-ZCP-13 (first-run setup contract: one auto/manual question persisted in `.leankg/config.json`, `leankg add <path> [--embed]` one-command repo registration, embeddings = preference never prerequisite) + FR-ZCP-05 bridge tier (pg_trgm GIN + text_pattern_ops before FTS). Prior: v4.2.0 (2026-09-04) added FR-ZCP-12 (M8); v4.1.1 added FR-ZCP-11; v4.1.0 added FR-ZCP-09/10; the 2026-09-03 reset (v4.0.0) preserved 585 unique IDs verbatim in [`archive/prd-task-tracker.md`](archive/prd-task-tracker.md) + machine copy [`archive/prd-task-tracker.json`](archive/prd-task-tracker.json).
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
| M3 — Honest search | 2 | FR-HEA-02, FR-HEA-04 | **IN_PROGRESS** (FR-ZCP-05 bridge tier DONE 7d902461+3a68d571 — tsvector FTS + RRF outstanding) |
| M4 — Harness memory | 1 | FR-SMA-01..03, FR-SM-04/05, US-SM-02 | TODO |
| M5 — Defensible evidence | 1 | FR-HEA-01, FR-HEA-03 | TODO |
| M6 — Org-scale portfolio | 2 | — | TODO |
| M7 — Embedding correctness | 1 | — | TODO |
| M8 — Measured simplicity | 1 | — | **IN_PROGRESS** (FR-ZCP-12 T1 DONE c5b4b991; T3 re-scoped to one-tool CI invariant — landed with v4.3.1; T2 TTFV outstanding) |
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
| FR-ZCP-04 | `leankg install --target opencode\|claude\|codex\|cursor\|omp` — project-less URLs + `--register-cwd` hook | P1 | M2 | FR-ZG-04, US-ZG-04 |
| FR-ZCP-06 | Freshness contract: `freshness: fresh\|possibly_stale\|cold` on every index-backed response; reconciliation off the query path | P1 | M3 | FR-ZG-03, US-ZG-03 |
| FR-ZCP-07 | Memory-backend adjacency: mnemopi-compatible bank naming (`<basename>-<wyhash36(cwd)>`, cwd-only), 3-mode scoping, `retained_through_user_turn` cursor, `session_retain`/`session_recall` + `<memories>`-equivalent injection; hindsight-shaped HTTP memory API as upstream `memory.backend: "mcp"` evidence | P1 | M4 | FR-SMA-04, US-SMA-04, US-SM-02 |
| FR-ZCP-08 | Cross-tool harness hardening: pinned SHAs/prompts, ≥3 trials/arm, judge-blind scorer, zg pitfalls checklist | P2 | M5 | FR-ZG-05, US-ZG-05, FR-B05 |
| FR-ZCP-09 | Project registry (`public.leankg_projects`) + portfolio scope (T0 manifest inventory, per-child freshness) + cross-schema portfolio queries + memory federation; one indexer slot, hot-set cap, LRU detach-to-cold | **P1** | M6 | — |
| FR-ZCP-10 | Per-schema migration fleet reconciliation + `doctor --deep` drift check (per-schema ledgers today, nothing fleet-wide) | P2 | M6 | — |
| FR-ZCP-11 | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **P1** | M7 | FR-EMBED-R4 (supersedes the aspirational perf-only goal with a correctness contract) |
| FR-ZCP-05 (remainder) | Postgres FTS: `tsvector` + GIN, `websearch_to_tsquery`, RRF fusion in `semantic_search` — the L2 rung's FTS half beyond the landed trgm bridge | P1 | M3 | FR-ZG-02, US-ZG-02 |
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

*Last updated: 2026-09-05 — implementation sprint merged: FR-ZCP-01/clause-2, 02, 03, 05-bridge, 12-T1, 13 DONE (6 sprint rows; 9 DONE total incl. 3 prior); open inventory: 1 IN_PROGRESS + 35 TODO (9 live incl. FR-ZCP-05/12 remainders + 26 carry-forward) = 36 open; full 585-ID history in archive.*


