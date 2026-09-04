# LeanKG Ontology Semantic Search MVP

Date: 2026-05-17
Status: Draft
Scope: Local-first MVP for trial use with Cursor, Claude Code, and other MCP-enabled agents

## Overview

LeanKG v2 needs two ontology layers to make agentic semantic search useful beyond code symbol lookup:

- Concept Ontology: the "what" layer, describing domain entities, services, APIs, data stores, environments, known issues, and knowledge artifacts.
- Procedural Ontology: the "how" layer, describing workflows, ordered steps, decision points, failure modes, and playbooks.

The full v2 PRD describes these ontologies as part of a larger platform with shared services, temporal history, Graphiti-style evolution, hybrid search, and operational knowledge. This MVP intentionally avoids that full platform scope. The goal is to run a useful local trial quickly by layering ontology nodes and relationships on top of the current LeanKG graph model.

## Problem Statement

Current LeanKG search is strongest when the user asks structural code questions:

- where is a function
- what imports this file
- what calls this handler
- what tests cover this module
- what is the impact radius of this file

Agent tools often ask broader semantic questions:

- where is checkout refund logic
- what happens after payment capture fails
- what services own subscription renewal
- what code, tests, docs, and incidents are related to tenant provisioning
- explain the end-to-end workflow before editing this feature

Without ontology nodes, the agent must infer domain meaning from filenames, symbols, comments, and docs. That makes search brittle and loses the "why" and "how" around the code.

## MVP Goal

Implement the smallest local ontology layer that lets LeanKG answer semantic agent questions by expanding natural-language queries into related graph context.

The MVP should let an agent ask:

```text
kg_context("refund checkout failure", env="local", depth=2)
```

and receive compact context containing:

- matched concept nodes
- related services, APIs, files, functions, tests, docs
- related workflows and ordered steps
- known issues or playbooks if available
- confidence and match explanation

## Non-Goals

This MVP does not include:

- Graphiti migration
- full bi-temporal history
- embeddings or vector database as a hard dependency
- shared organization-wide LeanKG server
- automatic LLM-based ontology extraction
- production/staging/local synchronization
- UI redesign
- complete PRD v2 coverage

Those can be added after the local trial proves the search flow.

## Existing Foundation

The current codebase already has enough primitives for a fast MVP:

- `CodeElement` has `qualified_name`, `element_type`, `metadata`, and `env`.
- `Relationship` has `source_qualified`, `target_qualified`, `rel_type`, `metadata`, and `env`.
- `RelationshipType` already includes several useful relationships such as `entry_point_of`, `step_in_process`, `caused_incident`, `resolved_by`, `conflicts_with`, `deployed_to`, and `supersedes`.
- `process_processor.rs` already detects process-like traces from call relationships and creates `process` nodes.

The MVP should extend these primitives instead of redesigning storage.

## Concept Ontology MVP

Concept ontology nodes are stored as normal `CodeElement` records with ontology-specific `element_type` values and structured `metadata`.

### Element Types

Initial concept element types:

| Element Type | Purpose |
| --- | --- |
| `domain_entity` | Product or business concept such as Refund, Order, Subscription |
| `service` | Runtime or logical service boundary |
| `api_endpoint` | HTTP, gRPC, CLI, or MCP endpoint |
| `data_store` | Database, table, collection, bucket, queue, topic |
| `environment` | Local, staging, production, or engineer-local namespace |
| `known_issue` | Recurring bug, incident pattern, or operational failure |
| `playbook` | Troubleshooting or recovery procedure |
| `team_knowledge` | Human-authored note, design decision, or operational context |

### Metadata Contract

Each concept node should use metadata like:

```json
{
  "gid": "local:checkout-service:domain_entity:Refund:v1",
  "ontology": "concept",
  "ontology_layer": "domain",
  "aliases": ["refund", "reversal", "chargeback"],
  "description": "Money returned to a customer after payment capture",
  "source": "docs/prd.md",
  "valid_from": "2026-05-17T00:00:00Z",
  "valid_until": null
}
```

For MVP, `valid_from` and `valid_until` are metadata only. They do not require temporal indexing yet.

### Concept Relationships

Initial relationship types can be represented as strings first, then added to `RelationshipType` after the flow stabilizes:

| Relationship | Meaning |
| --- | --- |
| `owns_concept` | service/team owns a domain concept |
| `implements_concept` | file/function/API implements a domain concept |
| `exposes_endpoint` | service exposes an API endpoint |
| `reads_from` | code or service reads from a data store |
| `writes_to` | code or service writes to a data store |
| `documents_concept` | doc explains a concept |
| `has_known_issue` | concept/service/workflow has a known issue |
| `resolved_by_playbook` | known issue is handled by a playbook |

## Procedural Ontology MVP

Procedural ontology nodes describe workflows and execution meaning.

### Element Types

Initial procedural element types:

| Element Type | Purpose |
| --- | --- |
| `workflow` | End-to-end business or technical process |
| `workflow_step` | A single ordered step in a workflow |
| `decision_point` | Branch condition in a workflow |
| `failure_mode` | Known failure path or operational hazard |
| `playbook_step` | Step in a recovery or troubleshooting playbook |

### Metadata Contract

Example workflow node:

```json
{
  "gid": "local:checkout-service:workflow:checkout:v1",
  "ontology": "procedural",
  "aliases": ["checkout flow", "place order", "purchase"],
  "description": "End-to-end customer checkout workflow",
  "entry_points": ["src/checkout/handler.rs::create_order"],
  "step_count": 5,
  "source": "detected_from_call_graph"
}
```

Example workflow step node:

```json
{
  "gid": "local:checkout-service:workflow_step:authorize_payment:v1",
  "ontology": "procedural",
  "workflow_gid": "local:checkout-service:workflow:checkout:v1",
  "order": 2,
  "code_refs": ["src/payment/client.rs::authorize"],
  "failure_modes": ["payment_timeout", "insufficient_funds"]
}
```

### Procedural Relationships

Initial relationship types:

| Relationship | Meaning |
| --- | --- |
| `has_step` | workflow contains workflow step |
| `next_step` | step order in the workflow |
| `branches_to` | decision point branches to another step |
| `implemented_by` | workflow or step is implemented by code |
| `has_failure_mode` | step or workflow has a known failure |
| `handled_by_playbook` | failure mode is handled by a playbook |
| `entry_point_of` | existing relationship, code is entry point of process/workflow |
| `step_in_process` | existing relationship, code participates in process trace |

## Agentic Semantic Search Flow

The core MVP behavior is query expansion:

1. Agent sends a natural-language query through MCP.
2. LeanKG searches ontology nodes by name, alias, description, metadata, and linked docs.
3. LeanKG expands matched nodes through ontology relationships.
4. LeanKG adds linked code elements, tests, docs, incidents, and workflows.
5. LeanKG compresses the result into an agent-friendly response.

Example:

```text
User: Where is checkout refund failure handled?
Agent: kg_context("checkout refund failure", env="local", depth=2)
```

LeanKG should resolve:

```text
refund -> domain_entity:Refund
checkout -> workflow:Checkout
failure -> failure_mode or known_issue nodes
```

Then expand to:

```text
domain_entity:Refund
workflow:Checkout
workflow_step:AuthorizePayment
failure_mode:PaymentTimeout
playbook:PaymentReconciliation
service:checkout-service
api_endpoint:POST /checkout
code refs
tests
docs
```

## MVP MCP Tools

### `kg_context(query, env, depth)`

Primary semantic search tool for agents.

Returns:

- matched ontology nodes
- expanded code context
- workflows
- docs
- tests
- confidence and match reasons

### `kg_concept_map(query, env)`

Returns a compact concept neighborhood for a domain, service, or feature.

Useful for:

- feature onboarding
- impact analysis before edits
- understanding ownership boundaries

### `kg_trace_workflow(workflow_id_or_query, env)`

Returns an ordered procedural trace.

Useful for:

- debugging a user flow
- understanding what code runs before/after a step
- identifying missing tests or failure handling

### `kg_ontology_status()`

Returns counts and coverage:

- concept nodes by type
- procedural nodes by type
- ontology relationships by type
- nodes missing aliases
- nodes missing code links
- workflows without failure modes

## Local Trial UX

The local trial should support both CLI and MCP usage.

CLI examples:

```bash
cargo run -- ontology status
cargo run -- ontology context "checkout refund failure" --env local --depth 2
cargo run -- ontology concept-map "refund" --env local
cargo run -- ontology trace-workflow "checkout" --env local
```

MCP examples:

```json
{
  "tool": "kg_context",
  "arguments": {
    "query": "checkout refund failure",
    "env": "local",
    "depth": 2
  }
}
```

## Updating the Ontologies

The MVP must make ontology updates easy without requiring Rust code changes or direct database edits.

Use a declarative source folder:

```text
ontology/
  concepts.yaml
  workflows.yaml
  aliases.yaml
  playbooks/
    payment-timeout.md
```

Then provide local commands:

```bash
cargo run -- ontology validate
cargo run -- ontology sync
cargo run -- ontology status
```

### Update Flow

The update flow is:

```text
YAML/Markdown/docs/code heuristics -> ontology loader -> CodeElement + Relationship records -> MCP tools
```

`ontology sync` should:

1. Read ontology YAML and Markdown files.
2. Validate required fields and relationship targets.
3. Generate stable GIDs.
4. Upsert ontology nodes into existing graph storage.
5. Upsert ontology relationships into existing graph storage.
6. Mark missing generated nodes as stale instead of deleting them.
7. Print a coverage report.

### Concept Source Example

```yaml
concepts:
  - id: refund
    type: domain_entity
    name: Refund
    env: local
    aliases:
      - reversal
      - chargeback
      - money back
    description: Money returned to a customer after payment capture.
    owned_by:
      - checkout-service
      - payment-service
    code_refs:
      - src/refund/handler.rs
    docs:
      - docs/refund.md
```

Expected graph output:

```text
domain_entity:Refund
service:checkout-service -> owns_concept -> domain_entity:Refund
src/refund/handler.rs -> implements_concept -> domain_entity:Refund
docs/refund.md -> documents_concept -> domain_entity:Refund
```

### Workflow Source Example

```yaml
workflows:
  - id: checkout
    name: Checkout
    env: local
    aliases:
      - place order
      - purchase flow
    entry_points:
      - src/checkout/handler.rs::create_order
    steps:
      - id: create_order
        name: Create Order
        code_refs:
          - src/order/service.rs::create
      - id: authorize_payment
        name: Authorize Payment
        code_refs:
          - src/payment/client.rs::authorize
        failure_modes:
          - payment_timeout
      - id: confirm_order
        name: Confirm Order
        code_refs:
          - src/order/service.rs::confirm
```

Expected graph output:

```text
workflow:Checkout
workflow:Checkout -> has_step -> workflow_step:CreateOrder
workflow_step:CreateOrder -> next_step -> workflow_step:AuthorizePayment
workflow_step:AuthorizePayment -> has_failure_mode -> failure_mode:PaymentTimeout
workflow_step:AuthorizePayment -> implemented_by -> src/payment/client.rs::authorize
```

### Source Priority

Use three ontology update sources in this order:

| Priority | Source | Purpose |
| --- | --- | --- |
| 1 | Manual ontology YAML/Markdown | Highest quality, human-curated ontology facts |
| 2 | Existing docs | PRDs, ERDs, playbooks, incident notes |
| 3 | Code heuristics | Services, routes, call traces, database access |

When multiple sources describe the same entity, merge them by stable GID. Manual ontology files win on conflicting names, aliases, and descriptions.

### Stable GID Rule

Every ontology node needs a stable ID:

```text
{env}:{scope}:{type}:{id}:v1
```

Examples:

```text
local:checkout-service:domain_entity:refund:v1
local:checkout-service:workflow:checkout:v1
local:checkout-service:failure_mode:payment_timeout:v1
```

Stable GIDs make `ontology sync` idempotent. Running sync repeatedly should update existing nodes instead of creating duplicates.

### Stale Node Handling

The MVP should avoid hard deletes. If a generated ontology node disappears from its source, mark it stale:

```json
{
  "stale": true,
  "stale_reason": "missing_from_source",
  "last_seen_at": "2026-05-17T12:00:00Z"
}
```

Manual cleanup can come later after the local trial stabilizes.

### Sync Report

After every sync, print a compact report:

```text
Ontology sync complete
Concept nodes: 42
Workflow nodes: 7
Workflow steps: 38
Aliases: 96
Code refs resolved: 31/36
Unresolved code refs: 5
Stale nodes: 3
```

This report is part of the MVP because ontology maintenance must be visible and easy to debug.

## Implementation Plan

### Phase 1: Schema Constants and Helpers

Add:

- `src/ontology/mod.rs`
- `src/ontology/concept.rs`
- `src/ontology/procedural.rs`

Responsibilities:

- define ontology element type constants
- define relationship type constants
- generate stable local GIDs
- normalize aliases
- validate required metadata

No database migration required in this phase.

### Phase 2: Indexer Integration

Extend indexing to create concept nodes from:

- manual `ontology/concepts.yaml`
- manual `ontology/workflows.yaml`
- detected services
- API routes
- database access patterns
- docs headings and requirement sections
- known issue and playbook docs if present

Extend process detection to create procedural nodes:

- upgrade `process` to `workflow`
- create `workflow_step` nodes from ordered traces
- preserve current `entry_point_of` and `step_in_process` links

### Phase 3: Query Engine

Add ontology query methods:

- search ontology nodes by name, alias, description
- expand ontology neighborhood by relationship type
- attach related code/files/tests/docs
- produce compact JSON suitable for agents

### Phase 4: MCP and CLI

Expose:

- `kg_context`
- `kg_concept_map`
- `kg_trace_workflow`
- `kg_ontology_status`

Add CLI equivalents for local testing:

- `ontology validate`
- `ontology sync`
- `ontology status`
- `ontology context`
- `ontology concept-map`
- `ontology trace-workflow`

### Phase 5: Tests

Add focused tests:

- concept node metadata validation
- GID generation
- alias matching
- ontology YAML parsing
- idempotent ontology sync
- stale node marking
- workflow trace creation
- `kg_context` expansion includes code and docs
- `kg_trace_workflow` preserves step order

## Acceptance Criteria

- A local LeanKG index contains at least one concept node and one procedural workflow node.
- `kg_context("some domain term", "local", 2)` returns ontology nodes plus linked code context.
- `kg_trace_workflow("some workflow", "local")` returns ordered steps.
- Existing code search, dependency, impact, and MCP tools continue to work.
- Ontology data is stored in existing graph records and does not require a new database backend.
- The MVP can run with `cargo run` on a local repository.
- Ontology updates can be made by editing YAML/Markdown files and running `cargo run -- ontology sync`.
- Re-running `ontology sync` is idempotent and does not duplicate nodes.
- Missing generated nodes are marked stale rather than hard-deleted.

## Risks

| Risk | Mitigation |
| --- | --- |
| Ontology extraction is noisy | Start with deterministic rules and aliases, not LLM extraction |
| Too many weak relationships | Include confidence and match reasons |
| Agents receive too much context | Return compact summaries with depth limits |
| Full PRD expectations creep into MVP | Keep Graphiti, embeddings, and shared server out of local trial |
| Existing UI does not display ontology well | Trial through CLI/MCP first |

## Future Enhancements

After the MVP proves useful:

- add embeddings for semantic alias matching
- add Graphiti-style temporal history
- add LLM-assisted ontology extraction
- add UI views for concept maps and workflow traces
- add shared server mode for team-wide knowledge
- add environment diff support across local, staging, and production

## Summary

The fastest path is not to build a separate ontology platform. The fastest path is to make ontology a first-class interpretation layer over existing LeanKG graph records.

For local MVP:

- store concept and procedural nodes as `CodeElement`
- store semantic links as `Relationship`
- store ontology details in `metadata`
- expose agent-facing MCP tools that expand natural-language queries into ontology-aware code context

This gives Cursor, Claude Code, and other agents a semantic map of the codebase without waiting for the full v2 platform.
