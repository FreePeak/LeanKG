//! Codex CLI client writer: `~/.codex/config.toml` → `[mcp_servers.leankg]`.
//!
//! Edits are format-preserving (comments, key order, and sibling tables
//! survive) via `toml_edit`.

use std::path::{Path, PathBuf};

use super::Transport;
use toml_edit::{table, value, Array, DocumentMut, Item, Table};

/// Absolute path of the Codex config for `home`.
fn config_path(home: &Path) -> PathBuf {
    home.join(".codex").join("config.toml")
}

/// Write (or merge) the leankg entry; returns the config path written.
pub fn apply(home: &Path, transport: &Transport) -> Result<PathBuf, String> {
    let path = config_path(home);
    let text = match std::fs::read_to_string(&path) {
        Ok(text) => text,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => String::new(),
        Err(e) => return Err(format!("reading {}: {}", path.display(), e)),
    };
    // DocumentMut preserves every comment, key order, and formatting quirk.
    let mut doc = text
        .parse::<DocumentMut>()
        .map_err(|e| format!("parsing {}: {}", path.display(), e))?;

    if doc.get("mcp_servers").is_none() {
        doc.insert("mcp_servers", table());
    }
    let servers = doc["mcp_servers"]
        .as_table_mut()
        .ok_or_else(|| format!("[mcp_servers] in {} is not a TOML table", path.display()))?;

    let mut entry = Table::new();
    match transport {
        Transport::Stdio { command, args } => {
            entry.insert("command", value(command));
            entry.insert(
                "args",
                value(args.iter().map(String::as_str).collect::<Array>()),
            );
        }
        Transport::Http { url } => {
            entry.insert("url", value(url));
        }
    }
    // Replaces any previous leankg table wholesale → idempotent re-runs.
    servers.insert(super::SERVER_KEY, Item::Table(entry));

    super::atomic_write(&path, doc.to_string().as_bytes())?;
    Ok(path)
}

/// Remove only the leankg entry; Ok even when absent. Untouched files are
/// not rewritten so unrelated bytes stay byte-for-byte identical.
pub fn remove(home: &Path) -> Result<PathBuf, String> {
    let path = config_path(home);
    if !path.exists() {
        return Ok(path);
    }
    let text =
        std::fs::read_to_string(&path).map_err(|e| format!("reading {}: {}", path.display(), e))?;
    let mut doc = text
        .parse::<DocumentMut>()
        .map_err(|e| format!("parsing {}: {}", path.display(), e))?;

    let removed = doc
        .get_mut("mcp_servers")
        .and_then(|item| item.as_table_mut())
        .and_then(|servers| servers.remove(super::SERVER_KEY))
        .is_some();
    if removed {
        super::atomic_write(&path, doc.to_string().as_bytes())?;
    }
    Ok(path)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::connect::Transport;
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

    fn parse(home: &Path) -> DocumentMut {
        std::fs::read_to_string(config_path(home))
            .unwrap()
            .parse::<DocumentMut>()
            .unwrap()
    }

    fn args_of(doc: &DocumentMut) -> Vec<String> {
        doc["mcp_servers"]["leankg"]["args"]
            .as_array()
            .unwrap()
            .iter()
            .map(|item| item.as_str().unwrap().to_string())
            .collect()
    }

    #[test]
    fn apply_creates_toml_from_scratch() {
        let home = TempDir::new().unwrap();
        let path = apply(home.path(), &sample_stdio()).unwrap();
        assert_eq!(path, home.path().join(".codex").join("config.toml"));

        let doc = parse(home.path());
        let entry = doc["mcp_servers"]
            .get("leankg")
            .expect("leankg table must exist");
        assert_eq!(
            entry.get("command").and_then(|item| item.as_str()),
            Some("/usr/local/bin/leankg")
        );
        assert_eq!(
            args_of(&doc),
            vec!["mcp-stdio", "--project", "/tmp/some-project"]
        );
        assert!(entry.get("url").is_none());
    }

    #[test]
    fn apply_is_idempotent_no_duplicate_tables() {
        let home = TempDir::new().unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let after_first = std::fs::read_to_string(config_path(home.path())).unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let after_second = std::fs::read_to_string(config_path(home.path())).unwrap();

        assert_eq!(after_first, after_second);
        assert_eq!(after_first.matches("[mcp_servers.leankg]").count(), 1);
        let doc = parse(home.path());
        let servers = doc["mcp_servers"].as_table().unwrap();
        assert_eq!(servers.len(), 1);
    }

    #[test]
    fn apply_preserves_comments_and_sibling_tables() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        let seeded = r##"# my codex config
model = "o4-mini"

[mcp_servers.other]
command = "foo"   # trailing comment

[profile.fast]
model = "o4-mini-full"
"##;
        std::fs::write(config_path(home.path()), seeded).unwrap();

        apply(home.path(), &sample_stdio()).unwrap();

        let raw = std::fs::read_to_string(config_path(home.path())).unwrap();
        assert!(raw.contains("# my codex config"), "comment lost: {raw}");
        assert!(raw.contains("# trailing comment"), "comment lost: {raw}");

        let doc = parse(home.path());
        assert_eq!(doc["model"].as_str(), Some("o4-mini"));
        assert_eq!(doc["mcp_servers"]["other"]["command"].as_str(), Some("foo"));
        assert_eq!(
            doc["profile"]["fast"]["model"].as_str(),
            Some("o4-mini-full")
        );
        assert_eq!(
            doc["mcp_servers"]["leankg"]["command"].as_str(),
            Some("/usr/local/bin/leankg")
        );
    }

    #[test]
    fn apply_remote_writes_url_key() {
        let home = TempDir::new().unwrap();
        apply(
            home.path(),
            &Transport::Http {
                url: "http://localhost:9699".into(),
            },
        )
        .unwrap();

        let doc = parse(home.path());
        let entry = doc["mcp_servers"]
            .get("leankg")
            .expect("leankg table must exist");
        assert_eq!(
            entry.get("url").and_then(|item| item.as_str()),
            Some("http://localhost:9699")
        );
        assert!(entry.get("command").is_none());
    }

    #[test]
    fn remove_deletes_only_leankg_table() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(
            config_path(home.path()),
            "# keep me\nmodel = \"o4-mini\"\n\n[mcp_servers.other]\ncommand = \"foo\"\n\n[mcp_servers.leankg]\ncommand = \"stale\"\n",
        )
        .unwrap();

        remove(home.path()).unwrap();

        let raw = std::fs::read_to_string(config_path(home.path())).unwrap();
        assert!(raw.contains("# keep me"));
        assert!(!raw.contains("leankg"), "raw: {raw}");
        let doc = parse(home.path());
        assert_eq!(doc["mcp_servers"]["other"]["command"].as_str(), Some("foo"));
        assert_eq!(doc["model"].as_str(), Some("o4-mini"));
    }

    #[test]
    fn remove_is_ok_when_file_absent() {
        let home = TempDir::new().unwrap();
        assert!(remove(home.path()).is_ok());
        assert!(!config_path(home.path()).exists());
    }

    #[test]
    fn remove_is_ok_when_mcp_servers_absent() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(config_path(home.path()), "model = \"o4-mini\"\n").unwrap();

        remove(home.path()).unwrap();

        assert_eq!(parse(home.path())["model"].as_str(), Some("o4-mini"));
    }
}
