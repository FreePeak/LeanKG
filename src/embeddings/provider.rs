//! Pluggable embedding providers (local ONNX + OpenAI-compatible HTTP).
//!
//! Available without the `embeddings` cargo feature so MCP can call an API
//! embedder without linking ONNX / fastembed.

use std::sync::Arc;
use thiserror::Error;

/// Must match `VEC_DIM` / `EMBEDDING_DIM` (pgvector column width).
pub const VEC_DIM: usize = 384;

#[derive(Debug, Error)]
pub enum EmbedError {
    #[error("embedding provider '{name}' reports dimensions={got}, expected {expected}")]
    DimensionMismatch {
        name: String,
        got: usize,
        expected: usize,
    },
    #[error("embedding response dimension mismatch: got {got}, expected {expected}")]
    ResponseDimensionMismatch { got: usize, expected: usize },
    #[error("embedding provider config: {0}")]
    Config(String),
    #[error("embedding HTTP error: {0}")]
    Http(String),
    #[error("embedding provider error: {0}")]
    Other(String),
}

/// Abstraction over local ONNX and remote OpenAI-compatible embed APIs.
pub trait EmbedProvider: Send + Sync {
    fn name(&self) -> &str;
    fn dimensions(&self) -> usize;
    fn embed_batch(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, EmbedError>;
}

/// Reject providers whose advertised dimension ≠ `expected_dim` (normally [`VEC_DIM`]).
pub fn validate_provider(
    provider: &dyn EmbedProvider,
    expected_dim: usize,
) -> Result<(), EmbedError> {
    let got = provider.dimensions();
    if got != expected_dim {
        return Err(EmbedError::DimensionMismatch {
            name: provider.name().to_string(),
            got,
            expected: expected_dim,
        });
    }
    Ok(())
}

/// Deterministic in-memory provider for tests and injection seams.
#[derive(Debug, Clone)]
pub struct FakeEmbedProvider {
    name: String,
    dim: usize,
}

impl FakeEmbedProvider {
    pub fn new(dim: usize) -> Self {
        Self {
            name: "fake".to_string(),
            dim,
        }
    }

    pub fn with_name(name: impl Into<String>, dim: usize) -> Self {
        Self {
            name: name.into(),
            dim,
        }
    }
}

impl EmbedProvider for FakeEmbedProvider {
    fn name(&self) -> &str {
        &self.name
    }

    fn dimensions(&self) -> usize {
        self.dim
    }

    fn embed_batch(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, EmbedError> {
        Ok(texts
            .iter()
            .enumerate()
            .map(|(i, _)| {
                let mut v = vec![0.0_f32; self.dim];
                if self.dim > 0 {
                    v[0] = (i as f32 + 1.0) / (self.dim as f32);
                }
                v
            })
            .collect())
    }
}

/// OpenAI-compatible `/v1/embeddings` HTTP client (blocking).
#[derive(Debug, Clone)]
pub struct OpenAiCompatibleProvider {
    base_url: String,
    api_key: String,
    model: String,
    dimensions: usize,
    http: reqwest::blocking::Client,
}

impl OpenAiCompatibleProvider {
    pub fn new(
        base_url: impl Into<String>,
        api_key: impl Into<String>,
        model: impl Into<String>,
        dimensions: usize,
    ) -> Result<Self, EmbedError> {
        let http = reqwest::blocking::Client::builder()
            .timeout(std::time::Duration::from_secs(60))
            .build()
            .map_err(|e| EmbedError::Http(e.to_string()))?;
        Ok(Self {
            base_url: base_url.into().trim_end_matches('/').to_string(),
            api_key: api_key.into(),
            model: model.into(),
            dimensions,
            http,
        })
    }
}

impl EmbedProvider for OpenAiCompatibleProvider {
    fn name(&self) -> &str {
        "openai"
    }

    fn dimensions(&self) -> usize {
        self.dimensions
    }

    fn embed_batch(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, EmbedError> {
        if texts.is_empty() {
            return Ok(Vec::new());
        }
        let url = format!("{}/v1/embeddings", self.base_url);
        let body = serde_json::json!({
            "input": texts,
            "model": self.model,
        });
        let resp = self
            .http
            .post(&url)
            .bearer_auth(&self.api_key)
            .json(&body)
            .send()
            .map_err(|e| EmbedError::Http(e.to_string()))?;
        if !resp.status().is_success() {
            let status = resp.status();
            let text = resp.text().unwrap_or_default();
            return Err(EmbedError::Http(format!("status {status}: {text}")));
        }
        let parsed: OpenAiEmbeddingsResponse = resp
            .json()
            .map_err(|e| EmbedError::Http(format!("parse response: {e}")))?;
        let mut data = parsed.data;
        data.sort_by_key(|d| d.index);
        let mut out = Vec::with_capacity(data.len());
        for item in data {
            if item.embedding.len() != self.dimensions {
                return Err(EmbedError::ResponseDimensionMismatch {
                    got: item.embedding.len(),
                    expected: self.dimensions,
                });
            }
            out.push(item.embedding);
        }
        if out.len() != texts.len() {
            return Err(EmbedError::Other(format!(
                "expected {} embeddings, got {}",
                texts.len(),
                out.len()
            )));
        }
        Ok(out)
    }
}

#[derive(Debug, serde::Deserialize)]
struct OpenAiEmbeddingsResponse {
    data: Vec<OpenAiEmbeddingData>,
}

#[derive(Debug, serde::Deserialize)]
struct OpenAiEmbeddingData {
    embedding: Vec<f32>,
    #[serde(default)]
    index: usize,
}

/// Local ONNX / DirectEmbedder wrapper. Only constructed when the
/// `embeddings` feature is enabled.
#[cfg(feature = "embeddings")]
pub struct LocalOnnxProvider {
    backend: LocalBackend,
}

#[cfg(feature = "embeddings")]
enum LocalBackend {
    Direct(super::models::DirectEmbedder),
    Fast(super::models::Embedder),
}

#[cfg(feature = "embeddings")]
impl LocalOnnxProvider {
    /// Prefer DirectEmbedder; fall back to fastembed Embedder when
    /// `LEANKG_EMBED_DIRECT=0`.
    pub fn new() -> Result<Self, EmbedError> {
        let use_direct = std::env::var("LEANKG_EMBED_DIRECT")
            .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
            .unwrap_or(true);
        if use_direct {
            let intra = std::env::var("LEANKG_EMBED_DIRECT_INTRA")
                .ok()
                .and_then(|v| v.parse::<usize>().ok())
                .filter(|n| (1..=128).contains(n))
                .unwrap_or(1);
            match super::models::DirectEmbedder::with_intra_threads(intra) {
                Ok(e) => {
                    return Ok(Self {
                        backend: LocalBackend::Direct(e),
                    });
                }
                Err(e) => {
                    return Err(EmbedError::Other(format!(
                        "DirectEmbedder init failed ({e}); run `leankg embed --init` \
                         or set LEANKG_EMBED_DIRECT=0"
                    )));
                }
            }
        }
        let fast = super::models::Embedder::new().map_err(|e| EmbedError::Other(e.to_string()))?;
        Ok(Self {
            backend: LocalBackend::Fast(fast),
        })
    }
}

#[cfg(feature = "embeddings")]
impl EmbedProvider for LocalOnnxProvider {
    fn name(&self) -> &str {
        "local"
    }

    fn dimensions(&self) -> usize {
        VEC_DIM
    }

    fn embed_batch(&self, texts: &[String]) -> Result<Vec<Vec<f32>>, EmbedError> {
        match &self.backend {
            LocalBackend::Direct(e) => e
                .embed(texts)
                .map_err(|err| EmbedError::Other(err.to_string())),
            LocalBackend::Fast(e) => e
                .embed(texts)
                .map_err(|err| EmbedError::Other(err.to_string())),
        }
    }
}

/// Read `LEANKG_EMBED_PROVIDER` (`local` | `openai`). Default: `local`.
pub fn provider_kind_from_env() -> Result<ProviderKind, EmbedError> {
    match std::env::var("LEANKG_EMBED_PROVIDER") {
        Err(_) => Ok(ProviderKind::Local),
        Ok(v) => match v.trim().to_ascii_lowercase().as_str() {
            "" | "local" | "onnx" => Ok(ProviderKind::Local),
            "openai" | "api" | "openai-compatible" => Ok(ProviderKind::OpenAi),
            other => Err(EmbedError::Config(format!(
                "unknown LEANKG_EMBED_PROVIDER={other:?}; expected local|openai"
            ))),
        },
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ProviderKind {
    Local,
    OpenAi,
}

/// Build a provider from env.
///
/// Env:
/// - `LEANKG_EMBED_PROVIDER` — `local` (default) or `openai`
/// - `LEANKG_EMBED_API_BASE_URL` — required for openai (e.g. `http://127.0.0.1:8080`)
/// - `LEANKG_EMBED_API_KEY` — required for openai
/// - `LEANKG_EMBED_API_MODEL` — optional (default `bge-small-en-v1.5`)
/// - `LEANKG_EMBED_API_DIM` — optional (default [`VEC_DIM`])
pub fn create_provider_from_env() -> Result<Arc<dyn EmbedProvider>, EmbedError> {
    match provider_kind_from_env()? {
        ProviderKind::Local => create_local_provider(),
        ProviderKind::OpenAi => {
            let provider = openai_provider_from_env()?;
            validate_provider(provider.as_ref(), VEC_DIM)?;
            Ok(provider)
        }
    }
}

fn create_local_provider() -> Result<Arc<dyn EmbedProvider>, EmbedError> {
    #[cfg(feature = "embeddings")]
    {
        let p = LocalOnnxProvider::new()?;
        validate_provider(&p, VEC_DIM)?;
        Ok(Arc::new(p))
    }
    #[cfg(not(feature = "embeddings"))]
    {
        Err(EmbedError::Config(
            "LEANKG_EMBED_PROVIDER=local requires the `embeddings` cargo feature \
             (or set LEANKG_EMBED_PROVIDER=openai with LEANKG_EMBED_API_*)"
                .into(),
        ))
    }
}

fn openai_provider_from_env() -> Result<Arc<dyn EmbedProvider>, EmbedError> {
    let base_url = std::env::var("LEANKG_EMBED_API_BASE_URL").map_err(|_| {
        EmbedError::Config(
            "LEANKG_EMBED_API_BASE_URL is required when LEANKG_EMBED_PROVIDER=openai".into(),
        )
    })?;
    let api_key = std::env::var("LEANKG_EMBED_API_KEY").map_err(|_| {
        EmbedError::Config(
            "LEANKG_EMBED_API_KEY is required when LEANKG_EMBED_PROVIDER=openai".into(),
        )
    })?;
    if api_key.trim().is_empty() {
        return Err(EmbedError::Config(
            "LEANKG_EMBED_API_KEY is required when LEANKG_EMBED_PROVIDER=openai".into(),
        ));
    }
    let model =
        std::env::var("LEANKG_EMBED_API_MODEL").unwrap_or_else(|_| "bge-small-en-v1.5".to_string());
    let dimensions = std::env::var("LEANKG_EMBED_API_DIM")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(VEC_DIM);
    let p = OpenAiCompatibleProvider::new(base_url, api_key, model, dimensions)?;
    Ok(Arc::new(p))
}

/// Embed a single query string via the given provider (query-time path helper).
pub fn embed_query(provider: &dyn EmbedProvider, query: &str) -> Result<Vec<f32>, EmbedError> {
    let batch = provider.embed_batch(&[query.to_string()])?;
    batch
        .into_iter()
        .next()
        .ok_or_else(|| EmbedError::Other("empty embed_batch result for query".into()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::sync::{Mutex, OnceLock};

    /// Serialize env-mutating factory tests across threads.
    fn env_lock() -> std::sync::MutexGuard<'static, ()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(())).lock().unwrap()
    }

    #[test]
    fn fake_provider_rejects_wrong_dimensions_at_validate() {
        let fake = FakeEmbedProvider::new(768);
        let err = validate_provider(&fake, VEC_DIM).expect_err("768-dim must fail");
        match err {
            EmbedError::DimensionMismatch { got, expected, .. } => {
                assert_eq!(got, 768);
                assert_eq!(expected, VEC_DIM);
            }
            other => panic!("unexpected error: {other}"),
        }
    }

    #[test]
    fn fake_provider_embed_batch_returns_384() {
        let fake = FakeEmbedProvider::new(VEC_DIM);
        validate_provider(&fake, VEC_DIM).expect("384-dim ok");
        let texts = vec!["hello".into(), "world".into()];
        let vecs = fake.embed_batch(&texts).expect("embed");
        assert_eq!(vecs.len(), 2);
        assert_eq!(vecs[0].len(), VEC_DIM);
        assert_eq!(vecs[1].len(), VEC_DIM);
    }

    #[test]
    fn openai_compatible_provider_posts_and_parses_vectors() {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
        let addr = listener.local_addr().unwrap();
        let handle = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut buf = [0u8; 8192];
            let n = stream.read(&mut buf).unwrap();
            let req = String::from_utf8_lossy(&buf[..n]);
            assert!(
                req.contains("POST /v1/embeddings"),
                "expected embeddings path, got: {req}"
            );
            assert!(req.contains("Bearer test-key"), "missing auth: {req}");
            assert!(req.contains("\"model\""), "missing model: {req}");
            assert!(req.contains("hello"), "missing input text: {req}");

            let embedding: Vec<f32> = (0..VEC_DIM).map(|i| i as f32 * 0.001).collect();
            let body = serde_json::json!({
                "data": [{ "embedding": embedding, "index": 0 }]
            });
            let body_str = body.to_string();
            let resp = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body_str.len(),
                body_str
            );
            stream.write_all(resp.as_bytes()).unwrap();
        });

        let provider = OpenAiCompatibleProvider::new(
            format!("http://{addr}"),
            "test-key",
            "bge-small-en-v1.5",
            VEC_DIM,
        )
        .unwrap();
        validate_provider(&provider, VEC_DIM).unwrap();
        let vecs = provider
            .embed_batch(&[String::from("hello")])
            .expect("embed_batch");
        assert_eq!(vecs.len(), 1);
        assert_eq!(vecs[0].len(), VEC_DIM);
        assert!((vecs[0][1] - 0.001).abs() < 1e-6);
        handle.join().unwrap();
    }

    #[test]
    fn openai_compatible_provider_errors_on_dim_mismatch_in_response() {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
        let addr = listener.local_addr().unwrap();
        let handle = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut buf = [0u8; 8192];
            let _ = stream.read(&mut buf).unwrap();
            let body = serde_json::json!({
                "data": [{ "embedding": [0.1, 0.2, 0.3], "index": 0 }]
            });
            let body_str = body.to_string();
            let resp = format!(
                "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
                body_str.len(),
                body_str
            );
            stream.write_all(resp.as_bytes()).unwrap();
        });

        let provider =
            OpenAiCompatibleProvider::new(format!("http://{addr}"), "k", "m", VEC_DIM).unwrap();
        let err = provider
            .embed_batch(&[String::from("x")])
            .expect_err("3-float response must fail");
        match err {
            EmbedError::ResponseDimensionMismatch { got, expected } => {
                assert_eq!(got, 3);
                assert_eq!(expected, VEC_DIM);
            }
            other => panic!("unexpected: {other}"),
        }
        handle.join().unwrap();
    }

    #[test]
    fn factory_defaults_to_local_when_unset() {
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_PROVIDER");
        assert_eq!(provider_kind_from_env().unwrap(), ProviderKind::Local);
    }

    #[test]
    fn factory_selects_openai_from_env() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
        std::env::set_var("LEANKG_EMBED_API_BASE_URL", "http://127.0.0.1:9");
        std::env::set_var("LEANKG_EMBED_API_KEY", "sk-test");
        std::env::set_var("LEANKG_EMBED_API_MODEL", "bge-small-en-v1.5");
        let result = create_provider_from_env();
        std::env::remove_var("LEANKG_EMBED_PROVIDER");
        std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
        std::env::remove_var("LEANKG_EMBED_API_KEY");
        std::env::remove_var("LEANKG_EMBED_API_MODEL");
        let p = result.expect("openai factory");
        assert_eq!(p.name(), "openai");
        assert_eq!(p.dimensions(), VEC_DIM);
    }

    #[test]
    fn factory_fails_when_openai_missing_api_key() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
        std::env::set_var("LEANKG_EMBED_API_BASE_URL", "http://127.0.0.1:9");
        std::env::remove_var("LEANKG_EMBED_API_KEY");
        let err = match create_provider_from_env() {
            Ok(_) => panic!("expected missing key error"),
            Err(e) => e,
        };
        std::env::remove_var("LEANKG_EMBED_PROVIDER");
        std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
        let msg = err.to_string();
        assert!(
            msg.contains("LEANKG_EMBED_API_KEY"),
            "expected key error, got: {msg}"
        );
    }

    #[test]
    fn query_embed_uses_provider_from_factory() {
        let fake = FakeEmbedProvider::new(VEC_DIM);
        let v = embed_query(&fake, "semantic query").expect("query embed");
        assert_eq!(v.len(), VEC_DIM);
        assert!(v[0] > 0.0);
    }

    #[test]
    fn bulk_embed_uses_provider_from_factory() {
        let fake = FakeEmbedProvider::new(VEC_DIM);
        let texts: Vec<String> = (0..3).map(|i| format!("blob-{i}")).collect();
        let vecs = fake.embed_batch(&texts).expect("bulk");
        assert_eq!(vecs.len(), 3);
        for v in &vecs {
            assert_eq!(v.len(), VEC_DIM);
        }
        // Distinct fake fingerprints per row (batch path preserved order).
        assert_ne!(vecs[0][0], vecs[1][0]);
    }
}
