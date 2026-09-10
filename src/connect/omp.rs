//! OMP (oh-my-pi) client writer: `~/.omp/agent/mcp.json` →
//! `mcpServers.leankg` with `{type:"stdio",command,args,enabled:true}` /
//! `{type:"http",url,enabled:true}` — PRD FR-ZCP-04.

use std::path::{Path, PathBuf};

use super::Transport;

fn config_path(home: &Path) -> PathBuf {
    home.join(".omp").join("agent").join("mcp.json")
}

/// OMP entries carry an explicit `type` key + `enabled` flag.
fn entry(transport: &Transport) -> serde_json::Value {
    match transport {
        Transport::Stdio { command, args } => serde_json::json!({
            "type": "stdio",
            "command": command,
            "args": args,
            "enabled": true,
        }),
        Transport::Http { url } => serde_json::json!({
            "type": "http",
            "url": url,
            "enabled": true,
        }),
    }
}

/// Write (or merge) the leankg entry; returns the config path written.
pub fn apply(home: &Path, transport: &Transport) -> Result<PathBuf, String> {
    let path = config_path(home);
    let mut root = super::read_json_file(&path)?;
    root[super::JSON_CONTAINER_KEY]["leankg"] = entry(transport);
    super::write_json_atomic(&path, &root)?;
    Ok(path)
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
            args: vec!["mcp-stdio".into()],
        }
    }

    fn read(home: &Path) -> serde_json::Value {
        let raw = std::fs::read_to_string(config_path(home)).unwrap();
        serde_json::from_str(&raw).unwrap()
    }

    #[test]
    fn apply_creates_mcp_json_from_scratch() {
        let home = TempDir::new().unwrap();
        let path = apply(home.path(), &sample_stdio()).unwrap();
        assert_eq!(
            path,
            home.path().join(".omp").join("agent").join("mcp.json")
        );
        assert_eq!(
            read(home.path())["mcpServers"]["leankg"],
            json!({"type": "stdio", "command": "/usr/local/bin/leankg", "args": ["mcp-stdio"], "enabled": true})
        );
    }

    #[test]
    fn apply_remote_writes_http_entry() {
        let home = TempDir::new().unwrap();
        apply(
            home.path(),
            &Transport::Http {
                url: "http://localhost:9699/mcp".into(),
            },
        )
        .unwrap();
        assert_eq!(
            read(home.path())["mcpServers"]["leankg"],
            json!({"type": "http", "url": "http://localhost:9699/mcp", "enabled": true})
        );
    }

    #[test]
    fn apply_preserves_siblings() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(
            config_path(home.path()),
            json!({"mcpServers": {"other": {"type": "stdio", "command": "x"}}}).to_string(),
        )
        .unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let root = read(home.path());
        assert!(root["mcpServers"]["other"].is_object(), "sibling preserved");
        assert_eq!(root["mcpServers"]["leankg"]["type"], "stdio");
    }

    #[test]
    fn remove_drops_only_leankg() {
        let home = TempDir::new().unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let path = remove(home.path()).unwrap();
        let root = read(home.path());
        assert!(root["mcpServers"].get("leankg").is_none());
    }
}
