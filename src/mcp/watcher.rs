use crate::db::schema::init_db;
use crate::graph::GraphEngine;
use crate::indexer::{reindex_file_sync, ParserManager};
use crate::watcher::{FileChange, FileChangeKind};
use std::path::PathBuf;
use tokio::sync::mpsc;

pub async fn handle_file_change(db_path: &PathBuf, change: FileChange) {
    let db = match init_db(db_path) {
        Ok(db) => db,
        Err(e) => {
            tracing::error!("Failed to init db: {}", e);
            return;
        }
    };
    let graph = GraphEngine::new(db);
    let mut parser = ParserManager::new();
    let _ = parser.init_parsers();

    let path_str = change.path.to_string_lossy();
    if path_str.contains("node_modules") || path_str.contains("vendor") || path_str.contains(".git")
    {
        return;
    }

    match change.kind {
        FileChangeKind::Modified | FileChangeKind::Created => {
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
        }
        FileChangeKind::Deleted => {
            if let Err(e) = graph.remove_elements_by_file(&path_str) {
                tracing::warn!("Failed to remove file from index: {}", e);
            }
        }
    }
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

    let (tx, watcher_rx) = mpsc::channel(100);
    let async_watcher = watcher.into_async(tx);

    tokio::spawn(async_watcher.run());

    let mut rx = watcher_rx;
    let db_path_clone = db_path.clone();

    loop {
        tokio::select! {
            Some(change) = rx.recv() => {
                handle_file_change(&db_path_clone, change).await;
            }
            _ = tokio::time::sleep(std::time::Duration::from_secs(60)) => {
                tracing::debug!("Watcher still running for {}", watch_path.display());
            }
        }
    }
}
