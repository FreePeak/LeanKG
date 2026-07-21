//! Debounced watcher for ontology YAML files during mcp-http / serve.

use crate::graph::GraphEngine;
use crate::ontology::sync::{
    ontology_watch_debounce_ms, resolve_ontology_dir, sync_from_dir, OntologySyncStats,
};
use notify::{Config, Event, EventKind, RecommendedWatcher, RecursiveMode, Watcher};
use std::path::{Path, PathBuf};
use std::sync::mpsc;
use std::sync::Arc;
use std::time::{Duration, Instant};

fn is_ontology_yaml(path: &Path) -> bool {
    let Some(name) = path.file_name().and_then(|n| n.to_str()) else {
        return false;
    };
    name == "concepts.yaml"
        || name == "workflows.yaml"
        || name == "concepts.yml"
        || name == "workflows.yml"
}

fn event_targets_ontology_yaml(event: &Event) -> bool {
    matches!(
        event.kind,
        EventKind::Create(_) | EventKind::Modify(_) | EventKind::Remove(_)
    ) && event.paths.iter().any(|p| is_ontology_yaml(p))
}

/// Spawn a background thread that watches `ontology/` and re-syncs on YAML changes.
///
/// Returns `None` if no ontology directory exists. The callback runs after each
/// successful sync (e.g. to clear MCP GraphEngine caches).
pub fn spawn_ontology_yaml_watcher<F>(
    project_root: PathBuf,
    graph: GraphEngine,
    on_synced: F,
) -> Option<std::thread::JoinHandle<()>>
where
    F: Fn(&OntologySyncStats) + Send + 'static,
{
    let ontology_dir = resolve_ontology_dir(&project_root)?;
    if !ontology_dir.is_dir() {
        return None;
    }

    let debounce = Duration::from_millis(ontology_watch_debounce_ms());
    let leankg_dir = project_root.join(".leankg");
    let watch_dir = ontology_dir.clone();
    let graph = Arc::new(graph);

    let handle = std::thread::Builder::new()
        .name("leankg-ontology-watch".into())
        .spawn(move || {
            let (tx, rx) = mpsc::channel::<Event>();
            let mut watcher = match RecommendedWatcher::new(
                move |res: Result<Event, notify::Error>| {
                    if let Ok(event) = res {
                        let _ = tx.send(event);
                    }
                },
                Config::default().with_poll_interval(Duration::from_secs(1)),
            ) {
                Ok(w) => w,
                Err(e) => {
                    tracing::warn!("ontology YAML watcher failed to start: {}", e);
                    return;
                }
            };

            if let Err(e) = watcher.watch(&watch_dir, RecursiveMode::NonRecursive) {
                tracing::warn!(
                    "ontology YAML watcher could not watch {}: {}",
                    watch_dir.display(),
                    e
                );
                return;
            }

            tracing::info!(
                "Ontology YAML watcher active on {} (debounce {}ms)",
                watch_dir.display(),
                debounce.as_millis()
            );

            let mut pending = false;
            let mut last_event = Instant::now();

            loop {
                let timeout = if pending {
                    debounce
                        .checked_sub(last_event.elapsed())
                        .unwrap_or(Duration::from_millis(50))
                        .max(Duration::from_millis(50))
                } else {
                    Duration::from_secs(3600)
                };

                match rx.recv_timeout(timeout) {
                    Ok(event) => {
                        if event_targets_ontology_yaml(&event) {
                            pending = true;
                            last_event = Instant::now();
                            tracing::debug!("ontology YAML change detected: {:?}", event.paths);
                        }
                    }
                    Err(mpsc::RecvTimeoutError::Timeout) => {
                        if pending && last_event.elapsed() >= debounce {
                            pending = false;
                            match sync_from_dir(&watch_dir, &graph, Some(&leankg_dir)) {
                                Ok(stats) => {
                                    tracing::info!(
                                        "Ontology auto-sync: concepts={} workflows={} steps={}",
                                        stats.concepts,
                                        stats.workflows,
                                        stats.workflow_steps
                                    );
                                    on_synced(&stats);
                                }
                                Err(e) => {
                                    tracing::warn!("Ontology auto-sync failed: {}", e);
                                }
                            }
                        }
                    }
                    Err(mpsc::RecvTimeoutError::Disconnected) => break,
                }
            }
        })
        .ok()?;

    Some(handle)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_ontology_yaml_names() {
        assert!(is_ontology_yaml(Path::new("/tmp/ontology/workflows.yaml")));
        assert!(is_ontology_yaml(Path::new("concepts.yaml")));
        assert!(!is_ontology_yaml(Path::new("src/main.rs")));
        assert!(!is_ontology_yaml(Path::new("ontology/readme.md")));
    }
}
