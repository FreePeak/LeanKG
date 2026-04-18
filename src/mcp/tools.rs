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
                        "lang": {"type": "string", "description": "Filter by language (e.g., go,ts,py,rs)"},
                        "exclude": {"type": "string", "description": "Exclude patterns (comma-separated)"}
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
                        "mcp_config_path": {"type": "string", "description": "Path for .mcp.json (default: .mcp.json)"}
                    },
                    "required": []
                }),
            },
            ToolDefinition {
                name: "mcp_status".to_string(),
                description: "Show LeanKG index status".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {},
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
                        "element_type": {"type": "string", "enum": ["file", "function", "struct", "class", "module"], "description": "Optional filter by element type"},
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
                        "file": {"type": "string", "description": "File to get dependencies for"}
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
                        "file": {"type": "string", "description": "File to get dependents for"}
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
                        "compress_response": {"type": "boolean", "default": false, "description": "Enable RTK-style compression for token savings"}
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
                        "min_confidence": {"type": "number", "default": 0.0, "description": "Minimum confidence threshold for affected symbols."}
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
                        "files": {"type": "array", "items": {"type": "string"}, "description": "Files to include in review context"}
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
                        "max_tokens": {"type": "integer", "default": 4000, "description": "Token budget cap"}
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
                        "fresh": {"type": "boolean", "default": false, "description": "Force fresh query, bypass cache"}
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
                        "lines": {"type": "string", "description": "Lines specification for 'lines' mode (e.g., '1-10,20,30-40')"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "find_function".to_string(),
                description: "Locate function definition by name. Optionally scope to a file.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "Function name to search for"},
                        "file": {"type": "string", "description": "Optional file to scope the search to"}
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
                        "file": {"type": "string", "description": "Optional file to scope the search"}
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
                        "max_results": {"type": "integer", "default": 30, "description": "Maximum number of results (default: 30)"}
                    },
                    "required": ["function"]
                }),
            },
            ToolDefinition {
                name: "search_code".to_string(),
                description: "Search code elements by name/type".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string", "description": "Search query string"},
                        "element_type": {"type": "string", "enum": ["file", "function", "struct", "class", "module", "import"], "description": "Filter by element type"},
                        "limit": {"type": "integer", "default": 20, "description": "Maximum number of results (default: 20, max: 50)"},
                        "project": {"type": "string", "description": "Optional: project path to search in (resolves to nearest .leankg directory)"}
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "generate_doc".to_string(),
                description: "Generate documentation for file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string", "description": "File to generate documentation for"}
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
                        "offset": {"type": "integer", "default": 0, "description": "Number of results to skip (pagination offset)"}
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
                        "file": {"type": "string", "description": "File to get test coverage for"}
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
                        "file": {"type": "string", "description": "File to get documentation for"}
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
                        "doc": {"type": "string", "description": "Documentation file path"}
                    },
                    "required": ["doc"]
                }),
            },
            ToolDefinition {
                name: "get_doc_structure".to_string(),
                description: "Get documentation directory structure".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {},
                    "required": []
                }),
            },
            ToolDefinition {
                name: "get_traceability".to_string(),
                description: "Get full traceability chain for a code element".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "element": {"type": "string", "description": "Code element to trace"}
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
                        "requirement_id": {"type": "string", "description": "Requirement ID to search for"}
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
                        "offset": {"type": "integer", "default": 0, "description": "Number of categories to skip (pagination offset)"}
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
                        "offset": {"type": "integer", "default": 0, "description": "Number of files to skip (pagination offset)"}
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
                        "file": {"type": "string", "description": "File that was changed"}
                    },
                    "required": ["file"]
                }),
            },
            ToolDefinition {
                name: "mcp_hello".to_string(),
                description: "Returns 'Hello, World!'".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {},
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
                        "offset": {"type": "integer", "default": 0, "description": "Number of clusters to skip (pagination offset)"}
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
                        "cluster_label": {"type": "string", "description": "Alternative: cluster label to search for"}
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
                        }
                    },
                    "required": ["query"]
                }),
            },
            ToolDefinition {
                name: "get_service_graph".to_string(),
                description: "Get microservice call graph with service repos as nodes. Returns aggregated service-to-service topology from service_calls relationships. The current service repo is the biggest node.".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "service": {"type": "string", "description": "Current service name (defaults to project directory name)"}
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
}
