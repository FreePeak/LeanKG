//! Contract tests for source ingestion, document/code indexing, and CLI surface.
//!
//! These tests run without network access. Live and benchmark tests live in
//! `remote_sources_live_tests.rs` and require explicit opt-in.

use clap::Parser;
use leankg::cli::CLICommand;
use leankg::sources::{parse_source_uri, SourceFactory, SourceUri};

#[derive(Parser)]
struct TestArgs {
    #[command(subcommand)]
    command: CLICommand,
}

#[test]
fn source_uri_contract_covers_gitlab_and_gcs() {
    assert_eq!(
        parse_source_uri("git+https://placeholder.example/group/repo.git").unwrap(),
        SourceUri::Git {
            url: "https://placeholder.example/group/repo.git".into(),
        }
    );
    assert_eq!(
        parse_source_uri("gs://bucket/prefix").unwrap(),
        SourceUri::Gcs {
            bucket: "bucket".into(),
            prefix: "prefix".into(),
        }
    );
}

#[test]
fn unsupported_backends_fail_before_network_access() {
    let result = SourceFactory::create(
        &SourceUri::S3 {
            bucket: "bucket".into(),
            prefix: "prefix".into(),
        },
        None,
        None,
    );
    assert!(result.is_err());
}

#[test]
fn cli_contract_accepts_remote_index_options() {
    let args = TestArgs::try_parse_from([
        "leankg",
        "index",
        "--source",
        "git+https://placeholder.example/group/repo.git",
        "--ref-name",
        "main",
        "--auth",
        "token",
        "--benchmark",
    ])
    .expect("remote index CLI contract changed");

    match args.command {
        CLICommand::Index {
            source,
            ref_name,
            auth,
            benchmark,
            ..
        } => {
            assert_eq!(
                source.as_deref(),
                Some("git+https://placeholder.example/group/repo.git")
            );
            assert_eq!(ref_name.as_deref(), Some("main"));
            assert_eq!(auth.as_deref(), Some("token"));
            assert!(benchmark);
        }
        _ => panic!("expected Index command"),
    }
}

#[test]
#[cfg(feature = "embeddings")]
fn cli_contract_accepts_refresh_and_index_docs() {
    assert!(TestArgs::try_parse_from(["leankg", "refresh"]).is_ok());
    assert!(TestArgs::try_parse_from(["leankg", "index-docs", "--path", "docs"]).is_ok());
}

#[test]
fn cli_contract_accepts_index_docs_without_embeddings() {
    assert!(TestArgs::try_parse_from(["leankg", "index-docs", "--path", "docs"]).is_ok());
}

#[test]
fn cli_contract_accepts_remote_watch() {
    assert!(TestArgs::try_parse_from([
        "leankg",
        "watch",
        "--source",
        "git+https://placeholder.example/group/repo.git",
        "--interval",
        "60",
    ])
    .is_ok());
}
