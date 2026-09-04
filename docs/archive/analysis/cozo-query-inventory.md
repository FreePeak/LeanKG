# CozoDB Query Inventory (Complete, Re-derived from Source)

Date: 2026-08-04
Branch: main @ f1b50d59 (docs: cozo->postgres+pgvector migration plan)
Scope scanned: all `src/**/*.rs` in the worktree `leankg-pg-migration` (main repo working tree at the same commit). Tests/benches excluded from the inventory itself but counted (see §6).

Method: grep for `run_script(` / `run_raw_query(` / `import_relations` / `::hnsw` / `::relations` / `PRAGMA` / `VACUUM` across src, then read every call site in full (query string verbatim, params, row-index consumption).

## Counts

| Metric | Count |
|---|---|
| Total run_script/run_raw_query call sites (non-test src) | 278 |
| Distinct query strings (incl. dynamically formatted) | ~115 |
| `import_relations` (non-script writes, no translator needed) | 2 |
| Direct `cozo::DbInstance::new` outside schema.rs/keys.rs | 0 |
| Test/bench/e2e query sites (excluded) | ~17 (14 files in tests/, 1 in benches/, 2 in e2e/) |
| Deep-coupling ANN queries (`~embedding_vectors:vec_idx`) | 2 (src) + 2 (tests) |

### By operation kind
| Kind | Count |
|---|---|
| Read (immutable `?[...]` / `::relations` / `:schema` / `PRAGMA` / `VACUUM`) | ~178 |
| `:put` (incl. `:put`+`<-` literal, `:put`+`$batch_data`) | ~45 |
| `:rm` (rule-derived full-row rm, `<-` literal rm) | ~18 |
| `:delete ... where ...` | 7 |
| `:create` (DDL) | 16 tables + 1 (`index_hashes` via `:put` only — no DDL found; see risk note) |
| `:replace` (schema repair) | 3 |
| `::index create` / `::index drop` | ~30 (see §1) |
| `::hnsw create` / `::hnsw drop` | 2 + 1 build-time stmt |
| `import_relations` (embedding bulk writes) | 2 |

### By difficulty class
| Class | Count (query strings) |
|---|---|
| TRIVIAL (single relation, `=`/`in`/`regex` filters, optional `:limit/:offset/:order`) | ~95 |
| MODERATE (aggregates `count()`, `:group`, `:order` on aggregates, keyed-table writes, DDL, `:delete` w/ subquery) | ~15 |
| HAND-WRITE (cross-relation joins / negated rules / ANN / special ops) | 6 (see §3) |

Note: TRIVIAL here includes queries whose filters are `regex_matches` / `str_includes` / `>=`/`<` range — mechanically translatable to SQL but not `=`-only. The plan's "single-relation equality" claim is wrong on this point: regex/`in`/range filters are pervasive (~40 of the reads).

---

## Section 1 — Table inventory (from DDL in src/db/schema.rs + embeddings/state.rs + graph/inventory.rs + db/keys.rs)

### 1.1 Relations created in `init_schema` (src/db/schema.rs)

| Table | DDL (`:create`) | file:line | Key (`=>`) | Indexes (`::index create`) |
|---|---|---|---|---|
| code_elements | `{qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}` | schema.rs:364 | none (composite tuple key) | file_path_index{file_path} (370), qualified_name_index{qualified_name} (376), element_type_index{element_type} (382), parent_qualified_index{parent_qualified} (388) — recreated 911-914 |
| relationships | `{source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}` | schema.rs:397 | none | rel_type_index{rel_type} (402), target_qualified_index{target_qualified} (408), source_qualified_index{source_qualified} (414) — recreated 960-962 |
| business_logic | `{element_qualified: String, description: String, user_story_id: String?, feature_id: String?}` | schema.rs:423 | none | none |
| context_metrics | `{tool_name: String, timestamp: Int, project_path: String, input_tokens: Int, output_tokens: Int, output_elements: Int, execution_time_ms: Int, baseline_tokens: Int, baseline_lines_scanned: Int, tokens_saved: Int, savings_percent: Float, correct_elements: Int?, total_expected: Int?, f1_score: Float?, query_pattern: String?, query_file: String?, query_depth: Int?, success: Bool, is_deleted: Bool}` | schema.rs:430 | none | tool_name_index{tool_name} (435), timestamp_index{timestamp} (441), project_path_index{project_path} (447) |
| query_cache | `{cache_key: String, value_json: String, created_at: Int, ttl_seconds: Int, tool_name: String, project_path: String, metadata: String}` | schema.rs:454 | none | cache_key_index{cache_key} (459), tool_name_index{tool_name} (464) |
| service_metadata | `{service_name: String, env: String default 'local', team: String?, on_call: String?, repo_url: String?, language: String?, health_endpoint: String?, slo_p99_ms: Int?, incident_count: Int, last_incident: Int?, tags: String, version: String?, deploy_envs: String, created_at: Int, updated_at: Int}` | schema.rs:481 | none | svc_name_index{service_name} (486), svc_env_index{env} (487) |
| teams | `{id: String, name: String, description: String, owner_id: String, created_at: Int, updated_at: Int, graph_read_users: String, graph_write_users: String, members: String}` | schema.rs:498 | none | owner_index{owner_id} (502) |
| team_invites | `{token: String, team_id: String, email: String?, role: String, created_by: String, created_at: Int, expires_at: Int, accepted: Bool, accepted_by: String?}` | schema.rs:512 | none | team_index{team_id} (517), token_index{token} (518) |
| migrations | `{id: String, applied_at: Int}` | schema.rs:544 | none | none |
| knowledge_entries | `{id: String, knowledge_type: String, title: String, content: String, element_qualified: String?, user_story_id: String?, feature_id: String?, tags: String, environment: String, branch: String?, author: String, created_at: Int, updated_at: Int}` | schema.rs:569 (migration 001) | none | type_index{knowledge_type} (576), element_index{element_qualified} (577), env_index{environment} (578), author_index{author} (579) |
| feature_workflow_links | `{feature_id: String, workflow_id: String}` | schema.rs:594 (migration 002) | none | feature_id_index{feature_id} (599) |
| incidents | `{id: String, env: String, title: String, severity: String, occurred_at: Int, resolved_at: Int?, root_cause: String, resolution: String, affected_services: String, trigger_pattern: String?, prevention: String?, tags: String, author: String, linked_ticket: String?}` | schema.rs:981 (repair) | none | env_index{env} (984), severity_index{severity} (985), author_index{author} (986) |

### 1.2 Relations created elsewhere

| Table | DDL | file:line | Key | Indexes |
|---|---|---|---|---|
| embedding_state | `:create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}` | src/embeddings/state.rs:25 | qualified_name | qn_index{qualified_name} (27), usearch_key_index{usearch_key} (30), state_index{state} (32) |
| embedding_vectors | `:create embedding_vectors {qualified_name: String => vector: <F32; 384>}` | src/embeddings/state.rs:96 | qualified_name | `::hnsw create embedding_vectors:vec_idx {dim: 384, dtype: F32, fields: [vector], distance: Cosine, ef_construction: {ef}, m: {m}, extend_candidates: false, keep_pruned_connections: false}` (build_hnsw_create_stmt, state.rs:132-155) |
| index_inventory | `:create index_inventory {key: String => computed_at: String, total_elements: Int, total_relationships: Int, total_vectors: Int, total_documents: Int, total_doc_sections: Int, elements_by_type_json: String, relationships_by_type_json: String, vectors_by_type_json: String, estimated_vector_bytes: Int, estimated_hnsw_bytes: Int, notes: String}` | src/graph/inventory.rs:10-24 | key | none |
| api_keys | `:create api_keys {id: String, name: String, key_hash: String, created_at: String, last_used_at: String?, revoked_at: String?}` | src/db/keys.rs:50 | none | none |

### 1.3 `index_hashes` — NO DDL found
`src/indexer/content_hash.rs` does `:put index_hashes {path, hash} <- $args` (line 93) and `?[path, hash] <- index_hashes[path, hash]` (line 68) but **no `:create index_hashes` exists anywhere in src/**. It must be created implicitly by `:put` (CozoDB auto-creates relations on first `:put` when the schema can be inferred). The PostgreSQL backend MUST create this table explicitly with `{path: String => hash: String}`. **This is an assumption-violation: the plan's §2.2 table list omits `index_hashes`.**

### 1.4 `:replace` repair scripts (schema.rs)
- `REPAIR_LEGACY_CODE_ELEMENTS_11_TO_13` (schema.rs:659-665): 11-col -> 13-col `:replace code_elements {13 cols}` with `env = "local", ontology_layer = "procedural"` derived.
- `REPAIR_LEGACY_CODE_ELEMENTS_12_TO_13` (schema.rs:666-671): 12-col -> 13-col.
- `REPAIR_LEGACY_RELATIONSHIPS_5_TO_6` (schema.rs:672-677): 5-col -> 6-col with `env = "local"`.

Canonical column lists: `CODE_ELEMENTS_13_COLUMNS` (schema.rs:726), `CODE_ELEMENTS_12_COLUMNS` (742), `CODE_ELEMENTS_11_COLUMNS` (757), `RELATIONSHIPS_6_COLUMNS` (771), `RELATIONSHIPS_5_COLUMNS` (780).

---

## Section 2 — Full query inventory (grouped by file)

Legend: out-cols = head vars in order (positional consumption downstream). Params = `$name: type`.

### 2.1 src/db/schema.rs (60 sites — mostly DDL; entries grouped)

| # | file:line | function | kind | query (verbatim) | params | out-cols | class |
|---|---|---|---|---|---|---|---|
| S1 | schema.rs:356 | init_schema | read | `::relations` | — | name | TRIVIAL |
| S2 | schema.rs:364 | init_schema | DDL | `:create code_elements {qualified_name: String, element_type: String, name: String, file_path: String, line_start: Int, line_end: Int, language: String, parent_qualified: String?, cluster_id: String?, cluster_label: String?, metadata: String, env: String default 'local', ontology_layer: String default 'procedural'}` | — | — | MODERATE |
| S3 | schema.rs:370 | init_schema | DDL | `::index create code_elements:file_path_index { file_path }` | — | — | MODERATE |
| S4 | schema.rs:376 | init_schema | DDL | `::index create code_elements:qualified_name_index { qualified_name }` | — | — | MODERATE |
| S5 | schema.rs:382 | init_schema | DDL | `::index create code_elements:element_type_index { element_type }` | — | — | MODERATE |
| S6 | schema.rs:388 | init_schema | DDL | `::index create code_elements:parent_qualified_index { parent_qualified }` | — | — | MODERATE |
| S7 | schema.rs:397 | init_schema | DDL | `:create relationships {source_qualified: String, target_qualified: String, rel_type: String, confidence: Float, metadata: String, env: String default 'local'}` | — | — | MODERATE |
| S8-S10 | schema.rs:402/408/414 | init_schema | DDL | `::index create relationships:rel_type_index { rel_type }` / `...:target_qualified_index { target_qualified }` / `...:source_qualified_index { source_qualified }` | — | — | MODERATE |
| S11 | schema.rs:423 | init_schema | DDL | `:create business_logic {element_qualified: String, description: String, user_story_id: String?, feature_id: String?}` | — | — | MODERATE |
| S12 | schema.rs:430 | init_schema | DDL | `:create context_metrics {tool_name: String, timestamp: Int, project_path: String, input_tokens: Int, output_tokens: Int, output_elements: Int, execution_time_ms: Int, baseline_tokens: Int, baseline_lines_scanned: Int, tokens_saved: Int, savings_percent: Float, correct_elements: Int?, total_expected: Int?, f1_score: Float?, query_pattern: String?, query_file: String?, query_depth: Int?, success: Bool, is_deleted: Bool}` | — | — | MODERATE |
| S13-S15 | schema.rs:435/441/447 | init_schema | DDL | `::index create context_metrics:tool_name_index { tool_name }` / `:timestamp_index { timestamp }` / `:project_path_index { project_path }` | — | — | MODERATE |
| S16 | schema.rs:454 | init_schema | DDL | `:create query_cache {cache_key: String, value_json: String, created_at: Int, ttl_seconds: Int, tool_name: String, project_path: String, metadata: String}` | — | — | MODERATE |
| S17-S18 | schema.rs:459/464 | init_schema | DDL | `::index create query_cache:cache_key_index { cache_key }` / `:tool_name_index { tool_name }` | — | — | MODERATE |
| S19 | schema.rs:481 | init_schema | DDL | `:create service_metadata {service_name: String, env: String default 'local', team: String?, on_call: String?, repo_url: String?, language: String?, health_endpoint: String?, slo_p99_ms: Int?, incident_count: Int, last_incident: Int?, tags: String, version: String?, deploy_envs: String, created_at: Int, updated_at: Int}` | — | — | MODERATE |
| S20-S21 | schema.rs:486/487 | init_schema | DDL | `::index create service_metadata:svc_name_index { service_name }` / `:svc_env_index { env }` | — | — | MODERATE |
| S22 | schema.rs:498 | init_schema | DDL | `:create teams {id: String, name: String, description: String, owner_id: String, created_at: Int, updated_at: Int, graph_read_users: String, graph_write_users: String, members: String}` | — | — | MODERATE |
| S23 | schema.rs:502 | init_schema | DDL | `::index create teams:owner_index { owner_id }` | — | — | MODERATE |
| S24 | schema.rs:512 | init_schema | DDL | `:create team_invites {token: String, team_id: String, email: String?, role: String, created_by: String, created_at: Int, expires_at: Int, accepted: Bool, accepted_by: String?}` | — | — | MODERATE |
| S25-S26 | schema.rs:517/518 | init_schema | DDL | `::index create team_invites:team_index { team_id }` / `:token_index { token }` | — | — | MODERATE |
| S27 | schema.rs:544 | run_migrations | DDL | `:create migrations {id: String, applied_at: Int}` | — | — | MODERATE |
| S28 | schema.rs:552 | run_migrations | read | `?[id] := *migrations[id, _]` | — | id | TRIVIAL |
| S29 | schema.rs:569 | run_migrations (001) | DDL | `:create knowledge_entries {id: String, knowledge_type: String, title: String, content: String, element_qualified: String?, user_story_id: String?, feature_id: String?, tags: String, environment: String, branch: String?, author: String, created_at: Int, updated_at: Int}` | — | — | MODERATE |
| S30-S33 | schema.rs:576-579 | run_migrations | DDL | `::index create knowledge_entries:type_index { knowledge_type }` / `:element_index { element_qualified }` / `:env_index { environment }` / `:author_index { author }` | — | — | MODERATE |
| S34 | schema.rs:594 | run_migrations (002) | DDL | `:create feature_workflow_links {feature_id: String, workflow_id: String}` | — | — | MODERATE |
| S35 | schema.rs:599 | run_migrations | DDL | `::index create feature_workflow_links:feature_id_index { feature_id }` | — | — | MODERATE |
| S36 | schema.rs:659-665 | repair (const) | :replace | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata], env = "local", ontology_layer = "procedural" :replace code_elements {qualified_name: String, ... 13 cols}` | — | 13 | HAND-WRITE |
| S37 | schema.rs:666-671 | repair (const) | :replace | same head, source binds 12 cols, `ontology_layer = "procedural"` | — | 13 | HAND-WRITE |
| S38 | schema.rs:672-677 | repair (const) | :replace | `?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata], env = "local" :replace relationships {6 cols}` | — | 6 | HAND-WRITE |
| S39 | schema.rs:684 | get_column_count | read probe | `?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0` | — | qualified_name | TRIVIAL |
| S40 | schema.rs:688 | get_column_count | read probe | same with 12 cols `:limit 0` | — | | TRIVIAL |
| S41 | schema.rs:692 | get_column_count | read probe | same with 11 cols `:limit 0` | — | | TRIVIAL |
| S42 | schema.rs:698 | get_column_count | read probe | `?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 0` | — | | TRIVIAL |
| S43 | schema.rs:702 | get_column_count | read probe | 5-col variant | — | | TRIVIAL |
| S44 | schema.rs:716 | get_column_count | read | `:schema {relation}` (format!) | — | schema rows | MODERATE |
| S45 | schema.rs:888-891 | ensure_canonical_code_elements | DDL | `::index drop code_elements:{idx}` (format!, 4 idx) | — | | MODERATE |
| S46 | schema.rs:911-914 | ensure_canonical_code_elements | DDL | `::index create code_elements:file_path_index { file_path }` etc (4x, re-create) | — | | MODERATE |
| S47 | schema.rs:951-955 | ensure_canonical_relationships | DDL | `::index drop relationships:{idx}` (rel_type_index, target_qualified_index) | — | | MODERATE |
| S48 | schema.rs:959-962 | ensure_canonical_relationships | DDL | `::index create relationships:rel_type_index { rel_type }` / `:target_qualified_index { target_qualified }` | — | | MODERATE |
| S49 | schema.rs:970 | ensure_incidents_table | read | `::relations` | — | name | TRIVIAL |
| S50 | schema.rs:981 | ensure_incidents_table | DDL | `:create incidents {id: String, env: String, title: String, severity: String, occurred_at: Int, resolved_at: Int?, root_cause: String, resolution: String, affected_services: String, trigger_pattern: String?, prevention: String?, tags: String, author: String, linked_ticket: String?}` | — | | MODERATE |
| S51-S53 | schema.rs:984-986 | ensure_incidents_table | DDL | `::index create incidents:env_index { env }` / `:severity_index { severity }` / `:author_index { author }` | — | | MODERATE |
| S54 | schema.rs:1000 | record_migration | :put | `?[id, applied_at] <- [[$mid, $ts]] :put migrations {id, applied_at}` | mid: string, ts: int | — | TRIVIAL |
| S55 | schema.rs:1012 | validate_code_elements_schema | read | `:schema code_elements` | — | schema rows | MODERATE |
| S56 | schema.rs:1033 | validate_relationships_schema | read | `:schema relationships` | — | schema rows | MODERATE |

(The remaining schema.rs sites are the `::relations` probe in `init_schema` [S1] and the `mutability_for`/`json_to_datavalue` unit tests — excluded.)

### 2.2 src/db/mod.rs (46 sites)

| # | file:line | function | kind | query (verbatim) | params | out-cols | class |
|---|---|---|---|---|---|---|---|
| D1 | mod.rs:20 | create_business_logic | :put | `?[element_qualified, description, user_story_id, feature_id] <- [[ $eq, $desc, $us, $feat ]] :put business_logic { element_qualified, description, user_story_id, feature_id }` | eq/desc/us/feat: string (us/feat nullable -> null) | — | TRIVIAL |
| D2 | mod.rs:58 | get_business_logic | read | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = $eq` | eq: string | eq, desc, us, feat | TRIVIAL |
| D3 | mod.rs:92 | update_business_logic | :put | same as D1 | | | TRIVIAL |
| D4 | mod.rs:134 | delete_business_logic | :rm | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = $eq :rm business_logic {element_qualified, description, user_story_id, feature_id}` | eq: string | — | TRIVIAL (rm-all-cols) |
| D5 | mod.rs:149 | get_by_user_story | read | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[...], user_story_id = $us` | us: string | 4 | TRIVIAL |
| D6 | mod.rs:181 | get_by_feature | read | `..., feature_id = $feat` | feat: string | 4 | TRIVIAL |
| D7 | mod.rs:214-217 | search_business_logic | read (regex) | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[...], regex_matches(lowercase(description), "{}")` (format!, pattern interpolated, NOT param) | none | 4 | TRIVIAL (regex) |
| D8 | mod.rs:243 | all_business_logic | read | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id]` | — | 4 | TRIVIAL |
| D9 | mod.rs:421 | get_documented_by | read | `?[target_qualified, rel_type, metadata, confidence] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $sq, rel_type = "documented_by"` | sq: string | 4 (note col swap: metadata at idx 2, confidence at 3) | TRIVIAL |
| D10 | mod.rs:493 | get_code_for_requirement | read | `...business_logic[...], user_story_id = $us` | us: string | 4 | TRIVIAL |
| D11 | mod.rs:528 | record_metric | :put | `?[tool_name, timestamp, project_path, input_tokens, output_tokens, output_elements, execution_time_ms, baseline_tokens, baseline_lines_scanned, tokens_saved, savings_percent, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, success, is_deleted] <- [[ $tool, $ts, $path, $in_tok, $out_tok, $out_elem, $exec_ms, $base_tok, $base_lines, $saved, $sav_pct, $correct, $total, $f1, $qpat, $qfile, $qdepth, $success, false ]] :put context_metrics { ...19 cols }` | tool/ts/path/...: string/int/float/bool/null mix | — | MODERATE (19-col literal) |
| D12 | mod.rs:657 | get_metrics_summary (with tool) | read | `?[tool_name, ...19 cols] := *context_metrics[...], timestamp >= $cutoff, tool_name = $tool, is_deleted = false` | cutoff: int, tool: string | 19 | TRIVIAL (>= filter) |
| D13 | mod.rs:659 | get_metrics_summary (no tool) | read | same minus `tool_name = $tool` | cutoff: int | 19 | TRIVIAL |
| D14 | mod.rs:756 | cleanup_old_metrics | read | `?[...19 cols] := *context_metrics[...], timestamp < $cutoff` | cutoff: int | 19 | TRIVIAL |
| D15 | mod.rs:773 | cleanup_old_metrics | :delete | `:delete context_metrics where timestamp < $cutoff` | cutoff: int | — | MODERATE (delete-by-predicate) |
| D16 | mod.rs:783 | reset_metrics | read | `?[...19 cols] := *context_metrics[...]` | — | 19 | TRIVIAL |
| D17 | mod.rs:789 | reset_metrics | :delete | `:delete context_metrics where tool_name != "NON_EXISTENT_TOOL_NAME_123456789"` | — | — | MODERATE |
| D18 | mod.rs:805 | create_knowledge_entry | :put | `?[id, knowledge_type, title, content, element_qualified, user_story_id, feature_id, tags, environment, branch, author, created_at, updated_at] <- [[$id, $kt, $title, $content, $eq, $us, $feat, $tags, $env, $branch, $author, $cat, $uat]] :put knowledge_entries {13 cols}` | 13 params (nullable eq/us/feat/branch) | — | MODERATE |
| D19 | mod.rs:884 | get_knowledge_entry | read | `?[...13 cols] := *knowledge_entries[...], id = $id` | id: string | 13 | TRIVIAL |
| D20 | mod.rs:907-908 | delete_knowledge_entry | :rm | `?[id, ...13 cols] := *knowledge_entries[...], id = $id :rm knowledge_entries {13 cols}` | id: string | — | TRIVIAL |
| D21 | mod.rs:942-945 | search_knowledge | read (regex+or) | `?[...13 cols] := *knowledge_entries[...], {conditions} :limit {limit}` (format!: `(regex_matches(lowercase(title), "...") or regex_matches(lowercase(content), "..."))` + optional `knowledge_type = $kt` + optional `environment = $env`) | kt/env: string (optional), limit interpolated | 13 | TRIVIAL (regex) |
| D22 | mod.rs:959 | get_knowledge_by_element | read | `..., element_qualified = $eq` | eq | 13 | TRIVIAL |
| D23 | mod.rs:978 | get_knowledge_by_feature | read | `..., feature_id = $feat` | feat | 13 | TRIVIAL |
| D24 | mod.rs:998-1000 | get_knowledge_by_environment | read | `..., environment = $env :limit {limit}` | env | 13 | TRIVIAL |
| D25 | mod.rs:1043 | link_feature_workflow | :put | `?[feature_id, workflow_id] <- [[ $feat, $wf ]] :put feature_workflow_links { feature_id, workflow_id }` | feat/wf: string | — | TRIVIAL |
| D26 | mod.rs:1062 | unlink_feature_workflow (find) | read | `?[feature_id, workflow_id] := *feature_workflow_links[feature_id, workflow_id], feature_id = $feat, workflow_id = $wf` | feat/wf | 2 | TRIVIAL |
| D27 | mod.rs:1074-1075 | unlink_feature_workflow (del) | :delete | `:delete feature_workflow_links where feature_id = $feat, workflow_id = $wf` | feat/wf | — | MODERATE |
| D28 | mod.rs:1095 | get_workflows_for_feature | read | `?[workflow_id] := *feature_workflow_links[feature_id, workflow_id], feature_id = $feat` | feat | 1 | TRIVIAL |
| D29 | mod.rs:1114 | get_features_for_workflow | read | `?[feature_id] := ..., workflow_id = $wf` | wf | 1 | TRIVIAL |
| D30 | mod.rs:1133 | create_incident | :put | `?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] <- [[$id, $env, $title, $sev, $occ, $res_at, $rc, $res, $svc, $tp, $prev, $tags, $author, $tk]] :put incidents {14 cols}` | 14 params (res_at/tp/prev/tk nullable) | — | MODERATE |
| D31 | mod.rs:1260 | get_incident | read | `?[id, ...14 cols] := *incidents[...], id = $id` | id | 14 | TRIVIAL |
| D32 | mod.rs:1280 | delete_incident | :delete | `:delete incidents where id = $id` | id | — | MODERATE |
| D33 | mod.rs:1323-1326 | query_incidents | read (regex) | `?[id, ...14 cols] := *incidents[...]{conditions} :limit {limit}` (conditions: `regex_matches(lowercase(affected_services), $svc)`, `(regex_matches(lowercase(title), $pat) or regex_matches(lowercase(root_cause), $pat))`, `env = $env`) | svc/pat/env: string (optional), limit interpolated | 14 | TRIVIAL (regex) |
| D34 | mod.rs:1374-1375 | get_elements_by_env (probe) | read probe | `?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0` | — | 1 | TRIVIAL |
| D35 | mod.rs:1384-1387 | get_elements_by_env | read | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[..., metadata{tail}], env = $env :limit {limit}` (tail = `, env, ontology_layer` or `, env` — arity probe) | env: string, limit interpolated | 12 | TRIVIAL |
| D36 | mod.rs:1403-1406 | get_relationships_by_env | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[...], env = $env :limit {limit}` | env | 6 | TRIVIAL |
| D37 | mod.rs:1421 | get_element_across_envs | read | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[...], qualified_name = $qn` | qn | 12 | TRIVIAL |
| D38 | mod.rs:1478 | upsert_service_metadata | :put | `?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] <- [[$svc, $env, $team, $oncall, $repo, $lang, $health, $slo, $icount, $lastinc, $tags, $ver, $denvs, $cat, $uat]] :put service_metadata {15 cols}` | 15 params | — | MODERATE |
| D39 | mod.rs:1538 | get_service_metadata | read | `?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] := *service_metadata{service_name, env, team, ...}, service_name == $svc, env == $env` | svc/env: string | 15 | TRIVIAL (**note `{}` attr syntax + `==` not `=`**) |
| D40 | mod.rs:1596 | create_team | :put | `?[id, name, description, owner_id, created_at, updated_at, graph_read_users, graph_write_users, members] <- [[$id, $name, $desc, $owner, $cat, $uat, $read_users, $write_users, $members]] :put teams {9 cols}` | 9 params | — | MODERATE |
| D41 | mod.rs:1637 | get_team | read | `?[id, ...9 cols] := *teams[...], id = $id` | id | 9 | TRIVIAL |
| D42 | mod.rs:1656 | delete_team | :delete | `:delete teams where id = $id` | id | — | MODERATE |
| D43 | mod.rs:1664 | list_teams | read | `?[id, ...9 cols] := *teams[...]` | — | 9 | TRIVIAL |
| D44 | mod.rs:1698 | create_team_invite | :put | `?[token, team_id, email, role, created_by, created_at, expires_at, accepted, accepted_by] <- [[$token, $tid, $email, $role, $by, $cat, $exp, $acc, $accept]] :put team_invites {9 cols}` | 9 params | — | MODERATE |
| D45 | mod.rs:1750 | get_team_invite | read | `?[token, ...9 cols] := *team_invites[...], token = $token` | token | 9 | TRIVIAL |
| D46 | mod.rs:1768 | get_team_invites | read | `..., team_id = $tid` | tid | 9 | TRIVIAL |
| D47 | mod.rs:1807 | delete_team_invite | :delete | `:delete team_invites where token = $token` | token | — | MODERATE |

### 2.3 src/db/keys.rs (9 sites — separate keys.db sqlite file)

| # | file:line | function | kind | query | params | out-cols | class |
|---|---|---|---|---|---|---|---|
| K1 | keys.rs:40 | ApiKeyStore::init_db | read | `::relations` | — | name | TRIVIAL |
| K2 | keys.rs:50 | ApiKeyStore::init_db | DDL | `:create api_keys {id: String, name: String, key_hash: String, created_at: String, last_used_at: String?, revoked_at: String?}` | — | — | MODERATE |
| K3 | keys.rs:79-82 | create_key | :put | `?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]] :put api_keys { id, name, key_hash, created_at, last_used_at, revoked_at }` | 6 params (last_used_at/revoked_at null) | — | TRIVIAL |
| K4 | keys.rs:101-103 | list_keys | read | `?[id, name, key_hash, created_at, last_used_at, revoked_at] := *api_keys[id, name, key_hash, created_at, last_used_at, revoked_at]` | — | 6 | TRIVIAL |
| K5 | keys.rs:142-144 | revoke_key (find) | read | `?[id, name, key_hash, created_at, last_used_at, revoked_at] := *api_keys[...], id = $id` | id | 6 | TRIVIAL |
| K6 | keys.rs:164-167 | revoke_key (update) | :put | `?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]] :put api_keys {6 cols}` | 6 params | — | TRIVIAL |
| K7 | keys.rs:200-202 | validate_key | read | `?[id, key_hash] := *api_keys[id, key_hash], revoked_at = null` | — | 2 | TRIVIAL (**null-equality on optional col** — see risk §5) |
| K8 | keys.rs:212 | validate_key (touch) | :delete | `:delete api_keys where id = "{key_id}"` (format!, interpolated, NOT param) | — | — | MODERATE (string-interpolated id — injection risk present today) |
| K9 | keys.rs:215-218 | validate_key (touch) | :put | `?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[...]] :put api_keys {...}` (same as K6; name/created_at become "") | 6 params | — | TRIVIAL |

### 2.4 src/graph/query.rs (106 sites — the big one)

All on `GraphEngine`; handle via `self.db: Arc<CozoDb>` (query.rs:62).

| # | file:line | fn | kind | query | params | out-cols | class |
|---|---|---|---|---|---|---|---|
| G1 | 144 | vacuum | PRAGMA | `VACUUM` | — | — | HAND-WRITE (engine-specific) |
| G2 | 153 | code_elements_tail | read probe | `?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0` | — | 1 | TRIVIAL |
| G3 | 187 | find_element | read | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[..., metadata{tail}], qualified_name = $qn` (format!, tail = `, env, ontology_layer` \| `, env`) | qn: string | 11 | TRIVIAL |
| G4 | 238 | get_elements_by_qualified_names | read (loop) | same as G3 but head has 12 cols (`..., metadata, env`), executed once per qn | qn | 12 | TRIVIAL |
| G5 | 281 | find_element_by_name | read | `..., name = $nm` | nm | 11 | TRIVIAL |
| G6 | 344 | get_dependencies | read (or) | `?[target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "imports"` | sq1, sq2: string | 4 | TRIVIAL (or) |
| G7 | 390 | get_relationships | read (or) | `?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[...], (source_qualified = $sq1 or source_qualified = $sq2)` | sq1, sq2 | 6 | TRIVIAL (or) |
| G8 | 456 | get_relationships_for_target | read (or) | `..., (target_qualified = $tq1 or target_qualified = $tq2)` | tq1, tq2 | 6 | TRIVIAL (or) |
| G9 | 513 | run_raw_query | passthrough | arbitrary user query | arbitrary | arbitrary | HAND-WRITE (opaque pass-through; must be fenced) |
| G10 | 528-531 | get_elements_paginated | read | `?[...11 cols] := *code_elements[..., metadata{tail}] :limit {limit} :offset {offset}` | none (interp) | 11 | TRIVIAL |
| G11 | 576-582 | get_code_elements_for_tree | read | `?[qualified_name, element_type, name, file_path, line_start, line_end] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type in ["function", "struct", "class", "module", "interface", "enum", "trait"] :limit {cap}` | none | 6 | TRIVIAL (in-list) |
| G12 | 606-608 | count_code_elements | count | `?[count(n)] := *code_elements[n, et, a, b, c, d, e, f, g, h, i, j{tail}], et in ["function", ...]` | — | 1 | MODERATE |
| G13 | 627-630 | get_relationships_paginated | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[... , _] :limit {} :offset {}` | — | 5 | TRIVIAL |
| G14 | 682-688 | get_relationships_for_elements_paginated | read (or) | `?[...5 cols] := *relationships[...], ({source_filter}) :limit {} :offset {}` (source_filter = `source_qualified = "{}" or ...` joined, escaped, interpolated) | none | 5 | TRIVIAL (or, interpolated) |
| G15 | 696-703 | same, with rel types | read | `..., ({}), rel_type in [{types}] :limit {} :offset {}` | none | 5 | TRIVIAL |
| G16 | 742 | all_elements | read | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[..., metadata{tail}]` | — | 12 | TRIVIAL |
| G17 | 810 | for_each_element | read | same as G16 | — | 12 | TRIVIAL |
| G18 | 848 | for_each_relationship | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env]` | — | 6 | TRIVIAL |
| G19 | 884 | for_each_element_of_type | read | `..., element_type = "{safe}"` (interpolated, quotes stripped) | — | 12 | TRIVIAL |
| G20 | 933 | get_elements_in_folder (root all_content) | read | `?[...11 cols] := *code_elements[...] :limit {} :offset {}` | — | 11 | TRIVIAL |
| G21 | 977-979 | get_elements_in_folder (rels 1) | read (in-list) | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], source_qualified in $qns` | qns: string[] | 5 | TRIVIAL (in-array) |
| G22 | 1020 | get_elements_in_folder (root direct) | read | same as G20 (limit 5000) | — | 11 | TRIVIAL |
| G23 | 1077-1079 | get_elements_in_folder (rels 2) | read | same as G21 | qns | 5 | TRIVIAL |
| G24 | 1123 | get_elements_in_folder (path) | read (regex) | `?[...11 cols] := *code_elements[...], regex_matches(file_path, $pat) :limit {} :offset {}` | pat: string | 11 | TRIVIAL (regex) |
| G25 | 1187-1189 | get_elements_in_folder (rels 3) | read | same as G21 | qns | 5 | TRIVIAL |
| G26 | 1292-1297 | get_relationships_for_elements_fast | read (or) | `?[...5 cols] := *relationships[...], ({source_filter}) :limit 5000` | — | 5 | TRIVIAL |
| G27 | 1305-1312 | same w/ types | read | `..., rel_type in [...] :limit 5000` | — | 5 | TRIVIAL |
| G28 | 1392-1395 | get_relationships_involving_elements_fast (out) | read | `?[...5 cols] := *relationships[...], source_qualified = $sq :limit 500` | sq | 5 | TRIVIAL |
| G29 | 1405-1408 | same (in) | read | `..., target_qualified = $tq :limit 500` | tq | 5 | TRIVIAL |
| G30 | 1429 | all_relationships | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[... , _]` | — | 5 | TRIVIAL |
| G31 | 1492 | get_children | read | `?[...11 cols] := *code_elements[...], parent_qualified = $pq` | pq | 11 | TRIVIAL |
| G32 | 1564 | get_children_filtered (root) | read | `?[...11 cols] := *code_elements[...] :limit {} :offset {}` | — | 11 | TRIVIAL |
| G33 | 1578 | get_children_filtered (path) | read (regex) | `..., regex_matches(file_path, $pat) :limit {} :offset {}` | pat | 11 | TRIVIAL |
| G34 | 1582-1585 | get_children_filtered (path+type) | read (regex) | `..., regex_matches(file_path, $pat), element_type = "{}" :limit {} :offset {}` | pat | 11 | TRIVIAL |
| G35 | 1661-1663 | get_children_filtered (rels) | read | same as G21 | qns | 5 | TRIVIAL |
| G36 | 1725 | get_top_level_directories | read (range) | `?[fp] := *code_elements[qualified_name, element_type, name, fp, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], fp >= $lo and fp < $hi` | lo, hi: string (prefix-range with `\x7f` upper bound) | 1 | TRIVIAL (range) |
| G37 | 1762 | get_annotation | read | `?[element_qualified, description, user_story_id, feature_id] := *business_logic[...], element_qualified = $eq` | eq | 4 | TRIVIAL |
| G38 | 1793 | search_annotations | read (regex) | `?[...4 cols] := *business_logic[...], regex_matches(lowercase(description), ".*{safe_pattern}.*")` (interpolated escaped) | — | 4 | TRIVIAL |
| G39 | 1816 | all_annotations | read | `?[...4 cols] := *business_logic[...]` | — | 4 | TRIVIAL |
| G40 | 1841 | get_documented_by | read (or) | `?[source_qualified, target_qualified, rel_type, metadata, confidence] := *relationships[...], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "documented_by"` | sq1, sq2 | 5 (metadata at 3, confidence at 4) | TRIVIAL |
| G41 | 1935 | get_business_logic_by_user_story | read | `..., user_story_id = $uid` | uid | 4 | TRIVIAL |
| G42 | 1980 | insert_elements_with | :put (batch) | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- $batch_data :put code_elements { ...11 cols }` | batch_data: array of 11-arrays (chunks of 1000) | — | MODERATE (batch data binding) |
| G43 | 2081 | insert_element | :put | `?[...11 cols] <- [[ $qn, $et, $nm, $fp, $ls, $le, $lg, $pq, $cid, $cl, $md ]] :put code_elements {...}` | 11 params (pq/cid/cl nullable) | — | TRIVIAL |
| G44 | 2103-2108 | update_element_cluster (rm) | :rm | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[..., metadata{tail}], qualified_name = $qn :rm code_elements {{...11 cols}}` (format!) | qn | — | TRIVIAL |
| G45 | 2148 | insert_relationship | :put | `?[source_qualified, target_qualified, rel_type, confidence, metadata] <- [[ $sq, $tq, $rt, $cn, $md ]] :put relationships { ...5 cols }` | 5 params | — | TRIVIAL |
| G46 | 2212 | insert_relationships_with | :put (batch) | `?[source_qualified, target_qualified, rel_type, confidence, metadata] <- $batch_data :put relationships {...}` | batch_data: array of 5-arrays (chunks of 1000) | — | MODERATE |
| G47 | 2262-2267 | remove_elements_by_file | :rm | `?[...11 cols] := *code_elements[...], file_path = $fp :rm code_elements {{...}}` (format!) | fp | — | TRIVIAL |
| G48 | 2291-2296 | remove_elements_by_file_bulk | :rm | same as G47 | fp | — | TRIVIAL |
| G49 | 2317-2322 | remove_elements_by_files_bulk | :rm | `?[...11 cols] := *code_elements[...], file_path in $fps :rm code_elements {{...}}` | fps: string[] | — | TRIVIAL |
| G50 | 2348-2352 | remove_relationships_by_files_bulk | :rm | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], source_qualified in $sqs :rm relationships {...}` | sqs: string[] | — | TRIVIAL |
| G51 | 2379-2383 | remove_relationships_by_source | :rm | `..., source_qualified = $sq :rm relationships {...}` | sq | — | TRIVIAL |
| G52 | 2406-2410 | remove_relationships_by_source_bulk | :rm | same as G51 | sq | — | TRIVIAL |
| G53 | 2428-2433 | remove_elements_by_qualified_name | :rm | `?[...11 cols] := *code_elements[...], qualified_name = $qn :rm code_elements {{...}}` | qn | — | TRIVIAL |
| G54 | 2459 | list_ontology_qualified_names | read (regex) | `?[qualified_name] := *code_elements[...], regex_matches(file_path, "^ontology://")` | — | 1 | TRIVIAL |
| G55 | 2476 | list_ontology_elements | read (regex) | `?[...12 cols] := *code_elements[...], regex_matches(file_path, "^ontology://")` | — | 12 | TRIVIAL |
| G56 | 2517 | clear_ontology_layer (count) | read | same as G54 (regex) | — | 1 | TRIVIAL |
| G57 | 2524-2532 | clear_ontology_layer (rels) | **:rm with join** | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], *code_elements[source_qualified, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], regex_matches(file_path, "^ontology://") :rm relationships {{...}}` (format!) | — | — | **HAND-WRITE** (2-relation join) |
| G58 | 2536-2542 | clear_ontology_layer (elems) | :rm | `?[...11 cols] := *code_elements[...], regex_matches(file_path, "^ontology://") :rm code_elements {{...}}` | — | — | TRIVIAL |
| G59 | 2554 | get_elements_by_file | read | `?[...11 cols] := *code_elements[...], file_path = $fp` | fp | 11 | TRIVIAL |
| G60 | 2607 | search_by_name | read (regex) | `?[...11 cols] := *code_elements[...], regex_matches(lowercase(name), ".*{safe_name}.*")` (interp escaped) | — | 11 | TRIVIAL |
| G61 | 2651 | search_by_type | read | `..., element_type = "{}"` (interp) | — | 11 | TRIVIAL |
| G62 | 2692-2694 | search_by_pattern | read (fn) | `?[...11 cols] := *code_elements[...], str_includes(lowercase(qualified_name), lowercase($pattern))` | pattern: string | 11 | TRIVIAL (fn filter) |
| G63 | 2751-2756 | search_by_content | read (or-fn) | `?[...11 cols] := *code_elements[...], str_includes(lowercase(name), "{pattern}") or str_includes(lowercase(qualified_name), "{pattern}") or str_includes(lowercase(file_path), "{pattern}") :limit 200` (interp escaped) | — | 11 | TRIVIAL |
| G64 | 2799 | search_by_relation_type | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], rel_type = "{}"` (interp) | — | 5 | TRIVIAL |
| G65 | 2832 | find_oversized_functions | read (arith) | `?[...11 cols] := *code_elements[...], element_type = "function", (line_end - line_start + 1) >= {}` (interp min_lines) | — | 11 | TRIVIAL (arithmetic filter) |
| G66 | 2880 | find_oversized_functions_by_lang | read (arith) | `..., element_type = "function", language = "{}", (line_end - line_start + 1) >= {}` | — | 11 | TRIVIAL |
| G67 | 2925 | run_element_query (helper) | read | arbitrary element query (passed in) | — | 11 | helper |
| G68 | 2966-2974 | search_by_name_typed (typed) | read (regex) | `?[...11 cols] := *code_elements[...]{filter_clause}, regex_matches(lowercase(name), "{pattern}") :limit {limit}` (interp; filter_clause = `, element_type = "{}"`) | — | 11 | TRIVIAL |
| G69 | 2976-2984 | search_by_name_typed (plain) | read | same minus filter | — | 11 | TRIVIAL |
| G70 | 3000-3007 | find_elements_by_name_exact | read | `?[...11 cols] := *code_elements[...]{type_clause}, name = "{name}" :limit 20` (interp escaped) | — | 11 | TRIVIAL |
| G71 | 3036-3041 | find_elements_by_file_path_prefix | read (or-regex) | `?[...11 cols] := *code_elements[...], (file_path = "{exact}" or regex_matches(file_path, "^{prefix}/.*") or regex_matches(file_path, ".*/{basename}$")), !regex_matches(file_path, "^ontology://") :limit {limit}` (interp escaped) | — | 11 | TRIVIAL |
| G72 | 3064-3072 | get_callers (edge) | read (regex) | `?[src, tgt, rel_type, conf, meta] := *relationships[src, tgt, rel_type, conf, meta, _], rel_type = "calls", regex_matches(tgt, ".*{function_name}.*"){target_scope} :limit 50` | — | 5 | TRIVIAL |
| G73 | 3099-3105 | get_callers (elements) | read (or) | `?[...11 cols] := *code_elements[...], ({sources}) :limit 50` (interp `qualified_name = "{}" or ...`) | — | 11 | TRIVIAL |
| G74 | 3151-3158 | get_call_graph_bounded (edge) | read | `?[src, tgt, conf, meta] := *relationships[src, tgt, rel_type, conf, meta, _], rel_type = "calls", {filter} :limit {max_results}` (filter = `src = "..."` or `(src = "..." or src = "./...")`) | — | 4 | TRIVIAL |
| G75 | 3201 | resolve_call_edges | read | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], rel_type = "calls"` | — | 5 | TRIVIAL |
| G76 | 3226-3228 | resolve_call_edges (functions) | read | `?[qualified_name, name, file_path] := *code_elements[...], element_type = "function"` | — | 3 | TRIVIAL |
| G77 | 3323-3327 | _batch_delete_unresolved_calls | :rm (batch) | `?[source_qualified, target_qualified, rel_type, confidence, metadata] <- $batch_data :rm relationships {5 cols}` | batch_data: array of 5-arrays (chunks 1000) | — | MODERATE |
| G78 | 3349 | find_function_by_name_with_confidence | read | `?[qualified_name, file_path] := *code_elements[...], element_type = "function", name = "{}", file_path = "{}" :limit 1` (interp escaped) | — | 2 | TRIVIAL |
| G79 | 3359 | same (no hint) | read | `?[qualified_name] := *code_elements[...], element_type = "function", name = "{}" :limit 1` | — | 1 | TRIVIAL |
| G80 | 3375-3379 | _delete_relationship | :rm | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], source_qualified = $sq, target_qualified = $tq, rel_type = "calls" :rm relationships {...}` | sq, tq | — | TRIVIAL |
| G81 | 3398 | get_service_graph | read | `?[...5 cols] := *relationships[...], rel_type = "service_calls"` | — | 5 | TRIVIAL |
| G82 | 3493 | count_elements | count | `?[count(n)] := *code_elements[n, a, b, c, d, e, f, g, h, i, j{tail}]` | — | 1 | MODERATE |
| G83 | 3506 | has_elements | read | `?[qualified_name] := *code_elements[...] :limit 1` | — | 1 | TRIVIAL |
| G84 | 3524 | count_elements_by_type | count | `?[count(n)] := *code_elements[n, et, a, b, c, d, e, f, g, h, i{tail}], et = $et` | et: string | 1 | MODERATE |
| G85 | 3551 | count_elements_by_type_in | count | `?[count(n)] := *code_elements[n, et, a, b, c, d, e, f, g, h, i{tail}], et in $ets` | ets: string[] | 1 | MODERATE |
| G86 | 3583 | count_relationships | count | `?[count(n)] := *relationships[n, a, b, c, d, _]` | — | 1 | MODERATE |
| G87 | 3594 | count_business_logic | count | `?[count(n)] := *business_logic[n, a, b, c]` | — | 1 | MODERATE |
| G88 | 3606-3609 | count_files | **multi-rule** | `files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j{tail}]` + `?[count(f)] := files[f]` (two rules in one script) | — | 1 | **HAND-WRITE** (intermediate rule) |
| G89 | 3625 | count_by_element_type | count | `?[count(n)] := *code_elements[n, t, a, b, c, d, e, f, g, h, i{tail}], t = "{}"` (interp) | — | 1 | MODERATE |
| G90 | 3663-3666 | query_incidents | read (regex) | `?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] := *incidents[...], {conditions} :limit {limit}` (conditions incl. `regex_matches(...)` and `env = "{}"` escaped interp) | — | 14 | TRIVIAL |
| G91 | 3710-3713 | get_service_context (elem) | read | `?[...12 cols] := *code_elements[...], qualified_name = "{}", env = "{}"` (escaped interp) | — | 12 | TRIVIAL |
| G92 | 3732-3735 | get_service_context (outgoing) | read (or) | `?[target_qualified] := *relationships[...], source_qualified = "{}", env = "{}", (rel_type = "calls" or rel_type = "service_calls")` | — | 1 | TRIVIAL |
| G93 | 3748-3751 | get_service_context (incoming) | read (or) | `?[source_qualified] := *relationships[...], target_qualified = "{}", env = "{}", (rel_type = "calls" or rel_type = "service_calls")` | — | 1 | TRIVIAL |
| G94 | 3765-3768 | get_service_context (schemas) | read (fn) | `?[name] := *code_elements[...], starts_with(file_path, "{}"), regex_matches(element_type, "(schema|protobuf|proto|openapi|json_schema|avro|sql_table|event|topic|config)")` | — | 1 | TRIVIAL |
| G95 | 3782-3786 | get_service_context (incidents) | read (regex) | `?[id, resolved_at, title, occurred_at, prevention, root_cause] := *incidents[...], regex_matches(lowercase(affected_services), "{}"), env = "{}"` | — | 6 | TRIVIAL |
| G96 | 3857-3861 | get_service_metadata_fields | read | `?[team, on_call, repo_url, language] := *service_metadata[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at], service_name = "{}", env = "{}"` | — | 4 | TRIVIAL |
| G97 | 3885-3889 | find_env_conflicts | read (loop x3 envs) | `?[...12 cols] := *code_elements[...], qualified_name = "{}", env = "{}"` | — | 12 | TRIVIAL |
| G98 | 4010-4012 | get_architecture (languages) | **:group-ish agg + :order** | `?[language, count(language)] := *code_elements[_, _, _, _, _, _, language, _, _, _, _{tail}] :order -count(language)` | — | 2 | **HAND-WRITE** (agg + order-agg) |
| G99 | 4030-4034 | get_architecture (entry points) | read | `?[qualified_name, file_path, language] := *code_elements[qualified_name, "function", name, file_path, _, _, language, _, _, _, _{tail}], (name = "main" or ... )` | — | 3 | TRIVIAL |
| G100 | 4052-4056 | get_architecture (clusters) | **:group agg** | `?[cluster_label, cluster_id, count(qn)] := *code_elements[qn, _, _, _, _, _, _, _, cluster_id, cluster_label, _{tail}], cluster_id != null, cluster_id != ""` | — | 3 | **HAND-WRITE** (agg + null-guard) |
| G101 | 4075 | get_architecture (rel types) | agg | `?[rel_type, count(rel_type)] := *relationships[_, _, rel_type, _, _, _]` | — | 2 | MODERATE |
| G102 | 4089-4094 | get_architecture (hotspots) | **:group agg + :order + :limit** | `?[file_path, count(qualified_name)] := *code_elements[qualified_name, "function", _, file_path, _, _, _, _, _, _, _{tail}], file_path != "" :order -count(qualified_name) :limit 10` | — | 2 | **HAND-WRITE** |
| G103 | 4113-4117 | get_architecture (routes) | read | `?[qualified_name, file_path, metadata] := *code_elements[qualified_name, "route", name, file_path, _, _, language, _, _, _, metadata{tail}]` | — | 3 | TRIVIAL |
| G104 | 4176 | count_knowledge | count | `?[count(id)] := *knowledge_entries[id, _, _, _, _, _, _, _, _, _, _, _, _]` | — | 1 | MODERATE |
| G105 | 4194-4198 | get_graph_schema (types) | **:group agg + :order** | `?[element_type, count(element_type)] := *code_elements[_, element_type, _, _, _, _, _, _, _, _, _{tail}] :order -count(element_type)` | — | 2 | **HAND-WRITE** |
| G106 | 4216-4217 | get_graph_schema (rel types) | agg + :order | `?[rel_type, count(rel_type)] := *relationships[_, _, rel_type, _, _, _] :order -count(rel_type)` | — | 2 | MODERATE |
| G107 | 4279-4282 | find_dead_code (candidates) | read (arith + computed col) | `?[qualified_name, file_path, line_end, line_start, language, name, span] := *code_elements[qualified_name, et, name, file_path, line_start, line_end, language, _, _, _, _{tail}], line_end >= 0, line_start >= 0, (line_end - line_start) >= {threshold}, et in [...], name != "main", ..., span = line_end - line_start :order -span` | — | 7 (computed `span`) | MODERATE (computed col + order) |
| G108 | 4337 | referenced_qualified_names | read | `?[tgt] := *relationships[_, tgt, rel, _, _, _], (rel = "calls" or rel = "tested_by")` | — | 1 | TRIVIAL |
| G109 | 4366 | referenced_bare_names | read (in) | `?[name] := *code_elements[qn, _, name, _, _, _, _, _, _, _, _{tail}], qn in $qns` | qns: string[] | 1 | TRIVIAL |
| G110 | 4385 | calls_source_qualified_names | read | `?[src] := *relationships[src, _, r, _, _, _], r = "calls"` | — | 1 | TRIVIAL |
| G111 | 5820 | get_all_service_metadata | read | `?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] := *service_metadata[...], env = $env` | env: string | 15 | TRIVIAL |

### 2.5 src/graph/inventory.rs (4 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| I1 | 48 | ensure_index_inventory_table | read | `::relations` | — | name | TRIVIAL |
| I2 | 54 | ensure_index_inventory_table | DDL | `:create index_inventory {key: String => computed_at: String, ...13 cols}` | — | — | MODERATE |
| I3 | 134-135 | upsert_inventory | :put | `?[key, computed_at, total_elements, total_relationships, total_vectors, total_documents, total_doc_sections, elements_by_type_json, relationships_by_type_json, vectors_by_type_json, estimated_vector_bytes, estimated_hnsw_bytes, notes] <- [[$key, ...]] :put index_inventory {key => computed_at, ...}` (note `key => computed_at` keyed put) | 13 params | — | MODERATE (keyed :put) |
| I4 | 200-202 | load_latest_inventory | read | `?[key, ...13 cols] := *index_inventory[key, ...], key = "latest"` | — | 13 | TRIVIAL |

### 2.6 src/graph/persistent_cache.rs (5 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| P1 | 177-184 | evict_from_db | **:delete + subquery** | `:delete query_cache where cache_key in ( select cache_key from query_cache order by created_at asc limit $count )` | count: int | — | **HAND-WRITE** (SQL subquery inside Cozo delete — SQLite-specific syntax!) |
| P2 | 222-225 | load_from_db | read (attr syntax) | `?[value_json, created_at, ttl_seconds] := *query_cache[cache_key = $key, value_json, created_at, ttl_seconds]` | key: string | 3 | TRIVIAL (note `[cache_key = $key]` attr binding) |
| P3 | 258-262 | save_to_db | :put | `?[cache_key, value_json, created_at, ttl_seconds, tool_name, project_path, metadata] <- [[ $key, $value_json, $created_at, $ttl_seconds, "unknown", "default", "{}" ]] :put query_cache {7 cols}` | key, value_json, created_at, ttl_seconds | — | TRIVIAL |
| P4 | 289 | delete_from_db | :delete | `:delete query_cache where cache_key = $key` | key | — | MODERATE |
| P5 | 322 | database_size_approx | PRAGMA | `PRAGMA page_count` | — | 1 | HAND-WRITE (SQLite-specific; no PG equivalent — use pg_database_size) |

### 2.7 src/graph/clustering.rs (3 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| C1 | 305-308 | load_precomputed_clusters (probe) | read probe | `?[qualified_name] := *code_elements[..., env, ontology_layer] :limit 0` | — | 1 | TRIVIAL |
| C2 | 317-322 | load_precomputed_clusters (ids) | read | `?[cluster_id, cluster_label] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], cluster_id != null, cluster_id != "" :limit {limit}` | none | 2 | TRIVIAL (**null-safe `!= null` on optional col**) |
| C3 | 357-361 | load_precomputed_clusters (members) | read | `?[qualified_name, file_path] := *code_elements[...], cluster_id = "{safe_cid}" :limit 40` (interp, backslash/quote-escaped) | — | 2 | TRIVIAL |

### 2.8 src/embeddings/state.rs (13 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| E1 | 50 | ensure_embedding_state_table | read | `::relations` | — | name | TRIVIAL |
| E2 | 60 | ensure | DDL | `:create embedding_state {qualified_name: String => usearch_key: Int, content_hash: String, state: String, embedded_at: String}` | — | — | MODERATE (keyed) |
| E3-E5 | 62 | ensure | DDL | `::index create embedding_state:qn_index { qualified_name }` / `:usearch_key_index { usearch_key }` / `:state_index { state }` | — | — | MODERATE |
| E6 | 74 | ensure | DDL | `:create embedding_vectors {qualified_name: String => vector: <F32; 384>}` | — | — | MODERATE (vector type!) |
| E7 | 82 | ensure | **::hnsw create** | `::hnsw create embedding_vectors:vec_idx { dim: 384, dtype: F32, fields: [vector], distance: Cosine, ef_construction: {ef_construction}, m: {m}, extend_candidates: false, keep_pruned_connections: false }` (format!) | — | — | **HAND-WRITE** (ANN DDL — pgvector `CREATE INDEX ... USING hnsw` analog) |
| E8 | 103-107 | drop_hnsw_index | **::hnsw drop** | `::hnsw drop embedding_vectors:vec_idx` | — | — | **HAND-WRITE** |
| E9 | 116 | create_hnsw_index | **::hnsw create** | same as E7 | — | — | **HAND-WRITE** |
| E10 | 231-234 | mark_stale_for_qualified_names | :put (literal) | `?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [{values}] :put embedding_state {{...5 cols}}` (values = `["qn", 0, "", "stale", "now"]` inline literals, chunk 500; usearch_key always 0) | none | — | MODERATE (inline literal list) |
| E11 | 244 | list_stale | read | `?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[...], state != "fresh"` | — | 5 | TRIVIAL |
| E12 | 257-261 | list_orphans | **negated cross-relation** | `?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[...], not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]` | — | 5 | **HAND-WRITE** (NOT EXISTS against code_elements, 13-col arity) |
| E13 | 273 | list_all | read | `?[qualified_name, usearch_key, content_hash, state, embedded_at] := *embedding_state[...]` | — | 5 | TRIVIAL |
| E14 | 285 | has_any | read | `?[qualified_name] := *embedding_state[...] :limit 1` | — | 1 | TRIVIAL |
| E15 | 335 | upsert_fresh | import_relations | — (cozo NamedRows API, not a script) | — | — | no translator needed |
| E16 | 356-358 | delete_state_rows | :rm (literal) | `?[qualified_name] <- [{values}] :rm embedding_state {{qualified_name}}` (inline literals chunk 500; key-only rm) | — | — | TRIVIAL |

### 2.9 src/embeddings/build.rs (6 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| B1 | 1401-1404 | put_pairs_to_db_script | :put (literal) | `?[qualified_name, vector] <- [{values}] :put embedding_vectors {{qualified_name => vector}}` (values = `["qn", vec([...384 floats...])]`, chunk `effective_upsert_chunk()`; used only when HNSW live) | none | — | **HAND-WRITE** (vector literal `vec([...])` — pgvector `'[...]'` cast) |
| B2 | 1472-1474 | remove_vectors | :rm (literal) | `?[qualified_name] <- [{values}] :rm embedding_vectors {{qualified_name}}` | — | — | TRIVIAL |
| B3 | 1481-1484 | count_vectors | read | `?[qualified_name] := *embedding_vectors{qualified_name}` (attr syntax) | — | 1 | TRIVIAL |
| B4 | 1734-1738 | bg-embed poller | read | `?[qualified_name] := *embedding_vectors{qualified_name}` (direct `db.run_script` with `ScriptMutability::Immutable` — bypasses the adapter!) | — | 1 | TRIVIAL (call-surface note: bypasses run_script wrapper) |
| B5 | 1367 / 1452 | upsert_pairs_to_db / upsert_vectors | import_relations | — (bulk write path) | — | — | no translator |
| (tests) | 2266-2272 / 2322-2329 | HNSW query tests | **ANN** | `?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([{vec}]), k: 5, ef: 50, bind_distance: dist }` | — | 2 (dist, qn) | **HAND-WRITE** (test-only but canonical shape) |

### 2.10 src/embeddings/control.rs (1 site)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| CV1 | 168-172 | count_embedding_vectors | read | `?[qualified_name] := *embedding_vectors{qualified_name}` | — | 1 | TRIVIAL |

### 2.11 src/ontology/query.rs (8 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| O1 | 129-132 | search_ontology_nodes | read (in-list + regex) | `?[qualified_name, element_type, name, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], element_type in [{13 quoted types}], regex_matches(file_path, "ontology://")` | — | 4 | TRIVIAL |
| O2 | 212 | expand_ontology_context | read | `?[target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $gid` | gid | 4 | TRIVIAL |
| O3 | 613-616 | load_indexed_code_elements (probe) | read probe | `?[qualified_name] := *code_elements[..., env, ontology_layer] :limit 0` | — | 1 | TRIVIAL |
| O4 | 624-629 | load_indexed_code_elements | read | `?[...12 cols] := *code_elements[..., metadata{tail}], !regex_matches(file_path, "^ontology://") :limit 1` | — | 12 | TRIVIAL |
| O5 | 712 | find_element_by_qualified | read | `?[...12 cols] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], qualified_name = $qn` | qn | 12 | TRIVIAL |
| O6 | 797 | trace_workflow (steps) | read | `?[qualified_name, element_type, name, metadata, env] := *code_elements[...], element_type = "workflow_step", parent_qualified = $wgid` | wgid | 5 | TRIVIAL |
| O7 | 840 | search_workflows | read (regex) | `?[qualified_name, element_type, name, metadata, env] := *code_elements[...], element_type = "workflow", regex_matches(file_path, "ontology://")` | — | 5 | TRIVIAL |
| O8 | 909 | get_ontology_status | read (regex) | `?[qualified_name, element_type, metadata, env] := *code_elements[...], regex_matches(file_path, "ontology://")` | — | 4 | TRIVIAL |

### 2.12 src/mcp/handler.rs (6 direct sites + 1 dynamic preprocessor)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| H1 | 1051-1056 | preprocess_datalog_query (field match) | dynamic read | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[..., metadata, _], regex_matches({field}, "{value}") :limit {limit}` (user input interpolated; 11-col head, `_` for env) | — | 11 | TRIVIAL (user-driven) |
| H2 | 1061-1066 | preprocess_datalog_query (free search) | dynamic read | `?[...11 cols] := *code_elements[..., metadata, _], regex_matches(name, "{term}") :limit 50` | — | 11 | TRIVIAL |
| H3 | 2782 | run_raw_query (tool) | passthrough | `self.graph_engine.run_raw_query(&processed_query, params)` — arbitrary | arbitrary | arbitrary | HAND-WRITE (pass-through, must fence) |
| H4 | 3819 | delete_ontology_concept (steps) | read | `?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], element_type = "workflow_step", parent_qualified = $gid` | gid | 1 | TRIVIAL |
| H5 | 3914-3917 | index_prd (wf match) | read (fn) | `?[qualified_name, parent_qualified, metadata] := *code_elements[...], element_type = "workflow_step", str_contains(metadata, "{}")` (file_part interpolated — no escaping!) | — | 3 | TRIVIAL (fn filter, unescaped interp) |
| H6 | 4007-4010 | get_feature_flow (fallback) | read | `?[qualified_name, name] := *code_elements[qualified_name, element_type, name, _, _, _, _, _, _, _, _, _, _], element_type = "workflow", name = "{}"` (wf_id interpolated — no escaping!) | — | 2 | TRIVIAL (unescaped interp) |

### 2.13 src/mcp/tracking_db.rs (1 site)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| T1 | 24 | TrackingDb::run_script | passthrough | forwards to `crate::db::schema::run_script` after `is_write_operation` check (contains `:put` / `:delete`) | — | — | wrapper — no own query strings |

### 2.14 src/indexer/content_hash.rs (2 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| CH1 | 67-69 | load_hashes | read | `?[path, hash] <- index_hashes[path, hash]` (note: `<-` in a read rule!) | — | 2 | TRIVIAL (syntax oddity) |
| CH2 | 93 | save_hashes | :put | `:put index_hashes {path, hash} <- $args` (params binding, per-row loop) | args = {path, hash} | — | MODERATE (keyed relation, auto-created — no DDL) |

### 2.15 src/indexer/mod.rs (1 site)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| IM1 | 1419 | mark_files_stale | read (in-list) | `?[qualified_name] := *code_elements[qualified_name, _, _, file_path, _, _, _, _, _, _, _, _, _], file_path in $fps` | fps: string[] | 1 | TRIVIAL (13-col arity hard-coded) |

### 2.16 src/main.rs (2 sites)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| M1 | 5333 | show_env_conflicts (rels) | read (regex) | `?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[...], rel_type = "conflicts_with", (regex_matches(lowercase(source_qualified), $svc) or regex_matches(lowercase(target_qualified), $svc))` | svc: string (regex) | 5 | TRIVIAL |
| M2 | 5360 | show_env_conflicts (envs) | **:group + :order agg** | `?[qualified_name, env, count(n)] := *code_elements[n, a, b, qualified_name, c, d, e, f, g, h, env, _] :group [qualified_name, env] :order count(n) desc` | — | 3 | **HAND-WRITE** (explicit `:group`, `:order count(n) desc`) |

### 2.17 src/web/handlers.rs (1 site)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| W1 | 3189 | api_query | passthrough | `engine.run_raw_query(&req.query, req.params.clone())` — arbitrary HTTP API query | arbitrary | arbitrary | HAND-WRITE (must fence) |

### 2.18 src/retrieval/pipeline.rs (1 site)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| R1 | 361-371 | hnsw_retrieve | **ANN** | `?[dist, qualified_name] := ~embedding_vectors:vec_idx { qualified_name | query: vec([{vec_literal}]), k: {k}, ef: {ef}, bind_distance: dist }` (ef from `resolve_ef(k)`, LEANKG_HNSW_EF env) | none | 2 (dist, qn) | **HAND-WRITE** (deep-coupling ANN — pgvector `<=>` operator with `ORDER BY` + `LIMIT k`; note Cozo returns rows ASC by distance) |

### 2.19 src/doc_indexer/paths.rs (2 sites — inside `#[cfg(test)] mod tests`)

| # | line | fn | kind | query | params | out | class |
|---|---|---|---|---|---|---|---|
| DOC1 | 377-384 | graph_with_doc_and_file (test) | :put (literal) | `?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <- [ ["./src/widget.rs", "file", ...], [...] ] :put code_elements {12 cols}` | — | — | TRIVIAL (test-only) |
| DOC2 | 451-462 | graph_with_symbols (test) | :put (literal) | same shape, 6 rows | — | — | TRIVIAL (test-only) |

---

## Section 3 — HAND-WRITE list (the non-mechanical queries)

Verbatim strings (whitespace as in source; `{tail}` = `, env, ontology_layer` or `, env` selected at runtime).

### H1. ANN retrieval — src/retrieval/pipeline.rs:361-371 (`hnsw_retrieve`)
```
?[dist, qualified_name] := ~embedding_vectors:vec_idx {
        qualified_name |
        query: vec([{vec_literal}]),
        k: {k},
        ef: {ef},
        bind_distance: dist
    }
```
`{vec_literal}` = 384 floats `{:.6}` joined. `{ef}` = `resolve_ef(k)` (env `LEANKG_HNSW_EF`). Out: `(dist: float, qualified_name: string)` — rows ascending by distance. pgvector: `SELECT qualified_name, vector <=> $q AS dist FROM embedding_vectors ORDER BY dist LIMIT k`.

### H2. HNSW index DDL — src/embeddings/state.rs:132-155 (`build_hnsw_create_stmt`) + drop at state.rs:105
```
::hnsw create embedding_vectors:vec_idx {
    dim: 384,
    dtype: F32,
    fields: [vector],
    distance: Cosine,
    ef_construction: {ef_construction},
    m: {m},
    extend_candidates: false,
    keep_pruned_connections: false
}
```
`{ef_construction}` = env `LEANKG_HNSW_EF_CONST` (default 20), `{m}` = env `LEANKG_HNSW_M` (default 50). Drop: `::hnsw drop embedding_vectors:vec_idx`. pgvector: `CREATE INDEX ... USING hnsw (vector vector_cosine_ops) WITH (m = ..., ef_construction = ...)` / `DROP INDEX`. Note: HNSW distance is **Cosine** (pgvector `vector_cosine_ops`), not inner product.

### H3. Cross-relation negated rule — src/embeddings/state.rs:257-261 (`list_orphans`)
```
?[qualified_name, usearch_key, content_hash, state, embedded_at] :=
    *embedding_state[qualified_name, usearch_key, content_hash, state, embedded_at],
    not *code_elements[qualified_name, _, _, _, _, _, _, _, _, _, _, _, _]
```
`NOT EXISTS (SELECT 1 FROM code_elements WHERE code_elements.qualified_name = embedding_state.qualified_name)`.

### H4. Cross-relation join + :rm — src/graph/query.rs:2524-2532 (`clear_ontology_layer`)
```
?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
    *code_elements[source_qualified, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
    regex_matches(file_path, "^ontology://")
:rm relationships {{source_qualified, target_qualified, rel_type, confidence, metadata}}
```
`DELETE FROM relationships WHERE source_qualified IN (SELECT qualified_name FROM code_elements WHERE file_path ~ '^ontology://')`.

### H5. `:group` + `:order count(n) desc` — src/main.rs:5360 (`show_env_conflicts`)
```
?[qualified_name, env, count(n)] := *code_elements[n, a, b, qualified_name, c, d, e, f, g, h, env, _] :group [qualified_name, env] :order count(n) desc
```
`SELECT qualified_name, env, count(*) FROM code_elements GROUP BY qualified_name, env ORDER BY count(*) DESC`. NOTE: head order is `[qualified_name, env, count]` and column 3 is `file_path` in the relation but rebound to `qualified_name` — the positional rebinding is the Cozo way of projecting.

### H6. Two-rule count script — src/graph/query.rs:3606-3609 (`count_files`)
```
files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j{tail}]
?[count(f)] := files[f]
```
`SELECT count(DISTINCT file_path) FROM code_elements` — Cozo rule `files` dedupes `f` (file_path) implicitly. Distinct matters: 4th column of code_elements bound as `f`.

### H7. `:delete` with embedded SQL subquery — src/graph/persistent_cache.rs:177-184 (`evict_from_db`)
```
:delete query_cache
where cache_key in (
    select cache_key from query_cache
    order by created_at asc
    limit $count
)
```
This is literal SQLite syntax passed through Cozo's `:delete` — translator can map to `DELETE FROM query_cache WHERE cache_key IN (SELECT cache_key FROM query_cache ORDER BY created_at ASC LIMIT $count)`. Assumption-violation: the plan claims no SQL is embedded in scripts; this one embeds SQL.

### H8. `PRAGMA page_count` — src/graph/persistent_cache.rs:322 (`database_size_approx`)
`PRAGMA page_count` — SQLite-only; PG equivalent `pg_database_size()` / `pg_relation_size`. Also `VACUUM` (graph/query.rs:144) — PG `VACUUM` is a different (autovacuum-managed) operation; the method is a no-op or needs `VACUUM (ANALYZE)` semantics.

### H9. Raw user query pass-through — 3 surfaces
- `GraphEngine::run_raw_query` (src/graph/query.rs:508-517) — used by MCP tool `run_raw_query` (handler.rs:2768-2799, incl. `preprocess_datalog_query` at 1009-1071 which synthesizes 2 more query shapes) and web API `api_query` (web/handlers.rs:3184-3209). Arbitrary Datalog. Phase 1 must either translate dynamically or reject with a clear error.
- `TrackingDb::run_script` (mcp/tracking_db.rs:16-25) — wraps schema::run_script, marks dirty on `:put`/`:delete`; feeds the MCP write-tracker.

### H10. Vector-literal `:put` — src/embeddings/build.rs:1401-1404 (`put_pairs_to_db_script`, HNSW-live path only)
```
?[qualified_name, vector] <- [{values_clause}]
   :put embedding_vectors {{qualified_name => vector}}
```
`{values_clause}` = `["qn", vec([0.123456, ...])]` × N (N = `effective_upsert_chunk()`), floats formatted `{:.6}`. pgvector: `INSERT INTO embedding_vectors (qualified_name, vector) VALUES ($1, '[0.123456,...]') ON CONFLICT (qualified_name) DO UPDATE`. The `:rm` twin (build.rs:1472-1474) is `?[qualified_name] <- [{values}] :rm embedding_vectors {{qualified_name}}` — key-only rm on keyed table (trivial).

### H11. `:replace` schema repairs — src/db/schema.rs:659-677 (see §1.4) — the 3 repair scripts. Also the 3 aggregate `:order -count(...)` queries (G98/G102/G105) are effectively HAND-WRITE if the translator cannot do `GROUP BY` + `ORDER BY` on the aggregate — reclassify from MODERATE if so.

### H12. Dynamic "search code" (handler.rs:1051-1066) — user-input `regex_matches({field}, "{value}")` shapes. Must be treated as untrusted dynamic SQL generation.

---

## Section 4 — Call-surface inventory for the DbBackend trait (Phase 1)

### 4.1 Handle producers (must all route through DbBackend)

| site | file:line | how obtained |
|---|---|---|
| `init_db` (fn def) | src/db/schema.rs:190 | `cozo::DbInstance::new("sqlite"\|"rocksdb", path, opts)` + `init_schema` |
| `init_db_readonly` (fn def) | src/db/schema.rs:114 | sqlite `mode=ro`; rocksdb same-as-writer (documented workaround) |
| `run_script` adapter (fn def) | src/db/schema.rs:25 | wraps `db.run_script(query, params, mutability_for(query))`; `mutability_for` scans for `:put :rm :create :replace :delete :update :insert PRAGMA ::set_triggers ::hnsw ::lsh ::fts ::index` |
| `GraphEngine::new / with_cache / with_persistence / open_readonly` | src/graph/query.rs:74/88/101/133 | wraps `CozoDb` in `Arc<CozoDb>`; `open_readonly` calls `init_db_readonly` |
| `GraphEngine::db()` / `db_arc()` | src/graph/query.rs:116/123 | the single accessor every `run_script(&self.db, ...)` uses |
| `ApiKeyStore::init_db` | src/db/keys.rs:36-55 | **separate keys.db** — its own `cozo::DbInstance::new("sqlite", ~/.leankg/keys.db)` — must be a second DbBackend instance (different file) |
| `MCPServer::get_graph_engine_for_path` | src/mcp/server.rs:498-513 | path-keyed cache + `init_db`/`init_db_readonly` (read_only flag) |
| `MCPServer::get_graph_engine` | src/mcp/server.rs:519-526 | routes through the cache |
| Web `AppState::init_db` / `get_graph_engine` | src/web/mod.rs:156/166/196/200/219-220 | `init_db` + `GraphEngine::new` |
| API (REST) `ApiState::init_db` | src/api/mod.rs:35-37 | `init_db` + `GraphEngine::new` |
| `GraphEngine::open_readonly` callers | src/ctags_export.rs:142, src/pack/mod.rs:68 | `init_db_readonly` |
| main.rs CLI | src/main.rs:105,156,642,702,775,815,1218,1526,1700,1853,1868,1903,1946,1980,2003,2031,2062,2101,2331,2520,2577,2606,2659,2686,2715,2804,2836 | all `db::schema::init_db(&db_path)` then `GraphEngine::new(db)` |
| other `init_db` users | src/cost_estimate.rs:173, src/benchmark/{ab_test.rs:133, tool_bench.rs:92, unified.rs:621}, src/conversation_indexer/mod.rs:157, src/obsidian/sync.rs:32, src/ontology/sync.rs:381/419/468, src/orchestrator/mod.rs:390/410, src/pack/mod.rs:226/242/263/283/308, src/report/write.rs:16 | all `init_db` + `GraphEngine::new` |
| `TrackingDb` | src/mcp/tracking_db.rs:12 | wraps `CozoDb` + `WriteTracker` |
| `PersistentCache::new` | src/graph/persistent_cache.rs:46 | takes `Arc<CozoDb>` (from `QueryCache::with_persistence`, graph/cache.rs:171-181, called only from `GraphEngine::with_persistence`) |
| `QueryCache::with_persistence` | src/graph/cache.rs:171 | `Arc<CozoDb>` |

### 4.2 Direct `cozo::DbInstance::new` outside schema.rs
- `src/db/keys.rs:38` (`ApiKeyStore::init_db`) — the ONLY one. All other modules go through `init_db`/`init_db_readonly`/`GraphEngine`.

### 4.3 Direct `db.` method calls (bypassing the run_script adapter)
- `db.import_relations(map)` — src/embeddings/build.rs:1367 (bulk vector upsert), src/embeddings/build.rs:1452 (`upsert_vectors`), src/embeddings/state.rs:335 (`upsert_fresh`). These are bulk NamedRows imports; DbBackend needs an `import_relations` equivalent (COPY / multi-row INSERT).
- `poller_graph.db().run_script("?[qualified_name] := *embedding_vectors{qualified_name}", map, ScriptMutability::Immutable)` — src/embeddings/build.rs:1732-1738 — direct 3-arg call bypassing the 2-arg adapter.

### 4.4 Indirect query executors (no direct script strings but DB-backed)
- `PersistentCache` (persistent_cache.rs) — used by `QueryCache::with_persistence`, wired into `GraphEngine::with_persistence` (server.rs:510 uses it).
- All `db::*` helpers in src/db/mod.rs (business_logic, knowledge_entries, incidents, metrics, teams, service_metadata, feature_workflow_links) — used by mcp/handler.rs, web/handlers.rs, obsidian/sync.rs, main.rs.

---

## Section 5 — Risk notes (assumption violations vs plan §2.1 "single-relation equality")

1. **`index_hashes` table has NO `:create` DDL anywhere** (src/indexer/content_hash.rs:68/93). Cozo auto-creates on first `:put`. The PG schema must add it explicitly: `{path: String => hash: String}`. Missing from plan §2.2.
2. **Regex filters are pervasive** (~40 reads): `regex_matches(...)` (G24, G54-G56, G60, G68, G71, G72, G90, G95, O1, O4, O7, O8, M1, D7, D21, D33...), `str_includes` (G62, G63), `str_contains` (H5), `starts_with` (G94). NOT "equality filters". Each maps to PG `~` / `LIKE` / `POSITION` — mechanical but not `=`-only. Some patterns are interpolated, some parameterized — see risk 7.
3. **Null-safe equality semantics**: `revoked_at = null` (K7, keys.rs:201) — in Cozo this matches NULL (Cozo's `=` with null literal binds nulls); in PG must be `IS NULL`. Conversely `cluster_id != null` (C2) and `cluster_id != null, cluster_id != ""` (G100) must become `IS NOT NULL`. Also `not *code_elements[...]` (E12) is a NOT EXISTS anti-join. And `?[..] := *business_logic[...], user_story_id = $us` where `$us` is a non-null string — safe; but `get_by_user_story`-style filters on nullable cols with null params would need `IS NULL` handling (currently none pass null params — params are always Some).
4. **`?`-optional columns**: no query reads an optional column and compares it with `?` syntax — optionals (parent_qualified, cluster_id, cluster_label, user_story_id, feature_id, element_qualified, branch, resolved_at, trigger_pattern, prevention, linked_ticket, correct_elements, total_expected, f1_score, query_pattern, query_file, query_depth, last_used_at, revoked_at, email, accepted_by) appear only as bound columns in full-row heads or in `:put`/`:rm` params with null values. The `:put` null semantics (DataValue::Null, not Bot — schema.rs:37-43) must be preserved: PG `NULL`.
5. **`:group` + `:order` combos**: exactly 1 (M2, main.rs:5360, `:group [qualified_name, env] :order count(n) desc`) + 5 implicit aggregate-with-order (`:order -count(...)`: G98, G102, G105, G106; plus G107 `:order -span` on a computed column). All are `GROUP BY` + `ORDER BY` in PG.
6. **Keyed-table semantics**: `embedding_state` (key `qualified_name`), `embedding_vectors` (key `qualified_name`), `index_inventory` (key `key`). `:put` on keyed tables = UPSERT (documented at state.rs:69-72, inventory.rs:133-135 `key => computed_at`). `:rm embedding_vectors {qualified_name}` / `:rm embedding_state {qualified_name}` are key-only deletes. `insert_element` (G43) and `insert_elements_with` (G42) :put on **code_elements which is NOT keyed** — Cozo keys the full tuple; the same qualified_name with different metadata creates duplicate rows (documented at query.rs:2420-2422 "Cozo :put keys the full tuple, so renames leave duplicate GID rows"). **The PG translator MUST NOT add a PK on qualified_name for code_elements** — that would silently change semantics (callers rely on delete-then-insert via G53/G44). PG keyed tables get `PRIMARY KEY`; unkeyed tables get none (or a surrogate).
7. **String-interpolated (unescaped or semi-escaped) values** — these are injection-adjacent today and become SQL-injection-adjacent after translation; translator must convert to bound params:
   - `keys.rs:212`: `:delete api_keys where id = "{key_id}"` — completely unescaped.
   - `handler.rs:3915` (H5) `str_contains(metadata, "{file_part}")` and `handler.rs:4008` (H6) `name = "{wf_id}"` — unescaped.
   - Escaped-but-interpolated: G14/G26/G73 (escape_datalog), G60/G63/G68/G70/G71/G78/G79 (escape_datalog + regex::escape), G19/G61/G64/G65/G66/G89/G90/G91/G92/G93/G94/G95/G96/G97 (escape_datalog only), D7 (format! with `.*{}.*`, no escape!), D21 (same), D33 (regex::escape).
8. **Attribute-binding syntax**: `*relation{...}` (D39, B3, B4, CV1, R1-ish) and `*query_cache[cache_key = $key, ...]` (P2) — Cozo attribute syntax; both are sugar for positional. D39 uses `==` (equality) instead of `=`.
9. **`?[path, hash] <- index_hashes[path, hash]`** (CH1) — `<-` in a read rule (no `:=`); the plan's translator regexes on `:=` and will miss it.
10. **Row-positional consumption**: out-cols order is load-bearing everywhere (e.g. D9/D40 swap metadata/confidence order; G107 head `span` computed last; M2 head `[qualified_name, env, count]`; R1 `[dist, qualified_name]`). Translator must preserve head order exactly, not column order.
11. **Arity probes**: `code_elements_tail()` (G2, and duplicates at D34, C1, O3, schema.rs:684-702) probes 13-col vs 12-col by attempting `:limit 0` queries and catching errors. In PG, this probe pattern (try-query-fail) must be replaced by a schema introspection query (information_schema) — the tail-param mechanism (`{tail}`) then becomes constant `, env, ontology_layer`.
12. **`VACUUM`** (G1) and **`PRAGMA page_count`** (P5) are SQLite engine commands — PG has `VACUUM` but different semantics, and no `page_count` equivalent.
13. **`::relations` introspection** (S1, S49, I1, E1, K1) — replace with `information_schema.tables`. Used in init_schema to decide whether to create tables — the PG migration must keep this idempotent path.
14. **`:schema {relation}`** (S44, S55, S56) — returns schema rows; PG equivalent `information_schema.columns` (used only for warnings, can be adapted).
15. **`:delete ... where ...`** (7 sites: D15, D17, D27, D32, D42, D47, P1, P4) — Cozo-specific `:delete` operator; maps to `DELETE FROM ... WHERE`.
16. **Cozo `:limit`+`:offset`** — G10, G13-G15, G20, G22, G24, G32-G34, G83, G90, G102: straightforward `LIMIT/OFFSET`. But note G20/G22/G24/G32-G34 fetch a page then filter in Rust (`has_more = len == limit`) — PG LIMIT/OFFSET preserves this behavior.
17. **HNSW distance is Cosine** (state.rs:147) — pgvector default opclass for `vector` is inner product for `<=>`; must use `vector_cosine_ops` explicitly in the index AND `<=>` for query to get cosine.
18. **`import_relations` skips HNSW maintenance** (documented build.rs:1415-1417) and `:put` maintains it — the PG translator must reproduce the "bulk path drops index, bulk-loads, rebuilds index" pattern (state.rs drop_hnsw_index/create_hnsw_index + build.rs upsert path). pgvector ivfflat/hnsw index maintenance on bulk COPY is likewise deferred — same pattern applies.
19. **MCP `run_raw_query` + `api_query` pass-through arbitrary Datalog** — these are client-supplied queries; the translator cannot be mechanical here. Phase 1 must either implement a mini-translator for the 2 synthesized shapes (H1/H2 in §2.12) and reject everything else, or run these against the legacy engine.
20. **`embedding_state.usearch_key` legacy column** — written as 0 (state.rs:216-219), read positionally at index 1. Schema-compat only; PG can keep it as `BIGINT NOT NULL DEFAULT 0`.
21. **count queries on `code_elements` bind 11 positional vars + tail** (`n, a, b, c, ...`) — after PG migration the arity probe disappears, and all such queries become `SELECT count(*) FROM code_elements [WHERE ...]`.
22. **`files[f] := ...` intermediate rule** (H6/G88) — Cozo materialized-rule dedup; `count(DISTINCT file_path)` in PG.
23. **MCP handler searches on `metadata` with `str_contains`** (H5) — metadata is a JSON string column; PG `LIKE '%' || $1 || '%'` on the text column, or `jsonb` text search if converted.

---

## Section 6 — Test/bench/e2e query sites (counted, excluded from inventory)

- `tests/` — 14 files reference `run_script`/`run_raw_query`; e.g. tests/mcp_tools_full_tests.rs:35/47/796-800 (seeded `:put code_elements`, `:put relationships`, `run_raw_query` tool). `tests/` total ~17 query strings.
- `e2e/` — 0.
- `benches/` — 1 file (bench/cache or similar).
- `src/**/` inline `#[cfg(test)]` modules also contain query strings (e.g. schema.rs tests, state.rs `has_any` test, build.rs HNSW ANN test queries at 2266-2272/2322-2329, doc_indexer/paths.rs DOC1/DOC2). These count toward the "tests" bucket but live in src — the translation harness (tests) must be updated alongside.

---

## Verification note

Every query string above was read from source in the worktree at commit f1b50d59; line numbers are exact. The plan's §2.1 table (which I was told not to trust) undercounts: it lists ~30 queries; actual distinct query strings ≈ 115, with regex/in-list/range filters dominating the read side and 6-11 queries needing hand translation (depending on whether aggregate `:order -count` counts as mechanical GROUP BY).
