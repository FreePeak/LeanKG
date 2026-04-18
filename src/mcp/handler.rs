use crate::compress::{FileReader, ReadMode, ResponseCompressor};
use crate::db::models::ContextMetric;
use crate::db::models::{CodeElement, Relationship};
use crate::db::record_metric;
use crate::graph::{GraphEngine, ImpactAnalyzer};
use crate::orchestrator::QueryOrchestrator;
use serde_json::{json, Value};
use std::process::Command;
use std::time::{Instant, SystemTime, UNIX_EPOCH};

const INSTRUCTIONS_CONTENT: &str = r#"# LeanKG Tools - Usage Instructions

## For AI Coding Agents (Cursor, OpenCode, etc.)

Use LeanKG tools **first** before performing any codebase search, navigation, or impact analysis.

---

## When to Use Each Tool

### Code Discovery & Search

| Task | Use This Tool |
|------|--------------|
| Find a file by name | `query_file` |
| Find a function definition | `find_function` |
| Search code by name/type | `search_code` |
| Get full codebase structure | `get_code_tree` |

### Dependency Analysis

| Task | Use This Tool |
|------|--------------|
| Get direct imports of a file | `get_dependencies` |
| Get files that import/use a file | `get_dependents` |
| Get function call chain (full depth) | `get_call_graph` |
| Get direct callers (who calls this) | `get_callers` |
| Calculate what breaks if file changes | `get_impact_radius` |

### Review & Context

| Task | Use This Tool |
|------|--------------|
| Generate focused review context | `get_review_context` |
| Get minimal AI context (token-optimized) | `get_context` |
| Find oversized functions | `find_large_functions` |

### Testing & Documentation

| Task | Use This Tool |
|------|--------------|
| Get test coverage for a function | `get_tested_by` |
| Get docs that reference a file | `get_doc_for_file` |
| Get code elements in a doc | `get_files_for_doc` |
| Get doc directory structure | `get_doc_structure` |
| Find docs related to a change | `find_related_docs` |

### Traceability & Requirements

| Task | Use This Tool |
|------|--------------|
| Get full traceability chain | `get_traceability` |
| Find code for a requirement | `search_by_requirement` |
| Get doc tree with hierarchy | `get_doc_tree` |

---

## Decision Flow

```
User asks about codebase →
  First check if LeanKG is initialized (mcp_status) →
    If not, use mcp_init first (CRITICAL: pass the absolute path to the project's .leankg directory, e.g. path: "/full/path/to/project/.leankg") →
    Then use appropriate LeanKG tool →
      NEVER fall back to naive grep/search until LeanKG is exhausted
```

---

## Example Usage Patterns

**"Where is the auth function?"**
```
search_code("auth") or find_function("auth")
```

**"What tests cover this file?"**
```
get_tested_by({ file: "src/auth.rs" })
```

**"What would break if I change this file?"**
```
get_impact_radius({ file: "src/main.rs", depth: 3 })
```

**"How does X work end-to-end?"**
```
get_call_graph({ function: "src/auth.rs::authenticate" })
```

---

## Important Notes

- LeanKG maintains a **knowledge graph** of your codebase - use it instead of text search
- `get_impact_radius` calculates blast radius - always check before making changes
- `get_context` returns token-optimized output - use it for AI prompts
- Tools are pre-indexed and **much faster** than runtime grep/search
"#;

pub struct ToolHandler {
    graph_engine: GraphEngine,
    db_path: std::path::PathBuf,
    orchestrator: QueryOrchestrator,
    session_cache: std::sync::Arc<parking_lot::RwLock<crate::compress::SessionCache>>,
}

impl ToolHandler {
    pub fn new(graph_engine: GraphEngine, db_path: std::path::PathBuf) -> Self {
        Self {
            graph_engine: graph_engine.clone(),
            db_path,
            orchestrator: QueryOrchestrator::with_persistence(graph_engine),
            session_cache: std::sync::Arc::new(parking_lot::RwLock::new(
                crate::compress::SessionCache::new(),
            )),
        }
    }

    fn maybe_compress(&self, response: Value, args: &Value, tool_name: &str) -> Value {
        let compress = args["compress_response"].as_bool().unwrap_or(false);
        if !compress {
            return response;
        }

        let compressor = ResponseCompressor::new();
        match tool_name {
            "get_impact_radius" => compressor.compress_impact_radius(&response),
            "get_call_graph" => compressor.compress_call_graph(&response),
            "search_code" => compressor.compress_search_code(&response),
            "get_dependencies" => compressor.compress_dependencies(&response),
            "get_dependents" => compressor.compress_dependents(&response),
            "get_context" => compressor.compress_context(&response),
            _ => response,
        }
    }

    pub async fn execute_tool(&self, tool_name: &str, arguments: &Value) -> Result<Value, String> {
        let start_time = Instant::now();
        let project_path = std::env::current_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_default();

        let result = match tool_name {
            "mcp_init" => self.mcp_init(arguments),
            "mcp_index" => self.mcp_index(arguments).await,
            "mcp_index_docs" => self.mcp_index_docs(arguments),
            "mcp_install" => self.mcp_install(arguments),
            "mcp_status" => self.mcp_status(arguments),
            "mcp_impact" => self.mcp_impact(arguments),
            "detect_changes" => self.detect_changes(arguments),
            "query_file" => self.query_file(arguments),
            "get_dependencies" => self.get_dependencies(arguments),
            "get_dependents" => self.get_dependents(arguments),
            "get_impact_radius" => self.get_impact_radius(arguments),
            "get_review_context" => self.get_review_context(arguments),
            "get_context" => self.get_context(arguments),
            "ctx_read" => self.ctx_read(arguments),
            "orchestrate" => self.orchestrate_tool(arguments),
            "find_function" => self.find_function(arguments),
            "get_callers" => self.get_callers(arguments),
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
            "mcp_hello" => self.mcp_hello(arguments),
            "get_clusters" => self.get_clusters(arguments),
            "get_cluster_context" => self.get_cluster_context(arguments),
            "run_raw_query" => self.run_raw_query(arguments),
            "get_service_graph" => self.get_service_graph(arguments),
            _ => Err(format!("Unknown tool: {}", tool_name)),
        };

        let execution_time_ms = start_time.elapsed().as_millis() as i32;
        let input_tokens = arguments.to_string().len() as i32 / 4;

        let (output_tokens, output_elements, baseline_tokens, baseline_lines, success) =
            match &result {
                Ok(response) => {
                    let response_str = response.to_string();
                    let output_tok = response_str.len() as i32 / 4;
                    let out_elem = Self::count_response_elements(response);
                    let (base_tok, base_lines) = self.estimate_baseline(tool_name, arguments);
                    (output_tok, out_elem, base_tok, base_lines, true)
                }
                Err(_) => (0, 0, 0, 0, false),
            };

        let tokens_saved = baseline_tokens - output_tokens;
        let savings_percent = if baseline_tokens > 0 {
            (tokens_saved as f64 / baseline_tokens as f64) * 100.0
        } else {
            0.0
        };

        let metric = ContextMetric {
            tool_name: tool_name.to_string(),
            timestamp: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap_or_default()
                .as_secs() as i64,
            project_path,
            input_tokens,
            output_tokens,
            output_elements,
            execution_time_ms,
            baseline_tokens,
            baseline_lines_scanned: baseline_lines,
            tokens_saved,
            savings_percent,
            correct_elements: None,
            total_expected: None,
            f1_score: None,
            query_pattern: arguments["query"].as_str().map(String::from),
            query_file: arguments["file"].as_str().map(String::from),
            query_depth: arguments["depth"].as_i64().map(|d| d as i32),
            success,
            is_deleted: false,
        };

        if let Err(e) = record_metric(self.graph_engine.db(), &metric) {
            eprintln!("Failed to record metric: {}", e);
        }

        result
    }

    fn estimate_baseline(&self, tool_name: &str, args: &Value) -> (i32, i32) {
        let src_path = "./src";
        match tool_name {
            "search_code" => {
                if let Some(query) = args["query"].as_str() {
                    let output = Command::new("grep")
                        .args(["-rn", "--include=*.rs", query, src_path])
                        .output();
                    if let Ok(out) = output {
                        let lines = String::from_utf8_lossy(&out.stdout);
                        let line_count = lines.lines().count();
                        return (line_count as i32 * 4, line_count as i32);
                    }
                }
                (0, 0)
            }
            "find_function" => {
                if let Some(name) = args["name"].as_str() {
                    let output = Command::new("grep")
                        .args(["-rn", "--include=*.rs", name, src_path])
                        .output();
                    if let Ok(out) = output {
                        let lines = String::from_utf8_lossy(&out.stdout);
                        let line_count = lines.lines().count();
                        return (line_count as i32 * 4, line_count as i32);
                    }
                }
                (0, 0)
            }
            "query_file" => {
                if let Some(pattern) = args["pattern"].as_str() {
                    let output = Command::new("find")
                        .args([src_path, "-name", pattern])
                        .output();
                    if let Ok(out) = output {
                        let files = String::from_utf8_lossy(&out.stdout);
                        let file_count = files.lines().count();
                        return (file_count as i32 * 50, file_count as i32);
                    }
                }
                (0, 0)
            }
            "get_dependencies" => {
                if let Some(file) = args["file"].as_str() {
                    let output = Command::new("grep")
                        .args(["-n", "import\\|use\\|require", file])
                        .output();
                    if let Ok(out) = output {
                        let lines = String::from_utf8_lossy(&out.stdout);
                        let line_count = lines.lines().count();
                        return (line_count as i32 * 4, line_count as i32);
                    }
                }
                (0, 0)
            }
            "get_dependents" => {
                if let Some(file) = args["file"].as_str() {
                    let output = Command::new("grep")
                        .args(["-rn", &format!("import.*{}", file), src_path])
                        .output();
                    if let Ok(out) = output {
                        let lines = String::from_utf8_lossy(&out.stdout);
                        let line_count = lines.lines().count();
                        return (line_count as i32 * 4, line_count as i32);
                    }
                }
                (0, 0)
            }
            "get_context" => {
                if let Some(file) = args["file"].as_str() {
                    if let Ok(content) = std::fs::read_to_string(file) {
                        let chars = content.len() as i32;
                        return (chars, chars / 80);
                    }
                }
                (0, 0)
            }
            "get_impact_radius" => {
                let depth = args["depth"].as_u64().unwrap_or(3) as i32;
                (depth * 1000, depth * 100)
            }
            _ => (0, 0),
        }
    }

    fn count_response_elements(response: &Value) -> i32 {
        match response {
            Value::Array(arr) => arr.len() as i32,
            Value::Object(obj) => {
                let mut count = 0;
                for (_, v) in obj {
                    count += Self::count_response_elements(v);
                }
                count
            }
            _ => 1,
        }
    }

    fn ctx_read(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
        let mode_str = args["mode"].as_str().unwrap_or("adaptive");
        let lines_spec = args["lines"].as_str();

        let requested_mode = ReadMode::from_str(mode_str)
            .ok_or_else(|| format!("Invalid mode: {}. Valid modes: adaptive, full, map, signatures, diff, aggressive, entropy, lines", mode_str))?;

        let mut reader = FileReader::new(self.session_cache.clone());
        let fresh = args["fresh"].as_bool().unwrap_or(false);

        let result = if requested_mode == ReadMode::Adaptive {
            let content = std::fs::read_to_string(file)
                .map_err(|e| format!("Failed to read file {}: {}", file, e))?;
            let lines: Vec<&str> = content.lines().collect();
            let lines_count = lines.len();
            let file_size = content.len();

            let selected_mode = ReadMode::select_adaptive(file, file_size, lines_count);
            reader
                .read(file, selected_mode, lines_spec, fresh)
                .map_err(|e| e.to_string())?
        } else {
            reader
                .read(file, requested_mode, lines_spec, fresh)
                .map_err(|e| e.to_string())?
        };

        let file_name = std::path::Path::new(file)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy();

        let header = format!(
            "{} [{}L] mode={}",
            file_name, result.output_lines, result.mode
        );
        let footer = format!(
            "---\noriginal: {} tokens | sent: {} tokens ({:.1}% saved)",
            result.total_tokens, result.tokens, result.savings_percent
        );

        let final_string = format!("{}\n{}\n{}", header, result.content, footer);
        Ok(Value::String(final_string))
    }

    fn orchestrate_tool(&self, args: &Value) -> Result<Value, String> {
        let intent = args["intent"]
            .as_str()
            .ok_or("Missing 'intent' parameter")?;
        let file = args["file"].as_str();
        let mode = args["mode"].as_str();
        let fresh = args["fresh"].as_bool().unwrap_or(false);

        let result = self.orchestrator.orchestrate(intent, file, mode, fresh)?;

        Ok(json!({
            "intent": result.intent,
            "query_type": result.query_type,
            "content": result.content,
            "mode": result.mode,
            "tokens": result.tokens,
            "total_tokens": result.total_tokens,
            "savings_percent": result.savings_percent,
            "is_cached": result.is_cached,
            "cache_key": result.cache_key,
            "elements_count": result.elements_count
        }))
    }

    fn mcp_init(&self, args: &Value) -> Result<Value, String> {
        let path = args["path"].as_str().unwrap_or(".leankg");

        std::fs::create_dir_all(path).map_err(|e| format!("Failed to create directory: {}", e))?;

        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config)
            .map_err(|e| format!("Failed to serialize config: {}", e))?;
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

        let exe_path = std::env::current_exe()
            .map_err(|e| format!("Failed to get current exe path: {}", e))?;

        let mcp_config = serde_json::json!({
            "mcpServers": {
                "leankg": {
                    "command": exe_path.to_string_lossy().as_ref(),
                    "args": ["mcp-stdio", "--watch"]
                }
            }
        });

        std::fs::write(
            mcp_config_path,
            serde_json::to_string_pretty(&mcp_config).unwrap(),
        )
        .map_err(|e| format!("Failed to write .mcp.json: {}", e))?;

        let instructions_dir = "instructions";
        let instructions_path = format!("{}/leankg-tools.md", instructions_dir);
        std::fs::create_dir_all(instructions_dir)
            .map_err(|e| format!("Failed to create instructions directory: {}", e))?;
        std::fs::write(&instructions_path, INSTRUCTIONS_CONTENT)
            .map_err(|e| format!("Failed to write instructions: {}", e))?;

        let opencode_config_path = ".opencode.json";
        let opencode_config = serde_json::json!({
            "$schema": "https://opencode.ai/config.json",
            "plugins": ["leankg"],
            "instructions": [instructions_path]
        });

        std::fs::write(
            opencode_config_path,
            serde_json::to_string_pretty(&opencode_config).unwrap(),
        )
        .map_err(|e| format!("Failed to write opencode.json: {}", e))?;

        Ok(json!({
            "success": true,
            "message": format!("Created MCP config at {}, opencode.json, and instructions at {}. Copy instructions to ~/.config/opencode/ for AI agents to auto-load them.", mcp_config_path, instructions_path),
            "mcp_path": mcp_config_path,
            "opencode_path": opencode_config_path,
            "instructions_path": instructions_path
        }))
    }

    async fn mcp_index(&self, args: &Value) -> Result<Value, String> {
        let path = args["path"].as_str().unwrap_or(".");
        let _incremental = args["incremental"].as_bool().unwrap_or(false);
        let lang = args["lang"].as_str();
        let exclude = args["exclude"].as_str();

        let db_path = self.db_path.clone();
        tokio::fs::create_dir_all(&db_path)
            .await
            .map_err(|e| format!("Failed to create .leankg: {}", e))?;

        let exclude_patterns: Vec<String> = exclude
            .map(|e| e.split(',').map(|s| s.trim().to_string()).collect())
            .unwrap_or_default();

        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        let files = crate::indexer::find_files_sync(path)
            .map_err(|e| format!("Find files error: {}", e))?;

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
                    ]
                    .iter()
                    .cloned()
                    .collect();
                    if let Some(lang_name) = lang_map.get(ext_str.as_str()) {
                        if !allowed_langs.iter().any(|l| l.to_lowercase() == *lang_name) {
                            continue;
                        }
                    }
                }
            }

            if !exclude_patterns.is_empty()
                && exclude_patterns.iter().any(|pat| file_path.contains(pat))
            {
                continue;
            }

            match crate::indexer::index_file_sync(
                &self.graph_engine,
                &mut parser_manager,
                file_path,
            ) {
                Ok(_) => indexed += 1,
                Err(_) => skipped += 1,
            }
        }

        let resolved = self.graph_engine.resolve_call_edges().unwrap_or(0);

        Ok(json!({
            "success": true,
            "message": format!("Indexed {} files, {} skipped, {} call edges resolved", indexed, skipped, resolved),
            "indexed": indexed,
            "skipped": skipped,
            "resolved": resolved,
            "path": path
        }))
    }

    fn mcp_index_docs(&self, args: &Value) -> Result<Value, String> {
        let docs_path = args["path"].as_str().unwrap_or("./docs");
        let path = std::path::Path::new(docs_path);

        if !path.exists() {
            return Err(format!("Docs path does not exist: {}", docs_path));
        }

        let result = crate::doc_indexer::index_docs_directory(path, &self.graph_engine)
            .map_err(|e| e.to_string())?;

        Ok(json!({
            "success": true,
            "documents": result.documents.len(),
            "sections": result.sections.len(),
            "relationships": result.relationships.len(),
            "path": docs_path,
            "message": format!(
                "Indexed {} documents, {} sections, {} relationships",
                result.documents.len(),
                result.sections.len(),
                result.relationships.len()
            )
        }))
    }

    fn mcp_status(&self, _args: &Value) -> Result<Value, String> {
        let db_path = &self.db_path;

        if !db_path.exists() {
            return Ok(json!({
                "initialized": false,
                "message": "LeanKG not initialized. Run mcp_init first."
            }));
        }

        let db = self.graph_engine.db();
        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;
        let relationships = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;
        let annotations = crate::db::all_business_logic(db).map_err(|e| e.to_string())?;

        let unique_files: std::collections::HashSet<_> =
            elements.iter().map(|e| e.file_path.clone()).collect();
        let files = unique_files.len();
        let functions = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();
        let classes = elements
            .iter()
            .filter(|e| e.element_type == "class" || e.element_type == "struct")
            .count();

        Ok(json!({
            "initialized": true,
            "database": db_path.to_string_lossy(),
            "elements": elements.len(),
            "relationships": relationships.len(),
            "files": files,
            "functions": functions,
            "classes": classes,
            "annotations": annotations.len()
        }))
    }

    fn mcp_hello(&self, _args: &Value) -> Result<Value, String> {
        Ok(json!({
            "message": "Hello, World!"
        }))
    }

    fn mcp_impact(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;
        let depth = args["depth"].as_u64().unwrap_or(3) as u32;

        let analyzer = crate::graph::ImpactAnalyzer::new(&self.graph_engine);

        let result = analyzer
            .calculate_impact_radius(file, depth)
            .map_err(|e| e.to_string())?;

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

    fn detect_changes(&self, args: &Value) -> Result<Value, String> {
        let scope = args["scope"].as_str().unwrap_or("all");
        let min_confidence = args["min_confidence"].as_f64().unwrap_or(0.0);

        let changed_files = match scope {
            "staged" => {
                crate::indexer::GitAnalyzer::get_staged_files().unwrap_or_else(|_| Vec::new())
            }
            "unstaged" => {
                let changed = crate::indexer::GitAnalyzer::get_changed_files_since_last_commit()
                    .unwrap_or_else(|_| crate::indexer::GitChangedFiles {
                        modified: Vec::new(),
                        added: Vec::new(),
                        deleted: Vec::new(),
                    });
                let mut files = changed.modified;
                files.extend(changed.added);
                files.extend(changed.deleted);
                files
            }
            _ => {
                let changed = crate::indexer::GitAnalyzer::get_changed_files_since_last_commit()
                    .unwrap_or_else(|_| crate::indexer::GitChangedFiles {
                        modified: Vec::new(),
                        added: Vec::new(),
                        deleted: Vec::new(),
                    });
                let mut files = changed.modified;
                files.extend(changed.added);
                files.extend(changed.deleted);
                files.extend(
                    crate::indexer::GitAnalyzer::get_untracked_files()
                        .unwrap_or_else(|_| Vec::new()),
                );
                files
            }
        };

        let mut changed_symbols = Vec::new();
        let mut affected_symbols = Vec::new();
        let mut risk_reasons = Vec::new();
        let mut max_dependents_at_depth1 = 0;
        let mut has_public_api_change = false;

        let all_elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;
        let all_relationships = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;

        for file in &changed_files {
            let file_elements: Vec<_> = all_elements
                .iter()
                .filter(|e| &e.file_path == file)
                .collect();

            for elem in file_elements {
                changed_symbols.push(json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path
                }));

                let dependents: Vec<_> = all_relationships
                    .iter()
                    .filter(|r| r.target_qualified == elem.qualified_name && r.rel_type == "calls")
                    .collect();

                let depth1_count = dependents.len();
                max_dependents_at_depth1 = max_dependents_at_depth1.max(depth1_count);

                if depth1_count >= 10 {
                    risk_reasons.push(format!(
                        "{} has {} direct callers (>=10)",
                        elem.name, depth1_count
                    ));
                } else if depth1_count >= 5 {
                    risk_reasons.push(format!(
                        "{} has {} direct callers (>=5)",
                        elem.name, depth1_count
                    ));
                }

                if elem.element_type == "function"
                    && (elem.name.starts_with("pub_")
                        || elem.name.starts_with("export_")
                        || elem.name == "main")
                {
                    has_public_api_change = true;
                    risk_reasons.push(format!("Public API change detected: {}", elem.name));
                }
            }
        }

        let min_confidence_filter = if min_confidence > 0.0 {
            min_confidence
        } else {
            0.0
        };

        for file in &changed_files {
            let dependents = crate::indexer::find_dependents(
                file,
                &all_relationships
                    .iter()
                    .map(|r| (r.source_qualified.clone(), r.target_qualified.clone()))
                    .collect::<Vec<_>>(),
            );

            for dep_file in dependents {
                if let Ok(Some(elem)) = self.graph_engine.find_element(&dep_file) {
                    let rels: Vec<_> = all_relationships
                        .iter()
                        .filter(|r| {
                            r.target_qualified == elem.qualified_name && r.rel_type == "calls"
                        })
                        .filter(|r| r.confidence >= min_confidence_filter)
                        .collect();

                    if !rels.is_empty() {
                        affected_symbols.push(json!({
                            "qualified_name": elem.qualified_name,
                            "name": elem.name,
                            "type": elem.element_type,
                            "file": elem.file_path,
                            "confidence": rels.first().map(|r| r.confidence).unwrap_or(1.0)
                        }));
                    }
                }
            }
        }

        let risk_level = if max_dependents_at_depth1 >= 10
            || (has_public_api_change && max_dependents_at_depth1 >= 5)
        {
            "critical"
        } else if max_dependents_at_depth1 >= 5 || has_public_api_change {
            "high"
        } else if max_dependents_at_depth1 >= 2 || affected_symbols.len() > 5 {
            "medium"
        } else {
            "low"
        };

        Ok(json!({
            "summary": {
                "changed_files": changed_files.len(),
                "changed_symbols": changed_symbols.len(),
                "affected_symbols": affected_symbols.len(),
                "risk_level": risk_level
            },
            "changed_files": changed_files,
            "changed_symbols": changed_symbols,
            "affected_symbols": affected_symbols,
            "risk_reasons": risk_reasons
        }))
    }

    fn query_file(&self, args: &Value) -> Result<Value, String> {
        let pattern = args["pattern"]
            .as_str()
            .ok_or("Missing 'pattern' parameter")?;

        let element_type_filter = args["element_type"].as_str().map(String::from);

        // Convert glob pattern to regex for matching
        // *.rs -> matches files ending with .rs
        // handler.rs -> matches files containing handler.rs
        let regex_pattern = if pattern.contains('*') {
            let mut regex_str = pattern.replace('.', r"\.").replace("*", ".*");
            // Anchor at end for file paths
            if !regex_str.ends_with(".*") {
                regex_str.push('$');
            }
            regex_str
        } else {
            // For non-glob patterns, match if path contains the pattern
            format!(".*{}.*$", regex::escape(pattern))
        };

        let re = regex::Regex::new(&regex_pattern).map_err(|e| e.to_string())?;

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .filter(|e| {
                let pattern_match = re.is_match(&e.file_path) || e.qualified_name.contains(pattern);
                let type_match = element_type_filter
                    .as_ref()
                    .map(|et| e.element_type.to_lowercase() == et.to_lowercase())
                    .unwrap_or(true);
                pattern_match && type_match
            })
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

        let elements = self
            .graph_engine
            .get_dependencies(file)
            .map_err(|e| e.to_string())?;

        let deps: Vec<_> = elements
            .iter()
            .map(|e| {
                json!({
                    "target": e.qualified_name,
                    "type": "imports"
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
        let min_confidence = args["min_confidence"].as_f64().unwrap_or(0.0);

        let analyzer = ImpactAnalyzer::new(&self.graph_engine);
        let result = analyzer
            .calculate_impact_radius_with_confidence(file, depth, min_confidence)
            .map_err(|e| e.to_string())?;

        let response = json!({
            "start_file": result.start_file,
            "max_depth": result.max_depth,
            "affected": result.affected_elements.len(),
            "elements": result.affected_elements.iter().map(|e| json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path
            })).collect::<Vec<_>>(),
            "elements_with_confidence": result.affected_with_confidence.iter().map(|a| json!({
                "qualified_name": a.element.qualified_name,
                "name": a.element.name,
                "type": a.element.element_type,
                "file": a.element.file_path,
                "confidence": a.confidence,
                "severity": a.severity,
                "depth": a.depth
            })).collect::<Vec<_>>()
        });

        Ok(self.maybe_compress(response, args, "get_impact_radius"))
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
        let signature_only = args["signature_only"].as_bool().unwrap_or(true);
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

                if signature_only {
                    let signature = elem
                        .metadata
                        .get("signature")
                        .and_then(|v| v.as_str())
                        .unwrap_or("")
                        .to_string();
                    json!({
                        "qualified_name": elem.qualified_name,
                        "name": elem.name,
                        "type": elem.element_type,
                        "file": elem.file_path,
                        "line": elem.line_start,
                        "signature": signature,
                        "priority": priority_str,
                        "token_count": ctx_elem.token_count,
                        "cluster_id": elem.cluster_id,
                        "cluster_label": elem.cluster_label
                    })
                } else {
                    json!({
                        "qualified_name": elem.qualified_name,
                        "name": elem.name,
                        "type": elem.element_type,
                        "file": elem.file_path,
                        "line_start": elem.line_start,
                        "line_end": elem.line_end,
                        "priority": priority_str,
                        "token_count": ctx_elem.token_count,
                        "cluster_id": elem.cluster_id,
                        "cluster_label": elem.cluster_label
                    })
                }
            })
            .collect();

        let file_element = self
            .graph_engine
            .find_element(file)
            .map_err(|e| e.to_string())?;
        let cluster_info = file_element.as_ref().map(|elem| {
            json!({
                "id": elem.cluster_id,
                "label": elem.cluster_label
            })
        });

        let dependents_count = file_element
            .as_ref()
            .map(|elem| {
                self.graph_engine
                    .get_dependents(elem.qualified_name.as_str())
                    .map(|d| d.len())
                    .unwrap_or(0)
            })
            .unwrap_or(0);

        let dependencies_count = file_element
            .as_ref()
            .map(|elem| {
                self.graph_engine
                    .get_dependencies(elem.qualified_name.as_str())
                    .map(|d| d.len())
                    .unwrap_or(0)
            })
            .unwrap_or(0);

        Ok(json!({
            "file": file,
            "cluster": cluster_info,
            "dependents_count": dependents_count,
            "dependencies_count": dependencies_count,
            "elements": elements_json,
            "total_tokens": result.total_tokens,
            "max_tokens": result.max_tokens,
            "truncated": result.truncated,
            "signature_only": signature_only,
            "prompt": result.to_prompt()
        }))
    }

    fn find_function(&self, args: &Value) -> Result<Value, String> {
        let name = args["name"].as_str().ok_or("Missing 'name' parameter")?;

        let elements = self
            .graph_engine
            .search_by_name_typed(name, Some("function"), 50)
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .filter(|e| e.name.contains(name))
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

    fn get_callers(&self, args: &Value) -> Result<Value, String> {
        let function = args["function"]
            .as_str()
            .ok_or("Missing 'function' parameter")?;
        let file_scope = args["file"].as_str();

        let callers = self
            .graph_engine
            .get_callers(function, file_scope)
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = callers
            .iter()
            .map(|e| {
                json!({
                    "name": e.name,
                    "qualified_name": e.qualified_name,
                    "file": e.file_path,
                    "line_start": e.line_start,
                    "line_end": e.line_end,
                })
            })
            .collect();

        Ok(json!({ "callers": matches }))
    }

    fn get_call_graph(&self, args: &Value) -> Result<Value, String> {
        let function = args["function"]
            .as_str()
            .ok_or("Missing 'function' parameter")?;
        let depth = args["depth"].as_u64().unwrap_or(2) as u32;
        let max_results = args["max_results"].as_u64().unwrap_or(30) as usize;

        let call_graph = self
            .graph_engine
            .get_call_graph_bounded(function, depth, max_results)
            .map_err(|e| e.to_string())?;

        let calls: Vec<_> = call_graph
            .iter()
            .map(|(src, tgt, d)| {
                json!({
                    "source": src,
                    "target": tgt,
                    "depth": d
                })
            })
            .collect();

        Ok(json!({ "calls": calls }))
    }

    fn search_code(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;
        let limit = args["limit"].as_i64().unwrap_or(20).min(50) as usize;
        let element_type = args["element_type"].as_str();

        let elements = self
            .graph_engine
            .search_by_name_typed(query, element_type, limit)
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "type": e.element_type,
                    "file": e.file_path,
                    "line": e.line_start,
                    "cluster_id": e.cluster_id,
                    "cluster_label": e.cluster_label
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
        let limit = args["limit"].as_i64().unwrap_or(20).clamp(1, 100) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).clamp(0, i64::MAX) as usize;

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let mut large_functions: Vec<_> = elements
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

        // Sort by lines descending (largest first)
        large_functions.sort_by(|a, b| {
            let a_lines = a["lines"].as_u64().unwrap_or(0);
            let b_lines = b["lines"].as_u64().unwrap_or(0);
            b_lines.cmp(&a_lines)
        });

        let total = large_functions.len();
        let paginated: Vec<_> = large_functions
            .into_iter()
            .skip(offset)
            .take(limit)
            .collect();

        Ok(json!({
            "large_functions": paginated,
            "pagination": {
                "total": total,
                "limit": limit,
                "offset": offset,
                "has_more": offset + limit < total
            }
        }))
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
                    "doc": r.target_qualified,
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

    fn get_doc_tree(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_i64().unwrap_or(50).clamp(1, 200) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).clamp(0, i64::MAX) as usize;
        let max_docs = args["max_docs"].as_i64().unwrap_or(20).clamp(1, 100) as usize;

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let mut tree: std::collections::BTreeMap<String, serde_json::Map<String, Value>> =
            std::collections::BTreeMap::new();
        let mut doc_counts: std::collections::HashMap<String, usize> =
            std::collections::HashMap::new();

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

            let count = doc_counts.entry(category.to_string()).or_insert(0);
            if *count >= max_docs {
                *count += 1;
                continue;
            }
            *count += 1;

            let node = json!({
                "qualified_name": elem.qualified_name,
                "name": elem.name,
                "type": elem.element_type,
                "line_start": elem.line_start,
                "line_end": elem.line_end
            });

            tree.entry(category.to_string())
                .or_default()
                .insert(elem.name.clone(), node);
        }

        // Sort and paginate categories
        let total_categories = tree.len();
        let keys: Vec<_> = tree.keys().cloned().collect();

        let paginated_tree: serde_json::Map<String, serde_json::Value> = keys
            .into_iter()
            .skip(offset)
            .take(limit)
            .filter_map(|k| {
                let docs = tree.remove(&k)?;
                let mut obj = serde_json::Map::new();
                let truncated = doc_counts.get(&k).copied().unwrap_or(0) > max_docs;
                obj.insert("docs".to_string(), json!(docs));
                obj.insert("truncated".to_string(), json!(truncated));
                obj.insert(
                    "total_docs".to_string(),
                    json!(doc_counts.get(&k).copied().unwrap_or(0)),
                );
                Some((k, json!(obj)))
            })
            .collect();

        Ok(json!({
            "tree": paginated_tree,
            "pagination": {
                "total_categories": total_categories,
                "limit": limit,
                "offset": offset,
                "has_more": offset + limit < total_categories
            }
        }))
    }

    fn get_code_tree(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_i64().unwrap_or(50).clamp(1, 200) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).clamp(0, i64::MAX) as usize;
        let max_elements = args["max_elements"].as_i64().unwrap_or(20).clamp(1, 100) as usize;

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let mut tree: std::collections::BTreeMap<String, (String, Vec<Value>)> =
            std::collections::BTreeMap::new();

        for elem in &elements {
            let is_code_element = matches!(
                elem.element_type.as_str(),
                "function" | "struct" | "class" | "module" | "interface" | "enum" | "trait"
            );
            if !is_code_element {
                continue;
            }

            let parts: Vec<&str> = elem.file_path.split('/').collect();
            if parts.is_empty() {
                continue;
            }

            let file_name = parts.last().unwrap_or(&"");

            let entry = tree
                .entry(file_name.to_string())
                .or_insert_with(|| (elem.file_path.clone(), Vec::new()));

            if entry.1.len() < max_elements {
                entry.1.push(json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "line_start": elem.line_start,
                    "line_end": elem.line_end
                }));
            }
        }

        // Sort and paginate
        let total_files = tree.len();
        let keys: Vec<_> = tree.keys().cloned().collect();

        let paginated_tree: serde_json::Map<String, serde_json::Value> = keys
            .into_iter()
            .skip(offset)
            .take(limit)
            .filter_map(|k| {
                tree.remove(&k).map(|(file_path, elements)| {
                    let mut obj = serde_json::Map::new();
                    obj.insert("file_path".to_string(), json!(file_path));
                    obj.insert("elements".to_string(), json!(elements));
                    obj.insert(
                        "truncated".to_string(),
                        json!(elements.len() >= max_elements),
                    );
                    (k, json!(obj))
                })
            })
            .collect();

        Ok(json!({
            "code_tree": paginated_tree,
            "pagination": {
                "total_files": total_files,
                "limit": limit,
                "offset": offset,
                "has_more": offset + limit < total_files
            }
        }))
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
                    "doc": if r.rel_type == "documented_by" { r.target_qualified.clone() } else { r.source_qualified.clone() },
                    "relationship": r.rel_type,
                    "context": r.metadata.get("context").and_then(|v| v.as_str()).unwrap_or("")
                })
            })
            .collect();

        Ok(json!({ "related_docs": related }))
    }

    fn get_clusters(&self, args: &Value) -> Result<Value, String> {
        use crate::graph::clustering::{get_cluster_stats, Cluster, CommunityDetector};

        let limit = args["limit"].as_i64().unwrap_or(50).clamp(1, 100) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).clamp(0, i64::MAX) as usize;
        let max_members = args["max_members"].as_i64().unwrap_or(10).clamp(1, 100) as usize;

        let detector = CommunityDetector::new(self.graph_engine.db());
        let clusters = detector.detect_communities().map_err(|e| e.to_string())?;

        let mut cluster_list: Vec<Cluster> = clusters.values().cloned().collect();
        let total = cluster_list.len();

        // Sort by cluster label for consistent pagination
        cluster_list.sort_by_key(|c| c.label.clone());

        let paginated: Vec<_> = cluster_list
            .into_iter()
            .skip(offset)
            .take(limit)
            .map(|mut c| {
                // Truncate members per cluster to keep response size bounded
                if c.members.len() > max_members {
                    c.members.truncate(max_members);
                }
                c
            })
            .collect();
        let stats = get_cluster_stats(&clusters);

        Ok(json!({
            "clusters": paginated,
            "stats": {
                "total_clusters": stats.total_clusters,
                "total_members": stats.total_members,
                "avg_cluster_size": stats.avg_cluster_size
            },
            "pagination": {
                "total": total,
                "limit": limit,
                "offset": offset,
                "has_more": offset + limit < total
            }
        }))
    }

    fn run_raw_query(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;

        let params: std::collections::BTreeMap<String, serde_json::Value> = args
            .get("params")
            .and_then(|p| p.as_object())
            .map(|obj| obj.iter().map(|(k, v)| (k.clone(), v.clone())).collect())
            .unwrap_or_default();

        let result = self
            .graph_engine
            .run_raw_query(query, params)
            .map_err(|e| e.to_string())?;

        let value = serde_json::to_value(&result)
            .map_err(|e| format!("Failed to serialize result: {}", e))?;

        Ok(value)
    }

    fn get_service_graph(&self, args: &Value) -> Result<Value, String> {
        let service_name = args["service"]
            .as_str()
            .map(String::from)
            .unwrap_or_else(|| {
                std::env::current_dir()
                    .ok()
                    .and_then(|p| p.file_name().map(|n| n.to_string_lossy().to_string()))
                    .unwrap_or_else(|| "unknown".to_string())
            });

        let sg = self
            .graph_engine
            .get_service_graph(&service_name)
            .map_err(|e| e.to_string())?;

        serde_json::to_value(&sg).map_err(|e| format!("Failed to serialize service graph: {}", e))
    }

    fn get_cluster_context(&self, args: &Value) -> Result<Value, String> {
        use crate::graph::clustering::CommunityDetector;

        let cluster_id = args["cluster_id"].as_str();
        let cluster_label = args["cluster_label"].as_str();

        let detector = CommunityDetector::new(self.graph_engine.db());
        let clusters = detector.detect_communities().map_err(|e| e.to_string())?;

        let target_cluster = if let Some(cid) = cluster_id {
            // Try direct lookup first, then with "cluster_" prefix
            clusters
                .get(cid)
                .or_else(|| clusters.get(&format!("cluster_{}", cid)))
                .cloned()
        } else if let Some(label) = cluster_label {
            clusters.values().find(|c| c.label == label).cloned()
        } else {
            None
        };

        match target_cluster {
            Some(cluster) => {
                let elements = self
                    .graph_engine
                    .all_elements()
                    .map_err(|e| e.to_string())?;
                let relationships = self
                    .graph_engine
                    .all_relationships()
                    .map_err(|e| e.to_string())?;

                let cluster_elements: Vec<_> = elements
                    .iter()
                    .filter(|e| cluster.members.contains(&e.qualified_name))
                    .map(|e| {
                        json!({
                            "qualified_name": e.qualified_name,
                            "element_type": e.element_type,
                            "name": e.name,
                            "file_path": e.file_path
                        })
                    })
                    .collect();

                let member_set: std::collections::HashSet<_> = cluster.members.iter().collect();
                let inter_cluster: Vec<_> = relationships
                    .iter()
                    .filter(|r| {
                        let src_in_cluster = member_set.contains(&r.source_qualified);
                        let tgt_in_cluster = member_set.contains(&r.target_qualified);
                        src_in_cluster != tgt_in_cluster
                    })
                    .map(|r| {
                        json!({
                            "source": r.source_qualified,
                            "target": r.target_qualified,
                            "type": r.rel_type
                        })
                    })
                    .collect();

                let entry_points: Vec<_> = cluster_elements
                    .iter()
                    .filter(|e| {
                        relationships.iter().any(|r| {
                            r.target_qualified == e["qualified_name"]
                                && !member_set.contains(&r.source_qualified)
                        })
                    })
                    .collect();

                Ok(json!({
                    "cluster_id": cluster.id,
                    "cluster_label": cluster.label,
                    "members": cluster_elements,
                    "member_count": cluster.members.len(),
                    "representative_files": cluster.representative_files,
                    "entry_points": entry_points,
                    "inter_cluster_dependencies": inter_cluster
                }))
            }
            None => Err("Cluster not found".to_string()),
        }
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
            ..Default::default()
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
            ..Default::default()
        }];
        let doc = generate_documentation("src/main.rs", &elements);
        assert!(doc.contains("src/main.rs"));
    }
}
