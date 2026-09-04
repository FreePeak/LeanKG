# Ontology Arity Mismatch Fix (2026-06-26)

## Summary

Three MCP ontology tools (`kg_concept_map`, `kg_context`, `kg_trace_workflow`) were failing with `MCP error -32603: Arity mismatch for rule application code_elements` whenever a real database was attached. `kg_ontology_status` worked (it never queried `code_elements`).

Root cause: the canonical `code_elements` schema in CozoDB has 13 columns (the 12th and 13th being `env` and `ontology_layer`, both added in the schema-repair migration), but five Cozo Datalog queries inside `src/ontology/query.rs` were binding only 12 columns. They pre-dated the `ontology_layer` field.

## Evidence

### Live diagnostic (Docker container, `leankg-leankg-1`, after `docker restart`)

Three arity-probe raw queries against the running MCP server returned:

| Probe binding | Result |
|---|---|
| `*code_elements[..., env, ontology_layer]` (13 cols) | OK — schema **has 13 columns** |
| `*code_elements[..., env]` (12 cols) | `Arity mismatch for rule application code_elements` |
| `*code_elements[...]` (11 cols) | `Arity mismatch for rule application code_elements` |

### Failing query sites (all in `src/ontology/query.rs`)

| Line | Function | Invocation path |
|---|---|---|
| 89 | `search_ontology_nodes` | `kg_concept_map`, `kg_context` |
| 364 | `find_element_by_qualified` | `expand_ontology_context` (called from `kg_concept_map`, `kg_context`) |
| 419 | (workflow-step lookup) | `trace_workflow` → `kg_trace_workflow` |
| 462 | `search_workflows` | `kg_context`, `kg_trace_workflow` |
| 530 | (all-ontology-nodes lookup) | `get_ontology_status` |

### Canonical 13-column pattern that does work

`src/graph/query.rs:17` — `const CODE_ELEMENTS_13_TAIL: &str = ", env, ontology_layer";`
`src/db/mod.rs:1279, 1284` — bindings include `, env, ontology_layer`
`src/db/schema.rs:414, 421` — `:replace` repair scripts include both fields

## Fix

Appended `, ontology_layer` to all five `*code_elements[...]` bindings in `src/ontology/query.rs`. No call-site changes required — the rule head bindings don't reference `ontology_layer`, so adding it as an unused trailing positional binding is safe.

## Regression test

`tests/integration.rs::test_ontology_queries_support_13_column_code_elements_schema`

Creates a DB with the canonical 13-column `code_elements` schema, seeds one `workflow`, two `workflow_step`s, and one `domain_entity` (all under the `ontology://` URL scheme that the engine filters on), then asserts:

- `search_ontology_nodes("checkout", ...)` returns the workflow
- `search_ontology_nodes("cart", ...)` returns the domain entity
- `search_workflows("checkout", ...)` returns the workflow
- `get_ontology_context("checkout", ...)` succeeds and matches nodes
- `trace_workflow("checkout", ...)` returns the two ordered steps
- `get_ontology_status()` succeeds

Each call uses the engine method that previously failed at the Datalog layer. The test fails fast with the old 12-column binding; it passes with the 13-column binding.

## Validation

```
$ cargo test --release --test integration
test result: ok. 25 passed; 0 failed

$ cargo test --release --lib ontology
test result: ok. 30 passed; 0 failed
```

## Followups (not in this change)

1. `src/ontology/query.rs` still has *another* arity bug: `expand_ontology_context` (`query.rs:172`) uses `*relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _]` — the `_` wildcard happens to make it work, but the same query was almost certainly written when `relationships` had 5 columns. Migrate to `relationships` 6-column pattern when convenient.
2. `search_ontology_nodes` reads `ontology_layer` from the JSON `metadata` column rather than the dedicated `ontology_layer` column. Reading from the column would be cheaper and avoid drift.
3. The MCP server has no startup self-test that would have caught this before exposing the broken tools to clients. Tracked under follow-up work in `docs/implementation/mcp-ontology-self-test-plan.md` (to be written).