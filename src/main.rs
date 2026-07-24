#![allow(dead_code)]
mod api;
mod benchmark;
mod budget;
mod cli;
mod compress;
mod config;
mod db;
mod doc;
mod doc_indexer;
mod embed;
#[cfg(feature = "embeddings")]
mod embeddings;
mod gc;
mod graph;
mod indexer;
mod mcp;
mod obsidian;
mod ontology;
mod orchestrator;
mod registry;
#[cfg(feature = "embeddings")]
mod retrieval;
mod runtime;
mod sources;
mod watcher;
mod web;

#[path = "lsp/mod.rs"]
mod lsp;

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
        cli::CLICommand::Init { path, with_lsp } => {
            init_project(&path, with_lsp)?;
        }
        cli::CLICommand::Index {
            path,
            incremental,
            lang,
            exclude,
            verbose,
            env,
            service_name,
            version,
            source,
            ref_name,
            auth,
        } => {
            let _service_name = service_name;
            let _version = version;
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
                    &env,
                    source.as_deref(),
                    ref_name.as_deref(),
                    auth.as_deref(),
                )
                .await?;
            } else {
                index_codebase(
                    path.as_deref().unwrap_or("."),
                    &db_path,
                    lang.as_deref(),
                    &exclude_patterns,
                    verbose,
                    &env,
                    source.as_deref(),
                    ref_name.as_deref(),
                    auth.as_deref(),
                )
                .await?;
            }
        }
        cli::CLICommand::Serve { port, project } => {
            let port = port.unwrap_or_else(|| {
                std::env::var("PORT")
                    .ok()
                    .and_then(|p| p.parse().ok())
                    .unwrap_or(8080)
            });
            let project_path = resolve_serve_project(project)?;
            let db_path = project_path.join(".leankg");
            tokio::fs::create_dir_all(&db_path).await.ok();

            println!("╔═══════════════════════════════════════════════════════════════╗");
            println!("║  LeanKG Web UI (Embedded)                                   ║");
            println!("╚═══════════════════════════════════════════════════════════════╝");
            println!();
            println!("🚀 Starting server on http://localhost:{}", port);
            println!("📁 Project: {}", project_path.display());
            println!();
            web::start_server(port, db_path, None).await?;
        }
        cli::CLICommand::Web { port, project } => {
            let port = port.unwrap_or_else(|| {
                std::env::var("PORT")
                    .ok()
                    .and_then(|p| p.parse().ok())
                    .unwrap_or(8080)
            });
            let project_path = resolve_serve_project(project)?;
            let db_path = project_path.join(".leankg");
            tokio::fs::create_dir_all(&db_path).await.ok();

            println!("╔═══════════════════════════════════════════════════════════════╗");
            println!("║  LeanKG Web UI (Embedded)                                   ║");
            println!("╚═══════════════════════════════════════════════════════════════╝");
            println!();
            println!("🚀 Starting server on http://localhost:{}", port);
            println!("📁 Project: {}", project_path.display());
            println!();
            web::start_server(port, db_path, None).await?;
        }
        cli::CLICommand::McpStdio { watch } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");

            tokio::fs::create_dir_all(&db_path).await.ok();

            if watch {
                let lockfile = db_path.join("leankg.pid");
                if let Ok(pid_str) = std::fs::read_to_string(&lockfile) {
                    if let Ok(old_pid) = pid_str.trim().parse::<u32>() {
                        let alive = std::process::Command::new("kill")
                            .args(["-0", &old_pid.to_string()])
                            .output()
                            .map(|o| o.status.success())
                            .unwrap_or(false);
                        if alive {
                            tracing::warn!(
                                "Another LeanKG watcher (PID {}) is already running for this project. Disabling --watch for this instance.",
                                old_pid
                            );
                            let mcp_server = mcp::MCPServer::new(db_path);
                            if let Err(e) = mcp_server.serve_stdio().await {
                                eprintln!("MCP stdio server error: {}", e);
                            }
                            return Ok(());
                        }
                    }
                }
                let _ = std::fs::write(&lockfile, std::process::id().to_string());
            }

            let mcp_server = if watch {
                mcp::MCPServer::new_with_watch(db_path, project_path.clone())
            } else {
                mcp::MCPServer::new(db_path)
            };
            if let Err(e) = mcp_server.serve_stdio().await {
                eprintln!("MCP stdio server error: {}", e);
            }
        }
        cli::CLICommand::McpHttp {
            port,
            auth,
            watch,
            reuse,
            project,
        } => {
            let project_path = if let Some(ref p) = project {
                std::path::PathBuf::from(p)
            } else {
                find_project_root()?
            };
            let db_path = project_path.join(".leankg");
            let port = port.unwrap_or_else(|| {
                std::env::var("MCP_HTTP_PORT")
                    .ok()
                    .and_then(|p| p.parse().ok())
                    .unwrap_or(9699)
            });
            let auth_token = auth.or_else(|| std::env::var("MCP_HTTP_AUTH").ok());

            tokio::fs::create_dir_all(&db_path).await.ok();

            let mcp_server = if watch {
                mcp::MCPServer::new_with_watch(db_path.clone(), project_path.clone())
            } else {
                mcp::MCPServer::new(db_path.clone())
            };

            println!("╔═══════════════════════════════════════════════════════════════╗");
            println!("║  LeanKG MCP HTTP Server (Remote Mode)                      ║");
            println!("╚═══════════════════════════════════════════════════════════════╝");
            println!();
            println!("🚀 Starting MCP HTTP server on http://localhost:{}", port);
            if auth_token.is_some() {
                println!("🔒 Authentication: enabled");
            } else {
                println!("🔓 Authentication: disabled (not recommended for production)");
            }
            if reuse {
                println!("🔄 Reuse mode: will connect to existing server if available");
            }
            println!();

            if let Err(e) = mcp_server.serve_http(port, auth_token, reuse).await {
                eprintln!("MCP HTTP server error: {}", e);
            }
        }
        cli::CLICommand::Impact {
            file,
            depth,
            max_affected,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            let result = calculate_impact(&file, depth, max_affected, &db_path)?;
            println!(
                "Impact radius for {} (depth={}, max_affected={}):",
                file, depth, max_affected
            );
            if result.truncated {
                println!(
                    "  WARNING: result truncated at max_affected={}; re-run with --max-affected higher or smaller depth.",
                    max_affected
                );
            }
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
        cli::CLICommand::Path {
            source,
            target,
            max_hops,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_shortest_path(&source, &target, max_hops, &db_path)?;
        }
        cli::CLICommand::Explain { name } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_explain_node(&name, &db_path)?;
        }
        cli::CLICommand::Gods {
            limit,
            exclude_hubs_percentile,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_god_nodes(limit, exclude_hubs_percentile, &db_path)?;
        }
        cli::CLICommand::Report { project_name, out } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_graph_report(
                &project_path,
                project_name.as_deref(),
                out.as_deref(),
                &db_path,
            )?;
        }
        cli::CLICommand::CheckConsistency { severity, limit } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_check_consistency(severity.as_deref(), limit, &db_path)?;
        }
        cli::CLICommand::LspResolve {
            language,
            file_path,
            line,
            character,
            request,
            project,
        } => {
            run_lsp_resolve(
                language.as_deref(),
                &file_path,
                line,
                character,
                &request,
                &project,
            )?;
        }
        cli::CLICommand::LspInstall {
            language,
            project,
            dry_run,
        } => {
            run_lsp_install(&language, &project, dry_run)?;
        }
        cli::CLICommand::LspList => {
            run_lsp_list()?;
        }
        cli::CLICommand::Tunnels { limit } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_tunnels(limit, &db_path)?;
        }
        cli::CLICommand::Reflect {
            question,
            outcome,
            nodes,
            note,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            let nodes_vec: Vec<String> = nodes
                .as_deref()
                .map(|s| {
                    s.split(',')
                        .map(|x| x.trim().to_string())
                        .filter(|x| !x.is_empty())
                        .collect()
                })
                .unwrap_or_default();
            run_reflect(
                &project_path,
                &question,
                &outcome,
                &nodes_vec,
                note.as_deref(),
                &db_path,
            )?;
        }
        cli::CLICommand::Prs { env, files } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            let files_vec: Vec<String> = files
                .as_deref()
                .map(|s| {
                    s.split(',')
                        .map(|x| x.trim().to_string())
                        .filter(|x| !x.is_empty())
                        .collect()
                })
                .unwrap_or_default();
            run_prs(&project_path, &env, &files_vec, &db_path)?;
        }
        cli::CLICommand::Generate { template: _ } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            generate_docs(&db_path)?;
        }
        cli::CLICommand::Query {
            query,
            kind,
            file,
            function,
            token_budget,
            max_depth,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            // If --file or --function is given, handle those directly;
            // otherwise fall through to the kind-based query.
            if let Some(file_path) = file {
                run_file_query(&file_path, &db_path)?;
            } else if let Some(func_name) = function {
                run_function_query(&func_name, &db_path)?;
            } else if kind == "subgraph" {
                run_graph_query(&query, token_budget, max_depth, &db_path)?;
            } else {
                run_query(&query, &kind, &db_path)?;
            }
        }
        cli::CLICommand::GraphQuery {
            question,
            token_budget,
            max_depth,
        } => {
            let project_path = find_project_root()?;
            let db_path = project_path.join(".leankg");
            run_graph_query(&question, Some(token_budget), Some(max_depth), &db_path)?;
        }
        cli::CLICommand::Install => {
            install_mcp_config()?;
        }
        cli::CLICommand::Doctor { kill } => {
            run_doctor(kill)?;
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
        #[cfg(feature = "embeddings")]
        cli::CLICommand::Embed {
            init,
            full,
            batch_size,
            project,
            wait,
            status,
            cancel,
            background,
            workers,
            types,
        } => {
            run_embed(
                init, full, batch_size, &project, wait, status, cancel, background, workers, &types,
            )?;
        }
        #[cfg(feature = "embeddings")]
        cli::CLICommand::SemanticContext {
            query,
            env,
            top_k,
            rerank_top_n,
            no_traverse,
            include_worktrees,
            include_ontology_steps,
            debug,
            project,
        } => {
            run_semantic_context(
                &query,
                &env,
                top_k,
                rerank_top_n,
                !no_traverse,
                include_worktrees,
                include_ontology_steps,
                debug,
                &project,
            )?;
        }
        #[cfg(feature = "embeddings")]
        cli::CLICommand::SmokeTest { project } => {
            run_smoke_test(&project)?;
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
                "claude" => benchmark::CliTool::Claude,
                _ => benchmark::CliTool::Kilo,
            };
            benchmark::run(category, cli_tool)?;
        }
        cli::CLICommand::ToolBench { project } => {
            let project_path = match project {
                Some(p) => std::path::PathBuf::from(p),
                None => find_project_root()?,
            };
            benchmark::tool_bench::run(&project_path.to_string_lossy())?;
        }
        cli::CLICommand::AbTest { project } => {
            let project_path = match project {
                Some(p) => std::path::PathBuf::from(p),
                None => find_project_root()?,
            };
            benchmark::ab_test::run(&project_path.to_string_lossy())?;
        }
        cli::CLICommand::BenchmarkUnified { project } => {
            let project_path = match project {
                Some(p) => std::path::PathBuf::from(p),
                None => find_project_root()?,
            };
            benchmark::unified::run(&project_path.to_string_lossy())?;
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
        cli::CLICommand::Incident { command } => {
            handle_incident_command(command)?;
        }
        cli::CLICommand::Team { command } => {
            handle_team_command(command)?;
        }
        cli::CLICommand::Note {
            target,
            content,
            env,
        } => {
            add_note(&target, &content, &env)?;
        }
        cli::CLICommand::Pattern {
            title,
            context,
            solution,
            env,
        } => {
            add_pattern(&title, &context, &solution, &env)?;
        }
        cli::CLICommand::EnvConflicts { service } => {
            show_env_conflicts(&service)?;
        }
        cli::CLICommand::Push { remote, token, env } => {
            push_to_remote(&remote, &token, &env)?;
        }
        cli::CLICommand::Pull { remote, token, env } => {
            pull_from_remote(&remote, &token, &env)?;
        }
        cli::CLICommand::Ontology { command } => {
            handle_ontology_command(command)?;
        }
    }

    Ok(())
}

fn push_to_remote(remote: &str, _token: &str, env: &str) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db);

    let elements = graph.all_elements()?;
    let relationships = graph.all_relationships()?;

    let payload = serde_json::json!({
        "env": env,
        "service": project_path.file_name().and_then(|n| n.to_str()).unwrap_or("unknown"),
        "elements": elements,
        "relationships": relationships,
    });

    let client = reqwest::blocking::Client::new();
    let url = format!("{}/api/v2/graph/push", remote.trim_end_matches('/'));
    let resp = client
        .post(&url)
        .header("X-LeanKG-Token", _token)
        .header(
            "X-LeanKG-Engineer",
            std::env::var("USER").unwrap_or_else(|_| "unknown".to_string()),
        )
        .header("X-LeanKG-Env", env)
        .json(&payload)
        .send()?;

    let status = resp.status();
    if status.is_success() {
        println!(
            "Pushed {} elements and {} relationships to {} (env: {})",
            elements.len(),
            relationships.len(),
            remote,
            env
        );
    } else {
        let body = resp.text()?;
        eprintln!("Push failed ({}): {}", status, body);
    }
    Ok(())
}

fn pull_from_remote(
    remote: &str,
    _token: &str,
    env: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    let client = reqwest::blocking::Client::new();
    let url = format!("{}/api/v2/status", remote.trim_end_matches('/'));
    let resp = client
        .get(&url)
        .header("X-LeanKG-Token", _token)
        .header("X-LeanKG-Env", env)
        .send()?;

    let status = resp.status();
    if status.is_success() {
        println!("Successfully connected to {} (env: {})", remote, env);
    } else {
        let body = resp.text()?;
        eprintln!("Pull failed ({}): {}", status, body);
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

/// Resolve the project root for `leankg serve` / `web`.
/// Prefer `--project`, then `LEANKG_SERVE_PROJECT`, then find_project_root (cwd).
/// Do **not** default to `LEANKG_MCP_PROJECT` — MCP often points at a multi-repo
/// mount while the UI demo should open `/workspace` (LeanKG itself).
fn resolve_serve_project(
    cli_project: Option<String>,
) -> Result<std::path::PathBuf, Box<dyn std::error::Error>> {
    if let Some(p) = cli_project {
        let path = std::path::PathBuf::from(p);
        if !path.is_dir() {
            return Err(format!("serve --project is not a directory: {}", path.display()).into());
        }
        return Ok(path.canonicalize().unwrap_or(path));
    }
    if let Ok(p) = std::env::var("LEANKG_SERVE_PROJECT") {
        let trimmed = p.trim();
        if !trimmed.is_empty() {
            let path = std::path::PathBuf::from(trimmed);
            if !path.is_dir() {
                return Err(format!(
                    "LEANKG_SERVE_PROJECT is not a directory: {}",
                    path.display()
                )
                .into());
            }
            return Ok(path.canonicalize().unwrap_or(path));
        }
    }
    find_project_root()
}

fn init_project(path: &str, with_lsp: bool) -> Result<(), Box<dyn std::error::Error>> {
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

    // FR-LSP-B / REL-039: prefab lsp block + typed_resolve for Go/TS MVP
    if with_lsp {
        config.lsp = Some(crate::lsp::config::LspConfig::prefab_defaults());
        config.indexer.typed_resolve = "go,ts".to_string();
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
    if with_lsp {
        let n = config.lsp.as_ref().map(|l| l.servers.len()).unwrap_or(0);
        println!(
            "  Prefab lsp: {} servers; indexer.typed_resolve={}",
            n, config.indexer.typed_resolve
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
    env: &str,
    source_uri: Option<&str>,
    ref_name: Option<&str>,
    auth: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let _ = env;
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

    // Resolve the effective source: CLI flags take precedence over leankg.yaml config.
    let effective_source = source_uri.or(config.source.as_ref().map(|s| s.uri.as_str()));
    let effective_auth = auth.or(config.source.as_ref().and_then(|s| s.auth.as_deref()));
    let effective_ref_name =
        ref_name.or(config.source.as_ref().and_then(|s| s.ref_name.as_deref()));

    let index_path = if let Some(src_uri) = effective_source {
        let uri = sources::parse_source_uri(src_uri)
            .map_err(|e| format!("invalid source URI '{}': {}", src_uri, e))?;
        let src = sources::SourceFactory::create(&uri, effective_auth, effective_ref_name)
            .map_err(|e| format!("cannot create source for '{}': {}", src_uri, e))?;

        let staging_root = db_path
            .parent()
            .unwrap_or(std::path::Path::new("."))
            .join(".leankg")
            .join("sources");
        tokio::fs::create_dir_all(&staging_root).await?;

        println!("Syncing from source '{}'...", src.name());
        let mut progress = sources::CliProgress;
        let synced = src
            .sync_to_local(&staging_root, &mut progress)
            .await
            .map_err(|e| format!("source sync failed: {}", e))?;
        synced.to_string_lossy().to_string()
    } else if path == "." {
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

    let total_elements = indexer::index_files_parallel_with_typed_resolve(
        &graph_engine,
        &files,
        verbose,
        &config.indexer.typed_resolve,
    )?;
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

    // FR-ONT-PROC-03: refresh procedural ontology after successful index.
    if let Some(project_root) = db_path.parent() {
        match crate::ontology::sync_for_project(project_root, &graph_engine) {
            Ok(stats) => {
                println!(
                    "Ontology refreshed after index (workflows={}, steps={})",
                    stats.workflows, stats.workflow_steps
                );
            }
            Err(e) => {
                tracing::debug!("Ontology post-index sync skipped: {}", e);
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
    env: &str,
    source_uri: Option<&str>,
    ref_name: Option<&str>,
    auth: Option<&str>,
) -> Result<(), Box<dyn std::error::Error>> {
    let _ = env;
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let mut parser_manager = indexer::ParserManager::new();
    parser_manager.init_parsers()?;

    // Resolve the effective source: CLI flags take precedence over leankg.yaml config.
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
    let effective_source = source_uri.or(config.source.as_ref().map(|s| s.uri.as_str()));
    let effective_auth = auth.or(config.source.as_ref().and_then(|s| s.auth.as_deref()));
    let effective_ref_name =
        ref_name.or(config.source.as_ref().and_then(|s| s.ref_name.as_deref()));

    let sync_path: String = if let Some(src_uri) = effective_source {
        let uri = sources::parse_source_uri(src_uri)
            .map_err(|e| format!("invalid source URI '{}': {}", src_uri, e))?;
        let src = sources::SourceFactory::create(&uri, effective_auth, effective_ref_name)
            .map_err(|e| format!("cannot create source for '{}': {}", src_uri, e))?;

        let staging_root = db_path
            .parent()
            .unwrap_or(std::path::Path::new("."))
            .join(".leankg")
            .join("sources");
        tokio::fs::create_dir_all(&staging_root).await?;

        println!("Re-syncing source '{}' for incremental index...", src.name());
        let mut progress = sources::CliProgress;
        let synced = src
            .sync_to_local(&staging_root, &mut progress)
            .await
            .map_err(|e| format!("source sync failed: {}", e))?;

        // For remote sources, always do a full index after sync since
        // incremental diff on the staged dir won't detect source changes.
        println!("Remote source synced. Falling back to full index on latest content.");
        return index_codebase(
            &synced.to_string_lossy(),
            db_path,
            lang_filter,
            exclude_patterns,
            verbose,
            env,
            source_uri,
            ref_name,
            auth,
        )
        .await;
    } else {
        path.to_string()
    };

    println!("Performing incremental indexing for {}...", sync_path);

    match indexer::incremental_index_sync(&graph_engine, &mut parser_manager, &sync_path).await {
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
            index_codebase(&sync_path, db_path, lang_filter, exclude_patterns, verbose, env, None, None, None).await?;
        }
    }

    // FR-ONT-PROC-03: refresh procedural ontology after incremental index.
    if let Some(project_root) = db_path.parent() {
        match crate::ontology::sync_for_project(project_root, &graph_engine) {
            Ok(stats) => {
                println!(
                    "Ontology refreshed after incremental index (workflows={}, steps={})",
                    stats.workflows, stats.workflow_steps
                );
            }
            Err(e) => {
                tracing::debug!("Ontology post-index sync skipped: {}", e);
            }
        }
    }

    Ok(())
}

fn calculate_impact(
    file: &str,
    depth: u32,
    max_affected: usize,
    db_path: &std::path::Path,
) -> Result<graph::ImpactResult, Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let analyzer = graph::ImpactAnalyzer::new(&graph_engine);
    let opts = graph::ImpactScanOptions { max_affected };

    let result = analyzer.calculate_impact_radius_with_options(file, depth, 0.0, &opts)?;
    Ok(result)
}

fn run_shortest_path(
    source: &str,
    target: &str,
    max_hops: usize,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    match graph_engine.shortest_path(source, target, max_hops)? {
        Some(result) => {
            println!(
                "{} -> {} ({} hops)",
                result.source, result.target, result.hops
            );
            for (i, hop) in result.path.iter().enumerate() {
                println!(
                    "  {}. {} --[{} conf={:.2} {}]--> {}",
                    i + 1,
                    hop.from,
                    hop.rel_type,
                    hop.confidence,
                    hop.confidence_label,
                    hop.to,
                );
            }
        }
        None => println!(
            "No path found between '{}' and '{}' within {} hops",
            source, target, max_hops
        ),
    }
    Ok(())
}

/// US-GF-03 / FR-GF-05..06: print a budgeted NL subgraph.
fn run_graph_query(
    question: &str,
    token_budget: Option<usize>,
    max_depth: Option<usize>,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let result = graph_engine.query_graph(question, token_budget, max_depth)?;
    println!("question: {}", result.question);
    println!(
        "seeds ({}): {}",
        result.seeds.len(),
        result.seeds.join(", ")
    );
    println!(
        "nodes={} edges={} hops={} tokens~{}/{} truncated={}",
        result.nodes.len(),
        result.edges.len(),
        result.hops,
        result.tokens_estimate,
        result.token_budget,
        result.truncated
    );
    if let Some(path) = &result.path {
        println!(
            "path: {} -> {} ({} hops)",
            path.source, path.target, path.hops
        );
    }
    for edge in &result.edges {
        println!(
            "  {} --[{} conf={:.2} {}]--> {}",
            edge.from, edge.rel_type, edge.confidence, edge.confidence_label, edge.to
        );
    }
    for node in result.nodes.iter().filter(|n| n.is_seed) {
        println!(
            "  seed: {} [{}] {}",
            node.qualified_name, node.element_type, node.file_path
        );
    }
    Ok(())
}

fn run_explain_node(
    name: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    match graph_engine.explain_node(name)? {
        Some(expl) => {
            println!(
                "{} [{}] {} (lines {}-{})",
                expl.qualified_name, expl.element_type, expl.name, expl.line_start, expl.line_end
            );
            println!("  file: {}", expl.file_path);
            if let Some(label) = expl.cluster_label {
                println!(
                    "  cluster: {} ({})",
                    label,
                    expl.cluster_id.unwrap_or_default()
                );
            }
            println!(
                "  in_degree: {}  out_degree: {}",
                expl.in_degree, expl.out_degree
            );
            for n in expl.top_neighbors.iter().take(8) {
                println!("    {} -> {}", n.rel_type, n.count);
            }
        }
        None => println!("Symbol '{}' not found in graph", name),
    }
    Ok(())
}

fn run_god_nodes(
    limit: usize,
    exclude_hubs_percentile: Option<u8>,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let nodes = graph_engine.get_god_nodes(limit, exclude_hubs_percentile)?;
    println!("Top {} god nodes:", nodes.len());
    for (i, n) in nodes.iter().enumerate() {
        println!(
            "  {}. {} [{}] degree={} ({})",
            i + 1,
            n.qualified_name,
            n.element_type,
            n.degree,
            n.name
        );
    }
    Ok(())
}

fn run_graph_report(
    project_path: &std::path::Path,
    project_name: Option<&str>,
    out: Option<&str>,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let name = project_name.map(String::from).unwrap_or_else(|| {
        project_path
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("project")
            .to_string()
    });
    let report = graph_engine.generate_graph_report(&name)?;
    let markdown = report.to_markdown();
    let out_path = match out {
        Some(p) => std::path::PathBuf::from(p),
        None => project_path.join(".leankg").join("GRAPH_REPORT.md"),
    };
    if let Some(parent) = out_path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    std::fs::write(&out_path, &markdown)?;
    println!("Wrote graph report to {}", out_path.display());
    Ok(())
}

fn run_check_consistency(
    severity_filter: Option<&str>,
    limit: usize,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let report = graph_engine.check_consistency()?;
    println!(
        "Total relationships: {} | BROKEN: {} | STALE: {}",
        report.total_relationships, report.broken, report.stale
    );
    let mut findings: Vec<_> = report
        .findings
        .iter()
        .filter(|f| severity_filter.is_none_or(|s| f.severity == s))
        .collect();
    let total_after_filter = findings.len();
    let effective_limit = if limit == 0 { usize::MAX } else { limit };
    findings.truncate(effective_limit);
    for f in findings.iter().take(effective_limit) {
        println!(
            "  [{}] {} --[{}]--> {} :: {}",
            f.severity, f.source, f.rel_type, f.target, f.message
        );
    }
    if total_after_filter > findings.len() {
        println!(
            "  ... and {} more (raise --limit to see more)",
            total_after_filter - findings.len()
        );
    }
    Ok(())
}

fn run_tunnels(limit: usize, db_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let mut tunnels = graph_engine.find_tunnels()?;
    tunnels.truncate(limit);
    println!("Found {} cross-cluster tunnels:", tunnels.len());
    for (i, t) in tunnels.iter().enumerate() {
        println!(
            "  {}. {} --[{}]--> {}  ({:.2})  [{} -> {}]",
            i + 1,
            t.source,
            t.rel_type,
            t.target,
            t.confidence,
            t.source_cluster,
            t.target_cluster,
        );
    }
    Ok(())
}

fn run_reflect(
    project_path: &std::path::Path,
    question: &str,
    outcome: &str,
    nodes: &[String],
    note: Option<&str>,
    _db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    graph::GraphEngine::report_query_outcome(project_path, question, nodes, outcome, note)?;
    println!("Recorded reflection for outcome '{}'", outcome);
    Ok(())
}

fn run_prs(
    _project_path: &std::path::Path,
    env: &str,
    files: &[String],
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);
    let report = graph_engine.pr_impact(files, env)?;
    println!(
        "PR impact: severity={} | touched_clusters={} | changed_files={}",
        report.severity,
        report.touched_clusters.len(),
        report.changed_file_count
    );
    for f in report.files.iter().take(50) {
        let cluster = f.cluster_label.as_deref().unwrap_or("(none)");
        println!("  {} -> cluster={}", f.file, cluster);
    }
    Ok(())
}

fn run_lsp_resolve(
    language: Option<&str>,
    file_path: &str,
    line: u32,
    character: u32,
    request: &str,
    project: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    use crate::lsp::{LspBridge, LspRequest};
    let config_path = std::path::Path::new(project).join("leankg.yaml");
    let bridge = LspBridge::from_leankg_yaml_or_default(&config_path);

    // Auto-detect language from file extension when not given.
    let detected = language
        .map(String::from)
        .or_else(|| crate::lsp::detect_language(std::path::Path::new(file_path)).map(String::from))
        .ok_or_else(|| {
            format!(
                "Could not detect language from '{file_path}'. Pass --language explicitly \
                 (e.g. --language go)."
            )
        })?;

    let lsp_request = match request {
        "references" => LspRequest::References,
        "hover" => LspRequest::Hover,
        _ => LspRequest::Definition,
    };
    match bridge.resolve(
        &detected,
        std::path::Path::new(file_path),
        line,
        character,
        lsp_request,
    )? {
        Some(locations) => {
            println!("LSP returned {} location(s):", locations.len());
            for loc in locations {
                println!(
                    "  {}:{}:{} - {}:{}",
                    loc.uri, loc.line, loc.character, loc.end_line, loc.end_character
                );
            }
        }
        None => {
            // Surface a helpful hint instead of a silent fallback.
            println!(
                "No LSP server configured for '{detected}' (or no server at {}. Falling back to tree-sitter.",
                config_path.display()
            );
            if let Some(spec) = crate::lsp::LspServerSpec::for_language(&detected) {
                println!(
                    "Hint: run `leankg lsp-install {detected}` to install '{}'.",
                    spec.command
                );
            } else {
                println!(
                    "Hint: '{detected}' is not in the LSP registry yet. \
                     Add a `lsp:` block to leankg.yaml."
                );
            }
        }
    }
    Ok(())
}

/// Install LSP server(s) for one language or "all".
fn run_lsp_install(
    language: &str,
    project: &str,
    dry_run: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    use crate::lsp::registry::LspServerSpec;
    let targets: Vec<&'static LspServerSpec> = if language.eq_ignore_ascii_case("all") {
        crate::lsp::registry::ALL_LSP_SERVERS.iter().collect()
    } else {
        match LspServerSpec::for_language(language) {
            Some(s) => vec![s],
            None => {
                return Err(format!(
                "Unknown language '{language}'. Run `leankg lsp-list` to see supported languages."
            )
                .into())
            }
        }
    };
    let mut installed = 0usize;
    let mut already_on_path = 0usize;
    let mut manual = 0usize;
    let mut failed: Vec<String> = Vec::new();
    for spec in targets {
        println!("[{}]  command = {}", spec.language, spec.command);
        if is_on_path(spec.command) {
            println!("  ✓ already on PATH, skipping");
            already_on_path += 1;
            continue;
        }
        // Pick the best automatic install method.
        let method = spec
            .install
            .iter()
            .find(|m| m.is_automatic())
            .unwrap_or(spec.install.first().unwrap());
        let cmd_str = method.hint();
        println!("  → {cmd_str}");
        if dry_run {
            manual += 1;
            continue;
        }
        let result = run_install_command(method);
        match result {
            Ok(()) => {
                installed += 1;
                println!("  ✓ installed");
            }
            Err(e) => {
                eprintln!("  ✗ failed: {e}");
                failed.push(format!("{}: {}", spec.language, e));
            }
        }
    }
    println!();
    println!(
        "Done: {} installed, {} already present, {} manual/dry-run, {} failed.",
        installed,
        already_on_path,
        manual,
        failed.len()
    );
    if !failed.is_empty() {
        eprintln!("Failures:\n  - {}", failed.join("\n  - "));
    }
    let _ = project;
    Ok(())
}

/// Print every language the LSP registry knows about.
fn run_lsp_list() -> Result<(), Box<dyn std::error::Error>> {
    println!(
        "LSP server catalog ({} entries):",
        crate::lsp::registry::ALL_LSP_SERVERS.len()
    );
    for spec in crate::lsp::registry::ALL_LSP_SERVERS {
        let on_path = if is_on_path(spec.command) { "✓" } else { " " };
        let extensions = spec.extensions.join(", ");
        println!(
            "  [{on_path}] {:<14} {:<32} {}",
            spec.language, spec.command, extensions
        );
    }
    Ok(())
}

fn is_on_path(cmd: &str) -> bool {
    if let Ok(paths) = std::env::var("PATH") {
        for dir in paths.split(':') {
            if dir.is_empty() {
                continue;
            }
            let candidate = std::path::Path::new(dir).join(cmd);
            if candidate.is_file() {
                return true;
            }
        }
    }
    false
}

fn run_install_command(method: &crate::lsp::registry::InstallMethod) -> Result<(), String> {
    use crate::lsp::registry::InstallMethod;
    let (program, args) = match method {
        InstallMethod::Npm { package } => (
            "npm",
            vec!["install".to_string(), "-g".to_string(), package.to_string()],
        ),
        InstallMethod::Pip { package } => ("pip", vec!["install".to_string(), package.to_string()]),
        InstallMethod::Cargo { crate_name } => {
            ("cargo", vec!["install".to_string(), crate_name.to_string()])
        }
        InstallMethod::Brew { formula } => {
            ("brew", vec!["install".to_string(), formula.to_string()])
        }
        InstallMethod::GoInstall { pkg } => ("go", vec!["install".to_string(), pkg.to_string()]),
        InstallMethod::Gem { gem } => ("gem", vec!["install".to_string(), gem.to_string()]),
        InstallMethod::Opam { pkg } => ("opam", vec!["install".to_string(), pkg.to_string()]),
        InstallMethod::Dotnet { tool } => (
            "dotnet",
            vec![
                "tool".to_string(),
                "install".to_string(),
                "-g".to_string(),
                tool.to_string(),
            ],
        ),
        InstallMethod::Manual { url, note } => {
            return Err(format!("manual install required: {note} ({url})"));
        }
    };
    let status = std::process::Command::new(program)
        .args(&args)
        .status()
        .map_err(|e| format!("failed to spawn `{program}`: {e}"))?;
    if status.success() {
        Ok(())
    } else {
        Err(format!(
            "`{program} {}` exited with status {}",
            args.join(" "),
            status
        ))
    }
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

/// Diagnose stale leankg processes and mmap'd DB files. With --kill,
/// also terminates the stale processes (but never the current
/// process or its parent).
fn run_doctor(kill: bool) -> Result<(), Box<dyn std::error::Error>> {
    use std::process::Command;
    let self_pid = std::process::id();
    let parent_pid = std::env::var("PPID")
        .ok()
        .and_then(|s| s.parse::<u32>().ok())
        .unwrap_or(0);

    // List every `leankg` process on the host.
    let output = Command::new("pgrep").args(["-fl", "leankg"]).output();
    let procs: Vec<(u32, String)> = match output {
        Ok(out) if out.status.success() => String::from_utf8_lossy(&out.stdout)
            .lines()
            .filter_map(|line| {
                let mut parts = line.splitn(2, ' ');
                let pid = parts.next()?.parse::<u32>().ok()?;
                let cmd = parts.next()?.to_string();
                Some((pid, cmd))
            })
            .collect(),
        _ => {
            // Fall back to `ps` if pgrep isn't available.
            let out = Command::new("ps")
                .args(["-axo", "pid=,command="])
                .output()?;
            String::from_utf8_lossy(&out.stdout)
                .lines()
                .filter_map(|line| {
                    let mut parts = line.splitn(2, ' ');
                    let pid = parts.next()?.trim().parse::<u32>().ok()?;
                    let cmd = parts.next()?.trim().to_string();
                    if cmd.contains("leankg") {
                        Some((pid, cmd))
                    } else {
                        None
                    }
                })
                .collect()
        }
    };

    let my_exe = std::env::current_exe()
        .map(|p| p.to_string_lossy().to_string())
        .unwrap_or_default();
    // Heuristic: the leankg binary path itself is what we want.
    // Match `argv[0]` ending in `leankg` (absolute or relative) but
    // NOT paths that merely contain a directory named `leankg` (e.g.
    // a cline daemon with `--cwd .../work/harvey/freepeak/leankg`).
    let is_leankg = |cmd: &str| -> bool {
        // Take the first token (argv[0]).
        let argv0 = cmd.split_whitespace().next().unwrap_or("");
        let bin = std::path::Path::new(argv0)
            .file_name()
            .and_then(|s| s.to_str())
            .unwrap_or("");
        bin == "leankg"
    };
    let others: Vec<_> = procs
        .into_iter()
        .filter(|(pid, cmd)| {
            // Skip the current process and its parent (the shell that
            // invoked us). Both can have "leankg" in their argv
            // transitively.
            *pid != self_pid
                && *pid != parent_pid
                && is_leankg(cmd)
                && (cmd.contains(&my_exe) || !cmd.starts_with('/'))
        })
        .collect();

    if others.is_empty() {
        println!("No stale leankg processes detected.");
    } else {
        println!("Stale leankg processes (RSS reported by `ps`):");
        for (pid, cmd) in &others {
            // ps -o rss= -p <pid> for actual RSS.
            let rss = Command::new("ps")
                .args(["-o", "rss=", "-p", &pid.to_string()])
                .output()
                .ok()
                .and_then(|o| String::from_utf8(o.stdout).ok())
                .map(|s| s.trim().parse::<u64>().unwrap_or(0))
                .unwrap_or(0);
            println!("  PID {:>6}  RSS {:>7} MB  {}", pid, rss / 1024, cmd);
        }
    }

    // List mmap'd DB files.
    let lsof_out = Command::new("lsof")
        .args(["-nP", "+D", "/Users/linh.doan/work"])
        .output();
    if let Ok(out) = lsof_out {
        if out.status.success() {
            let mut found = 0usize;
            for line in String::from_utf8_lossy(&out.stdout).lines() {
                if line.contains("leankg.db") && line.contains("leankg") {
                    println!("  {}", line);
                    found += 1;
                    if found > 20 {
                        println!("  ... (truncated)");
                        break;
                    }
                }
            }
        }
    }

    if kill && !others.is_empty() {
        println!();
        println!("Killing {} stale process(es)...", others.len());
        for (pid, _) in &others {
            if *pid == self_pid || *pid == parent_pid {
                continue;
            }
            // SIGTERM first; the daemon has a graceful shutdown path.
            let _ = Command::new("kill")
                .args(["-TERM", &pid.to_string()])
                .output();
        }
        // Give them 2s to exit cleanly; SIGKILL stragglers.
        std::thread::sleep(std::time::Duration::from_secs(2));
        for (pid, _) in &others {
            if *pid == self_pid || *pid == parent_pid {
                continue;
            }
            let still_alive = Command::new("kill")
                .args(["-0", &pid.to_string()])
                .output()
                .map(|o| o.status.success())
                .unwrap_or(false);
            if still_alive {
                eprintln!("  PID {} did not exit on SIGTERM; sending SIGKILL", pid);
                let _ = Command::new("kill")
                    .args(["-KILL", &pid.to_string()])
                    .output();
            }
        }
        println!("Done.");
    } else if !others.is_empty() {
        println!();
        println!("Re-run with --kill to terminate these processes.");
    }

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

    let storage = db::schema::resolve_storage_config(db_path);
    let db = db::schema::init_db(db_path)?;

    let elements = graph::GraphEngine::new(db.clone()).all_elements()?;
    let relationships = graph::GraphEngine::new(db.clone()).all_relationships()?;
    let annotations = db::all_business_logic(&db)?;

    println!("LeanKG Status:");
    println!("  Database: {}", db_path.display());
    println!("  Storage Engine: {:?}", storage.engine);
    println!("  Storage Path: {}", storage.path.display());
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

    let graph = graph::GraphEngine::new(db.clone());
    match crate::graph::inventory::load_latest_inventory(&db) {
        Ok(Some(inv)) => {
            println!("  Inventory ({}):", inv.computed_at);
            println!("    Vectors: {}", inv.total_vectors);
            println!("    Documents: {}", inv.total_documents);
            println!("    Est. vector bytes: {}", inv.estimated_vector_bytes);
        }
        Ok(None) => {
            if let Ok(inv) = crate::graph::inventory::refresh_index_inventory(&graph, "cli_status")
            {
                println!("  Inventory refreshed: {} vectors", inv.total_vectors);
            }
        }
        Err(e) => eprintln!("  Warning: inventory load failed: {}", e),
    }

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
        "content" => {
            let results = graph_engine.search_by_content(query)?;
            if results.is_empty() {
                println!("No elements found matching content '{}'", query);
            } else {
                println!(
                    "Found {} element(s) matching content '{}' (name / qualified_name / file_path):",
                    results.len(),
                    query
                );
                for elem in results {
                    println!(
                        "  - {} ({}) [{}:{}]",
                        elem.qualified_name, elem.element_type, elem.line_start, elem.line_end
                    );
                    println!("    File: {}", elem.file_path);
                }
            }
        }
        _ => {
            println!(
                "Unknown query kind '{}'. Use: name, type, rel, pattern, or content",
                kind
            );
        }
    }

    Ok(())
}

/// Query elements by file path (substring match). Supports the `--file` CLI flag.
fn run_file_query(
    file_path: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);

    let results = graph_engine.get_elements_by_file(file_path)?;
    if results.is_empty() {
        // Fallback: substring search across all file_path values
        let all = graph_engine.search_by_content(file_path)?;
        let filtered: Vec<_> = all
            .into_iter()
            .filter(|e| e.file_path.contains(file_path))
            .collect();
        if filtered.is_empty() {
            println!("No elements found in file matching '{}'", file_path);
        } else {
            println!(
                "Found {} element(s) in files matching '{}':",
                filtered.len(),
                file_path
            );
            for elem in filtered.iter().take(50) {
                println!(
                    "  - {} ({}) [{}:{}]",
                    elem.qualified_name, elem.element_type, elem.file_path, elem.line_start
                );
            }
        }
    } else {
        println!(
            "Found {} element(s) in file '{}':",
            results.len(),
            file_path
        );
        for elem in results.iter().take(50) {
            println!(
                "  - {} ({}) [{}:{}]",
                elem.qualified_name, elem.element_type, elem.file_path, elem.line_start
            );
        }
        if results.len() > 50 {
            println!("  ... and {} more", results.len() - 50);
        }
    }

    Ok(())
}

/// Query functions by name (substring match). Supports the `--function` CLI flag.
fn run_function_query(
    func_name: &str,
    db_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = db::schema::init_db(db_path)?;
    let graph_engine = graph::GraphEngine::new(db);

    let results = graph_engine.search_by_name_typed(func_name, Some("function"), 50)?;
    if results.is_empty() {
        println!("No functions found with name matching '{}'", func_name);
    } else {
        println!(
            "Found {} function(s) matching '{}':",
            results.len(),
            func_name
        );
        for elem in results {
            println!(
                "  - {} [{}:{}]",
                elem.qualified_name, elem.file_path, elem.line_start
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
        // Scoped export: BFS traversal from file. We only need a
        // subset of elements so the per-file BFS is bounded and
        // materializing it is fine.
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

        let mut scoped_elements: Vec<_> = Vec::new();
        for_each_with_filter(
            &engine,
            |e| visited_files.contains(&e.file_path),
            |e| {
                scoped_elements.push(e);
            },
        )?;
        (scoped_elements, scoped_rels)
    } else if format == "json" {
        // For full-graph JSON export we stream directly to disk so
        // peak RAM is O(1) per element, not the 470 MB we used to
        // hold. Other formats (dot, mermaid) need the full Vec for
        // their string assembly, so they keep the legacy path which
        // is already bounded by the existing budget / cache code.
        export_json_streaming(output, &engine)?;
        println!("Exported streaming JSON to {}", output);
        return Ok(());
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

/// Stream every element through `f` if the predicate `p` accepts it.
/// Used by scoped exports where we need to filter to a subset of
/// the graph without materializing the full set.
fn for_each_with_filter<F, P>(
    engine: &graph::GraphEngine,
    p: P,
    mut f: F,
) -> Result<(), Box<dyn std::error::Error>>
where
    P: Fn(&db::models::CodeElement) -> bool,
    F: FnMut(db::models::CodeElement),
{
    let _ = engine.for_each_element(|e| {
        if p(&e) {
            f(e);
        }
        Ok::<(), Box<dyn std::error::Error>>(())
    })?;
    Ok(())
}

/// Stream-export the full graph to a JSON file at `out_path`. Peak
/// RAM is O(1) per element; the previous implementation materialized
/// a 470 MB JSON string for a 627k-element graph.
fn export_json_streaming(
    out_path: &str,
    engine: &graph::GraphEngine,
) -> Result<(), Box<dyn std::error::Error>> {
    use std::io::{BufWriter, Write};
    let f = std::fs::File::create(out_path)?;
    let mut out = BufWriter::new(f);
    let mut guard = crate::budget::BudgetGuard::for_tool("export_json_streaming");
    let mut total: u64 = 0;
    let mut truncated = false;

    writeln!(out, "{{")?;
    writeln!(out, "  \"version\": 1,")?;
    writeln!(out, "  \"kind\": \"leankg.graph.streaming\",")?;
    writeln!(out, "  \"elements\": [")?;
    let mut i: usize = 0;
    let stream_elements = engine.for_each_element(|e| {
        if i > 0 {
            writeln!(out, ",")?;
        }
        write!(
            out,
            "    {{\"qualified_name\":{:?},\"element_type\":{:?},\"name\":{:?},\
             \"file_path\":{:?},\"line_start\":{},\"line_end\":{},\"language\":{:?},\
             \"cluster_id\":{:?},\"cluster_label\":{:?},\"parent_qualified\":{:?},\
             \"metadata\":{}}}",
            e.qualified_name,
            e.element_type,
            e.name,
            e.file_path,
            e.line_start,
            e.line_end,
            e.language,
            e.cluster_id,
            e.cluster_label,
            e.parent_qualified,
            serde_json::to_string(&e.metadata).unwrap_or_else(|_| "null".to_string()),
        )?;
        i += 1;
        if i.is_multiple_of(1000) {
            guard.tick();
            if guard.check().is_err() {
                return Err(Box::new(std::io::Error::other("export budget")));
            }
        }
        Ok(())
    });
    if let Err(e) = stream_elements {
        if e.to_string().contains("budget") {
            truncated = true;
        } else {
            return Err(e);
        }
    } else {
        total += i as u64;
    }
    writeln!(out)?;
    writeln!(out, "  ],")?;
    writeln!(out, "  \"relationships\": [")?;
    let mut j: usize = 0;
    let stream_rels = engine.for_each_relationship(|r| {
        if j > 0 {
            writeln!(out, ",")?;
        }
        write!(
            out,
            "    {{\"source_qualified\":{:?},\"target_qualified\":{:?},\
             \"rel_type\":{:?},\"confidence\":{},\"metadata\":{}}}",
            r.source_qualified,
            r.target_qualified,
            r.rel_type,
            r.confidence,
            serde_json::to_string(&r.metadata).unwrap_or_else(|_| "null".to_string()),
        )?;
        j += 1;
        if j.is_multiple_of(1000) {
            guard.tick();
            if guard.check().is_err() {
                return Err(Box::new(std::io::Error::other("export budget")));
            }
        }
        Ok(())
    });
    if let Err(e) = stream_rels {
        if e.to_string().contains("budget") {
            truncated = true;
        } else {
            return Err(e);
        }
    } else {
        total += j as u64;
    }
    writeln!(out)?;
    writeln!(out, "  ]")?;
    if truncated {
        writeln!(out, "  ,\"truncated\": true")?;
    }
    writeln!(out, "}}")?;
    out.flush()?;
    let _ = total;
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

    // 2. Prefer ui-v2/dist (current explorer shell)
    let cwd_ui_v2 = std::path::Path::new("ui-v2/dist");
    if cwd_ui_v2.join("index.html").exists() {
        println!(
            "📦 Using UI from current directory: {}",
            cwd_ui_v2.display()
        );
        return Some(cwd_ui_v2.to_path_buf());
    }

    // 3. Legacy ui/dist (Phase-1 only; do not use for OnRender / releases)
    let cwd_ui = std::path::Path::new("ui/dist");
    if cwd_ui.join("index.html").exists() {
        println!("📦 Using UI from legacy ui/dist: {}", cwd_ui.display());
        return Some(cwd_ui.to_path_buf());
    }

    // 4. Check relative to the executable's directory
    if let Ok(exe_path) = std::env::current_exe() {
        if let Some(exe_dir) = exe_path.parent() {
            let share_v2 = exe_dir.join("../share/leankg/ui-v2/dist");
            if share_v2.join("index.html").exists() {
                let path = share_v2.canonicalize().ok().unwrap_or(share_v2);
                println!("📦 Using UI from share directory: {}", path.display());
                return Some(path);
            }
            let exe_v2 = exe_dir.join("ui-v2/dist");
            if exe_v2.join("index.html").exists() {
                let path = exe_v2.canonicalize().ok().unwrap_or(exe_v2);
                println!("📦 Using UI from executable directory: {}", path.display());
                return Some(path);
            }
            let share_ui = exe_dir.join("../share/leankg/ui/dist");
            if share_ui.join("index.html").exists() {
                let path = share_ui.canonicalize().ok().unwrap_or(share_ui);
                println!(
                    "📦 Using UI from legacy share directory: {}",
                    path.display()
                );
                return Some(path);
            }
            let exe_ui = exe_dir.join("ui/dist");
            if exe_ui.join("index.html").exists() {
                let path = exe_ui.canonicalize().ok().unwrap_or(exe_ui);
                println!(
                    "📦 Using UI from legacy executable directory: {}",
                    path.display()
                );
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
    let current_pid = std::process::id();

    for pattern in patterns {
        let mut retries = 0;
        loop {
            // Get all processes matching the pattern, excluding self
            let matching_pids: Vec<u32> = {
                let mut sys = System::new_all();
                sys.refresh_all();
                sys.processes()
                    .iter()
                    .filter(|(pid, process)| {
                        let cmd: String = process
                            .cmd()
                            .iter()
                            .map(|s| s.to_string_lossy().into_owned())
                            .collect::<Vec<_>>()
                            .join(" ");
                        pid.as_u32() != current_pid && cmd.contains(pattern)
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
                // SAFETY: Never kill ourselves - double-check even though filter should catch this
                if *pid == current_pid {
                    continue;
                }
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
            .filter(|(pid, process)| {
                let cmd: String = process
                    .cmd()
                    .iter()
                    .map(|s| s.to_string_lossy().into_owned())
                    .collect::<Vec<_>>()
                    .join(" ");
                pid.as_u32() != current_pid && (cmd.contains("leankg") || cmd.contains("vite"))
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

fn handle_incident_command(
    command: cli::IncidentCommand,
) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");

    match command {
        cli::IncidentCommand::Add {
            title,
            severity,
            affected,
            root_cause,
            resolution,
            prevention,
            env,
            ticket,
        } => {
            let now = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64;
            let incident = db::models::Incident {
                id: format!("INC-{}", uuid::Uuid::new_v4()),
                env,
                title,
                severity,
                occurred_at: now,
                resolved_at: Some(now),
                root_cause,
                resolution,
                affected_services: affected.split(',').map(|s| s.trim().to_string()).collect(),
                trigger_pattern: None,
                prevention,
                tags: vec![],
                author: std::env::var("USER").unwrap_or_else(|_| "unknown".to_string()),
                linked_ticket: ticket,
            };
            let db = db::schema::init_db(&db_path)?;
            db::create_incident(&db, &incident)?;
            println!("Created incident '{}' ({})", incident.id, incident.title);
            println!("  Severity: {}", incident.severity);
            println!("  Affected: {}", incident.affected_services.join(", "));
        }
        cli::IncidentCommand::List {
            service,
            env,
            pattern,
            limit,
        } => {
            let db = db::schema::init_db(&db_path)?;
            let incidents =
                db::query_incidents(&db, Some(&service), pattern.as_deref(), Some(&env), limit)?;
            if incidents.is_empty() {
                println!(
                    "No incidents found for service '{}' in env '{}'",
                    service, env
                );
            } else {
                println!(
                    "Found {} incident(s) for service '{}' (env: {}):",
                    incidents.len(),
                    service,
                    env
                );
                for inc in incidents {
                    println!("\n  ID:          {}", inc.id);
                    println!("  Title:       {}", inc.title);
                    println!("  Severity:    {}", inc.severity);
                    println!("  Occurred:    {}", inc.occurred_at);
                    println!("  Root Cause:  {}", inc.root_cause);
                    println!("  Resolution:  {}", inc.resolution);
                    if let Some(ref prev) = inc.prevention {
                        println!("  Prevention:  {}", prev);
                    }
                    if let Some(ref tk) = inc.linked_ticket {
                        println!("  Ticket:      {}", tk);
                    }
                }
            }
        }
        cli::IncidentCommand::Show { id } => {
            let db = db::schema::init_db(&db_path)?;
            match db::get_incident(&db, &id)? {
                Some(inc) => {
                    println!("Incident Details:");
                    println!("  ID:             {}", inc.id);
                    println!("  Title:          {}", inc.title);
                    println!("  Environment:    {}", inc.env);
                    println!("  Severity:       {}", inc.severity);
                    println!("  Occurred At:    {}", inc.occurred_at);
                    if let Some(ref resolved) = inc.resolved_at {
                        println!("  Resolved At:    {}", resolved);
                    }
                    println!("  Root Cause:     {}", inc.root_cause);
                    println!("  Resolution:     {}", inc.resolution);
                    println!("  Affected Svcs:  {}", inc.affected_services.join(", "));
                    if let Some(ref tp) = inc.trigger_pattern {
                        println!("  Trigger:        {}", tp);
                    }
                    if let Some(ref prev) = inc.prevention {
                        println!("  Prevention:     {}", prev);
                    }
                    println!("  Tags:           {}", inc.tags.join(", "));
                    println!("  Author:         {}", inc.author);
                    if let Some(ref tk) = inc.linked_ticket {
                        println!("  Ticket:         {}", tk);
                    }
                }
                None => {
                    println!("Incident '{}' not found", id);
                }
            }
        }
    }

    Ok(())
}

fn handle_team_command(command: cli::TeamCommand) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    let db = db::schema::init_db(&db_path)?;

    match command {
        cli::TeamCommand::Create {
            name,
            description,
            owner,
        } => {
            let now = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64;
            let team = db::models::Team {
                id: format!("TEAM-{}", uuid::Uuid::new_v4()),
                name,
                description,
                owner_id: owner,
                created_at: now,
                updated_at: now,
                graph_read_users: vec![],
                graph_write_users: vec![],
                members: vec![],
            };
            db::create_team(&db, &team)?;
            println!("Created team '{}' ({})", team.name, team.id);
            println!("  Owner: {}", team.owner_id);
        }
        cli::TeamCommand::List => {
            let teams = db::list_teams(&db)?;
            if teams.is_empty() {
                println!("No teams found");
            } else {
                for t in teams {
                    println!("\nTeam: {} ({})", t.name, t.id);
                    println!("  Owner: {}", t.owner_id);
                    println!("  Members: {}", t.members.len());
                    println!("  Read users: {}", t.graph_read_users.len());
                    println!("  Write users: {}", t.graph_write_users.len());
                }
            }
        }
        cli::TeamCommand::Show { id } => match db::get_team(&db, &id)? {
            Some(t) => {
                println!("Team Details:");
                println!("  ID:          {}", t.id);
                println!("  Name:        {}", t.name);
                println!("  Description: {}", t.description);
                println!("  Owner:       {}", t.owner_id);
                println!("  Created:     {}", t.created_at);
                println!("  Updated:     {}", t.updated_at);
                println!("  Members ({}):", t.members.len());
                for m in &t.members {
                    println!("    - {} ({})", m.user_id, m.role);
                }
                println!("  Graph Read Users:  {:?}", t.graph_read_users);
                println!("  Graph Write Users: {:?}", t.graph_write_users);
            }
            None => {
                println!("Team '{}' not found", id);
            }
        },
        cli::TeamCommand::Update {
            id,
            name,
            description,
        } => {
            if let Some(mut t) = db::get_team(&db, &id)? {
                if let Some(n) = name {
                    t.name = n;
                }
                if let Some(d) = description {
                    t.description = d;
                }
                t.updated_at = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_secs() as i64;
                db::update_team(&db, &t)?;
                println!("Updated team '{}'", id);
            } else {
                println!("Team '{}' not found", id);
            }
        }
        cli::TeamCommand::Delete { id } => {
            db::delete_team(&db, &id)?;
            println!("Deleted team '{}'", id);
        }
        cli::TeamCommand::AddMember { team, user, role } => {
            let t = db::add_team_member(&db, &team, &user, &role)?;
            println!("Added '{}' to team '{}' as {}", user, team, role);
            println!("  Team now has {} members", t.members.len());
        }
        cli::TeamCommand::RemoveMember { team, user } => {
            let t = db::remove_team_member(&db, &team, &user)?;
            println!("Removed '{}' from team '{}'", user, team);
            println!("  Team now has {} members", t.members.len());
        }
        cli::TeamCommand::Invite {
            team,
            role,
            email,
            expires_hours,
        } => {
            let now = std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_secs() as i64;
            let expires_at = now + (expires_hours as i64 * 3600);
            let token = uuid::Uuid::new_v4().to_string().replace("-", "");
            let invite = db::models::TeamInvite {
                token: token.clone(),
                team_id: team,
                email,
                role,
                created_by: std::env::var("USER").unwrap_or_else(|_| "unknown".to_string()),
                created_at: now,
                expires_at,
                accepted: false,
                accepted_by: None,
            };
            db::create_team_invite(&db, &invite)?;
            println!("Created invite token: {}", token);
            println!("  Expires in {} hours", expires_hours);
        }
        cli::TeamCommand::Accept { token, user } => {
            let invite = db::accept_team_invite(&db, &token, &user)?;
            println!("Accepted invite for team '{}'", invite.team_id);
            println!("User '{}' is now a {}", user, invite.role);
        }
        cli::TeamCommand::Invites { team } => {
            let invites = db::get_team_invites(&db, &team)?;
            if invites.is_empty() {
                println!("No pending invites for team '{}'", team);
            } else {
                for inv in invites {
                    let status = if inv.accepted { "ACCEPTED" } else { "PENDING" };
                    println!("\nInvite: {} [{}]", inv.token, status);
                    println!("  Role:      {}", inv.role);
                    println!("  Created:   {}", inv.created_at);
                    println!("  Expires:   {}", inv.expires_at);
                    if let Some(ref email) = inv.email {
                        println!("  Email:     {}", email);
                    }
                    if let Some(ref accepted_by) = inv.accepted_by {
                        println!("  Accepted: {}", accepted_by);
                    }
                }
            }
        }
        cli::TeamCommand::RevokeInvite { token } => {
            db::delete_team_invite(&db, &token)?;
            println!("Revoked invite '{}'", token);
        }
        cli::TeamCommand::SetReadUsers { team, users } => {
            if let Some(mut t) = db::get_team(&db, &team)? {
                t.graph_read_users = users.split(',').map(|s| s.trim().to_string()).collect();
                t.updated_at = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_secs() as i64;
                db::update_team(&db, &t)?;
                println!("Updated graph read users for team '{}'", team);
                println!("  Users: {:?}", t.graph_read_users);
            } else {
                println!("Team '{}' not found", team);
            }
        }
        cli::TeamCommand::SetWriteUsers { team, users } => {
            if let Some(mut t) = db::get_team(&db, &team)? {
                t.graph_write_users = users.split(',').map(|s| s.trim().to_string()).collect();
                t.updated_at = std::time::SystemTime::now()
                    .duration_since(std::time::UNIX_EPOCH)
                    .unwrap()
                    .as_secs() as i64;
                db::update_team(&db, &t)?;
                println!("Updated graph write users for team '{}'", team);
                println!("  Users: {:?}", t.graph_write_users);
            } else {
                println!("Team '{}' not found", team);
            }
        }
        cli::TeamCommand::CheckPermission { team, user, write } => {
            let has_perm = db::check_graph_permission(&db, &team, &user, write)?;
            if has_perm {
                println!(
                    "User '{}' has {} permission on team '{}'",
                    user,
                    if write { "write" } else { "read" },
                    team
                );
            } else {
                println!(
                    "User '{}' does NOT have {} permission on team '{}'",
                    user,
                    if write { "write" } else { "read" },
                    team
                );
            }
        }
    }

    Ok(())
}

fn add_note(target: &str, content: &str, env: &str) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    let db = db::schema::init_db(&db_path)?;

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    let entry = db::models::KnowledgeEntry {
        id: format!("NOTE-{}", uuid::Uuid::new_v4()),
        knowledge_type: "general".to_string(),
        title: format!("Note for {}", target),
        content: content.to_string(),
        element_qualified: Some(target.to_string()),
        user_story_id: None,
        feature_id: None,
        tags: "note".to_string(),
        environment: env.to_string(),
        branch: None,
        author: std::env::var("USER").unwrap_or_else(|_| "unknown".to_string()),
        created_at: now,
        updated_at: now,
    };

    db::create_knowledge_entry(&db, &entry)?;
    println!("Added note to '{}' (env: {})", target, env);
    println!("  Content: {}", content);

    Ok(())
}

fn add_pattern(
    title: &str,
    context: &str,
    solution: &str,
    env: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    let db = db::schema::init_db(&db_path)?;

    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs() as i64;

    let content = format!("## Context\n{}\n\n## Solution\n{}", context, solution);

    let entry = db::models::KnowledgeEntry {
        id: format!("PATTERN-{}", uuid::Uuid::new_v4()),
        knowledge_type: "debugging".to_string(),
        title: title.to_string(),
        content,
        element_qualified: None,
        user_story_id: None,
        feature_id: None,
        tags: "pattern,risk".to_string(),
        environment: env.to_string(),
        branch: None,
        author: std::env::var("USER").unwrap_or_else(|_| "unknown".to_string()),
        created_at: now,
        updated_at: now,
    };

    db::create_knowledge_entry(&db, &entry)?;
    println!("Added risky pattern '{}' (env: {})", title, env);
    println!("  Context:  {}", context);
    println!("  Solution: {}", solution);

    Ok(())
}

fn show_env_conflicts(service: &str) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");

    if !db_path.exists() {
        println!("LeanKG not initialized. Run 'leankg init' and 'leankg index' first.");
        return Ok(());
    }

    let db = db::schema::init_db(&db_path)?;
    let graph_engine = graph::GraphEngine::new(db.clone());

    // Find conflicts_with relationships involving this service
    let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], rel_type = "conflicts_with", (regex_matches(lowercase(source_qualified), $svc) or regex_matches(lowercase(target_qualified), $svc))"#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "svc".to_string(),
        serde_json::Value::String(format!(".*{}.*", regex::escape(&service.to_lowercase()))),
    );

    let mut found = false;
    match crate::db::schema::run_script(graph_engine.db(), query, params) {
        Ok(result) => {
            if !result.rows.is_empty() {
                found = true;
                println!("Environment conflicts for service '{}':", service);
                for row in &result.rows {
                    let source = row[0].get_str().unwrap_or("");
                    let target = row[1].get_str().unwrap_or("");
                    let conf = row[3].get_float().unwrap_or(0.0);
                    println!("  - {} <-> {} (confidence: {:.2})", source, target, conf);
                }
            }
        }
        Err(e) => {
            tracing::warn!("Conflict query failed: {}", e);
        }
    }

    // Also check for elements with same qualified_name but different env
    let env_query = r#"?[qualified_name, env, count(n)] := *code_elements[n, a, b, qualified_name, c, d, e, f, g, h, env, _] :group [qualified_name, env] :order count(n) desc"#;
    match crate::db::schema::run_script(graph_engine.db(), env_query, Default::default()) {
        Ok(result) => {
            let mut env_map: std::collections::HashMap<String, Vec<String>> =
                std::collections::HashMap::new();
            for row in &result.rows {
                let qn = row[0].get_str().unwrap_or("").to_string();
                let env = row[1].get_str().unwrap_or("").to_string();
                env_map.entry(qn).or_default().push(env);
            }

            let conflicts: Vec<_> = env_map
                .into_iter()
                .filter(|(_, envs)| envs.len() > 1)
                .filter(|(qn, _)| qn.to_lowercase().contains(&service.to_lowercase()))
                .collect();

            if !conflicts.is_empty() {
                found = true;
                println!("\nCross-environment element variants for '{}':", service);
                for (qn, envs) in conflicts.iter().take(20) {
                    println!("  - {} (envs: {})", qn, envs.join(", "));
                }
                if conflicts.len() > 20 {
                    println!("  ... and {} more", conflicts.len() - 20);
                }
            }
        }
        Err(e) => {
            tracing::warn!("Environment query failed: {}", e);
        }
    }

    if !found {
        println!("No environment conflicts found for service '{}'", service);
    }

    Ok(())
}

fn handle_ontology_command(
    command: cli::OntologyCommand,
) -> Result<(), Box<dyn std::error::Error>> {
    let project_path = find_project_root()?;
    let db_path = project_path.join(".leankg");
    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db.clone());
    let query_engine = crate::ontology::OntologyQueryEngine::new(db);

    match command {
        cli::OntologyCommand::Validate => {
            println!("Validating ontology YAML files...");
            let ontology_path = project_path.join("ontology");
            if !ontology_path.exists() {
                println!("No ontology directory found at {}. Run 'ontology sync' to create from example files.", ontology_path.display());
                return Ok(());
            }

            let concepts_file = ontology_path.join("concepts");
            let workflows_file = ontology_path.join("workflows");

            if concepts_file.exists() {
                println!("  concepts/ found");
            }
            if workflows_file.exists() {
                println!("  workflows/ found");
            }

            println!("Validation complete.");
        }

        cli::OntologyCommand::Sync { path } => {
            println!("Syncing ontology from YAML files...");
            let ontology_path = match path {
                Some(p) => std::path::PathBuf::from(p),
                None => project_path.join("ontology"),
            };

            if !ontology_path.exists() {
                println!(
                    "Creating example ontology directory at {}",
                    ontology_path.display()
                );
                std::fs::create_dir_all(&ontology_path)?;
                std::fs::create_dir_all(ontology_path.join("concepts"))?;
                std::fs::create_dir_all(ontology_path.join("workflows"))?;
                std::fs::create_dir_all(ontology_path.join("playbooks"))?;
                println!(
                    "Created ontology structure. Add YAML files to define concepts and workflows."
                );
                return Ok(());
            }

            let leankg = project_path.join(".leankg");
            match crate::ontology::sync_from_dir(&ontology_path, &graph, Some(&leankg)) {
                Ok(stats) => {
                    println!("  Loaded {} concept nodes", stats.concepts);
                    println!("  Loaded {} workflow nodes", stats.workflows);
                    println!("  Loaded {} workflow steps", stats.workflow_steps);
                    println!("  Loaded {} failure modes", stats.failure_modes);
                    println!("\nOntology sync complete");
                    println!(
                        "  Total nodes: {}",
                        stats.concepts
                            + stats.workflows
                            + stats.workflow_steps
                            + stats.failure_modes
                    );
                    println!("  Total relationships: {}", stats.relationships);
                    if let Some(m) = stats.marker_path {
                        println!("  Marker: {}", m);
                    }
                }
                Err(e) => {
                    eprintln!("Ontology sync failed: {}", e);
                    return Err(e);
                }
            }
        }

        cli::OntologyCommand::Status => {
            println!("Ontology Status\n===============");

            let status = query_engine.get_ontology_status()?;

            println!("\nConcept Nodes:");
            for (t, count) in &status.concept_counts {
                println!("  {}: {}", t, count);
            }

            println!("\nProcedural Nodes:");
            for (t, count) in &status.procedural_counts {
                println!("  {}: {}", t, count);
            }

            println!("\nTotal aliases: {}", status.total_aliases);
            println!("Nodes missing aliases: {}", status.nodes_missing_aliases);
            println!(
                "Workflows without failure modes: {}",
                status.workflows_without_failure_modes
            );
        }

        cli::OntologyCommand::Context { query, env, depth } => {
            let context = query_engine.get_ontology_context(&query, &env, depth)?;

            if context.matched_ontology_nodes.is_empty() {
                println!("No ontology nodes matched query '{}'", query);
                return Ok(());
            }

            println!("Matched Ontology Nodes:");
            for node in &context.matched_ontology_nodes {
                println!(
                    "  [{}] {} ({}) - score: {:.2}",
                    node.gid, node.name, node.element_type, node.match_score
                );
                println!("    Match reason: {}", node.match_reason);
            }

            if !context.workflows.is_empty() {
                println!("\nWorkflows:");
                for w in &context.workflows {
                    println!("  - {} ({})", w.name, w.gid);
                }
            }

            if !context.workflow_steps.is_empty() {
                println!("\nWorkflow Steps:");
                for s in &context.workflow_steps {
                    println!("  {}. {} ({})", s.order, s.name, s.gid);
                }
            }

            if !context.expanded_code_context.is_empty() {
                println!(
                    "\nRelated Code Elements ({}):",
                    context.expanded_code_context.len()
                );
                for elem in context.expanded_code_context.iter().take(10) {
                    println!("  - {} ({})", elem.qualified_name, elem.element_type);
                }
                if context.expanded_code_context.len() > 10 {
                    println!(
                        "  ... and {} more",
                        context.expanded_code_context.len() - 10
                    );
                }
            }

            println!("\nConfidence: {:.2}", context.confidence);
        }

        cli::OntologyCommand::ConceptMap { query, env } => {
            let nodes = query_engine.search_ontology_nodes(&query, &env, 2)?;

            if nodes.is_empty() {
                println!("No concept nodes matched query '{}'", query);
                return Ok(());
            }

            println!("Concept Map for '{}':", query);
            for node in &nodes {
                println!("  [{}] {} ({})", node.gid, node.name, node.element_type);
                if !node.aliases.is_empty() {
                    println!("    Aliases: {}", node.aliases.join(", "));
                }
                println!("    Description: {}", node.description);
            }

            println!("\nFound {} matching nodes", nodes.len());
        }

        cli::OntologyCommand::TraceWorkflow {
            workflow_id_or_query,
            env,
        } => {
            let steps = query_engine.trace_workflow(&workflow_id_or_query, &env)?;

            if steps.is_empty() {
                println!("No workflow found matching '{}'", workflow_id_or_query);
                return Ok(());
            }

            println!(
                "Workflow Trace: {} ({} steps)",
                workflow_id_or_query,
                steps.len()
            );
            for step in &steps {
                println!("\n  Step {}: {}", step.order, step.name);
                println!("    GID: {}", step.gid);
                if !step.metadata.code_refs.is_empty() {
                    println!("    Code refs: {}", step.metadata.code_refs.join(", "));
                }
                if !step.metadata.failure_modes.is_empty() {
                    println!(
                        "    Failure modes: {}",
                        step.metadata.failure_modes.join(", ")
                    );
                }
            }
        }

        cli::OntologyCommand::ConceptSearch { query, env, limit } => {
            let result = query_engine.concept_search(&query, &env, limit)?;

            println!("Concept Search: '{}'", result.query);
            println!(
                "Workflow: extract_keywords -> scan_concept_ontology -> load_concept -> query_db"
            );
            println!(
                "Extracted keywords: {}",
                if result.extracted_keywords.is_empty() {
                    "(none)".to_string()
                } else {
                    result.extracted_keywords.join(", ")
                }
            );

            if result.matched_concepts.is_empty() {
                println!("\nNo concept ontology nodes matched.");
            } else {
                println!("\nMatched Concepts ({}):", result.concept_match_count);
                for c in &result.matched_concepts {
                    println!(
                        "  [{:.2}] {} ({}) - {}",
                        c.match_score, c.name, c.element_type, c.gid
                    );
                    println!("        reason: {}", c.match_reason);
                    if !c.aliases.is_empty() {
                        println!("        aliases: {}", c.aliases.join(", "));
                    }
                    if !c.code_refs.is_empty() {
                        println!("        code_refs: {}", c.code_refs.join(", "));
                    }
                }
            }

            if result.linked_code_count > 0 {
                println!("\nLinked Code from DB ({}):", result.linked_code_count);
                for elem in result.linked_code.iter().take(20) {
                    println!(
                        "  - {} ({}) [{}:{}]",
                        elem.qualified_name, elem.element_type, elem.file_path, elem.line_start
                    );
                }
                if result.linked_code_count > 20 {
                    println!("  ... and {} more", result.linked_code_count - 20);
                }
            } else if result.concept_match_count > 0 {
                println!("\nNo indexed code elements resolved from concept code_refs.");
                println!("  (Make sure the referenced files are indexed: cargo run -- index)");
            }

            if result.fallback_used {
                println!(
                    "\nFallback name search results ({}):",
                    result.fallback_results.len()
                );
                for elem in result.fallback_results.iter().take(10) {
                    println!(
                        "  - {} ({}) [{}]",
                        elem.qualified_name, elem.element_type, elem.file_path
                    );
                }
            }
        }
    }

    Ok(())
}

#[cfg(feature = "embeddings")]
fn run_embed(
    init: bool,
    full: bool,
    batch_size: usize,
    project: &str,
    wait: bool,
    status: bool,
    cancel: bool,
    background: bool,
    workers: usize,
    types_filter: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    if init {
        let report = embeddings::init_models()?;
        println!("Models cached at: {}", report.cache_dir.display());
        println!();
        println!("Next steps:");
        println!("  cargo run --release -- index {project}");
        println!("  cargo run --release -- embed --project {project}");
        return Ok(());
    }

    let project_path = std::path::PathBuf::from(project);
    let leankg_dir = project_path.join(".leankg");
    let status_path = leankg_dir.join("embed_status.json");
    let lock_path = leankg_dir.join("embed.lock");

    // --status: print progress for an in-flight background embed and exit.
    if status {
        return run_embed_status(&status_path, &lock_path);
    }

    // --cancel: SIGTERM the background process and exit.
    if cancel {
        return run_embed_cancel(&lock_path);
    }

    if !leankg_dir.exists() {
        return Err(format!(
            "LeanKG project not initialized at {}. Run `cargo run --release -- index {}` first.",
            leankg_dir.display(),
            project
        )
        .into());
    }

    // Foreground mode (--wait) or background-spawned child (--background):
    // do the actual work. Everything else falls through to the spawn branch.
    if wait || background {
        return run_embed_worker(
            init,
            full,
            batch_size,
            &project_path,
            &leankg_dir,
            workers,
            types_filter,
        );
    }

    // Default: refuse to start if a previous background embed is still
    // running (lock file present + process alive). Otherwise spawn detached.
    if let Some(existing_pid) = read_lock_pid(&lock_path) {
        if pid_alive(existing_pid) {
            return Err(format!(
                "An embed is already running for this project (PID {}). \
                 Use `leankg embed --status` to check progress, or \
                 `leankg embed --cancel` to stop it before re-running.",
                existing_pid
            )
            .into());
        }
    }
    let _ = std::fs::remove_file(&lock_path);

    let exe = std::env::current_exe()?;
    let mut cmd = std::process::Command::new(exe);
    cmd.args([
        "embed",
        "--project",
        project,
        "--background",
        "--batch-size",
        &batch_size.to_string(),
        "--workers",
        &workers.to_string(),
    ]);
    if full {
        cmd.arg("--full");
    }
    if !types_filter.is_empty() {
        // Pass the user's --types override through to the child so the
        // background worker honors it (otherwise the mega-graph heuristic
        // re-derives a default that ignores the user's intent).
        cmd.args(["--types", types_filter]);
    }
    let child = cmd
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::null())
        .stderr(std::process::Stdio::null())
        .spawn()?;
    println!(
        "Embed started in background (PID {}). Pass --wait to run synchronously, \
         or use `leankg embed --status` to poll progress.",
        child.id()
    );
    Ok(())
}

#[cfg(feature = "embeddings")]
fn run_embed_status(
    status_path: &std::path::Path,
    lock_path: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    if !status_path.exists() {
        if let Some(pid) = read_lock_pid(lock_path) {
            println!(
                "Embed job (PID {}) is running but has not yet written a status file.",
                pid
            );
        } else {
            println!("No background embed in flight for this project.");
        }
        return Ok(());
    }
    let raw = std::fs::read_to_string(status_path)?;
    let v: serde_json::Value = serde_json::from_str(&raw).unwrap_or_default();
    let pid = v.get("pid").and_then(|x| x.as_u64()).unwrap_or(0);
    let started = v.get("started_at").and_then(|x| x.as_u64()).unwrap_or(0);
    let considered = v.get("considered").and_then(|x| x.as_u64()).unwrap_or(0);
    let embedded = v.get("embedded").and_then(|x| x.as_u64()).unwrap_or(0);
    let skipped = v.get("skipped_fresh").and_then(|x| x.as_u64()).unwrap_or(0);
    let orphans = v.get("orphans").and_then(|x| x.as_u64()).unwrap_or(0);
    let workers = v.get("workers").and_then(|x| x.as_u64()).unwrap_or(1);
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);
    let elapsed = now.saturating_sub(started);
    let rate = if elapsed > 0 {
        embedded as f64 / elapsed as f64
    } else {
        0.0
    };
    let eta = if rate > 0.0 {
        ((considered.saturating_sub(embedded + skipped)) as f64 / rate) as u64
    } else {
        0
    };
    let status = v
        .get("status")
        .and_then(|x| x.as_str())
        .unwrap_or("running");
    println!("Embed status: {}", status);
    println!("  PID:           {}", pid);
    println!("  Workers:       {}", workers);
    println!("  Elapsed:       {}s", elapsed);
    println!("  Considered:    {}", considered);
    println!("  Embedded:      {}", embedded);
    println!("  Skipped fresh: {}", skipped);
    println!("  Orphans:       {}", orphans);
    println!("  Rate:          {:.1} vectors/sec", rate);
    if eta > 0 && status == "running" {
        println!("  ETA:           {}s", eta);
    }
    Ok(())
}

#[cfg(feature = "embeddings")]
fn run_embed_cancel(lock_path: &std::path::Path) -> Result<(), Box<dyn std::error::Error>> {
    let Some(pid) = read_lock_pid(lock_path) else {
        println!("No background embed in flight for this project.");
        return Ok(());
    };
    if !pid_alive(pid) {
        let _ = std::fs::remove_file(lock_path);
        println!(
            "Stale embed lock (PID {}) removed; no live process found.",
            pid
        );
        return Ok(());
    }
    let self_pid = u64::from(std::process::id());
    if pid == self_pid {
        embeddings::request_cancel_in_process_embed();
        println!(
            "Requested cooperative cancel for in-process embed (PID {}).",
            pid
        );
        return Ok(());
    }
    // SAFETY: best-effort signal; we don't own the PID namespace.
    let ret = unsafe { libc_kill(pid, libc_SIGTERM) };
    if ret == 0 {
        println!("Sent SIGTERM to embed worker (PID {}).", pid);
    } else {
        return Err(format!("Failed to send SIGTERM to PID {}: errno={}", pid, ret).into());
    }
    Ok(())
}

#[cfg(feature = "embeddings")]
fn read_lock_pid(lock_path: &std::path::Path) -> Option<u64> {
    let raw = std::fs::read_to_string(lock_path).ok()?;
    raw.trim().parse::<u64>().ok()
}

#[cfg(feature = "embeddings")]
fn pid_alive(pid: u64) -> bool {
    // kill(pid, 0) is the canonical liveness check on POSIX.
    let ret = unsafe { libc_kill(pid, libc_SIGTERM) };
    // SIGTERM (0) to our own pid is non-fatal; just check ESRCH.
    let _ = ret;
    // Re-check with signal 0 (no-op) to avoid self-killing.
    let probe = unsafe { libc_kill(pid, 0) };
    probe == 0
        || (probe == -1 && std::io::Error::last_os_error().raw_os_error() != Some(3)) && pid != 0
}

// Minimal libc bindings — avoid adding a `libc` dep just for kill().
#[cfg(feature = "embeddings")]
unsafe fn libc_kill(pid: u64, sig: i32) -> i32 {
    extern "C" {
        fn kill(pid: i32, sig: i32) -> i32;
    }
    kill(pid as i32, sig)
}
#[cfg(feature = "embeddings")]
const libc_SIGTERM: i32 = 15;

#[cfg(feature = "embeddings")]
fn run_embed_worker(
    _init: bool,
    full: bool,
    batch_size: usize,
    _project_path: &std::path::Path,
    leankg_dir: &std::path::Path,
    workers: usize,
    types_filter: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    use std::io::Write;
    let status_path = leankg_dir.join("embed_status.json");
    let lock_path = leankg_dir.join("embed.lock");
    let db_path = leankg_dir.join("leankg.db");

    // Write the lock with our PID so the parent / --status / --cancel can find us.
    let pid = std::process::id();
    std::fs::write(&lock_path, pid.to_string())?;
    let started_at = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    let write_status =
        |considered: u64, embedded: u64, skipped: u64, orphans: u64, status: &str| {
            let body = serde_json::json!({
                "pid": pid,
                "started_at": started_at,
                "considered": considered,
                "embedded": embedded,
                "skipped_fresh": skipped,
                "orphans": orphans,
                "workers": workers,
                "status": status,
            });
            if let Ok(mut f) = std::fs::File::create(&status_path) {
                let _ = f.write_all(body.to_string().as_bytes());
            }
        };

    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db.clone());

    let mode = if full {
        embeddings::BuildMode::Full
    } else {
        embeddings::BuildMode::Incremental
    };
    // Default to `function,method` on mega-graphs to keep cold embed under
    // 5 min. Smaller workspaces embed every type. Pass `--types all` to
    // override and embed everything regardless of size.
    let parsed_filter = embeddings::parse_type_filter(types_filter);
    // Compute the element count ONCE — both the mega-graph heuristic and
    // the initial status payload need it, so we don't pay two full
    // `all_elements()` scans (which on a 400k-row workspace costs seconds
    // to tens of seconds).
    let total = graph.all_elements().map(|v| v.len()).unwrap_or(0);
    let opts = embeddings::BuildOptions {
        mode,
        batch_size,
        reserve_capacity: None,
        type_filter: match &parsed_filter {
            Some(_) => parsed_filter.clone(),
            None => {
                if total > 50_000 {
                    let mut set = std::collections::HashSet::new();
                    set.insert("function".to_string());
                    set.insert("method".to_string());
                    Some(set)
                } else {
                    None
                }
            }
        },
        ..Default::default()
    };
    write_status(total as u64, 0, 0, 0, "running");

    let started = std::time::Instant::now();
    // Parallel inference + sequential DB writes. Per-thread ONNX session
    // is the only safe pattern with fastembed (TextEmbedding is !Sync).
    let report: embeddings::BuildReport = if workers > 1 {
        embeddings::build_index_parallel(&graph, std::path::Path::new(""), &opts, workers)
            .map_err(|e| -> Box<dyn std::error::Error> { e.into() })?
    } else {
        embeddings::build_index(&graph, std::path::Path::new(""), &opts)?
    };
    let elapsed = started.elapsed();

    write_status(
        report.considered_count as u64,
        report.embedded_count as u64,
        report.skipped_fresh_count as u64,
        report.orphaned_count as u64,
        "completed",
    );
    let _ = std::fs::remove_file(&lock_path);

    // On a TTY this prints human output; otherwise only the status file
    // (which the parent / --status polls) is authoritative.
    if atty_stdout() {
        println!(
            "Embed build complete ({:?}) in {:.2}s ({} workers, batch {})",
            mode,
            elapsed.as_secs_f64(),
            workers,
            batch_size
        );
        println!("  Considered:    {}", report.considered_count);
        println!("  Embedded:      {}", report.embedded_count);
        println!("  Skipped fresh: {}", report.skipped_fresh_count);
        println!("  Orphans reaped: {}", report.orphaned_count);
        println!("  Index size:    {} vectors", report.index_size);
        println!("  Index path:    {}", report.index_path.display());
    }
    Ok(())
}

#[cfg(feature = "embeddings")]
fn atty_stdout() -> bool {
    use std::io::IsTerminal;
    // Best-effort: use std::io::IsTerminal when stable; otherwise assume TTY.
    std::io::stdout().is_terminal()
}

#[cfg(feature = "embeddings")]
#[allow(clippy::too_many_arguments)]
fn run_semantic_context(
    query: &str,
    env: &str,
    top_k: Option<usize>,
    rerank_top_n: usize,
    traverse: bool,
    include_worktrees: bool,
    include_ontology_steps: bool,
    debug: bool,
    project: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    use embeddings::RerankerStatus;

    let project_path = std::path::PathBuf::from(project);
    let leankg_dir = project_path.join(".leankg");
    let db_path = leankg_dir.join("leankg.db");

    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db.clone());

    // Vectors live inside CozoDB now (embedding_vectors relation + HNSW index),
    // so the freshness check is a single count query rather than a file stat.
    let has_vectors = crate::embeddings::state::list_all(&db)
        .map(|rows| !rows.is_empty())
        .unwrap_or(false);
    if !has_vectors {
        return Err(format!(
            "No embedded vectors in {}. Run `leankg embed --init` \
             (to download models), then `leankg embed` (to build the index).",
            db_path.display()
        )
        .into());
    }

    let pipeline = retrieval::SemanticRetrievalPipeline::new(db)?;
    let opts = retrieval::RetrieveOptions {
        env: Some(env.to_string()),
        ann_top_k: top_k,
        rerank_top_n,
        include_worktrees,
        include_ontology_steps,
        embeddings_stale: false,
    };
    let adaptive = top_k.is_none();

    let started = std::time::Instant::now();
    let retrieval = pipeline.retrieve(query, &opts)?;
    let retrieve_ms = started.elapsed().as_millis();

    println!("Query:   {}", query);
    println!(
        "Reranker: {}",
        match retrieval.reranker_status {
            RerankerStatus::Active => "active (bge-reranker-v2-m3)",
            RerankerStatus::Fallback => "FALLBACK (ANN-only)",
        }
    );
    println!();

    println!("Seeds ({}):", retrieval.seeds.len());
    for (i, s) in retrieval.seeds.iter().enumerate() {
        let score = s
            .rerank_score
            .map(|x| format!("rerank={:.4}", x))
            .unwrap_or_else(|| format!("ann={:.4}", s.ann_distance));
        println!(
            "  {:>2}. [{:<15}] {}  ({})",
            i + 1,
            s.element_type,
            s.qualified_name,
            score
        );
        if debug {
            println!("       blob: {}", s.blob_excerpt);
        }
    }

    if traverse && !retrieval.seeds.is_empty() {
        let t = std::time::Instant::now();
        let seeds_iter = retrieval
            .seeds
            .iter()
            .map(|s| (s.qualified_name.clone(), s.element_type.clone()));
        let result = graph::traversal::traverse_seeds(&graph, seeds_iter, Some(env))?;
        let trav_ms = t.elapsed().as_millis();

        println!();
        println!(
            "Traversed ({} neighbors, {} edges{}) in {}ms:",
            result.nodes.len(),
            result.edges.len(),
            if result.capped { ", CAPPED" } else { "" },
            trav_ms
        );
        for n in &result.nodes {
            println!(
                "  hop {} via {:<20} [{:<15}] {}  (from {})",
                n.hop, n.via_edge, n.element_type, n.qualified_name, n.from_seed
            );
        }
    }

    if debug {
        println!();
        println!("Diagnostics:");
        println!("  ANN candidates:        {}", retrieval.ann_candidate_count);
        println!(
            "ANN k used:            {} ({})",
            retrieval.ann_top_k_used,
            if adaptive { "adaptive" } else { "override" }
        );
        println!("Index size:            {}", retrieval.index_size);
        println!(
            "Worktree-filtered:     {}",
            retrieval.worktree_filtered_count
        );
        println!("Env-filtered:          {}", retrieval.env_filtered_count);
        println!("Test-filtered:         {}", retrieval.test_filtered_count);
        println!(
            "Node-type-filtered:    {}",
            retrieval.node_type_filtered_count
        );
        println!("Retrieve latency:      {}ms", retrieve_ms);
    }

    Ok(())
}

#[cfg(feature = "embeddings")]
fn run_smoke_test(project: &str) -> Result<(), Box<dyn std::error::Error>> {
    use embeddings::RerankerStatus;

    let project_path = std::path::PathBuf::from(project);
    let leankg_dir = project_path.join(".leankg");
    let db_path = leankg_dir.join("leankg.db");

    let db = db::schema::init_db(&db_path)?;
    let graph = graph::GraphEngine::new(db.clone());

    let has_vectors = crate::embeddings::state::list_all(&db)
        .map(|rows| !rows.is_empty())
        .unwrap_or(false);
    if !has_vectors {
        return Err(format!(
            "No embedded vectors in {}. Run `leankg embed --init` \
             (to download models), then `leankg embed` (to build the index) \
             before running the smoke test.",
            db_path.display()
        )
        .into());
    }

    let pipeline = retrieval::SemanticRetrievalPipeline::new(db)?;
    let queries = [
        "embedding inference for code elements",
        "how does the reranker score documents",
        "graph traversal for impact radius calculation",
        "MCP tool to query a file",
        "where do we filter out worktree paths",
    ];

    let env = "local";
    let mut passed = 0usize;
    let mut any_traversed = false;

    for (idx, q) in queries.iter().enumerate() {
        let label = format!("[{}/{}] \"{}\"", idx + 1, queries.len(), q);
        let mut failures: Vec<String> = Vec::new();
        let mut notes: Vec<String> = Vec::new();

        let result = (|| {
            let opts = retrieval::RetrieveOptions::default();
            let retrieval = pipeline.retrieve(q, &opts)?;

            let reranker_ok = match retrieval.reranker_status {
                RerankerStatus::Active => true,
                RerankerStatus::Fallback => {
                    notes.push("reranker=Fallback".to_string());
                    false
                }
            };
            if !reranker_ok {
                failures.push("reranker_status != Active (Fallback)".to_string());
            }

            let nonempty_seeds = retrieval
                .seeds
                .iter()
                .filter(|s| s.blob_excerpt.trim().len() > 0)
                .count();
            if nonempty_seeds < 3 {
                failures.push(format!(
                    "only {} seeds have non-empty blob_excerpt (need >=3)",
                    nonempty_seeds
                ));
            }

            let trav_neighbors = if retrieval.seeds.is_empty() {
                0
            } else {
                let seeds_iter = retrieval
                    .seeds
                    .iter()
                    .map(|s| (s.qualified_name.clone(), s.element_type.clone()));
                let result = graph::traversal::traverse_seeds(&graph, seeds_iter, Some(env))?;
                result.nodes.len()
            };
            if trav_neighbors > 0 {
                any_traversed = true;
            }

            Ok::<(), Box<dyn std::error::Error>>(())
        })();

        match result {
            Ok(()) => {
                if failures.is_empty() {
                    println!("PASS {}", label);
                    if !notes.is_empty() {
                        println!("     (warn: {})", notes.join(", "));
                    }
                    passed += 1;
                } else {
                    println!("FAIL {} — {}", label, failures.join("; "));
                }
            }
            Err(e) => {
                println!("FAIL {} — query error: {}", label, e);
            }
        }
    }

    if !any_traversed {
        println!(
            "FAIL (global): no query produced >=1 traversed neighbor \
             (Stage 4 traversal regression?)"
        );
    }

    println!();
    println!("Smoke test: {}/{} queries passed", passed, queries.len());

    let traversal_ok = any_traversed;
    if passed == queries.len() && traversal_ok {
        Ok(())
    } else {
        Err(format!(
            "Smoke test failed: {}/{} queries passed, traversal_ok={}",
            passed,
            queries.len(),
            traversal_ok
        )
        .into())
    }
}
