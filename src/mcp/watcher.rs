use crate::graph::GraphEngine;
use crate::indexer::{reindex_file_sync, ParserManager};
use crate::watcher::FileChange;
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;

/// Maximum database size before triggering VACUUM (500 MiB).
/// Lowered from the previous 500 MiB; can be overridden via
/// `LEANKG_WATCHER_MAX_DB_SIZE` (bytes).
const MAX_DB_SIZE_BYTES: u64 = 500 * 1024 * 1024;
/// Check database size every N file changes.
const DB_SIZE_CHECK_INTERVAL: usize = 100;

/// Debounce window before flushing pending file changes. Tuned for editor
/// workloads: 500ms was too short and caused thrash on every keystroke, 2s
/// coalesces typical save bursts without leaving the index noticeably stale.
/// Override via `LEANKG_WATCHER_DEBOUNCE_MS`.
const DEFAULT_DEBOUNCE_MS: u64 = 2000;

/// Soft cap on the number of pending files per debounce window. If a single
/// event flush exceeds this (e.g. `git checkout` rewrites thousands of files),
/// the watcher drops down to a slower "batch-of-batch" mode and processes the
/// rest in chunks with extra spacing, so we don't fork-bomb the database.
/// Override via `LEANKG_WATCHER_BURST_LIMIT`.
const DEFAULT_BURST_LIMIT: usize = 256;

/// When the burst limit is hit, sleep this long between micro-batches so
/// the rest of the system can breathe. Override via
/// `LEANKG_WATCHER_BURST_PAUSE_MS`.
const DEFAULT_BURST_PAUSE_MS: u64 = 250;

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
    "dist",
    "build",
    "out",
    "bin",
    "coverage",
    ".next",
    ".nuxt",
    ".svelte-kit",
    ".turbo",
    ".cache",
    ".parcel-cache",
    ".pytest_cache",
    ".ruff_cache",
    ".mypy_cache",
    ".tox",
    ".venv",
    "venv",
    ".terraform",
    ".terragrunt-cache",
    "Godeps",
    "pb",
    "pb-go",
    "gen",
    "generated",
    "swagger",
    "fixtures",
    "__snapshots__",
    "testdata",
    "docs",
    "tmp",
    "logs",
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
    ".tmp",
    ".swp",
    ".swo",
    ".bak",
    ".orig",
    ".rej",
    ".min.js",
    ".min.css",
    ".map",
];

const SOURCE_EXTENSIONS: &[&str] = &[
    "rs", "go", "ts", "tsx", "js", "jsx", "py", "java", "kt", "kts", "c", "cpp", "h", "hpp", "cs",
    "rb", "swift", "scala", "clj", "hs", "zig", "nim", "tf", "proto", "graphql", "toml", "yaml",
    "yml", "md", "rst", "dart", "m", "mm",
];

/// Returns true when the MCP file watcher should reindex this extension.
pub fn is_watched_source_extension(ext: &str) -> bool {
    SOURCE_EXTENSIONS.contains(&ext.to_lowercase().as_str())
}

fn env_u64(key: &str, default: u64) -> u64 {
    std::env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}

fn env_usize(key: &str, default: usize) -> usize {
    std::env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}

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

/// File watcher that reindexes on change using a shared [`GraphEngine`].
///
/// Must receive the same handle as the MCP server (`get_graph_engine()`), not
/// open its own `init_db` — RocksDB allows only one writer per path per process.
/// When `shutdown` is set, the loop exits so the clone can drop cleanly before
/// process exit (releases the RocksDB LOCK for the next start).
pub async fn start_watcher(
    graph: GraphEngine,
    db_path: PathBuf,
    watch_path: PathBuf,
    shutdown: Arc<AtomicBool>,
    _rx: mpsc::Receiver<FileChange>,
) {
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

    // Larger channel (4096) to absorb the storm of events a `git pull` or
    // build emits before the debounce timer can flush. The previous 256-slot
    // channel was the choke point that caused the watcher to drop events
    // and then over-index.
    let (tx, mut rx) = mpsc::channel(4096);
    let async_watcher = watcher.into_async(tx);
    tokio::spawn(async_watcher.run());

    let mut parser = ParserManager::new();
    if let Err(e) = parser.init_parsers() {
        tracing::error!("Failed to init parsers for watcher: {}", e);
        return;
    }

    let debounce_interval =
        Duration::from_millis(env_u64("LEANKG_WATCHER_DEBOUNCE_MS", DEFAULT_DEBOUNCE_MS));
    let burst_limit = env_usize("LEANKG_WATCHER_BURST_LIMIT", DEFAULT_BURST_LIMIT);
    let burst_pause = Duration::from_millis(env_u64(
        "LEANKG_WATCHER_BURST_PAUSE_MS",
        DEFAULT_BURST_PAUSE_MS,
    ));
    let max_db_size: u64 = env_u64("LEANKG_WATCHER_MAX_DB_SIZE", MAX_DB_SIZE_BYTES);

    let mut pending: HashSet<PathBuf> = HashSet::new();
    let mut debounce_timer = tokio::time::Instant::now() + debounce_interval;
    let mut files_since_check: usize = 0;

    loop {
        if shutdown.load(Ordering::SeqCst) {
            tracing::info!(
                "Watcher shutting down for {} (dropping GraphEngine clone)",
                watch_path.display()
            );
            break;
        }

        tokio::select! {
            Some(change) = rx.recv() => {
                if should_ignore(&change.path) {
                    continue;
                }

                let ext = change.path.extension()
                    .and_then(|e| e.to_str())
                    .map(|e| e.to_lowercase())
                    .unwrap_or_default();
                if !is_watched_source_extension(&ext) {
                    continue;
                }

                pending.insert(change.path);
                debounce_timer = tokio::time::Instant::now() + debounce_interval;
            }
            _ = tokio::time::sleep_until(debounce_timer), if !pending.is_empty() => {
                let files: Vec<PathBuf> = pending.drain().collect();
                let total = files.len();
                if total > burst_limit {
                    tracing::warn!(
                        "Watcher burst: {} files pending (>{}); processing in chunks to avoid OOM",
                        total,
                        burst_limit
                    );
                }
                for (i, file_path) in files.into_iter().enumerate() {
                    if shutdown.load(Ordering::SeqCst) {
                        tracing::info!(
                            "Watcher shutting down mid-flush for {}",
                            watch_path.display()
                        );
                        return;
                    }
                    let path_str = file_path.to_string_lossy();
                    match reindex_file_sync(&graph, &mut parser, &path_str) {
                        Ok(count) => {
                            if count > 0 && total <= burst_limit {
                                tracing::info!("Indexed {} elements from {}", count, path_str);
                            }
                        }
                        Err(e) => {
                            tracing::warn!("Failed to index {}: {}", path_str, e);
                        }
                    }
                    files_since_check += 1;

                    if files_since_check >= DB_SIZE_CHECK_INTERVAL {
                        files_since_check = 0;
                        check_and_enforce_db_size(&db_path, &graph, max_db_size);
                    }

                    // Insert a small pause every burst_limit files when we're
                    // processing a large event flush, to keep RSS bounded.
                    if i > 0 && i % burst_limit == 0 {
                        tokio::time::sleep(burst_pause).await;
                    }
                }
            }
            _ = tokio::time::sleep(Duration::from_millis(250)), if pending.is_empty() => {
                // Short poll so we notice shutdown promptly while idle.
            }
        }
    }
    // `graph` drops here — shared Arc; last drop releases RocksDB LOCK.
}

/// Check database size and trigger a VACUUM if over the configured limit.
/// Phase 8: Postgres holds the data — the legacy `leankg.db` file no longer
/// exists, so this is a no-op (Postgres manages its own storage/VACUUM).
fn check_and_enforce_db_size(_db_path: &Path, _graph: &GraphEngine, _max_size: u64) {
    let _ = (_db_path, _graph, _max_size);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn watches_swift_and_objc_extensions() {
        assert!(is_watched_source_extension("swift"));
        assert!(is_watched_source_extension("m"));
        assert!(is_watched_source_extension("mm"));
        assert!(is_watched_source_extension("h"));
        assert!(is_watched_source_extension("M")); // case-insensitive
        assert!(!is_watched_source_extension("xyz"));
    }
}
