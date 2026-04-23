#![allow(dead_code)]
mod api;
mod benchmark;
mod cli;
mod compress;
mod config;
mod db;
mod doc;
mod doc_indexer;
mod embed;
mod graph;
mod indexer;
mod mcp;
mod obsidian;
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

    if !matches!(args.command, cli::CLICommand::McpStdio { watch: _ }) {
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

            println!("╔═══════════════════════════════════════════════════════════════╗");
            println!("║  LeanKG Web UI (Embedded)                                   ║");
            println!("╚═══════════════════════════════════════════════════════════════╝");
            println!();
            println!("🚀 Starting server on http://localhost:{}", port);
            println!();
            web::start_server(port, db_path, None).await?;
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

            println!("╔═══════════════════════════════════════════════════════════════╗");
            println!("║  LeanKG Web UI (Embedded)                                   ║");
            println!("╚═══════════════════════════════════════════════════════════════╝");
            println!();
            println!("🚀 Starting server on http://localhost:{}", port);
            println!();
            web::start_server(port, db_path, None).await?;
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
                _ => benchmark::CliTool::Kilo,
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
            install_claude_hooks()?;
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
        cli::CLICommand::Obsidian { command } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");

            match command {
                cli::ObsidianCommand::Init { vault } => {
                    obsidian_init(&db_path, vault.as_deref())?;
                }
                cli::ObsidianCommand::Push { vault } => {
                    obsidian_push(&db_path, vault.as_deref()).await?;
                }
                cli::ObsidianCommand::Pull { vault } => {
                    obsidian_pull(&db_path, vault.as_deref()).await?;
                }
                cli::ObsidianCommand::Watch { vault, debounce_ms } => {
                    obsidian_watch(&db_path, vault.as_deref(), debounce_ms).await?;
                }
                cli::ObsidianCommand::Status { vault } => {
                    obsidian_status(&db_path, vault.as_deref()).await?;
                }
            }
        }
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
        cli::CLICommand::Proc { command } => match command {
            cli::ProcCommand::Status => {
                proc_status()?;
            }
            cli::ProcCommand::Kill => {
                proc_kill()?;
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
    let project_name = std::path::Path::new(path)
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("my-project")
        .to_string();

    let mut config = config::ProjectConfig::default();
    config.project.name = project_name;

    // Store absolute path to project root for MCP server routing
    let current_dir = std::env::current_dir()?;
    config.project.project_path = Some(current_dir);

    let detected_root = detect_project_root(".");
    config.project.root = std::path::PathBuf::from(&detected_root);

    let mut detected_langs = Vec::new();
    let abs_root = std::path::Path::new(&detected_root);
    if abs_root.exists() {
        detect_languages(&detected_root, &mut detected_langs);
    } else {
        let cwd = std::env::current_dir().unwrap_or_default();
        eprintln!(
            "Warning: detected root '{}' not found (cwd: {})",
            detected_root,
            cwd.display()
        );
    }
    if !detected_langs.is_empty() {
        config.project.languages = detected_langs;
    }

    let config_yaml = serde_yaml::to_string(&config)?;

    std::fs::create_dir_all(path)?;
    let leankg_dir_config = std::path::Path::new(path).join("leankg.yaml");
    std::fs::write(&leankg_dir_config, &config_yaml)?;

    let cwd_config = std::path::Path::new("leankg.yaml");
    if cwd_config.exists() {
        if let Ok(existing) = std::fs::read_to_string(cwd_config) {
            if existing != config_yaml {
                std::fs::write(cwd_config, &config_yaml)?;
            }
        }
    } else {
        std::fs::write(cwd_config, &config_yaml)?;
    }

    println!("Initialized LeanKG project at {}", path);
    if detected_root != "./src" {
        println!("  Auto-detected source root: {}", detected_root);
    }
    if !config.project.languages.is_empty() {
        println!(
            "  Detected languages: {}",
            config.project.languages.join(", ")
        );
    }
    Ok(())
}

fn detect_project_root(base: &str) -> String {
    let candidates = [
        ("./src", "standard src/"),
        ("./app/src", "Android app/src/"),
        ("./app", "Android app/"),
        ("./lib", "library lib/"),
        ("./packages", "monorepo packages/"),
    ];

    for (dir, label) in candidates {
        let full = std::path::Path::new(base).join(dir.strip_prefix("./").unwrap_or(dir));
        if full.exists() && full.is_dir() && has_code_files(&full) {
            println!("  Detected project type: {}", label);
            return dir.to_string();
        }
    }

    ".".to_string()
}

fn has_code_files(dir: &std::path::Path) -> bool {
    if let Ok(rd) = std::fs::read_dir(dir) {
        for entry in rd.flatten() {
            let name = entry.file_name();
            let name_str = name.to_string_lossy();
            let ext = std::path::Path::new(name_str.as_ref())
                .extension()
                .and_then(|e| e.to_str())
                .unwrap_or("");
            if [
                "go", "ts", "js", "py", "rs", "java", "kt", "kts", "tf", "xml",
            ]
            .contains(&ext)
            {
                return true;
            }
            if entry.path().is_dir()
                && !name_str.starts_with('.')
                && !["node_modules", "vendor", "build", ".gradle", "target"]
                    .contains(&name_str.as_ref())
                && has_code_files(&entry.path())
            {
                return true;
            }
        }
    }
    false
}

fn detect_languages(root: &str, languages: &mut Vec<String>) {
    let root_path = std::path::Path::new(root);
    let ext_lang = [
        (".go", "go"),
        (".ts", "typescript"),
        (".js", "javascript"),
        (".py", "python"),
        (".rs", "rust"),
        (".java", "java"),
        (".kt", "kotlin"),
        (".kts", "kotlin"),
    ];

    for (ext, lang) in ext_lang {
        if has_extension_recursive(root_path, ext, 6) && !languages.contains(&lang.to_string()) {
            languages.push(lang.to_string());
        }
    }
}

fn has_extension_recursive(dir: &std::path::Path, ext: &str, max_depth: u32) -> bool {
    if max_depth == 0 {
        return false;
    }
    if let Ok(rd) = std::fs::read_dir(dir) {
        for entry in rd.flatten() {
            let path = entry.path();
            if path.extension().and_then(|e| e.to_str()) == Some(ext.trim_start_matches('.')) {
                return true;
            }
            if path.is_dir()
                && !path
                    .file_name()
                    .map(|n| n.to_string_lossy().starts_with('.'))
                    .unwrap_or(false)
                && !["node_modules", "vendor", "build", ".gradle", "target"]
                    .iter()
                    .any(|skip| path.file_name().map(|n| n == *skip).unwrap_or(false))
                && has_extension_recursive(&path, ext, max_depth - 1)
            {
                return true;
            }
        }
    }
    false
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

    let config_path = db_path
        .parent()
        .unwrap_or(std::path::Path::new("."))
        .join("leankg.yaml");
    let config = if config_path.exists() {
        let content = std::fs::read_to_string(&config_path)?;
        serde_yaml::from_str::<config::ProjectConfig>(&content).unwrap_or_default()
    } else {
        config::ProjectConfig::default()
    };

    let index_path = if path == "." {
        config.project.root.to_string_lossy().to_string()
    } else {
        path.to_string()
    };

    let final_exclude: Vec<String> = if exclude_patterns.is_empty() {
        config.indexer.exclude.clone()
    } else {
        exclude_patterns.to_vec()
    };

    println!("Indexing codebase at {}...", index_path);

    let mut files = indexer::find_files_sync(&index_path)?;

    if let Some(lang) = lang_filter {
        let allowed_langs: Vec<&str> = lang.split(',').map(|s| s.trim()).collect();
        files.retain(|f| {
            if let Some(ext) = std::path::Path::new(f).extension() {
                let ext_str = ext.to_string_lossy().to_lowercase();
                return allowed_langs.iter().any(|l| l.to_lowercase() == ext_str);
            }
            false
        });
        if verbose {
            println!("Language filter applied: {} allowed", allowed_langs.len());
        }
    }

    if !final_exclude.is_empty() {
        let prev_len = files.len();
        let normalized_excludes: Vec<String> = final_exclude
            .iter()
            .map(|pat| {
                pat.replace("**/", "/")
                    .replace("/**", "/")
                    .replace('*', "")
                    .trim_matches('/')
                    .to_string()
            })
            .filter(|p| !p.is_empty())
            .collect();
        files.retain(|f| {
            let path_lower = f.to_ascii_lowercase();
            !normalized_excludes
                .iter()
                .any(|pat| path_lower.contains(pat))
        });
        if verbose {
            println!(
                "Excluded {} files (matched {} exclude patterns)",
                prev_len - files.len(),
                normalized_excludes.len()
            );
        }
    }

    println!("Found {} files to index", files.len());

    let total_elements = indexer::index_files_parallel(&graph_engine, &files, verbose)?;
    println!(
        "Indexed {} files ({} elements)",
        files.len(),
        total_elements
    );

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
    sorted_clusters.sort_by_key(|b| std::cmp::Reverse(b.members.len()));

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

fn obsidian_init(
    db_path: &std::path::Path,
    vault: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let vault_path = obsidian::vault_path(db_path, vault);

    let engine =
        obsidian::SyncEngine::new(vault_path.to_str().unwrap_or(""), db_path.to_path_buf());

    engine.init()?;

    let readme_content = r#"# LeanKG Obsidian Vault

This vault is managed by LeanKG. Notes in `.leankg/obsidian/vault/` are auto-generated from LeanKG's knowledge graph.

## Sync Commands

- `leankg obsidian push` - Generate notes from LeanKG database
- `leankg obsidian pull` - Import annotation edits back to LeanKG
- `leankg obsidian watch` - Watch for changes and auto-sync

## Frontmatter Fields

- `leankg_id` - Unique identifier for the code element
- `leankg_type` - Element type (function, file, class, etc.)
- `leankg_file` - Source file path
- `leankg_line` - Line range in source file
- `leankg_relationships` - List of related elements
- `leankg_annotation` - Editable annotation description

## Notes

- LeanKG is the source of truth
- `push` overwrites `leankg_*` frontmatter fields
- `pull` imports only `leankg_annotation` back to LeanKG
- Your custom notes in note bodies are never overwritten
"#;

    let readme_path = vault_path.join("README.md");
    std::fs::write(&readme_path, readme_content)?;

    println!("Obsidian vault initialized at:");
    println!("  {}", vault_path.display());
    println!();
    println!("Next steps:");
    println!("  leankg obsidian push    # Generate notes from LeanKG");
    println!("  leankg obsidian status  # Check vault status");

    Ok(())
}

async fn obsidian_push(
    db_path: &std::path::Path,
    vault: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let vault_path = obsidian::vault_path(db_path, vault);

    if !vault_path.exists() {
        eprintln!("Vault not initialized. Run 'leankg obsidian init' first.");
        return Ok(());
    }

    println!("Pushing LeanKG data to Obsidian vault...");

    let engine =
        obsidian::SyncEngine::new(vault_path.to_str().unwrap_or(""), db_path.to_path_buf());
    let result = engine.push().await?;

    println!();
    println!("Push complete:");
    println!("  Notes generated: {}", result.pushed);
    println!("  Annotations pulled: {}", result.pulled);
    println!("  Conflicts: {}", result.conflicts);

    Ok(())
}

async fn obsidian_pull(
    db_path: &std::path::Path,
    vault: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let vault_path = obsidian::vault_path(db_path, vault);

    if !vault_path.exists() {
        eprintln!("Vault not initialized. Run 'leankg obsidian init' first.");
        return Ok(());
    }

    println!("Pulling annotations from Obsidian vault...");

    let engine =
        obsidian::SyncEngine::new(vault_path.to_str().unwrap_or(""), db_path.to_path_buf());
    let result = engine.pull().await?;

    println!();
    println!("Pull complete:");
    println!("  Notes pushed: {}", result.pushed);
    println!("  Annotations imported: {}", result.pulled);
    println!("  Conflicts: {}", result.conflicts);

    if result.conflicts > 0 {
        println!();
        println!("Conflicts detected (manual merge required):");
        println!("  Run 'leankg obsidian pull' after resolving conflicts.");
    }

    Ok(())
}

async fn obsidian_watch(
    db_path: &std::path::Path,
    vault: Option<&str>,
    debounce_ms: u64,
) -> Result<(), Box<dyn std::error::Error>> {
    let vault_path = obsidian::vault_path(db_path, vault);

    if !vault_path.exists() {
        eprintln!("Vault not initialized. Run 'leankg obsidian init' first.");
        return Ok(());
    }

    println!("╔═══════════════════════════════════════════╗");
    println!("║  LeanKG Obsidian Watcher                ║");
    println!("╚═══════════════════════════════════════════╝");
    println!("  Vault: {}", vault_path.display());
    println!("  Debounce: {}ms", debounce_ms);
    println!("  Press Ctrl+C to stop.");
    println!();

    let engine = std::sync::Arc::new(obsidian::SyncEngine::new(
        vault_path.to_str().unwrap_or(""),
        db_path.to_path_buf(),
    ));
    let watcher = obsidian::ObsidianWatcher::new(engine.clone(), debounce_ms);

    watcher.watch(vault_path.to_str().unwrap_or("")).await?;

    Ok(())
}

async fn obsidian_status(
    db_path: &std::path::Path,
    vault: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let vault_path = obsidian::vault_path(db_path, vault);

    println!("LeanKG Obsidian Vault Status");
    println!("============================");
    println!();
    println!("  Vault: {}", vault_path.display());
    println!("  Exists: {}", vault_path.exists());

    if vault_path.exists() {
        let note_count = walkdir_count(&vault_path);
        println!("  Notes: {}", note_count);
    } else {
        println!();
        println!("  Run 'leankg obsidian init' to initialize.");
    }

    Ok(())
}

fn walkdir_count(path: &std::path::Path) -> usize {
    let mut count = 0;
    if let Ok(entries) = std::fs::read_dir(path) {
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                count += walkdir_count(&path);
            } else if path.extension().map(|e| e == "md").unwrap_or(false) {
                count += 1;
            }
        }
    }
    count
}

#[allow(clippy::too_many_arguments)]
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
        println!(
            "Cleaned up {} old metric record(s) (retention: {} days).",
            count, ret_days
        );
        return Ok(());
    }

    let ret_days = if let Some(s) = since {
        if let Some(days) = s.strip_suffix('d') {
            days.parse().unwrap_or(30)
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
    println!(
        "Total Savings: {} tokens across {} calls",
        summary.total_tokens_saved, summary.total_invocations
    );
    println!(
        "Average Savings: {:.1}% (positive only)",
        summary.average_savings_percent
    );
    println!(
        "Average Correctness: {:.1}%",
        summary.average_correctness_percent
    );
    println!("Retention: {} days", summary.retention_days);

    if !summary.by_tool.is_empty() {
        println!("\nBy Tool:");
        for tm in &summary.by_tool {
            println!(
                "  {}: {} calls, {:.0}% save, {:.1}% correct",
                tm.tool_name, tm.calls, tm.avg_savings_percent, tm.avg_correctness_percent
            );
        }
    }

    if !summary.by_day.is_empty() {
        println!("\nBy Day:");
        for dm in &summary.by_day {
            println!(
                "  {}:  {} calls, {:.1}% correct",
                dm.date, dm.calls, dm.correctness
            );
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
        (
            "seed1",
            "search_code",
            now - 100,
            150i32,
            45i32,
            12i32,
            25i32,
            12000i32,
            5000i32,
            11955i32,
            99.6f64,
            true,
        ),
        (
            "seed2",
            "get_context",
            now - 90,
            200i32,
            35i32,
            8i32,
            18i32,
            8000i32,
            3200i32,
            7965i32,
            99.6f64,
            true,
        ),
        (
            "seed3",
            "find_function",
            now - 80,
            80i32,
            28i32,
            5i32,
            12i32,
            6000i32,
            2400i32,
            5972i32,
            99.5f64,
            true,
        ),
        (
            "seed4",
            "search_code",
            now - 70,
            120i32,
            52i32,
            15i32,
            30i32,
            14000i32,
            5800i32,
            13948i32,
            99.6f64,
            true,
        ),
        (
            "seed5",
            "get_impact_radius",
            now - 60,
            300i32,
            180i32,
            25i32,
            45i32,
            25000i32,
            10000i32,
            24820i32,
            99.3f64,
            true,
        ),
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

fn proc_status() -> Result<(), Box<dyn std::error::Error>> {
    use sysinfo::System;

    let mut sys = System::new_all();
    sys.refresh_all();

    let processes: Vec<_> = sys
        .processes()
        .iter()
        .filter(|(_pid, process)| {
            let cmd: String = process
                .cmd()
                .iter()
                .map(|s| s.to_string_lossy().into_owned())
                .collect::<Vec<_>>()
                .join(" ");
            cmd.contains("leankg") || cmd.contains("vite")
        })
        .collect();

    if processes.is_empty() {
        println!("No leankg or vite processes running");
        return Ok(());
    }

    println!("LeanKG Processes:");
    println!("==================");
    for (pid, process) in processes {
        let cmd: String = process
            .cmd()
            .iter()
            .map(|s| s.to_string_lossy().into_owned())
            .collect::<Vec<_>>()
            .join(" ");
        let cpu = process.cpu_usage();
        let mem_mb = process.memory() / 1_048_576; // Convert to MB
        let mem_pct = (mem_mb as f32 / (sys.total_memory() / 1_048_576) as f32) * 100.0;

        println!(
            "PID: {} | CPU: {:.1}% | MEM: {:.1}% | RSS: {}MB | Command: {}",
            pid, cpu, mem_pct, mem_mb, cmd
        );
    }

    Ok(())
}

fn proc_kill() -> Result<(), Box<dyn std::error::Error>> {
    let patterns = ["leankg", "vite"];
    let mut killed_any = false;

    for pattern in &patterns {
        let output = std::process::Command::new("pkill")
            .args(["-9", "-f", pattern])
            .output();

        match output {
            Ok(out) if out.status.success() => {
                killed_any = true;
            }
            Ok(_) => {
                // pkill returns non-zero when no processes matched
            }
            Err(e) => {
                eprintln!("Warning: pkill not available or failed: {}", e);
            }
        }
    }

    if killed_any {
        println!("Killed all leankg and vite processes");
    } else {
        println!("No leankg or vite processes found to kill");
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

#[allow(clippy::collapsible_str_replace)]
#[allow(clippy::used_underscore_binding)]
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

#[allow(clippy::collapsible_str_replace)]
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
        let source_short = r
            .source_qualified
            .split("::")
            .last()
            .unwrap_or(&r.source_qualified);
        let target_short = r
            .target_qualified
            .split("::")
            .last()
            .unwrap_or(&r.target_qualified);
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

fn find_ui_dist_path() -> Option<std::path::PathBuf> {
    // 1. Check LEANKG_UI_DIST environment variable first
    if let Ok(env_path) = std::env::var("LEANKG_UI_DIST") {
        let path = std::path::Path::new(&env_path);
        if path.join("index.html").exists() {
            println!("📦 Using UI from LEANKG_UI_DIST: {}", env_path);
            return Some(path.to_path_buf());
        }
    }

    // 2. Check ui/dist relative to current working directory
    let cwd_ui = std::path::Path::new("ui/dist");
    if cwd_ui.join("index.html").exists() {
        println!("📦 Using UI from current directory: {}", cwd_ui.display());
        return Some(cwd_ui.to_path_buf());
    }

    // 3. Check ui/dist relative to the executable's directory
    // This handles binary installations like /usr/local/bin/leankg
    if let Ok(exe_path) = std::env::current_exe() {
        if let Some(exe_dir) = exe_path.parent() {
            // Try ../share/leankg/ui/dist (common Linux installation)
            let share_ui = exe_dir.join("../share/leankg/ui/dist");
            if share_ui.join("index.html").exists() {
                let path = share_ui.canonicalize().ok().unwrap_or(share_ui);
                println!("📦 Using UI from share directory: {}", path.display());
                return Some(path);
            }
            // Try exe_dir/ui/dist (development and macOS brew)
            let exe_ui = exe_dir.join("ui/dist");
            if exe_ui.join("index.html").exists() {
                let path = exe_ui.canonicalize().ok().unwrap_or(exe_ui);
                println!("📦 Using UI from executable directory: {}", path.display());
                return Some(path);
            }
        }
    }

    None
}

async fn spawn_vite_dev_server(
    port: u16,
) -> Result<tokio::process::Child, Box<dyn std::error::Error>> {
    let ui_path = std::path::Path::new("ui");

    if !ui_path.exists() {
        return Err(format!(
            "UI directory not found at {}. Run 'cd ui && npm install' first.",
            ui_path.display()
        )
        .into());
    }

    let package_json = ui_path.join("package.json");
    if !package_json.exists() {
        return Err(format!(
            "package.json not found in {}. Run 'cd ui && npm install' first.",
            ui_path.display()
        )
        .into());
    }

    let vite_exe = which_vite().await?;

    println!("🚀 Starting Vite dev server on port {}...", port);

    let child = tokio::process::Command::new(&vite_exe)
        .args(["--port", &port.to_string()])
        .current_dir(ui_path)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .spawn()?;

    Ok(child)
}

async fn which_vite() -> Result<String, Box<dyn std::error::Error>> {
    let candidates = vec![
        "npx".to_string(),
        "npm".to_string(),
        "pnpm".to_string(),
        "bun".to_string(),
    ];

    let exe = which::which("npx")
        .map(|p| p.to_string_lossy().to_string())
        .ok();

    if let Some(ref exe_path) = exe {
        return Ok(format!("{} vite", exe_path));
    }

    for candidate in &candidates {
        if which::which(candidate).is_ok() {
            return Ok(candidate.to_string());
        }
    }

    Err("No Node.js package manager found (npx, npm, pnpm, or bun). Please install Node.js and npm.".into())
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

    println!("\nStopping any running LeanKG processes...");
    kill_old_processes()?;

    println!("\nUpdating LeanKG...");

    let platform = detect_platform();
    let url = get_download_url(&platform, &latest);

    println!("Downloading from {}...", url);

    let tmp_dir = tempfile::tempdir()?;
    let tar_path = tmp_dir.path().join("binary.tar.gz");

    download_file(&url, &tar_path).await?;

    extract_and_install(&tar_path).await?;

    println!("\nUpdating LeanKG hooks...");
    install_claude_hooks()?;

    println!("\nRemoving old LeanKG skill...");
    remove_old_skill()?;

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

    let bytes = resp.bytes().await?;
    let json: serde_json::Value = serde_json::from_slice(&bytes)?;

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

async fn download_file(
    url: &str,
    dest: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
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

    let install_dir =
        std::path::PathBuf::from(std::env::var("HOME").unwrap_or_default()).join(".local/bin");
    std::fs::create_dir_all(&install_dir)?;

    let dest = install_dir.join("leankg");

    // Remove existing binary first to avoid APFS metadata corruption issues on macOS
    // (overwriting in place can leave corrupted metadata, causing SIGKILL on exec)
    if dest.exists() {
        std::fs::remove_file(&dest)?;
    }

    let entries: Vec<_> = std::fs::read_dir(extract_dir)?
        .filter_map(|e| e.ok())
        .collect();

    for entry in entries {
        let path = entry.path();
        if path.is_file() && path.file_name().map(|n| n == "leankg").unwrap_or(false) {
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

fn kill_old_processes() -> Result<(), Box<dyn std::error::Error>> {
    use sysinfo::System;

    let patterns = ["leankg", "vite"];
    let max_retries = 3;

    for pattern in patterns {
        let mut retries = 0;
        loop {
            // Get all processes matching the pattern
            let matching_pids: Vec<u32> = {
                let mut sys = System::new_all();
                sys.refresh_all();
                sys.processes()
                    .iter()
                    .filter(|(_pid, process)| {
                        let cmd: String = process
                            .cmd()
                            .iter()
                            .map(|s| s.to_string_lossy().into_owned())
                            .collect::<Vec<_>>()
                            .join(" ");
                        cmd.contains(pattern)
                    })
                    .map(|(pid, _)| pid.as_u32())
                    .collect()
            };

            if matching_pids.is_empty() {
                if retries > 0 {
                    println!(
                        "  {} processes stopped (after {} retries)",
                        pattern, retries
                    );
                }
                break;
            }

            if retries >= max_retries {
                return Err(format!(
                    "Failed to stop {} processes after {} retries. PIDs: {:?}. Run 'leankg proc kill' manually.",
                    pattern, max_retries, matching_pids
                ).into());
            }

            if retries == 0 {
                println!(
                    "  Stopping {} processes (PID: {:?})...",
                    pattern, matching_pids
                );
            }

            // Kill each process directly
            for pid in &matching_pids {
                let kill_output = std::process::Command::new("kill")
                    .args(["-9", &pid.to_string()])
                    .output();

                if let Err(e) = kill_output {
                    eprintln!("    Failed to kill PID {}: {}", pid, e);
                }
            }

            // Wait for processes to terminate
            std::thread::sleep(std::time::Duration::from_millis(500));
            retries += 1;
        }
    }

    // Final verification - wait a bit and check no processes remain
    std::thread::sleep(std::time::Duration::from_millis(200));
    {
        let mut sys = System::new_all();
        sys.refresh_all();
        let remaining: Vec<_> = sys
            .processes()
            .iter()
            .filter(|(_pid, process)| {
                let cmd: String = process
                    .cmd()
                    .iter()
                    .map(|s| s.to_string_lossy().into_owned())
                    .collect::<Vec<_>>()
                    .join(" ");
                cmd.contains("leankg") || cmd.contains("vite")
            })
            .collect();

        if !remaining.is_empty() {
            let pids: Vec<u32> = remaining.iter().map(|(p, _)| p.as_u32()).collect();
            return Err(format!(
                "LeanKG/Vite processes still running after kill: {:?}. Run 'leankg proc kill' manually.",
                pids
            ).into());
        }
    }

    Ok(())
}

fn remove_old_skill() -> Result<(), Box<dyn std::error::Error>> {
    let skill_dir = std::path::PathBuf::from(std::env::var("HOME").unwrap_or_default())
        .join(".claude/skills/using-leankg");

    if skill_dir.exists() {
        std::fs::remove_dir_all(&skill_dir)?;
        println!("  Removed old LeanKG skill from {}", skill_dir.display());
    }

    Ok(())
}

fn install_claude_hooks() -> Result<(), Box<dyn std::error::Error>> {
    let plugin_dir = std::path::PathBuf::from(std::env::var("HOME").unwrap_or_default())
        .join(".claude/plugins/leankg");
    let hooks_dir = plugin_dir.join("hooks");

    std::fs::create_dir_all(&hooks_dir)?;

    // Write hooks.json
    let hooks_json = r#"{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "startup|clear|compact",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/sessionstart.mjs\""
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse.mjs\""
          }
        ]
      },
      {
        "matcher": "Grep",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse.mjs\""
          }
        ]
      },
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/pretooluse.mjs\""
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "mcp__leankg__",
        "hooks": [
          {
            "type": "command",
            "command": "node \"${CLAUDE_PLUGIN_ROOT}/hooks/posttooluse.mjs\""
          }
        ]
      }
    ]
  }
}"#;

    std::fs::write(hooks_dir.join("hooks.json"), hooks_json)?;

    // Write pretooluse.mjs
    let pretooluse_mjs = r#"#!/usr/bin/env node
/**
 * PreToolUse hook for LeanKG - Routing guidance for Claude Code
 * Shows nudges when users reach for native tools instead of LeanKG.
 */
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

const raw = await readStdin();
const input = JSON.parse(raw);
const tool = input.tool_name ?? "";
const toolInput = input.tool_input ?? {};

const GUIDANCE = {
  Read: `
<tool_routing>
Use LeanKG instead of Read for code analysis:
  - mcp__leankg__query_file(filename) - find files by name
  - mcp__leankg__get_context(file) - read with token optimization
</tool_routing>`,

  Grep: `
<tool_routing>
Use LeanKG instead of Grep for code search:
  - mcp__leankg__search_code(query, element_type) - search functions, files, structs
  - mcp__leankg__find_function(name) - locate function definitions
</tool_routing>`,

  Bash: `
<tool_routing>
Use LeanKG instead of Bash for dependency analysis:
  - mcp__leankg__get_impact_radius(file, depth) - blast radius analysis
  - mcp__leankg__get_dependencies(file) - what this file imports
  - mcp__leankg__get_dependents(file) - what depends on this file
</tool_routing>`,
};

function isCodeAnalysis(tool, toolInput) {
  if (tool === "Read") {
    const path = toolInput.file_path ?? toolInput.path ?? "";
    const codeExts = [".rs", ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".cpp", ".c", ".h", ".cs", ".rb"];
    return codeExts.some(ext => path.endsWith(ext));
  }
  if (tool === "Bash") {
    const cmd = toolInput.command ?? "";
    return /\b(grep|find|rg|ag|ack)\b/.test(cmd) || /\b(import|require|use|from)\b/.test(cmd);
  }
  return true;
}

if (GUIDANCE[tool] && isCodeAnalysis(tool, toolInput)) {
  const response = {
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      guidance: GUIDANCE[tool].trim(),
    },
  };
  process.stdout.write(JSON.stringify(response) + "\n");
}
"#;

    std::fs::write(hooks_dir.join("pretooluse.mjs"), pretooluse_mjs)?;

    // Write posttooluse.mjs
    let posttooluse_mjs = r#"#!/usr/bin/env node
/**
 * PostToolUse hook for LeanKG - Session continuity.
 * Captures LeanKG MCP tool calls for session continuity.
 */
import { appendFileSync, existsSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { homedir } from "node:os";

const LEANKG_TOOLS = [
  "mcp__leankg__orchestrate",
  "mcp__leankg__search_code",
  "mcp__leankg__find_function",
  "mcp__leankg__query_file",
  "mcp__leankg__get_impact_radius",
  "mcp__leankg__get_dependencies",
  "mcp__leankg__get_dependents",
  "mcp__leankg__get_context",
  "mcp__leankg__get_callers",
  "mcp__leankg__get_call_graph",
  "mcp__leankg__get_clusters",
  "mcp__leankg__get_doc_for_file",
  "mcp__leankg__get_traceability",
  "mcp__leankg__get_tested_by",
  "mcp__leankg__detect_changes",
  "mcp__leankg__mcp_status",
  "mcp__leankg__mcp_index",
];

const SESSION_LOG_DIR = join(homedir(), ".leankg", "sessions");
const SESSION_LOG_FILE = join(SESSION_LOG_DIR, "posttooluse.log");

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

try {
  const raw = await readStdin();
  const input = JSON.parse(raw);
  const toolName = input.tool_name ?? "";
  const toolInput = input.tool_input ?? {};

  const isLeankgTool = LEANKG_TOOLS.some(t => toolName.includes(t));

  if (isLeankgTool) {
    if (!existsSync(SESSION_LOG_DIR)) {
      mkdirSync(SESSION_LOG_DIR, { recursive: true });
    }
    const sessionId = process.env.CLAUDE_SESSION_ID || "unknown";
    const timestamp = new Date().toISOString();
    const logEntry = JSON.stringify({
      timestamp,
      sessionId,
      tool: toolName,
      input: toolInput,
    }) + "\n";
    appendFileSync(SESSION_LOG_FILE, logEntry);
  }
} catch { /* silent */ }
"#;

    std::fs::write(hooks_dir.join("posttooluse.mjs"), posttooluse_mjs)?;

    // Write sessionstart.mjs
    let sessionstart_mjs = r#"#!/usr/bin/env node
/**
 * SessionStart hook for LeanKG
 * Injects <tool_selection_hierarchy> at session start.
 */
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";

async function readStdin() {
  return new Promise((resolve) => {
    let data = "";
    process.stdin.on("data", (chunk) => (data += chunk));
    process.stdin.on("end", () => resolve(data));
  });
}

const raw = await readStdin();
const input = JSON.parse(raw);

const ROUTING_BLOCK = `
<tool_selection_hierarchy>
  1. ORCHESTRATE: mcp__leankg__orchestrate(intent)
     - Natural language: "show me impact of changing function X"

  2. CODE DISCOVERY: mcp__leankg__search_code(query, element_type)
     - Primary search. ONE call replaces many Grep/Bash commands.

  3. IMPACT ANALYSIS: mcp__leankg__get_impact_radius(file, depth)
     - Calculate blast radius BEFORE making changes.

  4. CONTEXT: mcp__leankg__get_context(file)
     - Get minimal token-optimized context for a file.

  5. DEPENDENCIES: mcp__leankg__get_dependencies(file) | mcp__leankg__get_dependents(file)

  6. CALLERS: mcp__leankg__get_callers(function) | mcp__leankg__find_function(name)

  7. DOCUMENTATION: mcp__leankg__get_doc_for_file(file) | mcp__leankg__get_traceability(element)

  8. TESTING: mcp__leankg__get_tested_by(file) | mcp__leankg__detect_changes(scope)
</tool_selection_hierarchy>

<forbidden_actions>
  - DO NOT use Grep for code search (use mcp__leankg__search_code instead)
  - DO NOT use Bash find/grep for file search (use mcp__leankg__query_file instead)
</forbidden_actions>
`;

console.log(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: "SessionStart",
    additionalContext: ROUTING_BLOCK,
  },
}));
"#;

    std::fs::write(hooks_dir.join("sessionstart.mjs"), sessionstart_mjs)?;

    // Write .claude-plugin/plugin.json (Claude Code plugin manifest)
    let claude_plugin_dir = plugin_dir.join(".claude-plugin");
    std::fs::create_dir_all(&claude_plugin_dir)?;

    let plugin_json = r#"{
  "name": "leankg",
  "version": "0.17.0",
  "description": "Lightweight knowledge graph for codebase understanding. Indexes code, builds dependency graphs, calculates impact radius, and exposes everything via MCP for AI tool integration.",
  "author": {
    "name": "LeanKG Team",
    "url": "https://github.com/FreePeak/LeanKG"
  },
  "homepage": "https://github.com/FreePeak/LeanKG#readme",
  "repository": "https://github.com/FreePeak/LeanKG",
  "license": "MIT",
  "keywords": ["mcp", "knowledge-graph", "code-indexing", "dependency-analysis", "context-window"],
  "mcpServers": {
    "leankg": {
      "command": "cargo",
      "args": ["run", "--", "mcp-stdio"]
    }
  }
}"#;
    std::fs::write(claude_plugin_dir.join("plugin.json"), plugin_json)?;
    println!(
        "  Installed plugin manifest to {}",
        claude_plugin_dir.join("plugin.json").display()
    );

    // Write .claude-plugin/marketplace.json (for marketplace distribution)
    let marketplace_json = r#"{
  "name": "leankg",
  "owner": {
    "name": "LeanKG Team",
    "email": "leankg@example.com"
  },
  "metadata": {
    "description": "LeanKG - Lightweight knowledge graph for codebase understanding",
    "version": "0.17.0"
  },
  "plugins": [
    {
      "name": "leankg",
      "source": "./",
      "description": "Claude Code plugin for lightweight knowledge graph-based codebase understanding. Indexes code, builds dependency graphs, calculates impact radius.",
      "version": "0.17.0",
      "author": {
        "name": "LeanKG Team"
      },
      "category": "development",
      "keywords": ["mcp", "knowledge-graph", "code-indexing", "dependency-analysis", "context-window"]
    }
  ]
}"#;
    std::fs::write(claude_plugin_dir.join("marketplace.json"), marketplace_json)?;
    println!(
        "  Installed marketplace manifest to {}",
        claude_plugin_dir.join("marketplace.json").display()
    );

    // Add LeanKG to enabledPlugins in settings.json
    add_to_enabled_plugins()?;

    println!("  Installed Claude hooks to {}", hooks_dir.display());

    Ok(())
}

fn add_to_enabled_plugins() -> Result<(), Box<dyn std::error::Error>> {
    let settings_path = std::path::PathBuf::from(std::env::var("HOME").unwrap_or_default())
        .join(".claude/settings.json");

    if !settings_path.exists() {
        println!("  Warning: settings.json not found, skipping enabledPlugins update");
        return Ok(());
    }

    let content = std::fs::read_to_string(&settings_path)?;
    let mut settings: serde_json::Map<String, serde_json::Value> =
        serde_json::from_str(&content).unwrap_or_default();

    // Get or create enabledPlugins object
    let enabled = settings
        .entry("enabledPlugins".to_string())
        .or_insert_with(|| serde_json::json!({}));

    // Add LeanKG if not present
    if let Some(obj) = enabled.as_object_mut() {
        if !obj.contains_key("leankg@local") {
            obj.insert("leankg@local".to_string(), serde_json::Value::Bool(true));
            let new_content = serde_json::to_string_pretty(&settings)?;
            std::fs::write(&settings_path, new_content)?;
            println!("  Added leankg@local to enabledPlugins in settings.json");
        } else {
            println!("  leankg@local already in enabledPlugins");
        }
    }

    Ok(())
}
