# LeanKG Competitive Research and Product Improvement Strategy

**Date:** 2026-08-02  
**Scope:** LeanKG v0.19.31 vs. GitNexus, LeanCTX, Codanna, Context7, DeepWiki, TencentDB Agent Memory, Letta/MemGPT, Mem0, Cognee, Microsoft GraphRAG, LightRAG, Neo4j GraphRAG, Zep/Graphiti, LangMem, Sourcegraph/SCIP, CodeSee, Aider repomap, CodeGraph, ctags/gtags/cscope, and adjacent code-intelligence systems  
**Method:** Primary-source review, repository audit, competitor comparison, academic/industry synthesis, adversarial qualification of vendor claims. Synthesizes [`docs/analysis/leankg-competitive-research-and-improvement-strategy-2026-08-02.md`](./leankg-competitive-research-and-improvement-strategy-2026-08-02.md) (the original LeanKG-authored report) with four parallel research sweeps.

This document supersedes the prior version. New sections and refinements are tagged `[NEW 2026-08-02 sync]`. Sections preserved verbatim are tagged `[UNCHANGED]`.

---

## Executive summary

LeanKG already has a strong and unusual foundation: a local-first Rust code knowledge graph, typed and provenanced edges, semantic and ontology-aware retrieval, procedural workflows, PRD-to-code traceability, impact analysis, multi-project serving, token-oriented responses, and a broad MCP surface. Its best strategic position is **not** "another repository indexer" and not "the tool with the most MCP methods." It should become the **evidence compiler for software agents**:

> **LeanKG compiles the smallest, freshest, graph-grounded evidence package that lets an agent understand and safely change a codebase.**

This sweep added ten systems the prior report did not deep-dive; six of them confirm the original thesis, and four sharpen specific tactics:

| Source class | Confirms LeanKG thesis | Sharpens LeanKG tactics |
|---|---|---|
| **GitNexus** (LadybugDB RAG, MCP) | process intelligence, small workflow surface, generated area skills | precomputed Leiden Processes, RRF hybrid, multi-repo registry, `GITNEXUS_MCP_READ_ONLY` mode, context hints appended to every tool result |
| **LeanCTX** (`yvgude/lean-ctx`) | context as engineered resource, recoverable compression, budgets/SLOs | `ctx_graph` action-verb union (`build\|related\|symbol\|impact\|context\|diagram\|enrich`), PageRank repomap, per-profile tool gating, trait-based registry (drift gate), temporal validity windows + contradiction detection |
| **Codanna** (Rust, Tantivy) | fast local code search beats APIs | `semantic_search_with_context` returns sym + sig + doc + callers + callees + impact in one call; **document RAG co-indexing** |
| **Context7** (Upstash) | token-budgeted tool APIs | `tokens:` parameter on every read tool; library-doc oracle niche is defensible |
| **DeepWiki** (Cognition, closed) | wiki-as-a-stable-retrieval-unit | `.devin/wiki.json` steer file, fast/deep Q&A modes, auto-refresh on badge |
| **TencentDB Agent Memory** | governed memory assets, layered retrieval | **Mermaid symbolic canvas + offload** to `refs/*.md` drill-down; 4-tier pyramid (L0 Conversation → L3 Persona); `tdai_recall` vs `tdai_memory_search` auto-vs-agent split |
| **Letta/MemGPT** | OS-style memory paging, agent self-edits | `ContextWindowOverview` per-section token reporting; git-backed memory |
| **Mem0 v3** | hybrid (semantic + BM25 + entity) beats vector-only | **3-signal fusion** pattern; 4-D scoping (`user_id × agent_id × app_id × run_id`) |
| **Cognee** | ECL pipeline + persistent KG | **OWL ontology resolver** + `ontology_valid` flag; MD5 content-hash incremental; single-Postgres stack mode |
| **Microsoft GraphRAG / LightRAG / Neo4j GraphRAG** | hierarchical Leiden + precomputed summaries + dual-level retrieval | **VectorCypher** pattern (seed from vector → fan out via structured query); **paragraph-semantic chunking**; **SchemaFromTextExtractor** |
| **Zep / Graphiti** | agent memory needs provenance | **bi-temporal** `valid_from` / `valid_to` / `observed_at` / `recorded_at`; sub-200ms p95 with no LLM in retrieval loop |
| **Sourcegraph / SCIP** | typed semantic truth > broad heuristic | **SCIP** as authoritative overlay; destructive LSIF→SCIP migration at 4.5→4.6 confirms the field moved; `src code-intel upload` route pattern |
| **CodeSee** | framework + service maps, OTLP integration | OTel/gRPC service-map enrichment; `.codesee.json` for steer; **acquired by GitKraken Aug 2024 → product sunset** (don't bet on independent roadmaps) |
| **Aider repomap** `[NEW sync]` | orientation layer beats raw queries | **weighted personalized PageRank** over call graph; binary-search budget-fitted token allocation; renderable ASCII / JSON |
| **ctags / gtags / cscope** `[NEW sync]` | persistent index shell integration | **ctags** (line-oriented, many wrapper formats), **gtags** (SQLite + inverted index + tag literal), **cscope** (C/C++ interactive shell); modern wrappers (`ctags-mcp`, `codeQuery`, `Clink`) wrap them with MCP/JSON |
| **MemVid / LangMem** | chosen as adjacent trade-off examples | immutable frames + WAL = git-history pattern; procedural memory = prompt-update proposal |

The rest of the existing analysis remains valid. The **adoption list** is appended in §17 with concrete LeanKG-shaped changes. The reliability P0 → context compiler P1 → semantic depth P2 → distro P3 sequence is preserved.

---

## 1. Source policy and confidence policy

### 1.1 Source policy `[UNCHANGED]`

The report favors:

1. Official project repositories and documentation.
2. Protocol specifications and academic papers.
3. LeanKG's PRD, benchmark artifacts, tests, and root-cause reports.
4. Vendor benchmark claims only when clearly labeled as vendor-reported.

GitHub stars, marketing benchmarks, and "number of tools" are treated as weak signals. They are not used as evidence of technical superiority.

### 1.2 Ambiguous names `[EXPANDED]`

The original report distinguished CodeGraph vs CodeGraphContext vs LeanCTX. Add:

- **GitNexus:** `abhigyanpatwari/GitNexus` (NOT `awslabs/gitnexus` — the Claude Marketplace avatar renders under the AWS org only because the skill publisher profile is misconfigured). Active npm v1.6.10-rc.95 (2026-07-23).
- **LeanCTX:** `yvgude/lean-ctx`. v3.9.13 (Aug 2026). Apache-style with paid Team Server; local free.
- **Codanna:** `bartolli/codanna`. Apache-2.0, 713 ★, weekly commits since 2025-07.
- **Context7:** `upstash/context7`. MIT; 60k ★; closed backend, hosted at context7.com.
- **DeepWiki:** `cognitionai/deepwiki` (docs repo only); main product at deepwiki.com. Closed-source backend.
- **TencentDB Agent Memory:** `TencentCloud/tencentdb-agent-memory`. Apache-2.0; TypeScript gateway + Python MCP.
- **Letta/MemGPT:** `letta-ai/letta` (legacy V1) → active `letta-ai/letta-code` + `letta-app-server`.
- **Mem0:** `mem0ai/mem0`. Apache-2.0; v3 algorithm removed the explicit graph DB.
- **Cognee:** `topoteretes/cognee`. Apache-2.0; v1.0 single-Postgres mode.
- **Microsoft GraphRAG:** `microsoft/graphrag`. MIT; demo-only, archived Azure accelerator.
- **LightRAG:** `HKUDS/LightRAG`. MIT; 38k ★; EMNLP 2025.
- **Neo4j GraphRAG:** `neo4j/neo4j-graphrag-python`. Apache-2.0.
- **Zep / Graphiti:** `getzep/graphiti`. Apache-2.0 core; arXiv 2501.13956.
- **LangMem:** `langchain-ai/langmem`.
- **Sourcegraph / SCIP:** spec at `scip-code/scip` (moved from `sourcegraph/scip` v0.7.0); MC IP at `sourcegraph/sourcegraph/.../internal/mcp`; transport `/.api/mcp`.
- **CodeSee:** `Codesee-io/codesee-action`, `codesee-deps-go`, `codesee-deps-dotnet`. MIT analyzers; **acquired by GitKraken Aug 2024**, product sunset. Caution: design lessons only, not a roadmap dependency.
- **Aider:** `Aider-AI/aider` — `aider/repomap.py`.
- **ctags:** `universal-ctags/ctags` (active fork). `gtags`: `syohex/gtags` or `GNU/gnulib`. `cscope`: `crossbeam-chris/cscope` (modern fork).
- **CodeGraph (project):** `colbymchenry/codegraph` — the native-kernel code graph used by TencentDB Agent Memory.

### 1.3 Claim qualification `[UNCHANGED]`

- **Confirmed:** directly supported by primary documentation or LeanKG code/report evidence.
- **Vendor-reported:** published by the project but not independently reproduced here.
- **Inference:** strategic conclusion derived from multiple sources.

### 1.4 What is new in this revision

- §3.8–§3.16: eight new competitor deep-dives distilled.
- §3.17 (NEW): Aider repomap, ctags/gtags/cscope, and modern wrappers.
- §4.1: expanded comparative matrix from 7 columns to 17.
- §6.4: **Hybrid retrieval** now mandates three signals (lexical + semantic + graph/proximity), not two.
- §6.7: **OWL ontology resolver** + `ontology_valid` flag adopted from Cognee.
- §6.8: **Bi-temporal** relationship schema (`valid_from`, `valid_to`, `observed_at`, `recorded_at`) from Zep/Graphiti.
- §6.9 (NEW): Mermaid symbolic canvas + offload layer from TencentDB Agent Memory.
- §6.10 (NEW): VectorCypher-style seed-and-fan-out as the canonical retrieval primitive (Neo4j + LeanKG's own `kg_semantic_context`).
- §6.11 (NEW): Precomputed Leiden Processes during indexing (GitNexus).
- §6.12 (NEW): Weighted personalized PageRank orientation layer (Aider).
- §17 (NEW): Ordered adoption list with concrete MCP changes.

---

## 2. LeanKG's current position `[UNCHANGED]`

### 2.1 What LeanKG already does well

LeanKG is broader than a conventional code graph. Current documented capabilities include:

- Tree-sitter and language-specific extraction into typed elements and relationships.
- SQLite and RocksDB-backed CozoDB storage.
- MCP stdio/HTTP and REST/UI surfaces.
- Lexical, concept, ontology, graph, and optional embedding retrieval.
- HNSW plus cross-encoder reranking when embeddings are enabled.
- Impact radius, callers/callees, shortest paths, dead code, clusters, tunnels, routes, Android navigation, tests, docs, services, incidents, and environments.
- `EXTRACTED`, `INFERRED`, and `AMBIGUOUS` provenance labels.
- Procedural ontology and hot-reloaded workflows.
- PRD/user-story/feature traceability.
- Session offload, agent diary, query-outcome reflection, and knowledge entries.
- Token-aware TOON responses and multiple context compression modes.
- Multi-project Docker deployment and a GitNexus-derived interactive explorer.

### 2.2 Strongest differentiation

LeanKG's defensible differentiation is the intersection of:

1. **Code structure:** symbols, calls, imports, tests, routes, services, Android-specific relationships.
2. **Team knowledge:** incidents, ownership, environment state, docs, PRDs, ontology, workflows.
3. **Agent economics:** bounded, compressed, provenance-rich retrieval over MCP.
4. **Local/team deployment:** embedded local mode and shared multi-project mode.

No researched competitor combines all four at LeanKG's current breadth. However, breadth is only an advantage if the most common paths are reliable and easy for agents to select.

### 2.3 Immediate blocker: reliability before expansion

LeanKG's live mega-graph validation found **44 of 88 MCP tools failed** on a graph with 662,378 elements and 2,259,855 relationships. The RCA identifies four code defects plus one data-absence class; several empty-result tools were correct because the target graph lacked PRD, incident, service, cluster, or documentation data. The code defects are documented at `docs/reports/root-cause-mcp-88-tool-validation-workspace-be-2026-08-02.md:5`:

- `project` routing is shadowed by `file`/`path` arguments.
- RocksDB can be opened twice in the same process.
- Synchronous CozoDB calls block Tokio workers and lack request timeouts.
- Mega-graph protection is opt-in, and its own count probe can scan the graph.

This finding changes the roadmap priority. A context compiler, PDG, skill generation, or team-memory governance will not create durable value if the underlying serving contract can stall or route to the wrong graph.

### 2.4 Product surface problem

LeanKG advertises 85+ tools (`README.md:247`). Anthropic's tool-design guidance warns that overlapping tools can confuse tool selection and consume context with duplicate descriptions. Comparable findings:

- **GitNexus** ships 17 workflow-oriented tools (`query`, `context`, `impact`, `trace`, `detect_changes`, `route_map`, `tool_map`, `shape_check`, `api_impact`, `pdg_query`, `explain`, `cypher`, `rename`, `group_*`, `list_repos`, `check`) — each replaces a long chain of low-level operations.
- **LeanCTX** ships 69–82 tools but explicitly groups them under action-verb unions (`ctx_graph build|symbol|related|impact|context|diagram`) and gates by profile (`minimal|standard|power`).
- **Codanna** ships 4–5 tools but `semantic_search_with_context` returns symbol + signature + docstring + callers + callees + impact in one payload.

The right move is not to delete every specialized tool. It is to establish two explicit tiers:

- **Workflow tools:** the default surface for agents.
- **Expert primitives:** discoverable on demand or exposed through raw/advanced mode.

---

## 3. Competitor lessons

### 3.1 CodeGraph (`colbymchenry/codegraph`) `[UNCHANGED]`

#### Confirmed strengths

The project positions itself as a pre-indexed local code knowledge graph with a native Rust parsing kernel, SQLite storage, file watching, and agent integrations. Its documentation emphasizes compiled parsing across many languages, dynamic worker/cache sizing based on cores and available memory, native filesystem events and debounced synchronization, framework-aware route extraction, explicit cross-language bridges such as Swift/Objective-C and React Native/Expo, heuristic edge provenance metadata, and a stale-file banner when the index has not caught up. TencentDB Agent Memory explicitly acknowledges CodeGraph as the foundation of its CodeGraph asset.

#### What LeanKG should learn

1. Resource budgets should be automatic. LeanKG exposes many knobs but should derive safe worker, memory, scan, and response defaults from cgroup/container limits.
2. Staleness must be visible in every response. Watching is insufficient; the consumer needs to know whether the answer includes dirty files.
3. Cross-language bridges deserve first-class extractors. Generic call resolution cannot fully model Swift/ObjC, React Native, generated clients, protobuf, JNI, FFI, or frontend/backend contracts.
4. Framework edges create product value. Routes, event channels, DI registrations, schema consumers, and RPC handlers answer practical questions better than a pure symbol graph.

#### Avoid copying

- Do not rely on a query-first instruction alone; agents often delegate exploration or bypass a graph.
- Do not treat breadth of language names as equivalent to semantic depth.

### 3.2 CodeGraphContext (`CodeGraphContext/CodeGraphContext`) `[EXPANDED]`

#### Confirmed strengths

CodeGraphContext combines tree-sitter with optional SCIP indexers and supports several embedded or remote graph backends. Its official documentation includes:

- 23 language families.
- Optional `scip-clang` for C/C++ and `scip-dotnet` for C#.
- FalkorDB Lite, KuzuDB, LadybugDB, Neo4j, and Nornic backends.
- Live watching, CLI/MCP modes, portable `.cgc` bundles, visualization, and GCF output.

#### What LeanKG should learn `[UNCHANGED]`

1. SCIP is the most practical path to typed semantic truth. Tree-sitter remains the universal fallback; compiler/LSP indexes should override or enrich heuristic edges.
2. Portable pre-indexed bundles lower cold-start friction. A repository can distribute an index snapshot with schema/version/fingerprint metadata.
3. Extraction quality should be explicit by language and relation. "Supported" should become a matrix of syntax, imports, calls, types, inheritance, routes, tests, and confidence.
4. Storage abstraction is useful only behind a stable query contract. LeanKG should not chase many stores now, but its enterprise remote Cozo client should complete the existing abstraction.

#### Avoid copying `[UNCHANGED]`

- Multiple database backends multiply migration, test, and consistency costs.
- Language-count marketing without relation-level quality metrics creates false confidence.

### 3.3 Graphify `[UNCHANGED]`

#### Confirmed strengths

Graphify's official repository emphasizes: local deterministic AST extraction for code; a shared graph over code, docs, configuration, schemas, PDFs, images, and media; visible `EXTRACTED`/`INFERRED` edge labels; `graph.html`, `GRAPH_REPORT.md`, and `graph.json` as immediately useful artifacts; Leiden communities, god nodes, rationale/ADR nodes, and suggested questions; team sharing by committing portable graph artifacts; a merge driver that union-merges parallel graph updates; and broad agent installation + optional strict graph-first hooks.

#### What LeanKG should learn

1. Artifacts are a product surface, not just export formats. LeanKG's portable snapshots and graph report should become a coherent "context pack."
2. Commit-friendly snapshots create team leverage. Use relative paths, content hashes, schema versions, and deterministic ordering; offer a safe merge driver.
3. Rationale is first-class knowledge. `WHY`, `NOTE`, `HACK`, ADR, RFC, PRD, and incident references should attach to code elements and edges.
4. Good packaging beats hidden capability. A user should get an architecture map, confidence legend, next questions, and editor integration immediately after indexing.
5. Multi-modal ingestion is optional, not core. LeanKG should first deepen docs, schemas, CI, PRDs, incidents, and code contracts before adding image/video support.

#### Avoid copying

- Do not make LLM-dependent extraction mandatory for code or private documents.
- Do not use a large committed graph as the canonical live database; snapshots are distribution artifacts, not the serving store.

### 3.4 GitNexus (`abhigyanpatwari/GitNexus`) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

GitNexus is a LadybugDB + tree-sitter + WASM graph with hybrid BM25 + vector + RRF search. The architecture is six pipeline phases (Structure → Parsing → Resolution → Clustering → Processes → Search). It precomputes Leiden communities and **Processes** (entry-point execution flows) at index time so MCP tools return scoped context in one call instead of asking the LLM to walk the graph.

#### MCP tool list (verbatim, current)

`list_repos`, `query`, `context`, `impact`, `trace`, `detect_changes`, `check`, `rename`, `cypher`, `route_map`, `tool_map`, `shape_check`, `api_impact`, `explain`, `pdg_query`, `group_list`, `group_sync`. Plus Resources (`gitnexus://repos`, `gitnexus://repo/{name}/...`, `gitnexus://group/{name}/...`) and Prompts (`detect_impact`, `generate_map`).

#### Innovations

1. **Precomputed structure as a first-class artifact** — communities + processes. `query` returns execution flows, `impact` returns confidence-tagged depth buckets, `trace` returns the shortest call/extends path. Massively reduces token burn vs raw-graph RAG.
2. **PDG / taint analysis** (`--pdg` index, statement-level CDG/REACHING_DEF).
3. **Multi-repo MCP via global registry** — one MCP server hosts N repos; tools accept optional `repo=`.
4. **Symmetric CLI + Web** — same pipeline in Node (native bindings) and browser (WASM).
5. **Server-side next-step hints** appended to every tool result (`gitnexus/src/mcp/server.ts:55-93`).
6. **Augmentation over replacement** — Claude/Cursor hooks add graph context to native Grep/Glob/Bash rather than replacing them.
7. **Hardened modes** — `GITNEXUS_MCP_READ_ONLY=1` strips `cypher`, `rename`, group tools; out-of-budget responses are truncated to `maxTokens` with `…` sentinel.

#### License

**PolyForm Noncommercial**, not OSI-open-source. Design research is permitted; code reuse in commercial contexts is not.

#### What LeanKG should learn

1. Precompute high-value relational products at index time. Entry-point processes, API consumers, tool definitions, and top impact neighborhoods should be built during indexing.
2. Expose workflow-shaped tools. `compile_context` or `change_context` should replace many routine tool chains.
3. Generate cluster skills automatically. LeanKG already has cluster context and skill generation primitives; it should publish an index-time `export-skills` workflow.
4. Add an optional PDG overlay. Start with one or two languages and security/change-impact questions, not universal statement indexing.
5. Model cross-repo contracts. OpenAPI, protobuf, GraphQL, event schemas, database migrations, package APIs, and MCP tool contracts should become typed cross-project edges.
6. Use policy at discovery time. Read-only mode, project allowlists, and environment/branch constraints should remove inaccessible tools/data rather than failing late.
7. **Adopt server-side next-step hints** — cheap to implement, big UX win.

#### Avoid copying

- Do not adopt a noncommercial license.
- Do not expose raw graph power without safe query budgets and authorization.

### 3.5 LeanCTX (`yvgude/lean-ctx`) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

LeanCTX treats context as an independent engineering layer. Five subsystems: Perceive / Compress / Remember / Route / Govern. Single Rust binary acts as MCP server + shell hook + CLI + proxy. 232+ edge types over 18-26 languages. Property graph stored in `graphs/<project-hash>/index.json.zst` (zstd-compressed). Hybrid **BM25 + dense embeddings + graph proximity**, fused via **Reciprocal Rank Fusion (RRF)**.

#### MCP tool list (verbatim, by category)

- **Read/shell:** `ctx_read`, `ctx_smart_read` (10 modes: full/map/signatures/diff/aggressive/entropy/task/reference/lines/auto), `ctx_delta`, `ctx_dedup`, `ctx_fill`, `ctx_multi_read`, `ctx_shell`, `ctx_url_read`, `ctx_discover`, `ctx_edit`, `ctx_compress`, `ctx_retrieve`.
- **Search/discovery:** `ctx_search`, `ctx_semantic_search`, `ctx_tree`, `ctx_overview`, `ctx_intent`.
- **Memory/knowledge:** `ctx_session`, `ctx_knowledge`, `ctx_knowledge_relations`, `ctx_verify`, `ctx_handoff`, `ctx_workflow`, `ctx_share`, `ctx_agent`.
- **Code intelligence / graph:** `ctx_graph` (unified graph: `build|related|symbol|impact|context|diagram|enrich`), `ctx_callgraph` (`callers|callees|trace|risk`), `ctx_impact` (`analyze|diff|chain|build|update|status`), `ctx_architecture` (`overview|clusters|layers|cycles|entrypoints|hotspots|health`), `ctx_repomap` (PageRank of most-important symbols), `ctx_routes`, `ctx_refactor`, `ctx_review`, `ctx_smells` (8 rules).
- **Productivity/observability:** `ctx_pack`, `ctx_artifacts`, `ctx_cost`, `ctx_benchmark`, `ctx_gain`.
- **Total:** 69–82 tools depending on `LEANKG_PROFILE` (`minimal|standard|power`).

#### Innovations

1. **Context-engineering layer** — intercepts requests, compresses on the wire (`lean-ctx proxy enable`), records evidence to a signed ledger, persists sessions via CCP, live dashboard of "what's in your context."
2. **LITM-aware positioning** — critical info at head/tail of context window to dodge "lost in the middle" attention degradation.
3. **PageRank repomap** — "what matters most here?" via combined impact + caller fan-in + coverage + smells.
4. **Per-tool action-verb union** — `ctx_graph` does in LeanKG what `get_call_graph`, `find_tunnels`, `shortest_path`, `query_graph` all do.
5. **Trait-based tool registry** — schema co-located with handler (eliminates schema-drift). LeanCTX's CI runs `tool_registry_complete.rs` as a drift gate.
6. **Time-aware knowledge** — temporal validity windows + contradiction detection.
7. **Ed25519-signed savings ledger** + AAAK compact format.

#### What LeanKG should learn

1. **Per-tool action-verb union over many tool names.** Consider exposing the same payload via one `query_graph` with `intent=` argument, keeping named specializations only for clients that prefer them.
2. **Knowledge entries with temporal validity windows** + contradiction detection, paired with `get_overview_context`.
3. **PageRank-repomap** — use cluster_id + in-degree to expose a single `get_hotspots` token-budget-aware tool.
4. **Hybrid BM25 + embeddings + graph-proximity RRF** as the canonical search.
5. **Per-profile tool gating** (`LEANKG_PROFILE=minimal|standard|power`).
6. **Code-smell ruleset** as a future tool.
7. **MCP Resources + Prompts** — emit `leankg://repo/{name}/hotspots` instead of forcing tool calls.

#### Avoid copying

- Do not absorb the full proxy, addon marketplace, multi-agent harness, and model gateway scope. LeanKG should integrate with such layers, not recreate them.

### 3.6 Codanna (`bartolli/codanna`) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Codanna is a Rust + Tantivy (BM25) + embedded custom symbol store; `.codanna/` per-project; sub-10ms lookups; tree-sitter for 15 languages. Self-documenting "LSP-too-slow" answer.

#### MCP tool list (verbatim)

- `find_symbol` — Symbol search (role filter)
- `semantic_search_with_context` — Concept search returning symbol + docstring + signature + callers + callees + impact in one response
- `analyze_impact` — Symbol blast radius
- Document RAG tool (markdown files indexable, feature flag)
- Plus `codanna mcp <tool> ...` CLI variants for one-shot shell fallback.

#### Innovations

1. **Self-correlating tool responses** — `semantic_search_with_context` returns symbol + signature + docstring + both call-graph directions + blast radius in one payload. Anti-Grep-and-read pattern.
2. **Dual-mode MCP** — persistent server + one-shot CLI.
3. **Document RAG module** — index markdown alongside code; single tool answers "where is X in docs?" and "where is X in code?"
4. **`--watch` flag** — incremental index updates while the developer types; sub-10ms lookups.
5. **Universal agent compat** — Claude / Gemini / Codex / Windsurf / Cursor.
6. **Local-only, no data egress** — explicit differentiator vs cloud IDEs.

#### What LeanKG should learn

1. **Self-correlating tool responses** — `get_context` should append callers, callees, blast radius, and the nearest test in one payload.
2. **Hybrid CLI + persistent MCP** — a `leankg mcp find_function ...` one-shot CLI for scripting/CI.
3. **Document co-indexing** — LeanKG already has `mcp_index_docs`; fold doc search into `concept_search` so doc and code elements compete for the same rank.
4. **`--watch` / fs-watcher incremental reindex** as a future flag (`leankg index --watch`).

### 3.7 Context7 (Upstash) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Closed backend at Upstash over scraped public docs (npm, PyPI, Maven, Go, NuGet). Recurring re-scrape job. Two transports: stdio (`@upstash/context7-mcp`) and remote HTTPS (`https://mcp.context7.com/mcp`).

#### MCP tool list

- `resolve-library-id` — Resolve an npm/PyPI/Maven/Go package name to a Context7 library ID.
- `get-library-docs` — Fetch docs with token-controlled `tokens` parameter.

#### Innovations

1. **Up-to-date documentation** — solves "LLM trained on docs from 18 months ago" with citations.
2. **Token budget in tool input** — `tokens: 5000` is the model telling the server how much text to return.
3. **Massive distribution** — 60k ★; sets baseline expectation for library-aware dev tools.

#### What LeanKG should learn

1. **Tokens parameter on every tool** — client-controlled response budget. Codify across all read tools.
2. **"Doc oracle" position is defensible** — LeanKG should not scrape the world's library docs, but its `mcp_index_docs`-loaded content can adopt the same "give me the canonical doc snippet for this requirement" semantics.
3. **Per-client-server compatibility table** in README (Cursor, Claude Code, Codex, …).

### 3.8 DeepWiki (Cognition Labs) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Closed-source backend at Cognition. Custom vector store (skip-stock per Latent Space interview). Leiden structure discovery, K8s-orchestrated indexing pipeline. 50k+ public repos indexed, free for public GitHub repos. Pipeline: `clone → structure (clusters) → page generation (per cluster) → embed for Ask Q&A`.

#### Steer file

`.devin/wiki.json` with `include`, `exclude`, `pages`, `repo_notes`, `page_notes`. Limits: 30 pages free / 80 enterprise; 100 notes total; 10k chars/note.

#### MCP tool list

- `read_wiki_structure` — List of documentation topics for a GitHub repo.
- `read_wiki_contents` — View wiki page (Markdown).
- `ask_question` — Natural-language Q&A. Two modes: **Fast** (sub-second) and **Deep Research** (20–60 sec). Both with line-level citations back to GitHub.

#### Innovations

1. **Wiki-as-abstraction** — instead of returning raw graph chunks (GraphRAG's weakness), returns wiki pages clustered by system structure. Compact retrieval unit.
2. **Deep Research vs Fast** — two latency tiers in one tool.
3. **Steerable generation** via `.devin/wiki.json`.
4. **Auto-refreshing** wikis + badge-driven on-demand rebuild.

#### What LeanKG should learn

1. **Steer file for indexing** — `.leankg.yaml` already partly does this; add `priority_paths` / `ignore_paths` block.
2. **"Wiki page as a stable retrieval unit"** — precompute per-cluster `cluster.doc.md` summaries at index time and serve via MCP Resources.
3. **Two-mode Q&A** — `search_code` with `mode=fast` (BM25 only, sub-100ms) vs `mode=deep` (semantic + RRF + cluster context, sec-level).

### 3.9 TencentDB Agent Memory `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Three layers: **base** (Tencent Cloud Vector Database + file storage), **core** (short-term compression + long-term 4-tier pyramid L0 Conversation → L3 Persona), **access** (OpenClaw plugin, Hermes API gateway, agent SDK). Local SQLite + sqlite-vec out-of-box; cloud TCVDB for production. Hybrid retrieval = BM25 (jieba/en) + vector + RRF.

#### MCP tools (5 tools, stdio)

`tdai_recall`, `tdai_memory_search`, `tdai_conversation_search`, `tdai_capture`, `tdai_session_end` (since PR #486, June 2026).

#### Key innovations

1. **Mermaid symbolic canvas** — verbose tool logs offloaded to `refs/*.md`; only high-density Mermaid state-graph stays in context. `node_id` drill-down preserves full traceability without token bloat.
2. **Lossless compression pyramid** — Persona/canvas top (Markdown, white-box), Scenario/Atoms mid (jsonl index), raw Conversation bottom (full evidence). Each upper layer links deterministically to lower-layer raw text.
3. **Skill distillation pipeline** — Conversation → Scenario → Persona doubles as a Skill-generation layer.
4. **Auto-recall vs agent-triggered search** — `tdai_recall` fires every turn; `tdai_memory_search` is agent-decided.

#### What LeanKG should learn

1. **Symbolic Mermaid canvas + offload** — LeanKG already has `.leankg/sessions/<id>/refs/<node_id>.md` (`mcp__leankg__session_recall`). Extend the rendering to a Mermaid call-graph for any file larger than a token budget.
2. **Auto-recall vs agent-decided search** — same pattern LeanKG already has between `search_code` (always-on concept search) and `concept_search` (agent-decided).
3. **Skill distillation from successful traces** — Conversation → Scenario → Persona = the same pipeline that promotes `add_ontology_workflow` from raw query traces.

### 3.10 Letta / MemGPT `[NEW 2026-08-02 sync]`

#### Confirmed strengths

OS-style memory: **Core** (RAM, labeled blocks always in context, agent-self-edited via `core_memory_append`/`replace`), **Recall** (page cache, full conversation history), **Archival** (disk, long-term vector). PostgreSQL + object storage + Redis + Turbopuffer/pgvector/Qdrant.

#### Innovations

1. **Agent self-edits memory** — `memory_insert`, `memory_replace`, `memory_rethink`, `archival_memory_search` are first-class tools.
2. **Git-backed memory** — `git_enabled=True` agents store memory as files in a git repo with `GitOperations` and `MemoryCommit` diffs.
3. **`ContextWindowOverview`** — live token counts per section (system/core/messages/tool-rules/filesystem) so the agent can reason about its own budget.

#### What LeanKG should learn

1. **`ContextWindowOverview` per-section token reporting** — emit per-tool result tokens so agents stay within budget.
2. **Block-as-file with slug label** — maps to `code_element` records with version tags.
3. **Hot-path vs background memory** — LeanKG's `add_annotation` is hot-path; consider background for memory writes to avoid blocking the agent.

### 3.11 Mem0 v3 `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Universal personalization layer (`add()` / `search()`). **ADD-only single-pass extraction** (v3). Hybrid retrieval (semantic + BM25 + entity matching). **Built-in entity graph** in vector store (no separate graph DB). 4-D multi-tenancy (`user_id × agent_id × app_id × run_id`).

#### Innovations

1. **3-step loop** — `add()` → `search()` → (LLM generates with context) → `add()` the new turn.
2. **Mem0g paper** (arXiv 2504.19413) — graph variant +2% over base, **91% lower p95 latency, 90% token savings** vs full-context on LoCoMo.
3. **Built-in entity graph** — extract proper nouns + compound phrases, embed them, link memories through shared entities. No `relations` field needed.

#### What LeanKG should learn

1. **3-signal fusion** (semantic + BM25 + entity) — the canonical pattern; LeanKG's `kg_semantic_context` should add BM25 first.
2. **4-D scoping as multi-tenant template** — per-repo, per-team, per-CI-run, per-session.
3. **Schema-free entity graph is the wrong fit for code** — typed edges (`calls`, `imports`, `tested_by`) matter more for code than for chat memory.

### 3.12 Cognee `[NEW 2026-08-02 sync]`

#### Confirmed strengths

ECL pipeline (Extract, Cognify, Load). Triple-DB: vector (LanceDB default) + graph (Kuzu/Ladybug) + relational (SQLite). Retrieval modes: `GRAPH_COMPLETION`, `GRAPH_SUMMARY_COMPLETION`, `GRAPH_COMPLETION_COT`, `TRIPLET_COMPLETION`, `RAG_COMPLETION`, `CHUNKS`, `CHUNKS_LEXICAL`, `SUMMARIES`, `CYPHER`. v1.0 single-Postgres mode (`DB_PROVIDER=postgres + pgvector + graph = postgres`).

#### Innovations

1. **OWL ontology resolver** (`RDFLibOntologyResolver`) — fuzzy-match LLM-extracted entities against OWL classes (0.80 cutoff); canonical URI names + BFS subgraph expansion. Every node tagged `ontology_valid: true/false`.
2. **Incremental loading via MD5 content hash** — same data skips re-processing.
3. **Contradiction detection** (`CONTRADICTION_DETECTION=true`) — opt-in task compares new facts against 1-hop neighborhood, writes `contradicts` edges with confidence.
4. **Migration module** — exports/imports from Mem0 / Zep / Letta.

#### What LeanKG should learn

1. **MD5 content hash incremental** — re-index only changed files.
2. **OWL ontology resolver + `ontology_valid` flag** — don't block on missing ontology; tag nodes that miss it.
3. **Contradiction detection on existing-relationship updates** — prevents stale annotations from overriding fresh ones.
4. **Single-Postgres stack** — SMB deployment option (CozoDB on RocksDB is the local analog).

### 3.13 Microsoft GraphRAG, LightRAG, Neo4j GraphRAG (combined) `[NEW 2026-08-02 sync]`

#### Confirmed strengths

- **GraphRAG** (Microsoft, MIT, 35k ★) — Leiden communities + precomputed summaries + Global/Local/DRIFT search modes.
- **LightRAG** (HKUDS, MIT, 38k ★, EMNLP 2025) — dual-level retrieval (low-level entities + high-level themes), **paragraph-semantic chunking** that respects document structure, KV_STORAGE for LLM cache, cheap incremental updates.
- **Neo4j GraphRAG** (Apache-2.0) — **VectorCypher** retriever (seed from vector → fan out via structured query 1-3 hops), **SchemaFromTextExtractor** (LLM proposes schema from sample, then guides bulk extraction), 3 entity resolvers (exact, spaCy semantic, RapidFuzz fuzzy), Lexical graph (Document → Chunk → NEXT_CHUNK → FROM_DOCUMENT).

#### Innovations

1. **Hierarchical Leiden + bottom-up community summaries** — pre-compute once, reuse per query.
2. **Dual-level retrieval** — one-pass vector search replaces GraphRAG's expensive community traversal.
3. **VectorCypher** — the killer pattern: vector search returns chunks; Cypher fans out 1-3 hops and returns both chunks and triples as textualized subgraph.
4. **Paragraph-semantic chunking** — aligns to heading/paragraph boundaries (preserves table headers), the right pattern for code (`fn`/`class`/`module` boundaries).
5. **SchemaFromTextExtractor** — bulk ontology discovery from a sample.

#### What LeanKG should learn

1. **VectorCypher pattern = the canonical retrieval primitive** — `find_with_neighbors(query, depth, edge_types)` runs vector retrieval then bounded edge expansion, replacing `semantic_search → get_call_graph` dance.
2. **Precomputed community summaries** — extend `get_cluster_skill` to generate per-cluster summaries at index time.
3. **SchemaFromTextExtractor-style bulk ontology discovery** — `bulk_ontology_discover` samples N files, extracts common entity/relationship types via LLM, offers YAML candidates.
4. **KV_STORAGE for LLM response cache** — avoid re-running semantic_search for identical queries.

### 3.14 Zep / Graphiti `[NEW 2026-08-02 sync]`

#### Confirmed strengths

Bi-temporal knowledge graph. 3 subgraphs per user: episode (raw messages), semantic entity (entities + relationships), community (clusters). Every edge carries `valid_from`, `valid_to`, `observed_at`, `recorded_at`. Old facts are **invalidated, not deleted**. Hybrid retrieval (vector cosine + BM25 + graph BFS) reranked with RRF / MMR / episode-mentions / node-distance / cross-encoder. Sub-200ms p95 at scale; **no LLM in retrieval loop**.

#### Performance

94.8% on DMR benchmark (vs MemGPT 93.4%); +18.5% on LongMemEval with 90% lower latency.

#### MCP server

`getzep/graphiti/blob/main/mcp_server/` — Episode/Entity/Group management + semantic + hybrid search exposed over MCP.

#### What LeanKG should learn

1. **Bi-temporal edges** — LeanKG already supports `valid_from`/`valid_to` (mentioned in `temporal_query` US-MP-01). Adding `observed_at`/`recorded_at` would let agents answer "what did this import look like before refactor X?" with full provenance.
2. **No-LLM-in-retrieval-loop principle** — validates LeanKG's choice to do query expansion in MCP server without calling an LLM per query.
3. **MCP server template** as a reference for `src/mcp/server.rs`.

### 3.15 Sourcegraph / SCIP + CodeSee `[NEW 2026-08-02 sync]`

#### SCIP

Language-agnostic index format for definitions, references, and symbol identities. Spec at `scip-code/scip` (moved from `sourcegraph/scip` v0.7.0). 11 languages GA (C/C++, C#, Go, Java/Kotlin/Scala, Python, Rust, TypeScript, Ruby, …). SCIP size ~4-5× smaller than LSIF. **LSIF deprecated 2023, removed at Sourcegraph 4.6**. SCI indexers: `scip-java`, `scip-typescript`, `scip-clang`, `scip-ruby`, `scip-python`, `scip-dotnet`, `scip-dart`, `scip-php`, `rust-analyzer --lsif` (SCIP variant).

Sourcegraph MCP server lives at `sourcegraph/sourcegraph/.../internal/mcp/` with endpoints `/.api/mcp`, `/.api/mcp/all`, `/.api/mcp/deepsearch`. Catalog: `list_files`, `list_repos`, `read_file`, `keyword_search`, `nls_search`, `evaluator`, `find_references`, `go_to_definition`, `commit_search`, `diff_search`, `compare_revisions`, `get_contributor_repos`, `code_finder`, `deepsearch`, `deepsearch_read`.

#### CodeSee

`.codesee.json` config schema for monorepo / Python sys.path / external packages. Four map types: **Codebase Map** (files + folders + imports), **Review Map** (added/removed/edited/unchanged coloring), **Service Map** (services + external APIs + implicit DB/S3 via OTLP gRPC `in-otel.codesee.io:443/v1/traces`), **Function Map** (functions/classes + call/reference/definition). MIT analyzers: `codesee-action`, `codesee-deps-go`, `codesee-deps-dotnet`. **Acquired by GitKraken Aug 2024**, product sunsetting.

#### What LeanKG should learn

1. **SCIP as authoritative semantic overlay** — Tier 1 in the evidence precedence (§6.2), imported per-language as compiled.

2. **`.leankg.yaml` steer file** — mirror `.codesee.json`'s contract; declare `priority_paths`, `ignore_paths`, language-specific extractor flags.

3. **OTel service-map enrichment** — optional layer for service-graph (`get_service_graph`) that pairs static call edges with runtime trace data.

4. **Lesson from CodeSee sunset** — don't depend on a single roadmap vendor for distributed indexers; community SCI modules are safer than proprietary analyzers.

#### Avoid copying

- Don't replicate Sourcegraph's monolithic search index. LeanKG's local-first + CozoDB is the right niche.

### 3.16 Aider repo-map + ctags/gtags/cscope `[NEW 2026-08-02 sync]`

#### Aider repo-map

Aider sends a concise map of important symbols and signatures, ranks using a **weighted personalized PageRank** over the call graph, and **binary-search-fits** the map to a token budget. Output is renderable as ASCII or JSON. Source: `Aider-AI/aider/repomap.py`.

#### ctags / gtags / cscope

| Tool | Storage | Format | Audience |
|---|---|---|---|
| **ctags** (`universal-ctags/ctags`) | line-oriented `tags` file | per-line `name<TAB>file<TAB>cmd` | editors, IDEs, wrappers |
| **gtags** (`syohex/gtags`) | SQLite + inverted index + saved tag literal | SQL, string match | shell, large multi-language repos |
| **cscope** (`crossbeam-chris/cscope`) | per-project C/C++ interactive shell database | line-oriented + symbol cross-ref | kernel / embedded + interactive session |

#### Modern wrappers

- `ctags-mcp` — wraps ctags over MCP.
- `Repograph` — Rust wrapper with HTTP/MCP.
- `CodeQuery` — graph query over ctags.
- `Clink` — polyglot ctags backend.

#### Innovations from the cluster

1. **Weighted personalized PageRank** for "what to look at first" — Aider's start nodes = recently edited files; PageRank dampens 0.85.
2. **Binary-search budget fit** — given a token limit, find the largest set of symbols that fits and rank by PageRank.
3. **Persistent index shelling** — ctags/gtags are useful as a *fast layer* under a richer graph; LeanKG could expose its own data as a tags file for editor integration (`leankg tags --format=ctags`).

#### What LeanKG should learn

1. **Weighted personalized PageRank as orientation layer** — LeanKG has clusters via Leiden; PageRank over the call graph (with priors on recently-touched files) gives a "what matters most here" tool.
2. **Binary-search budget fit** — `get_hotspots` (PageRank) + `get_orientation` (deterministic map) should both implement binary-search token allocation.
3. **Editor integration via ctags format** — `leankg tags --format=ctags` gives every existing editor immediate value.

#### Avoid copying

- Don't replace the typed graph with a flat tag file. Use ctags/gtags as a fast edge layer, not the model.

---

## 4. Comparative capability matrix

### 4.1 Cross-cutting dimensions

| Dimension | LeanKG | GitNexus | LeanCTX | Codanna | Context7 | DeepWiki | TencentDB | Letta | Mem0 | Cognee | GraphRAG | LightRAG | Neo4j GraphRAG | Zep | Aider | CodeGraph | CodeGraphContext |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Core identity | Code/team KG | Code/process KG | Context layer | Code KG | Library-doc oracle | Wiki generator | Memory hub | Stateful agent memory | Personalization layer | AI memory platform | Hierarchical RAG | Simple/fast RAG | GraphRAG SDK | Temporal memory | Repomap | Native code KG | Multi-store code KG |
| Local-first | Strong | Strong | Strong | Strong | No (cloud) | No (cloud) | Local + cloud | Strong | Both | Both | Library | Strong | Library | Both | Strong | Strong | Strong + remote |
| Semantic truth | Tree-sitter + optional LSP/embeddings | Tree-sitter + optional PDG | Tree-sitter + LSP | Tantivy BM25 + embeddings | N/A | Closed | TCVDB + RRF | Embeddings | BM25 + vec + entity | LanceDB + Kuzu | Parquet + Leiden | NanoVectorDB + NetworkX | Neo4j vector + graph | Vector + BM25 + BFS | PageRank + tree-sitter | Native parser + heuristics | Tree-sitter + SCIP |
| Graph provenance | Strong labels | Confidence/scoring | Context proof | Role filter | n/a | Citations | Asset/source metadata | OS blocks | Entity graph | OWL `ontology_valid` | Leiden communities | Dual-level keys | VectorCypher | Bi-temporal | n/a | Heuristic metadata | Varies |
| Typed program depth | Medium, uneven | High in PDG mode | Medium | Medium | n/a | n/a | Depends on CodeGraph | n/a | n/a | n/a | LLM-extracted | LLM-extracted | LLM-extracted | n/a | Symbol-level | Medium + bridges | Stronger where SCIP exists |
| Context compilation | Partial/orchestrate | High-level smart tools | Core product | `semantic_search_with_context` | `tokens:` budget | Wiki pages | Asset loadout | Self-edited | 3-step loop | `cognify` | Retrieve modes | 4 modes | VectorCypher | Hybrid | Ranked map | Curated queries | Basic |
| Reversible compression | Session refs, partial | Token budgets | Strong CCR | Limited | Tokens param | Markdown wiki | Raw-to-layer lineage | Context overview | n/a | n/a | n/a | n/a | n/a | WAL | Budget-fit | Limited | Limited |
| Team governance | Incidents/env/knowledge | Repo policies/groups | Policies/budgets | n/a | n/a | n/a | Strong ACL/loadout | n/a | 4-D scoping | Migration module | n/a | n/a | n/a | n/a | n/a | Limited | Limited |
| Cross-repo contracts | Service graph, partial | Strong groups/contracts | Multi-root/providers | n/a | n/a | Per-repo | Asset binding | n/a | n/a | Import from Mem0/Zep | n/a | n/a | External providers | n/a | n/a | Limited | Multi-repo |
| Hybrid retrieval | semantic + ontology | **RRF: BM25 + vec + graph** | **RRF: BM25 + vec + graph** | BM25 + embeddings | n/a | Closed | **RRF: BM25 + vec + item** | Embeddings | **BM25 + vec + entity** | LanceDB + Kuzu | Hierarchical | **Dual-level** | VectorCypher | **RRF: vec + BM25 + BFS** | PageRank | Hybrid | Multi-store |
| Best lesson | Domain/team graph | Precompute + small workflows + PDG | Action-verb union + PageRank | Self-correlating tools | Tokens param | Wiki as stable unit | Mermaid canvas + pyramid | Context overview | 3-signal fusion | OWL + MD5 | Hierarchical | Paragraph chunking | VectorCypher | Bi-temporal | PageRank orientation | Speed + freshness | SCIP/interchange |

### 4.2 Cross-cutting capability patterns

Seven patterns appear across ≥3 of the surveyed systems and are the strongest signals for what an "evidence compiler for software agents" should ship:

1. **Precomputed Processes / Clusters / Skills** — GitNexus, Microsoft GraphRAG, DeepWiki, LeanKG already.
2. **Hybrid RRF retrieval** — GitNexus, LeanCTX, TencentDB, Mem0, LightRAG, Zep. LeanKG's `kg_semantic_context` is close; explicit BM25 + graph proximity is the next step.
3. **Self-correlating tool responses** — Codanna, GitNexus, LeanCTX (LITM-aware positioning). One tool returns enough to act.
4. **MCP Resources + Prompts** — GitNexus, DeepWiki, LeanCTX. Stable retrieval units via URI scheme.
5. **Action-verb union over tool names** — LeanCTX, GitNexus. Fewer top-level tools, richer intent args.
6. **Per-tool token budget** — Context7, GitNexus, LeanCTX. Client-told budget.
7. **Governed memory assets** — TencentDB, LeanCTX, Cognee. Lifecycle metadata (draft/reviewed/active/deprecated).

---

## 5. Product strategy: what LeanKG should become `[UNCHANGED]`

### 5.1 Positioning

Recommended category:

> **Repository evidence compiler and team knowledge graph for software agents.**

Recommended promise:

> Given a task, LeanKG returns a bounded evidence package containing the right symbols, source slices, dependency paths, tests, docs, decisions, and freshness/provenance metadata—then lets the agent expand any omitted evidence on demand.

This is stronger than "85+ MCP tools," "semantic search," or "code graph." It defines the outcome and incorporates LeanKG's unique code + team knowledge capabilities.

### 5.2 The three product planes

#### Evidence plane

- Code and document entities.
- Typed relationships and semantic overlays.
- Tests, routes, APIs, contracts, services, ownership, incidents, environments, PRDs, and decisions.
- Version, branch, environment, freshness, provenance, and confidence.

#### Context plane

- Intent classification.
- Candidate generation.
- Graph expansion.
- Reranking and diversity.
- Hierarchical summaries.
- Token allocation.
- Recoverable source slices.
- Context receipt/proof.

#### Learning plane

- Query outcomes.
- Selected/opened/patched files.
- Successful and failed paths.
- Workflow proposals.
- Human review/promotion.
- Retention, supersession, and rollback.

LeanKG already contains pieces of all three; the opportunity is to make them coherent.

---

## 6. Recommended architecture

### 6.1 Reliable serving kernel `[UNCHANGED]`

Before new intelligence, establish a server execution contract:

```text
MCP request
  -> authenticate / project allowlist
  -> resolve authoritative project + branch + environment
  -> classify tool cost/read-write class
  -> acquire shared GraphEngine handle
  -> run blocking graph work in bounded blocking pool
  -> enforce deadline + cancellation/budget
  -> attach freshness + graph version
  -> encode/truncate/recovery-handle response
  -> emit metrics and query trace
```

Required invariants:

- One process-wide engine per canonical database path.
- `project` is authoritative; file paths are resolved inside it.
- Every query has a cost class, deadline, result budget, and mega-graph policy.
- Health checks do not share blocking execution capacity with graph scans.
- Writes are serialized and declared in tool metadata, not a manually maintained list.
- Responses identify graph version, indexed commit, dirty files, and degraded modes.

### 6.2 Stable identity and semantic evidence `[EXPANDED]`

Adopt a canonical symbol identity inspired by SCIP/Kythe:

```text
workspace / repository / revision / language / package / symbol / signature
```

Each relation should retain:

- Extractor.
- Extractor version.
- Resolution method.
- Confidence.
- Source span(s).
- Validity interval: `observed_at`, `recorded_at`.
- Environment/branch.
- Optional evidence payload.

Evidence precedence:

1. **Compiler/SCIP/LSP typed resolution** (Tier 1).
2. **Framework-specific deterministic extraction** (Tier 2).
3. **Tree-sitter structural extraction** (Tier 3).
4. **Name/file heuristic resolution** (Tier 4).
5. **LLM inference** (Tier 5, always labeled, never silently overrides).

### 6.3 Overlay model `[UNCHANGED]`

Keep the base graph compact; add optional overlays:

- **Syntax overlay:** files, symbols, declarations, imports.
- **Semantic overlay:** definitions, references, types, inheritance, implementations.
- **Flow overlay:** calls, routes, events, processes, service calls.
- **PDG overlay:** control/data dependence, sources/sinks, taint findings.
- **Delivery overlay:** tests, diffs, PRs, CI, ownership, incidents, environments.
- **Knowledge overlay:** docs, ADRs, PRDs, workflows, lessons, skills.
- **Temporal overlay:** valid-from/to, supersession, branch/release/deployment.

### 6.4 Unified context compiler `[EXPANDED — three-signal fusion]`

A single high-level operation should accept:

```text
intent/task
project + branch + environment
budget
mode: orient | explain | change | debug | review | test | security | orientation
freshness requirement
```

Pipeline:

```text
1. Resolve concepts, aliases, symbols, paths, routes, and requirements.
2. Generate candidates from FOUR signals:
   - Lexical (BM25 over names + signatures + docstrings)
   - Semantic (vector cosine over symbols + docs)
   - Ontology (concept nodes → code_refs expansion)
   - Graph (PageRank-personalized seeds, BFS in/out within budget)
3. Fuse via RRF (Reciprocal Rank Fusion) with deterministic per-task weights.
4. Expand direct graph evidence appropriate to task mode (depth-bounded).
5. Add tests, docs, configuration, contracts, decisions, and incidents.
6. Rerank with cross-encoder when embeddings enabled + task priors.
7. Diversity + super-hub penalty.
8. Select hierarchical context under a token budget.
9. Emit evidence package + exact expansion handles + receipt.
```

Suggested initial scoring features:

- Lexical rank
- Semantic rank
- Ontology/concept match
- Exact symbol/type match
- Graph distance and direction
- Cluster/process membership
- Test/doc/requirement proximity
- Changed-file and branch relevance
- Freshness and confidence
- Prior query usefulness
- Duplicate and utility-hub penalties

Adopt weighted personalized PageRank (Aider-style) for the orientation layer; binary-search token allocation; render ASCII + JSON to MCP.

### 6.5 Context package schema `[UNCHANGED]`

A package should contain:

```yaml
identity:
  project: ...
  revision: ...
  branch: ...
  environment: ...
intent:
  mode: change
  normalized_query: ...
budget:
  requested_tokens: 8000
  delivered_tokens: 7610
orientation:
  repository_summary: ...
  clusters: ...
evidence:
  - symbol: ...
    file: ...
    lines: ...
    why_selected: ...
    relations: ...
    confidence: extracted
    freshness: current
    expand_ref: ...
constraints:
  - decision: ...
  - workflow: ...
  - incident: ...
verification:
  tests: ...
  risks: ...
receipt:
  graph_version: ...
  source_hashes: ...
  omitted_candidates: ...
  stale_files: ...
```

### 6.6 Hierarchical summaries `[EXPANDED]`

Use deterministic graph structure plus optional reviewed summaries:

```text
workspace
  -> repository/service
    -> package/cluster
      -> file
        -> symbol signature
          -> source body
```

Every summary must include source element IDs, indexed revision, source hashes, generation method, and freshness. Summaries must never override exact source.

Adopt Microsoft GraphRAG's bottom-up precomputed community summaries — extend LeanKG's `get_cluster_skill` to generate per-cluster summaries at index time, persisted to MCP Resources (`leankg://repo/{name}/clusters/{id}/doc.md`).

### 6.7 Governed memory assets `[EXPANDED — ontology_valid flag + lifecycle]`

Unify LeanKG's knowledge, ontology, sessions, lessons, cluster skills, PRDs, and reports under an asset lifecycle:

- `draft`, `reviewed`, `active`, `deprecated`, `superseded`, `rejected`.
- `private`, `team`, `restricted`, `agent` visibility.
- Owner/team/agent bindings.
- Source IDs and evidence.
- Validity interval (`valid_from`, `valid_to`, `observed_at`, `recorded_at`).
- Version and supersession links.
- Hit count and last-used timestamp.
- Retention/pinning rules.
- **`ontology_valid: true|false`** flag (Cognee pattern) for any node referencing an ontology concept.

Automatic learning should create proposals, not silently rewrite authoritative workflows.

### 6.8 Freshness contract `[EXPANDED with bi-temporal]`

Extend `temporal_query` (US-MP-01) with bi-temporal edges:

```text
relationship {
  valid_from: <epoch>          # when fact became true in source
  valid_to: <epoch|never>      # when superseded
  observed_at: <epoch>         # when source stated it
  recorded_at: <epoch>         # when indexer ingested it
  invalidated_by: <relationship_id>
}
```

Old facts are **invalidated, not deleted** (Zep/Graphiti pattern). Old state remains queryable:

```sql
SELECT * FROM temporal_query(at=1718000000, file='src/foo.rs')
```

This enables "what did this import look like before refactor X?" with full provenance.

### 6.9 Mermaid symbolic canvas + offload `[NEW]`

Extend the existing `session_recall` (US-SM-01) pattern. When a tool result would exceed a token budget, render a Mermaid call-graph of the file (symbols + edges) into context, with `node_id` for drill-down to source via `session_recall` pattern.

```text
leankg_render_canvas({
  file: "src/mcp/server.rs",
  budget: 2000,
  format: "mermaid",
  detail: "signature-only"
})
```

Receives a `node_id` (`offload-007`); full content lives at `.leankg/sessions/<session>/refs/offload-007.md`.

### 6.10 VectorCypher-style seed-and-fan-out `[NEW]`

Replace the current `semantic_search → get_call_graph` two-step dance with a single high-level tool:

```text
find_with_neighbors({
  query: "user authentication middleware",
  depth: 2,
  edge_types: ["calls", "imports", "tested_by"],
  direction: "both",
  budget: 4000
})
```

Internally: vector retrieval (top-K), then bounded edge expansion (Neo4j `VectorCypherRetriever` pattern). Same shape as LeanKG's own `kg_semantic_context`, but exposed as one stable MCP tool.

### 6.11 Precomputed Leiden Processes `[NEW]`

Adopt GitNexus's Process precomputation at index time. Inputs: entry points (heuristic or explicit). Outputs: named processes (entry → leaf call chains) with confidence + evidence.

```text
process_id: proc-007
name: "handle_user_login"
entry: src/auth/handlers.rs::login
chain:
  - src/auth/handlers.rs::login
  - src/auth/jwt.rs::issue_token
  - src/db/users.rs::find_by_email
  - src/db/users.rs::update_last_login
leaf: src/db/users.rs::update_last_login
confidence: 0.92
evidence: <relationship IDs>
```

MCP: `get_process(name="handle_user_login")` returns the entire chain in one call.

### 6.12 Weighted personalized PageRank orientation `[NEW]`

Adopt Aider's orientation layer. Implement `get_orientation()`:

```text
get_orientation({
  budget: 1500,
  priors: ["recently_changed_files"],   # explicit feature
  personalization: "combined",          # or "impact", "fan_in", "test_coverage"
  format: "ascii"                       # or "json", "mermaid"
})
```

Internally: weighted personalized PageRank over the call graph (damping 0.85), then binary-search the largest subset that fits the budget. Aider proves this compresses to <2k tokens for 1M+ LOC repos.

---

## 7. Prioritized roadmap `[UNCHANGED sequencing, NEW items]`

### P0 — Reliability and trust (0–4 weeks)

#### P0.1 Correct project routing

Make `project` authoritative and resolve `file`/`path` relative to it. Reject path escapes and ambiguous combinations. This directly addresses `docs/reports/root-cause-mcp-88-tool-validation-workspace-be-2026-08-02.md:19`.

#### P0.2 Single graph handle per DB path

Replace cache-clear/reopen behavior with a process-wide engine registry and explicit lifecycle. Separate reader and writer processes if RocksDB constraints require it.

#### P0.3 Bounded execution

- Run blocking DB operations in a bounded blocking pool.
- Add read/write timeouts.
- Add cancellation and concurrency limits.
- Keep health/metrics independent.
- Return `timeout`, `retryable`, `suggested_narrowing`, and `cost_class`.

#### P0.4 Universal query budgets

Every graph-wide operation must declare one of:

- Keyed.
- Frontier-local.
- Precomputed.
- Paginated.
- Explicitly refused on mega-graphs.

Replace full-count guards with cached inventory metadata.

#### P0.5 Freshness and degradation headers (new candidate)

Attach stale files, graph revision, embedding readiness, and missing-data reasons to results. This extends the existing P0 reliability work and should receive a PRD/tracker ID before implementation.

#### P0.6 Validation gate (new candidate)

Release only when:

- All registered tools return or refuse within budget on fixture and mega graphs.
- A 5x/10x mixed request storm leaves health responsive.
- Multi-project routing tests prove isolation.
- Lock and cancellation tests pass.

### P1 — Unified context compiler and small default surface (1–3 months)

#### P1.1 `compile_context`

Add a high-level task-to-evidence package tool with modes for orient, explain, change, debug, review, and test.

#### P1.1a Default agent surface ~8–12 workflow tools `[UNCHANGED]`

`status/overview`, `search/discover`, `compile_context`, `explain_symbol`, `impact/change_context`, `trace_path/process`, `verify_tests/docs/requirements`, `recall/expand_evidence`, `report_outcome`. Retain expert primitives in advanced profile.

**Adopt LeanCTX action-verb unions** — `compile_context` can replace several. Pair with `LEANKG_PROFILE=minimal|standard|power` env var.

#### P1.1b Trait-based tool registry drift gate `[NEW]`

LeanCTX's `tool_registry_complete.rs` is a CI gate that ensures every registered tool has a schema + handler + test. Adopt patterned schema-co-located-with-handler to prevent schema drift.

#### P1.1c Server-side next-step hints `[NEW]`

GitNexus appends a one-line hint to every tool result guiding the agent's next call. Cheap to implement (`src/mcp/server.rs`); big UX win.

#### P1.1d Read-only MCP mode `[NEW]`

`LEANKG_MCP_READ_ONLY=1` strips `cypher`, `rename`, `delete_knowledge`, `add_ontology_*` from the tool list. Borrowed from GitNexus.

#### P1.2 Recoverable output

Store omitted source and oversized payloads in the existing session/content store and return exact recovery handles.

#### P1.3 Retrieval trace

Provide an optional `why` section containing candidate generators, scores, selected graph paths, exclusions, token allocation, and freshness.

#### P1.4 Hierarchical repository maps

Generate compact maps at repository, cluster, file, and symbol-signature levels. Use query-aware graph ranking and dynamic budgets. **Adopt Aider weighted personalized PageRank** for orientation; **binary-search token allocation**.

#### P1.5 Deterministic hybrid ranking `[EXPANDED — three-signal]`

Implement RRF across lexical, semantic, concept, and graph candidate lists, then apply task priors, freshness, diversity, and super-hub penalties. **Three signals minimum**: BM25 + vector + graph proximity (Cognee Mem0 Zep LightRAG consensus).

#### P1.5a Mermaid symbolic canvas + offload `[NEW]`

`leankg_render_canvas` for file-level call graphs over budget; `session_recall` for drill-down.

#### P1.5b VectorCypher-style `find_with_neighbors` `[NEW]`

One tool that replaces `semantic_search → get_call_graph`.

### P1 — Evaluation and observability (parallel, 1–3 months)

#### P1.6 Retrieval benchmark

Build a LeanKG benchmark derived from RepoQA:

- Natural-language symbol search.
- Exact definition/reference lookup.
- Caller/callee and shortest-path tasks.
- Test/doc/requirement retrieval.
- Architecture/cluster questions.
- Decoy modules and duplicate symbols.
- Stale-index and branch-mismatch cases.

Metrics:

- Recall@k, MRR, nDCG.
- Path correctness.
- Citation/provenance correctness.
- Freshness error rate.
- Context tokens and time-to-context.
- Tool-call count/error rate.

#### P1.7 End-to-end A/B

Run fixed-model comparisons:

- Native grep/read.
- LeanKG low-level tools.
- LeanKG compiled context.
- Compiled context with/without embeddings.
- Compiled context with/without memory.

Use real issue tasks where possible. Measure success, patch precision, tests, cost, latency, and context size.

#### P1.8 Tool ergonomics evaluation

Record which tools agents choose, invalid calls, redundant chains, abandoned results, and patch relevance. Use held-out tasks before changing names/descriptions.

#### P1.9 RULER / Lost-in-the-Middle regression tests `[NEW]`

Adopt RULER-style multi-hop needle aggregation tests and LITM positional checks on compile_context output. Prevents context-length regression.

### P2 — Semantic depth and cross-repo intelligence (3–6 months)

#### P2.1 SCIP import

Support SCIP documents as typed semantic overlays. Begin with TypeScript, Go, Rust, Java/Kotlin, and C/C++ where official indexers are available. Tier-1 evidence precedence.

#### P2.2 Deepen stable identity and alias resolution

Extend the existing `US-GE-03` / `FR-GE-03` cross-alias resolver across paths, signatures, packages, generated code, renamed symbols, languages, and compiler/SCIP identities. Avoid first-short-name wins.

#### P2.3 Framework bridge SDK

Create a small extractor interface for synthetic edges with explicit `synthesized_by`, confidence, evidence, and tests. Initial bridges:

- OpenAPI/GraphQL/protobuf clients to handlers.
- Frontend fetch calls to backend routes.
- Kafka/event producers to consumers.
- Swift/Objective-C and JNI/FFI.
- MCP tool definitions to handlers.
- Database schemas/migrations to ORM consumers.

#### P2.4 Process intelligence

Precompute common execution flows from entry points through calls, routes, services, and stores. Return them as named processes with evidence and confidence. **Adopt GitNexus Process pattern** (§6.11).

#### P2.5 Optional PDG/security overlay

Pilot statement-level control/data dependence for one language family. Target concrete workflows: taint explanation, API shape impact, and security review.

#### P2.6 Cross-repo contract registry

Index schemas and exported interfaces, link producers/consumers across mounted projects, and report stale or breaking contracts. **OTel service-map enrichment** (CodeSee pattern) is optional layer.

#### P2.7 Extraction quality report

Publish machine-readable language/relation coverage and confidence. Replace the vague "depth varies" note at `README.md:425`.

#### P2.8 OWL ontology resolver + `ontology_valid` flag `[NEW]`

Adopt Cognee's pattern: fuzzy-match LLM-extracted entities against ontology classes (0.80 cutoff). Tag every node with `ontology_valid: true|false`. Don't block on missing ontology.

#### P2.9 Bulk ontology auto-discovery `[NEW]`

`bulk_ontology_discover` samples N files, extracts common entity/relationship types via LLM (Neo4j `SchemaFromTextExtractor`), offers YAML candidates.

### P2 — Team memory and governance (3–6 months)

#### P2.10 Memory asset lifecycle

Add owner, status, visibility, version, validity, source IDs, and agent bindings to durable knowledge artifacts.

#### P2.11 Review and promotion

Create proposal workflows for:

- Query lessons.
- Repeated successful tool traces.
- Generated skills.
- Ontology concepts/workflows.
- Cluster summaries.

#### P2.12 Agent loadouts

Use existing personas and clusters to provide ACL-aware role context for architect, SRE, mobile, backend, security, and reviewer agents.

#### P2.13 Retention and conflict handling

Deduplicate, supersede, expire, pin, and garbage-collect session refs and learned artifacts. **Contradiction detection** (Cognee pattern).

### P3 — Distribution and ecosystem (6–12 months)

#### P3.1 Portable context packs

Export deterministic, relative-path, content-hashed packages with graph slices, summaries, evidence, source refs, tests, and receipts.

#### P3.2 Merge-safe snapshots

Offer an opt-in Git merge driver for portable snapshots; never merge live DB files.

#### P3.3 Productize repository skill export

Build on the shipped `US-GN-07` / `get_cluster_skill` primitive by generating all cluster/area skills in one index-time export, including entry points, key files, processes, cross-area dependencies, and usage guidance.

#### P3.4 Thin SDK/client

Publish a stable transport contract for Rust/TypeScript/Python clients without embedding the graph engine.

#### P3.5 Signed receipts

Optionally sign context packs and record index revision, source hashes, selection policy, and token accounting.

#### P3.6 Selective connectors

Prioritize software-delivery sources—issue trackers, CI, schemas, ADRs, runbooks, and postmortems—before generic image/video ingestion.

#### P3.7 ctags/gtags fast edge layer `[NEW]`

`leankg tags --format=ctags` exports LeanKG data as a `tags` file for every existing editor. Compete with ctags/gtags on coverage; coexist on latency.

#### P3.8 MCP server catalog and per-client compatibility table `[NEW]`

Adopt Context7's per-client install table (Cursor, Claude Code, Codex, Windsurf, etc.). Same one-page format.

---

## 8. Highest-value product ideas `[UNCHANGED + 3 new]`

### 8.1 "Change Context" as the flagship workflow

Input: task/issue plus optional changed files.  
Output: relevant implementation symbols, upstream/downstream impact, tests and fixtures, API/schema/contracts, decisions/workflows/incidents/environment conflicts, proposed verification commands, freshness/provenance receipt.

### 8.2 "Why this result?"

For every selected item: exact match / semantic match / ontology concept / graph path; relationship direction and evidence; confidence/provenance; freshness; why close alternatives were excluded.

Adopt VectorCypher-style fan-out (P1.5b) so the `why` section can be rendered deterministically.

### 8.3 "Repository skill export"

Turn Leiden communities into agent skills. Each skill contains: scope, entry points, public APIs, key files/tests, named processes, cross-cluster tunnels, known incidents/decisions, and which LeanKG workflow tool to call next.

Pre-generate all cluster skills at index time (GitNexus pattern).

### 8.4 "Contract radar"

Index and connect: OpenAPI, GraphQL, protobuf/gRPC, events, database schemas, generated SDKs, MCP tools. Report producer/consumer drift across repositories/environments.

### 8.5 "Context receipt"

Every compiled package includes: selected/omitted tokens, graph revision, dirty files, embedding coverage, confidence distribution, missing overlays, recovery handles.

### 8.6 "Orientation layer" (NEW)

Given a token budget, return the highest-ranked orientation map of the codebase — Aider-style weighted personalized PageRank + binary-search budget fit. Output: signatures, not bodies, in a single call. Pairs with `compile_context`'s deep mode.

### 8.7 "Process trace" (NEW)

Given an entry point, return the full execution flow as a named process with confidence and evidence. Adopts GitNexus's Process precomputation. Lets an agent ask "what happens when X is called?" in one call.

### 8.8 "Recoverable drill-down" (NEW)

For any tool result that exceeded budget, render a Mermaid call-graph inline and emit a `node_id` for drill-down via `session_recall`. Adopts TencentDB Agent Memory's symbolic canvas.

---

## 9. What not to build now `[EXPANDED]`

1. **A general chat-persona memory competitor.** Keep memory anchored to software delivery.
2. **A universal agent harness.** Integrate with Cursor, Claude, OpenCode, Codex, and others; do not own their planning/execution loop.
3. **Many graph storage backends.** Finish reliable local/remote Cozo operation before adding alternatives.
4. **A mandatory LLM extraction pipeline.** Deterministic sources must remain primary; LLM-derived knowledge should be optional and reviewed.
5. **A 100-tool default wall.** Preserve advanced capability but shrink the default decision surface.
6. **A language-count race.** Deepen semantic quality and publish relation-level grades.
7. **A full request proxy or addon marketplace.** LeanCTX already occupies the broad context-engineering-with-addons niche; LeanKG should expose clean APIs and context packs rather than reimplement its proxy and addon layers.
8. **Generic multimodal ingestion before delivery artifacts.** CI, schemas, PRDs, ADRs, incidents, and contracts have higher software-agent value.
9. **Benchmarks based only on synthetic token savings.** Measure retrieval correctness and end-to-end task outcomes.
10. **Graph centrality as relevance.** Apply task relevance, diversity, and super-hub penalties.
11. **A proprietary closed-source analyzer (CodeSee lesson).** LeanKG should not become a single-vendor dependency for distributed indexers.
12. **A second-generation memory store (Cognee/Letta lesson).** LeanKG's CozoDB + RocksDB is the local analog of Cognee's single-Postgres stack. Finish it before adding alternatives.
13. **Bidirectional LLM extraction at index time.** Both Letta and Cognee proved LLM-in-extraction is too slow for code; tree-sitter + optional SCIP is the right answer.

---

## 10. Success metrics `[EXPANDED]`

### Reliability

- 100% registered tools return/refuse within declared budget on mega-graph validation.
- Health remains responsive under mixed request storms.
- Zero wrong-project responses in multi-project tests.
- Zero same-process RocksDB double-open failures.

### Retrieval quality

- RepoQA-style target recall@10 and MRR by language.
- Correct caller/callee/path answers.
- Correct test/doc/requirement retrieval.
- Provenance and citation correctness.
- Stale-result rate.
- Three-signal RRF beats single-signal baseline (A/B).

### Agent outcomes

- Task success versus native search baseline.
- Patch precision and unintended edit rate.
- Test pass rate.
- Tool calls/task.
- Input tokens/task.
- Time-to-first-relevant-source and time-to-resolution.

### Product usability

- Install-to-first-use time.
- Percentage of sessions using high-level context workflows.
- Invalid/redundant tool-call rate.
- Percentage of graph answers followed by raw fallback reads.
- Query-outcome usefulness rate.

### Team knowledge

- Reviewed versus draft assets.
- Workflow reuse rate.
- Stale/superseded knowledge rate.
- Cross-repo contract drift detected before merge.
- Mean time to restore task context across sessions.

### `[NEW]` Orientation and process quality

- `get_orientation` returns Aider-equivalent representation at <2k tokens for 1M+ LOC repos.
- `get_process(name=X)` covers ≥90% of declared entry points.
- `find_with_neighbors` correctly surfaces test/doc edges at depth 2.

### `[NEW]` Bi-temporal correctness

- 100% of invalidated edges retain provenance via `observed_at` / `recorded_at`.
- `temporal_query(at=…)` returns consistent results across 50 sampled refactor histories.

---

## 11. Risks and mitigations `[EXPANDED]`

| Risk | Consequence | Mitigation |
|---|---|---|
| Tool-surface expansion | Agent confusion and schema overhead | Default workflow profile; advanced tools on demand |
| Stale graph confidence | Incorrect changes | Freshness contract, dirty-file warnings, direct-read fallback |
| Heuristic edge overreach | False impact paths | Evidence precedence, provenance, confidence, `explain_edge` |
| LLM summary drift | Incorrect architecture understanding | Source hashes, validity, reviewed summaries, exact-source fallback |
| Reranker opacity | Silent retrieval regression | Deterministic baseline, retrieval trace, held-out benchmark |
| Centrality bias | Utility hubs dominate context | Query/task features, MMR/diversity, hub penalty |
| PDG cost | Indexing/storage blow-up | Optional overlay, selected languages/workflows, measured gate |
| Memory pollution | Bad lessons compound | Draft/review lifecycle, source IDs, supersession, retention |
| Cross-repo data leakage | Privacy/security failure | Project allowlists, ACL-aware discovery, agent loadouts |
| Portable artifact conflicts | Broken team snapshots | Deterministic schema, union-aware merge driver, live DB excluded |
| Vendor benchmark imitation | Misleading product claims | Reproduce locally, label vendor-reported metrics |
| `[NEW]` Single-vendor dependency | Roadmap hostage (CodeSee lesson) | Prefer community SCI indexers; build adapters, not cores |
| `[NEW]` Tool schema drift | Silent registration mismatches | Trait-based registry + CI drift gate (LeanCTX pattern) |
| `[NEW]` LLM-in-extraction latency | Index time blows up (Cognee/Letta lesson) | Tier-5 evidence only; deterministic extraction primary |
| `[NEW]` Context-window regression | Retrieval gains silently lost | RULER + LITM regression tests in CI |

---

## 12. Proposed implementation epics `[UNCHANGED + 1 new]`

### Epic A — Reliable MCP kernel

**Outcome:** Every request is project-correct, deadline-bound, scan-safe, observable, and freshness-labeled.

Acceptance criteria:

- Mixed mega-graph storm does not affect health.
- Engine registry prevents duplicate opens.
- `project` isolation tests cover relative and absolute paths.
- Tool metadata declares read/write and cost class.
- Structured errors include remediation.

### Epic B — Context compiler v1

**Outcome:** One task query returns a bounded evidence package suitable for implementation.

Acceptance criteria:

- Supports orient / change / debug / review modes.
- Uses lexical + concept + semantic + graph candidates.
- Adds tests/docs/requirements.
- RRF plus deterministic scoring.
- Exact recovery handles.
- Retrieval trace and context receipt.
- `[NEW]` `find_with_neighbors` replaces two-step `semantic_search → get_call_graph`.
- `[NEW]` `get_orientation` returns PageRank-budgeted map.

### Epic C — Retrieval evaluation platform

**Outcome:** LeanKG can prove whether a retrieval or tool change improves agent performance.

Acceptance criteria:

- RepoQA-derived multilingual suite.
- Relationship and architecture suite.
- Stale/branch/decoy cases.
- Fixed-model A/B runner.
- Dashboard/artifact with quality, latency, tokens, and errors.
- `[NEW]` RULER / LITM regression harness.

### Epic D — Semantic identity and overlays

**Outcome:** Typed evidence can override heuristics without discarding broad tree-sitter coverage.

Acceptance criteria:

- Canonical symbol identity.
- SCIP import for at least two languages.
- Extractor evidence metadata.
- Alias and duplicate-name resolution.
- Language/relation quality report.
- `[NEW]` `ontology_valid` flag on every node.
- `[NEW]` Bi-temporal edges (`observed_at`, `recorded_at`).

### Epic E — Process and contract intelligence

**Outcome:** LeanKG answers how behavior and interfaces flow across files and repositories.

Acceptance criteria:

- Named execution processes.
- API/tool/event/schema contract nodes.
- Producer/consumer cross-project edges.
- Contract drift and API impact workflows.
- `[NEW]` `get_process(name=X)` returns process trace in one call.

### Epic F — Governed learning

**Outcome:** Successful work becomes reviewed, reusable team knowledge without polluting the graph.

Acceptance criteria:

- Asset lifecycle and visibility.
- Trace-to-workflow proposals.
- Human review/promotion.
- Agent loadouts.
- Deduplication, supersession, and retention.

### Epic G — Distro and ecosystem `[NEW]`

**Outcome:** LeanKG outputs and integrations earn their place in the editor + CI + agent-tool chain.

Acceptance criteria:

- `leankg tags --format=ctags` covers ≥95% of indexed symbols.
- MCP server install table for Cursor, Claude Code, Codex, Windsurf, OpenCode.
- Per-client tester in CI.
- Portable context pack export with merge-safe driver.

---

## 13. Suggested 90-day plan `[UNCHANGED]`

This report is strategic analysis, not a replacement for the PRD tracker. Before execution, map each accepted recommendation into `docs/prd.md` and `docs/prd-task-tracker.md`. Current anchors are: Epic A → `FR-P0-MCP-RC-01..04` and `REL-P0-MCP-RC`; session recovery and governed memory → `US-SM-01..07`; entity resolution → `US-GE-03`; cluster skill export → `US-GN-07`; semantic ranking/evaluation should extend the existing `US-SEM-*` and test-validation tracks. P0.5/P0.6 and the unified compiler require new approved IDs.

The working tree currently contains unrelated in-progress changes in MCP/DB files. Their completion status was not inferred or treated as shipped; validate them against the tracker and acceptance tests before starting overlapping work.

### Days 1–30: reliability and baseline

- Close the four current MCP P0 root causes.
- Add request/tool metrics and structured errors.
- Add graph revision, stale files, and embedding readiness to responses.
- Freeze a benchmark baseline for current search, context, impact, and mega-graph behavior.

### Days 31–60: context compiler alpha

- Implement intent modes and candidate fusion.
- Add RRF and transparent graph/task scoring.
- Add tests/docs/requirements expansion.
- Add package budgeting and exact expansion handles.
- Expose a small alpha workflow surface to selected agents.
- `[NEW]` Add `find_with_neighbors` (VectorCypher-style).
- `[NEW]` Add `get_orientation` (PageRank-budgeted).
- `[NEW]` Add server-side next-step hints.

### Days 61–90: evaluation and productization

- Build RepoQA-style and relationship retrieval suites.
- Run fixed-model A/B against native search and current low-level LeanKG.
- Tune tool descriptions and ranking using held-out cases.
- Publish context receipts and a "why selected" trace.
- Generate repository-area skills from clusters as an opt-in artifact.
- `[NEW]` Add RULER + LITM regression tests.
- `[NEW]` Adopt `LEANKG_PROFILE` (minimal/standard/power) and read-only mode.

A PDG, SCIP rollout, contract registry, and governed memory asset lifecycle should start only after this 90-day gate demonstrates reliability and retrieval gains.

---

## 14. Source notes and caveats `[EXPANDED]`

1. Competitor feature counts and benchmarks evolve quickly. Tool counts should be verified against each live registry before publication.
2. **CodeGraph, Graphify, GitNexus, LeanCTX, TencentDB, Codanna, Aider performance claims are vendor-reported unless LeanKG reproduces them.**
3. GitHub star counts were observed during research but are intentionally excluded from recommendations.
4. **GitNexus is PolyForm Noncommercial**, not OSI-open-source; design research does not imply code reuse rights.
5. **CodeSee was acquired by GitKraken Aug 2024**; the standalone product is sunsetting. Design lessons only, not a roadmap dependency.
6. Graphify's code path is local, but non-code semantic extraction may use configured models/providers.
7. LeanCTX's broad scope contains useful patterns but also demonstrates the maintenance risk of becoming an all-in-one context platform.
8. **SCIP/compiler indexes improve semantic accuracy but require real build configuration and language tooling**; tree-sitter fallback remains necessary.
9. SWE-bench measures an entire agent system, not retrieval alone. Retrieval-specific metrics are required to attribute improvements.
10. **Aider's repomap quality is bounded by call-graph quality** — identical to LeanKG's PageRank orientation. The lesson is the budget-fit algorithm, not the underlying graph.
11. **Cognee's "single-Postgres" stack is the analog of LeanKG's CozoDB-RocksDB** — both are local-first embeddable graphs. Cognee's OWL ontology resolver is the most portable takeaway.
12. **Mem0 v3 removed the explicit graph DB** — proves that for chat memory, an entity co-occurrence graph in the vector store beats a separate graph. **For code, typed edges matter more than for chat**; LeanKG should not drop its typed graph.
13. **Zep/Graphiti sub-200ms p95 with no LLM in retrieval loop** validates LeanKG's choice to do query expansion in MCP server without calling an LLM per query.
14. **Sourcegraph deprecated LSIF at 4.5 and removed it at 4.6** — SCIP is the universal interchange format. LeanKG should commit to it.

---

## 15. Primary sources `[EXPANDED]`

### LeanKG

- [LeanKG repository](https://github.com/FreePeak/LeanKG)
- [`README.md`](../../README.md)
- [`docs/prd.md`](../prd.md)
- [`docs/roadmap.md`](../roadmap.md)
- [Mega-graph MCP root-cause report](../reports/root-cause-mcp-88-tool-validation-workspace-be-2026-08-02.md)
- [TencentDB comparison](tencentdb-agent-memory-vs-leankg-2026-07-31.md)
- [Graphify comparison](graphify-vs-leankg-2026-07-20.md)

### Named competitors (deep-dived)

- [CodeGraph](https://github.com/colbymchenry/codegraph)
- [CodeGraphContext](https://github.com/CodeGraphContext/CodeGraphContext)
- [Graphify](https://github.com/Graphify-Labs/graphify)
- [GitNexus](https://github.com/abhigyanpatwari/GitNexus) · [docs](https://abhigyanpatwari-gitnexus.mintlify.app/)
- [TencentDB Agent Memory](https://github.com/TencentCloud/tencentdb-agent-memory) · [docs](https://cloud.tencent.com/document/product/1813/132100)
- [LeanCTX](https://github.com/yvgude/lean-ctx) · [docs](https://leanctx.com/)
- [Codanna](https://github.com/bartolli/codanna) · [docs](https://docs.codanna.sh/)
- [Context7](https://github.com/upstash/context7) · [site](https://context7.com)
- [DeepWiki](https://github.com/cognitionai/deepwiki) · [docs](https://docs.devin.ai/work-with-devin/deepwiki) · [MCP docs](https://docs.devin.ai/work-with-devin/deepwiki-mcp)

### Adjacent code-intelligence and retrieval

- [Sourcegraph MCP](https://docs.sourcegraph.com/docs/api/mcp)
- [SCIP](https://scip-code.org/) · [spec repo](https://github.com/scip-code/scip)
- [CodeSee](https://github.com/Codesee-io/codesee-action) · [config](https://docs.codesee.io/docs/repository-configuration)
- [Aider repo-map](https://aider.chat/docs/repomap.html)
- [KYTHE](https://kythe.io/docs/kythe-overview.html)
- [Joern Code Property Graph](https://docs.joern.io/code-property-graph/)
- [Microsoft GraphRAG](https://microsoft.github.io/graphrag/)
- [LightRAG](https://github.com/HKUDS/LightRAG/) · [paper](https://arxiv.org/html/2410.05779v2)
- [Neo4j GraphRAG](https://github.com/neo4j/neo4j-graphrag-python) · [docs](https://neo4j.com/docs/neo4j-graphrag-python/current/)
- [Zep / Graphiti](https://github.com/getzep/graphiti) · [paper](https://arxiv.org/abs/2501.13956)
- [LangMem](https://github.com/langchain-ai/langmem)
- [Mem0](https://github.com/mem0ai/mem0) · [paper](https://arxiv.org/abs/2504.19413)
- [Letta](https://github.com/letta-ai/letta) · [docs](https://docs.letta.com/)
- [Cognee](https://github.com/topoteretes/cognee) · [docs](https://docs.cognee.ai/)
- [MemVid](https://github.com/memvid/memvid)
- [Universal ctags](https://github.com/universal-ctags/ctags)
- [gtags](https://github.com/syohex/gtags)
- [cscope](https://github.com/crossbeam-chris/cscope)

### MCP code-graph server cluster

- [sdsrss/code-graph-mcp](https://github.com/sdsrss/code-graph-mcp)
- [wrale/mcp-server-tree-sitter](https://github.com/wrale/mcp-server-tree-sitter)
- [ralscha/tree-sitter-mcp](https://github.com/ralscha/tree-sitter-mcp)
- [ThinkyMiner/codeTree](https://github.com/ThinkyMiner/codeTree)
- [GlacierEQ/code-graph-mcp](https://github.com/GlacierEQ/code-graph-mcp)
- [frostorygon/codelens](https://github.com/frostorygon/codelens)
- [joeczar/code-graph-mcp](https://github.com/joeczar/code-graph-mcp)
- [asyncArijit/codegraph](https://github.com/asyncArijit/codegraph)
- [CodeGraphMCPServer (nahisaho)](https://github.com/nahisaho/CodeGraphMCPServer)
- [danweinerdev/code-graph-mcp](https://github.com/danweinerdev/code-graph-mcp)

### Context engineering and evaluation

- [Anthropic: Effective context engineering](https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents)
- [Anthropic: Writing effective tools for agents](https://www.anthropic.com/engineering/writing-tools-for-agents)
- [RepoQA](https://arxiv.org/abs/2406.06025)
- [SWE-bench](https://arxiv.org/abs/2310.06770)
- [RULER](https://arxiv.org/abs/2404.06654)
- [Lost in the Middle](https://arxiv.org/abs/2307.03172)

---

## 16. Final recommendation `[UNCHANGED]`

LeanKG should resist the temptation to win by adding more parsers, stores, tools, or memory layers. Its strongest next move is to make the existing graph **reliable, explainable, fresh, and task-shaped**.

The strategic sequence is:

1. **Trustworthy serving.** No wrong project, lock, stall, hidden full scan, or stale answer.
2. **One bounded context compiler.** Convert intent into evidence, not a manual tool chain.
3. **Evidence-grade retrieval.** Stable identities, provenance, freshness, exact recovery, and typed overlays.
4. **Measured outcomes.** Retrieval benchmarks plus end-to-end agent A/B tests.
5. **Governed compounding memory.** Turn successful work into reviewed team assets.

If LeanKG executes that sequence, it can occupy a clearer and more durable position than the current competitors: not merely a code graph, graph RAG server, prompt compressor, or chat-memory hub, but the **local-first software evidence layer that agents can safely reason and act from**.

---

## 17. Adoption list — concrete MCP changes `[NEW 2026-08-02 sync]`

Ranked by leverage × cost. Each item names a concrete LeanKG file or tool that should change.

### Tier 1 — Reliability + tactical MCP wins (0–4 weeks)

1. **Authoritative `project` routing** — `src/mcp/server.rs`: shadow `file`/`path` arg decoding by `project`. Fix P0.1.
2. **Single graph handle per DB path** — `src/db/mod.rs`: process-wide `GraphEngine` registry; reject duplicate opens. Fix P0.2.
3. **Bounded blocking pool + timeouts** — `src/db/mod.rs`, `src/mcp/server.rs`: separate `blocking_pool`, per-request deadline, structured `timeout`/`retryable`/`suggested_narrowing` errors.
4. **Per-tool cost class** — `src/mcp/tools.rs`: declare `read|write`, `cost_class: keyed|frontier|precomputed|paginated|refused`, default time budget per class.
5. **Read-only MCP mode** — `src/mcp/server.rs`: `LEANKG_MCP_READ_ONLY=1` strips `cypher`, `rename`, `delete_knowledge`, `add_ontology_*`. Borrowed from GitNexus.
6. **Profile gating** — `src/mcp/server.rs`: `LEANKG_PROFILE=minimal|standard|power` shrinks visible tool list. Borrowed from LeanCTX.
7. **Server-side next-step hints** — `src/mcp/server.rs`: append a one-line `next_step` hint to every tool result. GitNexus pattern.
8. **Trait-based tool registry drift gate** — `src/mcp/tools.rs`: schema + handler + test co-located; CI runs `tool_registry_complete.rs`-style test. LeanCTX pattern.
9. **Persistent universal-ctags export** — `src/cli/mod.rs`: `leankg tags --format=ctags` emits a `tags` file for every editor. Aider/ctags lesson.

### Tier 2 — Retrieval quality (1–3 months)

10. **Three-signal RRF** — `src/graph/query.rs`: BM25 (FTS) + vector + graph proximity fused via RRF. Mem0/Cognee/Zep/LightRAG consensus.
11. **`find_with_neighbors` MCP tool** — `src/mcp/tools.rs`, `src/graph/query.rs`: VectorCypher-style seed-and-fan-out, replaces `semantic_search → get_call_graph` dance.
12. **`get_orientation` MCP tool** — `src/graph/query.rs`: weighted personalized PageRank + binary-search token budget. Returns ASCII + JSON. Aider pattern.
13. **`get_process` MCP tool** — `src/graph/query.rs`: precomputed Leiden Processes returned in one call. GitNexus pattern.
14. **`leankg_render_canvas` MCP tool** — `src/mcp/server.rs`, `src/session/`: render Mermaid call-graph of a file over budget; emit `node_id` for `session_recall` drill-down. TencentDB pattern.
15. **Self-correlating `get_context`** — `src/mcp/tools.rs`: append callers, callees, blast radius, nearest test in one payload. Codanna pattern.
16. **Document co-indexing** — `src/indexer/docs.rs`: fold doc search into `concept_search` so doc and code elements compete for the same rank. Codanna pattern.
17. **`tokens` parameter on every read tool** — `src/mcp/tools.rs`: client-controlled response budget. Context7 + GitNexus consensus.
18. **Two-mode `search_code` (fast/deep)** — `src/mcp/tools.rs`: `mode=fast` (BM25 only, sub-100ms) vs `mode=deep` (semantic + RRF + cluster context). DeepWiki pattern.

### Tier 3 — Semantic depth (3–6 months)

19. **SCIP import** — `src/indexer/scip.rs`: Tier-1 evidence precedence for TS/Go/Rust/Java/Kotlin/C++. Sourcegraph/SCIP `/scip-code/scip`.
20. **Bi-temporal edges** — `src/db/schema.rs`: add `observed_at`, `recorded_at` to `Relationship`. Invalidate, don't delete. Zep/Graphiti pattern.
21. **OWL ontology resolver + `ontology_valid` flag** — `src/ontology/`: fuzzy-match LLM-extracted entities against ontology classes (0.80 cutoff). Cognee pattern.
22. **Bulk ontology auto-discovery** — `src/mcp/tools.rs`: `bulk_ontology_discover` samples N files, extracts common entity/relationship types via LLM, offers YAML candidates. Neo4j `SchemaFromTextExtractor` pattern.
23. **Contradiction detection** — `src/ontology/`: opt-in task compares new facts against 1-hop neighborhood, writes `contradicts` edges. Cognee pattern.
24. **MD5 content-hash incremental** — `src/indexer/`: re-index only changed files. Cognee pattern.
25. **OTel service-map enrichment** — `src/indexer/otel.rs`: optional OTel/gRPC ingest for `get_service_graph`. CodeSee pattern.
26. **`PageRank` over call graph** — `src/graph/query.rs`: damping 0.85, personalizable priors. Aider pattern.

### Tier 4 — Team memory and governance (3–6 months)

27. **Memory asset lifecycle** — `src/knowledge/`: `draft`/`reviewed`/`active`/`deprecated`/`superseded`/`rejected` + `private`/`team`/`agent` visibility. TencentDB pattern.
28. **Agent loadouts** — `src/agent/`: per-persona cluster + workflow + ACL sets. TencentDB pattern.
29. **Trace-to-workflow proposals** — `src/agent/`: Conversation → Scenario → Persona triple pipeline. TencentDB pattern.
30. **`ContextWindowOverview` per-tool** — `src/mcp/server.rs`: report per-section token usage so agents can reason about budget. Letta pattern.

### Tier 5 — Evaluation and reliability (parallel)

31. **RepoQA-derived benchmark** — `tests/bench/repoqa.rs`: 5-language symbol-retrieval suite.
32. **RULER + LITM regression harness** — `tests/bench/ruler.rs`, `tests/bench/litm.rs`: positional and aggregation tests on `compile_context` output.
33. **Fixed-model A/B runner** — `tests/bench/ab.rs`: native grep vs LeanKG low-level vs LeanKG compiled context.

### Tier 6 — Distribution (6–12 months)

34. **`.leankg.yaml` steer file** — `src/config/leankg.yaml`: `priority_paths`, `ignore_paths`, language-specific extractor flags. DeepWiki `.devin/wiki.json` pattern.
35. **MCP per-client compatibility table** — `README.md`: install snippets for Cursor, Claude Code, Codex, Windsurf, OpenCode. Context7 pattern.
36. **Portable context pack** — `src/pack/`: deterministic, relative-path, content-hashed package with graph slice + summary + evidence + receipt.
37. **Merge-safe snapshot driver** — `src/pack/`: opt-in git merge driver for portable snapshots; never merge live DB files.

---

## 18. Process and risk posture `[NEW 2026-08-02 sync]`

### 18.1 Adoption posture

This sweep turned up **17 systemic patterns** from 16 competitor systems. Of those, **8 are present in LeanKG in some form** (Leiden clusters, bi-temporal, MCP, ontology, embeddings, session offload, PRD traceability, cluster skills). The remaining 9 are concrete adoption candidates (Tiers 1–6).

LeanKG already does the hardest thing — typed graph + provenance + multi-project + MCP + ontology. The missing pieces are not architectural; they are **tactical MCP wins + retrieval quality + retrieval-quality-loop**.

### 18.2 What we explicitly did not adopt

- **PolyForm Noncommercial licensing** (GitNexus). LeanKG remains permissive.
- **Forced LLM-in-extraction** (Cognee, Letta). LeanKG's tree-sitter + optional SCIP is the right answer.
- **General chat-persona memory** (Letta). LeanKG stays anchored to software delivery.
- **Many graph storage backends** (CodeGraphContext). LeanKG stays with CozoDB; remote Cozo client completion is the only storage-side work.
- **Universal second-tier memory hub** (TencentDB). LeanKG is the evidence compiler; memory is downstream.
- **Cloud-only indexing** (Context7, DeepWiki). LeanKG is local-first.
- **Closed-source analyzer** (CodeSee). LeanKG uses community SCI indexers.
- **Proprietary vector DB** (Cognee private modes). LeanKG's HNSW + cross-encoder is sufficient.
- **A 100-tool default wall** (some smaller MCP servers). LeanKG will adopt profile gating.

### 18.3 Risk hot-spots where precedence is the source of truth

When a competitor pattern conflicts with LeanKG's existing routing:

1. **Authoritative `project` argument** trumps `file`/`path` (P0.1).
2. **One process-wide engine per DB path** (P0.2).
3. **Dead-letter queue for failed tool calls** with retry/suggestion.
4. **Mega-graph protection default-on** for any tool that does not declare a cost class.

### 18.4 What comes next

The next step is **not** to write more strategy. It is to land Tier 1 and start the Tier 2 benchmark harness. The strategy document is now complete and prioritized; the PRD tracker is the next surface.

---

**Research status:** Comprehensive strategic report; no product requirements or tracker priorities are changed until explicitly accepted into the PRD. The four parallel research sweeps (GitNexus/LeanCTX/Codanna/Context7/DeepWiki, TencentDB+Letta+Mem0+Cognee+GraphRAG+LightRAG+Neo4j+MemVid+Zep+LangMem, Sourcegraph SCIP+CodeSee, Aider/ctags/CodeGraph) returned 2026-08-02; their findings are merged into this revision. The prior standalone report at `./leankg-competitive-research-and-improvement-strategy-2026-08-02.md` is superseded by this file.

### 18.4a Companion landscape-deep-dive file

The fourth research sweep also produced a companion file with 212 cited URLs covering 16 tools in implementation detail:

- [`./code-graph-code-search-landscape-2026-08-02.md`](./code-graph-code-search-landscape-2026-08-02.md) — CodeGraph (colbymchenry + CodeGraphContext + 4 renamed/related repos), Graphify, Aider repomap (incl. `MultiDiGraph` direction, edge weights, personalization, Pygments fallback, refresh modes, token-budget algorithm), Universal Ctags / GNU Global 6.6.15 / cscope, modern wrappers (`ray-x/ctags-mcp`, `algorisys-oss/repograph`, `Smattr/clink`), Sourcegraph LSIF/SCIP/MCP, CodeSee status, **plus 10 additional large-scale AI-era code-intelligence tools** (Bloop, Tabby, Continue.dev, Cursor, Cody, Windsurf, Sourcetrail, CodeStory Aide+Sidecar, Zephyr, scc, boyter/cs, tokei, rq) with a storage/incremental/EI-exposure matrix and a closed-graveyard cluster analysis.

LeanKG-specific corrections worth incorporating from that sweep:

- **Aider currently parses whole files; no incremental tree reuse.** The PageRank orientation layer is build-on-demand, not indexed. Adopt the contract for the response layer, but pair it with LeanKG's persistent graph for the underlying data.
- **Universal Ctags:** `readtags` query model is the right format for a `leankg tags --format=ctags` fast edge layer.
- **GNU Global 6.6.15** active status: `gtags` supports SQLite + inverted index + tag literal — same shape as LeanKG's CozoDB backend.
- **Continue.dev's `compute`/`delete`/`addTag`/`removeTag` op semantics** are the cleanest separation of content reuse from branch membership in the survey. Adopt in `src/db/write_bus.rs`.
- **Bloop's BLAKE3 cache key formula** `(schema_version, path, repo, content, filters, branch)` is the deterministic invalidation pattern LeanKG's `write_bus.rs` should adopt.
- **Cursor/Turbopuffer** = largest reference deployment (>1T vectors, 80M namespaces, 1M writes/s). Not a copy target, but a feasibility anchor for "millions of LOC" capability.
- **LOCOMO-style `kg_cost estimate`** (scc) — unique niche opportunity: "rewriting this impact radius would cost N in / M out tokens."
- **Closed-graveyard cluster:** Zephyr (404), Sourcetrail (archived 2021), Cody (snapshot archived 2025), Bloop (archived 2024), CodeStory (sunset 2025). Tool longevity correlates with (a) hosted service revenue, (b) multi-maintainer bus factor, (c) MCP/agent surface — not feature breadth. LeanKG's local-first + multi-maintainer + MCP posture is the right shape; reinforce (b) by adding a co-maintainer if possible.

**[Tier 1 additions to §17]** (newly sharpened by the fourth sweep):

- **Refactor `src/db/write_bus.rs` op semantics** to Continue's `compute`/`delete`/`addTag`/`removeTag` four ops.
- **Adopt BLAKE3 cache key formula** `(schema_version, path, repo, content, filters, branch)` for deterministic invalidation in `write_bus.rs`.
- **Publish `/.well-known/mcp.json` + `server.json` + `mcp_status` discovery** on the existing `:9699` HTTP. Turns MCP from "tool" into "outcome" with the same engine.
- **State-machine lexer fallback** for files with no tree-sitter grammar (proprietary DSL coverage). Tokei `src/language/mod.rs` pattern.
- **`boyter/cs` structural filter words** (`--only-declarations`, `--only-usages`, `--only-code`, `--only-strings`) in MCP tool schema params.
- **LOCOMO-style `kg_cost estimate`** MCP tool — "rewriting this impact radius would cost N in / M out tokens." Unique niche.
- **Build-time codegen** of static data (YML/JSON → Rust enum/data tables for `CodeElement` schema, ontology templates, MCP tool catalog). Tokei pattern.
