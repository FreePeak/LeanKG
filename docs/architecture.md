# Architecture

LeanKG answers questions about a codebase from an index it builds itself,
rather than from a model's guesses or a grep loop over whole files. This
document describes the pieces and how a request moves through them. For
box-and-arrow diagrams, see [Flow diagrams](architecture-flow.md).

## Design constraints

| Constraint | Consequence |
| --- | --- |
| Answers must be traceable | Every query reports its retrieval rung and the freshness of the index |
| Answers must be cheap for agents | Responses are targeted subgraphs and compressed file reads, not whole files |
| The server owns access | Roles are declared and enforced per route; a non-loopback bind needs a token |
| Deployment stays simple | One CGO-free binary; SQLite by default; PostgreSQL with pgvector opt-in |
| One engine, many doors | stdio, HTTP MCP, REST, ConnectRPC, CLI and the web UI are thin layers over the same core |

## Component map

| Component | Responsibility | Location |
| --- | --- | --- |
| Indexer | Walk a repository, extract elements and relationships | [`internal/index`](../internal/index), [`internal/docindex`](../internal/docindex), [`internal/prdindex`](../internal/prdindex) |
| Language registry | Which languages are indexed, and which extractor tier applies | [`internal/langs`](../internal/langs) |
| Store | Elements, relationships, FTS index, vectors, memory, watermarks | [`internal/store`](../internal/store) |
| Query engine | The L0–L3 ladder, graph traversal, compression | [`internal/core`](../internal/core) |
| Embedding pipeline | Provider port, model stamp guards, incremental and full runs | [`internal/embed`](../internal/embed) |
| Transports | MCP, REST, ConnectRPC, web UI, CLI | [`internal/mcp`](../internal/mcp), [`internal/rest`](../internal/rest), [`internal/rpc`](../internal/rpc), [`internal/web`](../internal/web) |
| Auth | Roles, tokens, accounts, per-route role floors | [`internal/auth`](../internal/auth) |

The store contract is split by role ([`internal/store/roles.go`](../internal/store/roles.go)).
Only composition roots — `cmd/*`, `core.New`, `store.OpenBackend` — hold a
full `store.Backend`; every other consumer depends on the narrowest role it
uses.

## Indexing

Extraction runs in tiers, and the boundary between them is explicit:

1. **Regex tier** — always available, no external dependency. Every registered
   language gets it.
2. **tree-sitter tier** — built behind the `tstree` build tag, for grammars
   that need a real parse.
3. **ast-grep tier** — used when the `ast-grep` CLI is on `PATH`.
4. **Language servers** — start lazily at query time when configured, and stop
   after an idle TTL.

The result is a graph of elements (files, functions, types, modules) and
relationships (`contains`, `calls`, `imports`, `references`). Call edges are
resolved by name within a directory, so cross-package dispatch is best-effort;
see [Known limits](#known-limits).

Indexing modes:

- `leankg index` is one-shot. In a git repository root with commits, it reads
  the HEAD commit's objects, so change detection is by blob ID and
  uncommitted or ignored files are skipped.
- `leankg writer` is a long-running reconcile loop. For a git-backed store, a
  new commit (a change of `.git/logs/HEAD`) triggers a reindex; saving without
  committing does not change the graph.
- `leankg index --hook` is the session-start path, bounded by file count,
  embedding backlog and time so it never blocks an agent session.

## Storage

SQLite is the default: one store per repository at `.leankg/leankg.db`, with
WAL, FTS5 for the keyword rung, and float32-BLOB vectors. There is no database
server to run.

PostgreSQL with pgvector is opt-in (`LEANKG_DB_ENGINE=postgres` +
`LEANKG_PG_URL`) for shared, server-scale deployments. It uses a schema per
project and a per-model HNSW index.

Both backends implement the same roles, and `scripts/test-dual-engine.sh` is
the acceptance gate for storage-layer changes.

## Retrieval ladder

A query walks a ladder and stops at the first rung that can answer:

| Rung | Match | Backing index |
| --- | --- | --- |
| L1 | Exact identifier | FTS / exact key |
| L2 | Keyword and fuzzy | FTS5 on SQLite, full-text on PostgreSQL |
| L3 | Semantic | Embeddings, with keyword and vector ranks fused by reciprocal rank fusion |

A natural-language question starts at L3 when L1 misses. If a rung is
unavailable — no vectors yet, for example — the answer comes from the next
rung down and says so. Every answer carries:

- `retrieval.rung` and `retrieval.reason` — which rung answered, and why;
- a `freshness` block — `fresh`, `possibly_stale` or `cold`;
- `guidance` when the result is weak.

The ladder is what makes an answer auditable: a consumer can tell an exact
symbol hit from a semantic fallback without trusting the prose.

## Memory and team knowledge

The team layer lives in the same store as the code graph:

- markdown memory banks per project, at `<project>/.leankg/memory`;
- incidents, ownership, environments and service context;
- PRD requirements and trace links;
- annotations and links from elements to user stories.

The MCP `query` and `import` tools reach this layer through
`action=memory`, so an agent recalls context at session start and retains it at
session end without a second service.

## Transports

Every door calls the same core:

- **MCP** — stdio for spawned harnesses, streamable HTTP at `/mcp` for shared
  servers. The surface is four tools: `import`, `query`, `status`,
  `authenticate`.
- **REST** — `/api/v1/*` for scripts, CI and integrations.
- **ConnectRPC** — gRPC, gRPC-Web and JSON.
- **Web UI** — the embedded graph explorer and dashboard.
- **CLI** — `leankg query`, `impact`, `status` and friends.

Because the engine is shared, the same question returns the same answer
whichever door it came through.

## Multi-project routing

A process can serve several repositories. `LEANKG_PROJECT_DIRS` registers
them, and each request selects one by name or by a path inside it. Stores open
lazily on first use.

On a multi-project server the `project` argument is required: the server
refuses to guess which graph a question is about, because an omitted project
used to be answered from the server's working directory. That produced
confident answers built from the wrong graph. A single-project server keeps
the argument optional.

Tokens can be limited to named projects with `project:<name>` scopes; a
project outside the grant is refused, and an unknown name gets the same
refusal so the response never reveals which projects exist.

## Trust boundaries

| Boundary | Control |
| --- | --- |
| Network listener | Bearer token required beyond loopback; per-route role floors |
| MCP tool call | Role check plus project grant |
| stdio | Local subprocess, runs as Admin, not gated |
| File reads | Path confinement to the project root |
| Read-only instances | Store opened read-only; mutations return `LEANKG_ERROR_READ_ONLY` |
| Vector writes | Model-stamp guarded; a model change fails loudly |

## Known limits

- **Call edges are resolved by name within a directory.** There is no import
  or type resolution, so a same-name call in the same directory resolves, and
  cross-package dispatch is best-effort. The upgrade path is tree-sitter symbol
  tables ([`internal/index/relations.go`](../internal/index/relations.go)).
- **Indexer heuristic guards.** Files of 1 MiB or more are skipped as vendored
  or minified bundles; call targets shorter than 4 characters are dropped as
  noise; outgoing calls are capped per element (40) and per file (500).
- **The unit of scope is a repository.** A portfolio root with tens of
  thousands of nested files is not a project; register its child repositories
  one at a time.
- **`--ui` exposes a data API.** Bind it to loopback or front it with a proxy.
  Its `/api/file` reader needs a Viewer token when the gate is on, and answers
  403 on a non-loopback bind when the gate is off.

Enterprise gaps — no SSO, no TLS termination, no application-level encryption,
audit append not wired — are listed in
[Deployment and security](deployment.md#current-limits).
