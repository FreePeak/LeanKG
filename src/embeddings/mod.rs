//! Embedding-based retrieval for LeanKG.
//!
//! Behind the `embeddings` cargo feature. Provides:
//! - Text-blob construction for code, ontology, and doc nodes
//! - fastembed-backed embedding inference (BGE-small-en-v1.5) and reranking
//!   (bge-reranker-v2-m3)
//! - Vector storage via CozoDB's native HNSW index on `embedding_vectors`
//! - Incremental build via the `embedding_state` CozoDB table
//! - Lazy model download + `embed --init` pre-download
//! - In-process background embed (`spawn_background_embed`) for the
//!   `LEANKG_EMBED_BACKGROUND=1` mcp-http mode
//!
//! See `EMBEDDINGS.md` in this directory for the module architecture.

#![cfg(feature = "embeddings")]

pub mod build;
pub mod models;
pub mod redis_store;
pub mod runtime;
pub mod state;
pub mod text_blob;

#[allow(unused_imports)]
pub use build::{
    build_index_parallel, embed_max_rss_mb, parse_type_filter, plan_embed_memory,
    run as build_index, spawn_background_embed, BackgroundEmbedConfig, BackgroundEmbedHandle,
    BuildMode, BuildOptions, BuildReport, EmbedMemoryPlan,
};
#[allow(unused_imports)]
pub use models::{
    cache_dir, init_models, DirectEmbedder, EmbedModelKind, Embedder, InitReport, RerankScore,
    Reranker, RerankerStatus, DEFAULT_EMBEDDING_MODEL, DEFAULT_RERANKER_MODEL, EMBEDDING_DIM,
    MAX_SEQ_LEN,
};
#[allow(unused_imports)]
pub use runtime::{
    embed_fast_enabled, ensure_quantized_onnx, quantized_onnx_available, resolve_embed_runtime,
    EmbedRuntimePlan,
};
#[allow(unused_imports)]
pub use state::{
    count_by_state, create_hnsw_index, delete_state_rows, drop_hnsw_index,
    ensure_embedding_state_table, list_all, list_orphans, list_stale,
    mark_stale_for_qualified_names, upsert_fresh, EmbeddingStateRow, FreshRow, StateCounts,
};
#[allow(unused_imports)]
pub use text_blob::{build_blob, classify, BlobKind};
