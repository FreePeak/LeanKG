# LeanKG — Technical Highlights for Interview

> A concise overview of the most impressive technical features in LeanKG.
> Use these as talking points when introducing the project.

---

## 1. ONNX DirectEmbedder — 5× Faster Than Industry Default

**File:** `src/embeddings/models.rs:230-344`

The industry-standard `fastembed` crate hardcodes `intra_threads = available_parallelism()` per session. On a 10-core host, N workers = 10N threads oversubscribed, capping throughput at ~120 vec/sec.

LeanKG's `DirectEmbedder` loads the same tokenizer + ONNX model (BGE-small-en-v1.5, 384-dim) from fastembed's model cache but constructs `ort::Session` with controlled `with_intra_threads(1)`. Each worker owns one session → no thread oversubscription.

**Result:** ~600 vec/sec (4 workers, batch=128) vs 120 vec/sec — a **5× speedup**.

Supports BGE-FP32, BGE-INT8, BGE-FP16, and MiniLM models via `LEANKG_EMBED_MODEL`. Defenses include truncation enforcement (`effective_max_seq()`) and `with_memory_pattern(false)` to prevent arena retention across variable batch sizes.

```rust
// Key insight: one session per worker, intra_threads=1
let session = ort::SessionBuilder::new(&env)?
    .with_intra_threads(1)?      // Not available_parallelism()
    .with_memory_pattern(false)?  // Prevent arena retention
    .with_model_from_file(model_path)?;
```

---

## 2. Parallel Embedding Pipeline with Bounded MPMC Channel

**File:** `src/embeddings/build.rs:789-1183`

N inference workers (rayon) each own a `DirectEmbedder` and compute vectors in parallel. Results push onto a bounded `crossbeam_channel`. A single CozoDB writer thread drains the channel, accumulates up to `UPSERT_CHUNK` rows, and calls `import_relations()` — bypassing the CozoDB script parser entirely for 8× faster bulk insert.

Without this architecture, N workers contending for the CozoDB write Mutex serialized all `:put` calls and collapsed throughput to ~40 vec/sec.

**Result:** full embed from **~120 min → ~6 min** on a 400k-element workspace.

```
┌──────────┐  ┌──────────┐  ┌──────────┐
│ Worker 1 │  │ Worker 2 │  │ Worker N │   rayon threads
│ ONNX inf │  │ ONNX inf │  │ ONNX inf │   intra_threads=1 each
└────┬─────┘  └────┬─────┘  └────┬─────┘
     │              │              │
     └──────────────┼──────────────┘
                    │
            crossbeam bounded channel
                    │
                    ▼
           ┌────────────────┐
           │ Writer Thread  │  single CozoDB writer
           │ accumulate 5000 │  import_relations() (skip parser)
           │ → upsert_fresh │  per-batch fresh stamping
           └────────────────┘
```

RSS headroom gate: `wait_for_embed_rss_headroom()` pauses workers when memory pressure exceeds 90% of `LEANKG_EMBED_MAX_MB`. Mac-friendly: defaults to 2–4 GB depending on architecture.

---

## 3. Content-Hash Staleness — Never Re-Embed Unchanged Code

**File:** `src/embeddings/state.rs`, `src/embeddings/text_blob.rs`

Every code element has a SHA-256 of its text blob stored in the `embedding_state` CozoDB table:

```
embedding_state {
  qualified_name: String =>
  content_hash: String,   // SHA-256 of text blob
  state: String,          // "fresh" | "stale"
  embedded_at: String     // epoch seconds
}
```

When reindexing, `mark_stale_if_changed()` compares the current blob hash against the stored hash. If `state == "fresh"` and the hash matches → **skipped**. A no-op `leankg index` (zero code changes) forces zero re-embedding.

Combined with **incremental HNSW puts** for small dirty sets (<5% of total vectors) — the HNSW index stays live, no drop/rebuild needed:

```rust
pub fn should_use_incremental_hnsw_puts(dirty_count: usize, total_vectors: usize) -> bool {
    let threshold = (total_vectors / 20).max(1_000);
    dirty_count <= threshold
}
```

Per-batch fresh stamping (`upsert_fresh()`) makes the embed step crash-safe: killed mid-run → resume skips already-embedded batches.

---

## 4. Measured Agent Performance — Token/Time/Cost Reduction

**Source:** `docs/benchmark.md`, `benchmarks/alamofire-30q/`, README

### Unified A/B Gate (100 tasks, vector engine on vs off)

| Metric | Reduction | Floor |
|--------|-----------|-------|
| Input tokens | **-65.0%** | ≥60% |
| Tool calls | **-84.6%** | ≥80% |
| Wall-clock speedup | **2.50×** | ≥2× |

### Cross-Repo Benchmark (35 questions, 3 arms, 105+ agent runs)

LeanKG vs CodeGraph vs no-graph across 2 iOS repos:

| Metric | Reduction |
|--------|-----------|
| File reads | **-40% to -80%** |
| Wall-clock time | **-14% to -21%** |

### Vector Engine Performance

- 1M vector HNSW ANN P95: **~0.055 ms**
- Insert: **57k elements/sec**, **67k relationships/sec**
- Retrieve: **419k rows/sec**
- L1 cache speedup (cold → warm): **345–461×**

### Cross-Tool Agent Benchmark (Gin pilot, 4 runs/arm)

| Metric | Reduction |
|--------|-----------|
| Tool calls | -22% |
| Wall-clock | -44% |
| Cost | -31% |

---

## 5. Incremental Indexing with Dependency Propagation

**File:** `src/indexer/mod.rs:1239-1495`, `src/indexer/git_workspace.rs`

`git diff --name-status HEAD` drives incremental reindex. After detecting changed files, the system loads all relationships and finds **dependents** — files that import or reference the changed files. Those dependents get reindexed too, ensuring the graph stays consistent.

**Mega-graph safety:** `skip_incremental_dependents()` detects large graphs (>50k elements) and skips full-graph dependent expansion, preventing OOM in Docker containers with 6 GiB limits.

Smart categorization:
- **Deleted files** → immediate removal from DB
- **Modified + dependent files** → re-extract, then bulk remove old rows (one CozoDB scan vs N per-file scans)
- **New files** → insert only, skip the scan for old rows

Bulk `mark_files_stale()` at the end: one parameterized CozoDB query per file batch → all embedding_state rows flagged for re-embed.

---

## 6. 81+ MCP Tools with Prefer-Order Discovery Protocol

**File:** `src/mcp/tools.rs` (1426 lines), `docs/mcp-tools.md`

Not just a dump of tools — a **curated discovery protocol** that prevents agents from drowning in the graph:

```
Discovery prefer-order:
  get_overview_context → concept_search → semantic_search
  → search_code → find_function → connection verbs
```

Explicitly **blocks** opening with `query_graph` (too expensive for first contact). Every relationship edge carries:
- `resolution_method`: `name` | `name_file_hint` | `unresolved` | `typed`
- `confidence_label`: `EXTRACTED` | `INFERRED` | `AMBIGUOUS`

**Tool categories:**
| Category | Count | Examples |
|----------|-------|---------|
| Init/Index/Status | 5 | `mcp_init`, `mcp_index`, `mcp_status` |
| Search & Discovery | 8 | `semantic_search`, `search_code`, `find_function` |
| Dependency & Impact | 6 | `get_impact_radius`, `get_dependencies`, `shortest_path` |
| Context & Review | 5 | `get_context`, `ctx_read`, `get_review_context` |
| Documentation | 6 | `find_related_docs`, `generate_doc`, `index_prd` |
| Traceability | 5 | `get_traceability`, `get_feature_flow`, `get_traceability_matrix` |
| Ontology & Knowledge | 10 | `concept_search`, `kg_context`, `add_knowledge` |
| Clusters & Architecture | 8 | `get_clusters`, `get_architecture`, `get_cluster_context` |
| Services & Topology | 6 | `get_service_graph`, `get_service_context`, `find_tunnels` |
| Quality & Safety | 6 | `detect_changes`, `find_dead_code`, `check_consistency` |
| Agent & Orchestration | 5 | `orchestrate`, `agent_focus`, `agent_diary_write` |

Machine-checked redundancy matrix: `tests/redundant_tools_matrix.rs` classifies every tool.

---

## 7. CozoDB Native HNSW with Cross-Encoder Rerank

**File:** `src/retrieval/pipeline.rs`, `src/embeddings/state.rs:69-155`

Embeddings stored directly in CozoDB with a native HNSW index:

```cozo
:create embedding_vectors {qualified_name: String => vector: <F32; 384>}
::hnsw create embedding_vectors:vec_idx {
    dim: 384,
    dtype: F32,
    distance: Cosine,
    ef_construction: 20,
    m: 50,
}
```

**Semantic search pipeline:**
1. Embed query text via BGE-small-en-v1.5 (384-dim)
2. HNSW ANN retrieval via `~embedding_vectors:vec_idx`
3. Cross-encoder rerank via `bge-reranker-v2-m3` (~600 MB ONNX)

Tunable for mega-graphs: `LEANKG_HNSW_M` (4-256), `LEANKG_HNSW_EF_CONST` (1-2000), `LEANKG_HNSW_EF` (query-side). Incremental HNSW `:put` for small dirty sets avoids full index rebuild.

Optional Redis side-store: `LEANKG_EMBED_VECTOR_STORE=redis` for external vector storage.

---

## 8. Nested Multi-Repo Workspace Discovery (Polyrepo)

**File:** `src/indexer/git_workspace.rs`, `src/web/file_resolve.rs`

`discover_nested_git_repos()` walks up to depth 4 to find `.git` directories/markers. Handles polyrepo Docker mounts like `/workspace`, `/workspace-other` via `LEANKG_PROJECT_DIRS`.

```bash
# Docker: index multiple repos side-by-side
LEANKG_PROJECT_DIRS=/workspace,/workspace-other
```

Cross-mount file resolution: when MCP tools need to resolve a file across repos, the resolver tries each root in `LEANKG_PROJECT_DIRS` sequentially. Works in production for BE monorepos with nested service directories.

---

## 9. Procedural Ontology with Hot-Reload YAML

**File:** `src/ontology/sync.rs`, README

While MCP is serving, a file watcher monitors `ontology/workflows.yaml` and `ontology/concepts.yaml` with debounce (≥1s). On change, it fully replaces the `ontology://` layer in the served DB — clear → batch insert. Renames/removals leave no duplicates.

**Three auto-refresh triggers:**
1. YAML watch during serve
2. Docker boot when `.leankg/ontology_synced` is older than YAML files
3. After successful index

Agent loop: wrong steps → edit YAML → watcher syncs → next `kg_trace_workflow` returns corrected steps — **no restart needed**. Dynamic concepts added via `add_ontology_concept` persist across YAML syncs (tagged `source:dynamic`).

---

## 10. Production Docker — Two-Container Cozoserver + LeanKG Sidecar

**File:** `docs/enterprise-docker.md`, `Dockerfile.cozoserver`, `docker-compose.enterprise.yml`

Split monolithic image into two containers:

| Container | Role | RAM | Port |
|-----------|------|-----|------|
| `cozoserver` | RocksDB graph store | 4 GB | `127.0.0.1:3000` |
| `leankg` | MCP + REST APIs + Web UI | 4 GB | `:9699` (MCP), `:8080` (REST) |

Shared network namespace (`network_mode: service:leankg`) — no auth token needed, no host port exposed for the DB. Independent scaling: bump RocksDB RAM without paying for it on API replicas. Backup targets RocksDB alone (no MCP downtime). Future path: TiKV-backed cozoserver swap without changing LeanKG callers.

---

## 11. Multilingual Code Extraction — 14+ Languages

**File:** `src/indexer/extractor.rs`

Tree-sitter + regex extractors for:

| Languages | Parser |
|-----------|--------|
| Go, TypeScript, JavaScript, Python, Rust, Java, Kotlin, Dart, Ruby, PHP, Perl, R, Elixir | tree-sitter |
| Swift, Objective-C | regex (no tree-sitter bindings available) |
| Terraform (.tf) | dedicated Terraform HCL extractor |
| Android XML | manifest, resources, navigation, layout |
| CICD YAML | GitHub Actions, GitLab CI |

Android-specific depth: 4 navigation extractors (Jetpack XML/Kotlin DSL, FragmentManager, Leanback), Room ORM, Hilt DI, WorkManager, CoroutineDispatcher, ViewModel/Repository patterns, Kotlin annotations, resource reference linking.

---

## 12. Token-Optimized Output (TOON Compression)

**File:** `src/mcp/toon.rs`

TOON (Token-Optimized Observable Notation) response envelope: ~40% smaller MCP payloads. Compression modes in `ctx_read`:

| Mode | What it returns |
|------|----------------|
| `adaptive` | Auto-select based on file size |
| `full` | Complete file content |
| `signatures` | Function/struct signatures only |
| `map` | File structure overview |
| `diff` | Changed lines from last index |
| `aggressive` | Max compression |
| `entropy` | High-information lines only |
| `lines` | Specific line ranges |

Token budget controls: `get_architecture` and `get_graph_schema` accept `max_items` with per-section truncation and `truncated_sections` metadata.

---

## 13. MCP Agent Ecosystem — 7+ Targets, One Install

**File:** `scripts/install.sh`, README

```bash
curl .../install.sh | bash -s -- <target>
```

Supported targets: **Cursor, Claude Code, OpenCode, Gemini, Kilo, Antigravity, Codex**. Wires the binary + MCP config + skills + hooks in one command.

Claude Code gets full lifecycle hooks (`PreToolUse` nudge to graph-first). Always-on agent rule: "always query LeanKG first before grep/glob." Per-project session hook auto-runs `get_overview_context` on start.

Docker: `freepeak/leankg:latest` on Docker Hub with multi-repo mounts.

---

## 14. PRD-to-Graph Pipeline — Full Feature Flow Traceability

**File:** `docs/prd.md` (2913 lines), `src/prd_indexer/`

`index_prd` MCP tool parses `docs/prd.md` into structured KG entries:
- All `FR-*` (feature requirements) and `US-*` (user stories) become `knowledge_entries`
- Auto-links feature requirements to ontology workflows by matching code paths

**Forward traceability:**
```
FR-ONT-PROC-01
  → linked ontology workflows
    → ordered steps with code_refs + failure_modes
      → annotated code elements
```

Tools: `get_feature_flow` (FR → steps → code), `get_traceability_matrix` (PO-facing coverage matrix: FR-* → workflow count, annotated elements, doc links).

---

## 15. Leiden Community Detection with Cluster Context

**File:** `src/graph/clustering.rs`, `src/graph/query.rs`

Leiden algorithm partitions the code graph into functional communities. Tools:

| Tool | Purpose |
|------|---------|
| `get_clusters` | List all clusters with labels, member counts, representative files |
| `get_cluster_context` | All symbols in a cluster with entry points and inter-cluster dependencies |
| `find_tunnels` | Cross-domain relationships between different Leiden clusters, sorted by confidence |
| `get_cluster_skill` | Per-cluster SKILL.md generation for AI agents |
| `get_architecture` | Clusters + languages + entry points + routes + hotspots in one call |

---

## Quick Talking Points

1. **"We built our own ONNX embedding pipeline because fastembed was 5× too slow"**
2. **"Content-hash staleness means reindexing unchanged code costs zero re-embedding"**
3. **"Measured 65% token reduction and 2.5× speedup for AI agents doing codebase Q&A"**
4. **"81+ MCP tools with a curated discovery protocol — agents don't drown in the graph"**
5. **"Production split architecture: cozoserver + LeanKG sidecar, independently scalable"**
6. **"Polyrepo workspace: indexes multiple git repos in one graph, resolves cross-repo dependencies"**
7. **"Hot-reload ontology: edit YAML, watcher syncs, next query returns corrected results — zero restart"**
8. **"14+ languages, deep Android support (4 nav extractors, Room, Hilt, WorkManager)"**
