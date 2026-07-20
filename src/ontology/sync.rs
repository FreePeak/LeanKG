//! Idempotent ontology YAML → graph sync (concepts + workflows).
//!
//! Used by CLI `leankg ontology sync`, Docker boot, MCP/serve watchers,
//! post-index hooks, and MCP `ontology_control(action=sync)`.

use crate::graph::GraphEngine;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

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

/// Load `concepts.yaml` + `workflows.yaml` from `ontology_dir` and upsert into `graph`.
///
/// When `leankg_dir` is `Some`, touches `.leankg/ontology_synced` on success and
/// invalidates the graph query cache.
pub fn sync_from_dir(
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

    let concepts_file = ontology_dir.join("concepts.yaml");
    if concepts_file.exists() {
        match super::load_concepts_yaml(&concepts_file) {
            Ok(nodes) => {
                let elements = super::concept_nodes_to_elements(&nodes);
                for elem in &elements {
                    if let Err(e) = graph.insert_element(elem) {
                        tracing::warn!("Failed to insert concept element: {}", e);
                    }
                }
                stats.concepts = nodes.len();
            }
            Err(e) => {
                tracing::warn!("Failed to load concepts.yaml: {}", e);
            }
        }
    }

    let workflows_file = ontology_dir.join("workflows.yaml");
    if workflows_file.exists() {
        match super::load_workflows_yaml(&workflows_file) {
            Ok((workflows, steps, failures, relationships)) => {
                let workflow_elements = super::workflow_nodes_to_elements(&workflows);
                let step_elements = super::workflow_step_nodes_to_elements(&steps);
                let failure_elements = super::failure_mode_nodes_to_elements(&failures);

                for elem in workflow_elements
                    .iter()
                    .chain(step_elements.iter())
                    .chain(failure_elements.iter())
                {
                    if let Err(e) = graph.insert_element(elem) {
                        tracing::warn!("Failed to insert workflow element: {}", e);
                    }
                }

                for rel in &relationships {
                    if let Err(e) = graph.insert_relationship(rel) {
                        tracing::warn!("Failed to insert relationship: {}", e);
                    }
                }

                stats.workflows = workflows.len();
                stats.workflow_steps = steps.len();
                stats.failure_modes = failures.len();
                stats.relationships = relationships.len();
            }
            Err(e) => {
                tracing::warn!("Failed to load workflows.yaml: {}", e);
            }
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
