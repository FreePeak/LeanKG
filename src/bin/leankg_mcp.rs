//! `leankg-mcp` — query-only MCP server (read-only by default).
//!
//! Thin wrapper: parses the MCP-only CLI surface, then re-execs the compat
//! `leankg` binary with `--read-only` forced so all handlers stay in main.rs.

use clap::Parser;
use leankg::cli::mcp::McpArgs;
use leankg::cli::reexec::reexec_leankg;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = McpArgs::parse();
    let argv = args.command.to_leankg_argv();
    reexec_leankg(&argv)
}
