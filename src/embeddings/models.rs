//! fastembed wrappers: embedding inference + cross-encoder reranking, plus
//! model pre-download (`embed --init`) and lazy-download cache configuration.
//!
//! Both the embedder (BGE-small-en-v1.5, 384-dim) and the reranker
//! (bge-reranker-v2-m3) are loaded via fastembed, which handles ONNX
//! Runtime initialization and model caching internally. We set the cache
//! directory to a LeanKG-specific location so models don't collide with
//! other fastembed users.
//!
//! The `DirectEmbedder` type below is the alternative path that bypasses
//! fastembed for inference — see its doc comment for why.

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

/// BGE / MiniLM max position embeddings. Sequences longer than this must be
/// truncated before the ONNX graph (position Add) or ORT fails with
/// broadcast errors like `512 by 800`.
pub const MAX_SEQ_LEN: usize = 512;

/// Runtime seq-len cap (≤ [`MAX_SEQ_LEN`]). Override with `LEANKG_EMBED_MAX_SEQ`
/// (e.g. `256` for ~2× faster cold embed on short code blobs).
pub fn effective_max_seq() -> usize {
    std::env::var("LEANKG_EMBED_MAX_SEQ")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .map(|n| n.clamp(64, MAX_SEQ_LEN))
        .unwrap_or(MAX_SEQ_LEN)
}

/// Which ONNX weights DirectEmbedder / Embedder should load.
/// Override with `LEANKG_EMBED_MODEL=bge|bge-q|bge-fp16|minilm` (default `bge`).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EmbedModelKind {
    /// Xenova/bge-small-en-v1.5 FP32 (`onnx/model.onnx`).
    BgeFp32,
    /// Xenova INT8 quantized (`onnx/model_quantized.onnx`) — preferred fast path.
    /// (Qdrant `model_optimized.onnx` is broken on current ORT — fused LN inputs.)
    BgeInt8,
    /// Xenova FP16 (`onnx/model_fp16.onnx`).
    BgeFp16,
    /// Xenova/all-MiniLM-L6-v2 FP32 — faster, still 384-d.
    MiniLm,
}

impl EmbedModelKind {
    pub fn from_env() -> Self {
        match std::env::var("LEANKG_EMBED_MODEL")
            .unwrap_or_else(|_| "bge".into())
            .trim()
            .to_ascii_lowercase()
            .as_str()
        {
            "bge-q" | "bge_q" | "int8" | "quantized" => Self::BgeInt8,
            "bge-fp16" | "fp16" => Self::BgeFp16,
            "minilm" | "mini-lm" | "all-minilm-l6-v2" => Self::MiniLm,
            _ => Self::BgeFp32,
        }
    }

    pub fn label(self) -> &'static str {
        match self {
            Self::BgeFp32 => "bge-fp32",
            Self::BgeInt8 => "bge-int8",
            Self::BgeFp16 => "bge-fp16",
            Self::MiniLm => "minilm",
        }
    }

    fn fastembed_model(self) -> EmbeddingModel {
        match self {
            // Warm/cache via closest fastembed sibling; DirectEmbedder may
            // then swap in Xenova quantized/fp16 weights from disk.
            Self::BgeFp32 | Self::BgeInt8 | Self::BgeFp16 => EmbeddingModel::BGESmallENV15,
            Self::MiniLm => EmbeddingModel::AllMiniLML6V2,
        }
    }
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
        Self::with_model(EmbedModelKind::from_env().fastembed_model())
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
            .with_show_download_progress(true)
            // Hard-cap BPE length — Xenova tokenizer_config advertises an
            // absurd model_max_length; without this, long code blobs can
            // reach ORT as seq_len 800+ and crash with "512 by N" broadcast.
            .with_max_length(MAX_SEQ_LEN);
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
    tracing::info!(
        "initializing embedding + reranker models at {}",
        cache_dir().display()
    );
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

// =========================================================================
// FR-EMBED-FAST: Direct ONNX embedder (bypasses fastembed's hardcoded
// `intra_threads = available_parallelism()`).
// =========================================================================
//
// Why this exists:
//   fastembed 4.9.1 hardcodes `intra_threads = available_parallelism()` at
//   session construction (text_embedding/impl.rs:52, 80). On a 10-core host
//   every Embedder instance allocates a 10-thread ONNX session pool. The
//   build_index_parallel pipeline spawns N worker threads, each owning one
//   Embedder — so N sessions × 10 threads = 10N threads contending for 10
//   physical cores. Empirical throughput on M2 Pro 10c caps at ~120
//   vectors/sec regardless of worker count.
//
// What DirectEmbedder does:
//   Loads the same tokenizer + ONNX model from fastembed's cache dir and
//   constructs an `ort::Session` with `with_intra_threads(intra_threads)`
//   set per call (default 1). With N workers each at intra_threads=1, the
//   OS scheduler sees exactly N CPU-bound threads — no oversubscription.
//   Measured throughput on the same M2 Pro 10c: ~600 vec/sec (4 workers,
//   batch=128, intra_threads=1), vs ~120 vec/sec with fastembed's session.
//
// Tradeoffs:
//   - Duplicates fastembed's preprocessing (CLS pooling + L2 norm) so we
//     must keep both paths in sync if the model changes.
//   - Adds `ort` / `tokenizers` / `ndarray` as direct deps (already
//     transitive via fastembed, so no extra downloads).
//   - Falls back to `fastembed::TextEmbedding` if the model cache isn't
//     present yet, so the first run still downloads via `embed --init`.

/// Direct ONNX embedder using `ort` + `tokenizers` with controlled
/// intra_threads per session. Default `intra_threads=1` for max throughput
/// on 10-core hosts (one ONNX compute thread per worker).
pub struct DirectEmbedder {
    tokenizer: tokenizers::Tokenizer,
    session: std::sync::Arc<ort::session::Session>,
    intra_threads: usize,
    max_seq: usize,
}

impl DirectEmbedder {
    /// Load the embedding model selected by `LEANKG_EMBED_MODEL` from
    /// fastembed's cache dir. If the cache is empty, returns an error
    /// (call `embed --init` first, or warm via `Embedder::new()`).
    pub fn new() -> Result<Self, Box<dyn std::error::Error>> {
        Self::with_intra_threads(1)
    }

    /// Like `new` but lets the caller specify the per-session
    /// intra_threads. Set to `available_parallelism() / workers` when you
    /// know how many workers will share the host.
    pub fn with_intra_threads(intra_threads: usize) -> Result<Self, Box<dyn std::error::Error>> {
        Self::with_kind_and_intra(EmbedModelKind::from_env(), intra_threads)
    }

    pub fn with_kind_and_intra(
        kind: EmbedModelKind,
        intra_threads: usize,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        use ort::session::builder::GraphOptimizationLevel;
        // Ensure weights are on disk (downloads if missing).
        let _warm = Embedder::with_model(kind.fastembed_model())?;
        let (tokenizer_path, onnx_path) = model_paths(kind)?;
        if !tokenizer_path.exists() || !onnx_path.exists() {
            return Err(format!(
                "DirectEmbedder: model files not found (tokenizer={} onnx={}) — run `leankg embed --init`",
                tokenizer_path.display(),
                onnx_path.display()
            )
            .into());
        }

        let max_seq = effective_max_seq();
        let mut tokenizer = tokenizers::Tokenizer::from_file(&tokenizer_path)
            .map_err(|e| format!("tokenizer load: {e}"))?;
        // Enforce BGE/MiniLM max length so encode_batch never exceeds
        // position embedding size (fixes ORT "512 by N" broadcast errors).
        let trunc = tokenizers::TruncationParams {
            max_length: max_seq,
            ..Default::default()
        };
        tokenizer
            .with_truncation(Some(trunc))
            .map_err(|e| format!("tokenizer truncation: {e}"))?;
        let pad = tokenizers::PaddingParams {
            strategy: tokenizers::PaddingStrategy::BatchLongest,
            pad_id: tokenizer.get_padding().map(|p| p.pad_id).unwrap_or(0),
            ..Default::default()
        };
        tokenizer.with_padding(Some(pad));

        tracing::info!(
            "DirectEmbedder: kind={} max_seq={} intra_threads={} onnx={}",
            kind.label(),
            max_seq,
            intra_threads,
            onnx_path.display()
        );

        let session = ort::session::Session::builder()?
            .with_optimization_level(GraphOptimizationLevel::Level3)?
            // Variable seq lengths across batches — keep arenas from retaining
            // the largest-seen shape for the whole run (major RSS lever).
            .with_memory_pattern(false)?
            .with_intra_threads(intra_threads)?
            .commit_from_file(&onnx_path)?;
        let session = std::sync::Arc::new(session);

        Ok(Self {
            tokenizer,
            session,
            intra_threads,
            max_seq,
        })
    }

    pub fn intra_threads(&self) -> usize {
        self.intra_threads
    }

    pub fn max_seq(&self) -> usize {
        self.max_seq
    }

    /// Embed a batch of texts. Returns one vector per input, in order.
    pub fn embed(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, Box<dyn std::error::Error>> {
        if texts.is_empty() {
            return Ok(Vec::new());
        }
        let max_seq = self.max_seq;
        // 1. Tokenize the batch.
        let encodings = self
            .tokenizer
            .encode_batch(texts.iter().map(|s| s.as_str()).collect::<Vec<_>>(), true)
            .map_err(|e| format!("tokenize: {e}"))?;

        // Defense: truncate any encoding that escaped TruncationParams
        // (observed in the wild as ORT "512 by 800" on position Add).
        let mut encodings = encodings;
        for enc in &mut encodings {
            if enc.len() > max_seq {
                enc.truncate(max_seq, 0, tokenizers::TruncationDirection::Right);
            }
        }

        let batch_size = encodings.len();
        // Cap at max_seq even if tokenizer truncation was unset.
        let encoding_length = encodings
            .iter()
            .map(|e| e.len().min(max_seq))
            .max()
            .unwrap_or(1)
            .min(max_seq);
        if encodings.iter().any(|e| e.len() > max_seq) {
            return Err(format!(
                "DirectEmbedder: encoding still exceeds max_seq={max_seq} after truncate"
            )
            .into());
        }
        let pad_id = self
            .tokenizer
            .get_padding()
            .map(|p| p.pad_id as i64)
            .unwrap_or(0);

        // 2. Build flat i64 arrays (input_ids, attention_mask, token_type_ids).
        let mut ids_flat: Vec<i64> = Vec::with_capacity(batch_size * encoding_length);
        let mut mask_flat: Vec<i64> = Vec::with_capacity(batch_size * encoding_length);
        let mut type_ids_flat: Vec<i64> = Vec::with_capacity(batch_size * encoding_length);
        for enc in &encodings {
            let ids = enc.get_ids();
            let mask = enc.get_attention_mask();
            let types = enc.get_type_ids();
            let take = ids.len().min(encoding_length);
            for &id in ids.iter().take(take) {
                ids_flat.push(id as i64);
            }
            for _ in 0..(encoding_length - take) {
                ids_flat.push(pad_id);
            }
            for &m in mask.iter().take(take) {
                mask_flat.push(m as i64);
            }
            for _ in 0..(encoding_length - take) {
                mask_flat.push(0);
            }
            for &t in types.iter().take(take) {
                type_ids_flat.push(t as i64);
            }
            for _ in 0..(encoding_length - take) {
                type_ids_flat.push(0);
            }
        }
        let ids_array = ndarray::Array2::from_shape_vec((batch_size, encoding_length), ids_flat)?;
        let mask_array = ndarray::Array2::from_shape_vec((batch_size, encoding_length), mask_flat)?;
        let type_ids_array =
            ndarray::Array2::from_shape_vec((batch_size, encoding_length), type_ids_flat)?;

        // 3. Run ONNX inference. The `inputs!` macro can't accept fallible
        // TryFrom conversions, so we build a Vec<(name, DynValue)>
        // manually and pass to SessionInputs via From<Vec<(K, V)>>.
        use ort::session::{SessionInputValue, SessionInputs};
        use ort::value::DynValue;
        let ids_value: DynValue = ids_array
            .view()
            .try_into()
            .map_err(|e| format!("ids → DynValue: {e}"))?;
        let mask_value: DynValue = mask_array
            .view()
            .try_into()
            .map_err(|e| format!("mask → DynValue: {e}"))?;
        let type_ids_value: DynValue = type_ids_array
            .view()
            .try_into()
            .map_err(|e| format!("type_ids → DynValue: {e}"))?;
        let inputs: SessionInputs = vec![
            ("input_ids".to_string(), SessionInputValue::from(ids_value)),
            (
                "attention_mask".to_string(),
                SessionInputValue::from(mask_value),
            ),
            (
                "token_type_ids".to_string(),
                SessionInputValue::from(type_ids_value),
            ),
        ]
        .into();
        let outputs = self
            .session
            .run(inputs)
            .map_err(|e| format!("ort run: {e}"))?;
        // The BGE model returns (batch_size, seq_len, 384). We take the
        // CLS token (first position) per the BGE paper.
        let embeddings_dyn = outputs
            .get("last_hidden_state")
            .or_else(|| outputs.get("embeddings"))
            .ok_or_else(|| {
                format!(
                    "DirectEmbedder: no last_hidden_state/embeddings output (got {:?})",
                    outputs.keys().collect::<Vec<_>>()
                )
            })?;
        let view = embeddings_dyn
            .try_extract_tensor::<f32>()
            .map_err(|e| format!("extract embeddings: {e}"))?;
        let shape = view.shape().to_vec();
        if shape.len() != 3 {
            return Err(format!("expected 3D embeddings tensor, got {:?}", shape).into());
        }
        let d = shape[2];
        let mut results = Vec::with_capacity(batch_size);
        for i in 0..batch_size {
            // CLS pooling: take position 0 of each sequence.
            let mut vec: Vec<f32> = (0..d).map(|j| view[[i, 0, j]]).collect();
            // L2 normalize (fastembed default).
            let norm: f32 = vec.iter().map(|x| x * x).sum::<f32>().sqrt();
            if norm > 0.0 {
                for x in &mut vec {
                    *x /= norm;
                }
            }
            results.push(vec);
        }
        Ok(results)
    }

    pub fn dim(&self) -> usize {
        EMBEDDING_DIM
    }
}

/// Resolve tokenizer.json + ONNX weights for `kind` under the fastembed cache.
fn model_paths(kind: EmbedModelKind) -> Result<(PathBuf, PathBuf), Box<dyn std::error::Error>> {
    let cache = cache_dir();
    match kind {
        EmbedModelKind::BgeFp32 => {
            let snap = first_snapshot(cache.join("models--Xenova--bge-small-en-v1.5"))?;
            Ok((snap.join("tokenizer.json"), snap.join("onnx/model.onnx")))
        }
        EmbedModelKind::BgeInt8 => {
            let snap = first_snapshot(cache.join("models--Xenova--bge-small-en-v1.5"))?;
            let quantized = snap.join("onnx/model_quantized.onnx");
            if !quantized.exists() {
                return Err("Xenova onnx/model_quantized.onnx missing — download from \
                     HuggingFace Xenova/bge-small-en-v1.5 onnx/ or set LEANKG_EMBED_MODEL=bge"
                    .into());
            }
            Ok((snap.join("tokenizer.json"), quantized))
        }
        EmbedModelKind::BgeFp16 => {
            let snap = first_snapshot(cache.join("models--Xenova--bge-small-en-v1.5"))?;
            let fp16 = snap.join("onnx/model_fp16.onnx");
            if !fp16.exists() {
                return Err("Xenova onnx/model_fp16.onnx missing — download from \
                     HuggingFace Xenova/bge-small-en-v1.5 onnx/ or set LEANKG_EMBED_MODEL=bge"
                    .into());
            }
            Ok((snap.join("tokenizer.json"), fp16))
        }
        EmbedModelKind::MiniLm => {
            // fastembed AllMiniLML6V2 caches under Qdrant/all-MiniLM-L6-v2-onnx.
            let snap = first_snapshot(cache.join("models--Qdrant--all-MiniLM-L6-v2-onnx"))
                .or_else(|_| first_snapshot(cache.join("models--Xenova--all-MiniLM-L6-v2")))?;
            let onnx = {
                let optimized = snap.join("model_optimized.onnx");
                let nested = snap.join("onnx/model.onnx");
                if optimized.exists() {
                    optimized
                } else if nested.exists() {
                    nested
                } else {
                    snap.join("model.onnx")
                }
            };
            let tok = {
                let t = snap.join("tokenizer.json");
                if t.exists() {
                    t
                } else {
                    // Fall back to BGE tokenizer only if vocab-compatible — prefer local.
                    first_snapshot(cache.join("models--Xenova--bge-small-en-v1.5"))?
                        .join("tokenizer.json")
                }
            };
            Ok((tok, onnx))
        }
    }
}

fn first_snapshot(repo_dir: PathBuf) -> Result<PathBuf, Box<dyn std::error::Error>> {
    if !repo_dir.exists() {
        return Err(format!(
            "fastembed model not found at {} (run `leankg embed --init` or set LEANKG_EMBED_MODEL)",
            repo_dir.display()
        )
        .into());
    }
    let snapshots = repo_dir.join("snapshots");
    let entry = std::fs::read_dir(&snapshots)?
        .filter_map(|e| e.ok())
        .find(|e| e.path().is_dir())
        .ok_or_else(|| format!("no snapshots in {}", snapshots.display()))?;
    Ok(entry.path())
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

    #[test]
    fn embed_model_kind_from_env_defaults_bge() {
        std::env::remove_var("LEANKG_EMBED_MODEL");
        assert_eq!(EmbedModelKind::from_env(), EmbedModelKind::BgeFp32);
    }

    #[test]
    fn max_seq_len_is_bge_limit() {
        assert_eq!(MAX_SEQ_LEN, 512);
    }
}
