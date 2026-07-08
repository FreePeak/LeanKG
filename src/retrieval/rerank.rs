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
                tracing::warn!("rerank inference failed; falling back to ANN order: {}", e);
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ann_order_empty_returns_empty() {
        let result = ann_order(0);
        assert!(result.is_empty());
    }

    #[test]
    fn ann_order_returns_sequential_indices() {
        let result = ann_order(5);
        assert_eq!(result.len(), 5);
        for (i, score) in result.iter().enumerate() {
            assert_eq!(
                score.document_idx, i,
                "index {i} should map to document_idx {i}"
            );
        }
    }

    #[test]
    fn ann_order_scores_are_all_zero() {
        let result = ann_order(10);
        for score in &result {
            assert_eq!(score.score, 0.0, "fallback scores must be 0.0");
        }
    }

    #[test]
    fn ann_order_single_element() {
        let result = ann_order(1);
        assert_eq!(result.len(), 1);
        assert_eq!(result[0].document_idx, 0);
        assert_eq!(result[0].score, 0.0);
    }

    #[test]
    fn rerank_stage_inactive_reports_false() {
        // We can't construct a RerankStage without a model download, but we
        // can verify the is_active() contract on a stage with inner=None via
        // the try_new() fallback path. On CI without cached models, try_new()
        // will fail and inner will be None.
        // This test is a no-op if models are available; it only verifies the
        // inactive path when it occurs.
        let stage = RerankStage::try_new();
        if !stage.is_active() {
            // Fallback path: rerank should return ANN order with Fallback status.
            let docs = vec!["doc1".to_string(), "doc2".to_string(), "doc3".to_string()];
            let (scores, status) = stage.rerank("query", docs);
            assert_eq!(status, RerankerStatus::Fallback);
            assert_eq!(scores.len(), 3);
            for (i, s) in scores.iter().enumerate() {
                assert_eq!(s.document_idx, i);
                assert_eq!(s.score, 0.0);
            }
        }
    }
}
