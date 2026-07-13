use serde_json::json;
use serde_json::Value;

pub struct ToolRegistry;

impl ToolRegistry {
    pub fn list_tools() -> Vec<ToolDefinition> {
        vec![
            ToolDefinition {
                name: "mcp_init".to_string(),
                description: "Initialize LeanKG project (creates .leankg/ and leankg.yaml)"
                    .to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "path": {"type": "string", "description": "Path for LeanKG project (default: .leankg)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_index".to_string(),
                description: "Index codebase (mirrors CLI: leankg index)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "path": {"type": "string", "description": "Path to index (default: current directory)"},
                        "incremental": {"type": "boolean", "description": "Only index changed files (git-based)"},
                        "resolve_calls": {"type": "boolean", "default": false, "description": "Resolve unresolved call edges after indexing. Defaults to false for MCP responsiveness."},
                        "lang": {"type": "string", "description": "Filter by language (e.g., go,ts,py,rs,kt)"},
                        "exclude": {"type": "string", "description": "Exclude patterns (comma-separated)"},
                        "env": {"type": "string", "enum": ["local", "staging", "production"], "default": "local", "description": "Target environment for this index"},
                        "service_name": {"type": "string", "description": "Service name for this index"},
                        "version": {"type": "string", "description": "Version tag (semver or git sha)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_index_docs".to_string(),
                description: "Index documentation directory to create code-doc traceability edges. \
                              Run after mcp_index to populate documented_by and references relationships."
                    .to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "path": {"type": "string", "description": "Path to docs directory (default: ./docs)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_install".to_string(),
                description: "Create .mcp.json for MCP client configuration".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "mcp_config_path": {"type": "string", "description": "Path for .mcp.json (default: .mcp.json)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_status".to_string(),
                description: "Show LeanKG index status".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path to check status for (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_impact".to_string(),
                description: "Calculate impact radius (blast radius) for a file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to analyze"},
                        "depth": {"type": "integer", "description": "Depth of analysis (default: 3)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "query_file".to_string(),
                description: "Find file by name or pattern".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "pattern": {"type": "string", "description": "File name or pattern to search"},
                        "element_type": {"type": "string", "enum": ["file", "function", "struct", "class", "module", "activity", "fragment", "service", "receiver", "provider", "hilt_module", "room_entity", "room_dao", "room_database", "nav_destination", "android_widget", "annotation"], "description": "Optional filter by element type"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_dependencies".to_string(),
                description: "Get file dependencies (direct imports)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to get dependencies for"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "get_dependents".to_string(),
                description: "Get files depending on target".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to get dependents for"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "get_impact_radius".to_string(),
                description: "Get all files affected by change within N hops. Keep depth<=2 for LLM context budgets. Depth 3 may return hundreds of nodes. Results include confidence scores (0.0-1.0) and severity classification (WILL BREAK, LIKELY AFFECTED, MAY BE AFFECTED). Set compress_response=true for token-optimized output.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to analyze"},
                        "depth": {"type": "integer", "default": 3, "description": "Hop depth (default: 3). Keep <=2 for context budgets."},
                        "min_confidence": {"type": "number", "default": 0.0, "description": "Minimum confidence threshold (0.0-1.0). Only return results with confidence >= this value."},
                        "compress_response": {"type": "boolean", "default": false, "description": "Enable RTK-style compression for token savings"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "detect_changes".to_string(),
                description: "Pre-commit risk analysis: computes diff between working tree and last indexed commit. Returns changed files, affected symbols, and risk level (critical/high/medium/low). Risk classification: critical>=10 dependents at depth 1, high>=5 dependents or public API changed, medium=2-4 dependents or cross-module dep, low=<=1 dependent within single cluster.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "scope": {"type": "string", "enum": ["staged", "unstaged", "all"], "default": "all", "description": "Scope of changes to analyze: 'staged' (git staged), 'unstaged', or 'all' (default)"},
                        "min_confidence": {"type": "number", "default": 0.0, "description": "Minimum confidence threshold for affected symbols."},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_review_context".to_string(),
                description: "Generate focused subgraph + structured review prompt".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "files": {"type": "array", "items": {"type": "string"}, "description": "Files to include in review context"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_context".to_string(),
                description: "Get AI context for file (minimal, token-optimized)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to get context for"},
                        "signature_only": {"type": "boolean", "default": true, "description": "Return only signatures (default). Set false for full body metadata."},
                        "max_tokens": {"type": "integer", "default": 4000, "description": "Token budget cap"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "orchestrate".to_string(),
                description: "Smart context orchestration with caching. Provide natural language intent like 'show me impact of changing function X' or 'get context for file Y'. Internally: checks cache -> queries graph -> compresses -> caches result. Use this instead of multiple individual tools when you want LeanKG to optimize the flow.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "intent": {"type": "string", "description": "Natural language intent (e.g., 'show me impact of changing main.rs', 'get context for handler.rs', 'find function named parse')"},
                        "file": {"type": "string", "description": "Optional: specific file to query"},
                        "mode": {"type": "string", "enum": ["adaptive", "full", "map", "signatures"], "default": "adaptive", "description": "Compression mode for file content"},
                        "fresh": {"type": "boolean", "default": false, "description": "Force fresh query, bypass cache"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["intent"]
                }),
            },
            ToolDefinition {
                name: "ctx_read".to_string(),
                description: "Read file with compression modes for efficient LLM context".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File path to read"},
                        "mode": {"type": "string", "enum": ["adaptive", "full", "map", "signatures", "diff", "aggressive", "entropy", "lines"], "default": "adaptive", "description": "Compression mode"},
                        "lines": {"type": "string", "description": "Lines specification for 'lines' mode (e.g., '1-10,20,30-40')"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "explain_node".to_string(),
                description: "US-GF-02: Return a single-node dossier (definition site, cluster, in/out degree, top neighbors by relation type). Accepts qualified_name, exact name, or fuzzy suffix.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "Qualified_name, exact name, or fuzzy suffix"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["name"]
                }),
            },
            ToolDefinition {
                name: "get_overview_context".to_string(),
                description: "US-GN-08: Return the project overview context (identity, critical facts, recent hotspots) as a single MCP-callable resource. Acts as an MCP-Resources-style agent context shortcut for wake_up + L0/L1 layers.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string"},
                        "project_name": {"type": "string", "default": "project"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_team_map".to_string(),
                description: "US-V2-12: Aggregated team / ownership map (team name, on-call, services owned) for a given environment.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "env": {"type": "string", "default": "local", "description": "Environment scope (local/staging/production)"},
                        "project": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "report_query_outcome".to_string(),
                description: "US-GF-09: Record whether a graph query result was useful (useful | dead_end | corrected). Appends an entry to .leankg/reflections/LESSONS.md so future agents can bias ranking toward previously-useful nodes.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "question": {"type": "string", "description": "Original question"},
                        "nodes": {"type": "array", "items": {"type": "string"}, "description": "Qualified_names that were returned"},
                        "outcome": {"type": "string", "enum": ["useful", "dead_end", "corrected"]},
                        "note": {"type": "string", "description": "Optional free-form lesson learned"},
                        "project": {"type": "string"}
                    },
                    "required": ["question", "outcome"]
                }),
            },
            ToolDefinition {
                name: "agent_focus".to_string(),
                description: "US-MP-04: Return a focused subgraph filtered by agent persona (path filters, cluster_id, element_types). Persona config lives in .leankg/agents/<name>.json.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "Agent persona name"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["name"]
                }),
            },
            ToolDefinition {
                name: "agent_diary_write".to_string(),
                description: "US-MP-04: Append a note to an agent's diary (.leankg/agents/<name>.diary.jsonl).".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string"},
                        "note": {"type": "string"},
                        "tags": {"type": "array", "items": {"type": "string"}},
                        "project": {"type": "string"}
                    },
                    "required": ["name", "note"]
                }),
            },
            ToolDefinition {
                name: "agent_diary_read".to_string(),
                description: "US-MP-04: Read recent diary entries for an agent.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string"},
                        "limit": {"type": "integer", "default": 50},
                        "project": {"type": "string"}
                    },
                    "required": ["name"]
                }),
            },
            ToolDefinition {
                name: "get_cluster_skill".to_string(),
                description: "US-GN-07: Generate a per-cluster SKILL.md with label, member count, top files, entry points, and usage hints.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "cluster_id": {"type": "string", "description": "Cluster ID (from get_clusters)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["cluster_id"]
                }),
            },
            ToolDefinition {
                name: "find_tunnels".to_string(),
                description: "US-MP-06: Find cross-domain tunnels — relationships where source and target belong to different Leiden clusters. Sorted by confidence descending.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "limit": {"type": "integer", "default": 50, "minimum": 1, "maximum": 500},
                        "project": {"type": "string", "description": "Optional: project path"}
                    }
                }),
            },
            ToolDefinition {
                name: "check_consistency".to_string(),
                description: "US-MP-05: Detect broken or stale relationships (missing targets, invalidated edges). Returns BROKEN / STALE / CURRENT findings plus counts.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    }
                }),
            },
            ToolDefinition {
                name: "temporal_query".to_string(),
                description: "US-MP-01: Return the graph state as of a given epoch (seconds). Edges with valid_from <= now <= valid_to are included.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "at": {"type": "integer", "description": "Epoch seconds (e.g. 1718000000)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["at"]
                }),
            },
            ToolDefinition {
                name: "timeline".to_string(),
                description: "US-MP-01: Return the chronological evolution of a code element's relationships (added / invalidated events with timestamps).".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "qualified_name": {"type": "string", "description": "Code element qualified_name"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["qualified_name"]
                }),
            },
            ToolDefinition {
                name: "load_layer".to_string(),
                description: "US-MP-02: Load a context layer. layer=L0 -> identity (~50 tok). L1 -> critical facts (~120 tok). L2 -> cluster context (requires cluster_id). L3 -> deep subgraph search (requires query).".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "layer": {"type": "string", "enum": ["L0", "L1", "L2", "L3"], "description": "Context layer to load"},
                        "project": {"type": "string", "description": "Optional: project path"},
                        "project_name": {"type": "string", "default": "project"},
                        "cluster_id": {"type": "string", "description": "Required for L2"},
                        "query": {"type": "string", "description": "Required for L3"},
                        "limit": {"type": "integer", "default": 20}
                    },
                    "required": ["layer"]
                }),
            },
            ToolDefinition {
                name: "get_graph_report".to_string(),
                description: "US-GF-06: Return the full graph report (god nodes, confidence distribution, suggested questions). Writes `.leankg/GRAPH_REPORT.md` on disk.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"},
                        "project_name": {"type": "string", "default": "project", "description": "Display name for the report header"},
                        "format": {"type": "string", "enum": ["markdown", "json"], "default": "markdown"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_god_nodes".to_string(),
                description: "US-GF-05: Return the most-connected elements (highest combined in+out degree). Optional percentile cutoff excludes utility super-hubs.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "limit": {"type": "integer", "default": 20, "minimum": 1, "maximum": 200},
                        "exclude_hubs_percentile": {"type": "integer", "minimum": 0, "maximum": 100, "description": "Exclude top-N% super-hubs (e.g. 90 keeps bottom 90%)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    }
                }),
            },
            ToolDefinition {
                name: "shortest_path".to_string(),
                description: "US-GF-01: BFS shortest path between two symbols. Returns ordered hops with relation, confidence, and provenance label (EXTRACTED / INFERRED / AMBIGUOUS). Inputs accept qualified_name, exact name, or fuzzy suffix.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "source": {"type": "string", "description": "Source qualified_name, name, or fuzzy suffix"},
                        "target": {"type": "string", "description": "Target qualified_name, name, or fuzzy suffix"},
                        "max_hops": {"type": "integer", "default": 6, "minimum": 1, "maximum": 10, "description": "Maximum hops (1-10)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["source", "target"]
                }),
            },
            ToolDefinition {
                name: "find_function".to_string(),
                description: "Locate function definition by name. Optionally scope to a file.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "Function name to search for"},
                        "file": {"type": "string", "description": "Optional file to scope the search to"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["name"]
                }),
            },
            ToolDefinition {
                name: "get_callers".to_string(),
                description: "Find all functions/methods that call a given function. \
                              Returns the caller name, file path, and line number.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "function": {"type": "string", "description": "Function name to find callers for"},
                        "file": {"type": "string", "description": "Optional file to scope the search"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["function"]
                }),
            },
            ToolDefinition {
                name: "get_call_graph".to_string(),
                description: "Get bounded function call chain. Use depth=1 for direct callees, depth=2 for two hops. Avoid depth>3 to prevent neighbor explosion.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "function": {"type": "string", "description": "Function to get call graph for"},
                        "depth": {"type": "integer", "default": 2, "description": "Maximum call graph depth (default: 2, max: 3)"},
                        "max_results": {"type": "integer", "default": 30, "description": "Maximum number of results (default: 30)"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["function"]
                }),
            },
            ToolDefinition {
                name: "search_code".to_string(),
                description: "Ontology-first paginated code search. On mega-graphs (>LEANKG_MAX_CACHE_ELEMENTS) defaults to concept ontology → code_refs → DB, then semantic name fallback. Never full-table scans large workspaces. Use limit/offset for pagination.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Search query string (raw words/concepts allowed)"},
                        "element_type": {"type": "string", "enum": ["file", "function", "struct", "class", "module", "import"], "description": "Filter by element type"},
                        "limit": {"type": "integer", "default": 20, "description": "Page size (default: 20, max: 50)"},
                        "offset": {"type": "integer", "default": 0, "description": "Pagination offset"},
                        "use_ontology": {"type": "boolean", "default": true, "description": "Concept-gated workflow first. Defaults true on mega-graphs; set false only for tiny projects."},
                        "env": {"type": "string", "default": "local", "description": "Environment scope for the ontology scan"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "concept_search".to_string(),
                description: "Concept-gated semantic search: extracts keywords from raw input, scans the concept ontology for matching concepts, loads each concept's code references, and queries the LeanKG DB for the actual code. Use this for natural-language / domain-concept queries (e.g. 'feature flag', 'gorm store', 'grpc service'). Falls back to name-based code search if no concept matches.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Raw natural-language or concept query"},
                        "env": {"type": "string", "default": "local", "description": "Environment scope for the ontology scan"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum number of matched concepts / code results"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "search_annotations".to_string(),
                description: "Search for code elements by annotation. Returns classes, functions, or properties with matching annotations.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "annotation_name": {"type": "string", "description": "Annotation name to search for (e.g., 'Entity', 'HiltViewModel')"},
                        "target_type": {"type": "string", "enum": ["class", "function", "property", "parameter", "all"], "description": "Filter by target type"},
                        "file_pattern": {"type": "string", "description": "Optional file pattern to limit search"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum number of results (default: 20)"},
                        "project": {"type": "string", "description": "Optional: project path to search in"}
                    },
                    "required": ["annotation_name"]
                }),
            },
            ToolDefinition {
                name: "generate_doc".to_string(),
                description: "Generate documentation for file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to generate documentation for"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "find_large_functions".to_string(),
                description: "Find oversized functions by line count".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "min_lines": {"type": "integer", "default": 50, "description": "Minimum line count threshold (default: 50)"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum number of results (default: 20, max: 100)"},
                        "offset": {"type": "integer", "default": 0, "description": "Number of results to skip (pagination offset)"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_tested_by".to_string(),
                description: "Get test coverage for a function/file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to get test coverage for"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "get_doc_for_file".to_string(),
                description: "Get documentation files that reference a code element".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to get documentation for"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "get_files_for_doc".to_string(),
                description: "Get code elements referenced in a documentation file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "doc": {"type": "string", "description": "Documentation file path"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["doc"]
                }),
            },
            ToolDefinition {
                name: "get_doc_structure".to_string(),
                description: "Get documentation directory structure".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "include_counts": {
                            "type": "boolean",
                            "description": "Optional: compute full element/file/function counts. Disabled by default because large databases can take a long time to count.",
                            "default": false
                        },
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_traceability".to_string(),
                description: "Get full traceability chain for a code element".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "element": {"type": "string", "description": "Code element to trace"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["element"]
                }),
            },
            ToolDefinition {
                name: "search_by_requirement".to_string(),
                description: "Find code elements related to a specific requirement".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "requirement_id": {"type": "string", "description": "Requirement ID to search for"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["requirement_id"]
                }),
            },
            ToolDefinition {
                name: "get_doc_tree".to_string(),
                description: "Get documentation tree structure with hierarchy".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "limit": {"type": "integer", "default": 50, "description": "Maximum number of categories (default: 50, max: 200)"},
                        "offset": {"type": "integer", "default": 0, "description": "Number of categories to skip (pagination offset)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_code_tree".to_string(),
                description: "Get codebase structure".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "limit": {"type": "integer", "default": 50, "description": "Maximum number of files (default: 50, max: 200)"},
                        "offset": {"type": "integer", "default": 0, "description": "Number of files to skip (pagination offset)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "find_related_docs".to_string(),
                description: "Find documentation related to a code change".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File that was changed"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "mcp_hello".to_string(),
                description: "Returns 'Hello, World!'".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_clusters".to_string(),
                description: "Get all clusters (functional communities) in the codebase. Returns cluster ID, label, member count, and representative files.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "limit": {"type": "integer", "default": 50, "description": "Maximum number of clusters (default: 50, max: 100)"},
                        "offset": {"type": "integer", "default": 0, "description": "Number of clusters to skip (pagination offset)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_cluster_context".to_string(),
                description: "Get all symbols in a cluster with entry points and inter-cluster dependencies.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "cluster_id": {"type": "string", "description": "Cluster ID to get context for"},
                        "cluster_label": {"type": "string", "description": "Alternative: cluster label to search for"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "run_raw_query".to_string(),
                description: "Execute a raw Datalog/Cypher query against the LeanKG CozoDB engine".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "The CozoDB Datalog query to execute"},
                        "params": {
                            "type": "object",
                            "description": "Optional parameters for the parameterized query",
                            "additionalProperties": true
                        },
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "get_nav_graph".to_string(),
                description: "Get the navigation graph structure for a screen or nav file. Returns destinations, actions, arguments, and deep links.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "Nav XML file path or Kotlin DSL file path"},
                        "graph_id": {"type": "string", "description": "Nav graph ID (alternative to file)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "find_route".to_string(),
                description: "Find which destination a route string or action ID resolves to.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "route": {"type": "string", "description": "Route string (e.g. 'profile/{userId}') or action ID (e.g. 'action_home_to_detail')"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["route"]
                }),
            },
            ToolDefinition {
                name: "get_screen_args".to_string(),
                description: "List all arguments a screen/destination requires, with types and default values.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "destination": {"type": "string", "description": "Destination name, route, or file path"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum results"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["destination"]
                }),
            },
            ToolDefinition {
                name: "get_nav_callers".to_string(),
                description: "Find all call sites that navigate to a given destination. Use for impact radius when changing screen args.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "destination": {"type": "string", "description": "Destination name, route, fragment class, or activity class"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": ["destination"]
                }),
            },
            ToolDefinition {
                name: "get_service_graph".to_string(),
                description: "Get microservice call graph with service repos as nodes. Returns aggregated service-to-service topology from service_calls relationships. The current service repo is the biggest node.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "service": {"type": "string", "description": "Current service name (defaults to project directory name)"},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },

            // Knowledge Contribution Tools
            ToolDefinition {
                name: "add_knowledge".to_string(),
                description: "Add a knowledge entry to the knowledge base. Supports business knowledge, domain knowledge, architecture docs, PRD-code mapping, debugging notes, and general notes. Entries can optionally be linked to code elements, user stories, or features.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "knowledge_type": {"type": "string", "enum": ["business", "domain", "architecture", "prd_mapping", "debugging", "general"], "description": "Type of knowledge entry"},
                        "title": {"type": "string", "description": "Title of the knowledge entry"},
                        "content": {"type": "string", "description": "Content in markdown format"},
                        "element_qualified": {"type": "string", "description": "Optional: qualified name of code element to link (e.g., src/main.rs::main)"},
                        "user_story_id": {"type": "string", "description": "Optional: user story ID to link"},
                        "feature_id": {"type": "string", "description": "Optional: feature ID to link"},
                        "tags": {"type": "string", "description": "Comma-separated tags"},
                        "environment": {"type": "string", "enum": ["production", "staging", "dev", "upcoming"], "description": "Environment tag (default: production)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["knowledge_type", "title", "content"]
                }),
            },
            ToolDefinition {
                name: "update_knowledge".to_string(),
                description: "Update an existing knowledge entry by ID.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "id": {"type": "string", "description": "ID of the knowledge entry to update"},
                        "title": {"type": "string", "description": "New title"},
                        "content": {"type": "string", "description": "New content in markdown"},
                        "tags": {"type": "string", "description": "New comma-separated tags"},
                        "environment": {"type": "string", "description": "New environment tag"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["id"]
                }),
            },
            ToolDefinition {
                name: "delete_knowledge".to_string(),
                description: "Delete a knowledge entry by ID.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "id": {"type": "string", "description": "ID of the knowledge entry to delete"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["id"]
                }),
            },
            ToolDefinition {
                name: "search_knowledge".to_string(),
                description: "Search all knowledge entries by keyword. Filters by knowledge type and environment. Returns matching entries with titles, content snippets, and metadata.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Search query (keyword in title)"},
                        "knowledge_type": {"type": "string", "enum": ["business", "domain", "architecture", "prd_mapping", "debugging", "general"], "description": "Optional: filter by knowledge type"},
                        "environment": {"type": "string", "enum": ["production", "staging", "dev", "upcoming"], "description": "Optional: filter by environment"},
                        "limit": {"type": "integer", "description": "Max results (default: 20, max: 50)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "add_annotation".to_string(),
                description: "Add or update a business logic annotation for a code element. Links a description (and optionally a user story or feature) to a code element.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "element": {"type": "string", "description": "Qualified name of the code element (e.g., src/auth/login.rs::handle_login)"},
                        "description": {"type": "string", "description": "Business logic description"},
                        "user_story": {"type": "string", "description": "Optional: user story ID"},
                        "feature": {"type": "string", "description": "Optional: feature ID"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["element", "description"]
                }),
            },
            ToolDefinition {
                name: "link_element".to_string(),
                description: "Link a code element to a user story or feature ID.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "element": {"type": "string", "description": "Qualified name of the code element"},
                        "id": {"type": "string", "description": "User story or feature ID"},
                        "kind": {"type": "string", "enum": ["story", "feature"], "description": "Type of link: 'story' for user story, 'feature' for feature"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["element", "id", "kind"]
                }),
            },
            ToolDefinition {
                name: "add_documentation".to_string(),
                description: "Index a single documentation file into the knowledge graph. Extracts code references and creates documented_by/references relationships.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file_path": {"type": "string", "description": "Path to the documentation file to index"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["file_path"]
                }),
            },

            // Version/Branch Tagging Tools
            ToolDefinition {
                name: "search_by_environment".to_string(),
                description: "Search code elements and knowledge entries filtered by environment (production, staging, dev, upcoming). Useful for seeing what's in production vs what's in development.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                                                "environment": {"type": "string", "enum": ["production", "staging", "dev", "upcoming", "local"], "description": "Environment to filter by (use 'local' for default-indexed code)"},
                        "query": {"type": "string", "description": "Optional: search query to further filter results"},
                        "limit": {"type": "integer", "description": "Max results (default: 20)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["environment"]
                }),
            },
            ToolDefinition {
                name: "get_upcoming_changes".to_string(),
                description: "Get knowledge entries and code elements tagged as 'upcoming' (feature branch changes not yet in main). Shows what's coming in the next release.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "branch": {"type": "string", "description": "Optional: filter by specific branch name"},
                        "limit": {"type": "integer", "description": "Max results (default: 20)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "promote_environment".to_string(),
                description: "Promote knowledge entries and code elements from one environment to another (e.g., upcoming -> production). Used when a feature branch merges to main.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "branch": {"type": "string", "description": "Branch name to promote entries from"},
                        "target_environment": {"type": "string", "enum": ["production", "staging", "dev"], "description": "Target environment (default: production)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["branch"]
                }),
            },
            ToolDefinition {
                name: "query_incidents".to_string(),
                description: "Find past incidents matching a pattern or service. Returns structured incident records with root cause, resolution, and prevention advice.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "service": {"type": "string", "description": "Service name to filter incidents"},
                        "pattern": {"type": "string", "description": "Text pattern to search in title or root cause"},
                        "env": {"type": "string", "enum": ["production", "staging", "local"], "description": "Environment to query (default: local)"},
                        "limit": {"type": "integer", "default": 5, "description": "Maximum number of incidents to return"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "find_env_conflicts".to_string(),
                description: "Surface mismatches between local, staging, and production environments for a service. Detects schema version drift, config differences, and missing deployments.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "service": {"type": "string", "description": "Service name to check for conflicts"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["service"]
                }),
            },
            ToolDefinition {
                name: "get_service_context".to_string(),
                description: "Get a complete snapshot of a service in a given environment: dependencies, callers, open incidents, and recent incident history.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "service": {"type": "string", "description": "Service name"},
                        "env": {"type": "string", "enum": ["production", "staging", "local"], "default": "local", "description": "Environment"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["service"]
                }),
            },
            ToolDefinition {
                name: "semantic_search".to_string(),
                description: "Natural language semantic discovery with pagination. Ontology-first: scans concept ontology then falls back to bounded name search. Safe on mega-graphs / nested multi-repo workspaces (never loads full element tables).".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Natural language query (e.g., 'service that handles refunds')"},
                        "env": {"type": "string", "enum": ["production", "staging", "local"], "default": "local", "description": "Environment to search"},
                        "limit": {"type": "integer", "default": 20, "description": "Page size (default: 20, max: 50)"},
                        "offset": {"type": "integer", "default": 0, "description": "Pagination offset"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "wake_up".to_string(),
                description: "Load minimal project context (~170 tokens) for session start. Returns project identity (L0: name, languages, top directories) and critical facts (L1: module map, critical dependencies, recent hotspots). Cached in .leankg/wake_up.txt and regenerated on re-index.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": []
                }),
            },

            // Ontology Semantic Search Tools
            ToolDefinition {
                name: "kg_context".to_string(),
                description: "Get ontology-aware context for a semantic query. Returns matched concept nodes, expanded code context, workflows, docs, tests, and confidence scores. Use for agentic semantic questions like 'where is checkout refund failure handled?'".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Natural language query (e.g., 'checkout refund failure')"},
                        "env": {"type": "string", "enum": ["local", "staging", "production"], "default": "local", "description": "Environment to search"},
                        "depth": {"type": "integer", "default": 2, "description": "Expansion depth (default: 2)"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "kg_concept_map".to_string(),
                description: "Get a compact concept neighborhood for a domain, service, or feature. Useful for feature onboarding, impact analysis before edits, and understanding ownership boundaries.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Concept or service name to map"},
                        "env": {"type": "string", "enum": ["local", "staging", "production"], "default": "local", "description": "Environment"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "kg_trace_workflow".to_string(),
                description: "Get an ordered procedural trace for a workflow. Useful for debugging user flows, understanding what code runs before/after a step, and identifying missing tests or failure handling.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "workflow_id_or_query": {"type": "string", "description": "Workflow name, ID, or search query"},
                        "env": {"type": "string", "enum": ["local", "staging", "production"], "default": "local", "description": "Environment"},
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": ["workflow_id_or_query"]
                }),
            },
            ToolDefinition {
                name: "kg_ontology_status".to_string(),
                description: "Get ontology coverage status: counts of concept and procedural nodes by type, relationships by type, nodes missing aliases, and workflows without failure modes.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "kg_self_test".to_string(),
                description: "Run a smoke test against every kg_* ontology tool and the live CozoDB schema. Returns per-tool status plus the code_elements and relationships arity/columns. Use this to detect ontology-layer drift (e.g. arity mismatch from a missed schema migration) before any agent relies on kg_*. Safe to call at any time; does not mutate state.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "project": {"type": "string", "description": "Optional: project path"}
                    },
                    "required": []
                }),
            },
            #[cfg(feature = "embeddings")]
            ToolDefinition {
                name: "kg_semantic_context".to_string(),
                description: "Vector retrieval + cross-encoder rerank + adaptive KG traversal. Use for natural-language queries where keyword search misses: 'where do we validate access rights', 'how does the refund flow work'. Returns ranked seed nodes plus 1-2 hop graph context (related code, tests, docs, workflows). Requires the `embeddings` cargo feature and an embedding index built via `cargo run --release -- embed`.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Natural language query"},
                        "env": {"type": "string", "enum": ["local", "staging", "production"], "default": "local", "description": "Environment to search"},
                        "top_k": {"type": "integer", "default": 50, "description": "ANN retrieve depth (candidates before rerank)"},
                        "rerank_top_n": {"type": "integer", "default": 10, "description": "Final seed count after cross-encoder rerank"},
                        "traverse": {"type": "boolean", "default": true, "description": "Toggle Stage 4 graph enrichment (1-2 hop neighbors via ontology + code edges)"},
                        "include_worktrees": {"type": "boolean", "default": false, "description": "Include paths under .worktrees/ / .claude/worktrees/ / .opencode/worktrees/ (filtered by default to dedupe agent scratch copies)"},
                        "debug": {"type": "boolean", "default": false, "description": "Include diagnostics: candidate counts, latency per stage, reranker status"},
                        "project": {"type": "string", "description": "Optional: project path (defaults to current working directory)"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "get_architecture".to_string(),
                description: "Get architecture overview: languages, packages, entry points, routes, hotspots, clusters, knowledge counts, relationship summary. Single-call alternative to running multiple individual queries. Supports max_items to cap each section for token budget control.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "max_items": {"type": "integer", "description": "Optional: per-section item cap. When set, each array section (languages, entry_points, routes, clusters, hotspots, relationship_summary) is truncated to this many entries. truncated_sections reports which were trimmed."},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_graph_schema".to_string(),
                description: "Get graph schema overview: element type counts, relationship type counts. Use to understand database structure and find available element/relationship patterns. Supports max_items to cap each section for token budget control.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "max_items": {"type": "integer", "description": "Optional: per-section item cap. When set, each array section (element_types, relationship_types) is truncated to this many entries. truncated_sections reports which were trimmed."},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "find_dead_code".to_string(),
                description: "Find potentially dead code: functions with zero callers and zero tests, excluding known entry points (main, Main). Filter by minimum line count to avoid trivial getters/setters.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "min_lines": {"type": "integer", "default": 10, "description": "Minimum line count threshold (default: 10). Functions shorter than this are excluded."},
                        "project": {"type": "string", "description": "Optional: project path (resolves to nearest .leankg directory)"}
                    },
                    "required": []
                }),
            },
        ]
    }
}

#[derive(Debug, Clone)]
pub struct ToolDefinition {
    pub name: String,
    pub description: String,
    pub input_schema: Value,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_list_tools_returns_tools() {
        let tools = ToolRegistry::list_tools();
        assert!(!tools.is_empty());
    }

    #[test]
    fn test_list_tools_contains_expected() {
        let tools = ToolRegistry::list_tools();
        let names: Vec<_> = tools.iter().map(|t| t.name.as_str()).collect();
        assert!(names.contains(&"query_file"));
        assert!(names.contains(&"get_dependencies"));
        assert!(names.contains(&"get_impact_radius"));
    }

    #[test]
    fn test_tool_definitions_have_schemas() {
        let tools = ToolRegistry::list_tools();
        for tool in &tools {
            assert!(!tool.description.is_empty());
            assert!(tool.input_schema.is_object());
        }
    }

    #[test]
    fn test_list_tools_contains_v2_tools() {
        let tools = ToolRegistry::list_tools();
        let names: Vec<_> = tools.iter().map(|t| t.name.as_str()).collect();
        assert!(names.contains(&"query_incidents"));
        assert!(names.contains(&"find_env_conflicts"));
        assert!(names.contains(&"get_service_context"));
    }

    #[test]
    fn test_v2_tool_schemas_are_valid() {
        let tools = ToolRegistry::list_tools();
        let v2_tools = [
            "query_incidents",
            "find_env_conflicts",
            "get_service_context",
        ];
        for tool in &tools {
            if v2_tools.contains(&tool.name.as_str()) {
                assert!(!tool.description.is_empty());
                assert!(tool.input_schema.is_object());
                let schema = tool.input_schema.as_object().unwrap();
                assert!(schema.contains_key("type"));
                assert!(schema.contains_key("properties"));
                assert!(schema.contains_key("required"));
            }
        }
    }

    #[test]
    fn test_semantic_search_tool_exists() {
        let tools = ToolRegistry::list_tools();
        let names: Vec<_> = tools.iter().map(|t| t.name.as_str()).collect();
        assert!(names.contains(&"semantic_search"));

        let tool = tools.iter().find(|t| t.name == "semantic_search").unwrap();
        assert!(tool.description.contains("Natural language"));
        assert!(tool.input_schema.is_object());
        let schema = tool.input_schema.as_object().unwrap();
        assert!(schema.contains_key("properties"));
        let properties = schema["properties"].as_object().unwrap();
        assert!(properties.contains_key("query"));
        assert!(properties.contains_key("env"));
        assert!(properties.contains_key("limit"));
    }
}
