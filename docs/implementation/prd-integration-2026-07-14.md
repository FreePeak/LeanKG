# LeanKG PRD Integration — Status 2026-07-14

Snapshot of where the integration branch (`integration/prd-pending`)
stands. All paths in this document refer to the LeanKG source tree —
the large workspace used for runtime smoke-testing is **not** named in
this document and was used only as a non-committed sandbox.

## CI gate

| Check | Status |
|-------|--------|
| `cargo fmt --all -- --check` | PASS |
| `cargo clippy --release --all-targets -- -D warnings` | PASS |
| `cargo test --release --lib` | PASS (496 lib tests) |
| `cargo test --release --bin leankg` | PASS (491 bin tests) |
| `cargo test --release --test ontology_e2e` | PASS (16/16) |

PR #72 — Format and Clippy checks unblocked.

## Concept + procedural ontology tests

Added `tests/ontology_e2e.rs` (16 cases):

1. `e2e_ontology_concepts_yaml_loads_and_parses` — built-in `ontology/concepts.yaml` loads, every GID parses, `ontology_type` matches `element_type`, `env` matches.
2. `e2e_ontology_workflows_yaml_loads_and_parses` — built-in `ontology/workflows.yaml` loads every step's `workflow_gid` resolves to a known workflow, every failure-mode GID parses with `ontology_type=failure_mode`.
3. `e2e_concept_element_types_roundtrip` — all 8 concept element kinds roundtrip via `as_str`/`from_str`; unknown inputs return `None`.
4. `e2e_procedural_element_types_roundtrip` — all 5 procedural element kinds roundtrip.
5. `e2e_concept_relationship_types_roundtrip` — all 8 concept relationship kinds.
6. `e2e_procedural_relationship_types_roundtrip` — all 6 procedural relationship kinds.
7. `e2e_concept_domain_entity_node` — `ConceptNode::domain_entity` produces the expected GID `local:checkout-service:domain_entity:refund:v1`, `ontology=concept`, `ontology_layer=domain`, `stale=false`.
8. `e2e_concept_service_node_with_owner_and_code_refs` — `ConceptMetadata::with_owned_by` + `with_code_refs` persist into the node.
9. `e2e_concept_known_issue_with_playbook` — `ConceptNode::known_issue` factory produces GID with `known_issue` segment.
10. `e2e_procedural_workflow_with_entry_points_and_step_count` — `WorkflowNode::with_entry_points`/`with_step_count`/`with_source` persist.
11. `e2e_procedural_workflow_step_links_to_parent_workflow` — `WorkflowStepMetadata::workflow_gid` matches the parent workflow's GID exactly.
12. `e2e_procedural_failure_mode_with_playbook_handler` — `FailureModeNode::with_handled_by` persists the playbook-step GID list.
13. `e2e_normalize_alias_handles_casing_and_punctuation` — casing, leading/trailing whitespace, hyphen and `::` punctuation.
14. `e2e_normalize_aliases_iterates` — bulk normalization over `Vec<String>`.
15. `e2e_workflow_metadata_with_aliases_dedupes` — `WorkflowMetadata::with_aliases` normalizes inputs.
16. `e2e_concept_and_procedural_share_env_scope_format` — the concept-service GID and the workflow GID share the same `(env, scope, id)` prefix once the `ontology_type` segment is stripped, proving both layers address the same domain scope.

## CI lint remediation

Resolved every blocking clippy complaint. Categories:

- **unused imports / variables** — `HashMap` (lsp/client.rs), `AuthManager` (tests/mcp_tests.rs), `serde_json::json` (tests/test_check_imports.rs, tests/test_diagnose_empty3.rs), `_i` loop counters (orchestrator_bench.rs), `_t0` timing markers (load_test_1m_nodes.rs), and many test-only variables.
- **borrow / deref patterns** — `needless_borrows_for_generic_args` at data_store_tests.rs:113, v2_env_incidents_tests.rs:40/62.
- **length comparisons to zero / one** — replaced with `!is_empty()` everywhere.
- **useless `vec!`** — graph/query.rs:5911 swapped for `[a.clone(), b.clone()]` with `sort_by_key(Reverse(...))`.
- **items after `mod tests`** — relocated `jaccard_tokens` helper above the test module in graph/query.rs.
- **expect_fun_call** — `xml_extraction_tests.rs:163` swapped `expect(&format!(...))` for `unwrap_or_else(|_| panic!(...))`.
- **`print_literal`** in benches/orchestrator_real_bench.rs:301/330 — removed empty `{}` format wrappers.

Plus `cargo fmt --all` was applied across all touched files.

## Runtime smoke test

The release binary built from this commit was used to index, query,
and traverse a large multi-repo Go/TypeScript/Python workspace:

| Tool | Result |
|------|--------|
| `init` | Detected languages: `go, typescript, javascript, python`. |
| `index .` | 21,762 files → 598,447 elements, 1,077,849 relationships. |
| `status` | 369,158 functions, 30,407 classes indexed. |
| `query` (name + kind content + kind type) | Returns relevant matches. |
| `explain` | Reports `in_degree` / `out_degree` for chosen nodes. |
| `impact` | Walks references transitively up to depth N. |
| `gods` | Top god-nodes by `degree`. |
| `tunnels` | Cross-cluster tunnels. |
| `check-consistency --severity BROKEN` | Flags missing-target links. |
| `export --format json` | Produces a valid JSON edges/nodes file. |
| `reflect "<q>" useful` | Persists a `useful` lesson row. |
| `lsp-resolve` | Reports "no LSP configured" and falls back to tree-sitter (expected when no server is declared). |

No crashes observed. All access paths exercised end-to-end against
real binary data.

## Commits added on this branch in the last push

```
6f0a235 fix(lint): resolve clippy + fmt warnings across crates for CI gate
64b0fa6 feat(cbm-b1): LSP MCP/CLI wiring + typed_resolve aliases + e2e tests
534cd7f feat(cbm-b1): LSP bridge for typed resolve (multi-repo + nested dirs)
```

(Plus earlier commits for Graphify, MemPalace, Mass-Graph UI,
GitNexus, language breadth, team infra, distribution — see `docs/prd.md`
for the full backlog.)

## Open follow-ups

- LSP server bootstrap: ship a default `lsp:` block (gopls + tsserver +
  pyright) so out-of-the-box `typed_resolve=all` returns live data.
- Mega-graph guard tuning: `LEANKG_MAX_CACHE_ELEMENTS` is already
  honored (cache skipped at 627k elements / 1.07M rels in smoke test).
- Conflict-of-locks documentation: see
  `docs/analysis/mcp-http-stability-analysis-2026-05-05.md`.

