//! Stage 2: embed query, run usearch top-K, return raw key+distance pairs.
//!
//! This module deliberately does NOT touch CozoDB — it just wraps the embedder
//! and the usearch index. The pipeline (`pipeline.rs`) is responsible for
//! mapping u64 keys back to qualified_names and applying worktree filters.

use crate::embeddings::{index::AnnIndex, models::Embedder};

pub struct AnnRetrieve<'a> {
    embedder: &'a Embedder,
    index: &'a AnnIndex,
}

impl<'a> AnnRetrieve<'a> {
    pub fn new(embedder: &'a Embedder, index: &'a AnnIndex) -> Self {
        Self { embedder, index }
    }

    /// Embed the query and run top-K search. Returns keys + raw distances,
    /// sorted by usearch's internal ordering (best-first for cosine).
    pub fn retrieve(
        &self,
        query: &str,
        top_k: usize,
    ) -> Result<Vec<crate::embeddings::AnnSearchResult>, Box<dyn std::error::Error>> {
        let qv = self
            .embedder
            .embed(&[query.to_string()])?
            .into_iter()
            .next()
            .ok_or("fastembed returned no vectors for query")?;
        Ok(self.index.search(&qv, top_k)?)
    }
}
