# zvec-grep (zg) vs LeanKG — Competitive Analysis

**Date:** 2026-09-03
**Source:** direct read of https://github.com/zvec-ai/zvec-grep (README, docs/01–08, benchmarks/README) + LeanKG source ground-truth (`src/graph/query.rs`, `src/mcp/*`, `Cargo.toml`, `docs/prd.md` v3.8.8).
**zk:** 1,413 stars, Apache-2.0, TypeScript / Node ≥ 22, backed by Alibaba's [zvec](https://github.com/alibaba/zvec) embedded engine. Pre-1.0 ("work in progress").

---

## 1. What zg is

A **local-first hybrid search layer** for humans and agents: ripgrep + BM25/FTS + vector search behind one interface. Not a knowledge graph. Its center of gravity is *flat retrieval with compact, agent-friendly output* — the same problem space LeanKG's search half occupies, executed with unusual surface discipline.

Architecture (docs/05): CLI + Streamable-HTTP MCP daemon (`127.0.0.1:7999/mcp`, loopback-only, optional Bearer) over an engine with two retrieval paths:

| Path | Mechanism | Requires index |
|---|---|---|
| Indexed retrieval | BM25/FTS + vectors, fused via RRF; explicit `hybrid` / `fts` / `vector` / `--fuse` route groups | Yes (`.zvec-grep/` per workspace) |
| Managed ripgrep | Parsed (never shell-executed) rg invocation, exhaustive, rejects output-changing flags | No |

## 2. Head-to-head

| Dimension | zg | LeanKG (source-verified) | Edge |
|---|---|---|---|
| **Core model** | Flat hybrid retrieval (lexical+vector+RRF) over files/chunks | Knowledge graph (elements + relationships + clusters) + pgvector HNSW + cross-encoder rerank + ontology + session memory | Different products |
| **MCP surface** | **1 tool** default (`zvec_grep_search`), 6 in `full` toolset. Toolset switching is a server flag | ~76 tools (audited `tools.rs`); §3.16/5.18 rationalization waves already cut redundant ones; `orchestrate` exists as a smart router but is one of many | **zg, decisively** |
| **Lexical search** | Real BM25/FTS, ranked, fusable with vector results | `search_by_pattern` = `str_includes(lowercase(qualified_name), …)` substring scan (`src/graph/query.rs:2681`); `knowledge_entries` = `ILIKE` (`src/db/backend.rs:1948`). No tsvector/pg_trgm anywhere in `src/` | **zg, decisively** |
| **Exhaustive text/regex** | Managed rg with zvec-owned compact output format | None — relies on agent-native grep (honest, harness-era, but unmanaged output) | **zg** |
| **Freshness** | FS watcher → background refresh; `fresh` / `possibly_stale` reported in every response; hourly reconciliation probe; `autoUpdate` flag | Auto-indexing watcher exists (`src/watcher`, `mcp-stdio --watch`, burst-limit event-drop fix) but is "discouraged on query-only MCP"; **no freshness signal in tool responses** | **zg** |
| **Embedding catalog** | 14 models: Model2Vec (16M!), ONNX Q4–Q8, GGUF, remote Qwen; per-model dims/limits; device select (`metal`/`cuda`); concurrency; explicit rebuild semantics | Single fastembed path behind `embeddings` feature (off by default); `embed --import` for offsite batch; mega-graph embed has documented OOM/LOCK history (v3.7.5, v3.8.4) | **zg** |
| **Structure-aware extraction** | Code symbols/signatures/breadcrumbs (tree-sitter-like extractors, 10+ langs), Markdown heading sections, text, CSV/JSON/TOML, images w/ multimodal embedding | Deep tree-sitter graph: calls, imports, inheritance, routes, HTTP_CALLS, annotations — far richer *relations*, fewer formats (code + docs) | LeanKG on code depth, zg on format breadth |
| **Structural intelligence** | None (roadmap item: "knowledge-graph construction and graph retrieval") | Impact radius, call graphs, clusters, traceability FR→workflow→code, incidents, env conflicts, service graph, team map, LSP bridge | **LeanKG, decisively** |
| **Agent install UX** | `zg install --target codex|claude|opencode|cursor|…` one command, incl. Qoder elicitation fallback handling | Manual: `mcp-http --port 9699` + hand-written MCP config; Docker path requires container-mount `project=` discipline | **zg** |
| **Auth** | Loopback-only + opt-in Bearer + *separate data-egress authorization* (`zg auth grant --scope workspace`) for remote embedding | Bearer + DB-backed access-token store + roles (`src/mcp/auth.rs`) — richer for multi-user HTTP; no egress-grant concept (no remote embedding at all) | Tie (different threat models) |
| **Benchmarks** | Paired A/B protocol (BrowseComp-Plus 100 cases, SWE-QA 20 tasks), pinned inputs, judge-blind, published methodology + pitfalls doc | Repeatable cross-tool harness already exists — `benchmarks/cross_tool/` (`make full` reproduces the 7-repo WITH/WITHOUT suite; tool-calls/wall/cost metrics, `repos.yaml` pinned); gap vs zg is only input pinning to task versions, N-trial stochasticity, and judge-blind scoring | **zg, on rigor** |
| **Output compaction** | Compact text grouped by file, previews opt-in, per-hit trace | TOON envelope + per-tool token budgets + `compress_response` + `ctx_read` modes | LeanKG comparable, more machinery |
| **Stack risk** | Node 22 + embedded engine (zvec) — simple single-user deploy | Rust + managed Postgres — heavier, but multi-project/multi-user, mega-graph proven (662k elements) | Contextual |

## 3. The three things zg does better than anything LeanKG has

1. **Surface discipline.** One search tool whose *parameters* (`query` / `fts` / `vector` / `fuse` / `globs` / `symbolTypes`) express intent, instead of 76 tools the agent must triage. LeanKG's own history validates this: v3.8.5 found 50% of 88 tools failing live; v3.7.4/3.8.3 deleted redundant tools. `orchestrate` is the right idea buried as tool #77.
2. **The freshness contract.** Every response says `fresh` or `possibly_stale` and can schedule its own background repair. LeanKG has a real auto-indexing watcher (`src/watcher`, `mcp-stdio --watch`, burst-limit event-drop fix) — but query responses carry **no freshness signal**, so agents cannot distinguish current from drifted data — the exact class of bug behind the 2026-08-30 live-probe failures (§3.31: semantic probes 0/3 with dead-end hints).
3. **Benchmark methodology as a shipped artifact.** Their pitfalls doc (stochasticity, leakage, like-for-like) is better than most commercial eval docs and directly reusable.

## 4. Collision assessment

- **Search:** zg + harness-native grep/LSP covers the "find the code" job with less setup than LeanKG. LeanKG's PRD already conceded this (v3.8.7: "mid value as search tool"). zg *reinforces* the repositioning: **don't compete on search; compete as org-memory substrate.**
- **Roadmap threat is real but distant:** zg roadmap direction 2 explicitly adds "knowledge-graph construction and graph retrieval" plus query planning. That is the wedge into LeanKG's differentiator — but they are pre-1.0 and haven't shipped a single graph primitive.
- **Not a substitute:** zg has no impact analysis, no traceability, no incidents, no cross-env conflict detection, no multi-project/multi-user serving, no session memory. For the FR-traceability / org-memory mission (§3.12, PRD-in-KG), there is no overlap today.

## 5. Recommended steals (concrete, prioritized)

| # | Steal | LeanKG mapping | Effort |
|---|---|---|---|
| 1 | **One default tool.** Promote `orchestrate` (or a new `leankg_context`) to the *only* tool in a default toolset; move the rest behind `full`. Contract mirrors `zvec_grep_search`: intent expressed via params, router picks `semantic_search` / `search_code` / `get_impact_radius` / `query_graph`. | `src/mcp/server.rs` toolset registration; §5.18 continues | M |
| 2 | **Real lexical ranking.** Postgres `tsvector` + GIN on `code_elements(name, qualified_name)` and `knowledge_entries`; `websearch_to_tsquery`; fuse with vector scores (RRF) in `semantic_search`'s dual path. Kills the `str_includes`/`ILIKE` blind spot zg punishes. | `src/db/backend.rs`, `src/graph/query.rs` | M |
| 3 | **Freshness contract.** Watcher already exists (`src/mcp/watcher.rs`); add per-response `freshness: fresh|possibly_stale` + background reindex scheduling, decoupled from the query path (heavy work never shares the request — lesson of the pre-PG v3.8.4 RocksDB LOCK-poison incident). | `src/mcp/handler.rs` envelope | M |
| 4 | **`leankg install --target`** agent wiring for opencode/[CC]/codex writing MCP config + `project=` guidance automatically (the Docker container-path footgun is the #1 onboarding failure). | new CLI subcommand | S |
| 5 | **Harden the existing A/B harness.** `benchmarks/cross_tool/` already reproduces the 7-repo WITH/WITHOUT suite (`make full`). Extend it with zg's three rigor gaps: pinned task/repo versions, N-trial runs with reported variance, judge-blind scoring; adopt the zg pitfalls checklist (leakage, like-for-like, stochasticity) into `docs/cross-tool-benchmark.md`. | `benchmarks/cross_tool/`, `docs/cross-tool-benchmark.md` | M |
| 6 | **Embedding catalog breadth** (P2): at minimum a second in-catalog model (small/cheap) + documented model-switch/rebuild semantics like zg's; `embed --import` already covers the offsite path. | `src/embed.rs` | S–M |

Non-goals (correctly out of scope, keep it that way): managed-rg reimplementation (harness grep wins), image/multimodal (PRD §10 excludes), GUI/daemon-on-desktop polish.

## 6. Bottom line

zg is the strongest **search-layer** competitor to date and validates — with 1.4k stars of market evidence — the harness-era verdict already recorded in v3.8.7: retrieval-only value is eroding. It is also the best available template for three LeanKG weaknesses (tool sprawl, no freshness honesty in responses, benchmark rigor gaps). LeanKG's durable moat remains the graph: impact, traceability, incidents, ontology, org memory — a surface zg won't reach for a long time. Steal zg's *discipline*, not its *product*.
