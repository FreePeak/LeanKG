# LeanKG PRD Task Tracker (Single Session)

**Last synced:** 2026-07-18 — PR [#81](https://github.com/FreePeak/LeanKG/pull/81) embed-resume + SEM filter + mega-graph ops + Docker PID1 embed.lock fix  
**This file is the SoT for task inventory + status.**  
**PRD narrative / ACs / HLD:** [`docs/prd.md`](prd.md)  

> **Agent rule:** Implement in **Focus** order: **P0 → P1 → P2 → P3**.  
> **P0 embed-resume:** core + Docker evidence **DONE**.  
> **P1 this pass:** FR-SEM-06 / FR-MG-AUTO-01 / FR-OPS-EMBED-CPU **DONE**. Next: remaining P1 Must Have (LSP, etc.).  
> Open `prd.md` only for design narrative and acceptance criteria.

---

## Focus / priority legend

| Focus | Meaning | When to work |
|------:|---------|--------------|
| **P0** | Day-2 Embed Resume | **DONE** on PR #81 |
| **P1** | Other Must Have (SEM path filter + mega-graph ops landed; LSP/etc. remain) | Next open Must Have |
| **P2** | Should Have | Next |
| **P3** | Could Have / aspirational `OPEN` | Backlog |

## Status legend

| Status | Meaning |
|--------|---------|
| `DONE` | Implemented and accepted |
| `PARTIAL` | Some acceptance criteria met; remainder open |
| `PENDING` | Not started / blocked (usually user stories) |
| `NOT_DONE` | FR / release item still open |
| `OPEN` | Aspirational stretch |
| `WONT_DO` | Explicitly cancelled |

---

## Summary counts

| Metric | Count |
|--------|------:|
| **Total tracked** | **411** |
| NOT_DONE | 58 |
| PENDING | 16 |
| PARTIAL | 12 |
| OPEN | 1 |
| DONE | 321 |
| WONT_DO | 3 |
| Open work | **87** |

| Open by Focus | Count |
|---------------|------:|
| P0 | 0 |
| P1 | 23 |
| P2 | 57 |
| P3 | 7 |

| Kind | Count |
|------|------:|
| FR | 214 |
| Release | 52 |
| User Story | 145 |

---

## Active session — open work (sorted by priority)

> **2026-07-18 — closed on PR #81:** embed-resume day-2; SEM path filter (Probes G/H); `LEANKG_SKIP_FRESHNESS_CHECK`; compose **cpus 6 / mem_reservation 3g / MCP mem_limit 6g**; 3-workspace vector counts (3,271 / 146,977 / 14,110) + semantic_search OK.

| Focus | ID | Kind | Status | Priority | Title | PRD § |
|------:|----|------|--------|----------|-------|-------|
| **P1** | `US-08` | User Story | **PARTIAL** | Must Have | Multi-language support (Go, TS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP) | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-CBM-A2` | User Story | **PARTIAL** | Must Have | Ontology online ('kg_ontology_status', 'concept_search' non-empty after sync) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B1` | User Story | **PARTIAL** | Must Have | Typed call resolution Go + TypeScript MVP ('resolution_method=typed') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-GF-04` | User Story | **PARTIAL** | Must Have | Edge provenance labels 'EXTRACTED' / 'INFERRED' / 'AMBIGUOUS' on all relationships (unify… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `US-MG-02` | User Story | **PARTIAL** | Must Have | Single-repo projects expand fully on service double-click (no multi-level drilling) | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MP-02` | User Story | **PARTIAL** | Must Have | Layered Context Loading (L0-L3) — explicit token budgets per layer: L0 identity (~50 tok)… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P1** | `US-MP-08` | User Story | **PARTIAL** | Must Have | Folder Structure as Graph Edges — directories as first-class 'directory' nodes with 'cont… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P1** | `US-CBM-A1` | User Story | **PENDING** | Must Have | Correct MCP 'project' routing (multi-mount ≠ wrong RocksDB project) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-A4` | User Story | **PENDING** | Must Have | Moat smoke (ontology + routing) gates Phase 1 “done” | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-D3` | User Story | **PENDING** | Must Have | Re-evaluate dual-run after typed-resolve Phase | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-GF-03` | User Story | **PENDING** | Must Have | Natural-language scoped subgraph query ('query_graph "what connects auth to DB?"') | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `FR-B05` | FR | **NOT_DONE** | Must Have | Benchmark harness vs CBM (50-edge samples) | 5.10 CBM Structural Parity Requirements (merged) |
| **P1** | `FR-LSP-A` | FR | **NOT_DONE** | Must Have | LeanKG-native Hybrid LSP tier — an **in-process, no-spawn type resolver** for Go / TypeSc… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-B` | FR | **NOT_DONE** | Must Have | Prefab 'lsp:' block — 'leankg init --with-lsp' writes a default block listing 'gopls' / '… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-C` | FR | **NOT_DONE** | Must Have | Wire 'resolve_with_lsp' results into the indexer — when 'typed_resolve=go,ts' (or 'all') … | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-D` | FR | **NOT_DONE** | Must Have | Cross-file type registry shared across files in the same project (mirror 'internal/cbm/ls… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-MG-03` | FR | **NOT_DONE** | Must Have | Single-repo projects treated as single service — root double-click loads everything | 5.7 Massive Graph UI (DONE) |
| **P1** | `REL-032` | Release | **NOT_DONE** | Must Have | Swift / Vue / Svelte / SQL DDL — extractors present ('swift.rs' / 'sfc.rs' / 'sql.rs') bu… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-039` | Release | **NOT_DONE** | Must Have | Default LSP server bootstrap (FR-LSP-B / FR-B09 fanout — gopls + tsserver + pyright + dar… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-040` | Release | **NOT_DONE** | Must Have | REST API auth wiring + mutation endpoints (mutation endpoints still partial) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-041` | Release | **NOT_DONE** | Must Have | 3D graph UI Track E ('graph-ui/'; keep 2D 'ui/') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-042` | Release | **NOT_DONE** | Must Have | US-GF-03 NL scoped subgraph ('query_graph') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-043` | Release | **NOT_DONE** | Must Have | US-GF-04 provenance labels on all relationship types | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P2** | `US-GF-06` | User Story | **PARTIAL** | Should Have | Generate 'GRAPH_REPORT.md': god nodes, surprising cross-module links, suggested questions… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P2** | `US-LANG-02` | User Story | **PARTIAL** | Should Have | Swift parser (tree-sitter-swift) with regex entity extraction | 3.7 Additional Language Stories (US-LANG-01 to US-LANG… |
| **P2** | `US-CBM-B12` | User Story | **PENDING** | Should Have | ≥10 'run_raw_query' recipes in skills/docs | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-C3` | User Story | **PENDING** | Should Have | Selective language expansion with quality tiers | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E1` | User Story | **PENDING** | Should Have | New 3D graph UI ('graph-ui/') with WebGL galaxy + Bloom (keep existing 2D 'ui/') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E2` | User Story | **PENDING** | Should Have | Server-computed 3D layout in Rust + 'get_graph_layout' / '/api/graph' | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E3` | User Story | **PENDING** | Should Have | Adaptive rendering (InstancedMesh &lt;75k; point sprites above) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E4` | User Story | **PENDING** | Should Have | Node detail + edge-type filter panels | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-MP-03` | User Story | **PENDING** | Should Have | Conversation/Decision Mining — import Claude/ChatGPT/Slack transcripts; auto-extract deci… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P2** | `US-SEM-01` | User Story | **PENDING** | Should Have | Honest token accounting on truncated MCP payloads (delivered vs _token_budget.actual) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `US-SEM-02` | User Story | **PENDING** | Should Have | Adequate per-tool budgets for concept_search / kg_semantic_context (not default 1000) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `US-SEM-03` | User Story | **PENDING** | Should Have | Resilient MCP HTTP for long semantic calls (transient socket drop retry) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `FR-A01` | FR | **NOT_DONE** | Should Have | MCP 'project' resolves to correct RocksDB project for multi-mount setups | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A02` | FR | **NOT_DONE** | Should Have | Automate/document ontology sync for concepts + workflows YAML | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A03` | FR | **NOT_DONE** | Should Have | Verify ontology/knowledge tools after sync | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A04` | FR | **NOT_DONE** | Should Have | Index per 'leankg.yaml'; expose counts | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A06` | FR | **NOT_DONE** | Should Have | Smoke: ontology + routing must pass before Phase 1 “fully done” | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B06` | FR | **NOT_DONE** | Should Have | Python + Rust typed resolve (Could) — infra works; LSP server default wiring PENDING | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B13` | FR | **NOT_DONE** | Should Have | Extend 'service_calls' beyond k8s DNS regex (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B16` | FR | **NOT_DONE** | Should Have | Runtime trace ingestion (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B40..B44` | FR | **NOT_DONE** | Should Have | IaC Resource/Module, ADR, snapshot (subset done), DATA_FLOWS (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B50` | FR | **NOT_DONE** | Should Have | ≥ 10 'run_raw_query' recipes (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B51` | FR | **NOT_DONE** | Should Have | Optional openCypher→Cozo subset (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C02` | FR | **NOT_DONE** | Should Have | Document smaller-model / batch-size options (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C04` | FR | **NOT_DONE** | Should Have | Profile impact-radius latency (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C05` | FR | **NOT_DONE** | Should Have | Incremental languages with tier notes (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C06` | FR | **NOT_DONE** | Should Have | Per-language quality tier template; Go/TS first (Must Go/TS) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C07` | FR | **NOT_DONE** | Should Have | Large-repo benchmark ≥ 1M nodes or documented ceiling (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C08..C11` | FR | **NOT_DONE** | Should Have | Windows, pkg channel, SLSA, install targets (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-D04` | FR | **NOT_DONE** | Should Have | Re-evaluate dual-run after Phase 3 typed resolve | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E01..E05` | FR | **NOT_DONE** | Should Have | Vite/React/R3F/shadcn stack in 'graph-ui/' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E10..E14` | FR | **NOT_DONE** | Should Have | Rust 3D layout + 'get_graph_layout' / '/api/graph' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E20..E28` | FR | **NOT_DONE** | Should Have | R3F scene, Bloom, adaptive LOD, edge colors | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E30..E36` | FR | **NOT_DONE** | Should Have | Detail/filter panels, settings, multi-repo galaxies | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E40..E43` | FR | **NOT_DONE** | Should Have | HTTP integration; embed or static serve; keep 2D 'ui/' untouched | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-GF-05` | FR | **NOT_DONE** | Should Have | MCP tool 'query_graph(question, token_budget?)' — seed → expand → budget trim → TOON | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-06` | FR | **NOT_DONE** | Should Have | CLI 'leankg query "<question>"' wrapping FR-GF-05 (note: existing 'leankg query' is name/… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-07` | FR | **NOT_DONE** | Should Have | Relationship metadata field 'confidence_label' ∈ {EXTRACTED, INFERRED, AMBIGUOUS} written… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-08` | FR | **NOT_DONE** | Should Have | Map 'resolution_method' → 'confidence_label' at edge write time; backfill on reindex | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-09` | FR | **NOT_DONE** | Should Have | Propagate 'confidence_label' in impact, call_graph, path, query_graph, Web UI | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-10` | FR | **NOT_DONE** | Should Have | Index-time god-node / importance scoring (degree + optional PageRank-like) | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-12` | FR | **NOT_DONE** | Should Have | Include god nodes in 'get_architecture' hotspots section | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-13` | FR | **NOT_DONE** | Should Have | Auto-generate '.leankg/GRAPH_REPORT.md' on every index (CLI 'leankg report' / MCP 'get_gr… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-16` | FR | **NOT_DONE** | Should Have | ADR/RFC citation extraction from docs → rationale nodes linked to code (parser done in 'b… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-MP-02` | FR | **NOT_DONE** | Should Have | On re-index, set 'valid_to = now()' on removed edges instead of deleting them (deferred; … | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-09` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse Claude export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-10` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse ChatGPT export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-11` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse Slack export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-12` | FR | **NOT_DONE** | Should Have | Extract decisions, preferences, milestones, problems from conversations as new element ty… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-13` | FR | **NOT_DONE** | Should Have | New CLI command 'mine-conversations' with '--format' and '--project' flags | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-20` | FR | **NOT_DONE** | Should Have | Enhance 'orchestrate' intent parser to follow tunnels and use L0-L3 layer strategy (defer… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-24` | FR | **NOT_DONE** | Should Have | 'get_impact_radius' accepts directory qualified names (e.g., '"src/indexer/"') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-25` | FR | **NOT_DONE** | Should Have | 'search_code' and 'query_file' accept directory nodes for folder-scoped search | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-26` | FR | **NOT_DONE** | Should Have | Cluster-to-directory alignment via 'cluster_directory' metadata | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-SEM-01` | FR | **NOT_DONE** | Should Have | Dual token accounting: delivered tokens + _token_budget.{max,actual,truncated}; docs teac… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-SEM-02` | FR | **NOT_DONE** | Should Have | Explicit max_tokens_for_tool for concept_search + kg_semantic_context (≥ sibling kg_*, ta… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-SEM-03` | FR | **NOT_DONE** | Should Have | MCP HTTP resilience for long read-only semantic tools (retry docs + keep-alive / stale-li… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P3** | `US-GF-10` | User Story | **PARTIAL** | Could Have | Expand language extractors toward Graphify breadth (Vue/Svelte, Scala, Lua, Zig, shell, A… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P3** | `US-GF-12` | User Story | **PARTIAL** | Could Have | Live SQL / Postgres schema introspection into the same graph (tables, FKs, views ↔ app co… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P3** | `US-GN-08` | User Story | **PARTIAL** | Could Have | MCP Resources for overview context | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P3** | `US-CBM-C5` | User Story | **PENDING** | Could Have | Windows build + smoke | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P3** | `US-SEM-04` | User Story | **PENDING** | Could Have | Semantic hit diversity across files (MMR / file-diversity post-filter) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P3** | `FR-SEM-05` | FR | **NOT_DONE** | Could Have | Optional file-diversity / MMR post-filter after HNSW+rerank (top-k not ≥70% one file) | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P3** | `FR-EMBED-R4` | FR | **OPEN** | Could Have | (open / aspirational): Cold functions-only &lt;20 min on ~371k on reference M2 Pro 10c. *… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |

---

## Master table (all tasks)

| Focus | ID | Kind | Status | Priority | Title | PRD § |
|------:|----|------|--------|----------|-------|-------|
| **P0** | `FR-HNSW-E` | FR | **DONE** | Must Have | Incremental embed filter (foundation) — PARTIAL: day-2 resume/HNSW no-op/stale-blast trac… | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) + 5.12 |
| **P0** | `US-EMBED-01` | User Story | **DONE** | Must Have | Second standalone Docker/CLI embed --wait on unchanged code skips fresh vectors (day-2 de… | 3.15 Day-2 Embed Resume (US-EMBED) — v3.7.2 |
| **P0** | `US-EMBED-02` | User Story | **DONE** | Must Have | Interrupted embed (CLI or Docker MCP) resumes; already-fresh vectors are not re-inferred | 3.15 Day-2 Embed Resume (US-EMBED) — v3.7.2 |
| **P0** | `US-EMBED-03` | User Story | **DONE** | Must Have | Zero-dirty embed does not drop/rebuild HNSW | 3.15 Day-2 Embed Resume (US-EMBED) — v3.7.2 |
| **P0** | `US-EMBED-04` | User Story | **DONE** | Must Have | Docker MCP/boot/setup embed resumes existing RocksDB data; cold/fresh only when no embed … | 3.15 Day-2 Embed Resume (US-EMBED) — v3.7.2 |
| **P0** | `US-VE-01` | User Story | **DONE** | Must Have | As a local developer on Apple Silicon (≤16GB RAM), I want idle LeanKG MCP RSS **&lt; 150M… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-02` | User Story | **DONE** | Must Have | As an AI agent, I want code chunks + dependency JSON in **&lt; 100ms P95**, so tool loops… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-03` | User Story | **DONE** | Must Have | As a platform engineer, I want 'LocalEngine' vs 'CloudEngine' selected via env/config (Ru… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-04` | User Story | **DONE** | Must Have | As a query runtime, I want SQ8/INT8 vectors fully in RAM with dynamic SIMD (NEON / AVX2 /… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-05` | User Story | **DONE** | Must Have | As a storage owner on a 256GB SSD, I want mmap disabled + Zstd RocksDB + append/fsync dua… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-07` | User Story | **DONE** | Must Have | As a QA engineer, I want dual-write crash, SIMD differential, GC concurrency, and engine-… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `US-VE-08` | User Story | **DONE** | Must Have | As a product owner, I want Kilo/OpenCode A/B (≥100 tasks) showing ≥60% token cut, ≥80% to… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P0** | `FR-EMBED-RESUME-01` | FR | **DONE** | Must Have | Standalone embed --wait loads RocksDB embedding_state; unchanged second run skips fresh (… | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-VE-ABS` | FR | **DONE** | Must Have | Storage abstraction via Rust traits + **static enum dispatch** ('LocalEngine' / 'CloudEng… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-FS-DW` | FR | **DONE** | Must Have | Safe dual-write order: **Append Flat File → 'fsync' → Commit offsets to RocksDB/TiKV → Up… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-FS-REC` | FR | **DONE** | Must Have | Crash after Flat File write but before RocksDB commit → clean recovery, **no dangling poi… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-HNSW` | FR | **DONE** | Must Have | HNSW 'selectNeighborsHeuristic' with low **M ∈ [12, 16]**; raise 'efConstruction' to prot… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-RT-MEM` | FR | **DONE** | Must Have | Auto-tune RocksDB block cache from cgroups / 'sysinfo' available RAM (2GB survival → clou… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-RT-SIMD` | FR | **DONE** | Must Have | Runtime SIMD dispatch ('is_x86_feature_detected!' / 'is_aarch64_feature_detected!') → AVX… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-RT-THREADS` | FR | **DONE** | Must Have | Dynamic 'rayon' pool — leave **2 cores free** for OS/IDE on Local; utilize full machine o… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-T1` | FR | **DONE** | Must Have | **Tier 1 — Graph topology** in RocksDB (Local) / TiKV (Cloud): metadata, AST refs, HNSW a… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-T2` | FR | **DONE** | Must Have | **Tier 2 — Quantized vectors** as an in-memory SQ8/INT8 array (100% RAM). All hot ANN dis… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-T3` | FR | **DONE** | Must Have | **Tier 3 — Raw payload** flat binary file: original FP32 vectors + source/chunk payload. … | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-EMBED-RESUME-02` | FR | **DONE** | Must Have | Skip HNSW drop+recreate when to_embed empty and orphan set empty; fast no-op exit | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-EMBED-RESUME-03` | FR | **DONE** | Must Have | Mid-run durability: committed fresh rows survive kill/restart; next run dirty-only | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-EMBED-RESUME-04` | FR | **DONE** | Must Have | Indexer marks stale only for content_hash-changed QNs; no stale-all on no-op full index | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-EMBED-RESUME-05` | FR | **DONE** | Must Have | Day-2 SLA evidence: unchanged mega-graph second pass near-zero ONNX; wall time << cold | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-EMBED-RESUME-06` | FR | **DONE** | Must Have | All Docker embed-on paths share resume-if-data / cold-if-empty; never wipe on enable (BAC… | 5.16 Day-2 Embed Resume / Resource Gate (v3.7.2) |
| **P0** | `FR-VE-TEST-DW` | FR | **DONE** | Must Have | Dual-write crash simulation unit/integration test (assert recovery). | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-TEST-FACTORY` | FR | **DONE** | Must Have | Env injection selects LocalEngine vs CloudEngine correctly. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-TEST-GC` | FR | **DONE** | Must Have | 10k update/delete fragment → background GC + concurrent reads → integrity OK, reads never… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-TEST-SIMD` | FR | **DONE** | Must Have | Differential test: NEON / AVX2 / scalar on same mock set; abs error **&lt; 1e-6**. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `REL-052` | Release | **DONE** | Must Have | v3.7.2 embed-resume gate: day-2 proven for standalone embed --wait AND Docker MCP embed-o… | 8.5 v3.7.2 Embed Resume Gate |
| **P0** | `FR-VE-BENCH-IO` | FR | **DONE** | Must Have | Prove ≥ **80%** reduction in page faults / disk reads vs legacy mmap architecture. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-BENCH-OOM` | FR | **DONE** | Must Have | Simulated **2GB cgroup** — heap/RSS monitored; **must not** OOM-kill. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-BENCH-Q` | FR | **DONE** | Must Have | 'cargo bench' — 1 query vs **1,000,000** SQ8 chunks, Local mode P95 **&lt; 50ms**. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-BENCH-RECALL` | FR | **DONE** | Must Have | Recall **&gt; 90%** at 'efSearch=50' vs FP32 brute-force. | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `REL-044` | Release | **DONE** | Must Have | 3-tier LocalEngine implemented (FR-VE-T1..T3 + FR-VE-ABS) | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-045` | Release | **DONE** | Must Have | Dynamic SIMD + memory + thread auto-tune (FR-VE-RT-*) | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-046` | Release | **DONE** | Must Have | Dual-write + crash recovery + GC (FR-VE-FS-*) | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-047` | Release | **DONE** | Must Have | Unit/integration: DW crash, SIMD differential, GC concurrency, factory (FR-VE-TEST-*) | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-048` | Release | **DONE** | Must Have | Benches: &lt;50ms P95 @ 1M SQ8; ≥80% I/O reduction vs mmap; recall &gt;90% @ ef=50; 2GB c… | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-049` | Release | **DONE** | Must Have | Agent A/B: ≥60% tokens, ≥80% tool calls, ≥2× faster, success ≥ baseline (FR-VE-BENCH-AB) | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `REL-050` | Release | **DONE** | Must Have | Idle MCP RSS &lt; 150MB; time-to-context P95 &lt; 100ms | 8.4 v3.7 Vector Engine Gate (COMPLETE on PR #80) |
| **P0** | `FR-VE-BENCH-AB` | FR | **DONE** | Must Have | Agent A/B ('run_kilo_ab_final.sh' or existing harness), ≥100 tasks: | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-GATE` | FR | **DONE** | Must Have | Default Local switch only when FR-VE-TEST-* + FR-VE-BENCH-Q/IO/RECALL/OOM pass and FR-VE-… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `FR-VE-FS-GC` | FR | **DONE** | Should Have | Zero-downtime GC via shadow paging + micro-lock delta sync; trigger when fragmentation **… | 5.14 Optimized Local-First Vector Graph Engine (v3.7.0) |
| **P0** | `US-VE-06` | User Story | **DONE** | Should Have | As an operator, I want zero-downtime GC (shadow paging + micro-lock delta sync when fragm… | 3.13 Optimized Local-First Vector Graph Engine (US-VE)… |
| **P1** | `US-08` | User Story | **PARTIAL** | Must Have | Multi-language support (Go, TS, Python, Rust, Java, Kotlin, C++, C#, Ruby, PHP) | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-CBM-A2` | User Story | **PARTIAL** | Must Have | Ontology online ('kg_ontology_status', 'concept_search' non-empty after sync) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B1` | User Story | **PARTIAL** | Must Have | Typed call resolution Go + TypeScript MVP ('resolution_method=typed') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-GF-04` | User Story | **PARTIAL** | Must Have | Edge provenance labels 'EXTRACTED' / 'INFERRED' / 'AMBIGUOUS' on all relationships (unify… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `US-MG-02` | User Story | **PARTIAL** | Must Have | Single-repo projects expand fully on service double-click (no multi-level drilling) | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MP-02` | User Story | **PARTIAL** | Must Have | Layered Context Loading (L0-L3) — explicit token budgets per layer: L0 identity (~50 tok)… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P1** | `US-MP-08` | User Story | **PARTIAL** | Must Have | Folder Structure as Graph Edges — directories as first-class 'directory' nodes with 'cont… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P1** | `US-CBM-A1` | User Story | **PENDING** | Must Have | Correct MCP 'project' routing (multi-mount ≠ wrong RocksDB project) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-A4` | User Story | **PENDING** | Must Have | Moat smoke (ontology + routing) gates Phase 1 “done” | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-D3` | User Story | **PENDING** | Must Have | Re-evaluate dual-run after typed-resolve Phase | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-GF-03` | User Story | **PENDING** | Must Have | Natural-language scoped subgraph query ('query_graph "what connects auth to DB?"') | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `FR-B05` | FR | **NOT_DONE** | Must Have | Benchmark harness vs CBM (50-edge samples) | 5.10 CBM Structural Parity Requirements (merged) |
| **P1** | `FR-LSP-A` | FR | **NOT_DONE** | Must Have | LeanKG-native Hybrid LSP tier — an **in-process, no-spawn type resolver** for Go / TypeSc… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-B` | FR | **NOT_DONE** | Must Have | Prefab 'lsp:' block — 'leankg init --with-lsp' writes a default block listing 'gopls' / '… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-C` | FR | **NOT_DONE** | Must Have | Wire 'resolve_with_lsp' results into the indexer — when 'typed_resolve=go,ts' (or 'all') … | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-LSP-D` | FR | **NOT_DONE** | Must Have | Cross-file type registry shared across files in the same project (mirror 'internal/cbm/ls… | 5.13 LSP Adoption Track from CBM (moved from former 5.… |
| **P1** | `FR-MG-03` | FR | **NOT_DONE** | Must Have | Single-repo projects treated as single service — root double-click loads everything | 5.7 Massive Graph UI (DONE) |
| **P1** | `REL-032` | Release | **NOT_DONE** | Must Have | Swift / Vue / Svelte / SQL DDL — extractors present ('swift.rs' / 'sfc.rs' / 'sql.rs') bu… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-039` | Release | **NOT_DONE** | Must Have | Default LSP server bootstrap (FR-LSP-B / FR-B09 fanout — gopls + tsserver + pyright + dar… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-040` | Release | **NOT_DONE** | Must Have | REST API auth wiring + mutation endpoints (mutation endpoints still partial) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-041` | Release | **NOT_DONE** | Must Have | 3D graph UI Track E ('graph-ui/'; keep 2D 'ui/') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-042` | Release | **NOT_DONE** | Must Have | US-GF-03 NL scoped subgraph ('query_graph') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-043` | Release | **NOT_DONE** | Must Have | US-GF-04 provenance labels on all relationship types | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `FR-B03` | FR | **DONE** | Must Have | LSP bridge infrastructure for Go (could read 'gopls' textDocument/definition/references) … | 5.10 CBM Structural Parity Requirements (merged) |
| **P1** | `FR-B04` | FR | **DONE** | Must Have | LSP bridge infrastructure for TS/TSX — DONE infra; actual 'typed' edge production PENDING | 5.10 CBM Structural Parity Requirements (merged) |
| **P1** | `REL-001` | Release | **DONE** | Must Have | Code indexing works for 10 languages | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-002` | Release | **DONE** | Must Have | Dependency graph builds correctly with 10 relationship types | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-003` | Release | **DONE** | Must Have | CLI commands functional (28+ commands) | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-004` | Release | **DONE** | Must Have | MCP server exposes 65 tools (audited 2026-07-13 in 'src/mcp/tools.rs') | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-005` | Release | **DONE** | Must Have | Documentation generation produces valid markdown | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-006` | Release | **DONE** | Must Have | Business logic annotations can be created and queried | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-007` | Release | **DONE** | Must Have | Impact radius analysis works with confidence scores | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-008` | Release | **DONE** | Must Have | Auto-install MCP config works for 7 AI tools | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-009` | Release | **DONE** | Must Have | Web UI shows interactive graph visualization (20+ routes) | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-010` | Release | **DONE** | Must Have | Resource usage within targets | 8.1 MVP (v1.x) - COMPLETED |
| **P1** | `REL-011` | Release | **DONE** | Must Have | Cross-file call edges resolved correctly | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-012` | Release | **DONE** | Must Have | Go implements edges only for embedded fields | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-013` | Release | **DONE** | Must Have | Datalog injection prevention via escape_datalog | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-014` | Release | **DONE** | Must Have | Push-down queries for search_code, find_function, query_file | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-015` | Release | **DONE** | Must Have | signature_only mode for get_context | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-016` | Release | **DONE** | Must Have | Bounded call graph with depth and max_results | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-017` | Release | **DONE** | Must Have | mcp_index_docs tool functional | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-018` | Release | **DONE** | Must Have | Doc reference extraction with code-block skipping | 8.2 v2.0 Release - COMPLETED |
| **P1** | `REL-019` | Release | **DONE** | Must Have | RTK compression (8 modes, response compression) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-020` | Release | **DONE** | Must Have | Smart orchestrator with persistent cache (+ hot-path cache '836f0a3') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-021` | Release | **DONE** | Must Have | Git hooks (pre/post-commit, post-checkout, GitWatcher) + CI/CD auto-update GHA workflow | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-022` | Release | **DONE** | Must Have | Context metrics tracking | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-023` | Release | **DONE** | Must Have | REST API server with auth | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-024` | Release | **DONE** | Must Have | Global multi-repo registry | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-025` | Release | **DONE** | Must Have | Wiki generation | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-026` | Release | **DONE** | Must Have | Graph export (HTML, SVG, GraphML, Neo4j, JSON snapshot) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-027` | Release | **DONE** | Must Have | Cluster detection and cluster-grouped search + per-cluster SKILL.md | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-028` | Release | **DONE** | Must Have | Pre-commit change detection with severity + PR impact dashboard + community-conflict tria… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-029` | Release | **DONE** | Must Have | Benchmark runner (vs OpenCode, Gemini, Kilo) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-030` | Release | **DONE** | Must Have | npm-based installation (US-14 — 'df0fec2') | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-031` | Release | **DONE** | Must Have | Dart + XML entity extraction (indexed) | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-033` | Release | **DONE** | Must Have | LSP bridge infrastructure + 'resolve_with_lsp' MCP + 'leankg lsp-resolve' CLI ('534cd7f',… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-034` | Release | **DONE** | Must Have | Temporal knowledge graph + work-memory reflect loop + consistency checker + tunnels + age… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-035` | Release | **DONE** | Must Have | Rationale extraction ('WHY:' / 'NOTE:' / 'HACK:' / 'FIXME:' / 'XXX:') + ADR citations par… | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-036` | Release | **DONE** | Must Have | Clone detection ('find_clones' same-file Jaccard) + cross-repo similar edges | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-037` | Release | **DONE** | Must Have | Event-channel edges 'emits' / 'listens_on' | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `REL-038` | Release | **DONE** | Must Have | CozoDB HNSW semantic track (FR-HNSW-A..F + FR-BENCH-HNSW) on 'integration/prd-pending' | 8.3 v3.6 Roll-up (Current: v0.17.9) - STATUS |
| **P1** | `US-01` | User Story | **DONE** | Must Have | Auto-index codebase so AI tools have accurate context | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-02` | User Story | **DONE** | Must Have | Generate and update documentation automatically | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-03` | User Story | **DONE** | Must Have | Map business logic to code for AI understanding | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-04` | User Story | **DONE** | Must Have | Expose MCP server for AI tool integration | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-05` | User Story | **DONE** | Must Have | Full CLI interface with query and MCP server commands | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-06` | User Story | **DONE** | Must Have | Minimal resource usage | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-12` | User Story | **DONE** | Must Have | Fix impact radius calculation for qualified names | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-14` | User Story | **DONE** | Must Have | npm-based installation without Rust | 3.1 Core MVP Stories (US-01 to US-18) |
| **P1** | `US-19` | User Story | **DONE** | Must Have | Cross-file call edge resolution | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-20` | User Story | **DONE** | Must Have | Go 'implements' edge extraction fix | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-21` | User Story | **DONE** | Must Have | Push-down Datalog queries + injection safety | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-22` | User Story | **DONE** | Must Have | Token-efficient 'signature_only' context mode | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-24` | User Story | **DONE** | Must Have | Fix 'get_doc_for_file' query direction bug | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-25` | User Story | **DONE** | Must Have | Add 'mcp_index_docs' MCP tool | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P1** | `US-AB-01` | User Story | **DONE** | Must Have | OpenCode token parsing for benchmark comparison | 3.4 AB Testing Stories (US-AB-01 to US-AB-05) |
| **P1** | `US-AB-02` | User Story | **DONE** | Must Have | Context correctness validation (precision/recall/F1) | 3.4 AB Testing Stories (US-AB-01 to US-AB-05) |
| **P1** | `US-AB-03` | User Story | **DONE** | Must Have | CozoDB data store correctness tests | 3.4 AB Testing Stories (US-AB-01 to US-AB-05) |
| **P1** | `US-CBM-A3` | User Story | **DONE** | Must Have | Default call-edge resolution on index for Go/TS | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B10` | User Story | **DONE** | Must Have | Feature flag 'typed_resolve=off\/go,ts\/all' | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B11` | User Story | **DONE** | Must Have | Architecture/schema honor token budgets / truncation | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B2` | User Story | **DONE** | Must Have | HTTP Route nodes + 'http_calls' edges (≥2 Go + ≥2 TS frameworks) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B3` | User Story | **DONE** | Must Have | 'get_architecture' single-call overview | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B4` | User Story | **DONE** | Must Have | 'get_graph_schema' label/edge counts | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-B9` | User Story | **DONE** | Must Have | Call 'resolution_method' + numeric 'confidence' on edges | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-C1` | User Story | **DONE** | Must Have | Docker image: embeddings / semantic tools OOTB (Cozo HNSW) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-D1` | User Story | **DONE** | Must Have | Skills remain LeanKG-first; optional CBM escape hatch only | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-CBM-D2` | User Story | **DONE** | Must Have | Do not auto-install CBM into default '.mcp.json' | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P1** | `US-GF-01` | User Story | **DONE** | Must Have | Shortest path between two symbols/concepts ('leankg path A B' + MCP 'shortest_path') | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `US-GF-02` | User Story | **DONE** | Must Have | Explain a node: source location, community/cluster, degree, labeled neighbors | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `US-GF-05` | User Story | **DONE** | Must Have | God-node / hub ranking exposed via CLI + MCP (top-degree concepts; exclude utility super-… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P1** | `US-GN-01` | User Story | **DONE** | Must Have | Impact analysis with confidence scores and severity classifications | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P1** | `US-GN-02` | User Story | **DONE** | Must Have | Pre-commit 'detect_changes' tool | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P1** | `US-INF-01` | User Story | **DONE** | Must Have | Git pre-commit hook with critical file blocking | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P1** | `US-MG-01` | User Story | **DONE** | Must Have | Double-click Service node loads ALL elements and edges in one shot | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MG-03` | User Story | **DONE** | Must Have | Filter UI always shows ALL node type toggles regardless of loaded data | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MG-04` | User Story | **DONE** | Must Have | Default visible filters: Service, Folder, File, Function ON; rest OFF | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MG-05` | User Story | **DONE** | Must Have | Expand-service API optimized: targeted DB query instead of full scan | 3.8 Massive Graph Stories (US-MG-01 to US-MG-05) |
| **P1** | `US-MP-01` | User Story | **DONE** | Must Have | Temporal Knowledge Graph — relationships have valid_from/valid_to; historical queries ("w… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P1** | `US-RTK-01` | User Story | **DONE** | Must Have | LeanKGCompressor for internal command compression | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-RTK-02` | User Story | **DONE** | Must Have | CargoTestCompressor with failures-only mode (85%+ savings) | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-RTK-03` | User Story | **DONE** | Must Have | GitDiffCompressor with stats extraction (70%+ savings) | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-RTK-05` | User Story | **DONE** | Must Have | 8 read modes: adaptive, full, map, signatures, diff, aggressive, entropy, lines | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-RTK-07` | User Story | **DONE** | Must Have | ResponseCompressor for MCP JSON responses | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-RTK-08` | User Story | **DONE** | Must Have | Compress impact_radius, call_graph, search_code responses | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P1** | `US-TOON-01` | User Story | **DONE** | Must Have | MCP tool responses use TOON format for ~40% token reduction vs JSON | 3.9 TOON Format Stories (US-TOON-01) |
| **P1** | `US-V2-01` | User Story | **DONE** | Must Have | Environment namespacing ('local' / 'staging' / 'production' / 'upcoming') on nodes/edges;… | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-02` | User Story | **DONE** | Must Have | Incident knowledge layer — contribute/query incidents linked to services | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-03` | User Story | **DONE** | Must Have | Enhanced service context (deps, incidents, env) in one MCP call | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-04` | User Story | **DONE** | Must Have | Surface environment conflicts before promote/push | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-05` | User Story | **DONE** | Must Have | Team/knowledge contribution via MCP ('add_knowledge', annotations) | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-07` | User Story | **DONE** | Must Have | Token budget enforcement on MCP responses | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-V2-10` | User Story | **DONE** | Must Have | Multi-repo / shared RocksDB HTTP backend for teams | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P1** | `US-SEM-05` | User Story | **DONE** | Must Have | Exclude UI-bundle / benchmark noise from semantic seeds (embed/assets + src/benchmark gat… | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) |
| **P1** | `FR-SEM-06` | FR | **DONE** | Must Have | Path filter: always drop embed/assets/; query-gate src/benchmark/ unless query contains b… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P1** | `FR-MG-AUTO-01` | FR | **DONE** | Must Have | LEANKG_SKIP_FRESHNESS_CHECK=1 skips MCP auto-index; document mega-graph 6g/3g/cpus6 + AUT… | 5.17 Mega-graph MCP auto-index + embed ops (v3.7.3) |
| **P1** | `FR-OPS-EMBED-CPU` | FR | **DONE** | Must Have | Compose envelope: cpus 6, mem_reservation 3g; MCP mem_limit 6g; embed mem_limit 10g | 5.17 Mega-graph MCP auto-index + embed ops (v3.7.3) |
| **P2** | `US-GF-06` | User Story | **PARTIAL** | Should Have | Generate 'GRAPH_REPORT.md': god nodes, surprising cross-module links, suggested questions… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P2** | `US-LANG-02` | User Story | **PARTIAL** | Should Have | Swift parser (tree-sitter-swift) with regex entity extraction | 3.7 Additional Language Stories (US-LANG-01 to US-LANG… |
| **P2** | `US-CBM-B12` | User Story | **PENDING** | Should Have | ≥10 'run_raw_query' recipes in skills/docs | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-C3` | User Story | **PENDING** | Should Have | Selective language expansion with quality tiers | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E1` | User Story | **PENDING** | Should Have | New 3D graph UI ('graph-ui/') with WebGL galaxy + Bloom (keep existing 2D 'ui/') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E2` | User Story | **PENDING** | Should Have | Server-computed 3D layout in Rust + 'get_graph_layout' / '/api/graph' | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E3` | User Story | **PENDING** | Should Have | Adaptive rendering (InstancedMesh &lt;75k; point sprites above) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-E4` | User Story | **PENDING** | Should Have | Node detail + edge-type filter panels | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-MP-03` | User Story | **PENDING** | Should Have | Conversation/Decision Mining — import Claude/ChatGPT/Slack transcripts; auto-extract deci… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P2** | `US-SEM-01` | User Story | **PENDING** | Should Have | Honest token accounting on truncated MCP payloads (delivered vs _token_budget.actual) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `US-SEM-02` | User Story | **PENDING** | Should Have | Adequate per-tool budgets for concept_search / kg_semantic_context (not default 1000) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `US-SEM-03` | User Story | **PENDING** | Should Have | Resilient MCP HTTP for long semantic calls (transient socket drop retry) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P2** | `FR-A01` | FR | **NOT_DONE** | Should Have | MCP 'project' resolves to correct RocksDB project for multi-mount setups | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A02` | FR | **NOT_DONE** | Should Have | Automate/document ontology sync for concepts + workflows YAML | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A03` | FR | **NOT_DONE** | Should Have | Verify ontology/knowledge tools after sync | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A04` | FR | **NOT_DONE** | Should Have | Index per 'leankg.yaml'; expose counts | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A06` | FR | **NOT_DONE** | Should Have | Smoke: ontology + routing must pass before Phase 1 “fully done” | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B06` | FR | **NOT_DONE** | Should Have | Python + Rust typed resolve (Could) — infra works; LSP server default wiring PENDING | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B13` | FR | **NOT_DONE** | Should Have | Extend 'service_calls' beyond k8s DNS regex (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B16` | FR | **NOT_DONE** | Should Have | Runtime trace ingestion (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B40..B44` | FR | **NOT_DONE** | Should Have | IaC Resource/Module, ADR, snapshot (subset done), DATA_FLOWS (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B50` | FR | **NOT_DONE** | Should Have | ≥ 10 'run_raw_query' recipes (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B51` | FR | **NOT_DONE** | Should Have | Optional openCypher→Cozo subset (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C02` | FR | **NOT_DONE** | Should Have | Document smaller-model / batch-size options (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C04` | FR | **NOT_DONE** | Should Have | Profile impact-radius latency (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C05` | FR | **NOT_DONE** | Should Have | Incremental languages with tier notes (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C06` | FR | **NOT_DONE** | Should Have | Per-language quality tier template; Go/TS first (Must Go/TS) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C07` | FR | **NOT_DONE** | Should Have | Large-repo benchmark ≥ 1M nodes or documented ceiling (Should) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C08..C11` | FR | **NOT_DONE** | Should Have | Windows, pkg channel, SLSA, install targets (Could) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-D04` | FR | **NOT_DONE** | Should Have | Re-evaluate dual-run after Phase 3 typed resolve | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E01..E05` | FR | **NOT_DONE** | Should Have | Vite/React/R3F/shadcn stack in 'graph-ui/' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E10..E14` | FR | **NOT_DONE** | Should Have | Rust 3D layout + 'get_graph_layout' / '/api/graph' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E20..E28` | FR | **NOT_DONE** | Should Have | R3F scene, Bloom, adaptive LOD, edge colors | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E30..E36` | FR | **NOT_DONE** | Should Have | Detail/filter panels, settings, multi-repo galaxies | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-E40..E43` | FR | **NOT_DONE** | Should Have | HTTP integration; embed or static serve; keep 2D 'ui/' untouched | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-GF-05` | FR | **NOT_DONE** | Should Have | MCP tool 'query_graph(question, token_budget?)' — seed → expand → budget trim → TOON | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-06` | FR | **NOT_DONE** | Should Have | CLI 'leankg query "<question>"' wrapping FR-GF-05 (note: existing 'leankg query' is name/… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-07` | FR | **NOT_DONE** | Should Have | Relationship metadata field 'confidence_label' ∈ {EXTRACTED, INFERRED, AMBIGUOUS} written… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-08` | FR | **NOT_DONE** | Should Have | Map 'resolution_method' → 'confidence_label' at edge write time; backfill on reindex | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-09` | FR | **NOT_DONE** | Should Have | Propagate 'confidence_label' in impact, call_graph, path, query_graph, Web UI | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-10` | FR | **NOT_DONE** | Should Have | Index-time god-node / importance scoring (degree + optional PageRank-like) | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-12` | FR | **NOT_DONE** | Should Have | Include god nodes in 'get_architecture' hotspots section | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-13` | FR | **NOT_DONE** | Should Have | Auto-generate '.leankg/GRAPH_REPORT.md' on every index (CLI 'leankg report' / MCP 'get_gr… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-16` | FR | **NOT_DONE** | Should Have | ADR/RFC citation extraction from docs → rationale nodes linked to code (parser done in 'b… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-MP-02` | FR | **NOT_DONE** | Should Have | On re-index, set 'valid_to = now()' on removed edges instead of deleting them (deferred; … | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-09` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse Claude export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-10` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse ChatGPT export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-11` | FR | **NOT_DONE** | Should Have | New conversation_indexer module: parse Slack export JSON format | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-12` | FR | **NOT_DONE** | Should Have | Extract decisions, preferences, milestones, problems from conversations as new element ty… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-13` | FR | **NOT_DONE** | Should Have | New CLI command 'mine-conversations' with '--format' and '--project' flags | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-20` | FR | **NOT_DONE** | Should Have | Enhance 'orchestrate' intent parser to follow tunnels and use L0-L3 layer strategy (defer… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-24` | FR | **NOT_DONE** | Should Have | 'get_impact_radius' accepts directory qualified names (e.g., '"src/indexer/"') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-25` | FR | **NOT_DONE** | Should Have | 'search_code' and 'query_file' accept directory nodes for folder-scoped search | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-26` | FR | **NOT_DONE** | Should Have | Cluster-to-directory alignment via 'cluster_directory' metadata | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-SEM-01` | FR | **NOT_DONE** | Should Have | Dual token accounting: delivered tokens + _token_budget.{max,actual,truncated}; docs teac… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-SEM-02` | FR | **NOT_DONE** | Should Have | Explicit max_tokens_for_tool for concept_search + kg_semantic_context (≥ sibling kg_*, ta… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-SEM-03` | FR | **NOT_DONE** | Should Have | MCP HTTP resilience for long read-only semantic tools (retry docs + keep-alive / stale-li… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-01 to FR-07` | FR | **DONE** | Should Have | Code Indexing and Dependency Graph | 5.1 Core Features (DONE) |
| **P2** | `FR-08 to FR-12` | FR | **DONE** | Should Have | Auto Documentation Generation | 5.1 Core Features (DONE) |
| **P2** | `FR-13 to FR-16` | FR | **DONE** | Should Have | Business Logic to Code Mapping | 5.1 Core Features (DONE) |
| **P2** | `FR-17 to FR-22` | FR | **DONE** | Should Have | Context Provisioning | 5.1 Core Features (DONE) |
| **P2** | `FR-23 to FR-27` | FR | **DONE** | Should Have | MCP Server Interface | 5.1 Core Features (DONE) |
| **P2** | `FR-28 to FR-36` | FR | **DONE** | Should Have | CLI Interface | 5.1 Core Features (DONE) |
| **P2** | `FR-37 to FR-41` | FR | **DONE** | Should Have | Lightweight Web UI | 5.1 Core Features (DONE) |
| **P2** | `FR-42 to FR-50` | FR | **DONE** | Should Have | Pipeline Information Extraction | 5.1 Core Features (DONE) |
| **P2** | `FR-51 to FR-56` | FR | **DONE** | Should Have | Documentation-Structure Mapping | 5.1 Core Features (DONE) |
| **P2** | `FR-57 to FR-60` | FR | **DONE** | Should Have | Enhanced Business Logic Tagging | 5.1 Core Features (DONE) |
| **P2** | `FR-61 to FR-64` | FR | **DONE** | Should Have | Impact Analysis Improvements | 5.1 Core Features (DONE) |
| **P2** | `FR-65 to FR-68` | FR | **DONE** | Should Have | Additional MCP Tools | 5.1 Core Features (DONE) |
| **P2** | `FR-73 to FR-76` | FR | **DONE** | Should Have | MCP Server Self-Initialization | 5.1 Core Features (DONE) |
| **P2** | `FR-77 to FR-79` | FR | **DONE** | Should Have | Terraform Infrastructure Indexing | 5.1 Core Features (DONE) |
| **P2** | `FR-80 to FR-82` | FR | **DONE** | Should Have | CI/CD YAML Indexing | 5.1 Core Features (DONE) |
| **P2** | `FR-A05` | FR | **DONE** | Should Have | Default call-edge resolution for Go/TS on index | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-A07` | FR | **DONE** | Should Have | Agent operating-model: LeanKG-first; moat tools mandatory | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-AB-01` | FR | **DONE** | Should Have | OpenCode token parsing for benchmark comparison | 5.3 AB Testing & Validation (DONE) |
| **P2** | `FR-AB-02` | FR | **DONE** | Should Have | Context correctness validation (precision/recall/F1 per task) | 5.3 AB Testing & Validation (DONE) |
| **P2** | `FR-AB-03` | FR | **DONE** | Should Have | CozoDB data store correctness tests | 5.3 AB Testing & Validation (DONE) |
| **P2** | `FR-AB-04` | FR | **DONE** | Should Have | Prompt YAML format with 'expected_files' field | 5.3 AB Testing & Validation (DONE) |
| **P2** | `FR-AB-05` | FR | **DONE** | Should Have | Token savings summary report with overall verdict | 5.3 AB Testing & Validation (DONE) |
| **P2** | `FR-B01` | FR | **DONE** | Should Have | 'resolution_method': unresolved \/ name \/ name_file_hint \/ typed (typed reserved, not p… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B02` | FR | **DONE** | Should Have | Numeric 'confidence' consistent with method | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B07` | FR | **DONE** | Should Have | Fail soft: fall back to name resolve; never block index | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B08` | FR | **DONE** | Should Have | Feature flag 'typed_resolve=off\/go,ts\/all' in 'IndexerConfig' ('8971dc5') | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B10` | FR | **DONE** | Should Have | 'route' element type (method, path, handler, framework) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B11` | FR | **DONE** | Should Have | ≥ 2 Go + ≥ 2 TS framework extractors (chi/gin/echo + express/fastify) | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B12` | FR | **DONE** | Should Have | 'http_calls' edges with confidence | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B14` | FR | **DONE** | Should Have | Routes searchable; included in 'get_architecture' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B15` | FR | **DONE** | Should Have | EMITS / LISTENS_ON for Go/TS ('25a3b37') | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B20` | FR | **DONE** | Should Have | 'get_architecture' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B21` | FR | **DONE** | Should Have | 'get_graph_schema' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B22` | FR | **DONE** | Should Have | Honor token budgets / truncation on architecture/schema | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B23` | FR | **DONE** | Should Have | 'find_dead_code' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B30` | FR | **DONE** | Should Have | Near-clone detection → similarity edges ('55e6e72') — **non-strategic**; do not expand wi… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B31` | FR | **DONE** | Should Have | 'find_clones' MCP + 'leankg clones' CLI ('55e6e72') — same-file Jaccard only after FR-HNS… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B32` | FR | **DONE** | Should Have | Cross-repo edges across registry ('ab16c9b') | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-B33` | FR | **DONE** | Should Have | Cross-repo summary in tool or architecture ('ab16c9b', surfaced via 'find_cross_repo_simi… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-BENCH-HNSW` | FR | **DONE** | Should Have | Semantic recall smoke — 'tests/hnsw_recall_e2e.rs' (synthetic 384-d vectors + brute-force… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-C01` | FR | **DONE** | Should Have | Docker embeddings OOTB (Must — alias FR-HNSW-C; Dockerfiles '--features embeddings' + 'en… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-C03` | FR | **DONE** | Should Have | Hot-path cache — DONE ('836f0a3') | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-D01` | FR | **DONE** | Should Have | Skills remain LeanKG-first | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-D02` | FR | **DONE** | Should Have | Documented CBM escape hatch when confidence low / lang unsupported (Should — documented a… | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-D03` | FR | **DONE** | Should Have | No auto-install CBM into default '.mcp.json' | 5.10 CBM Structural Parity Requirements (merged) |
| **P2** | `FR-EMBED-R1` | FR | **DONE** | Should Have | MCP / Docker boot must not wait on cold embed. 'LEANKG_EMBED_ON_BOOT=0'; in-process backg… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-EMBED-R2` | FR | **DONE** | Should Have | Parallel embed pipeline — N ONNX workers + single writer; 'import_relations' bulk path; o… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-EMBED-R3` | FR | **DONE** | Should Have | Document measured ceilings — e2e ~170 vec/sec / ~36 min cold on ~371k; writer-only ~100k+… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-GF-01` | FR | **DONE** | Should Have | MCP tool 'shortest_path(source, target, max_hops?)' returns ordered hops with relation + … | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-02` | FR | **DONE** | Should Have | CLI 'leankg path <a> <b>' wrapping FR-GF-01 | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-03` | FR | **DONE** | Should Have | MCP tool 'explain_node(name_or_qn)' — definition, cluster, degree, neighbors | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-04` | FR | **DONE** | Should Have | CLI 'leankg explain <name>' wrapping FR-GF-03 | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-11` | FR | **DONE** | Should Have | MCP 'get_god_nodes(limit, exclude_hubs_percentile?)' + CLI 'leankg gods' | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-14` | FR | **DONE** | Should Have | MCP 'get_graph_report' returns report markdown or structured sections | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-15` | FR | **DONE** | Should Have | Extractor for '# WHY:' / '# NOTE:' / '# HACK:' / '# FIXME:' / '# XXX:' → 'rationale' elem… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-17` | FR | **DONE** | Should Have | CLI 'leankg prs' + MCP 'get_pr_impact' / 'triage_prs' using clusters + detect_changes ('3… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-18` | FR | **DONE** | Should Have | Community conflict detection: PRs whose changed files share clusters (merge-order risk) (… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-19` | FR | **DONE** | Should Have | MCP 'report_query_outcome' + 'leankg reflect' → '.leankg/reflections/LESSONS.md' + option… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GF-20` | FR | **DONE** | Should Have | Portable 'graph-snapshot.json' export (relative paths) + documented optional git merge dr… | 5.9 Graphify-Inspired Features |
| **P2** | `FR-GN-01 to FR-GN-04` | FR | **DONE** | Should Have | Confidence Scoring on Relationships | 5.2 GitNexus Enhancements (DONE) |
| **P2** | `FR-GN-05 to FR-GN-07` | FR | **DONE** | Should Have | Pre-Commit Change Detection Tool | 5.2 GitNexus Enhancements (DONE) |
| **P2** | `FR-GN-08 to FR-GN-12` | FR | **DONE** | Should Have | Multi-Repo Global Registry | 5.2 GitNexus Enhancements (DONE) |
| **P2** | `FR-GN-13 to FR-GN-17` | FR | **DONE** | Should Have | Community Detection and Cluster-Grouped Search | 5.2 GitNexus Enhancements (DONE) |
| **P2** | `FR-GN-18 to FR-GN-19` | FR | **DONE** | Should Have | Enhanced 360-Degree Context Tool | 5.2 GitNexus Enhancements (DONE) |
| **P2** | `FR-HNSW-B` | FR | **DONE** | Should Have | Sole **canonical shipped** ANN path (until FR-VE-GATE) = Cozo '::hnsw' on 'embedding_vect… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-HNSW-C` | FR | **DONE** | Should Have | (= FR-C01 / US-CBM-C1): Docker / RocksDB image builds with '--features embeddings'. Prefe… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-HNSW-D` | FR | **DONE** | Should Have | Default agent discovery path — NL query → embed → HNSW top-k → optional rerank → graph tr… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-HNSW-F` | FR | **DONE** | Should Have | Mega-graph HNSW ops — expose/document 'ef' / 'm' / page 'limit'+'offset'; keep RSS under … | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-INF-01` | FR | **DONE** | Should Have | Git pre-commit hook with critical file blocking | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-02` | FR | **DONE** | Should Have | Git post-commit hook triggers 'leankg index --incremental' | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-03` | FR | **DONE** | Should Have | Git post-checkout hook triggers reindex on branch switch | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-04` | FR | **DONE** | Should Have | GitWatcher for continuous index freshness via commit hash markers | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-05` | FR | **DONE** | Should Have | Context metrics tracking (18-field CozoDB schema) | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-06` | FR | **DONE** | Should Have | REST API server (Axum) with /health, /api/v1/status, /api/v1/search | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-07` | FR | **DONE** | Should Have | API key management (Argon2 hash, create/list/revoke) | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-08` | FR | **DONE** | Should Have | Wiki generation from code structure | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-09` | FR | **DONE** | Should Have | Graph export (HTML interactive, SVG, GraphML, Neo4j, JSON, DOT/Mermaid) | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-INF-10` | FR | **DONE** | Should Have | Orchestrator with intent parsing (7 types) and persistent cache | 5.5 Infrastructure Features (DONE) |
| **P2** | `FR-MG-01` | FR | **DONE** | Should Have | 'api_graph_expand_service' returns ALL relationship types (remove 'matches!(r.rel_type, "… | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-02` | FR | **DONE** | Should Have | Double-click Service node loads entire service tree in single API call | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-04` | FR | **DONE** | Should Have | Filter panel always shows ALL node types from 'DEFAULT_NODE_TYPE_ORDER' (static list, not… | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-05` | FR | **DONE** | Should Have | Default visible node types: 'Service', 'Folder', 'File', 'Function' (all others OFF by de… | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-06` | FR | **DONE** | Should Have | 'resetToStructuralDefaults()' resets to 'DEFAULT_VISIBLE_LABELS' (Service, Folder, File, … | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-07` | FR | **DONE** | Should Have | 'get_elements_in_folder()' targeted DB query for expand-service (regex_matches with bound… | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MG-08` | FR | **DONE** | Should Have | Handler converts absolute folder paths to DB format ('./platform-transport/...') | 5.7 Massive Graph UI (DONE) |
| **P2** | `FR-MP-01` | FR | **DONE** | Should Have | 'valid_from' / 'valid_to' on Relationship schema ('bc9cc53') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-03` | FR | **DONE** | Should Have | MCP tool 'temporal_query' — query graph state as of a given timestamp or commit ('tempora… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-04` | FR | **DONE** | Should Have | MCP tool 'timeline' — chronological evolution of a code element's relationships ('timelin… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-05` | FR | **DONE** | Should Have | '.leankg/identity.md' (L0 context) on 'init' / 'index' (backed by 'wake_up') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-06` | FR | **DONE** | Should Have | '.leankg/critical_facts.md' (L1 context) from graph stats + git log (backed by 'wake_up') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-07` | FR | **DONE** | Should Have | MCP tool 'wake_up' — returns L0+L1 in ~170 tokens, cached and regenerated on re-index | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-08` | FR | **DONE** | Should Have | MCP tool 'load_layer' — registered; L2/L3 paths pending deeper wiring | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-14` | FR | **DONE** | Should Have | MCP tool 'check_consistency' — detect stale/broken links, outdated annotations ('60a6111') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-15` | FR | **DONE** | Should Have | CLI command 'check-consistency' with '--severity' filter ('60a6111') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-16` | FR | **DONE** | Should Have | Relationship type 'tunnel' for cross-cluster domain links ('5b6547e') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-17` | FR | **DONE** | Should Have | MCP tool 'find_tunnels' — discover cross-cluster connections ('5b6547e') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-18` | FR | **DONE** | Should Have | Agent config system: '.leankg/agents/*.json' with focus and filter definitions ('1ea4bcd') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-19` | FR | **DONE** | Should Have | MCP tools 'agent_focus', 'agent_diary_write', 'agent_diary_read' ('1ea4bcd') | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-21` | FR | **DONE** | Should Have | 'directory' element type — every indexed directory becomes a first-class graph node ('gen… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-22` | FR | **DONE** | Should Have | 'contains' edges for full hierarchy: directory→directory, directory→file ('generate_physi… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-MP-23` | FR | **DONE** | Should Have | Directory metadata: 'child_count', 'language_distribution', 'total_lines' in metadata JSO… | 5.6 MemPalace-Inspired Features |
| **P2** | `FR-RTK-01` | FR | **DONE** | Should Have | LeanKGCompressor struct for CLI command compression | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-02` | FR | **DONE** | Should Have | CargoTestCompressor with failures-only mode (85%+ savings) | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-03` | FR | **DONE** | Should Have | GitDiffCompressor with stats extraction (70%+ savings) | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-04` | FR | **DONE** | Should Have | ShellCompressor with leankg-specific patterns | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-05` | FR | **DONE** | Should Have | 8 read modes via FileReader (adaptive, full, map, signatures, diff, aggressive, entropy, … | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-06` | FR | **DONE** | Should Have | EntropyAnalyzer (Shannon, Jaccard, Kolmogorov, repetitive patterns) | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-07` | FR | **DONE** | Should Have | ResponseCompressor for MCP JSON responses | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-08` | FR | **DONE** | Should Have | Compressed responses for impact_radius, call_graph, search_code, dependencies, dependents… | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-09` | FR | **DONE** | Should Have | 'compress_response' parameter on get_impact_radius and other graph tools | 5.4 RTK Compression (DONE) |
| **P2** | `FR-RTK-10` | FR | **DONE** | Should Have | '--compress' CLI flag on 'leankg run' command | 5.4 RTK Compression (DONE) |
| **P2** | `FR-SEM-04` | FR | **DONE** | Should Have | Formal live MCP semantic smoke checklist (Docker project=/workspace) as release complemen… | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `FR-UPD-01` | FR | **DONE** | Should Have | 'leankg update' from GitHub releases | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-01` | FR | **DONE** | Should Have | 'env' field on elements/relationships; default 'local' | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-02` | FR | **DONE** | Should Have | Incident data model + CLI/MCP contribute & query | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-03` | FR | **DONE** | Should Have | 'get_service_context' with env + incident summary | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-04` | FR | **DONE** | Should Have | 'find_env_conflicts' with risk levels | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-05` | FR | **DONE** | Should Have | Knowledge contribution ('add_knowledge' / annotations) | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-06` | FR | **DONE** | Should Have | 'semantic_search' (embeddings feature-flagged) | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-07` | FR | **DONE** | Should Have | Per-tool token budgets / TOON compression | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-08` | FR | **DONE** | Should Have | Vacuum scheduler ('LEANKG_VACUUM_INTERVAL_HOURS'; RocksDB no-op) | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-09` | FR | **DONE** | Should Have | 'kg_self_test' MCP + HTTP startup WARN (non-gating) | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-10` | FR | **DONE** | Should Have | Multi-project RocksDB HTTP deploy + registry | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-11` | FR | **DONE** | Should Have | CI/CD auto-graph update on release (< 3 min freshness) — GitHub Actions workflow ('eb3d33… | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `FR-V2-12` | FR | **DONE** | Should Have | 'get_team_map' ownership/on-call tool ('3368b5f') | 5.11 Team Infrastructure / v2 Requirements (merged fro… |
| **P2** | `REL-051` | Release | **DONE** | Should Have | Live semantic MCP smoke executed (or waived with reason) alongside embeddings cargo suite | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P2** | `US-07` | User Story | **DONE** | Should Have | Lightweight Web UI for graph visualization | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-09` | User Story | **DONE** | Should Have | Pipeline information extraction from CI/CD configs | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-10` | User Story | **DONE** | Should Have | Documentation-structure mapping | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-11` | User Story | **DONE** | Should Have | Enhanced business logic tagging with doc links | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-13` | User Story | **DONE** | Should Have | Additional MCP tools for docs and pipeline queries | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-15` | User Story | **DONE** | Should Have | MCP server expose init/index/install tools | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-16` | User Story | **DONE** | Should Have | MCP server auto-initialize on startup | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-17` | User Story | **DONE** | Should Have | MCP server auto-re-index when starting if stale | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-18` | User Story | **DONE** | Should Have | Configurable auto-indexing via leankg.yaml | 3.1 Core MVP Stories (US-01 to US-18) |
| **P2** | `US-23` | User Story | **DONE** | Should Have | Bounded depth call graph traversal | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P2** | `US-26` | User Story | **DONE** | Should Have | Fix doc-code reference extraction | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P2** | `US-27` | User Story | **DONE** | Should Have | MCP tool definition quality improvements | 3.2 v2.0 Enhancement Stories (US-19 to US-27) |
| **P2** | `US-AB-04` | User Story | **DONE** | Should Have | Token savings summary report with overall verdict | 3.4 AB Testing Stories (US-AB-01 to US-AB-05) |
| **P2** | `US-AB-05` | User Story | **DONE** | Should Have | Prompt YAML format with 'expected_files' field for ground truth | 3.4 AB Testing Stories (US-AB-01 to US-AB-05) |
| **P2** | `US-CBM-B5` | User Story | **DONE** | Should Have | Dead code detection ('find_dead_code') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-B6` | User Story | **DONE** | Should Have | Event channel edges (EMITS / LISTENS_ON) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-B8` | User Story | **DONE** | Should Have | Cross-repo edges on multi-repo registry | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-CBM-C2` | User Story | **DONE** | Should Have | Query hot-path cache (search/schema/architecture/find_function) | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P2** | `US-GF-07` | User Story | **DONE** | Should Have | Extract rationale nodes from '# WHY:' / '# NOTE:' / '# HACK:' / '# FIXME:' / '# XXX:' com… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P2** | `US-GF-08` | User Story | **DONE** | Should Have | PR impact dashboard: graph-aware PR review, community overlap / merge-order risk | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P2** | `US-GF-09` | User Story | **DONE** | Should Have | Work-memory reflect loop: record Q&A outcomes; aggregate lessons that bias future query r… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P2** | `US-GN-03` | User Story | **DONE** | Should Have | Multi-repo global registry | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P2** | `US-GN-04` | User Story | **DONE** | Should Have | Cluster-grouped search results | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P2** | `US-GN-05` | User Story | **DONE** | Should Have | Auto-detect functional clusters | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P2** | `US-GN-06` | User Story | **DONE** | Should Have | 360-degree context view in single tool call | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P2** | `US-INF-02` | User Story | **DONE** | Should Have | Git post-commit hook with auto-incremental reindex | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-03` | User Story | **DONE** | Should Have | Git post-checkout hook with branch-switch reindex | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-04` | User Story | **DONE** | Should Have | GitWatcher for continuous index freshness | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-05` | User Story | **DONE** | Should Have | Context metrics tracking with schema (18 fields) | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-06` | User Story | **DONE** | Should Have | REST API server with health/status/search endpoints | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-07` | User Story | **DONE** | Should Have | API key management with Argon2 hashing | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-09` | User Story | **DONE** | Should Have | Graph export to HTML, SVG, GraphML, Neo4j formats | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-INF-10` | User Story | **DONE** | Should Have | Smart orchestrator with intent parsing and persistent cache | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P2** | `US-LANG-01` | User Story | **DONE** | Should Have | Dart parser (tree-sitter-dart) with getter/setter/enum extraction | 3.7 Additional Language Stories (US-LANG-01 to US-LANG… |
| **P2** | `US-MP-04` | User Story | **DONE** | Should Have | Specialist Agent Contexts — define agent personas (reviewer, architect, ops) each with a … | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P2** | `US-MP-05` | User Story | **DONE** | Should Have | Contradiction & Staleness Detection — detect when stored context contradicts current code… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P2** | `US-MP-07` | User Story | **DONE** | Should Have | Wake-up Context Protocol — standardized 'wake_up' MCP tool that loads ~170 tokens of crit… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |
| **P2** | `US-RTK-04` | User Story | **DONE** | Should Have | ShellCompressor extended with leankg-specific patterns | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P2** | `US-RTK-06` | User Story | **DONE** | Should Have | Entropy analysis (Shannon, Jaccard, Kolmogorov) | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P2** | `US-RTK-09` | User Story | **DONE** | Should Have | 'compress_response' parameter on graph tools | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P2** | `US-RTK-10` | User Story | **DONE** | Should Have | '--compress' CLI flag for shell command output | 3.5 RTK Compression Stories (US-RTK-01 to US-RTK-15) |
| **P2** | `US-UPD-01` | User Story | **DONE** | Should Have | 'leankg update' installs latest GitHub release binary | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P2** | `US-V2-06` | User Story | **DONE** | Should Have | Semantic search natural language → graph nodes | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P2** | `US-V2-08` | User Story | **DONE** | Should Have | Scheduled DB vacuum on long-lived MCP servers | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P2** | `US-V2-09` | User Story | **DONE** | Should Have | Ontology 'kg_self_test' + HTTP startup self-test WARN | 3.12 Team Knowledge Infrastructure (US-V2) — merged fr… |
| **P2** | `FR-BENCH-A` | FR | **WONT_DO** | Should Have | CBM clone quality head-to-head — **Won't Do** (v3.6.2) | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-HNSW-A` | FR | **WONT_DO** | Should Have | Remove custom MinHash/LSH — delete 'src/minhash.rs', drop 'mod minhash' from 'lib.rs' / '… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P2** | `FR-LSH-A..F` | FR | **WONT_DO** | Should Have | AST MinHash / bucket guards / signature K env — **Won't Do** (v3.6.2) | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P3** | `US-GF-10` | User Story | **PARTIAL** | Could Have | Expand language extractors toward Graphify breadth (Vue/Svelte, Scala, Lua, Zig, shell, A… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P3** | `US-GF-12` | User Story | **PARTIAL** | Could Have | Live SQL / Postgres schema introspection into the same graph (tables, FKs, views ↔ app co… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P3** | `US-GN-08` | User Story | **PARTIAL** | Could Have | MCP Resources for overview context | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P3** | `US-CBM-C5` | User Story | **PENDING** | Could Have | Windows build + smoke | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P3** | `US-SEM-04` | User Story | **PENDING** | Could Have | Semantic hit diversity across files (MMR / file-diversity post-filter) | 3.14 Semantic MCP Agent UX Enhancements (US-SEM) — v3.… |
| **P3** | `FR-SEM-05` | FR | **NOT_DONE** | Could Have | Optional file-diversity / MMR post-filter after HNSW+rerank (top-k not ≥70% one file) | 5.15 Semantic MCP Agent UX Enhancements (v3.7.1) |
| **P3** | `FR-EMBED-R4` | FR | **OPEN** | Could Have | (open / aspirational): Cold functions-only &lt;20 min on ~371k on reference M2 Pro 10c. *… | 5.12 Semantic ANN — CozoDB HNSW expansion (v3.6.2) + e… |
| **P3** | `US-CBM-B7` | User Story | **DONE** | Could Have | Clone / near-duplicate detection ('find_clones', 'similar_to') | 3.11 CBM Structural Parity Stories (US-CBM) — merged f… |
| **P3** | `US-GF-11` | User Story | **DONE** | Could Have | Portable graph snapshot export + optional git merge driver for team-committed graph artif… | 3.10 Graphify-Inspired Stories (US-GF-01 to US-GF-12) |
| **P3** | `US-GN-07` | User Story | **DONE** | Could Have | Cluster-level SKILL.md generation | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P3** | `US-GN-09` | User Story | **DONE** | Could Have | Repository wiki generation | 3.3 GitNexus Enhancement Stories (US-GN-01 to US-GN-09) |
| **P3** | `US-INF-08` | User Story | **DONE** | Could Have | Wiki generation from code structure | 3.6 Infrastructure Stories (US-INF-01 to US-INF-10) |
| **P3** | `US-LANG-03` | User Story | **DONE** | Could Have | XML parser (tree-sitter-xml) with child-elements + attributes | 3.7 Additional Language Stories (US-LANG-01 to US-LANG… |
| **P3** | `US-MP-06` | User Story | **DONE** | Could Have | Cross-Domain Tunnels — auto-link clusters across projects/modules that share the same dom… | 3.9 MemPalace-Inspired Stories (US-MP-01 to US-MP-08) |

---

## Sync notes

- **PR [#81](https://github.com/FreePeak/LeanKG/pull/81):** embed-resume + SEM-06 + MG-AUTO-01 + OPS cpus6/3g/6g.
- **Evidence:** [`docs/reports/embed-3-workspaces-2026-07-17.md`](reports/embed-3-workspaces-2026-07-17.md), [`docs/semantic-search-mcp-verification-2026-07-18.md`](semantic-search-mcp-verification-2026-07-18.md).
- Machine mirror: [`prd-task-tracker.json`](prd-task-tracker.json).

*Regenerated: 2026-07-18 — ops envelope + SEM filter.*
