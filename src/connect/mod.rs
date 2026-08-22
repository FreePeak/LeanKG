//! FR-PLG-1 `leankg connect`: one-command MCP client setup.
//!
//! Writes (or removes) the LeanKG MCP server entry in the config file of a
//! supported AI client (`claude-code`, `cursor`, `codex`, `gemini`) so agents
//! can talk to LeanKG without hand-editing JSON/TOML. Idempotent: re-running
//! merges rather than duplicates and preserves every sibling key, unknown
//! field, comment, and formatting quirk of the existing config.

pub mod claude_code;
pub mod codex;
pub mod cursor;
pub mod gemini;

use clap::ValueEnum;
use std::path::{Path, PathBuf};

/// Key this tool owns inside the client's MCP server map / table.
pub const SERVER_KEY: &str = "leankg";
/// Container key holding MCP servers in JSON client configs.
const JSON_CONTAINER_KEY: &str = "mcpServers";

/// Target AI client whose config `connect` manages (FR-PLG-1).
#[derive(ValueEnum, Clone, Copy, Debug, PartialEq, Eq)]
pub enum Client {
    /// Claude Code CLI — `~/.claude.json` → `mcpServers.leankg`
    ClaudeCode,
    /// Cursor — `~/.cursor/mcp.json` → `mcpServers.leankg`
    Cursor,
    /// Codex CLI — `~/.codex/config.toml` → `[mcp_servers.leankg]`
    Codex,
    /// Gemini CLI — `~/.gemini/settings.json` → `mcpServers.leankg`
    Gemini,
}

/// Transport advertised in the client's leankg server entry.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Transport {
    /// Local stdio transport: spawn `<command> <args>` (the default).
    Stdio { command: String, args: Vec<String> },
    /// Remote HTTP MCP endpoint (e.g. `http://localhost:9699`).
    Http { url: String },
}

impl Transport {
    /// JSON representation used by claude-code, cursor, and gemini configs.
    fn json_entry(&self) -> serde_json::Value {
        match self {
            Transport::Stdio { command, args } => serde_json::json!({
                "command": command,
                "args": args,
            }),
            Transport::Http { url } => serde_json::json!({
                "type": "http",
                "url": url,
            }),
        }
    }
}

/// Resolve the home directory: test override wins, then `$HOME`, then the
/// `dirs` crate fallback.
fn resolve_home(explicit_home: Option<&Path>) -> PathBuf {
    if let Some(home) = explicit_home {
        return home.to_path_buf();
    }
    if let Ok(home) = std::env::var("HOME") {
        if !home.trim().is_empty() {
            return PathBuf::from(home);
        }
    }
    dirs::home_dir().unwrap_or_else(|| PathBuf::from("."))
}

/// Command path for stdio entries: the current executable when resolvable,
/// else bare `leankg` (relying on PATH).
fn current_command() -> String {
    std::env::current_exe()
        .map(|exe| exe.to_string_lossy().into_owned())
        .unwrap_or_else(|_| "leankg".to_string())
}

/// Make `path` absolute against the current working directory.
fn absolutize(path: &Path) -> PathBuf {
    if path.is_absolute() {
        return path.to_path_buf();
    }
    let cwd = std::env::current_dir().unwrap_or_else(|_| PathBuf::from("."));
    cwd.join(path)
}

/// Build the default stdio transport: `<current exe> mcp-stdio --project
/// <abs project>` where the project defaults to the current working
/// directory.
fn stdio_transport(project: Option<&Path>) -> Transport {
    let project_abs = match project {
        Some(project) => absolutize(project),
        None => std::env::current_dir().unwrap_or_else(|_| PathBuf::from(".")),
    };
    Transport::Stdio {
        command: current_command(),
        args: vec![
            "mcp-stdio".to_string(),
            "--project".to_string(),
            project_abs.to_string_lossy().into_owned(),
        ],
    }
}

/// Write (or merge) the leankg entry for `client`; returns the config path.
fn apply(
    client: Client,
    transport: &Transport,
    explicit_home: Option<&Path>,
) -> Result<PathBuf, String> {
    let home = resolve_home(explicit_home);
    match client {
        Client::ClaudeCode => claude_code::apply(&home, transport),
        Client::Cursor => cursor::apply(&home, transport),
        Client::Codex => codex::apply(&home, transport),
        Client::Gemini => gemini::apply(&home, transport),
    }
}

/// Remove only the leankg entry for `client`; returns the config path.
/// Succeeds even when the entry or the whole config is absent.
fn remove(client: Client, explicit_home: Option<&Path>) -> Result<PathBuf, String> {
    let home = resolve_home(explicit_home);
    match client {
        Client::ClaudeCode => claude_code::remove(&home),
        Client::Cursor => cursor::remove(&home),
        Client::Codex => codex::remove(&home),
        Client::Gemini => gemini::remove(&home),
    }
}

/// Dispatch entrypoint used by the CLI: resolves home from the environment
/// unless overridden (tests), and applies or removes the config, returning
/// the touched config path.
pub fn run_with_home(
    client: Client,
    remote: Option<&str>,
    remove_only: bool,
    project: Option<&Path>,
    explicit_home: Option<PathBuf>,
) -> Result<PathBuf, String> {
    if remove_only {
        if remote.is_some() {
            return Err("--remote and --remove are mutually exclusive".to_string());
        }
        remove(client, explicit_home.as_deref())
    } else {
        let transport = match remote {
            Some(url) => Transport::Http {
                url: url.to_string(),
            },
            None => stdio_transport(project),
        };
        apply(client, &transport, explicit_home.as_deref())
    }
}

/// Like [`run_with_home`] but always resolves HOME from the environment.
pub fn run(
    client: Client,
    remote: Option<&str>,
    remove_only: bool,
    project: Option<&Path>,
) -> Result<PathBuf, String> {
    run_with_home(client, remote, remove_only, project, None)
}

// ---------------------------------------------------------------------------
// Shared JSON plumbing (claude-code / cursor / gemini)
// ---------------------------------------------------------------------------

/// Read a JSON config; missing files read as an empty object so a fresh
/// machine gets a minimal valid config. Existing-but-invalid files are an
/// error — never clobber a broken user config silently.
fn read_json_file(path: &Path) -> Result<serde_json::Value, String> {
    match std::fs::read_to_string(path) {
        Ok(text) => serde_json::from_str(&text)
            .map_err(|e| format!("{} is not valid JSON: {}", path.display(), e)),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(serde_json::json!({})),
        Err(e) => Err(format!("reading {}: {}", path.display(), e)),
    }
}

/// Serialize with 2-space pretty printing plus trailing newline and write
/// atomically (tmp file + rename) after creating parent directories.
fn write_json_atomic(path: &Path, value: &serde_json::Value) -> Result<(), String> {
    let mut body = serde_json::to_string_pretty(value)
        .map_err(|e| format!("serializing {}: {}", path.display(), e))?;
    body.push('\n');
    atomic_write(path, body.as_bytes())
}

/// Atomic file replace: write a temp sibling then rename over the target.
pub(crate) fn atomic_write(path: &Path, contents: &[u8]) -> Result<(), String> {
    use std::io::Write;
    let parent = path
        .parent()
        .filter(|p| !p.as_os_str().is_empty())
        .unwrap_or_else(|| Path::new("."));
    std::fs::create_dir_all(parent).map_err(|e| format!("creating {}: {}", parent.display(), e))?;
    let mut tmp = tempfile::NamedTempFile::new_in(parent)
        .map_err(|e| format!("creating temp file in {}: {}", parent.display(), e))?;
    tmp.write_all(contents)
        .map_err(|e| format!("writing {}: {}", tmp.path().display(), e))?;
    tmp.persist(path)
        .map_err(|e| format!("replacing {}: {}", path.display(), e))?;
    Ok(())
}

/// Merge-or-replace `mcpServers.leankg` at `config_path` and write back
/// atomically; returns the config path.
fn apply_json_config(config_path: &Path, transport: &Transport) -> Result<PathBuf, String> {
    let mut root = read_json_file(config_path)?;
    if !root.is_object() {
        return Err(format!(
            "{} does not contain a JSON object at its root",
            config_path.display()
        ));
    }
    let obj = root.as_object_mut().expect("checked is_object");
    let servers = obj
        .entry(JSON_CONTAINER_KEY)
        .or_insert_with(|| serde_json::json!({}));
    if !servers.is_object() {
        return Err(format!(
            "\"{JSON_CONTAINER_KEY}\" in {} is not a JSON object",
            config_path.display()
        ));
    }
    servers
        .as_object_mut()
        .expect("checked is_object")
        .insert(SERVER_KEY.to_string(), transport.json_entry());
    write_json_atomic(config_path, &root)?;
    Ok(config_path.to_path_buf())
}

/// Drop `mcpServers.leankg` from `config_path` and write back atomically.
/// A missing config file — or one without the leankg entry — is a no-op
/// success so removal stays idempotent and never reformats untouched files.
fn remove_json_server(config_path: &Path) -> Result<PathBuf, String> {
    if !config_path.exists() {
        return Ok(config_path.to_path_buf());
    }
    let mut root = read_json_file(config_path)?;
    let removed = root
        .get_mut(JSON_CONTAINER_KEY)
        .and_then(|servers| servers.as_object_mut())
        .and_then(|servers| servers.remove(SERVER_KEY))
        .is_some();
    if removed {
        write_json_atomic(config_path, &root)?;
    }
    Ok(config_path.to_path_buf())
}

#[cfg(test)]
mod tests {
    use super::*;
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

    #[test]
    fn transport_json_entry_stdio_shape() {
        let entry = sample_stdio().json_entry();
        assert_eq!(
            entry,
            serde_json::json!({
                "command": "/usr/local/bin/leankg",
                "args": ["mcp-stdio", "--project", "/tmp/some-project"]
            })
        );
        assert!(entry.get("type").is_none());
    }

    #[test]
    fn transport_json_entry_http_shape() {
        let entry = Transport::Http {
            url: "http://localhost:9699".into(),
        }
        .json_entry();
        assert_eq!(
            entry,
            serde_json::json!({"type": "http", "url": "http://localhost:9699"})
        );
        assert!(entry.get("command").is_none());
    }

    #[test]
    fn resolve_home_explicit_override_wins() {
        let fake = TempDir::new().unwrap();
        assert_eq!(resolve_home(Some(fake.path())), fake.path().to_path_buf());
    }

    #[test]
    fn current_command_is_non_empty() {
        assert!(!current_command().is_empty());
    }

    #[test]
    fn stdio_transport_uses_cwd_when_project_missing() {
        match stdio_transport(None) {
            Transport::Stdio { command, args } => {
                assert!(!command.is_empty());
                assert_eq!(args.first().map(String::as_str), Some("mcp-stdio"));
                let project = args.last().unwrap();
                assert!(Path::new(project).is_absolute(), "project must be absolute");
            }
            other => panic!("expected stdio transport, got {other:?}"),
        }
    }

    #[test]
    fn stdio_transport_absolutizes_relative_project() {
        match stdio_transport(Some(Path::new("."))) {
            Transport::Stdio { args, .. } => {
                let project = args.last().unwrap();
                assert!(Path::new(project).is_absolute());
            }
            other => panic!("expected stdio transport, got {other:?}"),
        }
    }

    #[test]
    fn apply_dispatches_per_client() {
        let home = TempDir::new().unwrap();
        let t = sample_stdio();

        let written = apply(Client::ClaudeCode, &t, Some(home.path())).unwrap();
        assert_eq!(written, home.path().join(".claude.json"));
        let root: serde_json::Value =
            serde_json::from_str(&std::fs::read_to_string(&written).unwrap()).unwrap();
        assert_eq!(
            root["mcpServers"]["leankg"]["command"],
            "/usr/local/bin/leankg"
        );

        let written = apply(Client::Cursor, &t, Some(home.path())).unwrap();
        assert_eq!(written, home.path().join(".cursor").join("mcp.json"));

        let written = apply(Client::Codex, &t, Some(home.path())).unwrap();
        assert_eq!(written, home.path().join(".codex").join("config.toml"));
        let raw = std::fs::read_to_string(&written).unwrap();
        assert!(raw.contains("[mcp_servers.leankg]"), "raw: {raw}");

        let written = apply(Client::Gemini, &t, Some(home.path())).unwrap();
        assert_eq!(written, home.path().join(".gemini").join("settings.json"));
    }

    #[test]
    fn remove_dispatches_per_client_and_tolerates_absence() {
        let home = TempDir::new().unwrap();
        for client in [
            Client::ClaudeCode,
            Client::Cursor,
            Client::Codex,
            Client::Gemini,
        ] {
            // Absent config still succeeds (idempotent removal).
            remove(client, Some(home.path())).unwrap();
        }
    }

    #[test]
    fn run_with_home_writes_then_removes() {
        let home = TempDir::new().unwrap();
        let path = run_with_home(
            Client::Cursor,
            Some("http://localhost:9699"),
            false,
            None,
            Some(home.path().to_path_buf()),
        )
        .unwrap();
        let raw = std::fs::read_to_string(&path).unwrap();
        assert!(raw.contains("http://localhost:9699"), "raw: {raw}");

        let path = run_with_home(
            Client::Cursor,
            None,
            true,
            None,
            Some(home.path().to_path_buf()),
        )
        .unwrap();
        let root: serde_json::Value =
            serde_json::from_str(&std::fs::read_to_string(&path).unwrap()).unwrap();
        assert!(root["mcpServers"].get(SERVER_KEY).is_none());
    }
}
