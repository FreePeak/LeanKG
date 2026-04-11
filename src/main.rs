#![allow(dead_code)]
mod api;
mod benchmark;
mod cli;
mod compress;
mod config;
mod db;
mod doc;
mod doc_indexer;
mod graph;
mod hooks;
mod indexer;
mod mcp;
mod orchestrator;
mod registry;
mod runtime;
mod watcher;
mod web;

use clap::Parser;

#[derive(Parser, Debug)]
#[command(name = "leankg")]
#[command(version)]
#[command(about = "Lightweight knowledge graph for AI-assisted development")]
pub struct Args {
    /// Enable compressed output for shell commands (RTK-style)
    #[arg(long, global = true)]
    pub compress: bool,
    #[command(subcommand)]
    pub command: cli::CLICommand,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = Args::parse();

    if !matches!(args.command, cli::CLICommand::McpStdio { watch: _, .. }) {
        tracing_subscriber::fmt::init();
    }

    match args.command {
        cli::CLICommand::Version => {
            println!("leankg {}", env!("CARGO_PKG_VERSION"));
        }
        cli::CLICommand::Update => {
            update_leankg().await?;
        }
        cli::CLICommand::Init { path } => {
            init_project(&path)?;
        }
        cli::CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            ensure_db_path(&db_path).await?;
            let exclude_patterns: Vec<String> = exclude
                .as_ref()
                .map(|e| e.split(',').map(|s| s.trim().to_string()).collect())
                .unwrap_or_default();
            if incremental {
                incremental_index_codebase(
                    path.as_deref().unwrap_or("."),
                    &db_path,
                    lang.as_deref(),
                    &exclude_patterns,
                    verbose,
                )
                .await?;
            } else {
                index_codebase(
                    path.as_deref().unwrap_or("."),
                    &db_path,
                    lang.as_deref(),
                    &exclude_patterns,
                    verbose,
                )
                .await?;
            }
        }
        cli::CLICommand::Serve { port } => {
            let port = port.unwrap_or_else(|| {
                std::env::var("PORT")
                    .ok()
                    .and_then(|p| p.parse().ok())
                    .unwrap_or(8080)
            });
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            ensure_db_path(&db_path).await?;
            web::start_server(port, db_path).await?;
        }
        cli::CLICommand::Web { port } => {
            let port = port.unwrap_or_else(|| {
                std::env::var("PORT")
                    .ok()
                    .and_then(|p| p.parse().ok())
                    .unwrap_or(8080)
            });
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            ensure_db_path(&db_path).await?;
            web::start_server(port, db_path).await?;
        }
        cli::CLICommand::McpStdio { watch, project_path } => {
            let explicit_project_path = project_path.map(|p| std::path::PathBuf::from(p));
            let project_path = explicit_project_path.clone().unwrap_or_else(|| find_project_root().unwrap_or_else(|_| std::path::PathBuf::from(".")));
            let db_path = project_path.join(".leankg");

            ensure_db_path(&db_path).await?;

            let mcp_server = if let Some(ref pp) = explicit_project_path {
                mcp::MCPServer::new_with_project_path(db_path, pp.clone())
            } else if watch {
                mcp::MCPServer::new_with_watch(db_path, project_path.clone())
            } else {
                mcp::MCPServer::new(db_path)
            };
            if let Err(e) = mcp_server.serve_stdio().await {
                eprintln!("MCP stdio server error: {}", e);
            }
        }
        cli::CLICommand::Impact { file, depth } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            let result = calculate_impact(&file, depth, &db_path)?;
            println!("Impact radius for {} (depth={}):", file, depth);
            if result.affected_elements.is_empty() {
                println!("  No affected elements found");
            } else {
                for elem in result.affected_elements.iter().take(20) {
                    println!("  - {}", elem.qualified_name);
                }
                if result.affected_elements.len() > 20 {
                    println!("  ... and {} more", result.affected_elements.len() - 20);
                }
            }
        }
        cli::CLICommand::Generate { template: _ } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            generate_docs(&db_path)?;
        }
        cli::CLICommand::Query { query, kind } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_query(&query, &kind, &db_path)?;
        }
        cli::CLICommand::Install => {
            install_mcp_config()?;
        }
        cli::CLICommand::Status => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            show_status(&db_path)?;
        }
        cli::CLICommand::Watch { path: _ } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");

            if !db_path.exists() {
                eprintln!("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.");
                std::process::exit(1);
            }

            println!("╔═══════════════════════════════════════╗");
            println!("║  LeanKG File Watcher                  ║");
            println!("╚═══════════════════════════════════════╝");
            println!("  Watching: {}", project_path.display());
            println!("  DB:       {}", db_path.display());
            println!("  Press Ctrl+C to stop.\n");

            let (tx, rx) = tokio::sync::mpsc::channel(100);
            mcp::watcher::start_watcher(db_path, project_path, rx).await;
            drop(tx);
        }
        cli::CLICommand::Quality { min_lines, lang } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            find_oversized_functions(min_lines, lang.as_deref(), &db_path)?;
        }
        cli::CLICommand::Export {
            output,
            format,
            file,
            depth,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            export_graph(&output, &format, file.as_deref(), depth, &db_path)?;
        }
        cli::CLICommand::Annotate {
            element,
            description,
            user_story,
            feature,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            annotate_element(
                &element,
                &description,
                user_story.as_deref(),
                feature.as_deref(),
                &db_path,
            )?;
        }
        cli::CLICommand::Link { element, id, kind } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            link_element(&element, &id, &kind, &db_path)?;
        }
        cli::CLICommand::SearchAnnotations { query } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            search_annotations(&query, &db_path)?;
        }
        cli::CLICommand::ShowAnnotations { element } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            show_annotations(&element, &db_path)?;
        }
        cli::CLICommand::Trace {
            feature,
            user_story,
            all,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            show_traceability(&db_path, feature.as_deref(), user_story.as_deref(), all)?;
        }
        cli::CLICommand::FindByDomain { domain } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            find_by_domain(&domain, &db_path)?;
        }
        cli::CLICommand::Benchmark { category, cli } => {
            let cli_tool = match cli.as_str() {
                "opencode" => benchmark::CliTool::OpenCode,
                "gemini" => benchmark::CliTool::Gemini,
                "kilo" | _ => benchmark::CliTool::Kilo,
            };
            benchmark::run(category, cli_tool)?;
        }
        cli::CLICommand::Register { name } => {
            register_repo(&name)?;
        }
        cli::CLICommand::Unregister { name } => {
            unregister_repo(&name)?;
        }
        cli::CLICommand::List => {
            list_repos()?;
        }
        cli::CLICommand::StatusRepo { name } => {
            status_repo(&name)?;
        }
        cli::CLICommand::Setup {} => {
            setup_global()?;
        }
        cli::CLICommand::Run { command, compress } => {
            run_shell_command(&command, compress)?;
        }
        cli::CLICommand::DetectClusters {
            path,
            min_hub_edges: _,
        } => {
            let project_path = if let Some(p) = path {
                std::path::PathBuf::from(p)
            } else {
                find_project_root()?
            };
            let db_path = project_path.join(".leankg");
            detect_clusters(&db_path)?;
        }
        cli::CLICommand::ApiServe { port, auth } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            ensure_db_path(&db_path).await?;
            api::start_api_server(port, db_path, auth).await?;
        }
        cli::CLICommand::ApiKey { command } => match command {
            cli::ApiKeyCommand::Create { name } => {
                api_key_create(&name)?;
            }
            cli::ApiKeyCommand::List => {
                api_key_list()?;
            }
            cli::ApiKeyCommand::Revoke { id } => {
                api_key_revoke(&id)?;
            }
        },
        cli::CLICommand::Metrics {
            since,
            tool,
            json,
            session,
            reset,
            retention,
            cleanup,
            seed,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            
            if seed {
                seed_test_metrics(&db_path)?;
                return Ok(());
            }
            
            show_metrics(
                &db_path,
                since.as_deref(),
                tool.as_deref(),
                json,
                session,
                reset,
                retention,
                cleanup,
            )?;
        }
        cli::CLICommand::Wiki { output } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            generate_wiki(&output, &db_path)?;
        }
        cli::CLICommand::Hooks { command } => {
            let project_path = find_project_root()?;
            match command {
                cli::HooksCommand::Install => {
                    install_hooks(&project_path)?;
                }
                cli::HooksCommand::Uninstall => {
                    uninstall_hooks(&project_path)?;
                }
                cli::HooksCommand::Status => {
                    check_hooks_status(&project_path)?;
                }
                cli::HooksCommand::Watch { path } => {
                    let watch_path = if let Some(p) = path {
                        std::path::PathBuf::from(p)
                    } else {
                        project_path.clone()
                    };
                    let db_path = project_path.join(".leankg");
                    watch_git_events(&watch_path, &db_path).await?;
                }
            }
        }
    }

    Ok(())
}

fn find_project_root() -> Result<std::path::PathBuf, Box<dyn std::error::Error>> {
    let current_dir = std::env::current_dir()?;
    if current_dir.join(".leankg").is_dir() || current_dir.join("leankg.yaml").exists() {
        return Ok(current_dir);
    }
    for parent in current_dir.ancestors() {
        if parent.join(".leankg").is_dir() || parent.join("leankg.yaml").exists() {
            return Ok(parent.to_path_buf());
        }
    }
    Ok(current_dir)
}

/// Ensures the .leankg directory exists, handling legacy cases where
/// .leankg was a file instead of a directory.
async fn ensure_db_path(db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if db_path.exists() && !db_path.is_dir() {
        eprintln!(
            "Warning: '{}' is a file, removing it and creating directory",
            db_path.display()
        );
        tokio::fs::remove_file(db_path).await?;
    }
    tokio::fs::create_dir_all(db_path).await?;
    Ok(())
}

fn init_project(path: &str) -> Result<(), Box<dyn std::error::Error>> {
    let config = config::ProjectConfig::default();
    let config_yaml = serde_yaml::to_string(&config)?;

    std::fs::create_dir_all(path)?;
    std::fs::write(std::path::Path::new(path).join("leankg.yaml"), config_yaml)?;

    let readme = r#"# Project

This project uses LeanKG for code intelligence.

## Setup

```bash
leankg init
leankg index ./src
```

## Commands

- `leankg index ./src` - Index codebase
- `leankg serve` - Start server
- `leankg impact <file> --depth 3` - Calculate impact radius
"#;
    std::fs::write(std::path::Path::new(path).join("README.md"), readme)?;

    println!("Initialized LeanKG project at {}", path);
    Ok(())
}

async fn index_codebase(
    path: &str,
    db_path: &std::path::Path,
    lang_filter: Option<&str>,
    exclude_patterns: &[String],
    verbose: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let mut parser_manager = indexer::ParserManager::new();
    parser_manager.init_parsers()?;

    println!("Indexing codebase at {}...", path);

    let mut files = indexer::find_files_sync(path)?;

    if let Some(lang) = lang_filter {
        let allowed_langs: Vec<&str> = lang.split(',').map(|s| s.trim()).collect();
        files.retain(|f| {
            if let Some(ext) = std::path::Path::new(f).extension() {
                let ext_str = ext.to_string_lossy().to_lowercase();
                let lang_map: std::collections::HashMap<&str, &str> = [
                    ("go", "go"),
                    ("rs", "rust"),
                    ("ts", "typescript"),
                    ("js", "javascript"),
                    ("py", "python"),
                    ("java", "java"),
                    ("kt", "kotlin"),
                    ("kts", "kotlin"),
                    ("cpp", "cpp"),
                    ("cxx", "cpp"),
                    ("cc", "cpp"),
                    ("hpp", "cpp"),
                    ("h", "cpp"),
                    ("c", "cpp"),
                    ("cs", "csharp"),
                    ("rb", "ruby"),
                    ("php", "php"),
                ]
                .iter()
                .cloned()
                .collect();
                if let Some(lang_name) = lang_map.get(ext_str.as_str()) {
                    return allowed_langs.iter().any(|l| l.to_lowercase() == *lang_name);
                }
            }
            false
        });
        if verbose {
            println!("Language filter applied: {} allowed", allowed_langs.len());
        }
    }

    if !exclude_patterns.is_empty() {
        let prev_len = files.len();
        files.retain(|f| !exclude_patterns.iter().any(|pat| f.contains(pat)));
        if verbose {
            println!(
                "Excluded {} files (matched --exclude patterns)",
                prev_len - files.len()
            );
        }
    }

    println!("Found {} files to index", files.len());

    let total_elements = indexer::index_files_parallel(&graph_engine, &files, verbose)?;
    println!("Indexed {} files ({} elements)", files.len(), total_elements);

    let docs_path = std::path::Path::new("docs");
    if docs_path.exists() {
        println!("Indexing documentation at docs/...");
        match doc_indexer::index_docs_directory(docs_path, &graph_engine) {
            Ok(result) => {
                println!(
                    "Indexed {} documents and {} sections",
                    result.documents.len(),
                    result.sections.len()
                );
                if verbose && !result.relationships.is_empty() {
                    println!(
                        "  Created {} documentation relationships",
                        result.relationships.len()
                    );
                }
            }
            Err(e) => {
                eprintln!("Warning: Failed to index docs: {}", e);
            }
        }
    }

    Ok(())
}

async fn incremental_index_codebase(
    path: &str,
    db_path: &std::path::Path,
    lang_filter: Option<&str>,
    exclude_patterns: &[String],
    verbose: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let mut parser_manager = indexer::ParserManager::new();
    parser_manager.init_parsers()?;

    println!("Performing incremental indexing for {}...", path);

    match indexer::incremental_index_sync(&graph_engine, &mut parser_manager, path).await {
        Ok(result) => {
            if result.changed_files.is_empty() && result.dependent_files.is_empty() {
                println!("No changes detected since last index.");
            } else {
                println!("Changed files: {}", result.changed_files.len());
                for f in &result.changed_files {
                    println!("  Modified: {}", f);
                }

                println!(
                    "Dependent files re-indexed: {}",
                    result.dependent_files.len()
                );
                for f in &result.dependent_files {
                    println!("  Dependent: {}", f);
                }

                println!("Total files processed: {}", result.total_files_processed);
                println!("Total elements indexed: {}", result.elements_indexed);

                println!("Resolving call edges...");
                match graph_engine.resolve_call_edges() {
                    Ok(count) => {
                        if count > 0 {
                            println!("  Resolved {} call edges", count);
                        }
                    }
                    Err(e) => {
                        eprintln!("Warning: Failed to resolve call edges: {}", e);
                    }
                }
            }
        }
        Err(e) => {
            println!(
                "Incremental index failed: {}. Falling back to full index.",
                e
            );
            index_codebase(path, db_path, lang_filter, exclude_patterns, verbose).await?;
        }
    }

    Ok(())
}

fn calculate_impact(
    file: &str,
    depth: u32,
    db_path: &std::path::Path,
) -> Result<graph::ImpactResult, Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let analyzer = graph::ImpactAnalyzer::new(&graph_engine);

    let result = analyzer.calculate_impact_radius(file, depth)?;
    Ok(result)
}

fn generate_docs(db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let generator = doc::DocGenerator::new(graph_engine, std::path::PathBuf::from("./docs"));

    let content = generator.generate_agents_md()?;
    println!("Generated documentation:\n{}", content);

    std::fs::create_dir_all("./docs")?;
    std::fs::write("./docs/AGENTS.md", &content)?;
    println!("\nSaved to docs/AGENTS.md");

    Ok(())
}

fn install_mcp_config() -> Result<(), Box<dyn std::error::Error>> {
    let exe_path =
        std::env::current_exe().map_err(|e| format!("Failed to get current exe path: {}", e))?;

    // Create .cursor/mcp.json for per-project Cursor MCP configuration
    let mcp_config = serde_json::json!({
        "mcpServers": {
            "leankg": {
                "command": exe_path.to_string_lossy().as_ref(),
                "args": ["mcp-stdio"]
            }
        }
    });

    let cursor_dir = std::path::Path::new(".cursor");
    std::fs::create_dir_all(cursor_dir)?;

    let mcp_path = cursor_dir.join("mcp.json");
    std::fs::write(&mcp_path, serde_json::to_string_pretty(&mcp_config)?)?;
    println!("Installed MCP config to .cursor/mcp.json");
    println!("Restart Cursor to activate LeanKG MCP server for this project.");

    Ok(())
}

fn show_status(db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !db_path.exists() {
        println!("LeanKG not initialized. Run 'leankg init' first.");
        return Ok(());
    }

    let db = db::schema::init_db(db_path)?;

    let elements = graph::GraphEngine::new(db.clone()).all_elements()?;
    let relationships = graph::GraphEngine::new(db.clone()).all_relationships()?;
    let annotations = db::all_business_logic(&db)?;

    println!("LeanKG Status:");
    println!("  Database: {}", db_path.display());
    println!("  Elements: {}", elements.len());
    println!("  Relationships: {}", relationships.len());

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

    println!("  Files: {}", files);
    println!("  Functions: {}", functions);
    println!("  Classes: {}", classes);
    println!("  Annotations: {}", annotations.len());

    Ok(())
}

fn annotate_element(
    element: &str,
    description: &str,
    user_story: Option<&str>,
    feature: Option<&str>,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    let existing = db::get_business_logic(&db, element)?;

    if existing.is_some() {
        db::update_business_logic(&db, element, description, user_story, feature)?;
        println!("Updated annotation for '{}'", element);
    } else {
        db::create_business_logic(&db, element, description, user_story, feature)?;
        println!("Created annotation for '{}'", element);
    }

    println!("  Description: {}", description);
    if let Some(story) = user_story {
        println!("  User Story: {}", story);
    }
    if let Some(feat) = feature {
        println!("  Feature: {}", feat);
    }

    Ok(())
}

fn link_element(
    element: &str,
    id: &str,
    kind: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    let existing = db::get_business_logic(&db, element)?;

    match existing {
        Some(bl) => {
            if kind == "story" {
                let new_desc = if bl.description.starts_with("Linked to") {
                    bl.description
                } else {
                    format!("{} | Linked to story {}", bl.description, id)
                };
                db::update_business_logic(
                    &db,
                    element,
                    &new_desc,
                    Some(id),
                    bl.feature_id.as_deref(),
                )?;
            } else {
                let new_desc = if bl.description.starts_with("Linked to") {
                    bl.description
                } else {
                    format!("{} | Linked to feature {}", bl.description, id)
                };
                db::update_business_logic(
                    &db,
                    element,
                    &new_desc,
                    bl.user_story_id.as_deref(),
                    Some(id),
                )?;
            }
        }
        None => {
            let description = format!("Linked to {} {}", kind, id);
            if kind == "story" {
                db::create_business_logic(&db, element, &description, Some(id), None)?;
            } else {
                db::create_business_logic(&db, element, &description, None, Some(id))?;
            }
        }
    }

    println!("Linked '{}' to {} {}", element, kind, id);

    Ok(())
}

fn search_annotations(
    query: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    let results = db::search_business_logic(&db, query)?;

    if results.is_empty() {
        println!("No annotations found matching '{}'", query);
    } else {
        println!("Found {} annotation(s):", results.len());
        for bl in results {
            println!("\n  Element: {}", bl.element_qualified);
            println!("  Description: {}", bl.description);
            if let Some(story) = bl.user_story_id {
                println!("  User Story: {}", story);
            }
            if let Some(feature) = bl.feature_id {
                println!("  Feature: {}", feature);
            }
        }
    }

    Ok(())
}

fn show_annotations(
    element: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    let result = db::get_business_logic(&db, element)?;

    match result {
        Some(bl) => {
            println!("Annotations for '{}':", element);
            println!("  Description: {}", bl.description);
            if let Some(story) = bl.user_story_id {
                println!("  User Story: {}", story);
            }
            if let Some(feature) = bl.feature_id {
                println!("  Feature: {}", feature);
            }
        }
        None => {
            println!("No annotations found for '{}'", element);
        }
    }

    Ok(())
}

fn show_traceability(
    db_path: &std::path::Path,
    feature: Option<&str>,
    user_story: Option<&str>,
    all: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    if all {
        let all_bl = db::all_business_logic(&db)?;

        let mut feature_map: std::collections::HashMap<String, Vec<_>> =
            std::collections::HashMap::new();
        let mut story_map: std::collections::HashMap<String, Vec<_>> =
            std::collections::HashMap::new();

        for bl in &all_bl {
            if let Some(ref fid) = bl.feature_id {
                feature_map.entry(fid.clone()).or_default().push(bl);
            }
            if let Some(ref sid) = bl.user_story_id {
                story_map.entry(sid.clone()).or_default().push(bl);
            }
        }

        println!("Feature-to-Code Traceability:");
        if feature_map.is_empty() {
            println!("  No features with linked code elements");
        } else {
            for (fid, elements) in &feature_map {
                println!("\n  Feature: {}", fid);
                println!("    Code elements ({}):", elements.len());
                for elem in elements.iter().take(5) {
                    println!("      - {}: {}", elem.element_qualified, elem.description);
                }
                if elements.len() > 5 {
                    println!("      ... and {} more", elements.len() - 5);
                }
            }
        }

        println!("\nUser Story-to-Code Traceability:");
        if story_map.is_empty() {
            println!("  No user stories with linked code elements");
        } else {
            for (sid, elements) in &story_map {
                println!("\n  User Story: {}", sid);
                println!("    Code elements ({}):", elements.len());
                for elem in elements.iter().take(5) {
                    println!("      - {}: {}", elem.element_qualified, elem.description);
                }
                if elements.len() > 5 {
                    println!("      ... and {} more", elements.len() - 5);
                }
            }
        }
    } else if let Some(fid) = feature {
        let elements = db::get_by_feature(&db, fid)?;
        println!("Feature-to-Code Traceability for '{}':", fid);
        if elements.is_empty() {
            println!("  No code elements linked to this feature");
        } else {
            for elem in elements {
                println!("\n  Element: {}", elem.element_qualified);
                println!("    Description: {}", elem.description);
                if let Some(story) = elem.user_story_id {
                    println!("    User Story: {}", story);
                }
            }
        }
    } else if let Some(sid) = user_story {
        let elements = db::get_by_user_story(&db, sid)?;
        println!("User Story-to-Code Traceability for '{}':", sid);
        if elements.is_empty() {
            println!("  No code elements linked to this user story");
        } else {
            for elem in elements {
                println!("\n  Element: {}", elem.element_qualified);
                println!("    Description: {}", elem.description);
                if let Some(feat) = elem.feature_id {
                    println!("    Feature: {}", feat);
                }
            }
        }
    } else {
        println!("Specify --all, --feature <id>, or --user-story <id>");
    }

    Ok(())
}

fn find_by_domain(
    domain: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    let results = db::search_business_logic(&db, domain)?;

    if results.is_empty() {
        println!("No code elements found matching domain '{}'", domain);
    } else {
        println!(
            "Found {} code element(s) for domain '{}':",
            results.len(),
            domain
        );
        for bl in results {
            println!("\n  Element: {}", bl.element_qualified);
            println!("    Description: {}", bl.description);
            if let Some(story) = bl.user_story_id {
                println!("    User Story: {}", story);
            }
            if let Some(feat) = bl.feature_id {
                println!("    Feature: {}", feat);
            }
        }
    }

    Ok(())
}

fn run_query(
    query: &str,
    kind: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);

    match kind {
        "name" => {
            let results = graph_engine.search_by_name(query)?;
            if results.is_empty() {
                println!("No elements found with name matching '{}'", query);
            } else {
                println!("Found {} element(s) with name '{}':", results.len(), query);
                for elem in results {
                    println!(
                        "  - {} ({}:{} {})",
                        elem.name, elem.element_type, elem.line_start, elem.line_end
                    );
                    println!("    File: {}", elem.file_path);
                }
            }
        }
        "type" => {
            let results = graph_engine.search_by_type(query)?;
            if results.is_empty() {
                println!("No elements found of type '{}'", query);
            } else {
                println!("Found {} element(s) of type '{}':", results.len(), query);
                for elem in results {
                    println!(
                        "  - {} ({}:{})",
                        elem.qualified_name, elem.line_start, elem.line_end
                    );
                }
            }
        }
        "rel" => {
            let results = graph_engine.search_by_relation_type(query)?;
            if results.is_empty() {
                println!("No relationships found with type '{}'", query);
            } else {
                println!(
                    "Found {} relationship(s) of type '{}':",
                    results.len(),
                    query
                );
                for rel in results {
                    println!(
                        "  - {} -> {} ({})",
                        rel.source_qualified, rel.target_qualified, rel.rel_type
                    );
                }
            }
        }
        "pattern" => {
            let results = graph_engine.search_by_pattern(query)?;
            if results.is_empty() {
                println!("No elements found matching pattern '{}'", query);
            } else {
                println!(
                    "Found {} element(s) matching pattern '{}':",
                    results.len(),
                    query
                );
                for elem in results {
                    println!(
                        "  - {} ({}:{})",
                        elem.qualified_name, elem.element_type, elem.file_path
                    );
                }
            }
        }
        _ => {
            println!(
                "Unknown query kind '{}'. Use: name, type, rel, or pattern",
                kind
            );
        }
    }

    Ok(())
}

fn find_oversized_functions(
    min_lines: u32,
    lang: Option<&str>,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);

    let results = if let Some(language) = lang {
        graph_engine.find_oversized_functions_by_lang(min_lines, language)?
    } else {
        graph_engine.find_oversized_functions(min_lines)?
    };

    if results.is_empty() {
        println!("No functions found with >= {} lines", min_lines);
    } else {
        println!(
            "Found {} oversized function(s) (>={} lines):",
            results.len(),
            min_lines
        );
        for elem in &results {
            let line_count = elem.line_end - elem.line_start + 1;
            println!(
                "  - {} ({} lines, {}:{})",
                elem.name, line_count, elem.file_path, elem.line_start
            );
        }
    }

    Ok(())
}

fn register_repo(name: &str) -> Result<(), Box<dyn std::error::Error>> {
    let mut registry = registry::Registry::load()?;
    let current_dir = std::env::current_dir()?;
    let path = current_dir.to_string_lossy().to_string();

    registry.register(name.to_string(), path)?;
    println!(
        "Registered repository '{}' at {}",
        name,
        current_dir.display()
    );
    Ok(())
}

fn unregister_repo(name: &str) -> Result<(), Box<dyn std::error::Error>> {
    let mut registry = registry::Registry::load()?;

    if registry.get_repo(name).is_none() {
        println!("Repository '{}' not found in registry", name);
        return Ok(());
    }

    registry.unregister(name)?;
    println!("Unregistered repository '{}'", name);
    Ok(())
}

fn list_repos() -> Result<(), Box<dyn std::error::Error>> {
    let registry = registry::Registry::load()?;
    let repos = registry.list_repos();

    if repos.is_empty() {
        println!("No repositories registered. Run 'leankg register <name>' to add one.");
        return Ok(());
    }

    println!("Registered repositories:");
    for (name, entry) in repos {
        println!(
            "  - {}: {} (indexed: {:?})",
            name, entry.path, entry.last_indexed
        );
    }
    Ok(())
}

fn status_repo(name: &str) -> Result<(), Box<dyn std::error::Error>> {
    let registry = registry::Registry::load()?;

    match registry.get_repo(name) {
        Some(entry) => {
            println!("Repository: {}", name);
            println!("  Path: {}", entry.path);
            println!("  Last indexed: {:?}", entry.last_indexed);
            println!("  Element count: {:?}", entry.element_count);

            let db_path = std::path::Path::new(&entry.path).join(".leankg");
            if db_path.exists() {
                if let Ok(db) = db::schema::init_db(&db_path) {
                    let graph_engine = graph::GraphEngine::new(db);
                    if let Ok(elements) = graph_engine.all_elements() {
                        println!("  Current elements: {}", elements.len());
                    }
                    if let Ok(relationships) = graph_engine.all_relationships() {
                        println!("  Current relationships: {}", relationships.len());
                    }
                }
            } else {
                println!("  Status: Not indexed (no .leankg directory found)");
            }
        }
        None => {
            println!("Repository '{}' not found in registry", name);
        }
    }
    Ok(())
}

fn setup_global() -> Result<(), Box<dyn std::error::Error>> {
    let registry = registry::Registry::load()?;
    let repos = registry.list_repos();

    if repos.is_empty() {
        println!("No repositories registered. Run 'leankg register <name>' to add one.");
        return Ok(());
    }

    println!(
        "Setting up MCP configuration for {} repository(ies)...",
        repos.len()
    );

    let exe_path = std::env::current_exe()?;
    let config_dir =
        std::path::Path::new(&std::env::var("HOME").unwrap_or_else(|_| ".".to_string()))
            .join(".config")
            .join("mcp");

    std::fs::create_dir_all(&config_dir)?;

    let mut mcp_servers: serde_json::Map<String, serde_json::Value> = serde_json::Map::new();

    for (name, entry) in &repos {
        let server_name = format!("leankg-{}", name);
        mcp_servers.insert(
            server_name,
            serde_json::json!({
                "command": exe_path.to_string_lossy(),
                "args": ["mcp-stdio"],
                "cwd": entry.path
            }),
        );
        println!("  Configured MCP for '{}' at {}", name, entry.path);
    }

    let mcp_config = serde_json::json!({
        "mcpServers": mcp_servers
    });

    let config_path = config_dir.join("leankg-global.json");
    std::fs::write(&config_path, serde_json::to_string_pretty(&mcp_config)?)?;
    println!("\nGlobal MCP config written to: {}", config_path.display());
    println!("You can now use 'opencode --mcp-config ~/.config/mcp/leankg-global.json' to access all repositories.");

    Ok(())
}

fn detect_clusters(db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !db_path.exists() {
        println!("LeanKG not initialized. Run 'leankg init' first.");
        return Ok(());
    }

    let db = db::schema::init_db(db_path)?;
    let detector = graph::clustering::CommunityDetector::new(&db);

    println!("Running community detection...");
    let clusters = detector.detect_communities()?;

    if clusters.is_empty() {
        println!("No clusters found. Make sure the codebase is indexed.");
        return Ok(());
    }

    println!("\nDetected {} clusters:", clusters.len());

    let stats = graph::clustering::get_cluster_stats(&clusters);
    println!("  Total members: {}", stats.total_members);
    println!("  Average cluster size: {:.1}", stats.avg_cluster_size);

    let mut sorted_clusters: Vec<_> = clusters.values().collect();
    sorted_clusters.sort_by(|a, b| b.members.len().cmp(&a.members.len()));

    for cluster in sorted_clusters.iter().take(20) {
        println!("\n  Cluster: {} ({})", cluster.label, cluster.id);
        println!("    Members: {}", cluster.members.len());
        println!("    Files: {:?}", cluster.representative_files);
        for member in cluster.members.iter().take(5) {
            println!("      - {}", member);
        }
        if cluster.members.len() > 5 {
            println!("      ... and {} more", cluster.members.len() - 5);
        }
    }

    if sorted_clusters.len() > 20 {
        println!("\n... and {} more clusters", sorted_clusters.len() - 20);
    }

    println!("\nAssigning clusters to elements...");
    detector.assign_clusters_to_elements()?;
    println!("Done! Cluster assignments saved to the database.");

    Ok(())
}

fn api_key_create(name: &str) -> Result<(), Box<dyn std::error::Error>> {
    let store = db::keys::ApiKeyStore::new()?;
    let (key, api_key) = store.create_key(name)?;

    println!("API key created successfully!");
    println!("  ID:   {}", api_key.id);
    println!("  Name: {}", api_key.name);
    println!("  Created: {}", api_key.created_at);
    println!("\nIMPORTANT: Save this API key - it will not be shown again:");
    println!("  {}", key);

    Ok(())
}

fn api_key_list() -> Result<(), Box<dyn std::error::Error>> {
    let store = db::keys::ApiKeyStore::new()?;
    let keys = store.list_keys()?;

    if keys.is_empty() {
        println!("No API keys found. Create one with 'leankg api-key create --name <name>'");
        return Ok(());
    }

    println!("API Keys:");
    for key in keys {
        println!("  ID:        {}", key.id);
        println!("  Name:      {}", key.name);
        println!("  Created:   {}", key.created_at);
        if let Some(last_used) = key.last_used_at {
            println!("  Last used: {}", last_used);
        }
        println!();
    }

    Ok(())
}

fn api_key_revoke(id: &str) -> Result<(), Box<dyn std::error::Error>> {
    let store = db::keys::ApiKeyStore::new()?;
    let revoked = store.revoke_key(id)?;

    if revoked {
        println!("API key '{}' revoked successfully.", id);
    } else {
        println!("API key '{}' not found or already revoked.", id);
    }

    Ok(())
}

fn show_metrics(
    db_path: &std::path::Path,
    since: Option<&str>,
    tool: Option<&str>,
    json: bool,
    session: bool,
    reset: bool,
    retention: Option<i32>,
    cleanup: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;

    if reset {
        let count = db::reset_metrics(&db)?;
        println!("Reset {} metric record(s).", count);
        return Ok(());
    }

    if cleanup {
        let ret_days = retention.unwrap_or(30);
        let count = db::cleanup_old_metrics(&db, ret_days)?;
        println!("Cleaned up {} old metric record(s) (retention: {} days).", count, ret_days);
        return Ok(());
    }

    let ret_days = if let Some(s) = since {
        if s.ends_with('d') {
            s[..s.len() - 1].parse().unwrap_or(30)
        } else {
            s.parse().unwrap_or(30)
        }
    } else {
        retention.unwrap_or(30)
    };

    let summary = db::get_metrics_summary(&db, tool, ret_days)?;

    if json {
        println!("{}", serde_json::to_string_pretty(&summary)?);
        return Ok(());
    }

    println!("=== LeanKG Context Metrics ===\n");
    let display_total = if summary.total_tokens_saved < 0 { 0 } else { summary.total_tokens_saved };
    println!(
        "Total Savings: {} tokens across {} calls",
        display_total, summary.total_invocations
    );
    println!(
        "Average Savings: {:.1}%",
        summary.average_savings_percent.max(0.0)
    );
    println!("Retention: {} days", summary.retention_days);

    if !summary.by_tool.is_empty() {
        println!("\nBy Tool:");
        for tm in &summary.by_tool {
            if tm.total_saved > 0 {
                println!(
                    "  {}: {} calls,  avg {:.0}% saved, {} tokens saved",
                    tm.tool_name,
                    tm.calls,
                    tm.avg_savings_percent.max(0.0),
                    tm.total_saved
                );
            }
        }
    }

    if !summary.by_day.is_empty() {
        println!("\nBy Day:");
        for dm in &summary.by_day {
            if dm.savings > 0 {
                println!(
                    "  {}:  {} calls, {} tokens saved",
                    dm.date, dm.calls, dm.savings
                );
            }
        }
    }

    if session {
        println!("\nSession: Showing current session metrics not yet implemented");
    }

    Ok(())
}

fn seed_test_metrics(db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;
    
    let test_metrics = vec![
        ("seed1", "search_code", now - 100, 150i32, 45i32, 12i32, 25i32, 12000i32, 5000i32, 11955i32, 99.6f64, true),
        ("seed2", "get_context", now - 90, 200i32, 35i32, 8i32, 18i32, 8000i32, 3200i32, 7965i32, 99.6f64, true),
        ("seed3", "find_function", now - 80, 80i32, 28i32, 5i32, 12i32, 6000i32, 2400i32, 5972i32, 99.5f64, true),
        ("seed4", "search_code", now - 70, 120i32, 52i32, 15i32, 30i32, 14000i32, 5800i32, 13948i32, 99.6f64, true),
        ("seed5", "get_impact_radius", now - 60, 300i32, 180i32, 25i32, 45i32, 25000i32, 10000i32, 24820i32, 99.3f64, true),
        // Negative savings - these should be filtered out
        ("seed6", "get_clusters", now - 50, 100i32, 500i32, 5i32, 10i32, 300i32, 100i32, -200i32, -66.7f64, true),
        ("seed7", "get_code_tree", now - 40, 200i32, 800i32, 10i32, 20i32, 500i32, 200i32, -300i32, -60.0f64, true),
    ];
    
    for (id, tool, ts, inp, out, elem, ms, base, lines, saved, pct, success) in &test_metrics {
        let metric = db::models::ContextMetric {
            tool_name: tool.to_string(),
            timestamp: *ts,
            project_path: "/test".to_string(),
            input_tokens: *inp,
            output_tokens: *out,
            output_elements: *elem,
            execution_time_ms: *ms,
            baseline_tokens: *base,
            baseline_lines_scanned: *lines,
            tokens_saved: *saved,
            savings_percent: *pct,
            correct_elements: Some(*elem),
            total_expected: Some(*elem + 2),
            f1_score: Some(0.85),
            query_pattern: Some("name".to_string()),
            query_file: Some("src/*.rs".to_string()),
            query_depth: Some(2),
            success: *success,
            is_deleted: false,
        };
        db::record_metric(&db, &metric)?;
        println!("Seeded metric: {} ({})", id, tool);
    }
    
    println!("Seeded {} test metrics", test_metrics.len());
    Ok(())
}

async fn start_api_server_async(
    port: u16,
    require_auth: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    api::start_api_server(port, db_path, require_auth).await
}

fn export_graph(
    output: &str,
    format: &str,
    file_scope: Option<&str>,
    depth: u32,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    if !db_path.exists() {
        return Err("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.".into());
    }

    let db = db::schema::init_db(db_path)?;
    let engine = graph::GraphEngine::new(db);

    let (elements, relationships) = if let Some(file) = file_scope {
        // Scoped export: BFS traversal from file
        let mut visited_files = std::collections::HashSet::new();
        let mut queue = vec![(file.to_string(), 0u32)];
        let mut scoped_rels = Vec::new();

        while let Some((current, d)) = queue.pop() {
            if d >= depth || !visited_files.insert(current.clone()) {
                continue;
            }
            if let Ok(rels) = engine.get_relationships(&current) {
                for rel in &rels {
                    queue.push((rel.target_qualified.clone(), d + 1));
                }
                scoped_rels.extend(rels);
            }
        }

        let scoped_elements: Vec<_> = engine
            .all_elements()?
            .into_iter()
            .filter(|e| visited_files.contains(&e.file_path))
            .collect();
        (scoped_elements, scoped_rels)
    } else {
        (engine.all_elements()?, engine.all_relationships()?)
    };

    let content = match format {
        "json" => export_json(&elements, &relationships)?,
        "dot" => export_dot(&elements, &relationships),
        "mermaid" => export_mermaid(&relationships),
        "html" => {
            let exporter = graph::export::HtmlExporter::new();
            exporter.generate_html(&elements, &relationships)
        }
        "svg" => {
            let exporter = graph::export::SvgExporter::new();
            exporter.generate_svg(&elements, &relationships)
        }
        "graphml" => {
            let exporter = graph::export::GraphMlExporter::new();
            exporter.generate_graphml(&elements, &relationships)
        }
        "neo4j" => {
            let exporter = graph::export::Neo4jExporter::new();
            exporter.generate_cypher(&elements, &relationships)
        }
        _ => {
            return Err(
                format!("Unknown format '{}'. Supported: json, dot, mermaid, html, svg, graphml, neo4j", format).into(),
            )
        }
    };

    std::fs::write(output, &content)?;
    println!(
        "Exported {} nodes and {} edges to {} (format: {})",
        elements.len(),
        relationships.len(),
        output,
        format
    );
    Ok(())
}

fn export_json(
    elements: &[db::models::CodeElement],
    relationships: &[db::models::Relationship],
) -> Result<String, Box<dyn std::error::Error>> {
    let timestamp = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    let export = serde_json::json!({
        "metadata": {
            "generator": "leankg",
            "version": env!("CARGO_PKG_VERSION"),
            "exported_at_unix": timestamp,
            "node_count": elements.len(),
            "edge_count": relationships.len(),
        },
        "nodes": elements.iter().map(|e| serde_json::json!({
            "id": e.qualified_name,
            "type": e.element_type,
            "name": e.name,
            "file": e.file_path,
            "lines": [e.line_start, e.line_end],
            "language": e.language,
        })).collect::<Vec<_>>(),
        "edges": relationships.iter().map(|r| serde_json::json!({
            "source": r.source_qualified,
            "target": r.target_qualified,
            "type": r.rel_type,
            "confidence": r.confidence,
        })).collect::<Vec<_>>(),
    });
    Ok(serde_json::to_string_pretty(&export)?)
}

fn export_dot(
    elements: &[db::models::CodeElement],
    relationships: &[db::models::Relationship],
) -> String {
    let sanitize_id = |s: &str| -> String {
        s.replace("::", "__")
            .replace('/', "_")
            .replace('.', "_")
            .replace('-', "_")
            .replace(' ', "_")
    };

    let mut dot = String::from("digraph LeanKG {\n  rankdir=LR;\n  node [shape=box, style=rounded, fontname=\"Helvetica\"];\n  edge [fontname=\"Helvetica\", fontsize=10];\n\n");

    // Group nodes by file into subgraphs
    let mut files: std::collections::HashMap<&str, Vec<&db::models::CodeElement>> =
        std::collections::HashMap::new();
    for e in elements {
        files.entry(&e.file_path).or_default().push(e);
    }

    let mut sorted_files: Vec<_> = files.into_iter().collect();
    sorted_files.sort_by_key(|(k, _)| *k);

    for (file, elems) in &sorted_files {
        dot.push_str(&format!(
            "  subgraph cluster_{} {{\n    label=\"{}\";\n    style=dashed;\n    color=gray;\n",
            sanitize_id(file),
            file
        ));
        for e in elems {
            dot.push_str(&format!(
                "    {} [label=\"{} ({})\"];\n",
                sanitize_id(&e.qualified_name),
                e.name,
                e.element_type
            ));
        }
        dot.push_str("  }\n\n");
    }

    for r in relationships {
        dot.push_str(&format!(
            "  {} -> {} [label=\"{}\"];\n",
            sanitize_id(&r.source_qualified),
            sanitize_id(&r.target_qualified),
            r.rel_type
        ));
    }
    dot.push_str("}\n");
    dot
}

fn export_mermaid(relationships: &[db::models::Relationship]) -> String {
    let sanitize_id = |s: &str| -> String {
        s.replace("::", "__")
            .replace('/', "_")
            .replace('.', "_")
            .replace('-', "_")
            .replace(' ', "_")
    };

    let mut mermaid = String::from("graph LR\n");
    for r in relationships {
        let source_short = r.source_qualified.split("::").last().unwrap_or(&r.source_qualified);
        let target_short = r.target_qualified.split("::").last().unwrap_or(&r.target_qualified);
        mermaid.push_str(&format!(
            "    {}[\"{}\"] -->|{}| {}[\"{}\"]\n",
            sanitize_id(&r.source_qualified),
            source_short,
            r.rel_type,
            sanitize_id(&r.target_qualified),
            target_short,
        ));
    }
    mermaid
}

fn run_shell_command(command: &[String], compress: bool) -> Result<(), Box<dyn std::error::Error>> {
    if command.is_empty() {
        eprintln!("No command provided. Usage: leankg run -- <command>");
        return Ok(());
    }

    let program = &command[0];
    let args: Vec<&str> = command[1..].iter().map(|s| s.as_str()).collect();

    let runner = cli::shell_runner::ShellRunner::new(compress);

    match runner.run(program, &args, &command.join(" ")) {
        Ok(output) => {
            println!("{}", output);
        }
        Err(e) => {
            eprintln!("{}", e);
            std::process::exit(1);
        }
    }

    Ok(())
}

fn generate_wiki(output_path: &str, db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !db_path.exists() {
        return Err("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.".into());
    }

    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let output = std::path::PathBuf::from(output_path);

    println!("Generating wiki to {}...", output_path);

    let generator = doc::WikiGenerator::new(&graph_engine, output);
    let stats = generator.generate()?;

    println!("Wiki generated successfully!");
    println!("  Pages: {}", stats.pages_generated);
    println!("  Elements documented: {}", stats.elements_documented);
    println!("  Mermaid diagrams: {}", stats.mermaid_diagrams);

    Ok(())
}

fn install_hooks(project_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !project_path.join(".git").exists() {
        return Err("Not a git repository. Run 'leankg hooks install' from a git repository.".into());
    }

    let hooks = hooks::GitHooks::new(project_path.to_path_buf());
    
    println!("Installing LeanKG git hooks...");
    
    match hooks.install_pre_commit() {
        Ok(_) => {}
        Err(hooks::HookError::AlreadyInstalled(msg)) => {
            println!("  Pre-commit: {}", msg);
        }
        Err(e) => {
            eprintln!("  Pre-commit error: {}", e);
        }
    }
    
    match hooks.install_post_commit() {
        Ok(_) => {}
        Err(hooks::HookError::AlreadyInstalled(msg)) => {
            println!("  Post-commit: {}", msg);
        }
        Err(e) => {
            eprintln!("  Post-commit error: {}", e);
        }
    }
    
    match hooks.install_post_checkout() {
        Ok(_) => {}
        Err(hooks::HookError::AlreadyInstalled(msg)) => {
            println!("  Post-checkout: {}", msg);
        }
        Err(e) => {
            eprintln!("  Post-checkout error: {}", e);
        }
    }
    
    println!("\nLeanKG hooks installed successfully!");
    println!("Hooks will:");
    println!("  - Run leankg detect-changes on pre-commit");
    println!("  - Run leankg index --incremental on post-commit");
    println!("  - Run leankg index --incremental on post-checkout");
    
    Ok(())
}

fn uninstall_hooks(project_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !project_path.join(".git").exists() {
        return Err("Not a git repository. Run 'leankg hooks uninstall' from a git repository.".into());
    }

    let hooks = hooks::GitHooks::new(project_path.to_path_buf());
    hooks.uninstall_hooks()?;
    
    println!("LeanKG hooks uninstalled successfully!");
    
    Ok(())
}

fn check_hooks_status(project_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !project_path.join(".git").exists() {
        return Err("Not a git repository. Run 'leankg hooks status' from a git repository.".into());
    }

    let hooks = hooks::GitHooks::new(project_path.to_path_buf());
    let status = hooks.check_hooks_status()?;
    
    println!("LeanKG Git Hooks Status:");
    println!();
    println!("  Pre-commit:    {}", if status.pre_commit_installed { "Installed" } else { "Not installed" });
    println!("  Post-commit:   {}", if status.post_commit_installed { "Installed" } else { "Not installed" });
    println!("  Post-checkout: {}", if status.post_checkout_installed { "Installed" } else { "Not installed" });
    println!();
    
    if status.pre_commit_backup_exists || status.post_commit_backup_exists || status.post_checkout_backup_exists {
        println!("  Backups exist for restored hooks:");
        if status.pre_commit_backup_exists { println!("    - pre-commit.leankg.backup"); }
        if status.post_commit_backup_exists { println!("    - post-commit.leankg.backup"); }
        if status.post_checkout_backup_exists { println!("    - post-checkout.leankg.backup"); }
    }
    
    Ok(())
}

async fn watch_git_events(project_path: &std::path::Path, db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    if !db_path.exists() {
        eprintln!("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.");
        return Ok(());
    }

    let watcher = hooks::GitWatcher::new(
        project_path.to_path_buf(),
        db_path.to_path_buf(),
    );

    let status = watcher.check_index_status()?;
    
    println!("LeanKG Git Watcher");
    println!("==================");
    println!("  Project: {}", project_path.display());
    println!("  DB:     {}", db_path.display());
    println!("  Current commit: {}", &status.current_commit[..8]);
    println!("  Last indexed:   {}", status.last_indexed_commit.as_ref().map(|c| &c[..8]).unwrap_or("never"));
    println!("  Index status:   {}", if status.is_stale { "STALE - needs sync" } else { "Up to date" });
    
    if status.is_stale && !status.affected_files.is_empty() {
        println!("\n  Changed files since last index:");
        for file in status.affected_files.iter().take(10) {
            println!("    - {}", file.display());
        }
        if status.affected_files.len() > 10 {
            println!("    ... and {} more", status.affected_files.len() - 10);
        }
    }
    
    println!("\n  Press Ctrl+C to stop watching.");
    println!("  Watching for git branch changes...\n");

    let (tx, mut rx) = tokio::sync::mpsc::channel::<String>(10);
    
    let project_path_clone = project_path.to_path_buf();
    let tx_clone = tx.clone();
    
    std::thread::spawn(move || {
        loop {
            let output = std::process::Command::new("git")
                .args(["rev-parse", "--abbrev-ref", "HEAD"])
                .current_dir(&project_path_clone)
                .output();
            
            if let Ok(output) = output {
                if output.status.success() {
                    let branch = String::from_utf8_lossy(&output.stdout).trim().to_string();
                    let _ = tx_clone.blocking_send(branch);
                }
            }
            
            std::thread::sleep(std::time::Duration::from_secs(2));
        }
    });

    let mut last_branch = String::new();
    loop {
        tokio::select! {
            biased;
            
            _ = tokio::time::sleep(std::time::Duration::from_secs(60)) => {
                let new_watcher = hooks::GitWatcher::new(
                    project_path.to_path_buf(),
                    db_path.to_path_buf(),
                );
                if let Ok(status) = new_watcher.check_index_status() {
                    if status.is_stale {
                        println!("\n[LeanKG] Index is stale, syncing...");
                        if let Err(e) = new_watcher.run_incremental_index() {
                            eprintln!("  Sync failed: {}", e);
                        } else {
                            println!("  Sync complete!");
                        }
                    }
                }
            }
            
            branch = rx.recv() => {
                if let Some(branch) = branch {
                    if branch != last_branch {
                        println!("\n[LeanKG] Branch changed: {}", branch);
                        last_branch = branch.clone();
                        
                        let sync_watcher = hooks::GitWatcher::new(
                            project_path.to_path_buf(),
                            db_path.to_path_buf(),
                        );
                        if let Err(e) = sync_watcher.sync_on_branch_change(&branch) {
                            eprintln!("  Sync failed: {}", e);
                        }
                    }
                }
            }
        }
    }
}

async fn update_leankg() -> Result<(), Box<dyn std::error::Error>> {
    println!("Checking for updates...");

    let installed = get_installed_version()?;
    let latest = get_latest_version().await?;

    println!("Current: {}", installed);
    println!("Latest:  {}", latest);

    if installed == latest {
        println!("\nYou already have the latest version ({}).", latest);
        return Ok(());
    }

    println!("\nUpdating LeanKG...");

    let platform = detect_platform();
    let url = get_download_url(&platform, &latest);

    println!("Downloading from {}...", url);

    let tmp_dir = tempfile::tempdir()?;
    let tar_path = tmp_dir.path().join("binary.tar.gz");

    download_file(&url, &tar_path).await?;

    extract_and_install(&tar_path).await?;

    println!("\nSuccessfully updated to v{}", latest);
    println!("Run 'leankg --version' to verify.");

    Ok(())
}

fn get_installed_version() -> Result<String, Box<dyn std::error::Error>> {
    let output = std::process::Command::new("leankg")
        .arg("--version")
        .output()?;

    if !output.status.success() {
        return Ok("not installed".to_string());
    }

    let stdout = String::from_utf8_lossy(&output.stdout);
    let re = regex::Regex::new(r"(\d+\.\d+\.\d+)")?;
    if let Some(caps) = re.captures(&stdout) {
        Ok(caps.get(1).unwrap().as_str().to_string())
    } else {
        Ok("unknown".to_string())
    }
}

async fn get_latest_version() -> Result<String, Box<dyn std::error::Error>> {
    let client = reqwest::Client::new();
    let resp = client
        .get("https://api.github.com/repos/FreePeak/LeanKG/releases/latest")
        .header("User-Agent", "LeanKG")
        .header("Accept", "application/json")
        .send()
        .await?;

    if !resp.status().is_success() {
        return Err(format!("GitHub API returned status: {}", resp.status()).into());
    }

    let json: serde_json::Value = resp.json().await?;

    let tag = json["tag_name"]
        .as_str()
        .ok_or("Failed to parse tag_name")?
        .trim_start_matches('v')
        .to_string();

    Ok(tag)
}

fn detect_platform() -> String {
    let os = std::process::Command::new("uname")
        .arg("-s")
        .output()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();

    let arch = std::process::Command::new("uname")
        .arg("-m")
        .output()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();

    let platform = match os.as_str() {
        "Darwin" => "macos",
        "Linux" => "linux",
        _ => {
            eprintln!("Unsupported platform: {}", os);
            std::process::exit(1);
        }
    };

    let arch = match arch.as_str() {
        "x86_64" => "x64",
        "arm64" | "aarch64" => "arm64",
        _ => {
            eprintln!("Unsupported architecture: {}", arch);
            std::process::exit(1);
        }
    };

    format!("{}-{}", platform, arch)
}

fn get_download_url(platform: &str, version: &str) -> String {
    format!(
        "https://github.com/FreePeak/LeanKG/releases/download/v{}/leankg-{}.tar.gz",
        version, platform
    )
}

async fn download_file(url: &str, dest: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let resp = reqwest::get(url).await?;
    let bytes = resp.bytes().await?;

    std::fs::write(dest, bytes)?;
    Ok(())
}

async fn extract_and_install(tar_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let tmp_dir = tempfile::tempdir()?;
    let extract_dir = tmp_dir.path();

    let tar_gz = std::fs::File::open(tar_path)?;
    let mut ar = tar::Archive::new(flate2::read::GzDecoder::new(tar_gz));
    ar.unpack(extract_dir)?;

    let install_dir = std::path::PathBuf::from(std::env::var("HOME").unwrap_or_default()).join(".local/bin");
    std::fs::create_dir_all(&install_dir)?;

    let entries: Vec<_> = std::fs::read_dir(extract_dir)?
        .filter_map(|e| e.ok())
        .collect();

    for entry in entries {
        let path = entry.path();
        if path.is_file() && path.file_name().map(|n| n == "leankg").unwrap_or(false) {
            let dest = install_dir.join("leankg");
            std::fs::copy(&path, &dest)?;

            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                let mut perms = std::fs::metadata(&path)?.permissions();
                perms.set_mode(0o755);
                std::fs::set_permissions(&dest, perms)?;
            }

            break;
        }
    }

    Ok(())
}
