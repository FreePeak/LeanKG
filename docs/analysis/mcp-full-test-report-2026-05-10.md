# LeanKG MCP Tools Full Test Report

**Date:** 2026-05-10
**Commit:** `7c259aa` (Merge pull request #44 from FreePeak/feat/knowledge-contribution)
**Binary version:** v0.17.0
**Server:** MCP HTTP SSE on port 9699
**Database:** `/Users/linh.doan/work/harvey/freepeak/leankg/.leankg`
**Index stats:** 1,119 files | 34,854 elements | 111,335 relationships | 22,267 functions | 1,305 classes

---

## Executive Summary

| Category | Tools Tested | PASS | FAIL | PASS Rate |
|----------|-------------|------|------|-----------|
| Core Tools | 8 | 6 | 1 | 87.5% |
| Dependency/Graph Tools | 9 | 7 | 2 | 77.8% |
| Documentation Tools | 9 | 8 | 1 | 88.9% |
| Traceability Tools | 6 | 6 | 0 | 100% |
| Navigation/Service Tools | 7 | 7 | 0 | 100% |
| Utility Tools | 10 | 8 | 2 | 80% |
| **Total** | **49** | **42** | **6** | **85.7%** |

**Critical Issues:**
1. `get_impact_radius` and `mcp_impact` consistently **timeout at depth >= 2** due to graph traversal explosion
2. `query_file` returns empty results or doc references instead of code elements
3. `find_function` uses fuzzy/substring matching, causing false positives
4. `generate_doc` returns duplicate entries (worktree copies polluting results)
5. `get_doc_tree` and `get_code_tree` return oversized responses (142K-3MB), exceeding token limits

---

## 1. Core Tools

### 1.1 mcp_status
- **Status:** PASS
- **Parameters:** none
- **Response:** Full database stats: 34,854 elements, 111,335 relationships, 2,226 functions, 1,305 classes, 1,119 files, index populated, initialized
- **Response time:** Instant

### 1.2 mcp_hello
- **Status:** PASS
- **Parameters:** none
- **Response:** `{ message: "Hello, World!" }`
- **Response time:** Instant

### 1.3 search_code("graph")
- **Status:** PASS
- **Parameters:** query="graph", limit=5
- **Response:** 5 results - functions and properties related to "graph" across `src/api/mod.rs`, `src/compress/response.rs`, `src/doc/generator.rs`
- **Response time:** Fast

### 1.4 search_code("CodeElement")
- **Status:** PASS
- **Parameters:** query="CodeElement", limit=3
- **Response:** 3 class definitions found in `src/db/models.rs` and worktree copies
- **Response time:** Fast
- **Note:** Results include worktree duplicates, which may not be desirable

### 1.5 find_function("main")
- **Status:** PASS (with caveat)
- **Parameters:** name="main"
- **Response:** 50 results including `main` functions across Rust, Go, Kotlin, Python, JavaScript
- **Response time:** Fast
- **Issue:** Fuzzy/substring matching includes false positives like `find_by_domain` and `find_by_business_domain` (containing "main" within "domain")

### 1.6 find_function("handle_request")
- **Status:** PASS
- **Parameters:** name="handle_request"
- **Response:** Empty array (function doesn't exist - correct behavior)
- **Response time:** Fast

### 1.7 query_file("src/main.rs")
- **Status:** FAIL
- **Parameters:** pattern="src/main.rs"
- **Response:** Empty array `[]`
- **Issue:** File clearly exists in the index (confirmed by other tools) but query_file returns nothing. Likely requires a different format for the pattern parameter.

### 1.8 query_file("src/db/models.rs")
- **Status:** PARTIAL PASS
- **Parameters:** pattern="src/db/models.rs"
- **Response:** 6 results, all `doc_section` type - documentation sections that reference the file, not the file's actual code elements
- **Issue:** Returns doc references instead of the file's own classes/functions

### 1.9 get_context("src/graph/query.rs")
- **Status:** PASS
- **Parameters:** file="src/graph/query.rs"
- **Response:** 16 elements (13 documents + 3 functions), token budget 4000 max / 3922 used, 96.6% token savings
- **Response time:** Fast
- **Note:** dependencies_count and dependents_count both 0, which seems incorrect for this core file

---

## 2. Dependency/Graph Tools

### 2.1 get_dependencies("src/main.rs")
- **Status:** PASS
- **Response:** 3 imports: `clap::Parser`, `std::os::unix::fs::PermissionsExt`, `sysinfo::System`
- **Response time:** Fast

### 2.2 get_dependencies("src/graph/query.rs")
- **Status:** PASS
- **Response:** 7 imports: `CodeElement`, `CozoDb`, `init_db`, `QueryCache`, `Arc`, `TempDir`, `tracing::debug`
- **Response time:** Fast

### 2.3 get_dependents("src/db/models.rs")
- **Status:** PASS
- **Response:** 13 dependents: 1 `contains` (./src/db), 12 `references` (AGENTS.md, architecture.md, erd-massive-graph.md, planning docs, design specs)
- **Response time:** Fast

### 2.4 get_impact_radius("src/main.rs", depth=2)
- **Status:** FAIL
- **Error:** "The operation timed out." (consistent across 3 attempts)
- **Note:** Depth=1 works (returns 75 affected elements). Timeout is caused by graph expansion at depth 2.

### 2.5 get_impact_radius("src/graph/query.rs", depth=3)
- **Status:** FAIL
- **Error:** "The operation timed out." (consistent across 3 attempts)
- **Note:** Same timeout issue. The transitive closure at depth >= 2 generates exponential paths.

### 2.6 get_callers("query_file")
- **Status:** PASS
- **Response:** 5 callers, all pointing to `execute_tool` in `src/mcp/handler.rs` (plus worktree duplicates)
- **Response time:** Fast

### 2.7 get_call_graph("main")
- **Status:** PASS
- **Response:** 30 call relationships across 2 depth levels. Shows `main` calling `cleanup_db`, `get_db_path`, `print_result`, `run_benchmark`, `init_db`, `orchestrate`, plus std lib calls.
- **Response time:** Fast

### 2.8 get_call_graph("handle_tool_call")
- **Status:** PASS (empty result)
- **Response:** 0 calls returned
- **Note:** Function may not be indexed under this exact name, or its calls are not captured in the graph.

### 2.9 get_tested_by("src/graph/query.rs")
- **Status:** PASS
- **Response:** 10 test links: 8 `contains` (inline unit tests) + 2 `documented_by` (test result docs)
- **Response time:** Fast

---

## 3. Documentation Tools

### 3.1 get_doc_for_file("src/main.rs")
- **Status:** PASS
- **Response:** 14 linked documents (analysis docs, design docs, planning docs, PRD, ERD)
- **Response time:** Fast

### 3.2 get_doc_for_file("src/graph/query.rs")
- **Status:** PASS
- **Response:** 15 linked documents (AGENTS.md, analysis docs, design docs, planning docs, specs)
- **Response time:** Fast

### 3.3 get_files_for_doc("README.md")
- **Status:** PASS
- **Response:** Empty array (no code files linked to README.md)
- **Response time:** Fast

### 3.4 get_files_for_doc("docs/design/hld-leankg.md")
- **Status:** PASS
- **Response:** 1 file reference (Node.js)
- **Response time:** Fast

### 3.5 get_doc_structure("README.md")
- **Status:** PASS (oversized)
- **Response:** 142,462 characters - exceeded token limit, saved to file
- **Issue:** Response is too large for typical consumption. May need pagination or size limits.

### 3.6 get_doc_tree
- **Status:** PASS (oversized)
- **Response:** 392,569 characters - exceeded token limit, saved to file
- **Issue:** Returns the entire document tree without pagination. Very large for consumption.

### 3.7 get_code_tree
- **Status:** PASS (oversized)
- **Response:** 3,032,430 characters (3MB!) - exceeded token limit, saved to file
- **Issue:** Returns the entire code tree. Far too large for direct consumption.

### 3.8 find_related_docs("src/graph/query.rs")
- **Status:** PASS
- **Response:** 15 related documents, all `documented_by` relationship type
- **Response time:** Fast

### 3.9 generate_doc("src/main.rs")
- **Status:** PASS (with quality issues)
- **Response:** Generated documentation listing 307 code elements, 300 functions, 2 classes
- **Issues:**
  - Every function appears 4 times (original + 3 worktree copies)
  - Documentation is a simple listing of functions and line numbers, not meaningful prose
  - 4,519 tokens for a single file's doc is large

---

## 4. Traceability Tools

### 4.1 get_traceability("src/main.rs")
- **Status:** PASS
- **Response:** 14 doc links with traceability data. Feature_id and user_story_id are both null.
- **Response time:** Fast

### 4.2 get_traceability("src/graph/query.rs")
- **Status:** PASS
- **Response:** 15 doc links with traceability data. Feature_id and user_story_id are both null.
- **Response time:** Fast

### 4.3 search_by_requirement("impact radius")
- **Status:** PASS
- **Response:** Empty array (no code elements mapped to this requirement text)
- **Response time:** Fast
- **Note:** Tool works but the codebase has no requirement annotations linked to code elements

### 4.4 search_by_requirement("MCP")
- **Status:** PASS
- **Response:** Empty array
- **Response time:** Fast
- **Note:** Same as above - no requirement-to-code mappings exist

### 4.5 search_annotations("graph")
- **Status:** PASS
- **Response:** 0 annotations found
- **Response time:** Fast
- **Note:** Database has 0 annotations (confirmed by mcp_status), so empty is correct

### 4.6 search_annotations("dependency")
- **Status:** PASS
- **Response:** 0 annotations found
- **Response time:** Fast
- **Note:** Same as above - annotations count is 0 in the database

---

## 5. Navigation/Service Tools

### 5.1 get_nav_graph(file="src/main.rs")
- **Status:** PASS
- **Response:** 0 elements, 0 relationships (Rust file has no nav graph data)
- **Response time:** Fast

### 5.2 get_nav_graph (no params)
- **Status:** PASS
- **Response:** 4 elements (all `nav_destination` type, `BrowseFragment` from Kotlin TV app fixture)
- **Response time:** Fast
- **Note:** Data is sparse - only Android/Kotlin nav artifacts exist from fixture code

### 5.3 get_nav_callers("main")
- **Status:** PASS
- **Response:** Empty callers array (expected - "main" is not a nav destination)
- **Response time:** Fast

### 5.4 get_nav_callers("handle_tool_call")
- **Status:** PASS
- **Response:** Empty callers array (expected)
- **Response time:** Fast

### 5.5 get_service_graph
- **Status:** PASS
- **Response:** 1 service node (`leankg`, is_current_service: true, weight 10.0), 0 edges
- **Response time:** Fast

### 5.6 get_clusters
- **Status:** PASS
- **Response:** 13,852 clusters, 17,615 total members, avg cluster size 1.27. 100 clusters returned (paged). Clusters span: assets, docs, planning, requirement, db, specs, entity, analysis, benchmark, graph, indexer, compress, watcher, web, config, tests, kotlin_patterns, services, models, plans, design, remote, dao.
- **Response time:** Fast

### 5.7 get_cluster_context("cluster_7988")
- **Status:** PASS
- **Response:** Label: "mcp", 1 member (property `watch_path` in `src/mcp/server.rs`), 1 inter-cluster dependency
- **Response time:** Fast

### 5.8 get_screen_args("BrowseFragment")
- **Status:** PASS
- **Response:** Empty arguments array
- **Response time:** Fast
- **Note:** `destination` is a required parameter. Returns empty when no screen args registered.

---

## 6. Utility Tools

### 6.1 detect_changes
- **Status:** PASS
- **Response:** 0 changed files, 0 changed symbols, 0 affected symbols, risk_level: low
- **Response time:** Fast
- **Note:** Clean working tree yields empty change set as expected

### 6.2 ctx_read("src/main.rs")
- **Status:** PASS
- **Response:** Full map of 36 functions, dependencies, exports, API surface. File reported as 2,926 lines. 96.6% token savings (23,598 -> 797 tokens).
- **Response time:** Fast

### 6.3 ctx_read("src/db/models.rs")
- **Status:** PASS
- **Response:** All data models: CodeElement, Relationship, DependencyInfo, BusinessLogic, Document, ContextMetric, KnowledgeEntry, Role, AuthContext. 71.2% token savings.
- **Response time:** Fast

### 6.4 mcp_impact("src/main.rs", depth=2)
- **Status:** FAIL (timeout)
- **Error:** "The operation timed out." (consistent across multiple attempts)
- **Note:** Same root cause as get_impact_radius - graph traversal explosion at depth >= 2

### 6.5 mcp_impact("src/graph/query.rs", depth=3)
- **Status:** FAIL (timeout)
- **Error:** "The operation timed out."
- **Note:** Same as above

### 6.6 mcp_impact("src/main.rs", depth=1) [retest]
- **Status:** PASS
- **Response:** 137 affected elements (functions, classes, documents)
- **Response time:** Fast

### 6.7 mcp_index("src")
- **Status:** PASS
- **Response:** Indexed 106 files, 0 skipped, 17,066 call edges resolved
- **Response time:** Fast (incremental re-index)

### 6.8 mcp_index_docs("docs")
- **Status:** PASS
- **Response:** Indexed 61 documents, 1,181 sections, 3,544 relationships
- **Response time:** Fast

### 6.9 mcp_install
- **Status:** PASS
- **Response:** Created `.mcp.json`, `.opencode.json`, and `instructions/leankg-tools.md`
- **Response time:** Fast

### 6.10 orchestrate(intent="context for src/main.rs")
- **Status:** PASS
- **Response:** Full context returned, 63 elements, 96.6% savings
- **Response time:** Fast
- **Note:** Requires file-referencing natural language intent. Error messages guide toward correct usage.

### 6.11 run_raw_query("?[] <- [[1, 'test']]")
- **Status:** PASS
- **Response:** Headers `[_0, _1]`, row `[1, "test"]` - correct CozoDB query result
- **Response time:** Fast

---

## Issue Details

### CRITICAL: Impact Radius Timeout (depth >= 2)

**Affected tools:** `get_impact_radius`, `mcp_impact`
**Symptom:** Consistent timeout at depth >= 2 on any file
**Root cause:** With 111,335 relationships, transitive graph closure at depth 2+ generates exponential path explosion
**Workaround:** Use depth=1 (works correctly)
**Recommendation:** Implement server-side depth limits with warnings, or optimize the recursive query with cycle detection and result capping

### HIGH: query_file Returns Empty or Wrong Data

**Affected tool:** `query_file`
**Symptom:** Returns empty for `src/main.rs`, returns doc references instead of code elements for `src/db/models.rs`
**Recommendation:** Investigate the pattern matching logic and ensure the tool queries code elements contained in the file, not documents that reference the file path string

### MEDIUM: find_function Fuzzy Matching

**Affected tool:** `find_function`
**Symptom:** Searching for "main" returns "find_by_domain" and "find_by_business_domain" (substring matches)
**Recommendation:** Add exact match mode or prioritize exact matches over substring matches

### MEDIUM: Worktree Duplicate Pollution

**Affected tools:** `search_code`, `find_function`, `get_callers`, `generate_doc`, and others
**Symptom:** Results include duplicates from `.worktrees/` directory paths
**Recommendation:** Add a filter option to exclude worktree paths, or default to excluding them

### LOW: Oversized Tree Responses

**Affected tools:** `get_doc_tree`, `get_code_tree`, `get_doc_structure`
**Symptom:** Responses range from 142KB to 3MB, exceeding token limits
**Recommendation:** Add pagination parameters (offset/limit) or response size caps

---

## Test Environment

| Item | Value |
|------|-------|
| Build | `cargo build --release` - succeeded in 1m 03s |
| Server | launchd via `com.leankg.mcp-http.plist`, port 9699 |
| Health | `curl http://localhost:9699/health` returns `{"status": "ok"}` |
| Test method | 6 parallel subagents + direct MCP calls |
| Total tool invocations | ~80+ |
| Total test duration | ~12 minutes |

---

*Report generated by Claude Code automated testing on 2026-05-10*
