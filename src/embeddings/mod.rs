//! Embedding-based retrieval for LeanKG.
//!
//! Behind the `embeddings` cargo feature. Provides:
//! - Text-blob construction for code, ontology, and doc nodes
//! - fastembed-backed embedding inference (BGE-small-en-v1.5) and reranking
//!   (bge-reranker-v2-m3)
//! - usearch HNSW ANN index with file persistence
//! - Incremental build via the `embedding_state` CozoDB table
//! - Lazy model download + `embed --init` pre-download
//!
//! See `docs/plans/2026-06-30-embedding-retrieve-rerank-traverse.md` for
//! the design rationale and decision history.

#![cfg(feature = "embeddings")]

pub mod build;
pub mod index;
pub mod models;
pub mod state;
pub mod text_blob;

pub use build::{run as build_index, BuildMode, BuildOptions, BuildReport};
pub use index::{AnnIndex, AnnSearchResult};
pub use models::{
    cache_dir, init_models, Embedder, InitReport, Reranker, RerankerStatus, RerankScore,
    DEFAULT_EMBEDDING_MODEL, DEFAULT_RERANKER_MODEL, EMBEDDING_DIM,
};
pub use state::{
    count_by_state, delete_state_rows, ensure_embedding_state_table, list_all, list_orphans,
    list_stale, lookup_usearch_key, mark_stale_for_qualified_names, upsert_fresh,
    EmbeddingStateRow, FreshRow, StateCounts,
};
pub use text_blob::{build_blob, classify, usearch_key_for, BlobKind};
