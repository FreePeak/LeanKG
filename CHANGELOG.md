# Changelog

All notable changes to this project are documented in this file.

## [0.19.7](https://github.com/FreePeak/LeanKG/compare/v0.19.6...v0.19.7) (2026-07-24)


### Features

* dynamic ontology CRUD for agent memory ([0a1ab26](https://github.com/FreePeak/LeanKG/commit/0a1ab26f236006150bff77aed201a36277bfd17b))

## [0.19.6](https://github.com/FreePeak/LeanKG/compare/v0.19.5...v0.19.6) (2026-07-23)


### Features

* **benchmark:** codegraph-style cross-tool agent A/B harness (US-CT-BMK) — Alamofire verified ([025ce8b](https://github.com/FreePeak/LeanKG/commit/025ce8b2a111945a653ac8f9bdf9a76d9e09b924))


### Bug Fixes

* use html_url instead of url in release-please verify step ([f708b29](https://github.com/FreePeak/LeanKG/commit/f708b29e75159efcdc5c8c78eefd0e8813d5a2c8))
* use html_url instead of url in release-please verify step ([6c02160](https://github.com/FreePeak/LeanKG/commit/6c02160d340c196e72808b32088b024350575a2d))

## [0.19.5](https://github.com/FreePeak/LeanKG/compare/v0.19.4...v0.19.5) (2026-07-23)


### Features

* AB Testing & Validation for LeanKG MCP Server ([#11](https://github.com/FreePeak/LeanKG/issues/11)) ([00508b6](https://github.com/FreePeak/LeanKG/commit/00508b69a219db469c8b1eecc41a1196904db4f1))
* add --dir flag to mcp-stdio command for explicit directory ([#39](https://github.com/FreePeak/LeanKG/issues/39)) ([18f708e](https://github.com/FreePeak/LeanKG/commit/18f708ee877d7526dfa2d2db7b20d641c180e86b))
* add /workspace-be volume mount to docker-compose.rocksdb.yml ([3f53030](https://github.com/FreePeak/LeanKG/commit/3f5303020a72860e8e6606e66b93f665fe6a1882))
* add A/B test benchmark (LeanKG tools vs manual grep/find) ([357546d](https://github.com/FreePeak/LeanKG/commit/357546db32bcd6a2f441c125475504c48b306686))
* add A/B testing benchmark for LeanKG vs baseline ([bd8a5c3](https://github.com/FreePeak/LeanKG/commit/bd8a5c3aae7ac96b14322293cd898bf5f071c751))
* Add Android XML layout and manifest support ([#34](https://github.com/FreePeak/LeanKG/issues/34)) ([ff66111](https://github.com/FreePeak/LeanKG/commit/ff66111cf23968f671d100f73cff5d7cbf1f72cd))
* add Claude-Mem-like session management hooks ([3a5b88e](https://github.com/FreePeak/LeanKG/commit/3a5b88ef5f88b77a25474fa2bec18450846f1811))
* add Claude-Mem-like session management hooks ([7bec2bc](https://github.com/FreePeak/LeanKG/commit/7bec2bc8968209a68bf16ab6079c1277118945d9))
* add CLI fallback rules when MCP server unavailable ([#31](https://github.com/FreePeak/LeanKG/issues/31)) ([c534d48](https://github.com/FreePeak/LeanKG/commit/c534d48b3ff54b1379423edca91ef48352ac93ea))
* Add context metrics tracking with CLI and seed command ([#26](https://github.com/FreePeak/LeanKG/issues/26)) ([0b01117](https://github.com/FreePeak/LeanKG/commit/0b01117f34673020f4cc1b3aa50b37dc36d0581c))
* add context usage metrics + A/B comparison to tool-bench ([0c02100](https://github.com/FreePeak/LeanKG/commit/0c021005a064dbcb488cbf81403f3e5a448799a5))
* add correctness tracking to metrics summary ([9ee96ae](https://github.com/FreePeak/LeanKG/commit/9ee96ae20293acf153c0b4ab5335241cb2d5221f))
* Add Dart and Swift language indexing support ([#33](https://github.com/FreePeak/LeanKG/issues/33)) ([97d805a](https://github.com/FreePeak/LeanKG/commit/97d805aaed91ec33095706c8867a88e9195deb03))
* add database config structure for future PostgreSQL support ([d88ba6e](https://github.com/FreePeak/LeanKG/commit/d88ba6edfdab121b5435cd304eb293cc3d7ac0ed))
* add disk-persistent caching layer using CozoDB ([4904e72](https://github.com/FreePeak/LeanKG/commit/4904e726b8158df48ba852882dc7b35653064c1b))
* add efficiency & quality metrics to A/B test + auto-generate markdown report ([7bae909](https://github.com/FreePeak/LeanKG/commit/7bae9096d7f0e44f9f8a241acf3e998dcfd7324c))
* add environment namespacing and incident data model for v2 ([990d47a](https://github.com/FreePeak/LeanKG/commit/990d47a75c7bdc222c7538726d0f9f7fb282d216))
* add external REST API with API key management ([#2](https://github.com/FreePeak/LeanKG/issues/2)) ([1cb923d](https://github.com/FreePeak/LeanKG/commit/1cb923d7a4146cbf0233178b1d3738c1743a4bf8))
* add Fly.io free tier deployment support ([92ffe29](https://github.com/FreePeak/LeanKG/commit/92ffe2994a03429693387a733c9db1fb8e1547b4))
* add GitHub Codespaces devcontainer for demo ([8a4c3cf](https://github.com/FreePeak/LeanKG/commit/8a4c3cfb0c31179db8e950d0d37851b6aea68cf3))
* add GraphEngine.vacuum() to reclaim db file space ([4c3ca1f](https://github.com/FreePeak/LeanKG/commit/4c3ca1f1466b024cf65d4c00e058c953797474a2))
* add ignore folders ([e265f4c](https://github.com/FreePeak/LeanKG/commit/e265f4c7258ad09e9efe6b280925e39ae83eed31))
* add input/output/total token usage comparison to A/B test ([b604537](https://github.com/FreePeak/LeanKG/commit/b604537168c1e7687cc44750d359d76f5437f19c))
* add Java language support ([#12](https://github.com/FreePeak/LeanKG/issues/12)) ([13db1e8](https://github.com/FreePeak/LeanKG/commit/13db1e80ed90e76ea658bde8fc65ae0505a34e0d))
* add knowledge contribution, versioning, and RBAC via MCP ([7756834](https://github.com/FreePeak/LeanKG/commit/7756834d960928f063eb401e6a6d9791236290c6))
* add Kotlin import extraction in EntityExtractor ([5d71841](https://github.com/FreePeak/LeanKG/commit/5d71841bec07cbffca8a9a2507e967b21a3ecf31))
* add Kotlin language support ([#15](https://github.com/FreePeak/LeanKG/issues/15)) ([d7af258](https://github.com/FreePeak/LeanKG/commit/d7af25883f0e48c04bf4a1807a52fba8359dcafb))
* add leankg proc command for process management ([#11](https://github.com/FreePeak/LeanKG/issues/11)) ([4e26d63](https://github.com/FreePeak/LeanKG/commit/4e26d63228e1cb94def990b403ddfc43514b9bab))
* add LeanKG-Obsidian integration plan ([daa0c51](https://github.com/FreePeak/LeanKG/commit/daa0c51166b69e7bc4d4c80f2965a1ee77bb8097))
* add MCP HTTP transport for remote MCP server ([d377de2](https://github.com/FreePeak/LeanKG/commit/d377de2e0ea010fe7f61a6c605b7d50443d075e0))
* add memory-efficient query methods and cache optimizations ([#30](https://github.com/FreePeak/LeanKG/issues/30)) ([debd42e](https://github.com/FreePeak/LeanKG/commit/debd42ef3a8b2fbc4ee91bc4566f045f152247c1))
* add multi-project support for MCP HTTP server ([8b1bdda](https://github.com/FreePeak/LeanKG/commit/8b1bdda9a8e6b75890c1a9b95c211456e2bfddc1))
* add multiple layout algorithms and layout selector dropdown ([02b7e6b](https://github.com/FreePeak/LeanKG/commit/02b7e6b1922e89d60628334d95e7dd192d0a607d))
* add native update command to CLI ([#38](https://github.com/FreePeak/LeanKG/issues/38)) ([2ae702e](https://github.com/FreePeak/LeanKG/commit/2ae702e4b7ea166633a65502e19a2fba97f8b46e))
* add ontology semantic search layer for agentic queries ([#50](https://github.com/FreePeak/LeanKG/issues/50)) ([fe5df7b](https://github.com/FreePeak/LeanKG/commit/fe5df7b600aa83a65512f320113dbb01c7c50f61))
* add ontology-tools benchmark suite + tool-bench CLI command ([68009ba](https://github.com/FreePeak/LeanKG/commit/68009bac0535c545cf0fd1072a584c1965a40e1d))
* add orchestrator module with cache-graph-compress flow ([#14](https://github.com/FreePeak/LeanKG/issues/14)) ([15fb1d3](https://github.com/FreePeak/LeanKG/commit/15fb1d3b37c4605b21e91c38a27675f81ae9f0ff))
* add per-request auto-index for HTTP server project param ([4d67517](https://github.com/FreePeak/LeanKG/commit/4d67517e96263c8627a0b62a419836559ddab4b3))
* add RocksDB storage engine, dynamic schema detection, and multi-project HTTP MCP routing fixes ([6ad2437](https://github.com/FreePeak/LeanKG/commit/6ad243796aa517d167919392ccb1da0de660095b))
* add RTK-style compression for LeanKG CLI commands ([#18](https://github.com/FreePeak/LeanKG/issues/18)) ([43a1d13](https://github.com/FreePeak/LeanKG/commit/43a1d132f7ab755f039787205f2391c84f9907da))
* add semantic_search MCP tool with keyword+fuzzy fallback ([2fe4682](https://github.com/FreePeak/LeanKG/commit/2fe46827684ba853c5e2e55ac9dd91edfe262eb4))
* add session coordination and auto-reload for MCP HTTP server ([b463571](https://github.com/FreePeak/LeanKG/commit/b463571e9569d4960d5aea08270ecc06d3cf7edf))
* Add support for C++, C#, Ruby, PHP ([#30](https://github.com/FreePeak/LeanKG/issues/30)) ([5f4a1fc](https://github.com/FreePeak/LeanKG/commit/5f4a1fc9b8c45e0d10f9b9beec6bca70f6c363a9))
* add token budget enforcement for MCP tools ([d9bb1f3](https://github.com/FreePeak/LeanKG/commit/d9bb1f3f2ea19837408953d68e945e46610b435c))
* add v2 CLI commands for incident management and env conflicts ([007e9aa](https://github.com/FreePeak/LeanKG/commit/007e9aae248f53bbbf78e316efb46acb503276b8))
* add v2 graph engine queries for incidents and env conflicts ([54675a7](https://github.com/FreePeak/LeanKG/commit/54675a7b584fc27ca2c96e9cc79f0131450f8ea3))
* add v2 MCP tools for incidents and environment conflicts ([3c338a9](https://github.com/FreePeak/LeanKG/commit/3c338a9124da7821e670753b4b314b1686e90694))
* add version command to CLI ([#8](https://github.com/FreePeak/LeanKG/issues/8)) ([a7bf943](https://github.com/FreePeak/LeanKG/commit/a7bf943368e32b693077e4a27134be7a5632849b))
* add Web UI v2 components for incidents and env conflicts ([7af34b4](https://github.com/FreePeak/LeanKG/commit/7af34b458ffb6672299681ca402e5e55da6c0aed))
* allow multiple concurrent MCP server sessions ([#17](https://github.com/FreePeak/LeanKG/issues/17)) ([8f70f43](https://github.com/FreePeak/LeanKG/commit/8f70f43377310dd9cd289a4cc1343fad46244562))
* Android extraction with view binding and resource relationships ([#10](https://github.com/FreePeak/LeanKG/issues/10)) ([d247423](https://github.com/FreePeak/LeanKG/commit/d247423120f49f5d68cd99a49e4ec5462eacb846))
* auto-start API server when MCP server starts ([#23](https://github.com/FreePeak/LeanKG/issues/23)) ([059d403](https://github.com/FreePeak/LeanKG/commit/059d403ae303b688ac0e6b11d47cc4ae2c681cb6))
* **benchmark:** add Python scripts for token extraction and comparison ([6d06227](https://github.com/FreePeak/LeanKG/commit/6d06227501d1dc5dc73d165800f43a4b5a722ffd))
* **benchmark:** add token tracker tests and README ([9b6707c](https://github.com/FreePeak/LeanKG/commit/9b6707c3db5664d9659727e92d2b85823043d369))
* **benchmark:** create directory structure, Makefile, and test queries ([9c00a51](https://github.com/FreePeak/LeanKG/commit/9c00a5155828f9d72a1f0f701b5d82cc861bc386))
* **cli:** add 'content' query kind for broad substring search ([f0355b0](https://github.com/FreePeak/LeanKG/commit/f0355b0a9b46d09ea82ea017e8e02a9ec3fea1ff))
* **cli:** add smoke-test subcommand for retrieval pipeline ([3c2b977](https://github.com/FreePeak/LeanKG/commit/3c2b977ec0f0320d0219a33dba4b2064d99d5549))
* comprehensive Android/Kotlin navigation and analysis improvements ([#18](https://github.com/FreePeak/LeanKG/issues/18)) ([9f75453](https://github.com/FreePeak/LeanKG/commit/9f754534e6f5b9e406ac3ea61e5e9b1dd026919a))
* concept-gated search workflow + kg_context code-refs resolution + trace_workflow step fallback + CLI --file/--function flags ([7d6f117](https://github.com/FreePeak/LeanKG/commit/7d6f1174c60f01e21438bbdad76bea30e164706b))
* connect mock MCP handlers to real graph engine implementations ([f362954](https://github.com/FreePeak/LeanKG/commit/f3629545200ff3b1dcaa7bf0c426e4cd6a6b7bbf))
* **docker:** one-command setup with index + embed + MCP ([fd74ecd](https://github.com/FreePeak/LeanKG/commit/fd74ecdd57b4e524230fdfb9848f2466742cbf08))
* **embed:** day-2 resume — skip fresh, HNSW no-op, hash-aware stale ([#81](https://github.com/FreePeak/LeanKG/issues/81)) ([25292d0](https://github.com/FreePeak/LeanKG/commit/25292d03b89779ae8c0fc54a4afd1a8dac1bd222))
* **embeddings:** migrate from usearch sidecar to CozoDB native HNSW ([604d03b](https://github.com/FreePeak/LeanKG/commit/604d03bdfd66426427721bcdf5c7cd601b5f5b3d))
* **embeddings:** phase 0 — add embeddings feature gate with fastembed + usearch ([4f99304](https://github.com/FreePeak/LeanKG/commit/4f99304be1a00df1d5de8c33382fbeef66a32f5f))
* **embeddings:** phase 1 — embeddings module skeleton + indexer hook ([3b576ef](https://github.com/FreePeak/LeanKG/commit/3b576ef115c9000c91846cb09dbe5401b45747b6))
* **embeddings:** phase 2 — retrieval pipeline (ANN + rerank + fallback) ([80855f9](https://github.com/FreePeak/LeanKG/commit/80855f9867af593227e15dc170b456ea3e96cffd))
* **embeddings:** phase 3 — adaptive KG traversal (Stage 4) ([80fd33e](https://github.com/FreePeak/LeanKG/commit/80fd33edd35019c04f8f98b4ab8b4fc0201cbb6b))
* **embeddings:** phase 4 — kg_semantic_context MCP tool ([8fd7800](https://github.com/FreePeak/LeanKG/commit/8fd780097513217b01f7317bd241fefce4ac004f))
* **embeddings:** phase 5 — embed + semantic-context CLI subcommands ([9f0d801](https://github.com/FreePeak/LeanKG/commit/9f0d801c3bbca7398f6a7466c2cb810157dbe0c2))
* **embeddings:** phase 6 — docs + state-table integration tests ([19b3349](https://github.com/FreePeak/LeanKG/commit/19b3349bed716175765866edd673d73aa365909d))
* **embeddings:** synthesize code signature fallback in text blob ([f23bd56](https://github.com/FreePeak/LeanKG/commit/f23bd566ddae83df08f7c68f67dce53b798bd64e))
* enable concurrent MCP server access via SQLite WAL mode ([123c3f2](https://github.com/FreePeak/LeanKG/commit/123c3f20021c2950e93b67b5c7bb7cd54176a8f1))
* enable SQLite WAL mode for concurrent MCP access ([bd475fd](https://github.com/FreePeak/LeanKG/commit/bd475fdd5e4e8c30866f6d644a474b3d9c834b62))
* enhance Cursor installation with plugin, skills, rules, and agents ([e625ea6](https://github.com/FreePeak/LeanKG/commit/e625ea60e91dfc755700da07e1f30eda5f138d37))
* enhance LeanKG bootstrap with grep-fallback pattern ([61cf3f4](https://github.com/FreePeak/LeanKG/commit/61cf3f495274695b6090873156d3b7f5144f8101))
* expand noise call filter for JS/TS, Python, and Go ([#9](https://github.com/FreePeak/LeanKG/issues/9)) ([13c0b30](https://github.com/FreePeak/LeanKG/commit/13c0b30dc52e6cd4151817adfee3cec9d92aa355))
* **gitnexus:** add detect-clusters CLI command ([fa97227](https://github.com/FreePeak/LeanKG/commit/fa972279d4741bd1803df1214074aa3c2c311b25))
* **gitnexus:** US-GN-01 confidence scoring on relationships ([a365c50](https://github.com/FreePeak/LeanKG/commit/a365c507f2dc6c26c65cc33f722cd478ef318a52))
* **gitnexus:** US-GN-02 detect_changes pre-commit risk analysis tool ([22c2226](https://github.com/FreePeak/LeanKG/commit/22c22262c63c1d1951a50627194ea23a9682728b))
* **gitnexus:** US-GN-03 multi-repo global registry CLI ([b3f5e44](https://github.com/FreePeak/LeanKG/commit/b3f5e446d267e64a8c853116f68834da7c7cc178))
* **gitnexus:** US-GN-04/05 community detection and US-GN-06 enhanced context ([5983c93](https://github.com/FreePeak/LeanKG/commit/5983c93311a29a8e9b47e09d718024f0c56f2503))
* **graph:** US-GF-03 query_graph NL scoped subgraph ([#84](https://github.com/FreePeak/LeanKG/issues/84)) ([a752654](https://github.com/FreePeak/LeanKG/commit/a7526545e9f6db773bcffa347122a4c625a727f3))
* hard-delete wake_up and search_by_environment ([b7d4c5a](https://github.com/FreePeak/LeanKG/commit/b7d4c5af7a02326464fe83377262c266dd973b9c))
* hard-delete wake_up and search_by_environment (Wave 1a) ([83c351d](https://github.com/FreePeak/LeanKG/commit/83c351dc6803bd25952ae52a26237cc199f0ee45))
* honest edge provenance (Wave 2a) + company adoption waves 0a–1c ([0f5944b](https://github.com/FreePeak/LeanKG/commit/0f5944be93a75f0097672c13fe395bb00c822dba))
* honest edge provenance and company adoption waves ([39a8042](https://github.com/FreePeak/LeanKG/commit/39a80423ee024fde6dc70418aae6da219f0e042d))
* implement Export and Watch CLI commands ([#10](https://github.com/FreePeak/LeanKG/issues/10)) ([09cd82c](https://github.com/FreePeak/LeanKG/commit/09cd82cd8eacd0a3ad5b322ffdc3a97bb5c6b71e))
* **indexer:** add Android/Kotlin extractors for WorkManager, CoroutineDispatcher, ViewModel/Repository ([2eb1a84](https://github.com/FreePeak/LeanKG/commit/2eb1a846607e85da5114de8cbefa7694e094ec49))
* knowledge contribution, versioning, and RBAC via MCP ([7c259aa](https://github.com/FreePeak/LeanKG/commit/7c259aa843e10edaa1ec349692905baa6fe41b18))
* LeanKG v2 — Environment Namespacing & Incident Knowledge Layer ([8021f37](https://github.com/FreePeak/LeanKG/commit/8021f37ce46bae224c6272bdfc1dfb985e5ca15b))
* leankg web/serve now starts both backend and Vite dev server ([#43](https://github.com/FreePeak/LeanKG/issues/43)) ([11a6645](https://github.com/FreePeak/LeanKG/commit/11a6645791df14de64cbf463fd5e425d2f5b1b59))
* **lsp:** hybrid typed resolve Go/TS + SURF soft-deprecate ([#83](https://github.com/FreePeak/LeanKG/issues/83)) ([8ffe116](https://github.com/FreePeak/LeanKG/commit/8ffe116244407519b7275972b1cd2896454f8cec))
* MCP get_callers tool (reverse call graph) ([#6](https://github.com/FreePeak/LeanKG/issues/6)) ([#13](https://github.com/FreePeak/LeanKG/issues/13)) ([eae3718](https://github.com/FreePeak/LeanKG/commit/eae371863f21321bbb764c59fac1890ee8590d3e))
* MCP Token Compression & Context Bounds Integration ([294ca76](https://github.com/FreePeak/LeanKG/commit/294ca76bd807efa1bccc6e5c7cb1f22160ab1634))
* MCP token compression & lean-ctx features integration ([d7b0554](https://github.com/FreePeak/LeanKG/commit/d7b0554dba9e84b8d5df421b121e06b926647595))
* **mcp:** add hourly scheduled vacuum job ([7c47661](https://github.com/FreePeak/LeanKG/commit/7c476612fe243603f15d5af7e5b3691a8772ecea))
* **mcp:** add per-file error details to skipped files in mcp_index ([24210bf](https://github.com/FreePeak/LeanKG/commit/24210bfc67e8d6d02298136678d2b4dd5cea048c))
* **mcp:** embed_control idle resume + full tool redundancy audit ([#86](https://github.com/FreePeak/LeanKG/issues/86)) ([a89a2cc](https://github.com/FreePeak/LeanKG/commit/a89a2cc3c5bde7a7aa3117a2d07ed721ab698060))
* **mcp:** per-project MCP configuration for Cursor + serve_directly fix ([d24fdf9](https://github.com/FreePeak/LeanKG/commit/d24fdf947cc9d85d45bfed51a65606260a99130c))
* **mcp:** tool surface rationalization (FR-SURF-01..03) ([#82](https://github.com/FreePeak/LeanKG/issues/82)) ([94577d2](https://github.com/FreePeak/LeanKG/commit/94577d29b9555ce133b922fee896f53f30a6b209))
* memory optimizations - LEANKG_MMAP_SIZE env var and memory-efficient queries ([006353e](https://github.com/FreePeak/LeanKG/commit/006353e2b33348854b9d946677d074df68b7ccbd))
* merge v2 CLI branch ([371888b](https://github.com/FreePeak/LeanKG/commit/371888b2584e9f5dfbb9f7e696ad8ff23d47b7ea))
* merge v2 data model, graph engine, MCP tools, and CLI branches ([16373ef](https://github.com/FreePeak/LeanKG/commit/16373efdea42924db51114df2be19a3b3b1bc4f5))
* merge v2 MCP tools branch ([2ef5691](https://github.com/FreePeak/LeanKG/commit/2ef569117695567a0a13c6bd7eded5ef1cb72ec4))
* migrate deployment from fly.io to render.com ([40ce999](https://github.com/FreePeak/LeanKG/commit/40ce9998563c9cb27bb7be9d8a9b07096daacdc3))
* Obsidian vault integration for annotation IDE ([a66132f](https://github.com/FreePeak/LeanKG/commit/a66132fd0b7770bc6c7709d7ca2d482fc32dfb60))
* Obsidian vault integration for annotation IDE ([#35](https://github.com/FreePeak/LeanKG/issues/35)) ([9786fc1](https://github.com/FreePeak/LeanKG/commit/9786fc1a65aaec1c8f5f7fe5df6ec66a40445d43))
* Optimized Local-First Vector Graph Engine (v3.7 P0) ([#79](https://github.com/FreePeak/LeanKG/issues/79)) ([dbc22c4](https://github.com/FreePeak/LeanKG/commit/dbc22c48be894d3e405035480b78be79e55e9501))
* Phase 1 - HTTP route extraction for Go and TypeScript frameworks ([#68](https://github.com/FreePeak/LeanKG/issues/68)) ([a670875](https://github.com/FreePeak/LeanKG/commit/a6708756dbe2b83f889206116f09403799c26bee))
* Phase 1-2 v2 stabilization ([#49](https://github.com/FreePeak/LeanKG/issues/49)) ([fb2e7b7](https://github.com/FreePeak/LeanKG/commit/fb2e7b7099c9addd30164f077be2c002126c0f09))
* Phase 5 team rollout - team model, permissions, onboarding, shared graph ([#52](https://github.com/FreePeak/LeanKG/issues/52)) ([60905b6](https://github.com/FreePeak/LeanKG/commit/60905b6feec0863eacc1ed0a44f49a91b9b844c2))
* PRD v3.6.2 HNSW semantic + LSP bridge + performance/OOM safety ([#72](https://github.com/FreePeak/LeanKG/issues/72)) ([90e0f9d](https://github.com/FreePeak/LeanKG/commit/90e0f9d6b263adaec1b0030f4f302af35d757616))
* procedural ontology auto-update while serving ([#93](https://github.com/FreePeak/LeanKG/issues/93)) ([815a1b6](https://github.com/FreePeak/LeanKG/commit/815a1b6d4b3e3d1d6fe094d7af346a9e58d9a440))
* replace D3.js with sigma.js for graph visualization ([490848c](https://github.com/FreePeak/LeanKG/commit/490848c713313196069096f3b6d1810cf2016534))
* replace using-leankg skill with PreToolUse hooks ([#20](https://github.com/FreePeak/LeanKG/issues/20)) ([a4066fe](https://github.com/FreePeak/LeanKG/commit/a4066fe0c51bfe7e7edcb8bf4f13d72780f96e4c))
* resolve markdown doc-code joins ([401eac1](https://github.com/FreePeak/LeanKG/commit/401eac12601e76de664b384f6d6df8b463860ddf))
* resolve markdown doc-code joins (DOCJOIN) ([8f2d5df](https://github.com/FreePeak/LeanKG/commit/8f2d5dfcb755004bfe865af849cc589e8e491851))
* restore update command for self-updating LeanKG binary ([5e21e32](https://github.com/FreePeak/LeanKG/commit/5e21e326044681f51b98b76db797e47ad9fd1bad))
* **retrieval:** adaptive ANN depth based on index size ([9e17cb9](https://github.com/FreePeak/LeanKG/commit/9e17cb92095a0a2b2eb2fb51711875dfff46dfd7))
* **retrieval:** per-node-type candidate filtering ([b52e755](https://github.com/FreePeak/LeanKG/commit/b52e755afa0db4551fbc6aa2a7423d77c8ead445))
* **retrieval:** use full blob for rerank, filter test-name candidates ([9e97588](https://github.com/FreePeak/LeanKG/commit/9e975886ea17982f5b1edc93f729ed74ff704874))
* **ship:** add automated shipping workflow with Superpowers and LeanKG ([0bc8cd1](https://github.com/FreePeak/LeanKG/commit/0bc8cd164db3ed4f345adc7f25ce43120dc87c65))
* **structural-parity:** Phase 1 — resolution_method, get_architecture, get_graph_schema, find_dead_code ([#67](https://github.com/FreePeak/LeanKG/issues/67)) ([8b0fb5c](https://github.com/FreePeak/LeanKG/commit/8b0fb5cb4b7d5bffeb5261a3dc8569721ed13693))
* **ui-v2:** expand load-more pagination and folder sidebar ([d217d18](https://github.com/FreePeak/LeanKG/commit/d217d18f409b91ab2fea766ea8165cd21ed938c9))
* **ui:** embed UI v2 for serve, Docker, and onrender ([#90](https://github.com/FreePeak/LeanKG/issues/90)) ([e85acb2](https://github.com/FreePeak/LeanKG/commit/e85acb2620b1f1a3f5652c5615d4c2e62973b85e))
* **ui:** LeanKG UI v2 graph shell (Phase 1) ([#89](https://github.com/FreePeak/LeanKG/issues/89)) ([b99f2e7](https://github.com/FreePeak/LeanKG/commit/b99f2e798700fb942598bde510af96fd6ab2bed4))
* Update install script with LeanKG rules hierarchy and E2E fixes ([2573e68](https://github.com/FreePeak/LeanKG/commit/2573e683b3db69ca29c375ad5e5d3e96a79a6f97))
* Update LeanKG skill with stricter enforcement ([a81a72a](https://github.com/FreePeak/LeanKG/commit/a81a72ac98e95ba30558aca44e97d03f3be8f7e0))
* **US-19:** complete resolve_call_edges implementation ([6268cd1](https://github.com/FreePeak/LeanKG/commit/6268cd1b1ce11591ff64479528634e1fd4451b40))
* **US-26:** fix doc reference extraction ([b964cbd](https://github.com/FreePeak/LeanKG/commit/b964cbd8dbf15d4aa92f8edcbe8f78af2c7dd43f))
* **vector-engine:** close P0 quality gate with A/B evidence ([#80](https://github.com/FreePeak/LeanKG/issues/80)) ([8c8932b](https://github.com/FreePeak/LeanKG/commit/8c8932baee58a8eb87918a6c69cf9113c8e181c9))
* web UI / UX reconstruction & graph physics stabilization ([#40](https://github.com/FreePeak/LeanKG/issues/40)) ([2eb2c71](https://github.com/FreePeak/LeanKG/commit/2eb2c71c28b197e95d164e53a8f4fc4c89da987e))
* **web:** add current_project_path and new routes for path selector ([81969b0](https://github.com/FreePeak/LeanKG/commit/81969b0afce898a93d0ab415d6a352e75ed0174a))
* **web:** add project selector page with GitHub URL and local path support ([8b11dd2](https://github.com/FreePeak/LeanKG/commit/8b11dd21e8ce24aec94f69e00d989314fec323ff))
* **web:** add project selector page with GitHub URL support ([ab41cec](https://github.com/FreePeak/LeanKG/commit/ab41ceca6a08b1b3b60e8d3afec7b88ea5108218))
* **web:** add tooltip on graph node hover showing name, type and related nodes ([#3](https://github.com/FreePeak/LeanKG/issues/3)) ([a1bd3cb](https://github.com/FreePeak/LeanKG/commit/a1bd3cb0a163b76eaad6b896a9fe45ef5ce7a90c))


### Bug Fixes

* add --version flag support to CLI ([cf4c697](https://github.com/FreePeak/LeanKG/commit/cf4c697d61c4acf92c240975c5ff179e6dea3edb))
* add * prefix and use row count for ontology status queries ([68dd72c](https://github.com/FreePeak/LeanKG/commit/68dd72c7c50bb8b3a3003e5c1d7337a13334fa50))
* add clippy allow for regex creation in loops ([6f766b2](https://github.com/FreePeak/LeanKG/commit/6f766b2b8bbcec583fb606a6c676cbf8d872890a))
* add database size limits and cache eviction to prevent unbounded growth ([9338e41](https://github.com/FreePeak/LeanKG/commit/9338e4170cb0366d64215a65abda1da6b0a6016f))
* add docker resource limits and safer container defaults ([558a8e9](https://github.com/FreePeak/LeanKG/commit/558a8e914666eecc85dcf4469e0e8df5450e0efa))
* add git to Dockerfile for Fly.io builds ([d96767b](https://github.com/FreePeak/LeanKG/commit/d96767bf7452923ff16ec3b82a7828d1c00a9af9))
* add git to runtime stage for web UI git operations ([b12a527](https://github.com/FreePeak/LeanKG/commit/b12a527c8c47e493074a5912d60082a05b5f9f4d))
* add memory limits and single-instance lock for MCP server ([#15](https://github.com/FreePeak/LeanKG/issues/15)) ([8b6da1b](https://github.com/FreePeak/LeanKG/commit/8b6da1b018d64f20ec533e8f06c818ee078d5a53))
* add missing confidence column to relationship queries to resolve arity mismatch ([f97c160](https://github.com/FreePeak/LeanKG/commit/f97c1602b89b3a23dbaed88ba6ecd819dd768e92))
* add missing MCP tool handlers and normalize mcp_init path ([9e16a6d](https://github.com/FreePeak/LeanKG/commit/9e16a6d315878de388f49aa8d436e9120049a38a))
* add missing metrics correctness fields to models ([14fd2f2](https://github.com/FreePeak/LeanKG/commit/14fd2f279ede5e3a740915d6b4cf66a112af33a4))
* add path normalization for CozoDB queries to handle ./ prefix ([52b337c](https://github.com/FreePeak/LeanKG/commit/52b337c815219c50906c03930f0d63f710946cf2))
* add rm before cp in release pipeline to fix Windows build ([cb1bde0](https://github.com/FreePeak/LeanKG/commit/cb1bde0809d32e18f6f32662abfbe41b343a900c))
* add src/embed/assets/ to .safeskillignore ([96d6aae](https://github.com/FreePeak/LeanKG/commit/96d6aae3d02ea55a1d80403201a466658ab6de29))
* add target to publish job to prevent cross-compile verification failure ([15389dc](https://github.com/FreePeak/LeanKG/commit/15389dc56b6b333c39c24df2362fcaf7ad45be3e))
* allow cargo/npm build commands through hook ([6aa1614](https://github.com/FreePeak/LeanKG/commit/6aa16146cc64f668a96288967a61d07cf02abf9a))
* **api:** return 500 instead of panicking when ApiKeyStore init fails ([#78](https://github.com/FreePeak/LeanKG/issues/78)) ([bbc645e](https://github.com/FreePeak/LeanKG/commit/bbc645e2228fd1cd80eec5fa7faf91f13f1e72bf)), closes [#70](https://github.com/FreePeak/LeanKG/issues/70)
* apply path normalization for CozoDB queries and add cache integration tests ([e66f59a](https://github.com/FreePeak/LeanKG/commit/e66f59a33c128e0057f92c492e59eb61f3bbbf4f))
* avoid absolutizing graph query paths ([#56](https://github.com/FreePeak/LeanKG/issues/56)) ([a64aa2a](https://github.com/FreePeak/LeanKG/commit/a64aa2a1714cae9fe37d0cd55096d1e304a18065))
* bump version to v0.14.5 for crates.io publish ([938e1c9](https://github.com/FreePeak/LeanKG/commit/938e1c989b18328ca5184100400566354c1ee840))
* bump version to v0.15.2 ([31a7d67](https://github.com/FreePeak/LeanKG/commit/31a7d671d26a945b3c4b14b50c54cf8adc99482e))
* bump version to v0.15.3 ([50e16c3](https://github.com/FreePeak/LeanKG/commit/50e16c35fc6d9eb6d25f80cebb8070229bb5df74))
* cap indexer file size, expand default excludes ([a640546](https://github.com/FreePeak/LeanKG/commit/a6405468ad88772a9fcfbd178a897de71c87696e))
* **ci:** restore green format check, build, and tests ([77030d5](https://github.com/FreePeak/LeanKG/commit/77030d55cc8e6faa235b0f5eb173359627936ddd))
* clarify tool result handling and document unused PostgreSQL fields ([32aa40a](https://github.com/FreePeak/LeanKG/commit/32aa40a6ff257f7d34aace8925fd19454cb3cff7))
* clear PR-introduced clippy warning; re-run unit + live tests ([df17e40](https://github.com/FreePeak/LeanKG/commit/df17e402fcb9d7dfd7f9038448007576a61dd99f))
* **clippy:** resolve -D warnings violations under cargo clippy --all ([10a1509](https://github.com/FreePeak/LeanKG/commit/10a1509c7bbecd6df2b14f83c2d8bd1aac3f3a8e))
* copy full ui directory for build, not just package files ([0db1eb7](https://github.com/FreePeak/LeanKG/commit/0db1eb7b6b4a9eee682d4e22014ec07384d0b085))
* correct byte string literal syntax in test_detect_gradle_submodules ([f548228](https://github.com/FreePeak/LeanKG/commit/f548228b8b38d2021875474b7b522ea1cc7371d6))
* correct call edge resolution query and remove broken delete ([b154b54](https://github.com/FreePeak/LeanKG/commit/b154b54e974099c365aa7942fce95c9056461787))
* correct jq variable name in configure_cursor/claude/gemini functions ([d6157b7](https://github.com/FreePeak/LeanKG/commit/d6157b7b959120805b6abe540392deac024b8ced))
* deduplicate context and impact results ([#19](https://github.com/FreePeak/LeanKG/issues/19)) ([d89ba8a](https://github.com/FreePeak/LeanKG/commit/d89ba8acf164d8e89bddbd3bc3fcdd06fa6018ba)), closes [#14](https://github.com/FreePeak/LeanKG/issues/14)
* deduplicate context and impact results ([#20](https://github.com/FreePeak/LeanKG/issues/20)) ([be7470a](https://github.com/FreePeak/LeanKG/commit/be7470a778604d32a82699856c4d4153ee780cea)), closes [#14](https://github.com/FreePeak/LeanKG/issues/14)
* default auto_index_on_db_write to false ([6b07a27](https://github.com/FreePeak/LeanKG/commit/6b07a270b59e70a80b9d2c4f0650077147bc35c2))
* **docker:** route LEANKG_MCP_PROJECT through env_file for multi-project compose ([#66](https://github.com/FreePeak/LeanKG/issues/66)) ([faa89d3](https://github.com/FreePeak/LeanKG/commit/faa89d3b57a3b5a389248718118149de7fa6132d))
* eliminate all build warnings ([db2889b](https://github.com/FreePeak/LeanKG/commit/db2889be59cd1f8f9a0286792d19f6dba1dc8d9e))
* **embeddings:** compile fixes from arm64 Docker validation ([28243a5](https://github.com/FreePeak/LeanKG/commit/28243a57da900e2170ec010f6ca31cfa08eccfac))
* **embed:** HNSW path, MCP decoupling, and INT8 fast path ([#76](https://github.com/FreePeak/LeanKG/issues/76)) ([7032d6e](https://github.com/FreePeak/LeanKG/commit/7032d6e2afaf246d32ec5699c178439f02f5dc4d))
* enforce LeanKG usage by denying raw code search tools ([4a3a26c](https://github.com/FreePeak/LeanKG/commit/4a3a26cdeff145f1613ad26dc3801f76a8dc1530))
* ensure MCP server auto-init and auto-index work when .leankg exists ([828f286](https://github.com/FreePeak/LeanKG/commit/828f286086dd4a8ab7580c8d541f8ea562027d7a))
* extract project param from URL query for HTTP MCP server ([4d98496](https://github.com/FreePeak/LeanKG/commit/4d98496e177436c6996b1bf4cc47a6ccc5543d35))
* filter metrics by CONTEXT_TOOLS and skip negative token savings ([170d587](https://github.com/FreePeak/LeanKG/commit/170d58752d1a27e65fe0fafb94e0c7e4b0ba0d3b))
* filter out negative token savings in metrics display ([#36](https://github.com/FreePeak/LeanKG/issues/36)) ([daffa8c](https://github.com/FreePeak/LeanKG/commit/daffa8c7ff4646834db1baea1405990cde8d1e22))
* **graph:** improve visualization with degree-based sizing and hover highlighting ([68feb62](https://github.com/FreePeak/LeanKG/commit/68feb627f81e94a1889dca052ff8a1a69bad8c24))
* **graph:** skip indexer-noise neighbors in traverse_seeds ([4058555](https://github.com/FreePeak/LeanKG/commit/405855541c557347dfc5dbab1c99c069554cbe34))
* handle empty settings.json in configure_claude ([d644d3d](https://github.com/FreePeak/LeanKG/commit/d644d3d08cca0ebb81370dd46f804827af4c2fcf))
* handle legacy .leankg file vs directory conflict ([997bb95](https://github.com/FreePeak/LeanKG/commit/997bb9555b153dfd7b1834d4006afcab2a9f4a19))
* hide orphan nodes and optimize sigma.js performance ([76bc2c8](https://github.com/FreePeak/LeanKG/commit/76bc2c8cff7474ab4ab56f1a55d08aecc094b16e))
* hide orphan nodes in webui graph filters ([ea87c90](https://github.com/FreePeak/LeanKG/commit/ea87c905663370be09f39417cbda4019f821fe3f))
* improve graph performance by removing N+1 queries and filtering orphaned nodes ([96a7287](https://github.com/FreePeak/LeanKG/commit/96a7287147f8c370a24042692a3b2298a619dbff))
* improve MCP tool robustness and pagination ([96affa3](https://github.com/FreePeak/LeanKG/commit/96affa3b53a2991a2ca2b641a51c258337e930c7))
* improve OpenCode install script with robust JSON handling ([4d668dd](https://github.com/FreePeak/LeanKG/commit/4d668dd030540750c82eab6676aab7556ec8af4a))
* improve orchestrate tool to resolve module names ([#12](https://github.com/FreePeak/LeanKG/issues/12)) ([c9473d9](https://github.com/FreePeak/LeanKG/commit/c9473d98f0097349f7582aeffe001d95d79bbaaa))
* index *.tsx/*.jsx files and fix query regex patterns ([2d47635](https://github.com/FreePeak/LeanKG/commit/2d476355590c0022494ae6df2b9a4a0201692efa))
* index LeanKG codebase during Docker build for demo ([662a65f](https://github.com/FreePeak/LeanKG/commit/662a65fbcd099500e81d6cdd2ceb6895140e0793))
* invalidate GraphEngine cache after all write tools ([242fd23](https://github.com/FreePeak/LeanKG/commit/242fd236d94b2b52ba410eb583da5f1548a10ff6))
* lower LEANKG_MMAP_SIZE default to 64 MiB ([78a0ef4](https://github.com/FreePeak/LeanKG/commit/78a0ef405be8282df88d62fff587f3666ea496ee))
* make PreToolUse hook actually deny code search tools ([f3755c0](https://github.com/FreePeak/LeanKG/commit/f3755c0c79d426f887399d1c7c704a2b1e5799fe))
* MCP tool robustness and HTTP auto-index ([3631d10](https://github.com/FreePeak/LeanKG/commit/3631d104cdd329deddc0c05214d13f3271a6f635))
* MCP tools bug fixes ([#13](https://github.com/FreePeak/LeanKG/issues/13)) ([93e2fe5](https://github.com/FreePeak/LeanKG/commit/93e2fe5c7dd5fe27e06ef2aacfa404806f29f285))
* **mcp:** restore search availability on mega-graph boot ([#85](https://github.com/FreePeak/LeanKG/issues/85)) ([f5e26f5](https://github.com/FreePeak/LeanKG/commit/f5e26f5de252ae07dc2a371cece0ffabb9f44363))
* **mcp:** unblock HTTP listener + resolve RocksDB lock conflict on /workspace-be ([4f6422a](https://github.com/FreePeak/LeanKG/commit/4f6422a96a5ddb829156996b78c491bf5a7c10cb))
* mega HNSW semantic_search OOM (FR-SEM-07 / REL-054) ([#87](https://github.com/FreePeak/LeanKG/issues/87)) ([ce03fd8](https://github.com/FreePeak/LeanKG/commit/ce03fd85efa85df7eeee3876d730b60efbd0482a))
* mega-safe concept_search, query_graph, get_clusters (REL-055) ([#88](https://github.com/FreePeak/LeanKG/issues/88)) ([03b9179](https://github.com/FreePeak/LeanKG/commit/03b9179b0d3437d7d1c86881908c826253c43412))
* nested multi-repo auto-index + OOM-safe ontology queries ([#71](https://github.com/FreePeak/LeanKG/issues/71)) ([c44e306](https://github.com/FreePeak/LeanKG/commit/c44e30600877c04e4782d259e0202a7c3b7832b5))
* only block raw grep/find in Bash, allow Read/Grep/Glob ([d46cf79](https://github.com/FreePeak/LeanKG/commit/d46cf79fcce04ced1bd630754d1cc3ba18beee13))
* **onrender:** bake demo index at /app and reject project=/ ([2b5452c](https://github.com/FreePeak/LeanKG/commit/2b5452c17c64fa2901ba9ca4d5d2a3ab9a31071b))
* **onrender:** copy benches for Cargo manifest parse ([602e987](https://github.com/FreePeak/LeanKG/commit/602e987cbdc688ca83bce1db00f31dc977465f6a))
* **onrender:** copy benches for Cargo manifest parse ([7f310e9](https://github.com/FreePeak/LeanKG/commit/7f310e9656728284e47c44af4afe8779bdd896b8))
* **onrender:** multi-stage Docker build to stay under 8GB RAM ([2f9f7e6](https://github.com/FreePeak/LeanKG/commit/2f9f7e68e892ef47bfafec84194025e51ade1033))
* **onrender:** rebake ui-v2 embed and bust stale Docker UI cache ([9db7fed](https://github.com/FreePeak/LeanKG/commit/9db7fed4b21cb558a115eff9c0215f73e820b7ac))
* ontology sync on Docker startup, token budgets, match scoring, workflow aliases ([18bb8bf](https://github.com/FreePeak/LeanKG/commit/18bb8bf16309263321e942bd105a937c6cd82311))
* **ontology:** bind ontology_layer in query rules + add kg_self_test tool ([#62](https://github.com/FreePeak/LeanKG/issues/62)) ([94d5420](https://github.com/FreePeak/LeanKG/commit/94d5420a808dd65cddb910a670db6bd540955635))
* path normalization for CozoDB queries ([#28](https://github.com/FreePeak/LeanKG/issues/28)) ([a8011b9](https://github.com/FreePeak/LeanKG/commit/a8011b9cf2ff4dcb2d38513d744370c1f479371a))
* preserve all elements including functions for complete call graph ([c903296](https://github.com/FreePeak/LeanKG/commit/c903296f870cc14acf0d214a3ab7919902f0515b))
* prevent leankg update from killing itself ([a30bcba](https://github.com/FreePeak/LeanKG/commit/a30bcba3628d00a4d8fdeca1d19130ee6113bebd))
* prevent self-termination during leankg update ([ebc701d](https://github.com/FreePeak/LeanKG/commit/ebc701d547923cfa11fcd29ea8e24ab8569fb98d))
* prevent self-termination during leankg update ([cd56c7a](https://github.com/FreePeak/LeanKG/commit/cd56c7a008495cee83e5302d3fce7fa68516d198))
* prevent zombie processes with proper graceful shutdown ([2831fa3](https://github.com/FreePeak/LeanKG/commit/2831fa3068c4d1de232af78e3b7df2250edf1b45))
* prevent zombie processes with proper graceful shutdown ([66a344d](https://github.com/FreePeak/LeanKG/commit/66a344d6686b010a3cb41d5ff00facbaae3a9c31))
* properly return early on cache hit in get_dependencies and get_relationships_for_target ([4f05b6a](https://github.com/FreePeak/LeanKG/commit/4f05b6ad1728f127ac19714fefc8e845665d9402))
* reduce watcher CPU/RAM by 90%+ with debouncing, DB reuse, and file filtering ([#31](https://github.com/FreePeak/LeanKG/issues/31)) ([689c156](https://github.com/FreePeak/LeanKG/commit/689c156e8a1e7f8c6efbafa58e193afc28634ece))
* remove /tmp/ from ignore paths to allow test fixtures in temp dirs ([9f60f79](https://github.com/FreePeak/LeanKG/commit/9f60f797c5211a3c6938f5ef516bfc1bba1aa979))
* remove binary before extracting in install script ([99f3d51](https://github.com/FreePeak/LeanKG/commit/99f3d51e90e495e82a23e28da2add44a13b654d1))
* remove dead code and use constant-time token comparison ([b0a77de](https://github.com/FreePeak/LeanKG/commit/b0a77ded612bfa1e8f4c2185d5c1f4fa1e3ef2b0))
* remove false marketing claims, update with actual benchmark data ([1da08a7](https://github.com/FreePeak/LeanKG/commit/1da08a7802c3f1987c1e2e91333d07efc802a939))
* remove hardcoded target from cargo config and fix CI target per matrix job ([22293b1](https://github.com/FreePeak/LeanKG/commit/22293b120b0c0793a92ab1125276687e9951ed90))
* Removed edgeNodeIds filter that excluded orphan nodes. ([087fec8](https://github.com/FreePeak/LeanKG/commit/087fec8adb938ade3f79d1d962a44c4bcc8555b2))
* repair sigma.js graph filters and add position caching ([9924fb2](https://github.com/FreePeak/LeanKG/commit/9924fb252e4989e5fb57f2d2afd3cb54d6de4e98))
* replace =~ with regex_matches for workflow search ([7163c8f](https://github.com/FreePeak/LeanKG/commit/7163c8fa38320215edc11e117aefac1cd5eea970))
* replace all_elements() with targeted queries in orchestrate ([4e02b3d](https://github.com/FreePeak/LeanKG/commit/4e02b3d93261c534d287cd155abde08e6162ffaf))
* replace broken :collect count queries with working Cozo syntax ([a9bb4bb](https://github.com/FreePeak/LeanKG/commit/a9bb4bb1f112a0294c50d0e7d8900380c7dd2c6c))
* replace dtolnay/rust-toolchain with actions/setup-rust - stable branch SHA was garbage collected ([bf347f9](https://github.com/FreePeak/LeanKG/commit/bf347f9270191b5b49f62936a8c9e1a4be00c0b8))
* resolve 4 bugs found in test report ([fd97f81](https://github.com/FreePeak/LeanKG/commit/fd97f817bbe9157ad85fb52426205c006251ee05))
* resolve arity mismatch in get_documented_by queries and fix get_callers column name ([68f9d8c](https://github.com/FreePeak/LeanKG/commit/68f9d8c442284983f4ee501597d5f4a52e9a8392))
* resolve arity mismatch in MCP server tools ([#22](https://github.com/FreePeak/LeanKG/issues/22)) ([bc85153](https://github.com/FreePeak/LeanKG/commit/bc85153e092050a69f015134063627eac173809d))
* resolve call edge arity mismatch and index bug ([d95daf7](https://github.com/FreePeak/LeanKG/commit/d95daf75283b851107581259b6223da1c1044992))
* resolve call edges without regex operator ([#5](https://github.com/FreePeak/LeanKG/issues/5)) ([bdf8aa7](https://github.com/FreePeak/LeanKG/commit/bdf8aa78fd749f85cf02b4b9e1ab726ba6ca6af6))
* resolve conflict marker and import error in MCP HTTP transport ([4fa1635](https://github.com/FreePeak/LeanKG/commit/4fa1635a5c867c84ed0b75a318e77af18a2ee568))
* resolve Go imports to filesystem paths using go.mod module mapping ([3fed36a](https://github.com/FreePeak/LeanKG/commit/3fed36aed1d29b565f53a2ee4c0e52da067a7c6e))
* resolve_call_edges now deletes __unresolved__ edges before inserting resolved ones ([6a5a00d](https://github.com/FreePeak/LeanKG/commit/6a5a00db61f8b45d6d2a36c6e47f39be8dd2a323))
* resolve_call_edges query parser issue ([c77aff5](https://github.com/FreePeak/LeanKG/commit/c77aff5c7913659e8640b689937417d1244ccc3b))
* **retrieval:** project env column in graph relationships ([05737e5](https://github.com/FreePeak/LeanKG/commit/05737e52b0b008df07b13c50a77a5364ed73ee32))
* run_raw_query preprocessor - use correct Cozo syntax and column names ([e4204a0](https://github.com/FreePeak/LeanKG/commit/e4204a0f5864eab3c42bc20c3792316170b68c3f))
* search_by_name empty results and run_raw_query ignoring params ([1522efe](https://github.com/FreePeak/LeanKG/commit/1522efe9ecaa3cd2d2eb0e0b23217b37f0447516))
* separate crates.io publish into dedicated job, only publish from ubuntu ([f87f4b5](https://github.com/FreePeak/LeanKG/commit/f87f4b5e4e9be530760a9a17d527f066bd3e277c))
* **serve:** open LeanKG /workspace, not MCP multi-repo cwd ([efbb60a](https://github.com/FreePeak/LeanKG/commit/efbb60a94b6bb33d842c325e0cd41dc64fcf5b65))
* set WORKDIR to /app in Dockerfile for ui/dist lookup ([254c5c8](https://github.com/FreePeak/LeanKG/commit/254c5c83a5842388e1ae43ba245131c5709551ff))
* skip Vite dev server when ui/dist exists for production deploys ([71fe0f9](https://github.com/FreePeak/LeanKG/commit/71fe0f9a8919b8ab8eae6b9eb5326e42577405a7))
* source ui embed ([d241b3c](https://github.com/FreePeak/LeanKG/commit/d241b3cf9e9b602f8d051ecc6fc6c63600030f4c))
* stabilize HTTP MCP indexing ([123fe77](https://github.com/FreePeak/LeanKG/commit/123fe773367aae0f52c76056d1cfc52ace1530d3))
* stabilize HTTP MCP indexing ([90e30e8](https://github.com/FreePeak/LeanKG/commit/90e30e88276622b56d270f05c593145a6a7d25cb))
* stabilize v2 env queries and MCP tests ([a9480a4](https://github.com/FreePeak/LeanKG/commit/a9480a4af2af685b46a87874b674b7193208bc38))
* status counts, debug logs, and graph file nodes ([af45eb9](https://github.com/FreePeak/LeanKG/commit/af45eb950cbbc63efb3dde3e37213868c29907aa))
* support ontology layer schema repair ([403fecf](https://github.com/FreePeak/LeanKG/commit/403fecf1c912e2c143f13503f5c83c18edb8542f))
* **ui-v2:** re-switch project before container double-click expand ([ed5e3ce](https://github.com/FreePeak/LeanKG/commit/ed5e3cec5e3d2298840c5a86780467dd97151efb))
* **ui-v2:** replace invalid Sigma defaultDrawEdgeHover for Render build ([52324a3](https://github.com/FreePeak/LeanKG/commit/52324a37319f8fcf3175f7c666fabade7093cc29))
* **ui-v2:** replace-graph, file API, and correct /workspace serve graph ([b62ee29](https://github.com/FreePeak/LeanKG/commit/b62ee29867331da4d6fd80980e44875fc9f37772))
* **ui-v2:** Service/Folder replace-graph; gate /api/file ([99fce80](https://github.com/FreePeak/LeanKG/commit/99fce807e874e0dcad6809d347ff33ad4ba533b2))
* **ui-v2:** stale double-click handlers; rebake Render embed ([5f60f5b](https://github.com/FreePeak/LeanKG/commit/5f60f5be63464b3303c203725ea78da061ae278c))
* **ui-v2:** unblock Render build — replace invalid Sigma defaultDrawEdgeHover ([e974579](https://github.com/FreePeak/LeanKG/commit/e97457947928402cbab9d7520a4d6d8d782aaab6))
* update Cargo.lock dependencies ([d6a579d](https://github.com/FreePeak/LeanKG/commit/d6a579da7390af9644ccb5f61f5e0cdc72f09de1))
* update Dockerfile to build new Vite+React UI ([#42](https://github.com/FreePeak/LeanKG/issues/42)) ([c667d3f](https://github.com/FreePeak/LeanKG/commit/c667d3fbbd24d6b40a87fa9ea43f9221ac3542cf))
* Update install script to fix MCP config for Claude, Cursor, Kilo ([3419659](https://github.com/FreePeak/LeanKG/commit/3419659b6cd96298e318b9148c90715b5d5fdbb8))
* update leankg command to install hooks and remove old skill ([#21](https://github.com/FreePeak/LeanKG/issues/21)) ([0d326ff](https://github.com/FreePeak/LeanKG/commit/0d326fff0324a3d92942d34f63e2f61ea30abde7))
* update PreToolUse hooks to use "*" matcher for universal coverage ([48e8794](https://github.com/FreePeak/LeanKG/commit/48e8794443a0047c700fa90edabbc32f2fce17e4))
* update tests to match actual schema behavior ([24a5608](https://github.com/FreePeak/LeanKG/commit/24a56082181d5392f707fe91d39aeec648d14a1e))
* **US-21:** get_dependencies calls GraphEngine.get_dependencies ([31004e1](https://github.com/FreePeak/LeanKG/commit/31004e1a896fbbff7824a0ff2ab218d02e645072))
* **US-24:** get_doc_for_file extracts target_qualified for documented_by ([9eacf9d](https://github.com/FreePeak/LeanKG/commit/9eacf9db94a080aa1471be574ef770812e5a8244))
* **US-27:** search_code default limit 20, hard cap 50 ([87cf690](https://github.com/FreePeak/LeanKG/commit/87cf690db08aeab78669291fd473eaeef64d5952))
* use absolute path for leankg binary in MCP config ([f234cd2](https://github.com/FreePeak/LeanKG/commit/f234cd2a9937a02e626dc32c36c8622be3bf0127))
* use bash shell for package step on Windows ([da930e5](https://github.com/FreePeak/LeanKG/commit/da930e5424d7de78121477a2e7366c2f47a29fc3))
* use bash shell for rm command in release pipeline ([bdf2ef1](https://github.com/FreePeak/LeanKG/commit/bdf2ef1b73e65be58e4e107294859e74e9194d8c))
* use batch inserts in doc indexing to avoid SQLite lock contention ([c63299a](https://github.com/FreePeak/LeanKG/commit/c63299a4110323afa2a22fbf454ce7856f229246))
* use correct Claude Code mcp_settings.json path ([9c0336b](https://github.com/FreePeak/LeanKG/commit/9c0336b6265b3e128de3b3ee5d58b604d01f69d9))
* use COUNT queries in mcp_status instead of loading all data ([c0eab96](https://github.com/FreePeak/LeanKG/commit/c0eab96d7c2a68b14731283074016ab275760dc4))
* use delete query instead of rm for unresolved relationships ([ec2aad5](https://github.com/FreePeak/LeanKG/commit/ec2aad5ca83fa010b89c73902a67fb1787e48e91))
* use dtolnay/rust-toolchain@master instead of [@stable](https://github.com/stable) to resolve stale action SHA ([eb13f44](https://github.com/FreePeak/LeanKG/commit/eb13f4450679a3befa652db1c873344ec0268d48))
* use explicit ConstantTimeEq::ct_eq for token comparison ([03832f8](https://github.com/FreePeak/LeanKG/commit/03832f8effc253d0cf5a69daafb4484c1a96de26))
* use project_param instead of undefined query variable ([b1e9aef](https://github.com/FreePeak/LeanKG/commit/b1e9aef04828ea9f5bc4c2ba1aabf1c8d11ad980))
* use proper CozoDB count aggregation for mcp_status ([151b089](https://github.com/FreePeak/LeanKG/commit/151b0895c23a5a15b831d5b9552737ff2048ac4d))
* use proper CozoDB count aggregation instead of capped limit+rows.len() ([693b1ec](https://github.com/FreePeak/LeanKG/commit/693b1ecb575c6a1219052f0ada894b813f87b55b))
* use rust:1-bookworm to match glibc version ([9389709](https://github.com/FreePeak/LeanKG/commit/9389709467350393e187587779b1541d537c716b))
* use rustup installer directly instead of broken third-party GitHub actions ([ce53f22](https://github.com/FreePeak/LeanKG/commit/ce53f22b9ab15695ee867f61dcd2bb6a85b86051))
* validate required parameters before dispatching to handlers ([8dbc996](https://github.com/FreePeak/LeanKG/commit/8dbc996ac2df344330136dac2cfa46de5401e6fa))
* watcher debounce, burst pacing, db size enforcement ([55eab7a](https://github.com/FreePeak/LeanKG/commit/55eab7a53969517b19a9c8f048b791c83b5b89ce))
* **web:** fix document/function filter showing empty graph ([00c4b12](https://github.com/FreePeak/LeanKG/commit/00c4b12f57f2b54df65fe5152166c1cb019db4ae))
* **web:** graph visualization - edges and labels ([9e6c678](https://github.com/FreePeak/LeanKG/commit/9e6c67850d4d1d5d4c4bba06980c5e01a9d12c6c))
* **web:** handle D3-mutated edge objects in filter functions ([c68142b](https://github.com/FreePeak/LeanKG/commit/c68142b7e7e33548964d30fc9e56afb3bd695db5))
* **web:** improve graph performance and usability ([51998bb](https://github.com/FreePeak/LeanKG/commit/51998bb310cb7140bb785b3af3927b4fe44c4f0b))
* **web:** include all nodes in graph, not just nodes with edges ([087fec8](https://github.com/FreePeak/LeanKG/commit/087fec8adb938ade3f79d1d962a44c4bcc8555b2))
* **web:** resolve /api/file across LEANKG_PROJECT_DIRS ([3e5d271](https://github.com/FreePeak/LeanKG/commit/3e5d271b6cbe9f863e1221a563cc15999dd7520c))
* **web:** restructure filter flow - type filter before limiting ([6760c0b](https://github.com/FreePeak/LeanKG/commit/6760c0b7cc03befd72cec8ce19db52a97fc3027e))


### Performance

* Architecturally optimize GraphEngine, caching mechanisms, and indexer concurrency ([#29](https://github.com/FreePeak/LeanKG/issues/29)) ([0a4ed91](https://github.com/FreePeak/LeanKG/commit/0a4ed917bda158d84d70622c87a6fa0187f523a6))
* batch delete in resolve_call_edges (O(1) DB queries vs O(n)) ([#2](https://github.com/FreePeak/LeanKG/issues/2)) ([da88ab5](https://github.com/FreePeak/LeanKG/commit/da88ab5c02e07cb5d1a3efc6334800954b236925))
* CPU optimization Phase 1 - reduce idle CPU from 61% to &lt;5% ([#25](https://github.com/FreePeak/LeanKG/issues/25)) ([bc12302](https://github.com/FreePeak/LeanKG/commit/bc123021ed3fdd2ee6b2f00eac8264601a957b5f))
* optimize indexing for large codebases ([#7](https://github.com/FreePeak/LeanKG/issues/7)) ([eb37690](https://github.com/FreePeak/LeanKG/commit/eb376908222925105779e5f12b07a5baf24fb579))


### Refactoring

* replace alwaysApply with trigger-based LeanKG rule ([cc922ba](https://github.com/FreePeak/LeanKG/commit/cc922baafdce813a4b81779f33ae34a657314311))


### Reverts

* revert README UI documentation changes ([8fcda4a](https://github.com/FreePeak/LeanKG/commit/8fcda4afae5bab7f591e46eb10eb184ad721c3e6))

## [Unreleased]

### Changed
- MCP: hard-delete `wake_up` and `search_by_environment`; prefer
  `get_overview_context` and `env=` on search/`kg_*` (~81 tools with
  embeddings). Agent docs, install hooks, and plugin manifests synced
  (Wave 1a / REL-062).

### Added
- Procedural ontology auto-update while serving: debounce-watch
  `ontology/workflows.yaml` + `concepts.yaml`, post-index refresh,
  Docker boot marker vs **both** YAML mtimes, MCP
  `ontology_control(action=sync|status)` (FR-ONT-PROC / REL-059).

### Fixed
- Ontology sync replaces the `ontology://` layer (clear then insert)
  so YAML renames/removals no longer leave duplicate workflow steps
  under Cozo composite-key `:put` (REL-059).

## [0.19.2] - 2026-07-20

### Fixed
- MCP: mega-graph search availability on boot — ontology sync is
  timed (45s default) or skippable via
  `LEANKG_ONTOLOGY_SYNC_ON_BOOT=skip`, so `mcp-http` no longer hangs
  and `search_code` / `find_function` look completely broken (#85,
  REL-052).
- MCP: in-process `LEANKG_EMBED_BACKGROUND=1` is **skipped** on
  mega-graphs (override with `LEANKG_EMBED_BACKGROUND_MEGA=1`).
  Prefer offline `embed --wait` for >150k workspaces (#85, REL-052).
- MCP: Docker PID-1 stale `embed.lock` from a killed prior run no
  longer looks "alive" forever. Same-PID locks are treated as stale
  unless an in-process embed is already active (#81).
- HNSW `semantic_search`: keyed seed hydration without `all_elements`
  on mega-graphs, avoiding OOM on 150k+ workspaces (#87, FR-SEM-07,
  REL-054).
- HNSW `kg_semantic_context`: cheap `has_any` gate keeps the path off
  the `list_all` (~147k `embedding_state` rows) on mega-graphs (#87,
  FR-SEM-07, REL-054).
- `concept_search`, `query_graph`, `get_clusters`: mega-safe paths
  key `code_refs`, use frontier-local BFS, and serve a precomputed
  `cluster_id` instead of running live Louvain on huge graphs (#88,
  REL-055).
- `query_graph`: avoid unindexed name/edge full scans on mega-graphs
  (US-MG-TOOL-01 / FR-ONT-MEGA-01 / FR-GF-MEGA-01 / FR-CL-MEGA-01).
- `semantic_search` mega path: tighten response shape and avoid the
  `env=production` false-positive on locally-indexed code.
- Clippy: drop `map_identity` in threads pool test (#82).

### Added
- MCP `embed_control(action="on|off|status")` for day-2 partial
  resume when boot embed is off; idle-gated, RSS-fraction bounded,
  cooperative cancel, Docker PID-1 safe (#86, FR-EMBED-TOGGLE-01).
- MCP `query_graph` and CLI `graph-query` / `query --kind subgraph`:
  natural-language scoped subgraph with seed retrieval → BFS / shortest
  path → token-budget trim and `confidence_label` (EXTRACTED /
  INFERRED / AMBIGUOUS) on every edge (#84, US-GF-03, FR-GF-05/06,
  REL-042).
- Hybrid typed CALLS resolution for Go/TS without an LSP server
  (`indexer.typed_resolve=go,ts` or `all`) — in-process
  `TypeRegistry` + resolver upgrade `resolution_method=typed` during
  indexing (#83, FR-LSP-A..D, REL-039).
- `leankg init --with-lsp` writes a prefab `lsp:` block from the
  server catalog; empty `leankg.yaml` falls back to the prefab (#83).
- MCP prefer-order schema hints on `concept_search` / `semantic_search`
  / `search_code` / `kg_semantic_context` / `kg_context` to drive
  agent tool selection (#82, FR-SURF-02, US-SURF-01).
- Soft-deprecate `wake_up` and `search_by_environment`; prefer
  `get_overview_context` and `env=` on search / `kg_*` (#83,
  FR-SURF-04/05, REL-053).
- Day-2 embed resume: HNSW drop/rebuild and model load are skipped
  when nothing is dirty; per-batch freshness stamp survives kill;
  `content_hash` change is the only signal that marks vectors stale
  (no full-index forced re-embed) (#81, FR-HNSW-E).
- Mega-graph compose defaults: `cpus: "6"`, `mem_reservation: 3g`,
  MCP `mem_limit: 6g`; FilterPolicy drops embed/assets and gate
  benchmark paths; `LEANKG_SKIP_FRESHNESS_CHECK=1` honored (#81).
- UI v2 (Phase 1) in `ui-v2/`: GitNexus-style explorer with
  Force/Tree/Circles layouts, mega-graph skip, LeanKG REST client,
  Vitest unit tests, Playwright e2e, screenshot report (#89).
- UI v2 baked into `src/embed/` via `rust-embed`; `leankg serve`,
  Docker, and onrender ship the new shell on `:8080` (#90).
- Docker `entrypoint.sh` now starts `leankg serve` on `:8080` and
  execs MCP as PID 1; compose publishes `8080:8080` + `9699:9699`
  (Option A for UI v2 + MCP) (#89).
- `scripts/mcp-smoke-tools.py` honest-skip smoke harness for the
  full MCP tool surface (#84).
- Redundant-tools matrix classifies every MCP tool and documents
  skills/rules removal impact (#86).

### Removed
- `mcp_hello`, `mcp_impact`, `get_doc_for_file` — superseded by
  `get_impact_radius`, `find_related_docs`, and `mcp_status` /
  `kg_self_test` (#82, FR-SURF-03, US-SURF-02).
- `find_clones` tool and the `leankg clones` CLI command — same-file
  Jaccard clone detection was unused by agents and refused on
  mega-graphs; prefer `semantic_search` / `concept_search`.

### Changed
- AGENTS.md mega-graph guidance and prefer-order instructions synced
  with FR-SURF-02 search/semantic triples (#82, #85, #86).

## [0.19.1] - 2026-07-17

### Fixed
- API auth: `auth_middleware` and `team_token_middleware` no longer
  panic when `ApiKeyStore` initialization fails (disk or permission
  error). They now return `500 Internal Server Error`, matching the
  existing `validate_key` error arm. Closes #70 (#78).
- Vector engine: avoid `i8` overflow in synthetic SQ8 patterning
  (centered value computed in `i32` before casting) so CI debug builds
  no longer panic on `% 254 as i8 - 127`.
- Vector engine: idle GC trims the heap only once per quiet period
  (honors `LEANKG_GC_POLL_SECS`) instead of re-trimming empty caches
  every 30s.
- Vector engine: idle RSS gate asserts the warm **delta** under
  `cargo test --lib` (debug builds blow past absolute 150MB), keeping
  the absolute check for lean bench processes.

### Added
- Vector engine P0 quality gate closed with A/B evidence (#80):
  - `Sq8Nsw` layer-0 search over in-RAM SQ8 — measured 1M ANN
    P95≈0.065ms (Neon), gated `cargo bench --default` at 1M
    (FR-VE-BENCH-Q).
  - ≥80% modeled I/O cut vs `mmap`, SQ8 recall≥90% @ `efSearch=50`,
    1M corpus under 2GB (live RSS≈567MB) — FR-VE-BENCH-IO/RECALL/OOM.
  - Idle warm SQ8 NSW RSS≈89MB (<150MB) and ANN+JSON time-to-context
    P95≈0.094ms (<100ms) — US-VE-01/02.
  - `cargo bench --bench vector_engine_ab` now writes
    `target/vector_engine_ab_result.json` for gate/live injection
    (FR-VE-BENCH-AB).
  - `evaluate_gate` flips `ready_for_default=true` and
    `preferred_ann_backend=local_engine` when
    `LEANKG_VE_GATE_FULL=1` and all Q/IO/RECALL/OOM/AB floors pass.
- `tests/vector_engine_e2e.rs` — P0 gate paths covered end-to-end.
- README polished to product landing style (CodeGraph-style
  get-started, agent badges, why/how, measured A/B results).
- Semantic MCP verification captured as PRD v3.7.1 backlog (US-SEM /
  FR-SEM enhancements for a later sprint).

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.19.1` (also
  tagged `latest`).

## [0.19.0] - 2026-07-17

### Added
- Local-first vector graph engine (v3.7 P0): new `src/vector_engine/`
  module with tiered storage (`tier1` hot cache, `tier2` warm HNSW,
  `tier3` cold RocksDB), SIMD-accelerated distance kernels, dual-write
  reconciliation, background GC, and `gate`-based fallback routing
  (FR-VE-RT-MEM / FR-VE-BENCH-OOM, PRD §5.14).
- `vector_engine_ab` benchmark harness for A/B testing the new engine
  against the legacy in-memory path under realistic query mixes.
- `engine.recovery` path that rehydrates tier1/tier2 from RocksDB on
  restart without blocking MCP startup.

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.19.0` (also
  tagged `latest`).

## [0.18.2] - 2026-07-16

### Fixed
- Docker MCP no longer enables background embed by default (it dropped
  HNSW and broke `semantic_search` on mega-graphs).
- INT8 fast path warms the Xenova cache before ensuring quantized ONNX;
  MCP-safe worker/batch caps when callers request ≤2 workers / ≤32 batch.
- Offline embed profile: INT8, workers 8 / batch 128, soft RSS pause off,
  shared `leankg_models` volume, and multi-project mounts for
  `leankg-embed`.

### Added
- `scripts/embed-all-workspaces-then-mcp.sh` — offline embed all
  `LEANKG_PROJECT_DIRS`, then start MCP and verify `hnsw+rerank`.
- `scripts/docker-up.sh` and `install.sh … docker` — one-command Docker
  setup (index + embed + MCP) with no Rust install.
- Entrypoint passthrough for one-shot `embed` / `index` after auto-index.

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.18.2` (also
  tagged `latest`).

## [0.18.1] - 2026-07-16

### Fixed
- Embedding fast path: correct HNSW route, MCP-decoupled lookup, and INT8
  quantisation option (`#76`).
- LeanKG graph workflow end-to-end (`#75`).

### Changed
- Rebuilt and republished Docker image `freepeak/leankg:0.18.1` (also
  tagged `latest`).

## [0.17.2] - 2026-06-06

### Fixed
- Indexer no longer reads files larger than 2 MiB (configurable via
  `LEANKG_MAX_FILE_SIZE`); stops the indexer from slurping checked-in
  binaries and huge generated XML/JSON into memory.
- Watcher debounce raised from 500 ms to 2 s and the event channel
  expanded to 4096; large bursts (e.g. `git pull`) now process in chunks
  with a 250 ms pause between batches instead of fork-bombing the DB.
- Watcher now skips minified JS/CSS, editor swap files, `.bak`, `.tmp`,
  `.pid`, `.lock` and a much longer list of build / generated dirs.
- Watcher now actually runs `VACUUM` on the SQLite `leankg.db` when the
  file exceeds the size cap, instead of only logging a warning. This
  bounds a previously unbounded growth problem (a single workspace had
  grown to 14 GB).
- Default `LEANKG_MMAP_SIZE` lowered from 256 MiB to 64 MiB. The
  previous default pushed containers past their memory limit and was
  the proximate cause of OOM kills (container exit 137).
- Default `mcp.auto_index_on_db_write` flipped to `false`; the previous
  default created reindex storms on every external DB write.

### Added
- `GraphEngine::vacuum()` to reclaim SQLite file space after large
  deletes.
- Docker compose now sets `mem_limit: 6g`, `mem_reservation: 4g`,
  `cpus: "4"`, `pids_limit: 4096`, and `restart: unless-stopped` so the
  container can no longer consume the entire host memory.
- New env tunables for the watcher: `LEANKG_WATCHER_DEBOUNCE_MS`,
  `LEANKG_WATCHER_BURST_LIMIT`, `LEANKG_WATCHER_BURST_PAUSE_MS`,
  `LEANKG_WATCHER_MAX_DB_SIZE`.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.15.1] - 2026-04-14

### Fixed
- Normalize glob patterns in exclude matching
- Use .gitignore files only for file traversal
- Apply config.project.root when indexing with '.'

### Changed
- Read config from .leankg/leankg.yaml in index_codebase()
- Default project.root changed from './src' to '.'

### Removed
- Dead should_ignore_path function

## [0.14.9] - 2026-04-14

### Fixed
- Correct byte string literal syntax in `test_detect_gradle_submodules` test (b#"..." → br#"...")

## [0.14.8] - 2026-04-14

### Fixed
- Inline call resolution during indexing (resolves `__unresolved__` calls in-memory, eliminates separate DB pass)
- Batch delete for resolved call edges (O(1) queries vs O(n) sequential deletes)
- ~6x speedup: 10s → 1.7s for indexing with 7926 call edges

## [0.14.7] - 2026-04-12

### Added
- Obsidian vault integration for annotation IDE
- Obsidian module with note generator and sync logic
- Watcher for live file monitoring
- CLI with obsidian subcommand
- New documentation: architecture.md, benchmark.md, metrics.md
- Dockerfile improvements for LeanKG indexing during build

### Changed
- Updated README with new UI architecture documentation
- Vite dev server integration for production deployments

### Fixed
- Dockerfile to build new Vite+React UI
- UI directory build copy issue
- WORKDIR setting in Dockerfile
- Preserved all elements for complete call graph
