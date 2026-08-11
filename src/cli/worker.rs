//! Pipeline CLI surface for the `leankg-worker` binary.
//!
//! Owns index / embed / watch / status. MCP transports are intentionally absent.

use clap::{Parser, Subcommand};

use crate::cli::CLICommand;

/// Top-level args for `leankg-worker`.
#[derive(Parser, Debug)]
#[command(name = "leankg-worker")]
#[command(version)]
#[command(about = "LeanKG pipeline worker (index / embed / watch)")]
pub struct WorkerArgs {
    #[command(subcommand)]
    pub command: WorkerCommand,
}

/// Worker pipeline subcommands. MCP server commands are rejected at parse time.
#[derive(Subcommand, Debug)]
pub enum WorkerCommand {
    /// Index the codebase
    Index {
        /// Path to index
        path: Option<String>,
        #[arg(long, short)]
        incremental: bool,
        /// Filter by language (e.g., go,ts,py)
        #[arg(long, short)]
        lang: Option<String>,
        /// Exclude patterns (comma-separated)
        #[arg(long)]
        exclude: Option<String>,
        /// Verbose output
        #[arg(long, short)]
        verbose: bool,
        /// Target environment (local, staging, production)
        #[arg(long, default_value = "local")]
        env: String,
        /// Service name for this index
        #[arg(long)]
        service_name: Option<String>,
        /// Version tag for this index (semver or git sha)
        #[arg(long)]
        version: Option<String>,
        /// Source URI for remote indexing
        #[arg(long)]
        source: Option<String>,
        /// Git branch/tag/commit to check out (used with --source git+...)
        #[arg(long)]
        ref_name: Option<String>,
        /// Auth credential for remote sources
        #[arg(long)]
        auth: Option<String>,
        /// Run live A/B benchmark before and after indexing
        #[arg(long)]
        benchmark: bool,
    },
    /// Build or refresh the embedding index
    Embed {
        /// Download the embedding + reranker models to the cache and exit
        #[arg(long)]
        init: bool,
        /// Ignore embedding_state freshness and re-embed every node
        #[arg(long)]
        full: bool,
        /// Override the embedding batch size (default 32)
        #[arg(long, default_value = "32")]
        batch_size: usize,
        /// Project root (defaults to current working directory)
        #[arg(long, default_value = ".")]
        project: String,
        /// Wait for the embed to complete in the foreground
        #[arg(long)]
        wait: bool,
        /// Print progress for an in-flight background embed and exit
        #[arg(long)]
        status: bool,
        /// Cancel an in-flight background embed and exit
        #[arg(long)]
        cancel: bool,
        /// Internal: set by the parent when re-spawning as a background worker
        #[arg(long, hide = true)]
        background: bool,
        /// Number of parallel ONNX inference workers (default 2)
        #[arg(long, default_value = "2")]
        workers: usize,
        /// Comma-separated list of element types to embed
        #[arg(long, default_value = "")]
        types: String,
        /// Run live A/B benchmark measuring semantic search quality
        #[arg(long)]
        benchmark: bool,
    },
    /// Start file watcher for incremental re-indexing
    Watch {
        /// Path to watch (default: project root)
        #[arg(long)]
        path: Option<String>,
        /// Remote source URI (git+https:// or gs://)
        #[arg(long)]
        source: Option<String>,
        /// Ref name for git sources (default: main)
        #[arg(long)]
        ref_name: Option<String>,
        /// Poll interval in seconds (default: 60). Only used with --source
        #[arg(long, default_value = "60")]
        interval: u64,
        /// Auth credential for the remote source
        #[arg(long)]
        auth: Option<String>,
        /// Also run embed after each detected change
        #[arg(long)]
        embed: bool,
    },
    /// Show index status
    Status,
}

impl WorkerCommand {
    /// Map to the shared `CLICommand` used by the compat `leankg` facade.
    ///
    /// `Embed` requires the `embeddings` feature on the compat binary; without
    /// it this returns an error string instead of a command.
    pub fn try_into_cli_command(self) -> Result<CLICommand, String> {
        match self {
            WorkerCommand::Index {
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
                benchmark,
            } => Ok(CLICommand::Index {
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
                benchmark,
            }),
            WorkerCommand::Watch {
                path,
                source,
                ref_name,
                interval,
                auth,
                embed,
            } => Ok(CLICommand::Watch {
                path,
                source,
                ref_name,
                interval,
                auth,
                embed,
            }),
            WorkerCommand::Status => Ok(CLICommand::Status),
            WorkerCommand::Embed {
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
                benchmark,
            } => {
                #[cfg(feature = "embeddings")]
                {
                    Ok(CLICommand::Embed {
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
                        benchmark,
                        no_vectors: false,
                    })
                }
                #[cfg(not(feature = "embeddings"))]
                {
                    let _ = (
                        init, full, batch_size, project, wait, status, cancel, background, workers,
                        types, benchmark,
                    );
                    Err(
                        "embed requires building with `--features embeddings` (compat leankg / leankg-worker)"
                            .to_string(),
                    )
                }
            }
        }
    }

    /// Argv tail for re-executing the compat `leankg` binary.
    pub fn to_leankg_argv(&self) -> Vec<String> {
        match self {
            WorkerCommand::Index {
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
                benchmark,
            } => {
                let mut v = vec!["index".to_string()];
                if let Some(p) = path {
                    v.push(p.clone());
                }
                if *incremental {
                    v.push("--incremental".to_string());
                }
                if let Some(l) = lang {
                    v.push("--lang".to_string());
                    v.push(l.clone());
                }
                if let Some(e) = exclude {
                    v.push("--exclude".to_string());
                    v.push(e.clone());
                }
                if *verbose {
                    v.push("--verbose".to_string());
                }
                v.push("--env".to_string());
                v.push(env.clone());
                if let Some(s) = service_name {
                    v.push("--service-name".to_string());
                    v.push(s.clone());
                }
                if let Some(ver) = version {
                    v.push("--version".to_string());
                    v.push(ver.clone());
                }
                if let Some(s) = source {
                    v.push("--source".to_string());
                    v.push(s.clone());
                }
                if let Some(r) = ref_name {
                    v.push("--ref-name".to_string());
                    v.push(r.clone());
                }
                if let Some(a) = auth {
                    v.push("--auth".to_string());
                    v.push(a.clone());
                }
                if *benchmark {
                    v.push("--benchmark".to_string());
                }
                v
            }
            WorkerCommand::Embed {
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
                benchmark,
            } => {
                let mut v = vec!["embed".to_string()];
                if *init {
                    v.push("--init".to_string());
                }
                if *full {
                    v.push("--full".to_string());
                }
                v.push("--batch-size".to_string());
                v.push(batch_size.to_string());
                v.push("--project".to_string());
                v.push(project.clone());
                if *wait {
                    v.push("--wait".to_string());
                }
                if *status {
                    v.push("--status".to_string());
                }
                if *cancel {
                    v.push("--cancel".to_string());
                }
                if *background {
                    v.push("--background".to_string());
                }
                v.push("--workers".to_string());
                v.push(workers.to_string());
                if !types.is_empty() {
                    v.push("--types".to_string());
                    v.push(types.clone());
                }
                if *benchmark {
                    v.push("--benchmark".to_string());
                }
                v
            }
            WorkerCommand::Watch {
                path,
                source,
                ref_name,
                interval,
                auth,
                embed,
            } => {
                let mut v = vec!["watch".to_string()];
                if let Some(p) = path {
                    v.push("--path".to_string());
                    v.push(p.clone());
                }
                if let Some(s) = source {
                    v.push("--source".to_string());
                    v.push(s.clone());
                }
                if let Some(r) = ref_name {
                    v.push("--ref-name".to_string());
                    v.push(r.clone());
                }
                v.push("--interval".to_string());
                v.push(interval.to_string());
                if let Some(a) = auth {
                    v.push("--auth".to_string());
                    v.push(a.clone());
                }
                if *embed {
                    v.push("--embed".to_string());
                }
                v
            }
            WorkerCommand::Status => vec!["status".to_string()],
        }
    }
}
