//! `.leankg.yaml` steer file — DeepWiki `.devin/wiki.json` pattern
//! (strategy §3.8 / §17 Tier 6 item 34).
//!
//! Declares which paths the indexer should prioritize vs ignore, plus
//! language-specific extractor flags. Additive config only: nothing in the
//! runtime consumes this yet (wiring lands with the indexer walk follow-up).
//!
//! The structs are `#[serde(default)]`-friendly so a minimal `.leankg.yaml`
//! (or none at all) still parses.

use serde::{Deserialize, Serialize};
use std::collections::BTreeSet;

/// Root steer block. Mirrors the `steer:` key in `.leankg.yaml`.
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(default)]
pub struct SteerConfig {
    /// Path prefixes that are most relevant — indexed first, exempt from
    /// aggressive pruning, and boosted in retrieval later.
    #[serde(default)]
    pub priority_paths: Vec<String>,
    /// Path prefixes to skip entirely during the index walk.
    #[serde(default)]
    pub ignore_paths: Vec<String>,
    /// Language-specific extractor toggles (e.g. `swift: false`).
    #[serde(default)]
    pub languages: BTreeMapCompat,
    /// Optional per-cluster notes (DeepWiki `page_notes` analog).
    #[serde(default)]
    pub notes: Vec<ClusterNote>,
}

/// Language toggle map. Kept as an explicit vec-of-pairs so serde_yaml
/// round-trips deterministically (BTreeMap ordering can vary).
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(default)]
pub struct BTreeMapCompat {
    /// `(language, enabled)` pairs, e.g. `[("swift", false)]`.
    #[serde(default)]
    pub items: Vec<(String, bool)>,
}

impl BTreeMapCompat {
    /// Whether a language is explicitly disabled.
    pub fn is_disabled(&self, lang: &str) -> bool {
        self.items.iter().any(|(k, v)| k == lang && !v)
    }

    /// Set of disabled languages.
    pub fn disabled(&self) -> BTreeSet<&str> {
        self.items
            .iter()
            .filter(|(_, v)| !v)
            .map(|(k, _)| k.as_str())
            .collect()
    }
}

/// A free-form note attached to a path prefix or cluster (DeepWiki `page_notes`).
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(default)]
pub struct ClusterNote {
    /// Path prefix or cluster id this note applies to.
    pub path: String,
    /// Human / agent-readable note body.
    pub note: String,
}

impl SteerConfig {
    /// Whether a given repo-relative path should be ignored.
    pub fn is_ignored(&self, rel_path: &str) -> bool {
        self.ignore_paths.iter().any(|p| rel_path.starts_with(p))
    }

    /// Whether a given repo-relative path is a priority path.
    pub fn is_priority(&self, rel_path: &str) -> bool {
        self.priority_paths.iter().any(|p| rel_path.starts_with(p))
    }
}

/// Load `steer:` from an optional YAML string. Returns an empty default when
/// the block is absent.
pub fn parse_steer(yaml: &str) -> Result<SteerConfig, Box<dyn std::error::Error>> {
    let doc: serde_yaml::Value = serde_yaml::from_str(yaml)?;
    match doc.get("steer") {
        Some(v) => Ok(serde_yaml::from_value(v.clone())?),
        None => Ok(SteerConfig::default()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_full_steer_block() {
        let yaml = r#"
steer:
  priority_paths:
    - src/auth
    - src/db
  ignore_paths:
    - vendor
    - generated
  languages:
    items:
      - [swift, false]
      - [objc, true]
  notes:
    - path: src/auth
      note: auth middleware owns token issuance
"#;
        let cfg = parse_steer(yaml).expect("parse");
        assert!(cfg.is_priority("src/auth/handlers.rs"));
        assert!(cfg.is_ignored("vendor/lib/a.rs"));
        assert!(!cfg.is_ignored("src/auth/x.rs"));
        assert!(cfg.languages.is_disabled("swift"));
        assert!(!cfg.languages.is_disabled("objc"));
        assert_eq!(cfg.notes[0].path, "src/auth");
    }

    #[test]
    fn absent_steer_block_is_empty_default() {
        let cfg = parse_steer("project:\n  name: demo\n").expect("parse");
        assert!(cfg.priority_paths.is_empty());
        assert!(!cfg.is_ignored("anything"));
    }

    #[test]
    fn empty_document_parses() {
        let cfg = parse_steer("").expect("parse empty");
        assert!(cfg.ignore_paths.is_empty());
    }

    #[test]
    fn round_trips_deterministically() {
        let yaml = "steer:\n  priority_paths:\n    - src\n";
        let cfg = parse_steer(yaml).expect("parse");
        let back = serde_yaml::to_string(&cfg).expect("serialize");
        assert!(back.contains("priority_paths"), "field preserved: {back}");
    }
}
