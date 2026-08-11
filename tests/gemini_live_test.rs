//! Live integration test against Google's Gemini OpenAI-compatible embeddings
//! endpoint (free tier).
//!
//! This test makes a REAL network call to:
//!   https://generativelanguage.googleapis.com/v1beta/openai/embeddings
//! It is gated on the `GOOGLE_EMBEDING` env var (set in the user's ~/.zshrc);
//! when the var is absent the test skips, so normal CI / `cargo test` runs are
//! unaffected.
//!
//! Run:
//! ```bash
//! GOOGLE_EMBEDING="$(grep GOOGLE_EMBEDING ~/.zshrc | sed 's/^[^=]*=//')" \
//!   cargo test --features embeddings --test gemini_live_test -- --nocapture
//! ```

use leankg::embeddings::provider::{embed_query, EmbedProvider};
use leankg::embeddings::registry::{create_provider_for_active_model, resolve_active_model};
use std::sync::{Mutex, OnceLock};

const GEMINI_MODEL_ID: &str = "gemini-embedding-2-3072";
const GEMINI_BASE_URL: &str = "https://generativelanguage.googleapis.com/v1beta/openai";
const GEMINI_API_MODEL: &str = "gemini-embedding-2";
const GEMINI_DIM: usize = 3072;

fn env_lock() -> std::sync::MutexGuard<'static, ()> {
    static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
    LOCK.get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap_or_else(|e| e.into_inner())
}

fn gemini_key() -> Option<String> {
    std::env::var("GOOGLE_EMBEDING")
        .ok()
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
}

/// Real call to Google's OpenAI-compat endpoint through the registry + env
/// factory (the exact code path a user exercises). Verifies:
/// - the URL is `<base>/embeddings` (no `/v1` segment),
/// - Google returns the default 3072-d vector (matches the registry entry),
/// - the provider parses it into the active model's collection dim.
#[test]
fn live_gemini_openai_compat_embeds_3072() {
    let _g = env_lock();
    let Some(key) = gemini_key() else {
        eprintln!("skipping: GOOGLE_EMBEDING not set (no live Google call)");
        return;
    };

    std::env::set_var("LEANKG_EMBED_PROVIDER", "openai");
    std::env::set_var("LEANKG_EMBED_ACTIVE_MODEL", GEMINI_MODEL_ID);
    std::env::set_var("LEANKG_EMBED_API_BASE_URL", GEMINI_BASE_URL);
    std::env::set_var("LEANKG_EMBED_API_KEY", &key);
    std::env::set_var("LEANKG_EMBED_API_MODEL", GEMINI_API_MODEL);
    // Registry dim is the default; clear any explicit override so validation
    // runs against 3072 (Google's default output dimensionality).
    std::env::remove_var("LEANKG_EMBED_API_DIM");

    let entry = resolve_active_model().expect("gemini entry resolves");
    assert_eq!(entry.dimensions, GEMINI_DIM);

    let provider = create_provider_for_active_model().expect("provider from env");
    assert_eq!(provider.dimensions(), GEMINI_DIM);

    let v = embed_query(provider.as_ref(), "LeanKG semantic query roundtrip")
        .expect("live embed from Google");
    assert_eq!(v.len(), GEMINI_DIM, "Google default output dim is 3072");
    // Embeddings are normalized by the API; magnitude ~ 1.0.
    let norm: f64 = v
        .iter()
        .map(|x| (*x as f64) * (*x as f64))
        .sum::<f64>()
        .sqrt();
    assert!(
        (norm - 1.0).abs() < 0.05,
        "expected unit-norm embedding, got magnitude {norm:.4}"
    );

    std::env::remove_var("LEANKG_EMBED_PROVIDER");
    std::env::remove_var("LEANKG_EMBED_ACTIVE_MODEL");
    std::env::remove_var("LEANKG_EMBED_API_BASE_URL");
    std::env::remove_var("LEANKG_EMBED_API_KEY");
    std::env::remove_var("LEANKG_EMBED_API_MODEL");
}
