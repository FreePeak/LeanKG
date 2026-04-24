use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectConfig {
    pub project: ProjectSettings,
    pub indexer: IndexerConfig,
    pub mcp: McpConfig,
    pub documentation: DocConfig,
    pub database: DatabaseConfig,
    pub microservice: Option<MicroserviceExtractorConfig>,
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
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct McpConfig {
    pub enabled: bool,
    pub port: u16,
    pub auth_token: String,
    pub auto_index_on_start: bool,
    pub auto_index_threshold_minutes: u64,
    pub auto_index_on_db_write: bool,
}

/// Database configuration for LeanKG
/// SQLite (CozoDB embedded) is the default, PostgreSQL can be used for
/// multi-client HTTP server deployments
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DatabaseConfig {
    /// Database backend: "sqlite" or "postgres"
    #[serde(default = "default_backend")]
    pub backend: String,
    /// For SQLite: path to .leankg directory
    /// For PostgreSQL: connection string (e.g., "postgres://user:pass@localhost:5432/leankg")
    pub path: Option<String>,
    /// PostgreSQL connection pool size (only for postgres backend)
    #[serde(default = "default_pool_size")]
    pub pool_size: u32,
    /// Enable SSL for PostgreSQL connections
    #[serde(default)]
    pub ssl_enabled: bool,
}

fn default_backend() -> String {
    "sqlite".to_string()
}

fn default_pool_size() -> u32 {
    10
}

impl Default for DatabaseConfig {
    fn default() -> Self {
        Self {
            backend: default_backend(),
            path: None,
            pool_size: default_pool_size(),
            ssl_enabled: false,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocConfig {
    pub output: PathBuf,
    pub templates: Vec<String>,
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
            },
            mcp: McpConfig {
                enabled: true,
                port: 3000,
                auth_token: "".to_string(),
                auto_index_on_start: true,
                auto_index_threshold_minutes: 5,
                auto_index_on_db_write: true,
            },
            documentation: DocConfig {
                output: PathBuf::from("./docs"),
                templates: vec!["agents".to_string(), "claude".to_string()],
            },
            database: DatabaseConfig::default(),
            microservice: None,
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
}
