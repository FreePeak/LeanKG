//! Comprehensive CLI argument parsing coverage for every `CLICommand` variant.
//!
//! `tests/cli_tests.rs` already exercises Init / Index / Query / Serve / Web /
//! Impact / LspResolve / Path / McpStdio / McpHttp / Doctor / Reflect.
//! This file fills the remaining gaps: every command must parse without
//! panicking and reject unknown args.
//!
//! Run:
//! ```bash
//! cargo test --release --test cli_full_coverage_tests
//! ```

use clap::Parser;
use leankg::cli::CLICommand;

#[derive(Parser)]
struct TestArgs {
    #[command(subcommand)]
    command: CLICommand,
}

fn parse(args: &[&str]) -> Result<CLICommand, clap::Error> {
    let mut full = vec!["leankg"];
    full.extend_from_slice(args);
    TestArgs::try_parse_from(full).map(|t| t.command)
}

#[test]
fn version_parses() {
    let cmd = parse(&["version"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Version));
}

#[test]
fn update_parses() {
    let cmd = parse(&["update"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Update));
}

#[test]
fn query_parses_with_file() {
    let cmd = parse(&["query", "auth"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Query { .. }));
}

#[test]
fn generate_parses() {
    let cmd = parse(&["generate"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Generate { .. }));
}

#[test]
fn generate_with_template_parses() {
    let cmd = parse(&["generate", "--template", "wiki"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Generate { .. }));
}

#[test]
fn impact_parses() {
    let cmd = parse(&["impact", "src/lib.rs", "--depth", "2"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Impact { .. }));
}

#[test]
fn explain_parses() {
    let cmd = parse(&["explain", "src/lib.rs::main"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Explain { .. }));
}

#[test]
fn gods_parses() {
    let cmd = parse(&["gods", "--limit", "5"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Gods { .. }));
}

#[test]
fn report_parses() {
    let cmd = parse(&["report"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Report { .. }));
}

#[test]
fn check_consistency_parses() {
    let cmd = parse(&["check-consistency"]).expect("parse");
    assert!(matches!(cmd, CLICommand::CheckConsistency { .. }));
}

#[test]
fn lsp_resolve_parses() {
    let cmd = parse(&["lsp-resolve", "--language", "go", "src/main.go"]).expect("parse");
    assert!(matches!(cmd, CLICommand::LspResolve { .. }));
}

#[test]
fn lsp_install_parses() {
    let cmd = parse(&["lsp-install", "go"]).expect("parse");
    assert!(matches!(cmd, CLICommand::LspInstall { .. }));
}

#[test]
fn lsp_list_parses() {
    let cmd = parse(&["lsp-list"]).expect("parse");
    assert!(matches!(cmd, CLICommand::LspList));
}

#[test]
fn tunnels_parses() {
    let cmd = parse(&["tunnels"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Tunnels { .. }));
}

#[test]
fn reflect_parses() {
    let cmd = parse(&["reflect", "what is X", "useful"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Reflect { .. }));
}

#[test]
fn prs_parses() {
    let cmd = parse(&["prs"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Prs { .. }));
}

#[test]
fn clones_parses() {
    let cmd = parse(&["clones"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Clones { .. }));
}

#[test]
fn doctor_parses() {
    let cmd = parse(&["doctor"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Doctor { .. }));
}

#[test]
fn watch_parses() {
    let cmd = parse(&["watch"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Watch { .. }));
}

#[test]
fn quality_parses() {
    let cmd = parse(&["quality"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Quality { .. }));
}

#[cfg(feature = "embeddings")]
#[test]
fn embed_parses_with_options() {
    let cmd = parse(&[
        "embed",
        "--wait",
        "--types",
        "function,method",
        "--workers",
        "4",
        "--batch-size",
        "64",
    ])
    .expect("parse");
    assert!(matches!(cmd, CLICommand::Embed { .. }));
}

#[cfg(feature = "embeddings")]
#[test]
fn semantic_context_parses() {
    let cmd = parse(&["semantic-context", "auth flow"]).expect("parse");
    assert!(matches!(cmd, CLICommand::SemanticContext { .. }));
}

#[cfg(feature = "embeddings")]
#[test]
fn smoke_test_parses() {
    let cmd = parse(&["smoke-test"]).expect("parse");
    assert!(matches!(cmd, CLICommand::SmokeTest { .. }));
}

#[test]
fn export_parses_with_format() {
    let cmd = parse(&["export", "--format", "graphml"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Export { .. }));
}

#[test]
fn annotate_parses() {
    let cmd = parse(&[
        "annotate",
        "src/lib.rs::main",
        "--description",
        "entry point",
    ])
    .expect("parse");
    assert!(matches!(cmd, CLICommand::Annotate { .. }));
}

#[test]
fn link_parses() {
    let cmd = parse(&["link", "src/a.rs::alpha", "US-01"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Link { .. }));
}

#[test]
fn search_annotations_parses() {
    let cmd = parse(&["search-annotations", "bcrypt"]).expect("parse");
    assert!(matches!(cmd, CLICommand::SearchAnnotations { .. }));
}

#[test]
fn show_annotations_parses() {
    let cmd = parse(&["show-annotations", "src/lib.rs"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ShowAnnotations { .. }));
}

#[test]
fn trace_parses() {
    let cmd = parse(&["trace", "--feature", "F-01"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Trace { .. }));
}

#[test]
fn trace_all_parses() {
    let cmd = parse(&["trace", "--all"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Trace { .. }));
}

#[test]
fn find_by_domain_parses() {
    let cmd = parse(&["find-by-domain", "auth"]).expect("parse");
    assert!(matches!(cmd, CLICommand::FindByDomain { .. }));
}

#[test]
fn benchmark_parses() {
    let cmd = parse(&["benchmark", "--category", "smoke"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Benchmark { .. }));
}

#[test]
fn tool_bench_parses() {
    let cmd = parse(&["tool-bench"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ToolBench { .. }));
}

#[test]
fn ab_test_parses() {
    let cmd = parse(&["ab-test"]).expect("parse");
    assert!(matches!(cmd, CLICommand::AbTest { .. }));
}

#[test]
fn benchmark_unified_parses() {
    let cmd = parse(&["benchmark-unified"]).expect("parse");
    assert!(matches!(cmd, CLICommand::BenchmarkUnified { .. }));
}

#[test]
fn register_parses() {
    let cmd = parse(&["register", "demo"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Register { .. }));
}

#[test]
fn unregister_parses() {
    let cmd = parse(&["unregister", "demo"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Unregister { .. }));
}

#[test]
fn list_parses() {
    let cmd = parse(&["list"]).expect("parse");
    assert!(matches!(cmd, CLICommand::List));
}

#[test]
fn status_repo_parses() {
    let cmd = parse(&["status-repo", "demo"]).expect("parse");
    assert!(matches!(cmd, CLICommand::StatusRepo { .. }));
}

#[test]
fn setup_parses() {
    let cmd = parse(&["setup"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Setup { .. }));
}

#[test]
fn run_parses() {
    let cmd = parse(&["run", "auth"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Run { .. }));
}

#[test]
fn detect_clusters_parses() {
    let cmd = parse(&["detect-clusters"]).expect("parse");
    assert!(matches!(cmd, CLICommand::DetectClusters { .. }));
}

#[test]
fn api_serve_parses() {
    let cmd = parse(&["api-serve", "--port", "8080"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ApiServe { .. }));
}

#[test]
fn api_key_create_parses() {
    let cmd = parse(&["api-key", "create", "--name", "ci"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ApiKey { .. }));
}

#[test]
fn api_key_list_parses() {
    let cmd = parse(&["api-key", "list"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ApiKey { .. }));
}

#[test]
fn api_key_revoke_parses() {
    let cmd = parse(&["api-key", "revoke", "--id", "kid"]).expect("parse");
    assert!(matches!(cmd, CLICommand::ApiKey { .. }));
}

#[test]
fn obsidian_init_parses() {
    let cmd = parse(&["obsidian", "init"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Obsidian { .. }));
}

#[test]
fn obsidian_push_parses() {
    let cmd = parse(&["obsidian", "push"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Obsidian { .. }));
}

#[test]
fn metrics_parses() {
    let cmd = parse(&["metrics"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Metrics { .. }));
}

#[test]
fn proc_status_parses() {
    let cmd = parse(&["proc", "status"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Proc { .. }));
}

#[test]
fn proc_kill_parses() {
    let cmd = parse(&["proc", "kill"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Proc { .. }));
}

#[test]
fn incident_parses() {
    let cmd = parse(&["incident", "show", "inc-1"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Incident { .. }));
}

#[test]
fn note_parses() {
    let cmd = parse(&[
        "note",
        "--target",
        "src/lib.rs",
        "--content",
        "review before merge",
    ])
    .expect("parse");
    assert!(matches!(cmd, CLICommand::Note { .. }));
}

#[test]
fn pattern_parses() {
    let cmd = parse(&[
        "pattern",
        "--title",
        "x",
        "--context",
        "y",
        "--solution",
        "z",
    ])
    .expect("parse");
    assert!(matches!(cmd, CLICommand::Pattern { .. }));
}

#[test]
fn env_conflicts_parses() {
    let cmd = parse(&["env-conflicts", "--service", "api"]).expect("parse");
    assert!(matches!(cmd, CLICommand::EnvConflicts { .. }));
}

#[test]
fn push_parses() {
    let cmd = parse(&["push", "--remote", "https://x", "--token", "t"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Push { .. }));
}

#[test]
fn pull_parses() {
    let cmd = parse(&["pull", "--remote", "https://x", "--token", "t"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Pull { .. }));
}

#[test]
fn team_create_parses() {
    let cmd = parse(&[
        "team",
        "create",
        "--name",
        "core",
        "--description",
        "x",
        "--owner",
        "alice",
    ])
    .expect("parse");
    assert!(matches!(cmd, CLICommand::Team { .. }));
}

#[test]
fn team_list_parses() {
    let cmd = parse(&["team", "list"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Team { .. }));
}

#[test]
fn team_add_member_parses() {
    let cmd = parse(&["team", "add-member", "--team", "core", "--user", "alice"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Team { .. }));
}

#[test]
fn ontology_parses() {
    let cmd = parse(&["ontology", "status"]).expect("parse");
    assert!(matches!(cmd, CLICommand::Ontology { .. }));
}

// ---------------------------------------------------------------------------
// Negative cases: every command must reject unknown subcommands/args.
// ---------------------------------------------------------------------------

#[test]
fn unknown_subcommand_is_rejected() {
    assert!(parse(&["definitely-not-a-command"]).is_err());
}

#[test]
fn missing_required_value_is_rejected() {
    // `impact` requires a file positional argument.
    assert!(parse(&["impact"]).is_err());
}

#[test]
fn duplicate_flag_is_rejected() {
    // clap will fail on duplicate --depth.
    assert!(parse(&["index", "--depth", "2", "--depth", "3"]).is_err());
}
