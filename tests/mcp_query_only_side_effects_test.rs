//! Read-only MCP must not trigger pipeline side effects.
//!
//! Query-only servers (`with_read_only(true)`) must never auto-index on start,
//! never reindex via `ensure_project_indexed` on tool calls, never arm
//! background embed, and must disable file-watch reindex when constructed with
//! a watch path.

use leankg::db::backend::init_db;
use leankg::mcp::server::{AutoIndexDecision, MCPServer};
use tempfile::TempDir;

fn seeded_leankg() -> (TempDir, std::path::PathBuf) {
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("leankg");
    std::fs::create_dir_all(&db_path).unwrap();
    let db = init_db(&db_path).expect("init_db must succeed");
    drop(db);
    (tmp, db_path)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn read_only_server_skips_auto_index_on_start() {
    let (_tmp, db_path) = seeded_leankg();
    let server = MCPServer::new(db_path).with_read_only(true);

    let decision = server
        .auto_index_if_needed_pub()
        .await
        .expect("RO auto-index path must succeed without indexing");
    assert_eq!(
        decision,
        AutoIndexDecision::SkippedReadOnly,
        "RO MCP must skip auto-index freshness / index writes"
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn read_only_execute_tool_does_not_ensure_project_indexed() {
    let (_tmp, db_path) = seeded_leankg();
    let project_root = db_path
        .parent()
        .expect("leankg lives under a project root")
        .to_path_buf();

    let server = MCPServer::new(db_path).with_read_only(true);

    // Direct seam: ensure_project_indexed must short-circuit in RO (same
    // path execute_tool would take for an empty project).
    let decision = server
        .ensure_project_indexed_pub(project_root.to_string_lossy().as_ref())
        .await
        .expect("RO ensure_project_indexed must not error");
    assert_eq!(decision, AutoIndexDecision::SkippedReadOnly);
}

#[test]
fn read_only_rejects_watch_or_disables_watcher() {
    let (_tmp, db_path) = seeded_leankg();
    let watch = db_path.parent().unwrap().to_path_buf();

    let server = MCPServer::new_with_watch(db_path, watch).with_read_only(true);

    assert!(
        !server.file_watcher_enabled(),
        "RO MCP must not enable file-watch reindex even when constructed with a watch path"
    );
    assert!(
        !server.background_embed_allowed(),
        "RO MCP must not arm or spawn background embed"
    );
}
