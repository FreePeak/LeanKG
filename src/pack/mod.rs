//! Portable context pack export (strategy §8.5 / §17 Tier 6 item 36).
//!
//! A *context pack* is a deterministic, relative-path, content-hashed bundle:
//!
//! ```text
//! <out>/
//!   snapshot.json        # deterministic graph slice (GraphEngine::export_snapshot shape)
//!   manifest.json        # schema version, content hashes, source revision, counts
//! ```
//!
//! It is a **distribution artifact** — never a live serving store. The serving
//! DB (CozoDB/RocksDB) remains authoritative; a pack can be diffed, committed,
//! or shipped to a cold-start consumer.
//!
//! Determinism: elements are written sorted by `qualified_name`; the manifest
//! content hash is computed over the *serialized snapshot bytes* (not map
//! iteration order), so identical graphs produce byte-identical packs.

use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};

use sha2::{Digest, Sha256};

/// A path-prefix-scoped pack. When `path` is `None`, packs the whole graph
/// (refusing on graphs over `max_nodes`).
pub struct PackOptions {
    pub path: Option<String>,
    pub max_nodes: usize,
    pub source_revision: Option<String>,
}

impl Default for PackOptions {
    fn default() -> Self {
        Self {
            path: None,
            max_nodes: 5000,
            source_revision: None,
        }
    }
}

/// Deterministic manifest describing a pack.
#[derive(Debug, Clone, serde::Serialize)]
pub struct PackManifest {
    pub schema_version: u32,
    pub kind: &'static str,
    pub generated: u64,
    pub elements: usize,
    pub relationships: usize,
    pub truncated: bool,
    pub content_hash: String,
    pub source_revision: Option<String>,
    pub path_scope: Option<String>,
}

/// Write a portable context pack to `out_dir`. Returns the manifest.
pub fn write_pack(
    project_root: &Path,
    db_path: &Path,
    out_dir: &Path,
    opts: &PackOptions,
) -> Result<PackManifest, Box<dyn std::error::Error>> {
    std::fs::create_dir_all(out_dir)?;
    let db = crate::db::schema::init_db_readonly(db_path)?;
    let graph = crate::graph::GraphEngine::new(db);

    let (elements, relationships, truncated) = select_slice(&graph, opts)?;

    // Deterministic snapshot bytes.
    let snapshot_path = out_dir.join("snapshot.json");
    let snapshot_bytes = render_snapshot(project_root, &elements, &relationships)?;
    std::fs::write(&snapshot_path, &snapshot_bytes)?;

    let content_hash = hex::encode(Sha256::digest(&snapshot_bytes));
    let manifest = PackManifest {
        schema_version: 1,
        kind: "leankg.context.pack",
        generated: now_secs(),
        elements: elements.len(),
        relationships: relationships.len(),
        truncated,
        content_hash,
        source_revision: opts.source_revision.clone(),
        path_scope: opts.path.clone(),
    };
    let manifest_bytes = serde_json::to_vec_pretty(&manifest)?;
    std::fs::write(out_dir.join("manifest.json"), &manifest_bytes)?;
    Ok(manifest)
}

/// The selected slice of a graph for a pack.
type Slice = (
    Vec<crate::db::models::CodeElement>,
    Vec<crate::db::models::Relationship>,
    bool,
);

/// Select the element/relationship slice for a pack (respects `path` scope
/// and `max_nodes`). Mirror of `export::export_select` used by HTML export,
/// but kept local so pack doesn't depend on the mcp export module.
fn select_slice(
    graph: &crate::graph::GraphEngine,
    opts: &PackOptions,
) -> Result<Slice, Box<dyn std::error::Error>> {
    let all = graph.all_elements()?;
    let all_rel = graph.all_relationships()?;
    let mut elements: Vec<_> = match &opts.path {
        Some(p) => {
            let p = p.trim_start_matches("./");
            all.into_iter()
                .filter(|e| {
                    e.file_path.trim_start_matches("./").starts_with(p)
                        || e.qualified_name.starts_with(p)
                })
                .collect()
        }
        None => all,
    };
    elements.sort_by(|a, b| a.qualified_name.cmp(&b.qualified_name));
    let truncated = elements.len() > opts.max_nodes;
    if truncated {
        elements.truncate(opts.max_nodes);
    }
    let ids: std::collections::HashSet<String> =
        elements.iter().map(|e| e.qualified_name.clone()).collect();
    let relationships: Vec<_> = all_rel
        .into_iter()
        .filter(|r| ids.contains(&r.source_qualified) && ids.contains(&r.target_qualified))
        .collect();
    Ok((elements, relationships, truncated))
}

/// Render a deterministic snapshot JSON (sorted, relative paths, no timestamps).
fn render_snapshot(
    project_root: &Path,
    elements: &[crate::db::models::CodeElement],
    relationships: &[crate::db::models::Relationship],
) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    let root = project_root.to_string_lossy().to_string();
    let mut elems: Vec<serde_json::Value> = elements
        .iter()
        .map(|e| {
            serde_json::json!({
                "qualified_name": e.qualified_name,
                "element_type": e.element_type,
                "name": e.name,
                "file_path": relativize(&e.file_path, &root),
                "line_start": e.line_start,
                "line_end": e.line_end,
                "language": e.language,
                "cluster_id": e.cluster_id,
                "cluster_label": e.cluster_label,
                "parent_qualified": e.parent_qualified,
            })
        })
        .collect();
    elems.sort_by(|a, b| {
        a["qualified_name"]
            .as_str()
            .cmp(&b["qualified_name"].as_str())
    });
    let mut rels: Vec<serde_json::Value> = relationships
        .iter()
        .map(|r| {
            serde_json::json!({
                "source": r.source_qualified,
                "target": r.target_qualified,
                "rel_type": r.rel_type,
                "confidence": r.confidence,
            })
        })
        .collect();
    rels.sort_by(|a, b| {
        (
            a["source"].as_str(),
            a["rel_type"].as_str(),
            a["target"].as_str(),
        )
            .cmp(&(
                b["source"].as_str(),
                b["rel_type"].as_str(),
                b["target"].as_str(),
            ))
    });
    let doc = serde_json::json!({
        "version": 1,
        "kind": "leankg.context.pack.snapshot",
        "project_root": root,
        "elements": elems,
        "relationships": rels,
    });
    serde_json::to_vec_pretty(&doc).map_err(Into::into)
}

/// Strip a project-root prefix from an absolute path, yielding `./rel` form
/// (mirrors `relativize` used by `GraphEngine::export_snapshot`).
fn relativize(file_path: &str, project_root: &str) -> String {
    let norm = file_path.replace('\\', "/");
    let root_norm = project_root.replace('\\', "/");
    if let Some(stripped) = norm.strip_prefix(&root_norm) {
        return format!(".{}", stripped);
    }
    // Already relative or on a different root: keep the leading `./` off.
    norm.trim_start_matches("./").to_string()
}

static EPOCH: AtomicU64 = AtomicU64::new(0);
fn now_secs() -> u64 {
    // Injected epoch (deterministic in tests). Fallback to wall clock — the
    // manifest timestamp is metadata, not part of the content hash.
    match EPOCH.load(Ordering::Relaxed) {
        0 => std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0),
        v => v,
    }
}

/// Test seam: pin the manifest timestamp for byte-stable assertions.
#[cfg(test)]
pub fn set_epoch(secs: u64) {
    EPOCH.store(secs, Ordering::Relaxed);
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn write(base: &Path, rel: &str, content: &str) {
        let p = base.join(rel);
        std::fs::create_dir_all(p.parent().unwrap()).unwrap();
        std::fs::write(p, content).unwrap();
    }

    #[test]
    fn manifest_and_snapshot_written() {
        set_epoch(1_700_000_000);
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        // Seed schema so readonly open has tables to query.
        crate::db::schema::init_db(&db_path).expect("seed db");
        let out = tmp.path().join("pack");
        let m = write_pack(&proj, &db_path, &out, &PackOptions::default()).expect("pack");
        assert!(out.join("manifest.json").exists());
        assert!(out.join("snapshot.json").exists());
        assert_eq!(m.schema_version, 1);
        assert_eq!(m.kind, "leankg.context.pack");
        assert!(!m.content_hash.is_empty());
    }

    #[test]
    fn same_graph_same_hash() {
        set_epoch(1_700_000_000);
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        crate::db::schema::init_db(&db_path).expect("seed db");
        let out1 = tmp.path().join("p1");
        let out2 = tmp.path().join("p2");
        let m1 = write_pack(&proj, &db_path, &out1, &PackOptions::default()).expect("p1");
        let m2 = write_pack(&proj, &db_path, &out2, &PackOptions::default()).expect("p2");
        assert_eq!(
            m1.content_hash, m2.content_hash,
            "deterministic content hash"
        );
        let s1 = std::fs::read(out1.join("snapshot.json")).unwrap();
        let s2 = std::fs::read(out2.join("snapshot.json")).unwrap();
        assert_eq!(s1, s2, "byte-identical snapshots");
    }

    #[test]
    fn path_scope_filters_elements() {
        set_epoch(1_700_000_000);
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        write(&proj, "tests/t.rs", "mod t;\n");
        let db_path = proj.join(".leankg");
        crate::db::schema::init_db(&db_path).expect("seed db");
        let out = tmp.path().join("pack");
        let opts = PackOptions {
            path: Some("src".to_string()),
            max_nodes: 1000,
            source_revision: None,
        };
        let m = write_pack(&proj, &db_path, &out, &opts).expect("pack");
        assert!(m.elements <= 1000);
        assert_eq!(m.path_scope.as_deref(), Some("src"));
    }
}
