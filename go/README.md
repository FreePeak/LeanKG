# LeanKG Go Engine (W1)
> **v4.6.0 status:** all migration waves have landed (W2 watcher/writer, W4 PostgreSQL+pgvector, W5 ConnectRPC + auth, session offload, graph verbs, doc indexing, parity goldens, benchmark harness). **The Rust tree has been removed — this module is the codebase.** Explicit deferred ledger: ontology procedural workflows/traceability, context compression, LSP bridge, Android specialist extractors, in-process local-ONNX runtime (use the llama.cpp sidecar via `LEANKG_EMBED_PROVIDER=local`). Open ops item: Go release engineering (binary artifacts, npm wrapper, semantic-release retarget).


Greenfield Go rewrite of the LeanKG engine — the strangler-pattern successor
described in [`../docs/go-rewrite-analysis.md`](../docs/go-rewrite-analysis.md)
(PRD v4.5.0/4.5.1). The Rust line (v0.30.x) stays released and in maintenance;
all new product work lands here.

## Scope of this slice (W1 + #368 + #369)

| Wave | Delivered |
|---|---|
| W1 core | 3-tool surface (`import`/`query`/`status`), SQLite store (WAL, FTS5, watermark freshness), L0–L3 ladder with `retrieval{rung,reason}` provenance, regex indexer with 3-signal change detection |
| #368 | `leankg-embed` as an independent binary (`run`/`full`/`export`/`import`/`status`), shared `internal/embed` library, ModelStamp guard on every vector writer, NDJSON offsite export/import, stamp pins as real revisions |
| #369 | Full-markdown memory: `MEMORY.md`/`USER.md` (2,200-byte bounds, error-not-truncate) + `topics/*.md`, Claude-Code file commands with path-traversal rejection, Hermes substring sugar, FTS5 re-index on write, mnemopi JSONL banks adapter (wyhash64 port) |
| W1 transports | MCP stdio + streamable HTTP (official `modelcontextprotocol/go-sdk`), REST `/health`, `/api/v1/{status,query,import}` + hindsight-shaped memory endpoints |

**Deferred** (later waves per the migration plan): tree-sitter extraction
(W2 — regex extraction is the documented ceiling), PostgreSQL/pgvector backend
(W4), ConnectRPC (W5), local llama.cpp sidecar runtime inside leankg-embed
(W6 — the provider port already speaks the sidecar's OpenAI shape), parity
fixtures + cutover (W7).

## Layout

```
cmd/leankg/         server: serve (MCP stdio+HTTP, REST) | index | doctor
cmd/leankg-embed/   embedding pipeline: run | full | export | import | status
internal/store/     SQLite WAL, migrations, FTS5, vector BLOBs, watermark
internal/core/      3-tool envelope + L0-L3 ladder + memory routing
internal/index/     regex extraction, 3-signal change detection
internal/memory/    markdown memory + mnemopi banks adapter (#369)
internal/embed/     Provider port, ModelStamp guard, pipeline, NDJSON (#368)
internal/mcp/       go-sdk adapters (stdio + streamable HTTP)
internal/rest/      stdlib net/http REST surface
```

## Quick start

```bash
leankg index /path/to/repo
leankg serve --http :9699 --rest :8080 --memory \
    # query-time embedder (L3); empty = L3 degrades with reason:
    #   LEANKG_EMBED_PROVIDER=openai|local|deterministic
leankg-embed run            # incremental embed (hash-diffed)
leankg-embed status         # last run / stamps / coverage (DB-read only)
```

## Decisions taken (analysis §9 open questions)

1. **Tool names**: `import`/`query`/`status` — exactly 3, CI-pinned in
   `internal/mcp/server_test.go`. `set`/`get` stay valid as *action* aliases
   per the deprecation policy; as tool names they are rejected over the wire
   (tested).
2. **Query-time embeddings**: provider-first (OpenAI-compatible API or
   llama.cpp sidecar — same HTTP shape). The serving binary performs zero
   inference; query-time embedding is an HTTP client call.
3. **Stamp guards**: every vector writer enforces the stamp (full+mismatch ⇒
   clear+rebuild; incremental+mismatch ⇒ hard fail with a `leankg-embed full`
   directive; first build writes). Query-side drift DEGRADES to L2 with
   `retrieval.reason` — never silently wrong answers.
4. **Project resolution (W1)**: explicit `--project` / cwd. roots/list is NOT
   the only path (deprecated by SEP-2577 in the go-sdk); server-initiated
   roots/list lands when the MCP HTTP surface needs remote resolution.

## Verification

```bash
go build ./... && go test ./...   # 7 packages, incl.:
#   - store: WAL-mode pin, RO-write rejection, DSN space-path safety
#   - core:  ladder order L1→L2→L3, L3 stamp-drift degrade, memory routing
#   - mcp:   registry == exactly 3 tools; tools/call round-trip; legacy 'set' rejected
#   - embed: incremental-on-fresh stamps; incremental-mismatch hard-fail, vectors untouched
#   - memory: traversal/overflow/ambiguous/banks-cursor pins
```

Live smoke: index a real dir → embed run twice (second run skips) → serve →
L1/L2/L3 queries over REST → memory create/search → MCP tools/list == 3.
