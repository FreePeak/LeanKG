<p align="center">
  <img src="https://www.leankg.com/icon.svg" alt="LeanKG" width="80" height="80">
</p>

# LeanKG

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://opensource.org/licenses/MIT)
[![Rust](https://img.shields.io/badge/rust-1.75%2B-orange?logo=rust&logoColor=white)](https://www.rust-lang.org/)
[![crates.io](https://img.shields.io/badge/crates.io-latest-orange)](https://crates.io/crates/leankg)
[![SafeSkill 77/100](https://img.shields.io/badge/SafeSkill-77%2F100_Passes%20with%20Notes-yellow)](https://safeskill.dev/scan/freepeak-leankg)

**Lightweight Knowledge Graph for AI-Assisted Development**

LeanKG is a local-first knowledge graph that gives AI coding tools accurate codebase context. It indexes your code, builds dependency graphs, and exposes an MCP server so tools like Cursor, OpenCode, and Claude Code can query the knowledge graph directly. No cloud services, no external databases.


Visualize your knowledge graph with force-directed layout, WebGL rendering, and community clustering.

![LeanKG Graph Visualization](docs/screenshots/graph.jpeg)
![LeanKG Obsidian](docs/screenshots/obsidian.jpeg)

See [docs/web-ui.md](docs/web-ui.md) for more features.

---

## Live Demo

Try LeanKG without installing: **https://leankg.onrender.com**

```bash
leankg web --port 9000
```

---

## Installation

### One-Line Install (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- <target>
```

**Supported targets:**

| Target | AI Tool | Auto-Installed |
|--------|---------|-----------------|
| `opencode` | OpenCode AI | Binary + MCP + Plugin + Skill + AGENTS.md |
| `cursor` | Cursor AI | Binary + MCP + Skill + AGENTS.md + Session Hook |
| `claude` | Claude Code | Binary + MCP + Plugin + Skill + CLAUDE.md + Session Hook |
| `gemini` | Gemini CLI | Binary + MCP + Skill + GEMINI.md |
| `kilo` | Kilo Code | Binary + MCP + Skill + AGENTS.md |
| `antigravity` | Google Antigravity | Binary + MCP + Skill + GEMINI.md |

**Examples:**
```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- claude
```

### Install via Cargo or Build from Source

```bash
cargo install leankg && leankg --version
```

```bash
git clone https://github.com/FreePeak/LeanKG.git && cd LeanKG && cargo build --release
```

---

### Docker (Recommended for Teams)

One command — mount your codebase and start the MCP HTTP server on port 9699 (auto-indexes on first start):

```bash
docker run -d --name leankg -p 9699:9699 -v /absolute/path/to/your/project:/workspace -v leankg-rocksdb:/data/leankg-rocksdb freepeak/leankg:latest
```

Use the current directory:

```bash
docker run -d --name leankg -p 9699:9699 -v "$PWD:/workspace" -v leankg-rocksdb:/data/leankg-rocksdb freepeak/leankg:latest
```

Verify:

```bash
curl http://localhost:9699/health
```

Stop / remove:

```bash
docker rm -f leankg
```

Requires [Docker](https://docs.docker.com/engine/install/) or [OrbStack](https://orbstack.dev). Point your AI tool MCP config at `http://localhost:9699/mcp`.

> **Note:** Published image tags (`freepeak/leankg:latest`, `:0.17.8`) currently target `linux/arm64` (Apple Silicon / ARM hosts). On `linux/amd64`, build locally with compose below.

#### Build from source (compose)

```bash
docker compose -f docker-compose.rocksdb.yml up --build
```

For multi-project mounts and local overrides, see [AGENTS.md](AGENTS.md) → RocksDB Docker Deployment.

---

## Quick Start

```bash
leankg init                              # Initialize LeanKG in your project
leankg index ./src                        # Index your codebase
leankg watch ./src                        # Auto-index on file changes
leankg impact src/main.rs --depth 3       # Calculate blast radius
leankg status                             # Check index status
leankg metrics                            # View token savings
leankg web                                # Start Web UI at http://localhost:8080
leankg export --format mermaid            # Export graph as Mermaid, DOT, or JSON
leankg quality --min-lines 50             # Find oversized functions
leankg detect-clusters                    # Identify functional code communities
leankg trace --all                        # Show feature-to-code traceability
leankg annotate src/main.rs::main -d "Entry point"  # Annotate code elements

# Run shell commands with RTK compression
leankg run -- cargo test -- --compress

# REST API server with auth
leankg api-serve --port 8081 --auth
leankg api-key create --name my-key

# Process management
leankg proc status                        # Show running LeanKG/Vite processes
leankg proc kill                          # Kill all LeanKG/Vite processes

# Obsidian vault sync
leankg obsidian init                      # Initialize Obsidian vault structure
leankg obsidian push                      # Push LeanKG data to Obsidian notes
leankg obsidian pull                      # Pull annotation edits from Obsidian
leankg obsidian watch                     # Watch vault for changes and auto-pull
leankg obsidian status                    # Show vault status

# Microservice call graph (via Web UI)
leankg web                                # Start Web UI at http://localhost:8080
                                          # Then visit http://localhost:8080/services

# Multi-repo registry
leankg register my-project                # Register a repository
leankg list                               # List all registered repos
leankg setup                              # Configure MCP for all repos + install Claude hooks
```

See [docs/cli-reference.md](docs/cli-reference.md) for all commands.

---

## Semantic Search (Embeddings)

Optional feature: dense-vector retrieval + cross-encoder reranking + graph
traversal. Off by default to keep the binary slim. Requires building with the
`embeddings` Cargo feature.

**Canonical store:** CozoDB `embedding_vectors` + native HNSW
(`embedding_vectors:vec_idx`, Cosine, f32, 384-dim). Do not migrate the graph
DB to Redis/FalkorDB to speed cold embed — measured writer-only throughput is
already ~100k+ vec/sec; cold wall time is dominated by ONNX inference
(~170 vec/sec e2e → roughly **~36 min** for ~371k `function,method` nodes on
an M2 Pro 10-core). See PRD v3.6.3 (`FR-EMBED-R1..R4`) and
`generated_docs/embed_bg_job_and_runtime_plan_2026-07-15.md`.

### Build & first-time setup

```bash
# 1. Build with the feature flag
cargo build --release --features embeddings

# 2. Pre-download models (~2.3 GB) — do this once per machine
./target/release/leankg embed --init
```

Models cache to `~/Library/Caches/leankg/models/` (macOS),
`~/.cache/leankg/models/` (Linux), or `%LOCALAPPDATA%\leankg\models` (Windows).

### Build the embedding index

```bash
# Incremental (default): only changed/new nodes — day-2 path (seconds–minutes)
# Defaults: --workers 2 --batch-size 32; further capped by LEANKG_EMBED_MAX_MB
leankg embed --wait --workers 2 --batch-size 32

# Mega-graph cold build: functions/methods only (default filter when >50k nodes)
leankg embed --wait --types function,method

# Force re-embed every selected node
leankg embed --wait --full

# Progress / cancel
leankg embed --status
leankg embed --cancel
```

Incremental runs diff against `embedding_state` and skip rows whose content
hash hasn't changed. Prefer incremental after the first cold pass; avoid
`--full` unless the model or schema changed.

### MCP / Docker: do not block boot on cold embed

Cold embed on a mega-graph can take tens of minutes. Keep MCP healthy
immediately and let vectors catch up in the background:

```bash
export LEANKG_EMBED_ON_BOOT=0              # entrypoint must not wait on embed
export LEANKG_EMBED_BACKGROUND=1           # in-process embed inside mcp-http
export LEANKG_EMBED_MAX_MB=2048            # soft RSS budget (macOS default)
export LEANKG_EMBED_BACKGROUND_WORKERS=1
export LEANKG_EMBED_BACKGROUND_BATCH=32
```

While the index is building, keyword/graph MCP tools work; semantic tools
degrade until HNSW is ready (`leankg embed --status` to poll).

### Query

```bash
# CLI one-shot (retrieve → rerank → traverse)
leankg semantic-context "embedding inference for semantic search"
leankg semantic-context "auth token validation" --env production --top-k 100
leankg semantic-context "..." --no-traverse       # skip Stage 4 graph enrichment
leankg semantic-context "..." --debug             # diagnostics: counts, latency
```

Via MCP, the `kg_semantic_context` tool exposes the same pipeline to AI tools.

### Memory tuning

Embed auto-caps workers, batch size, upsert chunk, and the in-flight vector
queue from `LEANKG_EMBED_MAX_MB` (default **2048** on macOS, **3072** elsewhere)
so a cold run cannot balloon into swap and freeze the host. Inference also
pauses briefly when RSS crosses 90% of that soft cap.

| Knob | Effect |
|------|--------|
| `LEANKG_EMBED_MAX_MB=2048` | Soft RSS budget (set `0` to disable caps — not recommended) |
| `--workers` / `--batch-size` | Requested values; clamped by the memory plan |
| `LEANKG_EMBED_UPSERT_CHUNK` | Writer flush size (also capped under a low budget) |

| `--batch-size` | Approx peak RSS (10-core Mac) | When to use |
|---------------|-------------------------------|------------|
| 32 (CLI default) | ~1–2 GB with 1–2 workers      | Laptop / default |
| 16            | lower                         | Tight `LEANKG_EMBED_MAX_MB` |
| 8             | ~730 MB                       | Memory-pressured host |
| 4             | ~400 MB                       | 1-vCPU container |

For Docker cold embeds, prefer `mem_limit` ≥ 6g **or** set
`LEANKG_EMBED_MAX_MB` below the container limit so backpressure engages
before the OOM killer.

### Internals & design rationale

See [`src/embeddings/EMBEDDINGS.md`](src/embeddings/EMBEDDINGS.md) for the
module architecture, file map, data model, the embed/retrieve pipelines,
operational gotchas, and the rationale for storing vectors natively in
CozoDB's HNSW index.

Design philosophy for the retrieve→rerank→traverse flow is in
[docs/design/hybrid-retrieval-reranking.md](docs/design/hybrid-retrieval-reranking.md).

Runtime measurements and rejected storage levers (WAL-off, Redis side-store):
[generated_docs/embed_bg_job_and_runtime_plan_2026-07-15.md](generated_docs/embed_bg_job_and_runtime_plan_2026-07-15.md).

---

## Configuration (Environment Variables)

| Variable | Default | Purpose |
|----------|---------|---------|
| `LEANKG_MMAP_SIZE` | `67108864` (64 MiB) | SQLite mmap window. Lower = less RSS, more page faults. |
| `LEANKG_DB_ENGINE` | `sqlite` | `rocksdb` enables the RocksDB storage backend (recommended for teams). |
| `LEANKG_ROCKSDB_ROOT` | `~/.leankg-rocksdb` | Centralized RocksDB project store. |
| `LEANKG_AUTO_INDEX` | `1` | Enable index-if-needed on container startup. |
| `LEANKG_EMBED_ON_BOOT` | `1` (image-dependent) | Set `0` so Docker entrypoint does **not** block MCP on cold embed. |
| `LEANKG_EMBED_BACKGROUND` | unset | Set `1` to spawn in-process background embed inside `mcp-http` (shared DB). |
| `LEANKG_EMBED_MAX_MB` | `2048` (macOS) / `3072` | Soft RSS budget for embed: caps workers/batch/channel; pauses infer at 90%. `0` disables. |
| `LEANKG_EMBED_BACKGROUND_WORKERS` | `1` | Worker count for in-process background embed (further capped by `LEANKG_EMBED_MAX_MB`). |
| `LEANKG_EMBED_BACKGROUND_BATCH` | `32` | Batch size for in-process background embed (further capped by `LEANKG_EMBED_MAX_MB`). |
| `LEANKG_EMBED_UPSERT_CHUNK` | `5000` | Rows per Cozo `import_relations` flush during embed (capped under a low RSS budget). |
| `LEANKG_VACUUM_INTERVAL_HOURS` | `1` | Hourly tick that calls `GraphEngine.vacuum()`. Set `0` to disable. **No-op on RocksDB** (background compaction handles it). |
| `LEANKG_WATCHER_DEBOUNCE_MS` | `2000` | File-watcher debounce window. |
| `LEANKG_WATCHER_BURST_LIMIT` | `256` | Soft cap on pending file changes per debounce window. |
| `LEANKG_WATCHER_MAX_DB_SIZE` | `524288000` (500 MiB) | Trigger VACUUM once the on-disk DB exceeds this size. |
| `LEANKG_CACHE_MAX_TOKENS` | `500000` | SessionCache upper bound. Lower this on memory-constrained hosts. |
| `LEANKG_API_PORT` | `9699` | Port for the auto-spawned REST API child process. |

See [INSTRUCTION.md](INSTRUCTION.md) for the full memory-tuning playbook.

---

## Claude Code Setup

LeanKG auto-triggers in Claude Code sessions via lifecycle hooks that route search intents to LeanKG tools instead of native tools.

```bash
# Install LeanKG with Claude Code hooks and plugin
leankg setup

# Then restart Claude Code or run:
/reload-plugins
```

**What `leankg setup` installs:**
- `.claude-plugin/` - Plugin manifest for Claude Code validation
- `hooks/` - Full lifecycle hooks: Setup, SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop
- Adds `leankg@local` to `enabledPlugins` in `~/.claude/settings.json`

**Hook lifecycle:**
- `Setup` - Version gating on startup
- `SessionStart` - Injects tool selection hierarchy into every session
- `UserPromptSubmit` - Initializes session context with LeanKG patterns
- `PreToolUse` - Nudges toward LeanKG when you use Grep/Read/Bash for code analysis
- `PostToolUse` - Logs LeanKG MCP tool usage for analytics
- `Stop` - Captures session summary for future context retrieval

---

## How LeanKG Helps

```mermaid
graph LR
    subgraph "Without LeanKG"
        A1[AI Tool] -->|Full codebase context| B1[15,000-45,000 tokens]
        B1 --> A1
    end

    subgraph "With LeanKG"
        A2[AI Tool] -->|Targeted subgraph| C[LeanKG Graph]
        C -->|Context reduction| A2
    end
```

**Without LeanKG**: AI processes full context from files found via grep/search.
**With LeanKG**: AI queries knowledge graph for targeted context. Token reduction varies by task complexity (see [benchmark results](tests/benchmark/results/clean-benchmark-2026-04-21.md)).

---


## Highlights

- **Auto-Init** -- Install script configures MCP, rules, skills, and hooks automatically
- **Auto-Trigger** -- Session hooks inject LeanKG context into every AI tool session
- **Token Optimized** -- Targeted subgraph retrieval vs full file scanning
- **Impact Radius** -- Compute blast radius before making changes
- **Pre-Commit Risk Analysis** -- `detect_changes` classifies risk as critical/high/medium/low
- **Dependency Graph** -- Build call graphs with `IMPORTS`, `CALLS`, `TESTED_BY` edges
- **MCP Server** -- Expose graph via MCP protocol for AI tool integration (40 tools)
- **Orchestration** -- Smart context routing with caching via natural language intent
- **Community Detection** -- Auto-detect functional clusters in your codebase
- **Multi-Language** -- Index Go, TypeScript, Python, Rust, Java, Kotlin, Ruby, PHP, Perl, R, Elixir, Bash with tree-sitter
- **Android** -- Extract XML layouts, resources, manifest relationships, and navigation graphs
- **Service Topology** -- Microservice call graph visualization
- **Annotation Search** -- Search code by `@Entity`, `@HiltViewModel`, and other annotations
- **Graph Export** -- Export as JSON, DOT, or Mermaid formats
- **REST API** -- Full REST API with auth and API key management
- **RTK Compression** -- Run shell commands with token-saving compression

See [docs/architecture.md](docs/architecture.md) for system design and data model details.

---

## Supported AI Tools

| Tool | Auto-Setup | Session Hook | Plugin | Full Lifecycle Hooks |
|------|------------|--------------|--------|---------------------|
| Cursor | Yes | session-start | - | - |
| Claude Code | Yes | session-start | Yes | Setup, SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop |
| OpenCode | Yes | - | Yes | - |
| Docker | Yes | - | - | - |
| Kilo Code | Yes | - | - | - |
| Gemini CLI | Yes | - | - | - |
| Google Antigravity | Yes | - | - | - |
| Codex | Yes | - | - | - |

> **Note:** Cursor requires per-project installation. The AI features work on a per-workspace basis, so LeanKG should be installed in each project directory where you want AI context injection.

See [docs/agentic-instructions.md](docs/agentic-instructions.md) for detailed setup and auto-trigger behavior.

---

## Context Metrics

Track token savings to understand LeanKG's efficiency.

```bash
leankg metrics --json              # View with JSON output
leankg metrics --since 7d           # Filter by time
leankg metrics --tool search_code   # Filter by tool
```

See [docs/metrics.md](docs/metrics.md) for schema and examples.

---

## Update

```bash
# Check current version
leankg version

# Update LeanKG binary (kills processes, removes old binary, installs hooks)
leankg update

# Or via install script
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- update

# Obsidian vault sync
leankg obsidian init                      # Initialize Obsidian vault
leankg obsidian push                      # Push LeanKG data to Obsidian notes
leankg obsidian pull                      # Pull annotation edits from Obsidian
```


---

## Documentation

| Doc | Description |
|-----|-------------|
| [docs/cli-reference.md](docs/cli-reference.md) | All CLI commands |
| [docs/mcp-tools.md](docs/mcp-tools.md) | MCP tools reference |
| [docs/agentic-instructions.md](docs/agentic-instructions.md) | AI tool setup & auto-trigger |
| [docs/architecture.md](docs/architecture.md) | System design, data model |
| [docs/web-ui.md](docs/web-ui.md) | Web UI features |
| [docs/metrics.md](docs/metrics.md) | Metrics schema & examples |
| [docs/benchmark.md](docs/benchmark.md) | Performance benchmarks |
| [docs/roadmap.md](docs/roadmap.md) | Feature planning |
| [docs/tech-stack.md](docs/tech-stack.md) | Tech stack & structure |
| [docs/android-extraction.md](docs/android-extraction.md) | Android XML & resource extraction |
| [src/embeddings/EMBEDDINGS.md](src/embeddings/EMBEDDINGS.md) | Embeddings module internals (vector index, pipelines, native HNSW rationale) |

---

## Troubleshooting

## Troubleshooting

### High RAM Usage on macOS

LeanKG uses memory-mapped I/O and in-memory caching which can consume significant RAM on macOS. Primary causes:

| Cause | Location | Fix |
|-------|----------|-----|
| SQLite mmap_size=256MB | `src/db/schema.rs:20` | Set `LEANKG_MMAP_SIZE=134217728` (128MB) |
| Deprecated `all_elements()` | `src/graph/query.rs:537` | Use `get_elements_paginated()` instead |
| Deprecated `all_relationships()` | `src/graph/query.rs:992` | Use `get_relationships_paginated()` |
| SessionCache 500K tokens | `src/compress/session_cache.rs:11` | Set `LEANKG_CACHE_MAX_TOKENS=100000` |
| Multiple GraphEngine cached | `src/mcp/server.rs:48-49` | Cache eviction with TTL |
| Multiple cache layers | Various | Enable `memory_only` mode for PersistentCache |
| Unbounded DB file growth on long-lived servers | `src/graph/query.rs:100` | `LEANKG_VACUUM_INTERVAL_HOURS=1` (default) reclaims free pages hourly. No-op on RocksDB. |

**Quick fix - add to your shell profile:**
```bash
export LEANKG_MMAP_SIZE=134217728   # 128MB instead of 256MB
export LEANKG_CACHE_MAX_TOKENS=100000  # 100K instead of 500K
```

See [INSTRUCTION.md](INSTRUCTION.md) for detailed memory tuning and MCP server setup.

### Embeddings feature (`--features embeddings`)

The optional embeddings pipeline (dense-vector retrieval + reranker) has its
own failure modes. See [`src/embeddings/EMBEDDINGS.md`](src/embeddings/EMBEDDINGS.md)
for full operational notes. Common issues:

| Symptom | Cause | Fix |
|---------|-------|-----|
| Cold embed takes ~30–40+ min on mega-graphs | ONNX inference ~170 vec/sec e2e (not Cozo writer) | Keep MCP up with `LEANKG_EMBED_ON_BOOT=0` + background embed; use `--types function,method`; rely on incremental day-2 |
| MCP / Docker stuck unhealthy for hours | Entrypoint waiting on sync embed | Set `LEANKG_EMBED_ON_BOOT=0`; use `LEANKG_EMBED_BACKGROUND=1` |
| `embed` peaks at 10+ GB RSS | ORT arenas × many workers × large batch/channel | Set `LEANKG_EMBED_MAX_MB=2048` (default on macOS); use `--workers 1 --batch-size 8` |
| `semantic-context` returns 0 seeds, `Env-filtered: N` in `--debug` | elements' `env` doesn't match the requested env (default `local`) | pass `--env <value>`, or re-index with the right env |
| `parser::pest` from `embed` | ran against an old build that uses `:delete` (CozoDB 0.2.2 only supports `:rm`) | rebuild from current `main` |
| `semantic-context` says `Reranker: fallback` | bge-reranker-v2-m3 failed to init (corrupt cache, OOM) | `leankg embed --init` to re-download; lower `--batch-size` |
| Searches miss elements that state says are `fresh` | DB was modified out-of-band (manual SQLite edit) without re-running `embed` | `leankg embed --full` |
| Considering Redis/FalkorDB to “fix” cold embed | Writer is not the limiter (~100k+ vec/sec empty-DB) | Do not migrate; see plan doc / PRD v3.6.3 Won't Do |

### Database Lock Error

If you see `database is locked (code 5)`, another LeanKG process is holding the database:

```bash
# Kill all leankg and vite processes
leankg-kill

# Or manually
pkill -9 -f "leankg"
pkill -9 -f "vite"
```

### Process Management

```bash
leankg proc kill        # Kill all leankg and vite processes
leankg proc status      # Show running leankg/vite processes
```

**Important:** Always kill the web server before indexing to avoid database lock conflicts.

---

## Performance Benchmarks

### Load Test Results (100K nodes)

| Operation | Throughput |
|-----------|------------|
| Insert elements | ~57,618 elements/sec |
| Insert relationships | ~67,067 relationships/sec |
| Retrieve all elements | ~418,718 elements/sec |
| Cache speedup (cold to warm) | 345-461x |

Run load tests:
```bash
cargo test --release load_test -- --nocapture
```

### Unified A/B Benchmark (All Tools, Simple to Complex)

Measures latency, input/output token usage, and token efficiency across **19 test cases** spanning all LeanKG tools (search, find, context, dependencies, impact radius, call graphs, ontology) at 3 complexity levels, with automated Markdown export.

```bash
# Run the unified benchmark (rebuild first if source changed)
cargo build --release
target/release/leankg benchmark-unified --project .
```

| Metric | With LeanKG | Without (grep) | Winner |
|--------|-------------|----------------|--------|
| Input Token Savings | 30.0% | -- | **LeanKG** |
| Token Efficiency (tokens/result) | 2.09 | 6.39 | **LeanKG (3x)** |
| Latency (simple queries) | 20.4ms | 20.2ms | ~Equal |
| Latency (complex queries) | 8.9s | 34.9ms | Manual (impact radius is heavy) |

See [benchmark/results/unified-benchmark-1782980096.md](benchmark/results/unified-benchmark-1782980096.md) for the full report (JSON + Markdown).

### A/B Benchmark Results (Legacy)

See [tests/benchmark/results/clean-benchmark-2026-04-21.md](tests/benchmark/results/clean-benchmark-2026-04-21.md) for earlier A/B testing results comparing LeanKG vs baseline code search.

---

## Requirements

- Rust 1.75+
- macOS or Linux

---

## License

MIT

---

## Star History

<a href="https://www.star-history.com/?repos=FreePeak%2FLeanKG&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=FreePeak/LeanKG&type=date&theme=dark&legend=top-left" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=FreePeak/LeanKG&type=date&legend=top-left" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=FreePeak/LeanKG&type=date&legend=top-left" />
 </picture>
</a>
