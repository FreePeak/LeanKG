//! Multi-model embedding registry: model_id → provider, dimensions, collection names.
//!
//! Active model resolves from `LEANKG_EMBED_ACTIVE_MODEL` (default BGE 384-d).
//! Each model gets its own vector/state collection so ANN never mixes dims.

#[allow(unused_imports)]
use super::provider::{
    create_provider_from_env_with_dim, EmbedError, EmbedProvider, FakeEmbedProvider,
    OpenAiCompatibleProvider, ProviderKind,
};
use std::collections::HashMap;
use std::sync::Arc;

/// Canonical id for the default local BGE-small-en-v1.5 (384-d) collection.
pub const DEFAULT_BGE_MODEL_ID: &str = "bge-small-en-v1.5-384";

/// Legacy single-table names kept for the default BGE model (backward compat).
pub const LEGACY_VECTORS_RELATION: &str = "embedding_vectors";
pub const LEGACY_STATE_RELATION: &str = "embedding_state";

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RegistryProviderKind {
    Local,
    OpenAi,
}

impl RegistryProviderKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Local => "local",
            Self::OpenAi => "openai",
        }
    }
}

/// One row in the logical `embedding_models` registry.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct EmbeddingModelEntry {
    pub model_id: String,
    pub provider: RegistryProviderKind,
    /// Provider-facing model name (ONNX variant or API model id).
    pub model_name: String,
    pub dimensions: usize,
    pub distance: String,
}

impl EmbeddingModelEntry {
    pub fn vectors_relation(&self) -> String {
        vectors_relation_for_model_id(&self.model_id)
    }

    pub fn state_relation(&self) -> String {
        state_relation_for_model_id(&self.model_id)
    }

    pub fn hnsw_index_relation(&self) -> String {
        format!("{}:vec_idx", self.vectors_relation())
    }
}

/// Built-in registry entries (local BGE + OpenAI-compatible API path).
pub fn builtin_registry() -> HashMap<String, EmbeddingModelEntry> {
    let mut m = HashMap::new();
    m.insert(
        DEFAULT_BGE_MODEL_ID.to_string(),
        EmbeddingModelEntry {
            model_id: DEFAULT_BGE_MODEL_ID.to_string(),
            provider: RegistryProviderKind::Local,
            model_name: "bge-small-en-v1.5".to_string(),
            dimensions: 384,
            distance: "cosine".to_string(),
        },
    );
    m.insert(
        "qwen3-emb-4b-2560".to_string(),
        EmbeddingModelEntry {
            model_id: "qwen3-emb-4b-2560".to_string(),
            provider: RegistryProviderKind::OpenAi,
            model_name: "Qwen/Qwen3-Embedding-4B".to_string(),
            dimensions: 2560,
            distance: "cosine".to_string(),
        },
    );
    m.insert(
        "jina-embeddings-v3-1024".to_string(),
        EmbeddingModelEntry {
            model_id: "jina-embeddings-v3-1024".to_string(),
            provider: RegistryProviderKind::OpenAi,
            model_name: "jina-embeddings-v3".to_string(),
            dimensions: 1024,
            distance: "cosine".to_string(),
        },
    );
    m
}

/// Sanitize model_id for table suffix (`-` → `_`).
pub fn sanitize_model_id_for_table(model_id: &str) -> String {
    model_id.replace('-', "_")
}

/// Vector collection relation/table for a model. Default BGE keeps legacy name.
pub fn vectors_relation_for_model_id(model_id: &str) -> String {
    if model_id == DEFAULT_BGE_MODEL_ID {
        LEGACY_VECTORS_RELATION.to_string()
    } else {
        format!(
            "embedding_vectors_{}",
            sanitize_model_id_for_table(model_id)
        )
    }
}

/// State collection relation/table for a model. Default BGE keeps legacy name.
pub fn state_relation_for_model_id(model_id: &str) -> String {
    if model_id == DEFAULT_BGE_MODEL_ID {
        LEGACY_STATE_RELATION.to_string()
    } else {
        format!("embedding_state_{}", sanitize_model_id_for_table(model_id))
    }
}

/// Read `LEANKG_EMBED_ACTIVE_MODEL`; default [`DEFAULT_BGE_MODEL_ID`].
pub fn active_model_id_from_env() -> String {
    std::env::var("LEANKG_EMBED_ACTIVE_MODEL")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| DEFAULT_BGE_MODEL_ID.to_string())
}

/// Resolve active model entry from env + built-in registry.
pub fn resolve_active_model() -> Result<EmbeddingModelEntry, EmbedError> {
    let id = active_model_id_from_env();
    lookup_model(&id).ok_or_else(|| {
        EmbedError::Config(format!(
            "unknown LEANKG_EMBED_ACTIVE_MODEL={id:?}; register the model or use a built-in id \
             (e.g. {DEFAULT_BGE_MODEL_ID})"
        ))
    })
}

pub fn lookup_model(model_id: &str) -> Option<EmbeddingModelEntry> {
    builtin_registry().get(model_id).cloned()
}

/// Provider kind override from env when set; otherwise from registry entry.
pub fn effective_provider_kind(entry: &EmbeddingModelEntry) -> Result<ProviderKind, EmbedError> {
    match std::env::var("LEANKG_EMBED_PROVIDER") {
        Ok(v) => match v.trim().to_ascii_lowercase().as_str() {
            "" | "local" | "onnx" => Ok(ProviderKind::Local),
            "openai" | "api" | "openai-compatible" => Ok(ProviderKind::OpenAi),
            other => Err(EmbedError::Config(format!(
                "unknown LEANKG_EMBED_PROVIDER={other:?}; expected local|openai"
            ))),
        },
        Err(_) => Ok(match entry.provider {
            RegistryProviderKind::Local => ProviderKind::Local,
            RegistryProviderKind::OpenAi => ProviderKind::OpenAi,
        }),
    }
}

/// Build an embed provider for the active registry entry (dim from registry).
pub fn create_provider_for_active_model() -> Result<Arc<dyn EmbedProvider>, EmbedError> {
    let entry = resolve_active_model()?;
    create_provider_for_entry(&entry)
}

/// Build provider for a specific registry entry; validates dim against registry.
pub fn create_provider_for_entry(
    entry: &EmbeddingModelEntry,
) -> Result<Arc<dyn EmbedProvider>, EmbedError> {
    let kind = effective_provider_kind(entry)?;
    create_provider_from_env_with_dim(kind, entry.dimensions)
}

/// SQL/backfill helper: legacy undecorated tables belong to the default BGE model.
pub fn backfill_legacy_model_id() -> &'static str {
    DEFAULT_BGE_MODEL_ID
}

/// Migration note for operators: copy/rename legacy rows under this model_id.
pub fn legacy_backfill_target_relation(model_id: &str) -> Option<&'static str> {
    if model_id == DEFAULT_BGE_MODEL_ID {
        Some(LEGACY_VECTORS_RELATION)
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Mutex, OnceLock};

    fn env_lock() -> std::sync::MutexGuard<'static, ()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(())).lock().unwrap()
    }

    #[test]
    fn default_active_model_is_bge_384() {
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        assert_eq!(active_model_id_from_env(), DEFAULT_BGE_MODEL_ID);
        let entry = resolve_active_model().expect("default must resolve");
        assert_eq!(entry.dimensions, 384);
        assert_eq!(entry.provider, RegistryProviderKind::Local);
    }

    #[test]
    fn active_model_from_env_selects_qwen_entry() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", "qwen3-emb-4b-2560");
        let entry = resolve_active_model().expect("qwen must resolve");
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        assert_eq!(entry.dimensions, 2560);
        assert_eq!(entry.provider, RegistryProviderKind::OpenAi);
    }

    #[test]
    fn unknown_active_model_errors() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", "no-such-model");
        let err = resolve_active_model().expect_err("unknown model");
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        assert!(err.to_string().contains("no-such-model"));
    }

    #[test]
    fn bge_uses_legacy_relation_names() {
        let entry = lookup_model(DEFAULT_BGE_MODEL_ID).unwrap();
        assert_eq!(entry.vectors_relation(), LEGACY_VECTORS_RELATION);
        assert_eq!(entry.state_relation(), LEGACY_STATE_RELATION);
        assert_eq!(entry.hnsw_index_relation(), "embedding_vectors:vec_idx");
    }

    #[test]
    fn qwen_uses_table_per_model_names() {
        let entry = lookup_model("qwen3-emb-4b-2560").unwrap();
        assert_eq!(
            entry.vectors_relation(),
            "embedding_vectors_qwen3_emb_4b_2560"
        );
        assert_eq!(entry.state_relation(), "embedding_state_qwen3_emb_4b_2560");
    }

    #[test]
    fn sanitize_replaces_hyphens_only() {
        assert_eq!(
            sanitize_model_id_for_table("bge-small-en-v1.5-384"),
            "bge_small_en_v1.5_384"
        );
    }

    #[test]
    fn openai_provider_accepts_registry_dim_not_hardcoded_384() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
        std::env::set_var("LEANKG_EMBED_API_BASE_URL", "http://127.0.0.1:9");
        std::env::set_var("LEANKG_EMBED_API_KEY", "sk-test");
        std::env::set_var("LEANKG_EMBED_API_MODEL", "Qwen/Qwen3-Embedding-4B");
        std::env::remove_var("LEANKG_EMBED_API_DIM");
        let entry = lookup_model("qwen3-emb-4b-2560").unwrap();
        let p = create_provider_for_entry(&entry).expect("2560-d openai ok");
        std::env::remove_var("LEANKG_EMBED_PROVIDER");
        std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
        std::env::remove_var("LEANKG_EMBED_API_KEY");
        std::env::remove_var("LEANKG_EMBED_API_MODEL");
        assert_eq!(p.dimensions(), 2560);
    }

    #[test]
    fn fake_provider_validates_against_registry_dim() {
        let entry = lookup_model("jina-embeddings-v3-1024").unwrap();
        let fake = FakeEmbedProvider::with_name("jina", entry.dimensions);
        assert_eq!(fake.dimensions(), 1024);
    }

    #[test]
    fn backfill_legacy_maps_to_default_bge() {
        assert_eq!(backfill_legacy_model_id(), DEFAULT_BGE_MODEL_ID);
        assert_eq!(
            legacy_backfill_target_relation(DEFAULT_BGE_MODEL_ID),
            Some(LEGACY_VECTORS_RELATION)
        );
        assert!(legacy_backfill_target_relation("qwen3-emb-4b-2560").is_none());
    }
}
