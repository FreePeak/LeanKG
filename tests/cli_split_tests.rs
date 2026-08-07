//! CLI surface for the split binaries: query-only `leankg-mcp` and pipeline
//! `leankg-worker`. Parse-only — does not start servers or run the indexer.
//!
//! Run:
//! ```bash
//! cargo test --test cli_split_tests -- --nocapture
//! ```

use clap::Parser;
use leankg::cli::mcp::{McpArgs, McpCommand};
use leankg::cli::worker::{WorkerArgs, WorkerCommand};

#[test]
fn mcp_cli_parses_mcp_http_and_stdio() {
    let http = McpArgs::try_parse_from(["leankg-mcp", "mcp-http", "--port", "9700"]).unwrap();
    match http.command {
        McpCommand::McpHttp {
            port,
            read_only,
            watch,
            ..
        } => {
            assert_eq!(port, Some(9700));
            assert!(read_only, "leankg-mcp must default to read-only");
            assert!(!watch);
        }
        other => panic!("expected McpHttp, got {other:?}"),
    }

    let stdio = McpArgs::try_parse_from(["leankg-mcp", "mcp-stdio"]).unwrap();
    match stdio.command {
        McpCommand::McpStdio { read_only, watch } => {
            assert!(read_only, "leankg-mcp must default to read-only");
            assert!(!watch);
        }
        other => panic!("expected McpStdio, got {other:?}"),
    }
}

#[test]
fn mcp_cli_rejects_index_subcommand() {
    let err = McpArgs::try_parse_from(["leankg-mcp", "index"]).unwrap_err();
    let msg = err.to_string();
    assert!(
        msg.contains("unrecognized subcommand")
            || msg.contains("unexpected")
            || msg.contains("index"),
        "expected parse rejection of index, got: {msg}"
    );
}

#[test]
fn mcp_cli_defaults_or_requires_read_only() {
    let http = McpArgs::try_parse_from(["leankg-mcp", "mcp-http"]).unwrap();
    match http.command {
        McpCommand::McpHttp { read_only, .. } => {
            assert!(read_only, "mcp-http must default read_only=true");
        }
        other => panic!("expected McpHttp, got {other:?}"),
    }

    let stdio = McpArgs::try_parse_from(["leankg-mcp", "mcp-stdio"]).unwrap();
    match stdio.command {
        McpCommand::McpStdio { read_only, .. } => {
            assert!(read_only, "mcp-stdio must default read_only=true");
        }
        other => panic!("expected McpStdio, got {other:?}"),
    }

    // Explicit --read-only stays true; there is no --no-read-only escape hatch
    // on the mcp binary (RO is the product contract).
    let forced = McpArgs::try_parse_from(["leankg-mcp", "mcp-http", "--read-only"]).unwrap();
    match forced.command {
        McpCommand::McpHttp { read_only, .. } => assert!(read_only),
        other => panic!("expected McpHttp, got {other:?}"),
    }
}

#[test]
fn worker_cli_parses_index_embed_watch_status() {
    let index = WorkerArgs::try_parse_from(["leankg-worker", "index", "--verbose"]).unwrap();
    match index.command {
        WorkerCommand::Index { verbose, .. } => assert!(verbose),
        other => panic!("expected Index, got {other:?}"),
    }

    let embed =
        WorkerArgs::try_parse_from(["leankg-worker", "embed", "--project", ".", "--wait"]).unwrap();
    match embed.command {
        WorkerCommand::Embed { wait, project, .. } => {
            assert!(wait);
            assert_eq!(project, ".");
        }
        other => panic!("expected Embed, got {other:?}"),
    }

    let watch = WorkerArgs::try_parse_from(["leankg-worker", "watch", "--interval", "30"]).unwrap();
    match watch.command {
        WorkerCommand::Watch { interval, .. } => assert_eq!(interval, 30),
        other => panic!("expected Watch, got {other:?}"),
    }

    let status = WorkerArgs::try_parse_from(["leankg-worker", "status"]).unwrap();
    assert!(matches!(status.command, WorkerCommand::Status));
}

#[test]
fn worker_cli_rejects_mcp_http() {
    let err = WorkerArgs::try_parse_from(["leankg-worker", "mcp-http"]).unwrap_err();
    let msg = err.to_string();
    assert!(
        msg.contains("unrecognized subcommand")
            || msg.contains("unexpected")
            || msg.contains("mcp-http"),
        "expected parse rejection of mcp-http, got: {msg}"
    );
}
