# Code-Graph and Code-Search Tool Landscape

**Research date:** 2026-08-02  
**Scope:** CodeGraph projects, Graphify, Aider repo-map, Universal Ctags/GNU Global/cscope and current wrappers, Sourcegraph LSIF/SCIP, and CodeSee Maps  
**Evidence policy:** Primary sources only where available: official repositories, source files, protocol specifications, product documentation, and live GitHub API metadata. Performance numbers are labeled **vendor-reported** unless independently reproduced.

## Executive summary

The tools fall into four distinct architectural families:

1. **Persistent code knowledge graphs for agents:** CodeGraph variants and LeanKG parse symbols and relationships ahead of time, persist them, then expose graph/context queries over MCP.
2. **Portable artifact graphs:** Graphify builds a NetworkX graph and exports `graph.json`, `graph.html`, and `GRAPH_REPORT.md`; its persistent artifact is a file, not a serving database.
3. **Prompt-time ranked maps:** Aider extracts definitions/references, computes personalized PageRank, and renders only signatures/critical lines that fit a token budget. It does not expose a general graph query service.
4. **Compiler/search indexes:** Universal Ctags, GNU Global, cscope, and Sourcegraph SCIP optimize exact navigation. SCIP contributes stable semantic symbol identity and compiler-accurate occurrences; older tag tools contribute cheap, build-independent, high-scale lookup.

CodeSee represents a fifth, product-oriented pattern: CI-side dependency extraction plus hosted, collaborative visual maps. Its standalone product ended and its technology moved into GitKraken in 2024.

| Tool | Core representation | Store/artifact | Agent query surface | MCP | Source status |
|---|---|---|---|---|---|
| CodeGraph (`colbymchenry`) | Symbols + typed edges + unresolved references | SQLite, FTS5, local `.codegraph/codegraph.db` | One default `codegraph_explore` tool; narrower tools optionally exposed | First-party | MIT; active |
| CodeGraphContext | Tree-sitter/SCIP graph | Pluggable FalkorDB/LadybugDB/KuzuDB/Neo4j-family stores | Graph queries and Cypher-backed tools | First-party | MIT; active |
| Graphify | Attributed NetworkX graph | `graph.json`, `graph.html`, reports; optional graph DB exports | Query, node, neighbors, community, path, PR tools | First-party stdio/HTTP | Apache-2.0; active |
| Aider repo-map | File-reference graph + ranked symbol tags | Disk cache for tags; rendered prompt text | Internal prompt context, not a standalone query API | None for repo-map | Apache-2.0; active |
| Universal Ctags | Symbol tag records | `tags`/`TAGS`, optional JSON/xref output | Editor jumps, CLI/filter consumers | Community wrappers | GPL-2.0; active |
| GNU Global | Definition/reference/path indexes | `GTAGS`, `GRTAGS`, `GPATH` | `global` exact, prefix, path, grep-style queries | Community wrappers | GPL; maintained GNU project |
| cscope | C-oriented cross-reference and optional inverted index | `cscope.out`, `cscope.in.out`, `cscope.po.out` | Definition/reference/caller/callee/include/text queries | No notable first-party MCP | BSD; legacy upstream, maintained forks |
| Sourcegraph SCIP | Documents, occurrences, symbols, relationships | Binary Protobuf upload, then Sourcegraph code-intel DB | Web/GraphQL/search plus Enterprise MCP | First-party Enterprise MCP | SCIP Apache-2.0; Sourcegraph product closed/hosted |
| CodeSee | File/folder/function/service dependency maps | CI-generated map uploaded to hosted service | Interactive maps, tours, comments, insights | None found | Standalone product sunset; OSS actions/analyzers remain |

Sources: [CodeGraph README and schema](https://github.com/colbymchenry/codegraph), [CodeGraphContext repository](https://github.com/CodeGraphContext/CodeGraphContext), [Graphify architecture](https://github.com/Graphify-Labs/graphify/blob/v8/ARCHITECTURE.md), [Aider repo-map](https://aider.chat/docs/repomap.html), [Universal Ctags](https://github.com/universal-ctags/ctags), [GNU Global manual](https://www.gnu.org/software/global/manual/global.html), [cscope manual](https://cscope.sourceforge.net/cscope_man_page.html), [SCIP schema](https://github.com/scip-code/scip/blob/main/scip.proto), [Sourcegraph MCP](https://sourcegraph.com/docs/api/mcp), [CodeSee action](https://github.com/Codesee-io/codesee-action), [GitKraken acquisition announcement](https://www.gitkraken.com/blog/gitkraken-launches-devex-platform-acquires-codesee).

---

## 1. CodeGraph

### 1.1 Name disambiguation and status

“CodeGraph” is not one project. Live `gh search repos 'codegraph in:name'` found many unrelated repositories. Two user names suggested for investigation do **not** own a CodeGraph or Graphify repository:

- `mlocati` has 116 public repositories, but no repository or code-search hit for CodeGraph/Graphify.
- `anderseknert` has 93 public repositories, but no repository or code-search hit for CodeGraph/Graphify.

These are negative search findings, not evidence of renamed projects. Durable profile links: [mlocati](https://github.com/mlocati), [anderseknert](https://github.com/anderseknert). Searches used `gh repo list`, `gh search repos`, and `gh search code` against both accounts on 2026-08-02.

Most relevant active projects found:

| Repository | Live status on 2026-08-02 | Role |
|---|---|---|
| [`colbymchenry/codegraph`](https://github.com/colbymchenry/codegraph) | 64,073 stars; pushed 2026-08-01; MIT; latest release [`v1.5.0`](https://github.com/colbymchenry/codegraph/releases/tag/v1.5.0) | Largest project using exact “CodeGraph” product name |
| [`CodeGraphContext/CodeGraphContext`](https://github.com/CodeGraphContext/CodeGraphContext) | 4,029 stars; pushed 2026-08-01; MIT; latest release [`v0.5.2`](https://github.com/CodeGraphContext/CodeGraphContext/releases/tag/v0.5.2) | SCIP-aware, pluggable graph-store implementation |
| [`codegraph-ai/CodeGraph`](https://github.com/codegraph-ai/CodeGraph) | 49 stars; pushed 2026-07-19; Apache-2.0 | Rust/native graph with broad MCP and embedding surface |
| [`Lordymine/codegraph`](https://github.com/Lordymine/codegraph) | 4 stars; pushed 2026-06-23; MIT | Focused Go + TypeScript design using compiler/type-checker indexes |
| [`Jakedismo/codegraph-rust`](https://github.com/Jakedismo/codegraph-rust) | 857 stars; last push 2025-12-20; no detected license | SurrealDB/Rust experiment; stale and legally unsafe to reuse without permission |

GitHub metadata source: repository REST endpoints, for example [`GET /repos/colbymchenry/codegraph`](https://api.github.com/repos/colbymchenry/codegraph) and [`GET /repos/CodeGraphContext/CodeGraphContext`](https://api.github.com/repos/CodeGraphContext/CodeGraphContext).

### 1.2 `colbymchenry/codegraph`: architecture

**Indexing pipeline.** CodeGraph documents four main stages: tree-sitter extraction, SQLite storage, reference resolution, and native filesystem auto-sync. Its native Rust kernel handles compiled parsing for a documented set of languages; portable extraction remains a per-file fallback. File events are debounced and only changed source files are synchronized. [README, “How It Works” and auto-sync sections](https://github.com/colbymchenry/codegraph#how-it-works); [Rust kernel manifest](https://github.com/colbymchenry/codegraph/blob/main/codegraph-kernel/Cargo.toml).

**Storage and data model.** Store is local SQLite with WAL/FTS5. Schema is explicit:

- `nodes`: ID, kind, name, qualified name, file/language/range, docstring, signature, visibility, flags, decorators, type parameters, return type.
- `edges`: source, target, kind, JSON metadata, location, provenance.
- `files`: content hash, language, size, modification/index time, node count, extraction errors.
- `unresolved_refs`: reference name/kind/location/candidates/status, retained after failed resolution so later incremental sync can retry.
- `nodes_fts`: FTS5 search over names, qualified names, docstrings, and signatures.

Primary source: [`src/db/schema.sql`](https://github.com/colbymchenry/codegraph/blob/main/src/db/schema.sql), especially `nodes`/`edges`/`files`/`unresolved_refs` definitions at lines 19–92 and FTS/index declarations after line 95.

**Query capabilities.** Default MCP surface intentionally lists one high-level tool, `codegraph_explore`. It returns relevant verbatim code grouped by file, relationship paths, and blast-radius context. Narrow tools such as node lookup, search, callers, callees, impact, files, and status remain implemented but hidden by default unless `CODEGRAPH_MCP_TOOLS` enables them. This is deliberate tool-selection compression, not missing functionality. [README, MCP Tools](https://github.com/colbymchenry/codegraph#mcp-tools); [`src/mcp/server-instructions.ts`](https://github.com/colbymchenry/codegraph/blob/main/src/mcp/server-instructions.ts).

**MCP integration.** First-party MCP server supports multiple projects through `projectPath`; installation configures supported agents. The initialization response also includes usage guidance, aiming to make the agent choose graph retrieval before raw file crawling. [README, Agent Tool Guidance](https://github.com/colbymchenry/codegraph#agent-tool-guidance); [`src/mcp/server-instructions.ts`](https://github.com/colbymchenry/codegraph/blob/main/src/mcp/server-instructions.ts).

**Performance.** Vendor benchmark reports, across seven repositories and four runs per arm, median reductions of 89% in tool calls, 69% in tokens, and 60% in cost when an agent has CodeGraph; wall time averaged 20% faster but lost on two small repositories. This is an agent-system A/B benchmark, not isolated parser/query latency, and was not reproduced here. [README, Benchmark Results](https://github.com/colbymchenry/codegraph#benchmark-results); detailed artifacts under [`docs/benchmarks/`](https://github.com/colbymchenry/codegraph/tree/main/docs/benchmarks).

**Unique features.** Framework-aware routes, dynamic-dispatch synthesis, cross-language bridges for Swift/Objective-C and React Native/Expo, current-source return in one exploration call, agent-visible freshness, and unresolved-edge retry distinguish it from a plain AST index. [README](https://github.com/colbymchenry/codegraph); design notes under [`docs/design/`](https://github.com/colbymchenry/codegraph/tree/main/docs/design).

### 1.3 CodeGraphContext: architecture

CodeGraphContext uses tree-sitter as broad extraction and can ingest compiler-derived SCIP for stronger semantic navigation. It supports graph backends including FalkorDB Lite/remote, LadybugDB, KuzuDB, NornicDB, and Neo4j-family deployments. Queries are graph-oriented, with Cypher as the common power-user model. [Repository README](https://github.com/CodeGraphContext/CodeGraphContext); [`pyproject.toml`](https://github.com/CodeGraphContext/CodeGraphContext/blob/main/pyproject.toml); backend implementations under [`src/codegraphcontext/core/`](https://github.com/CodeGraphContext/CodeGraphContext/tree/main/src/codegraphcontext/core).

First-party MCP definitions and dispatch live in [`src/codegraphcontext/server.py`](https://github.com/CodeGraphContext/CodeGraphContext/blob/main/src/codegraphcontext/server.py) and [`src/codegraphcontext/tool_definitions.py`](https://github.com/CodeGraphContext/CodeGraphContext/blob/main/src/codegraphcontext/tool_definitions.py). Portable `.cgc` bundles and multiple stores emphasize interoperability and distribution more than one optimized embedded engine. [Repository README](https://github.com/CodeGraphContext/CodeGraphContext).

### 1.4 Lessons for LeanKG

1. **One task-shaped default tool can outperform a huge menu.** Keep specialized LeanKG tools, but present a default `compile_context`/`explore` path that returns source, paths, tests, and blast radius together.
2. **Retain unresolved references as retryable evidence.** Content changes can make yesterday’s unresolved call resolvable without rebuilding everything.
3. **Make freshness part of every answer.** Watchers alone do not prove results include current dirty files.
4. **Use SCIP or compiler indexes as semantic overlays.** Tree-sitter remains fallback; typed occurrences should override heuristic call/reference edges.
5. **Do not copy benchmark headlines without reproducing methodology.** Measure retrieval accuracy, tokens, tool calls, and task success separately.

---

## 2. Graphify

### Status

Canonical code-knowledge-graph project is [`Graphify-Labs/graphify`](https://github.com/Graphify-Labs/graphify), formerly under `safishamsi`; default branch is `v8`. On 2026-08-02 it was unarchived, Apache-2.0, pushed 2026-08-01, and latest release was [`v0.9.32`](https://github.com/Graphify-Labs/graphify/releases/tag/v0.9.32). Repositories `mlocati/graphify` and `anderseknert/graphify` return 404 and were not found in either owner’s public repository list.

Avoid name collisions: archived [`kbastani/graphify`](https://github.com/kbastani/graphify) is a Neo4j text-classification extension, not this code tool; [`TtTRz/graphify-rs`](https://github.com/TtTRz/graphify-rs) is an independent Rust rewrite with no detected standard license.

### Architecture summary

Official architecture defines a linear, side-effect-bounded pipeline:

```text
detect() -> extract() -> build_graph() -> cluster() -> analyze() -> report() -> export()
```

Stages exchange Python dictionaries and `networkx.Graph`; core writes only under `graphify-out/`. Modules include `detect.py`, `extract.py`, `build.py`, `cluster.py`, `analyze.py`, `report.py`, `export.py`, `serve.py`, and `watch.py`. [Official `ARCHITECTURE.md`](https://github.com/Graphify-Labs/graphify/blob/v8/ARCHITECTURE.md); [implementation tree](https://github.com/Graphify-Labs/graphify/tree/v8/graphify).

Code extraction is deterministic tree-sitter parsing. Non-code semantic ingestion—documents, PDF, images, audio/video—can use an assistant/model backend. This privacy distinction matters: “local code parsing” does not mean every optional content pipeline is model-free. [README](https://github.com/Graphify-Labs/graphify); parser modules under [`graphify/extractors/`](https://github.com/Graphify-Labs/graphify/tree/v8/graphify/extractors).

### Data model and storage

Extractor schema is intentionally small:

```json
{
  "nodes": [
    {"id": "unique_string", "label": "human name", "source_file": "path", "source_location": "L42"}
  ],
  "edges": [
    {"source": "id_a", "target": "id_b", "relation": "calls|imports|uses|...", "confidence": "EXTRACTED|INFERRED|AMBIGUOUS"}
  ]
}
```

`validate.py` checks this before graph construction. Confidence is part of each edge, not inferred later from relation type. [Official `ARCHITECTURE.md`, Extraction output schema](https://github.com/Graphify-Labs/graphify/blob/v8/ARCHITECTURE.md#extraction-output-schema); [`graphify/validate.py`](https://github.com/Graphify-Labs/graphify/blob/v8/graphify/validate.py).

Default working store is in-memory NetworkX; durable products are portable `graph.json`, `graph.html`, and `GRAPH_REPORT.md`. Optional exporters can push to graph databases, but those are not required for normal query use. [README output description](https://github.com/Graphify-Labs/graphify); [`graphify/export.py`](https://github.com/Graphify-Labs/graphify/blob/v8/graphify/export.py); [`graphify/exporters/`](https://github.com/Graphify-Labs/graphify/tree/v8/graphify/exporters).

### Query and MCP capabilities

CLI offers `query`, `path`, and `explain`. MCP server exposes at least:

- `query_graph`
- `get_node`
- `get_neighbors`
- `get_community`
- `god_nodes`
- `graph_stats`
- `shortest_path`
- `list_prs`
- `get_pr_impact`
- `triage_prs`

It also publishes resources for report, stats, god nodes, surprising connections, confidence audit, and suggested questions. Both stdio and Streamable HTTP transports are implemented. Primary source: [`graphify/serve.py`](https://github.com/Graphify-Labs/graphify/blob/v8/graphify/serve.py), tool declarations around lines 1348–1457 and resources around lines 1806–1811; transport code around lines 1924–2194.

Graphify also installs host-specific skills and optional graph-first hooks rather than relying only on MCP descriptions. [Skill implementations](https://github.com/Graphify-Labs/graphify/tree/v8/graphify/skills); [`graphify/hooks.py`](https://github.com/Graphify-Labs/graphify/blob/v8/graphify/hooks.py).

### Performance characteristics

Official benchmark file reports:

- Code-intelligence test on ERPNext (~1M LOC): fixed agent’s key-fact coverage increased from 70.8% to 82.0% across six graded questions when given one Graphify tool, at roughly 140K tokens/query.
- Temporal extraction: 689 weekly ERPNext checkpoints from 2011–2026; final checkpoint 22,620 nodes, 48,710 edges, 3,758 files.
- Code graph build uses no LLM credits; conversational-memory benchmarks involve model and embedding components and should not be confused with code-index speed.

All are **vendor-reported** from Graphify’s own harness, not reproduced here. [Official `BENCHMARKS.md`](https://github.com/Graphify-Labs/graphify/blob/v8/BENCHMARKS.md).

### Innovations and differentiators

1. **Portable graph as product:** useful HTML, JSON, and report appear immediately.
2. **Edge honesty:** `EXTRACTED`, `INFERRED`, and `AMBIGUOUS` are visible to users and agents.
3. **Architecture affordances:** Leiden communities, god nodes, surprising links, and suggested questions orient users without raw graph inspection.
4. **Rationale nodes:** `WHY`/`NOTE`/`HACK` and ADR/RFC references can join code structure.
5. **Broad corpus:** code, docs, configs, schemas, SQL, and optional media inhabit one graph.
6. **No mandatory serving database:** easy sharing and cold-start distribution, at cost of weaker transactional/incremental serving than a database-backed system.

### Lessons for LeanKG

- Treat exported context packs as first-class products: deterministic ordering, relative paths, schema/version/fingerprint, report, and interactive viewer.
- Preserve serving DB as authority; use portable JSON as distribution artifact, not live canonical store.
- Surface edge provenance consistently in every query response.
- Productize existing clusters/hotspots into a short architecture report and suggested next questions.
- Keep code extraction model-free; make all model-dependent enrichments explicit and optional.

---

## 3. Aider repository map

### Status

Repo-map is an active feature inside [`Aider-AI/aider`](https://github.com/Aider-AI/aider), an Apache-2.0 repository. GitHub reported 47,877 stars and latest source push 2026-05-22 on research date. Official behavior is documented at [Repository map](https://aider.chat/docs/repomap.html); main implementation is [`aider/repomap.py`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py).

### Architecture and indexing pipeline

Aider performs prompt-time context compilation rather than building a general serving graph:

1. Detect language from filename.
2. Load tree-sitter language/parser and language-specific `tags.scm` query.
3. Parse file and capture definitions/references.
4. Cache tags by file modification time in `.aider.tags.cache.v<CACHE_VERSION>` using a disk cache backed by SQLite, with in-memory fallback after cache errors.
5. Build a directed NetworkX `MultiDiGraph`: each edge points from referencing file to defining file; unreferenced definitions receive a small self-edge so they remain rankable.
6. Weight edges by reference frequency and identifier/file relevance, then run PageRank personalized by chat files, mentioned files, and identifiers.
7. Redistribute file PageRank through outgoing identifier edges, rank definitions, and render source “lines of interest” through `TreeContext`.
8. Use binary search over candidate map size to fit active token budget.

Primary code: immutable revision [`aider/repomap.py` at `541bba6e`](https://github.com/Aider-AI/aider/blob/541bba6ef4a5385b8cf032201ec6e3f3e32a6ea6/aider/repomap.py): `Tag` at line 29, cache declaration at line 43, entry point at line 103, raw tree-sitter extraction near line 279, graph construction/ranking near lines 365–574, ranked-map pipeline near lines 576/629, tree rendering near lines 710/748. Official conceptual description: [Aider repo-map docs](https://aider.chat/docs/repomap.html) and [tree-sitter design article](https://aider.chat/2023/10/22/repomap.html).

Current extraction recognizes `name.definition.*` and `name.reference.*` captures. Query lookup prefers `aider/queries/tree-sitter-language-pack/<lang>-tags.scm`, then falls back to the older `tree-sitter-languages` query directory. If a query yields definitions but no references, Aider lexes `Token.Name` occurrences with Pygments as lower-confidence references. Although tree-sitter itself supports incremental parsing, current repo-map code calls `parser.parse(bytes(code, "utf-8"))` for the whole file; it does not pass a previous tree or use `Tree.edit`. [`get_tags_raw`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L279-L363); [`get_scm_fname`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L805-L829).

Aider originally used Universal Ctags, then moved to tree-sitter because it could include richer signatures, bundle language parsers through Python packages, and remove an external ctags install requirement. Current language support requires both parser availability and a useful `tags.scm`. [Official design article, “What about ctags?”](https://aider.chat/2023/10/22/repomap.html#what-about-ctags); [supported languages](https://aider.chat/docs/languages.html).

### Data model and output format

Internal tag record is `Tag(rel_fname, fname, line, name, kind)`, where `kind` distinguishes definition/reference. Graph is ephemeral NetworkX structure used for ranking, not durable graph storage. Persistent cache stores extracted tag data and file modification time, not a globally queryable knowledge graph. [`aider/repomap.py`](https://github.com/Aider-AI/aider/blob/541bba6ef4a5385b8cf032201ec6e3f3e32a6ea6/aider/repomap.py#L29-L43).

Edge weights encode pragmatic relevance. Explicitly mentioned identifiers and descriptive mixed/snake/kebab identifiers of at least eight characters get ×10; leading-underscore names and identifiers defined in more than five files get ×0.1; references originating in chat files get ×50; repeated references contribute `sqrt(count)`. Personalization assigns weight to chat files, mentioned filenames, and file path components matching mentioned identifiers; the same vector handles dangling nodes. [`get_ranked_tags`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L365-L574). One source quirk deserves caution: square-rooting occurs inside the definer loop, so identifiers with multiple definers may receive repeated square roots on later edges.

Rendered output is plain prompt text organized by relative path. It includes critical source lines for selected definitions, preserves indentation/context, uses omission markers such as `⋮...`, and truncates output lines to 100 characters. Example format is shown in [official repo-map docs](https://aider.chat/docs/repomap.html#using-a-repo-map-to-provide-context); rendering code is [`render_tree` / `to_tree`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L710-L784).

### Query capabilities and MCP

Agents do not query repo-map through MCP. Aider automatically injects map into each change request. User/chat state acts as query: named files and identifiers personalize graph ranking. Default `--map-tokens` is 1K; budget can expand when no files are already in chat, because broad orientation is then more valuable. [Official repo-map docs, Optimizing the map](https://aider.chat/docs/repomap.html#optimizing-the-map); [`get_repo_map`](https://github.com/Aider-AI/aider/blob/541bba6ef4a5385b8cf032201ec6e3f3e32a6ea6/aider/repomap.py#L103-L167).

### Performance characteristics

Aider avoids full tokenization for large candidate maps by sampling text to estimate tokens, caches extracted tags by modification time, caches rendered trees/maps, and uses binary search to find largest map fitting budget. Constructor defaults are `map_tokens=1024` and `map_mul_no_files=8`; without chat files, budget may grow to `min(map_tokens * 8, max_context_window - 4096)`. Refresh modes are `manual`, `always`, `files`, and `auto`; `auto` caches a rendered map only when prior generation took over one second. Official docs and source specify these behaviors but publish no stable, current end-to-end indexing latency benchmark. Claims such as “incremental reparse” or “sub-second on most repositories” should not be inferred. [`token_count`](https://github.com/Aider-AI/aider/blob/541bba6ef4a5385b8cf032201ec6e3f3e32a6ea6/aider/repomap.py#L88-L101); [`get_repo_map`](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L103-L167); [map caching](https://github.com/Aider-AI/aider/blob/main/aider/repomap.py#L576-L706); [repo-map docs](https://aider.chat/docs/repomap.html).

### Innovations and differentiators

- **Context-sensitive PageRank:** ranking changes with chat state rather than remaining globally static.
- **Signatures before bodies:** maximum architectural coverage per prompt token.
- **Hard token fit:** map size is an explicit optimization target, not post-hoc truncation.
- **Transparent output:** exact map text sent to model is inspectable.
- **Graceful parser fallback:** unsupported/incomplete reference capture can degrade without blocking editing.

### Lessons for LeanKG

- Add a deterministic orientation compiler: signatures + selected relationships + exact token budget.
- Personalize graph ranking from task terms, open files, changed files, and known target symbols.
- Separate stable repo orientation from volatile task/diff context for prompt-cache reuse.
- Make selection trace visible: score, seed, relationship, and omission reason.
- Evaluate concise map against multi-tool exploration, not only search recall.

---

## 4. Universal Ctags, GNU Global, and cscope

### 4.1 Universal Ctags

**Status.** [`universal-ctags/ctags`](https://github.com/universal-ctags/ctags) is explicitly the maintained successor to Exuberant Ctags. On 2026-08-02 it was unarchived, GPL-2.0, pushed that day, and had 7,258 stars. [Repository README](https://github.com/universal-ctags/ctags); [official manuals](https://docs.ctags.io/en/latest/man-pages.html).

**Architecture/data.** Ctags scans source with native language-specific parsers/state machines or optlib patterns; it is not one shared tree-sitter layer. It emits tag records to a `tags` file (or Emacs `TAGS`). A tag identifies name, source file, address/pattern, and optional extension fields such as kind, scope, signature, typeref, roles, and language. Universal Ctags adds machine-readable JSON Lines output and optlib parsers, making it useful as an extraction frontend. Primary implementation paths include [`main/parse.c`](https://github.com/universal-ctags/ctags/blob/master/main/parse.c), [`main/entry.c`](https://github.com/universal-ctags/ctags/blob/master/main/entry.c), [`main/writer.c`](https://github.com/universal-ctags/ctags/blob/master/main/writer.c), and [`parsers/`](https://github.com/universal-ctags/ctags/tree/master/parsers). [Official `ctags(1)` manual](https://docs.ctags.io/en/latest/man/ctags.1.html); [JSON output manual](https://docs.ctags.io/en/latest/man/ctags-json-output.5.html); [tags format manual](https://docs.ctags.io/en/latest/man/tags.5.html).

**Queries.** Core product generates indexes; `readtags`, editors, and wrappers perform exact/prefix/case-insensitive symbol lookup and jumps. `readtags` also supports filter, sorter, and formatter expressions and can use binary search on sorted tag files. Ctags does not itself provide a complete semantic call graph or compiler-accurate reference resolution. Parser-specific reference-role tags remain extracted occurrences, not type-resolved calls. [`readtags(1)` source](https://github.com/universal-ctags/ctags/blob/master/docs/man/readtags.1.rst); [Universal Ctags README](https://github.com/universal-ctags/ctags).

### 4.2 GNU Global (`gtags`)

**Status.** GNU Global remains maintained as GNU software. Official site/manual showed version 6.6.15/current 2026 material during research; official FTP contains [`global-6.6.15.tar.gz`](https://ftp.gnu.org/gnu/global/global-6.6.15.tar.gz). [GNU Global manual](https://www.gnu.org/software/global/manual/global.html); [GNU project page](https://www.gnu.org/software/global/).

**Architecture/data.** Running `gtags` at project root traverses source and writes:

- `GTAGS`: definition database
- `GRTAGS`: reference database
- `GPATH`: path-name database

`global` locates project DB from subdirectories, so clients need not pass DB path on each query. Native parsers cover C/C++/Yacc/Java/PHP/assembly; plug-in parsers can use Universal Ctags or Pygments for broader languages, with lower reference precision. [GNU Global manual, Basic Usage and Plug-in Parser](https://www.gnu.org/software/global/manual/global.html).

**Queries.** `global` supports definitions, references, symbols, path matching, grep/regex, completion/prefix, and file-scoped output. Results can be formatted for editors or scripts. This is indexed navigation, not arbitrary graph traversal. [Official `global(1)` manual](https://www.gnu.org/software/global/manual/global.html#global-invocation).

**Performance.** Official manual calls database access high performance and warns tag files need considerable disk; no current, standardized benchmark is published there. Modern wrapper `mcp-gtags-server` claims 37M lines indexed in about one minute, but that number is **wrapper vendor-reported** and hardware/corpus dependent. [GNU Global manual](https://www.gnu.org/software/global/manual/global.html); [`mcp-gtags-server` README](https://github.com/harshithsunku/mcp-gtags-server).

### 4.3 cscope

**Status.** Original SourceForge tree remains available and source tree shows changes through 2022, but upstream project is legacy. Modern fork [`agvxov/csope`](https://github.com/agvxov/csope) was pushed 2026-06-06 and retains BSD-family licensing. [Official cscope source](https://sourceforge.net/p/cscope/cscope/ci/master/tree/); [Csope repository](https://github.com/agvxov/csope).

**Architecture/data.** cscope’s fuzzy C parser builds symbol cross-reference `cscope.out`. On later runs it rebuilds only if files/list changed and copies unchanged-file data from old cross-reference. `-q` adds inverted indexes `cscope.in.out` and `cscope.po.out` for faster symbol lookup on large projects. [Official cscope manual](https://cscope.sourceforge.net/cscope_man_page.html).

**Queries.** It supports symbol references, global definitions, functions called by a function, callers, text, regex, files, and include relationships. Line-oriented mode allows scripting/backend use; curses UI and editor integrations are traditional clients. Official site reports historical use on projects with 20 million LOC, but this is historical capacity evidence, not a modern benchmark. [Official cscope homepage](https://cscope.sourceforge.net/); [manual](https://cscope.sourceforge.net/cscope_man_page.html).

### 4.4 Modern wrappers

| Wrapper | Architecture | Agent tools | Status/license |
|---|---|---|---|
| [`harshithsunku/mcp-gtags-server`](https://github.com/harshithsunku/mcp-gtags-server) | Python MCP around GNU Global; auto-installs user-space Global, Universal Ctags, Pygments; ctags enrichment adds kind/signature/scope; guard/macro logic targets kernel trees | Definition/reference, callers/callees, symbol body/info, file symbols, reachability, blast radius, update/freshness | New in 2026; MIT; 1 star; pushed 2026-07-13 |
| [`ryogrid/gtags-mcp`](https://github.com/ryogrid/gtags-mcp) | MCP translates requests to `global`; builds DB at startup; refresh manually/hook | Definition, references, prefix symbols, pattern search, refresh | MIT; pushed 2026-06-02 |
| [`gladiatr72/mcp-ctags`](https://github.com/gladiatr72/mcp-ctags) | FastMCP over existing ctags files | Detect, find/list symbol, location, source search | MIT; initial small project; pushed 2025-09-18 |
| [`vishalkumar14/mcp-ctags`](https://github.com/vishalkumar14/mcp-ctags) | Loads static ctags index for definitions; live ripgrep for current references; staleness warning | `find_symbol`, `find_references`, `refresh_tags` | MIT; pushed 2026-05-29 |
| [`netmute/ctags-lsp`](https://github.com/netmute/ctags-lsp) | Universal Ctags index held in memory behind LSP | completion, definition, document/workspace symbols | MIT; 148 stars; pushed 2026-04-11 |
| [`ruben2020/codequery`](https://github.com/ruben2020/codequery) | Imports cscope + ctags into SQLite; Qt GUI | Symbol/call/include/class queries, call/inheritance visualization | MPL-2.0; 773 stars; pushed 2026-07-12 |
| [`ray-x/ctags-mcp`](https://github.com/ray-x/ctags-mcp) | Go stdio MCP invokes Universal Ctags in batches; generated `tags` plus SHA-256 workspace state | `search_symbols`, `generate_tags` | BSD-3-Clause; very new/small; pushed 2026-05-17 |
| [`algorisys-oss/repograph`](https://github.com/algorisys-oss/repograph) | Tree-sitter, ctags, or regex extraction; content-hash JSON cache; heuristic confidence graph | Index, symbol/ref search, callers/callees, impact, affected files, node, explore | MIT; new experimental project; pushed 2026-08-01 |
| [`Smattr/clink`](https://github.com/Smattr/clink) | libclang semantic C/C++ plus fuzzy parsers and SQL DB; modern cscope-style backend | Symbol/definition/reference/caller/callee/include queries via CLI/Vim | Unlicense; active push 2026-05-15; no MCP |

Metadata came from live GitHub REST endpoints on 2026-08-02. Tool/architecture claims come from each linked official README. Wrapper benchmark claims are not independent measurements.

### Lessons for LeanKG

1. **Cheap exact lookup remains valuable.** Route exact name/prefix/file queries through compact indexes before semantic retrieval.
2. **Hybrid confidence tiers beat one parser.** Ctags/Global-like syntax results can remain fallback while SCIP/LSP edges carry stronger authority.
3. **Expose a freshness barrier.** `update_index`/staleness warnings give agents a clear contract after edits.
4. **Kernel/config awareness matters.** Preprocessor guard stacks and macro-generated symbols answer practical C/C++ questions generic AST graphs miss.
5. **Do not market syntax references as semantic references.** Plugin token occurrences must be labeled accordingly.

---

## 5. Sourcegraph, LSIF, and SCIP

### Status and licensing

- SCIP specification/CLI moved to [`scip-code/scip`](https://github.com/scip-code/scip); it is Apache-2.0, active, pushed 2026-07-21, with latest release [`v0.9.0`](https://github.com/scip-code/scip/releases/tag/v0.9.0).
- LSIF site states it has been superseded by SCIP. [LSIF](https://lsif.dev/); [migration guide](https://sourcegraph.com/docs/admin/how-to/lsif-scip-migration).
- Current Sourcegraph product is commercial Enterprise software. Its old monorepo snapshot [`sourcegraph/sourcegraph-public-snapshot`](https://github.com/sourcegraph/sourcegraph-public-snapshot) is archived; search engine [`sourcegraph/zoekt`](https://github.com/sourcegraph/zoekt) remains Apache-2.0 and active.
- Sourcegraph MCP is available on Enterprise plans. [Official MCP docs](https://sourcegraph.com/docs/api/mcp).

### Why SCIP replaced LSIF

LSIF encoded LSP-style results as a JSON graph with opaque numeric IDs. Sourcegraph reported four scaling problems: weak machine-readable typing, large in-memory graph processing, difficult debugging, and global-ID ordering that complicated incremental indexing. SCIP replaced graph plumbing with typed Protobuf records and human-readable symbol strings. [Sourcegraph announcement](https://sourcegraph.com/blog/announcing-scip); [SCIP design](https://github.com/scip-code/scip/blob/main/docs/DESIGN.md); [historical LSIF specification](https://github.com/microsoft/language-server-protocol/blob/main/indexFormat/specification.md).

### SCIP data model

Top-level Protobuf:

```text
Index
  metadata: Metadata
  documents: repeated Document
  external_symbols: repeated SymbolInformation

Document
  language
  relative_path
  occurrences: repeated Occurrence
  symbols: repeated SymbolInformation
  optional text
  position_encoding
```

`Metadata` records protocol version, indexer tool name/version/arguments, project root, and source text encoding. `Occurrence` connects source ranges to stable symbol strings and roles. `SymbolInformation` carries docs, relationships, kind, and signature information. Symbol syntax encodes scheme, package manager/name/version, and namespace/type/term/method descriptors; local symbols remain document-scoped. [Official `scip.proto`](https://github.com/scip-code/scip/blob/main/scip.proto).

This is an interchange/index format, not a graph database or online query language. Consumers upload/read `index.scip` and materialize their own indexes. CLI supports linting, printing/JSON, snapshots, stats, tests, and experimental conversion. [`docs/CLI.md`](https://github.com/scip-code/scip/blob/main/docs/CLI.md).

### Sourcegraph indexing and query architecture

Precise pipeline is:

```text
language-specific SCIP indexer
  -> index.scip (binary Protobuf)
  -> `src code-intel upload`
  -> Sourcegraph processing/code-intel storage
  -> precise hover/definition/reference/implementation queries
```

Indexers include compiler/type-checker-backed implementations such as `scip-typescript`, `scip-java`, `scip-clang`, `scip-python`, `scip-ruby`, `scip-dotnet`, and rust-analyzer SCIP output. [Sourcegraph indexer docs](https://sourcegraph.com/docs/code_navigation); [SCIP repository list](https://github.com/scip-code/scip#scip-indexers); [upload explanation source](https://github.com/sourcegraph/docs/blob/main/docs/code-navigation/explanations/uploads.mdx).

Sourcegraph also offers search-based navigation, using text/syntax heuristics for immediate broad coverage, versus precise navigation using compile-time data for compiler-accurate cross-repository results. [Official Code Navigation docs](https://sourcegraph.com/docs/code_navigation); [precise-navigation source](https://github.com/sourcegraph/docs/blob/main/docs/code-navigation/precise-code-navigation.mdx).

### MCP query capabilities

First-party HTTP MCP endpoints:

- `/.api/mcp`: core suite
- `/.api/mcp/all`: full suite
- `/.api/mcp/deepsearch`: Deep Search-only suite

Tools include file/repository operations, `keyword_search`, natural-language `nls_search`, sandboxed Lua `evaluator`, `go_to_definition`, `find_references`, commit/diff/revision search, synchronous `code_finder`, and asynchronous/open-ended `deepsearch`. Results use limits/pagination and respect repository permissions plus MCP RBAC/tool disablement. [Official MCP docs](https://sourcegraph.com/docs/api/mcp); [documentation source](https://github.com/sourcegraph/docs/blob/main/docs/api/mcp/index.mdx).

### Performance characteristics

Sourcegraph reported SCIP payloads about 4× smaller compressed and 5× smaller uncompressed than equivalent LSIF; migration from `lsif-node` to `scip-typescript` yielded about 10× CI speedup, though Sourcegraph explicitly says protocol change was not sole cause. A Meta/Glean integration reported 8× smaller and 3× faster processing. These are first-party/partner reports from 2022, not current independent benchmarks. [SCIP announcement](https://sourcegraph.com/blog/announcing-scip).

Historical `lsif-go` benchmark reported indexing 30.75M SLOC in 18m52s with a 33GB index on stated 2017 iMac Pro hardware; this shows approximate LSIF scaling, not current SCIP/Sourcegraph latency. [`sourcegraph/lsif-go` benchmark](https://github.com/sourcegraph/lsif-go/blob/master/BENCHMARK.md).

### Innovations and differentiators

1. **Stable semantic identity:** package/version-aware symbol strings enable cross-repository joins.
2. **Compiler truth as portable artifact:** precise navigation can be generated in CI, detached from live LSP sessions.
3. **Typed streaming format:** Protobuf schema improves language bindings, validation, and payload processing.
4. **Dual precision:** fast syntax/search fallback plus compiler-accurate SCIP when configured.
5. **Enterprise code estate:** cross-repository search, permissions, history, and agent access operate over many repos.

### Lessons for LeanKG

- Import SCIP as an overlay; do not replace tree-sitter fallback.
- Preserve SCIP symbol identity, package/version, occurrence roles, encoding, indexer metadata, and source revision.
- Grade edge authority: compiler/indexer evidence above extracted syntax above inferred resolution.
- Separate portable semantic interchange from serving-store schema.
- Copy MCP result budgets/RBAC/tool suppression concepts, not Enterprise coupling.

---

## 6. CodeSee

### Status

Standalone CodeSee product is no longer independent. GitKraken announced acquisition on 2024-05-14 and plans to integrate CodeSee code visualization, function maps, workflow automation, and code-understanding capabilities into GitKraken’s DevEx platform. [GitKraken acquisition announcement](https://www.gitkraken.com/blog/gitkraken-launches-devex-platform-acquires-codesee); [press release](https://www.prnewswire.com/news-releases/gitkraken-acquires-codesee-launches-new-devex-platform-including-support-for-google-geminis-ai-model-302144298.html).

GitHub organization [`Codesee-io`](https://github.com/Codesee-io) remains. Unified [`codesee-action`](https://github.com/Codesee-io/codesee-action) is not archived and was pushed 2026-05-06, but legacy [`codesee-map-action`](https://github.com/Codesee-io/codesee-map-action) is archived. This confirms surviving integration assets, but does not by itself prove continuation of standalone hosted product.

### Architecture and indexing pipeline

CodeSee analyzed a GitHub repository through a GitHub Action; official product page says code stayed on GitHub rather than being stored on CodeSee servers. Generated map data was uploaded for hosted visualization. [Official “How CodeSee works”](https://www.codesee.io/how-codesee-works); [continuous understanding page](https://www.codesee.io/continuous-understanding).

Current composite action confirms pipeline:

1. Checkout full repository history.
2. Set up detected language toolchains (Node, JDK, Python, Rust, .NET; Go static tooling needs no setup).
3. Detect languages.
4. Generate map with Node process and 6GB max old-space setting.
5. Upload map.
6. Compute/upload insights.

Primary source: [`action.yml`](https://github.com/Codesee-io/codesee-action/blob/main/action.yml); map action metadata: [`map/action.yml`](https://github.com/Codesee-io/codesee-action/blob/main/map/action.yml); implementation entry points: [`map/src/action.js`](https://github.com/Codesee-io/codesee-action/blob/main/map/src/action.js) and [`map/src/insights.js`](https://github.com/Codesee-io/codesee-action/blob/main/map/src/insights.js).

Open-source analyzers include [`codesee-deps-go`](https://github.com/Codesee-io/codesee-deps-go) and [`codesee-deps-dotnet`](https://github.com/Codesee-io/codesee-deps-dotnet). A complete multi-language analyzer/backend is not published as one OSS repository; action orchestration and selected analyzers are open, while hosted visualization/product remained proprietary.

### Data model and query capabilities

Official map docs describe:

- **Codebase Map:** files/folders as nodes; arrows point from a file/folder to dependency it uses.
- **Review Map:** dependency map plus change status for PR review.
- **Function Maps:** function/class/type-level relationships.
- **Service Maps:** services/external systems built from OpenTelemetry or Datadog traces.
- **Insights:** engineering hot spots, latest activity, creation date, lines of code.
- **Tours/comments:** curated walkthroughs and persistent collaboration context.

Sources: [Explore Your Map](https://docs.codesee.io/docs/explore-your-map), [Codebase Maps](https://www.codesee.io/codebase-maps), [GitKraken acquisition feature list](https://www.gitkraken.com/blog/gitkraken-launches-devex-platform-acquires-codesee).

Primary query surface was interactive hosted visualization—search/filter, upstream/downstream dependency exploration, drill-down, tours, and comments—not a general graph DSL or agent API. No first-party MCP server was found in the CodeSee organization, package surface, or official documentation as of 2026-08-02. MCP post-dates CodeSee’s standalone shutdown; absence claim is based on organization/API search, not proof that no private prototype existed.

### Performance characteristics

Official sources reviewed provide no reproducible indexing/query benchmark. Action’s `NODE_OPTIONS: --max-old-space-size=6144` is evidence of configured memory ceiling, not actual requirement or measured performance. Claims should focus on CI isolation and automatic updates, not unsupported speed comparisons. [`codesee-action/action.yml`](https://github.com/Codesee-io/codesee-action/blob/main/action.yml).

### Innovations and differentiators

1. **CI-side privacy boundary:** repository code remains in code host/runner; derived map uploads to service.
2. **Visual-first onboarding:** directory, dependency, and change maps create fast mental models for humans.
3. **Collaboration layer:** tours, comments, custom views, and PR Review Maps capture explanation around structure.
4. **Static + runtime maps:** code dependencies and telemetry-derived service flows share visual language.
5. **Always-updated product loop:** commit/PR events refresh maps without local manual indexing.

### Lessons for LeanKG

- Offer CI-generated, revision-addressed snapshots without uploading source bodies.
- Add guided architecture tours or shareable saved subgraphs on top of current UI.
- Join static service edges with optional OpenTelemetry evidence while retaining provenance.
- Put latest activity, creation age, churn, and ownership directly on graph views.
- Avoid depending on hosted visualization for core agent queries; local MCP is strategic advantage.

---

## 7. Cross-tool comparison and recommendations for LeanKG

### Storage and semantic-depth trade-off

| Pattern | Strength | Weakness | LeanKG action |
|---|---|---|---|
| SQLite/FTS code graph (CodeGraph) | Simple local deployment, strong exact search, cheap incremental writes | Recursive/analytic graph operations become custom SQL/CTEs | Keep CozoDB graph strengths; benchmark exact lookup against FTS side indexes |
| NetworkX + JSON (Graphify) | Portable, inspectable, merge/share friendly | Weak concurrent/incremental serving and large-graph memory behavior | Export deterministic snapshots, do not replace serving DB |
| Prompt-time PageRank (Aider) | Excellent token economics and task sensitivity | No durable graph query API or deep semantics | Add ranked orientation compiler over existing graph |
| Tag/cross-reference DB (ctags/Global/cscope) | Fast, cheap, build-independent, handles broken trees | Limited identity/type/call precision | Use as fallback/extraction tier with honest provenance |
| SCIP compiler index (Sourcegraph) | Precise identity/definitions/references/cross-repo packages | Requires language tooling and viable build configuration | Import as authoritative semantic overlay |
| Hosted visual map (CodeSee) | Human onboarding and collaboration | Closed service, weak agent programmability, shutdown/acquisition risk | Keep local core; add shareable visual artifacts and CI snapshots |

### Highest-value innovations to adopt

1. **Unified task-shaped context call.** Input: task + project/revision + budget. Output: ranked symbols, exact source slices, relationship paths, tests/docs/config, impact summary, provenance, freshness, and recovery handles.
2. **SCIP import overlay.** Map `Document`, `Occurrence`, `SymbolInformation`, relationships, package identity, and source encoding into CozoDB without discarding original fields.
3. **Aider-style ranked orientation.** Personalized graph score from mentioned identifiers, open/changed files, entry points, and task concepts; binary-fit to token budget.
4. **Portable context pack.** Graphify-style deterministic `graph.json`, architecture report, interactive HTML, confidence audit, source revision, and schema version.
5. **Freshness contract.** Every response reports indexed revision, dirty/unindexed files, watcher lag, and whether query waited for synchronization.
6. **Edge authority model.** At minimum: compiler/SCIP, extracted syntax, resolved heuristic, ambiguous token occurrence. Preserve producer/version/provenance.
7. **Exact-index fast path.** Name/prefix/file queries should avoid embeddings and broad graph traversal.
8. **Human navigation layer.** CodeSee-style saved views/tours, activity/churn overlays, and PR Review Maps over same local graph.

### What not to copy

- Do not multiply storage backends before current store reliability is proven.
- Do not treat GitHub stars or vendor benchmarks as quality evidence.
- Do not call token-based occurrences semantic references.
- Do not make model-dependent extraction mandatory for private code.
- Do not expose dozens of equal-priority MCP tools without a default workflow.
- Do not use portable snapshots as canonical live state.
- Do not couple core value to a hosted viewer that can be sunset.

---

## 8. Primary source index

### CodeGraph

- https://github.com/colbymchenry/codegraph
- https://github.com/colbymchenry/codegraph/blob/main/src/db/schema.sql
- https://github.com/colbymchenry/codegraph/blob/main/src/mcp/server-instructions.ts
- https://github.com/colbymchenry/codegraph/tree/main/codegraph-kernel
- https://github.com/colbymchenry/codegraph/tree/main/docs/benchmarks
- https://github.com/CodeGraphContext/CodeGraphContext
- https://github.com/CodeGraphContext/CodeGraphContext/blob/main/src/codegraphcontext/server.py
- https://github.com/CodeGraphContext/CodeGraphContext/blob/main/src/codegraphcontext/tool_definitions.py
- https://github.com/codegraph-ai/CodeGraph
- https://github.com/Lordymine/codegraph

### Graphify

- https://github.com/Graphify-Labs/graphify
- https://github.com/Graphify-Labs/graphify/blob/v8/ARCHITECTURE.md
- https://github.com/Graphify-Labs/graphify/blob/v8/BENCHMARKS.md
- https://github.com/Graphify-Labs/graphify/blob/v8/graphify/serve.py
- https://github.com/Graphify-Labs/graphify/tree/v8/graphify/extractors
- https://github.com/Graphify-Labs/graphify/tree/v8/graphify/skills

### Aider

- https://aider.chat/docs/repomap.html
- https://aider.chat/2023/10/22/repomap.html
- https://aider.chat/docs/languages.html
- https://github.com/Aider-AI/aider/blob/541bba6ef4a5385b8cf032201ec6e3f3e32a6ea6/aider/repomap.py
- https://github.com/Aider-AI/aider/blob/main/aider/website/docs/repomap.md
- https://github.com/Aider-AI/aider/blob/main/aider/website/docs/ctags.md
- https://github.com/Aider-AI/aider/tree/main/aider/queries

### Traditional tools and wrappers

- https://github.com/universal-ctags/ctags
- https://docs.ctags.io/en/latest/man/ctags.1.html
- https://docs.ctags.io/en/latest/man/ctags-json-output.5.html
- https://www.gnu.org/software/global/manual/global.html
- https://cscope.sourceforge.net/
- https://cscope.sourceforge.net/cscope_man_page.html
- https://sourceforge.net/p/cscope/cscope/ci/master/tree/
- https://github.com/harshithsunku/mcp-gtags-server
- https://github.com/ryogrid/gtags-mcp
- https://github.com/gladiatr72/mcp-ctags
- https://github.com/vishalkumar14/mcp-ctags
- https://github.com/netmute/ctags-lsp
- https://github.com/ruben2020/codequery
- https://github.com/agvxov/csope
- https://github.com/ray-x/ctags-mcp
- https://github.com/algorisys-oss/repograph
- https://github.com/Smattr/clink

### Sourcegraph, LSIF, SCIP

- https://github.com/scip-code/scip
- https://github.com/scip-code/scip/blob/main/scip.proto
- https://github.com/scip-code/scip/blob/main/docs/DESIGN.md
- https://github.com/scip-code/scip/blob/main/docs/CLI.md
- https://sourcegraph.com/blog/announcing-scip
- https://lsif.dev/
- https://github.com/microsoft/language-server-protocol/blob/main/indexFormat/specification.md
- https://sourcegraph.com/docs/code_navigation
- https://sourcegraph.com/docs/api/mcp
- https://github.com/sourcegraph/docs/blob/main/docs/api/mcp/index.mdx
- https://github.com/sourcegraph/docs/blob/main/docs/code-navigation/explanations/uploads.mdx
- https://github.com/sourcegraph/sourcegraph-public-snapshot
- https://github.com/sourcegraph/zoekt

### CodeSee

- https://www.codesee.io/how-codesee-works
- https://www.codesee.io/codebase-maps
- https://docs.codesee.io/docs/explore-your-map
- https://www.codesee.io/continuous-understanding
- https://github.com/Codesee-io/codesee-action
- https://github.com/Codesee-io/codesee-action/blob/main/action.yml
- https://github.com/Codesee-io/codesee-action/blob/main/map/action.yml
- https://github.com/Codesee-io/codesee-deps-go
- https://github.com/Codesee-io/codesee-deps-dotnet
- https://www.gitkraken.com/blog/gitkraken-launches-devex-platform-acquires-codesee
- https://www.prnewswire.com/news-releases/gitkraken-acquires-codesee-launches-new-devex-platform-including-support-for-google-geminis-ai-model-302144298.html

---

## Caveats

- GitHub stars, pushes, and releases are point-in-time observations from 2026-08-02 and will change.
- Vendor performance claims were not reproduced; corpus, model, hardware, prompts, and budget strongly affect results.
- “No MCP found” means no first-party public MCP integration appeared in official repository/doc/package searches; it cannot rule out private or abandoned prototypes.
- Language “support” varies from symbol extraction through full compiler-accurate references. This report avoids treating language count as semantic-depth parity.
- CodeGraph and Graphify names have many unrelated repositories; owner-qualified URLs are required in all product decisions.
