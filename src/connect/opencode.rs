//! OpenCode client writer: `~/.config/opencode/opencode.json` → `mcp.leankg`
//! with `{type:"local",command:[…],enabled:true}` (stdio) or
//! `{type:"remote",url,enabled:true}` (http) — PRD FR-ZCP-04.

use std::path::{Path, PathBuf};

use super::Transport;

fn config_path(home: &Path) -> PathBuf {
    home.join(".config").join("opencode").join("opencode.json")
}

/// Build the opencode-shaped entry for a transport.
fn entry(transport: &Transport) -> serde_json::Value {
    match transport {
        Transport::Stdio { command, args } => {
            let mut cmd = vec![command.clone()];
            cmd.extend(args.clone());
            serde_json::json!({
                "type": "local",
                "command": cmd,
                "enabled": true,
            })
        }
        Transport::Http { url } => serde_json::json!({
            "type": "remote",
            "url": url,
            "enabled": true,
        }),
    }
}

/// Write (or merge) the leankg entry under `mcp.leankg`; returns the config
/// path written. Siblings preserved; atomic tmp+rename; invalid JSON aborts.
pub fn apply(home: &Path, transport: &Transport) -> Result<PathBuf, String> {
    let path = config_path(home);
    let mut root = super::read_json_file(&path)?;
    let entry = entry(transport);
    root["mcp"]["leankg"] = entry;
    super::write_json_atomic(&path, &root)?;
    Ok(path)
}

/// Remove only the leankg entry; Ok even when absent.
pub fn remove(home: &Path) -> Result<PathBuf, String> {
    let path = config_path(home);
    if !path.exists() {
        return Ok(path);
    }
    let mut root = super::read_json_file(&path)?;
    let removed = root
        .get_mut("mcp")
        .and_then(|m| m.as_object_mut())
        .and_then(|m| m.remove(super::SERVER_KEY))
        .is_some();
    if removed {
        super::write_json_atomic(&path, &root)?;
    }
    Ok(path)
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
    fn apply_creates_opencode_config_from_scratch() {
        let home = TempDir::new().unwrap();
        let path = apply(home.path(), &sample_stdio()).unwrap();
        assert_eq!(
            path,
            home.path()
                .join(".config")
                .join("opencode")
                .join("opencode.json")
        );
        let entry = &read(home.path())["mcp"]["leankg"];
        assert_eq!(entry["type"], "local");
        // Flat string array — the json! spread bug produced nested objects.
        assert!(entry["command"]
            .as_array()
            .unwrap()
            .iter()
            .all(|v| v.is_string()));
        assert_eq!(entry["command"][0], "/usr/local/bin/leankg");
        assert_eq!(entry["command"][1], "mcp-stdio");
        assert_eq!(entry["enabled"], true);
    }

    #[test]
    fn apply_remote_writes_remote_entry() {
        let home = TempDir::new().unwrap();
        apply(
            home.path(),
            &Transport::Http {
                url: "http://localhost:9699/mcp".into(),
            },
        )
        .unwrap();
        assert_eq!(
            read(home.path())["mcp"]["leankg"],
            json!({"type": "remote", "url": "http://localhost:9699/mcp", "enabled": true})
        );
    }

    #[test]
    fn apply_preserves_siblings_and_replaces_leankg() {
        let home = TempDir::new().unwrap();
        std::fs::create_dir_all(config_path(home.path()).parent().unwrap()).unwrap();
        std::fs::write(
            config_path(home.path()),
            json!({"mcp": {"other-server": {"type": "local", "command": ["x"]}}}).to_string(),
        )
        .unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let root = read(home.path());
        assert!(root["mcp"]["other-server"].is_object(), "sibling preserved");
        assert_eq!(root["mcp"]["leankg"]["type"], "local");
    }

    #[test]
    fn remove_drops_only_leankg() {
        let home = TempDir::new().unwrap();
        apply(home.path(), &sample_stdio()).unwrap();
        let path = remove(home.path()).unwrap();
        let root = read(home.path());
        assert!(root["mcp"]["leankg"].is_null());
    }
}
