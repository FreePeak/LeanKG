use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectConfig {
    pub project: ProjectSettings,
    pub indexer: IndexerConfig,
    pub mcp: McpConfig,
    pub documentation: DocConfig,
    pub microservice: Option<MicroserviceExtractorConfig>,
    pub auth: AuthSettings,
    /// FR-LSP-B: optional prefab / user LSP server block.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub lsp: Option<crate::lsp::config::LspConfig>,
    /// Remote source configuration for indexing from non-local sources.
    /// When set, overrides the local `project.root` and syncs content to
    /// `.leankg/sources/` before indexing. CLI flags `--source`, `--auth`,
    /// and `--ref-name` take precedence over config values.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub source: Option<SourceConfig>,
    /// Optional Postgres connection settings. When unset, the backend uses
    /// `LEANKG_PG_URL` or its built-in dev default.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub db: Option<DbConfig>,
}

/// Optional Postgres settings that override the compiled-in defaults.
/// Read by the DB backend as `env LEANKG_PG_URL / LEANKG_PG_POOL_SIZE /
/// LEANKG_PG_LOCK` > `db:` yaml block > built-in default. Every field is
/// optional so a minimal `db: {}` or a partial block is valid.
#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct DbConfig {
    /// Connection URL, e.g. `postgresql://postgres:postgres@localhost:5433/leankg`.
    pub url: Option<String>,
    /// Lazy connection pool size (default 5, clamped >= 1).
    pub pool_size: Option<usize>,
    /// `false` disables the index advisory lock (default true).
    pub lock: Option<bool>,
}

/// Load the `db:` block from the nearest `leankg.yaml` walking up from the
/// current directory (same resolution as [`crate::find_project_root`] in
/// `main.rs`). Returns `None` when no config file or `db:` block exists.
/// The backend uses this as the middle precedence tier:
/// `LEANKG_PG_URL` env > `db:` yaml > built-in default.
pub fn db_config_from_cwd() -> Option<DbConfig> {
    let cwd = std::env::current_dir().ok()?;
    for dir in cwd.ancestors() {
        let cfg_path = dir.join("leankg.yaml");
        if cfg_path.is_file() {
            let content = std::fs::read_to_string(cfg_path).ok()?;
            let config: ProjectConfig = serde_yaml::from_str(&content).ok()?;
            return config.db;
        }
    }
    None
}

// ----------------------------------------------------------------------
// N1 (cycle-2 R2a): leankg.yaml writers must preserve user fields.
//
// Every writer of a project config (CLI `init`, the `mcp_init` tool, the
// setup pipeline) used to serialize a freshly generated `ProjectConfig`
// straight over the existing file — dropping `project.project_path` (the
// schema identity anchor) and every key serde does not model. The helpers
// below implement read-modify-write: EXISTING keys always win, missing keys
// are filled from the generated config.
// ----------------------------------------------------------------------

/// Recursively fill keys missing in `target` from `source`. Scalars and
/// sequences present in `target` are never touched; mappings are merged
/// depth-first so nested user overrides survive.
pub(crate) fn fill_missing_yaml_keys(target: &mut serde_yaml::Value, source: &serde_yaml::Value) {
    if let (Some(tmap), serde_yaml::Value::Mapping(smap)) = (target.as_mapping_mut(), source) {
        for (k, sv) in smap {
            match tmap.get_mut(k) {
                Some(tv) => fill_missing_yaml_keys(tv, sv),
                None => {
                    tmap.insert(k.clone(), sv.clone());
                }
            }
        }
    }
}

/// Merge a freshly generated config UNDER an existing `leankg.yaml` document.
/// Existing keys — including fields serde does not model — win; missing keys
/// are filled from `fresh`. An unparseable existing document falls back to
/// the fresh serialization (nothing recoverable to preserve).
pub fn merge_yaml_preserving_existing(existing: &str, fresh: &ProjectConfig) -> String {
    let fresh_value = serde_yaml::to_value(fresh).unwrap_or(serde_yaml::Value::Null);
    match serde_yaml::from_str::<serde_yaml::Value>(existing) {
        Ok(mut merged) => {
            fill_missing_yaml_keys(&mut merged, &fresh_value);
            serde_yaml::to_string(&merged)
                .unwrap_or_else(|_| serde_yaml::to_string(fresh).unwrap_or_default())
        }
        Err(_) => serde_yaml::to_string(fresh).unwrap_or_default(),
    }
}

/// Write `fresh` to `path`, preserving user fields when the file already
/// exists and parses. Creates parent directories as needed.
pub fn write_config_preserving_existing(
    path: &std::path::Path,
    fresh: &ProjectConfig,
) -> std::io::Result<()> {
    if let Some(parent) = path.parent() {
        if !parent.as_os_str().is_empty() {
            std::fs::create_dir_all(parent)?;
        }
    }
    let content = match std::fs::read_to_string(path) {
        Ok(existing) => merge_yaml_preserving_existing(&existing, fresh),
        Err(_) => serde_yaml::to_string(fresh).unwrap_or_default(),
    };
    std::fs::write(path, content)
}

/// N1 self-heal for the index-init path: refill a MISSING
/// `project.project_path` anchor in an existing config using `identity_hint`
/// — the canonical path THIS index run keys on (its own target), i.e. the
/// exact identity the writer is about to use. The anchor is the reader/writer
/// schema identity (see `project_identity_keys_in` in db::backend); when a
/// rewrite loses it, the next boot resolves a different schema and serves an
/// empty DB. Every other key — including unmodeled custom fields — is
/// preserved verbatim; an anchor already present is never touched.
/// Missing files are skipped.
pub fn ensure_identity_fields(
    config_path: &std::path::Path,
    identity_hint: &std::path::Path,
) -> std::io::Result<()> {
    let Ok(existing) = std::fs::read_to_string(config_path) else {
        return Ok(());
    };
    let mut doc: serde_yaml::Value = match serde_yaml::from_str::<serde_yaml::Value>(&existing) {
        Ok(v) if v.is_mapping() => v,
        _ => return Ok(()),
    };

    let Some(project) = doc.get_mut("project").and_then(|p| p.as_mapping_mut()) else {
        return Ok(());
    };
    let key = serde_yaml::Value::String("project_path".into());
    if project.contains_key(&key) {
        return Ok(()); // anchor present — nothing to heal
    }
    project.insert(
        key.clone(),
        serde_yaml::Value::String(identity_hint.to_string_lossy().to_string()),
    );
    let out = serde_yaml::to_string(&doc)
        .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
    std::fs::write(config_path, out)
}

/// Run [`ensure_identity_fields`] over every conventional location of a
/// project config around a `.leankg` dir: `<root>/.leankg/leankg.yaml`,
/// `<root>/leankg.yaml`, and the same pair one level up (the `index ./src`
/// invocation style anchors `.leankg` inside the source dir while the config
/// lives at the repo root). Only files that already exist are touched.
pub fn ensure_identity_fields_for_db(db_path: &std::path::Path, identity_hint: &std::path::Path) {
    let mut roots: Vec<std::path::PathBuf> = Vec::new();
    if let Some(r) = db_path.parent() {
        roots.push(r.to_path_buf());
        if let Some(g) = r.parent() {
            roots.push(g.to_path_buf());
        }
    }
    let mut seen = std::collections::HashSet::new();
    for root in roots {
        if !seen.insert(root.clone()) {
            continue;
        }
        for cfg in [
            root.join(".leankg").join("leankg.yaml"),
            root.join("leankg.yaml"),
        ] {
            let _ = ensure_identity_fields(&cfg, identity_hint);
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SourceConfig {
    /// Source URI: gs://bucket, s3://bucket, git+https://..., etc.
    pub uri: String,
    /// Auth credential: access token, key file path, or env var reference.
    pub auth: Option<String>,
    /// Git reference (branch/tag/commit), only for git sources.
    #[serde(default)]
    pub ref_name: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MicroserviceExtractorConfig {
    pub client_dirs: Vec<String>,
    pub config_files: Vec<String>,
    pub grpc_address_pattern: String,
    pub http_address_pattern: String,
    pub track_protocols: Vec<String>,
}

impl Default for MicroserviceExtractorConfig {
    fn default() -> Self {
        Self {
            client_dirs: vec!["internal/external".to_string()],
            config_files: vec![
                "config/config.go".to_string(),
                "config/*.yaml".to_string(),
                "config/*.yml".to_string(),
            ],
            grpc_address_pattern: r"dns:///{service}\.default\.svc\.cluster\.local\.::{port}"
                .to_string(),
            http_address_pattern: r"http://{service}\.default\.svc\.cluster\.local\.".to_string(),
            track_protocols: vec!["grpc".to_string()],
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct ProjectSettings {
    pub name: String,
    pub root: PathBuf,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub project_path: Option<PathBuf>,
    pub languages: Vec<String>,
    /// Steer file (`priority_paths` / `ignore_paths`). Additive; the indexer
    /// walk consumes this in a follow-up PR (DeepWiki `.devin/wiki.json`
    /// pattern, strategy §3.8 / §17 Tier 6 item 34).
    #[serde(default)]
    pub steer: crate::config::steer::SteerConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct IndexerConfig {
    pub exclude: Vec<String>,
    pub include: Vec<String>,
    /// US-CBM-B10 / FR-B08: typed call resolution feature flag.
    /// `off`     - never attempt typed resolve
    /// `go,ts`   - attempt typed resolve only for Go and TypeScript
    /// `all`     - attempt typed resolve for every supported language
    #[serde(default = "default_typed_resolve")]
    pub typed_resolve: String,
}

fn default_typed_resolve() -> String {
    "off".to_string()
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpConfig {
    pub enabled: bool,
    pub port: u16,
    pub auth_token: String,
    pub auto_index_on_start: bool,
    pub auto_index_threshold_minutes: u64,
    pub auto_index_on_db_write: bool,
    #[serde(default = "default_true")]
    pub require_git_for_auto_index: bool,
}

fn default_true() -> bool {
    true
}

/// US-CBM-B10 / FR-B08: Interpret the typed_resolve feature flag.
/// Returns true when typed call resolution should be attempted for
/// the given language.
pub fn typed_resolve_enabled(setting: &str, language: &str) -> bool {
    let s = setting.trim().to_lowercase();
    match s.as_str() {
        "off" | "" | "false" | "no" => false,
        "all" | "true" | "yes" | "on" => true,
        // CSV of language names: "go,ts,py". We also accept common
        // aliases (ts -> typescript, js -> javascript, etc.) so the
        // user's config is forgiving.
        other => {
            let aliases: &[(&str, &[&str])] = &[
                (
                    "typescript",
                    &["ts", "tsx", "typescript", "javascript", "js", "jsx"],
                ),
                ("javascript", &["js", "jsx", "javascript"]),
                ("python", &["py", "python"]),
                ("rust", &["rs", "rust"]),
                ("ruby", &["rb", "ruby"]),
                ("csharp", &["cs", "csharp", "c#"]),
                ("swift", &["swift"]),
                ("objc", &["objc", "objective-c", "objectivec", "m", "mm"]),
            ];
            let lang_lower = language.to_lowercase();
            let mut accepted: std::collections::HashSet<String> = std::collections::HashSet::new();
            accepted.insert(lang_lower.clone());
            for (canonical, alias_list) in aliases {
                if alias_list.iter().any(|a| *a == lang_lower) {
                    accepted.insert(canonical.to_string());
                }
                if *canonical == lang_lower {
                    for a in *alias_list {
                        accepted.insert(a.to_string());
                    }
                }
            }
            other
                .split(&[',', ' ', ';'][..])
                .filter(|s| !s.is_empty())
                .any(|s| accepted.contains(s) || s == "all")
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocConfig {
    pub output: PathBuf,
    pub templates: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct AuthSettings {
    pub enabled: bool,
    #[serde(default)]
    pub provider: AuthProvider,
    #[serde(default)]
    pub tokens: Vec<TokenEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default, PartialEq)]
#[serde(rename_all = "lowercase")]
pub enum AuthProvider {
    #[default]
    Static,
    // Future: Oidc,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TokenEntry {
    pub token: String,
    pub role: String,
    pub client_id: String,
}

impl Default for ProjectConfig {
    fn default() -> Self {
        Self {
            project: ProjectSettings {
                name: "my-project".to_string(),
                root: PathBuf::from("."),
                project_path: None,
                languages: vec![
                    "go".to_string(),
                    "typescript".to_string(),
                    "python".to_string(),
                    "java".to_string(),
                    "kotlin".to_string(),
                    "rust".to_string(),
                    "dart".to_string(),
                    "swift".to_string(),
                    "objc".to_string(),
                    "c".to_string(),
                    "cpp".to_string(),
                    "ruby".to_string(),
                    "php".to_string(),
                    "perl".to_string(),
                    "r".to_string(),
                    "elixir".to_string(),
                    "bash".to_string(),
                    "lua".to_string(),
                    "scala".to_string(),
                    "zig".to_string(),
                    "solidity".to_string(),
                    "csharp".to_string(),
                ],
                steer: crate::config::steer::SteerConfig::default(),
            },
            indexer: IndexerConfig {
                exclude: vec!["**/node_modules/**".to_string(), "**/vendor/**".to_string()],
                include: vec![
                    "*.go".to_string(),
                    "*.ts".to_string(),
                    "*.py".to_string(),
                    "*.java".to_string(),
                    "*.kt".to_string(),
                    "*.xml".to_string(),
                    "*.rs".to_string(),
                    "*.dart".to_string(),
                    "*.swift".to_string(),
                    "*.m".to_string(),
                    "*.mm".to_string(),
                    "*.c".to_string(),
                    "*.h".to_string(),
                    "*.cpp".to_string(),
                    "*.hpp".to_string(),
                    "*.rb".to_string(),
                    "*.php".to_string(),
                    "*.pl".to_string(),
                    "*.pm".to_string(),
                    "*.r".to_string(),
                    "*.ex".to_string(),
                    "*.exs".to_string(),
                    "*.sh".to_string(),
                    "*.lua".to_string(),
                    "*.scala".to_string(),
                    "*.zig".to_string(),
                    "*.sol".to_string(),
                    "*.cs".to_string(),
                ],
                typed_resolve: default_typed_resolve(),
            },
            mcp: McpConfig {
                enabled: true,
                port: 3000,
                auth_token: "".to_string(),
                auto_index_on_start: true,
                auto_index_threshold_minutes: 5,
                // auto_index_on_db_write defaults to false: re-indexing on every
                // external DB write can create CPU/memory storms in large workspaces
                // and is rarely what users want. Set explicitly to true in leankg.yaml
                // if needed.
                auto_index_on_db_write: false,
                require_git_for_auto_index: true,
            },
            documentation: DocConfig {
                output: PathBuf::from("./docs"),
                templates: vec!["agents".to_string(), "claude".to_string()],
            },
            microservice: None,
            auth: AuthSettings::default(),
            lsp: None,
            source: None,
            db: None,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_default_config() {
        let config = ProjectConfig::default();
        assert_eq!(config.project.name, "my-project");
        assert!(config.mcp.enabled);
        assert_eq!(config.mcp.port, 3000);
    }

    #[test]
    fn test_config_project_settings() {
        let config = ProjectConfig::default();
        assert_eq!(config.project.root, PathBuf::from("."));
        let langs = &config.project.languages;
        // Default language list is a superset of the original five plus all
        // registry-backed languages.
        for l in [
            "go",
            "typescript",
            "python",
            "java",
            "kotlin",
            "rust",
            "dart",
            "swift",
            "objc",
            "c",
            "cpp",
            "ruby",
            "php",
            "perl",
            "r",
            "elixir",
            "bash",
            "lua",
            "scala",
            "zig",
            "solidity",
            "csharp",
        ] {
            assert!(langs.contains(&l.to_string()), "missing default lang {}", l);
        }
    }

    #[test]
    fn test_config_indexer_excludes() {
        let config = ProjectConfig::default();
        assert!(config
            .indexer
            .exclude
            .contains(&"**/node_modules/**".to_string()));
        assert!(config.indexer.exclude.contains(&"**/vendor/**".to_string()));
        assert!(config.indexer.include.contains(&"*.go".to_string()));
        assert!(config.indexer.include.contains(&"*.java".to_string()));
    }

    #[test]
    fn test_config_documentation() {
        let config = ProjectConfig::default();
        assert_eq!(config.documentation.output, PathBuf::from("./docs"));
        assert_eq!(config.documentation.templates, vec!["agents", "claude"]);
    }

    #[test]
    fn db_block_parses_from_yaml() {
        let yaml = r#"
db:
  url: postgresql://u:p@host:9999/bar
  pool_size: 12
  lock: false
"#;
        let config: ProjectConfig = serde_yaml::from_str(yaml).unwrap();
        let db = config.db.expect("db block parsed");
        assert_eq!(db.url.as_deref(), Some("postgresql://u:p@host:9999/bar"));
        assert_eq!(db.pool_size, Some(12));
        assert_eq!(db.lock, Some(false));
    }

    #[test]
    fn db_defaults_to_none_when_absent() {
        let config = ProjectConfig::default();
        assert!(config.db.is_none());
        // Serializing a default config must not emit a db: block.
        let yaml = serde_yaml::to_string(&config).unwrap();
        assert!(
            !yaml.contains("db:"),
            "default config serializes without db:\n{yaml}"
        );
    }

    #[test]
    fn db_empty_block_is_valid() {
        let yaml = "db: {}\n";
        let config: ProjectConfig = serde_yaml::from_str(yaml).unwrap();
        let db = config.db.expect("empty db block parsed");
        assert!(db.url.is_none() && db.pool_size.is_none() && db.lock.is_none());
    }

    // ------------------------------------------------------------------
    // N1 (cycle-2 R2a): leankg.yaml writers must preserve user fields.
    // ------------------------------------------------------------------

    /// The identity anchor must actually be SERIALIZED: `skip_serializing`
    /// made every ProjectConfig round-trip silently drop `project_path`
    /// (the R2 sweep saw `leankg init`/`mcp_init` write yamls with no
    /// `project_path` at all, breaking reader/writer schema agreement).
    #[test]
    fn serialized_config_emits_project_path_when_set() {
        let mut config = ProjectConfig::default();
        config.project.project_path = Some(PathBuf::from("/host/demo"));
        let yaml = serde_yaml::to_string(&config).unwrap();
        assert!(
            yaml.contains("project_path: /host/demo"),
            "project_path must be written back to yaml\n{yaml}"
        );
        // None must stay absent (no `project_path: ~` noise).
        let yaml = serde_yaml::to_string(&ProjectConfig::default()).unwrap();
        assert!(
            !yaml.contains("project_path"),
            "None project_path must not serialize\n{yaml}"
        );
    }

    /// Merging a freshly generated config under an existing file keeps EVERY
    /// existing key — including fields serde does not model — and only fills
    /// the missing ones.
    #[test]
    fn merge_yaml_preserves_existing_fields_and_fills_missing() {
        let existing = "\
project:
  name: keep-me
  root: ./custom-src
  project_path: /host/keep
  languages:
    - rust
  vendor_probe: user-custom-value
indexer:
  exclude:
    - \"**/secret/**\"
";
        let merged = merge_yaml_preserving_existing(existing, &ProjectConfig::default());
        // Existing values win.
        assert!(merged.contains("keep-me"), "name preserved:\n{merged}");
        assert!(merged.contains("./custom-src"), "root preserved:\n{merged}");
        assert!(
            merged.contains("/host/keep"),
            "project_path preserved:\n{merged}"
        );
        assert!(
            merged.contains("user-custom-value"),
            "unknown custom field preserved (serde would drop it):\n{merged}"
        );
        assert!(
            merged.contains("**/secret/**"),
            "existing indexer.exclude preserved:\n{merged}"
        );
        // Missing keys are filled from the fresh defaults.
        assert!(
            merged.contains("auto_index_on_start"),
            "missing mcp block filled from defaults:\n{merged}"
        );
        assert!(
            merged.contains("typed_resolve"),
            "missing indexer.typed_resolve filled:\n{merged}"
        );
        // And the result still parses as a ProjectConfig.
        let parsed: ProjectConfig = serde_yaml::from_str(&merged).unwrap();
        assert_eq!(parsed.project.name, "keep-me");
        assert_eq!(parsed.project.root, PathBuf::from("./custom-src"));
    }

    /// A corrupt existing file cannot be merged — fall back to the fresh
    /// config rather than losing init entirely.
    #[test]
    fn merge_yaml_falls_back_to_fresh_on_unparseable_existing() {
        let merged = merge_yaml_preserving_existing(":::: not yaml :::", &ProjectConfig::default());
        let parsed: Result<ProjectConfig, _> = serde_yaml::from_str(&merged);
        assert!(
            parsed.is_ok(),
            "fallback output must be valid yaml:\n{merged}"
        );
        assert_eq!(parsed.unwrap().project.name, "my-project");
    }

    /// `write_config_preserving_existing` end-to-end on the real files an
    /// index/init run touches: `<root>/leankg.yaml` and
    /// `<root>/.leankg/leankg.yaml`.
    #[test]
    fn write_config_preserving_existing_round_trip() {
        let dir = tempfile::TempDir::new().unwrap();
        let path = dir.path().join(".leankg").join("leankg.yaml");
        std::fs::create_dir_all(path.parent().unwrap()).unwrap();
        std::fs::write(
            &path,
            "project:\n  name: mine\n  project_path: ./src\n  keep_me: yes\n",
        )
        .unwrap();

        let mut fresh = ProjectConfig::default();
        fresh.project.project_path = Some(PathBuf::from("/elsewhere"));
        write_config_preserving_existing(&path, &fresh).unwrap();

        let out = std::fs::read_to_string(&path).unwrap();
        assert!(out.contains("mine"), "name kept:\n{out}");
        assert!(out.contains("./src"), "anchor kept:\n{out}");
        assert!(out.contains("keep_me"), "custom key kept:\n{out}");
        assert!(
            !out.contains("/elsewhere"),
            "fresh must not override:\n{out}"
        );

        // A MISSING file is simply created with the fresh content.
        let other = dir.path().join("leankg.yaml");
        write_config_preserving_existing(&other, &fresh).unwrap();
        let out = std::fs::read_to_string(&other).unwrap();
        assert!(out.contains("project_path: /elsewhere"), "\n{out}");
    }

    // ------------------------------------------------------------------
    // N1 self-heal: ensure_identity_fields refills a missing anchor.
    // ------------------------------------------------------------------

    #[test]
    fn ensure_identity_fields_refills_missing_anchor_from_index_hint() {
        let dir = tempfile::TempDir::new().unwrap();
        let leankg = dir.path().join(".leankg");
        std::fs::create_dir_all(&leankg).unwrap();
        std::fs::create_dir_all(dir.path().join("src")).unwrap();
        let cfg = leankg.join("leankg.yaml");
        std::fs::write(
            &cfg,
            "project:\n  name: ident-fixture-a\n  root: ./src\n  languages:\n    - rust\nteam_identity_probe: keep-me-through-reindex\n",
        )
        .unwrap();

        let hint = dir.path().join("src");
        ensure_identity_fields(&cfg, &hint).unwrap();

        let out = std::fs::read_to_string(&cfg).unwrap();
        assert!(
            out.contains(&format!("project_path: {}", hint.display())),
            "anchor rebuilt from the index-target hint:\n{out}"
        );
        assert!(out.contains("keep-me-through-reindex"), "\n{out}");
        assert!(out.contains("ident-fixture-a"), "\n{out}");
    }

    #[test]
    fn ensure_identity_fields_leaves_existing_anchor_untouched() {
        let dir = tempfile::TempDir::new().unwrap();
        let leankg = dir.path().join(".leankg");
        std::fs::create_dir_all(&leankg).unwrap();
        let cfg = leankg.join("leankg.yaml");
        let original = format!(
            "project:\n  name: p\n  root: .\n  project_path: {}\n  custom_probe: 1\n",
            "/elsewhere/anchor"
        );
        std::fs::write(&cfg, &original).unwrap();

        ensure_identity_fields(&cfg, std::path::Path::new("/somewhere/else")).unwrap();
        let out = std::fs::read_to_string(&cfg).unwrap();
        assert_eq!(out, original, "present anchor must not be rewritten");
    }

    #[test]
    fn ensure_identity_fields_skips_missing_file() {
        let dir = tempfile::TempDir::new().unwrap();
        let cfg = dir.path().join(".leankg").join("leankg.yaml");
        assert!(ensure_identity_fields(&cfg, std::path::Path::new("/x")).is_ok());
        assert!(!cfg.exists(), "must not create configs on its own");
    }

    #[test]
    fn ensure_identity_fields_for_db_covers_root_and_parent_levels() {
        let dir = tempfile::TempDir::new().unwrap();
        let repo = dir.path().join("repo");
        std::fs::create_dir_all(repo.join(".leankg")).unwrap();
        std::fs::create_dir_all(repo.join("src").join(".leankg")).unwrap();
        std::fs::write(repo.join("src").join("main.rs"), "fn main() {}").unwrap();
        // Repo-root config lost its anchor (the corruption under test).
        std::fs::write(
            repo.join(".leankg").join("leankg.yaml"),
            "project:\n  name: r\n  root: ./src\nprobe: v\n",
        )
        .unwrap();

        // `leankg index ./src` anchors the db inside src/; the CLI passes
        // the CANONICALIZED target as the heal hint.
        let hint = std::fs::canonicalize(repo.join("src")).unwrap_or_else(|_| repo.join("src"));
        ensure_identity_fields_for_db(&repo.join("src").join(".leankg"), &hint);

        let healed = std::fs::read_to_string(repo.join(".leankg").join("leankg.yaml")).unwrap();
        let expected = hint;
        assert!(
            healed.contains(&format!("project_path: {}", expected.display())),
            "grandparent-level config healed via one-level walk:\n{healed}"
        );
        assert!(healed.contains("probe: v"), "\n{healed}");
    }

    /// `ensure_identity_fields_for_db` is safe when no config exists anywhere
    /// (fresh projects before first init).
    #[test]
    fn ensure_identity_fields_for_db_is_noop_without_configs() {
        let dir = tempfile::TempDir::new().unwrap();
        ensure_identity_fields_for_db(
            &dir.path().join("whatever").join(".leankg"),
            std::path::Path::new("/x"),
        );
        // No assertion beyond "did not panic / did not write".
        assert!(!dir.path().join("whatever").exists());
    }

    // US-CBM-B10: typed_resolve flag
    #[test]
    fn typed_resolve_off_disables_all() {
        for lang in &["go", "ts", "python", "rust"] {
            assert!(!typed_resolve_enabled("off", lang));
            assert!(!typed_resolve_enabled("", lang));
            assert!(!typed_resolve_enabled("false", lang));
        }
    }

    #[test]
    fn typed_resolve_all_enables_all() {
        for lang in &["go", "ts", "python", "rust"] {
            assert!(typed_resolve_enabled("all", lang));
            assert!(typed_resolve_enabled("on", lang));
            assert!(typed_resolve_enabled("yes", lang));
        }
    }

    #[test]
    fn typed_resolve_csv_enables_listed_only() {
        assert!(typed_resolve_enabled("go,ts", "go"));
        assert!(typed_resolve_enabled("go,ts", "ts"));
        assert!(!typed_resolve_enabled("go,ts", "python"));
        assert!(!typed_resolve_enabled("go,ts", "rust"));
    }
}
