//! FR-ZCP-01 clause 2: server-initiated MCP `roots/list` for HTTP project
//! resolution (PRD v4.3.0 §3.1, resolution step 2 of the HTTP arm).
//!
//! Streamable-HTTP clients (OMP's pi-coding-agent, OpenCode, the official
//! TS SDK) answer a server-to-client `roots/list` request with
//! `file://<cwd>`. LeanKG asks once during the initialize exchange: the
//! initialize SSE response carries a second `event: message` frame holding
//! the `roots/list` JSON-RPC request, and the client answers it with an
//! ordinary `POST /mcp` that echoes the `Mcp-Session-Id`. The resolved
//! root is cached per session so the per-request FS walk never repeats.
//!
//! A client that does not advertise the `roots` capability, or that
//! errors / never answers, degrades silently: resolution falls through to
//! the legacy `?project=` parameter (and then the FS walk).

use std::path::PathBuf;
use std::sync::atomic::{AtomicI64, Ordering};
use std::time::Duration;

use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

/// Give the client time to answer `roots/list` before the session cache
/// entry is dropped and the connection falls back to `?project=` routing.
pub const ROOTS_ANSWER_WINDOW: Duration = Duration::from_secs(10);

/// Monotonic source for server-to-client JSON-RPC request ids.
static NEXT_PENDING_ID: AtomicI64 = AtomicI64::new(1);

/// Root as reported by the client: MCP `Root` object (`{uri, name?}`).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ClientRoot {
    pub uri: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub name: Option<String>,
}

/// Server-to-client `roots/list` JSON-RPC request frame. Kept serializable
/// so tests can pin the exact wire shape.
#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct RootsListRequest {
    pub jsonrpc: &'static str,
    pub id: i64,
    pub method: &'static str,
    pub params: RootsListParams,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct RootsListParams {
    /// Opaque marker so log analysis can trace the request without
    /// implying the client must echo it back (spec: params SHOULD be `{}`).
    #[serde(rename = "_meta")]
    pub meta: RootsListMeta,
}

#[derive(Debug, Clone, PartialEq, Serialize)]
pub struct RootsListMeta {
    /// MCP wire format is camelCase; `progressToken` is the spec'd name.
    #[serde(rename = "progressToken")]
    pub progress_token: String,
}

/// Build the server-initiated `roots/list` request. One frame per call;
/// each request carries a fresh id + progress token so concurrent
/// initializes never collide.
pub fn build_roots_list_request() -> RootsListRequest {
    RootsListRequest {
        jsonrpc: "2.0",
        id: NEXT_PENDING_ID.fetch_add(1, Ordering::Relaxed),
        method: "roots/list",
        params: RootsListParams {
            meta: RootsListMeta {
                progress_token: format!("leankg-roots-{}", Uuid::new_v4()),
            },
        },
    }
}

/// Does the initialize params advertise the roots capability? True for
/// `"roots": {}`, `{"listChanged": true}`, or `{"supported": true}`-style
/// shapes — anything object-shaped counts; a missing/null/non-object
/// capability does not.
pub fn client_supports_roots(capabilities: Option<&serde_json::Value>) -> bool {
    capabilities
        .and_then(|c| c.get("roots"))
        .map(|r| r.is_object())
        .unwrap_or(false)
}

/// Does the roots capability additionally declare `listChanged`?
pub fn client_supports_roots_list_changed(capabilities: Option<&serde_json::Value>) -> bool {
    capabilities
        .and_then(|c| c.get("roots"))
        .and_then(|r| r.get("listChanged"))
        .and_then(|v| v.as_bool())
        .unwrap_or(false)
}

/// Extract the project working directory from a `roots/list` response
/// payload. Accepts both the raw `params.result` (`{"roots": [...]}`) and
/// the full JSON-RPC response envelope. Takes the FIRST file:// root;
/// non-file schemes (http://, urn:, …) are rejected. Empty root list or
/// no usable file:// root yields `None` (graceful degradation).
pub fn project_root_from_roots_response(payload: &serde_json::Value) -> Option<PathBuf> {
    // Locate the roots array: either `payload.roots` (direct result) or
    // `payload.result.roots` (full JSON-RPC envelope).
    let roots = payload
        .get("roots")
        .or_else(|| payload.get("result").and_then(|r| r.get("roots")))
        .and_then(|v| v.as_array())?;

    for entry in roots {
        let parsed: Option<ClientRoot> = serde_json::from_value(entry.clone()).ok();
        if let Some(root) = parsed {
            if let Some(path) = file_uri_to_path(&root.uri) {
                return Some(path);
            }
        }
    }
    None
}

/// Convert a `file://` URI to a filesystem path. Returns `None` for any
/// other scheme. Handles the hostless form (`file:///abs/path`) and
/// percent-encoded segments; a non-empty host (e.g. `file://server/share`)
/// is rejected because it is not a local path this process can resolve.
pub fn file_uri_to_path(uri: &str) -> Option<PathBuf> {
    let rest = uri.strip_prefix("file://")?;
    if rest.is_empty() {
        return None;
    }
    // file:///abs/path → path continues after the double slash; a
    // non-empty host (file://host/path) is a remote share → rejected.
    let path = format!("/{}", rest.strip_prefix('/')?);
    if path == "/" || path.is_empty() {
        return None;
    }
    let decoded = percent_decode(&path)?;
    let buf = PathBuf::from(decoded);
    if buf.is_absolute() {
        Some(buf)
    } else {
        None
    }
}

/// Minimal percent-decoding (%XX → byte) for URI path components.
/// Invalid escapes are passed through verbatim (lenient, like the query
/// param decoder in `handle_mcp_request`).
fn percent_decode(input: &str) -> Option<String> {
    let bytes = input.as_bytes();
    let mut out: Vec<u8> = Vec::with_capacity(bytes.len());
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' && i + 2 < bytes.len() {
            let hex = std::str::from_utf8(&bytes[i + 1..i + 3]).ok()?;
            if let Ok(byte) = u8::from_str_radix(hex, 16) {
                out.push(byte);
                i += 3;
                continue;
            }
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8(out).ok()
}

/// Per-session root cache (FR-ZCP-01: "Resolution is cached per
/// connection"). Keyed by the `Mcp-Session-Id` the server allocated at
/// initialize.
#[derive(Default)]
pub struct SessionRootCache {
    entries: RwLock<std::collections::HashMap<String, SessionRoot>>,
}

impl Clone for SessionRootCache {
    fn clone(&self) -> Self {
        Self {
            entries: RwLock::new(self.entries.read().clone()),
        }
    }
}

#[derive(Debug, Clone)]
struct SessionRoot {
    /// The raw first root reported by the client (pre-resolution).
    root: PathBuf,
    /// True once the client answered `roots/list`; false while the probe
    /// is still in flight. Entries for unanswered sessions expire via
    /// [`SessionRootCache::settle`].
    answered: bool,
    /// Client declared `roots.listChanged` — refresh on the notification.
    list_changed: bool,
}

impl SessionRootCache {
    pub fn new() -> Self {
        Self::default()
    }

    /// Record the session the server just initialized, with the roots
    /// capability it declared. The entry starts unanswered so
    /// `pending_root` can distinguish "no answer yet" from "client has no
    /// usable roots" while the probe is in flight.
    pub fn register_session(&self, session_id: &str, list_changed: bool) {
        self.entries.write().insert(
            session_id.to_string(),
            SessionRoot {
                root: PathBuf::new(),
                answered: false,
                list_changed,
            },
        );
    }

    /// Store the client's answer. `root = None` records a definitive
    /// "client answered with nothing usable" so later lookups stop
    /// waiting on the probe.
    pub fn set_root(&self, session_id: &str, root: Option<PathBuf>) {
        let mut entries = self.entries.write();
        if let Some(entry) = entries.get_mut(session_id) {
            entry.answered = true;
            if let Some(root) = root {
                entry.root = root;
            }
        }
        // Unknown session: ignore. The probe only rides streams the
        // server itself initialized, so this is not reachable in practice.
    }

    /// The session's root if known and the probe has been answered.
    /// Unanswered sessions return `None` so resolution falls through to
    /// `?project=` while the probe is still in flight.
    pub fn root_for_session(&self, session_id: &str) -> Option<PathBuf> {
        let entries = self.entries.read();
        let entry = entries.get(session_id)?;
        if !entry.answered {
            return None;
        }
        if entry.root.as_os_str().is_empty() {
            None
        } else {
            Some(entry.root.clone())
        }
    }

    /// Drop the cached root for a session whose probe window elapsed
    /// without an answer (or whose answer never came) so the map does not
    /// grow with dead sessions. Returns true when an unanswered entry was
    /// removed.
    pub fn settle(&self, session_id: &str) -> bool {
        let mut entries = self.entries.write();
        match entries.get(session_id) {
            Some(entry) if !entry.answered => {
                entries.remove(session_id);
                true
            }
            _ => false,
        }
    }

    /// Did this session's client declare `roots.listChanged`? Only such
    /// sessions react to `notifications/roots/list_changed` by re-probing;
    /// others cache for the connection lifetime.
    pub fn wants_list_changed(&self, session_id: &str) -> bool {
        self.entries
            .read()
            .get(session_id)
            .map(|e| e.list_changed)
            .unwrap_or(false)
    }

    /// `notifications/roots/list_changed` arrived: forget the cached root
    /// so the next initialize-like re-probe (or the next explicit refresh)
    /// picks up the new cwd. Only meaningful when the client declared
    /// `roots.listChanged`; returns true when the cache entry existed.
    pub fn invalidate(&self, session_id: &str) -> bool {
        let mut entries = self.entries.write();
        if entries.remove(session_id).is_some() {
            // Re-register as pending so in-flight probes can still land.
            entries.insert(
                session_id.to_string(),
                SessionRoot {
                    root: PathBuf::new(),
                    answered: false,
                    list_changed: true,
                },
            );
            true
        } else {
            false
        }
    }

    /// Drop all sessions (used by tests and shutdown paths).
    pub fn clear(&self) {
        self.entries.write().clear();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    // ------------------------------------------------------------------
    // Request shape
    // ------------------------------------------------------------------

    #[test]
    fn fr_zcp01_roots_list_request_wire_shape() {
        let req = build_roots_list_request();
        assert_eq!(req.jsonrpc, "2.0");
        assert_eq!(req.method, "roots/list");
        // Server-to-client requests carry a numeric id and (empty) params.
        let value = serde_json::to_value(&req).unwrap();
        assert_eq!(value["jsonrpc"], "2.0");
        assert!(value["id"].is_i64(), "id must be a number");
        assert_eq!(value["method"], "roots/list");
        assert!(value["params"].is_object());
        // Ids are unique across calls.
        let req2 = build_roots_list_request();
        assert_ne!(req.id, req2.id, "concurrent probes must not share ids");
    }

    #[test]
    fn fr_zcp01_roots_list_request_serializes_params_object() {
        let req = build_roots_list_request();
        let value = serde_json::to_value(&req).unwrap();
        // progressToken rides _meta so concurrent answers are traceable.
        assert!(value["params"]["_meta"]["progressToken"]
            .as_str()
            .unwrap()
            .starts_with("leankg-roots-"));
    }

    // ------------------------------------------------------------------
    // Capability detection
    // ------------------------------------------------------------------

    #[test]
    fn fr_zcp01_capability_detection() {
        let caps = json!({"roots": {"listChanged": true}});
        assert!(client_supports_roots(Some(&caps)));
        assert!(client_supports_roots_list_changed(Some(&caps)));

        let caps_empty_roots = json!({"roots": {}});
        assert!(client_supports_roots(Some(&caps_empty_roots)));
        assert!(!client_supports_roots_list_changed(Some(&caps_empty_roots)));

        let caps_no_roots = json!({"tools": {"listChanged": true}});
        assert!(!client_supports_roots(Some(&caps_no_roots)));
        assert!(!client_supports_roots_list_changed(Some(&caps_no_roots)));

        // OMP sends `capabilities: {}` — no roots capability at all.
        assert!(!client_supports_roots(Some(&json!({}))));
        assert!(!client_supports_roots(None));
        // Non-object roots capability is not a capability declaration.
        assert!(!client_supports_roots(Some(&json!({"roots": true}))));
        assert!(!client_supports_roots(Some(&json!({"roots": null}))));
    }

    // ------------------------------------------------------------------
    // file:// URI parsing
    // ------------------------------------------------------------------

    #[test]
    fn fr_zcp01_file_uri_single_root() {
        assert_eq!(
            file_uri_to_path("file:///Users/dev/repo"),
            Some(PathBuf::from("/Users/dev/repo"))
        );
    }

    #[test]
    fn fr_zcp01_file_uri_percent_encoded() {
        assert_eq!(
            file_uri_to_path("file:///Users/dev/My%20Projects/repo"),
            Some(PathBuf::from("/Users/dev/My Projects/repo"))
        );
    }

    #[test]
    fn fr_zcp01_file_uri_non_file_schemes_rejected() {
        assert_eq!(file_uri_to_path("http://example.com/repo"), None);
        assert_eq!(file_uri_to_path("https://example.com/repo"), None);
        assert_eq!(file_uri_to_path("urn:isbn:0451450523"), None);
        assert_eq!(file_uri_to_path("ftp://host/path"), None);
        assert_eq!(file_uri_to_path("/plain/absolute/path"), None);
        assert_eq!(file_uri_to_path("relative/path"), None);
        assert_eq!(file_uri_to_path(""), None);
    }

    #[test]
    fn fr_zcp01_file_uri_remote_host_rejected() {
        // file://server/share is a remote share, not a local path.
        assert_eq!(file_uri_to_path("file://server/share"), None);
        // Bare file:/// (filesystem root) is not a usable project root.
        assert_eq!(file_uri_to_path("file:///"), None);
    }

    // ------------------------------------------------------------------
    // Response payload parsing
    // ------------------------------------------------------------------

    #[test]
    fn fr_zcp01_first_file_root_wins() {
        let result = json!({"roots": [
            {"uri": "file:///repo-a", "name": "A"},
            {"uri": "file:///repo-b"}
        ]});
        assert_eq!(
            project_root_from_roots_response(&result),
            Some(PathBuf::from("/repo-a"))
        );
    }

    #[test]
    fn fr_zcp01_skips_non_file_roots_then_uses_file_root() {
        let result = json!({"roots": [
            {"uri": "https://example.com/x"},
            {"uri": "file:///repo-a"}
        ]});
        assert_eq!(
            project_root_from_roots_response(&result),
            Some(PathBuf::from("/repo-a"))
        );
    }

    #[test]
    fn fr_zcp01_full_envelope_accepted() {
        let envelope = json!({
            "jsonrpc": "2.0",
            "id": 7,
            "result": {"roots": [{"uri": "file:///repo-a"}]}
        });
        assert_eq!(
            project_root_from_roots_response(&envelope),
            Some(PathBuf::from("/repo-a"))
        );
    }

    #[test]
    fn fr_zcp01_empty_or_unusable_roots_degrade_to_none() {
        assert_eq!(
            project_root_from_roots_response(&json!({"roots": []})),
            None
        );
        assert_eq!(
            project_root_from_roots_response(&json!({"roots": [{"uri": "https://example.com/x"}]})),
            None
        );
        assert_eq!(project_root_from_roots_response(&json!({})), None);
        assert_eq!(project_root_from_roots_response(&json!(null)), None);
        // Malformed entries are skipped, not fatal.
        assert_eq!(
            project_root_from_roots_response(&json!({"roots": [
                {"no_uri": true}, {"uri": 42}, {"uri": "file:///ok"}
            ]})),
            Some(PathBuf::from("/ok"))
        );
    }

    // ------------------------------------------------------------------
    // Per-session cache
    // ------------------------------------------------------------------

    #[test]
    fn fr_zcp01_cache_register_set_get() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", false);
        // While the probe is unanswered, lookups fall through.
        assert_eq!(cache.root_for_session("sid-1"), None);
        cache.set_root("sid-1", Some(PathBuf::from("/repo-a")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            Some(PathBuf::from("/repo-a"))
        );
    }

    #[test]
    fn fr_zcp01_cache_unanswered_session_falls_through() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", false);
        // A root stored while unanswered (race: answer arrived via a
        // stream the server did not open) is still not served…
        assert_eq!(cache.root_for_session("sid-1"), None);
        cache.set_root("sid-1", Some(PathBuf::from("/repo-a")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            Some(PathBuf::from("/repo-a"))
        );
    }

    #[test]
    fn fr_zcp01_cache_definitive_no_answer() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", false);
        cache.set_root("sid-1", None);
        // Definitive "client answered with nothing usable" stays None
        // (vs. unanswered, which is also None but expires via settle()).
        assert_eq!(cache.root_for_session("sid-1"), None);
        assert!(!cache.settle("sid-1"), "answered entries are not settled");
    }

    #[test]
    fn fr_zcp01_cache_settle_expires_unanswered() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", false);
        assert!(cache.settle("sid-1"), "unanswered entry must expire");
        assert!(!cache.settle("sid-1"), "second settle is a no-op");
        cache.set_root("sid-1", Some(PathBuf::from("/late")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            None,
            "answers after the window was settled are dropped"
        );
    }

    #[test]
    fn fr_zcp01_cache_invalidate_reprobes() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", true);
        cache.set_root("sid-1", Some(PathBuf::from("/repo-a")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            Some(PathBuf::from("/repo-a"))
        );
        assert!(cache.invalidate("sid-1"));
        // list_changed → back to pending until the next answer lands.
        assert_eq!(cache.root_for_session("sid-1"), None);
        cache.set_root("sid-1", Some(PathBuf::from("/repo-b")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            Some(PathBuf::from("/repo-b"))
        );
        // Unknown session: invalidate reports false, cache untouched.
        assert!(!cache.invalidate("sid-unknown"));
    }

    #[test]
    fn fr_zcp01_cache_sessions_are_isolated() {
        let cache = SessionRootCache::new();
        cache.register_session("sid-1", false);
        cache.register_session("sid-2", false);
        cache.set_root("sid-1", Some(PathBuf::from("/repo-a")));
        assert_eq!(
            cache.root_for_session("sid-2"),
            None,
            "sid-1's answer must not leak into sid-2"
        );
        cache.set_root("sid-2", Some(PathBuf::from("/repo-b")));
        assert_eq!(
            cache.root_for_session("sid-1"),
            Some(PathBuf::from("/repo-a"))
        );
        assert_eq!(
            cache.root_for_session("sid-2"),
            Some(PathBuf::from("/repo-b"))
        );
        // Late answer for an unknown session is ignored (no entry created).
        cache.set_root("sid-unknown", Some(PathBuf::from("/repo-c")));
        assert_eq!(cache.root_for_session("sid-unknown"), None);
    }

    #[test]
    fn fr_zcp01_root_window_is_bounded() {
        // The probe window must stay short (spec: ask once, degrade fast).
        assert!(ROOTS_ANSWER_WINDOW <= Duration::from_secs(10));
    }
}
