# LeanKG — Go engine

The code knowledge graph for AI coding agents: index a repository into a
symbol/relationship store, then answer structure questions over MCP in a few
hundred tokens instead of a grep loop. **This module is the codebase** — the
Rust engine was removed at the parity cutover (f7624143).

Product doc: [`../README.md`](../README.md) · requirements + design: [`../docs/prd.md`](../docs/prd.md)

```bash
go install github.com/FreePeak/LeanKG/go/cmd/leankg@latest         # server + CLI
go install github.com/FreePeak/LeanKG/go/cmd/leankg-embed@latest   # embedding pipeline
```

Module versions are tagged `go/vX.Y.Z` — a subdirectory module needs the
directory prefix or the proxy sees no releases at all — starting at `go/v0.31.3`.
[Releases](https://github.com/FreePeak/LeanKG/releases/latest) also carry
CGO-free `leankg-<os>-<arch>.tgz` for linux/darwin × amd64/arm64.

## Surface

| | |
|---|---|
| **MCP** | exactly 3 tools — `import` / `query` / `status` (pinned by `internal/mcp/server_test.go`). `query` routes the ladder (L1 exact → L2 keyword/FTS → L3 semantic) and **degrades instead of erroring**, so every answer carries `retrieval{rung,reason}` + `freshness` |
| **Storage** | SQLite (WAL, FTS5, float32-BLOB vectors, DB-resident watermark) by default; PostgreSQL + pgvector opt-in (`LEANKG_DB_ENGINE=postgres` + `LEANKG_PG_URL`) with schema-per-project and per-model HNSW — both behind `store.Backend` |
| **Transports** | MCP stdio · MCP streamable HTTP (`--http`, `/mcp` + `/health`) · REST (`--rest`, `/health` + `/api/v1/*`) · ConnectRPC (`--rpc`) · embedded dashboard (`--ui`, `/health` + the legacy `/api/*` the UI calls) |
| **Indexing** | 40 language profiles (`internal/langs.Default`), AST tiers regex → ast-grep → tree-sitter (behind the `tstree` tag), 3-signal change detection, `writer` role with fsnotify reconcile, remote `--source` sync |
| **Embeddings** | `leankg-embed` binary + provider port (OpenAI-compatible / llama.cpp sidecar / deterministic). Every vector writer is `ModelStamp`-guarded, so a model change fails loudly instead of mixing vector spaces |
| **Graph + org layer** | impact, shortest path, callers/callees, context, explain, clusters, ontology concept/procedural queries, PRD traceability, incidents / env-conflicts / service topology, portfolio (fleet) reads |

## Layout

```
cmd/leankg/         serve (stdio | MCP HTTP | REST | RPC | dashboard) · index · writer
                    query · impact · status · doctor · report · connect · install
                    prd · prd-trace · incident · note · obsidian · push · pull · update
cmd/leankg-embed/   run · full · export · import · status
internal/store/     Backend interface + SQLite (WAL/FTS5/watermark) + PGStore (pgvector)
internal/core/      3-tool envelope + L0–L3 ladder + memory/graph routing
internal/index/     extractors, 3-signal detection, call-edge resolution
internal/langs/     the 40 profiles, AST tiers, per-language LSP specs
internal/graph/     impact · path · callers/callees · context · explain · clusters
internal/ontology/  concept catalog + procedural workflows/traceability
internal/mcp/       modelcontextprotocol/go-sdk adapters (stdio + streamable HTTP)
internal/rest/      stdlib net/http REST surface
internal/web/       ui-v2 dashboard via //go:embed (checked-in build) + its /api/*
internal/embed/     provider port, ModelStamp guards, NDJSON export/import
internal/memory/    full-markdown memory + mnemopi bank adapter
internal/watch/     fsnotify reconcile (writer role)
internal/golden/    Rust-vs-Go parity fixtures
```

## Build

```bash
go build ./... && go vet ./... && go test ./... -count=1   # CGO-free shape
go build -tags tstree ./...                                # tree-sitter tier (CGO)
```

The dashboard build under `internal/web/embed` is checked in and re-synced by
`make go-ui-assets`; its provenance marker is `embed/ui-build.json`.
`scripts/test-dual-engine.sh` is the SQLite + live-PostgreSQL gate
(`LEANKG_TEST_PG_URL` gates the PG half).

## Known limits

- **Call edges are package-scoped.** No import/type resolution, so a same-name
  call in the same package resolves and cross-package dispatch is best-effort;
  the upgrade path is tree-sitter symbol tables.
- Heuristic guards, documented in `internal/index/relations.go`: files ≥ 1 MiB
  are skipped as vendored/minified bundles, call targets shorter than 4
  characters are dropped as noise, and outgoing calls are capped per element and
  per file.
- The **unit of scope is a repository.** A portfolio root (tens of thousands of
  nested files) is not a project; register its children one at a time.
- `--ui` binds an **unauthenticated** data API (`query`/`read`/import routes).
  Bind it loopback or front it with a proxy — the public demo container serves
  it `--read-only` against a disposable baked graph.
- `docs/mcp-tool-contract.md` at the repo root is still generated from the
  deleted Rust tool registry, so it documents the pre-cutover `set`/`get` pair.
