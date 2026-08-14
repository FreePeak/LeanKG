//! Offsite embedding pipeline: export embed queries to a file, import the
//! resulting vectors back later.
//!
//! Enables isolation of the expensive inference step from the LeanKG host:
//!
//! ```text
//! host:  leankg embed --dry-run                      → .leankg/embed_export.jsonl
//! gpu:   python scripts/embed_batch.py --in … --out … → import.jsonl
//! host:  leankg embed --import .leankg/import.jsonl  → vectors in DB, state fresh
//! ```
//!
//! ## File format (NDJSON — one record per line, streamable)
//!
//! Line 1 of each file is a meta object (`{"_meta":true,...}`) carrying the
//! vector dimension, model label, and provenance. Lines 2+ are one record per
//! query. Any line whose JSON has a truthy `_meta` field is treated as a meta
//! line and skipped by data consumers.
//!
//! - **Export row**: `{i, qualified_name, blob, content_hash}` — `qualified_name`
//!   is the authoritative join key (PK of `embedding_vectors` +
//!   `embedding_state`); `blob` is the text to embed; `content_hash` is the
//!   SHA-256 of the (truncated) blob; `i` is a 0-based gap-detection index.
//! - **Import row**: `{i, qualified_name, vector, content_hash}` — echoes
//!   `qualified_name` + `content_hash` unchanged and attaches the vector.
//!
//! ## Correctness guarantees
//!
//! 1. **One row per query**: the export writes exactly one row per
//!    `WorkItem` the live pipeline would embed; the import reads exactly one
//!    row per vector and keys it back by `qualified_name`.
//! 2. **Resume**: `import_vectors` skips any row already `fresh` in
//!    `embedding_state` with a matching `content_hash`, so re-running after a
//!    kill picks up where it left off (same rule the live writer uses).
//! 3. **Drift safety** (default): when `verify=true`, import rebuilds the
//!    current `content_hash` from the live graph and skips rows whose element
//!    drifted (hash mismatch) or vanished (orphan) — the next normal `embed`
//!    re-embeds them. `--no-verify` trusts the file (faster, risk of stale
//!    vectors if the graph changed).

use std::collections::{HashMap, HashSet};
use std::io::{BufRead, BufReader, Write};
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use crate::embeddings::build::{
    collect_incremental_dirty_work, collect_work_items, count_vectors, effective_upsert_chunk,
    populate_file_size_cache, should_escalate_incremental_to_full, upsert_pairs_to_db,
};
use crate::embeddings::control::{embed_resume_preflight, should_use_incremental_hnsw_puts};
use crate::embeddings::provider::vec_dim;
use crate::embeddings::state::{self, EmbeddingStateRow, FreshRow};
use crate::embeddings::text_blob;
use crate::embeddings::{active_profile, BuildMode, BuildOptions};
use crate::graph::query::GraphEngine;

/// `format` field value written into export-file meta lines.
pub const META_FORMAT_EXPORT: &str = "leankg-embed-export";
/// `format` field value written into / expected from import-file meta lines.
pub const META_FORMAT_IMPORT: &str = "leankg-embed-import";
/// On-disk schema version for both export and import meta lines.
pub const META_VERSION: u32 = 1;

/// Below this QN count, import uses per-QN `find_element` for the verify pass;
/// above it, a single paginated scan of `code_elements` (mirrors the live
/// incremental pipeline's `INCREMENTAL_POINT_LOOKUP_CAP` heuristic).
const VERIFY_POINT_LOOKUP_CAP: usize = 2_000;

// ---------------------------------------------------------------------------
// serde types
// ---------------------------------------------------------------------------

/// Leading self-describing record of an export/import file. `format` selects
/// between [`META_FORMAT_EXPORT`] and [`META_FORMAT_IMPORT`]; the other fields
/// are informational provenance.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MetaLine {
    /// Discriminator — always `true`; lets consumers skip meta records inline.
    #[serde(rename = "_meta")]
    pub meta: bool,
    pub format: String,
    pub version: u32,
    pub vec_dim: usize,
    pub model: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub profile: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub mode: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub source_export: Option<String>,
    pub created_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub item_count: Option<usize>,
}

/// One text query in an export file — exactly what the live pipeline would
/// send to the embedder for this `qualified_name`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExportRow {
    pub i: u64,
    pub qualified_name: String,
    pub blob: String,
    pub content_hash: String,
}

/// One embedded vector in an import file — the answer produced from the
/// matching [`ExportRow`] by an external embedder.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ImportRow {
    #[serde(default)]
    pub i: u64,
    pub qualified_name: String,
    pub vector: Vec<f32>,
    pub content_hash: String,
}

// ---------------------------------------------------------------------------
// reports
// ---------------------------------------------------------------------------

/// Summary of a `--dry-run` export run.
#[derive(Debug, Clone)]
pub struct ExportReport {
    pub item_count: usize,
    pub skipped_fresh: usize,
    pub export_path: PathBuf,
    pub vec_dim: usize,
    pub model: String,
}

/// Summary of a `--import` run.
#[derive(Debug, Clone)]
pub struct ImportReport {
    pub imported: usize,
    pub skipped_resume: usize,
    pub skipped_drift: usize,
    pub skipped_orphan: usize,
    pub vec_dim: usize,
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

fn now_unix_secs() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs().to_string())
        .unwrap_or_else(|_| "0".into())
}

/// Informational model label for the meta line. Local ONNX → the
/// `EmbedModelKind` label; OpenAI-compatible → `LEANKG_EMBED_MODEL` (or a
/// placeholder). Used only for provenance — the authoritative width is
/// `vec_dim()`.
fn provider_model_label() -> String {
    use crate::embeddings::models::EmbedModelKind;
    use crate::embeddings::provider::{provider_kind_from_env, ProviderKind};
    if matches!(provider_kind_from_env(), Ok(ProviderKind::OpenAi)) {
        std::env::var("LEANKG_EMBED_MODEL").unwrap_or_else(|_| "openai-compatible".into())
    } else {
        EmbedModelKind::from_env().label().to_string()
    }
}

/// Build a `qualified_name -> current content_hash` map for the given QN set,
/// recomputing blobs from the live graph (so a profile or code change between
/// export and import is detected).
fn collect_current_hashes(
    graph: &GraphEngine,
    qns: &HashSet<String>,
) -> Result<HashMap<String, String>, Box<dyn std::error::Error>> {
    let mut out: HashMap<String, String> = HashMap::with_capacity(qns.len());
    if qns.is_empty() {
        return Ok(out);
    }
    if qns.len() <= VERIFY_POINT_LOOKUP_CAP {
        for qn in qns {
            if let Some(el) = graph.find_element(qn)? {
                if let Some(blob) = text_blob::build_blob(&el) {
                    out.insert(qn.clone(), text_blob::content_hash_for(&blob));
                }
            }
        }
        return Ok(out);
    }
    // Large set: one paginated scan of code_elements, HashSet-filtered.
    let total = graph.count_elements().unwrap_or(0);
    let page_size = 5_000usize;
    let mut offset = 0usize;
    loop {
        let (page, _) = graph.get_elements_paginated(page_size, offset)?;
        if page.is_empty() {
            break;
        }
        offset += page.len();
        for el in page {
            if out.len() >= qns.len() {
                break;
            }
            if !qns.contains(&el.qualified_name) {
                continue;
            }
            if let Some(blob) = text_blob::build_blob(&el) {
                out.insert(
                    el.qualified_name.clone(),
                    text_blob::content_hash_for(&blob),
                );
            }
        }
        if offset >= total {
            break;
        }
    }
    Ok(out)
}

// ---------------------------------------------------------------------------
// export (--dry-run)
// ---------------------------------------------------------------------------

/// Collect embed queries exactly as the live pipeline would, then write them
/// to `export_path` as NDJSON (meta line + one [`ExportRow`] per query).
///
/// Non-mutating: does not call the embedder, does not touch `embedding_vectors`
/// or `embedding_state`, and does not drop/rebuild HNSW. The export is a
/// faithful snapshot of what the next real `embed` run would process (same
/// Incremental/Full escalation, same work-list collectors).
pub fn export_work_items(
    graph: &GraphEngine,
    opts: &BuildOptions,
    export_path: &Path,
) -> Result<ExportReport, Box<dyn std::error::Error>> {
    let db = graph.db();
    let mut opts = opts.clone();
    populate_file_size_cache(graph, &mut opts)?;

    // Mirror the live pipeline's Incremental→Full self-heal so the export
    // represents what a real run would do, not an empty dirty set left behind
    // by a wiped vector store.
    let preflight = embed_resume_preflight(db).ok();
    let fresh_state_rows = preflight.as_ref().map(|p| p.fresh).unwrap_or(0);
    let vectors_existing = count_vectors(db).unwrap_or(0);
    if matches!(opts.mode, BuildMode::Incremental)
        && should_escalate_incremental_to_full(vectors_existing, fresh_state_rows)
    {
        tracing::info!(
            "export: escalating Incremental -> Full (fresh={} vectors={})",
            fresh_state_rows,
            vectors_existing
        );
        opts.mode = BuildMode::Full;
    }

    let (work, skipped_fresh) = match opts.mode {
        BuildMode::Incremental => {
            let (w, _orphans, fresh) = collect_incremental_dirty_work(graph, &opts)?;
            (w, fresh)
        }
        BuildMode::Full => (collect_work_items(graph, &opts)?, 0),
    };

    let vec_dim = vec_dim();
    let model = provider_model_label();
    let meta = MetaLine {
        meta: true,
        format: META_FORMAT_EXPORT.to_string(),
        version: META_VERSION,
        vec_dim,
        model: model.clone(),
        profile: Some(active_profile().label().to_string()),
        mode: Some(format!("{:?}", opts.mode)),
        source_export: None,
        created_at: now_unix_secs(),
        item_count: Some(work.len()),
    };

    if let Some(parent) = export_path.parent() {
        if !parent.as_os_str().is_empty() {
            std::fs::create_dir_all(parent)?;
        }
    }

    let file = std::fs::File::create(export_path)?;
    let mut writer = std::io::BufWriter::new(file);
    writeln!(writer, "{}", serde_json::to_string(&meta)?)?;
    for (i, item) in work.iter().enumerate() {
        let row = ExportRow {
            i: i as u64,
            qualified_name: item.qualified_name.clone(),
            blob: item.blob.clone(),
            content_hash: item.current_hash.clone(),
        };
        writeln!(writer, "{}", serde_json::to_string(&row)?)?;
    }
    writer.flush()?;

    Ok(ExportReport {
        item_count: work.len(),
        skipped_fresh,
        export_path: export_path.to_path_buf(),
        vec_dim,
        model,
    })
}

// ---------------------------------------------------------------------------
// import (--import)
// ---------------------------------------------------------------------------

/// Parse an import NDJSON file: meta line first, then one [`ImportRow`] per
/// line. Blank lines and stray `_meta` lines are tolerated.
fn parse_import_file(
    path: &Path,
) -> Result<(MetaLine, Vec<ImportRow>), Box<dyn std::error::Error>> {
    let file = std::fs::File::open(path)?;
    let reader = BufReader::new(file);
    let mut lines = reader.lines();
    let first = lines
        .next()
        .ok_or_else(|| -> Box<dyn std::error::Error> { "import file is empty".into() })??;
    let meta: MetaLine =
        serde_json::from_str(&first).map_err(|e| -> Box<dyn std::error::Error> {
            format!("import file must start with a meta line: {e}").into()
        })?;

    let mut rows: Vec<ImportRow> = Vec::new();
    for line in lines {
        let line = line?;
        if line.trim().is_empty() {
            continue;
        }
        // Tolerate stray meta lines anywhere in the file.
        if let Ok(val) = serde_json::from_str::<serde_json::Value>(&line) {
            if val.get("_meta").is_some() {
                continue;
            }
        }
        let row: ImportRow = serde_json::from_str(&line)?;
        rows.push(row);
    }
    Ok((meta, rows))
}

/// Read vectors produced from a `--dry-run` export file (typically by
/// `scripts/embed_batch.py`) and upsert them into the DB, stamping
/// `embedding_state` fresh in the same batches — the exact pair the live
/// parallel writer uses, so resume semantics are identical.
///
/// - **Dim guard**: refuses if the file's `vec_dim` ≠ runtime `vec_dim()`.
/// - **Resume**: skips any row already `fresh` with matching `content_hash`.
/// - **Verify** (`verify=true`, default): skips rows whose element drifted
///   (current hash ≠ file hash) or vanished (orphan). Pass `verify=false` to
///   trust the file blindly (faster; can leave stale vectors if the graph
///   changed).
pub fn import_vectors(
    graph: &GraphEngine,
    import_path: &Path,
    verify: bool,
) -> Result<ImportReport, Box<dyn std::error::Error>> {
    let db = graph.db();
    let (meta, rows) = parse_import_file(import_path)?;

    if meta.format != META_FORMAT_IMPORT {
        return Err(format!(
            "import file format mismatch: expected {META_FORMAT_IMPORT}, got {}",
            meta.format
        )
        .into());
    }
    let runtime_dim = vec_dim();
    if meta.vec_dim != runtime_dim {
        return Err(format!(
            "vec_dim mismatch: file={} runtime={runtime_dim}; \
             set LEANKG_EMBED_DIM to match the file before importing",
            meta.vec_dim
        )
        .into());
    }

    let total = rows.len();

    // Resume map: qn -> existing state row.
    let existing_state: HashMap<String, EmbeddingStateRow> = state::list_all(db)?
        .into_iter()
        .map(|r| (r.qualified_name.clone(), r))
        .collect();

    // Verify map: qn -> current content_hash from the live graph.
    let current_hashes: HashMap<String, String> = if verify {
        let qns: HashSet<String> = rows.iter().map(|r| r.qualified_name.clone()).collect();
        tracing::info!(
            "import: verifying {} unique qns against live graph",
            qns.len()
        );
        collect_current_hashes(graph, &qns)?
    } else {
        HashMap::new()
    };

    let vectors_existing = count_vectors(db).unwrap_or(0);
    let use_incr_hnsw = should_use_incremental_hnsw_puts(total, vectors_existing);
    if use_incr_hnsw {
        tracing::info!(
            "import: keeping HNSW live for incremental puts ({} rows)",
            total
        );
    } else {
        tracing::info!(
            "import: dropping HNSW index for bulk import ({} rows)",
            total
        );
        let _ = state::drop_hnsw_index(db);
    }

    let chunk_size = effective_upsert_chunk();
    let mut pending_pairs: Vec<(String, Vec<f32>)> = Vec::with_capacity(chunk_size);
    let mut pending_fresh: Vec<FreshRow> = Vec::with_capacity(chunk_size);
    let mut imported = 0usize;
    let mut skipped_resume = 0usize;
    let mut skipped_drift = 0usize;
    let mut skipped_orphan = 0usize;

    for row in rows {
        // Resume: already fresh with matching hash → skip.
        if let Some(st) = existing_state.get(&row.qualified_name) {
            if st.state == "fresh" && st.content_hash == row.content_hash {
                skipped_resume += 1;
                continue;
            }
        }
        // Verify (default): drifted / orphaned → skip.
        if verify {
            match current_hashes.get(&row.qualified_name) {
                Some(h) if h == &row.content_hash => {}
                Some(_) => {
                    skipped_drift += 1;
                    continue;
                }
                None => {
                    skipped_orphan += 1;
                    continue;
                }
            }
        }
        // Per-row width sanity (guards a malformed file against pgvector).
        if row.vector.len() != runtime_dim {
            return Err(format!(
                "vector width mismatch for {}: got {} expected {runtime_dim}",
                row.qualified_name,
                row.vector.len()
            )
            .into());
        }
        pending_pairs.push((row.qualified_name.clone(), row.vector.clone()));
        pending_fresh.push(FreshRow {
            qualified_name: row.qualified_name.clone(),
            usearch_key: 0,
            content_hash: row.content_hash.clone(),
        });
        if pending_pairs.len() >= chunk_size {
            upsert_pairs_to_db(db, &pending_pairs, use_incr_hnsw)?;
            state::upsert_fresh(db, &pending_fresh)?;
            imported += pending_pairs.len();
            tracing::info!(
                "import: flushed {} rows, total {}",
                pending_pairs.len(),
                imported
            );
            pending_pairs.clear();
            pending_fresh.clear();
        }
    }
    // Final flush.
    if !pending_pairs.is_empty() {
        upsert_pairs_to_db(db, &pending_pairs, use_incr_hnsw)?;
        state::upsert_fresh(db, &pending_fresh)?;
        imported += pending_pairs.len();
        tracing::info!(
            "import: final flush {} rows, total {}",
            pending_pairs.len(),
            imported
        );
    }

    if !use_incr_hnsw {
        tracing::info!("import: rebuilding HNSW index");
        state::create_hnsw_index(db)?;
    }
    if let Err(e) = crate::graph::inventory::refresh_index_inventory(graph, "embed_import") {
        tracing::warn!("index_inventory refresh after import failed: {}", e);
    }

    Ok(ImportReport {
        imported,
        skipped_resume,
        skipped_drift,
        skipped_orphan,
        vec_dim: runtime_dim,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::CodeElement;
    use crate::embeddings::provider::VEC_DIM;

    fn fresh_db() -> crate::db::backend::SharedDb {
        let tmp = tempfile::tempdir().expect("tempdir");
        let db_path = tmp.path().join("offsite.db");
        // Leak the tempdir so the SQLite/Fake file survives the test body.
        std::mem::forget(tmp);
        let db = crate::db::backend::init_db(&db_path).expect("init_db");
        crate::embeddings::state::ensure_embedding_state_table(db.as_ref()).expect("ensure tables");
        db
    }

    fn make_graph_with_elements(qns: &[&str]) -> crate::graph::GraphEngine {
        let db = fresh_db();
        let graph = crate::graph::GraphEngine::new(db);
        for (i, qn) in qns.iter().enumerate() {
            let el = CodeElement {
                qualified_name: (*qn).to_string(),
                element_type: "function".to_string(),
                name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
                file_path: format!("src/mod_{i}.rs"),
                line_start: 1,
                line_end: 10,
                language: "rust".to_string(),
                parent_qualified: None,
                cluster_id: None,
                cluster_label: None,
                metadata: serde_json::json!({"doc_comment": "does the thing"}),
                env: "local".to_string(),
            };
            graph.insert_element(&el).expect("insert_element");
        }
        graph
    }

    #[test]
    fn serde_round_trips() {
        let export = ExportRow {
            i: 7,
            qualified_name: "src/main.rs::main".into(),
            blob: "fn main() {}".into(),
            content_hash: "abc123".into(),
        };
        let s = serde_json::to_string(&export).unwrap();
        let back: ExportRow = serde_json::from_str(&s).unwrap();
        assert_eq!(back.i, 7);
        assert_eq!(back.qualified_name, "src/main.rs::main");

        let imp = ImportRow {
            i: 7,
            qualified_name: "src/main.rs::main".into(),
            vector: vec![0.1; VEC_DIM],
            content_hash: "abc123".into(),
        };
        let s = serde_json::to_string(&imp).unwrap();
        let back: ImportRow = serde_json::from_str(&s).unwrap();
        assert_eq!(back.vector.len(), VEC_DIM);
        assert_eq!(back.content_hash, "abc123");

        let meta = MetaLine {
            meta: true,
            format: META_FORMAT_EXPORT.into(),
            version: META_VERSION,
            vec_dim: VEC_DIM,
            model: "bge-fp32".into(),
            profile: Some("small".into()),
            mode: Some("Full".into()),
            source_export: None,
            created_at: "123".into(),
            item_count: Some(42),
        };
        let s = serde_json::to_string(&meta).unwrap();
        assert!(s.contains("\"_meta\":true"));
        let back: MetaLine = serde_json::from_str(&s).unwrap();
        assert_eq!(back.format, META_FORMAT_EXPORT);
    }

    #[test]
    fn export_writes_one_row_per_work_item_and_is_gap_free() {
        let qns = ["src/a.rs::foo", "src/b.rs::bar", "src/c.rs::baz"];
        let graph = make_graph_with_elements(&qns);
        let opts = BuildOptions {
            mode: BuildMode::Full,
            ..Default::default()
        };
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("export.jsonl");
        let report = export_work_items(&graph, &opts, &path).expect("export");

        assert_eq!(report.item_count, qns.len());
        assert_eq!(report.vec_dim, vec_dim());

        let lines: Vec<String> = std::fs::read_to_string(&path)
            .expect("read")
            .lines()
            .filter(|l| !l.is_empty())
            .map(String::from)
            .collect();
        // meta + N rows
        assert_eq!(lines.len(), qns.len() + 1);
        let meta: MetaLine = serde_json::from_str(&lines[0]).unwrap();
        assert_eq!(meta.format, META_FORMAT_EXPORT);
        assert_eq!(meta.item_count, Some(qns.len()));

        let mut seen_qns = std::collections::HashSet::new();
        for (idx, line) in lines[1..].iter().enumerate() {
            let row: ExportRow = serde_json::from_str(line).unwrap();
            assert_eq!(row.i as usize, idx, "i must be gap-free 0..N");
            assert!(!row.blob.is_empty(), "blob must be non-empty");
            assert!(!row.content_hash.is_empty());
            seen_qns.insert(row.qualified_name);
        }
        for qn in &qns {
            assert!(seen_qns.contains(*qn), "missing {qn} in export");
        }
    }

    #[test]
    fn export_is_non_mutating() {
        let qns = ["src/a.rs::foo"];
        let graph = make_graph_with_elements(&qns);
        let db = graph.db();
        let opts = BuildOptions {
            mode: BuildMode::Full,
            ..Default::default()
        };
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("export.jsonl");

        let vectors_before = count_vectors(db).unwrap_or(0);
        let state_before = state::list_all(db).expect("list_all").len();
        export_work_items(&graph, &opts, &path).expect("export");
        let vectors_after = count_vectors(db).unwrap_or(0);
        let state_after = state::list_all(db).expect("list_all").len();

        assert_eq!(vectors_before, vectors_after);
        assert_eq!(state_before, state_after);
    }

    fn write_import_file(path: &Path, rows: &[(String, Vec<f32>, String)]) {
        let mut f = std::fs::File::create(path).expect("create");
        let meta = MetaLine {
            meta: true,
            format: META_FORMAT_IMPORT.into(),
            version: META_VERSION,
            vec_dim: vec_dim(),
            model: "test".into(),
            profile: None,
            mode: None,
            source_export: Some("export.jsonl".into()),
            created_at: "0".into(),
            item_count: None,
        };
        writeln!(f, "{}", serde_json::to_string(&meta).unwrap()).unwrap();
        for (i, (qn, vec, hash)) in rows.iter().enumerate() {
            let row = ImportRow {
                i: i as u64,
                qualified_name: qn.clone(),
                vector: vec.clone(),
                content_hash: hash.clone(),
            };
            writeln!(f, "{}", serde_json::to_string(&row).unwrap()).unwrap();
        }
    }

    #[test]
    fn import_writes_vectors_and_stamps_state_fresh() {
        let qns = ["src/a.rs::foo", "src/b.rs::bar"];
        let graph = make_graph_with_elements(&qns);
        let db = graph.db();

        // Build an import file whose content_hashes match the live graph.
        let mut rows = Vec::new();
        for qn in &qns {
            let el = graph.find_element(qn).expect("find").expect("present");
            let blob = text_blob::build_blob(&el).expect("blob");
            let hash = text_blob::content_hash_for(&blob);
            rows.push((qn.to_string(), vec![0.125f32; vec_dim()], hash));
        }
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("import.jsonl");
        write_import_file(&path, &rows);

        let report = import_vectors(&graph, &path, true).expect("import");
        assert_eq!(report.imported, qns.len());
        assert_eq!(report.skipped_resume, 0);
        assert_eq!(report.skipped_drift, 0);
        assert_eq!(report.skipped_orphan, 0);

        // Vectors landed and state is fresh with the file's hash.
        assert_eq!(count_vectors(db).unwrap_or(0), qns.len());
        for r in state::list_all(db).expect("list_all") {
            assert_eq!(r.state, "fresh");
            assert!(!r.content_hash.is_empty());
        }
    }

    #[test]
    fn import_resume_is_idempotent() {
        let qns = ["src/a.rs::foo", "src/b.rs::bar", "src/c.rs::baz"];
        let graph = make_graph_with_elements(&qns);

        let mut rows = Vec::new();
        for qn in &qns {
            let el = graph.find_element(qn).expect("find").expect("present");
            let blob = text_blob::build_blob(&el).expect("blob");
            let hash = text_blob::content_hash_for(&blob);
            rows.push((qn.to_string(), vec![0.25f32; vec_dim()], hash));
        }
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("import.jsonl");
        write_import_file(&path, &rows);

        let first = import_vectors(&graph, &path, true).expect("first import");
        assert_eq!(first.imported, qns.len());
        let second = import_vectors(&graph, &path, true).expect("resume import");
        assert_eq!(second.imported, 0);
        assert_eq!(second.skipped_resume, qns.len());
    }

    #[test]
    fn import_verify_skips_drifted_and_orphaned() {
        // Live graph has `foo` only.
        let qns_live = ["src/a.rs::foo"];
        let graph = make_graph_with_elements(&qns_live);

        // Import file claims `foo` (with a WRONG hash → drifted), `bar`
        // (absent from graph → orphan), and `foo_again` reuse for a
        // well-formed row. We construct hashes explicitly to force drift.
        let good_hash = {
            let el = graph
                .find_element("src/a.rs::foo")
                .expect("find")
                .expect("present");
            let blob = text_blob::build_blob(&el).expect("blob");
            text_blob::content_hash_for(&blob)
        };
        let rows = vec![
            (
                "src/a.rs::foo".to_string(),
                vec![0.5f32; vec_dim()],
                "deadbeef".to_string(), // drifted
            ),
            (
                "src/gone.rs::bar".to_string(),
                vec![0.5f32; vec_dim()],
                "deadbeef".to_string(), // orphan
            ),
            (
                "src/a.rs::foo".to_string(),
                vec![0.5f32; vec_dim()],
                good_hash.clone(), // valid
            ),
        ];
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("import.jsonl");
        write_import_file(&path, &rows);

        let report = import_vectors(&graph, &path, true).expect("import");
        assert_eq!(report.imported, 1);
        assert_eq!(report.skipped_drift, 1);
        assert_eq!(report.skipped_orphan, 1);
    }

    #[test]
    fn import_refuses_on_vec_dim_mismatch() {
        let graph = make_graph_with_elements(&["src/a.rs::foo"]);
        let tmp = tempfile::tempdir().expect("tempdir");
        let path = tmp.path().join("import.jsonl");
        // Hand-craft a meta line with a wrong vec_dim.
        let meta = serde_json::json!({
            "_meta": true,
            "format": META_FORMAT_IMPORT,
            "version": META_VERSION,
            "vec_dim": vec_dim() + 1,
            "model": "test",
            "created_at": "0",
        });
        let mut f = std::fs::File::create(&path).expect("create");
        writeln!(f, "{meta}").unwrap();
        writeln!(
            f,
            "{{\"i\":0,\"qualified_name\":\"x\",\"vector\":[],\"content_hash\":\"h\"}}"
        )
        .unwrap();

        let err = import_vectors(&graph, &path, true).unwrap_err();
        let msg = err.to_string();
        assert!(msg.contains("vec_dim mismatch"), "got: {msg}");
    }
}
