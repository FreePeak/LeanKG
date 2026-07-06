//! Embedding-backed retrieval pipeline. Stages 2 (ANN) and 3 (cross-encoder
//! rerank) live here; Stage 4 (KG traversal) stays in `crate::graph` and is
//! invoked by the MCP handler after this pipeline returns its seeds.
//!
//! Behind the `embeddings` feature like `crate::embeddings`.

#![cfg(feature = "embeddings")]

pub mod filter_policy;
pub mod pipeline;
pub mod rerank;

#[allow(unused_imports)]
pub use filter_policy::FilterPolicy;
#[allow(unused_imports)]
pub use pipeline::{RetrievalResult, RetrieveOptions, Seed, SemanticRetrievalPipeline};
