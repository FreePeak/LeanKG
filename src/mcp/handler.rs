use crate::compress::{FileReader, ReadMode, ResponseCompressor};
use crate::db;
use crate::db::models::{CodeElement, ContextMetric, KnowledgeEntry, Relationship};
use crate::db::record_metric;
use crate::graph::{GraphEngine, ImpactAnalyzer};
use crate::mcp::token_budget::TokenBudget;
use crate::orchestrator::QueryOrchestrator;
use glob;
use serde_json::{json, Value};
use std::time::{Instant, SystemTime, UNIX_EPOCH};

const INSTRUCTIONS_CONTENT: &str = r#"# LeanKG MCP Tools - Agent Guide

## Core Principle

LeanKG is a **pre-built knowledge graph** of the codebase. Always query it first — never grep/ripgrep unless the tool returns no results.

---

## Tool Selection Flowchart

```
User asks about codebase → mcp_status (check initialized)
  │
  ├─ "Where is X?" / "Find Y" ───────────────► search_code or find_function
  │   ├─ by name/type ─────────────────────────► search_code(query="X")
  │   └─ exact function ───────────────────────► find_function(name="parseJson")
  │                                              scope to file: find_function(name="foo", file="src/bar.rs")
  │
  ├─ "What breaks if I change X?" ────────────► get_impact_radius(file="X", depth=2)
  │   └─ use depth<=2 for token budgets (depth=3 returns hundreds of nodes)
  │
  ├─ "How does X work?" / call chain ─────────► get_call_graph(function="X")
  │   └─ keep depth≤2, avoid depth>3 (neighbor explosion)
  │
  ├─ "Who calls X?" / callers ────────────────► get_callers(function="X")
  │
  ├─ "What does X import/use?" ───────────────► get_dependencies(file="X")
  ├─ "What uses X?" ──────────────────────────► get_dependents(file="X")
  │
  ├─ "Show me file context" / read large file ─► ctx_read(file="X", mode=adaptive)
  │   └─ modes: adaptive, signatures (smallest), full, map, diff, lines("1-20,30-40")
  │
  ├─ "Get minimal AI context for prompt" ─────► get_context(file="X", signature_only=true)
  │
  ├─ "What tests cover X?" ───────────────────► get_tested_by(file="X")
  │
  ├─ "Show me all files/folders" ─────────────► get_code_tree(limit=50)
  │
  ├─ "Find oversized functions" ──────────────► find_large_functions(min_lines=50, limit=20)
  │
  ├─ Natural language query (any of the above) ─► orchestrate(intent="...")
  │   └─ file param is OPTIONAL — only needed for impact/dependency queries
  │      e.g. orchestrate(intent="show me impact of changing src/lib.rs", file="src/lib.rs")
  │
  ├─ "What docs reference X?" ─────────────────► get_doc_for_file(file="X")
  ├─ "What code is in this doc?" ─────────────► get_files_for_doc(doc="docs/X.md")
  │
  └─ Pre-commit risk check ───────────────────► detect_changes(scope="staged"|"all")
```

---

## Smart Shortcut: `orchestrate`

Use when you want LeanKG to pick the best tool automatically. Only requires `intent`:

| Intent Pattern | What It Does |
|----------------|-------------|
| "show me impact of changing X" | Impact radius analysis |
| "get context for file X" | Token-optimized file context |
| "find function named X" | Function location search |
| "what does module X do?" | Cluster + dependency summary |

**Parameters:** `intent` (required), `file` (optional — only needed when intent references a specific file for impact/dependency queries), `mode` (adaptive/full/map/signatures), `fresh` (bypass cache)

---

## Token Optimization Tips

| Scenario | Tool + Params |
|----------|--------------|
| Read large file (>50 lines) | `ctx_read(file="X", mode=signatures)` — 80-90% token savings |
| Impact analysis | `get_impact_radius(file="X", depth=2, compress_response=true)` |
| Call graph | `get_call_graph(function="X", max_results=30)` |
| File context for prompt | `get_context(file="X", signature_only=true, max_tokens=4000)` |

---

## Anti-Patterns (Don't Do These)

- **grep before LeanKG** — The graph is pre-built and faster
- **depth>2 on get_impact_radius** — Returns hundreds of nodes, wastes tokens
- **depth>3 on get_call_graph** — Neighbor explosion
- **Reading full files with ctx_read mode=full** — Use signatures or adaptive for large files
- **Calling orchestrate without intent** — intent is the only required param

---

## Path Formats (All Equivalent)

```
src/main.rs      ./src/main.rs      src/lib.rs::parse_config
```

Works across all tools. No need to worry about `./` prefix or absolute paths.

---

## Multi-Project Setup (HTTP/SSE Server)

LeanKG supports multiple projects through a single Docker-based HTTP server.

### How Routing Works

The server identifies which project database to use via the `?project=` URL query parameter:

| URL | Project |
|-----|---------|
| `http://host:9699/mcp` | Default project (where server started) |
| `http://host:9699/mcp?project=/workspace-foo` | Side-by-side project mounted at `/workspace-foo` |
| `http://host:9699/mcp?project=/workspace-new` | Custom project |

The side-by-side project path is whatever the user configured in their
local `.dockerfile` (see `.dockerfile.example`); the canonical example
used in this repo's docker-compose is `/workspace`.

### Registering a New Project Directory

**Option A: Docker volume mount**
1. Add volume mount to `docker-compose.rocksdb.yml`:
   ```yaml
   volumes:
     - /host/path/to/project:/workspace-new
   ```
2. Restart: `docker compose restart`
3. Auto-discovery entrypoint detects the new `.leankg` directory and indexes it.

**Option B: Via MCP tools (from AI agent)**
1. Call `mcp_init(path="/workspace-new")` to create `.leankg/leankg.yaml`
2. Call `mcp_index(path="/workspace-new")` to index all files
3. All subsequent queries use `?project=/workspace-new` for that project

**Option C: Via CLI (Docker exec)**
```bash
docker exec leankg-leankg-1 leankg index /workspace-new
```

### Adding MCP Config for a New Project Tool

Each AI tool (opencode, Claude, Cursor) needs the `?project=` param in its MCP URL:

```json
// .mcp.json or equivalent config
{
  "mcpServers": {
    "leankg": {
      "url": "http://localhost:9699/mcp?project=/workspace-new"
    }
  }
}
```

Without the param, the server defaults to the project it was started in (`/workspace`).
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
            "search_annotations" => compressor.compress_search_annotations(&response),
            "get_nav_graph" => compressor.compress_nav_graph(&response),
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
            "explain_node" => self.explain_node(arguments),
            "get_god_nodes" => self.get_god_nodes(arguments),
            "load_layer" => self.load_layer(arguments),
            "temporal_query" => self.temporal_query(arguments),
            "check_consistency" => self.check_consistency(arguments),
            "timeline" => self.timeline(arguments),
            "find_tunnels" => self.find_tunnels(arguments),
            "get_graph_report" => self.get_graph_report(arguments),
            "shortest_path" => self.shortest_path(arguments),
            "get_callers" => self.get_callers(arguments),
            "get_call_graph" => self.get_call_graph(arguments),
            "search_code" => self.search_code(arguments),
            "concept_search" => self.concept_search(arguments),
            "search_annotations" => self.search_annotations(arguments),
            "semantic_search" => self.semantic_search(arguments),
            "wake_up" => self.wake_up(arguments),
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
            "get_nav_graph" => self.get_nav_graph(arguments),
            "find_route" => self.find_route(arguments),
            "get_screen_args" => self.get_screen_args(arguments),
            "get_nav_callers" => self.get_nav_callers(arguments),
            // Knowledge contribution tools
            "add_knowledge" => self.add_knowledge(arguments),
            "update_knowledge" => self.update_knowledge(arguments),
            "delete_knowledge" => self.delete_knowledge(arguments),
            "search_knowledge" => self.search_knowledge_tool(arguments),
            "add_annotation" => self.add_annotation(arguments),
            "link_element" => self.link_element_tool(arguments),
            "add_documentation" => self.add_documentation(arguments),
            // Versioning tools
            "search_by_environment" => self.search_by_environment(arguments),
            "get_upcoming_changes" => self.get_upcoming_changes(arguments),
            "promote_environment" => self.promote_environment(arguments),
            // Incident and environment tools
            "query_incidents" => self.query_incidents(arguments),
            "find_env_conflicts" => self.find_env_conflicts(arguments),
            "get_service_context" => self.get_service_context(arguments),
            // Ontology semantic search tools
            "kg_context" => self.kg_context(arguments),
            "kg_concept_map" => self.kg_concept_map(arguments),
            "kg_trace_workflow" => self.kg_trace_workflow(arguments),
            "kg_ontology_status" => self.kg_ontology_status(arguments),
            "kg_self_test" => self.kg_self_test(arguments),
            "get_architecture" => self.get_architecture(arguments),
            "get_graph_schema" => self.get_graph_schema(arguments),
            "find_dead_code" => self.find_dead_code(arguments),
            #[cfg(feature = "embeddings")]
            "kg_semantic_context" => self.kg_semantic_context(arguments),
            _ => Err(format!("Unknown tool: {}", tool_name)),
        };

        // Apply token budget enforcement
        let result = result.map(|response| TokenBudget::apply(response, tool_name));

        let execution_time_ms = start_time.elapsed().as_millis() as i32;
        let input_tokens = arguments.to_string().len() as i32 / 4;

        let (output_tokens, output_elements, success) = match &result {
            Ok(response) => {
                let response_str = response.to_string();
                let output_tok = response_str.len() as i32 / 4;
                let out_elem = Self::count_response_elements(response);
                (output_tok, out_elem, true)
            }
            Err(_) => (0, 0, false),
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
            baseline_tokens: 0,
            baseline_lines_scanned: 0,
            tokens_saved: 0,
            savings_percent: 0.0,
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
        // Use project argument as default path (injected by HTTP server via ?project= URL param)
        let path = args["path"]
            .as_str()
            .or_else(|| args["project"].as_str())
            .unwrap_or(".leankg");
        let path_ref = std::path::Path::new(path);

        if !path_ref.exists() || path_ref.is_dir() {
            std::fs::create_dir_all(path_ref)
                .map_err(|e| format!("Failed to create directory: {}", e))?;
        }

        let config = crate::config::ProjectConfig::default();
        let config_yaml = serde_yaml::to_string(&config)
            .map_err(|e| format!("Failed to serialize config: {}", e))?;
        let config_path = if path_ref.is_file() {
            std::path::PathBuf::from("leankg.yaml")
        } else {
            path_ref.join("leankg.yaml")
        };
        std::fs::write(config_path, config_yaml)
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
        let incremental = args["incremental"].as_bool().unwrap_or(false);
        let resolve_calls = args["resolve_calls"].as_bool().unwrap_or(false);
        let lang = args["lang"].as_str();
        let exclude = args["exclude"].as_str();
        let env = args["env"].as_str().unwrap_or("local");
        let _service_name = args["service_name"].as_str();
        let _version = args["version"].as_str();

        let _ = env;

        let db_path = self.db_path.clone();
        if !db_path.exists() {
            if let Some(parent) = db_path.parent() {
                if !parent.as_os_str().is_empty() {
                    tokio::fs::create_dir_all(parent)
                        .await
                        .map_err(|e| format!("Failed to create .leankg parent: {}", e))?;
                }
            }
        } else if db_path.is_dir() {
            tokio::fs::create_dir_all(&db_path)
                .await
                .map_err(|e| format!("Failed to create .leankg: {}", e))?;
        }

        let exclude_patterns: Vec<String> = exclude
            .map(|e| e.split(',').map(|s| s.trim().to_string()).collect())
            .unwrap_or_default();

        let mut parser_manager = crate::indexer::ParserManager::new();
        parser_manager
            .init_parsers()
            .map_err(|e| format!("Parser init error: {}", e))?;

        if incremental {
            let result = crate::indexer::incremental_index_sync(
                &self.graph_engine,
                &mut parser_manager,
                path,
            )
            .await
            .map_err(|e| format!("Incremental index error: {}", e))?;
            let resolved = if resolve_calls {
                self.graph_engine.resolve_call_edges().unwrap_or(0)
            } else {
                0
            };

            return Ok(json!({
                "success": true,
                "message": if resolve_calls {
                    format!(
                        "Incrementally indexed {} files ({} elements), {} dependent files, {} call edges resolved",
                        result.total_files_processed,
                        result.elements_indexed,
                        result.dependent_files.len(),
                        resolved
                    )
                } else {
                    format!(
                        "Incrementally indexed {} files ({} elements), {} dependent files; call edge resolution skipped",
                        result.total_files_processed,
                        result.elements_indexed,
                        result.dependent_files.len()
                    )
                },
                "incremental": true,
                "changed_files": result.changed_files,
                "dependent_files": result.dependent_files,
                "indexed": result.total_files_processed,
                "elements_indexed": result.elements_indexed,
                "resolved": resolved,
                "resolve_calls": resolve_calls,
                "path": path
            }));
        }

        let files = crate::indexer::find_files_sync(path)
            .map_err(|e| format!("Find files error: {}", e))?;

        let mut indexed = 0;
        let mut skipped_files: Vec<serde_json::Value> = Vec::new();

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
                Err(e) => {
                    skipped_files.push(serde_json::json!({
                        "file": file_path,
                        "reason": e.to_string()
                    }));
                }
            }
        }

        let resolved = if resolve_calls {
            self.graph_engine.resolve_call_edges().unwrap_or(0)
        } else {
            0
        };

        let skipped_count = skipped_files.len();
        Ok(json!({
            "success": true,
            "message": if resolve_calls {
                format!("Indexed {} files, {} skipped, {} call edges resolved", indexed, skipped_count, resolved)
            } else {
                format!("Indexed {} files, {} skipped; call edge resolution skipped", indexed, skipped_count)
            },
            "incremental": false,
            "indexed": indexed,
            "skipped_count": skipped_count,
            "skipped_files": skipped_files,
            "resolved": resolved,
            "resolve_calls": resolve_calls,
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

    fn mcp_status(&self, args: &Value) -> Result<Value, String> {
        let db_path = &self.db_path;
        let include_counts = args["include_counts"].as_bool().unwrap_or(false);
        let storage = db::schema::resolve_storage_config(db_path);
        let storage_engine = match storage.engine {
            db::schema::StorageEngine::Sqlite => "sqlite",
            db::schema::StorageEngine::RocksDb => "rocksdb",
        };

        if !db_path.exists() {
            return Ok(json!({
                "initialized": false,
                "storage_engine": storage_engine,
                "storage_path": storage.path.to_string_lossy(),
                "message": "LeanKG not initialized. Run mcp_init first."
            }));
        }

        // Verify database is actually initialized with proper tables
        let has_elements = self.graph_engine.has_elements().unwrap_or(false);
        if !has_elements {
            return Ok(json!({
                "initialized": false,
                "message": "LeanKG directory exists but database not initialized. Run mcp_index to populate index.",
                "database_exists": false,
                "storage_engine": storage_engine,
                "storage_path": storage.path.to_string_lossy(),
                "counts_included": false
            }));
        }

        if !include_counts {
            return Ok(json!({
                "initialized": true,
                "index_populated": true,
                "database_exists": true,
                "database": db_path.to_string_lossy(),
                "storage_engine": storage_engine,
                "storage_path": storage.path.to_string_lossy(),
                "counts_included": false,
                "message": "Database exists and contains indexed elements. Pass include_counts=true for full counts."
            }));
        }

        Ok(json!({
            "initialized": true,
            "index_populated": true,
            "database_exists": true,
            "database": db_path.to_string_lossy(),
            "storage_engine": storage_engine,
            "storage_path": storage.path.to_string_lossy(),
            "counts_included": true,
            "elements": self.graph_engine.count_elements().unwrap_or(0),
            "relationships": self.graph_engine.count_relationships().unwrap_or(0),
            "files": self.graph_engine.count_files().unwrap_or(0),
            "functions": self.graph_engine.count_by_element_type("function").unwrap_or(0),
            "classes": self.graph_engine.count_by_element_type("class").unwrap_or(0)
                + self.graph_engine.count_by_element_type("struct").unwrap_or(0),
            "annotations": self.graph_engine.count_business_logic().unwrap_or(0)
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

        for file in &changed_files {
            let file_elements = self
                .graph_engine
                .get_elements_by_file(file)
                .map_err(|e| e.to_string())?;

            for elem in &file_elements {
                changed_symbols.push(json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path
                }));

                let deps = self
                    .graph_engine
                    .get_relationships_for_elements_fast(
                        std::slice::from_ref(&elem.qualified_name),
                        Some(&["calls"]),
                    )
                    .map_err(|e| e.to_string())?;

                let depth1_count = deps.len();
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

        let all_deps = self
            .graph_engine
            .get_relationships_for_elements_fast(
                &changed_files.to_vec(),
                Some(&["imports", "calls", "references"]),
            )
            .map_err(|e| e.to_string())?;

        let mut seen_affected = std::collections::HashSet::new();
        for rel in &all_deps {
            if let Ok(Some(elem)) = self.graph_engine.find_element(&rel.target_qualified) {
                if rel.confidence >= min_confidence_filter
                    && seen_affected.insert(elem.qualified_name.clone())
                {
                    affected_symbols.push(json!({
                        "qualified_name": elem.qualified_name,
                        "name": elem.name,
                        "type": elem.element_type,
                        "file": elem.file_path,
                        "confidence": rel.confidence
                    }));
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

    /// Glob matching using the glob crate (already a dependency).
    /// Supports ** (any path), * (any chars), ? (single char), [abc] (char class).
    fn glob_match(&self, pattern: &str, text: &str) -> bool {
        if let Ok(g) = glob::Pattern::new(pattern) {
            g.matches(text)
        } else {
            // Fall back to substring match for invalid patterns
            text.contains(pattern)
        }
    }

    /// Pre-process a user query string into valid Cozo Datalog syntax.
    /// Handles common patterns like:
    ///   "function[file ~ 'chat']"  →  full Cozo query
    ///   "?[name] := *code_elements"  →  pass through
    fn preprocess_datalog_query(query: &str) -> String {
        let trimmed = query.trim();

        // Already a valid Cozo query (starts with ? or :)
        if trimmed.starts_with('?') || trimmed.starts_with(':') {
            return trimmed.to_string();
        }

        // Pattern: relation[field ~ 'value'] or relation[field = 'value']
        // e.g., "function[file ~ 'chat']" or "code_elements[name = 'foo']"
        if let Some(cap) =
            regex::Regex::new(r"^(\w+)\[(\w+)\s*(~|=)\s*'([^']+)'\](?::limit\s+(\d+))?")
                .ok()
                .and_then(|r| r.captures(trimmed))
        {
            let _relation = cap.get(1).map(|m| m.as_str()).unwrap_or("code_elements");
            let field_raw = cap.get(2).map(|m| m.as_str()).unwrap_or("file_path");
            let _op = cap.get(3).map(|m| m.as_str()).unwrap_or("~");
            let value = cap.get(4).map(|m| m.as_str()).unwrap_or("");
            let limit = cap.get(5).map(|m| m.as_str()).unwrap_or("50");

            // Map short field names to actual column names
            let field = match field_raw {
                "file" | "path" => "file_path",
                "qualified_name" | "qname" => "qualified_name",
                "name" | "n" => "name",
                "type" | "element_type" => "element_type",
                "language" | "lang" => "language",
                "parent" | "parent_qualified" => "parent_qualified",
                "start" | "line_start" => "line_start",
                "end" | "line_end" => "line_end",
                "cluster" | "cluster_id" => "cluster_id",
                "label" | "cluster_label" => "cluster_label",
                "metadata" | "meta" => "metadata",
                other => other,
            };

            // NOTE: Cozo requires all columns to be bound in the head when using
            // a materialized relation (*relation[...]). The full schema is:
            // qualified_name, element_type, name, file_path, line_start, line_end,
            // language, parent_qualified, cluster_id, cluster_label, metadata
            // Use regex_matches() for regex filtering in Cozo
            return format!(
                "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] \
                 := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, _], \
                 regex_matches({}, \"{}\") :limit {}",
                field, value, limit
            );
        }

        // Pattern: simple search "search term" → scan all elements
        if !trimmed.contains('[') && !trimmed.contains('?') && !trimmed.contains(':') {
            return format!(
                "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] \
                 := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, _], \
                 regex_matches(name, \"{}\") :limit 50",
                trimmed.replace('\\', "\\\\").replace('"', "\\\"")
            );
        }

        // Fall through - pass as-is and let Cozo report the error
        trimmed.to_string()
    }

    fn query_file(&self, args: &Value) -> Result<Value, String> {
        let pattern = args["pattern"]
            .as_str()
            .ok_or("Missing 'pattern' parameter")?;

        let element_type_filter = args["element_type"].as_str().map(String::from);
        let limit = crate::ontology::safe_discover::clamp_limit(
            args["limit"].as_i64().unwrap_or(50) as usize,
        );
        let offset = args["offset"].as_i64().unwrap_or(0).max(0) as usize;

        // Mega-graph: ontology/semantic discover instead of full table scan.
        if crate::ontology::safe_discover::is_mega_graph(&self.graph_engine) {
            let page = crate::ontology::safe_discover::discover(
                &self.graph_engine,
                pattern,
                args["env"].as_str().unwrap_or("local"),
                limit,
                offset,
                true,
            )
            .map_err(|e| e.to_string())?;
            let mut matches: Vec<Value> = page
                .results
                .iter()
                .filter(|e| {
                    let pattern_match = if pattern.contains('*') || pattern.contains('?') {
                        self.glob_match(pattern, &e.file_path)
                            || self.glob_match(pattern, &e.qualified_name)
                    } else {
                        e.file_path.contains(pattern) || e.qualified_name.contains(pattern)
                    };
                    let type_match = element_type_filter
                        .as_ref()
                        .map(|et| &e.element_type == et)
                        .unwrap_or(true);
                    pattern_match && type_match
                })
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
            // If ontology path filtered everything, fall back to typed name search page.
            if matches.is_empty() {
                let probe = pattern.trim_matches(|c| c == '*' || c == '?' || c == '/');
                let found = self
                    .graph_engine
                    .search_by_name_typed(probe, element_type_filter.as_deref(), limit + offset)
                    .map_err(|e| e.to_string())?;
                matches = found
                    .into_iter()
                    .skip(offset)
                    .take(limit)
                    .filter(|e| {
                        e.file_path.contains(probe)
                            || e.qualified_name.contains(probe)
                            || probe.is_empty()
                    })
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
            }
            return Ok(json!({
                "results": matches,
                "count": matches.len(),
                "limit": limit,
                "offset": offset,
                "method": "ontology_or_name_paginated"
            }));
        }

        let elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let matches: Vec<_> = elements
            .iter()
            .filter(|e| {
                let pattern_match = if pattern.contains('*') || pattern.contains('?') {
                    self.glob_match(pattern, &e.file_path)
                        || self.glob_match(pattern, &e.qualified_name)
                } else {
                    e.file_path.contains(pattern) || e.qualified_name.contains(pattern)
                };
                let type_match = element_type_filter
                    .as_ref()
                    .map(|et| &e.element_type == et)
                    .unwrap_or(true);
                pattern_match && type_match
            })
            .skip(offset)
            .take(limit)
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

        Ok(json!({
            "results": matches,
            "count": matches.len(),
            "limit": limit,
            "offset": offset,
            "method": "scan"
        }))
    }

    fn get_dependencies(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        let deps = self
            .graph_engine
            .get_dependencies(file)
            .map_err(|e| e.to_string())?;

        let dependencies: Vec<_> = deps
            .iter()
            .map(|d| {
                json!({
                    "target": d.target_qualified,
                    "confidence": d.confidence,
                    "type": "imports"
                })
            })
            .collect();

        Ok(json!({ "dependencies": dependencies }))
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
                if let Ok(elements) = self.graph_engine.get_elements_by_file(file_path) {
                    let file_elements: Vec<_> = elements
                        .into_iter()
                        .filter(|e| {
                            !e.file_path.contains("/.claude/worktrees/")
                                && !e.file_path.contains("/.worktrees/")
                        })
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

    fn shortest_path(&self, args: &Value) -> Result<Value, String> {
        let source = args["source"]
            .as_str()
            .ok_or("Missing 'source' parameter")?;
        let target = args["target"]
            .as_str()
            .ok_or("Missing 'target' parameter")?;
        let max_hops = args["max_hops"].as_u64().unwrap_or(6) as usize;

        let result = self
            .graph_engine
            .shortest_path(source, target, max_hops)
            .map_err(|e| e.to_string())?;
        Ok(json!({
            "found": result.is_some(),
            "result": result,
        }))
    }

    fn explain_node(&self, args: &Value) -> Result<Value, String> {
        let name = args["name"].as_str().ok_or("Missing 'name' parameter")?;
        let explanation = self
            .graph_engine
            .explain_node(name)
            .map_err(|e| e.to_string())?;
        Ok(json!({
            "found": explanation.is_some(),
            "explanation": explanation,
        }))
    }

    fn get_god_nodes(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_u64().unwrap_or(20) as usize;
        let exclude = args["exclude_hubs_percentile"].as_u64().map(|v| v as u8);
        let nodes = self
            .graph_engine
            .get_god_nodes(limit, exclude)
            .map_err(|e| e.to_string())?;
        Ok(json!({
            "count": nodes.len(),
            "nodes": nodes,
        }))
    }

    fn get_graph_report(&self, args: &Value) -> Result<Value, String> {
        let project_name = args["project_name"].as_str().unwrap_or("project");
        let format = args["format"].as_str().unwrap_or("markdown");
        let report = self
            .graph_engine
            .generate_graph_report(project_name)
            .map_err(|e| e.to_string())?;
        if format == "json" {
            return Ok(json!({ "report": report }));
        }
        let markdown = report.to_markdown();
        Ok(json!({
            "report": report,
            "markdown": markdown,
        }))
    }

    fn temporal_query(&self, args: &Value) -> Result<Value, String> {
        let at = args["at"].as_i64().ok_or("Missing 'at' (epoch seconds)")?;
        let rels = self
            .graph_engine
            .temporal_query(at)
            .map_err(|e| e.to_string())?;
        Ok(json!({
            "at": at,
            "count": rels.len(),
            "relationships": rels,
        }))
    }

    fn timeline(&self, args: &Value) -> Result<Value, String> {
        let qn = args["qualified_name"]
            .as_str()
            .ok_or("Missing 'qualified_name'")?;
        let events = self.graph_engine.timeline(qn).map_err(|e| e.to_string())?;
        Ok(json!({
            "qualified_name": qn,
            "events": events,
        }))
    }

    fn check_consistency(&self, _args: &Value) -> Result<Value, String> {
        let report = self
            .graph_engine
            .check_consistency()
            .map_err(|e| e.to_string())?;
        Ok(json!({
            "total_relationships": report.total_relationships,
            "broken": report.broken,
            "stale": report.stale,
            "findings": report.findings,
        }))
    }

    fn find_tunnels(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_u64().unwrap_or(50) as usize;
        let mut tunnels = self
            .graph_engine
            .find_tunnels()
            .map_err(|e| e.to_string())?;
        tunnels.truncate(limit);
        Ok(json!({
            "count": tunnels.len(),
            "tunnels": tunnels,
        }))
    }

    fn load_layer(&self, args: &Value) -> Result<Value, String> {
        let layer = args["layer"].as_str().unwrap_or("L0");
        let project_name = args["project_name"].as_str().unwrap_or("project");
        match layer {
            "L0" => {
                let text = self
                    .graph_engine
                    .identity_context(project_name)
                    .map_err(|e| e.to_string())?;
                // Persist for next session to skip regeneration.
                let project = args["project"].as_str().unwrap_or(".");
                let path = std::path::Path::new(project)
                    .join(".leankg")
                    .join("identity.md");
                if let Some(parent) = path.parent() {
                    let _ = std::fs::create_dir_all(parent);
                }
                let _ = std::fs::write(&path, &text);
                Ok(json!({ "layer": "L0", "context": text }))
            }
            "L1" => {
                let text = self
                    .graph_engine
                    .critical_facts_context()
                    .map_err(|e| e.to_string())?;
                let project = args["project"].as_str().unwrap_or(".");
                let path = std::path::Path::new(project)
                    .join(".leankg")
                    .join("critical_facts.md");
                if let Some(parent) = path.parent() {
                    let _ = std::fs::create_dir_all(parent);
                }
                let _ = std::fs::write(&path, &text);
                Ok(json!({ "layer": "L1", "context": text }))
            }
            "L2" => {
                let cluster_id = args["cluster_id"]
                    .as_str()
                    .ok_or("L2 requires cluster_id")?;
                let elements = self
                    .graph_engine
                    .all_elements()
                    .map_err(|e| e.to_string())?;
                let members: Vec<_> = elements
                    .into_iter()
                    .filter(|e| e.cluster_id.as_deref() == Some(cluster_id))
                    .take(args["limit"].as_u64().unwrap_or(20) as usize)
                    .map(|e| {
                        json!({
                            "qualified_name": e.qualified_name,
                            "element_type": e.element_type,
                            "name": e.name,
                        })
                    })
                    .collect();
                Ok(json!({ "layer": "L2", "cluster_id": cluster_id, "members": members }))
            }
            "L3" => {
                let query = args["query"].as_str().ok_or("L3 requires query")?;
                let _limit = args["limit"].as_u64().unwrap_or(20) as usize;
                let elements = self
                    .graph_engine
                    .search_by_name(query)
                    .map_err(|e| e.to_string())?;
                let hits: Vec<_> = elements
                    .into_iter()
                    .map(|e| {
                        json!({
                            "qualified_name": e.qualified_name,
                            "element_type": e.element_type,
                            "name": e.name,
                            "file": e.file_path,
                        })
                    })
                    .collect();
                Ok(json!({ "layer": "L3", "query": query, "results": hits }))
            }
            other => Err(format!("Unknown layer: {}", other)),
        }
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
        let limit = args["limit"].as_i64().unwrap_or(20) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).max(0) as usize;
        let element_type = args["element_type"].as_str();
        let env = args["env"].as_str().unwrap_or("local");
        // Mega-graphs default to ontology-first; small graphs keep legacy opt-in.
        let mega = crate::ontology::safe_discover::is_mega_graph(&self.graph_engine);
        let use_ontology = args["use_ontology"].as_bool().unwrap_or(mega);

        if use_ontology || mega {
            let mut page = crate::ontology::safe_discover::discover(
                &self.graph_engine,
                query,
                env,
                limit,
                offset,
                true,
            )
            .map_err(|e| format!("Ontology-first search failed: {}", e))?;

            // Optional element_type filter on the page (already bounded).
            if let Some(et) = element_type {
                page.results.retain(|e| e.element_type == et);
                page.total_estimate = page.results.len();
                page.has_more = false;
            }
            return Ok(crate::ontology::safe_discover::discover_page_to_json(&page));
        }

        let limit = crate::ontology::safe_discover::clamp_limit(limit);
        let elements = self
            .graph_engine
            .search_by_name_typed(query, element_type, limit.saturating_add(offset))
            .map_err(|e| e.to_string())?;
        let total_estimate = elements.len();
        let page: Vec<_> = elements.into_iter().skip(offset).take(limit).collect();
        let matches: Vec<_> = page
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

        Ok(json!({
            "results": matches,
            "count": matches.len(),
            "limit": limit,
            "offset": offset,
            "total_estimate": total_estimate,
            "has_more": offset + matches.len() < total_estimate,
            "method": "name"
        }))
    }

    fn concept_search(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;
        let env = args["env"].as_str().unwrap_or("local");
        let limit = args["limit"].as_i64().unwrap_or(20) as usize;

        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());
        let result = query_engine
            .concept_search(query, env, limit)
            .map_err(|e| format!("Concept search failed: {}", e))?;

        Ok(self.format_concept_search_result(&result))
    }

    /// Shared JSON formatter for `ConceptSearchResult` used by both
    /// `concept_search` and the `use_ontology` path of `search_code`.
    fn format_concept_search_result(&self, result: &crate::ontology::ConceptSearchResult) -> Value {
        let matched_concepts: Vec<Value> = result
            .matched_concepts
            .iter()
            .map(|c| {
                json!({
                    "gid": c.gid,
                    "name": c.name,
                    "element_type": c.element_type,
                    "description": c.description,
                    "aliases": c.aliases,
                    "match_score": c.match_score,
                    "match_reason": c.match_reason,
                    "code_refs": c.code_refs,
                    "docs": c.docs,
                    "owned_by": c.owned_by,
                })
            })
            .collect();

        let linked_code: Vec<Value> = result
            .linked_code
            .iter()
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "type": e.element_type,
                    "file": e.file_path,
                    "line": e.line_start,
                    "language": e.language,
                })
            })
            .collect();

        let fallback_results: Vec<Value> = result
            .fallback_results
            .iter()
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "type": e.element_type,
                    "file": e.file_path,
                    "line": e.line_start,
                })
            })
            .collect();

        json!({
            "query": result.query,
            "extracted_keywords": result.extracted_keywords,
            "workflow": "extract_keywords -> scan_concept_ontology -> load_concept -> query_db",
            "matched_concepts": matched_concepts,
            "concept_match_count": result.concept_match_count,
            "code_ref_count": result.code_ref_count,
            "linked_code": linked_code,
            "linked_code_count": result.linked_code_count,
            "fallback_used": result.fallback_used,
            "fallback_results": fallback_results,
        })
    }

    fn search_annotations(&self, args: &Value) -> Result<Value, String> {
        let annotation_name = args["annotation_name"]
            .as_str()
            .ok_or("Missing 'annotation_name' parameter")?;
        let target_type = args["target_type"].as_str();
        let file_pattern = args["file_pattern"].as_str();
        let limit = args["limit"].as_i64().unwrap_or(20) as usize;

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "search_annotations",
        ) {
            return Ok(refusal);
        }

        let all_elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let all_relationships = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;

        let element_by_qn: std::collections::HashMap<&str, &CodeElement> = all_elements
            .iter()
            .map(|e| (e.qualified_name.as_str(), e))
            .collect();

        let annotates_by_src: std::collections::HashMap<&str, &Relationship> = all_relationships
            .iter()
            .filter(|r| r.rel_type == "annotates")
            .map(|r| (r.source_qualified.as_str(), r))
            .collect();

        let annotations = all_elements
            .iter()
            .filter(|e| e.element_type == "annotation" && e.name == annotation_name)
            .filter(|e| file_pattern.is_none_or(|p| e.file_path.contains(p)));

        let results: Vec<_> = annotations
            .filter_map(|ann| {
                let target_rel = annotates_by_src.get(ann.qualified_name.as_str())?;
                let target_elem = element_by_qn.get(target_rel.target_qualified.as_str())?;

                if let Some(tt) = target_type {
                    let actual_type = ann
                        .metadata
                        .get("target_type")
                        .and_then(|v| v.as_str())
                        .unwrap_or(&target_elem.element_type);
                    if actual_type != tt && tt != "all" {
                        return None;
                    }
                }

                Some(json!({
                    "annotation_name": ann.name,
                    "target_qualified": target_elem.qualified_name,
                    "target_name": target_elem.name,
                    "target_type": ann.metadata.get("target_type")
                        .and_then(|v| v.as_str())
                        .unwrap_or(&target_elem.element_type),
                    "file_path": ann.file_path,
                    "line": ann.line_start,
                    "arguments": ann.metadata.get("arguments").cloned().unwrap_or(json!({}))
                }))
            })
            .take(limit)
            .collect();

        Ok(json!({
            "annotations": results,
            "count": results.len()
        }))
    }

    fn semantic_search(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query'")?;
        let env = args["env"].as_str().unwrap_or("local");
        let limit = args["limit"].as_i64().unwrap_or(20) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0).max(0) as usize;

        // Always ontology-first + paginated — never load env-wide element dumps.
        let page = crate::ontology::safe_discover::discover(
            &self.graph_engine,
            query,
            env,
            limit,
            offset,
            true,
        )
        .map_err(|e| format!("Semantic search failed: {}", e))?;

        let mut body = crate::ontology::safe_discover::discover_page_to_json(&page);
        if let Some(obj) = body.as_object_mut() {
            obj.insert(
                "method".to_string(),
                json!(format!("ontology+semantic({})", page.method)),
            );
        }
        Ok(body)
    }

    fn generate_doc(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str().ok_or("Missing 'file' parameter")?;

        // File-scoped DB query — never all_elements().
        let file_elements = self
            .graph_engine
            .get_elements_by_file(file)
            .map_err(|e| e.to_string())?
            .into_iter()
            .filter(|e| {
                let fp = &e.file_path;
                !fp.contains("/.claude/worktrees/") && !fp.contains("/.worktrees/")
            })
            .collect::<Vec<_>>();

        let doc = generate_documentation(file, &file_elements);

        Ok(json!({ "documentation": doc }))
    }

    fn find_large_functions(&self, args: &Value) -> Result<Value, String> {
        let min_lines = args["min_lines"].as_u64().unwrap_or(50) as u32;
        let limit = crate::ontology::safe_discover::clamp_limit(
            args["limit"].as_i64().unwrap_or(50) as usize,
        );
        let offset = args["offset"].as_i64().unwrap_or(0).max(0) as usize;

        // DB-filtered oversized query — never all_elements(). Cap fetch for memory.
        let fetch_cap = (limit + offset).min(500).max(limit);
        let mut elements = self
            .graph_engine
            .find_oversized_functions(min_lines)
            .map_err(|e| e.to_string())?;
        elements.retain(|e| {
            !e.file_path.contains("/.claude/worktrees/") && !e.file_path.contains("/.worktrees/")
        });
        let total = elements.len();
        let large_functions: Vec<_> = elements
            .into_iter()
            .skip(offset)
            .take(fetch_cap.min(limit))
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "file": e.file_path,
                    "lines": e.line_end.saturating_sub(e.line_start),
                    "line_start": e.line_start,
                    "line_end": e.line_end
                })
            })
            .collect();

        Ok(json!({
            "large_functions": large_functions,
            "total": total,
            "limit": limit,
            "offset": offset,
            "has_more": offset + large_functions.len() < total
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
        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_doc_structure",
        ) {
            return Ok(refusal);
        }

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
        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_doc_tree",
        ) {
            return Ok(refusal);
        }

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

    fn get_code_tree(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_i64().unwrap_or(100) as usize;
        let offset = args["offset"].as_i64().unwrap_or(0) as usize;

        // Use DB-level filtered query instead of all_elements() to avoid
        // loading the entire graph into memory (RC1 fix).
        let cap = std::env::var("LEANKG_CODE_TREE_CAP")
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(50_000);
        let elements = self
            .graph_engine
            .get_code_elements_for_tree(cap)
            .map_err(|e| e.to_string())?;

        let mut files_map: std::collections::BTreeMap<String, (String, Vec<Value>)> =
            std::collections::BTreeMap::new();

        for elem in &elements {
            let parts: Vec<&str> = elem.file_path.split('/').collect();
            if parts.is_empty() {
                continue;
            }

            let file_name = parts.last().unwrap_or(&"");

            let entry = files_map
                .entry(file_name.to_string())
                .or_insert_with(|| (elem.file_path.clone(), Vec::new()));

            entry.1.push(json!({
                "qualified_name": elem.qualified_name,
                "name": elem.name,
                "type": elem.element_type,
                "line_start": elem.line_start,
                "line_end": elem.line_end
            }));
        }

        let total = files_map.len();
        let truncated = elements.len() >= cap;
        let code_tree: Vec<Value> = files_map
            .into_iter()
            .skip(offset)
            .take(limit)
            .map(|(file, (file_path, elems))| {
                json!({
                    "file": file,
                    "file_path": file_path,
                    "elements": elems
                })
            })
            .collect();

        Ok(json!({
            "code_tree": code_tree,
            "total": total,
            "offset": offset,
            "limit": limit,
            "truncated": truncated,
            "cap": cap
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
        use crate::graph::clustering::{Cluster, CommunityDetector};

        let limit = args["limit"].as_i64().unwrap_or(100) as usize;

        // Guard: refuse clustering on huge graphs to avoid OOM.
        // Louvain over 600k nodes pushed RSS to 5.64 GiB / 6 GiB (94%).
        // See root_cause_docker_memory_2026-07-13.md RC1/RC2.
        let max_cluster_elements = std::env::var("LEANKG_MAX_CLUSTER_ELEMENTS")
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(50_000);
        let element_count = self
            .graph_engine
            .count_elements()
            .map_err(|e| e.to_string())?;
        if element_count > max_cluster_elements {
            tracing::warn!(
                target: "leankg::mem",
                elements = element_count,
                max = max_cluster_elements,
                "get_clusters refused: graph too large (set LEANKG_MAX_CLUSTER_ELEMENTS to override)"
            );
            return Ok(json!({
                "clusters": [],
                "stats": { "total_clusters": 0, "total_members": 0, "avg_cluster_size": 0.0 },
                "error": format!(
                    "Clustering refused: graph has {} elements (max {}). Set LEANKG_MAX_CLUSTER_ELEMENTS to override or shard the project.",
                    element_count, max_cluster_elements
                )
            }));
        }

        let detector = CommunityDetector::new(self.graph_engine.db());
        let clusters = match detector.detect_communities() {
            Ok(c) => c,
            Err(e) => {
                tracing::warn!(
                    "detect_communities failed ({}), returning empty clusters",
                    e
                );
                return Ok(json!({
                    "clusters": [],
                    "stats": { "total_clusters": 0, "total_members": 0, "avg_cluster_size": 0.0 }
                }));
            }
        };

        // Filter out noise clusters from build artifacts, worktrees, .next, etc.
        let noise_patterns = ["target/", "build/", ".next/", ".worktrees/", "typenum"];
        let filtered_clusters: Vec<Cluster> = clusters
            .values()
            .filter(|c| {
                !noise_patterns
                    .iter()
                    .any(|p| c.representative_files.iter().any(|f| f.contains(p)))
                    && !c.label.contains("typenum")
            })
            .cloned()
            .collect();

        // Compute stats directly from filtered clusters (get_cluster_stats needs HashMap)
        let total_members: usize = filtered_clusters.iter().map(|c| c.members.len()).sum();
        let total_clusters = filtered_clusters.len();
        let avg_cluster_size = if total_clusters > 0 {
            total_members as f64 / total_clusters as f64
        } else {
            0.0
        };

        Ok(json!({
            "clusters": filtered_clusters.iter().take(limit).cloned().collect::<Vec<_>>(),
            "stats": {
                "total_clusters": total_clusters,
                "total_members": total_members,
                "avg_cluster_size": avg_cluster_size
            }
        }))
    }

    fn run_raw_query(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;

        // Pre-process common query patterns to valid Cozo Datalog
        let processed_query = Self::preprocess_datalog_query(query);

        let params: std::collections::BTreeMap<String, serde_json::Value> = args
            .get("params")
            .and_then(|p| p.as_object())
            .map(|obj| obj.iter().map(|(k, v)| (k.clone(), v.clone())).collect())
            .unwrap_or_default();

        let result = self
            .graph_engine
            .run_raw_query(&processed_query, params)
            .map_err(|e| {
                let msg = e.to_string();
                if msg.contains("does not have field") {
                    format!(
                        "{}. Schema: *code_elements {{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}}. *relationships {{source_qualified, target_qualified, rel_type, confidence, metadata}}",
                        msg
                    )
                } else {
                    msg
                }
            })?;

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

    fn get_nav_graph(&self, args: &Value) -> Result<Value, String> {
        let file = args["file"].as_str();
        let graph_id = args["graph_id"].as_str();

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_nav_graph",
        ) {
            return Ok(refusal);
        }

        let all_elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;

        let nav_elements: Vec<_> = all_elements
            .iter()
            .filter(|e| {
                let is_nav = matches!(
                    e.element_type.as_str(),
                    "nav_graph"
                        | "nav_destination"
                        | "nav_action"
                        | "nav_argument"
                        | "nav_deep_link"
                );
                if let Some(f) = file {
                    is_nav && e.file_path.contains(f)
                } else {
                    is_nav
                }
            })
            .filter(|e| {
                if let Some(gid) = graph_id {
                    e.qualified_name.contains(gid)
                } else {
                    true
                }
            })
            .collect();

        let nav_rels = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?
            .into_iter()
            .filter(|r| {
                matches!(
                    r.rel_type.as_str(),
                    "navigates_to"
                        | "nav_action"
                        | "provides_arg"
                        | "requires_arg"
                        | "deep_link"
                        | "presents"
                )
            })
            .collect::<Vec<_>>();

        Ok(json!({
            "elements": nav_elements,
            "relationships": nav_rels
        }))
    }

    fn find_route(&self, args: &Value) -> Result<Value, String> {
        let route = args["route"].as_str().ok_or("Missing 'route' parameter")?;

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "find_route",
        ) {
            return Ok(refusal);
        }

        let all_elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;
        let all_rels = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;

        let destinations: Vec<_> = all_elements
            .iter()
            .filter(|e| e.element_type == "nav_destination" && e.name.contains(route))
            .collect();

        let actions: Vec<_> = all_rels
            .iter()
            .filter(|r| r.rel_type == "nav_action" && r.target_qualified.contains(route))
            .collect();

        Ok(json!({
            "route": route,
            "destinations": destinations,
            "actions": actions
        }))
    }

    fn get_screen_args(&self, args: &Value) -> Result<Value, String> {
        let destination = args["destination"].as_str().unwrap_or("");
        let limit = args["limit"].as_i64().unwrap_or(20) as usize;

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_screen_args",
        ) {
            return Ok(refusal);
        }

        let all_elements = self
            .graph_engine
            .all_elements()
            .map_err(|e| e.to_string())?;
        let _all_rels = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;

        let dest_elem = all_elements
            .iter()
            .find(|e| e.element_type == "nav_destination" && e.name.contains(destination));

        let args: Vec<_> = if let Some(d) = dest_elem {
            all_elements
                .iter()
                .filter(|e| {
                    e.element_type == "nav_argument"
                        && e.parent_qualified.as_ref() == Some(&d.qualified_name)
                })
                .take(limit)
                .cloned()
                .collect()
        } else {
            Vec::new()
        };

        Ok(json!({
            "destination": destination,
            "arguments": args
        }))
    }

    fn get_nav_callers(&self, args: &Value) -> Result<Value, String> {
        let destination = args["destination"]
            .as_str()
            .ok_or("Missing 'destination' parameter")?;

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_nav_callers",
        ) {
            return Ok(refusal);
        }

        let all_rels = self
            .graph_engine
            .all_relationships()
            .map_err(|e| e.to_string())?;

        let callers: Vec<_> = all_rels
            .iter()
            .filter(|r| {
                r.rel_type == "navigates_to"
                    && (r.target_qualified.contains(destination)
                        || r.target_qualified
                            .contains(&format!("class:{}", destination)))
            })
            .map(|r| r.source_qualified.clone())
            .collect();

        Ok(json!({
            "destination": destination,
            "callers": callers
        }))
    }

    fn get_cluster_context(&self, args: &Value) -> Result<Value, String> {
        use crate::graph::clustering::CommunityDetector;

        let cluster_id = args["cluster_id"].as_str();
        let cluster_label = args["cluster_label"].as_str();

        if let Some(refusal) = crate::ontology::safe_discover::refuse_full_scan_if_mega(
            &self.graph_engine,
            "get_cluster_context",
        ) {
            return Ok(refusal);
        }

        let detector = CommunityDetector::new(self.graph_engine.db());
        let clusters = match detector.detect_communities() {
            Ok(c) => c,
            Err(e) => {
                tracing::warn!(
                    "detect_communities failed in get_cluster_context ({}): {}",
                    cluster_id.map(|s| s.to_string()).unwrap_or_default(),
                    e
                );
                return Err(format!("Failed to load clusters: {}", e));
            }
        };

        let target_cluster = if let Some(cid) = cluster_id {
            clusters.get(cid).cloned()
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
            None => {
                Err("Cluster not found. Try get_clusters to see available cluster IDs.".to_string())
            }
        }
    }

    // ========================================================================
    // Knowledge Contribution Tools
    // ========================================================================

    fn add_knowledge(&self, args: &Value) -> Result<Value, String> {
        let knowledge_type = args["knowledge_type"]
            .as_str()
            .ok_or("Missing knowledge_type")?;
        let title = args["title"].as_str().ok_or("Missing title")?;
        let content = args["content"].as_str().ok_or("Missing content")?;
        let environment = args["environment"].as_str().unwrap_or("production");
        let tags = args["tags"].as_str().unwrap_or("[]");

        let now = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        let id = format!("k-{}-{}", knowledge_type, uuid_simple());

        let entry = KnowledgeEntry {
            id: id.clone(),
            knowledge_type: knowledge_type.to_string(),
            title: title.to_string(),
            content: content.to_string(),
            element_qualified: args["element_qualified"].as_str().map(String::from),
            user_story_id: args["user_story_id"].as_str().map(String::from),
            feature_id: args["feature_id"].as_str().map(String::from),
            tags: tags.to_string(),
            environment: environment.to_string(),
            branch: args["branch"].as_str().map(String::from),
            author: args["author"].as_str().unwrap_or("mcp-client").to_string(),
            created_at: now,
            updated_at: now,
        };

        db::create_knowledge_entry(self.graph_engine.db(), &entry)
            .map_err(|e| format!("Failed to create knowledge entry: {}", e))?;

        Ok(json!({
            "id": entry.id,
            "knowledge_type": entry.knowledge_type,
            "title": entry.title,
            "environment": entry.environment,
            "status": "created"
        }))
    }

    fn update_knowledge(&self, args: &Value) -> Result<Value, String> {
        let id = args["id"].as_str().ok_or("Missing id")?;

        let existing = db::get_knowledge_entry(self.graph_engine.db(), id)
            .map_err(|e| format!("Failed to get knowledge entry: {}", e))?;

        let mut entry = existing.ok_or("Knowledge entry not found")?;

        if let Some(title) = args["title"].as_str() {
            entry.title = title.to_string();
        }
        if let Some(content) = args["content"].as_str() {
            entry.content = content.to_string();
        }
        if let Some(tags) = args["tags"].as_str() {
            entry.tags = tags.to_string();
        }
        if let Some(env) = args["environment"].as_str() {
            entry.environment = env.to_string();
        }
        entry.updated_at = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs() as i64;

        db::update_knowledge_entry(self.graph_engine.db(), &entry)
            .map_err(|e| format!("Failed to update knowledge entry: {}", e))?;

        Ok(json!({
            "id": entry.id,
            "title": entry.title,
            "environment": entry.environment,
            "status": "updated"
        }))
    }

    fn delete_knowledge(&self, args: &Value) -> Result<Value, String> {
        let id = args["id"].as_str().ok_or("Missing id")?;

        db::delete_knowledge_entry(self.graph_engine.db(), id)
            .map_err(|e| format!("Failed to delete knowledge entry: {}", e))?;

        Ok(json!({
            "id": id,
            "status": "deleted"
        }))
    }

    fn search_knowledge_tool(&self, args: &Value) -> Result<Value, String> {
        let query_str = args["query"].as_str().ok_or("Missing query")?;
        let limit = args["limit"].as_u64().unwrap_or(20).min(50) as usize;
        let knowledge_type = args["knowledge_type"].as_str();
        let environment = args["environment"].as_str();

        let entries = db::search_knowledge(
            self.graph_engine.db(),
            query_str,
            knowledge_type,
            environment,
            limit,
        )
        .map_err(|e| format!("Failed to search knowledge: {}", e))?;

        let results: Vec<Value> = entries
            .iter()
            .map(|e| {
                json!({
                    "id": e.id,
                    "knowledge_type": e.knowledge_type,
                    "title": e.title,
                    "content_preview": truncate_str(&e.content, 200),
                    "element_qualified": e.element_qualified,
                    "tags": e.tags,
                    "environment": e.environment,
                    "branch": e.branch,
                    "author": e.author,
                    "created_at": e.created_at,
                    "updated_at": e.updated_at
                })
            })
            .collect();

        Ok(json!({
            "results": results,
            "count": results.len()
        }))
    }

    fn add_annotation(&self, args: &Value) -> Result<Value, String> {
        let element = args["element"].as_str().ok_or("Missing element")?;
        let description = args["description"].as_str().ok_or("Missing description")?;
        let user_story = args["user_story"].as_str();
        let feature = args["feature"].as_str();

        let existing = db::get_business_logic(self.graph_engine.db(), element)
            .map_err(|e| format!("DB error: {}", e))?;

        if existing.is_some() {
            db::update_business_logic(
                self.graph_engine.db(),
                element,
                description,
                user_story,
                feature,
            )
            .map_err(|e| format!("Failed to update annotation: {}", e))?;
        } else {
            db::create_business_logic(
                self.graph_engine.db(),
                element,
                description,
                user_story,
                feature,
            )
            .map_err(|e| format!("Failed to create annotation: {}", e))?;
        }

        Ok(json!({
            "element": element,
            "description": description,
            "action": if existing.is_some() { "updated" } else { "created" }
        }))
    }

    fn link_element_tool(&self, args: &Value) -> Result<Value, String> {
        let element = args["element"].as_str().ok_or("Missing element")?;
        let id = args["id"].as_str().ok_or("Missing id")?;
        let kind = args["kind"].as_str().ok_or("Missing kind")?;

        let existing = db::get_business_logic(self.graph_engine.db(), element)
            .map_err(|e| format!("DB error: {}", e))?;

        match existing {
            Some(bl) => {
                let new_desc = if bl.description.starts_with("Linked to") {
                    bl.description.clone()
                } else if kind == "story" {
                    format!("{} | Linked to story {}", bl.description, id)
                } else {
                    format!("{} | Linked to feature {}", bl.description, id)
                };
                db::update_business_logic(
                    self.graph_engine.db(),
                    element,
                    &new_desc,
                    if kind == "story" {
                        Some(id)
                    } else {
                        bl.user_story_id.as_deref()
                    },
                    if kind == "feature" {
                        Some(id)
                    } else {
                        bl.feature_id.as_deref()
                    },
                )
                .map_err(|e| format!("Failed to link: {}", e))?;
            }
            None => {
                let description = format!("Linked to {} {}", kind, id);
                db::create_business_logic(
                    self.graph_engine.db(),
                    element,
                    &description,
                    if kind == "story" { Some(id) } else { None },
                    if kind == "feature" { Some(id) } else { None },
                )
                .map_err(|e| format!("Failed to create link: {}", e))?;
            }
        }

        Ok(json!({
            "element": element,
            "linked_to": format!("{} {}", kind, id),
            "status": "linked"
        }))
    }

    fn add_documentation(&self, args: &Value) -> Result<Value, String> {
        let file_path = args["file_path"].as_str().ok_or("Missing file_path")?;
        let path = std::path::Path::new(file_path);
        if !path.exists() {
            return Err(format!("File not found: {}", file_path));
        }

        let parent = path.parent().unwrap_or(std::path::Path::new("."));
        let result = crate::doc_indexer::index_docs_directory(parent, &self.graph_engine)
            .map_err(|e| format!("Failed to index documentation: {}", e))?;

        Ok(json!({
            "file_indexed": file_path,
            "documents_processed": result.documents.len(),
            "total_references": result.relationships.len(),
            "status": "indexed"
        }))
    }

    // ========================================================================
    // Version/Branch Tagging Tools
    // ========================================================================

    fn search_by_environment(&self, args: &Value) -> Result<Value, String> {
        let environment = args["environment"].as_str().ok_or("Missing environment")?;
        let limit = args["limit"].as_u64().unwrap_or(20).min(50) as usize;

        let knowledge =
            db::get_knowledge_by_environment(self.graph_engine.db(), environment, limit)
                .map_err(|e| format!("Failed to search by environment: {}", e))?;

        let results: Vec<Value> = knowledge
            .iter()
            .map(|e| {
                json!({
                    "id": e.id,
                    "knowledge_type": e.knowledge_type,
                    "title": e.title,
                    "content_preview": truncate_str(&e.content, 200),
                    "branch": e.branch,
                    "author": e.author,
                    "environment": e.environment,
                    "created_at": e.created_at
                })
            })
            .collect();

        Ok(json!({
            "environment": environment,
            "results": results,
            "count": results.len()
        }))
    }

    fn get_upcoming_changes(&self, args: &Value) -> Result<Value, String> {
        let limit = args["limit"].as_u64().unwrap_or(20).min(50) as usize;
        let branch_filter = args["branch"].as_str();

        let mut entries =
            db::get_knowledge_by_environment(self.graph_engine.db(), "upcoming", limit)
                .map_err(|e| format!("Failed to get upcoming changes: {}", e))?;

        if let Some(branch) = branch_filter {
            entries.retain(|e| e.branch.as_deref() == Some(branch));
        }

        let results: Vec<Value> = entries
            .iter()
            .map(|e| {
                json!({
                    "id": e.id,
                    "knowledge_type": e.knowledge_type,
                    "title": e.title,
                    "content_preview": truncate_str(&e.content, 200),
                    "branch": e.branch,
                    "author": e.author,
                    "created_at": e.created_at
                })
            })
            .collect();

        Ok(json!({
            "environment": "upcoming",
            "results": results,
            "count": results.len()
        }))
    }

    fn promote_environment(&self, args: &Value) -> Result<Value, String> {
        let branch = args["branch"].as_str().ok_or("Missing branch")?;
        let target_env = args["target_environment"].as_str().unwrap_or("production");

        // Get all upcoming entries for this branch
        let mut entries =
            db::get_knowledge_by_environment(self.graph_engine.db(), "upcoming", 1000)
                .map_err(|e| format!("Failed to query knowledge: {}", e))?;

        entries.retain(|e| e.branch.as_deref() == Some(branch));

        let mut promoted = 0;
        for mut entry in entries {
            entry.environment = target_env.to_string();
            entry.updated_at = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64;

            db::update_knowledge_entry(self.graph_engine.db(), &entry)
                .map_err(|e| format!("Failed to promote entry: {}", e))?;
            promoted += 1;
        }

        Ok(json!({
            "branch": branch,
            "target_environment": target_env,
            "promoted_count": promoted,
            "status": "promoted"
        }))
    }

    fn query_incidents(&self, args: &Value) -> Result<Value, String> {
        let service = args["service"].as_str();
        let pattern = args["pattern"].as_str();
        let env = args["env"].as_str().unwrap_or("local");
        let limit = args["limit"].as_i64().unwrap_or(5) as usize;

        let incidents =
            db::query_incidents(self.graph_engine.db(), service, pattern, Some(env), limit)
                .map_err(|e| format!("Failed to query incidents: {}", e))?;

        let incidents_json: Vec<Value> = incidents
            .iter()
            .map(|i| {
                json!({
                    "id": i.id,
                    "env": i.env,
                    "title": i.title,
                    "severity": i.severity,
                    "occurred_at": i.occurred_at,
                    "resolved_at": i.resolved_at,
                    "root_cause": i.root_cause,
                    "resolution": i.resolution,
                    "affected_services": i.affected_services,
                    "trigger_pattern": i.trigger_pattern,
                    "prevention": i.prevention,
                    "tags": i.tags,
                    "author": i.author,
                    "linked_ticket": i.linked_ticket
                })
            })
            .collect();

        Ok(json!({
            "incidents": incidents_json,
            "query": {
                "service": service,
                "pattern": pattern,
                "env": env,
                "limit": limit
            }
        }))
    }

    fn find_env_conflicts(&self, args: &Value) -> Result<Value, String> {
        let service = args["service"]
            .as_str()
            .ok_or("Missing 'service' parameter")?;

        let conflicts = self
            .graph_engine
            .find_env_conflicts(service)
            .map_err(|e| format!("Failed to find env conflicts: {}", e))?;

        let conflicts_json: Vec<Value> = conflicts
            .into_iter()
            .map(|c| {
                json!({
                    "conflict_type": c.conflict_type,
                    "detail": c.detail,
                    "risk": c.risk,
                })
            })
            .collect();

        Ok(json!({
            "conflicts": conflicts_json,
            "service": service,
        }))
    }

    fn get_service_context(&self, args: &Value) -> Result<Value, String> {
        let service = args["service"]
            .as_str()
            .ok_or("Missing 'service' parameter")?;
        let env = args["env"].as_str().unwrap_or("local");

        let context = self
            .graph_engine
            .get_service_context(service, env)
            .map_err(|e| format!("Failed to get service context: {}", e))?;

        Ok(json!({
            "service": context.service,
            "env": context.env,
            "version": context.version,
            "team": context.team,
            "on_call": context.on_call,
            "repo_url": context.repo_url,
            "language": context.language,
            "calls": context.calls,
            "called_by": context.called_by,
            "schemas": context.schemas,
            "open_incidents": context.open_incidents,
            "recent_incidents": context.recent_incidents,
            "last_incident": context.last_incident,
            "known_risks": context.known_risks,
        }))
    }

    fn kg_context(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;
        let env = args["env"].as_str().unwrap_or("local");
        let depth = args["depth"].as_u64().unwrap_or(2) as u32;

        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());

        let context = query_engine
            .get_ontology_context(query, env, depth)
            .map_err(|e| format!("Failed to get ontology context: {}", e))?;

        Ok(json!({
            "matched_ontology_nodes": context.matched_ontology_nodes,
            "expanded_code_context": context.expanded_code_context,
            "expanded_relationships": context.expanded_relationships,
            "workflows": context.workflows,
            "workflow_steps": context.workflow_steps,
            "failure_modes": context.failure_modes,
            "confidence": context.confidence,
            "match_reasons": context.match_reasons,
        }))
    }

    fn kg_concept_map(&self, args: &Value) -> Result<Value, String> {
        let query = args["query"].as_str().ok_or("Missing 'query' parameter")?;
        let env = args["env"].as_str().unwrap_or("local");

        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());

        // Search for matching ontology nodes
        let nodes = query_engine
            .search_ontology_nodes(query, env, 2)
            .map_err(|e| format!("Failed to search ontology: {}", e))?;

        // Expand context for each node
        let mut all_elements = Vec::new();
        let mut all_relationships = Vec::new();

        for node in &nodes {
            let (elements, relationships) = query_engine
                .expand_ontology_context(&node.gid, 2)
                .unwrap_or_else(|_| (vec![], vec![]));
            all_elements.extend(elements);
            all_relationships.extend(relationships);
        }

        Ok(json!({
            "concept_nodes": nodes,
            "related_code": all_elements,
            "relationships": all_relationships,
        }))
    }

    fn kg_trace_workflow(&self, args: &Value) -> Result<Value, String> {
        let workflow_query = args["workflow_id_or_query"]
            .as_str()
            .ok_or("Missing 'workflow_id_or_query' parameter")?;
        let env = args["env"].as_str().unwrap_or("local");

        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());

        let steps = query_engine
            .trace_workflow(workflow_query, env)
            .map_err(|e| format!("Failed to trace workflow: {}", e))?;

        let step_info: Vec<serde_json::Value> = steps
            .iter()
            .map(|s| {
                json!({
                    "gid": s.gid,
                    "name": s.name,
                    "order": s.order,
                    "description": s.description,
                    "code_refs": s.metadata.code_refs,
                    "failure_modes": s.metadata.failure_modes,
                })
            })
            .collect();

        Ok(json!({
            "workflow_query": workflow_query,
            "steps": step_info,
            "step_count": steps.len(),
        }))
    }

    fn kg_ontology_status(&self, _args: &Value) -> Result<Value, String> {
        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());

        let status = query_engine
            .get_ontology_status()
            .map_err(|e| format!("Failed to get ontology status: {}", e))?;

        Ok(json!({
            "concept_counts": status.concept_counts,
            "procedural_counts": status.procedural_counts,
            "total_aliases": status.total_aliases,
            "nodes_missing_aliases": status.nodes_missing_aliases,
            "workflows_without_failure_modes": status.workflows_without_failure_modes,
        }))
    }

    fn kg_self_test(&self, _args: &Value) -> Result<Value, String> {
        let query_engine =
            crate::ontology::OntologyQueryEngine::new(self.graph_engine.db().clone());
        let report = query_engine.self_test();
        Ok(serde_json::to_value(report).unwrap_or_else(|e| {
            json!({
                "all_ok": false,
                "serialization_error": format!("{}", e),
            })
        }))
    }

    /// Embedding-backed semantic retrieval + adaptive KG traversal.
    /// Compiles out entirely unless the binary was built with
    /// `--features embeddings`.
    #[cfg(feature = "embeddings")]
    fn kg_semantic_context(&self, args: &Value) -> Result<Value, String> {
        use crate::embeddings as emb;
        use crate::graph::traversal::traverse_seeds;
        use crate::retrieval::{RetrieveOptions, SemanticRetrievalPipeline};

        let query = args["query"]
            .as_str()
            .ok_or("Missing 'query' parameter")?
            .trim();
        if query.is_empty() {
            return Err("'query' must not be empty".to_string());
        }

        let env = args["env"].as_str().unwrap_or("local").to_string();
        let top_k = args["top_k"].as_u64().unwrap_or(50) as usize;
        let rerank_top_n = args["rerank_top_n"].as_u64().unwrap_or(10) as usize;
        let do_traverse = args["traverse"].as_bool().unwrap_or(true);
        let include_worktrees = args["include_worktrees"].as_bool().unwrap_or(false);
        let debug = args["debug"].as_bool().unwrap_or(false);
        let project = args["project"].as_str().unwrap_or(".");

        // Vectors live in CozoDB now; freshness check is a state-table count.
        let has_vectors = crate::embeddings::state::list_all(self.graph_engine.db())
            .map(|rows| !rows.is_empty())
            .unwrap_or(false);
        if !has_vectors {
            return Err("No embedded vectors found. Run `leankg embed --init` \
                 to download models, then `leankg embed` to build the index."
                .to_string());
        }

        let t0 = std::time::Instant::now();
        let pipeline = SemanticRetrievalPipeline::new(self.graph_engine.db().clone())
            .map_err(|e| format!("Failed to init retrieval pipeline: {}", e))?;
        let t_pipeline_ms = t0.elapsed().as_millis() as u64;

        // CozoDB HNSW stores vectors as first-class data, so the old
        // `.meta.json` mtime staleness check no longer applies. Staleness
        // is now detected via the `embedding_state.state` column.
        let embeddings_stale = false;

        let opts = RetrieveOptions {
            env: Some(env.clone()),
            ann_top_k: Some(top_k),
            rerank_top_n,
            include_worktrees,
            include_ontology_steps: false,
            embeddings_stale,
        };

        let t1 = std::time::Instant::now();
        let retrieval = pipeline
            .retrieve(query, &opts)
            .map_err(|e| format!("Retrieval failed: {}", e))?;
        let t_retrieve_ms = t1.elapsed().as_millis() as u64;

        let mut traversed_json: Vec<Value> = Vec::new();
        let mut edges_json: Vec<Value> = Vec::new();
        let mut traverse_capped = false;
        let mut total_neighbors = 0usize;
        let mut t_traverse_ms = 0u64;

        if do_traverse && !retrieval.seeds.is_empty() {
            let t2 = std::time::Instant::now();
            let seeds_iter = retrieval
                .seeds
                .iter()
                .map(|s| (s.qualified_name.clone(), s.element_type.clone()));
            let traverse_result =
                traverse_seeds(&self.graph_engine, seeds_iter, Some(env.as_str()))
                    .map_err(|e| format!("Traversal failed: {}", e))?;
            t_traverse_ms = t2.elapsed().as_millis() as u64;
            traverse_capped = traverse_result.capped;
            total_neighbors = traverse_result.total_neighbors;

            traversed_json = traverse_result
                .nodes
                .iter()
                .map(|n| {
                    json!({
                        "qualified_name": n.qualified_name,
                        "element_type": n.element_type,
                        "from_seed": n.from_seed,
                        "via_edge": n.via_edge,
                        "hop": n.hop,
                    })
                })
                .collect();
            edges_json = traverse_result
                .edges
                .iter()
                .map(|e| {
                    json!({
                        "source": e.source,
                        "target": e.target,
                        "rel_type": e.rel_type,
                    })
                })
                .collect();
        }

        let total_ms = t0.elapsed().as_millis() as u64;

        let seeds_json: Vec<Value> = retrieval
            .seeds
            .iter()
            .map(|s| {
                let mut obj = json!({
                    "qualified_name": s.qualified_name,
                    "element_type": s.element_type,
                    "file_path": s.file_path,
                    "ann_distance": s.ann_distance,
                });
                if let Some(score) = s.rerank_score {
                    obj["rerank_score"] = json!(score);
                }
                if debug {
                    obj["blob_excerpt"] = json!(s.blob_excerpt);
                }
                obj
            })
            .collect();

        let mut response = json!({
            "query": query,
            "env": env,
            "seeds": seeds_json,
            "traversed": traversed_json,
        });

        if debug {
            let reranker_label = match retrieval.reranker_status {
                emb::RerankerStatus::Active => "bge-reranker-v2-m3",
                emb::RerankerStatus::Fallback => "fallback_ann",
            };
            response["diagnostics"] = json!({
                "ann_candidate_count": retrieval.ann_candidate_count,
                "worktree_filtered_count": retrieval.worktree_filtered_count,
                "env_filtered_count": retrieval.env_filtered_count,
                "reranker": reranker_label,
                "embeddings_stale": retrieval.embeddings_stale,
                "traversal": {
                    "enabled": do_traverse,
                    "capped": traverse_capped,
                    "neighbor_count": total_neighbors,
                },
                "latency_ms": {
                    "pipeline_init": t_pipeline_ms,
                    "retrieve": t_retrieve_ms,
                    "traverse": t_traverse_ms,
                    "total": total_ms,
                },
            });
            if !edges_json.is_empty() {
                response["diagnostics"]["edges"] = json!(edges_json);
            }
        }

        Ok(response)
    }

    fn wake_up(&self, args: &Value) -> Result<Value, String> {
        let project_path = args["project"].as_str().unwrap_or(".");
        let cache_path = std::path::Path::new(project_path)
            .join(".leankg")
            .join("wake_up.txt");

        if let Ok(cached) = std::fs::read_to_string(&cache_path) {
            if !cached.trim().is_empty() {
                return Ok(json!({
                    "context": cached,
                    "source": "cache",
                }));
            }
        }

        let summary = self
            .graph_engine
            .wake_up_summary()
            .unwrap_or_else(|e| format!("LeanKG project (context generation error: {})", e));

        if let Some(parent) = cache_path.parent() {
            let _ = std::fs::create_dir_all(parent);
        }
        let _ = std::fs::write(&cache_path, &summary);

        Ok(json!({
            "context": summary,
            "source": "generated",
        }))
    }

    /// FR-B20: Get architecture overview.
    /// FR-B22: Honors per-section max_items token budget.
    fn get_architecture(&self, args: &Value) -> Result<Value, String> {
        let max_items = args["max_items"].as_u64().map(|v| v as usize);
        self.graph_engine
            .get_architecture(max_items)
            .map_err(|e| format!("Failed to get architecture: {}", e))
    }

    /// FR-B21: Get graph schema overview.
    /// FR-B22: Honors per-section max_items token budget.
    fn get_graph_schema(&self, args: &Value) -> Result<Value, String> {
        let max_items = args["max_items"].as_u64().map(|v| v as usize);
        self.graph_engine
            .get_graph_schema(max_items)
            .map_err(|e| format!("Failed to get graph schema: {}", e))
    }

    /// FR-B23: Find dead code
    fn find_dead_code(&self, args: &Value) -> Result<Value, String> {
        let min_lines = args["min_lines"].as_u64().unwrap_or(10) as u32;
        let dead = self
            .graph_engine
            .find_dead_code(min_lines)
            .map_err(|e| format!("Failed to find dead code: {}", e))?;
        Ok(json!({
            "dead_functions": dead,
            "count": dead.len(),
            "min_lines": min_lines,
        }))
    }
}

fn uuid_simple() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let ts = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    format!("{:x}", ts)
}

fn truncate_str(s: &str, max_len: usize) -> String {
    if s.len() <= max_len {
        s.to_string()
    } else {
        format!("{}...", &s[..max_len])
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
