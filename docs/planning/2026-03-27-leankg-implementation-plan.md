# LeanKG Implementation Plan

**Date:** 2026-03-27
**Version:** 1.0
**Status:** Draft
**Based on:** 
- `docs/requirement/prd-leankg.md` (v1.7)
- `docs/requirement/prd-leankg-v2.0-enhancements.md`
- `docs/requirement/prd-leankg-gitnexus-enhancements.md`
- `docs/analysis/gitnexus-analysis-2026-03-27.md`
- `docs/implementation-feature-verification-2026-03-25.md`

---

## 1. Current Status Summary

### Completed (per verification report)
- Core MVP: indexing, CLI, MCP server, documentation generation, impact analysis
- Phase 2 features: pipeline extraction, doc-structure mapping, business logic tagging
- v1.10+: signature_only mode, bounded call graph, query push-down, injection safety
- v1.14+: Web UI embedded in binary

### Remaining Work

#### v2.0 Enhancements (US-19 to US-27)
| ID | Requirement | FR | Priority | Status |
|----|-------------|-----|----------|--------|
| US-19 | Cross-file call edge resolution | FR-77, FR-78, FR-79 | P1 | Pending |
| US-20 | Go implements edge fix | FR-80 | P1 | Pending |
| US-21 | Query push-down + injection safety | FR-81, FR-82, FR-83, FR-84 | P0/P1 | Partial |
| US-22 | signature_only context mode | FR-85, FR-86, FR-87, FR-88 | P2 | Partial |
| US-23 | Bounded call graph | FR-89, FR-90 | P2 | Completed |
| US-24 | get_documented_by fix | FR-91 | P0 | Pending |
| US-25 | mcp_index_docs tool | FR-92, FR-93 | P1 | Completed |
| US-26 | Doc reference extraction fix | FR-94, FR-95 | P2 | Pending |
| US-27 | MCP tool schema quality | FR-96, FR-97 | P2 | Partial |

#### GitNexus-Inspired Enhancements (v0.3.0)
| ID | Requirement | Priority | Status |
|----|-------------|----------|--------|
| US-GN-01 | Confidence scoring on relationships | Must Have | Pending |
| US-GN-02 | detect_changes tool | Must Have | Pending |
| US-GN-03 | Multi-repo global registry | Should Have | Pending |
| US-GN-04 | Cluster-grouped search | Should Have | Pending |
| US-GN-05 | Community detection | Should Have | Pending |
| US-GN-06 | Enhanced 360-degree context | Should Have | Pending |
| US-GN-07 | Cluster-level skills generation | Could Have | Pending |
| US-GN-08 | MCP Resources | Could Have | Pending |

---

## 2. Implementation Phases

### Phase 1: v2.0 Critical Fixes (Week 1)

**Goal:** Fix correctness failures and complete incomplete v2.0 items

#### 1.1 US-24: Fix get_documented_by query direction (P0)
- **FR-91:** Fix filter direction in `graph/query.rs`
- **File:** `src/graph/query.rs:382`
- **Action:** Change `target_qualified = element` to `source_qualified = element`

#### 1.2 US-19: Cross-file call edge resolution (P1)
- **FR-77:** Store unresolved calls with `__unresolved__` prefix
- **FR-78:** Implement `resolve_call_edges` post-index resolution pass
- **FR-79:** Update `is_noise_call` filter for stdlib functions
- **Files:** `src/indexer/extractor.rs`, new `src/indexer/resolution.rs`

#### 1.3 US-20: Go implements edge fix (P1)
- **FR-80:** Only emit `implements` for anonymous/embedded fields
- **File:** `src/indexer/extractor.rs:267-301`

#### 1.4 US-26: Doc reference extraction fix (P2)
- **FR-94:** Fix `extract_code_references` - code block tracking, widen regex
- **FR-95:** Store headings in document metadata
- **File:** `src/doc/indexer/mod.rs`

---

### Phase 2: v2.0 Improvements (Week 2)

**Goal:** Complete remaining v2.0 functional requirements

#### 2.1 US-21: Complete query push-down (P1)
- **FR-82:** Add `search_by_name_typed`, `find_elements_by_name_exact` to GraphEngine
- **FR-83:** Add `run_element_query` helper
- **FR-84:** Fix `get_dependencies` to query relationships table
- **File:** `src/graph/query.rs`, `src/mcp/handler.rs`

#### 2.2 US-22: Complete signature_only mode (P2)
- **FR-85:** Store function signatures in metadata during extraction
- **FR-86:** Add `find_body_start_line` helper
- **FR-87:** Update `get_context` tool schema
- **FR-88:** Update handler for `signature_only` branching
- **Files:** `src/indexer/extractor.rs`, `src/mcp/handler.rs`

#### 2.3 US-27: MCP tool schema quality (P2)
- **FR-96:** Add `required` arrays to all tool schemas
- **FR-97:** Update tool descriptions and defaults
- **File:** `src/mcp/tools.rs`

---

### Phase 3: GitNexus-Inspired Features (Week 3-4)

**Goal:** Implement high-value GitNexus-inspired enhancements

#### 3.1 US-GN-01: Confidence scoring (Must Have)
- **FR-GN-01:** Add `confidence` field to Relationship model
- **FR-GN-02:** Emit confidence scores during call resolution
- **FR-GN-03:** Update `get_impact_radius` response with severity classification
- **FR-GN-04:** Add `min_confidence` parameter
- **Files:** `src/db/models.rs`, `src/indexer/extractor.rs`, `src/graph/traversal.rs`, `src/mcp/tools.rs`

#### 3.2 US-GN-02: detect_changes tool (Must Have)
- **FR-GN-05:** New MCP tool computing diff vs last indexed commit
- **FR-GN-06:** Risk level classification (critical/high/medium/low)
- **FR-GN-07:** Offline operation using local git index
- **Files:** `src/mcp/tools.rs`, `src/mcp/handler.rs`, `src/indexer/git.rs`

#### 3.3 US-GN-03: Multi-repo global registry (Should Have)
- **FR-GN-08:** Create `~/.leankg/registry.json`
- **FR-GN-09:** CLI commands: `register`, `unregister`, `list`, `status`
- **FR-GN-10:** MCP server reads registry, supports `repo` parameter
- **FR-GN-11:** Lazy connection management (max 5 concurrent, 10min idle eviction)
- **FR-GN-12:** Global `leankg setup` for MCP config
- **Files:** `src/config/`, `src/cli/`, `src/mcp/server.rs`

#### 3.4 US-GN-06: Enhanced get_context (Should Have)
- **FR-GN-18:** Include cluster membership and flow participation
- **FR-GN-19:** Add `dependents_count` and `dependencies_count`
- **File:** `src/mcp/handler.rs`

---

### Phase 4: Community Detection (Week 5)

**Goal:** Implement functional clustering for architectural awareness

#### 4.1 US-GN-05: Community detection
- **FR-GN-13:** Implement label propagation or Leiden algorithm
- **FR-GN-14:** Store `cluster_id` and `cluster_label` in CodeElement
- **File:** New `src/graph/clustering.rs`

#### 4.2 US-GN-04: Cluster-grouped search
- **FR-GN-15:** Add `get_clusters` MCP tool
- **FR-GN-16:** Update `search_code` response with cluster info
- **FR-GN-17:** Add `get_cluster_context` MCP tool
- **Files:** `src/mcp/tools.rs`, `src/mcp/handler.rs`

---

### Phase 5: Future Enhancements (Backlog)

| Item | Priority | Notes |
|------|----------|-------|
| US-GN-07: Cluster-level skills generation | Could Have | Depends on community detection |
| US-GN-08: MCP Resources | Could Have | Depends on multi-repo registry |
| US-GN-09: Wiki generation | Won't Have v0.3 | Requires optional LLM dependency |

---

## 3. File Impact Summary

| File | Phase 1 | Phase 2 | Phase 3 | Phase 4 |
|------|---------|---------|---------|---------|
| `src/db/models.rs` | | | X | |
| `src/indexer/extractor.rs` | X | X | X | |
| `src/indexer/resolution.rs` | X (new) | | | |
| `src/indexer/git.rs` | | | X | |
| `src/graph/query.rs` | X | X | | |
| `src/graph/traversal.rs` | | | X | |
| `src/graph/clustering.rs` | | | | X (new) |
| `src/mcp/tools.rs` | | X | X | X |
| `src/mcp/handler.rs` | X | X | X | X |
| `src/mcp/server.rs` | | | X | |
| `src/cli/` | | | X | |
| `src/config/` | | | X | |

---

## 4. Testing Strategy

### Unit Tests
- Each FR has specific acceptance criteria in PRD v2.0
- Run `cargo test` after each implementation

### Integration Tests
- Re-index LeanKG's own codebase after each phase
- Verify MCP tool responses

### Verification Commands
```bash
# Build and test
cargo build && cargo test

# Re-index LeanKG
cargo run -- index ./src

# Verify cross-file calls
cargo run -- impact src/mcp/handler.rs 2

# Verify doc-code cross-edges
# (via MCP) mcp_index_docs { "path": "./docs" }
# (via MCP) get_doc_for_file { "file": "src/indexer/extractor.rs" }

# Verify signature_only
# (via MCP) get_context { "file": "src/mcp/handler.rs" }
```

---

## 5. Implementation Checklist

### Phase 1: v2.0 Critical Fixes
- [ ] US-24: Fix get_documented_by query direction (FR-91)
- [ ] US-19: Cross-file call edge resolution (FR-77, FR-78, FR-79)
- [ ] US-20: Go implements edge fix (FR-80)
- [ ] US-26: Doc reference extraction fix (FR-94, FR-95)

### Phase 2: v2.0 Improvements
- [ ] US-21: Complete query push-down (FR-82, FR-83, FR-84)
- [ ] US-22: Complete signature_only mode (FR-85, FR-86, FR-87, FR-88)
- [ ] US-27: MCP tool schema quality (FR-96, FR-97)

### Phase 3: GitNexus-Inspired
- [ ] US-GN-01: Confidence scoring (FR-GN-01 to FR-GN-04)
- [ ] US-GN-02: detect_changes tool (FR-GN-05 to FR-GN-07)
- [ ] US-GN-03: Multi-repo global registry (FR-GN-08 to FR-GN-12)
- [ ] US-GN-06: Enhanced get_context (FR-GN-18, FR-GN-19)

### Phase 4: Community Detection
- [ ] US-GN-05: Community detection (FR-GN-13, FR-GN-14)
- [ ] US-GN-04: Cluster-grouped search (FR-GN-15 to FR-GN-17)

---

## 6. Notes

- All changes should maintain backward compatibility with existing `.leankg` databases
- New fields should be additive (nullable) to avoid schema migrations
- Follow existing code patterns in the codebase
- Run `cargo clippy -- -D warnings` before committing
- Update CHANGELOG.md after each phase completion
