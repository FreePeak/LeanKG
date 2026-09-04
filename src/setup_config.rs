//! FR-ZCP-13: first-run setup contract for LeanKG projects.
//!
//! `<project>/.leankg/config.json` is the persisted setup choice:
//!
//! ```json
//! {"setup": "auto" | "manual", "embed": bool}
//! ```
//!
//! Read semantics (never panic / error out the setup flow):
//! - Missing file → [`ConfigOutcome::Missing`] + [`SetupConfig::default()`]
//!   (NotConfigured: the caller decides whether a prompt is possible).
//! - Corrupt file → [`ConfigOutcome::Corrupt`] + default (the caller warns).
//!
//! Mode resolution precedence (pure, unit-tested):
//! `--auto`/`--manual` flag > `LEANKG_SETUP_MODE` env > stored config.json >
//! interactive prompt (only when stdin AND stdout are TTYs) > manual default
//! (non-interactive; never blocks scripts).

use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};

/// The chosen first-run setup mode.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SetupMode {
    /// Index (and optionally embed) in the background right after `add`.
    Auto,
    /// Print the next commands; the user runs indexing explicitly.
    Manual,
}

impl SetupMode {
    pub fn as_str(self) -> &'static str {
        match self {
            SetupMode::Auto => "auto",
            SetupMode::Manual => "manual",
        }
    }

    /// Parse an env/CLI value ("auto" | "manual", case-insensitive).
    pub fn parse_env_value(raw: &str) -> Option<SetupMode> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "auto" => Some(SetupMode::Auto),
            "manual" => Some(SetupMode::Manual),
            _ => None,
        }
    }
}

/// Provenance of the resolved setup mode (surfaced in the `add` summary).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SetupModeSource {
    Flag,
    Env,
    Stored,
    Prompt,
    ManualDefault,
}

impl SetupModeSource {
    pub fn as_str(self) -> &'static str {
        match self {
            SetupModeSource::Flag => "flag",
            SetupModeSource::Env => "env",
            SetupModeSource::Stored => "stored",
            SetupModeSource::Prompt => "prompt",
            SetupModeSource::ManualDefault => "manual_default",
        }
    }
}

/// Persisted per-project setup contract (`<project>/.leankg/config.json`).
///
/// Unknown JSON fields are ignored (forward compatibility); a wrong-typed
/// known field makes the file corrupt (see [`ConfigOutcome`]).
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct SetupConfig {
    /// The stored setup choice; `None` = not configured (re-ask next run).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub setup: Option<SetupMode>,
    /// Whether auto setup should chain an embedding pass after indexing.
    #[serde(default)]
    pub embed: bool,
}

/// Outcome of reading the setup config.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ConfigOutcome {
    Found,
    Missing,
    Corrupt(String),
}

/// Path of the setup config for a project root.
pub fn config_path_for(project_root: &Path) -> PathBuf {
    project_root.join(".leankg").join("config.json")
}

/// Parse the config from raw JSON text.
pub fn parse_config(raw: &str) -> Result<SetupConfig, String> {
    serde_json::from_str(raw).map_err(|e| e.to_string())
}

/// Read the setup config for `project_root`. Missing/corrupt degrade to
/// [`SetupConfig::default()`] with the outcome reported — never an error.
pub fn load(project_root: &Path) -> (SetupConfig, ConfigOutcome) {
    let path = config_path_for(project_root);
    let Ok(raw) = std::fs::read_to_string(&path) else {
        return (SetupConfig::default(), ConfigOutcome::Missing);
    };
    match parse_config(&raw) {
        Ok(cfg) => (cfg, ConfigOutcome::Found),
        Err(e) => (SetupConfig::default(), ConfigOutcome::Corrupt(e)),
    }
}

/// Persist the setup config (creates `.leankg/` when missing).
pub fn save(project_root: &Path, cfg: &SetupConfig) -> Result<(), Box<dyn std::error::Error>> {
    let path = config_path_for(project_root);
    if let Some(parent) = path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    std::fs::write(&path, serde_json::to_string_pretty(cfg)?)?;
    Ok(())
}

/// FR-ZCP-13 `leankg setup --reset`: clear the stored setup choice so the
/// next run re-asks. Keeps every other field (e.g. `embed`). Returns `false`
/// when no config file exists yet.
pub fn reset_setup_choice(project_root: &Path) -> Result<bool, Box<dyn std::error::Error>> {
    if !config_path_for(project_root).exists() {
        return Ok(false);
    }
    let (mut cfg, _outcome) = load(project_root);
    cfg.setup = None;
    save(project_root, &cfg)?;
    Ok(true)
}

/// Resolve the setup mode. Precedence: flag > env > stored >
/// prompt (TTY-gated) > manual default. `prompt` is only invoked when
/// `is_tty` is true and no earlier signal resolved.
pub fn resolve_setup_mode(
    flag: Option<SetupMode>,
    env: Option<SetupMode>,
    stored: Option<SetupMode>,
    is_tty: bool,
    mut prompt: impl FnMut() -> Option<SetupMode>,
) -> (SetupMode, SetupModeSource) {
    if let Some(mode) = flag {
        return (mode, SetupModeSource::Flag);
    }
    if let Some(mode) = env {
        return (mode, SetupModeSource::Env);
    }
    if let Some(mode) = stored {
        return (mode, SetupModeSource::Stored);
    }
    if is_tty {
        if let Some(mode) = prompt() {
            return (mode, SetupModeSource::Prompt);
        }
    }
    (SetupMode::Manual, SetupModeSource::ManualDefault)
}

/// Dedupe project roots for the `leankg status` listing: canonicalize
/// best-effort (missing paths are kept as-is), first occurrence wins.
pub fn merge_project_roots(roots: &[PathBuf]) -> Vec<PathBuf> {
    let mut out: Vec<PathBuf> = Vec::new();
    for root in roots {
        let canon = std::fs::canonicalize(root).unwrap_or_else(|_| root.clone());
        if !out.contains(&canon) {
            out.push(canon);
        }
    }
    out
}

/// Freshness label per the FR-ZCP-06 vocabulary (`fresh|possibly_stale|cold`)
/// from cheap local facts only:
/// - no elements yet → `cold` (initialized, index empty or still building);
/// - an inventory snapshot taken at/after the last commit → `fresh`;
/// - everything else (commit newer than the snapshot, or no git context /
///   no inventory to prove freshness) → `possibly_stale`.
pub fn freshness_label(
    elements: usize,
    inventory_computed_at: Option<i64>,
    last_commit_time: Option<i64>,
) -> &'static str {
    if elements == 0 {
        return "cold";
    }
    let Some(computed_at) = inventory_computed_at else {
        return "possibly_stale";
    };
    match last_commit_time {
        Some(commit) if commit > computed_at => "possibly_stale",
        Some(_) => "fresh",
        // No git context — freshness cannot be proven.
        None => "possibly_stale",
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn roundtrip_auto_and_manual() {
        let dir = tempfile::TempDir::new().unwrap();
        for mode in [SetupMode::Auto, SetupMode::Manual] {
            let cfg = SetupConfig {
                setup: Some(mode),
                embed: true,
            };
            save(dir.path(), &cfg).unwrap();
            let (loaded, outcome) = load(dir.path());
            assert_eq!(outcome, ConfigOutcome::Found);
            assert_eq!(loaded, cfg);
        }
    }

    #[test]
    fn missing_file_is_not_configured() {
        let dir = tempfile::TempDir::new().unwrap();
        let (cfg, outcome) = load(dir.path());
        assert_eq!(outcome, ConfigOutcome::Missing);
        assert_eq!(cfg, SetupConfig::default());
        assert!(cfg.setup.is_none());
        assert!(!cfg.embed);
    }

    #[test]
    fn corrupt_json_degrades_to_default() {
        let dir = tempfile::TempDir::new().unwrap();
        std::fs::create_dir_all(dir.path().join(".leankg")).unwrap();
        std::fs::write(config_path_for(dir.path()), "{not json").unwrap();
        let (cfg, outcome) = load(dir.path());
        assert!(matches!(outcome, ConfigOutcome::Corrupt(_)));
        assert_eq!(cfg, SetupConfig::default());
    }

    #[test]
    fn wrong_typed_fields_are_corrupt() {
        for raw in [
            r#"{"setup": "bogus", "embed": true}"#,
            r#"{"setup": "auto", "embed": "yes"}"#,
            r#"{"setup": 1}"#,
        ] {
            assert!(parse_config(raw).is_err(), "should be corrupt: {raw}");
        }
        // Unknown fields are ignored (forward compatibility).
        let cfg = parse_config(r#"{"setup":"manual","embed":false,"future":1}"#).unwrap();
        assert_eq!(cfg.setup, Some(SetupMode::Manual));
        assert!(!cfg.embed);
    }

    #[test]
    fn reset_clears_setup_keeps_embed() {
        let dir = tempfile::TempDir::new().unwrap();
        // No file yet → false.
        assert!(!reset_setup_choice(dir.path()).unwrap());
        save(
            dir.path(),
            &SetupConfig {
                setup: Some(SetupMode::Auto),
                embed: true,
            },
        )
        .unwrap();
        assert!(reset_setup_choice(dir.path()).unwrap());
        let (cfg, outcome) = load(dir.path());
        assert_eq!(outcome, ConfigOutcome::Found);
        assert!(cfg.setup.is_none());
        assert!(cfg.embed);
    }

    #[test]
    fn precedence_flag_env_stored() {
        fn boom() -> Option<SetupMode> {
            panic!("prompt must not run when an earlier signal resolves")
        }
        // Flag wins over everything.
        let (m, s) = resolve_setup_mode(
            Some(SetupMode::Manual),
            Some(SetupMode::Auto),
            Some(SetupMode::Auto),
            true,
            boom,
        );
        assert_eq!((m, s), (SetupMode::Manual, SetupModeSource::Flag));
        // Env beats stored.
        let (m, s) = resolve_setup_mode(
            None,
            Some(SetupMode::Auto),
            Some(SetupMode::Manual),
            true,
            boom,
        );
        assert_eq!((m, s), (SetupMode::Auto, SetupModeSource::Env));
        // Stored beats prompt.
        let (m, s) = resolve_setup_mode(None, None, Some(SetupMode::Auto), true, boom);
        assert_eq!((m, s), (SetupMode::Auto, SetupModeSource::Stored));
    }

    #[test]
    fn prompt_only_when_tty() {
        fn boom() -> Option<SetupMode> {
            panic!("prompt ran while non-interactive")
        }
        // Non-TTY: the prompt must NOT be consulted; manual default.
        let (m, s) = resolve_setup_mode(None, None, None, false, boom);
        assert_eq!((m, s), (SetupMode::Manual, SetupModeSource::ManualDefault));

        // TTY + EOF (prompt returns None) → manual default.
        fn eof() -> Option<SetupMode> {
            None
        }
        let (m, s) = resolve_setup_mode(None, None, None, true, eof);
        assert_eq!((m, s), (SetupMode::Manual, SetupModeSource::ManualDefault));

        // TTY + answer → prompt provenance.
        fn answers_manual() -> Option<SetupMode> {
            Some(SetupMode::Manual)
        }
        let (m, s) = resolve_setup_mode(None, None, None, true, answers_manual);
        assert_eq!((m, s), (SetupMode::Manual, SetupModeSource::Prompt));

        fn answers_auto() -> Option<SetupMode> {
            Some(SetupMode::Auto)
        }
        let (m, s) = resolve_setup_mode(None, None, None, true, answers_auto);
        assert_eq!((m, s), (SetupMode::Auto, SetupModeSource::Prompt));
    }

    #[test]
    fn env_value_parsing() {
        assert_eq!(SetupMode::parse_env_value(" auto "), Some(SetupMode::Auto));
        assert_eq!(
            SetupMode::parse_env_value("MANUAL"),
            Some(SetupMode::Manual)
        );
        assert_eq!(SetupMode::parse_env_value("yes"), None);
        assert_eq!(SetupMode::parse_env_value(""), None);
    }

    #[test]
    fn merge_project_roots_dedupes_and_canonicalizes() {
        let dir = tempfile::TempDir::new().unwrap();
        let a = dir.path().join("a");
        std::fs::create_dir_all(&a).unwrap();
        let roots = vec![
            a.clone(),
            dir.path().join("a"), // same dir, different spelling
            dir.path().join("missing"),
            dir.path().to_path_buf(),
        ];
        let merged = merge_project_roots(&roots);
        assert_eq!(merged.len(), 3);
        // macOS /tmp is a symlink to /private/tmp: `a` canonicalizes to a
        // different literal string, so compare against the canonical form.
        let canon_a = std::fs::canonicalize(&a).unwrap();
        assert_eq!(merged[0], canon_a);
        assert!(merged.contains(&dir.path().join("missing")));
        // dir.path() itself may also canonicalize through the /tmp symlink.
        let canon_dir = std::fs::canonicalize(dir.path()).unwrap();
        assert!(merged.contains(&canon_dir));
    }

    #[test]
    fn freshness_ladder() {
        assert_eq!(freshness_label(0, None, None), "cold");
        assert_eq!(freshness_label(100, Some(1000), Some(500)), "fresh");
        assert_eq!(freshness_label(100, Some(1000), Some(1000)), "fresh");
        assert_eq!(
            freshness_label(100, Some(1000), Some(2000)),
            "possibly_stale"
        );
        assert_eq!(freshness_label(100, None, Some(500)), "possibly_stale");
        assert_eq!(freshness_label(100, Some(1000), None), "possibly_stale");
    }
}
