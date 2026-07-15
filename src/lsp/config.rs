//! US-CBM-B1 / FR-B03..04: LSP bridge configuration.
//!
//! Per-language LSP server command + args. The bridge spawns the
//! configured binary, sends `initialize` / `initialized`, and is
//! then ready to answer `textDocument/definition` and
//! `textDocument/references` requests via stdin/stdout JSON-RPC.
//!
//! Resolution behavior: when `typed_resolve` is enabled for a
//! language (per `IndexerConfig.typed_resolve`), the bridge is
//! spun up on demand for that project. Resolution requests return
//! a `Vec<Location>` that the bridge turns into a list of
//! qualified_names using the project's own `qualified_name`
//! convention.
//!
//! Example leankg.yaml:
//! ```yaml
//! lsp:
//!   servers:
//!     go:           { command: "gopls", args: ["serve"] }
//!     typescript:   { command: "typescript-language-server", args: ["--stdio"] }
//!     python:       { command: "pylsp", args: [] }
//!     rust:         { command: "rust-analyzer", args: [] }
//! ```
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LspServerConfig {
    /// Executable name (resolved via PATH or absolute path).
    pub command: String,
    /// Optional args passed verbatim to the command.
    #[serde(default)]
    pub args: Vec<String>,
    /// Optional file extensions this server handles. Defaults to
    /// common ones per language when empty.
    #[serde(default)]
    pub extensions: Vec<String>,
    /// Optional initialization options (sent in `initialize` request).
    #[serde(default)]
    pub initialization_options: Option<serde_json::Value>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LspConfig {
    #[serde(default)]
    pub servers: HashMap<String, LspServerConfig>,
    /// Per-project root where LSP indexes are kept (e.g. node_modules
    /// for TS). Defaults to the leankg.yaml file's parent directory.
    #[serde(default)]
    pub workspace_root: Option<PathBuf>,
    /// Per-request timeout in milliseconds (default 5000).
    #[serde(default = "default_lsp_timeout")]
    pub timeout_ms: u64,
}

impl Default for LspConfig {
    fn default() -> Self {
        Self {
            servers: HashMap::new(),
            workspace_root: None,
            timeout_ms: default_lsp_timeout(),
        }
    }
}

impl LspConfig {
    /// Default extension list for a given language when the user
    /// didn't supply one in the config file.
    pub fn default_extensions(language: &str) -> Vec<&'static str> {
        match language {
            "go" => vec!["go"],
            "typescript" | "javascript" => vec!["ts", "tsx", "js", "jsx"],
            "python" => vec!["py"],
            "rust" => vec!["rs"],
            "java" => vec!["java"],
            "kotlin" => vec!["kt", "kts"],
            "ruby" => vec!["rb"],
            "csharp" => vec!["cs"],
            "cpp" | "c" => vec!["cpp", "cxx", "cc", "hpp", "h"],
            "php" => vec!["php"],
            _ => vec![],
        }
    }
}

fn default_lsp_timeout() -> u64 {
    5000
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_extensions_for_common_languages() {
        assert!(LspConfig::default_extensions("go").contains(&"go"));
        assert!(LspConfig::default_extensions("typescript").contains(&"ts"));
        assert!(LspConfig::default_extensions("rust").contains(&"rs"));
    }

    #[test]
    fn empty_config_serializes_and_deserializes() {
        let cfg = LspConfig::default();
        let json = serde_json::to_string(&cfg).unwrap();
        let back: LspConfig = serde_json::from_str(&json).unwrap();
        assert_eq!(back.timeout_ms, 5000);
        assert!(back.servers.is_empty());
    }
}
