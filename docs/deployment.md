# Deployment and security

LeanKG is self-hosted software. This document covers the deployment models,
the security model, scale-out, operations and the limits a buyer or operator
should know before adopting it. Setup steps are in
[Getting started](getting-started.md); every command is in the
[CLI reference](cli-reference.md).

## Deployment models

| Model | What it is | Notes |
| --- | --- | --- |
| Developer or CI | The `leankg` binary with a SQLite store per repository | No database server, no Docker |
| Shared team server | `leankg serve --http`, `--rest`, `--rpc` behind bearer tokens | Non-loopback binds need a token; terminate TLS in front |
| Reader instance | The same server with `--read-only` | Mutations return `LEANKG_ERROR_READ_ONLY` |
| Multi-project server | One process, `LEANKG_PROJECT_DIRS` registering several repositories | Requests select a project; selection is fail-closed |
| Shared database | PostgreSQL with pgvector (`LEANKG_DB_ENGINE=postgres`, `LEANKG_PG_URL`) | Opt-in; the default flow never touches PostgreSQL |
| Container | [`Dockerfile`](../Dockerfile) builds a read-only demo image | Not a production deployment template |
| Hosted or managed | Not provided by this repository | |

## Security and access control

| Capability | How it works | Evidence |
| --- | --- | --- |
| Role model | Viewer (read), Contributor (writes such as import and memory writes), Admin (management actions) | [`internal/auth/auth.go`](../internal/auth/auth.go) |
| Role floors per route | Every route on a gated listener declares its role at registration: `auth.Require(role, h)` or `auth.Public(h)` | [`internal/auth/route.go`](../internal/auth/route.go), [`internal/rest/rest.go`](../internal/rest/rest.go) |
| Bearer tokens | Opaque `lkg_...` secrets sent as `Authorization: Bearer`; optional expiry; revocable | [`internal/auth/tokens.go`](../internal/auth/tokens.go) |
| Project grants | `project:<name>` scopes limit a token to named projects; a withheld or unknown project gets the same refusal | [`AGENTS.md`](../AGENTS.md#auth-and-the-http-gate) |
| Admin bootstrap | `leankg auth admin init` creates one site-admin account; only the PBKDF2 hash is stored, credentials go to a 0600 env file | [`cmd/leankg/auth.go`](../cmd/leankg/auth.go) |
| Password storage | PBKDF2-HMAC-SHA256 at 600,000 iterations (standard library) | [`internal/auth/accounts.go`](../internal/auth/accounts.go) |
| Login throttling | After 5 consecutive failures for a username, `authenticate` locks that username for 60 seconds | [`internal/auth/login_throttle.go`](../internal/auth/login_throttle.go) |
| Exposure guard | A non-loopback `--http`, `--rest` or `--rpc` bind exits unless a bearer token is configured | [`cmd/leankg/main.go`](../cmd/leankg/main.go), [`internal/servebind`](../internal/servebind) |
| Read-only serving | `serve --read-only` opens the store read-only and skips migrations | [`cmd/leankg/main.go`](../cmd/leankg/main.go), [`internal/errs`](../internal/errs) |
| Secret redaction | A shared redaction helper scrubs secrets from stored memory and session-link text | [`internal/telemetry`](../internal/telemetry) |

stdio is not gated. It is a local subprocess started by the user and runs as
Admin.

## Scale and multi-repo

| Capability | How it works |
| --- | --- |
| Multi-project server | `LEANKG_PROJECT_DIRS` registers repositories; requests select one with `?project=<dir or name>` or the MCP `project` argument |
| Fail-closed selection | On a multi-project server the `project` argument is required; the server refuses to guess |
| Lazy stores | A project's store opens on first use, not at boot |
| Shared database | PostgreSQL with pgvector, schema per project, per-model HNSW index |
| Per-repository SQLite | `.leankg/leankg.db` with WAL; no server process |

## Operations

| Capability | How it works |
| --- | --- |
| Single binary | CGO-free build (`CGO_ENABLED=0`); builds `leankg` and `leankg-embed` |
| Container image | Three-stage build, unprivileged user (uid 10001), `HEALTHCHECK` on `/health` |
| Health endpoint | `GET /health`, registered as a public route with no role floor |
| Diagnostics | `leankg doctor` (`--deep`, `--format text\|json`) and `leankg status` |
| Self-update | `leankg update [--check]` follows published GitHub releases |
| Telemetry | Capture is off by default and needs explicit consent in `telemetry.yaml` |
| Embedding providers | `local` (llama.cpp sidecar, default), `openai`, `deterministic` (tests). Every writer is model-stamp guarded |

### Backups and maintenance

- The SQLite store is a single directory (`.leankg/`); back it up with
  `sqlite3 .backup` or a filesystem snapshot while the writer is stopped.
- Run `leankg gc` to drop abandoned embedding collections and
  `leankg vacuum` to reclaim space.
- After an upgrade, `leankg doctor` is the first check; `leankg status`
  reports freshness and embedding state.

### Runbook: restarting a server safely

A restart can leave a duplicate process behind, and the duplicate can be the
one answering. Find listeners, not process names:

```bash
lsof -nP -iTCP:9699 -sTCP:LISTEN
lsof -nP -iTCP:9700 -sTCP:LISTEN
```

Every line must be the same pid, and that pid must be the one your supervisor
reports. Kill by the listener's pid, confirm the port is free, then start.

## Governance and traceability

| Capability | How it works |
| --- | --- |
| Incidents and ownership | `leankg incident`, `leankg team-map`, `leankg note`; queryable with `query --action incidents` and `service_context` |
| Environment governance | `leankg env-conflicts --service NAME`; `--env` on notes and service context |
| Requirement traceability | `leankg prd` ingests a PRD; `leankg prd-trace <FEATURE_ID>` traces a requirement to code |
| Ontology | Concept and workflow catalog in [`ontology/`](../ontology); match API at `POST /api/v1/ontology/match` |
| Self-description | The MCP surface, ladder and storage are described as an importable ontology |

## Current limits

These are the gaps a buyer is likely to ask about. None is claimed as
implemented.

- **No SSO.** There is no OIDC, SAML or SCIM integration. Users are local
  accounts with bearer tokens.
- **No TLS in the server.** Listeners do not terminate TLS. Put them behind a
  TLS-terminating reverse proxy.
- **Audit ledger is not wired.** The store has a hash-chained audit ledger, and
  `leankg audit export` and `leankg audit verify` read and check it, but the
  runtime does not append events yet (`AppendAudit` is called only from tests).
- **No encryption at rest by LeanKG.** Use disk or database encryption.
- **No SIEM export, no compliance attestations.** No SOC 2, ISO 27001 or GDPR
  claim is made.
- **No hosted service.** This repository ships software only.
- **stdio is not gated.** The stdio transport runs as Admin.
- **`--ui` exposes a data API.** Bind it to loopback or front it with a proxy.
  The public demo serves it `--read-only` against a disposable baked graph.

Technical limits of the indexer and graph (call resolution, heuristic guards,
repository scope) are in [Architecture § Known limits](architecture.md#known-limits).
