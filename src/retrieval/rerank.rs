//! Stage 3: cross-encoder rerank with Q4 option-A fallback.
//!
//! On any failure to load OR score, the reranker degrades to ANN-order
//! pass-through. The pipeline reads `RerankerStatus` from the result to
//! populate diagnostics so callers know when they're in fallback mode.

use crate::embeddings::{models::Reranker, RerankScore, RerankerStatus};

/// Wraps an optional `Reranker`. Constructed once at pipeline startup; if
/// construction fails, `inner` stays None and every `rerank` call returns
/// `RerankerStatus::Fallback` with the input order unchanged.
pub struct RerankStage {
    inner: Option<Reranker>,
}

impl RerankStage {
    /// Try to load the reranker. Failure is non-fatal — the pipeline still
    /// works, just without Stage 3.
    pub fn try_new() -> Self {
        match Reranker::new() {
            Ok(r) => Self { inner: Some(r) },
            Err(e) => {
                tracing::warn!(
                    "reranker load failed; pipeline will run in ANN-only fallback mode: {}",
                    e
                );
                Self { inner: None }
            }
        }
    }

    pub fn is_active(&self) -> bool {
        self.inner.is_some()
    }

    /// Score `(query, doc)` pairs and return indices into `documents` sorted
    /// by descending score. If the reranker is unavailable or the call
    /// fails, returns `(0..n, RerankerStatus::Fallback)` — i.e., ANN order
    /// is preserved.
    pub fn rerank(
        &self,
        query: &str,
        documents: Vec<String>,
    ) -> (Vec<RerankScore>, RerankerStatus) {
        let n = documents.len();
        let Some(reranker) = &self.inner else {
            return (ann_order(n), RerankerStatus::Fallback);
        };
        match reranker.rerank(query, documents) {
            Ok(scores) => (scores, RerankerStatus::Active),
            Err(e) => {
                tracing::warn!(
                    "rerank inference failed; falling back to ANN order: {}",
                    e
                );
                (ann_order(n), RerankerStatus::Fallback)
            }
        }
    }
}

fn ann_order(n: usize) -> Vec<RerankScore> {
    (0..n)
        .map(|i| RerankScore {
            document_idx: i,
            score: 0.0,
        })
        .collect()
}
