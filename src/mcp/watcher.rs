use crate::db::schema::init_db;
use crate::graph::GraphEngine;
use crate::indexer::{reindex_file_sync, ParserManager};
use crate::watcher::FileChange;
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use tokio::sync::mpsc;

/// Maximum database size before triggering prune (500MB)
const MAX_DB_SIZE_BYTES: u64 = 500 * 1024 * 1024;
/// Check database size every N file changes
const DB_SIZE_CHECK_INTERVAL: usize = 100;

const IGNORED_PATH_SEGMENTS: &[&str] = &[
    ".git",
    ".leankg",
    "node_modules",
    "vendor",
    "target",
    "__pycache__",
    ".DS_Store",
    ".gradle",
    ".idea",
    ".vscode",
];

const IGNORED_EXTENSIONS: &[&str] = &[
    ".db",
    ".db-wal",
    ".db-shm",
    ".db-journal",
    ".sqlite",
    ".sqlite-wal",
    ".sqlite-shm",
    ".lock",
    ".log",
    ".pid",
];

fn should_ignore(path: &Path) -> bool {
    let path_str = path.to_string_lossy();

    for segment in IGNORED_PATH_SEGMENTS {
        if path_str.contains(segment) {
            return true;
        }
    }

    if let Some(ext) = path.extension().and_then(|e| e.to_str()) {
        let ext_with_dot = format!(".{}", ext.to_lowercase());
        if IGNORED_EXTENSIONS.contains(&ext_with_dot.as_str()) {
            return true;
        }
    }

    false
}

pub async fn start_watcher(db_path: PathBuf, watch_path: PathBuf, _rx: mpsc::Receiver<FileChange>) {
    use crate::watcher::FileWatcher;

    let watcher = match FileWatcher::new(&watch_path) {
        Ok(w) => w,
        Err(e) => {
            tracing::error!(
                "Failed to create watcher for {}: {}",
                watch_path.display(),
                e
            );
            return;
        }
    };

    let (tx, mut rx) = mpsc::channel(256);
    let async_watcher = watcher.into_async(tx);
    tokio::spawn(async_watcher.run());

    let db = match init_db(&db_path) {
        Ok(db) => db,
        Err(e) => {
            tracing::error!("Failed to init db for watcher: {}", e);
            return;
        }
    };
    let graph = GraphEngine::new(db);
    let mut parser = ParserManager::new();
    if let Err(e) = parser.init_parsers() {
        tracing::error!("Failed to init parsers for watcher: {}", e);
        return;
    }

    let debounce_interval = std::time::Duration::from_millis(500);
    let mut pending: HashSet<PathBuf> = HashSet::new();
    let mut debounce_timer = tokio::time::Instant::now() + debounce_interval;
    let mut files_since_check: usize = 0;

    loop {
        tokio::select! {
            Some(change) = rx.recv() => {
                if should_ignore(&change.path) {
                    continue;
                }

                let ext = change.path.extension()
                    .and_then(|e| e.to_str())
                    .map(|e| e.to_lowercase())
                    .unwrap_or_default();
                let is_source = [
                    "rs", "go", "ts", "tsx", "js", "jsx", "py", "java",
                    "kt", "kts", "c", "cpp", "h", "hpp", "cs", "rb",
                    "swift", "scala", "clj", "hs", "zig", "nim",
                    "tf", "proto", "graphql", "toml", "yaml", "yml",
                    "md", "rst",
                ].contains(&ext.as_str());

                if !is_source {
                    continue;
                }

                pending.insert(change.path);
                debounce_timer = tokio::time::Instant::now() + debounce_interval;
            }
            _ = tokio::time::sleep_until(debounce_timer), if !pending.is_empty() => {
                let files: Vec<PathBuf> = pending.drain().collect();
                for file_path in files {
                    let path_str = file_path.to_string_lossy();
                    match reindex_file_sync(&graph, &mut parser, &path_str) {
                        Ok(count) => {
                            if count > 0 {
                                tracing::info!("Indexed {} elements from {}", count, path_str);
                            }
                        }
                        Err(e) => {
                            tracing::warn!("Failed to index {}: {}", path_str, e);
                        }
                    }
                    files_since_check += 1;

                    // Periodically check and enforce database size limit
                    if files_since_check >= DB_SIZE_CHECK_INTERVAL {
                        files_since_check = 0;
                        check_and_enforce_db_size(&db_path);
                    }
                }
            }
            _ = tokio::time::sleep(std::time::Duration::from_secs(60)), if pending.is_empty() => {
                tracing::debug!("Watcher still running for {}", watch_path.display());
            }
        }
    }
}

/// Check database size and trigger prune if over limit
fn check_and_enforce_db_size(db_path: &Path) {
    let db_file = db_path.join("leankg.db");
    if let Ok(metadata) = std::fs::metadata(&db_file) {
        let size = metadata.len();
        if size > MAX_DB_SIZE_BYTES {
            tracing::warn!(
                "Database size {} bytes exceeds limit {} bytes, consider running vacuum or cleanup",
                size,
                MAX_DB_SIZE_BYTES
            );
            // Future: could trigger async vacuum or cleanup here
        }
    }
}
