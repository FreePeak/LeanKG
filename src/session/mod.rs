//! Session memory offload (US-SM-01 / FR-SM-01..03).
//!
//! Verbose MCP/tool payloads are persisted under
//! `.leankg/sessions/<session_id>/refs/<node_id>.md`; a compact canvas
//! (Mermaid + node index) stays in context. `session_recall` recovers the
//! original payload bit-for-bit via `node_id`.

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::compress::estimate_tokens;

pub const CANVAS_FILE: &str = "canvas.md";
pub const REFS_DIR: &str = "refs";
pub const DEFAULT_NODE_ID: &str = "offload-001";
/// Minimum node_id length enforced by the stable scheme (FR-SM-01).
pub const MIN_NODE_ID_LEN: usize = 3;

/// Per-node offload metadata kept on the canvas. Compact by design so a
/// long session canvas stays well under a few hundred tokens.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct NodeEntry {
    pub node_id: String,
    pub tool: String,
    pub step: usize,
    pub char_len: usize,
    pub summary: String,
}

impl NodeEntry {
    pub fn new(node_id: &str, tool: &str, step: usize, payload: &str) -> Self {
        let summary = summarize(payload);
        Self {
            node_id: node_id.to_string(),
            tool: tool.to_string(),
            step,
            char_len: payload.chars().count(),
            summary,
        }
    }
}

/// Session canvas (FR-SM-02): steps + `node_id`s. Serializes as compact JSON
/// or Mermaid.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct Canvas {
    pub session_id: String,
    pub created_at: String,
    pub steps: Vec<NodeEntry>,
    /// Mermaid `flowchart TD` listing steps and drill-down ids.
    pub mermaid: String,
}

impl Canvas {
    pub fn new(session_id: &str) -> Self {
        Self {
            session_id: session_id.to_string(),
            created_at: now_iso8601(),
            steps: Vec::new(),
            mermaid: String::new(),
        }
    }

    pub fn push(&mut self, entry: NodeEntry) {
        self.steps.push(entry);
        self.mermaid = mermaid(&self.session_id, &self.steps);
    }

    /// List of `node_id`s in step order (index for `session_recall`).
    pub fn node_ids(&self) -> Vec<&str> {
        self.steps.iter().map(|e| e.node_id.as_str()).collect()
    }

    /// Compact JSON representation kept in context.
    pub fn to_compact_json(&self) -> Value {
        serde_json::json!({
            "session_id": self.session_id,
            "created_at": self.created_at,
            "steps": self.steps,
        })
    }
}

/// Deterministic stable node_id scheme (FR-SM-01): `offload-<NNN>`.
pub fn node_id_for(step: usize) -> String {
    format!("offload-{:03}", step)
}

/// Mermaid flowchart for the session canvas.
pub fn mermaid(session_id: &str, steps: &[NodeEntry]) -> String {
    let mut out = String::from("flowchart TD\n");
    out.push_str(&format!("  S0[\"session {}\"]\n", session_id));
    for (i, e) in steps.iter().enumerate() {
        out.push_str(&format!(
            "  S{}[\"{}: {} ({})\"]\n",
            i + 1,
            e.tool,
            e.summary,
            e.node_id
        ));
        out.push_str(&format!("  S{} --> S{}\n", i, i + 1));
    }
    out
}

/// 12-word prefix summary for canvas entries; ellipsis when truncated.
pub fn summarize(text: &str) -> String {
    let words: Vec<&str> = text.split_whitespace().collect();
    let n = words.len().min(12);
    let head = words[..n].join(" ");
    if words.len() > n {
        format!("{head}…")
    } else {
        head
    }
}

/// Per-session offload store: `.leankg/sessions/<id>/refs/<node_id>.md`.
#[derive(Debug)]
pub struct SessionStore {
    session_id: String,
    session_dir: PathBuf,
    refs_dir: PathBuf,
}

impl SessionStore {
    pub fn new(session_id: &str, project_dir: &Path) -> Result<Self, String> {
        if !is_valid_session_id(session_id) {
            return Err(format!("invalid session_id: {session_id}"));
        }
        let session_dir = project_dir
            .join(".leankg")
            .join("sessions")
            .join(session_id);
        let refs_dir = session_dir.join(REFS_DIR);
        std::fs::create_dir_all(&refs_dir)
            .map_err(|e| format!("create {}: {e}", refs_dir.display()))?;
        Ok(Self {
            session_id: session_id.to_string(),
            session_dir,
            refs_dir,
        })
    }

    /// Full on-disk path of the ref markdown for `node_id` (not read).
    pub fn ref_path(&self, node_id: &str) -> PathBuf {
        self.refs_dir.join(format!("{node_id}.md"))
    }

    pub fn canvas_path(&self) -> PathBuf {
        self.session_dir.join(CANVAS_FILE)
    }

    /// Write one offloaded payload to `refs/<node_id>.md` (FR-SM-01).
    /// Bit-for-bit: body is the exact JSON payload text; only the front
    /// matter header wraps it.
    pub fn write_ref(
        &self,
        node_id: &str,
        tool: &str,
        step: usize,
        payload: &Value,
    ) -> Result<PathBuf, String> {
        if node_id.chars().count() < MIN_NODE_ID_LEN
            || node_id
                .chars()
                .any(|c| !(c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.'))
        {
            return Err(format!("invalid node_id: {node_id}"));
        }
        let body = serde_json::to_string_pretty(payload).map_err(|e| e.to_string())?;
        let content = format!(
            "# Ref: {node_id}\n\n\
             - tool: {tool}\n\
             - step: {step}\n\
             - bytes: {}\n\
             - sha256: {}\n\n\
             ```json\n{body}\n```\n",
            body.len(),
            short_sha256(&body)
        );
        let path = self.ref_path(node_id);
        std::fs::write(&path, content).map_err(|e| e.to_string())?;
        Ok(path)
    }

    /// Bit-for-bit recall: parse the JSON fenced block back out and compare
    /// against the parsed payload.
    pub fn read_ref(&self, node_id: &str) -> Result<Value, String> {
        let raw = std::fs::read_to_string(self.ref_path(node_id))
            .map_err(|e| format!("node_id {node_id} not found: {e}"))?;
        parse_ref_body(&raw).ok_or_else(|| format!("node_id {node_id}: malformed ref file"))
    }

    /// Write the canvas file (FR-SM-02) and return its text.
    pub fn write_canvas(&self, canvas: &Canvas) -> Result<String, String> {
        let text = canvas_text(canvas);
        std::fs::write(self.canvas_path(), &text).map_err(|e| e.to_string())?;
        Ok(text)
    }

    /// Load the current canvas if present.
    pub fn load_canvas(&self) -> Result<Canvas, String> {
        let raw = std::fs::read_to_string(self.canvas_path())
            .map_err(|e| format!("no canvas for session: {e}"))?;
        parse_canvas(&raw).ok_or_else(|| "malformed canvas".to_string())
    }
}

/// Canvas markdown: Mermaid diagram + node index table.
pub fn canvas_text(canvas: &Canvas) -> String {
    let mut out = String::new();
    out.push_str(&format!("# Session canvas: {}\n\n", canvas.session_id));
    out.push_str(&format!("- created: {}\n", canvas.created_at));
    out.push_str(&format!("- steps: {}\n\n", canvas.steps.len()));
    out.push_str("```mermaid\n");
    out.push_str(&canvas.mermaid);
    out.push_str(
        "```\n\n## Nodes\n\n| node_id | tool | step | chars | summary |\n|---|---|---|---|---|\n",
    );
    for e in &canvas.steps {
        out.push_str(&format!(
            "| {} | {} | {} | {} | {} |\n",
            e.node_id, e.tool, e.step, e.char_len, e.summary
        ));
    }
    out
}

/// Extract the JSON fenced block body from a ref file.
pub fn parse_ref_body(raw: &str) -> Option<Value> {
    let start = raw.find("```json\n")? + "```json\n".len();
    let rest = &raw[start..];
    let end = rest.find("\n```")?;
    serde_json::from_str(&rest[..end]).ok()
}

/// Parse a canvas markdown back into a `Canvas`.
pub fn parse_canvas(raw: &str) -> Option<Canvas> {
    let mut session_id = String::new();
    let mut created_at = String::new();
    for line in raw.lines().take(4) {
        if let Some(v) = line.strip_prefix("# Session canvas: ") {
            session_id = v.trim().to_string();
        } else if let Some(v) = line.strip_prefix("- created: ") {
            created_at = v.trim().to_string();
        }
    }
    let mut steps = Vec::new();
    let mut in_table = false;
    for line in raw.lines() {
        if line == "| node_id | tool | step | chars | summary |" {
            in_table = true;
            continue;
        }
        if in_table {
            if line.trim().is_empty() {
                in_table = false;
                continue;
            }
            let cols: Vec<&str> = line.trim_matches('|').split('|').map(str::trim).collect();
            // Skip the markdown separator row (---) between header and rows.
            if cols.len() >= 5 && cols[1].starts_with("---") {
                continue;
            }
            if cols.len() >= 5 {
                steps.push(NodeEntry {
                    node_id: cols[0].to_string(),
                    tool: cols[1].to_string(),
                    step: cols[2].parse().unwrap_or(0),
                    char_len: cols[3].parse().unwrap_or(0),
                    summary: cols[4].to_string(),
                });
            }
        }
    }
    if session_id.is_empty() || steps.is_empty() {
        return None;
    }
    Some(Canvas {
        mermaid: mermaid(&session_id, &steps),
        session_id,
        created_at,
        steps,
    })
}

fn is_valid_session_id(id: &str) -> bool {
    !id.is_empty()
        && id
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.')
}

/// Estimated tokens of the compact context that replaces the raw payload.
pub fn context_replacement_tokens(canvas: &Canvas, max_chars: usize) -> usize {
    let mut s = String::from("session: ").to_string();
    s.push_str(&canvas.session_id);
    let mut budget = max_chars.saturating_sub(s.chars().count());
    for e in &canvas.steps {
        let line = format!("- {} {} {} ({})", e.node_id, e.tool, e.summary, e.char_len);
        if line.chars().count() > budget {
            break;
        }
        budget -= line.chars().count();
        s.push('\n');
        s.push_str(&line);
    }
    estimate_tokens(&s)
}

/// Whether a payload is considered verbose enough to offload.
pub fn exceeds_budget(payload: &Value, budget_chars: usize) -> bool {
    payload.to_string().chars().count() > budget_chars
}

fn now_iso8601() -> String {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| format!("{}", d.as_secs()))
        .unwrap_or_else(|_| "0".to_string())
}

fn short_sha256(text: &str) -> String {
    use std::fmt::Write;
    let digest = sha256(text.as_bytes());
    let mut s = String::with_capacity(12);
    for b in digest.iter().take(6) {
        let _ = write!(s, "{b:02x}");
    }
    s
}

/// Minimal SHA-256 (FIPS 180-4) — 12-hex content fingerprint for ref front
/// matter; avoids pulling a crypto crate into the session module.
fn sha256(data: &[u8]) -> [u8; 32] {
    const K: [u32; 64] = [
        0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4,
        0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe,
        0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f,
        0x4a7484aa, 0x5cb0a9dc, 0x76f988da, 0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
        0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc,
        0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
        0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070, 0x19a4c116,
        0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
        0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7,
        0xc67178f2,
    ];
    let mut state: [u32; 8] = [
        0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab,
        0x5be0cd19,
    ];
    let mut msg = data.to_vec();
    let bit_len = (msg.len() as u64).wrapping_mul(8);
    msg.push(0x80);
    while msg.len() % 64 != 56 {
        msg.push(0);
    }
    msg.extend_from_slice(&bit_len.to_be_bytes());
    for chunk in msg.chunks(64) {
        let mut w = [0u32; 64];
        for (i, word) in chunk.chunks(4).enumerate().take(16) {
            w[i] = u32::from_be_bytes([word[0], word[1], word[2], word[3]]);
        }
        for i in 16..64 {
            let s0 = w[i - 15].rotate_right(7) ^ w[i - 15].rotate_right(18) ^ (w[i - 15] >> 3);
            let s1 = w[i - 2].rotate_right(17) ^ w[i - 2].rotate_right(19) ^ (w[i - 2] >> 10);
            w[i] = w[i - 16]
                .wrapping_add(s0)
                .wrapping_add(w[i - 7])
                .wrapping_add(s1);
        }
        let [mut a, mut b, mut c, mut d, mut e, mut f, mut g, mut h] = state;
        for (i, &k) in K.iter().enumerate() {
            let s1 = e.rotate_right(6) ^ e.rotate_right(11) ^ e.rotate_right(25);
            let ch = (e & f) ^ (!e & g);
            let t1 = h
                .wrapping_add(s1)
                .wrapping_add(ch)
                .wrapping_add(k)
                .wrapping_add(w[i]);
            let s0 = a.rotate_right(2) ^ a.rotate_right(13) ^ a.rotate_right(22);
            let maj = (a & b) ^ (a & c) ^ (b & c);
            let t2 = s0.wrapping_add(maj);
            h = g;
            g = f;
            f = e;
            e = d.wrapping_add(t1);
            d = c;
            c = b;
            b = a;
            a = t1.wrapping_add(t2);
        }
        state[0] = state[0].wrapping_add(a);
        state[1] = state[1].wrapping_add(b);
        state[2] = state[2].wrapping_add(c);
        state[3] = state[3].wrapping_add(d);
        state[4] = state[4].wrapping_add(e);
        state[5] = state[5].wrapping_add(f);
        state[6] = state[6].wrapping_add(g);
        state[7] = state[7].wrapping_add(h);
    }
    let mut out = [0u8; 32];
    for (i, s) in state.iter().enumerate() {
        out[i * 4..i * 4 + 4].copy_from_slice(&s.to_be_bytes());
    }
    out
}

/// One offload operation: write ref, update canvas, return compact context.
/// Stateless across calls — node_id derives from the canvas on disk, so the
/// MCP handler needs no per-session in-memory state.
pub fn offload_step(
    store: &SessionStore,
    tool: &str,
    payload: &Value,
    budget_chars: usize,
) -> Result<Value, String> {
    if !exceeds_budget(payload, budget_chars) {
        return Err("payload below offload budget; keep inline".to_string());
    }
    let mut canvas = store
        .load_canvas()
        .unwrap_or_else(|_| Canvas::new(&store.session_id));
    let step = canvas.steps.len() + 1;
    let node_id = node_id_for(canvas.steps.len() + 1);
    store.write_ref(&node_id, tool, step, payload)?;
    canvas.push(NodeEntry::new(&node_id, tool, step, &payload.to_string()));
    store.write_canvas(&canvas)?;
    Ok(canvas.to_compact_json())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use tempfile::TempDir;

    fn big_payload(n: usize) -> Value {
        serde_json::json!({
            "results": (0..n).map(|i| serde_json::json!({
                "qualified_name": format!("src/mod_{i}.rs::func_{i}"),
                "file": format!("src/mod_{i}.rs"),
                "line": i,
                "doc": format!("function number {i} with a fairly long description to consume tokens"),
            })).collect::<Vec<_>>(),
        })
    }

    fn store_in(tmp: &TempDir, session: &str) -> SessionStore {
        SessionStore::new(session, tmp.path()).expect("store")
    }

    #[test]
    fn writes_ref_markdown_with_frontmatter_and_json_block() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let payload = json!({"tool": "search_code", "hits": [{"name": "login", "line": 12}]});
        let path = store
            .write_ref("offload-001", "search_code", 3, &payload)
            .expect("write_ref");
        assert!(path.ends_with(".leankg/sessions/sess-001/refs/offload-001.md"));
        let raw = std::fs::read_to_string(&path).unwrap();
        assert!(raw.starts_with("# Ref: offload-001\n"), "header: {raw}");
        assert!(raw.contains("- tool: search_code"));
        assert!(raw.contains("- step: 3"));
        assert!(raw.contains("- sha256: "));
        assert!(raw.contains("```json\n"));
        assert!(raw.contains("\n```\n"));
    }

    #[test]
    fn recall_is_bit_for_bit() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let payload = big_payload(3);
        let node_id = "offload-001";
        store
            .write_ref(node_id, "search_code", 1, &payload)
            .unwrap();
        let recalled = store.read_ref(node_id).expect("read_ref");
        assert_eq!(recalled, payload, "recalled payload must equal original");
    }

    #[test]
    fn recall_missing_node_errors() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let err = store.read_ref("offload-999").unwrap_err();
        assert!(err.contains("not found"), "{err}");
    }

    #[test]
    fn canvas_lists_steps_and_node_ids() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let mut canvas = Canvas::new("sess-001");
        canvas.push(NodeEntry::new(
            "offload-001",
            "search_code",
            1,
            "a b c d e f g h i j k l m n",
        ));
        canvas.push(NodeEntry::new("offload-002", "get_context", 2, "x y z"));
        let text = store.write_canvas(&canvas).expect("write_canvas");
        assert!(text.contains("```mermaid"));
        assert!(text.contains("flowchart TD"));
        assert!(text.contains("offload-001"));
        assert!(text.contains("| offload-002 | get_context | 2 | 5 | x y z |"));
        assert_eq!(canvas.node_ids(), vec!["offload-001", "offload-002"]);
        assert_eq!(canvas.mermaid, mermaid("sess-001", &canvas.steps));
    }

    #[test]
    fn canvas_round_trip_parses() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let mut canvas = Canvas::new("sess-001");
        canvas.push(NodeEntry::new(
            "offload-001",
            "search_code",
            1,
            "one two three four five six seven eight nine ten eleven twelve thirteen",
        ));
        store.write_canvas(&canvas).unwrap();
        let parsed = store.load_canvas().expect("load_canvas");
        assert_eq!(parsed.session_id, "sess-001");
        assert_eq!(parsed.steps.len(), 1);
        assert_eq!(parsed.steps[0].node_id, "offload-001");
        assert_eq!(parsed.steps[0].tool, "search_code");
        assert_eq!(parsed.steps[0].step, 1);
        assert!(parsed.mermaid.contains("offload-001"));
    }

    #[test]
    fn offload_step_keeps_compact_context_and_canvas() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let payload = big_payload(40);
        let compact = offload_step(&store, "search_code", &payload, 2000).expect("offload");
        assert!(compact["session_id"] == "sess-001");
        let steps = compact["steps"].as_array().unwrap();
        assert_eq!(steps.len(), 1);
        assert_eq!(steps[0]["node_id"], "offload-001");
        let canvas = store.load_canvas().unwrap();
        assert_eq!(canvas.steps.len(), 1);
        // drill-down recovers the original payload
        assert_eq!(store.read_ref("offload-001").unwrap(), payload);
    }

    #[test]
    fn offload_is_stateless_and_node_ids_are_stable_across_calls() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let p1 = big_payload(30);
        let p2 = big_payload(25);
        let c1 = offload_step(&store, "search_code", &p1, 2000).expect("first offload");
        assert_eq!(c1["steps"][0]["node_id"], "offload-001");
        let c2 = offload_step(&store, "get_context", &p2, 2000).expect("second offload");
        let steps = c2["steps"].as_array().unwrap();
        assert_eq!(steps.len(), 2);
        assert_eq!(steps[1]["node_id"], "offload-002");
        assert_eq!(steps[1]["tool"], "get_context");
        assert_eq!(store.read_ref("offload-002").unwrap(), p2);
    }

    #[test]
    fn small_payload_stays_inline() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let err = offload_step(&store, "search_code", &json!({"ok": true}), 2000).unwrap_err();
        assert!(err.contains("below offload budget"), "{err}");
        // nothing written, no canvas
        assert!(store.load_canvas().is_err());
    }

    #[test]
    fn fixture_offloaded_context_drops_30_percent_tokens() {
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-fixture");
        let payloads = vec![
            big_payload(50),
            big_payload(60),
            big_payload(70),
            big_payload(55),
        ];
        let mut inline_chars = 0usize;
        let mut compact_chars = 0usize;
        for p in payloads.iter() {
            let s = p.to_string();
            inline_chars += s.chars().count();
            let compact = offload_step(&store, "search_code", p, 2000).expect("offload");
            compact_chars += compact.to_string().chars().count();
        }
        let inline_tokens = estimate_tokens(&format!("prefix {}", "x".repeat(inline_chars)));
        let compact_tokens = estimate_tokens(&format!("prefix {}", "x".repeat(compact_chars)));
        let drop = (inline_tokens as f64 - compact_tokens as f64) / inline_tokens as f64;
        assert!(
            drop >= 0.30,
            "token drop {drop:.2} must be >= 0.30 (inline {inline_tokens}, compact {compact_tokens})"
        );
    }

    #[test]
    fn node_id_scheme_is_stable_and_validated() {
        assert_eq!(node_id_for(1), "offload-001");
        assert_eq!(node_id_for(42), "offload-042");
        let tmp = TempDir::new().unwrap();
        let store = store_in(&tmp, "sess-001");
        let err = store.write_ref("../evil", "t", 1, &json!(1)).unwrap_err();
        assert!(err.contains("invalid node_id"), "{err}");
        assert!(SessionStore::new("../evil", tmp.path()).is_err());
    }
}
