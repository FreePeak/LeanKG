# Remaining PRD Closeout Plan

**Date:** 2026-08-03  
**Status:** Ready to dispatch  
**Baseline:** `origin/main` @ `0.19.31` (PRD `v3.8.5-mcp-validation-rca`)  
**Supersedes for open work:** [`2026-08-01-all-open-prd-campaign.md`](2026-08-01-all-open-prd-campaign.md) (that plan froze ~113 open items; most are now DONE)

| SoT | Path |
|-----|------|
| Narrative + ACs | [`docs/prd.md`](../prd.md) |
| Task inventory (machine) | [`docs/prd-task-tracker.json`](../prd-task-tracker.json) — **prefer over stale MD headers** |
| Task inventory (human) | [`docs/prd-task-tracker.md`](../prd-task-tracker.md) |
| Dev workflow | [`docs/workflow-opencode-agent.md`](../workflow-opencode-agent.md) |

> **Rule:** Do not invent new FRs. Close, split, or explicitly `WONT_DO` the 9 open tracker rows. Update JSON + MD tracker after every merge. Sync the PRD priority banner when Wave 0 lands.

---

## 1. Mission

Ship or consciously retire the **residual PRD backlog** so tracker open work is either **0** or only intentional `OPEN` / `WONT_DO`.

| Metric (freeze 2026-08-03) | Count |
|----------------------------|------:|
| Total tracked | 546 |
| DONE | 534 |
| Open (`NOT_DONE` + `PENDING` + `PARTIAL` + `OPEN`) | **9** |
| P0 / P1 Must Have open | **0** |

Critical path (company adoption + MCP mega P0s + session memory + graph-eng) is **already closed**. This plan is packaging / parity / stretch only.

---

## 2. Inventory — the 9 open rows

| # | ID | Focus | Status | Priority | Reality check (code vs tracker) | Disposition |
|--:|----|------:|--------|----------|----------------------------------|-------------|
| 1 | `US-SURF-05` | P3 | PENDING | Could | Unify already **DONE** via Wave 1b hard-delete of `get_doc_structure` ([REL-076](../reports/rel-076-mcp-surf-1b-2026-08-01.md)); PRD §3.16 says DONE | **Wave 0 — mark DONE** |
| 2 | `FR-SURF-06` | P3 | NOT_DONE | Could | Unify closed; leftover = mega-safe **keyed/paginated** `get_doc_tree` (still `all_elements()` after mega refuse) | **Wave 0 split** → DONE (unify) + optional `FR-SURF-06b` OR implement Wave 3 |
| 3 | `US-GF-10` | P3 | PARTIAL | Could | Vue/Svelte **wired** (`src/indexer/sfc.rs` + walker). Tracker notes stale (“not wired”) | **Wave 0 note fix** + Wave 2 lang slices |
| 4 | `US-GF-12` | P3 | PARTIAL | Could | `.sql` DDL **wired**. Live `--postgres <dsn>` still missing | **Wave 0 note fix** + Wave 2 postgres |
| 5 | `FR-B05` | P2 | NOT_DONE | Should | No CBM 50-edge structural harness yet; cross-tool agent harness exists separately | **Wave 1 — implement** |
| 6 | `FR-B16` | P2 | NOT_DONE | Should | Runtime / OTel-style trace → graph edges not shipped | **Wave 1 — thin MVP or WONT_DO** |
| 7 | `FR-B51` | P2 | NOT_DONE | Should | openCypher→Cozo subset not shipped; ui-v2 Advanced already uses raw Cozo | **Wave 1 — decide; likely WONT_DO or tiny subset** |
| 8 | `FR-C08..C11` | P2 | NOT_DONE | Should | **Windows binary already in** `release.yml`; pkg/SLSA/install-channel gaps remain | **Wave 0 split** + Wave 4 packaging |
| 9 | `FR-EMBED-R4` | P3 | OPEN | Could | Aspirational cold embed &lt;20 min @ ~371k on M2 Pro | **Keep OPEN** unless measured + tuned |

**Ops leftover (not in the 9 JSON rows):** OnRender **F3** — prebuilt image → Render pull ([RCA](../reports/root_cause_onrender_embeddings_exit101-2026-08-01.md)). Optional Wave 4b.

---

## 3. Efficiency principles

1. **Hygiene before code** — Wave 0 removes false open work so agents do not re-implement DELETED tools.
2. **One PR = one ID (or one atomic split)** — worktrees under `.worktrees/prd/<slug>/`.
3. **TDD vertical slices** — failing test → minimal code → green → evidence.
4. **Decision gate first** for stretch CBM items (`FR-B16`, `FR-B51`) — 30-minute product call → implement MVP **or** `WONT_DO` with rationale in tracker notes.
5. **Serialize hot files** — `src/mcp/handler.rs`, `src/mcp/tools.rs`, `src/cli/mod.rs`, `src/db/schema.rs`, tracker MD/JSON.
6. **Prefer new modules** — e.g. `src/indexer/trace_ingest.rs`, `benchmarks/cbm_edges/`, `src/cypher_subset/` (only if building).
7. **Max 3 parallel subagents** — residual set is small; avoid RocksDB LOCK contention on live MCP.

---

## 4. Quality protocol (non-negotiable)

| Gate | Command / artifact |
|------|-------------------|
| Format | `cargo fmt --all -- --check` |
| Lint | `cargo clippy --all -- -D warnings` |
| Unit | `cargo test --lib` (+ targeted `--test` / module tests) |
| Integration | `TempDir` fixtures; no host `be/` paths |
| Live (when REL / MCP surface) | Docker `:9699`, `project=/workspace`; report under `docs/reports/<id>-YYYY-MM-DD.md` |
| Tracker | Flip status in **JSON + MD**; clear stale narrative sections |
| Commits | Conventional; **no** AI attribution |

Build: always `--release` for runtime smoke. Embeddings: `--features embeddings` only when the PR needs it.

Hard-removed tools must stay gone: `mcp_hello`, `mcp_impact`, `get_doc_for_file`, `find_clones`, `wake_up`, `search_by_environment`, `load_layer`, `get_doc_structure`.

---

## 5. Wave matrix

```text
Wave 0  Docs/tracker hygiene + ID splits          [serial, 1 PR]
   │
   ├─► Wave 1  CBM stretch decisions + FR-B05      [1–2 PRs]
   │
   ├─► Wave 2  US-GF-10 / US-GF-12 closeout        [2 PRs, parallel OK]
   │
   ├─► Wave 3  Optional FR-SURF-06b mega doc_tree  [0–1 PR]
   │
   └─► Wave 4  FR-C packaging + optional F3        [1–2 PRs]
```

### Wave 0 — Tracker + PRD banner sync (do first)

**PR:** `docs/remaining-prd-wave0-tracker-sync`  
**Owns:** `docs/prd-task-tracker.md`, `docs/prd-task-tracker.json`, PRD priority banner (top of `docs/prd.md`), this plan’s checklist.

| Step | Action | Done when |
|------|--------|-----------|
| 0.1 | Mark `US-SURF-05` **DONE**; notes: “Wave 1b hard-delete; REL-076” | JSON + MD + sorted table agree |
| 0.2 | Resolve `FR-SURF-06`: either (A) **DONE** unify + spawn `FR-SURF-06b` NOT_DONE for keyed `get_doc_tree`, or (B) keep ID, rewrite title to “mega-safe get_doc_tree pagination” | Intent matches code |
| 0.3 | Refresh `US-GF-10` notes: Vue/Svelte wired; remaining langs listed | Notes match `sfc.rs` + walker tests |
| 0.4 | Refresh `US-GF-12` notes: `.sql` wired; remaining = live Postgres DSN | Notes match `sql.rs` |
| 0.5 | Split `FR-C08..C11` into four rows **or** checklist sub-bullets: C08 Windows (**DONE** via release matrix), C09 pkg channel, C10 SLSA provenance, C11 extra install targets | No false “Windows missing” |
| 0.6 | Replace PRD + tracker “P2 CURRENT: US-SM-01…” banner with “Residual closeout — see `2026-08-03-remaining-prd-closeout.md`” | Agents stop chasing SM/GE |
| 0.7 | Leave `FR-EMBED-R4` as **OPEN**; note “measure before optimize” | Still aspirational |

**Verify:**  
`python3 -c "…"` open-count drops by ≥2 (`US-SURF-05` + C08).  
`rg "P2 CURRENT.*US-SM" docs/prd.md docs/prd-task-tracker.md` → no hits.

---

### Wave 1 — CBM residual (`FR-B05`, `FR-B16`, `FR-B51`)

#### Decision gate (before code) — 30 min

| ID | Ship MVP if… | Prefer `WONT_DO` if… |
|----|--------------|----------------------|
| `FR-B05` | Want public structural parity evidence vs CBM edge samples | Already covered enough by `benchmarks/cross_tool/` — **still recommend shipping** a thin harness |
| `FR-B16` | Clear producer (OTLP JSON / Jaeger file) + edge types (`emits`/`calls` already exist) | No committed ingestion format / ops demand |
| `FR-B51` | Agents need Cypher habit with ≤10 patterns mapped to Cozo | ui-v2 Advanced + `query_graph` NL enough — **default lean WONT_DO** |

Record the decision in tracker `notes` even when choosing `WONT_DO`.

#### PR-1a — `FR-B05` CBM 50-edge harness (recommended)

| Field | Value |
|-------|-------|
| Branch | `prd/fr-b05-cbm-edge-harness` |
| Modules | `benchmarks/cbm_edges/` (Makefile + fixtures + compare script); optional thin CLI `leankg bench edges` |
| Unit | Fixture graph → expected edge set hash / count; golden 50-edge sample JSON |
| Integration | TempDir index of tiny multi-file fixture; assert ≥N EXTRACTED edges with confidence labels |
| Live / report | `docs/reports/fr-b05-cbm-edge-harness-YYYY-MM-DD.md` — table: LeanKG vs CBM sample (match / miss / N/A) |
| AC | Given frozen 50-edge fixture, When harness runs, Then exit 0 and Markdown report lists per-edge verdict; CI job optional (`workflow_dispatch`) |

**Verify:** `make -C benchmarks/cbm_edges check` (or documented cargo/python entry) green in PR.

#### PR-1b — `FR-B16` runtime trace ingestion (MVP or WONT_DO)

**MVP scope (if ship):**

- Ingest **file-based** OTLP JSON / simple span dump (no live collector required).
- Create/update nodes for services/spans; edges `calls` / `emits` with `confidence_label=INFERRED` + provenance in `context`.
- CLI: `leankg ingest-traces <path>`; optional MCP later.

| Test layer | Requirement |
|------------|-------------|
| Unit | Parse sample spans → edge list (no DB) |
| Integration | TempDir DB insert; `get_call_graph` / search finds span-derived QNs |
| Report | `docs/reports/fr-b16-trace-ingest-YYYY-MM-DD.md` |

**Out of scope:** Always-on OpenTelemetry sidecar; replacing static index.

#### PR-1c — `FR-B51` openCypher subset (likely WONT_DO)

If **WONT_DO**: tracker note + one-line PRD Won’t Do under §5.10; point agents to `query_graph` + Advanced Cozo.

If **ship:** translate only `MATCH (a)-[r]->(b) WHERE … RETURN` with hard node/edge budget; refuse everything else with structured error. Unit-test the translator; never run unbounded on mega.

---

### Wave 2 — Language / SQL closeout (`US-GF-10`, `US-GF-12`)

Parallel OK (different modules).

#### PR-2a — `US-GF-10` next languages (scoped PARTIAL → DONE or smaller PARTIAL)

**Do not** chase Graphify’s full long-tail. Close the story when **one** of these exit criteria is met:

| Exit | Criteria |
|------|----------|
| **DONE** | Shell (`.sh`/`.bash`) + Scala **or** Astro extractor wired into walker + unit + TempDir index smoke |
| **PARTIAL keep** | Document remaining langs in tracker; ship only shell this PR |

| Test | Spec |
|------|------|
| Unit | Extractor returns file + function-like symbols for fixture |
| Walker | `find_files_sync` includes new extensions (mirror REL-032 tests in `src/indexer/mod.rs`) |
| Integration | TempDir `index` → `search_code` hits |

Report optional: `docs/reports/us-gf-10-lang-YYYY-MM-DD.md`.

#### PR-2b — `US-GF-12` live Postgres introspection

| Field | Value |
|-------|-------|
| Branch | `prd/us-gf-12-postgres-extract` |
| CLI | `leankg extract --postgres <dsn>` (PRD §3.10) |
| Behavior | Connect → introspect tables/FKs/views → upsert `CodeElement` + relationships; best-effort link to ORM/repo symbols when name match unique |
| Unit | Mock/schema fixture SQL → expected nodes (no live DB required for CI) |
| Integration | Optional `#[ignore]` live test behind `LEANKG_TEST_POSTGRES_DSN` |
| Live report | When DSN available: `docs/reports/us-gf-12-postgres-YYYY-MM-DD.md` |
| AC | Given reachable DSN, When extract runs, Then ≥1 table node + ≥1 FK edge in graph; mega-safe (no full code dump) |

**Deps:** Prefer `tokio-postgres` / `sqlx` only if already aligned with Cargo policy; otherwise thin `psql -c` subprocess with documented Windows caveat — pick one and test it.

**DONE when:** `.sql` path (already) + Postgres path both documented in `docs/mcp-tools.md` / CLI help.

---

### Wave 3 — Optional mega-safe `get_doc_tree` (`FR-SURF-06b`)

Only if Wave 0 kept a NOT_DONE mega-pagination ID.

| Field | Value |
|-------|-------|
| Problem | `get_doc_tree` mega-refuses via `refuse_full_scan_if_mega`, else still `all_elements()` |
| Fix | Keyed query: documents/doc_sections by `element_type` + pagination / path prefix; no full table load |
| Unit | Synthetic engine / TempDir with &gt;threshold docs still returns tree page |
| Live | On `/workspace-be` (or other mega): tool returns structured page or refuse **without** unhealthy container |
| Report | `docs/reports/fr-surf-06b-doc-tree-mega-YYYY-MM-DD.md` |

Skip this wave if product accepts mega refuse-as-final for doc trees.

---

### Wave 4 — Packaging (`FR-C09..C11`) + optional OnRender F3

After Wave 0 marks **C08 Windows DONE**.

| Sub-ID | Intent | Testable done |
|--------|--------|---------------|
| `FR-C09` | At least one pkg channel (Homebrew tap **or** documented `cargo install` + release asset matrix) | Install doc + CI/release evidence link |
| `FR-C10` | SLSA / provenance attestation on release artifacts | `release.yml` step or documented deferral → `WONT_DO` with reason |
| `FR-C11` | Extra install targets (e.g. winget/scoop) **or** close as Won’t Do if Windows tarball + crates.io enough | Tracker flip |
| OnRender F3 | GHCR/Hub prebuilt embeddings image; Render pulls | Optional; RCA F3 checkbox |

Prefer **one packaging PR** that either ships C09+docs or explicitly WONT_DO C10/C11 with product rationale — do not leave a vague composite `FR-C08..C11` NOT_DONE.

---

### Explicitly out of this campaign

| Item | Reason |
|------|--------|
| Reopening hard-deleted MCP tools | Wave 1b SoT |
| `FR-EMBED-R4` cold-SLA heroics without measurement | Leave OPEN |
| Track E 3D / conversation mining mega | Not in the 9 open rows |
| New Must-Have product tracks | Separate PRD revision |

---

## 6. Tracking board (copy into PR / issue)

Update status cells as PRs merge.

| Wave | ID | Owner PR | Status | Evidence |
|-----:|----|----------|--------|----------|
| 0 | `US-SURF-05` | | ☐ | tracker |
| 0 | `FR-SURF-06` / `06b` | | ☐ | tracker |
| 0 | `US-GF-10` notes | | ☐ | tracker |
| 0 | `US-GF-12` notes | | ☐ | tracker |
| 0 | `FR-C08` Windows | | ☐ | `release.yml` cite |
| 0 | PRD/tracker banner | | ☐ | docs |
| 1 | `FR-B05` | | ☐ | `docs/reports/fr-b05-…` |
| 1 | `FR-B16` | | ☐ | report **or** WONT_DO note |
| 1 | `FR-B51` | | ☐ | report **or** WONT_DO note |
| 2 | `US-GF-10` | | ☐ | unit + walker |
| 2 | `US-GF-12` | | ☐ | CLI + report/ignore live |
| 3 | `FR-SURF-06b` | | ☐ | mega smoke **or** skip |
| 4 | `FR-C09..C11` | | ☐ | release/docs **or** WONT_DO |
| — | `FR-EMBED-R4` | | OPEN | leave |
| — | OnRender F3 | | ☐ | optional |

**Campaign exit criteria**

- [ ] Open tracker rows = 0 **except** intentional `OPEN` (`FR-EMBED-R4`) and any explicit `WONT_DO`
- [ ] PRD priority banner matches residual reality
- [ ] Every shipped FR has unit + (integration or live) evidence path above
- [ ] `cargo test --lib`, fmt, clippy green on `main`

---

## 7. Conductor prompt (copy-paste)

```text
You are the LeanKG remaining-PRD closeout conductor.

Follow: docs/planning/2026-08-03-remaining-prd-closeout.md

Rules:
1. Start with Wave 0 only (tracker/PRD hygiene). Do not implement FR-B* until Wave 0 merges.
2. After Wave 0: run the Wave 1 decision gate for FR-B16 and FR-B51; record WONT_DO in tracker if chosen.
3. One worktree per PR: .worktrees/prd/<slug>/ from origin/main.
4. TDD; update prd-task-tracker.json + .md in the same PR that closes an ID.
5. Docker MCP: project=/workspace (never Mac host paths). Health: curl -sf http://localhost:9699/health
6. No AI attribution. No force-push to main.
7. Max 3 parallel subagents after Wave 0.

First actions:
- git fetch origin && confirm HEAD matches origin/main
- Re-count open rows from docs/prd-task-tracker.json
- Open Wave 0 PR
```

### Worktree bootstrap

```bash
SLUG="<pr-slug>"
git fetch origin
git worktree add ".worktrees/prd/${SLUG}" -b "prd/${SLUG}" origin/main
cd ".worktrees/prd/${SLUG}"
```

---

## 8. Suggested calendar (efficient, not rushed)

| Day | Wave | Outcome |
|----:|------|---------|
| 0 | Wave 0 | Tracker truthful; open count ≤7 |
| 1 | Wave 1 decisions + FR-B05 harness | B05 green or scheduled |
| 2 | FR-B16 / FR-B51 close (MVP or WONT_DO) | No dangling CBM stretch |
| 3–4 | Wave 2 parallel | GF-10 + GF-12 |
| 5 | Wave 3 optional | Doc tree mega **or** skip |
| 6 | Wave 4 | Packaging honesty + optional F3 |

Total calendar ~1 week wall time with 1–2 engineers/agents; less if Wave 1 stretch items are mostly `WONT_DO`.

---

## 9. Risk register

| Risk | Mitigation |
|------|------------|
| Agents re-add `get_doc_structure` | Wave 0 DONE + `redundant_tools_matrix` stays red on resurrection |
| `FR-C08..C11` stays forever NOT_DONE | Wave 0 split; close Windows; decide pkg/SLSA |
| Postgres CI flaky | Mock unit path mandatory; live behind `#[ignore]` + env |
| Cypher subset becomes unbounded Cozo | Hard refuse + node budget; or WONT_DO |
| Stale campaign doc still dispatched | Point conductors here; mark Aug-1 plan superseded for open work |

---

*Last updated: 2026-08-03 — residual 9-row closeout after P0 MCP RCA + P1 waves + SM/GE DONE.*
