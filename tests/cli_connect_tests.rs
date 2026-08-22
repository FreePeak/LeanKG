//! FR-PLG-1 integration tests: `leankg connect <client>` writes, merges,
//! and removes MCP client configs end-to-end through the real CLI dispatch
//! path (`clap` parse → `connect::run_with_home`) with a fake HOME.

use clap::Parser;
use leankg::cli::CLICommand;
use leankg::connect::{self, Client};
use serde_json::json;
use tempfile::TempDir;

#[derive(Parser)]
struct TestArgs {
    #[command(subcommand)]
    command: CLICommand,
}

/// Parse argv exactly like `main()` would, then execute the Connect
/// dispatch with `home` injected as the home directory.
fn dispatch_connect(argv: &[&str], home: &std::path::Path) -> Result<std::path::PathBuf, String> {
    let args = TestArgs::try_parse_from(std::iter::once("leankg").chain(argv.iter().copied()))
        .map_err(|e| e.to_string())?;
    match args.command {
        CLICommand::Connect {
            client,
            remote,
            remove,
            project,
        } => connect::run_with_home(
            client,
            remote.as_deref(),
            remove,
            project.as_deref().map(std::path::Path::new),
            Some(home.to_path_buf()),
        ),
        other => panic!("expected Connect command, got {other:?}"),
    }
}

fn read_json(path: &std::path::Path) -> serde_json::Value {
    serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap()
}

const CLIENTS: [(&str, Client); 4] = [
    ("claude-code", Client::ClaudeCode),
    ("cursor", Client::Cursor),
    ("codex", Client::Codex),
    ("gemini", Client::Gemini),
];

fn expected_config_path(home: &std::path::Path, client: Client) -> std::path::PathBuf {
    match client {
        Client::ClaudeCode => home.join(".claude.json"),
        Client::Cursor => home.join(".cursor").join("mcp.json"),
        Client::Codex => home.join(".codex").join("config.toml"),
        Client::Gemini => home.join(".gemini").join("settings.json"),
    }
}

#[test]
fn cli_connect_writes_config_for_every_client() {
    let home = TempDir::new().unwrap();
    let project = TempDir::new().unwrap();
    let project_str = project.path().to_str().unwrap();

    for (name, client) in CLIENTS {
        let path = dispatch_connect(&["connect", name, "--project", project_str], home.path())
            .unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(path, expected_config_path(home.path(), client), "{name}");

        match client {
            Client::Codex => {
                let raw = std::fs::read_to_string(&path).unwrap();
                assert!(
                    raw.contains("[mcp_servers.leankg]"),
                    "{name}: missing table in {raw}"
                );
                assert!(raw.contains(project_str), "{name}: project arg missing");
            }
            _ => {
                let root = read_json(&path);
                let entry = &root["mcpServers"]["leankg"];
                assert!(entry["command"].is_string(), "{name}: {root}");
                assert_eq!(entry["args"][0], "mcp-stdio", "{name}");
                assert_eq!(entry["args"][2], project_str, "{name}");
            }
        }
    }
}

#[test]
fn cli_connect_is_idempotent_across_runs() {
    for (name, _) in CLIENTS {
        let home = TempDir::new().unwrap();
        dispatch_connect(&["connect", name], home.path()).unwrap();
        let path =
            expected_config_path(home.path(), CLIENTS.iter().find(|c| c.0 == name).unwrap().1);
        let first = std::fs::read_to_string(&path).unwrap();
        dispatch_connect(&["connect", name], home.path()).unwrap();
        let second = std::fs::read_to_string(&path).unwrap();
        assert_eq!(first, second, "{name}: re-run must be byte-stable");

        // No duplicate server entries.
        if name == "codex" {
            assert_eq!(first.matches("[mcp_servers.leankg]").count(), 1);
        } else {
            let root = read_json(&path);
            assert_eq!(root["mcpServers"].as_object().unwrap().len(), 1);
        }
    }
}

#[test]
fn cli_connect_remove_preserves_sibling_servers() {
    for (name, client) in CLIENTS {
        let home = TempDir::new().unwrap();
        let path = expected_config_path(home.path(), client);

        // Seed a sibling "other" server next to a stale leankg entry.
        match client {
            Client::Codex => {
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(
                    &path,
                    "[mcp_servers.other]\ncommand = \"keep\"\n\n[mcp_servers.leankg]\ncommand = \"stale\"\n",
                )
                .unwrap();
            }
            _ => {
                std::fs::create_dir_all(path.parent().unwrap()).unwrap();
                std::fs::write(
                    &path,
                    serde_json::to_string_pretty(&json!({
                        "mcpServers": {"other": {"command": "keep"}, "leankg": {"command": "stale"}}
                    }))
                    .unwrap(),
                )
                .unwrap();
            }
        }

        dispatch_connect(&["connect", name, "--remove"], home.path())
            .unwrap_or_else(|e| panic!("{name}: {e}"));

        if client == Client::Codex {
            let raw = std::fs::read_to_string(&path).unwrap();
            assert!(!raw.contains("leankg"), "{name}: {raw}");
            assert!(raw.contains("[mcp_servers.other]"), "{name}: {raw}");
            assert!(raw.contains("\"keep\""), "{name}: {raw}");
        } else {
            let root = read_json(&path);
            assert!(root["mcpServers"].get("leankg").is_none(), "{name}");
            assert_eq!(
                root["mcpServers"]["other"]["command"],
                json!("keep"),
                "{name}"
            );
        }
    }
}

#[test]
fn cli_connect_remove_succeeds_when_absent() {
    for (name, client) in CLIENTS {
        let home = TempDir::new().unwrap();
        let result = dispatch_connect(&["connect", name, "--remove"], home.path());
        assert!(result.is_ok(), "{name}: absent removal must succeed");
        // Must not create anything either.
        assert!(
            !expected_config_path(home.path(), client).exists(),
            "{name}"
        );
    }
}

#[test]
fn cli_connect_remote_writes_http_entry() {
    let home = TempDir::new().unwrap();

    // JSON clients get {"type":"http","url":...}.
    let path = dispatch_connect(
        &[
            "connect",
            "claude-code",
            "--remote",
            "http://localhost:9699",
        ],
        home.path(),
    )
    .unwrap();
    assert_eq!(
        read_json(&path)["mcpServers"]["leankg"],
        json!({"type": "http", "url": "http://localhost:9699"})
    );

    // Codex TOML gets url = "...".
    let path = dispatch_connect(
        &["connect", "codex", "--remote", "http://localhost:9699"],
        home.path(),
    )
    .unwrap();
    let raw = std::fs::read_to_string(&path).unwrap();
    assert!(raw.contains("url = \"http://localhost:9699\""), "{raw}");
    assert!(!raw.contains("command"), "{raw}");
}

#[test]
fn cli_connect_unknown_client_rejected_by_clap() {
    let err = TestArgs::try_parse_from(["leankg", "connect", "vscode"])
        .err()
        .expect("unknown client must fail parsing");
    let rendered = err.to_string();
    for valid in ["claude-code", "cursor", "codex", "gemini"] {
        assert!(
            rendered.contains(valid),
            "choices missing '{valid}': {rendered}"
        );
    }
}

#[test]
fn cli_connect_resolves_home_from_environment() {
    let fake_home = TempDir::new().unwrap();
    let original = std::env::var("HOME").ok();

    // Serializes HOME mutation against itself only; all other tests inject
    // an explicit home and never read the env.
    std::env::set_var("HOME", fake_home.path());
    let result = connect::run(Client::Gemini, None, false, None);
    match original {
        Some(home) => std::env::set_var("HOME", home),
        None => std::env::remove_var("HOME"),
    }

    let path = result.expect("env-resolved run failed");
    assert_eq!(path, fake_home.path().join(".gemini").join("settings.json"));
    assert!(path.exists());
}
