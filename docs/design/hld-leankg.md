# LeanKG v2 — High-Level Design Document

**Project:** FreePeak/LeanKG
**Version:** 2.0
**Status:** In Progress
**Date:** 2026-05-12

---

## 1. Architecture Overview

```
+-----------------------------------------------------+
|                   LeanKG Backend                    |
|                  (Axum + CozoDB)                    |
|                                                     |
|  +--------------+  +--------------+  +----------+  |
|  |  production  |  |   staging    |  |  local   |  |
|  |  namespace   |  |  namespace   |  |namespace |  |
|  +--------------+  +--------------+  +----------+  |
|                                                     |
|  +-----------------------------------------------+  |
|  |           CozoDB (Datalog engine)             |  |
|  |   HNSW vector index (fastembed-rs)            |  |
|  +-----------------------------------------------+  |
+-----------------------------------------------------+
         ^                    ^                ^
         |                   |                |
  +------+------+    +-------+------+  +------+------+
  |  MCP server |    |  REST API    |  |  Web UI     |
  |  (stdio/SSE)|    |  /v2/graph   |  |  (existing) |
  +------+------+    +-------+------+  +-------------+
         |                   |
  +------+------+    +-------+----------------------+
  |   Cursor    |    |  CI/CD webhooks              |
  |  (per-eng)  |    |  (GitHub Actions / GitLab)   |
  +-------------+    +------------------------------+
```

---

## 2. Component Design

### 2.1 Data Layer (CozoDB)

**Schema Changes for v2:**

| Table | Change | Description |
|-------|--------|-------------|
| `code_elements` | Add `env` column | Environment namespace |
| `relationships` | Add `env` column | Environment namespace |
| `incidents` | New table | Incident records |
| `service_metadata` | New table | Enhanced service info |

**Environment Filtering:**
All queries include `WHERE env = $env` or `WHERE env IN ($envs)` clause.
Default env is read from `.cursor/leankg.toml` or falls back to `local`.

### 2.2 Graph Query Engine

**New Query Types:**
- `get_service_context(service, env)` — Returns service snapshot with incidents
- `find_env_conflicts(service)` — Compares local/staging/production for a service
- `query_incidents(service, pattern, env)` — Searches incident nodes
- `impact_analysis(target, target_type, env)` — Env-aware blast radius

**Query Budgets:**
| Tool | Max Tokens | Strategy |
|------|-----------|----------|
| get_service_context | 800 | Top 5 callers/callees, 3 recent incidents |
| impact_analysis | 600 | Direct impact only; --deep adds indirect |
| query_incidents | 500 | Most recent 3 incidents, summary only |
| trace_call_chain | 400 | Shortest path only |
| find_env_conflicts | 400 | HIGH risk conflicts only by default |
| semantic_search | 300 | Top 3 matches with 1-line descriptions |

### 2.3 MCP Server

**Tool Registry:**
- Existing tools enhanced with optional `env` parameter
- New tools: `query_incidents`, `find_env_conflicts`, `contribute_knowledge`, `get_service_context`

**Authentication:**
- `X-LeanKG-Token`: shared team token
- `X-LeanKG-Engineer`: engineer identity
- `X-LeanKG-Env`: active environment

### 2.4 CLI

**New Commands:**
```
leankg incident add [OPTIONS]
leankg note add --target <SERVICE> --content <TEXT>
leankg pattern add --title <TITLE> --context <PATTERN> --solution <FIX>
leankg env conflicts --service <SERVICE>
```

---

## 3. Data Flow

### 3.1 Incident Contribution Flow

```
Engineer (Cursor or CLI)
    |
    v
+-----------+     +----------------+     +-------------+
|  CLI/API  | --> |  Incident Node | --> |  CozoDB     |
|  Input    |     |  Validation    |     |  (indexed)  |
+-----------+     +----------------+     +-------------+
                                                |
                                                v
                                         +-------------+
                                         |  MCP Query  |
                                         |  Response   |
                                         +-------------+
```

### 3.2 Environment Conflict Detection Flow

```
Query: find_env_conflicts("payment-service")
    |
    v
+---------------------------------------+
|  Fetch service in all environments    |
|  (local, staging, production)         |
+---------------------------------------+
    |
    v
+---------------------------------------+
|  Compare:                             |
|  - Schema versions                    |
|  - Config values                      |
|  - API endpoints                      |
|  - Deploy status                      |
+---------------------------------------+
    |
    v
+---------------------------------------+
|  Return conflicts with risk levels    |
|  (HIGH, MEDIUM, LOW)                  |
+---------------------------------------+
```

---

## 4. Implementation Plan

### Phase 1: Data Model & Schema
- Add `env` field to `CodeElement` and `Relationship`
- Create `Incident` struct and `ServiceMetadata` struct
- Add new `RelationshipType` variants
- Update database schema and migrations

### Phase 2: Graph Engine
- Implement env-scoped query variants
- Implement incident CRUD operations
- Implement conflict detection logic

### Phase 3: MCP Tools
- Add new tool definitions to `src/mcp/tools.rs`
- Add handlers to `src/mcp/handler.rs`
- Implement token budget enforcement

### Phase 4: CLI Commands
- Add new CLI subcommands to `src/cli/mod.rs`
- Implement command handlers in `src/cli/`

### Phase 5: Integration & Testing
- End-to-end tests for incident workflow
- Environment conflict detection tests
- Token budget compliance tests

---

## 5. Interface Specifications

### 5.1 MCP Tool: query_incidents

```json
// Input
{
  "service": "payment-service",
  "pattern": "redis timeout",
  "env": "production",
  "limit": 5
}

// Output
{
  "incidents": [
    {
      "id": "INC-2025-047",
      "title": "Redis connection pool exhaustion",
      "severity": "P1",
      "root_cause": "Max connections set to 10, peak demand was 200",
      "resolution": "Increased pool to 100, added circuit breaker",
      "prevention": "Always set pool_size = expected_peak * 1.5",
      "occurred_at": "2025-03-14T14:22:00Z",
      "author": "linh"
    }
  ]
}
```

### 5.2 MCP Tool: find_env_conflicts

```json
// Input
{
  "service": "payment-service"
}

// Output
{
  "conflicts": [
    {
      "type": "schema_version",
      "detail": "ChargeRecord is v1.5 in production, v1.4 in staging",
      "risk": "HIGH"
    },
    {
      "type": "config_drift",
      "detail": "REDIS_MAX_CONNECTIONS=100 in production, 10 in staging",
      "risk": "MEDIUM"
    }
  ]
}
```

### 5.3 CLI: incident add

```bash
leankg incident add \
  --title "Redis pool exhaustion under load" \
  --severity P1 \
  --affected payment-service \
  --root-cause "Pool size too small for peak concurrency" \
  --resolution "Increased pool_size, added circuit breaker" \
  --prevention "Set pool_size = peak_rps * 0.5 minimum" \
  --env production \
  --ticket INC-2025-047
```

---

## 6. Risk & Mitigation

| Risk | Impact | Mitigation |
|------|--------|------------|
| Schema migration breaks v1 data | High | Default `env` to `local` for all existing data |
| Token budgets too restrictive | Medium | Make budgets configurable in `.cursor/leankg.toml` |
| Performance with 200 services | High | Add query result caching, pagination |
| Concurrent writes to shared backend | Medium | Use CozoDB transactions, implement optimistic locking |

---

*Last updated: 2026-05-12*
