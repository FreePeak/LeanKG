<p align="center">
  <img src="assets/icon.svg" alt="LeanKG" width="128" height="128">
</p>

<h1 align="center">LeanKG</h1>

<p align="center">
  <strong>Local-first code knowledge graph for AI coding agents</strong><br>
  Surgical context · fewer tool calls · blast-radius awareness · 100% local<br>
  Pre-index your repo. Serve precise subgraphs over MCP to Cursor, Claude Code, OpenCode, and more — no cloud, no external database.
</p>

<p align="center">
  <a href="https://leankg.onrender.com"><strong>Live Demo →</strong></a>
  ·
  <a href="https://github.com/FreePeak/LeanKG/blob/main/docs/cli-reference.md">Docs</a>
  ·
  <a href="https://hub.docker.com/r/freepeak/leankg">Docker Hub</a>
</p>

<p align="center">
  <a href="https://mcptoplist.com/server/glama%2FFreePeak%2FLeanKG"><img src="https://mcptoplist.com/badge/glama%2FFreePeak%2FLeanKG.svg" alt="MCP Toplist"></a>
  <a href="https://github.com/FreePeak/LeanKG/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0"></a>
  <a href="https://www.rust-lang.org/"><img src="https://img.shields.io/badge/Rust-1.75%2B-orange?logo=rust&logoColor=white" alt="Rust 1.75+"></a>
  <a href="https://crates.io/crates/leankg"><img src="https://img.shields.io/crates/v/leankg.svg" alt="crates.io"></a>
  <a href="https://hub.docker.com/r/freepeak/leankg"><img src="https://img.shields.io/docker/v/freepeak/leankg?label=docker&logo=docker" alt="Docker Hub"></a>
  <a href="https://github.com/FreePeak/LeanKG/actions"><img src="https://img.shields.io/github/actions/workflow/status/FreePeak/LeanKG/ci.yml?branch=main&label=CI" alt="CI"></a>
  <a href="https://safeskill.dev/scan/freepeak-leankg"><img src="https://img.shields.io/badge/SafeSkill-77%2F100-yellow" alt="SafeSkill"></a>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/macOS-supported-blue.svg" alt="macOS">
  <img src="https://img.shields.io/badge/Linux-supported-blue.svg" alt="Linux">
  <img src="https://img.shields.io/badge/Docker-supported-blue.svg" alt="Docker">
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Cursor-supported-blueviolet.svg" alt="Cursor">
  <img src="https://img.shields.io/badge/Claude_Code-supported-blueviolet.svg" alt="Claude Code">
  <img src="https://img.shields.io/badge/OpenCode-supported-blueviolet.svg" alt="OpenCode">
  <img src="https://img.shields.io/badge/Gemini-supported-blueviolet.svg" alt="Gemini">
  <img src="https://img.shields.io/badge/Codex-supported-blueviolet.svg" alt="Codex">
  <img src="https://img.shields.io/badge/Antigravity-supported-blueviolet.svg" alt="Antigravity">
  <img src="https://img.shields.io/badge/Kilo-supported-blueviolet.svg" alt="Kilo">
</p>

<p align="center">
  <img src="assets/banner.svg" alt="LeanKG — Next Gen Knowledge Core: Semantic Pruning, Knowledge Enrichment, Dynamic Querying, Vector Embedding, Inference Synthesis, Ontology Alignment, Context-Aware Reasoning, Unified KG" width="100%">
</p>

---

## Installation

**One command** — binary, MCP wiring, and agent docs for your tool of choice:

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- <target>
```

| Target                            | What you get                                                              |
| --------------------------------- | ------------------------------------------------------------------------- |
| `cursor`                          | Binary + MCP + skill + AGENTS.md + session hook                           |
| `claude`                          | Binary + MCP + plugin + skill + CLAUDE.md + hooks                         |
| `opencode`                        | Binary + MCP + plugin + skill + AGENTS.md                                 |
| `gemini` / `kilo` / `antigravity` | Binary + MCP + skill + agent docs                                         |
| `docker`                          | Hub image (`freepeak/leankg:latest`, `:0.19.21`) + MCP HTTP (**no Rust**) |

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
```

<details>
<summary>Prefer Cargo or build from source?</summary>

```bash
cargo install leankg
# or
git clone https://github.com/FreePeak/LeanKG.git && cd LeanKG && cargo build --release
```

</details>

<details>
<summary>Teams / Docker (no Rust toolchain)</summary>

```bash
# Single-container (embedded RocksDB inside leankg) — small/medium teams.
# Pull and run the latest stable image:
docker run -d --name leankg -p 9699:9699 \
  -v $(pwd):/workspace \
  freepeak/leankg:latest
curl http://localhost:9699/health

# Or pin a version:
docker run -d --name leankg -p 9699:9699 \
  -v $(pwd):/workspace \
  freepeak/leankg:0.19.21
curl http://localhost:9699/health

# Quick-start script (same as above, in one command):
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/docker-up.sh | bash

# Enterprise (RocksDB in its own `cozoserver` sidecar) — independent scaling,
# backup orchestration, HA on the storage tier. See docs/enterprise-docker.md.
docker build -f Dockerfile.cozoserver -t freepeak/cozoserver:latest .
docker build -f Dockerfile.rocksdb    -t freepeak/leankg:latest     .
docker compose -f docker-compose.enterprise.yml up -d
```

Point your MCP client at `http://localhost:9699/mcp`. Multi-project RocksDB mounts: [AGENTS.md](AGENTS.md).

> Published Hub tags currently target `linux/arm64`. On `linux/amd64`, build with `docker compose -f docker-compose.rocksdb.yml up --build`.

</details>

---

## Contents

- [Installation](#installation)
- [Get Started](#get-started)
- [Why LeanKG?](#why-leankg)
- [Measured Results](#measured-results)
- [Key Features](#key-features)
- [Screenshots](#screenshots)
- [How It Works](#how-it-works)
- [MCP & Agents](#mcp--agents)
- [Language Support](#language-support)
- [CLI Quick Reference](#cli-quick-reference)
- [Documentation](#documentation)
- [Troubleshooting](#troubleshooting)
- [Contributing](#contributing)
- [License](#license)
- [Star History](#star-history)

---

## Get Started

### 1. Wire up your agent(s)

Installing the binary alone does **not** connect your agent. Run setup (or use an install target above) so MCP is registered:

```bash
leankg setup
```

This configures Cursor, Claude Code, OpenCode, Gemini, and other supported clients with LeanKG’s MCP server, skills, and hooks where available.

### 2. Index each project

```bash
cd your-project
leankg init
leankg index ./src
leankg status
```

Optional: enable watch mode so the graph stays fresh while you and your agent edit code:

```bash
leankg mcp-stdio --watch
```

### 3. Ask better questions

```bash
leankg impact src/main.rs --depth 3
leankg path "Handler" "Repository"
leankg explain "APIRouter"
leankg graph-query "what connects auth to the database?"
leankg web    # UI at http://localhost:8080
```

Upgrade anytime:

```bash
leankg update
```

---

## Why LeanKG?

When an AI agent needs to understand code, it usually discovers structure the slow way: grep, glob, and Read — one file at a time — rebuilding call paths and dependencies by hand. That is a pile of tool calls and round-trips before the real work starts.

**LeanKG hands the agent the exact subgraph it needs.** It indexes symbols, edges, tests, docs, and (optionally) embeddings into a local knowledge graph, then exposes them over MCP. Instead of crawling the tree, the agent asks one question and gets back callers, dependents, blast radius, and targeted source — **surgical context, not a file-by-file search**.

```mermaid
graph LR
    A[AI Agent] -->|intent| B[LeanKG MCP]
    B --> C[Graph + Embeddings]
    C -->|targeted context| A
```

| Without LeanKG                         | With LeanKG                                    |
| -------------------------------------- | ---------------------------------------------- |
| Grep → open many files → large context | Query the graph → minimal, relevant subgraph   |
| No blast-radius awareness              | Impact radius with confidence + severity       |
| Keyword-only search                    | Keyword + semantic (HNSW) + ontology           |
| Stale mental model of the repo         | Index + optional `--watch` incremental updates |

> **On cost:** LeanKG’s win on every codebase is **precision and speed** — fewer tool calls, faster answers. Token savings are real and **scale-dependent**: modest on small repos, material on large monorepos multiplied by team-wide agent usage.

### Company ROI vs grep and Graphify

For engineering managers choosing a team-wide stack: [LeanKG vs Graphify — Company ROI Brief](docs/reports/leankg-vs-graphify-company-roi-2026-07-21.md) (token/tool-call floors, multi-repo Docker TCO, mega-graph safety, ops/traceability). The primary adoption lever is always-on graph-first install (`curl …/install.sh | bash -s -- cursor` or `claude`) so agents query the graph before grep.

---

## Measured Results

Vector-engine A/B gate (100 tasks, synthetic agent workload vs grep/cat-style baseline) — see [`docs/benchmarks/vector_engine_gate_results.json`](docs/benchmarks/vector_engine_gate_results.json):

| Metric              | Result        | Floor      |
| ------------------- | ------------- | ---------- |
| Token reduction     | **−65.0%**    | ≥ 60%      |
| Tool-call reduction | **−84.6%**    | ≥ 80%      |
| Speedup             | **2.50×**     | ≥ 2×       |
| 1M SQ8 ANN P95      | **~0.055 ms** | &lt; 50 ms |

Unified agent A/B (19 cases vs grep baseline): **~30% input token savings**, **~3× tokens/result efficiency**.

Load test (~100K nodes):

| Operation                   | Throughput  |
| --------------------------- | ----------- |
| Insert elements             | ~57k / sec  |
| Insert relationships        | ~67k / sec  |
| Retrieve elements           | ~419k / sec |
| Cache speedup (cold → warm) | 345–461×    |

```bash
cargo build --release
target/release/leankg benchmark-unified --project .
cargo bench --bench vector_engine_ab
```

Full methodology: [docs/benchmark.md](docs/benchmark.md)

---

## Key Features

- **MCP-native** — 85+ tools for search, impact, call graphs, ontology, architecture, and team knowledge
- **Procedural ontology auto-update** — edit `ontology/workflows.yaml` while serving; watcher re-syncs so `kg_trace_workflow` returns corrected steps without restart
- **Impact radius** — blast radius before you change code, with confidence and severity
- **Dependency graph** — `imports`, `calls`, `tested_by`, `http_calls`, `service_calls`, tunnels, and more
- **Semantic search** — CozoDB HNSW over dense embeddings (`--features embeddings`; included in Docker)
- **Community detection** — Leiden clusters with per-cluster skill context
- **Local-first storage** — SQLite by default; RocksDB for multi-project / team deploy
- **Token-aware payloads** — targeted subgraphs + TOON responses (~40% smaller MCP payloads)
- **Team knowledge** — incidents, env conflicts, service topology, Obsidian vault sync
- **Graph export** — Mermaid, DOT, HTML, SVG, GraphML, Neo4j, portable snapshots
- **Web UI (v2)** — explorer shell adapted from [GitNexus](https://github.com/abhigyanpatwari/GitNexus) `gitnexus-web` (Force / Tree / Circles, filters, search, code panel); data plane is LeanKG `/api/*` (`ui-v2/` + `leankg serve`)

Architecture: [docs/architecture.md](docs/architecture.md) · MCP catalog: [docs/mcp-tools.md](docs/mcp-tools.md) · UI v2: [ui-v2/README.md](ui-v2/README.md)

---

## Screenshots

<p align="center">
  <strong>UI v2</strong> uses the <a href="https://github.com/abhigyanpatwari/GitNexus">GitNexus</a> web exploring shell (layout modes, 3-pane chrome, Sigma) with LeanKG’s REST graph API.
</p>

<p align="center">
  <img src="docs/reports/screenshots/01-force-src.png" alt="LeanKG UI v2 Force layout" width="90%">
</p>

<p align="center">
  <em>UI v2 — Force layout (Sigma), filters, and status bar against <code>leankg serve</code>.</em>
</p>

<p align="center">
  <img src="docs/reports/screenshots/02-tree-src.png" alt="LeanKG UI v2 Tree layout" width="45%">
  &nbsp;
  <img src="docs/reports/screenshots/03-circles-src.png" alt="LeanKG UI v2 Circles layout" width="45%">
</p>

<p align="center">
  <em>Tree and Circles layouts on the same subgraph.</em>
</p>

<p align="center">
  <img src="docs/reports/screenshots/07-code-panel.png" alt="LeanKG UI v2 code panel" width="90%">
</p>

<p align="center">
  <em>Node select opens syntax-highlighted source via <code>/api/file</code>.</em>
</p>

<p align="center">
  <img src="docs/reports/screenshots/05-search.png" alt="LeanKG UI v2 header search" width="45%">
  &nbsp;
  <img src="docs/reports/screenshots/04-query-panel.png" alt="LeanKG UI v2 Query FAB" width="45%">
</p>

<p align="center">
  <em>Header search (<code>/api/search</code>) and Query FAB (<code>/api/query</code>).</em>
</p>

<p align="center">
  <img src="docs/reports/screenshots/06-mega-skip.png" alt="LeanKG UI v2 mega-graph skip gate" width="90%">
</p>

<p align="center">
  <em>Mega-graph skip gate with “Load graph anyway”.</em>
</p>

## Full set: [docs/reports/ui-v2-screenshots-2026-07-20.md](docs/reports/ui-v2-screenshots-2026-07-20.md) · App notes: [ui-v2/README.md](ui-v2/README.md) · Live demo: **https://leankg.onrender.com** · Shell provenance: [GitNexus](https://github.com/abhigyanpatwari/GitNexus)

## How It Works

1. **Extract** — tree-sitter (and language-specific extractors) turn source into `CodeElement` nodes and typed relationships.
2. **Store** — CozoDB over SQLite (local), embedded RocksDB (Docker, single container), or a remote `cozoserver` (enterprise two-container mode — see [docs/enterprise-docker.md](docs/enterprise-docker.md)) holds the graph + optional HNSW vectors.
3. **Serve** — MCP stdio (editor agents) or HTTP/SSE (Docker / remote) answers tools like `get_impact_radius`, `search_code`, `semantic_search`, `get_architecture`.
4. **Refresh** — `--watch` and incremental index keep code edges fresh; ontology YAML watch keeps procedural workflows aligned.

```text
Repo ──► Indexer ──► Knowledge Graph ──► MCP Tools ──► AI Agent
              │              │
              └─ embeddings ─┘ (optional)
```

---

## MCP & Agents

| Agent                      | Auto-setup | Notes                                                                                |
| -------------------------- | ---------- | ------------------------------------------------------------------------------------ |
| Cursor                     | Yes        | Per-project install; always-on graph-first rule + session hook; skill `using-leankg` |
| Claude Code                | Yes        | Plugin + full lifecycle hooks (PreToolUse nudge)                                     |
| OpenCode                   | Yes        | Plugin + skill                                                                       |
| Gemini CLI                 | Yes        | MCP + skill / agent docs                                                             |
| Codex / Antigravity / Kilo | Yes        | MCP + skill / agent docs                                                             |
| Docker MCP HTTP            | Yes        | Shared RocksDB; multi-repo mounts                                                    |

```bash
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
leankg mcp-stdio --watch     # local AI tools
leankg mcp-http --port 9699  # HTTP/SSE for Docker / remote
```

### Prefer-order (discover before connection verbs)

When `:9699` health is OK, for fuzzy / NL / “where is X?” questions **discover first** — do **not** open with `query_graph`:

`get_overview_context` → `mcp_status` → `concept_search` → **`semantic_search`** → `search_code` / `find_function` → then connection verbs → `get_context` / impact / deps.

| Question type               | First tools                                              |
| --------------------------- | -------------------------------------------------------- |
| Fuzzy / meaning / domain NL | `concept_search` → **`semantic_search`** → `search_code` |
| Exact symbol / file name    | `find_function` / `search_code` / `query_file`           |
| How A↔B? (known endpoints)  | `shortest_path`                                          |
| What is this known symbol?  | `explain_node`                                           |
| Expand subgraph after seeds | `query_graph` (**after** semantic/concept hits)          |

Docker MCP: pass container `project=` (`/workspace`); override with `LEANKG_MCP_PROJECT`.

### Background embed without blocking MCP

With persistent RocksDB volumes, the in-process background embed now defaults to **partial / duty-cycled** (serial + yield + pause), so MCP requests keep flowing while the resume pass runs. Operators turn it on at boot via:

```yaml
# docker-compose.override.yml (key knobs)
LEANKG_EMBED_BACKGROUND=0   # keep MCP decoupled from embed (FR-EMBED-R1)
LEANKG_EMBED_AUTO_ARM=1     # arm embed on first idle pass
LEANKG_EMBED_BACKGROUND_FULL=0   # stay in partial/duty-cycled mode
LEANKG_EMBED_BACKGROUND_MEGA=1   # opt-in if your graph is mega (opt-in only)
LEANKG_EMBED_IDLE_AFTER_SECS=30  # idle window before the arm kicks in
LEANKG_EMBED_PARTIAL_BATCHES=4   # batches per yield cycle
LEANKG_EMBED_PARTIAL_PAUSE_MS=500
```

Operator rules of thumb:

- **First run / cold fill** — leave the default `LEANKG_EMBED_BACKGROUND_FULL=0`; the partial path does a serial pass on the smallest needed rows.
- **Day-2 / resume** — same env, no rebuild cost; `vectors_existing` becomes non-zero and `embed_control(status)` reports `mode: partial_incremental`.
- **Cold rebuild (escape hatch)** — offline: stop the container, run `docker compose -f docker-compose.embed.yml --profile embed up`, then bring MCP back up. This avoids competing with MCP for RocksDB / RSS.

To inspect / flip mid-flight: `embed_control(action=on|off|status)` over MCP.

---

### Embedding operational guide (what the docs above assume)

**Two-workspace local convention.** The local MCP container always mounts exactly two project roots:

| Container mount | Host dir |
|-----------------|----------|
| `/workspace`    | this repo (`freepeak/leankg` … your checked-out tree) |
| `/workspace-be` | the side-by-side monorepo (`/Users/<you>/work/be` locally) |

Pass `project=/workspace-be` (never the host path) on every MCP tool call that targets the side repo.

**Why in-process `embed_control action=on` can OOM on a mega-graph.** MCP already holds both `/workspace` + `/workspace-be` RocksDBs (~5–6 GB RSS before embedding starts). The embed shares that same process (RocksDB is single-writer per path), so a 4–6 worker INT8 embed can push the container past its `mem_limit` (exit 137 / restart loop). `LEANKG_EMBED_MAX_MB=0` in the merged override disables the RSS cap — that combination is exactly what kills it.

**Reliable cold full embed of a side mount (step-by-step):**

1. **Stop MCP** so the embed is the single writer:
   ```bash
   docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml stop leankg
   ```
2. **Choose the embed command by workload:**
   - Small / medium graph → the compose profile (auto `--full`):
     ```bash
     LEANKG_MCP_PROJECT=/workspace-be \
       docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml \
       -f docker-compose.embed.yml --profile embed run --rm leankg-embed
     ```
   - **Mega-graph (hundreds of k elements) → keep workers low.** The compose profile resolves 4→6 workers and pins RSS against the `LEANKG_EMBED_MAX_MB=5500` soft cap, so it duty-cycles (pauses inference) and crawls. Use a throwaway container with `--workers 2` (RSS stays ~3 GB, no throttle) and let it run:
     ```bash
     docker run --rm -v leankg_leankg-rocksdb:/data/leankg-rocksdb \
       -v leankg_leankg_models:/root/.cache/leankg \
       -v /Users/<you>/work/be:/workspace-be \
       -e LEANKG_DB_ENGINE=rocksdb -e LEANKG_ROCKSDB_ROOT=/data/leankg-rocksdb \
       -e LEANKG_EMBED_FAST=1 -e LEANKG_EMBED_MODEL=bge-q -e LEANKG_EMBED_MAX_SEQ=128 \
       -e LEANKG_EMBED_MAX_BLOB_CHARS=500 -e LEANKG_EMBED_MAX_MB=5500 \
       -e OMP_NUM_THREADS=1 freepeak/leankg:latest \
       embed --wait --project /workspace-be --workers 2 --batch-size 64
     ```
3. **Interrupting an embed is safe (resume skips done work).** Every batch stamps `embedding_state` rows `fresh`, so `SIGKILL` mid-embed only loses the in-flight batch. To resume: re-run the same command **without** `--full` (incremental) — it embeds only the remaining `stale` rows.
4. **Restart MCP:**
   ```bash
   docker compose -f docker-compose.rocksdb.yml -f docker-compose.override.yml up -d leankg
   ```
5. **Verify:** `embed_control(action=status, project=/workspace-be)` → `vectors_existing` non-zero and `resume_preflight.stale` trending to 0; then a `semantic_search` returns HNSW hits (`method: hnsw+ontology-traverse`, `ann_candidate_count > 0`).

**Memory sizing for a mega-graph embed.** `plan_embed_memory` budgets `BASE_MB=900` + `PER_WORKER_MB=350`/worker under `LEANKG_EMBED_MAX_MB`. Each DirectEmbedder INT8 session holds ~300–400 MB; 6 workers + RocksDB block cache ≈ 5 GB, which collides with a 6 GB `mem_limit`. Drop workers (2) or raise `LEANKG_EMBED_MAX_MB` only if the host has RAM to spare. The RSS soft cap is 90% of `LEANKG_EMBED_MAX_MB`; staying under it keeps inference running flat-out instead of duty-cycling.

### Procedural ontology (auto-update)

While `mcp-http` / `mcp-stdio` / `leankg serve` runs, LeanKG watches `ontology/concepts.yaml` and `ontology/workflows.yaml`, debounces (≥1s), and **replaces** the ontology layer in the served DB so `kg_trace_workflow` stays fresh without a restart.

Typical agent loop:

1. Wrong workflow steps in YAML → `kg_trace_workflow` returns them
2. User corrects (rename / add / remove steps) and saves YAML
3. Watcher auto-syncs (YAML is source of truth — old steps disappear, no GID duplicates)
4. Next session query retrieves only the corrected ordered steps

Ontology also refreshes after index, and Docker boot re-syncs when `.leankg/ontology_synced` is older than **either** YAML file. Prefer `kg_trace_workflow` after edits; use `ontology_control(action=sync|status)` when you need an explicit refresh.

| Knob                                | Default              | Purpose                              |
| ----------------------------------- | -------------------- | ------------------------------------ |
| `LEANKG_ONTOLOGY_DIR`               | `<project>/ontology` | Override ontology YAML directory     |
| `LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS` | `1500` (min 1000)    | Debounce for in-process YAML watch   |
| `LEANKG_ONTOLOGY_SYNC_ON_BOOT`      | `timeout`            | Docker: `skip` / `force` / `timeout` |
| MCP `ontology_control`              | —                    | `action=sync\|status` (Admin)        |

Details: [docs/mcp-tools.md](docs/mcp-tools.md) · Smoke: [docs/reports/ontology-proc-auto-smoke-2026-07-21.md](docs/reports/ontology-proc-auto-smoke-2026-07-21.md)

Setup details: [docs/agentic-instructions.md](docs/agentic-instructions.md) · Skill: [instructions/using-leankg/SKILL.md](instructions/using-leankg/SKILL.md) · Tool catalog: [docs/mcp-tools.md](docs/mcp-tools.md)

---

## Language Support

Structural extraction and cross-file edges into one graph (no per-language product setup):

| Family    | Languages / formats    |
| --------- | ---------------------- |
| Systems   | Rust, Go, C / C++\*    |
| JVM       | Java, Kotlin           |
| Web       | TypeScript, JavaScript |
| Scripting | Python, Ruby*, PHP*    |
| Mobile    | Dart, Swift*, Objective-C*, Android XML |
| Infra     | Terraform, CI YAML     |

\*Depth varies by extractor maturity — see the PRD / roadmap for parity status.

---

## CLI Quick Reference

```bash
leankg init
leankg index ./src
leankg status
leankg impact <file> --depth 3
leankg path <from> <to>
leankg explain <symbol>
leankg graph-query "<question>"
leankg detect-clusters
leankg embed --init && leankg embed   # needs --features embeddings
leankg web
leankg mcp-stdio --watch
leankg mcp-http --port 9699
leankg ontology sync                  # concepts + workflows → DB
leankg ontology trace <workflow>      # ordered procedural steps
leankg update
```

Full CLI: [docs/cli-reference.md](docs/cli-reference.md)

---

## Documentation

| Doc                                                          | Description                                                                                |
| ------------------------------------------------------------ | ------------------------------------------------------------------------------------------ |
| [docs/cli-reference.md](docs/cli-reference.md)               | All CLI commands                                                                           |
| [docs/mcp-tools.md](docs/mcp-tools.md)                       | MCP tool reference                                                                         |
| [docs/agentic-instructions.md](docs/agentic-instructions.md) | AI tool setup & auto-trigger                                                               |
| [docs/architecture.md](docs/architecture.md)                 | System design & data model                                                                 |
| [docs/web-ui.md](docs/web-ui.md)                             | Web UI                                                                                     |
| [docs/benchmark.md](docs/benchmark.md)                       | Benchmark methodology                                                                      |
| [src/embeddings/EMBEDDINGS.md](src/embeddings/EMBEDDINGS.md) | Embeddings / HNSW internals                                                                |
| [INSTRUCTION.md](INSTRUCTION.md)                             | Memory tuning & ops playbook                                                               |
| [docs/roadmap.md](docs/roadmap.md)                           | Roadmap                                                                                    |
| [AGENTS.md](AGENTS.md)                                       | Agent / Docker deployment notes                                                            |
| [docs/enterprise-docker.md](docs/enterprise-docker.md)       | Two-container cozoserver + leankg enterprise stack (deploy, sizing, backup, upgrade paths) |

---

## Troubleshooting

| Issue                           | Fix                                                                                                             |
| ------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| High RAM on macOS               | `export LEANKG_MMAP_SIZE=134217728` and `LEANKG_CACHE_MAX_TOKENS=100000` — see [INSTRUCTION.md](INSTRUCTION.md) |
| `database is locked`            | `leankg proc kill` (stop web/MCP before re-index)                                                               |
| Embeddings / cold embed         | [src/embeddings/EMBEDDINGS.md](src/embeddings/EMBEDDINGS.md)                                                    |
| MCP “not initialized” in Docker | Pass **container** `project=` paths (e.g. `/workspace`), not the host Mac path — see [AGENTS.md](AGENTS.md)     |

---

## Requirements

- Rust **1.75+** (only when building from source)
- **macOS** or **Linux**
- Docker optional (recommended for teams / multi-repo)

---

## Contributing

Issues and PRs are welcome. For larger changes, open an issue first so we can align on design.

1. Fork and create a feature branch (prefer a git worktree for isolation)
2. Update docs when behavior changes (`docs/prd.md` / task tracker as needed)
3. `cargo build --release && cargo test`
4. Open a PR with a clear summary and test plan

---

## License

[Apache License 2.0](LICENSE)
