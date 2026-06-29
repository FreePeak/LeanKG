# LeanKG HTTP MCP Server Issue Report

**Date:** 2026-06-10
**Repository:** `/Users/linh.doan/work/harvey/freepeak/leankg`
**Scope:** Release-mode validation of the LeanKG HTTP MCP server
**Status:** Core no-auth HTTP MCP paths pass; auth-enabled mode has a blocking issue.

## Summary

The HTTP MCP server works for the core unauthenticated JSON-RPC and SSE paths tested locally. The server built in release mode, the targeted MCP server unit tests passed, and live no-auth endpoint checks passed for health, initialization, tool listing, representative tool calls, project query routing, notifications, invalid JSON handling, CORS preflight, SSE, and `--reuse`.

One blocking issue was found in auth-enabled mode: a server started with `MCP_HTTP_AUTH` rejects the same configured bearer token. This means the documented/configured authentication path is not currently usable for authenticated HTTP MCP clients.

Two non-blocking issues were also found:

- startup auto-index logs directory reindex failures for directory paths;
- there is no dedicated test named around HTTP endpoint behavior, and the current MCP server unit tests did not catch the auth regression.

## Validation Environment

- Branch: `main`
- Release binary: `target/release/leankg`
- Project argument: `/Users/linh.doan/work/harvey/freepeak/leankg`
- No-auth test port: `19799`
- Auth-enabled test port: `19800`
- Test ports were closed after validation.

Tracked files were already modified before this report was written:

```text
Cargo.lock
leankg.yaml
src/config/project.rs
src/mcp/server.rs
```

## Commands Run

```bash
cargo build --release
cargo test --release mcp::server -- --nocapture
cargo test --release http -- --list
target/release/leankg mcp-http --port 19799 --project /Users/linh.doan/work/harvey/freepeak/leankg
target/release/leankg mcp-http --port 19799 --project /Users/linh.doan/work/harvey/freepeak/leankg --reuse
MCP_HTTP_AUTH=codex-token target/release/leankg mcp-http --port 19800 --project /Users/linh.doan/work/harvey/freepeak/leankg
```

## Passing Checks

### Build and Unit Tests

- `cargo build --release` passed.
- `cargo test --release mcp::server -- --nocapture` passed.
- MCP server unit result: `3 passed; 0 failed`.
- `cargo test --release http -- --list` found no matching test cases named `http`.

### Live No-Auth HTTP MCP Server

The server started successfully on port `19799` without auth.

Passing endpoint and protocol checks:

- `GET /health` returned `200` with `{"status": "ok"}`.
- `POST /mcp` `initialize` returned server info with `name: leankg` and version `0.17.2`.
- `resources/list` returned an empty `resources` list.
- `resources/templates/list` returned an empty `resourceTemplates` list.
- `prompts/list` returned an empty `prompts` list.
- `tools/list` returned 59 tools, including `mcp_status`, `search_code`, `query_file`, and `get_context`.
- `tools/call` for `mcp_status` returned content containing `status: ok` and `initialized: true`.
- `tools/call` for `search_code` through `/mcp?project=...` returned `serve_http` in `src/mcp/server.rs`, confirming project query routing.
- JSON-RPC notification `notifications/initialized` returned `204 No Content`.
- Invalid JSON returned HTTP `200` with JSON-RPC parse error code `-32700`.
- CORS preflight returned a successful response with access-control headers.
- `GET /mcp/stream` returned `text/event-stream` with:

```text
event: endpoint
data: /mcp
```

- `--reuse` against the running server exited successfully and reported that port `19799` was already locked by the existing server process.

## Issue 1: Auth-Enabled Server Rejects Configured Bearer Token

**Severity:** Major
**Status:** Open
**Area:** HTTP MCP authentication

### Reproduction

Start the server with a configured token:

```bash
MCP_HTTP_AUTH=codex-token target/release/leankg mcp-http \
  --port 19800 \
  --project /Users/linh.doan/work/harvey/freepeak/leankg
```

Send an authenticated JSON-RPC request:

```bash
curl -i http://127.0.0.1:19800/mcp \
  -H 'Authorization: Bearer codex-token' \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}'
```

### Expected

The server accepts `Authorization: Bearer codex-token` because `codex-token` is the configured token from `MCP_HTTP_AUTH`, then returns the normal `initialize` result.

### Actual

The server returns:

```text
HTTP/1.1 401 Unauthorized
{"error": "Unauthorized"}
```

Unauthenticated and wrong-token requests also return `401`, which is correct. The failing behavior is that the correct configured token also returns `401`.

### Root Cause

`serve_http` stores the configured auth token in `HttpMcpServer.auth_token`, but builds a separate auth manager using `AuthManager::with_default_token()`:

- `src/mcp/server.rs` initializes the HTTP server state with `auth_token` and `AuthManager::with_default_token()`.
- `src/mcp/auth.rs` generates a fresh default token at runtime in `with_default_token()`.
- `extract_auth_context` first compares the bearer token with the configured `auth_token`.
- If that comparison succeeds, it then calls `server.auth_manager.validate_token(stripped)`.
- The configured token is not present in the default auth manager, so validation fails and returns `401`.

Relevant files:

- `src/mcp/server.rs`
- `src/mcp/auth.rs`

### Impact

Authenticated HTTP MCP clients cannot use `--auth` or `MCP_HTTP_AUTH` successfully. This blocks production-like authenticated deployments even though unauthenticated local mode works.

### Recommended Fix

Construct the HTTP server auth manager from the configured token instead of generating an unrelated default token. A minimal fix should make the same configured token pass both checks and map it to an admin `AuthContext`.

Add regression coverage for:

- no auth configured: request succeeds without bearer token;
- auth configured, no bearer token: `401`;
- auth configured, wrong bearer token: `401`;
- auth configured, correct bearer token: request succeeds;
- auth configured, correct bearer token on `/mcp/stream`: SSE succeeds.

## Issue 2: Auto-Index Attempts to Reindex Directories as Files

**Severity:** Minor
**Status:** Open
**Area:** Startup auto-index

### Evidence

During no-auth server startup on port `19799`, auto-index logged:

```text
Failed to reindex /Users/linh.doan/work/harvey/freepeak/leankg/./src/config: Is a directory (os error 21)
Failed to reindex /Users/linh.doan/work/harvey/freepeak/leankg/./src/mcp: Is a directory (os error 21)
```

The server continued running and all live no-auth endpoint checks passed.

### Expected

The auto-indexer should skip directory paths or recurse through them intentionally without logging per-directory file read errors.

### Actual

The auto-indexer attempted to reindex directory paths as files and logged OS errors.

### Impact

This did not break the HTTP MCP server smoke tests, but it creates noisy startup logs and may hide real indexing failures in larger runs.

### Recommended Fix

Update the stale-change or incremental reindex path to filter directories before file-level reindex work, or route directory paths to an explicit recursive indexing path.

Add regression coverage for a changed directory entry so the auto-index path does not attempt to parse it as a file.

## Issue 3: Missing Dedicated HTTP MCP Endpoint Test Coverage

**Severity:** Medium
**Status:** Open
**Area:** Test coverage

### Evidence

The targeted MCP server unit tests passed:

```text
3 passed; 0 failed
```

However, listing tests filtered by `http` produced no matching tests:

```text
cargo test --release http -- --list
0 tests, 0 benchmarks
```

The auth regression was only found through a live server smoke test.

### Impact

Core endpoint behavior can regress without unit or integration tests catching it. The current test suite did not catch that configured bearer-token authentication is rejected.

### Recommended Fix

Add HTTP-level integration tests that start the router or server in-process and verify:

- `/health`;
- `/mcp` JSON-RPC `initialize`;
- `tools/list`;
- representative `tools/call`;
- project query routing;
- notification `204`;
- invalid JSON parse error;
- CORS preflight;
- `/mcp/stream`;
- auth success and failure cases.

These tests should run in release-compatible code paths and avoid requiring a long-running external server.

## Cleanup Verification

Both live test servers were stopped after validation.

```text
19799 closed
19800 closed
```

## Current Recommendation

Do not claim the HTTP MCP server is fully working until Issue 1 is fixed and covered by tests. The unauthenticated local HTTP MCP workflow is operational, but authenticated HTTP MCP mode is currently broken for configured tokens.
