# Root Cause Plan: LeanKG MCP "not working" on BE Monorepo

**Date:** 2026-08-05
**Session evidence:** `/Users/linh.doan/.claude/projects/-Users-linh-doan-work-be/3e3cdb6b-aec6-4072-acbd-6523259aaacc.jsonl` (cwd `/Users/linh.doan/work/be`)
**Symptom:** `mcp__leankg__semantic_search` returns `count: 0` for every query; `mcp_index` aborts.
**Status:** Plan written; remediation sequenced in §6.

---

## 1. Executive Summary

The LeanKG MCP server is **not broken** — it returns well-formed `status: ok` envelopes for every tool call. The "not working" symptom is a stack of five mutually reinforcing configuration and operational mistakes that make the server *behave* like it is broken on the `/Users/linh.doan/work/be` workspace:

1. The LeanKG database for this cwd is **never populated** (`mcp_status` → `database_exists: false`).
2. The only index attempt targets the **entire 60-repo monorepo**, which exceeds the timeout and memory envelope of the in-process MCP embed path and ends in `AbortError: The operation was aborted`.
3. The local-stdio transport is being used here instead of the **Docker HTTP server on `:9699`**, where the workspace-be index already lives behind proper timeout/mem overrides.
4. The `project=` argument passed to search tools is either **missing** (relies on cwd inference) or uses the **host Mac path** instead of the container mount path, which makes the Docker-backed index invisible.
5. The three semantic queries themselves (`CMC signing`, `driver rating`, `update config k8s`) are valid but **never run against a populated DB**, so all results are vacuously empty.

Fix = **pick the right transport, point it at the right mount path, scope indexing to a sub-graph the runtime can finish**, and **only then** re-run the queries.

---

## 2. Reproducible Evidence (from session 3e3cdb6b…)

### 2.1 Tool-call timeline

| Line | Event | Outcome |
|------|-------|---------|
| L18 | `mcp_status` (no args) | `database_exists: false`, `initialized: false`, `storage_path: /Users/linh.doan/work/be/./.leankg/leankg.db`, `storage_engine: sqlite` |
| L19 | `semantic_search("CMC signing")` | `count: 0`, `total_estimate: 0`, `method: ontology+semantic(semantic+name_fallback)` |
| L20 | `semantic_search("driver rating")` | `count: 0` |
| L25 | `semantic_search("update config k8s")` | `count: 0` |
| L28 | `mcp_index(path="/Users/linh.doan/work/be")` | Timeout at 120 s → moved to background as `task k135po9rg` |
| L41 | `task-notification` for `k135po9rg` | **`failed` — `MCP error -32001: AbortError: The operation was aborted`** |

### 2.2 What the responses prove

- The MCP server itself responded in milliseconds with structured JSON on every call. No `status: error`, no transport-level failure. The runtime is healthy.
- The empty `count: 0` is the **correct, documented** behavior of `semantic_search` against an empty database. The server has nothing to search.
- The `AbortError` from `mcp_index` is the client-side MCP SDK aborting the long-running call after its own 120 s timeout — it is a *consequence* of indexing a multi-GB workspace through the MCP tool, not a server fault.

### 2.3 What the session does *not* show

- No `project="/workspace-be"` argument on any call.
- No `LEANKG_MCP_TOOL_TIMEOUT_SECS` override in the environment.
- No `docker-compose.override.yml` lookup before indexing.
- No follow-up after the `AbortError` notification arrived at L41 (the session ends after the away summary).

---

## 3. Root Cause Decomposition

### 3.1 RC-1 — DB never populated for this cwd

`mcp_status` returns `database_exists: false`. There is no `.leankg/leankg.db` under `/Users/linh.doan/work/be/.leankg/`. Without an initialized DB, every search/lookup tool returns empty by design.

**Why it matters:** This is the primary cause of the user-visible "search is broken" symptom. Search is not broken; the dataset is empty.

### 3.2 RC-2 — Monorepo-scale `mcp_index` aborts

`mcp_index(path="/Users/linh.doan/work/be")` is called on the entire BE monorepo (60 repos, multi-GB source). The MCP client aborts after its default 120 s budget. Background task `k135po9rg` ends in `AbortError: The operation was aborted`.

**Why it matters:** Indexing the whole monorepo through the MCP tool is the wrong path. The reliable paths are:

- **Cold offline embed single-writer** (bulk-load 1250 v/s via RocksDB bulk-load; see memory `leankg-embed-bulkload-1250.md`).
- **Docker HTTP MCP** with `LEANKG_MCP_TOOL_TIMEOUT_SECS=300` and `mem_limit: 12g` in `docker-compose.override.yml` (see memory `leankg-mcp-tool-timeout-and-oom.md`).

In-process embed from inside the MCP tool on a multi-GB workspace will OOM (exit 137 / `LEANKG_EMBED_MAX_MB` untied) or hit the SDK abort. Both are observed failure modes.

### 3.3 RC-3 — Wrong transport

`storage_engine: sqlite` and `storage_path: …/.leankg/leankg.db` prove the session is talking to a **local stdio MCP backend**, not the Docker HTTP server on `:9699`. The Docker container already holds the workspace-be index with proper timeout/mem overrides. Local stdio has neither.

**Why it matters:** The Docker MCP on `:9699` is the canonical backend for the BE monorepo (memory `prefer-docker-http-mcp.md`). Local stdio on a never-initialized host path yields "LeanKG is broken" even when the container's index is fine.

### 3.4 RC-4 — Project-path confusion (host vs container)

Neither `mcp_status` nor any of the three `semantic_search` calls pass a `project=` argument. If the active MCP is the Docker HTTP backend, the index is keyed by the **in-container mount path** (`/workspace-be`). The host Mac path `/Users/linh.doan/work/be` resolves to "not initialized" against that index even when data exists.

**Why it matters:** This is the same anti-pattern called out in project `CLAUDE.md` §"MANDATORY: Docker MCP project paths". Passing the wrong `project=` looks identical to "the server is broken" from the client side.

### 3.5 RC-5 — Coverage gap exposed by the queries

Even after fixing RC-1…RC-4, the queries `CMC signing`, `driver rating`, `update config k8s` need both a populated DB and a populated **embeddings table** to be retrievable semantically. If the bulk-load embed step never ran (RC-1/RC-2), the rows exist as code elements but `semantic_search` has no vectors to rank against and falls back to `name_fallback` — also empty when the index is empty.

**Why it matters:** Two layers (RocksDB code graph + vector embeddings) must both be present. The session only attempted one of them.

---

## 4. Failure Mode Cross-Reference (known precedents)

| Failure mode | Memory file | This session |
|--------------|-------------|--------------|
| In-process embed OOM on workspace-be | `leankg-inprocess-embed-oom.md` | Matches — `mcp_index` aborts on full monorepo |
| MCP tool timeout + OOM on mega-graph | `leankg-mcp-tool-timeout-and-oom.md` | Matches — default 120 s abort, no `LEANKG_MCP_TOOL_TIMEOUT_SECS=300` |
| Embed lock poison on first `semantic_search` | `leankg-embed-lock-poison.md` | Latent risk if first call on this setup triggers background embed |
| Enterprise index blocks HTTP | `leankg-enterprise-index-blocks-http.md` | N/A — local stdio here, but same root pattern (blocking work before serving) |
| Embed bulk-load 1250 v/s | `leankg-embed-bulkload-1250.md` | Recommended path for RC-2 |
| Docker `/workspace-be` mount | `leankg-docker-workspace-be-mount.md` | Confirms container mount exists; override bind missing here |
| Large-workspace tuning defaults | `leankg-large-workspace-tuning.md` | Defaults not applied for local stdio |

---

## 5. Decision Matrix: Which Transport to Use

| If you see… | Use… | Why |
|-------------|------|-----|
| `storage_engine: sqlite` + host `.leankg/` path | Local stdio | Wrong for BE monorepo. Switch to Docker MCP. |
| `mcp_status` returns `database_exists: false` on host path | Local stdio against never-initialized cwd | Initialize scoped to one sub-repo or switch transports. |
| Need to index >1 GB workspace | Docker HTTP with `mem_limit: 12g`, `LEANKG_MCP_TOOL_TIMEOUT_SECS=300`, offline cold-embed single-writer | In-process MCP embed will OOM/abort. |
| Quick lookup of an already-indexed workspace | Docker HTTP + `project="/workspace-be"` | Fastest, indexes already loaded. |
| Need to *create* the index for the first time | Offline bulk-load, *then* start Docker HTTP | Avoids RC-2 abort loop. |

---

## 6. Remediation Plan

Sequenced. Each step has a single concrete action and a verification probe.

### Step 1 — Confirm intended backend

```bash
cat ~/.claude.json | jq '.mcpServers'        # which leankg entry is wired?
cat ~/.claude.json | jq '.projects | to_entries[] | select(.key|contains("work/be")) | .value.mcpServers'
```

**Decision gate:** if `user-leankg-be` (Docker HTTP) is wired for the `/Users/linh.doan/work/be` cwd, continue with Steps 2A–5A. If only local stdio is wired, continue with Steps 2B–5B.

### Step 2A — Verify Docker HTTP is healthy and the workspace is mounted

```bash
curl http://localhost:9699/health
docker inspect leankg-leankg-1 --format '{{ json .Mounts }}' | jq '.[] | select(.Destination | test("workspace-be"))'
```

Pass criteria: `/health` returns `{"status":"ok"}`, mount includes `/workspace-be → /Users/linh.doan/work/be`.

### Step 3A — Verify index exists for `/workspace-be`

```jsonc
mcp__leankg__mcp_status(project="/workspace-be")
```

Pass criteria: `database_exists: true`, `initialized: true`.

### Step 4A — Re-run the three queries with `project="/workspace-be"` and `env=`

```jsonc
mcp__leankg__semantic_search(query="CMC signing",      project="/workspace-be", env="local")
mcp__leankg__semantic_search(query="driver rating",    project="/workspace-be", env="local")
mcp__leankg__semantic_search(query="update config k8s", project="/workspace-be", env="local")
```

Pass criteria: non-zero `count` for each (or documented empty + reason if a topic truly has no semantic match in the index).

### Step 5A — If `mcp_status(project="/workspace-be")` shows `database_exists: false`

The Docker container is healthy but the workspace-be index has never been built. Do **not** call `mcp_index` over MCP — run `leankg index` offline (cold-embed single-writer, see memory `leankg-embed-bulkload-1250.md`), then restart the container to load it.

```bash
docker exec leankg-leankg-1 leankg index --path /workspace-be --bulk-embed
docker restart leankg-leankg-1
```

### Step 2B — Decide to keep local stdio

Only valid for sub-repos under ~1 GB. If you must stay on local stdio for BE monorepo, accept the failure mode and scope indexing to one sub-repo:

```bash
cd /Users/linh.doan/work/be/<one-sub-repo>
leankg index .           # offline CLI, not via MCP tool
```

### Step 3B — Override timeout + memory for local stdio

In the shell that launches the local stdio MCP, export:

```bash
export LEANKG_MCP_TOOL_TIMEOUT_SECS=300
export LEANKG_EMBED_MAX_MB=$(sysctl -n hw.memsize | awk '{print int($1/1024/1024/2)}')
```

(Pick `LEANKG_EMBED_MAX_MB` as roughly half of physical RAM; 12 GB cap is a sane ceiling on a 16 GB Mac.)

### Step 4B — Pass `project=` matching the local `.leankg/` key

Local stdio keys by cwd-relative or explicit path. Pass the same path that `mcp_status` returned in `storage_path`:

```jsonc
mcp__leankg__semantic_search(query="CMC signing", project="/Users/linh.doan/work/be/<one-sub-repo>")
```

### Step 5B — Re-run the three queries scoped to the indexed sub-repo

Pick a sub-repo that plausibly contains the topics (e.g. the platform/CMC service for "CMC signing", the driver-telematics service for "driver rating", the platform/cluster-config service for "update config k8s"). Index only that sub-repo first; re-run queries against its `project=`.

---

## 7. Long-Term Preventive Controls

These are durable fixes, not one-time cleanups.

### 7.1 Wire `user-leankg-be` as the canonical backend for the `/Users/linh.doan/work/be` cwd

Add an `mcpServers` override in `.claude/settings.json` at the repo root:

```jsonc
{
  "projects": {
    "/Users/linh.doan/work/be": {
      "mcpServers": {
        "leankg": { "command": "docker", "args": ["exec", "-i", "leankg-leankg-1", "leankg", "mcp-http"] }
      }
    }
  }
}
```

Keeps transport choice out of agent memory; makes the Docker HTTP backend the default for BE work.

### 7.2 Move long-running `leankg index` out of MCP tool surface

Add to project `CLAUDE.md` under "MANDATORY":

> Never call `mcp_index` on a workspace >1 GB. Run `leankg index` offline (CLI) and only use the MCP tool to verify or query.

### 7.3 Standardize `docker-compose.override.yml` overrides for the BE container

Pin in compose:

```yaml
environment:
  LEANKG_MCP_TOOL_TIMEOUT_SECS: "300"
  LEANKG_EMBED_MAX_MB: "12288"
mem_limit: 12g
```

Keep real bind paths in gitignored `docker-compose.override.yml`; reviewers
copy `.dockerfile.example` → `.dockerfile` and edit host paths locally.

### 7.4 Probe script before any `mcp_status` in a new cwd

Add `scripts/probe-leankg.sh` that returns: transport, project path, `database_exists`, `initialized`, last index timestamp. Standardize the first 30 seconds of any agent session on BE to running this probe.

---

## 8. Verification Checklist

After running Steps 1–5A (or 1–5B), each item must pass:

- [ ] `curl http://localhost:9699/health` → `{"status":"ok"}` (A path) OR `mcp_status(project=…)` returns `database_exists: true` (B path)
- [ ] `semantic_search("CMC signing", project=…, env="local")` returns non-zero count **or** a documented empty-with-reason
- [ ] `semantic_search("driver rating", project=…, env="local")` returns non-zero count **or** documented empty
- [ ] `semantic_search("update config k8s", project=…, env="local")` returns non-zero count **or** documented empty
- [ ] No `AbortError` or timeout in the call logs
- [ ] `docker inspect leankg-leankg-1` shows `mem_limit: 12g` and `LEANKG_MCP_TOOL_TIMEOUT_SECS=300` (A path)

If any item fails, do **not** retry the same call. Re-run §6 starting at Step 2 with the relevant diagnostic captured.

---

## 9. Out of Scope / Explicitly Skipped

- **Replacing the `semantic_search` calls with `concept_search` first.** `concept_search` would also return empty on an unpopulated DB. The right fix is populating the DB, not re-routing the same empty query.
- **Adding `CozoDB` rebuild logic.** The migration to PostgreSQL + pgvector already shipped (v0.20.0, commit `f9066b09`). Don't reintroduce Cozo-specific fixes.
- **Touching the Rust source.** No code change solves a missing index or a wrong `project=` argument. Config + transport only.
- **Indexing the full 60-repo monorepo in one shot.** Even after fixes, this will OOM. Scope to sub-repos or use the bulk-load path.

---

## 10. Appendix — Evidence Pack

### A. Session transcript highlights

```
L18  mcp_status → database_exists: false, initialized: false
L19  semantic_search("CMC signing")        → count: 0
L20  semantic_search("driver rating")       → count: 0
L25  semantic_search("update config k8s")   → count: 0
L28  mcp_index(path="/Users/linh.doan/work/be") → background task k135po9rg
L41  task-notification → failed, AbortError: The operation was aborted
```

### B. Memory files cross-referenced

- `leankg-embed-bulkload-1250.md` — correct offline embed path
- `leankg-mcp-tool-timeout-and-oom.md` — timeout/mem overrides for mega-graph
- `leankg-inprocess-embed-oom.md` — why in-process MCP embed aborts
- `leankg-embed-lock-poison.md` — first-call embed poison risk
- `leankg-enterprise-index-blocks-http.md` — blocking-work-before-serving pattern
- `leankg-docker-workspace-be-mount.md` — mount path expectations
- `prefer-docker-http-mcp.md` — canonical backend for BE
- `leankg-large-workspace-tuning.md` — defaults + knobs

### C. Project CLAUDE.md sections invoked

- §"MANDATORY: Docker MCP project paths" — host vs container path
- §"MCP Server Management" — health, restart, port 9699
- §"Step 1: Always Try LeanKG First" — `mcp_status` first probe