//! Embedding build orchestration: incremental vs full rebuild, plus orphan
//! reaping. Implements `cargo run --release -- embed [--full]`.
//!
//! Incremental flow (default):
//! 1. Load (or create) the usearch index from `embeddings.usearch`.
//! 2. Walk all `code_elements` and compute the current text blob + hash for
//!    each embeddable node.
//! 3. Diff against `embedding_state`: embed any qualified_name where
//!    (a) no state row exists, OR
//!    (b) `state != "fresh"`, OR
//!    (c) stored `content_hash` differs from the current blob hash.
//! 4. Reap orphans: state rows whose qualified_name is no longer in
//!    `code_elements` get their vector removed from usearch and their row
//!    deleted.
//! 5. Persist `embeddings.usearch` + `embeddings.meta.json`.
//!
//! Full rebuild (`--full`): step 3 becomes "embed every embeddable node".

use crate::embeddings::{
    index::AnnIndex,
    models::{EMBEDDING_DIM, Embedder},
    state::{self, FreshRow},
    text_blob,
};
use crate::graph::query::GraphEngine;
use std::path::{Path, PathBuf};

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
    /// Vectors per embed call. fastembed handles batching internally; we
    /// chunk to keep peak memory bounded on very large repos.
    pub batch_size: usize,
    /// Optional capacity hint for the usearch index. If None, reserve to
    /// the current element count + 10% headroom.
    pub reserve_capacity: Option<usize>,
}

impl Default for BuildOptions {
    fn default() -> Self {
        Self {
            mode: BuildMode::Incremental,
            batch_size: 256,
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
    index_path: &Path,
    opts: &BuildOptions,
) -> Result<BuildReport, Box<dyn std::error::Error>> {
    let embedder = Embedder::new()?;
    let dim = embedder.dim();

    let index = if index_path.exists() {
        match AnnIndex::load(index_path) {
            Ok(loaded) if loaded.dim() == dim => loaded,
            Ok(loaded) => {
                tracing::warn!(
                    "existing index dim {} != model dim {}; rebuilding from scratch",
                    loaded.dim(),
                    dim
                );
                AnnIndex::new(dim)?
            }
            Err(e) => {
                tracing::warn!("failed to load existing index ({}); rebuilding", e);
                AnnIndex::new(dim)?
            }
        }
    } else {
        let new = AnnIndex::new(dim)?;
        if let Some(cap) = opts.reserve_capacity {
            new.reserve(cap)?;
        }
        new
    };

    // 1. Walk code_elements and build the work list.
    let elements = graph.all_elements()?;
    let work: Vec<WorkItem> = elements
        .iter()
        .filter_map(|el| {
            let blob = text_blob::build_blob(el)?;
            let hash = text_blob::content_hash_for(&blob);
            let key = text_blob::usearch_key_for(&el.qualified_name);
            Some(WorkItem {
                qualified_name: el.qualified_name.clone(),
                blob,
                current_hash: hash,
                key,
            })
        })
        .collect();

    // 2. Build the "needs embed" set.
    let existing_state: std::collections::HashMap<String, state::EmbeddingStateRow> = state::list_all(graph.db())?
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

    // 3. Reserve usearch capacity ahead of any insertions. usearch panics
    // ("Reserve capacity ahead of insertions!") if you add before reserving.
    // Use the existing index size + the new embed count as a lower bound,
    // with 10% headroom for future incremental runs.
    let needed_capacity = match opts.reserve_capacity {
        Some(cap) => cap,
        None => index.size() + to_embed.len() + (to_embed.len() / 10).max(16),
    };
    if needed_capacity > index.size() {
        index.reserve(needed_capacity)?;
    }

    // 4. Batch embed and add to usearch.
    let mut embedded = 0usize;
    let mut fresh_rows: Vec<FreshRow> = Vec::with_capacity(to_embed.len());
    for chunk in to_embed.chunks(opts.batch_size) {
        let texts: Vec<String> = chunk.iter().map(|w| w.blob.clone()).collect();
        let vectors = embedder.embed(&texts)?;
        for (item, vector) in chunk.iter().zip(vectors.iter()) {
            // Remove the old vector if it exists (usearch `add` does NOT
            // overwrite by default — it can leave duplicate keys).
            let _ = index.remove(item.key);
            index.add(item.key, vector)?;
            fresh_rows.push(FreshRow {
                qualified_name: item.qualified_name.clone(),
                usearch_key: item.key,
                content_hash: item.current_hash.clone(),
            });
            embedded += 1;
        }
    }

    // 4. Persist fresh state.
    state::upsert_fresh(graph.db(), &fresh_rows)?;

    // 5. Reap orphans: state rows whose qualified_name is no longer present.
    let work_qns: std::collections::HashSet<&str> =
        work.iter().map(|w| w.qualified_name.as_str()).collect();
    let orphans: Vec<String> = existing_state
        .keys()
        .filter(|qn| !work_qns.contains(qn.as_str()))
        .cloned()
        .collect();
    for qn in &orphans {
        if let Ok(Some(key)) = state::lookup_usearch_key(graph.db(), qn) {
            let _ = index.remove(key);
        }
    }
    if !orphans.is_empty() {
        state::delete_state_rows(graph.db(), &orphans)?;
    }

    // 6. Persist index + meta.
    index.save(index_path)?;
    write_meta(index_path, dim, embedded, index.size())?;

    Ok(BuildReport {
        considered_count: considered,
        embedded_count: embedded,
        skipped_fresh_count: skipped_fresh,
        orphaned_count: orphans.len(),
        index_size: index.size(),
        index_path: index_path.to_path_buf(),
    })
}

struct WorkItem {
    qualified_name: String,
    blob: String,
    current_hash: String,
    key: u64,
}

#[derive(Debug, Clone, serde::Serialize)]
struct IndexMeta {
    model_id: &'static str,
    dim: usize,
    metric: &'static str,
    size: usize,
    built_at: u64,
}

fn write_meta(index_path: &Path, dim: usize, _embedded: usize, size: usize) -> Result<(), Box<dyn std::error::Error>> {
    let meta = IndexMeta {
        model_id: "BAAI/bge-small-en-v1.5",
        dim,
        metric: "cosine",
        size,
        built_at: std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_secs())
            .unwrap_or(0),
    };
    let meta_path = meta_path_for(index_path);
    let bytes = serde_json::to_vec_pretty(&meta)?;
    std::fs::write(&meta_path, bytes)?;
    Ok(())
}

pub fn meta_path_for(index_path: &Path) -> PathBuf {
    let mut p = index_path.to_path_buf();
    p.set_extension("meta.json");
    p
}

pub const EMBEDDING_DIM_CONST: usize = EMBEDDING_DIM;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn meta_path_swaps_extension() {
        let p = PathBuf::from("/tmp/.leankg/embeddings.usearch");
        let meta = meta_path_for(&p);
        assert_eq!(meta.file_name().unwrap(), "embeddings.meta.json");
    }

    // End-to-end build tests live in /tests/embeddings_build_e2e.rs (Phase 6).
    // They require a live CozoDB + fastembed model cache, so they aren't run
    // as part of `cargo test` on machines without the `embeddings` feature.
}
