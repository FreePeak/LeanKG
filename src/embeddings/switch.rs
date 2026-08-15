//! Runtime-active embedding model: overrides `LEANKG_EMBED_ACTIVE_MODEL` for
//! the life of the process. Used by the `set_embed_model` MCP tool so a user
//! choice becomes the default for every embedding tool without restarting.
//!
//! The runtime selection (process env `LEANKG_EMBED_ACTIVE_MODEL`) is the
//! source of truth — a fresh process boots with whatever env/`persist.json`
//! gives it, then a tool call can override it in-process. No atomics needed:
//! [`std::env::set_var`] on Rust 2021 only races when called concurrently
//! with itself, and MCP tool handlers run single-threaded.

use super::registry::{
    active_model_id_from_env, builtin_registry, lookup_model, EmbeddingModelEntry,
};

/// Path (relative to project root) where `persist=true` writes the choice so
/// the next boot restores it as the default.
pub const PERSIST_PATH: &str = ".leankg/embed-model.json";

/// Active model id, runtime override first.
pub fn active_model_id() -> String {
    std::env::var("LEANKG_EMBED_ACTIVE_MODEL")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(active_model_id_from_env)
}

/// Resolve the active model entry; runtime override wins over env/default.
pub fn resolve_active() -> Result<EmbeddingModelEntry, super::EmbedError> {
    let id = active_model_id();
    lookup_model(&id).ok_or_else(|| {
        super::EmbedError::Config(format!(
            "unknown active embed model {id:?}; use set_embed_model to register one"
        ))
    })
}

/// Set the runtime-active model. Only registry ids are accepted — the active
/// model must exist so every embedding tool has a known dimension/collection.
/// Returns the resolved entry. `project_root` is used to persist the choice
/// when `persist` is set; pass `None` to skip persistence.
pub fn set_active_model(
    model_id: &str,
    persist: bool,
    project_root: Option<&std::path::Path>,
) -> Result<EmbeddingModelEntry, super::EmbedError> {
    let entry = lookup_model(model_id).ok_or_else(|| {
        let known: Vec<String> = builtin_registry().keys().cloned().collect();
        super::EmbedError::Config(format!(
            "unknown embed model {model_id:?}; known: {}",
            known.join(", ")
        ))
    })?;
    // SAFETY: single-threaded MCP handlers; same discipline as existing
    // LEANKG_* env toggles elsewhere in this crate.
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", model_id);
    if persist {
        if let Some(root) = project_root {
            let _ = std::fs::create_dir_all(root.join(".leankg"));
            let json = serde_json::json!({ "model_id": model_id }).to_string();
            if std::fs::write(root.join(PERSIST_PATH), json).is_ok() {
                tracing::info!(
                    "set_embed_model: persisted {} to {}",
                    model_id,
                    root.join(PERSIST_PATH).display()
                );
            }
        }
    }
    Ok(entry)
}

/// Load a persisted choice (if any) into the process env so a fresh boot
/// starts with the last user selection. Call once at startup.
pub fn apply_persisted_model(project_root: Option<&std::path::Path>) {
    let Some(root) = project_root else { return };
    let path = root.join(PERSIST_PATH);
    let Ok(raw) = std::fs::read_to_string(&path) else {
        return;
    };
    let Ok(cfg) = serde_json::from_str::<serde_json::Value>(&raw) else {
        return;
    };
    if let Some(id) = cfg["model_id"].as_str() {
        if lookup_model(id).is_some() {
            std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", id);
            tracing::info!(
                "set_embed_model: restored persisted model {id} from {}",
                path.display()
            );
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::embeddings::test_env;

    fn env_lock() -> std::sync::MutexGuard<'static, ()> {
        test_env::lock()
    }

    #[test]
    fn persist_and_restore_roundtrip() {
        let _g = env_lock();
        let dir = std::env::temp_dir().join(format!("leankg-switch-test-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        let entry = set_active_model("qwen3-emb-4b-2560", true, Some(&dir)).expect("set+persist");
        assert_eq!(entry.dimensions, 2560);
        let persisted = dir.join(".leankg/embed-model.json");
        assert!(persisted.exists(), "persist file must be written");
        let raw = std::fs::read_to_string(&persisted).expect("readable");
        assert!(raw.contains("qwen3-emb-4b-2560"));

        // Fresh-boot simulation: runtime override is gone; restore must pick
        // the persisted choice back up from the project root.
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        apply_persisted_model(Some(&dir));
        assert_eq!(active_model_id(), "qwen3-emb-4b-2560");

        // Unknown model ids in the file must not be restored.
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        std::fs::write(&persisted, r#"{"model_id":"no-such-model"}"#).expect("write");
        apply_persisted_model(Some(&dir));
        assert_eq!(
            active_model_id(),
            crate::embeddings::registry::DEFAULT_BGE_MODEL_ID
        );

        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        let _ = std::fs::remove_dir_all(&dir);
    }

    #[test]
    fn runtime_override_beats_env() {
        let _g = env_lock();
        std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", "qwen3-emb-4b-2560");
        assert_eq!(active_model_id(), "qwen3-emb-4b-2560");
        let entry = resolve_active().expect("qwen must resolve");
        assert_eq!(entry.dimensions, 2560);
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    }

    #[test]
    fn defaults_to_bge_when_unset() {
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        assert_eq!(
            active_model_id(),
            crate::embeddings::registry::DEFAULT_BGE_MODEL_ID
        );
    }

    #[test]
    fn set_unknown_model_errors() {
        let _g = env_lock();
        let err = set_active_model("nope", false, None).expect_err("unknown");
        assert!(err.to_string().contains("unknown embed model"));
        assert!(err.to_string().contains("known:"));
    }

    #[test]
    fn set_model_then_resolve() {
        let _g = env_lock();
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
        let entry = set_active_model("jina-embeddings-v3-1024", false, None).expect("set");
        assert_eq!(entry.dimensions, 1024);
        assert_eq!(active_model_id(), "jina-embeddings-v3-1024");
        std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    }
}
