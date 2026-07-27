//! Embedding-backed retrieval pipeline. Stages 2 (ANN) and 3 (cross-encoder
//! rerank) live here; Stage 4 (KG traversal) stays in `crate::graph` and is
//! invoked by the MCP handler after this pipeline returns its seeds.
//!
//! Behind the `embeddings` feature like `crate::embeddings`.

#![cfg(feature = "embeddings")]

pub mod filter_policy;
pub mod ontology_traversal;
pub mod pipeline;
pub mod rerank;

#[allow(unused_imports)]
pub use filter_policy::FilterPolicy;
#[allow(unused_imports)]
pub use ontology_traversal::{
    composite_text, cosine, downward_rule_for, is_function_target, is_upper_type,
    score_functions, traverse_to_functions, DiscoveredFunction, UpperSeed,
};
#[allow(unused_imports)]
pub use pipeline::{RetrievalResult, RetrieveOptions, Seed, SemanticRetrievalPipeline};
