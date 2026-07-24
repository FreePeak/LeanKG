//! Idempotent ontology YAML → graph sync (concepts + workflows).
//!
//! Used by CLI `leankg ontology sync`, Docker boot, MCP/serve watchers,
//! post-index hooks, and MCP `ontology_control(action=sync)`.

use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use std::path::{Path, PathBuf};
use std::sync::Mutex;
use std::time::{SystemTime, UNIX_EPOCH};

/// Serialize ontology sync across watcher + MCP control + post-index hooks
/// so SQLite is not written concurrently (avoids `database is locked`).
static ONTOLOGY_SYNC_LOCK: Mutex<()> = Mutex::new(());

/// Result of syncing ontology YAML into the graph.
#[derive(Debug, Clone, Default, serde::Serialize)]
pub struct OntologySyncStats {
    pub concepts: usize,
    pub workflows: usize,
    pub workflow_steps: usize,
    pub failure_modes: usize,
    pub relationships: usize,
    pub ontology_dir: String,
    pub marker_path: Option<String>,
    pub synced_at_unix: u64,
}

/// Resolve the ontology source directory for a project.
///
/// Order: `LEANKG_ONTOLOGY_DIR` env → `<project>/ontology` if it exists.
pub fn resolve_ontology_dir(project_root: &Path) -> Option<PathBuf> {
    if let Ok(dir) = std::env::var("LEANKG_ONTOLOGY_DIR") {
        let p = PathBuf::from(dir);
        if p.is_dir() {
            return Some(p);
        }
    }
    let candidate = project_root.join("ontology");
    if candidate.is_dir() {
        Some(candidate)
    } else {
        None
    }
}

/// Path to the boot/freshness marker under `.leankg/`.
pub fn ontology_synced_marker(leankg_dir: &Path) -> PathBuf {
    leankg_dir.join("ontology_synced")
}

/// Touch `.leankg/ontology_synced` after a successful sync.
pub fn touch_ontology_synced_marker(leankg_dir: &Path) -> std::io::Result<PathBuf> {
    let marker = ontology_synced_marker(leankg_dir);
    if let Some(parent) = marker.parent() {
        std::fs::create_dir_all(parent)?;
    }
    // Create / truncate so mtime advances on every successful sync.
    std::fs::File::create(&marker)?;
    Ok(marker)
}

fn unix_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}

/// Load `concepts.yaml` + `workflows.yaml` from `ontology_dir` and replace the
/// ontology layer in `graph` (YAML is source of truth).
///
/// Strategy (same idea as `reindex_file_sync`): clear all existing
/// `ontology://` rows + their outgoing relationships, then insert fresh.
/// Avoids Cozo composite-key `:put` duplicates when `name`/`metadata` change.
///
/// When `leankg_dir` is `Some`, touches `.leankg/ontology_synced` on success and
/// invalidates the graph query cache.
pub fn sync_from_dir(
    ontology_dir: &Path,
    graph: &GraphEngine,
    leankg_dir: Option<&Path>,
) -> Result<OntologySyncStats, Box<dyn std::error::Error + Send + Sync>> {
    let _guard = ONTOLOGY_SYNC_LOCK
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    sync_from_dir_locked(ontology_dir, graph, leankg_dir)
}

fn sync_from_dir_locked(
    ontology_dir: &Path,
    graph: &GraphEngine,
    leankg_dir: Option<&Path>,
) -> Result<OntologySyncStats, Box<dyn std::error::Error + Send + Sync>> {
    if !ontology_dir.is_dir() {
        return Err(format!(
            "ontology directory does not exist: {}",
            ontology_dir.display()
        )
        .into());
    }

    let mut stats = OntologySyncStats {
        ontology_dir: ontology_dir.display().to_string(),
        synced_at_unix: unix_now(),
        ..Default::default()
    };

    // Declarative replace: wipe prior YAML-sourced ontology layer so renames/removals
    // apply, while preserving dynamic (agent-discovered) concepts and workflows.
    match with_db_retry(|| graph.clear_yaml_ontology_layer()) {
        Ok(n) => {
            tracing::debug!("cleared {} prior ontology GID(s) before sync", n);
        }
        Err(e) => {
            tracing::warn!("clear_ontology_layer before sync failed: {}", e);
        }
    }

    let mut all_elements: Vec<CodeElement> = Vec::new();

    let concepts_file = ontology_dir.join("concepts.yaml");
    if concepts_file.exists() {
        match super::load_concepts_yaml(&concepts_file) {
            Ok(nodes) => {
                all_elements.extend(super::concept_nodes_to_elements(&nodes));
                stats.concepts = nodes.len();
            }
            Err(e) => {
                tracing::warn!("Failed to load concepts.yaml: {}", e);
            }
        }
    }

    let mut pending_relationships: Vec<Relationship> = Vec::new();
    let workflows_file = ontology_dir.join("workflows.yaml");
    if workflows_file.exists() {
        match super::load_workflows_yaml(&workflows_file) {
            Ok((workflows, steps, failures, relationships)) => {
                all_elements.extend(super::workflow_nodes_to_elements(&workflows));
                all_elements.extend(super::workflow_step_nodes_to_elements(&steps));
                all_elements.extend(super::failure_mode_nodes_to_elements(&failures));
                pending_relationships = relationships;
                stats.workflows = workflows.len();
                stats.workflow_steps = steps.len();
                stats.failure_modes = failures.len();
                stats.relationships = pending_relationships.len();
            }
            Err(e) => {
                tracing::warn!("Failed to load workflows.yaml: {}", e);
            }
        }
    }

    if !all_elements.is_empty() {
        if let Err(e) = with_db_retry(|| graph.insert_elements(&all_elements)) {
            tracing::warn!("Failed to insert ontology elements: {}", e);
        }
    }

    for rel in &pending_relationships {
        if let Err(e) = with_db_retry(|| graph.insert_relationship(rel)) {
            tracing::warn!("Failed to insert relationship: {}", e);
        }
    }

    graph.invalidate_cache();

    if let Some(leankg) = leankg_dir {
        match touch_ontology_synced_marker(leankg) {
            Ok(marker) => {
                stats.marker_path = Some(marker.display().to_string());
            }
            Err(e) => {
                tracing::warn!("Failed to touch ontology_synced marker: {}", e);
            }
        }
    }

    stats.synced_at_unix = unix_now();
    Ok(stats)
}

fn is_db_locked_msg(msg: &str) -> bool {
    let s = msg.to_lowercase();
    s.contains("database is locked") || s.contains("code 5")
}

fn with_db_retry<T, E, F>(mut f: F) -> Result<T, E>
where
    E: std::fmt::Display,
    F: FnMut() -> Result<T, E>,
{
    const ATTEMPTS: u32 = 8;
    let mut attempt = 0;
    loop {
        attempt += 1;
        match f() {
            Ok(v) => return Ok(v),
            Err(e) if is_db_locked_msg(&e.to_string()) && attempt < ATTEMPTS => {
                let backoff_ms = 25 * u64::from(attempt);
                tracing::warn!(
                    "ontology sync: database locked (attempt {}/{}), retry in {}ms",
                    attempt,
                    ATTEMPTS,
                    backoff_ms
                );
                std::thread::sleep(std::time::Duration::from_millis(backoff_ms));
            }
            Err(e) => return Err(e),
        }
    }
}

/// Sync ontology for a project root (resolves ontology dir + `.leankg` marker).
pub fn sync_for_project(
    project_root: &Path,
    graph: &GraphEngine,
) -> Result<OntologySyncStats, Box<dyn std::error::Error + Send + Sync>> {
    let ontology_dir = resolve_ontology_dir(project_root).ok_or_else(|| {
        format!(
            "no ontology directory found under {} (set LEANKG_ONTOLOGY_DIR)",
            project_root.display()
        )
    })?;
    let leankg = project_root.join(".leankg");
    sync_from_dir(&ontology_dir, graph, Some(&leankg))
}

/// Status payload for MCP `ontology_control(action=status)`.
pub fn ontology_sync_status(project_root: &Path) -> serde_json::Value {
    let ontology_dir = resolve_ontology_dir(project_root);
    let leankg = project_root.join(".leankg");
    let marker = ontology_synced_marker(&leankg);

    let file_mtime = |p: &Path| -> Option<u64> {
        std::fs::metadata(p)
            .ok()
            .and_then(|m| m.modified().ok())
            .and_then(|t| t.duration_since(UNIX_EPOCH).ok())
            .map(|d| d.as_secs())
    };

    let concepts = ontology_dir
        .as_ref()
        .map(|d| d.join("concepts.yaml"))
        .filter(|p| p.exists());
    let workflows = ontology_dir
        .as_ref()
        .map(|d| d.join("workflows.yaml"))
        .filter(|p| p.exists());

    serde_json::json!({
        "project_root": project_root.display().to_string(),
        "ontology_dir": ontology_dir.as_ref().map(|p| p.display().to_string()),
        "concepts_yaml": concepts.as_ref().map(|p| p.display().to_string()),
        "workflows_yaml": workflows.as_ref().map(|p| p.display().to_string()),
        "concepts_mtime_unix": concepts.as_ref().and_then(|p| file_mtime(p)),
        "workflows_mtime_unix": workflows.as_ref().and_then(|p| file_mtime(p)),
        "marker": marker.display().to_string(),
        "marker_exists": marker.exists(),
        "marker_mtime_unix": file_mtime(&marker),
        "watch_debounce_ms": ontology_watch_debounce_ms(),
    })
}

/// Debounce for ontology YAML watcher (`LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS`, default 1500).
pub fn ontology_watch_debounce_ms() -> u64 {
    std::env::var("LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(1500)
        .max(1000)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::schema::init_db;
    use std::io::Write;
    use tempfile::TempDir;

    #[test]
    fn sync_from_dir_loads_workflows_and_touches_marker() {
        let tmp = TempDir::new().unwrap();
        let project = tmp.path();
        let ontology = project.join("ontology");
        let leankg = project.join(".leankg");
        std::fs::create_dir_all(&ontology).unwrap();
        std::fs::create_dir_all(&leankg).unwrap();

        let mut f = std::fs::File::create(ontology.join("workflows.yaml")).unwrap();
        writeln!(
            f,
            r#"workflows:
  - id: test_flow
    name: Test Flow
    env: local
    description: unit test workflow
    aliases: [test-flow]
    entry_points: []
    steps:
      - id: step_a
        name: Step A
        code_refs: [src/main.rs::main]
        failure_modes: []
"#
        )
        .unwrap();

        let db = init_db(&leankg).unwrap();
        let graph = GraphEngine::new(db);
        let stats = sync_from_dir(&ontology, &graph, Some(&leankg)).unwrap();
        assert_eq!(stats.workflows, 1);
        assert_eq!(stats.workflow_steps, 1);
        assert!(leankg.join("ontology_synced").exists());

        let q = crate::ontology::OntologyQueryEngine::new(graph.db().clone());
        let steps = q.trace_workflow("Test Flow", "local").unwrap();
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0].name, "Step A");
    }

    #[test]
    fn sync_from_dir_rename_replaces_not_duplicates() {
        let tmp = TempDir::new().unwrap();
        let project = tmp.path();
        let ontology = project.join("ontology");
        let leankg = project.join(".leankg");
        std::fs::create_dir_all(&ontology).unwrap();
        std::fs::create_dir_all(&leankg).unwrap();

        let yaml_v1 = r#"workflows:
  - id: test_flow
    name: Test Flow
    env: local
    description: unit test workflow
    aliases: [test-flow]
    entry_points: []
    steps:
      - id: step_a
        name: Step A Original
        code_refs: [src/main.rs::main]
        failure_modes: []
"#;
        let yaml_v2 = yaml_v1.replace("Step A Original", "Step A Renamed");

        std::fs::write(ontology.join("workflows.yaml"), yaml_v1).unwrap();
        let db = init_db(&leankg).unwrap();
        let graph = GraphEngine::new(db);
        sync_from_dir(&ontology, &graph, Some(&leankg)).unwrap();

        std::fs::write(ontology.join("workflows.yaml"), &yaml_v2).unwrap();
        sync_from_dir(&ontology, &graph, Some(&leankg)).unwrap();

        let q = crate::ontology::OntologyQueryEngine::new(graph.db().clone());
        let steps = q.trace_workflow("Test Flow", "local").unwrap();
        assert_eq!(
            steps.len(),
            1,
            "rename must not leave duplicate step rows: {:?}",
            steps.iter().map(|s| &s.name).collect::<Vec<_>>()
        );
        assert_eq!(steps[0].name, "Step A Renamed");
        assert!(!steps.iter().any(|s| s.name.contains("Original")));
    }

    #[test]
    fn sync_from_dir_removed_step_disappears() {
        let tmp = TempDir::new().unwrap();
        let project = tmp.path();
        let ontology = project.join("ontology");
        let leankg = project.join(".leankg");
        std::fs::create_dir_all(&ontology).unwrap();
        std::fs::create_dir_all(&leankg).unwrap();

        std::fs::write(
            ontology.join("workflows.yaml"),
            r#"workflows:
  - id: test_flow
    name: Test Flow
    env: local
    description: d
    aliases: []
    entry_points: []
    steps:
      - id: step_a
        name: Keep Me
        code_refs: []
        failure_modes: []
      - id: step_b
        name: Drop Me
        code_refs: []
        failure_modes: []
"#,
        )
        .unwrap();
        let db = init_db(&leankg).unwrap();
        let graph = GraphEngine::new(db);
        sync_from_dir(&ontology, &graph, Some(&leankg)).unwrap();

        std::fs::write(
            ontology.join("workflows.yaml"),
            r#"workflows:
  - id: test_flow
    name: Test Flow
    env: local
    description: d
    aliases: []
    entry_points: []
    steps:
      - id: step_a
        name: Keep Me
        code_refs: []
        failure_modes: []
"#,
        )
        .unwrap();
        sync_from_dir(&ontology, &graph, Some(&leankg)).unwrap();

        let q = crate::ontology::OntologyQueryEngine::new(graph.db().clone());
        let steps = q.trace_workflow("Test Flow", "local").unwrap();
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0].name, "Keep Me");
    }

    #[test]
    fn resolve_ontology_dir_prefers_env() {
        let tmp = TempDir::new().unwrap();
        let project = tmp.path().join("proj");
        let other = tmp.path().join("other_ont");
        std::fs::create_dir_all(project.join("ontology")).unwrap();
        std::fs::create_dir_all(&other).unwrap();
        std::env::set_var("LEANKG_ONTOLOGY_DIR", other.display().to_string());
        let resolved = resolve_ontology_dir(&project).unwrap();
        std::env::remove_var("LEANKG_ONTOLOGY_DIR");
        assert_eq!(resolved, other);
    }
}
