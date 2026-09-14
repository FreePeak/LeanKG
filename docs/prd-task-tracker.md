# LeanKG Task Tracker

**Last synced:** 2026-09-14 — v4.11.4 **vector-reclaim**: `gc` now reclaims orphaned vector+state rows too (#411 — the repo's 11 permanent `Orphans` purged live; next embed reports `Orphans: 0`, Coverage 1.0, freshness kept). The loop's first three waves shipped as **v0.32.0** with all four platform tarballs verified — FR-SELF-04's release clause demonstrated. 12 engine defects fixed by the loop to date.
**Previous syncs:** v4.11.3 doctor-truth (#409, closes #406 — index-freshness bookkeeping source, T0 MANIFEST/not_indexed on fleet + fan-out, inventory refresh on gc + writer); v4.11.2 waves one+two (S1 five gates green; 6 defects); v4.11.1 ship-surface; v4.11.0 loop anchored.
**SoT pairing:** narrative + ACs live in [`docs/prd.md`](prd.md); statuses live here.
**Status legend:** `IN_PROGRESS` (being worked now) · `TODO` (backlog, ordered) · `DONE` (implemented + verified) · `BLOCKED` (needs external input) · `WONT_DO` (explicitly cancelled).

---

## Summary

| Status | Count |
|--------|------:|
| IN_PROGRESS | 3 (FR-ZCP-01; **FR-SELF-02** — 12 engine defects found+fixed by the loop across #401/#405/#409/#411; **FR-SELF-03** — three children live under `games/`) |
| TODO | 37 (9 live + 26 carry-forward + FR-GO-DASH #371 + FR-SELF-04) |
| DONE | 10 (FR-SELF-01: self-host bootstrapped + all five S1 gates verified live, v4.11.2) |
| Open work | 42 (36 archived-inventory + 3 Go-rewrite slices pending PR #370 merge + FR-GO-DASH #371 + FR-SELF-04; FR-SELF-01 DONE, FR-SELF-02/03 IN_PROGRESS) — relocation of the Go module to the repo root tracked as #403 (held) |

**Inventory note (ID-level accounting):** the archived tracker holds **40 open inventory items** (35 master-table `NOT_DONE`/`PENDING`/`PARTIAL`/`OPEN` IDs + 5 `FR-HEA-*` section-table rows). All 40 are accounted for below: FR IDs appear as named rows; each paired `US-*` tracks with its FR (the archive itself pairs them `US-X / FR-X` as one work item); `FR-ZG-01..05` + `US-ZG-01..05` + `FR-B05` are superseded inside the live `FR-ZCP-*` rows (Supersedes column); `FR-HEA-05` is DONE (v4.0.0 §1 cutover). `FR-ZCP-09/10/11/12/13` are **new in v4.1.x–v4.3.0** (no archive IDs). Row-level open work = 1 IN_PROGRESS + 9 live + 26 carry-forward = 36. (The 26 carry-forward rows cover 35 archived open IDs: 3 rows pair multiple US stories with their FR; the inventory not…

| Milestone | Live items | Carry-forward items | Status |
|---|---|---|---|
| M1 — Zero-config attach | 3 | — | **IN_PROGRESS** (FR-ZCP-01 clause-2 roots/list DONE 87e18287; FR-ZCP-02 DONE 0aba41ad+d9ccd8b5; FR-ZCP-13 DONE b251046c) |
| M2 — One-tool surface | 2 | — | **IN_PROGRESS** (FR-ZCP-03 router+ladder DONE 4231d256; **v4.3.1 hard cutover DONE** — registry 1 tool + verb envelope; FR-ZCP-04 install --target DONE 1873c132 #363: opencode+omp writers, install --target, --register-cwd) |
| M3 — Honest search | 2 | FR-HEA-02, FR-HEA-04 | **DONE** (FR-ZCP-06 freshness contract #347 — plus the writer-path snapshot refresh #297/#376/#279 needed to make it truthful; FR-ZCP-05 bridge tier + the PG `tsvector`/GIN/RRF remainder #273, verified live on this repo's PG store) |
| M4 — Harness memory | 1 | FR-SMA-01..03, FR-SM-04/05, US-SM-02 | **IN_PROGRESS** (surface DONE #357: mnemopi bank naming/scoping/cursor, session_retain/recall + memory_* verbs; outstanding: hindsight-shaped HTTP API + OMP end-to-end injection AC) |
| M5 — Defensible evidence | 1 | — | **DONE** (FR-ZCP-08 harness #276: 40-hex pins with refusal-to-measure, ≥3 trials/arm at aggregation, label-erased blind judging, computed zg-pitfalls report; FR-HEA-01 alias accounting + FR-HEA-03 mega-graph banner/guard folded in) |
| M6 — Org-scale portfolio | 2 | — | **DONE** (FR-ZCP-09 registry + T0/T1 fan-out + `register-project`/`projects` verbs; FR-ZCP-10 fleet drift leg in `doctor --deep` — #376) |
| M7 — Embedding correctness | 1 | — | **DONE** (FR-ZCP-11 closed by #279: pinned catalog with 40-hex revisions and query/document prefixes, whole-identity `ModelStamp` incl. `chunker_version`, hard rebuild guard on both write paths **and** the read path, 3-signal detection, per-file atomic replace + truncation accounting, watcher reconciliation. Remaining: single-flight indexing — see §6b remainders) |
| M8 — Measured simplicity | 1 | — | **DONE** (FR-ZCP-12 T1 error catalog c5b4b991; T2 TTFV gate #280 — measured 21.6 s cold against a 300 s budget in CI; T3 superseded by v4.3.1's CI-enforced one-tool invariant) |
| M9 — Three tools + dual backend | 4 | — | **IN_PROGRESS** (FR-3T-01/02/03 DONE; FR-3T-04 live validation complete on this repo — 581 files, 9522 vectors, L1/L2/L3 verified; PR #284 merged (v4.3.x)) |
| M10 — Self-host dogfood loop | 4 | — | **IN_PROGRESS** (FR-SELF-01 **DONE** v4.11.2; FR-SELF-02 running — 12 defects fixed across #401/#405/#409/#411-wave, v0.32.0 shipped the lot, doctor 10-pass/0-fail/`Orphans: 0`; FR-SELF-03 running — `games/` T0 + three embedded children, fan-out spans 3 stores; FR-SELF-04 is the steady state — see §M10 below) |
| M-GO — Go engine rewrite (#365) | 3 | — | **IN_PROGRESS** (FR-GO-W1 core DONE on feat/go-rewrite: store/core/index/mcp/rest + live smoke; FR-GO-EMBED DONE: leankg-embed binary + stamp guards + NDJSON; FR-GO-MEM DONE: full-markdown memory + banks adapter; v4.6.0: ALL waves landed (W2 watcher/writer, W4 pgvector, W5 ConnectRPC+auth, session, graph verbs, goldens, benchmarks + executed Rust-vs-Go A/B REPORT) and the Rust tree REMOVED — deferred ledger in docs/prd.md) |
| FR-GO-LANGS | Lazy language wave (v4.7.0): 13-language registry + tstree/astgrep/lsp tiers + java/kotlin/swift/objc/dart extractors | **DONE** on feat/go-rewrite (objc/dart tree-sitter grammar gap documented) |
| FR-GO-DASH | #371: port the ui-v2 dashboard data API — legacy `/api/*` (11 endpoints) or rebuild ui-v2 against `/api/v1/*`; today the SPA fallback answers those calls with `index.html`, so the embedded dashboard loads no data | 2026-09-11 | **TODO** — ledger row `web api+ui` corrected to PARTIAL in v4.7.1 |
| FR-GO-PARITY | v4.8.0 full-parity wave: dashboard API, ontology workflows/traceability, compression, LSP bridge (config-gated enrich), Android/Gradle/Maven specialists, embedding sidecar lifecycle, 40-language registry + objc/dart grammars, enterprise auth + token lifecycle, multi-project serving + doctor --deep, obsidian, Rust CLI verb set | 2026-09-12 | **DONE** — full gate green (build/vet/test ×2 tags, CGO=0 tree build in CI, tidy no-op); live-verified dashboard/multi-project/MCP routing/run/audit/auth/doctor |
| FR-GO-371 | Dashboard legacy `/api/*` (11 endpoints) | 2026-09-12 | **DONE** — #371 closed with live route evidence |
| FR-GO-372 | Federation push/pull shared-server sync | 2026-09-13 | **DONE** — receiver mounted (`POST /api/v2/graph/push` + `GET /api/v2/graph`, Contributor+ to write, upsert-by-identity apply that never deletes unmentioned rows and carries local content forward); `pull` now fetches **and applies**, degrading to the Rust connectivity probe against a server without the route. Live push→pull round trip verified (#372 closed) |
| FR-GO-373 | Conversation mining (US-MP-03) | 2026-09-12 | **DONE** — `internal/convo` + `mine-conversations`; #373 closed (also fixes a Rust edge-loss defect) |
| FR-GO-374 | Org knowledge surfaces (incidents/notes/env-conflicts) | 2026-09-12 | **DONE** — `internal/orgknowledge` + migration 010 + CLI verbs + `/api/v2/*`; #374 closed |
| FR-GO-375 | Persisted usage metrics + `leankg metrics` | 2026-09-12 | **DONE** — `context_metrics` (migration 011) + `metrics`/`dashboard` verbs; #375 closed |
| FR-GO-376 | FR-ZCP-09/10 registry portfolio + cross-schema + fleet doctor | 2026-09-13 | **DONE** — migration 13 registry (sqlite `$LEANKG_PORTFOLIO_DB` or its own PG schema), register-on-index, T0 manifest + T1 hot-set fan-out running the real L0–L3 ladder per project with attribution, `portfolio` action, two verbs, `doctor --deep` fleet leg, `projects --forget` (#376; #277/#278 remainders closed with it) |
| Unmilestoned (P3) | — | FR-B16, FR-B51, FR-SURF-06, US-SURF-05, US-GF-10, US-GF-12, FR-EMBED-R4, FR-SMA-05/06, US-SMA-05/06, FR-ZG-06 | TODO |

---

## In Progress

| ID | Title | Started | Notes |
|----|-------|---------|-------|
| FR-ZCP-01 | Contextual project resolution — connection→project mapping (cwd / server-initiated `roots/list` / session registration); `?project=` demoted to escape hatch | 2026-09-03 | **Clause 2 (HTTP roots/list) DONE 2026-09-04** — commit 87e18287: probe rides the initialize SSE response as a second `event: message` frame, answer via POST /mcp with Mcp-Session-Id (mechanism chosen because LeanKG's custom axum dispatcher has no server-to-client channel; streamable-HTTP spec allows request frames in POST response bodies — matches OMP's TS-SDK client behavior); per-connection SessionRootCache; capability-gated (roots object) + list_changed invalidation; 24 tests. Remaining clauses: stdio cwd (clause 1, already works), session registration table (clause 3, partially via leankg install --register-cwd follow-up). Resolution order + cache design in prd.md §3.1; verified anchors: `find_leankg_for_path` `src/mcp/server.rs:588-605`, `resolve_project_db_path` `:637-661`, silent default-schema fallback `:2987-2989` (KILLED by FR-ZCP-02), no `X-LeanKG-Project` header in `src/`, identity = canonical root via `project_identity_keys_in` `src/db/backend.rs:2613-2675` |
| FR-GO-W1 | Go engine core (W1 of #365): SQLite WAL store + FTS5 + watermark freshness, 3-tool envelope (import/query/status), L0-L3 ladder with provenance, regex indexer + 3-signal detection, MCP (go-sdk) + REST | 2026-09-10 | **W1 DONE** on feat/go-rewrite — 7 packages, go test ./... green, live-smoked (index→embed→serve→L1/L2/L3→memory→MCP tools/list==3). Remaining: W2 tree-sitter, W4 pgvector, W5 ConnectRPC, W7 cutover (see go/README.md) |
| FR-GO-EMBED | #368: embedding pipeline as independent binary — internal/embed shared library, ModelStamp guard on every vector writer, cmd/leankg-embed run/full/export/import/status, NDJSON offsite flow | 2026-09-10 | **DONE** on feat/go-rewrite — stamp guards pinned by tests (fresh-store stamps; incremental-mismatch HARD-FAILS with `leankg-embed full` directive, vectors untouched; full clears+rebuilds); provider port (OpenAI-compatible/llama-sidecar shape + deterministic); serving binary does zero inference |
| FR-GO-MEM | #369: full-markdown memory — MEMORY.md/USER.md bounded 2200B error-not-truncate, topics/, Claude-Code file commands + traversal rejection, Hermes substring sugar, FTS5 reindex-on-write, mnemopi banks adapter (wyhash64 port) | 2026-09-10 | **DONE** on feat/go-rewrite — 11 tests incl. exact snapshot-header pin, symlink escape, overflow, ambiguous match, cursor resume, zero-match filter; hindsight HTTP endpoints landed in internal/rest |


## M10 — self-host dogfood loop (v4.11.0, the operating plan)

| ID | Title | Priority | Status | Gate / exit criteria |
|----|-------|----------|--------|----------------------|
| FR-SELF-01 | Bootstrap the self-host: dynamic `leankg serve --http :9699 --rest :9700 --ui :9701 --memory --embed-provider local` over this repo (9700/9701 — the live onegw gateway owns 8080); index + embed (pinned `LEANKG_EMBED_*` identity) + memory live | **P0** | **DONE 2026-09-14 (v4.11.2)** — sidecar: `llama-server` bge-small-en-v1.5 f16 GGUF @ 127.0.0.1:8085; store: 9,025 elements / 19,662 edges / 8,989 vectors (coverage 0.9984, chunker-v2 stamp `ea104dace…`); gates verified live: `/health` ×3, L1/L2/L3 with provenance (L3 = cosine on real vectors), retain→recall survives restart, `doctor --deep` 0-fail after the wave's `leankg gc` run | exit criteria all met — evidence in [§v4.11.2](prd.md#v4112-selfhost-validated--the-self-host-ran-on-itself-every-defect-it-found-is-fixed-2026-09-14) |
| FR-SELF-02 | Build LeanKG with LeanKG: every exported-symbol change informed by `impact`/`callers`, every close by tested-by/traceability, every session-open by `session_recall`; wrong answers filed as engine defects, fixed failing-test-first against this repo's data, re-indexed, re-asked | **P0** | **IN_PROGRESS 2026-09-14** — running since v4.11.2; cumulative output: 12 engine defects found by the loop and fixed failing-first (#401 embed budget/shrink-seed + poison-item fallback + `gc` verb + held-lock probe + test hermeticity; #405 shrink-retry + embed-verb positional; #409/#406 three honest classifications + two inventory-refresh carry-ins; #411 vector reclaim — `gc` now reclaims orphaned vectors, closing the residue its own report surfaced). v0.32.0 shipped the first three waves. |
| FR-SELF-03 | Scale to nested-repo parents: add small parents one repo at a time via registry + `LEANKG_PROJECT_DIRS` (T0 manifest, T1 hot-set cap 8, zero eager indexing). Hard guard: never bulk-index the `freepeak` root (99,574 files, multi-GB store) | P1 | **IN_PROGRESS 2026-09-14** — verified: `games/` registered T0 (zero eager; classified `MANIFEST`/`not_indexed` honestly per #409); children `the-survival` (132/132, Godot profile) + `sketchpocalypse` (244/244, TS profile) embedded Coverage 1.0; portfolio fan-out attributes per child with no restart; stale fixture registrations pruned via `projects --forget`. Next: one child at a time (retrogames, then the bigger repos only while fleet stays honest) |
| FR-SELF-04 | Keep building, keep fixing: the loop is the end state — every wave surfaces defects, engine fixes ship via release-please, this PRD + tracker stay the status ledger | **P0** | TODO (steady state after S1–S3) | Dogfood-found defect rate > 0 with fix rate keeping pace; no milestone regresses; self-host up whenever development happens |

The deploy/brand/module surface FR-SELF-01 was gated on shipped as `REL-SHIP-01..04` (§Done).

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
| FR-ZCP-07 | Memory-backend adjacency: mnemopi-compatible bank naming (`<basename>-<wyhash36(cwd)>`, cwd-only), 3-mode scoping, `retained_through_user_turn` cursor, `session_retain`/`session_recall` + `<memories>`-equivalent injection; hindsight-shaped HTTP memory API as upstream `memory.backend: "mcp"` evidence | P1 | M4 | FR-SMA-04, US-SMA-04, US-SM-02 — **DONE** (#357 slice 1; #275 closed the rest: `session_retain` write with the cursor over MCP/REST/CLI, `session_recall` + `memories` injection reads, the scope matrix with strict `ParseScope`, three routes; verified live: `written=2` then `skipped=2, written=0` on re-retain) |
| FR-ZCP-08 | Cross-tool harness hardening: pinned SHAs/prompts, ≥3 trials/arm, judge-blind scorer, zg pitfalls checklist | P2 | M5 | FR-ZG-05, US-ZG-05, FR-B05 — **DONE** (lock-file SHAs, prompt_version+SHA, >=3-trials gate, judge-blind score.py, computed pitfalls checklist) |

| FR-ZCP-09 | Project registry (`public.leankg_projects`) + portfolio scope (T0 manifest inventory, per-child freshness) + cross-schema portfolio queries + memory federation; one indexer slot, hot-set cap, LRU detach-to-cold | **P1** | M6 | **DONE** #376 — Go registry in migration 13 (the sqlite/PG-sibling equivalent of the PG table), hot-set cap `LEANKG_PORTFOLIO_MAX_REPOS` (8) with serving-only detach, per-child RO handles, real ladder per project (no duplicated retrieval path) |
| FR-ZCP-10 | Per-schema migration fleet reconciliation + `doctor --deep` drift check (per-schema ledgers today, nothing fleet-wide) | P2 | M6 | **DONE** #376 — `fleet` leg in `Defaults()`: per-project ledger vs the embedded list (behind/ahead), freshness, totals; a vanished checkout is `MISSING` (informational + the `--forget` hint), not a store failure |
| FR-ZCP-11 | Embedding correctness ported from zvec-grep: pinned model catalog (commit revision + query/document prefixes), model-stamped vectors + hard rebuild guard, chunker-version coupling, 3-signal change detection, per-file atomic replace + truncation accounting, watcher reconciliation, single-flight indexing | **P1** | M7 | FR-EMBED-R4 (supersedes the aspirational perf-only goal with a correctness contract) — **DONE except single-flight** (#279): catalog pinned with revisions/prefixes/dims, `chunker_version` + prefixes in the stamp (migration 15, both engines), whole-identity compare on the read path so drift degrades with the rebuild hint, watcher reconciliation. Single-flight remains open (§6b) |
| FR-ZCP-05 (remainder) | Postgres FTS: `tsvector` + GIN, `websearch_to_tsquery`, RRF fusion in `semantic_search` — the L2 rung's FTS half beyond the landed trgm bridge | P1 | M3 | **DONE** #273 — migration 12 generated `fts` over (name, qualified_name, content) with GIN, `ts_rank` L2 with trigram→ILIKE degrade, RRF fusion of the three arms at L3; verified live on this repo's 11,327-element PG store (`rrf(vector+tsvector)`) |
| FR-3T-01 | Registry: exactly 3 tools — `set` (import repo / nested dir of repos), `get` (query, multi-layer L0–L3 ladder + legacy actions), `status` (health/inventory/freshness); legacy verbs remain valid as per-tool actions | **P0** | M9 | Supersedes FR-ZCP-03 one-tool end-state (verb namespace preserved as actions) |
| FR-3T-02 | SQLite storage backend: cozo-sqlite engine (storage-sqlite feature), per-project `.leankg/leankg.db`, HNSW vectors (cosine dim 384), audit ledger on Cozo relations, migrations standalone | **P0** | M9 | — |
| FR-3T-03 | Session default: SQLite engaged when `LEANKG_DB_ENGINE=sqlite` or `LEANKG_PG_URL` unset; migrate/index/serve run on SQLite; PG reachable via `LEANKG_PG_URL` | **P0** | M9 | — |
| FR-3T-04 | Live validation: this repo indexed into SQLite; 3-tool smoke (search/status/router) over MCP HTTP; persistence across restart | **P1** | M9 | DONE 2026-09-07 (581 files / 9522 vectors; L1/L2/L3 verified live; follow-up bugs #286 crash-on-concurrent-embed, #287 fuzzy regex escaping, #288 positional-rule audit) |
| FR-ZCP-12 (remainder) | Measured-simplicity T2 (CI-timed published TTFV ≤ 5 min) — T1 error catalog DONE; T3 superseded by v4.3.1's CI-enforced one-tool invariant (landed) | P1 | M8 | **DONE** #280 — `scripts/ttfv_smoke.sh` + the `ttfv` CI job with a cold-cache artifact; README publishes the measured number |

## Todo — carry-forward from archive (original IDs preserved)

| ID | Title (from archive) | Priority | Archive PRD § | Live mapping |
|----|----------------------|----------|---------------|--------------|
| US-SMA-01 / US-SMA-02 / US-SMA-03 | Stories paired with FR-SMA-01..03 (write path / decay / feedback) | P2 | 3.32 | Close via FR-SMA-01..03 |
| FR-HEA-01 | `kg_ontology_status` alias accounting self-consistent — `nodes_missing_aliases ≤ sum(domain_entity_counts)` invariant; backfill or fix formula | **P1** | 5.36 | **DONE** #276 — 5 accounting tests in `internal/ontology` pin the invariant, YAML+seed alias totals, the orphanless-gap negative control, code-ref accounting and orphan-free procedural nodes |
| FR-HEA-02 | Empty / below-floor `semantic_search` / `kg_semantic_context` carry structured `search_code` fallback hint (no bare dead ends) | **P1** | 5.36 | Extends FR-ZCP-05/06 (M3) |
| FR-HEA-04 | Per-tool `tokio::time::timeout` floors below the 30s client budget; structured timeout response; local-PG / materialised-view docs | **P1** | 5.36 | Extends FR-ZCP-06 (M3) |
| FR-HEA-03 | Mega-graph 50k full-scan banner + guarded-tool list in `get_architecture` / `mcp_status` output | P2 | 5.36 | **DONE** #276 — `ontology.MegaGraphBanner` merges into `status` (byte-identical below the cap) and the four full-table ontology cmds refuse with the paginated escape hatch named, matching Rust's `Ok(refusal)` |
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
| REL-SHIP-01 | Go container deploy restored: three-stage CGO-free `Dockerfile` (engine binaries → demo graph baked at build time → unprivileged runtime serving it `--read-only`) + `.dockerignore` | `docker run --user leankg` → `/health` `{"ok":true}`, `/` embedded shell, `/favicon.svg` 200, `/api/search` + `/api/graph/clusters` answering the baked store (534 files, 4,641 elements, 14,727 relationships) |
| REL-SHIP-02 | `serve --ui` JSON `/health` route, so the dashboard listener's health probe cannot be satisfied by the SPA fallback's `200 index.html` | `go test ./cmd/leankg/ -run TestDashboardListenerServesHealthAndShell` green |
| REL-SHIP-03 | Go module published from a subdirectory (`go/`): mirror job and directory-prefixed tags — SUPERSEDED by #403 (module moved to repo root, mirror job retired; the root tag now versions the module, `go install github.com/FreePeak/LeanKG/cmd/leankg@latest` resolves) | Historical evidence: proxy.golang.org/…/@v/list → `go/v0.31.3` mirror, `pkg.go.dev` module page 200 |
| REL-SHIP-04 | Brand mark redesigned as a graph-"K" (stem + two arms + hub) across `assets/icon.svg` and both favicons; README regained its platform/client tag badges and lost its stale claims (releases shipping, 40 language profiles, ontology procedural layer + traceability implemented, dead `release-go.yml` reference) | Mark rasterized and read at 16/32/128 px; all nine badge URLs fetched and their text checked |
| REL-SHIP-05 | The deploy verified **live**, not just in a container: Render rebuilt from the restored Dockerfile after two consecutive `build_failed` deploys | `dep-dajp5slg1s2s73cln2o0` → `live`; <https://leankg.onrender.com> `/` 200 shell, `/health` `{"ok":true}`, `/favicon.svg` the new mark, `/api/index/status` → ~4.6k elements / ~14.7k relationships (rebuilt from the tree each image build, so the counts move), `/api/search` + `/api/graph/clusters` answering |
| REL-SHIP-06 | `go/README.md` (the pkg.go.dev module page) rewritten — it was the W1 slice note: shipped backends listed as deferred, a nonexistent `internal/ports/`, "7 packages" against 54, a moved doc link, Rust described as still maintained | Every claim read back against the code; both `go install` lines run for real |
| REL-SHIP-07 | Module root cut (#403): `git mv go/* .`, imports `github.com/FreePeak/LeanKG/go` → `github.com/FreePeak/LeanKG` (196 files), release/CI/Makefile/Dockerfile/.gitignore/.gitattributes/docs repointed | `go build ./... && go vet ./... && go test ./... -count=1` green at repo root; `go mod tidy` no-op; `make go-build` → `bin/leankg` = `leankg 0.32.1`; CI green; PR #403 |
*Last updated: 2026-09-14 (v4.12.0-module-root — #403 module-root cut landed: cmd/internal/go.mod at root, imports rewritten, modtag mirror retired, go/README folded into root README; protobuf pb seed go_package length repaired; PR #403 open). Prior waves same as v4.11.4 vector-reclaim.*


## Repo hygiene (non-PRD)

| Item | Detail |
|------|--------|
| Dependabot backlog | 22 vulnerabilities flagged on default branch (14 high, 7 moderate, 1 low) as of 2026-09-05 — https://github.com/FreePeak/LeanKG/security/dependabot; PR #264 remediated the Cargo-side set, the remainder are npm/ui-v2-side; not tied to a FR |
| Pre-existing integration-test hangs | `test_mcp_index`, `test_mcp_index_docs` (tests/mcp_tools_full_tests.rs) and `handle_reuse_tests::index_tool_keeps_shared_handle` (tests/mcp_tests.rs) hang at baseline HEAD too (verified via clean worktree 2026-09-05) — full-index-in-test family; CI gates on `cargo test --lib` so they never block CI; separate fix (TempDir-seeded rewrite or `#[ignore]`) queued outside the cutover |


