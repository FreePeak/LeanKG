//! `leankg-worker` — pipeline process (index / embed / watch / status).
//!
//! Thin wrapper: parses the worker-only CLI surface, then re-execs the compat
//! `leankg` binary so indexer/embed handlers stay in main.rs.

use clap::Parser;
use leankg::cli::reexec::reexec_leankg;
use leankg::cli::worker::WorkerArgs;

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args = WorkerArgs::parse();
    let argv = args.command.to_leankg_argv();
    reexec_leankg(&argv)
}
