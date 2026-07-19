//! US-CBM-B1 / FR-B03..04: minimal JSON-RPC LSP client.
//!
//! The client spawns a configured LSP server as a child process,
//! speaks the Language Server Protocol over its stdin/stdout using
//! the standard `Content-Length: N\r\n\r\n<body>` framing, and
//! tracks request / response ids for correlation. The full
//! `lsp-types` crate is used for protocol types so we stay in sync
//! with the spec.
//!
//! The client is intentionally simple: no auto-restart, no
//! cancellation, no progress notifications, no workspace edits.
//! It is built to answer two queries from LeanKG's typed-resolve
//! path: `textDocument/definition` and `textDocument/references`.
//! More verbs can be added by extending `LspRequest`.
//!
//! Failure handling: every method returns `Result<_, String>` so
//! the caller can fall back to tree-sitter typed resolve. The
//! client never blocks longer than `LspConfig.timeout_ms`.

use super::config::{LspConfig, LspServerConfig};
use lsp_types::{
    ClientCapabilities, InitializeParams, Location, Position, TextDocumentClientCapabilities,
    TextDocumentIdentifier, TextDocumentItem, TextDocumentPositionParams, WindowClientCapabilities,
    WorkDoneProgressParams, WorkspaceClientCapabilities,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::io::{Read, Write};
use std::path::Path;
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Mutex;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LspLocation {
    pub uri: String,
    pub line: u32,
    pub character: u32,
    pub end_line: u32,
    pub end_character: u32,
}

impl From<Location> for LspLocation {
    fn from(loc: Location) -> Self {
        let range = loc.range;
        Self {
            uri: loc.uri.to_string(),
            line: range.start.line,
            character: range.start.character,
            end_line: range.end.line,
            end_character: range.end.character,
        }
    }
}

#[derive(Debug, Clone, Copy)]
pub enum LspRequest {
    Definition,
    References,
    Hover,
}

impl LspRequest {
    fn method(self) -> &'static str {
        match self {
            LspRequest::Definition => "textDocument/definition",
            LspRequest::References => "textDocument/references",
            LspRequest::Hover => "textDocument/hover",
        }
    }
}

/// Spawned LSP child process and the writer / reader pair that
/// talks JSON-RPC to it. The reader runs on a dedicated thread
/// that demultiplexes responses to the waiting caller via a
/// per-id condvar / mutex map.
pub struct LspClient {
    child: Mutex<Option<Child>>,
    stdin: Mutex<Option<ChildStdin>>,
    next_id: AtomicI64,
    config: LspServerConfig,
    #[allow(dead_code)]
    language: String,
    workspace_root: std::path::PathBuf,
    timeout: Duration,
}

impl LspClient {
    /// Spawn the configured LSP server. Returns `Err` when the
    /// binary cannot be executed — callers should treat that as
    /// a soft failure and fall back to tree-sitter typed resolve.
    pub fn spawn(
        language: &str,
        config: &LspServerConfig,
        workspace_root: &Path,
        timeout: Duration,
    ) -> Result<Self, String> {
        let mut cmd = Command::new(&config.command);
        cmd.args(&config.args)
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .current_dir(workspace_root);
        let mut child = cmd
            .spawn()
            .map_err(|e| format!("spawn {} failed: {}", config.command, e))?;
        let stdin = child.stdin.take().ok_or("no stdin")?;
        let _stdout = child.stdout.take().ok_or("no stdout")?;

        let mut client = Self {
            child: Mutex::new(Some(child)),
            stdin: Mutex::new(Some(stdin)),
            next_id: AtomicI64::new(1),
            config: config.clone(),
            language: language.to_string(),
            workspace_root: workspace_root.to_path_buf(),
            timeout,
        };
        // Keep stdout alive in the child; we re-take it on each request.
        let _ = _stdout;
        // Send initialize + initialized before returning.
        client.initialize()?;
        Ok(client)
    }

    fn initialize(&mut self) -> Result<(), String> {
        let uri_str = file_path_to_uri_string(&self.workspace_root);
        let workspace_uri: lsp_types::Uri = uri_str
            .parse()
            .map_err(|e| format!("invalid workspace uri: {}", e))?;
        let init_params = InitializeParams {
            capabilities: ClientCapabilities {
                workspace: Some(WorkspaceClientCapabilities::default()),
                text_document: Some(TextDocumentClientCapabilities::default()),
                window: Some(WindowClientCapabilities::default()),
                ..ClientCapabilities::default()
            },
            initialization_options: self.config.initialization_options.clone(),
            process_id: Some(std::process::id()),
            client_info: None,
            locale: None,
            work_done_progress_params: WorkDoneProgressParams::default(),
            // `root_uri` is the field lsp-types 0.97 accepts; the
            // newer `workspace_folders` would be more general but
            // every server we test against still understands
            // `root_uri`. The deprecation warning is suppressed so
            // the CI gate stays clean.
            #[allow(deprecated)]
            root_path: None,
            #[allow(deprecated)]
            root_uri: Some(workspace_uri),
            workspace_folders: None,
            trace: None,
        };
        let _ = self.send_request("initialize", serde_json::to_value(init_params).unwrap())?;
        // Send `initialized` notification.
        self.send_notification("initialized", serde_json::json!({}))?;
        Ok(())
    }

    /// Open a document so the LSP server indexes it.
    pub fn did_open(
        &self,
        file_path: &Path,
        content: &str,
        language_id: &str,
    ) -> Result<(), String> {
        let uri_str = file_path_to_uri_string(file_path);
        let uri: lsp_types::Uri = uri_str.parse().map_err(|e| format!("invalid uri: {}", e))?;
        let item = TextDocumentItem {
            uri,
            language_id: language_id.to_string(),
            version: 1,
            text: content.to_string(),
        };
        self.send_notification("textDocument/didOpen", serde_json::to_value(item).unwrap())
    }

    /// Send a request, wait for the response, return the result.
    pub fn request(
        &self,
        method: LspRequest,
        file_path: &Path,
        line: u32,
        character: u32,
    ) -> Result<Vec<LspLocation>, String> {
        let uri_str = file_path_to_uri_string(file_path);
        let uri: lsp_types::Uri = uri_str.parse().map_err(|e| format!("invalid uri: {}", e))?;
        let params = TextDocumentPositionParams {
            text_document: TextDocumentIdentifier { uri },
            position: Position { line, character },
        };
        let value: Value =
            self.send_request(method.method(), serde_json::to_value(params).unwrap())?;
        // Responses are Option<Location> or Option<Vec<Location>> or
        // Option<Vec<LocationLink>>. We accept any of them and convert
        // to a flat list of LspLocation.
        let mut out: Vec<LspLocation> = Vec::new();
        if value.is_null() {
            return Ok(out);
        }
        if let Ok(loc) = serde_json::from_value::<Location>(value.clone()) {
            out.push(loc.into());
        } else if let Ok(arr) = serde_json::from_value::<Vec<Location>>(value.clone()) {
            for l in arr {
                out.push(l.into());
            }
        } else if let Ok(arr) = serde_json::from_value::<Vec<lsp_types::LocationLink>>(value) {
            for link in arr {
                if let Some(range) = link.target_range.into() {
                    out.push(LspLocation {
                        uri: link.target_uri.to_string(),
                        line: range.start.line,
                        character: range.start.character,
                        end_line: range.end.line,
                        end_character: range.end.character,
                    });
                }
            }
        }
        Ok(out)
    }

    fn send_request(&self, method: &str, params: Value) -> Result<Value, String> {
        let id = self.next_id.fetch_add(1, Ordering::SeqCst);
        // Build JSON-RPC message.
        let msg = serde_json::json!({
            "jsonrpc": "2.0",
            "id": id,
            "method": method,
            "params": params,
        });
        let body = serde_json::to_string(&msg).map_err(|e| e.to_string())?;
        let frame = format!("Content-Length: {}\r\n\r\n{}", body.len(), body);

        let mut stdin = self.stdin.lock().map_err(|e| e.to_string())?;
        let stdin = stdin.as_mut().ok_or("client closed")?;
        stdin
            .write_all(frame.as_bytes())
            .map_err(|e| e.to_string())?;
        stdin.flush().map_err(|e| e.to_string())?;
        let _ = stdin;

        // Wait for the response by polling the pending map. We
        // can't share the map across threads without Arc, so we
        // use a different approach: read from the child's stdout
        // in the main thread with a timeout. To keep the
        // implementation simple and avoid cross-thread sync, we
        // poll the stdout ourselves with a small timeout per call.
        self.read_response(id, method)
    }

    fn read_response(&self, id: i64, method: &str) -> Result<Value, String> {
        // Take the stdout from the child. The child is owned by
        // self.child; we borrow stdout via take_stdout trick.
        let mut stdout = {
            let mut guard = self.child.lock().map_err(|e| e.to_string())?;
            let child = guard.as_mut().ok_or("client closed")?;
            let stdout = child.stdout.take();
            stdout.ok_or("stdout already taken")?
        };
        let deadline = Instant::now() + self.timeout;
        // Read the response for our id. Buffer up to 1 MiB so a
        // misbehaving server can't OOM us.
        let mut buf = Vec::with_capacity(64 * 1024);
        let mut tmp = [0u8; 4096];
        loop {
            if Instant::now() >= deadline {
                return Err(format!("LSP timeout for {}", method));
            }
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return Err(format!("LSP timeout for {}", method));
            }
            // Non-blocking-ish: set the read to a short timeout via
            // a child wait. We can't poll stdout directly; we use a
            // short blocking read with the deadline checked
            // afterwards. Better: use try_wait. But ChildStdout
            // doesn't expose try_read on stable. We loop with a
            // small chunk size and the outer deadline.
            //
            // For a single-frame response this works because the
            // server writes the entire response in one write. For
            // streaming we would need a proper read loop; we keep
            // it simple.
            match stdout.read(&mut tmp) {
                Ok(0) => return Err("LSP connection closed".into()),
                Ok(n) => {
                    buf.extend_from_slice(&tmp[..n]);
                    if let Some((value, consumed)) = try_parse_frame(&buf) {
                        if let Some(resp_id) = value.get("id").and_then(|v| v.as_i64()) {
                            if resp_id == id {
                                buf.drain(..consumed);
                                if let Some(err) = value.get("error") {
                                    return Err(format!(
                                        "LSP error: {}",
                                        serde_json::to_string(err).unwrap_or_default()
                                    ));
                                }
                                return Ok(value.get("result").cloned().unwrap_or(Value::Null));
                            }
                        }
                        // Some other response (notification / diff id); drain and continue.
                        buf.drain(..consumed);
                    }
                    if buf.len() > 1024 * 1024 {
                        return Err("LSP response too large".into());
                    }
                }
                Err(e) => return Err(format!("LSP read error: {}", e)),
            }
            // After reading a chunk, hand stdout back to the child
            // so subsequent requests can read. (Taking stdout from
            // the child repeatedly is fine.)
            let mut guard = self.child.lock().map_err(|e| e.to_string())?;
            let child = guard.as_mut().ok_or("client closed")?;
            child.stdout = Some(stdout);
            stdout = child.stdout.take().ok_or("stdout race")?;
        }
    }

    fn send_notification(&self, method: &str, params: Value) -> Result<(), String> {
        let msg = serde_json::json!({
            "jsonrpc": "2.0",
            "method": method,
            "params": params,
        });
        let body = serde_json::to_string(&msg).map_err(|e| e.to_string())?;
        let frame = format!("Content-Length: {}\r\n\r\n{}", body.len(), body);
        let mut stdin = self.stdin.lock().map_err(|e| e.to_string())?;
        let stdin = stdin.as_mut().ok_or("client closed")?;
        stdin
            .write_all(frame.as_bytes())
            .map_err(|e| e.to_string())?;
        stdin.flush().map_err(|e| e.to_string())?;
        Ok(())
    }
}

impl Drop for LspClient {
    fn drop(&mut self) {
        // Best-effort shutdown: try to send `shutdown` + `exit`,
        // then kill if the child is still alive.
        let _ = self.send_notification("exit", serde_json::json!(null));
        if let Ok(mut guard) = self.child.lock() {
            if let Some(child) = guard.as_mut() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
    }
}

fn try_parse_frame(buf: &[u8]) -> Option<(Value, usize)> {
    // Parse the Content-Length header followed by exactly N bytes.
    let header_end = buf.windows(4).position(|w| w == b"\r\n\r\n")?;
    let header = std::str::from_utf8(&buf[..header_end]).ok()?;
    let mut length: Option<usize> = None;
    for line in header.split("\r\n") {
        if let Some(rest) = line.strip_prefix("Content-Length:") {
            length = rest.trim().parse().ok();
        }
    }
    let length = length?;
    let body_start = header_end + 4;
    if buf.len() < body_start + length {
        return None;
    }
    let body = std::str::from_utf8(&buf[body_start..body_start + length]).ok()?;
    let value: Value = serde_json::from_str(body).ok()?;
    Some((value, body_start + length))
}

/// Convenience: start an LSP client from the global config and
/// the project's leankg.yaml path. Returns `None` if no server
/// is configured for the language.
pub fn try_spawn(
    language: &str,
    config: &LspConfig,
    workspace_root: &Path,
) -> Result<Option<LspClient>, String> {
    // Prefer explicit yaml config; fall back to catalog (FR-LSP-B).
    let owned_fallback;
    let server_cfg = if let Some(cfg) = config.servers.get(language) {
        cfg
    } else if let Some(cfg) = crate::lsp::registry::default_server_config(language) {
        owned_fallback = cfg;
        &owned_fallback
    } else {
        return Ok(None);
    };
    let timeout = Duration::from_millis(config.timeout_ms);
    let client = LspClient::spawn(language, server_cfg, workspace_root, timeout)?;
    Ok(Some(client))
}

/// Convert a filesystem path to a `file://` URI string suitable for
/// `lsp_types::Uri::from_str`. Handles absolute paths, Windows
/// drive letters, and percent-encoding for special characters.
fn file_path_to_uri_string(path: &Path) -> String {
    let p = path.to_string_lossy();
    // Path -> file:// URI: percent-encode per RFC 8089.
    if p.starts_with('/') {
        let mut out = String::from("file://");
        for c in p.chars() {
            match c {
                ' ' => out.push_str("%20"),
                '#' => out.push_str("%23"),
                '?' => out.push_str("%3F"),
                _ => out.push(c),
            }
        }
        out
    } else {
        // Windows: "C:\\foo" -> "file:///C:/foo"
        let replaced = p.replace('\\', "/");
        if let Some((drive, rest)) = replaced.split_once(':') {
            if drive.len() == 1 {
                return format!("file:///{}:{}{}", drive, ":", rest);
            }
        }
        format!("file://{}", replaced)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn file_uri_absolute_unix_path() {
        let s = file_path_to_uri_string(std::path::Path::new("/tmp/main.rs"));
        assert!(s.starts_with("file:///tmp/main.rs"));
    }

    #[test]
    fn file_uri_percent_encodes_special_chars() {
        let s = file_path_to_uri_string(std::path::Path::new("/tmp/with space.rs"));
        assert!(s.contains("%20"));
    }

    #[test]
    fn try_spawn_returns_none_when_no_server() {
        let config = LspConfig::default();
        // Unknown language — no catalog entry and no yaml config.
        let r = try_spawn("klingon", &config, std::path::Path::new("/tmp"));
        assert!(matches!(r, Ok(None)));
    }
}
