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
            "mcp_init" => self.mcp_init(arguments),
            "mcp_index" => self.mcp_index(arguments).await,
            "mcp_install" => self.mcp_install(arguments),
            "mcp_status" => self.mcp_status(arguments),
            "mcp_impact" => self.mcp_impact(arguments),
            "query_file" => self.query_file(arguments),
            "get_dependencies" => self.get_dependencies(arguments),
            "get_dependents" => self.get_dependents(arguments),
            "get_impact_radius" => self.get_impact_radius(arguments),
            "get_review_context" => self.get_review_context(arguments),
            "get_context" => self.get_context(arguments),
            "find_function" => self.find_function(arguments),
            "get_call_graph" => self.get_call_graph(arguments),
            "search_code" => self.search_code(arguments),
            "generate_doc" => self.generate_doc(arguments),
            "find_large_functions" => self.find_large_functions(arguments),
            "get_tested_by" => self.get_tested_by(arguments),
            "get_doc_for_file" => self.get_doc_for_file(arguments),
            "get_files_for_doc" => self.get_files_for_doc(arguments),
            "get_doc_structure" => self.get_doc_structure(arguments),
            "get_traceability" => self.get_traceability(arguments),
            "search_by_requirement" => self.search_by_requirement(arguments),
            "get_doc_tree" => self.get_doc_tree(arguments),
            "get_code_tree" => self.get_code_tree(arguments),
            "find_related_docs" => self.find_related_docs(arguments),
            _ => Err(format!("Unknown tool: {}", tool_name)),
        }
    }

    fn mcp_init(&self, args: &Value) -> Result<Value, String> {
        let path = args["path"].as_str().unwrap_or(".leankg");

        std::fs::create_dir_all(path).map_err(|e| format!("Failed to create directory: {}", e))?;

        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config).map_err(|e| format!("Failed to serialize config: {}", e))?;
        std::fs::write(std::path::Path::new(path).join("leankg.yaml"), config_yaml)
            .map_err(|e| format!("Failed to write config: {}", e))?;

        Ok(json!({
            "success": true,
            "message": format!("Initialized LeanKG project at {}", path),
            "path": path
        }))
    }

    fn mcp_install(&self, args: &Value) -> Result<Value, String> {
        let mcp_config_path = args["mcp_config_path"].as_str().unwrap_or(".mcp.json");

        let exe_path = std::env::current_exe().map_err(|e| format!("Failed to get current exe path: {}", e))?;

        let mcp_config = serde_json::json!({
            "mcpServers": {
                "leankg": {
                    "command": exe_path.to_string_lossy().as_ref(),
                    "args": ["mcp-stdio", "--watch"]
                }
            }
        });

        std::fs::write(mcp_config_path, serde_json::to_string_pretty(&mcp_config).unwrap())
            .map_err(|e| format!("Failed to write .mcp.json: {}", e))?;

        Ok(json!({
            "success": true,
            "message": format!("Created MCP config at {}", mcp_config_path),
            "path": mcp_config_path
        }))
    }

    async fn mcp_index(&self, args: &Value) -> Result<Value, String> {
        let path = args["path"].as_str().unwrap_or(".");
        let incremental = args["incremental"].as_bool().unwrap_or(false);
        let lang = args["lang"].as_str();
        let exclude = args["exclude"].as_str();

        let db_path = std::path::PathBuf::from(".leankg");
        tokio::fs::create_dir_all(&db_path).await.map_err(|e| format!("Failed to create .leankg: {}", e))?;

        let exclude_patterns: Vec<String> = exclude
            .map(|e| e.split(',').map(|s| s.trim().to_string()).collect())
            .unwrap_or_default();

        let db = crate::db::schema::init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager.init_parsers().map_err(|e| format!("Parser init error: {}", e))?;

        let files = crate::indexer::find_files_sync(path).map_err(|e| format!("Find files error: {}", e))?;

        let mut indexed = 0;
        let mut skipped = 0;

        for file_path in &files {
            if let Some(lang_filter) = lang {
                let allowed_langs: Vec<&str> = lang_filter.split(',').map(|s| s.trim()).collect();
                if let Some(ext) = std::path::Path::new(file_path).extension() {
                    let ext_str = ext.to_string_lossy().to_lowercase();
                    let lang_map: std::collections::HashMap<&str, &str> = [
                        ("go", "go"),
                        ("rs", "rust"),
                        ("ts", "typescript"),
                        ("js", "javascript"),
                        ("py", "python"),
                    ].iter().cloned().collect();
                    if let Some(lang_name) = lang_map.get(ext_str.as_str()) {
                        if !allowed_langs.iter().any(|l| l.to_lowercase() == *lang_name) {
                            continue;
                        }
                    }
                }
            }

            if !exclude_patterns.is_empty() && exclude_patterns.iter().any(|pat| file_path.contains(pat)) {
                continue;
            }

            match crate::indexer::index_file_sync(&graph_engine, &mut parser_manager, file_path) {
                Ok(_) => indexed += 1,
                Err(_) => skipped += 1,
            }
        }

        Ok(json!({
            "success": true,
            "message": format!("Indexed {} files, {} skipped", indexed, skipped),
            "indexed": indexed,
            "skipped": skipped,
            "path": path
        }))
    }

    fn mcp_status(&self, _args: &Value) -> Result<Value, String> {
        let db_path = std::path::PathBuf::from(".leankg");

        if !db_path.exists() {
            return Ok(json!({
                "initialized": false,
                "message": "LeanKG not initialized. Run mcp_init first."
            }));
        }

        let db = crate::db::schema::init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;

        let graph_engine = crate::graph::GraphEngine::new(db.clone());
        let elements = graph_engine.all_elements().map_err(|e| e.to_string())?;
        let relationships = graph_engine.all_relationships().map_err(|e| e.to_string())?;
        let annotations = crate::db::all_business_logic(&db).map_err(|e| e.to_string())?;

        let files = elements.iter().filter(|e| e.element_type == "file").count();
        let functions = elements.iter().filter(|e| e.element_type == "function").count();
        let classes = elements.iter().filter(|e| e.element_type == "class").count();

        Ok(json!({
            "initialized": true,
            "database": ".leankg",
            "elements": elements.len(),
            "relationships": relationships.len(),
            "files": files,
            "functions": functions,
            "classes": classes,
            "annotations": annotations.len()
        }))
    }

    fn mcp_impact(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
        let depth = args["depth"].as_u64().unwrap_or(3) as u32;

        let db_path = std::path::PathBuf::from(".leankg");
        let db = crate::db::schema::init_db(&db_path).map_err(|e| format!("Database error: {}", e))?;
        let graph_engine = crate::graph::GraphEngine::new(db);
        let analyzer = crate::graph::ImpactAnalyzer::new(&graph_engine);

        let result = analyzer.calculate_impact_radius(file, depth).map_err(|e| e.to_string())?;

        Ok(json!({
            "start_file": result.start_file,
            "max_depth": result.max_depth,
            "affected_count": result.affected_elements.len(),
            "elements": result.affected_elements.iter().map(|e| json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path
            })).collect::<Vec<_>>()
        }))
    }

    fn query_file(&self, args: &Value) -> Result<Value, String> {
        let pattern = args["pattern"]
            .as_str()
            .ok_or("Missing 'pattern' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
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

    fn get_dependencies(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
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

    fn get_dependents(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_dependents(file)
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

    fn get_impact_radius(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
        let depth = args["depth"].as_u64().unwrap_or(3) as u32;

        let analyzer = ImpactAnalyzer::new(&self.graph_engine);
        let result = analyzer
            .calculate_impact_radius(file, depth)
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

    fn get_review_context(&self, args: &Value) -> Result<Value, String> {
        let files = args["files"]
            .as_array()
            .ok_or("Missing 'files' parameter")?;

        let mut context_elements = Vec::new();
        let mut context_relationships = Vec::new();

        for file_val in files {
            if let Some(file_path) = file_val.as_str() {
                if let Ok(elements) = self.graph_engine.all_elements() {
                    let file_elements: Vec<_> = elements
                        .into_iter()
                        .filter(|e| e.file_path.contains(file_path))
                        .collect();
                    context_elements.extend(file_elements);
                }

                if let Ok(rels) = self.graph_engine.get_relationships(file_path) {
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

    fn get_context(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let max_tokens = args["max_tokens"].as_u64().unwrap_or(4000) as usize;

        let result = self
            .graph_engine
            .get_context(file, max_tokens)
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

    fn find_function(&self, args: &Value) -> Result<Value, String> {
        let name = args["name"].as_str().ok_or("Missing 'name' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
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

    fn get_call_graph(&self, args: &Value) -> Result<Value, String> {
        let function = args["function"]
            .as_str()
            .ok_or("Missing 'function' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(function)
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

    fn search_code(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
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

    fn generate_doc(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let file_elements: Vec<CodeElement> = elements
            .into_iter()
            .filter(|e| e.file_path.contains(file))
            .collect();

        let doc = generate_documentation(file, &file_elements);

        Ok(json!({ "documentation": doc }))
    }

    fn find_large_functions(&self, args: &Value) -> Result<Value, String> {
        let min_lines = args["min_lines"].as_u64().unwrap_or(50) as u32;

        let elements = self
            .graph_engine
            .all_elements()
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

    fn get_tested_by(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
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

    fn get_doc_for_file(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
            .map_err(|e| e.to_string())?;

        let docs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "documented_by")
            .map(|r| {
                json!({
                    "doc": r.source_qualified,
                    "context": r.metadata.get("context").and_then(|v| v.as_str()).unwrap_or("")
                })
            })
            .collect();

        Ok(json!({ "documents": docs }))
    }

    fn get_files_for_doc(&self, args: &Value) -> Result<Value, String> {
        let doc = args["doc"].as_str().ok_or("Missing 'doc' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(doc)
            .map_err(|e| e.to_string())?;

        let files: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "references")
            .map(|r| {
                json!({
                    "file": r.target_qualified,
                    "context": r.metadata.get("context").and_then(|v| v.as_str()).unwrap_or("")
                })
            })
            .collect();

        Ok(json!({ "files": files }))
    }

    fn get_doc_structure(&self, _args: &Value) -> Result<Value, String> {
        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let docs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "document")
            .map(|e| {
                let category = e
                    .metadata
                    .get("category")
                    .and_then(|v| v.as_str())
                    .unwrap_or("root");
                let headings = e
                    .metadata
                    .get("headings")
                    .and_then(|v| v.as_array())
                    .map(|arr| arr.iter().filter_map(|v| v.as_str()).collect::<Vec<_>>())
                    .unwrap_or_default();
                json!({
                    "qualified_name": e.qualified_name,
                    "title": e.name,
                    "category": category,
                    "headings": headings,
                    "file_path": e.file_path
                })
            })
            .collect();

        Ok(json!({ "documents": docs }))
    }

    fn get_traceability(&self, args: &Value) -> Result<Value, String> {
        let element = args["element"]
            .as_str()
            .ok_or("Missing 'element' parameter")?;

        let report = self
            .graph_engine
            .get_traceability_report(element)
            .map_err(|e| e.to_string())?;

        let entries: Vec<_> = report
            .entries
            .iter()
            .map(|e| {
                let doc_links: Vec<_> = e
                    .doc_links
                    .iter()
                    .map(|d| {
                        json!({
                            "doc": d.doc_qualified,
                            "title": d.doc_title,
                            "context": d.context
                        })
                    })
                    .collect();
                json!({
                    "element": e.element_qualified,
                    "description": e.description,
                    "user_story_id": e.user_story_id,
                    "feature_id": e.feature_id,
                    "doc_links": doc_links
                })
            })
            .collect();

        Ok(json!({ "traceability": entries }))
    }

    fn search_by_requirement(&self, args: &Value) -> Result<Value, String> {
        let requirement_id = args["requirement_id"]
            .as_str()
            .ok_or("Missing 'requirement_id' parameter")?;

        let entries = self
            .graph_engine
            .get_code_for_requirement(requirement_id)
            .map_err(|e| e.to_string())?;

        let results: Vec<_> = entries
            .iter()
            .map(|e| {
                let doc_links: Vec<_> = e
                    .doc_links
                    .iter()
                    .map(|d| {
                        json!({
                            "doc": d.doc_qualified,
                            "title": d.doc_title
                        })
                    })
                    .collect();
                json!({
                    "element": e.element_qualified,
                    "description": e.description,
                    "doc_links": doc_links
                })
            })
            .collect();

        Ok(json!({ "code_elements": results }))
    }

    fn get_doc_tree(&self, _args: &Value) -> Result<Value, String> {
        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let mut tree = serde_json::Map::new();

        for elem in elements
            .iter()
            .filter(|e| e.element_type == "document" || e.element_type == "doc_section")
        {
            let parts: Vec<&str> = elem.qualified_name.split("::").collect();
            if parts.is_empty() {
                continue;
            }

            let category = elem
                .metadata
                .get("category")
                .and_then(|v| v.as_str())
                .unwrap_or("root");

            let node = json!({
                "qualified_name": elem.qualified_name,
                "name": elem.name,
                "type": elem.element_type,
                "line_start": elem.line_start,
                "line_end": elem.line_end
            });

            if !tree.contains_key(category) {
                tree.insert(category.to_string(), json!({}));
            }

            if let Some(cat_obj) = tree.get_mut(category) {
                if let Some(obj) = cat_obj.as_object_mut() {
                    obj.insert(elem.name.clone(), node);
                }
            }
        }

        Ok(json!({ "tree": tree }))
    }

    fn get_code_tree(&self, _args: &Value) -> Result<Value, String> {
        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let mut tree = serde_json::Map::new();

        for elem in &elements {
            if elem.element_type == "file" || elem.qualified_name.contains("::") {
                continue;
            }

            let parts: Vec<&str> = elem.file_path.split('/').collect();
            if parts.is_empty() {
                continue;
            }

            let file_name = parts.last().unwrap_or(&"");

            if !tree.contains_key(*file_name) {
                tree.insert(
                    file_name.to_string(),
                    json!({
                        "file_path": elem.file_path,
                        "elements": Vec::<Value>::new()
                    }),
                );
            }

            if let Some(file_obj) = tree.get_mut(*file_name) {
                if let Some(obj) = file_obj.as_object_mut() {
                    if let Some(elems) = obj.get_mut("elements") {
                        if let Some(arr) = elems.as_array_mut() {
                            arr.push(json!({
                                "qualified_name": elem.qualified_name,
                                "name": elem.name,
                                "type": elem.element_type,
                                "line_start": elem.line_start,
                                "line_end": elem.line_end
                            }));
                        }
                    }
                }
            }
        }

        Ok(json!({ "code_tree": tree }))
    }

    fn find_related_docs(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let relationships = self
            .graph_engine
            .get_relationships(file)
            .map_err(|e| e.to_string())?;

        let related: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "documented_by" || r.rel_type == "references")
            .map(|r| {
                json!({
                    "doc": if r.rel_type == "documented_by" { r.source_qualified.clone() } else { r.target_qualified.clone() },
                    "relationship": r.rel_type,
                    "context": r.metadata.get("context").and_then(|v| v.as_str()).unwrap_or("")
                })
            })
            .collect();

        Ok(json!({ "related_docs": related }))
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
        prompt += &format!("### {}\n\n", file);
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
