//! Query-only MCP CLI surface for the `leankg-mcp` binary.
//!
//! Accepts only `mcp-http` / `mcp-stdio` and defaults to read-only.

use clap::{Parser, Subcommand};

use crate::cli::CLICommand;

/// Top-level args for `leankg-mcp`.
#[derive(Parser, Debug)]
#[command(name = "leankg-mcp")]
#[command(version)]
#[command(about = "LeanKG MCP server (query-only, read-only)")]
pub struct McpArgs {
    #[command(subcommand)]
    pub command: McpCommand,
}

/// MCP transport subcommands. Pipeline commands (`index`, `embed`, …) are
/// intentionally absent — use `leankg-worker`.
#[derive(Subcommand, Debug)]
pub enum McpCommand {
    /// Start MCP server with stdio transport
    McpStdio {
        /// Enable auto-indexing with file watcher (discouraged on query-only MCP)
        #[arg(long)]
        watch: bool,
        /// Open the database in read-only mode (reject all write tools).
        /// Defaults to true for `leankg-mcp`.
        #[arg(long, default_value_t = true)]
        read_only: bool,
    },
    /// Start MCP server with HTTP transport
    McpHttp {
        /// Port to listen on (default: 9699)
        #[arg(long)]
        port: Option<u16>,
        /// Bearer token for authentication (optional)
        #[arg(long)]
        auth: Option<String>,
        /// Enable auto-indexing with file watcher (discouraged on query-only MCP)
        #[arg(long)]
        watch: bool,
        /// Reuse existing server if already running
        #[arg(long)]
        reuse: bool,
        /// Project root directory (default: auto-detect from cwd)
        #[arg(long)]
        project: Option<String>,
        /// Open the database in read-only mode (reject all write tools).
        /// Defaults to true for `leankg-mcp`.
        #[arg(long, default_value_t = true)]
        read_only: bool,
    },
}

impl McpCommand {
    /// Map to the shared `CLICommand` used by the compat `leankg` facade.
    /// Always forces `read_only = true` (product contract for this binary).
    pub fn into_cli_command(self) -> CLICommand {
        match self {
            McpCommand::McpStdio {
                watch,
                read_only: _,
            } => CLICommand::McpStdio {
                watch,
                read_only: true,
            },
            McpCommand::McpHttp {
                port,
                auth,
                watch,
                reuse,
                project,
                read_only: _,
            } => CLICommand::McpHttp {
                port,
                auth,
                watch,
                reuse,
                project,
                read_only: true,
            },
        }
    }

    /// Argv tail for re-executing the compat `leankg` binary.
    pub fn to_leankg_argv(&self) -> Vec<String> {
        match self {
            McpCommand::McpStdio { watch, .. } => {
                let mut v = vec!["mcp-stdio".to_string(), "--read-only".to_string()];
                if *watch {
                    v.push("--watch".to_string());
                }
                v
            }
            McpCommand::McpHttp {
                port,
                auth,
                watch,
                reuse,
                project,
                ..
            } => {
                let mut v = vec!["mcp-http".to_string(), "--read-only".to_string()];
                if let Some(p) = port {
                    v.push("--port".to_string());
                    v.push(p.to_string());
                }
                if let Some(a) = auth {
                    v.push("--auth".to_string());
                    v.push(a.clone());
                }
                if *watch {
                    v.push("--watch".to_string());
                }
                if *reuse {
                    v.push("--reuse".to_string());
                }
                if let Some(p) = project {
                    v.push("--project".to_string());
                    v.push(p.clone());
                }
                v
            }
        }
    }
}
