//! Gemini CLI client writer: `~/.gemini/settings.json` → `mcpServers.leankg`.

use std::path::{Path, PathBuf};

use super::Transport;

/// Absolute path of the Gemini CLI settings for `home`.
fn config_path(home: &Path) -> PathBuf {
    home.join(".gemini").join("settings.json")
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
    fn apply_creates_settings_from_scratch() {
        let home = TempDir::new().unwrap();
        let path = apply(home.path(), &sample_stdio()).unwrap();
        assert_eq!(path, home.path().join(".gemini").join("settings.json"));

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

        assert_eq!(after_first, after_second);
        assert_eq!(
            read(home.path())["mcpServers"].as_object().unwrap().len(),
            1
        );
    }

    #[test]
    fn apply_preserves_sibling_servers_and_unknown_fields() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(
            config_path(home.path()),
            serde_json::to_string_pretty(&json!({
                "mcpServers": {"other": {"command": "foo"}},
                "theme": {"name": "auto"},
                "model": "gemini-2.5-pro"
            }))
            .unwrap(),
        )
        .unwrap();

        apply(home.path(), &sample_stdio()).unwrap();

        let root = read(home.path());
        assert_eq!(root["mcpServers"]["other"]["command"], "foo");
        assert_eq!(root["theme"]["name"], "auto");
        assert_eq!(root["model"], "gemini-2.5-pro");
        assert!(root["mcpServers"]["leankg"]["command"].is_string());
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

        assert_eq!(
            read(home.path())["mcpServers"]["leankg"],
            json!({"type": "http", "url": "http://localhost:9699"})
        );
    }

    #[test]
    fn remove_deletes_only_leankg_and_keeps_siblings() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(
            config_path(home.path()),
            serde_json::to_string_pretty(&json!({
                "mcpServers": {"other": {}, "leankg": {}},
                "model": "x"
            }))
            .unwrap(),
        )
        .unwrap();

        remove(home.path()).unwrap();

        let root = read(home.path());
        assert!(root["mcpServers"].get("leankg").is_none());
        assert_eq!(root["model"], "x");
    }

    #[test]
    fn remove_is_ok_when_file_absent() {
        let home = TempDir::new().unwrap();
        assert!(remove(home.path()).is_ok());
        assert!(!config_path(home.path()).exists());
    }
}
