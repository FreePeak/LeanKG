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
    #[serde(skip_serializing, default)]
    pub project_path: Option<PathBuf>,
    pub languages: Vec<String>,
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
                ],
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
        assert_eq!(
            config.project.languages,
            vec!["go", "typescript", "python", "java", "kotlin"]
        );
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
