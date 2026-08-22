//! Embedding-based retrieval for LeanKG.
//!
//! The [`provider`] module (trait + OpenAI-compatible HTTP + factory) is
//! always available so MCP can embed via API without the ONNX stack.
//!
//! Behind the `embeddings` cargo feature:
//! - Text-blob construction for code, ontology, and doc nodes
//! - fastembed-backed embedding inference (BGE-small-en-v1.5) and reranking
//!   (bge-reranker-v2-m3)
//! - Vector storage via the pgvector HNSW index on `embedding_vectors`
//! - Incremental build via the `embedding_state` Postgres table
//! - Lazy model download + `embed --init` pre-download
//! - In-process background embed (`spawn_background_embed`) for the
//!   `LEANKG_EMBED_BACKGROUND=1` mcp-http mode
//!
//! See `EMBEDDINGS.md` in this directory for the module architecture.

pub mod profile;
pub mod provider;
pub mod registry;

// Re-exported only for `main.rs` embed/bench code (both feature-gated); the
// feature-agnostic indexer path (file_summary) uses the full
// `crate::embeddings::profile::*` path so no re-export is needed there.
#[cfg(feature = "embeddings")]
pub use profile::{active_profile, EmbedProfile};

#[cfg(feature = "embeddings")]
pub mod build;
#[cfg(feature = "embeddings")]
pub mod control;
#[cfg(feature = "embeddings")]
pub mod models;
#[cfg(feature = "embeddings")]
pub mod offsite;
#[cfg(feature = "embeddings")]
pub mod runtime;
#[cfg(feature = "embeddings")]
pub mod state;
pub mod switch;
#[cfg(feature = "embeddings")]
pub mod text_blob;

/// Serializes env-var mutation across embedding unit tests (one process-wide
/// lock) — `set_var`/`remove_var` are process-global and tests run in parallel.
#[cfg(test)]
pub(crate) mod test_env {
    use std::sync::{Mutex, OnceLock};

    pub fn lock() -> std::sync::MutexGuard<'static, ()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(())).lock().unwrap()
    }
}

#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use build::{
    build_index_parallel, embed_max_rss_mb, parse_type_filter, plan_embed_memory,
    plan_embed_memory_with_budget, run as build_index, spawn_background_embed,
    write_vectors_enabled, BackgroundEmbedConfig, BackgroundEmbedHandle, BuildMode, BuildOptions,
    BuildReport, EmbedMemoryPlan, SUMMARY_ONLY_TYPES, SUMMARY_PRIMARY_DEFAULT_FILE_CAP,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use control::{
    arm_embed, disarm_embed, embed_job_status, embed_resume_preflight, is_armed,
    request_cancel_in_process_embed, resolve_partial_embed_budget_mb,
    should_use_incremental_hnsw_puts, EmbedResumePreflight, PartialEmbedPolicy,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use models::{
    cache_dir, init_models, DirectEmbedder, EmbedModelKind, Embedder, InitReport, RerankScore,
    Reranker, RerankerStatus, DEFAULT_EMBEDDING_MODEL, DEFAULT_RERANKER_MODEL, EMBEDDING_DIM,
    MAX_SEQ_LEN,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use offsite::{
    export_work_items, import_vectors, ExportReport, ImportReport, ImportRow, MetaLine,
    META_FORMAT_EXPORT, META_FORMAT_IMPORT, META_VERSION,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use runtime::{
    embed_fast_enabled, ensure_quantized_onnx, quantized_onnx_available, resolve_embed_runtime,
    EmbedRuntimePlan,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use state::{
    count_by_state, create_hnsw_index, delete_state_rows, drop_hnsw_index,
    ensure_embedding_state_table, ensure_model_collections, has_any, list_all, list_orphans,
    list_stale, mark_stale_for_qualified_names, upsert_fresh, EmbeddingStateRow, FreshRow,
    StateCounts,
};
#[allow(unused_imports)]
pub use switch::{
    active_model_id, apply_persisted_model, resolve_active, set_active_model, PERSIST_PATH,
};
#[cfg(feature = "embeddings")]
#[allow(unused_imports)]
pub use text_blob::{build_blob, classify, BlobKind, PERF_TYPE_PRESET};

#[allow(unused_imports)]
pub use provider::{
    create_provider_from_env, create_provider_from_env_with_dim, embed_query,
    provider_kind_from_env, validate_provider, vec_dim, EmbedError, EmbedProvider,
    FakeEmbedProvider, OpenAiCompatibleProvider, ProviderKind, VEC_DIM,
};

#[allow(unused_imports)]
pub use registry::{
    active_model_id_from_env, backfill_legacy_model_id, builtin_registry,
    create_provider_for_active_model, create_provider_for_entry, lookup_model,
    resolve_active_model, sanitize_model_id_for_table, state_relation_for_model_id,
    vectors_relation_for_model_id, EmbeddingModelEntry, RegistryProviderKind, DEFAULT_BGE_MODEL_ID,
    LEGACY_STATE_RELATION, LEGACY_VECTORS_RELATION,
};
