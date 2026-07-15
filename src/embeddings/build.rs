//! Embedding build orchestration: incremental vs full rebuild, plus orphan
//! reaping. Implements `cargo run --release -- embed [--full]`.
//!
//! Vectors live in the CozoDB `embedding_vectors` relation (keyed by
//! qualified_name, HNSW index via `::hnsw create embedding_vectors:vec_idx`).
//! The `embedding_state` relation tracks freshness for incremental builds.
//!
//! Incremental flow (default):
//! 1. Walk all `code_elements` and compute the current text blob + hash for
//!    each embeddable node.
//! 2. Diff against `embedding_state`: embed any qualified_name where
//!    (a) no state row exists, OR
//!    (b) `state != "fresh"`, OR
//!    (c) stored `content_hash` differs from the current blob hash.
//! 3. For each batch: run fastembed inference, then `:put embedding_vectors`
//!    in chunks of `UPSERT_CHUNK` (CozoDB pest parser limits).
//! 4. Mark embedded rows fresh in `embedding_state`.
//! 5. Reap orphans: state rows whose qualified_name is no longer in the work
//!    list get their vector removed (`:rm embedding_vectors`) and their state
//!    row deleted.
//!
//! Full rebuild (`--full`): step 2 becomes "embed every embeddable node".

use crate::db::schema::{run_script, CozoDb};
use crate::embeddings::{
    models::{Embedder, EMBEDDING_DIM},
    state::{self, EmbeddingStateRow, FreshRow},
    text_blob,
};
use crate::graph::query::GraphEngine;
use std::path::PathBuf;

/// CozoDB pest parser has stack-depth limits on `<~ [...]` literals; keep
/// each `:put` / `:rm` bounded.
const UPSERT_CHUNK: usize = 500;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BuildMode {
    /// Skip up-to-date rows; embed only stale/missing/changed.
    Incremental,
    /// Re-embed every embeddable CodeElement, regardless of state.
    Full,
}

#[derive(Debug, Clone)]
pub struct BuildOptions {
    pub mode: BuildMode,
    /// Vectors per fastembed call. ONNX Runtime pre-allocates per-thread
    /// memory arenas, so peak RSS scales with batch size.
    pub batch_size: usize,
    /// Accepted for backward-compat with CLI flag; ignored (CozoDB HNSW
    /// manages its own capacity).
    pub reserve_capacity: Option<usize>,
}

impl Default for BuildOptions {
    fn default() -> Self {
        Self {
            mode: BuildMode::Incremental,
            batch_size: 32,
            reserve_capacity: None,
        }
    }
}

#[derive(Debug, Clone, Default)]
pub struct BuildReport {
    pub considered_count: usize,
    pub embedded_count: usize,
    pub skipped_fresh_count: usize,
    pub orphaned_count: usize,
    pub index_size: usize,
    pub index_path: PathBuf,
}

pub fn run(
    graph: &GraphEngine,
    _index_path: &std::path::Path,
    opts: &BuildOptions,
) -> Result<BuildReport, Box<dyn std::error::Error>> {
    let embedder = Embedder::new()?;
    let db = graph.db();

    // 1. Walk code_elements and build the work list.
    let elements = graph.all_elements()?;
    let work: Vec<WorkItem> = elements
        .iter()
        .filter_map(|el| {
            let blob = text_blob::build_blob(el)?;
            let hash = text_blob::content_hash_for(&blob);
            Some(WorkItem {
                qualified_name: el.qualified_name.clone(),
                blob,
                current_hash: hash,
            })
        })
        .collect();

    // 2. Build the "needs embed" set.
    let existing_state: std::collections::HashMap<String, EmbeddingStateRow> = state::list_all(db)?
        .into_iter()
        .map(|r| (r.qualified_name.clone(), r))
        .collect();

    let to_embed: Vec<&WorkItem> = work
        .iter()
        .filter(|w| match opts.mode {
            BuildMode::Full => true,
            BuildMode::Incremental => match existing_state.get(&w.qualified_name) {
                None => true,
                Some(row) => {
                    row.state != "fresh"
                        || row.content_hash.is_empty()
                        || row.content_hash != w.current_hash
                }
            },
        })
        .collect();

    let considered = work.len();
    let skipped_fresh = considered - to_embed.len();

    // 3. Batch embed and :put into embedding_vectors.
    let mut embedded = 0usize;
    let mut fresh_rows: Vec<FreshRow> = Vec::with_capacity(to_embed.len());
    for chunk in to_embed.chunks(opts.batch_size) {
        let texts: Vec<String> = chunk.iter().map(|w| w.blob.clone()).collect();
        let vectors = embedder.embed(&texts)?;
        let pairs: Vec<(&WorkItem, &Vec<f32>)> =
            chunk.iter().copied().zip(vectors.iter()).collect();
        upsert_vectors(db, pairs.iter().copied())?;
        for (item, _vector) in pairs {
            fresh_rows.push(FreshRow {
                qualified_name: item.qualified_name.clone(),
                usearch_key: 0,
                content_hash: item.current_hash.clone(),
            });
            embedded += 1;
        }
        tracing::info!(
            "embed batch done: running total {}/{} (chunk_size={})",
            embedded,
            to_embed.len(),
            chunk.len()
        );
    }

    tracing::info!(
        "embed loop complete, calling upsert_fresh for {} rows",
        fresh_rows.len()
    );
    state::upsert_fresh(db, &fresh_rows)?;
    tracing::info!("upsert_fresh complete");

    // 4. Reap orphans: state rows whose qualified_name is no longer present
    // in the work list (either removed from code_elements, or SKIP-classified
    // element types like clusters/processes that don't get embedded).
    let work_qns: std::collections::HashSet<&str> =
        work.iter().map(|w| w.qualified_name.as_str()).collect();
    let orphan_rows: Vec<EmbeddingStateRow> = existing_state
        .iter()
        .filter(|(qn, _)| !work_qns.contains(qn.as_str()))
        .map(|(_, row)| row.clone())
        .collect();
    tracing::info!("orphan reap: {} orphans", orphan_rows.len());
    if !orphan_rows.is_empty() {
        // Remove vectors from HNSW index first, then state rows.
        let orphan_qns: Vec<String> = orphan_rows
            .iter()
            .map(|r| r.qualified_name.clone())
            .collect();
        remove_vectors(db, &orphan_qns)?;
        tracing::info!(
            "calling delete_state_rows for {} orphans",
            orphan_rows.len()
        );
        state::delete_state_rows(db, &orphan_rows)?;
        tracing::info!("delete_state_rows complete");
    }

    let index_size = count_vectors(db)?;

    Ok(BuildReport {
        considered_count: considered,
        embedded_count: embedded,
        skipped_fresh_count: skipped_fresh,
        orphaned_count: orphan_rows.len(),
        index_size,
        index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
    })
}

/// `:put embedding_vectors {qualified_name => vector}` for a batch.
/// CozoDB `<F32; 384>` literal is `[f32, f32, ...]` — we build it from a
/// `Vec<f32>` via a comma-joined decimal list.
fn upsert_vectors<'a, I>(db: &CozoDb, items: I) -> Result<(), Box<dyn std::error::Error>>
where
    I: Iterator<Item = (&'a WorkItem, &'a Vec<f32>)>,
{
    let rows: Vec<String> = items
        .map(|(item, vector)| {
            let vec_literal = vector
                .iter()
                .map(|f| format!("{:.6}", f))
                .collect::<Vec<_>>()
                .join(", ");
            format!(
                "[{}, vec([{}])]",
                serde_json::Value::String(item.qualified_name.clone()),
                vec_literal
            )
        })
        .collect();
    for chunk in rows.chunks(UPSERT_CHUNK) {
        let values_clause = chunk.join(", ");
        let query = format!(
            r#"?[qualified_name, vector] <- [{values_clause}]
               :put embedding_vectors {{qualified_name => vector}}"#
        );
        run_script(db, &query, Default::default())?;
    }
    Ok(())
}

/// `:rm embedding_vectors {qualified_name}` for a batch of orphans.
fn remove_vectors(db: &CozoDb, qns: &[String]) -> Result<(), Box<dyn std::error::Error>> {
    if qns.is_empty() {
        return Ok(());
    }
    for chunk in qns.chunks(UPSERT_CHUNK) {
        let literals: Vec<String> = chunk
            .iter()
            .map(|qn| format!("[{}]", serde_json::Value::String(qn.clone())))
            .collect();
        let values_clause = literals.join(", ");
        let query = format!(
            r#"?[qualified_name] <- [{values_clause}] :rm embedding_vectors {{qualified_name}}"#
        );
        run_script(db, &query, Default::default())?;
    }
    Ok(())
}

fn count_vectors(db: &CozoDb) -> Result<usize, Box<dyn std::error::Error>> {
    let result = run_script(
        db,
        "?[qualified_name] := *embedding_vectors{qualified_name}",
        Default::default(),
    )?;
    Ok(result.rows.len())
}

struct WorkItem {
    qualified_name: String,
    blob: String,
    current_hash: String,
}

pub const EMBEDDING_DIM_CONST: usize = EMBEDDING_DIM;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_options_batch_size_32() {
        assert_eq!(BuildOptions::default().batch_size, 32);
    }

    #[test]
    fn default_options_mode_is_incremental() {
        assert_eq!(BuildOptions::default().mode, BuildMode::Incremental);
    }

    #[test]
    fn default_options_reserve_capacity_is_none() {
        assert!(BuildOptions::default().reserve_capacity.is_none());
    }

    #[test]
    fn build_mode_variants_are_distinct() {
        assert_ne!(BuildMode::Incremental, BuildMode::Full);
    }

    #[test]
    fn embedding_dim_const_matches_model_dim() {
        assert_eq!(EMBEDDING_DIM_CONST, EMBEDDING_DIM);
        assert_eq!(EMBEDDING_DIM_CONST, 384);
    }

    #[test]
    fn build_report_default_has_zero_counts() {
        let report = BuildReport::default();
        assert_eq!(report.considered_count, 0);
        assert_eq!(report.embedded_count, 0);
        assert_eq!(report.skipped_fresh_count, 0);
        assert_eq!(report.orphaned_count, 0);
        assert_eq!(report.index_size, 0);
    }

    #[test]
    fn upsert_chunk_is_500() {
        // CozoDB pest parser stack-depth limit — documented contract.
        assert_eq!(UPSERT_CHUNK, 500);
    }
}
