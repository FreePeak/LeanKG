#![allow(dead_code)]
mod api;
mod benchmark;
mod cli;
mod config;
mod db;
mod doc;
mod doc_indexer;
mod graph;
mod indexer;
mod mcp;
mod registry;
mod watcher;
mod web;

use clap::Parser;

#[derive(Parser, Debug)]
#[command(name = "leankg")]
#[command(version)]
#[command(about = "Lightweight knowledge graph for AI-assisted development")]
pub struct Args {
    #[command(subcommand)]
    pub command: cli::CLICommand,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = Args::parse();

    if !matches!(args.command, cli::CLICommand::McpStdio { watch: _ }) {
        tracing_subscriber::fmt::init();
    }

    match args.command {
        cli::CLICommand::Version => {
            println!("leankg {}", env!("CARGO_PKG_VERSION"));
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
            tokio::fs::create_dir_all(&db_path).await?;
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
            tokio::fs::create_dir_all(&db_path).await.ok();
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
            tokio::fs::create_dir_all(&db_path).await.ok();
            web::start_server(port, db_path).await?;
        }
        cli::CLICommand::McpStdio { watch } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");

            tokio::fs::create_dir_all(&db_path).await.ok();

            let mcp_server = if watch {
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
            tokio::fs::create_dir_all(&db_path).await.ok();
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
    }

    Ok(())
}

fn find_project_root() -> Result<std::path::PathBuf, Box<dyn std::error::Error>> {
    let current_dir = std::env::current_dir()?;
    if current_dir.join(".leankg").exists() || current_dir.join("leankg.yaml").exists() {
        return Ok(current_dir);
    }
    for parent in current_dir.ancestors() {
        if parent.join(".leankg").exists() || parent.join("leankg.yaml").exists() {
            return Ok(parent.to_path_buf());
        }
    }
    Ok(current_dir)
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

    let mcp_config = serde_json::json!({
        "mcpServers": {
            "leankg": {
                "command": exe_path.to_string_lossy().as_ref(),
                "args": ["mcp-stdio", "--watch"]
            }
        }
    });

    std::fs::write(".mcp.json", serde_json::to_string_pretty(&mcp_config)?)?;
    println!("Installed MCP config to .mcp.json");

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
        _ => {
            return Err(
                format!("Unknown format '{}'. Supported: json, dot, mermaid", format).into(),
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
