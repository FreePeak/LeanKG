//! fastembed wrappers: embedding inference + cross-encoder reranking, plus
//! model pre-download (`embed --init`) and lazy-download cache configuration.
//!
//! Both the embedder (BGE-small-en-v1.5, 384-dim) and the reranker
//! (bge-reranker-v2-m3) are loaded via fastembed, which handles ONNX
//! Runtime initialization and model caching internally. We set the cache
//! directory to a LeanKG-specific location so models don't collide with
//! other fastembed users.

use fastembed::{
    EmbeddingModel, InitOptions, RerankInitOptions, RerankerModel, TextEmbedding, TextRerank,
};
use std::path::PathBuf;

/// Where fastembed will store downloaded ONNX weights. Linux:
/// `~/.cache/leankg/models`; macOS: `~/Library/Caches/leankg/models`;
/// Windows: `%LOCALAPPDATA%\leankg\models`. Falls back to
/// `./.leankg-cache/models` if no home directory is resolvable.
pub fn cache_dir() -> PathBuf {
    dirs::cache_dir()
        .unwrap_or_else(|| std::path::PathBuf::from(".leankg-cache"))
        .join("leankg")
        .join("models")
}

/// Default embedding model. 384-dim, ~130MB ONNX, fast on CPU.
pub const DEFAULT_EMBEDDING_MODEL: EmbeddingModel = EmbeddingModel::BGESmallENV15;

/// Default reranker model. Multilingual, ~600MB ONNX.
pub const DEFAULT_RERANKER_MODEL: RerankerModel = RerankerModel::BGERerankerV2M3;

/// Embedding dimension for the default embedding model. Used to size the
/// usearch index without having to load the model first.
pub const EMBEDDING_DIM: usize = 384;

/// Wraps a fastembed `TextEmbedding`. Cheap to clone post-construction;
/// construction is expensive (model load, ~1s after first cache).
pub struct Embedder {
    inner: TextEmbedding,
}

impl Embedder {
    /// Load the default embedding model. Triggers lazy-download on first
    /// call per machine. Subsequent calls hit the on-disk cache.
    pub fn new() -> Result<Self, Box<dyn std::error::Error>> {
        Self::with_model(DEFAULT_EMBEDDING_MODEL)
    }

    pub fn with_model(model: EmbeddingModel) -> Result<Self, Box<dyn std::error::Error>> {
        // NOTE: fastembed 4.9.1 hard-codes `intra_threads = available_parallelism()`
        // (text_embedding/impl.rs:52) with no public override. On a 10-core host
        // each ORT thread pre-allocates its own arena, which is the RSS blow-up
        // users see at large batch sizes. We bound peak memory via batch_size
        // (see BuildOptions::default) rather than thread count. Users on small
        // hosts should pass `--batch-size 4` (or lower) to `embed`.
        let opts = InitOptions::new(model)
            .with_cache_dir(cache_dir())
            .with_show_download_progress(true);
        let inner = TextEmbedding::try_new(opts)?;
        Ok(Self { inner })
    }

    /// Embed a batch of texts. Returns one 384-dim vector per input text,
    /// in the same order. Batch size is fastembed's default (256).
    pub fn embed(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, Box<dyn std::error::Error>> {
        let borrowed: Vec<&str> = texts.iter().map(|s| s.as_str()).collect();
        let vectors = self.inner.embed(borrowed, None)?;
        Ok(vectors)
    }

    pub fn dim(&self) -> usize {
        EMBEDDING_DIM
    }
}

/// Wraps a fastembed `TextRerank` cross-encoder.
pub struct Reranker {
    inner: TextRerank,
}

impl Reranker {
    pub fn new() -> Result<Self, Box<dyn std::error::Error>> {
        Self::with_model(DEFAULT_RERANKER_MODEL)
    }

    pub fn with_model(model: RerankerModel) -> Result<Self, Box<dyn std::error::Error>> {
        // See Embedder::with_model note: fastembed 4.9.1 doesn't expose
        // intra_threads publicly; we bound reranker RSS via the retrieval
        // pipeline's candidate count (default top_k=50 → ≤50 docs reranked).
        let opts = RerankInitOptions::new(model)
            .with_cache_dir(cache_dir())
            .with_show_download_progress(true);
        let inner = TextRerank::try_new(opts)?;
        Ok(Self { inner })
    }

    /// Score `(query, document)` pairs and return indices sorted by
    /// descending score. `documents` is consumed; the returned indices
    /// reference the original input positions.
    pub fn rerank(
        &self,
        query: &str,
        documents: Vec<String>,
    ) -> Result<Vec<RerankScore>, Box<dyn std::error::Error>> {
        let borrowed: Vec<&str> = documents.iter().map(|s| s.as_str()).collect();
        let results = self.inner.rerank(query, borrowed, false, None)?;
        Ok(results
            .into_iter()
            .map(|r| RerankScore {
                document_idx: r.index,
                score: r.score,
            })
            .collect())
    }
}

#[derive(Debug, Clone)]
pub struct RerankScore {
    pub document_idx: usize,
    pub score: f32,
}

/// Operational status of the reranker. Used by the retrieval pipeline to
/// decide whether to skip Stage 3 (Q4 option A: ANN-only fallback).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RerankerStatus {
    /// Cross-encoder is loaded and being applied.
    Active,
    /// Reranker failed to initialize; pipeline is returning ANN-order top-N.
    Fallback,
}

/// Pre-download both models into the cache so subsequent `embed` and
/// `kg_semantic_context` calls don't pay the download cost. Implements
/// `cargo run --release -- embed --init`.
pub fn init_models() -> Result<InitReport, Box<dyn std::error::Error>> {
    tracing::info!("initializing embedding + reranker models at {}", cache_dir().display());
    let _embedder = Embedder::new()?;
    let _reranker = Reranker::new()?;
    Ok(InitReport {
        cache_dir: cache_dir(),
    })
}

#[derive(Debug, Clone)]
pub struct InitReport {
    pub cache_dir: PathBuf,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cache_dir_ends_with_leankg_models() {
        let dir = cache_dir();
        let components: Vec<_> = dir.components().collect();
        let last_two: Vec<String> = components
            .into_iter()
            .rev()
            .take(2)
            .map(|c| c.as_os_str().to_string_lossy().to_string())
            .collect();
        assert_eq!(last_two, vec!["models".to_string(), "leankg".to_string()]);
    }

    #[test]
    fn embedding_dim_matches_bge_small() {
        assert_eq!(EMBEDDING_DIM, 384);
    }
}
