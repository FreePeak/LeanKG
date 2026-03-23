use crate::db::models::{CodeElement, Relationship};
use crate::graph::{GraphEngine, ImpactAnalyzer};
use serde_json::{json, Value};

pub struct ToolHandler {
    graph_engine: GraphEngine,
}

impl ToolHandler {
    pub fn new(graph_engine: GraphEngine) -> Self {
        Self { graph_engine }
    }

    pub async fn execute_tool(&self, tool_name: &str, arguments: &Value) -> Result<Value, String> {
        match tool_name {
            "query_file" => self.query_file(arguments).await,
            "get_dependencies" => self.get_dependencies(arguments).await,
            "get_dependents" => self.get_dependents(arguments).await,
            "get_impact_radius" => self.get_impact_radius(arguments).await,
            "get_review_context" => self.get_review_context(arguments).await,
            "get_context" => self.get_context(arguments).await,
            "find_function" => self.find_function(arguments).await,
            "get_call_graph" => self.get_call_graph(arguments).await,
            "search_code" => self.search_code(arguments).await,
            "generate_doc" => self.generate_doc(arguments).await,
            "find_large_functions" => self.find_large_functions(arguments).await,
            "get_tested_by" => self.get_tested_by(arguments).await,
            _ => Err(format!("Unknown tool: {}", tool_name)),
        }
    }

    async fn query_file(&self, args: &Value) -> Result<Value, String> {
        let pattern = args["pattern"]
            .as_str()
            .ok_or("Missing 'pattern' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
            .await
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .filter(|e| e.file_path.contains(pattern) || e.qualified_name.contains(pattern))
            .take(50)
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "type": e.element_type,
                    "file": e.file_path,
                    "line": e.line_start
                })
            })
            .collect();

        Ok(json!({ "files": matches }))
    }

    async fn get_dependencies(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
            .await
            .map_err(|e| e.to_string())?;

        let deps: Vec<_> = relationships
            .iter()
            .map(|r| {
                json!({
                    "target": r.target_qualified,
                    "type": r.rel_type
                })
            })
            .collect();

        Ok(json!({ "dependencies": deps }))
    }

    async fn get_dependents(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_dependents(file)
            .await
            .map_err(|e| e.to_string())?;

        let deps: Vec<_> = relationships
            .iter()
            .map(|r| {
                json!({
                    "source": r.source_qualified,
                    "type": r.rel_type
                })
            })
            .collect();

        Ok(json!({ "dependents": deps }))
    }

    async fn get_impact_radius(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
        let depth = args["depth"].as_u64().unwrap_or(3) as u32;

        let analyzer = ImpactAnalyzer::new(&self.graph_engine);
        let result = analyzer
            .calculate_impact_radius(file, depth)
            .await
            .map_err(|e| e.to_string())?;

        Ok(json!({
            "start_file": result.start_file,
            "max_depth": result.max_depth,
            "affected": result.affected_elements.len(),
            "elements": result.affected_elements.iter().map(|e| json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path
            })).collect::<Vec<_>>()
        }))
    }

    async fn get_review_context(&self, args: &Value) -> Result<Value, String> {
        let files = args["files"]
            .as_array()
            .ok_or("Missing 'files' parameter")?;

        let mut context_elements = Vec::new();
        let mut context_relationships = Vec::new();

        for file_val in files {
            if let Some(file_path) = file_val.as_str() {
                if let Ok(elements) = self.graph_engine.all_elements().await {
                    let file_elements: Vec<_> = elements
                        .into_iter()
                        .filter(|e| e.file_path.contains(file_path))
                        .collect();
                    context_elements.extend(file_elements);
                }

                if let Ok(rels) = self.graph_engine.get_relationships(file_path).await {
                    context_relationships.extend(rels);
                }
            }
        }

        let review_prompt = generate_review_prompt(&context_elements, &context_relationships);

        Ok(json!({
            "elements": context_elements.iter().map(|e| json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path,
                "lines": format!("{}-{}", e.line_start, e.line_end)
            })).collect::<Vec<_>>(),
            "relationships": context_relationships.iter().map(|r| json!({
                "source": r.source_qualified,
                "target": r.target_qualified,
                "type": r.rel_type
            })).collect::<Vec<_>>(),
            "review_prompt": review_prompt
        }))
    }

    async fn get_context(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let max_tokens = args["max_tokens"].as_u64().unwrap_or(4000) as usize;

        let result = self
            .graph_engine
            .get_context(file, max_tokens)
            .await
            .map_err(|e| e.to_string())?;

        let elements_json: Vec<_> = result
            .elements
            .iter()
            .map(|ctx_elem| {
                let elem = &ctx_elem.element;
                let priority_str = match ctx_elem.priority {
                    crate::graph::ContextPriority::RecentlyChanged => "recently_changed",
                    crate::graph::ContextPriority::Imported => "imported",
                    crate::graph::ContextPriority::Contained => "contained",
                };
                json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path,
                    "line_start": elem.line_start,
                    "line_end": elem.line_end,
                    "priority": priority_str,
                    "token_count": ctx_elem.token_count
                })
            })
            .collect();

        Ok(json!({
            "file": file,
            "elements": elements_json,
            "total_tokens": result.total_tokens,
            "max_tokens": result.max_tokens,
            "truncated": result.truncated,
            "prompt": result.to_prompt()
        }))
    }

    async fn find_function(&self, args: &Value) -> Result<Value, String> {
        let name = args["name"].as_str().ok_or("Missing 'name' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
            .await
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "function" && e.name.contains(name))
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "file": e.file_path,
                    "line": e.line_start,
                    "line_end": e.line_end
                })
            })
            .collect();

        Ok(json!({ "functions": matches }))
    }

    async fn get_call_graph(&self, args: &Value) -> Result<Value, String> {
        let function = args["function"]
            .as_str()
            .ok_or("Missing 'function' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(function)
            .await
            .map_err(|e| e.to_string())?;

        let calls: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "calls" || r.rel_type == "imports")
            .map(|r| {
                json!({
                    "target": r.target_qualified,
                    "type": r.rel_type
                })
            })
            .collect();

        Ok(json!({ "calls": calls }))
    }

    async fn search_code(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
            .await
            .map_err(|e| e.to_string())?;

        let query_lower = query.to_lowercase();
        let matches: Vec<_> = elements
            .iter()
            .filter(|e| {
                e.name.to_lowercase().contains(&query_lower)
                    || e.qualified_name.to_lowercase().contains(&query_lower)
                    || e.element_type.to_lowercase().contains(&query_lower)
            })
            .take(100)
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "type": e.element_type,
                    "file": e.file_path,
                    "line": e.line_start
                })
            })
            .collect();

        Ok(json!({ "results": matches }))
    }

    async fn generate_doc(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
            .await
            .map_err(|e| e.to_string())?;

        let file_elements: Vec<CodeElement> = elements
            .into_iter()
            .filter(|e| e.file_path.contains(file))
            .collect();

        let doc = generate_documentation(file, &file_elements);

        Ok(json!({ "documentation": doc }))
    }

    async fn find_large_functions(&self, args: &Value) -> Result<Value, String> {
        let min_lines = args["min_lines"].as_u64().unwrap_or(50) as u32;

        let elements = self
            .graph_engine
            .all_elements()
            .await
            .map_err(|e| e.to_string())?;

        let large_functions: Vec<_> = elements
            .iter()
            .filter(|e| {
                e.element_type == "function"
                    && (e.line_end.saturating_sub(e.line_start)) >= min_lines
            })
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "file": e.file_path,
                    "lines": e.line_end - e.line_start,
                    "line_start": e.line_start,
                    "line_end": e.line_end
                })
            })
            .collect();

        Ok(json!({ "large_functions": large_functions }))
    }

    async fn get_tested_by(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
            .await
            .map_err(|e| e.to_string())?;

        let tests: Vec<_> = relationships
            .iter()
            .filter(|r| {
                r.rel_type == "tested_by"
                    || r.rel_type == "tests"
                    || r.target_qualified.contains("test")
                    || r.target_qualified.contains("spec")
            })
            .map(|r| {
                json!({
                    "test": r.target_qualified,
                    "type": r.rel_type
                })
            })
            .collect();

        Ok(json!({ "tests": tests }))
    }
}

fn generate_review_prompt(elements: &[CodeElement], _relationships: &[Relationship]) -> String {
    if elements.is_empty() {
        return "No elements found for review.".to_string();
    }

    let mut prompt = String::from("# Code Review Context\n\n");
    prompt += &format!("## Files to Review ({} elements)\n\n", elements.len());

    let files: std::collections::HashSet<_> =
        elements.iter().map(|e| e.file_path.clone()).collect();
    for file in files {
        prompt += &format!("### {}\n", file);
        let file_elements: Vec<_> = elements.iter().filter(|e| e.file_path == file).collect();
        for elem in file_elements {
            prompt += &format!(
                "- **{}** (`{}`): lines {}-{}\n",
                elem.name, elem.element_type, elem.line_start, elem.line_end
            );
        }
        prompt += "\n";
    }

    prompt += "## Review Focus\n\n";
    prompt += "- Check function signatures and parameter usage\n";
    prompt += "- Look for potential bugs or edge cases\n";
    prompt += "- Identify any security concerns\n";
    prompt += "- Evaluate error handling patterns\n";

    prompt
}

fn generate_documentation(file_path: &str, elements: &[CodeElement]) -> String {
    let mut doc = String::new();
    doc += &format!("# Documentation for {}\n\n", file_path);

    if elements.is_empty() {
        doc += "No indexed elements found for this file.\n";
        return doc;
    }

    doc += "## Overview\n\n";
    doc += &format!("This file contains {} code elements.\n\n", elements.len());

    let functions: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "function")
        .collect();
    let classes: Vec<_> = elements
        .iter()
        .filter(|e| e.element_type == "class")
        .collect();

    if !functions.is_empty() {
        doc += &format!("## Functions ({})\n\n", functions.len());
        for func in functions {
            doc += &format!("### `{}`\n\n", func.name);
            doc += &format!("- Location: lines {}-{}\n", func.line_start, func.line_end);
            if let Some(parent) = &func.parent_qualified {
                doc += &format!("- Parent: `{}`\n", parent);
            }
            doc += "\n";
        }
    }

    if !classes.is_empty() {
        doc += &format!("## Classes ({})\n\n", classes.len());
        for class in classes {
            doc += &format!("### `{}`\n\n", class.name);
            doc += &format!(
                "- Location: lines {}-{}\n",
                class.line_start, class.line_end
            );
            doc += "\n";
        }
    }

    doc
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_generate_review_prompt_empty() {
        let prompt = generate_review_prompt(&[], &[]);
        assert!(prompt.contains("No elements"));
    }

    #[test]
    fn test_generate_review_prompt_with_elements() {
        let elements = vec![CodeElement {
            qualified_name: "src/main.rs::main".to_string(),
            element_type: "function".to_string(),
            name: "main".to_string(),
            file_path: "src/main.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: json!({}),
        }];
        let prompt = generate_review_prompt(&elements, &[]);
        assert!(prompt.contains("main"));
        assert!(prompt.contains("src/main.rs"));
    }

    #[test]
    fn test_generate_documentation() {
        let elements = vec![CodeElement {
            qualified_name: "src/main.rs".to_string(),
            element_type: "file".to_string(),
            name: "main.rs".to_string(),
            file_path: "src/main.rs".to_string(),
            line_start: 1,
            line_end: 100,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: json!({}),
        }];
        let doc = generate_documentation("src/main.rs", &elements);
        assert!(doc.contains("src/main.rs"));
    }
}
