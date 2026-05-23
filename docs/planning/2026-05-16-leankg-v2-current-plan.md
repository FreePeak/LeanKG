# LeanKG v2 Current Execution Plan

**Date:** 2026-05-16  
**Owner:** Product and Engineering  
**Status:** Active planning  
**Related docs:** `docs/roadmap.md`, `docs/requirement/prd-leankg.md`, `.docs/LeanKG_v2_PRD.html`

## Purpose

This plan converts the LeanKG v2 roadmap into concrete implementation work for the current Rust repository. It deliberately separates current CozoDB-backed v2 completion from the future enterprise Graphiti/Neo4j architecture described in `.docs/LeanKG_v2_PRD.html`.

## Current Codebase Facts

| Area | Evidence | Status |
| --- | --- | --- |
| Package version | `Cargo.toml` is `0.17.0` | Current product version |
| Graph store | `src/db/schema.rs` initializes CozoDB/SQLite | CozoDB is active |
| Env model | `CodeElement`, `Relationship`, and `Incident` include `env` | Partial |
| Incident model | `Incident` struct and CRUD exist | Partial |
| MCP v2 tools | `query_incidents`, `find_env_conflicts`, `get_service_context`, `semantic_search` exist | Partial |
| Token budgets | `src/mcp/token_budget.rs` defines budgets | Partial |
| Web UI v2 panels | `EnvironmentSelector`, `IncidentPanel`, `ConflictView` exist | Partial |
| Shared backend | No `leankg-server`, Graphiti, Neo4j, or `/api/v2` implementation | Not started |
| Semantic vector search | No fastembed/HNSW/Graphiti vector path in active code | Not started |

## Immediate Documentation Work

| Task | Output | Acceptance criteria |
| --- | --- | --- |
| Update roadmap | `docs/roadmap.md` | Roadmap names current, next, planned, and future work accurately. |
| Update PRD status | `docs/requirement/prd-leankg.md` | Each v2 requirement has Done, Partial, Planned, or Future status with file references. |
| Update HLD | `docs/design/hld-leankg.md` | Architecture no longer claims unimplemented HNSW, `/v2/graph`, SSE, or CI/CD webhooks as active. |
| Link current plan | `docs/README.md` | Docs index includes roadmap and this plan. |
| Reclassify HTML PRD | `.docs/LeanKG_v2_PRD.html` referenced from project docs | HTML is labeled future enterprise roadmap input, not current implementation state. |

## Sprint 1 - Stabilize v2 Local Core

**Goal:** Make existing v2 scaffolding reliable enough to build on.

| ID | Task | Files likely affected | Acceptance criteria |
| --- | --- | --- | --- |
| V2-CORE-01 | Fix CozoDB migration strategy | `src/db/schema.rs`, tests | Fresh and existing databases initialize without migration warnings; schema arity tests pass. |
| V2-CORE-02 | Add env-aware index inputs | `src/cli/mod.rs`, `src/main.rs`, `src/mcp/tools.rs`, `src/mcp/handler.rs`, `src/indexer/mod.rs` | `leankg index --env local --service-name x --version y` stores env and metadata. |
| V2-CORE-03 | Add service metadata model | `src/db/models.rs`, `src/db/schema.rs`, `src/db/mod.rs` | Service metadata can store team, on-call, repo URL, language, health endpoint, SLO, tags, and current version. |
| V2-CORE-04 | Complete service context response | `src/graph/query.rs`, `src/mcp/handler.rs` | `get_service_context` returns service, env, version, team, on_call, calls, called_by, schemas, incidents, and known risks. |
| V2-CORE-05 | Harden token budget tests | `src/mcp/token_budget.rs`, tests | v2 tool responses remain inside documented budgets and include truncation metadata when needed. |

## Sprint 2 - Incident and Knowledge Workflow

**Goal:** Turn incident storage into a useful product workflow.

| ID | Task | Files likely affected | Acceptance criteria |
| --- | --- | --- | --- |
| V2-INC-01 | Validate incident commands | `src/cli/mod.rs`, `src/main.rs`, `src/db/mod.rs` | Incident add rejects missing required fields, invalid severity, empty affected services, and invalid env. |
| V2-INC-02 | Add incident update/resolve commands | `src/cli/mod.rs`, `src/main.rs` | Users can resolve an incident and update root cause, resolution, prevention, trigger pattern, tags, and ticket. |
| V2-INC-03 | Add web incident form | `ui/src/components`, `src/web/handlers.rs`, `src/web/mod.rs` | Users can create and resolve incidents from the web UI. |
| V2-INC-04 | Link incidents to graph edges | `src/db/models.rs`, `src/db/mod.rs`, `src/graph/query.rs` | `caused_incident` and `resolved_by` edges can be created and queried. |
| V2-INC-05 | Pattern warning MVP | `src/mcp/handler.rs`, hooks | MCP responses can warn when a query matches an incident trigger pattern. |

## Sprint 3 - Shared Backend MVP

**Goal:** Add team sharing without requiring Graphiti migration.

| ID | Task | Files likely affected | Acceptance criteria |
| --- | --- | --- | --- |
| V2-SHARED-01 | Design `/api/v2` contract | `docs/design/hld-leankg.md`, `src/api/mod.rs`, `src/api/handlers.rs` | API contract covers graph push/pull, service context, incidents, env diff, and search. |
| V2-SHARED-02 | Implement header auth | `src/api/auth.rs`, `src/api/mod.rs` | `X-LeanKG-Token`, `X-LeanKG-Engineer`, and `X-LeanKG-Env` are accepted in server mode. |
| V2-SHARED-03 | Add push/pull CLI | `src/cli/mod.rs`, `src/main.rs`, `src/api` | `leankg push --env local` and `leankg pull --env production` work against an Axum server. |
| V2-SHARED-04 | Add CI workflow templates | `.github/workflows`, docs | Template indexes with env/service/version and pushes to configured remote backend. |
| V2-SHARED-05 | Add audit logging | `src/db/schema.rs`, `src/mcp/handler.rs`, `src/api` | Query log stores engineer, env, tool/path, latency, token count, and success/failure. |

## Sprint 4 - 200-Service Graph Intelligence

**Goal:** Support the service-scale workflows in the v2 PRD.

| ID | Task | Files likely affected | Acceptance criteria |
| --- | --- | --- | --- |
| V2-SCALE-01 | Normalize service nodes | `src/indexer/microservice.rs`, `src/db/models.rs`, `src/graph/query.rs` | Service nodes have stable IDs and metadata across repositories. |
| V2-SCALE-02 | Extract API and schema contracts | `src/indexer`, tests | OpenAPI, protobuf/gRPC, JSON schema, SQL table, and event contracts are indexed as typed nodes. |
| V2-SCALE-03 | Implement env diff engine | `src/graph/query.rs`, `src/mcp/tools.rs`, `src/mcp/handler.rs` | Queries identify version, config, endpoint, schema, and missing-deployment drift. |
| V2-SCALE-04 | Implement call-chain tracing | `src/graph/query.rs`, `src/mcp/tools.rs`, `src/mcp/handler.rs` | `trace_call_chain` returns shortest service paths with edge metadata. |
| V2-SCALE-05 | Implement target-type impact analysis | `src/graph/query.rs`, `src/mcp/tools.rs`, `src/mcp/handler.rs` | Impact works for service, endpoint, schema, event, config, function, and file targets. |

## Future Enterprise Track

The HTML PRD proposes Graphiti + Neo4j + FastAPI. That should remain a research track until the current CozoDB-backed shared backend is stable.

| ID | Research task | Exit criteria |
| --- | --- | --- |
| ENT-01 | Define GraphStore trait | Current CozoDB graph operations can be tested behind a backend interface. |
| ENT-02 | Build Graphiti proof of concept | A sample repo can be indexed into Graphiti and compared with CozoDB output. |
| ENT-03 | Compare operational cost | Document deployment, backup, auth, latency, and local-offline tradeoffs. |
| ENT-04 | Decision record | Explicit go/no-go for Graphiti migration based on measured evidence. |

## Verification Commands

Run these after implementation changes:

```bash
cargo fmt --all -- --check
cargo test --test v2_env_incidents_tests
cargo test --lib
cargo test
cd ui && npm run build
```

For documentation-only changes:

```bash
rg -n "Graphiti|Neo4j|fastembed|HNSW|/api/v2|trace_call_chain|get_team_map|contribute_knowledge" docs .docs
rg -n "Last updated|Current package version|Status" docs/roadmap.md docs/planning/2026-05-16-leankg-v2-current-plan.md docs/requirement/prd-leankg.md
```
