# LeanKG v2 — Product Requirements Document
## From Local-First Tool to Team Knowledge Infrastructure

**Project:** FreePeak/LeanKG
**Version:** 2.0
**Status:** In Progress
**Target:** 200-service microservice system, multi-engineer team, Cursor-native

---

## 1. Vision

LeanKG v1 is a local-first knowledge graph that gives a single developer accurate codebase context in Cursor. It works well for individual repos and small projects.

LeanKG v2 evolves into the **shared knowledge backbone** for an engineering team operating 200 microservices across production, staging, and local environments. Every developer gets the same accurate, up-to-date context. Every incident becomes permanent knowledge. Every release automatically updates the graph. One MCP query returns concise, environment-aware, token-efficient context.

---

## 2. Current State (v1 Baseline)

| Capability | v1 Status |
|---|---|
| Single-repo indexing | ✅ |
| Dependency graph (IMPORTS, CALLS, TESTED_BY) | ✅ |
| Impact radius CLI | ✅ |
| MCP server (stdio) | ✅ |
| Cursor / Claude Code / OpenCode auto-trigger | ✅ |
| Web UI (force-directed graph) | ✅ |
| Obsidian vault sync | ✅ |
| Multi-repo / shared backend | ❌ |
| Environment namespacing (prod/staging/local) | ❌ |
| Incident knowledge layer | ❌ |
| Team contribution workflow | ❌ |
| CI/CD auto-update on release | ❌ |
| 200-service scale query engine | ❌ |

---

## 3. Problems to Solve

### P1 — Engineer context at 200-service scale
A developer opens `payment-service` in Cursor. They need to know: what this service calls, what calls it, what schemas it owns, what events it publishes, which team maintains it, and what known issues exist in production. Today they piece this together from Confluence, Slack, and README files. This takes 30–60 minutes per unfamiliar service and produces stale, incomplete context.

**Goal:** One MCP query returns a complete, concise, live snapshot of any service's position in the system. Cursor uses this without any extra engineer action.

### P2 — Shared knowledge, not per-developer silos
v1 runs locally, meaning each engineer builds their own graph from their own repos. When engineer A fixes a bug and documents the pattern, engineer B and Cursor never know about it.

**Goal:** A central LeanKG backend that every team member reads from and writes to. One source of truth for the entire 200-service estate.

### P3 — Incidents become permanent, actionable knowledge
When an engineer resolves a production incident, the root cause, fix, affected services, and prevention pattern exist briefly in their brain and an incident ticket. Two months later, Cursor suggests the same broken pattern again.

**Goal:** A structured incident knowledge layer in the graph. Engineers contribute incident nodes after resolution. Cursor queries this layer before suggesting any pattern that matches a known failure.

### P4 — Environment conflicts slow down the team
Local development, staging integration, and production have different service versions, configurations, and behaviors. Engineers waste time debugging issues caused by environment mismatch.

**Goal:** The graph is partitioned into `local`, `staging`, and `production` namespaces. Queries are environment-scoped by default. Conflicts between environments are surfaced proactively.

---

## 4. User Stories

### As a software engineer using Cursor:

- When I open any service file, I get an automatic context snapshot: dependencies, owners, known incidents, environment-specific config.
- When I write code that calls a deprecated API, Cursor tells me immediately, cites the replacement, and references the incident where the old API caused an outage.
- When I add a new field to a shared schema, Cursor tells me which of the 200 services will break and need updates before I push.
- When I search for "how does the refund flow work", I get a traced call chain across services with known failure points annotated.
- When I debug a production incident, I query the graph for "similar past incidents in payment-service" and get structured resolution patterns.

### As a team member contributing knowledge:

- After resolving an incident, I run `leankg incident add` to document the root cause, fix, affected services, and prevention. This is stored in the graph and immediately available to all teammates and Cursor.
- After releasing a new feature, CI/CD calls the LeanKG API to update the graph with new service relationships, schema changes, and endpoint additions automatically.
- I can annotate any service, endpoint, or schema with team-specific notes that persist and are query-accessible.

### As a team lead:

- I can see which services have no incidents documented (knowledge gaps).
- I can see which services are most queried by the team, indicating high complexity or poor documentation.
- I can audit what context Cursor is using when generating code for my team.

---

## 5. Functional Requirements

### FR-01: Environment Namespacing
- Every node and edge in the graph must carry an `env` field: `local | staging | production`.
- Queries default to the engineer's active environment.
- Cross-environment queries are opt-in.

### FR-02: Incident Data Model
- Support `Incident` nodes with fields: id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected services, trigger_pattern, prevention, tags, author, linked_ticket.
- Support relationship types: `caused_incident`, `resolved_by`, `conflicts_with`, `deployed_to`, `supercedes`.

### FR-03: Service Context Enhancement
- Enhanced Service nodes with: version, deploy_env, slo_p99_ms, health_endpoint, on_call, incident_count, last_incident, tags.

### FR-04: MCP Tools v2
- `get_service_context` — Enhanced with env scoping + incident summary (max 800 tokens).
- `impact_analysis` — Env-aware, shows cross-environment divergence (max 600 tokens).
- `trace_call_chain` — Trace request path across services (max 400 tokens).
- `query_incidents` — Find past incidents matching a pattern or service (max 500 tokens).
- `find_env_conflicts` — Surface mismatches between environments (max 400 tokens).
- `contribute_knowledge` — Team members add knowledge entries via Cursor.
- `get_team_map` — Returns ownership + on-call for any set of services.
- `semantic_search` — Natural language to graph nodes (max 300 tokens).

### FR-05: CLI Commands
- `leankg incident add` — Add an incident after resolution.
- `leankg note add` — Add a team note to a service.
- `leankg pattern add` — Annotate a known risky pattern.
- `leankg env conflicts` — Show environment conflicts for a service.

### FR-06: CI/CD Integration
- GitHub Actions workflow to update LeanKG on release.
- Source commit to LeanKG updated: < 3 minutes.
- Graph query reflects new data: immediate.

### FR-07: Token Budget Enforcement
- Every MCP tool response must enforce a maximum token budget.
- Responses exceeding the budget are truncated with a "...truncated" indicator.
- Budgets are configurable per-tool in `.cursor/leankg.toml`.

| Tool | Max Token Budget | Strategy |
|------|-----------------|---------|
| `get_service_context` | 800 | Top 5 callers/callees, 3 recent incidents |
| `impact_analysis` | 600 | Direct impact only by default |
| `query_incidents` | 500 | Most recent 3 incidents, summary only |
| `trace_call_chain` | 400 | Shortest path only |
| `find_env_conflicts` | 400 | HIGH risk conflicts only by default |
| `semantic_search` | 300 | Top 3 matches with 1-line descriptions |

### FR-08: 3-Tier Retrieval
- For every query, LeanKG tries retrieval in order, stopping at the first tier that returns results:
  1. **Exact match** — name/ID match in CozoDB relations (< 2ms)
  2. **Fuzzy match** — trigram index on names (< 5ms)
  3. **Semantic embed** — keyword fallback on natural language queries (< 20ms)

### FR-09: Team Map MCP Tool
- `get_team_map` — Returns ownership + on-call contacts for any set of services.
- Input: `{ "services": ["payment-service", "ledger-service"] }`
- Output: `{ "teams": [{ "service": "...", "team": "...", "on_call": "..." }] }`

---

## 6. Data Model v2

### 6.1 Environment Namespace

Every node and edge carries an `env` field: `local | staging | production`.

### 6.2 Incident Node

```
Incident {
  id:            uuid
  env:           "production" | "staging"
  title:         string
  severity:      P0 | P1 | P2 | P3
  occurred_at:   timestamp
  resolved_at:   timestamp
  root_cause:    string
  resolution:    string
  affected:      [ServiceId]
  trigger_pattern: string
  prevention:    string
  tags:          [string]
  author:        string
  linked_ticket: string
}
```

### 6.3 New Edges for v2

```
:caused_incident  (from: Service | Schema | Config, to: Incident)
:resolved_by      (from: Incident, to: KnowledgeEntry)
:conflicts_with   (from: Node{env:local}, to: Node{env:staging})
:deployed_to      (from: Service, to: Environment, at: timestamp, version: string)
:supercedes       (from: APIEndpoint, to: APIEndpoint)
```

### 6.4 Updated Service Node

```
Service {
  id, name, lang, team, repo_url,
  env:            "production" | "staging" | "local"
  version:        string
  deploy_env:     ["production", "staging"]
  slo_p99_ms:     int
  health_endpoint: string
  on_call:        string
  incident_count: int
  last_incident:  timestamp
  tags:           [string]
}
```

---

## 7. Success Metrics

| Metric | Target |
|---|---|
| Time to get full service context in Cursor | < 5 seconds |
| Token usage per Cursor session (context queries) | < 2,000 tokens per session |
| Incident recurrence rate | -50% within 6 months |
| Graph freshness (production namespace) | < 3 minutes from commit |
| Environment conflict detection before staging push | > 80% of schema conflicts caught |
| Team knowledge contributions per week | >= 1 incident/note per engineer |

---

## 8. Changelog

- v2.0 - Environment Namespacing & Incident Knowledge Layer
  - FR-01 to FR-09: New functional requirements for v2
  - Environment namespacing (production, staging, local)
  - Incident data model and knowledge workflow
  - Enhanced MCP tools with token budgets
  - CLI commands for incident and knowledge management
  - 3-tier retrieval (exact → fuzzy → semantic)
  - Token budget enforcement on all MCP tools
  - CI/CD GitHub Actions workflow template
  - Team map and ownership lookup

---

*Last updated: 2026-05-12*
