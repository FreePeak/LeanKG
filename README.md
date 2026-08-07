<p align="center">
  <img src="assets/icon.svg" alt="LeanKG" width="128" height="128">
</p>

<h1 align="center">LeanKG</h1>

<p align="center">
  <strong>Enterprise-ready code knowledge graph for AI coding agents</strong><br>
  Multi-repo · env governance · incidents &amp; services · req↔code · −65% tokens / −85% tool calls
</p>

<p align="center">
  <a href="https://leankg.onrender.com"><strong>Live Demo</strong></a>
  ·
  <a href="docs/cli-reference.md">Docs</a>
  ·
  <a href="https://hub.docker.com/r/freepeak/leankg">Docker Hub</a>
</p>

<p align="center">
  <a href="https://github.com/FreePeak/LeanKG/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0"></a>
  <a href="https://crates.io/crates/leankg"><img src="https://img.shields.io/crates/v/leankg.svg" alt="crates.io"></a>
  <a href="https://hub.docker.com/r/freepeak/leankg"><img src="https://img.shields.io/docker/v/freepeak/leankg?label=docker&logo=docker" alt="Docker Hub"></a>
  <a href="https://github.com/FreePeak/LeanKG/actions"><img src="https://img.shields.io/github/actions/workflow/status/FreePeak/LeanKG/ci.yml?branch=main&label=CI" alt="CI"></a>
</p>

<p align="center">
  <img src="assets/banner.svg" alt="LeanKG" width="100%">
</p>

---

## Installation

### Prerequisites

Postgres + pgvector is required (only storage engine). From a LeanKG checkout:

```bash
docker compose up -d postgres   # host :5433
```

Default URL: `postgresql://postgres:postgres@localhost:5433/leankg` (override with `LEANKG_PG_URL`).  
One-liners below do **not** start Postgres — they fail if `:5433` is down.

### One-liners

```bash
# Docker — index + embed + MCP HTTP (Postgres must already be up)
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/docker-up.sh | bash

# Agent — binary + MCP wiring (cursor | claude | opencode | gemini | kilo | antigravity | docker | update)
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install.sh | bash -s -- cursor
```

Skip cold embed: `LEANKG_SKIP_EMBED=1 curl -fsSL …/docker-up.sh | bash`

### Docker (manual)

```bash
docker compose up -d          # Postgres :5433 + MCP :9699
# or MCP only (bring your own PG via LEANKG_PG_URL):
docker run -d --name leankg -p 9699:9699 \
  -e LEANKG_PG_URL=postgresql://postgres:postgres@host.docker.internal:5433/leankg \
  -v "$(pwd):/workspace" freepeak/leankg:latest
curl http://localhost:9699/health
```

MCP URL: `http://localhost:9699/mcp`

### From source

```bash
cargo install leankg
# or: git clone https://github.com/FreePeak/LeanKG.git && cd LeanKG && cargo build --release
```

---

## Get Started

```bash
# 1. Postgres (once)
docker compose up -d postgres

# 2. Index your project
leankg setup                 # wire MCP into your agents
cd your-project
leankg init && leankg index ./src && leankg status
leankg impact src/main.rs --depth 3
leankg mcp-stdio --watch     # local agents
leankg mcp-http --port 9699  # HTTP / Docker
```

Docker MCP: pass **container** paths as `project=` (e.g. `/workspace`), never the host Mac path.

### Web UI

UI talks REST (`:8080`), not MCP (`:9699`). Start the API, then the Vite app in `ui-v2/`:

```bash
# Terminal A — REST API (+ embedded UI if assets are in src/embed/)
leankg serve --port 8080
# open http://127.0.0.1:8080/

# Terminal B — hot-reload explorer (recommended for local UI work)
cd ui-v2
npm install
npm run dev
# open http://127.0.0.1:5173/?path=src
```

Vite proxies `/api` → `127.0.0.1:8080`. Status should show **connected**.  
Details: [ui-v2/README.md](ui-v2/README.md) · [docs/web-ui.md](docs/web-ui.md)

---

## Enterprise Ready

Peers in this space are mostly personal / single-repo. LeanKG is the **company platform**: shared index, ops graph, and measured agent economics.

| Pillar | Ships as |
| ------ | -------- |
| Multi-repo server | Docker MCP `:9699` + Postgres/pgvector; `LEANKG_PROJECT_DIRS` |
| Env governance | `env=`, `promote_environment`, `find_env_conflicts` |
| Ops & ownership | `get_service_graph`, `query_incidents`, `get_team_map` |
| Req ↔ code | `index_prd`, `get_traceability`, `search_by_requirement` |
| Mega-graph | Frontier-local queries; 100k–700k+ elements |
| Agent surface | **85+** MCP tools (peers typically ~1–17) |
| Cost | A/B **−65% tokens**, **−85% tool calls**, **2.5×** vs grep/cat |

| Capability | LeanKG | GitNexus | Graphify | Codanna | Context7 |
| ---------- | ------ | -------- | -------- | ------- | -------- |
| Multi-repo team deploy | Yes | Partial | Limited | Limited | n/a |
| Env / incidents / team map | Yes | No | No | No | No |
| PRD traceability | Yes | No | Partial | No | No |
| Mega-graph (100k+) | Yes | Partial | Viz capped | Varies | n/a |
| MCP depth | 85+ | ~17 | ~10 | ~5 | docs only |

Deep dives: [ROI vs Graphify](docs/reports/leankg-vs-graphify-company-roi-2026-07-21.md) · [Competitive one-pager](docs/competitive-analysis.md) · [Research matrix](docs/analysis/leankg-competitive-research-and-improvement-strategy-2026-08-02.md)

---

## Why LeanKG?

Agents normally rebuild structure with grep → open files → huge context. LeanKG returns a **targeted subgraph** (callers, dependents, blast radius, tests, docs) plus the **team layer** (env, services, incidents, requirements) over MCP.

| Without | With LeanKG |
| ------- | ----------- |
| Many tool calls, large context | Surgical subgraph + TOON (~40% smaller payloads) |
| No blast radius | Severity-graded impact |
| Keyword only | Keyword + HNSW semantic + ontology |
| Single-repo guesswork | Multi-repo index + ops tools |

---

## Key Features

- **MCP-native** — search, impact, call graphs, ontology, architecture, team knowledge
- **Postgres + pgvector** — only storage engine; HNSW semantic search (`--features embeddings` / Docker)
- **Procedural ontology** — hot-reload `ontology/workflows.yaml` → `kg_trace_workflow`
- **Impact & deps** — `imports`, `calls`, `tested_by`, `http_calls`, `service_calls`
- **Web UI v2** — Force / Tree / Circles explorer (`leankg serve` + `cd ui-v2 && npm run dev`)
- **Languages** — Rust, Go, C/C++, Java, Kotlin, TS/JS, Python, Ruby*, PHP*, Dart, Swift*, ObjC*, Terraform, CI YAML (*depth varies)

---

## MCP prefer-order

Discover first — do **not** open with `query_graph`:

`get_overview_context` → `mcp_status` → `concept_search` → `semantic_search` → `search_code` / `find_function` → impact / deps / `get_context`

| Question | First tools |
| -------- | ----------- |
| Fuzzy / domain NL | `concept_search` → `semantic_search` → `search_code` |
| Exact symbol / file | `find_function` / `search_code` / `query_file` |
| How A↔B? | `shortest_path` |
| Expand after seeds | `query_graph` |

Catalog: [docs/mcp-tools.md](docs/mcp-tools.md) · Setup: [docs/agentic-instructions.md](docs/agentic-instructions.md)

---

## CLI

```bash
leankg init | index ./src | status | update
leankg impact <file> --depth 3
leankg path <from> <to> | explain <symbol> | graph-query "<q>"
leankg embed --init && leankg embed   # --features embeddings
leankg mcp-stdio --watch | mcp-http --port 9699 | serve --port 8080
leankg ontology sync | ontology trace <workflow>
```

UI hot-reload: `cd ui-v2 && npm install && npm run dev` → http://127.0.0.1:5173

Full reference: [docs/cli-reference.md](docs/cli-reference.md)

---

## Docs

| Doc | |
| --- | --- |
| [Architecture](docs/architecture.md) | Design & data model |
| [MCP tools](docs/mcp-tools.md) | Tool catalog |
| [CLI](docs/cli-reference.md) | All commands |
| [Benchmarks](docs/benchmark.md) | Methodology |
| [Embeddings](src/embeddings/EMBEDDINGS.md) | HNSW / ops |
| [Postgres migration](docs/analysis/pg-migration-report.md) | Engine notes |
| [AGENTS.md](AGENTS.md) | Agent / Docker notes |

---

## Troubleshooting

| Issue | Fix |
| ----- | --- |
| High RAM (macOS) | `LEANKG_MMAP_SIZE=134217728` — see [INSTRUCTION.md](INSTRUCTION.md) |
| MCP “not initialized” in Docker | Use container `project=/workspace`, not the host path |
| Embeddings / cold embed | [src/embeddings/EMBEDDINGS.md](src/embeddings/EMBEDDINGS.md) |

**Requirements:** macOS or Linux · Docker recommended for teams · Rust 1.75+ only when building from source.

---

## Contributing

1. Fork + feature branch (prefer a worktree)
2. Update docs when behavior changes
3. `cargo build --release && cargo test`
4. Open a PR with summary + test plan

## License

[Apache License 2.0](LICENSE)
