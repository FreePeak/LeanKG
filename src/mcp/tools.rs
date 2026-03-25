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
                    }
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
                    }
                }),
            },
            ToolDefinition {
                name: "mcp_install".to_string(),
                description: "Create .mcp.json for MCP client configuration".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "mcp_config_path": {"type": "string", "description": "Path for .mcp.json (default: .mcp.json)"}
                    }
                }),
            },
            ToolDefinition {
                name: "mcp_status".to_string(),
                description: "Show LeanKG index status".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {}
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
                        "pattern": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_dependencies".to_string(),
                description: "Get file dependencies (direct imports)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_dependents".to_string(),
                description: "Get files depending on target".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_impact_radius".to_string(),
                description: "Get all files affected by change within N hops".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"},
                        "depth": {"type": "integer", "default": 3}
                    }
                }),
            },
            ToolDefinition {
                name: "get_review_context".to_string(),
                description: "Generate focused subgraph + structured review prompt".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "files": {"type": "array", "items": {"type": "string"}}
                    }
                }),
            },
            ToolDefinition {
                name: "get_context".to_string(),
                description: "Get AI context for file (minimal, token-optimized)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "find_function".to_string(),
                description: "Locate function definition".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "name": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_call_graph".to_string(),
                description: "Get function call chain (full depth)".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "function": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "search_code".to_string(),
                description: "Search code elements by name/type".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "query": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "generate_doc".to_string(),
                description: "Generate documentation for file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "find_large_functions".to_string(),
                description: "Find oversized functions by line count".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "min_lines": {"type": "integer", "default": 50}
                    }
                }),
            },
            ToolDefinition {
                name: "get_tested_by".to_string(),
                description: "Get test coverage for a function/file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_doc_for_file".to_string(),
                description: "Get documentation files that reference a code element".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_files_for_doc".to_string(),
                description: "Get code elements referenced in a documentation file".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "doc": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_doc_structure".to_string(),
                description: "Get documentation directory structure".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {}
                }),
            },
            ToolDefinition {
                name: "get_traceability".to_string(),
                description: "Get full traceability chain for a code element".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "element": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "search_by_requirement".to_string(),
                description: "Find code elements related to a specific requirement".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "requirement_id": {"type": "string"}
                    }
                }),
            },
            ToolDefinition {
                name: "get_doc_tree".to_string(),
                description: "Get documentation tree structure with hierarchy".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {}
                }),
            },
            ToolDefinition {
                name: "get_code_tree".to_string(),
                description: "Get codebase structure".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {}
                }),
            },
            ToolDefinition {
                name: "find_related_docs".to_string(),
                description: "Find documentation related to a code change".to_string(),
                input_schema: json!({
                    "type": "object",
                    "properties": {
                        "file": {"type": "string"}
                    }
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
