# LeanKG HTTP MCP Validation Handoff

Date: 2026-05-20 10:15 +07

## Current Goal

Merge the HTTP MCP fix, pull latest `origin/main`, rebuild the LeanKG HTTP MCP server, and validate the tools against the BE project graph.

## Repository State

- Repository: `/Users/linh.doan/work/harvey/freepeak/leankg`
- Current branch: `fix/mcp-project-arg-routing`
- Latest local commit: `aab818b fix: avoid absolutizing graph query paths`
- `origin/main` currently points to: `123fe77 Merge pull request #55 from FreePeak/fix/mcp-index-fetch-failed`
- PR #55 was merged successfully into `origin/main`.
- The current branch contains a follow-up fix discovered during real HTTP MCP validation after PR #55 was merged.

Untracked files existed before/around this work and were not modified except for the new handoff document:

- `.codex-backup/`
- `.leankg.backup-20260515-0948`
- `docs/LeanKG_v2_PRD.html`
- `docs/design/hybrid-retrieval-reranking.md`
- `docs/design/hybrid-search-reranking-proposal.md`
- `docs/planning/2026-05-16-leankg-v2-current-plan.md`
- `docs/planning/2026-05-17-ontology-semantic-search-mvp.md`

One untracked report that conflicted with PR #55 was moved earlier to:

- `.codex-backup/mcp-index-fetch-failed-root-cause-2026-05-19.untracked-before-pr55.md`

## What Was Already Done

1. Verified PR #55 was open, mergeable, and green.
2. Merged PR #55 into `origin/main`.
3. Pulled `origin/main` into the main checkout.
4. Built merged main with `cargo build`; it passed.
5. Started the HTTP MCP server from merged main on port `9699` with:

   ```bash
   /Users/linh.doan/work/harvey/freepeak/leankg/target/debug/leankg mcp-http --port 9699 --project /Users/linh.doan/work/be
   ```

6. Validated basic HTTP MCP tools against:

   ```text
   http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be
   ```

7. Confirmed these worked on merged main:

   - `mcp_hello`
   - `mcp_status`
   - `search_code`
   - `find_function`

8. Found a regression during real validation:

   - `get_context` with a BE project-relative file path hung.
   - Concurrent `mcp_index` then also timed out.
   - The server process showed high CPU.
   - Sampling showed execution stuck under `ToolHandler::get_context -> GraphEngine::get_context -> ContextProvider::get_context_for_file -> GraphEngine::find_element -> Cozo run_script`.

## Root Cause Found After PR #55 Merge

PR #55 fixed the original Cursor `{"error":"fetch failed"}` class of failures by adding project query routing for HTTP MCP. However, the server-side routing logic was too broad.

When the request URL included:

```text
?project=/Users/linh.doan/work/be
```

the HTTP MCP server injected `project` into tool arguments and also resolved every `file`, `doc`, `path`, and `files[]` argument to an absolute path.

That path absolutization is correct for filesystem tools such as `mcp_index`, but incorrect for graph-query tools such as `get_context`, `search_code`, and `find_function`. The BE graph stores many paths as project-relative values such as `./platform-core/...`. Passing `/Users/linh.doan/work/be/platform-core/...` to graph lookups forced expensive fallback scans and caused `get_context` to hang on the large BE graph.

## Follow-Up Fix Implemented

Commit:

```text
aab818b fix: avoid absolutizing graph query paths
```

Changed file:

- `src/mcp/server.rs`

Behavior after the fix:

- The HTTP MCP server still injects `project` into tool arguments for database routing.
- The server now only resolves relative path arguments to absolute paths for filesystem-oriented tools:
  - `mcp_index`
  - `mcp_index_docs`
  - `mcp_init`
  - `detect_changes`
- Graph-query tools keep their original project-relative arguments:
  - `get_context`
  - `search_code`
  - `find_function`
  - and similar read/query tools

New unit test:

```text
test_project_routing_only_absolutizes_filesystem_tools
```

## Tests Already Run

These commands passed on branch `fix/mcp-project-arg-routing`:

```bash
cargo build
cargo test test_project_routing_only_absolutizes_filesystem_tools -- --nocapture
cargo test mcp_server -- --nocapture
cargo fmt --check
git diff --check
```

Known note: test output includes existing warnings in unrelated test/indexer files. No new failure was observed.

## Current Server State

The broken debug server on port `9699` was stopped.

At handoff time, this process was still running:

```text
/Users/linh.doan/work/harvey/freepeak/leankg/target/release/leankg mcp-http --watch --reuse
```

Before starting a new validation server, check which port it owns and avoid accidentally killing an unrelated user process unless you intend to replace it.

Useful checks:

```bash
ps -ef | rg 'leankg mcp-http|target/debug/leankg|target/release/leankg'
lsof -nP -iTCP:9699 -sTCP:LISTEN
```

## How To Continue

1. Confirm working tree state:

   ```bash
   git status --short --branch
   git log --oneline --decorate -3
   ```

2. Push the follow-up branch:

   ```bash
   git push -u origin fix/mcp-project-arg-routing
   ```

3. Create a PR for `fix/mcp-project-arg-routing` into `main`.

4. Wait for CI and merge the PR if green.

5. Pull latest main locally:

   ```bash
   git switch main
   git pull --ff-only origin main
   ```

6. Build from merged `main`:

   ```bash
   cargo build
   ```

7. Start a fresh HTTP MCP server for validation:

   ```bash
   tmux new-session -d -s leankg-mcp-9699-main \
     '/Users/linh.doan/work/harvey/freepeak/leankg/target/debug/leankg mcp-http --port 9699 --project /Users/linh.doan/work/be >> /Users/linh.doan/work/be/.leankg/leankg-mcp-9699-main.log 2>&1'
   ```

8. Validate MCP tools with `curl` against:

   ```text
   http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be
   ```

## Validation Commands To Run Next

Health:

```bash
curl --max-time 30 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"hello","method":"tools/call","params":{"name":"mcp_hello","arguments":{}}}'
```

Status:

```bash
curl --max-time 30 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"status","method":"tools/call","params":{"name":"mcp_status","arguments":{}}}'
```

Search:

```bash
curl --max-time 30 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"search","method":"tools/call","params":{"name":"search_code","arguments":{"query":"main","limit":3}}}'
```

Find function:

```bash
curl --max-time 30 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"find","method":"tools/call","params":{"name":"find_function","arguments":{"name":"main"}}}'
```

Query file. Note that this tool expects `pattern`, not `file`:

```bash
curl --max-time 30 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"query-file","method":"tools/call","params":{"name":"query_file","arguments":{"pattern":"main.go"}}}'
```

Context. This is the key regression check; it should not hang after `aab818b`:

```bash
curl --max-time 60 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"context","method":"tools/call","params":{"name":"get_context","arguments":{"file":"platform-core/be-activity-history/cmd/client/main.go","max_tokens":1000}}}'
```

Index a small BE subdirectory:

```bash
curl --max-time 120 -sS -X POST 'http://127.0.0.1:9699/mcp?project=/Users/linh.doan/work/be' \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"index","method":"tools/call","params":{"name":"mcp_index","arguments":{"path":"platform-core/be-activity-history/cmd/client","resolve_calls":false}}}'
```

## Pass Criteria

The issue should be considered fixed only when all of these are true:

- The follow-up PR containing `aab818b` is merged into `origin/main`.
- `git pull --ff-only origin main` brings the local `main` to the merged fix.
- `cargo build` passes from `main`.
- A freshly started HTTP MCP server from `main` responds successfully to:
  - `mcp_hello`
  - `mcp_status`
  - `search_code`
  - `find_function`
  - `query_file`
  - `get_context`
  - `mcp_index`
- `get_context` no longer hangs when called with a project-relative BE file path and `?project=/Users/linh.doan/work/be`.
- `mcp_index` accepts a project-relative `path` and resolves it to the BE project filesystem path.

