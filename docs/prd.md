# LeanKG PRD - Consolidated Tracking Document

**Version:** 3.7.0-vector-engine
**Date:** 2026-07-17
**Status:** Active Development — **single source of truth** for product requirements + HLD
**Author:** Product Owner
**Target Users:** Software developers using AI coding tools (Cursor, OpenCode, Claude Code, Gemini CLI, etc.)
**Codebase Version:** 0.19.0 (`origin/main` @ `e5d1490`)

> **Task lists + status live in one place (humans + AI agents):**
> - Markdown: [`docs/prd-task-tracker.md`](prd-task-tracker.md) — **all** US / FR / Release tasks + status (**sorted by Focus P0→P3**)
> - Machine: [`docs/prd-task-tracker.json`](prd-task-tracker.json)
>
> **Current implementation focus (P0):** Section **3.13 / 5.14 / 8.4** — Optimized Local-First Vector Graph Engine. **Core landed on `main` via #79** (`src/vector_engine/*`); remaining P0 is **FR-VE-GATE / full-scale benches / live A/B / idle RSS** before default cutover. Cozo `::hnsw` remains the shipped ANN default.
>
> This PRD is the SoT for *mission, narrative ACs, HLD, NFRs, glossary*.  
> The tracker is the SoT for *task inventory and Done/Pending/Partial status*.  
> Do **not** reintroduce status tables or FR checkboxes here — link the tracker instead.

> All prior PRD/HLD files under `docs/requirement/`, `docs/design/hld-leankg.md`, and duplicate `.docs/` PRDs have been merged here. Do not recreate split PRDs — update this file only.

---

## Changelog

### v3.7.0-vector-engine - Optimized Local-First Vector Graph Engine (2026-07-17)

> **Task inventory move (same day):** All US/FR/Release status tables and checkboxes were moved to [`prd-task-tracker.md`](prd-task-tracker.md). Sections 3/4/5/8 now reference that file instead of duplicating lists.

> **Code status (synced 2026-07-17 against `origin/main`):** Merged in `#79` / `dbc22c4` — `src/vector_engine/*` (3-tier LocalEngine/CloudEngine, SIMD, dual-write, GC, unit tests, in-process A/B ≥100, `cargo bench --bench vector_engine_ab`). Tracker: FR-VE-ABS..TEST-* + REL-044..047 **DONE**; US-VE-01/02/08 + FR-VE-BENCH-* + FR-VE-GATE + REL-048..050 **PARTIAL**. `evaluate_gate_smoke` keeps `ready_for_default=false`. Crate **0.19.0**.

> **Mission reinforcement:** *"Stop Burning Tokens. Start Coding Lean."* Surgical retrieval = Semantic Search (vectors) + Structural Graphs (LSP/KG). Same product surface as FR-HNSW-*; **new storage/runtime engine** for constrained local hardware and cloud scale without rewriting core query logic.

**Strategic decision (relationship to v3.6.2 / v3.6.3):**
- **Keep** CozoDB `::hnsw` on `embedding_vectors:vec_idx` as the **current shipped canonical ANN** (FR-HNSW-B) until the Local/Cloud vector engine reaches parity and FR-VE-GATE flips default.
- **Adopt** a decoupled **3-tier storage architecture** (graph topology + quantized RAM vectors + flat payload) as the **next-gen LocalEngine / CloudEngine** path — solves query latency, idle RAM, and SSD write amplification under M2 Pro / 16GB / 256GB SSD constraints; scales to Linux x86_64 + TiKV without rewriting retrieval APIs.
- **Do not** reopen FalkorDB/Redis as cold-embed SLA fixes (v3.6.3 Won't Do still stands). This track is about **query/runtime I/O + memory**, not ONNX cold-write throughput.

**Success metrics (product KPIs):**

| KPI | Target | Measurement |
|-----|--------|-------------|
| Token consumption vs grep/cat baseline | ≥ **61%** reduction (floor **60%**) | Agent A/B (`run_kilo_ab_final.sh` / existing benchmark) |
| Tool-call frequency vs baseline | ≥ **84%** reduction (floor **80%**) | Same A/B harness |
| Task success rate | ≥ baseline | Patch/tests pass without hallucination regression |
| Time-to-resolution | ≥ **2×** faster than baseline | End-to-end task timer |
| Idle daemon RSS | **&lt; 150MB** | Local MCP idle after warm |
| Time-to-context (P95) | **&lt; 100ms** | JSON chunks + deps payload to agent |
| ANN query P95 (1M SQ8, local) | **&lt; 50ms** | `cargo bench` |
| Recall @ efSearch=50 vs FP32 brute-force | **&gt; 90%** | Bench + unit |
| Disk reads / page faults vs legacy mmap | ≥ **80%** reduction | Bench instrumentation |
| 2GB cgroup survival | Never OOM-killed | Simulated cgroup test |

**New content:**
- Section **3.13** — US-VE-01..08 (vector engine stories)
- Section **5.14** — FR-VE-* (3-tier storage, SIMD, HNSW prune, dual-write, GC, tests/benches)
- Section **6.10** — HLD for LocalEngine vs CloudEngine + 3-tier diagram
- Section **8.4** — v3.7 vector-engine release gate
- Section **9** — NFR table refreshed for idle/query/hardware targets

### v3.6.3-embed-runtime - Cold embed SLA reality + MCP decoupling (2026-07-16)

> **Measured reality (mega-graph cold embed):** end-to-end sustained rate is ~**170 vec/sec** → ~**36 min** for ~371k `function,method` nodes (M2 Pro 10c). Writer-only microbenches on empty RocksDB show Cozo `import_relations` at ~**100k–130k vec/sec** (&lt;1 min for 371k). **Storage commit / WAL is not the cold-SLA bottleneck**; ONNX inference + end-to-end CPU contention is.

**Done (ops / architecture):**
- MCP boot decoupled from embed: `LEANKG_EMBED_ON_BOOT=0` + in-process `LEANKG_EMBED_BACKGROUND=1` (shared `CozoDb`). MCP healthy ~60s while embed continues. See FR-EMBED-R1.
- Parallel embed pipeline + `import_relations` + `DirectEmbedder` (FR-EMBED-R2). ~2× vs earlier `:put` path (~73 min → ~36 min ETA) — still above aspirational &lt;10/&lt;20 min cold.

**Tried and rejected as cold-SLA fixes (evidence in `generated_docs/embed_bg_job_and_runtime_plan_2026-07-15.md`):**
- Cozo RocksDB WAL-off / `sync(false)` / no-snapshot write txs (`LEANKG_COZO_ROCKS_BULK`) — **≤1.15×** writer-only; no meaningful e2e gain.
- Redis Stack HNSW as vector side-store (`LEANKG_EMBED_VECTOR_STORE=redis`) — bulk HASH write ~164k/s (similar to Cozo); live HNSW during write ~2.7k/s (**worse**). Does **not** beat Cozo for cold SLA. Keep Cozo HNSW as canonical (FR-HNSW-B). Redis remains experimental only.

**Product SLA (revised):**
- **Must:** MCP never blocks on cold embed; semantic tools degrade until HNSW ready; day-2 incremental embed stays fast (FR-HNSW-E).
- **Aspirational / open:** cold functions-only &lt;20 min on ~371k (needs **faster inference / smaller model / less volume**, not a new DB). Do not plan FalkorDB/Redis migration to fix cold embed.

**New FRs:** Section **5.12** additions FR-EMBED-R1..R4.

### v3.6.2-hnsw-semantic - Drop LSH roadmap; expand CozoDB HNSW for semantic search (2026-07-15)

> **Strategic decision:** LeanKG differentiates on **meaning-based retrieval** (dense embeddings + CozoDB native `::hnsw`), not on copy-paste / near-clone detection (MinHash / LSH). Agents need “what means like this,” not “which bodies are Jaccard-near.”

**Cancel / Won’t Do (LSH track):**
- FR-LSH-A..F and FR-BENCH-A (CBM MinHash parity) — **Won’t Do**. Do not expand MinHash/LSH; do not adopt Cozo `::lsh` for clones either (clone ANN is out of product focus).
- Custom in-process LSH (`src/minhash.rs` + `find_clones --cross-file`) **removed** on `integration/prd-pending` (FR-HNSW-A). Same-file Jaccard `find_clones` remains as a light non-strategic tool.
- US-CBM-B7 / FR-B30 / FR-B31 remain historically DONE for the light same-file Jaccard tool, but are **non-strategic** — no further LSH investment; optional later deprecation of `find_clones` / `leankg clones` if unused.

**Adopt / Expand (HNSW track) — reuse CozoDB 0.7.x native index (already in tree):**
- LeanKG already depends on `cozo = "0.7.6"` and already uses `::hnsw create embedding_vectors:vec_idx` (`src/embeddings/state.rs`, `src/retrieval/pipeline.rs`). Pattern to double down on: **LeanKG extracts features → Cozo indexes**.
- New FRs: Section **5.12** (HNSW expansion) + Section **5.13** (LSP-only remainder from former CBM adoption track).
- **Implementation landed on `integration/prd-pending` (2026-07-15):** FR-HNSW-A..F + FR-BENCH-HNSW + US-CBM-C1 / FR-C01 (Docker `--features embeddings` + `entrypoint.sh` `embed_if_needed`; HNSW `semantic_search` dispatch; `LEANKG_HNSW_{M,EF_CONST,EF}` knobs; `tests/hnsw_recall_e2e.rs` synthetic recall@k smoke).
- **PRD hygiene (2026-07-15):** corrected language / Graphify / MemPalace status rows that overclaimed “DONE” for extractors that exist as modules but are **not hooked into the index walk** (Swift, Vue/Svelte, SQL DDL). Softened “17 languages fully extracted” claims to match `find_files_sync` + `get_language`.
- Research record: `generated_docs/research_cozo_native_lsh_vs_custom_minhash_2026-07-15.md` (main tree) — Cozo already ships both `::hnsw` and `::lsh`; we choose HNSW only.

**CBM deep-compare (v3.6.1) still valid for LSP gaps** (FR-LSP-A..D). MinHash / LSH “wins” from that compare are explicitly **not** adopted.

### v3.6-lsp-ontology - LSP infra, language breadth, status flips (integration/prd-pending push)
- LSP infrastructure shipped (US-CBM-B1 infra, FR-B03..B07 scaffolding): new `src/lsp/{bridge,client,config,mod}.rs` — generic JSON-RPC bridge that spawns any configured language server, answers `textDocument/definition` and `/references`; per-(language, workspace_root) client cache; 12-language manifest detection (go.mod / package.json / Cargo.toml / pyproject.toml / pom.xml / build.gradle* / tsconfig.json / Gemfile / mix.exs / pubspec.yaml / Project.toml / Package.swift). Wired through MCP `resolve_with_lsp` (`src/mcp/handler.rs:1674`) and CLI `leankg lsp-resolve` (`src/main.rs:lsp_resolve`). Commits `534cd7f` + `64b0fa6`.
- `typed_resolve` feature flag landed (US-CBM-B10 / FR-B08, `8971dc5`). Default `LspConfig` is still empty (`src/lsp/config.rs:57`); LSP server bootstrap (default `lsp:` block for gopls + tsserver + pyright) remains the open follow-up.
- Codebase version: 0.17.8 → 0.17.9 (`3e103b1 chore(release): regen Cargo.lock for 0.17.9` + `1c6f1eb chore(release): bump version to 0.17.9`).
- Language breadth — **status corrected 2026-07-15 (wiring audit):**
  - US-LANG-01 Dart — **DONE and indexed** (in `find_files_sync` + `get_language`) (`7ec6484`)
  - US-LANG-02 Swift — **PARTIAL**: regex extractor in `src/indexer/swift.rs` (`7027d6b`); **not wired** into `find_files_sync` / index walk (`.swift` not scanned)
  - US-LANG-03 XML — **DONE and indexed** (`.xml` + Android path) (`92db9aa`)
  - US-GF-10 Vue/Svelte — **PARTIAL**: regex extractors in `src/indexer/sfc.rs` (`e617a49`); **not wired** into index walk (`.vue` / `.svelte` not scanned)
  - US-GF-12 SQL DDL — **PARTIAL**: parser in `src/indexer/sql.rs` (`de314eb`); **not wired** into index walk (`.sql` not scanned)
- Agent-graph UX series — DONE:
  - US-GF-07 rationale extraction (`# WHY:` / `# NOTE:` / `# HACK:` / `# FIXME:` / `# XXX:` markers) → `rationale` elements with `explains` edges (`b0c9477`)
  - US-GF-08 PR impact dashboard — `get_pr_impact` MCP + `leankg prs` CLI (`30e41f0`)
  - US-GF-09 work-memory reflect loop — `report_query_outcome` + `.leankg/reflections/LESSONS.md` (`373e808`)
  - US-GF-11 portable graph snapshot — `export_graph_snapshot` MCP (`0087991`)
- MemPalace series — DONE:
  - US-MP-01 temporal knowledge graph — `valid_from` / `valid_to` on `Relationship` (`bc9cc53`)
  - US-MP-04 specialist agent contexts — `agent_focus` + `agent_diary_{read,write}` MCP (`1ea4bcd`)
  - US-MP-05 consistency checker — `check_consistency` MCP + `leankg check-consistency` CLI (`60a6111`)
  - US-MP-06 cross-domain tunnels — `find_tunnels` MCP + `leankg tunnels` CLI (`5b6547e`)
- CBM structural — DONE:
  - US-CBM-B6 event-channel edges `emits` / `listens_on` (`25a3b37`)
  - US-CBM-B7 clone / near-duplicate detection — `find_clones` MCP + `leankg clones` CLI (`55e6e72`)
  - US-CBM-B8 cross-repo similar edges — `find_cross_repo_similar` (`ab16c9b`)
  - US-CBM-C2 hot-path cache for high-frequency MCP tools (`836f0a3`)
- GitNexus — DONE:
  - US-GN-07 `get_cluster_skill` MCP — per-cluster `SKILL.md` (`10b15a0`)
  - US-GN-08 `get_overview_context` MCP — resource-style overview (`9124959`); formal `resources/read` not yet wired (PARTIAL).
- Team / distribution — DONE:
  - US-14 npm-based installation wrapper (`df0fec2`)
  - US-V2-11 CI/CD auto-graph update — GitHub Actions workflow that reindexes / commits the portable snapshot on release (`eb3d331`)
  - US-V2-12 `get_team_map` MCP — team + on-call ownership + environment map (`3368b5f`)
- Quality gate: `cargo fmt --all -- --check`, `cargo clippy --release --all-targets -- -D warnings`, `cargo test --release --lib` (496), `cargo test --release --bin leankg` (491), `cargo test --release --test ontology_e2e` (16/16) all PASS (`docs/implementation/prd-integration-2026-07-14.md`).
- MCP tool count: 65 → 85 (`src/mcp/tools.rs` — audit using `awk '/^[[:space:]]+name:[[:space:]]*"/{ print }' src/mcp/tools.rs | sort -u | wc -l` = 85 unique tool registrations as of 2026-07-14).
- Open follow-ups: default `lsp:` block for gopls + tsserver + pyright + dart-language-server + sourcekit-lsp + kotlin-language-server; FR-B03 / FR-B04 actual `typed` resolution for Go and TS; FR-MG-03 single-repo root expansion; 3D graph UI (Track E). **Superseded by v3.6.2 for LSH:** do not pursue FR-LSH-*; pursue FR-HNSW-* instead.

### v3.6.1-cbm-deep-compare - In-process read of CBM LSH + Hybrid LSP (2026-07-15)

> Source: direct read of `DeusData/codebase-memory-mcp` at `/Users/linh.doan/work/harvey/freepeak/codebase-memory-mcp` (v0.9.x, Pure C, 158 languages, 15 MCP tools).
>
> **Superseded for LSH:** v3.6.2 cancels MinHash/LSH adoption. This section remains as competitive research only. **Still actionable:** Hybrid LSP gaps → FR-LSP-A..D in Section 5.13.
>
> **TL;DR — CBM's "Hybrid LSP" is not actual LSP.** It is a lightweight C implementation of language type-resolution algorithms embedded in the binary (no spawn, no JSON-RPC). Their `LshIndex` for near-clones is a textbook MinHash+LSH pipeline — useful for *their* clone-edge product; **LeanKG will not mirror it** (semantic HNSW focus instead).

**CBM MinHash / LSH for `SIMILAR_TO` (clone) edges** — research only (`src/simhash/minhash.{h,c}`). Historical LeanKG comparison to `src/minhash.rs` is obsolete once that module is removed (v3.6.2).

| Knob | CBM | LeanKG (pre-removal) | Note |
|------|-----|----------------------|------|
| Role | Core clone product | Non-strategic `find_clones` helper | **Won't adopt** further LSH |
| Shingle unit | AST leaf trigrams `I/S/N/T` | Whitespace 5-grams | Irrelevant under HNSW strategy |
| Index home | In-process C | Custom Rust `LshIndex` (also unused Cozo `::lsh`) | Prefer deleting custom LSH; do not wire Cozo `::lsh` |

**CBM Hybrid LSP (pass over tree-sitter)** — `internal/cbm/lsp/{py,go,ts,java,kotlin,rust,c,cpp,cs,php,perl}_lsp.{c,h}` plus `type_rep.{c,h}`, `scope.{c,h}`, `type_registry.{c,h}`, `py_builtins.c`, `kotlin_builtins.c`, `rust_cargo.c`, `rust_proc_macros.c`, `rust_rustdoc.{c,h}`, `generated/python_stdlib_data.c` (12k lines of pre-baked stdlib metadata):

| Surface | CBM | LeanKG (`src/lsp/{bridge,client,config,mod}.rs`) |
|---------|-----|--------------------------------------------------|
| Approach | **In-process C type evaluator.** No `fork`/`exec`/`popen`, no JSON-RPC. Each language file re-implements the resolver inline (e.g., `py_lsp_init` / `py_lsp_process_file` / `py_lsp_bind_imports`) | **Real JSON-RPC bridge.** Spawns external server (`gopls`, `tsserver`, `pyright`, …); sends `textDocument/definition` + `/references`; caches one client per `(language, workspace_root)` |
| Languages | 10 — Python, TS/JS/JSX/TSX, PHP, C#, Go, C, C++, Java, Kotlin, Rust, Perl (per-language files in `internal/cbm/lsp/`) | 12 manifest-detected (go.mod / package.json / Cargo.toml / pyproject.toml / pom.xml / build.gradle* / tsconfig.json / Gemfile / mix.exs / pubspec.yaml / Project.toml / Package.swift), **0 default-configured servers** |
| Setup | Zero. Embedded in the static binary | User must populate `lsp.servers.<lang>.command` in `leankg.yaml` |
| Correctness model | Re-implements the algorithm the way gopls/pyright/Roslyn would — output is "structurally compatible" | Uses the real server's answer; can get accurate types the C reimplementation misses |
| When does it run? | Per-file during extraction, BEFORE `CALLS` edges are written — refines `CALLS`/`USAGE`/`RESOLVED_CALLS` directly | After index, on demand via `resolve_with_lsp` MCP / `leankg lsp-resolve` CLI; has not yet been wired to write `resolution_method=typed` edges |
| Failure mode | Falls back to "textual resolution" (tree-sitter-only) for unsupported languages | Returns `Ok(None)` and the caller falls back to tree-sitter typed resolve (FR-B07) |

**LeanKG wins (what CBM does not have):**
- 85 MCP tools vs CBM's 15
- Ontology / concept / workflow layer
- **CozoDB native HNSW embeddings path** (semantic ANN) — primary differentiation going forward (v3.6.2)
- `env` namespacing + incident knowledge + service context + env-conflict detection
- Android / Kotlin / XML deep features, Graphify-inspired work-memory loop, tunnel detection, consistency checker, portable graph snapshot, npm install
- Real language-server correctness (when a server is configured)
- REST API + RocksDB multi-project HTTP team deploy
- Per-cluster SKILL.md, overview-context, team-map

**CBM wins — adopt vs ignore:**
- **Adopt:** Zero-setup Hybrid LSP on 10 languages → FR-LSP-A..D (Section 5.13)
- **Ignore (v3.6.2):** AST-trigram MinHash, K=64 signatures, big-bucket guards, clone Jaccard defaults — clone LSH is not LeanKG's bet

**Adoption FRs:** LSP → Section 5.13 (FR-LSP-A..D). HNSW expansion → Section 5.12 (FR-HNSW-*). Former FR-LSH-* → Won't Do.

### v3.5-unified - Single PRD+HLD document
- Merged `docs/requirement/prd-leankg.md` (v2 team infrastructure) → Section 3.12 / 5.11
- Merged `docs/design/hld-leankg.md` → Section 6.4–6.9 (HLD)
- Merged `leankg update` CLI PRD → US-UPD-01
- Confirmed CBM structural parity already in 3.11 / 5.10; deleted redundant source files
- Codebase status refresh: env/incident/service tools, vacuum scheduler, `kg_self_test`, `leankg update` marked DONE where implemented
- Removed: `docs/requirement/prd-*.md`, `docs/design/hld-leankg.md`, `docs/LeanKG_v2_PRD.html`, duplicate `.docs` PRD stubs

### v3.4-cbm-structural-merge - Merge structural parity (CBM) PRD + codebase status refresh
- Merged CBM structural parity into this document (Section 3.11 US-CBM, Section 5.10 FR-CBM); source file removed in v3.5
- Codebase audit (2026-07-13, v0.17.8): **65 MCP tools** in `src/mcp/tools.rs`; Phase 1 aggregators DONE; Routes/HTTP_CALLS extractors DONE; typed resolve / clones / cross-repo / 3D UI still PENDING
- Updated executive metrics, pending table, and roadmap Phase 1 status to match code
- Source CBM PRD retained as archive with pointer to this document as SoT

### v3.3-graphify-parity - Graphify competitive enhancements
- Competitive analysis of [Graphify](https://github.com/Graphify-Labs/graphify) (v8 / ~83k stars) vs LeanKG deploy + agent tooling
- Full comparison: `docs/analysis/graphify-comparison-2026-07-13.md`
- Added US-GF-01..12 user stories for Graphify-inspired agent graph UX, edge provenance, reports, PRs, and learning loop
- Added FR-GF-01..20 functional requirements (Section 5.9)
- Priority focus: shortest-path / explain / NL subgraph query, EXTRACTED|INFERRED|AMBIGUOUS edge labels, god-node ranking, GRAPH_REPORT.md — not rewriting LeanKG's stronger RocksDB multi-project deploy

### v3.2-toon-format - TOON response format for MCP tools (~40% token reduction)
- Added US-TOON-01 user story for TOON (Token-Oriented Object Notation) format adoption
- TOON is a compact notation that reduces field name repetition in arrays
- Example: `elements[2]{qualified_name,type}: src/main.rs::main,function` vs JSON with full field names
- TOON spec: https://github.com/toon-format/toon
- Added Section 7.5 TOON Response Templates for all MCP tool categories

### v3.1-massive-graph - Massive graph service expansion
- Added US-MG-01..05 user stories for service node double-click behavior
- Added FR-MG-01..08 functional requirements for expand-service optimization and filter UI
- Expand-service API optimized: targeted folder query (7.7k vs 1.5M elements), ~30% faster
- FR-MG-01..02, 04..08 implemented: expand-service returns all edge types, double-click calls expandService directly, filter panel always shows all 14 types, defaults = Service/Folder/File/Function
- FR-MG-03 (single-repo root expansion) still pending

### v3.0-consolidated - Full codebase audit
- Deep dive codebase analysis: 35 MCP tools verified (0 stubs), 28+ CLI commands, 10 language extractors
- Updated language support: 10 fully extracted (Go, TS/JS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP) + 3 parser-only (Dart, Swift, XML) — **superseded by 2026-07-15 wiring audit:** only Go/TS/JS/Python/Rust/Java/Kotlin/Dart (+XML/TF/CI) are in the current index walk; C++/C#/Ruby/PHP/Swift not scanned
- Updated all user story statuses based on actual implementation
- Added missing feature sections: Git Hooks, Context Metrics, REST API, Wiki Generation, Global Registry, Graph Export, Orchestrator
- Unified RTK Compression status: ResponseCompressor (FR-RTK-11..15) now marked DONE
- Fixed US-GN-03 (Global Registry) status: DONE (was PENDING)
- Fixed AB Testing stories: US-AB-02..04 marked DONE
- Removed outdated references to non-existent features
- Added new user stories for recently implemented features

### v2.0-consolidated - Merged from 3 source PRDs
- Source 1: `prd-leankg.md` (v1.7, 2026-03-27)
- Source 2: `prd-leankg-v2.0-enhancements.md` (v2.0, 2026-03-27)
- Source 3: `prd-leankg-gitnexus-enhancements.md` (v1.0, 2026-03-27)

---

## 1. Executive Summary

LeanKG is a lightweight, local-first knowledge graph solution designed for developers who use AI-assisted coding tools. The mission is *"Stop Burning Tokens. Start Coding Lean."* — resolve AI agent **context blindness** with surgical retrieval: **Semantic Search (vectors) + Structural Graphs (LSP/KG)**, not shotgun `grep`/`cat`.

Unlike heavy frameworks like Graphiti that require external databases (Neo4j) and cloud infrastructure, LeanKG runs on constrained local hardware (Apple Silicon, 16GB RAM, 256GB SSD) with a strict idle footprint, while the same core logic can scale to self-hosted cloud (Linux x86_64, TiKV) via a storage abstraction — without rewriting retrieval APIs.

**Value proposition (agent economics):**
- Cut LLM tokens by ≥ **61%** and tool calls by ≥ **84%** vs traditional grep/cat baselines, while holding Fix Success Rate ≥ baseline
- Deliver code chunks + dependencies JSON to the agent in **&lt; 100ms P95**; idle MCP **&lt; 150MB RSS**
- Prefer vector+graph scalpel over full-repo dumps (see Section 3.13 / 5.14)

**Key Metrics (v0.19.0 — codebase `origin/main` 2026-07-17; engine KPIs in Section 9 / 8.4):**
- **Vector engine (v3.7 P0):** `src/vector_engine/*` on `main` (#79) — opt-in via `LEANKG_VECTOR_ENGINE=local|cloud`; Cozo `::hnsw` still default until FR-VE-GATE
- **85 MCP tools** defined in `src/mcp/tools.rs` (stdio + HTTP/SSE)
- 30+ CLI commands (added `leankg lsp-resolve`, `leankg check-consistency`, `leankg tunnels`, `leankg prs`, `leankg clones`, `leankg reflect`)
- **Indexed languages (production walk):** Go, TS/JS, Python, Rust, Java, Kotlin, Dart + Android/XML + Terraform/CI YAML + common config manifests. **Extractor modules exist but not indexed yet:** Swift (`swift.rs`), Vue/Svelte (`sfc.rs`), SQL DDL (`sql.rs`). Parsers may exist for Ruby/PHP/etc. without index-walk wiring. + Markdown docs
- 8 compression/read modes + TOON responses
- Smart orchestrator with persistent cache + hot-path cache for high-frequency MCP tools (`836f0a3`)
- Git hooks (pre-commit, post-commit, post-checkout) + CI/CD auto-graph update GitHub Actions workflow (`eb3d331`)
- REST API server with auth
- Context metrics tracking
- Global multi-repo registry
- RocksDB multi-project HTTP deploy
- Structural aggregators: `get_architecture`, `get_graph_schema`, `find_dead_code` (DONE)
- Route + `http_calls` extractors for Go (chi/gin/echo) and TS (express/fastify) (DONE)
- Event-channel edges `emits` / `listens_on` (DONE `25a3b37`)
- Wake-up context + consistency checker + cross-domain tunnels (`wake_up`, `check_consistency`, `find_tunnels` — DONE)
- LSP bridge infrastructure + `resolve_with_lsp` MCP + `leankg lsp-resolve` CLI (DONE `534cd7f` + `64b0fa6`); `typed_resolve` feature flag in `IndexerConfig` (DONE `8971dc5`); **default `LspConfig::servers` is still empty** — default-server bootstrap is the open follow-up.
- Call edges carry `resolution_method` + numeric `confidence` (`name` / `name_file_hint` / `unresolved`; `typed` not yet produced)
- Temporal knowledge graph (`valid_from` / `valid_to`) + specialist agent contexts (`agent_focus` + diary) (DONE `bc9cc53`, `1ea4bcd`)
- Agent-side report: rationale nodes (WHY/NOTE/HACK/FIXME/XXX), PR impact dashboard, work-memory reflect loop → `.leankg/reflections/LESSONS.md` (DONE `b0c9477`, `30e41f0`, `373e808`)
- Portable graph snapshot (`export_graph_snapshot` MCP) (DONE `0087991`)
- npm-based installation wrapper (DONE `df0fec2`)

**Competitive notes:**
- vs [Graphify](https://github.com/Graphify-Labs/graphify): see Section 3.10 / `docs/analysis/graphify-comparison-2026-07-13.md`
- vs [codebase-memory-mcp](https://github.com/DeusData/codebase-memory-mcp): see Section 3.11 / 5.10 — Lean into business-context depth; close structural gaps; do **not** chase 158-language / Pure-C parity
- vs LSP-by-default (CBM style): see Section 3.11 / 5.10 — LeanKG now has the bridge + wiring (FR-B03..B07 + FR-B08); `typed`-class edges still PENDING for Go (`FR-B03`) and TS (`FR-B04`).
- vs mmap-heavy / full-FP32-in-RAM vector stores: LeanKG targets SQ8 hot path + flat payload post-filter (Section 5.14 / 6.10) to protect 256GB SSDs and 16GB laptops.

---

## 2. Problem Statement

### 2.1 Current Pain Points

| Pain Point | Description |
|------------|-------------|
| **Context Window Dilution** | AI tools scan entire codebases, including irrelevant files, wasting context window tokens |
| **Outdated Documentation** | Manual docs quickly become stale; AI receives wrong context |
| **Business Logic Disconnect** | No clear mapping between business requirements and code implementation |
| **Token Waste** | Redundant code scanning generates unnecessary token costs |
| **Poor Code Generation** | AI lacks accurate context, producing incorrect or suboptimal code |
| **Feature Transfer Difficulty** | Onboarding new developers requires extensive code exploration |
| **Impact radius lacks confidence grades** | `get_impact_radius` returns all edges at equal weight; LLM cannot distinguish "WILL BREAK" from "MIGHT BE AFFECTED" |
| **No pre-commit risk signal** | No tool exists to assess change risk before commit |
| **Flat search results** | `search_code` returns symbol matches with no grouping by functional area |
| **No shortest-path / explain verbs** | Agents cannot ask "how do A and B connect?" or get a single-node dossier (Graphify gap) |
| **Opaque edge provenance** | Agents cannot tell EXTRACTED vs INFERRED vs AMBIGUOUS relationships at a glance |
| **No architecture brief artifact** | Missing god-node + surprising-connection report (`GRAPH_REPORT.md`) after index |
| **No query outcome learning** | Context metrics exist, but agents cannot report whether a graph answer was useful |

---

## 3. User Stories

### 3.1 Core MVP Stories (US-01 to US-18)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-01`..`US-18`.


### 3.2 v2.0 Enhancement Stories (US-19 to US-27)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-19`..`US-27`.


### 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-GN-01`..`US-GN-09`.


### 3.4 AB Testing Stories (US-AB-01 to US-AB-05)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-AB-01`..`US-AB-05`.


### 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-RTK-*`.


### 3.6 Infrastructure Stories (US-INF-01 to US-INF-10)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-INF-*`.


### 3.7 Additional Language Stories (US-LANG-01 to US-LANG-03)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-LANG-*`.


### 3.8 Massive Graph Stories (US-MG-01 to US-MG-05)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-MG-*`.


### 3.9 TOON Format Stories (US-TOON-01)

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `US-TOON-01`.


**Detailed Feature Descriptions:**

<details>
<summary>US-TOON-01: TOON Response Format</summary>

**Problem:** JSON responses from MCP tools include repetitive field names in arrays, wasting tokens.

**Solution:** Adopt TOON (Token-Oriented Object Notation) format which omits field names within array items when they match the schema template.

**Example comparison:**

JSON (312 tokens):
```json
{
  "elements": [
    {"qualified_name": "src/main.rs::main", "type": "function", "language": "rust"},
    {"qualified_name": "src/lib.rs::init", "type": "function", "language": "rust"}
  ],
  "tokens": 312
}
```

TOON (187 tokens, 40% reduction):
```
{
  elements[2]{qualified_name,type,language}:
    src/main.rs::main,function,rust
    src/lib.rs::init,function,rust
  tokens: 187
}
```

**Specification:** https://github.com/toon-format/toon

**Implementation:**
- All MCP tool responses wrapped in Response Format Envelope
- Envelope: `{status: ok, tool: <name>, format: toon|json, tokens: <count>, data: <toon_string>}`
- Default format is `toon`; clients can request `json` via `format=json` parameter

</details>

**Detailed Feature Descriptions:**

<details>
<summary>US-MG-01: Service node loads all elements and edges</summary>

**Problem:** Previously, double-clicking a Service node in the graph only returned a subset of relationship types (`contains`, `defines`, `imports`, `calls`). Other edges like `extends`, `implements`, `references`, `tested_by` were missing from the expanded view.

**Behavior:**
- Double-click on a Service node → `/api/graph/expand-service?path=<absolute_path>` returns ALL elements under that service folder AND ALL relationship types between them
- Backend must NOT filter by relationship type — let the frontend filter UI control visibility
- All node types are returned: `service`, `folder`, `directory`, `file`, `module`, `class`, `struct`, `interface`, `enum`, `function`, `method`, `constructor`, `property`, `decorator`

**Backend changes:**
- `api_graph_expand_service` handler removes the `matches!(r.rel_type.as_str(), "contains" | "defines" | "imports" | "calls")` filter
- Returns ALL relationships where source is in the service folder

**Frontend changes:**
- Filter UI is the sole mechanism for controlling what's visible
- User toggles edge types on/off to see calls, imports, contains, etc.
</details>

<details>
<summary>US-MG-02: Single-repo full expansion</summary>

**Problem:** When a service has many nested folder layers (e.g., `platform-transport/be-engagement/internal/handler/v2/`), the user must double-click through each folder level to see contents. This loses the overall service context.

**Behavior:**
- Double-click on a Service node loads the ENTIRE service tree at once
- All folders, sub-folders, files, and functions are loaded in a single API call
- The filter UI controls visibility: by default, only `Service`, `Folder`, `File`, `Function` nodes are shown
- User can toggle on `Method`, `Class`, etc. to see more detail without making another API call
- For single-repo projects (no multi-service layout), the same behavior applies — the root is treated as the "service"

**Rationale:** Loading everything at once is fast (~13s for 7.7k elements after optimization) and avoids the UX problem of losing the chart context during multi-level drilling.
</details>

<details>
<summary>US-MG-03: Filter UI always shows all node types</summary>

**Problem:** Previously `discoveredNodeTypes` was computed from loaded graph data. If the current view only has `File` and `Function` nodes, the filter panel only shows `File` and `Function` toggles.

**Behavior:**
- The filter panel ALWAYS shows ALL node types from `DEFAULT_NODE_TYPE_ORDER`: `Service`, `Folder`, `Directory`, `File`, `Module`, `Class`, `Struct`, `Interface`, `Enum`, `Function`, `Method`, `Constructor`, `Property`, `Decorator`
- This is a static list, not data-driven
- Types not present in current data still appear but are visually dimmed or show "(0)" count

**Implementation:**
- `discoveredNodeTypes` in `App.tsx` uses `DEFAULT_NODE_TYPE_ORDER` directly instead of computing from `data.nodes`
</details>

<details>
<summary>US-MG-04: Default visible filters</summary>

**Problem:** Previously the default visible labels included `Service`, `Folder`, `Directory`, `File` — missing `Function` which is the most important code-level type. Also, ALL types started as visible, making the graph too noisy.

**Behavior:**
- **Default ON (visible):** `Service`, `Folder`, `File`, `Function`
- **Default OFF (hidden):** `Directory`, `Module`, `Class`, `Struct`, `Interface`, `Enum`, `Method`, `Constructor`, `Property`, `Decorator`
- `resetToStructuralDefaults()` resets to these 4 types
- After double-clicking a service, filters reset to these 4 defaults

**Implementation:**
- `DEFAULT_VISIBLE_LABELS` = `['Service', 'Folder', 'File', 'Function']`
- `useGraphFilters` initial state uses `DEFAULT_VISIBLE_LABELS`
- `resetToStructuralDefaults()` uses `DEFAULT_VISIBLE_LABELS`
</details>

<details>
<summary>US-MG-05: Expand-service API optimization</summary>

**Problem:** The original `api_graph_expand_service` handler called `g.all_elements()` and `g.all_relationships()` which loaded ALL 1.5M elements and ALL 1.6M relationships into memory, then filtered in Rust. This took ~19 seconds.

**Solution (DONE):**
- Added `get_elements_in_folder()` to `GraphEngine` using CozoDB `regex_matches(file_path, $pat)` with bound parameter
- Handler converts absolute paths to DB format: `/Users/.../be-engagement` → `./platform-transport/be-engagement`
- Only loads ~7.7k relevant elements instead of 1.5M
- Response time: ~13s (30% improvement). Remaining time is from loading all 1.6M relationships.
</details>

### 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08)

> **Source:** Competitive analysis of [MemPalace](https://github.com/milla-jovovich/mempalace) — the highest-scoring AI memory system on LongMemEval (96.6% R@5 raw mode). Key differentiator: raw verbatim storage without summarization, structured spatial navigation (wings/rooms/closets/drawers), temporal entity graph with validity windows, and a 4-layer memory stack (L0-L3) for token-efficient context loading.


> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter IDs for this section (`US-*` / related).


**Detailed Feature Descriptions:**

<details>
<summary>US-MP-01: Temporal Knowledge Graph</summary>

**MemPalace inspiration:** Entity relationships have validity windows (`valid_from`, `valid_to`). When something stops being true, it's invalidated but retained for historical queries.

**LeanKG adaptation:**
- Add `valid_from` and `valid_to` (nullable) fields to `Relationship` table
- When re-indexing detects a removed import/call, set `valid_to = now()` instead of deleting
- New MCP tool: `temporal_query` — "what did the dependency graph look like before commit X?"
- New MCP tool: `invalidate_edge` — manually mark an edge as no longer current
- Timeline view: chronological story of how a code element's dependencies evolved
</details>

<details>
<summary>US-MP-02: Layered Context Loading (L0-L3)</summary>

**MemPalace inspiration:** 4-layer memory stack where L0+L1 (~170 tokens) are always loaded, L2 is on-demand, L3 is deep search.

**LeanKG adaptation:**
- **L0 — Project Identity** (~50 tokens): Project name, languages, top-level directories, architecture pattern.
- **L1 — Critical Facts** (~120 tokens): Module map, critical dependencies, recent change hotspots.
- **L2 — Cluster Context** (on demand): When a query touches a specific area, load the relevant cluster's symbols.
- **L3 — Deep Search** (on demand): Full graph traversal, impact analysis, cross-cluster queries.
- New MCP tools: `wake_up` (L0+L1), `load_layer` (L2/L3)
</details>

<details>
<summary>US-MP-03: Conversation/Decision Mining</summary>

**MemPalace inspiration:** Mines conversation exports (Claude, ChatGPT, Slack) to extract decisions, preferences, milestones. Stores raw verbatim.

**LeanKG adaptation:**
- New indexer module: `conversation_indexer` — parses Claude/ChatGPT/Slack export JSON
- Extracts: decisions, preferences, milestones, problems
- Creates `decision`, `preference`, `milestone`, `problem` element types
- Links decisions to code elements via `decided_about` relationship
- Store raw verbatim — no summarization
- New CLI command: `leankg mine-conversations ~/chats/ --format claude|chatgpt|slack`
</details>

<details>
<summary>US-MP-04: Specialist Agent Contexts</summary>

**MemPalace inspiration:** Define agent personas (reviewer, architect, ops) each with their own wing and diary.

**LeanKG adaptation:**
- Agent config in `.leankg/agents/*.json` — focus areas and context filters
- Each agent gets a filtered view of the graph
- Agent diary: per-agent CozoDB table storing session notes
- New MCP tools: `agent_focus`, `agent_diary_write`, `agent_diary_read`
</details>

<details>
<summary>US-MP-05: Contradiction & Staleness Detection</summary>

**MemPalace inspiration:** `fact_checker.py` validates assertions against stored entity facts.

**LeanKG adaptation:**
- New module: `consistency_checker` — runs on `detect_changes` or standalone
- Checks: annotations referencing deleted code, documented_by links to moved files, stale clusters
- Severity: 🔴 BROKEN, 🟡 STALE, 🟢 CURRENT
- New MCP tool: `check_consistency`, new CLI: `leankg check-consistency`
</details>

<details>
<summary>US-MP-06: Cross-Domain Tunnels</summary>

**MemPalace inspiration:** "Tunnels" auto-connect rooms from different wings when the same topic appears.

**LeanKG adaptation:**
- Auto-detect shared domain concepts across clusters
- Create `tunnel` relationship type linking related clusters
- New MCP tool: `find_tunnels`
- Enhance `orchestrate` to follow tunnels
</details>

<details>
<summary>US-MP-07: Wake-up Context Protocol</summary>

**MemPalace inspiration:** `mempalace wake-up` loads ~170 tokens of L0+L1.

**LeanKG adaptation:**
- New MCP tool: `wake_up` — returns compressed project summary (~170 tokens)
- Content: project name, languages, top directories (wings), recent hotspots, critical files
- Cached in `.leankg/wake_up.txt`, regenerated on re-index
</details>

<details>
<summary>US-MP-08: Folder Structure as Graph Edges</summary>

**MemPalace inspiration:** MemPalace's wing → room → closet → drawer is a spatial hierarchy. Each level is a navigable node with typed edges.

**LeanKG adaptation:**
- **`directory` element type** — every indexed directory becomes a first-class node
- **`contains` edges for full hierarchy:**
  - `directory → directory` (e.g., `src/` contains `src/graph/`)
  - `directory → file` (e.g., `src/graph/` contains `query.rs`)
  - `file → function/class` (existing behavior)
- **qualified_name format:** `src/graph/` for directories (trailing slash distinguishes from files)
- **metadata on directory nodes:** `child_count`, `language_distribution`, `total_lines`
- **Impact analysis at directory level:** `get_impact_radius("src/indexer/")` shows all affected modules
- **Cluster-to-directory alignment:** When Leiden clusters map to physical directories, link them
- **Wake-up context:** L0/L1 lists top-level directories as "palace wings"
- **Folder-scoped search:** `search_code` and `query_file` accept directory qualified names

```
Palace Mapping:

  Wing (project area)     →  src/            [directory node]
    Room (module)         →  src/graph/      [directory node]
      Closet (file)       →  src/graph/query.rs  [file node]
        Drawer (element)  →  query.rs::GraphEngine  [function node]

  All connected by `contains` edges. Traversal = BFS from any directory.
```
</details>

### 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12)

> **Source:** Competitive analysis of [Graphify](https://github.com/Graphify-Labs/graphify) — AI coding-assistant skill that builds a queryable knowledge graph from code (tree-sitter, no LLM) plus optional docs/media. Key differentiators: `path` / `explain` / `query` agent verbs, every edge tagged `EXTRACTED|INFERRED|AMBIGUOUS`, god-node + surprising-connection reports, WHY/ADR rationale nodes, PR community conflict triage, and a work-memory reflect loop. Full matrix: `docs/analysis/graphify-comparison-2026-07-13.md`.
>
> **LeanKG keep / do not regress:** TOON/RTK token compression, requirement↔code traceability, microservice topology, severity-graded impact radius, CozoDB/RocksDB persistence, multi-project HTTP deploy.


> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter IDs for this section (`US-*` / related).


**Detailed Feature Descriptions:**

<details>
<summary>US-GF-01: Shortest Path</summary>

**Graphify inspiration:** `graphify path "FastAPI" "ModelField"` returns hop-by-hop edges with relation + confidence tags.

**LeanKG adaptation:**
- New MCP tool: `shortest_path(source, target, max_hops?)`
- New CLI: `leankg path <a> <b> [--max-hops N]`
- Resolve inputs by qualified_name, symbol name, or fuzzy label
- Return ordered hops: `{from, to, relation, confidence_label, source_file}`
- Prefer EXTRACTED edges when multiple equal-length paths exist
</details>

<details>
<summary>US-GF-02: Explain Node</summary>

**Graphify inspiration:** `graphify explain "APIRouter"` shows source, community, degree, and neighbor list.

**LeanKG adaptation:**
- New MCP tool: `explain_node(name_or_qn)`
- Aggregate: definition site, cluster membership, degree (in/out), top neighbors by relation type, importance/god-node rank if available
- Reuse `get_clusters`, dependents/dependencies, call graph — single agent-facing response
</details>

<details>
<summary>US-GF-03: NL Scoped Subgraph Query</summary>

**Graphify inspiration:** `graphify query "what connects auth to the database?"` returns a budgeted subgraph, not a full dump.

**LeanKG adaptation:**
- New MCP tool: `query_graph(question, token_budget?)`
- Pipeline: seed retrieval (keyword + optional embeddings) → bounded BFS/DFS expand → budget trim → TOON response
- Distinct from `orchestrate` (routing) and `kg_semantic_context` (embed pipeline): oriented to *connection* questions
- Surface confidence_label on every returned edge
</details>

<details>
<summary>US-GF-04: Edge Provenance Labels</summary>

**Graphify inspiration:** Every edge is `EXTRACTED` (explicit in source), `INFERRED` (resolver-derived), or `AMBIGUOUS` (needs review).

**LeanKG adaptation:**
- Map existing `resolution_method` (`name`, `name_file_hint`, `unresolved`, future `typed`) to provenance labels
- Store `confidence_label` on Relationship metadata; keep numeric confidence for impact severity
- Propagate labels through `get_impact_radius`, `get_call_graph`, `shortest_path`, `query_graph`, Web UI edge tooltips
</details>

<details>
<summary>US-GF-05: God-Node Ranking</summary>

**Graphify inspiration:** Report highlights most-connected concepts; optional hub exclusion for utilities.

**LeanKG adaptation:**
- Precompute degree / PageRank-like importance at index time (aligns with enhancement-analysis Priority 2)
- Expose via `get_architecture` hotspots and new `get_god_nodes(limit, exclude_hubs_percentile?)`
- CLI: `leankg gods [--limit N]`
</details>

<details>
<summary>US-GF-06: GRAPH_REPORT.md</summary>

**Graphify inspiration:** Three artifacts after build: `graph.html`, `GRAPH_REPORT.md`, `graph.json`.

**LeanKG adaptation:**
- On `index` / `leankg report`: write `.leankg/GRAPH_REPORT.md`
- Sections: god nodes, surprising cross-cluster edges, confidence distribution, 4–5 suggested agent questions
- Web UI link + MCP `get_graph_report`
</details>

<details>
<summary>US-GF-07: Rationale / WHY Nodes</summary>

**Graphify inspiration:** `# NOTE:` / `# WHY:` / `# HACK:` and ADR/RFC citations become first-class nodes linked to code.

**LeanKG adaptation:**
- Extractor pass for comment markers + markdown ADR/RFC links
- New element type: `rationale` with `explains` relationship to code elements
- Searchable via `search_code` / `search_annotations` / `query_graph`
</details>

<details>
<summary>US-GF-08: PR Impact Dashboard</summary>

**Graphify inspiration:** `graphify prs`, `--triage`, `--conflicts` (PRs sharing communities = merge-order risk).

**LeanKG adaptation:**
- CLI: `leankg prs [number] [--triage] [--conflicts]`
- MCP: `list_prs`, `get_pr_impact`, `triage_prs`
- Combine `detect_changes` + cluster membership of touched files
- Conflicts: PRs whose changed files share clusters
</details>

<details>
<summary>US-GF-09: Work-Memory Reflect Loop</summary>

**Graphify inspiration:** `save-result` + `reflect` → `LESSONS.md` and learning overlay that biases `explain` / `query`.

**LeanKG adaptation:**
- MCP: `report_query_outcome(question, nodes[], outcome: useful|dead_end|corrected)`
- Aggregate into `.leankg/reflections/LESSONS.md`
- Optional overlay tags on nodes: preferred / tentative / contested
- Feeds context-quality loop from enhancement-analysis Priority 6
</details>

<details>
<summary>US-GF-10..12: Coverage & Portability</summary>

**US-GF-10 Languages:** Prioritize high-demand gaps (Vue/Svelte/Astro, shell, Scala) before long-tail grammars.

**US-GF-11 Portable snapshot:** Export merge-friendly `graph-snapshot.json` (relative paths); optional git merge driver. Complements RocksDB deploy — does not replace it.

**US-GF-12 SQL schema:** Optional extractor for `.sql` + `leankg extract --postgres <dsn>` creating table/FK nodes linked to ORM/repository code when detectable.
</details>

### 3.11 CBM Structural Parity Stories (US-CBM) — merged from `prd-structural-parity-cbm.md`

> **Source:** Competitive analysis of [codebase-memory-mcp (CBM)](https://github.com/DeusData/codebase-memory-mcp) v0.9.0 vs LeanKG (current: v0.17.9). Deep comparison notes also in `docs/analysis/` historical stubs.
>
> **Product rule:** Lean into business-context depth (ontology, knowledge, env, Android, req↔code). Close structural gaps that erode agent trust. Do **not** chase Pure-C / 158-language parity.
>
> **Tracks:** A Activate · B Structural · C Platform · D Dual-run escape hatch · E 3D graph UI
>
> **Codebase status audit:** 2026-07-14 against `src/` (v0.17.9)

#### Positioning (summary)

| Dimension | LeanKG | CBM |
|-----------|--------|-----|
| Stack | Rust + CozoDB/RocksDB | Pure C + SQLite |
| MCP | 85 tools (current), stdio + HTTP/SSE + REST | ~14 tools, stdio |
| Strength | Ontology, knowledge, env/incidents, Android, Docker+RocksDB, RTK | Speed, 158 langs, Hybrid LSP, clones, CROSS_*, static binary |
| Call resolve today | `name` / `name_file_hint` / `unresolved` + confidence | Hybrid LSP Tier 1/2/3 |

#### User stories — Track A Activate


> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter IDs for this section (`US-*` / related).


#### User stories — Track B Structural


#### User stories — Track C Platform


#### User stories — Track D Dual-run


#### User stories — Track E 3D Visualization


<details>
<summary>US-CBM detailed notes (implementation evidence)</summary>

**DONE evidence (post-v3.6 audit, 2026-07-14):**
- `get_architecture` / `get_graph_schema` / `find_dead_code` — `src/mcp/tools.rs`, `src/mcp/handler.rs`, `src/graph/query.rs`
- `resolution_method` + `confidence` — `src/indexer/call_graph.rs`
- Routes + `http_calls` — `src/indexer/route_extractor.rs`, `RelationshipType::HttpCalls` in `src/db/models.rs`
- `wake_up` — `src/mcp/handler.rs` (also closes MemPalace US-MP-07)
- `LSP` module — `src/lsp/{bridge,client,config,mod}.rs`; `resolve_with_lsp` MCP; `leankg lsp-resolve` CLI; `IndexerConfig::typed_resolve` (`8971dc5`)
- Event edges `emits` / `listens_on` — `src/db/models.rs` + `25a3b37`
- Clones — `find_clones` MCP + `leankg clones` + `SimilarTo` (`55e6e72`); LSH path non-strategic / scheduled removal (v3.6.2)
- Cross-repo similar — `find_cross_repo_similar` MCP + `CrossRepoSimilar` (`ab16c9b`)
- Hot-path cache — `src/cache/hot_path.rs` + `836f0a3`
- Temporal graph fields — `src/db/models.rs` `valid_from` / `valid_to` (`bc9cc53`)
- Consistency checker — `check_consistency` MCP + `leankg check-consistency` (`60a6111`)
- Tunnels — `find_tunnels` MCP + `leankg tunnels` + `Tunnel` (`5b6547e`)
- Agent personas — `agent_focus` + `agent_diary_{read,write}` (`1ea4bcd`)
- Rationale extraction — `src/indexer/rationale_extractor.rs` (`b0c9477`)
- PR impact dashboard — `get_pr_impact` MCP + `leankg prs` (`30e41f0`)
- Work-memory reflect — `report_query_outcome` + `Lessons` aggregation (`373e808`)
- Portable snapshot — `export_graph_snapshot` MCP (`0087991`)
- Team / on-call — `get_team_map` MCP (`3368b5f`)
- Cluster SKILL — `get_cluster_skill` MCP (`10b15a0`)
- Overview context — `get_overview_context` MCP (`9124959`)
- CI/CD auto-update — `.github/workflows/leankg-graph-update.yml` (`eb3d331`)
- Vue + Svelte — `src/indexer/sfc.rs` (regex; **not called from index walk**) (`e617a49`)
- SQL DDL — `src/indexer/sql.rs` (**not called from index walk**) (`de314eb`)
- Swift — `src/indexer/swift.rs` (**not called from index walk**) (`7027d6b`)

**PENDING evidence:**
- No `typed` `resolution_method` produced at index time; LSP bridge returns `LspLocation[]` but does not yet write CALLS edges with `resolution_method=typed`
- No `graph-ui/` directory; no `get_graph_layout` / 3D scene
- No formal `resources/read` endpoint for `get_overview_context` (tool-only)
- Swift / Vue / Svelte / SQL extractors exist as modules but `.swift` / `.vue` / `.svelte` / `.sql` are absent from `find_files_sync`

**Won’t Have (this program):** Full 158-language parity; Pure-C rewrite; replace Cozo/RocksDB; full Hybrid LSP for all CBM families in one release; drop HTTP/SSE/REST or Docker team path; **custom MinHash/LSH or Cozo `::lsh` clone ANN** (v3.6.2 — semantic HNSW only).
</details>

### 3.12 Team Knowledge Infrastructure (US-V2) — merged from `prd-leankg.md` v2

> **Vision:** Evolve from local-first single-dev tool to shared knowledge backbone for multi-service teams: environment-scoped graph, incident knowledge, CI freshness, token-budgeted MCP tools.
>
> **Codebase status audit:** 2026-07-14 (v0.17.9)


> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter IDs for this section (`US-*` / related).


**v2 success metrics (targets):**

| Metric | Target |
|--------|--------|
| Full service context in Cursor | < 5s |
| Context tokens per session (LeanKG queries) | < 2,000 |
| Graph freshness after release hook | < 3 minutes |
| Env conflict catch rate (schema) | > 80% |

<details>
<summary>US-V2 data model (from HLD/PRD v2)</summary>

**Incident node fields:** id, env, title, severity (P0–P3), occurred_at, resolved_at, root_cause, resolution, affected services, trigger_pattern, prevention, tags, author, linked_ticket

**v2 edges:** `caused_incident`, `resolved_by`, `conflicts_with`, `deployed_to`, `supercedes`

**Service metadata:** version, deploy_env, slo_p99_ms, health_endpoint, on_call, incident_count, last_incident, tags
</details>

### 3.13 Optimized Local-First Vector Graph Engine (US-VE) — v3.7.0

> **Implementation focus: P0 (highest).** Core implementation **merged to `origin/main`** (`dbc22c4` / #79). Remaining open work is gate/KPI evidence — see tracker Focus=`P0`. Full ordered queue: [`prd-task-tracker.md`](prd-task-tracker.md).
>
> **Epic:** Replace mmap-heavy / opaque vector I/O with a **3-tier LocalEngine** (and CloudEngine twin) so semantic+LSP retrieval stays surgical under M2 Pro / 16GB / 256GB SSD, and scales to Linux x86_64 + TiKV without rewriting MCP/CLI callers.
>
> **Depends on:** FR-HNSW-* product path (semantic ANN UX). **Does not cancel** FR-HNSW-B until LocalEngine recall/latency gates pass and factory switch is default for Local mode.
>
> **Landed on main:** US-VE-03..07 + FR-VE-ABS / T1–T3 / RT-* / FS-* / TEST-* / HNSW prune (**DONE**). **FR-VE-BENCH-Q DONE** (1M ANN P95). **Still PARTIAL:** US-VE-01/02/08, FR-VE-BENCH-IO/RECALL/OOM/AB, FR-VE-GATE.

> **Tasks:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter Focus=`P0` / `US-VE-*` / `FR-VE-*`.


**Acceptance criteria (epic-level):**

- **Given** a warm LocalEngine index of ≥1M SQ8 chunks on reference M2 Pro, **When** an agent issues semantic retrieval, **Then** P95 end-to-end time-to-context JSON is &lt; 100ms and ANN-only P95 is &lt; 50ms.
- **Given** a 2GB cgroup limit, **When** the engine auto-tunes RocksDB block cache + rayon threads, **Then** the process is never OOM-killed during index+query smoke.
- **Given** Flat File append succeeds and process crashes before RocksDB offset commit, **When** the engine recovers, **Then** no dangling pointers remain and queries skip incomplete records.
- **Given** `LEANKG_VECTOR_ENGINE=local|cloud` (or equivalent), **When** the factory constructs storage, **Then** the correct backend enum variant is used (unit-asserted).

---

## 4. Implementation Status Summary

> **Implementation status:** see [`prd-task-tracker.md`](prd-task-tracker.md) — Summary counts + Active session (open work) + Master table.

## 5. Functional Requirements

### 5.1 Core Features (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.2 GitNexus Enhancements (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.3 AB Testing & Validation (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.4 RTK Compression (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.5 Infrastructure Features (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.6 MemPalace-Inspired Features

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.7 Massive Graph UI (DONE)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.8 Multi-Language Support

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.


> **Indexed vs module-only (audit 2026-07-15):** “DONE (indexed)” means the extension is in `find_files_sync` and extraction runs on index. “PARTIAL (unwired)” means an extractor module/tests exist but the extension is **not** scanned.

| Language | Extensions | Extractor Status | Parser / module |
|----------|-----------|-----------------|-----------------|
| Go | `.go` | DONE (indexed) | tree-sitter-go |
| TypeScript/JavaScript | `.ts`, `.tsx`, `.js`, `.jsx` | DONE (indexed) | tree-sitter-typescript |
| Python | `.py` | DONE (indexed) | tree-sitter-python |
| Rust | `.rs` | DONE (indexed) | tree-sitter-rust |
| Java | `.java` | DONE (indexed) | tree-sitter-java |
| Kotlin | `.kt`, `.kts` | DONE (indexed) + Android depth | tree-sitter-kotlin-ng |
| Dart | `.dart` | DONE (indexed) (`7ec6484`) | tree-sitter-dart |
| XML | `.xml` | DONE (indexed) (`92db9aa`) + Android | tree-sitter-xml / Android extractors |
| Terraform | `.tf` | DONE (indexed) | Custom extractor |
| CI/CD YAML | `.yml`, `.yaml` | DONE (indexed) | GitHub Actions, GitLab CI, Azure Pipelines |
| Markdown | `.md` | DONE (doc indexer) | pulldown-cmark |
| Swift | `.swift` | PARTIAL (unwired) — `src/indexer/swift.rs` (`7027d6b`) | regex stub |
| Vue (SFC) | `.vue` | PARTIAL (unwired) — `src/indexer/sfc.rs` (`e617a49`) | regex stub |
| Svelte (SFC) | `.svelte` | PARTIAL (unwired) — `src/indexer/sfc.rs` (`e617a49`) | regex stub |
| SQL DDL | `.sql` | PARTIAL (unwired) — `src/indexer/sql.rs` (`de314eb`) | regex stub |
| C/C++ | `.cpp`, `.cxx`, `.cc`, `.hpp`, `.h`, `.c` | PARTIAL — tree-sitter parser present; **not** in current `find_files_sync` extensions list | tree-sitter-cpp |
| C# | `.cs` | PARTIAL — parser present; **not** in current index walk | tree-sitter-c-sharp |
| Ruby | `.rb` | PARTIAL — parser present; **not** in current index walk | tree-sitter-ruby |
| PHP | `.php` | PARTIAL — parser present; **not** in current index walk | tree-sitter-php |

### 5.9 Graphify-Inspired Features

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.


> Evidence: `docs/analysis/graphify-comparison-2026-07-13.md`. Deploy parity with Graphify HTTP MCP is **not** a gap — LeanKG RocksDB multi-project compose is competitive. Focus requirements on agent query UX and edge honesty.


### 5.10 CBM Structural Parity Requirements (merged)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.


> Canonical FR IDs retained from `prd-structural-parity-cbm.md` (FR-A/B/C/D/E). Status audited 2026-07-14 (v0.17.9).

Tracks A–E (activate / structural / platform / dual-run / 3D UI): see tracker `FR-A*` / `FR-B*` / `FR-C*` / `FR-D*` / `FR-E*`.


### 5.11 Team Infrastructure / v2 Requirements (merged from `prd-leankg.md`)

> **FRs + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-*` for this section.



### 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + embed runtime (v3.6.3)

> **FR checklist + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-HNSW-*`, `FR-EMBED-*`, `FR-LSH-*`, `FR-BENCH-HNSW`.

> **Product bet:** LeanKG's strong path is **semantic search** via dense embeddings + CozoDB native `::hnsw`. Do **not** reimplement MinHash/LSH in-process, and do **not** wire Cozo `::lsh` for clones. Pattern already proven by embeddings: LeanKG builds text blobs → Cozo stores vectors + HNSW index.
>
> **Cold-embed reality (v3.6.3):** on mega-graphs, wall time is dominated by **ONNX embedding inference** (~170 vec/sec e2e → ~36 min for ~371k functions). Cozo/Redis writer-only paths are ~100k+ vec/sec. Do **not** treat storage migration (WAL-off, Redis, FalkorDB) as the primary cold-SLA lever.

**Policy (details + status in tracker):**
- Remove custom MinHash/LSH; keep Cozo `::hnsw` as shipped default until FR-VE-GATE
- MCP must not block on cold embed; day-2 incremental embed is the fast path
- **Won't Do:** Cozo `::lsh` for clones; migrate KG to FalkorDB/Redis to fix cold embed

### 5.13 LSP Adoption Track from CBM (moved from former 5.12; deep compare 2026-07-15)

> **FR checklist + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter `FR-LSP-A`..`FR-LSP-D`.

> LSP-only FRs retained from the CBM deep read. Clone/LSH FRs cancelled in Section 5.12.
>
> **Intent:** close the zero-setup gap (LeanKG currently requires user-configured LSP servers) via prefab `lsp:` block, optional in-process native resolver, indexer wiring for `resolution_method=typed`, and cross-file type registry.

### 5.14 Optimized Local-First Vector Graph Engine (v3.7.0)

> **Implementation focus: P0 (highest).** Core module on `origin/main` (#79 / `dbc22c4`).  
> **FR checklist + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter Focus=`P0` / `FR-VE-*` / `US-VE-*` / Kind=`Release` (§8.4).

> **Goal:** Ultra-lightweight vector/graph storage + retrieval that works under Apple M2 Pro / 16GB / 256GB SSD and scales to Linux x86_64 + TiKV **without rewriting** MCP/CLI semantic APIs.
>
> **Coexistence:** Until FR-VE-GATE is met, FR-HNSW-B (Cozo `::hnsw`) remains the **shipped default**. LocalEngine / CloudEngine are opt-in via `LEANKG_VECTOR_ENGINE=local|cloud` and must match recall/latency gates before becoming Local default (`ready_for_default` stays false in `src/vector_engine/gate.rs`).
>
> **Hardware envelope:** Local survival cap **2GB** (Docker/cgroup) → Cloud **50–80%** of available RAM. Prefer sequential append I/O; minimize random SSD writes.

#### 5.14.1 Decoupled 3-tier storage

- Tier 1: graph topology in RocksDB (Local) / TiKV (Cloud) — metadata, AST refs, HNSW adjacency; Local RocksDB: mmap off, pin L0 filter/index, BinaryAndHash, Zstd
- Tier 2: SQ8/INT8 vectors 100% in RAM for SIMD ANN (no disk I/O on inner loop)
- Tier 3: flat binary FP32 + source payload — read once at post-filter
- Abstraction: Rust traits + static enum dispatch (`LocalEngine` | `CloudEngine`)

#### 5.14.2 Dynamic runtime adaptation

- Runtime SIMD dispatch (AVX-512 / AVX2 / NEON / scalar) — never SIGILL
- Auto-tune RocksDB block cache from cgroups / sysinfo
- Dynamic rayon pool (leave 2 cores free Local; full machine Cloud)
- HNSW M ∈ [12, 16]; raise efConstruction; recall &gt; 90% at efSearch=50

#### 5.14.3 Flat file consistency & GC

- Dual-write order: Append → fsync → commit offsets → update RAM SQ8
- Crash recovery must leave no dangling pointers
- Zero-downtime GC (shadow paging + micro-lock delta) when fragmentation &gt; 30%

#### 5.14.4 Tests & benches (mandatory before default switch)

Agent A/B floors (also in NFR / tracker `FR-VE-BENCH-*`):

| Metric | Target |
|--------|--------|
| Token consumption | ≥ **60%** reduction vs grep/cat baseline (stretch **61%**) |
| Tool-call frequency | ≥ **80%** reduction (stretch **84%**, aim 1-hop context) |
| Time-to-resolution | ≥ **2×** faster |
| Task success rate | ≥ baseline |

**Won't Do (this track):**
- Reopen Redis/FalkorDB as cold-embed write accelerator (still Won't Do per v3.6.3)
- Require Cloud SaaS hosting (self-hosted TiKV/CloudEngine only)
- Rewrite MCP tool names/APIs for the engine swap

---

## 6. Technical Architecture / HLD

### 6.1 Technology Stack

| Component | Technology | Version |
|-----------|------------|---------|
| Core Language | Rust | 1.70+ (edition 2021) |
| Database | CozoDB (SQLite / RocksDB) + native `::hnsw` | 0.7.6 |
| Code Parsing | tree-sitter | 0.25 |
| MCP Server | rmcp (Rust MCP library) | 1.2 |
| CLI Framework | Clap | 4 |
| Web UI | Axum | 0.7 |
| Async Runtime | Tokio | 1 |
| File Watching | notify | 7 |
| Parallel Processing | rayon | 1.10 |
| Markdown Parsing | pulldown-cmark | 0.12 |
| Auth (API keys) | Argon2 | 0.5 |
| CORS | tower-http | 0.6 |

### 6.2 Data Model

```
CodeElement:
  - qualified_name: string (PK) - format: "path/to/file.rs::function_name" or "path/to/dir/" for directories
  - element_type: string - directory | file | function | class | import | export | pipeline | pipeline_stage | pipeline_step | terraform | cicd | document | doc_section
  - name: string
  - file_path: string
  - line_start: int
  - line_end: int
  - language: string
  - parent_qualified: string? (nullable)
  - cluster_id: string? (nullable)
  - cluster_label: string? (nullable)
  - metadata: JSON (includes signature, headings, ci_platform, child_count for directories, etc.)

Relationship:
  - source_qualified: string (FK)
  - target_qualified: string (FK)
  - rel_type: string - imports | calls | references | documented_by | tested_by | tests | contains | defines | implements | implementations | tunnel | decided_about
  - confidence: float (0.0-1.0)
  - metadata: JSON
  Indexes: rel_type_index, target_qualified_index

> **Folder-as-Graph Design (MemPalace-inspired):** Directories are first-class `directory` nodes in the graph. The `contains` edge is overloaded to represent the full hierarchy: `directory → directory`, `directory → file`, `file → function/class`. This mirrors MemPalace's wing → room → closet → drawer spatial architecture:
>
> | MemPalace | LeanKG | Edge |
> |-----------|--------|------|
> | Wing (project/person) | Top-level directory (`src/`, `docs/`) | `contains` |
> | Room (topic) | Sub-directory (`src/graph/`, `src/mcp/`) | `contains` |
> | Closet (summary) | File (`src/graph/query.rs`) | `contains` |
> | Drawer (verbatim) | Function/class within file | `contains` |
>
> Benefits:
> - **Impact analysis at directory level:** "What modules are affected if I change anything in `src/indexer/`?"
> - **Cluster-to-directory alignment:** Auto-detect when a Leiden cluster maps to a physical directory
> - **Wake-up context includes module map:** L0/L1 can list top-level directories as the "palace wings"
> - **Tunnel edges between directories:** Link `src/auth/` and `src/middleware/` when they share domain concepts
> - **Folder search:** `query_file` and `search_code` can scope to directory nodes

BusinessLogic:
  - element_qualified: string (PK, FK)
  - description: string
  - user_story_id: string? (nullable)
  - feature_id: string? (nullable)

ContextMetric:
  - tool_name: string (indexed)
  - timestamp: int (indexed)
  - project_path: string (indexed)
  - input_tokens: int
  - output_tokens: int
  - output_elements: int
  - execution_time_ms: int
  - baseline_tokens: int
  - baseline_lines_scanned: int
  - tokens_saved: int
  - savings_percent: float
  - (+ optional fields: correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted)

QueryCache:
  - cache_key: string (unique)
  - value_json: string
  - created_at: int
  - ttl_seconds: int
  - tool_name: string
  - project_path: string
  - metadata: JSON

ApiKey:
  - id: string (UUID)
  - name: string
  - key_hash: string (Argon2)
  - created_at: int
  - last_used_at: int?
  - revoked_at: int?
```

### 6.3 Module Map

```
src/
├── main.rs              # CLI entry point (30+ commands; includes lsp_resolve, check_consistency, tunnels, prs, clones, reflect)
├── lib.rs               # Library exports (registers modules below)
├── cli/                 # Clap command enum + ShellRunner
├── config/              # ProjectConfig, IndexerConfig (typed_resolve flag), DocConfig, McpConfig
├── db/                  # CozoDB models, schema, operations, API key store, valid_from/valid_to fields
├── doc/                 # DocGenerator, template rendering, wiki generation
├── doc_indexer/         # Documentation indexing (docs/ → documented_by edges)
├── graph/               # GraphEngine, queries, context, traversal, clustering, cache (incl. hot-path cache), export (HTML/SVG/GraphML/Neo4j/snapshot), clones, tunnels
├── indexer/             # tree-sitter parsers (17), extractors (incl. dart/swift/xml/vue/svelte/sql_ddl/rationale/routes), git analysis, Terraform, CI/CD
├── lsp/                 # NEW — generic LSP bridge (bridge.rs, client.rs, config.rs, mod.rs); per-(language, workspace) client cache
├── mcp/                 # MCP tools (85), handler (resolve_with_lsp + agent_focus + diary + …), server (rmcp), auth, write tracker
├── orchestrator/        # Query orchestration with intent parsing and persistent cache
├── compress/            # RTK-style compression: 8 read modes, response/shell/cargo/git compressors, entropy analysis
├── web/                 # Axum web UI (20+ routes, embedded HTML/CSS/JS)
├── api/                 # REST API handlers, auth middleware
├── watcher/             # notify-based file watcher for auto-indexing
├── hooks/               # Git hooks (pre-commit, post-commit, post-checkout, GitWatcher)
├── benchmark/           # Benchmark runner (LeanKG vs OpenCode/Gemini/Kilo)
├── ontology/            # Concept + procedural ontology (concepts.yaml, workflows.yaml) — kg_* tools
├── embeddings/          # Semantic embeddings → CozoDB `embedding_vectors` + `::hnsw` (feature-gated; product focus)
├── retrieval/           # embed → HNSW ANN → rerank → graph traverse
├── embed.rs             # Legacy/compat embedding wrappers (prefer `embeddings/`)
├── budget.rs            # Per-tool token / RSS / wall-clock budget enforcement
├── gc.rs                # MemoryGuard for long-running MCP daemons
├── obsidian/            # Obsidian-vault doc adapter
├── registry.rs          # Global repository registry (multi-repo management)
└── runtime.rs           # Tokio runtime utilities
```

### 6.4 HLD — System Overview (merged from `hld-leankg.md`)

```
+-----------------------------------------------------+
|                   LeanKG Backend                    |
|            (Axum + CozoDB / RocksDB)                |
|                                                     |
|  +--------------+  +--------------+  +----------+  |
|  |  production  |  |   staging    |  |  local   |  |
|  |  namespace   |  |  namespace   |  |namespace |  |
|  +--------------+  +--------------+  +----------+  |
|                                                     |
|  +-----------------------------------------------+  |
|  |  CozoDB (Datalog) + HNSW embeddings (semantic ANN focus)  |  |
|  +-----------------------------------------------+  |
+-----------------------------------------------------+
         ^                    ^                ^
         |                    |                |
  +------+------+    +--------+-----+   +------+------+
  |  MCP server |    |  REST API    |   |  Web UI     |
  |  stdio/HTTP |    |  /api/...    |   |  2D (+3D E) |
  +------+------+    +--------+-----+   +-------------+
         |                    |
  +------+------+    +--------+---------------------+
  | AI assistants|   | CI/CD hooks / GitHub Actions |
  +--------------+   +------------------------------+
```

### 6.5 HLD — Component Design

**Data layer:** `env` on `code_elements` / `relationships`; incidents + service metadata tables; all queries filter by env (default `local`).

**Graph engine tools (v2):** `get_service_context`, `find_env_conflicts`, `query_incidents`, env-aware impact.

**MCP auth headers (HTTP):** `X-LeanKG-Token` / Bearer; optional engineer + env headers.

**CLI (v2):** `leankg incident add|list|show`, `leankg update`, note/pattern/env helpers as implemented.

**Vacuum scheduler:** tokio task; `LEANKG_VACUUM_INTERVAL_HOURS` (default 1, `0` disables); Sqlite VACUUM; RocksDB debug no-op; invalidate caches after success.

**Ontology self-test:** `kg_self_test` + HTTP startup WARN (non-gating) for arity/schema drift on `kg_*` tools.

### 6.6 HLD — Key Data Flows

**Incident contribution:** CLI/API → validate Incident → CozoDB → available to MCP queries.

**Env conflicts:** fetch service across envs → compare schema/config/endpoints/deploy → risk HIGH/MEDIUM/LOW.

**Vacuum:** boot → spawn loop → vacuum → log → sleep.

**kg_self_test:** bind HTTP → run OntologyQueryEngine::self_test → info if OK, warn per failure → still serve.

### 6.7 HLD — Implementation Phases (v2)

| Phase | Scope | Status |
|-------|-------|--------|
| 1 Data model & schema (`env`, Incident) | Schema | DONE |
| 2 Graph engine env/incident queries | Engine | DONE |
| 3 MCP tools + token budgets | MCP | DONE |
| 4 CLI incident/update | CLI | DONE |
| 5 Integration tests / CI template | Test/Docs | PARTIAL |

### 6.8 HLD — Interface Sketches

`query_incidents` input: `{service, pattern?, env, limit}` → incidents[].  
`find_env_conflicts` input: `{service}` → conflicts[{type, detail, risk}].  
`leankg incident add --title … --severity P1 --affected svc --env production`.

### 6.9 HLD — Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Schema migration breaks v1 data | High | Default `env=local` for existing rows |
| Token budgets too tight | Medium | Configurable budgets + TOON |
| Scale to large multi-service graphs | High | RocksDB, caches, pagination |
| Concurrent writes | Medium | Cozo transactions |
| Unbounded DB growth | Medium | Hourly vacuum |
| Ontology arity drift → MCP -32603 | High | `kg_self_test` startup WARN |
| LocalEngine dual-write crash leaves dangling offsets | High | Append → fsync → Rocks commit → RAM; recovery skips incomplete (FR-VE-FS-*) |
| SIMD path SIGILL on older CPUs | High | Runtime feature detect + scalar fallback (FR-VE-RT-SIMD) |
| 2GB cgroup OOM during ANN warm | High | Auto-tune block cache + SQ8-only hot path (FR-VE-RT-MEM / BENCH-OOM) |
| Premature Cozo→LocalEngine default switch | Medium | Hard FR-VE-GATE before changing FR-HNSW-B default |

### 6.10 HLD — Optimized Local-First Vector Graph Engine (v3.7.0)

```
                    +---------------------------------------------+
                    |     Retrieval API (unchanged MCP/CLI)       |
                    |  semantic_search / kg_semantic_context / …  |
                    +----------------------+----------------------+
                                           |
                    +----------------------v----------------------+
                    |   Storage Factory (env / .env / leankg.yaml)|
                    |   LocalEngine  |  CloudEngine (static enum) |
                    +----------+------------------+---------------+
                               |                  |
              Local (ARM64/x86) |                  | Cloud (x86_64)
                               v                  v
         +---------------------+----+    +--------+------------------+
         | Tier 1 RocksDB           |    | Tier 1 TiKV               |
         | metadata + HNSW adj      |    | metadata + HNSW adj       |
         | mmap OFF, Zstd, pin L0   |    | distributed KV            |
         +------------+-------------+    +--------+------------------+
                      |                           |
         +------------v-------------+    +--------v------------------+
         | Tier 2 SQ8/INT8 in RAM   |    | Tier 2 SQ8/INT8 in RAM    |
         | SIMD: NEON/AVX2/AVX-512  |    | SIMD + full-core rayon    |
         | (leave 2 cores Local)    |    | (use 50-80% RAM)          |
         +------------+-------------+    +--------+------------------+
                      |                           |
         +------------v-------------+    +--------v------------------+
         | Tier 3 Flat binary       |    | Tier 3 Flat / object store|
         | FP32 + source payload    |    | FP32 + source payload     |
         | post-filter read once    |    | post-filter read once     |
         +--------------------------+    +---------------------------+

Dual-write: Flat append → fsync → Tier1 offsets → Tier2 RAM update
GC: shadow page + delta sync when fragmentation > 30% (readers unblocked)
```

**Dynamic adaptation:** cgroups/`sysinfo` → RocksDB block cache; runtime CPU feature detect → SIMD lane; Local leaves 2 cores free.

**Migration:** Cozo `embedding_vectors:vec_idx` remains default until FR-VE-GATE; optional dual-run / shadow compare for recall before cutover.

---

## 7. MCP Tools (85 total — audited 2026-07-14 against `src/mcp/tools.rs` v0.17.9)

### Project Management (5)
| Tool | Description |
|------|-------------|
| `mcp_init` | Initialize LeanKG project |
| `mcp_index` | Index codebase |
| `mcp_index_docs` | Index docs directory |
| `mcp_install` | Create .mcp.json |
| `mcp_status` | Show index status |

### Impact & Dependency (6)
| Tool | Description |
|------|-------------|
| `mcp_impact` | Calculate blast radius |
| `get_impact_radius` | Affected files within N hops with confidence/severity |
| `detect_changes` | Pre-commit risk analysis |
| `get_dependencies` | Direct imports of a file |
| `get_dependents` | Files depending on target |
| `get_review_context` | Focused subgraph + review prompt |

### Code Search (7)
| Tool | Description |
|------|-------------|
| `search_code` | Search by name/type |
| `find_function` | Locate function definition |
| `query_file` | Find file by pattern |
| `get_callers` | Find callers of a function |
| `get_call_graph` | Bounded call chain |
| `get_code_tree` | Codebase structure |
| `find_large_functions` | Oversized functions by line count |

### Context & Compression (3)
| Tool | Description |
|------|-------------|
| `get_context` | AI-optimized file context |
| `ctx_read` | Read file with 8 compression modes |
| `orchestrate` | Smart query routing with cache |

### Testing & Docs (7)
| Tool | Description |
|------|-------------|
| `get_tested_by` | Test coverage info |
| `get_doc_for_file` | Docs referencing code element |
| `get_files_for_doc` | Code elements in a doc |
| `get_doc_structure` | Documentation directory structure |
| `get_doc_tree` | Doc tree with hierarchy |
| `generate_doc` | Generate documentation |
| `find_related_docs` | Docs related to code change |

### Traceability (2)
| Tool | Description |
|------|-------------|
| `get_traceability` | Full traceability chain |
| `search_by_requirement` | Code for a requirement |

### Clustering & Graph (3)
| Tool | Description |
|------|-------------|
| `get_clusters` | Functional communities |
| `get_cluster_context` | Cluster symbols and dependencies |
| `generate_graph_report` | Comprehensive graph analysis |

### Export & Utility (2)
| Tool | Description |
|------|-------------|
| `export_graph` | Export in json/html/svg/graphml/neo4j |
| `mcp_hello` | Health check / debug |

### 7.5 TOON Response Templates

All MCP tool responses use TOON (Token-Oriented Object Notation) format by default for ~40% token reduction. See [TOON Specification](https://github.com/toon-format/toon) for details.

**Response Format Envelope:**
```
{
  status: ok|error
  tool: <tool_name>
  format: toon|json
  tokens: <token_count>
  data: <response_data>
}
```

**TOON Format Examples:**

1. **Search/Query Results:**
```
{
  status: ok
  tool: search_code
  format: toon
  tokens: 156
  data:
    results[3]{qualified_name,type,language}:
      src/main.rs::main,function,rust
      src/lib.rs::init,function,rust
      src/cli.rs::run,function,rust
}
```

2. **Impact Radius:**
```
{
  status: ok
  tool: get_impact_radius
  format: toon
  tokens: 203
  data:
    impact[4]{qualified_name,type,severity,confidence}:
      src/main.rs::main,function,WILL_BREAK,1.0
      src/lib.rs::init,function,LIKELY_AFFECTED,0.85
      src/config.rs::load,function,LIKELY_AFFECTED,0.72
      tests/main_test.rs::test_main,test,MAY_BE_AFFECTED,0.31
}
```

3. **Dependencies/Dependents:**
```
{
  status: ok
  tool: get_dependencies
  format: toon
  tokens: 98
  data:
    dependencies[2]{qualified_name,type}:
      src/lib.rs,file
      src/config.rs,file
}
```

4. **Call Graph:**
```
{
  status: ok
  tool: get_call_graph
  format: toon
  tokens: 187
  data:
    calls[3]{from,to,depth}:
      src/main.rs::main,src/lib.rs::init,1
      src/main.rs::main,src/cli.rs::run,1
      src/lib.rs::init,src/config.rs::load,2
}
```

5. **Context/Compression:**
```
{
  status: ok
  tool: get_context
  format: toon
  tokens: 412
  data:
    context:
      sig[1]: src/main.rs::main->()
      imports[2]: src/lib.rs,src/config.rs
      calls[1]: src/lib.rs::init
}
```

6. **Cluster/Graph Data:**
```
{
  status: ok
  tool: get_clusters
  format: toon
  tokens: 234
  data:
    clusters[2]{id,name,members}:
      c1,mcp_tools,12
      c2,graph_core,8
}
```

**JSON Fallback:** Clients can request JSON format by adding `format=json` parameter to any MCP tool call.

---

## 8. Release Criteria

> **Release checklist + status:** [`prd-task-tracker.md`](prd-task-tracker.md) — filter Kind=`Release` or section 8.* / FR-VE-GATE.

## 9. Non-Functional Requirements

| Metric | Target | Status |
|--------|--------|--------|
| Cold start time | < 2 seconds | TBD |
| Indexing speed | > 10,000 lines/second (parallel via rayon) | TBD |
| Time-to-context (chunks + deps JSON) P95 | **&lt; 100ms** | DONE (US-VE-02 — ANN+JSON P95≈0.094ms) |
| ANN query P95 (1M SQ8, Local) | **&lt; 50ms** | DONE (FR-VE-BENCH-Q — 1M ANN P95=0.065ms Neon, 2026-07-17) |
| Query response time (legacy general) | < 100ms | TBD |
| Memory usage (idle MCP) | **&lt; 150MB** (was 100MB aspirational) | DONE (US-VE-01 — warm SQ8 path RSS≈89MB) |
| Memory usage (indexing) | < 500MB typical; Cloud may use 50–80% RAM for SQ8 | TBD |
| Survival under cgroup | **2GB hard** — never OOM-killed | DONE (FR-VE-BENCH-OOM — plan + live 1M RSS≈567MB) |
| Disk I/O vs legacy mmap | ≥ **80%** fewer page faults / disk reads | DONE (FR-VE-BENCH-IO) |
| HNSW recall @ efSearch=50 vs FP32 BF | **&gt; 90%** | DONE (FR-VE-BENCH-RECALL — SQ8≥90% @ ef=50) |
| Agent token savings vs grep/cat | ≥ **60%** (stretch 61%) | PARTIAL (FR-VE-BENCH-AB — in-process suite pass; live harness open) |
| Agent tool-call reduction vs baseline | ≥ **80%** (stretch 84%) | PARTIAL (FR-VE-BENCH-AB) |
| Agent time-to-resolution | ≥ **2×** faster | PARTIAL (FR-VE-BENCH-AB) |
| Agent task success rate | ≥ baseline | PARTIAL (FR-VE-BENCH-AB) |
| detect_changes response time | < 2 seconds | TBD |
| get_context enhanced response size | < 4000 tokens | TBD |
| Batch insert size | 5000 rows/batch | DONE |
| Supported parser / extractor count | Tree-sitter + specialized extractors; **indexed walk ≈ 8 code langs + Android/XML/TF/CI** (Swift/Vue/Svelte/SQL modules unwired) | PARTIAL |
| MCP tool count | 85 tools (`src/mcp/tools.rs`) | DONE (audited 2026-07-14; still 85 on v0.19.0) |
| Cross-platform | Apple Silicon (ARM64) Local + Linux x86_64 Cloud | PARTIAL (FR-VE-ABS DONE; CloudEngine TiKV Tier-1 still stub root) |

---

## 10. Out of Scope

1. **Full multi-modal PDF/image/video graph ingest (Graphify-style)** - Code + docs + infra first
2. **Cloud SaaS hosting of LeanKG** - Self-hosted only (team HTTP MCP / RocksDB / **self-hosted TiKV CloudEngine** is in scope; managed multi-tenant SaaS is not)
3. **Multi-user collaborative editing of the graph** - Single writer per project DB; shared read via HTTP MCP is OK
4. **Plugin system** - Future consideration
5. **Raw Datalog query passthrough** - Security risk (except controlled `run_raw_query`)
6. **Replacing CozoDB/RocksDB with NetworkX-only primary store** - Snapshot export is additive
7. **Full 158-language / Pure-C rewrite (CBM chase)** - Selective languages only
8. **Split PRD/HLD documents** - This file is the only SoT for narrative/HLD; do not recreate `docs/requirement/prd-*.md` or `docs/design/hld-leankg.md`. Task lists/status live only in [`prd-task-tracker.md`](prd-task-tracker.md)
9. **Status tables / FR checkboxes inside this PRD** - Forbidden; use the tracker
10. **Redis/FalkorDB as cold-embed write accelerator** - Rejected v3.6.3; not revived by v3.7 vector engine
11. **Default cutover from Cozo HNSW before FR-VE-GATE** - Explicitly forbidden

---

## 11. Glossary

| Term | Definition |
|------|------------|
| Knowledge Graph | Graph structure storing entities and relationships from codebase |
| Code Indexing | Process of parsing code and extracting structural information |
| MCP Server | Model Context Protocol server for AI tool integration (rmcp) |
| Context Window | AI model's input capacity; LeanKG minimizes tokens needed |
| Business Logic Mapping | Linking code to business requirements |
| Qualified Name | Natural node identifier: `file_path::parent::name` format |
| Blast Radius / Impact Radius | All files affected by a change within N hops |
| Confidence Score | Float 0.0-1.0 indicating edge reliability |
| Confidence Label | EXTRACTED / INFERRED / AMBIGUOUS provenance |
| Severity Classification | WILL BREAK / LIKELY AFFECTED / MAY BE AFFECTED |
| Cluster | Functional community (Leiden) |
| God Node | High-degree hub concept |
| Environment Namespace | `local` / `staging` / `production` / `upcoming` partition of graph data |
| Incident Node | Structured outage/knowledge record linked to services |
| Vacuum Scheduler | Periodic SQLite VACUUM on long-lived MCP servers |
| RTK | Rust Token Killer — compression reducing LLM tokens |
| Orchestrator | Intent parsing + persistent cache |
| Global Registry | Multi-repo management for cross-project queries |
| Temporal Graph | Relationships with valid_from/valid_to |
| Wake-up Protocol | Minimal L0+L1 context at session start |
| HLD | High-Level Design — architecture and flows in Section 6.4–6.10 |
| LocalEngine | 3-tier local vector/graph backend (RocksDB + SQ8 RAM + flat payload) |
| CloudEngine | Same API as LocalEngine backed by TiKV (and cloud-scale RAM) |
| SQ8 / INT8 quantization | Down-casted vectors kept fully in RAM for SIMD ANN |
| Flat Payload File | Tier-3 append-only binary storing FP32 + source for post-filter |
| Dual-Write | Append → fsync → commit offsets → update RAM (crash-safe order) |
| FR-VE-GATE | Quality gate required before replacing Cozo HNSW as Local default |

---

## 12. References

- CozoDB: https://github.com/cozodb/cozo
- tree-sitter: https://tree-sitter.github.io/tree-sitter/
- MCP Protocol: https://modelcontextprotocol.io/
- rmcp: https://crates.io/crates/rmcp
- Leiden Algorithm: https://en.wikipedia.org/wiki/Leiden_algorithm
- MemPalace: https://github.com/milla-jovovich/mempalace
- Graphify: https://github.com/Graphify-Labs/graphify — `docs/analysis/graphify-comparison-2026-07-13.md`
- codebase-memory-mcp: https://github.com/DeusData/codebase-memory-mcp — see Section 3.11 / 5.10
- Context enhancement analysis: `docs/analysis/enhancement-analysis-2026-07-09.md`
- Roadmap: `docs/roadmap.md`
- MCP tool reference: `docs/mcp-tools.md`
- CLI reference: `docs/cli-reference.md`
- Embed store how-it-works: `generated_docs/embed_store_how_it_works_2026-07-16.md`
- **Task tracker (all US/FR/Release + status):** [`docs/prd-task-tracker.md`](prd-task-tracker.md) / [`prd-task-tracker.json`](prd-task-tracker.json)

---

*Last updated: 2026-07-17 (v3.7.0 — status synced to origin/main @ dbc22c4 / #79 / crate 0.19.0; tracker is SoT for Done/Partial)*
