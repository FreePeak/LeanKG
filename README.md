<p align="center">
  <img src="assets/icon.svg" alt="LeanKG" width="128" height="128">
</p>

<h1 align="center">LeanKG</h1>

<p align="center"><strong>⚠️ Implementation: 100% Go (v4.6.0).</strong> The Rust implementation has been removed; the engine now lives in <a href="go/">go/</a> — see <a href="docs/prd.md">docs/prd.md</a> for the parity ledger. Build: <code>make go-build</code> · Test: <code>make go-test</code> · Bench: <code>make go-bench</code>. Rust-era sections below are historical.</p>



<p align="center">
  <strong>Enterprise-ready code knowledge graph for AI coding agents</strong><br>
  Multi-repo · env governance · incidents &amp; services · req↔code · −65% tokens / −85% tool calls
</p>

<p align="center">
  <a href="https://leankg.onrender.com"><strong>Live Demo</strong></a>
  ·
  <a href="docs/prd.md">Docs</a>
  ·
</p>

<p align="center">
  <a href="https://github.com/FreePeak/LeanKG/blob/main/LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0"></a>
  <a href="https://github.com/FreePeak/LeanKG/actions"><img src="https://img.shields.io/github/actions/workflow/status/FreePeak/LeanKG/ci.yml?branch=main&label=CI" alt="CI"></a>
</p>

<p align="center">
  <img src="assets/banner.svg" alt="LeanKG" width="100%">
</p>

---

## Installation

### Prerequisites

None — **sqlite is the default storage engine**. No Postgres, no Docker.

Postgres remains available as an explicit opt-in (`LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`) for server-scale deployments, but nothing in the default flow touches it.

### Install

Requires [Go 1.25+](https://go.dev/dl/) and git. Builds two binaries: `leankg`
(server + CLI) and `leankg-embed` (embedding pipeline).

```bash
# From a checkout — installs to ~/.local/bin (pass a PREFIX to change it)
git clone https://github.com/FreePeak/LeanKG.git && cd LeanKG
scripts/install-go.sh                # or: make install-go

# Or fetch and run the installer directly (clones over HTTPS, same behavior):
curl -fsSL https://raw.githubusercontent.com/FreePeak/LeanKG/main/scripts/install-go.sh | bash
```

Release archives (`leankg-<os>-<arch>.tgz`, both binaries inside) are produced
by the manual [Go Release](.github/workflows/release-go.yml) workflow — it is
`workflow_dispatch`-only and **no Go release has been published yet**, so build
from source for now.

---

## Get Started

```bash
# 1. Per project: one-shot index (sqlite default — zero config, store at .leankg/leankg.db)
cd your-project
leankg index .

# 2. Wire up an AI client — one command (claude-code | cursor | codex | gemini | opencode | omp)
leankg connect claude-code           # stdio entry; --http --url http://host:9699/mcp to reuse a shared server

# 3. ...or serve MCP over HTTP yourself (endpoint /mcp; GET /health returns 200 when ready)
leankg serve --http 127.0.0.1:9699 --rest 127.0.0.1:8080
```

Self-check any deployment: `leankg doctor` — prints the store path, element and
file counts and the write watermark (exit 0 pass / 2 fail).

MCP over HTTP: the server resolves the project from its process cwd — run it
from the checkout or pass `--project DIR` to pin one.

### Web UI

The embedded dashboard is served by `leankg serve --ui ADDR` (a ui-v2 build
compiled into the binary). The Go engine's dashboard data API is not wired up
yet — the REST surface on `--rest` exposes only `/health` and the `/api/v1/*`
tool endpoints — so treat the dashboard as not yet functional.

For UI development, run the Vite dev server against a REST address (it proxies
`/api` to `BACKEND_TARGET`, default `http://127.0.0.1:8080`):

```bash
# Terminal A — REST API
leankg serve --rest 127.0.0.1:8080

# Terminal B — hot-reload dev server
cd ui-v2
npm install
npm run dev
# open http://127.0.0.1:5173
```

Details: [ui-v2/README.md](ui-v2/README.md) · [docs/archive/web-ui.md](docs/archive/web-ui.md)

---

## Enterprise Ready

Peers in this space are mostly personal / single-repo. LeanKG is the **company platform**: shared index, ops graph, and measured agent economics.

| Pillar | Ships as |
| ------ | -------- |
| Multi-repo server | MCP HTTP `:9699`, one project per server (`--project DIR`); sqlite default, PG opt-in |
| Env governance | `env=`, `promote_environment`, `find_env_conflicts` |
| Ops & ownership | `get_service_graph`, `query_incidents`, `get_team_map` |
| Req ↔ code | `index_prd`, `get_traceability`, `get_traceability_matrix` |
| Mega-graph | Frontier-local queries; 100k–700k+ elements |
| Agent surface | **1** MCP tool (`leankg_context`) serving ~76 capabilities as verbs; peers typically ~1–17 raw tools |
| Cost | A/B **−65% tokens**, **−85% tool calls**, **2.5×** vs grep/cat |

| Capability | LeanKG | GitNexus | Graphify | Codanna | Context7 |
| ---------- | ------ | -------- | -------- | ------- | -------- |
| Multi-repo team deploy | Yes | Partial | Limited | Limited | n/a |
| Env / incidents / team map | Yes | No | No | No | No |
| PRD traceability | Yes | No | Partial | No | No |
| Mega-graph (100k+) | Yes | Partial | Viz capped | Varies | n/a |
| MCP depth | 77 | ~17 | ~10 | ~5 | docs only |

Deep dives (archived): [ROI vs Graphify](docs/archive/reports/leankg-vs-graphify-company-roi-2026-07-21.md) · [Competitive one-pager](docs/archive/competitive-analysis.md) · [Research matrix](docs/archive/analysis/leankg-competitive-research-and-improvement-strategy-2026-08-02.md)

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
- **SQLite default** (zero-config, no Docker) with an opt-in Postgres/pgvector backend (`LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`)
- **Ontology** — concept-catalog matching (`POST /api/v1/ontology/match`); procedural workflows and req↔code traceability are not implemented in the Go engine yet
- **Impact & deps** — `contains`, `calls`, `imports` edges; BFS blast radius (`leankg impact`)
- **Web UI v2** — Force / Tree / Circles explorer (`cd ui-v2 && npm run dev`; the embedded build is served by `leankg serve --ui`)
- **Languages** — 13 built-in profiles: Go, Rust, TypeScript/TSX, JavaScript/JSX, Python, Markdown, Java, Kotlin, Swift, Objective-C, Dart

---

## MCP prefer-order

Discover first — do **not** open with `query_graph`:

`leankg_context` → `get_overview_context` → `mcp_status` → `concept_search` / `semantic_search` / `search_code` → impact / deps / `get_context`

| Question | First tools |
| -------- | ----------- |
| Any question (default) | `leankg_context` (intent is auto-classified; degrades L3→L0 instead of erroring) |
| Fuzzy / domain NL | `concept_search` → `semantic_search` → `search_code` |
| Exact symbol / file | `search_code` |
| How A↔B? | `shortest_path` |
| Expand after seeds | `query_graph` |

Catalog: [docs/archive/mcp-tools.md](docs/archive/mcp-tools.md) · Setup: [docs/archive/agentic-instructions.md](docs/archive/agentic-instructions.md)

---

## CLI

```bash
leankg index .                          # one-shot index -> .leankg/leankg.db
leankg writer                           # index once, then watch + re-index
leankg query "parseConfig"              # name lookup (exact, then fuzzy) — JSON out
leankg query "parseConfig" --compress   # one line per result
leankg impact src/main.go --depth 3     # blast radius of a file or element
leankg status                           # health, inventory, freshness, embed state
leankg doctor                           # store path, element/file counts, watermark
leankg connect claude-code              # MCP entry: claude-code|cursor|codex|gemini|opencode|omp
leankg install --target cursor          # same wiring, flag form (--register-cwd: claude-code hook)
leankg serve --stdio                    # MCP over stdio (what harnesses spawn)
leankg serve --http 127.0.0.1:9699      # MCP over streamable HTTP (/mcp, /health)
leankg serve --rest 127.0.0.1:8080      # REST API (/health, /api/v1/*)
leankg serve --ui 127.0.0.1:8081        # embedded dashboard (data API not wired yet)
leankg serve --rpc 127.0.0.1:9090       # ConnectRPC (gRPC + gRPC-Web + JSON)
leankg version
```

UI hot-reload: `cd ui-v2 && npm install && npm run dev` → http://127.0.0.1:5173

Full usage: `leankg help` and `leankg <command> --help`. The archived Rust-era
CLI reference: [docs/archive/cli-reference.md](docs/archive/cli-reference.md)

---

## Docs

The documentation set lives in [`docs/`](docs/) — a single unified PRD (`docs/prd.md`) + task tracker (`docs/prd-task-tracker.md`). All historical design docs, analyses, reports, and plans are preserved under [`docs/archive/`](docs/archive/).

| Doc | |
| --- | --- |
| [PRD](docs/prd.md) | Unified product requirements + HLD (single SoT) |
| [Task tracker](docs/prd-task-tracker.md) | Done / in-progress / todo |
| [Architecture (archived)](docs/archive/architecture.md) | Design & data model (historical) |
| [MCP tools (archived)](docs/archive/mcp-tools.md) | Tool catalog (historical) |
| [CLI (archived)](docs/archive/cli-reference.md) | All commands (historical) |
| [Benchmarks (archived)](docs/archive/benchmark.md) | Methodology (historical) |
| [Postgres migration (archived)](docs/archive/analysis/pg-migration-report.md) | Engine notes (historical) |
| [AGENTS.md](AGENTS.md) | Agent notes |

---

## Troubleshooting

| Issue | Fix |
| ----- | --- |
| Wrong project served | Start the server with `--project DIR` (`query`/`impact` also honor `LEANKG_PROJECT`) |
| Embeddings / cold embed | `leankg-embed status`, then `leankg-embed full` (provider env: `LEANKG_EMBED_*`) |

**Requirements:** macOS or Linux · Go 1.25+ only when building from source. No
Docker, no Postgres — sqlite is the default store.

---

## Contributing

1. Fork + feature branch (prefer a worktree)
2. Update docs when behavior changes
3. `cd go && go build ./... && go vet ./... && go test ./...`
4. Open a PR with summary + test plan

## License

[Apache License 2.0](LICENSE)
