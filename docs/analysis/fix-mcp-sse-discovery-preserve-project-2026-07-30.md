# Fix: MCP SSE endpoint discovery strips `?project=` query

**Date:** 2026-07-30
**Branch:** `fix/mcp-sse-discovery-preserve-project`
**Worktree:** `.worktrees/fix-mcp-sse-discovery/`
**Method:** TDD (red → green → next), vertical slices

---

## Problem

Cursor's MCP HTTP transport (streamable-HTTP / SSE) discovers the JSON-RPC
endpoint via a `GET /mcp` SSE handshake, then POSTs JSON-RPC to whatever URL
the server advertises in the SSE response.

`src/mcp/server.rs:3278` hardcodes that advertised URL:

```rust
let sse_data = "event: endpoint\ndata: /mcp\n\n";
```

The query string from the discovery GET is discarded. So even when
`~/.cursor/mcp.json` is configured with:

```json
"leankg-be": {
    "url": "http://localhost:9699/mcp?project=/workspace-be"
}
```

…Cursor first hits `GET /mcp?project=/workspace-be`, gets back
`data: /mcp`, and from then on POSTs to bare `/mcp`. `handle_mcp_request`
sees `uri.query() == None`, no `project_param` is set, and every tool
falls back to `LEANKG_MCP_PROJECT` env (= `/workspace`, the leankg repo
itself). The be monorepo (mounted at `/workspace-be`) is unreachable from
Cursor.

### Reproduction (before fix)

```
$ curl -sN 'http://localhost:9699/mcp?project=/workspace-be'
event: endpoint
data: /mcp                          ← query dropped

$ curl -sN 'http://localhost:9699/mcp/stream?project=/workspace-be'
event: endpoint
data: /mcp                          ← query dropped

$ curl -sN 'http://localhost:9699/mcp'
event: endpoint
data: /mcp
```

### Expected behavior (after fix)

```
$ curl -sN 'http://localhost:9699/mcp?project=/workspace-be'
event: endpoint
data: /mcp?project=/workspace-be    ← preserved

$ curl -sN 'http://localhost:9699/mcp'
event: endpoint
data: /mcp                          ← unchanged when no project
```

---

## Seams under test

| Seam | Boundary | What it tests |
|---|---|---|
| **S1** | `pub(crate) fn discovery_endpoint_url(project: Option<&str>) -> String` in `src/mcp/server.rs` | Pure helper: returns SSE endpoint URL for a given `project` query value. No HTTP, no state. |
| **S2** | `Router` mounted in `src/mcp/server.rs:1775-1780` | HTTP integration: `GET /mcp[?project=…]` and `GET /mcp/stream[?project=…]` return SSE bodies advertising the project-preserved URL. |

S1 = tracer bullet (fast feedback). S2 = regression net (wire-format
witness that Cursor actually consumes).

---

## Vertical slices

### Slice 1 — S1: preserve project in discovery URL

**Red** — `#[cfg(test)] mod tests` in `src/mcp/server.rs`:

| Test | Asserts |
|---|---|
| `returns_just_mcp_when_project_is_none` | `discovery_endpoint_url(None) == "/mcp"` |
| `returns_mcp_with_query_when_project_is_set` | `discovery_endpoint_url(Some("/workspace-be")) == "/mcp?project=/workspace-be"` |
| `treats_empty_project_as_none` | `discovery_endpoint_url(Some("")) == "/mcp"` |

**Green** — extract helper:

```rust
pub(crate) fn discovery_endpoint_url(project: Option<&str>) -> String {
    match project.filter(|p| !p.is_empty()) {
        Some(p) => format!("/mcp?project={}", percent_encode_path(p)),
        None => "/mcp".to_string(),
    }
}
```

### Slice 2 — S1: percent-encode the project value

**Red**:

| Test | Asserts |
|---|---|
| `encodes_spaces_and_special_chars` | `discovery_endpoint_url(Some("/workspace foo?bar")) == "/mcp?project=%2Fworkspace%20foo%3Fbar"` |
| `handles_unicode_path` | non-ASCII project value round-trips encode → decode |

**Green** — hand-rolled encoder (~10 lines), no new deps. Mirrors the
existing inline decoder at `src/mcp/server.rs:2982-3004` but reverse-engineered
into a UTF-8-safe encoder. Avoids adding `urlencoding` crate to keep the
patch dep-free.

### Slice 3 — S2: wire helper into `handle_sse_stream`

**Red** — `#[tokio::test]` in the same `mod tests`:

| Test | Probe | Expected body |
|---|---|---|
| `sse_discovery_preserves_project_on_get_mcp` | `GET /mcp?project=/workspace-be` | `event: endpoint\ndata: /mcp?project=/workspace-be\n\n` |
| `sse_discovery_omits_query_when_no_project` | `GET /mcp` | `event: endpoint\ndata: /mcp\n\n` |
| `sse_discovery_preserves_project_on_get_stream` | `GET /mcp/stream?project=/workspace-be` | `event: endpoint\ndata: /mcp?project=/workspace-be\n\n` |

**Green** — extend handler signature:

```rust
async fn handle_sse_stream(
    State(server): State<Arc<HttpMcpServer>>,
    headers: HeaderMap,
    Query(query): Query<HashMap<String, String>>,
) -> Response {
    // …auth…
    let project = query.get("project").map(String::as_str);
    let sse_data = format!(
        "event: endpoint\ndata: {}\n\n",
        discovery_endpoint_url(project)
    );
    // …response…
}
```

The route at `src/mcp/server.rs:1776-1777` already routes both `GET /mcp`
and `GET /mcp/stream` to `handle_sse_stream`, so one signature change covers
both.

### Slice 4 — S2: regression guard against re-introducing the bug

**Red** — one round-trip test:

| Test | Asserts |
|---|---|
| `discovery_endpoint_url_round_trips_full_path` | `discovery_endpoint_url(Some("/workspace/be"))` decodes back to `/workspace/be` via the server's existing query-string parser |

**Green** — already covered by the encoder from slice 2; this slice is just
the wire-format witness.

---

## Test placement

- All tests in `#[cfg(test)] mod tests` at the bottom of
  `src/mcp/server.rs`, matching the existing pattern in
  `src/mcp/handler.rs:5109`, `src/mcp/tools.rs:1233`,
  `src/mcp/toon.rs:365`.

---

## Refactor stage (after all slices green, NOT part of TDD loop)

Candidates worth a follow-up PR (do not bundle):

- The buggy `byte as char` decoder at `src/mcp/server.rs:2989` — silently
  corrupts any non-ASCII byte in incoming `?project=`. A different bug, but
  the same surface area; should be rewritten alongside this fix in a
  separate commit.
- `handle_sse_stream` returns a static placeholder; the comment at
  `src/mcp/server.rs:3275-3277` notes this is a stub for a real SSE
  message stream. Out of scope here.

---

## Verification (after green, before docker rebuild)

| Probe | Expected |
|---|---|
| `GET /mcp?project=/workspace-be` | `event: endpoint\ndata: /mcp?project=/workspace-be\n\n` |
| `GET /mcp/stream?project=/workspace-be` | `event: endpoint\ndata: /mcp?project=/workspace-be\n\n` |
| `GET /mcp` | `event: endpoint\ndata: /mcp\n\n` |
| `POST /mcp` (no query, no project arg) | `database: /workspace/.leankg` (CLI default unchanged) |
| `POST /mcp?project=/workspace-be` (via SSE-discovered URL) | `database: /workspace-be/.leankg` |

---

## Build + deploy

```bash
# Inside .worktrees/fix-mcp-sse-discovery
cargo build --release
cargo test --release                      # all green, no regressions
docker build -f Dockerfile.rocksdb -t freepeak/leankg:local .

# Outside worktree, where compose stack lives
docker compose -f docker-compose.enterprise.yml \
               -f docker-compose.enterprise.local.yml \
               -f docker-compose.override.yml \
               up -d --force-recreate

curl -fsS http://localhost:9699/health
# Re-run §Verification probes

# Publish
# Bump Cargo.toml 0.19.24 → 0.19.25
docker tag freepeak/leankg:local freepeak/leankg:0.19.25
docker tag freepeak/leankg:local freepeak/leankg:latest
docker push freepeak/leankg:0.19.25
docker push freepeak/leankg:latest

# Commit + PR (no Co-Authored-By per AGENTS.md rule 6)
git commit -m "fix(mcp): preserve ?project= in SSE endpoint discovery"
git push -u origin fix/mcp-sse-discovery-preserve-project
```

---

## Out of scope

- Fixing the byte-as-char decoder at `server.rs:2989`.
- Implementing a real SSE message stream.
- Forcing HNSW rebuild for the be project (separate thread, runs after
  this fix so we can actually target `/workspace-be`).
- Cleaning up the 10 zombie `leankg mcp-stdio --watch` processes.

---

## Decision log

- **Seam choice:** S1 + S2 (both). One pure helper + one HTTP integration.
- **URL encoder:** hand-rolled, no `urlencoding` crate. Keeps the patch
  zero-dep.
- **Test placement:** `#[cfg(test)] mod tests` in `src/mcp/server.rs`.
- **Worktree path:** `.worktrees/fix-mcp-sse-discovery/`, branch off
  `main`.
