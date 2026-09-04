# `run_raw_query` Recipes (FR-B50)

> **FR-B50 (Should Have):** ≥ 10 validated `run_raw_query` recipes.
> **Status:** 14 recipes, every one executed against the live Docker MCP
> (`project=/workspace`, RocksDB backend) on 2026-08-02 and verified to
> return rows or a count. Machine-checked copy of the fixture list lives in
> `scripts/mcp-smoke-tools.py` (`RAW_QUERY_RECIPES`) and is gated by the
> FR-B50 smoke gate.

`run_raw_query` executes **legacy Datalog-style scripts** — translated to SQL by
the runtime translator — directly against the resolved
project's relations. It is a read tool; the read-write filter
(`src/db/schema.rs` — `:put` / `:rm` / `:create` / `:replace` / `PRAGMA` are
rejected) keeps it safe for agents.

## Syntax essentials

- Query starts with `?` (read) or `:` (system op, e.g. `::relations`).
- `*relation{col: val}` is the *named* pattern — bind only the columns you
  need. `*relation[col1, col2, …]` is the *positional* pattern — every column
  of the relation must be bound in the body.
- Aggregates (`count`, `sum`, `min`, `max`) go in the rule head:
  `?[count(qualified_name)] := *code_elements{qualified_name}`.
- Regex filtering: `regex_matches(col, "pattern")`.
- Negation: `not *relation[col, …]` (must be fully bound, positional).
- Limits: `:limit N` at the end of the query.

## Relations

The canonical schema (source: `src/db/schema.rs` `:create` statements):

| Relation | Columns |
|----------|---------|
| `code_elements` | `qualified_name`, `element_type`, `name`, `file_path`, `line_start`, `line_end`, `language`, `parent_qualified?`, `cluster_id?`, `cluster_label?`, `metadata`, `env`, `ontology_layer` |
| `relationships` | `source_qualified`, `target_qualified`, `rel_type`, `confidence`, `metadata`, `env` |
| `knowledge_entries` | `id`, `knowledge_type`, `title`, `content`, `element_qualified?`, `user_story_id?`, `feature_id?`, `tags`, `environment`, `branch?`, `author`, `created_at`, `updated_at` |
| `embedding_vectors` | `qualified_name => vector: <F32; 384>` (HNSW-backed) |
| `incidents` | `id`, `env`, `title`, `severity`, `occurred_at`, `resolved_at?`, `root_cause`, `resolution`, `affected_services`, `trigger_pattern?`, `prevention?`, `tags`, `author`, `linked_ticket?` |
| `business_logic` | `element_qualified`, `description`, `user_story_id?`, `feature_id?` |
| `service_metadata`, `teams`, `team_invites`, `context_metrics`, `query_cache`, `feature_workflow_links`, `migrations`, `embedding_state` | see `src/db/schema.rs` |

## Recipes

Call shape (HTTP MCP):

```bash
curl -s -X POST http://localhost:9699/mcp -H "Content-Type: application/json" \
  --data-raw '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{
    "name":"run_raw_query","arguments":{
      "project":"/workspace",
      "query":"?[count(qualified_name)] := *code_elements{qualified_name}"
    }}}'
```

| # | Name | Query | Use case |
|---|------|-------|----------|
| 1 | `count_elements` | `?[count(qualified_name)] := *code_elements{qualified_name}` | Total graph size (matches `mcp_status include_counts`). |
| 2 | `count_relationships` | `?[count(source_qualified)] := *relationships{source_qualified}` | Total edge count. |
| 3 | `by_language` | `?[language, count(qualified_name)] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 15` | Language distribution — which stack dominates the repo. |
| 4 | `by_element_type` | `?[element_type, count(qualified_name)] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 20` | Element mix (File / function / class / workflow / doc…). |
| 5 | `calls_edges` | `?[source_qualified, target_qualified] := *relationships{source_qualified, target_qualified, rel_type}, rel_type = "calls" :limit 5` | Sample `calls` edges — see also `resolution_method` in `metadata`. |
| 6 | `imports_edges` | `?[source_qualified, target_qualified] := *relationships{source_qualified, target_qualified, rel_type}, rel_type = "imports" :limit 5` | Module import graph sampling. |
| 7 | `tested_by_edges` | `?[count(source_qualified)] := *relationships{source_qualified, rel_type}, rel_type = "tested_by"` | Test coverage edge count. |
| 8 | `docs_elements` | `?[qualified_name, name, file_path] := *code_elements{qualified_name, name, file_path, language}, language = "markdown" :limit 5` | Indexed markdown docs (after `mcp_index_docs`). |
| 9 | `ontology_nodes` | `?[count(qualified_name)] := *code_elements{qualified_name, file_path}, regex_matches(file_path, "ontology://")` | Procedural/domain ontology nodes synced from YAML. |
| 10 | `knowledge_count` | `?[count(id)] := *knowledge_entries{id}` | Agent-memory knowledge entries. |
| 11 | `vector_count` | `?[count(qualified_name)] := *embedding_vectors{qualified_name, vector}` | Embedded function/doc vectors (HNSW ANN readiness). |
| 12 | `orphan_elements` | `?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], not *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 5` | Elements with no edges — candidates for `find_dead_code`. |
| 13 | `longest_functions` | `?[qualified_name, name, line_end, lines] := *code_elements{qualified_name, name, line_start, line_end, element_type}, element_type = "function", lines = line_end - line_start, lines > 200 :limit 5` | Functions > 200 lines — refactor targets (see `find_large_functions`). |
| 14 | `incident_count` | `?[count(id)] := *incidents{id}` | Post-incident records (`query_incidents` surface). |

## Deriving new recipes

1. `::relations` lists every relation in the resolved project.
2. Named-pattern queries need only the bound columns; positional queries need
   **all** relation columns in order (the `run_raw_query` error message prints
   the full `code_elements` / `relationships` schemas on mismatch).
3. Keep `:limit` on row-returning recipes — the smoke gate runs all 14 against
   the live server on every check.

## FR-B50 gate

`scripts/mcp-smoke-tools.py` runs `RAW_QUERY_RECIPES` (this table) against the
live MCP on every smoke run and fails if any recipe errors or if fewer than
10 are defined. Both the table above and the script fixture must stay ≥ 10.
