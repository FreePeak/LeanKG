use std::path::Path;
use tracing::warn;

use crate::db;
use crate::graph::GraphEngine;

/// Auto-write `.leankg/GRAPH_REPORT.md` after indexing.
///
/// Soft-fails (logs warn, returns Ok) so report generation never blocks
/// indexing. Skips write when content is byte-identical to the existing
/// file (watch-mode noise control).
pub async fn write_graph_report_after_index(
    project_path: &Path,
    db_path: &Path,
) -> Result<(), Box<dyn std::error::Error>> {
    let db = match db::backend::init_db(db_path) {
        Ok(db) => db,
        Err(e) => {
            warn!("GRAPH_REPORT auto-write skipped (db init): {}", e);
            return Ok(());
        }
    };
    let engine = GraphEngine::new(db);
    let name = project_path
        .file_name()
        .and_then(|n| n.to_str())
        .unwrap_or("project");

    let report = match engine.generate_graph_report(name) {
        Ok(r) => r,
        Err(e) => {
            warn!("GRAPH_REPORT auto-write skipped (generate): {}", e);
            return Ok(());
        }
    };
    let markdown = report.to_markdown();
    let out_path = project_path.join(".leankg").join("GRAPH_REPORT.md");

    // Skip write when content unchanged (watch-mode noise control)
    match tokio::fs::read(&out_path).await {
        Ok(existing) if existing == markdown.as_bytes() => {
            return Ok(());
        }
        _ => {}
    }

    if let Some(parent) = out_path.parent() {
        let _ = tokio::fs::create_dir_all(parent).await;
    }
    if let Err(e) = tokio::fs::write(&out_path, &markdown).await {
        warn!("GRAPH_REPORT auto-write skipped (write): {}", e);
        return Ok(());
    }
    tracing::info!("Wrote {}", out_path.display());
    Ok(())
}
