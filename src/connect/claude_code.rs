//! Claude Code client writer: `~/.claude.json` → `mcpServers.leankg`.

use std::path::{Path, PathBuf};

use super::Transport;

/// Absolute path of the Claude Code MCP config for `home`.
fn config_path(home: &Path) -> PathBuf {
    home.join(".claude.json")
}

/// Write (or merge) the leankg entry; returns the config path written.
pub fn apply(home: &Path, transport: &Transport) -> Result<PathBuf, String> {
    super::apply_json_config(&config_path(home), transport)
}

/// Remove only the leankg entry; Ok even when absent.
pub fn remove(home: &Path) -> Result<PathBuf, String> {
    super::remove_json_server(&config_path(home))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::connect::Transport;
    use serde_json::json;
    use tempfile::TempDir;

    fn sample_stdio() -> Transport {
        Transport::Stdio {
            command: "/usr/local/bin/leankg".into(),
            args: vec![
                "mcp-stdio".into(),
                "--project".into(),
                "/tmp/some-project".into(),
            ],
        }
    }

    fn read(home: &Path) -> serde_json::Value {
        let raw = std::fs::read_to_string(config_path(home)).unwrap();
        serde_json::from_str(&raw).unwrap()
    }

    #[test]
    fn apply_creates_config_from_scratch() {
        let home = TempDir::new().unwrap();
        let path = apply(home.path(), &sample_stdio()).unwrap();
        assert_eq!(path, home.path().join(".claude.json"));

        let root = read(home.path());
        assert_eq!(
            root["mcpServers"]["leankg"],
            json!({
                "command": "/usr/local/bin/leankg",
                "args": ["mcp-stdio", "--project", "/tmp/some-project"]
            })
        );
    }

    #[test]
    fn apply_is_idempotent_no_duplicate_keys() {
        let home = TempDir::new().unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let after_first = std::fs::read_to_string(config_path(home.path())).unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let after_second = std::fs::read_to_string(config_path(home.path())).unwrap();

        assert_eq!(after_first, after_second, "re-run must be byte-stable");
        let root = read(home.path());
        let servers = root["mcpServers"].as_object().unwrap();
        assert_eq!(servers.len(), 1, "no duplicate server entries");
        assert!(servers.contains_key("leankg"));
    }

    #[test]
    fn apply_preserves_sibling_servers_and_unknown_fields() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(home.path()).unwrap();
        std::fs::write(
            config_path(home.path()),
            serde_json::to_string_pretty(&json!({
                "mcpServers": {
                    "other": {"command": "foo", "args": ["bar"]}
                },
                "theme": "dark",
                "projects": {"a": 1}
            }))
            .unwrap(),
        )
        .unwrap();

        apply(home.path(), &sample_stdio()).unwrap();

        let root = read(home.path());
        assert_eq!(root["theme"], "dark");
        assert_eq!(root["projects"]["a"], 1);
        assert_eq!(root["mcpServers"]["other"]["command"], "foo");
        assert_eq!(
            root["mcpServers"]["leankg"]["command"],
            "/usr/local/bin/leankg"
        );
    }

    #[test]
    fn apply_remote_writes_http_entry() {
        let home = TempDir::new().unwrap();
        apply(
            home.path(),
            &Transport::Http {
                url: "http://localhost:9699".into(),
            },
        )
        .unwrap();

        let root = read(home.path());
        assert_eq!(
            root["mcpServers"]["leankg"],
            json!({"type": "http", "url": "http://localhost:9699"})
        );
    }

    #[test]
    fn remove_deletes_only_leankg_entry() {
        let home = TempDir::new().unwrap();
        apply(home.path(), &sample_stdio()).unwrap();

        remove(home.path()).unwrap();

        let root = read(home.path());
        assert!(root.get("mcpServers").is_some(), "container stays put");
        assert!(root["mcpServers"].get("leankg").is_none());
    }

    #[test]
    fn remove_preserves_siblings() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(home.path()).unwrap();
        std::fs::write(
            config_path(home.path()),
            serde_json::to_string_pretty(&json!({
                "mcpServers": {
                    "other": {"command": "foo"},
                    "leankg": {"command": "stale"}
                },
                "theme": "dark"
            }))
            .unwrap(),
        )
        .unwrap();

        remove(home.path()).unwrap();

        let root = read(home.path());
        assert_eq!(root["mcpServers"]["other"]["command"], "foo");
        assert_eq!(root["theme"], "dark");
        assert!(root["mcpServers"].get("leankg").is_none());
    }

    #[test]
    fn remove_is_ok_when_file_absent() {
        let home = TempDir::new().unwrap();
        let result = remove(home.path());
        assert!(result.is_ok(), "absent config must not error");
        assert!(!config_path(home.path()).exists(), "must not create file");
    }

    #[test]
    fn remove_is_ok_when_leankg_absent_from_existing_file() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(home.path()).unwrap();
        std::fs::write(
            config_path(home.path()),
            serde_json::to_string_pretty(&json!({"mcpServers": {"other": {}}})).unwrap(),
        )
        .unwrap();

        remove(home.path()).unwrap();

        let root = read(home.path());
        assert_eq!(root["mcpServers"]["other"], json!({}));
    }
}
