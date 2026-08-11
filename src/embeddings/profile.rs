//! Big-context embedding profiles (FR-EMBED-PROFILE).
//!
//! `LEANKG_EMBED_PROFILE=small|8k|32k` selects the *default* text budgets fed
//! to the embedder — blob length, summary-TOC length, and TOC item caps.
//! Individual env overrides (`LEANKG_EMBED_MAX_BLOB_CHARS`,
//! `LEANKG_EMBED_SUMMARY_CHARS`) still win; the profile only changes the
//! defaults, so existing behavior is bit-for-bit preserved when unset.
//!
//! - `small` (default): historical budgets for the local BGE-small-en-v1.5
//!   ONNX model (512-token hard cap, 400-char summary TOCs).
//! - `8k`: for 8k-context models (bge-m3, Qwen3-Embedding) served via
//!   `LEANKG_EMBED_PROVIDER=openai`. 16k-char blobs ≈ ~4k tokens of code —
//!   comfortably inside an 8192-token window.
//! - `32k`: for 32k-context models (Qwen3-Embedding-8B). 64k-char blobs ≈
//!   ~16k tokens of code, leaving query/rerank headroom inside the window.
//!
//! Profile budgets pair with the runtime-configurable vector width
//! ([`crate::embeddings::provider::vec_dim`]): a big-context model only pays
//! off when the pgvector column width matches its embedding size.

/// Default blob char limits per profile (before `max_blob_chars` env/fast
/// handling).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EmbedProfile {
    Small,
    Ctx8k,
    Ctx32k,
}

/// Read `LEANKG_EMBED_PROFILE` (`small` | `8k` | `32k`). Unknown or unset
/// values fall back to [`EmbedProfile::Small`] — full backward compat.
pub fn active_profile() -> EmbedProfile {
    match std::env::var("LEANKG_EMBED_PROFILE")
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase()
        .as_str()
    {
        "8k" | "8000" | "8192" => EmbedProfile::Ctx8k,
        "32k" | "32000" | "32768" => EmbedProfile::Ctx32k,
        _ => EmbedProfile::Small,
    }
}

impl EmbedProfile {
    pub fn label(self) -> &'static str {
        match self {
            Self::Small => "small",
            Self::Ctx8k => "8k",
            Self::Ctx32k => "32k",
        }
    }

    /// Default `max_blob_chars` for the profile (explicit
    /// `LEANKG_EMBED_MAX_BLOB_CHARS` and the small-profile fast path still
    /// take precedence in `text_blob::max_blob_chars`).
    pub fn blob_chars(self) -> usize {
        match self {
            Self::Small => 1500,
            Self::Ctx8k => 16_000,
            Self::Ctx32k => 64_000,
        }
    }

    /// Default summary-TOC char cap (`file_summary_max_chars`).
    pub fn summary_chars(self) -> usize {
        match self {
            Self::Small => 400,
            Self::Ctx8k => 4_000,
            Self::Ctx32k => 16_000,
        }
    }

    /// TOC item caps: (exported types, function signatures, module member
    /// files). Larger budgets let denser TOCs keep more signal instead of
    /// truncating at 24 items.
    pub fn toc_item_caps(self) -> (usize, usize, usize) {
        match self {
            Self::Small => (24, 24, 16),
            Self::Ctx8k => (48, 48, 32),
            Self::Ctx32k => (96, 96, 64),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    /// Env vars are process-global; serialize the readers.
    static ENV_LOCK: Mutex<()> = Mutex::new(());

    #[test]
    fn profile_defaults_to_small_when_unset_or_unknown() {
        let _g = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        std::env::remove_var("LEANKG_EMBED_PROFILE");
        assert_eq!(active_profile(), EmbedProfile::Small);
        std::env::set_var("LEANKG_EMBED_PROFILE", "nonsense");
        assert_eq!(active_profile(), EmbedProfile::Small);
        std::env::remove_var("LEANKG_EMBED_PROFILE");
    }

    #[test]
    fn profile_parses_aliases() {
        let _g = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
        for (v, want) in [
            ("8k", EmbedProfile::Ctx8k),
            ("8192", EmbedProfile::Ctx8k),
            ("32K", EmbedProfile::Ctx32k),
            ("32768", EmbedProfile::Ctx32k),
            ("small", EmbedProfile::Small),
        ] {
            std::env::set_var("LEANKG_EMBED_PROFILE", v);
            assert_eq!(active_profile(), want, "profile {v}");
        }
        std::env::remove_var("LEANKG_EMBED_PROFILE");
    }

    #[test]
    fn budgets_grow_monotonically_with_context() {
        let small = EmbedProfile::Small;
        let c8k = EmbedProfile::Ctx8k;
        let c32k = EmbedProfile::Ctx32k;
        assert!(small.blob_chars() < c8k.blob_chars());
        assert!(c8k.blob_chars() < c32k.blob_chars());
        assert!(small.summary_chars() < c8k.summary_chars());
        assert!(c8k.summary_chars() < c32k.summary_chars());
        let (t, s, f) = EmbedProfile::Small.toc_item_caps();
        assert_eq!((t, s, f), (24, 24, 16)); // exact historical defaults
        assert!(c8k.toc_item_caps() > EmbedProfile::Small.toc_item_caps());
        assert!(c32k.toc_item_caps() > c8k.toc_item_caps());
    }
}
