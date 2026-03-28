use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectConfig {
    pub project: ProjectSettings,
    pub indexer: IndexerConfig,
    pub mcp: McpConfig,
    pub documentation: DocConfig,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectSettings {
    pub name: String,
    pub root: PathBuf,
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
                root: PathBuf::from("./src"),
                languages: vec![
                    "go".to_string(),
                    "typescript".to_string(),
                    "python".to_string(),
                ],
            },
            indexer: IndexerConfig {
                exclude: vec!["**/node_modules/**".to_string(), "**/vendor/**".to_string()],
                include: vec!["*.go".to_string(), "*.ts".to_string(), "*.py".to_string()],
            },
            mcp: McpConfig {
                enabled: true,
                port: 3000,
                auth_token: "".to_string(),
                auto_index_on_start: true,
                auto_index_threshold_minutes: 5,
            },
            documentation: DocConfig {
                output: PathBuf::from("./docs"),
                templates: vec!["agents".to_string(), "claude".to_string()],
            },
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
        assert_eq!(config.project.root, PathBuf::from("./src"));
        assert_eq!(config.project.languages, vec!["go", "typescript", "python"]);
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
    }

    #[test]
    fn test_config_documentation() {
        let config = ProjectConfig::default();
        assert_eq!(config.documentation.output, PathBuf::from("./docs"));
        assert_eq!(config.documentation.templates, vec!["agents", "claude"]);
    }
}
