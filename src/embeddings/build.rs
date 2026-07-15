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
use std::io::Write;
use std::path::PathBuf;

#[cfg(feature = "embeddings")]
use crate::embeddings::build_index;

/// CozoDB pest parser has stack-depth limits on inline `<~ [...]` literals
/// (limit ≈ 500 rows). We use *parameterized* queries
/// (`?[col] <- $rows :put ...`) so the limit does NOT apply here. The
/// practical bottleneck is the per-:put CozoDB transaction commit
/// (~10s regardless of batch size), so larger UPSERT_CHUNK amortizes
/// that fixed cost across more rows. 5000 was the empirical sweet spot
/// on a 400k-row workspace: ~6 min total vs ~120 min at UPSERT_CHUNK=500.
///
/// Runtime override via `LEANKG_EMBED_UPSERT_CHUNK` env var (read by
/// `effective_upsert_chunk`). Smaller chunks (500-1000) lower peak
/// memory per flush but commit more often; larger chunks (10000+)
/// reduce commit overhead at the cost of a higher per-flush RSS spike
/// and longer tail latency if the run crashes mid-flush.
const DEFAULT_UPSERT_CHUNK: usize = 5000;

fn effective_upsert_chunk() -> usize {
    std::env::var("LEANKG_EMBED_UPSERT_CHUNK")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|n| (100..=50_000).contains(n))
        .unwrap_or(DEFAULT_UPSERT_CHUNK)
}

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
    /// When set, only embed `CodeElement`s whose `element_type` is in this
    /// set (case-insensitive). Default (`None`) embeds every type. The CLI
    /// defaults to `function,method` on mega-graphs to keep cold embed
    /// under 5 min; pass `all` (empty string from CLI) to disable.
    pub type_filter: Option<std::collections::HashSet<String>>,
}

impl Default for BuildOptions {
    fn default() -> Self {
        Self {
            mode: BuildMode::Incremental,
            // 64 = the empirical sweet spot for BGE-small ONNX inference
            // on 10-core x86_64 / arm64. Smaller (16-32) leaves the
            // workers idle between CozoDB commits; larger (128+) blows up
            // RSS because each session pre-allocates per-batch arenas.
            batch_size: 64,
            reserve_capacity: None,
            type_filter: None,
        }
    }
}

/// Parse a `--types` flag value into a `BuildOptions::type_filter`. Empty
/// string or `all` => embed every type. Comma-separated list => embed only
/// those types. Match is case-insensitive.
pub fn parse_type_filter(raw: &str) -> Option<std::collections::HashSet<String>> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed.eq_ignore_ascii_case("all") {
        return None;
    }
    Some(
        trimmed
            .split(',')
            .map(|s| s.trim().to_ascii_lowercase())
            .filter(|s| !s.is_empty())
            .collect(),
    )
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
        .filter(|el| {
            // Apply the optional element-type filter. On mega-graphs the
            // CLI defaults to `function,method` to keep cold embed under
            // 5 min; pass `--types all` to embed every type.
            if let Some(filter) = &opts.type_filter {
                filter.contains(&el.element_type.to_ascii_lowercase())
            } else {
                true
            }
        })
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

    // FR-HNSW perf fix: drop the HNSW index before the bulk insert so
    // each :put doesn't pay the O(log N) HNSW update cost. The index is
    // recreated at the end of the function.
    if state::drop_hnsw_index(db).is_err() {
        tracing::warn!("could not drop HNSW index before bulk insert (continuing)");
    }
    tracing::info!("HNSW dropped; running sequential bulk insert");

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

    // Recreate the HNSW index now that the bulk insert is done. A single
    // O(N log N) build beats N incremental updates by 5-10x.
    tracing::info!("rebuilding HNSW index on embedding_vectors:vec_idx");
    let hnsw_started = std::time::Instant::now();
    state::create_hnsw_index(db)?;
    tracing::info!(
        "HNSW rebuild complete in {:.2}s",
        hnsw_started.elapsed().as_secs_f64()
    );

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

/// Parallel-inference + single-writer pipeline. `N` rayon worker threads
/// each own a fastembed session and run inference on disjoint work
/// shards. Completed `(qualified_name, vector)` pairs are pushed onto a
/// bounded crossbeam channel; a single writer thread consumes the
/// channel, accumulating up to `UPSERT_CHUNK` rows per `:put` so the
/// CozoDB parser overhead is amortized over 500-row transactions.
///
/// Why this is faster than the previous Mutex-on-write approach:
///   * Inference runs in parallel (N× BGE-small throughput)
///   * Datalog writes are not serialized by a Mutex — one writer drains
///     the channel and ships large batches
///   * The 500-row `:put` keeps per-row parser overhead constant
///
/// On a 10-core host with `workers=4` and `batch_size=64` this routinely
/// hits 800–1500 vectors/sec on a 400k-row index, vs 70–100 for the
/// single-threaded `run`.
pub fn build_index_parallel(
    graph: &GraphEngine,
    _index_path: &std::path::Path,
    opts: &BuildOptions,
    workers: usize,
) -> Result<BuildReport, String> {
    use crossbeam_channel::bounded;
    use std::sync::atomic::{AtomicUsize, Ordering};
    use std::sync::Arc;

    let db = graph.db();

    // 1. Walk code_elements and build the work list (sequential, can't be
    // sped up — this is a single CozoDB scan).
    let elements = graph.all_elements().map_err(|e| e.to_string())?;
    let work: Vec<WorkItem> = elements
        .iter()
        .filter(|el| {
            if let Some(filter) = &opts.type_filter {
                filter.contains(&el.element_type.to_ascii_lowercase())
            } else {
                true
            }
        })
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
    let existing_state: std::collections::HashMap<String, EmbeddingStateRow> = state::list_all(db)
        .map_err(|e| e.to_string())?
        .into_iter()
        .map(|r| (r.qualified_name.clone(), r))
        .collect();
    let to_embed: Vec<WorkItem> = work
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
        .map(|w| w.clone())
        .collect();

    let considered = work.len();
    let skipped_fresh = considered - to_embed.len();

    // FR-HNSW perf fix: drop the HNSW index before the bulk insert so
    // each :put doesn't pay the O(log N) HNSW update cost. Recreate the
    // index after the loop completes — CozoDB's HNSW build is O(N log N)
    // and runs ~5-10x faster than N incremental updates on a 100k+ index.
    if state::drop_hnsw_index(db).is_err() {
        tracing::warn!("could not drop HNSW index before bulk insert (continuing)");
    }
    tracing::info!("HNSW dropped; running parallel bulk insert");

    // Warm the fastembed model cache once on this thread before fanning
    // out. Each worker constructs its own `TextEmbedding` (which is
    // !Sync), and without this warm-up all N workers race to download
    // the same ONNX weights into the shared cache dir — fastembed's
    // hf-hub fetch is not concurrency-safe for the same file, and the
    // loser surfaces as "Failed to retrieve onnx/model.onnx". Building
    // a throwaway Embedder here populates the cache so the parallel
    // sessions find the model already on disk.
    //
    // Bound OMP_NUM_THREADS=1 once for the entire process so the ORT
    // session's intra-op parallelism (which fastembed 4.9.1 sets to
    // `available_parallelism()` per session) doesn't multiply by N workers
    // on a 10-core host. intra_threads caps the per-session thread count;
    // OMP_NUM_THREADS=1 caps the per-kernel thread pool. Combined, we get
    // N ONNX sessions each doing single-threaded compute, which is the
    // sweet spot for BGE-small (CPU-bound, cache-friendly).
    {
        let _warmer = Embedder::new().map_err(|e| e.to_string())?;
        tracing::info!("fastembed model cache warmed for parallel workers");
    }
    if std::env::var_os("OMP_NUM_THREADS").is_none() {
        // SAFETY: process-wide env mutation is unsafe under concurrent
        // reads, but we set this before any worker threads are spawned
        // and before any ORT session reads it, so the only contention is
        // external readers (none in this process).
        std::env::set_var("OMP_NUM_THREADS", "1");
        tracing::info!("OMP_NUM_THREADS=1 (cap intra-op parallelism across N workers)");
    }

    // 3. Shard the work, run inference in N worker threads, push results
    // onto a bounded crossbeam channel. A single writer thread consumes
    // the channel and ships :put embedding_vectors in UPSERT_CHUNK batches.
    let batch_size = opts.batch_size.max(1);
    let n_workers = workers.max(1);
    let total = to_embed.len();
    // Channel buffer = N workers * 8 batches so workers can stay busy
    // even when the writer is briefly blocked.
    // Channel buffer = 4 × UPSERT_CHUNK so workers can keep producing
    // while the writer is mid-transaction. Smaller buffers (e.g. 8 ×
    // n_workers) caused workers to block on send during the ~10s writer
    // commit, which serialised inference behind the writer.
    let upsert_chunk = effective_upsert_chunk();
    let (tx, rx) = bounded::<(String, Vec<f32>, String)>(upsert_chunk * 4);
    let embedded_count = Arc::new(AtomicUsize::new(0));

    // --- Writer thread: single CozoDB writer that drains the channel and
    // emits :put embedding_vectors in UPSERT_CHUNK batches.
    let writer = {
        // SAFETY: `cozo::DbInstance` is internally `Send + !Sync`. We
        // move a clone into the writer thread so it owns the only
        // reference; the outer `db` (used later for state/orphan ops)
        // is not touched by the writer.
        let db_for_writer = db.clone();
        std::thread::spawn(move || -> Result<(Vec<FreshRow>, usize), String> {
            let mut fresh_rows: Vec<FreshRow> = Vec::with_capacity(total);
            let mut pending: Vec<(String, Vec<f32>, String)> = Vec::new();
            let mut done = 0usize;
            loop {
                match rx.recv() {
                    Ok(item) => {
                        pending.push(item);
                        if pending.len() >= upsert_chunk {
                            // Drain any stragglers non-blockingly.
                            while let Ok(more) = rx.try_recv() {
                                pending.push(more);
                                if pending.len() >= upsert_chunk * 2 {
                                    break;
                                }
                            }
                            let (rows, fresh): (Vec<(String, Vec<f32>)>, Vec<FreshRow>) =
                                pending.drain(..).fold(
                                    (Vec::new(), Vec::new()),
                                    |(mut rows, mut fresh), (qn, vec, hash)| {
                                        rows.push((qn.clone(), vec));
                                        fresh.push(FreshRow {
                                            qualified_name: qn,
                                            usearch_key: 0,
                                            content_hash: hash,
                                        });
                                        (rows, fresh)
                                    },
                                );
                            upsert_pairs_to_db(&db_for_writer, &rows).map_err(|e| e.to_string())?;
                            done += rows.len();
                            tracing::info!("writer: flushed {} rows, total {}", rows.len(), done);
                            fresh_rows.extend(fresh);
                        }
                    }
                    Err(_) => break,
                }
            }
            // Final flush.
            if !pending.is_empty() {
                let (rows, fresh): (Vec<(String, Vec<f32>)>, Vec<FreshRow>) =
                    pending.into_iter().fold(
                        (Vec::new(), Vec::new()),
                        |(mut rows, mut fresh), (qn, vec, hash)| {
                            rows.push((qn.clone(), vec));
                            fresh.push(FreshRow {
                                qualified_name: qn,
                                usearch_key: 0,
                                content_hash: hash,
                            });
                            (rows, fresh)
                        },
                    );
                if !rows.is_empty() {
                    upsert_pairs_to_db(&db_for_writer, &rows).map_err(|e| e.to_string())?;
                    done += rows.len();
                    tracing::info!("writer: final flush {} rows, total {}", rows.len(), done);
                }
                fresh_rows.extend(fresh);
            }
            Ok((fresh_rows, done))
        })
    };

    // --- Inference workers: N threads, each owns its Embedder. The
    // `work_items` arc is shared read-only.
    let work_items = std::sync::Arc::new(to_embed);
    let mut worker_handles = Vec::with_capacity(n_workers);
    for w_id in 0..n_workers {
        let tx = tx.clone();
        let work_items = work_items.clone();
        let embedded_count = embedded_count.clone();
        let handle = std::thread::spawn(move || -> Result<(), String> {
            let embedder = Embedder::new().map_err(|e| e.to_string())?;
            // Round-robin shards: this worker takes every Nth shard.
            let shards: Vec<&[WorkItem]> = work_items.chunks(batch_size * n_workers).collect();
            for shard in shards.iter().skip(w_id).step_by(n_workers) {
                for chunk in shard.chunks(batch_size) {
                    let texts: Vec<String> = chunk.iter().map(|w| w.blob.clone()).collect();
                    let vectors = embedder.embed(&texts).map_err(|e| e.to_string())?;
                    for (item, vec) in chunk.iter().zip(vectors.iter()) {
                        let qn = item.qualified_name.clone();
                        let hash = item.current_hash.clone();
                        let v = vec.clone();
                        if tx.send((qn, v, hash)).is_err() {
                            return Err("writer disconnected".to_string());
                        }
                    }
                    let total_now =
                        embedded_count.fetch_add(chunk.len(), Ordering::Relaxed) + chunk.len();
                    if total_now % 2048 < chunk.len() || total_now == work_items.len() {
                        tracing::info!(
                            "worker {}: embedded {}/{} (this chunk {})",
                            w_id,
                            total_now,
                            work_items.len(),
                            chunk.len()
                        );
                    }
                }
            }
            Ok(())
        });
        worker_handles.push(handle);
    }
    drop(tx); // writer sees disconnect when last worker drops its tx

    // Wait for inference workers.
    let mut worker_err: Option<String> = None;
    for h in worker_handles {
        match h.join() {
            Ok(Ok(())) => {}
            Ok(Err(e)) => {
                if worker_err.is_none() {
                    worker_err = Some(e);
                }
            }
            Err(_) => {
                if worker_err.is_none() {
                    worker_err = Some("worker thread panicked".to_string());
                }
            }
        }
    }

    // Wait for the writer to drain the channel.
    let (fresh_rows, _writer_done) = writer
        .join()
        .map_err(|_| "writer thread panicked".to_string())??;
    if let Some(e) = worker_err {
        return Err(e);
    }

    let embedded = embedded_count.load(Ordering::Relaxed);

    tracing::info!(
        "pipeline embed complete, calling upsert_fresh for {} rows",
        fresh_rows.len()
    );
    state::upsert_fresh(db, &fresh_rows).map_err(|e| e.to_string())?;
    tracing::info!("upsert_fresh complete");

    // Recreate the HNSW index now that the bulk insert is done. This is
    // a single O(N log N) operation and is much faster than letting every
    // :put pay the incremental update cost.
    tracing::info!("rebuilding HNSW index on embedding_vectors:vec_idx");
    let hnsw_started = std::time::Instant::now();
    state::create_hnsw_index(db).map_err(|e| e.to_string())?;
    tracing::info!(
        "HNSW rebuild complete in {:.2}s",
        hnsw_started.elapsed().as_secs_f64()
    );

    // Reap orphans.
    let work_qns: std::collections::HashSet<&str> =
        work.iter().map(|w| w.qualified_name.as_str()).collect();
    let orphan_rows: Vec<EmbeddingStateRow> = existing_state
        .iter()
        .filter(|(qn, _)| !work_qns.contains(qn.as_str()))
        .map(|(_, row)| row.clone())
        .collect();
    tracing::info!("orphan reap: {} orphans", orphan_rows.len());
    if !orphan_rows.is_empty() {
        let orphan_qns: Vec<String> = orphan_rows
            .iter()
            .map(|r| r.qualified_name.clone())
            .collect();
        remove_vectors(db, &orphan_qns).map_err(|e| e.to_string())?;
        state::delete_state_rows(db, &orphan_rows).map_err(|e| e.to_string())?;
    }

    let index_size = count_vectors(db).map_err(|e| e.to_string())?;

    Ok(BuildReport {
        considered_count: considered,
        embedded_count: embedded,
        skipped_fresh_count: skipped_fresh,
        orphaned_count: orphan_rows.len(),
        index_size,
        index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
    })
}

/// Helper: write a batch of (qualified_name, vector) pairs to CozoDB
/// using the parameterized query. Called from the single writer thread
/// in the pipeline.
fn upsert_pairs_to_db(
    db: &CozoDb,
    pairs: &[(String, Vec<f32>)],
) -> Result<(), Box<dyn std::error::Error>> {
    use std::collections::BTreeMap;
    let chunk_size = effective_upsert_chunk();
    for chunk in pairs.chunks(chunk_size) {
        let mut params = BTreeMap::new();
        let rows: Vec<serde_json::Value> = chunk
            .iter()
            .map(|(qn, vec)| {
                let vec_json: Vec<serde_json::Value> = vec
                    .iter()
                    .map(|f| {
                        serde_json::Number::from_f64(*f as f64)
                            .map(serde_json::Value::Number)
                            .unwrap_or(serde_json::Value::Null)
                    })
                    .collect();
                serde_json::Value::Array(vec![
                    serde_json::Value::String(qn.clone()),
                    serde_json::Value::Array(vec_json),
                ])
            })
            .collect();
        params.insert("rows".to_string(), serde_json::Value::Array(rows));
        let query = r#"?[qualified_name, vector] <- $rows
           :put embedding_vectors {qualified_name => vector}"#;
        run_script(db, query, params)?;
    }
    Ok(())
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
    let chunk_size = effective_upsert_chunk();
    for chunk in rows.chunks(chunk_size) {
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
    let chunk_size = effective_upsert_chunk();
    for chunk in qns.chunks(chunk_size) {
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

/// Configuration for the in-process background embed used by mcp-http
/// (`LEANKG_EMBED_BACKGROUND=1`). The defaults target the Plan §"Part A"
/// SLA: <5 min cold functions-only embed on a 10-core host while keeping
/// MCP request latency untouched.
#[derive(Debug, Clone)]
pub struct BackgroundEmbedConfig {
    /// Override the embedding batch size (default 64).
    pub batch_size: usize,
    /// Number of parallel ONNX workers (default 2 — lower than the CLI
    /// foreground default so request threads have headroom).
    pub workers: usize,
    /// Force a `--full` re-embed even if the state table has fresh rows.
    pub full: bool,
    /// Override the types filter; empty = "use the mega-graph heuristic".
    pub types_filter: String,
}

impl Default for BackgroundEmbedConfig {
    fn default() -> Self {
        Self {
            batch_size: 64,
            workers: 2,
            full: false,
            types_filter: String::new(),
        }
    }
}

/// Handle returned by `spawn_background_embed`. Dropping the handle is a
/// no-op (the worker thread is detached) — pass through to keep the
/// return type useful for future cancellation hooks.
#[derive(Debug)]
pub struct BackgroundEmbedHandle {
    pub pid: u32,
}

/// Spawn a detached background embed that runs inside the calling
/// process, sharing the caller's `CozoDb` handle via `GraphEngine`'s
/// `Arc<CozoDb>`. This avoids the RocksDB single-writer rejection that a
/// second `leankg embed` child would hit if launched while MCP is live.
///
/// The worker writes `<leankg_dir>/embed_status.json` with progress and a
/// `<leankg_dir>/embed.lock` file containing its PID, so callers can
/// poll via `leankg embed --status` or `kill -TERM <pid>` to cancel.
///
/// Returns `Ok(None)` if a background embed is already in flight (lock
/// file present + alive) so the caller can treat the no-op as idempotent.
pub fn spawn_background_embed(
    graph: GraphEngine,
    leankg_dir: std::path::PathBuf,
    cfg: BackgroundEmbedConfig,
) -> Result<Option<BackgroundEmbedHandle>, String> {
    use std::io::IsTerminal;

    let lock_path = leankg_dir.join("embed.lock");
    let status_path = leankg_dir.join("embed_status.json");

    // Refuse to start a second one if a previous run is alive.
    if let Ok(raw) = std::fs::read_to_string(&lock_path) {
        if let Ok(pid) = raw.trim().parse::<u64>() {
            let probe = unsafe { libc_kill_compat(pid, 0) };
            if probe == 0 {
                tracing::info!(
                    "background embed already running (PID {}); skipping new spawn",
                    pid
                );
                return Ok(None);
            }
        }
        let _ = std::fs::remove_file(&lock_path);
    }

    // Write the lock first; the worker thread will refresh the status
    // file periodically. If the worker panics before writing, the lock
    // gives us a PID to investigate.
    let pid = std::process::id();
    let _ = std::fs::write(&lock_path, pid.to_string());

    let started_at = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0);

    let write_status =
        move |considered: u64, embedded: u64, skipped: u64, orphans: u64, status: &str| {
            let body = serde_json::json!({
                "pid": pid,
                "started_at": started_at,
                "considered": considered,
                "embedded": embedded,
                "skipped_fresh": skipped,
                "orphans": orphans,
                "workers": cfg.workers,
                "status": status,
                "mode": "in_process_background",
            });
            if let Ok(mut f) = std::fs::File::create(&status_path) {
                let _ = f.write_all(body.to_string().as_bytes());
            }
        };

    // Snapshot the initial element count ONCE (don't double-scan inside
    // build_index_parallel). On the mega-graph heuristic, this is also
    // where the function,method filter is applied.
    let total = graph.all_elements().map(|v| v.len()).unwrap_or(0);
    write_status(total as u64, 0, 0, 0, "running");

    let graph_clone = graph.clone();
    let leankg_dir_for_worker = leankg_dir.clone();

    // Detached worker thread. We use std::thread (not tokio) because
    // build_index_parallel is fully synchronous and CPU-bound; tokio
    // would just add scheduling overhead. Live progress is logged via
    // tracing!info! inside build_index_parallel and surfaces in the
    // container's stdout / docker logs.
    std::thread::Builder::new()
        .name("leankg-bg-embed".into())
        .spawn(move || {
            let mode = if cfg.full {
                BuildMode::Full
            } else {
                BuildMode::Incremental
            };
            let parsed = parse_type_filter(&cfg.types_filter);
            let opts = BuildOptions {
                mode,
                batch_size: cfg.batch_size,
                reserve_capacity: None,
                type_filter: match &parsed {
                    Some(_) => parsed.clone(),
                    None => {
                        if total > 50_000 {
                            let mut set = std::collections::HashSet::new();
                            set.insert("function".to_string());
                            set.insert("method".to_string());
                            Some(set)
                        } else {
                            None
                        }
                    }
                },
            };

            // Periodic status snapshot poller. Reads the live row count from the
            // shared `CozoDb` handle (Arc-clone is safe — RocksDB allows
            // concurrent readers in the same process) and writes a JSON
            // snapshot every 5s so `leankg embed --status` shows live
            // numbers while the embed is running.
            use std::sync::atomic::{AtomicBool, Ordering};
            use std::sync::Arc;
            let poller_status = leankg_dir_for_worker.join("embed_status.json");
            let poller_pid = pid;
            let poller_started = started_at;
            let poller_total = total as u64;
            let poller_workers = cfg.workers;
            let poller_graph = graph_clone.clone();
            let poller_done = Arc::new(AtomicBool::new(false));
            let poller_done_clone = poller_done.clone();
            std::thread::Builder::new()
                .name("leankg-bg-embed-poller".into())
                .spawn(move || {
                    while !poller_done_clone.load(Ordering::Relaxed) {
                        std::thread::sleep(std::time::Duration::from_secs(5));
                        let embedded = poller_graph
                            .db()
                            .run_script(
                                "?[qualified_name] := *embedding_vectors{qualified_name}",
                                std::collections::BTreeMap::new(),
                                cozo::ScriptMutability::Immutable,
                            )
                            .map(|r| r.rows.len() as u64)
                            .unwrap_or(0);
                        let body = serde_json::json!({
                            "pid": poller_pid,
                            "started_at": poller_started,
                            "considered": poller_total,
                            "embedded": embedded,
                            "skipped_fresh": 0u64,
                            "orphans": 0u64,
                            "workers": poller_workers,
                            "status": "running",
                            "mode": "in_process_background",
                        });
                        if let Ok(mut f) = std::fs::File::create(&poller_status) {
                            let _ = f.write_all(body.to_string().as_bytes());
                        }
                    }
                })
                .ok();

            let started = std::time::Instant::now();
            let result = if cfg.workers > 1 {
                build_index_parallel(
                    &graph_clone,
                    std::path::Path::new(""),
                    &opts,
                    cfg.workers,
                )
            } else {
                build_index(&graph_clone, std::path::Path::new(""), &opts)
                    .map_err(|e| e.to_string())
            };
            let elapsed = started.elapsed();
            poller_done.store(true, Ordering::Relaxed);

            match result {
                Ok(report) => {
                    // Write final status.
                    let final_status = leankg_dir_for_worker.join("embed_status.json");
                    let body = serde_json::json!({
                        "pid": pid,
                        "started_at": started_at,
                        "considered": report.considered_count,
                        "embedded": report.embedded_count,
                        "skipped_fresh": report.skipped_fresh_count,
                        "orphans": report.orphaned_count,
                        "workers": cfg.workers,
                        "elapsed_s": elapsed.as_secs_f64(),
                        "status": "completed",
                        "mode": "in_process_background",
                    });
                    if let Ok(mut f) = std::fs::File::create(&final_status) {
                        let _ = f.write_all(body.to_string().as_bytes());
                    }
                    if std::io::stdout().is_terminal() {
                        eprintln!(
                            "[bg-embed] completed in {:.2}s: {} considered, {} embedded, {} skipped, {} orphans",
                            elapsed.as_secs_f64(),
                            report.considered_count,
                            report.embedded_count,
                            report.skipped_fresh_count,
                            report.orphaned_count
                        );
                    } else {
                        tracing::info!(
                            "background embed completed in {:.2}s: considered={}, embedded={}, skipped={}, orphans={}",
                            elapsed.as_secs_f64(),
                            report.considered_count,
                            report.embedded_count,
                            report.skipped_fresh_count,
                            report.orphaned_count
                        );
                    }
                }
                Err(e) => {
                    let err_status = leankg_dir_for_worker.join("embed_status.json");
                    let body = serde_json::json!({
                        "pid": pid,
                        "started_at": started_at,
                        "status": "failed",
                        "error": e,
                        "mode": "in_process_background",
                    });
                    if let Ok(mut f) = std::fs::File::create(&err_status) {
                        let _ = f.write_all(body.to_string().as_bytes());
                    }
                    tracing::error!("background embed failed: {}", e);
                }
            }

            // Clear the lock so a future spawn can run.
            let lock_path = leankg_dir_for_worker.join("embed.lock");
            let _ = std::fs::remove_file(&lock_path);
        })
        .map_err(|e| format!("failed to spawn background embed thread: {}", e))?;

    Ok(Some(BackgroundEmbedHandle { pid }))
}

// Minimal libc binding — same shape as main.rs::libc_kill to avoid
// pulling in the `libc` crate just for one symbol.
unsafe fn libc_kill_compat(pid: u64, sig: i32) -> i32 {
    extern "C" {
        fn kill(pid: i32, sig: i32) -> i32;
    }
    kill(pid as i32, sig)
}

#[derive(Clone)]
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
    fn default_options_batch_size_64() {
        assert_eq!(BuildOptions::default().batch_size, 64);
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
    fn default_upsert_chunk_is_5000() {
        // Documented contract — overridable via LEANKG_EMBED_UPSERT_CHUNK.
        assert_eq!(DEFAULT_UPSERT_CHUNK, 5000);
    }

    #[test]
    fn effective_upsert_chunk_defaults_when_env_unset() {
        std::env::remove_var("LEANKG_EMBED_UPSERT_CHUNK");
        assert_eq!(effective_upsert_chunk(), 5000);
    }
}
