//! Embedding-based retrieval for LeanKG.
//!
//! Behind the `embeddings` cargo feature. Provides:
//! - Text-blob construction for code, ontology, and doc nodes
//! - fastembed-backed embedding inference (BGE-small-en-v1.5) and reranking
//!   (bge-reranker-v2-m3)
//! - Vector storage via CozoDB's native HNSW index on `embedding_vectors`
//! - Incremental build via the `embedding_state` CozoDB table
//! - Lazy model download + `embed --init` pre-download
//!
//! See `EMBEDDINGS.md` in this directory for the module architecture.

#![cfg(feature = "embeddings")]

pub mod build;
pub mod models;
pub mod state;
pub mod text_blob;

#[allow(unused_imports)]
pub use build::{run as build_index, BuildMode, BuildOptions, BuildReport};
#[allow(unused_imports)]
pub use models::{
    cache_dir, init_models, Embedder, InitReport, Reranker, RerankerStatus, RerankScore,
    DEFAULT_EMBEDDING_MODEL, DEFAULT_RERANKER_MODEL, EMBEDDING_DIM,
};
#[allow(unused_imports)]
pub use state::{
    count_by_state, delete_state_rows, ensure_embedding_state_table, list_all, list_orphans,
    list_stale, mark_stale_for_qualified_names, upsert_fresh,
    EmbeddingStateRow, FreshRow, StateCounts,
};
#[allow(unused_imports)]
pub use text_blob::{build_blob, classify, BlobKind};
