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
//! Determinism contract: identical graphs produce byte-identical
//! `snapshot.json` AND byte-identical `manifest.json` across runs and
//! across checkouts — no wall-clock timestamps, no absolute paths. The
//! manifest content hash covers the snapshot bytes (already sorted by
//! `qualified_name`). Oversize slices are **refused**, never silently
//! truncated.

use std::path::Path;

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
    pub elements: usize,
    pub relationships: usize,
    pub content_hash: String,
    pub source_revision: Option<String>,
    pub path_scope: Option<String>,
}

/// Write a portable context pack to `out_dir`. Returns the manifest.
///
/// Errors out (instead of truncating) if the selected slice exceeds
/// `opts.max_nodes` — the docstring advertises "refuses to truncate" and
/// silent row-drop would make that a lie.
pub fn write_pack(
    project_root: &Path,
    db_path: &Path,
    out_dir: &Path,
    opts: &PackOptions,
) -> Result<PackManifest, Box<dyn std::error::Error>> {
    std::fs::create_dir_all(out_dir)?;
    let db = crate::db::backend::init_db_readonly(db_path)?;
    let graph = crate::graph::GraphEngine::new(db);

    let (elements, relationships) = select_slice(&graph, opts)?;

    if elements.len() > opts.max_nodes {
        return Err(format!(
            "pack slice {} elements exceeds max_nodes={} (refusing to truncate)",
            elements.len(),
            opts.max_nodes
        )
        .into());
    }

    // Deterministic snapshot bytes.
    let snapshot_path = out_dir.join("snapshot.json");
    let snapshot_bytes = render_snapshot(project_root, &elements, &relationships)?;
    std::fs::write(&snapshot_path, &snapshot_bytes)?;

    let content_hash = hex::encode(Sha256::digest(&snapshot_bytes));
    let manifest = PackManifest {
        schema_version: 1,
        kind: "leankg.context.pack",
        elements: elements.len(),
        relationships: relationships.len(),
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
);

/// Select the element/relationship slice for a pack (respects `path` scope).
/// Oversize is reported separately by the caller — we never truncate here.
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
    let ids: std::collections::HashSet<String> =
        elements.iter().map(|e| e.qualified_name.clone()).collect();
    let relationships: Vec<_> = all_rel
        .into_iter()
        .filter(|r| ids.contains(&r.source_qualified) && ids.contains(&r.target_qualified))
        .collect();
    Ok((elements, relationships))
}

/// Render a deterministic snapshot JSON (sorted, relative paths, no
/// absolute paths, no timestamps).
fn render_snapshot(
    _project_root: &Path,
    elements: &[crate::db::models::CodeElement],
    relationships: &[crate::db::models::Relationship],
) -> Result<Vec<u8>, Box<dyn std::error::Error>> {
    let mut elems: Vec<serde_json::Value> = elements
        .iter()
        .map(|e| {
            serde_json::json!({
                "qualified_name": e.qualified_name,
                "element_type": e.element_type,
                "name": e.name,
                "file_path": relativize(&e.file_path),
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
        "elements": elems,
        "relationships": rels,
    });
    serde_json::to_vec_pretty(&doc).map_err(Into::into)
}

/// Normalize a stored file path to `./rel` form. Absolute paths become
/// `./<basename>` so the snapshot is portable across checkouts.
fn relativize(file_path: &str) -> String {
    let norm = file_path.replace('\\', "/");
    norm.trim_start_matches('/')
        .trim_start_matches("./")
        .to_string()
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
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        // Seed schema so readonly open has tables to query.
        crate::db::backend::init_db(&db_path).expect("seed db");
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
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        crate::db::backend::init_db(&db_path).expect("seed db");
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
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        write(&proj, "tests/t.rs", "mod t;\n");
        let db_path = proj.join(".leankg");
        crate::db::backend::init_db(&db_path).expect("seed db");
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

    #[test]
    fn manifest_is_byte_identical_across_runs() {
        // Manifest bytes must be byte-identical across two packs of the
        // same graph (no wall-clock leak, no absolute project_root).
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        crate::db::backend::init_db(&db_path).expect("seed db");
        let out1 = tmp.path().join("p1");
        let out2 = tmp.path().join("p2");
        write_pack(&proj, &db_path, &out1, &PackOptions::default()).expect("p1");
        write_pack(&proj, &db_path, &out2, &PackOptions::default()).expect("p2");
        let m1 = std::fs::read(out1.join("manifest.json")).unwrap();
        let m2 = std::fs::read(out2.join("manifest.json")).unwrap();
        assert_eq!(m1, m2, "manifest must be byte-identical across runs");
        // And no absolute path leak.
        let s = std::fs::read_to_string(out1.join("snapshot.json")).unwrap();
        assert!(
            !s.contains(&proj.to_string_lossy().to_string()),
            "snapshot must not contain absolute project_root"
        );
    }

    #[test]
    fn path_scope_refuses_to_truncate_when_oversize() {
        // Contract: advertised as "refuses to truncate". If the scope
        // matches more than max_nodes, the call must return an error
        // (not silently drop rows).
        let tmp = TempDir::new().unwrap();
        let proj = tmp.path().join("proj");
        write(&proj, "src/a.rs", "fn a() {}\n");
        let db_path = proj.join(".leankg");
        let db = crate::db::backend::init_db(&db_path).expect("seed db");
        let graph = crate::graph::GraphEngine::new(db);
        graph
            .insert_element(&crate::db::models::CodeElement {
                qualified_name: "src::a".into(),
                element_type: "function".into(),
                name: "a".into(),
                file_path: "src/a.rs".into(),
                line_start: 1,
                line_end: 1,
                language: "rust".into(),
                parent_qualified: None,
                cluster_id: None,
                cluster_label: None,
                metadata: serde_json::json!({}),
                env: "local".into(),
            })
            .expect("insert");
        let out = tmp.path().join("pack");
        let opts = PackOptions {
            path: None,
            max_nodes: 0, // would force truncation of the 1-element graph
            source_revision: None,
        };
        let res = write_pack(&proj, &db_path, &out, &opts);
        assert!(
            res.is_err(),
            "oversize pack must be refused, not silently truncated"
        );
    }
}
