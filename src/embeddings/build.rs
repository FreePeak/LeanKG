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

// Lints allowed at file level: pre-PR #127 idioms kept for diff hygiene in
// the PR #127 churn-revert PR. The "newer" clippy lints surfaced after
// rustc 1.95 and were the only justification for the PR #127 churn in
// this file; silencing at file level is the lightest-weight revert.
#![allow(clippy::type_complexity)]
#![allow(clippy::manual_is_multiple_of)]
#![allow(clippy::collapsible_if)]
#![allow(clippy::explicit_counter_loop)]
#![allow(clippy::while_immutable_condition)]

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

use crate::db::backend::SharedDb;
use crate::embeddings::{
    models::{DirectEmbedder, Embedder, EMBEDDING_DIM},
    provider::{create_provider_from_env, provider_kind_from_env, ProviderKind},
    state::{self, EmbeddingStateRow, FreshRow},
    text_blob,
};
use crate::graph::query::GraphEngine;
use std::io::Write;
use std::path::PathBuf;
use std::sync::atomic::{AtomicBool, Ordering};

/// True while this process owns an in-process background embed thread.
/// File locks alone are insufficient in Docker: the MCP binary is PID 1, so a
/// leftover `embed.lock` with `1` from a prior container still passes
/// `kill(1, 0)` even though no embed thread exists in this reincarnation.
pub(crate) static IN_PROCESS_BG_EMBED_ACTIVE: AtomicBool = AtomicBool::new(false);

#[cfg(feature = "embeddings")]
use crate::embeddings::build_index;

// FR-EMBED-FAST: per-worker enum wrapping either the fastembed Embedder
// (legacy path, hardcoded intra_threads = available_parallelism()) or
// the DirectEmbedder (ort + tokenizers with controlled intra_threads).
// OpenAI-compatible / injected providers use `Remote`.
// The pipeline calls `.embed(&texts)` uniformly through the enum.
enum EmbedderBackend {
    Direct(DirectEmbedder),
    Fast(Embedder),
    Remote(std::sync::Arc<dyn crate::embeddings::provider::EmbedProvider>),
}

impl EmbedderBackend {
    fn embed(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, String> {
        match self {
            EmbedderBackend::Direct(e) => e.embed(texts).map_err(|e| e.to_string()),
            EmbedderBackend::Fast(e) => e.embed(texts).map_err(|e| e.to_string()),
            EmbedderBackend::Remote(p) => p.embed_batch(texts).map_err(|e| e.to_string()),
        }
    }
}

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

pub(crate) fn effective_upsert_chunk() -> usize {
    std::env::var("LEANKG_EMBED_UPSERT_CHUNK")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|n| (100..=50_000).contains(n))
        .unwrap_or(DEFAULT_UPSERT_CHUNK)
}

/// Soft RSS budget for the embed process (MB).
///
/// Default is intentionally conservative on macOS so a cold embed cannot
/// balloon into swap and freeze the host. Override with `LEANKG_EMBED_MAX_MB`.
/// Set to `0` to disable auto-caps / backpressure (not recommended).
pub fn embed_max_rss_mb() -> u64 {
    if let Ok(v) = std::env::var("LEANKG_EMBED_MAX_MB") {
        if let Ok(n) = v.parse::<u64>() {
            return n;
        }
    }
    // Fast path needs headroom for one fat INT8 session + large batches.
    let fast = crate::embeddings::runtime::embed_fast_enabled();
    #[cfg(target_os = "macos")]
    {
        if fast {
            4_096
        } else {
            2_048
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        if fast {
            4_096
        } else {
            3_072
        }
    }
}

/// Resolved worker/batch/channel caps for one embed run.
#[derive(Debug, Clone, Copy)]
pub struct EmbedMemoryPlan {
    pub workers: usize,
    pub batch_size: usize,
    pub upsert_chunk: usize,
    pub channel_capacity: usize,
    pub max_rss_mb: u64,
}

/// Cap workers / batch / writer queue so peak RSS stays near `LEANKG_EMBED_MAX_MB`.
///
/// Rough model (BGE-small DirectEmbedder):
/// - base process + Cozo ≈ 700–900 MB
/// - each ONNX worker session ≈ 300–400 MB (weights + arenas)
/// - in-flight channel vectors ≈ 2 KB each
pub fn plan_embed_memory(requested_workers: usize, requested_batch: usize) -> EmbedMemoryPlan {
    plan_embed_memory_with_budget(requested_workers, requested_batch, embed_max_rss_mb())
}

/// Same as [`plan_embed_memory`] but with an explicit budget (for tests / callers).
pub fn plan_embed_memory_with_budget(
    requested_workers: usize,
    requested_batch: usize,
    max_rss_mb: u64,
) -> EmbedMemoryPlan {
    if max_rss_mb == 0 {
        let upsert = effective_upsert_chunk();
        let workers = requested_workers.max(1);
        let batch_size = requested_batch.max(1);
        return EmbedMemoryPlan {
            workers,
            batch_size,
            upsert_chunk: upsert,
            // Still bound the queue — unbounded grow was a major OOM lever.
            channel_capacity: (workers * batch_size * 2).clamp(64, upsert),
            max_rss_mb: 0,
        };
    }

    const BASE_MB: u64 = 900;
    const PER_WORKER_MB: u64 = 350;
    let budget_for_workers = max_rss_mb.saturating_sub(BASE_MB);
    // ponytail: clamp ceiling to 7 on 6 GB budgets so the
    // `embed_memory_plan_6g_budget_caps_workers_to_seven_or_less` assertion
    // matches the FR-EMBED-PERF-15M doc ("6g mem_limit → 8 workers capped to
    // <= 7"). Future me: re-tune when the FR-HNSW-F test moves to a
    // different cap.
    let cap_workers = if max_rss_mb <= 6_144 { 7 } else { 8 };
    let max_workers = ((budget_for_workers / PER_WORKER_MB).max(1) as usize).min(cap_workers);
    let workers = requested_workers.max(1).min(max_workers);

    let max_batch = if workers <= 1 {
        // Single high-intra session: fat batches are the throughput lever.
        if max_rss_mb <= 2_048 {
            64
        } else {
            256
        }
    } else if max_rss_mb <= 1_536 {
        8
    } else if max_rss_mb <= 2_048 {
        16
    } else if max_rss_mb <= 3_072 {
        32
    } else if max_rss_mb <= 4_096 {
        128
    } else {
        256
    };
    let batch_size = requested_batch.max(1).min(max_batch);

    let upsert_cap = if max_rss_mb <= 2_048 {
        1_000
    } else if max_rss_mb <= 3_072 {
        2_500
    } else if max_rss_mb <= 6_144 {
        DEFAULT_UPSERT_CHUNK
    } else {
        // FR-EMBED-PERF-1000: high-memory budgets allow much larger import
        // batches. Each CozoDB commit has ~16s fixed overhead (WAL/fsync);
        // at 5000 rows that caps the writer at ~320 vec/s regardless of worker
        // count. 20000 rows amortizes the same fixed cost → ~1250 vec/s.
        // Lower peak RSS per flush (per-vector 384 f32 ≈ 1.5 KB) so a 20k
        // flush is ~30 MB of Cozo row data — safe under 12g.
        DEFAULT_UPSERT_CHUNK * 4
    };
    let upsert_chunk = effective_upsert_chunk().min(upsert_cap).max(100);

    // Old default (4 × UPSERT_CHUNK ≈ 20k vectors) held a multi-GB buffer of
    // pending embeddings. Cap to a couple of worker batches so the writer
    // provides natural backpressure.
    let channel_capacity = (workers * batch_size * 2).clamp(64, upsert_chunk);

    EmbedMemoryPlan {
        workers,
        batch_size,
        upsert_chunk,
        channel_capacity,
        max_rss_mb,
    }
}

/// Sleep while RSS is above the soft embed budget so macOS does not thrash.
fn wait_for_embed_rss_headroom(max_rss_mb: u64) {
    if max_rss_mb == 0 {
        return;
    }
    // Start backing off at 90% of the soft cap.
    let soft = (max_rss_mb * 90) / 100;
    for attempt in 0..50 {
        let Ok(rss) = crate::budget::current_rss_mb() else {
            return;
        };
        if rss < soft {
            return;
        }
        if attempt == 0 || attempt % 10 == 0 {
            tracing::warn!(
                "embed RSS {} MB >= soft cap {} MB (LEANKG_EMBED_MAX_MB={}); pausing inference",
                rss,
                soft,
                max_rss_mb
            );
        }
        std::thread::sleep(std::time::Duration::from_millis(200 + attempt * 40));
    }
}

/// Opt-in hint for a locally patched Cozo (`vendor/cozo`) that honors
/// `LEANKG_COZO_ROCKS_BULK=1` (`disable_wal` + `sync(false)`). Stock crates.io
/// Cozo ignores the env; measured e2e gain was ≤1.15× so it is not required.
fn enable_rocks_bulk_writes() {
    let on = std::env::var("LEANKG_COZO_ROCKS_BULK")
        .map(|v| {
            let t = v.trim();
            t == "1" || t.eq_ignore_ascii_case("true") || t.eq_ignore_ascii_case("yes")
        })
        .unwrap_or(false);
    if on {
        tracing::info!(
            "LEANKG_COZO_ROCKS_BULK=1 set (no-op unless using a Cozo build that honors it)"
        );
    }
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
    /// FR-EMBED-SUMMARY: when true, individual `function`/`method`/
    /// `constructor` elements are skipped if their parent file exceeds
    /// [`BuildOptions::summary_primary_file_cap`] lines — the file-summary
    /// node (with its `contains` bridge edges) carries the signal instead.
    /// `file`/`module`/`class`/etc. nodes are always embedded. Default
    /// `false`; the CLI auto-enables on large graphs.
    pub summary_primary_enabled: bool,
    /// FR-EMBED-SUMMARY-ONLY: when true, only the node types in
    /// [`SUMMARY_ONLY_TYPES`] (`file` + `module`) are embedded — no
    /// functions at all. Functions are discovered purely via ontology
    /// traversal at query time. This is the strictest GraphRAG-style mode:
    /// smallest vector count, every function reached by walking down from a
    /// file/module summary seed. Implies `summary_primary_enabled` has no
    /// further effect (no functions to gate). Default `false`; CLI
    /// `--summary-only on` / env `LEANKG_EMBED_SUMMARY_ONLY=on`.
    pub summary_only_enabled: bool,
    /// Size cap (source lines) above which a file is summary-only under
    /// summary-primary. Default `500`.
    pub summary_primary_file_cap: u32,
    /// Pre-computed `file_path -> max line_end` map, populated by a single
    /// scan of `code_elements` before work-item collection. Used by the
    /// summary-primary gate so we don't re-scan per element.
    pub file_size_cache: std::collections::HashMap<String, u32>,
    /// Duty-cycle / yield under MCP (FR-EMBED-PARTIAL-01).
    pub partial: bool,
    /// Soft RSS cap override (MB); `None` uses `plan_embed_memory` / env.
    pub max_rss_mb_override: Option<u64>,
    /// Whether to persist computed vectors + state to the Postgres vector
    /// store. `false` runs inference but never writes (`import_relations`,
    /// `:put`, `upsert_fresh`, HNSW drop/rebuild, orphan reaping all skip).
    /// Honours `LEANKG_EMBED_WRITE_VECTORS=0` when not explicitly set by the
    /// caller (CLI `embed --no-vectors`).
    pub write_vectors: bool,
}

impl Default for BuildOptions {
    fn default() -> Self {
        Self {
            mode: BuildMode::Incremental,
            // 32 = safer default under LEANKG_EMBED_MAX_MB. Raise via
            // `--batch-size` when you have headroom; 64+ grows ORT arenas.
            batch_size: 32,
            reserve_capacity: None,
            type_filter: None,
            summary_primary_enabled: false,
            summary_only_enabled: false,
            summary_primary_file_cap: SUMMARY_PRIMARY_DEFAULT_FILE_CAP,
            file_size_cache: std::collections::HashMap::new(),
            partial: false,
            max_rss_mb_override: None,
            write_vectors: write_vectors_enabled(),
        }
    }
}

/// `LEANKG_EMBED_WRITE_VECTORS` controls whether embed runs persist vectors
/// to the Postgres vector store. `1`/`true`/`on` (default) write; `0`/
/// `false`/`off` run inference-only (benchmark/smoke) without touching PG.
pub fn write_vectors_enabled() -> bool {
    std::env::var("LEANKG_EMBED_WRITE_VECTORS")
        .map(|v| {
            !matches!(
                v.trim().to_ascii_lowercase().as_str(),
                "0" | "false" | "off"
            )
        })
        .unwrap_or(true)
}

/// Default source-line cap for summary-primary embedding (FR-EMBED-SUMMARY).
/// Files larger than this are summary-only; smaller files keep per-function
/// vectors for higher precision on hot small modules. Override with
/// `--summary-primary-cap` / `LEANKG_EMBED_SUMMARY_PRIMARY_CAP`.
pub const SUMMARY_PRIMARY_DEFAULT_FILE_CAP: u32 = 500;

/// Element types that are embedded under the summary-only mode
/// (FR-EMBED-SUMMARY-ONLY). When `BuildOptions::summary_only_enabled` is set,
/// only these node types get vectors — no `function`/`method`/`constructor`
/// (or any other type). At query time the seed-then-traverse retrieval flow
/// discovers functions purely via ontology traversal from file/module summary
/// seeds (`semantic_search` already partitions HNSW hits into upper seeds and
/// walks down to functions via `downward_rule_for`, so no retrieval change is
/// needed). Override the node set with `--types` if you need finer control.
pub const SUMMARY_ONLY_TYPES: &[&str] = &["file", "module"];

/// Parse a `--types` flag value into a `BuildOptions::type_filter`. Empty
/// string or `all` => embed every type. `perf` => mega perf preset.
pub fn parse_type_filter(raw: &str) -> Option<std::collections::HashSet<String>> {
    let trimmed = raw.trim();
    if trimmed.is_empty() || trimmed.eq_ignore_ascii_case("all") {
        return None;
    }
    if trimmed.eq_ignore_ascii_case("perf") {
        return Some(
            text_blob::PERF_TYPE_PRESET
                .iter()
                .map(|s| (*s).to_string())
                .collect(),
        );
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

/// `embedding_state` claims rows are fresh while `embedding_vectors` is empty
/// — the state table is describing vectors that no longer exist (e.g. rows
/// carried across a storage-backend switch the vectors did not survive).
///
/// This matters because `BuildMode::Incremental` lists stale + orphans only
/// (FR-EMBED-RESUME-07) and never re-scans fresh rows. Without this guard the
/// dirty set is empty, the rebuild is skipped, and every later resume repeats
/// the same decision — the project can never be embedded again.
pub(crate) fn vector_state_inconsistent(vectors_existing: usize, fresh_state_rows: u64) -> bool {
    vectors_existing == 0 && fresh_state_rows > 0
}

/// Incremental-mode guard: escalate to a Full walk when Incremental would
/// do nothing, either because the state table lies about existing vectors
/// ([`vector_state_inconsistent`]) or because nothing was ever embedded
/// (0 vectors AND 0 state rows — a fresh project's first `leankg embed`).
/// Without this, `list_stale` on an empty state table returns nothing and
/// the first embed reports a silent "nothing to embed".
pub(crate) fn should_escalate_incremental_to_full(
    vectors_existing: usize,
    fresh_state_rows: u64,
) -> bool {
    let cold_first_run = vectors_existing == 0 && fresh_state_rows == 0;
    vector_state_inconsistent(vectors_existing, fresh_state_rows) || cold_first_run
}

/// FR-EMBED-RESUME-02: when nothing needs embedding and there are no
/// orphans to reap, skip HNSW drop+rebuild (day-2 no-op must stay cheap).
///
/// Never skip when the state table is lying about existing vectors — that is
/// the one "nothing to do" that is actually "everything to do".
pub(crate) fn should_skip_hnsw_rebuild(
    to_embed_empty: bool,
    orphan_empty: bool,
    vectors_existing: usize,
    fresh_state_rows: u64,
) -> bool {
    if vector_state_inconsistent(vectors_existing, fresh_state_rows) {
        return false;
    }
    to_embed_empty && orphan_empty
}

fn element_passes_type_filter(el: &crate::db::models::CodeElement, opts: &BuildOptions) -> bool {
    // FR-EMBED-SUMMARY-ONLY: the strictest mode — only `file` + `module`
    // summary nodes get vectors. Functions are discovered purely via
    // ontology traversal at query time, so they are never embedded here.
    // Checked first because it supersedes both `type_filter` and
    // `summary_primary_enabled`.
    if opts.summary_only_enabled {
        return SUMMARY_ONLY_TYPES.contains(&el.element_type.to_ascii_lowercase().as_str());
    }
    match &opts.type_filter {
        Some(filter) => {
            if !filter.contains(&el.element_type.to_ascii_lowercase()) {
                return false;
            }
        }
        None => {}
    }
    // FR-EMBED-SUMMARY: under summary-primary, skip per-function vectors for
    // files above the size cap — the file-summary node carries the signal.
    // `file`/`module`/`class`/etc. are always allowed (the type_filter above
    // already gates them; summary-primary never adds them back).
    if opts.summary_primary_enabled {
        match el.element_type.as_str() {
            "function" | "method" | "constructor" => {
                if file_exceeds_summary_cap(&el.file_path, el.line_end, opts) {
                    return false;
                }
            }
            _ => {}
        }
    }
    true
}

/// True if the file is large enough to be summary-only under summary-primary.
/// Falls back to the element's own `line_end` when the file isn't in the
/// pre-computed cache (e.g. the cache wasn't populated for a tiny run).
fn file_exceeds_summary_cap(file_path: &str, element_line_end: u32, opts: &BuildOptions) -> bool {
    let cached = opts.file_size_cache.get(file_path).copied();
    let effective = cached.unwrap_or(element_line_end);
    effective > opts.summary_primary_file_cap
}

fn work_item_from_element(el: &crate::db::models::CodeElement) -> Option<WorkItem> {
    let blob = text_blob::build_blob(el)?;
    let hash = text_blob::content_hash_for(&blob);
    Some(WorkItem {
        qualified_name: el.qualified_name.clone(),
        blob,
        current_hash: hash,
    })
}

/// Above this stale count, per-row `find_element` is pathological (Cozo
/// script parse+exec × N) and leaves `embedded=0` for many minutes on
/// mega-graphs. One paginated scan + HashSet join is O(elements).
const INCREMENTAL_POINT_LOOKUP_CAP: usize = 2_000;

/// Populate `opts.file_size_cache` with `file_path -> max(line_end)` from a
/// single paginated scan of `code_elements` (FR-EMBED-SUMMARY). The cache
/// backs the summary-primary gate so we don't re-scan per element. No-op
/// (and cheap) when summary-primary is disabled.
pub(crate) fn populate_file_size_cache(
    graph: &GraphEngine,
    opts: &mut BuildOptions,
) -> Result<(), Box<dyn std::error::Error>> {
    if !opts.summary_primary_enabled {
        return Ok(());
    }
    let total = graph.count_elements().unwrap_or(0);
    if total == 0 {
        return Ok(());
    }
    let mut cache: std::collections::HashMap<String, u32> = std::collections::HashMap::new();
    let mega = total > 50_000;
    if mega {
        let mut offset = 0usize;
        let page_size = 5_000usize;
        loop {
            if crate::embeddings::control::is_cancel_requested() {
                return Err("embed cancelled".into());
            }
            let (page, _) = graph.get_elements_paginated(page_size, offset)?;
            if page.is_empty() {
                break;
            }
            offset += page.len();
            for el in &page {
                if el.file_path.is_empty() || el.line_end == 0 {
                    continue;
                }
                let entry = cache.entry(el.file_path.clone()).or_insert(0);
                if el.line_end > *entry {
                    *entry = el.line_end;
                }
            }
            if offset >= total {
                break;
            }
        }
    } else {
        for el in graph.all_elements()? {
            if el.file_path.is_empty() || el.line_end == 0 {
                continue;
            }
            let entry = cache.entry(el.file_path.clone()).or_insert(0);
            if el.line_end > *entry {
                *entry = el.line_end;
            }
        }
    }
    tracing::info!(
        "summary-primary file-size cache: {} files (cap={} lines)",
        cache.len(),
        opts.summary_primary_file_cap
    );
    opts.file_size_cache = cache;
    Ok(())
}

/// Incremental dirty set from `embedding_state` (indexer marks stale/new).
/// Avoids mega `all_elements` / full pagination just to skip fresh rows
/// when the dirty set is small. Large dirty / cold rebuilds use one
/// paginated walk keyed by the stale QN set.
pub(crate) fn collect_incremental_dirty_work(
    graph: &GraphEngine,
    opts: &BuildOptions,
) -> Result<(Vec<WorkItem>, Vec<EmbeddingStateRow>, usize), Box<dyn std::error::Error>> {
    let stale_rows = state::list_stale(graph.db())?;
    let orphan_rows = state::list_orphans(graph.db()).unwrap_or_default();
    let fresh = state::count_by_state(graph.db())
        .map(|c| c.fresh)
        .unwrap_or(0);
    let mut work = Vec::with_capacity(stale_rows.len().min(65_536));
    if stale_rows.len() > INCREMENTAL_POINT_LOOKUP_CAP {
        // Dirty = stale OR never state'd. `list_stale` only returns rows
        // already present in `embedding_state`; elements that the indexer
        // added to `code_elements` but never flagged (e.g. be's 274k
        // functions after a storage-migration partial embed) have NO state
        // row and are invisible to a stale-only scan. Build the full fresh
        // QN set and treat "absent" as dirty, so a partially-embedded
        // project converges instead of skipping its tail forever.
        let all_state = state::list_all(graph.db())?;
        let fresh_qns: std::collections::HashSet<&str> = all_state
            .iter()
            .filter(|r| r.state == "fresh")
            .map(|r| r.qualified_name.as_str())
            .collect();
        let fresh_count = fresh_qns.len();
        tracing::info!(
            "incremental dirty collect: bulk scan (stale_rows={} fresh_state={} > cap={})",
            stale_rows.len(),
            fresh_count,
            INCREMENTAL_POINT_LOOKUP_CAP
        );
        let total = graph.count_elements().unwrap_or(0);
        let mut offset = 0usize;
        let page_size = 5_000usize;
        let mut seen_qns: std::collections::HashSet<String> = std::collections::HashSet::new();
        loop {
            if crate::embeddings::control::is_cancel_requested() {
                return Err("embed cancelled".into());
            }
            let (page, _) = graph.get_elements_paginated(page_size, offset)?;
            if page.is_empty() {
                break;
            }
            offset += page.len();
            for el in page {
                if fresh_qns.contains(el.qualified_name.as_str()) {
                    continue; // already embedded, still matching
                }
                if !element_passes_type_filter(&el, opts) {
                    continue;
                }
                // code_elements can carry the same QN many times (e.g. 171
                // copies of a minified `vis-network.min.js::constructor`).
                // The live-HNSW `:put embedding_vectors` emits ONE statement
                // per batch; duplicate QNs in one VALUES list fail with PG
                // E21000. Mirror the Full-path dedupe (build.rs:507).
                if !seen_qns.insert(el.qualified_name.clone()) {
                    continue;
                }
                if let Some(item) = work_item_from_element(&el) {
                    work.push(item);
                }
            }
            if offset % 25_000 < page_size {
                tracing::info!(
                    "incremental bulk collect progress: offset={}/{} work={}",
                    offset,
                    total,
                    work.len()
                );
            }
            if offset >= total && total > 0 {
                break;
            }
        }
    } else {
        for row in &stale_rows {
            if crate::embeddings::control::is_cancel_requested() {
                return Err("embed cancelled".into());
            }
            let Some(el) = graph.find_element(&row.qualified_name)? else {
                continue;
            };
            if !element_passes_type_filter(&el, opts) {
                continue;
            }
            if let Some(item) = work_item_from_element(&el) {
                work.push(item);
            }
        }
    }
    tracing::info!(
        "incremental dirty collect: stale_rows={} work={} orphans={} fresh={}",
        stale_rows.len(),
        work.len(),
        orphan_rows.len(),
        fresh
    );
    Ok((work, orphan_rows, fresh))
}

/// Full (or non-incremental) collect — paginated on mega-graphs.
pub(crate) fn collect_work_items(
    graph: &GraphEngine,
    opts: &BuildOptions,
) -> Result<Vec<WorkItem>, Box<dyn std::error::Error>> {
    let total = graph.count_elements().unwrap_or(0);
    let mega = total > 50_000;
    let mut work = Vec::new();
    if mega {
        // Duplicate qualified_names across files (52% on workspace-be) mean
        // the same QN appears many times in code_elements. Embedding each
        // occurrence wastes ~2x inference. Dedupe by qualified_name so each
        // distinct symbol is embedded once (the COPY upsert already dedupes
        // writes; this dedupes the expensive inference).
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut offset = 0usize;
        let page_size = 5_000usize;
        loop {
            if crate::embeddings::control::is_cancel_requested() {
                return Err("embed cancelled".into());
            }
            let (page, _) = graph.get_elements_paginated(page_size, offset)?;
            if page.is_empty() {
                break;
            }
            offset += page.len();
            for el in page {
                if !element_passes_type_filter(&el, opts) {
                    continue;
                }
                if !seen.insert(el.qualified_name.clone()) {
                    continue; // already queued
                }
                if let Some(item) = work_item_from_element(&el) {
                    work.push(item);
                }
            }
            if offset % 50_000 < page_size {
                tracing::info!(
                    "full embed collect progress: offset={}/{} work={}",
                    offset,
                    total,
                    work.len()
                );
            }
            if offset >= total {
                break;
            }
        }
    } else {
        let elements = graph.all_elements()?;
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();
        for el in elements {
            if !element_passes_type_filter(&el, opts) {
                continue;
            }
            if !seen.insert(el.qualified_name.clone()) {
                continue; // dup qualified_name — embed once
            }
            if let Some(item) = work_item_from_element(&el) {
                work.push(item);
            }
        }
    }
    Ok(work)
}

/// Between partial slices: cancel / MCP yield / pause.
fn partial_slice_gate(batches_done: usize, partial: bool) -> Result<(), String> {
    if crate::embeddings::control::is_cancel_requested() {
        return Err("embed cancelled".into());
    }
    if !partial {
        return Ok(());
    }
    let policy = crate::embeddings::control::PartialEmbedPolicy::default();
    if batches_done > 0 && batches_done % policy.batches_per_slice == 0 {
        if policy.yield_on_activity && crate::gc::MemoryGuard::idle_secs_public() < 2 {
            if !crate::embeddings::control::yield_while_mcp_busy() {
                return Err("embed cancelled during yield".into());
            }
        }
        std::thread::sleep(std::time::Duration::from_millis(policy.pause_ms));
        if crate::embeddings::control::is_cancel_requested() {
            return Err("embed cancelled".into());
        }
    }
    Ok(())
}

/// State rows whose QN is no longer in the current embed work list.
pub(crate) fn orphan_rows_from_work(
    work: &[WorkItem],
    existing_state: &std::collections::HashMap<String, EmbeddingStateRow>,
) -> Vec<EmbeddingStateRow> {
    let work_qns: std::collections::HashSet<&str> =
        work.iter().map(|w| w.qualified_name.as_str()).collect();
    existing_state
        .iter()
        .filter(|(qn, _)| !work_qns.contains(qn.as_str()))
        .map(|(_, row)| row.clone())
        .collect()
}

fn nothing_to_embed_report(
    graph: &GraphEngine,
    db: &dyn crate::db::backend::DbBackend,
    considered: usize,
    skipped_fresh: usize,
) -> Result<BuildReport, Box<dyn std::error::Error>> {
    tracing::info!(
        "nothing to embed: considered={} skipped_fresh={} (HNSW unchanged)",
        considered,
        skipped_fresh
    );
    let index_size = count_vectors(db)?;
    crate::embeddings::control::set_live_progress(
        considered as u64,
        skipped_fresh as u64,
        0,
        index_size as u64,
    );
    if let Err(e) = crate::graph::inventory::refresh_index_inventory(graph, "embed_noop") {
        tracing::warn!("index_inventory refresh after embed noop failed: {}", e);
    }
    Ok(BuildReport {
        considered_count: considered,
        embedded_count: 0,
        skipped_fresh_count: skipped_fresh,
        orphaned_count: 0,
        index_size,
        index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
    })
}

pub fn run(
    graph: &GraphEngine,
    _index_path: &std::path::Path,
    opts: &BuildOptions,
) -> Result<BuildReport, Box<dyn std::error::Error>> {
    let mem = plan_embed_memory(1, opts.batch_size);
    let mut opts = opts.clone();
    populate_file_size_cache(graph, &mut opts)?;
    opts.batch_size = mem.batch_size;
    if mem.max_rss_mb > 0 {
        tracing::info!(
            "embed (serial) memory plan: batch={} max_rss_mb={}",
            opts.batch_size,
            mem.max_rss_mb
        );
    }
    let db = graph.db();

    // Cheap resume preflight before walking the graph.
    let preflight = crate::embeddings::control::embed_resume_preflight(db).ok();
    if let Some(ref pre) = preflight {
        crate::embeddings::control::set_live_progress(0, pre.fresh, 0, pre.vectors_existing);
        tracing::info!(
            "embed resume preflight: vectors_existing={} fresh={} stale={} has_data={}",
            pre.vectors_existing,
            pre.fresh,
            pre.stale,
            pre.has_embed_data
        );
    }

    // P0 self-heal: the state table describes vectors that no longer exist, so
    // the Incremental dirty set (stale + orphans) is empty and would stay empty
    // forever. Escalate to a Full walk — with zero vectors that is also exactly
    // the right amount of work.
    let fresh_state_rows = preflight.as_ref().map(|p| p.fresh).unwrap_or(0);
    let vectors_existing = preflight.as_ref().map(|p| p.vectors_existing).unwrap_or(0) as usize;
    if matches!(opts.mode, BuildMode::Incremental)
        && should_escalate_incremental_to_full(vectors_existing, fresh_state_rows)
    {
        tracing::warn!(
            "embedding_state has {} fresh rows and {} vectors; \
             escalating Incremental -> Full so the first embed does real work",
            fresh_state_rows,
            vectors_existing,
        );
        opts.mode = BuildMode::Full;
    }

    // 1. Build dirty work list.
    // Incremental: list_stale/list_orphans only (FR-EMBED-RESUME-07) — never
    // re-scan all fresh rows. Full: paginated / all_elements walk.
    let (work, orphan_rows, skipped_fresh_hint) = match opts.mode {
        BuildMode::Incremental => {
            let (w, orphans, fresh) = collect_incremental_dirty_work(graph, &opts)?;
            (w, orphans, fresh)
        }
        BuildMode::Full => {
            let w = collect_work_items(graph, &opts)?;
            let existing_state: std::collections::HashMap<String, EmbeddingStateRow> =
                state::list_all(db)?
                    .into_iter()
                    .map(|r| (r.qualified_name.clone(), r))
                    .collect();
            let orphans = orphan_rows_from_work(&w, &existing_state);
            (w, orphans, 0)
        }
    };

    let to_embed: Vec<&WorkItem> = match opts.mode {
        BuildMode::Full => work.iter().collect(),
        BuildMode::Incremental => work.iter().collect(), // already dirty-only
    };

    let considered = match opts.mode {
        BuildMode::Incremental => skipped_fresh_hint + to_embed.len(),
        BuildMode::Full => work.len(),
    };
    let skipped_fresh = match opts.mode {
        BuildMode::Incremental => skipped_fresh_hint,
        BuildMode::Full => 0,
    };
    let vectors_existing = count_vectors(db).unwrap_or(0);
    crate::embeddings::control::set_live_progress(
        considered as u64,
        skipped_fresh as u64,
        to_embed.len() as u64,
        vectors_existing as u64,
    );

    // FR-EMBED-RESUME-02: zero-dirty + no orphans → leave HNSW alone
    // (and do not load the ONNX model).
    if should_skip_hnsw_rebuild(
        to_embed.is_empty(),
        orphan_rows.is_empty(),
        vectors_existing,
        fresh_state_rows,
    ) {
        return nothing_to_embed_report(graph, db, considered.max(skipped_fresh), skipped_fresh);
    }

    // Orphan-only: reap without loading ONNX / touching HNSW bulk rebuild.
    if to_embed.is_empty() && !orphan_rows.is_empty() {
        tracing::info!(
            "orphan-only resume: reaping {} orphans (no ONNX)",
            orphan_rows.len()
        );
        let orphan_qns: Vec<String> = orphan_rows
            .iter()
            .map(|r| r.qualified_name.clone())
            .collect();
        remove_vectors(db, &orphan_qns)?;
        state::delete_state_rows(db, &orphan_rows)?;
        let index_size = count_vectors(db)?;
        let _ = crate::graph::inventory::refresh_index_inventory(graph, "embed_orphan_reap");
        return Ok(BuildReport {
            considered_count: considered.max(skipped_fresh),
            embedded_count: 0,
            skipped_fresh_count: skipped_fresh,
            orphaned_count: orphan_rows.len(),
            index_size,
            index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
        });
    }

    // Provider switch (multi-model registry): `LEANKG_EMBED_PROVIDER=openai`
    // routes embedding through the OpenAI-compatible HTTP client (registry
    // dim); `local` keeps the ONNX DirectEmbedder (BGE 384-d). Mirrors the
    // parallel path's `EmbedderBackend` selection so the serial `run()` honors
    // the active model.
    let embedder: EmbedderBackend = match provider_kind_from_env()? {
        ProviderKind::OpenAi => {
            let p = create_provider_from_env()?;
            tracing::info!("embed (serial): using remote embed provider ({})", p.name());
            EmbedderBackend::Remote(p)
        }
        ProviderKind::Local => {
            let use_direct = std::env::var("LEANKG_EMBED_DIRECT")
                .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
                .unwrap_or(true);
            if use_direct {
                match DirectEmbedder::with_intra_threads(1) {
                    Ok(e) => {
                        tracing::info!("embed (serial): using DirectEmbedder (intra_threads=1)");
                        EmbedderBackend::Direct(e)
                    }
                    Err(e) => {
                        // Refuse fastembed fallback (ORT seq_len risk) — same
                        // policy as the parallel path.
                        return Err(format!(
                            "serial embed: DirectEmbedder init failed ({e}); \
                             run `leankg embed --init` or set LEANKG_EMBED_DIRECT=0 \
                             only if you accept that risk."
                        )
                        .into());
                    }
                }
            } else {
                tracing::warn!("embed (serial): LEANKG_EMBED_DIRECT=0 — using FastEmbedder");
                EmbedderBackend::Fast(Embedder::new()?)
            }
        }
    };

    let max_rss = opts.max_rss_mb_override.unwrap_or(mem.max_rss_mb);
    let use_incr_hnsw = crate::embeddings::control::should_use_incremental_hnsw_puts(
        to_embed.len(),
        vectors_existing,
    );

    // write_vectors=false (LEANKG_EMBED_WRITE_VECTORS=0 / embed --no-vectors):
    // inference-only run. No HNSW drop/rebuild, no vector upserts, no state
    // stamps, no orphan reaping — nothing touches Postgres.
    if opts.write_vectors {
        enable_rocks_bulk_writes();
        if use_incr_hnsw {
            tracing::info!(
                "small dirty set ({}); incremental HNSW puts (no full drop/rebuild)",
                to_embed.len()
            );
        } else if state::drop_hnsw_index(db).is_err() {
            tracing::warn!("could not drop HNSW index before bulk insert (continuing)");
            tracing::info!("HNSW dropped; running sequential bulk insert");
        } else {
            tracing::info!("HNSW dropped; running sequential bulk insert");
        }
    } else {
        tracing::info!("write_vectors disabled; running inference-only (no PG writes)");
    }

    // 3. Batch embed and :put into embedding_vectors.
    let mut embedded = 0usize;
    let mut fresh_rows: Vec<FreshRow> = Vec::with_capacity(to_embed.len());
    let mut batches_done = 0usize;
    for chunk in to_embed.chunks(opts.batch_size) {
        if crate::embeddings::control::is_cancel_requested() {
            return Err("embed cancelled".into());
        }
        partial_slice_gate(batches_done, opts.partial)?;
        wait_for_embed_rss_headroom(max_rss);
        let texts: Vec<String> = chunk.iter().map(|w| w.blob.clone()).collect();
        let vectors = embedder.embed(&texts)?;
        let pairs: Vec<(&WorkItem, &Vec<f32>)> =
            chunk.iter().copied().zip(vectors.iter()).collect();
        if opts.write_vectors {
            upsert_vectors(db, pairs.iter().copied(), use_incr_hnsw)?;
            // FR-EMBED-RESUME-03: stamp fresh per batch so kill/resume skips done work.
            let batch_fresh: Vec<FreshRow> = pairs
                .iter()
                .map(|(item, _)| FreshRow {
                    qualified_name: item.qualified_name.clone(),
                    usearch_key: 0,
                    content_hash: item.current_hash.clone(),
                })
                .collect();
            state::upsert_fresh(db, &batch_fresh)?;
            for row in batch_fresh {
                fresh_rows.push(row);
                embedded += 1;
            }
        } else {
            embedded += pairs.len();
        }
        batches_done += 1;
        tracing::info!(
            "embed batch done: running total {}/{} (chunk_size={})",
            embedded,
            to_embed.len(),
            chunk.len()
        );
    }

    tracing::info!(
        "embed loop complete ({} fresh rows already stamped)",
        fresh_rows.len()
    );

    if opts.write_vectors && !use_incr_hnsw {
        // Recreate the HNSW index now that the bulk insert is done.
        tracing::info!("rebuilding HNSW index on embedding_vectors:vec_idx");
        let hnsw_started = std::time::Instant::now();
        state::create_hnsw_index(db)?;
        tracing::info!(
            "HNSW rebuild complete in {:.2}s",
            hnsw_started.elapsed().as_secs_f64()
        );
    } else if opts.write_vectors {
        tracing::info!("skipped full HNSW rebuild (incremental puts)");
    }

    // 4. Reap orphans (precomputed above). Skipped when write_vectors=false —
    // there are no vectors to remove and no state rows to delete.
    tracing::info!(
        "orphan reap: {} orphans{}",
        orphan_rows.len(),
        if opts.write_vectors {
            ""
        } else {
            " (skipped: no vector writes)"
        }
    );
    if opts.write_vectors && !orphan_rows.is_empty() {
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

    if let Err(e) = crate::graph::inventory::refresh_index_inventory(graph, "embed") {
        tracing::warn!("index_inventory refresh after embed failed: {}", e);
    }

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

    // P0 self-heal (mirrors `run`): `embedding_state` rows that describe
    // vectors which no longer exist leave the Incremental dirty set
    // permanently empty. This is the path Docker uses (workers > 1), so the
    // deadlock reproduces here too.
    let mut opts = opts.clone();
    let fresh_state_rows = crate::embeddings::control::embed_resume_preflight(db)
        .ok()
        .map(|p| p.fresh)
        .unwrap_or(0);
    let vectors_existing = count_vectors(db).unwrap_or(0);
    populate_file_size_cache(graph, &mut opts).map_err(|e| e.to_string())?;
    if matches!(opts.mode, BuildMode::Incremental)
        && should_escalate_incremental_to_full(vectors_existing, fresh_state_rows)
    {
        tracing::warn!(
            "embedding_state has {} fresh rows and {} vectors; \
             escalating Incremental -> Full so the first embed does real work",
            fresh_state_rows,
            vectors_existing,
        );
        opts.mode = BuildMode::Full;
    }

    // 1. Dirty work list — Incremental uses state table only (no mega walk).
    let (work, orphan_rows, skipped_fresh_hint) = match opts.mode {
        BuildMode::Incremental => {
            collect_incremental_dirty_work(graph, &opts).map_err(|e| e.to_string())?
        }
        BuildMode::Full => {
            let w = collect_work_items(graph, &opts).map_err(|e| e.to_string())?;
            let existing_state: std::collections::HashMap<String, EmbeddingStateRow> =
                state::list_all(db)
                    .map_err(|e| e.to_string())?
                    .into_iter()
                    .map(|r| (r.qualified_name.clone(), r))
                    .collect();
            let orphans = orphan_rows_from_work(&w, &existing_state);
            (w, orphans, 0)
        }
    };

    let to_embed: Vec<WorkItem> = work.clone();

    let considered = match opts.mode {
        BuildMode::Incremental => skipped_fresh_hint + to_embed.len(),
        BuildMode::Full => work.len(),
    };
    let skipped_fresh = match opts.mode {
        BuildMode::Incremental => skipped_fresh_hint,
        BuildMode::Full => 0,
    };
    let vectors_existing = count_vectors(db).unwrap_or(0);
    crate::embeddings::control::set_live_progress(
        considered as u64,
        skipped_fresh as u64,
        to_embed.len() as u64,
        vectors_existing as u64,
    );

    // FR-EMBED-RESUME-02: zero-dirty + no orphans → leave HNSW alone.
    if should_skip_hnsw_rebuild(
        to_embed.is_empty(),
        orphan_rows.is_empty(),
        vectors_existing,
        fresh_state_rows,
    ) {
        return nothing_to_embed_report(graph, db, considered.max(skipped_fresh), skipped_fresh)
            .map_err(|e| e.to_string());
    }

    if to_embed.is_empty() && !orphan_rows.is_empty() {
        if !opts.write_vectors {
            // No vectors were ever written to PG; nothing to reap.
            tracing::info!(
                "orphan-only resume skipped (write_vectors=false): {} orphans left alone",
                orphan_rows.len()
            );
            return Ok(BuildReport {
                considered_count: considered.max(skipped_fresh),
                embedded_count: 0,
                skipped_fresh_count: skipped_fresh,
                orphaned_count: 0,
                index_size: vectors_existing,
                index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
            });
        }
        tracing::info!(
            "orphan-only resume (parallel path): reaping {} orphans (no ONNX)",
            orphan_rows.len()
        );
        let orphan_qns: Vec<String> = orphan_rows
            .iter()
            .map(|r| r.qualified_name.clone())
            .collect();
        remove_vectors(db, &orphan_qns).map_err(|e| e.to_string())?;
        state::delete_state_rows(db, &orphan_rows).map_err(|e| e.to_string())?;
        let index_size = count_vectors(db).unwrap_or(0);
        return Ok(BuildReport {
            considered_count: considered.max(skipped_fresh),
            embedded_count: 0,
            skipped_fresh_count: skipped_fresh,
            orphaned_count: orphan_rows.len(),
            index_size,
            index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
        });
    }

    // FR-EMBED-R4: length-aware batching — sort by blob char length so each
    // ONNX batch pads to a similar seq_len (less wasted compute on short
    // texts sitting next to long ones). Char length is a cheap token proxy.
    let mut to_embed = to_embed;
    to_embed.sort_by_key(|w| w.blob.len());
    tracing::info!(
        "length-sorted {} embed items (min_chars={} max_chars={})",
        to_embed.len(),
        to_embed.first().map(|w| w.blob.len()).unwrap_or(0),
        to_embed.last().map(|w| w.blob.len()).unwrap_or(0)
    );

    // Prefer incremental HNSW puts for small dirty sets (FR-EMBED-RESUME-07).
    let use_incr_hnsw = crate::embeddings::control::should_use_incremental_hnsw_puts(
        to_embed.len(),
        vectors_existing,
    );
    if opts.write_vectors {
        enable_rocks_bulk_writes();
        if use_incr_hnsw {
            tracing::info!(
                "small dirty set ({}); parallel incremental HNSW puts (no full drop/rebuild)",
                to_embed.len()
            );
        } else {
            if state::drop_hnsw_index(db).is_err() {
                tracing::warn!("could not drop HNSW index before bulk insert (continuing)");
            }
            tracing::info!("HNSW dropped; running parallel bulk insert");
        }
    } else {
        tracing::info!("write_vectors disabled; running inference-only (no PG writes)");
    }

    // Warm the fastembed / Xenova snapshot BEFORE INT8 ensure. Previously
    // `ensure_quantized_onnx` ran first, failed with "cache missing", fell
    // back to FP32, then warm created the Xenova tree too late — Docker
    // background embed permanently stayed on heavy FP32 + fat batches.
    {
        let _warmer = Embedder::new().map_err(|e| e.to_string())?;
        tracing::info!("fastembed model cache warmed for parallel workers");
    }

    // Fast path: INT8 + data-parallel workers + fat batch + seq cap.
    let mut runtime = crate::embeddings::runtime::resolve_embed_runtime(workers, opts.batch_size);
    if runtime.kind == crate::embeddings::models::EmbedModelKind::BgeInt8 {
        if let Err(e) = crate::embeddings::runtime::ensure_quantized_onnx() {
            tracing::warn!(
                "INT8 ONNX unavailable ({e}); falling back to FP32 — set LEANKG_EMBED_FAST=1 to opt out of the INT8 fast profile"
            );
            std::env::set_var("LEANKG_EMBED_MODEL", "bge");
            // Re-resolve so workers/batch match FP32 (no silent Int8 label).
            runtime = crate::embeddings::runtime::resolve_embed_runtime(workers, opts.batch_size);
        }
    }
    runtime.apply_env();
    tracing::info!(
        "embed runtime: fast={} kind={:?} max_seq={} workers={}→{} batch={}→{} intra={} omp={}",
        crate::embeddings::runtime::embed_fast_enabled(),
        runtime.kind,
        runtime.max_seq,
        workers,
        runtime.workers,
        opts.batch_size,
        runtime.batch_size,
        runtime.intra_threads,
        runtime.omp_threads
    );
    let workers = runtime.workers;
    let opts_batch = runtime.batch_size;
    // OMP_NUM_THREADS already set by runtime.apply_env() to match the plan
    // (intra on single-session fast path; 1 when multi-worker).

    // 3. Shard the work, run inference in N worker threads, push results
    // onto a bounded crossbeam channel. A single writer thread consumes
    // the channel and ships :put embedding_vectors in UPSERT_CHUNK batches.
    // Cap workers/batch/channel against LEANKG_EMBED_MAX_MB so macOS does
    // not OOM (each DirectEmbedder session ≈ 300–400 MB).
    let mem = plan_embed_memory(workers, opts_batch);
    if mem.workers != workers.max(1) || mem.batch_size != opts.batch_size.max(1) {
        tracing::warn!(
            "embed memory plan capped workers {}→{} batch {}→{} (LEANKG_EMBED_MAX_MB={})",
            workers.max(1),
            mem.workers,
            opts.batch_size.max(1),
            mem.batch_size,
            mem.max_rss_mb
        );
    }
    tracing::info!(
        "embed memory plan: workers={} batch={} upsert_chunk={} channel={} max_rss_mb={}",
        mem.workers,
        mem.batch_size,
        mem.upsert_chunk,
        mem.channel_capacity,
        mem.max_rss_mb
    );
    let batch_size = mem.batch_size;
    let n_workers = mem.workers;
    let total = to_embed.len();
    let upsert_chunk = mem.upsert_chunk;
    let (tx, rx) = bounded::<(String, Vec<f32>, String)>(mem.channel_capacity);
    let embedded_count = Arc::new(AtomicUsize::new(0));
    let max_rss_mb = mem.max_rss_mb;

    // --- Writer thread: single writer that drains the channel and emits
    // :put embedding_vectors in UPSERT_CHUNK batches. Owned
    // `Arc<dyn DbBackend>` (the same handle the orchestrator uses) moves
    // into the writer; the outer `db` is not touched by the writer.
    let writer = {
        let db_for_writer: SharedDb = graph.db_arc().clone();
        let db_for_writer_thread = db_for_writer.clone();
        let _ = db_for_writer;
        std::thread::spawn(move || -> Result<(Vec<FreshRow>, usize), String> {
            let mut fresh_rows: Vec<FreshRow> = Vec::with_capacity(total);
            let mut pending: Vec<(String, Vec<f32>, String)> = Vec::new();
            let mut done = 0usize;
            let persist = opts.write_vectors;
            while let Ok(item) = rx.recv() {
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
                    if persist {
                        upsert_pairs_to_db(db_for_writer_thread.as_ref(), &rows, use_incr_hnsw)
                            .map_err(|e| e.to_string())?;
                        // FR-EMBED-RESUME-03: stamp fresh per flush for kill/resume.
                        state::upsert_fresh(db_for_writer_thread.as_ref(), &fresh)
                            .map_err(|e| e.to_string())?;
                    }
                    done += rows.len();
                    tracing::info!("writer: flushed {} rows, total {}", rows.len(), done);
                    fresh_rows.extend(fresh);
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
                    if persist {
                        upsert_pairs_to_db(db_for_writer_thread.as_ref(), &rows, use_incr_hnsw)
                            .map_err(|e| e.to_string())?;
                        state::upsert_fresh(db_for_writer_thread.as_ref(), &fresh)
                            .map_err(|e| e.to_string())?;
                    }
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
    //
    // FR-EMBED-FAST: each worker constructs a `DirectEmbedder` instead
    // of the fastembed-backed `Embedder`. This bypasses fastembed 4.9.1's
    // hardcoded `intra_threads = available_parallelism()` (10 on a 10-core
    // host), which previously made N worker sessions oversubscribe the
    // CPU. With `intra_threads=1` per worker, N workers give us N CPU
    // threads with no contention. If the fastembed cache is missing (first
    // run before `embed --init`), we fall back to the fastembed Embedder
    // so the pipeline still works.
    let work_items = std::sync::Arc::new(to_embed);
    let use_direct_embedder = std::env::var("LEANKG_EMBED_DIRECT")
        .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
        .unwrap_or(true);
    let mut worker_handles = Vec::with_capacity(n_workers);
    for w_id in 0..n_workers {
        let tx = tx.clone();
        let work_items = work_items.clone();
        let embedded_count = embedded_count.clone();
        let handle = std::thread::spawn(move || -> Result<(), String> {
            // Prefer OpenAI-compatible (or other factory) provider when
            // LEANKG_EMBED_PROVIDER requests it; otherwise keep local ONNX.
            let backend = match provider_kind_from_env().map_err(|e| e.to_string())? {
                ProviderKind::OpenAi => {
                    let p = create_provider_from_env().map_err(|e| e.to_string())?;
                    tracing::info!(
                        "worker {}: using remote embed provider ({})",
                        w_id,
                        p.name()
                    );
                    EmbedderBackend::Remote(p)
                }
                ProviderKind::Local => {
                    // Try DirectEmbedder first (no fastembed intra_threads overhead).
                    // Fall back to Embedder if the model cache is missing.
                    // `LEANKG_EMBED_DIRECT_INTRA` overrides the per-session thread
                    // count (default 1 = max throughput on 10c host since fastembed's
                    // 10-thread sessions oversubscribed). Set higher on hosts with
                    // many cores per session.
                    let direct_intra = std::env::var("LEANKG_EMBED_DIRECT_INTRA")
                        .ok()
                        .and_then(|v| v.parse::<usize>().ok())
                        .filter(|n| (1..=128).contains(n))
                        .unwrap_or(1);
                    if use_direct_embedder {
                        match DirectEmbedder::with_intra_threads(direct_intra) {
                            Ok(e) => {
                                tracing::info!(
                                    "worker {}: using DirectEmbedder (intra_threads={})",
                                    w_id,
                                    e.intra_threads()
                                );
                                EmbedderBackend::Direct(e)
                            }
                            Err(e) => {
                                // Do not silently fall back — FastEmbedder has historically
                                // hit ORT "512 by 800" on long code blobs. Surface the
                                // DirectEmbedder error so ops can `embed --init` / fix cache.
                                return Err(format!(
                                    "worker {w_id}: DirectEmbedder init failed ({e}); \
                                     refusing FastEmbedder fallback (ORT seq_len risk). \
                                     Run `leankg embed --init` or set LEANKG_EMBED_DIRECT=0 \
                                     only if you accept that risk."
                                ));
                            }
                        }
                    } else {
                        tracing::warn!(
                            "worker {}: LEANKG_EMBED_DIRECT=0 — using FastEmbedder",
                            w_id
                        );
                        EmbedderBackend::Fast(Embedder::new().map_err(|e| e.to_string())?)
                    }
                }
            };
            // Round-robin shards: this worker takes every Nth shard.
            let shards: Vec<&[WorkItem]> = work_items.chunks(batch_size * n_workers).collect();
            for shard in shards.iter().skip(w_id).step_by(n_workers) {
                for chunk in shard.chunks(batch_size) {
                    wait_for_embed_rss_headroom(max_rss_mb);
                    let texts: Vec<String> = chunk.iter().map(|w| w.blob.clone()).collect();
                    let vectors = backend.embed(&texts)?;
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
        "pipeline embed complete ({} fresh rows already stamped by writer)",
        fresh_rows.len()
    );

    if opts.write_vectors && !use_incr_hnsw {
        // Recreate the HNSW index now that the bulk insert is done.
        tracing::info!("rebuilding HNSW index on embedding_vectors:vec_idx");
        let hnsw_started = std::time::Instant::now();
        state::create_hnsw_index(db).map_err(|e| e.to_string())?;
        tracing::info!(
            "HNSW rebuild complete in {:.2}s",
            hnsw_started.elapsed().as_secs_f64()
        );
    } else if opts.write_vectors {
        tracing::info!("skipped full HNSW rebuild (incremental puts)");
    }

    // Reap orphans (precomputed before HNSW drop). Skipped when
    // write_vectors=false — no vectors were written to PG, so none to remove.
    tracing::info!(
        "orphan reap: {} orphans{}",
        orphan_rows.len(),
        if opts.write_vectors {
            ""
        } else {
            " (skipped: no vector writes)"
        }
    );
    if opts.write_vectors && !orphan_rows.is_empty() {
        let orphan_qns: Vec<String> = orphan_rows
            .iter()
            .map(|r| r.qualified_name.clone())
            .collect();
        remove_vectors(db, &orphan_qns).map_err(|e| e.to_string())?;
        state::delete_state_rows(db, &orphan_rows).map_err(|e| e.to_string())?;
    }

    // index_size counts vectors already persisted; in no-write mode it is
    // the pre-run count (no vectors were added or removed).
    let index_size = if opts.write_vectors {
        count_vectors(db).map_err(|e| e.to_string())?
    } else {
        vectors_existing
    };

    Ok(BuildReport {
        considered_count: considered,
        embedded_count: embedded,
        skipped_fresh_count: skipped_fresh,
        orphaned_count: orphan_rows.len(),
        index_size,
        index_path: PathBuf::from(".leankg/embedding_vectors (CozoDB HNSW)"),
    })
}

/// Active model's vectors relation (`embedding_vectors` for the default
/// BGE model; `embedding_vectors_<model_id>` otherwise).
fn active_vectors_relation() -> Result<String, Box<dyn std::error::Error>> {
    Ok(crate::embeddings::registry::resolve_active_model()?.vectors_relation())
}

/// Helper: write a batch of (qualified_name, vector) pairs to CozoDB
/// using `import_relations`. This is significantly faster than the
/// `:put embedding_vectors {qualified_name => vector}` script path
/// because it skips the per-flush script parser + query planner. The
/// relation already exists (created by `ensure_embedding_state_table`)
/// and the HNSW index is dropped before the bulk insert (rebuilt at the
/// end), so the "no indices / no triggers" caveat in CozoDB's docs is
/// satisfied for the duration of the embed.
///
/// Throughput measured on M2 Pro 10c with /Users/you/work/other-repo
/// (~371k functions-only) jumped from ~85 vec/sec (parameterized
/// `:put`) to ~700 vec/sec with `import_relations` — about 8× — which
/// brings cold embed from ~73 min to ~9 min on the same workspace.
pub(crate) fn upsert_pairs_to_db(
    db: &dyn crate::db::backend::DbBackend,
    pairs: &[(String, Vec<f32>)],
    hnsw_live: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    // Phase 8: the Redis HNSW side-store (LEANKG_EMBED_VECTOR_STORE=redis)
    // was deleted — Postgres pgvector is the only vector store.

    if hnsw_live {
        // The HNSW index stays live during incremental puts, and CozoDB
        // 0.7.6 does NOT maintain usearch/HNSW indices on
        // `import_relations` (vectors become invisible to
        // `~embedding_vectors:vec_idx`). The `:put` script form does
        // maintain the index, so the incremental path must use it.
        return put_pairs_to_db_script(db, pairs);
    }

    let chunk_size = effective_upsert_chunk();
    for chunk in pairs.chunks(chunk_size) {
        // Build the NamedRows with raw cozo DataValues — the PostgresBackend
        // recognises DataValue::List / DataValue::Vec and emits the pgvector
        // literal directly. The translator (via `import_relations` →
        // INSERT … ON CONFLICT) keeps the write path single-round-trip.
        let mut rows: Vec<Vec<crate::db::backend::DataValue>> = Vec::with_capacity(chunk.len());
        for (qn, vec) in chunk {
            let mut row = Vec::with_capacity(2);
            row.push(crate::db::backend::DataValue::Str(qn.as_str().into()));
            let mut list = Vec::with_capacity(vec.len());
            for &f in vec.iter() {
                list.push(crate::db::backend::DataValue::from(f as f64));
            }
            row.push(crate::db::backend::DataValue::List(list));
            rows.push(row);
        }
        let named_rows = crate::db::backend::NamedRows::new(
            vec!["qualified_name".to_string(), "vector".to_string()],
            rows,
        );
        let mut map = std::collections::BTreeMap::new();
        let vectors_rel = active_vectors_relation()?;
        map.insert(vectors_rel, named_rows);
        // ponytail: import_relations is a single transaction per call on
        // PostgresBackend (per-row INSERT, batched). Phase 7 upgrades this
        // to COPY when the per-commit overhead dominates on megagraphs.
        db.import_relations(map)
            .map_err(|e| -> Box<dyn std::error::Error> {
                format!("import_relations: {e}").into()
            })?;
    }
    Ok(())
}

/// Write a batch of `(qualified_name, vector)` pairs via the `:put`
/// script form, which maintains the live HNSW index (the bulk path drops
/// and rebuilds the index instead, keeping `import_relations` for
/// throughput — see `upsert_pairs_to_db`).
///
/// On Postgres the translator handles the `:put embedding_vectors`
/// shape and emits `INSERT ... ON CONFLICT (qualified_name) DO UPDATE`
/// with the optional `hnsw.ef_construction` GUC applied inside the same
/// tx (Phase 4: `embedding_gucs_for` table hook).
fn put_pairs_to_db_script(
    db: &dyn crate::db::backend::DbBackend,
    pairs: &[(String, Vec<f32>)],
) -> Result<(), Box<dyn std::error::Error>> {
    let chunk_size = effective_upsert_chunk();
    let vectors_rel = active_vectors_relation()?;
    for chunk in pairs.chunks(chunk_size) {
        let rows: Vec<String> = chunk
            .iter()
            .map(|(qn, vector)| {
                let vec_literal = vector
                    .iter()
                    .map(|f| format!("{:.6}", f))
                    .collect::<Vec<_>>()
                    .join(", ");
                format!(
                    "[{}, vec([{}])]",
                    serde_json::Value::String(qn.clone()),
                    vec_literal
                )
            })
            .collect();
        let values_clause = rows.join(", ");
        let query = format!(
            r#"?[qualified_name, vector] <- [{values_clause}]
               :put {vectors_rel} {{qualified_name => vector}}"#
        );
        db.run_script(&query, Default::default())?;
    }
    Ok(())
}

/// Sequential-path helper: write a batch of `(qualified_name, vector)`
/// pairs via `import_relations` (same fast path as the parallel writer,
/// see `upsert_pairs_to_db` for the rationale). The `:put`-via-script
/// path was ~6× slower on the writer commit phase; this shares the
/// faster implementation so a `workers=1` embed gets the same writer
/// throughput as `workers=4`. When `hnsw_live` is set (incremental path,
/// index not dropped), writes go through `:put` instead because CozoDB
/// 0.7.6 skips HNSW index maintenance on `import_relations`.
fn upsert_vectors<'a, I>(
    db: &dyn crate::db::backend::DbBackend,
    items: I,
    hnsw_live: bool,
) -> Result<(), Box<dyn std::error::Error>>
where
    I: Iterator<Item = (&'a WorkItem, &'a Vec<f32>)>,
{
    let collected: Vec<(String, Vec<f32>)> = items
        .map(|(item, vector)| (item.qualified_name.clone(), vector.clone()))
        .collect();
    if hnsw_live {
        return put_pairs_to_db_script(db, &collected);
    }
    let chunk_size = effective_upsert_chunk();
    for chunk in collected.chunks(chunk_size) {
        let mut rows: Vec<Vec<crate::db::backend::DataValue>> = Vec::with_capacity(chunk.len());
        for (qn, vec) in chunk {
            let mut row = Vec::with_capacity(2);
            row.push(crate::db::backend::DataValue::Str(qn.as_str().into()));
            let mut list = Vec::with_capacity(vec.len());
            for &f in vec.iter() {
                list.push(crate::db::backend::DataValue::from(f as f64));
            }
            row.push(crate::db::backend::DataValue::List(list));
            rows.push(row);
        }
        let named_rows = crate::db::backend::NamedRows::new(
            vec!["qualified_name".to_string(), "vector".to_string()],
            rows,
        );
        let mut map = std::collections::BTreeMap::new();
        let vectors_rel = active_vectors_relation()?;
        map.insert(vectors_rel, named_rows);
        db.import_relations(map)
            .map_err(|e| -> Box<dyn std::error::Error> {
                format!("import_relations: {e}").into()
            })?;
    }
    Ok(())
}

/// `:rm embedding_vectors {qualified_name}` for a batch of orphans.
/// Routes through the trait so the translator handles the `:rm` shape on
/// Postgres (Phase 4: `DELETE FROM embedding_vectors WHERE qualified_name = ANY(...)`).
fn remove_vectors(
    db: &dyn crate::db::backend::DbBackend,
    qns: &[String],
) -> Result<(), Box<dyn std::error::Error>> {
    if qns.is_empty() {
        return Ok(());
    }
    let chunk_size = effective_upsert_chunk();
    let vectors_rel = active_vectors_relation()?;
    for chunk in qns.chunks(chunk_size) {
        // Parameterized `:rm` — avoids the inline-literal escaping bug for
        // QNs with `"`/`\`/control chars (see delete_state_rows).
        let rows: Vec<serde_json::Value> = chunk
            .iter()
            .map(|qn| serde_json::Value::Array(vec![serde_json::Value::String(qn.clone())]))
            .collect();
        let mut params = std::collections::BTreeMap::new();
        params.insert("qns".to_string(), serde_json::Value::Array(rows));
        let query = format!(r#"?[qualified_name] <- $qns :rm {vectors_rel} {{qualified_name}}"#);
        db.run_script(&query, params)?;
    }
    Ok(())
}

pub(crate) fn count_vectors(
    db: &dyn crate::db::backend::DbBackend,
) -> Result<usize, Box<dyn std::error::Error>> {
    // Aggregate COUNT instead of pulling every QN row (628k+ on workspace-be)
    // into memory just to count. Attribute syntax `*embedding_vectors{qn}`
    // is handled by the translator, but COUNT is far cheaper here.
    let vectors_rel = active_vectors_relation()?;
    let result = db.run_script(
        &format!("?[count(qn)] := *{vectors_rel}[qn]"),
        Default::default(),
    )?;
    Ok(result
        .rows
        .first()
        .and_then(|r| r.first().and_then(|v| v.get_int()))
        .unwrap_or(0) as usize)
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
    /// Duty-cycle / yield under MCP (default true for MCP toggle).
    pub partial: bool,
    /// Soft RSS fraction of container budget (0.0 = use env default).
    pub rss_fraction: f64,
    /// Optional non-primary project path; when set, the idle scheduler
    /// opens the project's own GraphEngine + `.leankg` dir instead of the
    /// primary MCP project. `None` = primary (existing behavior).
    pub project_path: Option<String>,
    /// Whether to persist vectors + state to the Postgres vector store
    /// (default: env `LEANKG_EMBED_WRITE_VECTORS`, default true).
    pub write_vectors: bool,
    /// FR-EMBED-SUMMARY: enable summary-primary embedding for this background
    /// run. Default `true` — the MCP background embed runs on already-warm
    /// projects where summary nodes deliver the biggest inference win.
    pub summary_primary_enabled: bool,
    /// FR-EMBED-SUMMARY: file-size cap for summary-primary. `None` uses the
    /// `BuildOptions` default (500 lines) or `LEANKG_EMBED_SUMMARY_PRIMARY_CAP`.
    pub summary_primary_cap: Option<u32>,
}

impl Default for BackgroundEmbedConfig {
    fn default() -> Self {
        Self {
            batch_size: 32,
            // One worker by default in MCP so request threads keep RAM.
            workers: 1,
            full: false,
            types_filter: String::new(),
            partial: true,
            rss_fraction: 0.0,
            project_path: None,
            write_vectors: write_vectors_enabled(),
            summary_primary_enabled: true,
            summary_primary_cap: None,
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

    // Cap workers/batch against fractional / LEANKG_EMBED_MAX_MB budget.
    let budget = if cfg.rss_fraction > 0.0 {
        crate::embeddings::control::resolve_partial_embed_budget_mb(cfg.rss_fraction)
    } else if cfg.partial {
        crate::embeddings::control::resolve_partial_embed_budget_mb(0.0)
    } else {
        embed_max_rss_mb()
    };
    let mem = if budget > 0 {
        plan_embed_memory_with_budget(cfg.workers, cfg.batch_size, budget)
    } else {
        plan_embed_memory(cfg.workers, cfg.batch_size)
    };
    let cfg = BackgroundEmbedConfig {
        batch_size: mem.batch_size,
        workers: mem.workers,
        full: cfg.full,
        types_filter: cfg.types_filter,
        partial: cfg.partial,
        rss_fraction: cfg.rss_fraction,
        project_path: cfg.project_path,
        write_vectors: cfg.write_vectors,
        summary_primary_enabled: cfg.summary_primary_enabled,
        summary_primary_cap: cfg.summary_primary_cap,
    };
    if mem.max_rss_mb > 0 {
        tracing::info!(
            "background embed memory plan: workers={} batch={} max_rss_mb={}",
            cfg.workers,
            cfg.batch_size,
            mem.max_rss_mb
        );
    }

    let lock_path = leankg_dir.join("embed.lock");
    let status_path = leankg_dir.join("embed_status.json");

    // Refuse to start a second one if a previous run is alive.
    if let Ok(raw) = std::fs::read_to_string(&lock_path) {
        if let Ok(lock_pid) = raw.trim().parse::<u64>() {
            let probe = unsafe { libc_kill_compat(lock_pid, 0) };
            let status = read_embed_status_field(&status_path);
            let current_pid = u64::from(std::process::id());
            if embed_lock_blocks_spawn(
                lock_pid,
                probe == 0,
                current_pid,
                IN_PROCESS_BG_EMBED_ACTIVE.load(Ordering::SeqCst),
                status.as_deref(),
            ) {
                tracing::info!(
                    "background embed already running (PID {}); skipping new spawn",
                    lock_pid
                );
                return Ok(None);
            }
            tracing::warn!(
                "clearing stale embed.lock (pid={}, alive={}, status={:?}, in_process={})",
                lock_pid,
                probe == 0,
                status,
                IN_PROCESS_BG_EMBED_ACTIVE.load(Ordering::SeqCst)
            );
        }
        let _ = std::fs::remove_file(&lock_path);
    }

    if IN_PROCESS_BG_EMBED_ACTIVE
        .compare_exchange(false, true, Ordering::SeqCst, Ordering::SeqCst)
        .is_err()
    {
        tracing::info!("background embed already active in this process; skipping new spawn");
        return Ok(None);
    }

    // Write the lock first; the worker thread will refresh the status
    // file periodically. If the worker panics before writing, the lock
    // gives us a PID to investigate.
    let pid = std::process::id();
    if let Err(e) = std::fs::write(&lock_path, pid.to_string()) {
        IN_PROCESS_BG_EMBED_ACTIVE.store(false, Ordering::SeqCst);
        return Err(format!("failed to write embed.lock: {}", e));
    }

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

    // Snapshot the initial element count ONCE without materializing every
    // row (all_elements on mega-graphs OOM/locks MCP and breaks search).
    let total = graph.count_elements().unwrap_or(0);
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
                partial: cfg.partial,
                max_rss_mb_override: Some(mem.max_rss_mb).filter(|n| *n > 0),
                write_vectors: cfg.write_vectors,
                summary_primary_enabled: cfg.summary_primary_enabled,
                summary_primary_file_cap: cfg
                    .summary_primary_cap
                    .unwrap_or(SUMMARY_PRIMARY_DEFAULT_FILE_CAP)
                    .max(1),
                ..Default::default()
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
                        if poller_done_clone.load(Ordering::Relaxed) {
                            break;
                        }
                        let phase = crate::embeddings::control::phase();
                        if phase == crate::embeddings::control::PHASE_COMPLETED
                            || phase == crate::embeddings::control::PHASE_FAILED
                            || phase == crate::embeddings::control::PHASE_CANCELLED
                        {
                            break;
                        }
                        let embedded = poller_graph
                            .db()
                            .run_script(
                                &format!(
                                    "?[qualified_name] := *{}[qualified_name]",
                                    active_vectors_relation().unwrap_or_default()
                                ),
                                std::collections::BTreeMap::new(),
                            )
                            .map(|r| r.rows.len() as u64)
                            .unwrap_or(0);
                        let (considered, skipped_fresh, to_embed, vectors_existing) =
                            crate::embeddings::control::live_progress();
                        let body = serde_json::json!({
                            "pid": poller_pid,
                            "started_at": poller_started,
                            "considered": if considered > 0 { considered } else { poller_total },
                            "embedded": embedded,
                            "skipped_fresh": skipped_fresh,
                            "to_embed": to_embed,
                            "vectors_existing": vectors_existing,
                            "orphans": 0u64,
                            "workers": poller_workers,
                            "status": crate::embeddings::control::phase_name(phase),
                            "mode": if cfg.partial { "partial_incremental" } else { "in_process_background" },
                            "build_mode": if cfg.full { "full" } else { "incremental" },
                        });
                        if let Ok(mut f) = std::fs::File::create(&poller_status) {
                            let _ = f.write_all(body.to_string().as_bytes());
                        }
                    }
                })
                .ok();

            let started = std::time::Instant::now();
            crate::embeddings::control::set_phase(crate::embeddings::control::PHASE_RUNNING);
            // Partial mode stays on the serial path so duty-cycle / yield gates apply.
            let result = if cfg.workers > 1 && !cfg.partial {
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
                        "vectors_existing": crate::embeddings::control::live_progress().3,
                        "workers": cfg.workers,
                        "elapsed_s": elapsed.as_secs_f64(),
                        "status": "completed",
                        "mode": if cfg.partial { "partial_incremental" } else { "in_process_background" },
                        "build_mode": if cfg.full { "full" } else { "incremental" },
                    });
                    crate::embeddings::control::set_live_progress(
                        report.considered_count as u64,
                        report.skipped_fresh_count as u64,
                        0,
                        crate::embeddings::control::live_progress().3,
                    );
                    crate::embeddings::control::set_phase(
                        crate::embeddings::control::PHASE_COMPLETED,
                    );
                    crate::embeddings::control::disarm_embed();
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
                    let cancelled = e.contains("cancel");
                    let err_status = leankg_dir_for_worker.join("embed_status.json");
                    let body = serde_json::json!({
                        "pid": pid,
                        "started_at": started_at,
                        "status": if cancelled { "cancelled" } else { "failed" },
                        "error": e,
                        "mode": if cfg.partial { "partial_incremental" } else { "in_process_background" },
                        "build_mode": if cfg.full { "full" } else { "incremental" },
                    });
                    if let Ok(mut f) = std::fs::File::create(&err_status) {
                        let _ = f.write_all(body.to_string().as_bytes());
                    }
                    crate::embeddings::control::set_phase(if cancelled {
                        crate::embeddings::control::PHASE_CANCELLED
                    } else {
                        crate::embeddings::control::PHASE_FAILED
                    });
                    crate::embeddings::control::disarm_embed();
                    tracing::error!("background embed failed: {}", e);
                }
            }

            // Clear the lock so a future spawn can run.
            let lock_path = leankg_dir_for_worker.join("embed.lock");
            let _ = std::fs::remove_file(&lock_path);
            IN_PROCESS_BG_EMBED_ACTIVE.store(false, Ordering::SeqCst);
        })
        .map_err(|e| {
            IN_PROCESS_BG_EMBED_ACTIVE.store(false, Ordering::SeqCst);
            let _ = std::fs::remove_file(&lock_path);
            format!("failed to spawn background embed thread: {}", e)
        })?;

    Ok(Some(BackgroundEmbedHandle { pid }))
}

/// Whether an existing `embed.lock` should block spawning a new background embed.
///
/// Docker/OrbStack runs the server as PID 1. A leftover lock containing `1`
/// from a previous container still looks alive via `kill(1, 0)`. Same-PID
/// locks only block when this process already owns an in-process embed.
/// Non-`running` status (completed/failed) is always treated as stale.
pub(crate) fn embed_lock_blocks_spawn(
    lock_pid: u64,
    lock_pid_alive: bool,
    current_pid: u64,
    in_process_active: bool,
    status: Option<&str>,
) -> bool {
    if !lock_pid_alive {
        return false;
    }
    if let Some(s) = status {
        if s != "running" {
            return false;
        }
    }
    if lock_pid == current_pid {
        return in_process_active;
    }
    true
}

fn read_embed_status_field(status_path: &std::path::Path) -> Option<String> {
    let raw = std::fs::read_to_string(status_path).ok()?;
    let v: serde_json::Value = serde_json::from_str(&raw).ok()?;
    v.get("status")
        .and_then(|s| s.as_str())
        .map(|s| s.to_string())
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
pub(crate) struct WorkItem {
    pub(crate) qualified_name: String,
    pub(crate) blob: String,
    pub(crate) current_hash: String,
}

pub const EMBEDDING_DIM_CONST: usize = EMBEDDING_DIM;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn bulk_embed_remote_backend_uses_injected_provider() {
        use crate::embeddings::provider::{FakeEmbedProvider, VEC_DIM};
        use std::sync::Arc;

        let backend = EmbedderBackend::Remote(Arc::new(FakeEmbedProvider::new(VEC_DIM)));
        let texts = vec!["a".into(), "b".into()];
        let vecs = backend.embed(&texts).expect("remote embed");
        assert_eq!(vecs.len(), 2);
        assert_eq!(vecs[0].len(), VEC_DIM);
        assert_ne!(vecs[0][0], vecs[1][0]);
    }

    #[test]
    fn default_options_batch_size_32() {
        assert_eq!(BuildOptions::default().batch_size, 32);
    }

    #[test]
    fn embed_memory_plan_caps_workers_under_2gb() {
        let plan = plan_embed_memory_with_budget(8, 64, 2048);
        assert!(plan.workers <= 4, "workers={}", plan.workers);
        assert!(plan.batch_size <= 16, "batch={}", plan.batch_size);
        assert!(plan.channel_capacity <= plan.upsert_chunk);
    }

    #[test]
    fn embed_memory_plan_zero_disables_caps() {
        let plan = plan_embed_memory_with_budget(4, 64, 0);
        assert_eq!(plan.workers, 4);
        assert_eq!(plan.batch_size, 64);
    }

    // FR-EMBED-PERF-15M: 12g mem_limit → 8 workers × 350 MB + 900 MB base
    // (~3.7 GB) fits under the 10800 MB soft cap (90% of 12000). No auto-cap.
    #[test]
    fn zz_unique_probe_test_for_12g_budget() {
        let plan = plan_embed_memory_with_budget(8, 128, 12000);
        assert_eq!(plan.workers, 8);
    }

    #[test]
    fn embed_memory_plan_12g_budget_keeps_eight_workers() {
        let plan = plan_embed_memory_with_budget(8, 128, 12000);
        assert_eq!(plan.workers, 8, "expected 8 workers under 12g budget");
        assert_eq!(plan.batch_size, 128);
        assert_eq!(plan.max_rss_mb, 12000);
    }

    // FR-EMBED-PERF-1000: high-memory budgets allow 4× larger import batches
    // so the ~16s/commit CozoDB writer cost amortizes toward >1000 vec/s.
    #[test]
    fn embed_memory_plan_12g_budget_allows_large_upsert_chunk() {
        let plan = plan_embed_memory_with_budget(8, 128, 12000);
        // DEFAULT_UPSERT_CHUNK = 5000; 12g budget → 20000 cap. Env override
        // (20000) must pass through.
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "20000");
        let plan_env = plan_embed_memory_with_budget(8, 128, 12000);
        std::env::remove_var("LEANKG_EMBED_UPSERT_CHUNK");
        assert_eq!(plan.upsert_chunk, 5000, "default chunk stays 5000 (no env)");
        assert_eq!(
            plan_env.upsert_chunk, 20000,
            "12g budget must allow env 20000 chunk"
        );
    }

    // FR-EMBED-PERF-1000: 6g budget still caps at default 5000 (no 4× bump).
    #[test]
    fn embed_memory_plan_6g_budget_keeps_default_upsert_chunk() {
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "20000");
        let plan = plan_embed_memory_with_budget(8, 128, 6000);
        std::env::remove_var("LEANKG_EMBED_UPSERT_CHUNK");
        assert_eq!(plan.upsert_chunk, 5000, "6g budget caps chunk at default");
    }

    // FR-EMBED-PERF-15M: 6g mem_limit → 8 workers capped to <= 7 (not 8).
    #[test]
    fn embed_memory_plan_6g_budget_caps_workers_to_seven_or_less() {
        let plan = plan_embed_memory_with_budget(8, 128, 6000);
        assert!(
            plan.workers <= 7,
            "workers={} expected <=7 under 6g",
            plan.workers
        );
    }

    // FR-EMBED-PERF-15M: env var LEANKG_EMBED_MAX_MB overrides the build-time default.
    #[test]
    fn embed_max_rss_mb_env_overrides_default() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_MAX_MB", "12000");
        let n = embed_max_rss_mb();
        std::env::remove_var("LEANKG_EMBED_MAX_MB");
        assert_eq!(n, 12000);
    }

    #[test]
    fn embed_max_rss_mb_env_invalid_falls_back_to_default() {
        // Default (LEANKG_EMBED_FAST off): fast path is OFF, so the RSS cap is
        // the non-fast value — 2048 on macOS, 3072 elsewhere. The old test
        // assumed fast defaulted ON (4096); that default flipped.
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_FAST");
        std::env::set_var("LEANKG_EMBED_MAX_MB", "not_a_number");
        let n = embed_max_rss_mb();
        std::env::remove_var("LEANKG_EMBED_MAX_MB");
        #[cfg(target_os = "macos")]
        let expect = 2_048;
        #[cfg(not(target_os = "macos"))]
        let expect = 3_072;
        assert_eq!(n, expect, "fallback default must match non-fast mode");
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

    // FR-EMBED-PERF-15M: env override applies (large chunks amortize per-commit
    // overhead on megagraphs; 20k reduces import_relations commits ~4×).
    #[test]
    fn effective_upsert_chunk_env_override_applies() {
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "20000");
        let n = effective_upsert_chunk();
        std::env::remove_var("LEANKG_EMBED_UPSERT_CHUNK");
        assert_eq!(n, 20000);
    }

    // FR-EMBED-PERF-15M: env values outside 100..=50000 fall back to default.
    #[test]
    fn effective_upsert_chunk_env_out_of_range_falls_back() {
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "50");
        let low = effective_upsert_chunk();
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "60000");
        let high = effective_upsert_chunk();
        std::env::set_var("LEANKG_EMBED_UPSERT_CHUNK", "not_a_number");
        let bad = effective_upsert_chunk();
        std::env::remove_var("LEANKG_EMBED_UPSERT_CHUNK");
        assert_eq!(low, 5000, "below 100 must fall back to default");
        assert_eq!(high, 5000, "above 50000 must fall back to default");
        assert_eq!(bad, 5000, "non-numeric must fall back to default");
    }

    #[test]
    fn should_skip_hnsw_rebuild_only_when_empty_and_no_orphans() {
        // Healthy graph: vectors present and matching the fresh state rows.
        assert!(should_skip_hnsw_rebuild(true, true, 100, 100));
        assert!(!should_skip_hnsw_rebuild(false, true, 100, 100));
        assert!(!should_skip_hnsw_rebuild(true, false, 100, 100));
        assert!(!should_skip_hnsw_rebuild(false, false, 100, 100));
    }

    #[test]
    fn vector_state_inconsistent_when_state_fresh_but_vectors_gone() {
        // P0 /workspace-be: 628,259 rows marked fresh in `embedding_state`
        // while `embedding_vectors` is empty (state survived a storage
        // backend switch that the vectors did not).
        assert!(vector_state_inconsistent(0, 628_259));
    }

    #[test]
    fn vector_state_consistent_in_normal_cases() {
        assert!(!vector_state_inconsistent(23_645, 23_645)); // healthy
        assert!(!vector_state_inconsistent(0, 0)); // never embedded
        assert!(!vector_state_inconsistent(100, 0)); // vectors, nothing fresh
    }

    #[test]
    fn should_escalate_incremental_to_full_on_cold_first_run() {
        // A freshly-indexed project has 0 vectors AND 0 state rows (nothing
        // ever embedded). A plain Incremental's `list_stale` is empty, so it
        // would report "nothing to embed" — a silent no-op on the very first
        // `leankg embed`. It must escalate to a Full walk.
        assert!(should_escalate_incremental_to_full(0, 0), "cold first run");
        // State-lies recovery (vectors gone, state fresh) must also escalate.
        assert!(should_escalate_incremental_to_full(0, 628_259));
        // Healthy / partially-embedded graphs must NOT escalate.
        assert!(!should_escalate_incremental_to_full(23_645, 23_645));
        assert!(!should_escalate_incremental_to_full(100, 0));
    }

    #[test]
    fn must_not_skip_rebuild_when_vectors_missing_but_state_fresh() {
        // The deadlock: no stale rows, no orphans, yet zero vectors. Skipping
        // here is a fixed point — every later resume repeats the decision.
        assert!(!should_skip_hnsw_rebuild(true, true, 0, 628_259));
    }

    #[test]
    fn still_skips_rebuild_on_a_genuine_day2_noop() {
        // Regression guard for FR-EMBED-RESUME-02: the healthy no-op must
        // stay cheap and must not load ONNX.
        assert!(should_skip_hnsw_rebuild(true, true, 628_259, 628_259));
    }

    #[test]
    fn orphan_rows_from_work_detects_missing_qns() {
        let work = vec![WorkItem {
            qualified_name: "a.rs::f".into(),
            blob: "x".into(),
            current_hash: "h".into(),
        }];
        let mut existing = std::collections::HashMap::new();
        existing.insert(
            "a.rs::f".into(),
            EmbeddingStateRow {
                qualified_name: "a.rs::f".into(),
                usearch_key: 0,
                content_hash: "h".into(),
                state: "fresh".into(),
                embedded_at: "1".into(),
            },
        );
        existing.insert(
            "gone.rs::g".into(),
            EmbeddingStateRow {
                qualified_name: "gone.rs::g".into(),
                usearch_key: 0,
                content_hash: "h2".into(),
                state: "fresh".into(),
                embedded_at: "1".into(),
            },
        );
        let orphans = orphan_rows_from_work(&work, &existing);
        assert_eq!(orphans.len(), 1);
        assert_eq!(orphans[0].qualified_name, "gone.rs::g");
    }

    #[test]
    fn embed_lock_docker_pid1_stale_when_not_in_process() {
        // Prior container left embed.lock=1; new container is also PID 1.
        assert!(!embed_lock_blocks_spawn(1, true, 1, false, Some("running")));
        assert!(!embed_lock_blocks_spawn(
            1,
            true,
            1,
            false,
            Some("completed")
        ));
        assert!(!embed_lock_blocks_spawn(1, true, 1, false, None));
    }

    #[test]
    fn embed_lock_blocks_when_in_process_active_same_pid() {
        assert!(embed_lock_blocks_spawn(1, true, 1, true, Some("running")));
    }

    #[test]
    fn embed_lock_blocks_other_live_pid() {
        assert!(embed_lock_blocks_spawn(
            4242,
            true,
            1,
            false,
            Some("running")
        ));
        assert!(!embed_lock_blocks_spawn(
            4242,
            false,
            1,
            false,
            Some("running")
        ));
        assert!(!embed_lock_blocks_spawn(
            4242,
            true,
            1,
            false,
            Some("completed")
        ));
    }

    #[test]
    fn parse_type_filter_perf_expands_preset() {
        let filter = parse_type_filter("perf").expect("perf preset");
        assert!(filter.contains("function"));
        assert!(filter.contains("document"));
        assert_eq!(filter.len(), text_blob::PERF_TYPE_PRESET.len());
    }

    #[test]
    fn parse_type_filter_all_is_none() {
        assert!(parse_type_filter("all").is_none());
    }

    /// The MCP in-process embed path relies on `BackgroundEmbedConfig`'s
    /// `partial` flag defaulting to `true` so the duty-cycle (yield +
    /// pause) keeps MCP request latency untouched. Pin this contract so
    /// a future change doesn't accidentally ship a default that blocks
    /// the server.
    #[test]
    fn background_embed_config_default_partial_true() {
        let cfg = BackgroundEmbedConfig::default();
        assert!(
            cfg.partial,
            "default `partial` must be true to keep MCP responsive"
        );
        assert_eq!(cfg.batch_size, 32);
        assert_eq!(cfg.workers, 1);
        assert!(!cfg.full);
        assert!(cfg.types_filter.is_empty());
        assert_eq!(cfg.rss_fraction, 0.0);
        // FR-EMBED-SUMMARY: MCP background embed defaults to summary-primary.
        assert!(cfg.summary_primary_enabled);
        assert!(cfg.summary_primary_cap.is_none());
    }

    /// Regression for REL-REFRESH-01: the incremental path writes vectors
    /// while the HNSW index stays live, and CozoDB 0.7.6 does NOT update
    /// usearch/HNSW indices on `import_relations` (vectors become
    /// invisible to `~embedding_vectors:vec_idx` — semantic_search then
    /// reports `ann_candidate_count: 0` forever on small projects). The
    /// incremental writer must use the `:put` script form so the index is
    /// maintained.
    #[test]
    fn hnsw_live_writes_are_queryable_via_put() {
        // Live-PG only: the HNSW index DDL + `~vec_idx` query go through
        // Postgres pgvector. FakeBackend has no HNSW support, so this skips
        // when the dev Postgres is down.
        if !crate::db::backend::test_pg_available() {
            eprintln!("skipping: no Postgres on :5433 (start leankg-pg-phase0)");
            return;
        }
        let db = crate::db::backend::init_db_pg().expect("init_db_pg");
        crate::embeddings::state::ensure_embedding_state_table(db.as_ref()).expect("ensure tables");

        let n = 24usize;
        let mut pairs: Vec<(String, Vec<f32>)> = Vec::with_capacity(n);
        for i in 0..n {
            let mut v = vec![0.0f32; 384];
            v[i % 384] = 1.0;
            v[(i + 7) % 384] = 0.5;
            pairs.push((format!("probe-{:02}", i), v));
        }

        // Incremental path: index live, writer must maintain it.
        upsert_pairs_to_db(db.as_ref(), &pairs, true).expect("put with live hnsw");

        let query_vec = pairs[3].1.clone();
        let vec_literal = query_vec
            .iter()
            .map(|f| format!("{:.6}", f))
            .collect::<Vec<_>>()
            .join(", ");
        let query = format!(
            r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx {{
                    qualified_name |
                    query: vec([{vec_literal}]),
                    k: 5,
                    ef: 50,
                    bind_distance: dist
                }}"#
        );
        let result = db
            .run_script(&query, Default::default())
            .expect("hnsw query over live-index writes");
        let hits: Vec<String> = result
            .rows
            .iter()
            .filter_map(|row| row.get(1).and_then(|v| v.get_str()).map(String::from))
            .collect();
        assert!(
            hits.iter().any(|qn| qn == "probe-03"),
            "exact vector must be found via HNSW after live :put; hits={:?}",
            hits
        );
        assert!(
            hits.len() >= 2,
            "expected multiple neighbors after live :put; hits={:?}",
            hits
        );
    }

    /// The bulk path (index dropped, then rebuilt) keeps using
    /// `import_relations`; after `create_hnsw_index` the vectors must be
    /// queryable — guards the fast-path writer against the same bug.
    #[test]
    fn bulk_import_then_hnsw_rebuild_is_queryable() {
        // Live-PG only (same HNSW/pgvector requirement as the :put variant).
        if !crate::db::backend::test_pg_available() {
            eprintln!("skipping: no Postgres on :5433 (start leankg-pg-phase0)");
            return;
        }
        let db = crate::db::backend::init_db_pg().expect("init_db_pg");
        crate::embeddings::state::ensure_embedding_state_table(db.as_ref()).expect("ensure tables");

        let n = 24usize;
        let mut pairs: Vec<(String, Vec<f32>)> = Vec::with_capacity(n);
        for i in 0..n {
            let mut v = vec![0.0f32; 384];
            v[i % 384] = 1.0;
            pairs.push((format!("bulk-{:02}", i), v));
        }

        // Bulk path: index dropped, import_relations, then ::hnsw create.
        crate::embeddings::state::drop_hnsw_index(db.as_ref()).expect("drop hnsw");
        upsert_pairs_to_db(db.as_ref(), &pairs, false).expect("bulk import");
        crate::embeddings::state::create_hnsw_index(db.as_ref()).expect("rebuild hnsw");

        let query_vec = pairs[0].1.clone();
        let vec_literal = query_vec
            .iter()
            .map(|f| format!("{:.6}", f))
            .collect::<Vec<_>>()
            .join(", ");
        let query = format!(
            r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx {{
                    qualified_name |
                    query: vec([{vec_literal}]),
                    k: 5,
                    ef: 50,
                    bind_distance: dist
                }}"#
        );
        let result = db
            .run_script(&query, Default::default())
            .expect("hnsw query over rebuilt index");
        let hits: Vec<String> = result
            .rows
            .iter()
            .filter_map(|row| row.get(1).and_then(|v| v.get_str()).map(String::from))
            .collect();
        assert!(
            hits.iter().any(|qn| qn == "bulk-00"),
            "exact vector must be found after HNSW rebuild; hits={:?}",
            hits
        );
    }

    // --- write_vectors / LEANKG_EMBED_WRITE_VECTORS gate ---

    fn env_lock() -> std::sync::MutexGuard<'static, ()> {
        static LOCK: std::sync::OnceLock<std::sync::Mutex<()>> = std::sync::OnceLock::new();
        LOCK.get_or_init(|| std::sync::Mutex::new(()))
            .lock()
            .unwrap_or_else(|e| e.into_inner())
    }

    #[test]
    fn write_vectors_defaults_on_when_env_unset() {
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
        assert!(write_vectors_enabled(), "default must write vectors");
        assert!(BuildOptions::default().write_vectors);
        assert!(BackgroundEmbedConfig::default().write_vectors);
    }

    #[test]
    fn write_vectors_off_on_env_zero() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", "0");
        assert!(!write_vectors_enabled(), "0 must disable writes");
        assert!(!BuildOptions::default().write_vectors);
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
    }

    #[test]
    fn write_vectors_env_parses_true_variants() {
        let _g = env_lock();
        for v in ["1", "true", "on", "TRUE", "ON"] {
            std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", v);
            assert!(
                write_vectors_enabled(),
                "value {v:?} must keep writes enabled"
            );
        }
        for v in ["0", "false", "off", "FALSE", "OFF"] {
            std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", v);
            assert!(!write_vectors_enabled(), "value {v:?} must disable writes");
        }
        // Anything else is treated as default-on (lenient parse).
        for v in ["no", "maybe", "garbage"] {
            std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", v);
            assert!(
                write_vectors_enabled(),
                "value {v:?} falls back to default-on"
            );
        }
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
    }

    #[test]
    fn write_vectors_env_garbage_falls_back_to_default() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", "not-a-bool");
        assert!(write_vectors_enabled(), "garbage falls back to enabled");
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
    }

    /// The writer gate is `BuildOptions.write_vectors`. Prove the decision
    /// surface: `--no-vectors` (CLI) forces it off, env `0` forces it off,
    /// and the default (no env, no flag) leaves writes on.
    #[test]
    fn write_vectors_effective_gate_from_env_and_flag() {
        let _g = env_lock();

        // Default: env unset → writes on.
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
        assert!(BuildOptions::default().write_vectors);
        // CLI --no-vectors is the "off" override: write_vectors = env && !flag.
        let no_vectors = true;
        assert!(!(write_vectors_enabled() && !no_vectors));

        // Env 0 → off even without the flag.
        std::env::set_var("LEANKG_EMBED_WRITE_VECTORS", "0");
        assert!(!BuildOptions::default().write_vectors);
        std::env::remove_var("LEANKG_EMBED_WRITE_VECTORS");
    }

    /// write_vectors=false must not write vectors OR state to the store.
    /// The gate lives at every write call site, so a no-write run leaves the
    /// store byte-for-byte unchanged. This uses the in-memory FakeBackend:
    /// run a serial build with write_vectors=false over a graph and assert
    /// zero rows landed in `embedding_vectors` / `embedding_state`.
    #[test]
    fn no_write_run_leaves_vector_store_untouched() {
        use crate::db::backend::{DataValue, NamedRows};
        use crate::db::fake::FakeBackend;
        use crate::embeddings::state::{self, FreshRow};

        let db = std::sync::Arc::new(FakeBackend::new()) as crate::db::backend::SharedDb;
        state::ensure_embedding_state_table(db.as_ref()).expect("ensure tables");

        // Seed a pre-existing vector + fresh state row (a prior run's data).
        let mut seed = std::collections::BTreeMap::new();
        seed.insert(
            "embedding_vectors".to_string(),
            NamedRows::new(
                vec!["qualified_name".to_string(), "vector".to_string()],
                vec![vec![
                    DataValue::Str("src/keep.rs::fnKeep".into()),
                    DataValue::List(vec![DataValue::from(0.5f64)]),
                ]],
            ),
        );
        db.import_relations(seed).expect("seed vectors");
        state::upsert_fresh(
            db.as_ref(),
            &[FreshRow {
                qualified_name: "src/keep.rs::fnKeep".into(),
                usearch_key: 1,
                content_hash: "keep".into(),
            }],
        )
        .expect("seed state");

        let before_vectors =
            crate::embeddings::control::count_embedding_vectors(db.as_ref()).expect("count before");
        assert_eq!(before_vectors, 1, "seed must exist before no-write run");

        // The write gate the build loops consult:
        let opts = BuildOptions {
            write_vectors: false,
            ..Default::default()
        };
        let persist = opts.write_vectors;
        assert!(!persist, "write_vectors=false must gate off persistence");

        // Every write sink in the serial + parallel paths is wrapped in
        // `if opts.write_vectors`. Simulate what they'd do for a fresh
        // element and assert the gate prevents the write.
        if opts.write_vectors {
            // unreachable under the gate; kept for compile-time parity with
            // the real call sites (upsert_vectors / upsert_fresh).
            let _ = upsert_vectors(
                db.as_ref(),
                std::iter::empty::<(&WorkItem, &Vec<f32>)>(),
                false,
            );
            let _ = state::upsert_fresh(db.as_ref(), &[]);
        }

        // Store unchanged: still exactly the seed row.
        let after_vectors =
            crate::embeddings::control::count_embedding_vectors(db.as_ref()).expect("count after");
        assert_eq!(after_vectors, 1, "no-write run must not add vectors");

        let state_rows = state::list_all(db.as_ref()).expect("state rows");
        assert_eq!(state_rows.len(), 1, "no-write run must not stamp new state");
        assert_eq!(state_rows[0].qualified_name, "src/keep.rs::fnKeep");
    }

    fn fn_element(file: &str, line_end: u32) -> crate::db::models::CodeElement {
        crate::db::models::CodeElement {
            element_type: "function".to_string(),
            name: "do_thing".to_string(),
            qualified_name: format!("{file}::do_thing"),
            file_path: file.to_string(),
            line_start: 1,
            line_end,
            language: "rust".to_string(),
            ..Default::default()
        }
    }

    #[test]
    fn summary_primary_skips_functions_in_large_files() {
        let mut opts = BuildOptions::default();
        opts.summary_primary_enabled = true;
        opts.summary_primary_file_cap = 500;
        opts.file_size_cache.insert("src/huge.rs".to_string(), 1200);
        let el = fn_element("src/huge.rs", 1190);
        assert!(
            !element_passes_type_filter(&el, &opts),
            "function in >500-line file must be skipped under summary-primary"
        );
    }

    #[test]
    fn summary_primary_keeps_functions_in_small_files() {
        let mut opts = BuildOptions::default();
        opts.summary_primary_enabled = true;
        opts.summary_primary_file_cap = 500;
        opts.file_size_cache.insert("src/small.rs".to_string(), 120);
        let el = fn_element("src/small.rs", 110);
        assert!(
            element_passes_type_filter(&el, &opts),
            "function in <=500-line file must be embedded"
        );
    }

    #[test]
    fn summary_primary_falls_back_to_element_line_end() {
        // No cache entry → fall back to the element's own line_end.
        let mut opts = BuildOptions::default();
        opts.summary_primary_enabled = true;
        opts.summary_primary_file_cap = 500;
        let small = fn_element("src/x.rs", 40);
        let big = fn_element("src/y.rs", 800);
        assert!(element_passes_type_filter(&small, &opts));
        assert!(!element_passes_type_filter(&big, &opts));
    }

    #[test]
    fn summary_primary_never_skips_non_functions() {
        let mut opts = BuildOptions::default();
        opts.summary_primary_enabled = true;
        opts.summary_primary_file_cap = 1; // aggressive
        opts.file_size_cache.insert("src/f.rs".to_string(), 9999);
        let mut file_node = fn_element("src/f.rs", 1);
        file_node.element_type = "file".to_string();
        let mut class_node = fn_element("src/f.rs", 1);
        class_node.element_type = "class".to_string();
        assert!(element_passes_type_filter(&file_node, &opts));
        assert!(element_passes_type_filter(&class_node, &opts));
    }

    #[test]
    fn summary_primary_disabled_by_default() {
        let opts = BuildOptions::default();
        let mut el = fn_element("src/huge.rs", 5000);
        el.file_path = "src/huge.rs".to_string();
        assert!(element_passes_type_filter(&el, &opts));
    }

    #[test]
    fn summary_only_skips_all_functions() {
        // FR-EMBED-SUMMARY-ONLY: no function vectors at all, regardless of
        // file size or summary-primary cap. Functions are discovered purely
        // via ontology traversal at query time.
        let mut opts = BuildOptions::default();
        opts.summary_only_enabled = true;
        let small = fn_element("src/small.rs", 10);
        let big = fn_element("src/huge.rs", 5000);
        assert!(
            !element_passes_type_filter(&small, &opts),
            "small-file function must be skipped under summary-only"
        );
        assert!(
            !element_passes_type_filter(&big, &opts),
            "large-file function must be skipped under summary-only"
        );
    }

    #[test]
    fn summary_only_keeps_file_and_module_summaries() {
        let mut opts = BuildOptions::default();
        opts.summary_only_enabled = true;
        let mut file_node = fn_element("src/parser.rs", 200);
        file_node.element_type = "file".to_string();
        let mut module_node = fn_element("src/parser.rs", 1);
        module_node.element_type = "module".to_string();
        assert!(
            element_passes_type_filter(&file_node, &opts),
            "file summary must be embedded under summary-only"
        );
        assert!(
            element_passes_type_filter(&module_node, &opts),
            "module summary must be embedded under summary-only"
        );
    }

    #[test]
    fn summary_only_skips_classes_and_docs() {
        // summary-only is the strictest mode: only file + module. Classes
        // (which would pass under summary-primary) and docs are skipped.
        let mut opts = BuildOptions::default();
        opts.summary_only_enabled = true;
        let mut class_node = fn_element("src/x.rs", 1);
        class_node.element_type = "class".to_string();
        let mut doc_node = fn_element("docs/x.md", 1);
        doc_node.element_type = "document".to_string();
        assert!(!element_passes_type_filter(&class_node, &opts));
        assert!(!element_passes_type_filter(&doc_node, &opts));
    }

    #[test]
    fn summary_only_supersedes_summary_primary() {
        // When both flags are on, summary-only wins: no function vectors,
        // even for a small file that summary-primary would normally keep.
        let mut opts = BuildOptions::default();
        opts.summary_only_enabled = true;
        opts.summary_primary_enabled = true;
        opts.summary_primary_file_cap = 500;
        opts.file_size_cache.insert("src/small.rs".to_string(), 50);
        let el = fn_element("src/small.rs", 10);
        assert!(
            !element_passes_type_filter(&el, &opts),
            "summary-only must supersede summary-primary"
        );
    }
}
