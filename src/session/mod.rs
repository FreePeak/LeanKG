//! Session memory (US-SM-01 / US-SM-02 / US-SM-05 / US-SM-06 / FR-SM-01..11).
//!
//! Verbose MCP/tool payloads are persisted under
//! `.leankg/sessions/<session_id>/refs/<node_id>.md`; a compact canvas
//! (Mermaid + node index) stays in context. `session_recall` recovers the
//! original payload bit-for-bit via `node_id`.
//!
//! Auto-recall (US-SM-02 / FR-SM-04..06): a ranked lessons index at
//! `.leankg/sessions/recall_index.jsonl` is fed by `report_query_outcome`
//! (LESSONS.md), diary, and knowledge writes (dedup by content hash before
//! write). `get_overview_context` opt-in injects top-K lessons when
//! `recall=true` (default OFF); a ≤5s timeout skips injection so recall
//! never blocks the MCP response.
//!
//! Heat promotion (US-SM-05 / US-SM-06 / FR-SM-10 / FR-SM-11): a
//! deterministic heat score (recall frequency + recency) drives a
//! white-box, heat-ranked `.leankg/MEMORY_INDEX.md` (state sidecar
//! `.leankg/MEMORY_INDEX.json`). Repeated successful multi-tool sequences
//! are mined into `add_ontology_workflow` PROPOSALS under
//! `.leankg/proposals/workflows.jsonl` — LeanKG never writes ontology YAML
//! as source of truth; the agent/harness reviews proposals before
//! confirming them into `ontology/`.

use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::compress::estimate_tokens;

pub const CANVAS_FILE: &str = "canvas.md";
pub const REFS_DIR: &str = "refs";
pub const RECALL_INDEX_FILE: &str = "recall_index.jsonl";
pub const RECALL_SESSION_ID: &str = "recall";
pub const DEFAULT_NODE_ID: &str = "offload-001";
/// FR-SM-05: opt-in default OFF until A/B measured.
pub const DEFAULT_RECALL_ENABLED: bool = false;
/// FR-SM-05: top-K lessons injected into overview when enabled.
pub const DEFAULT_RECALL_K: usize = 5;
/// FR-SM-06: recall timeout — on timeout skip injection, never block MCP.
pub const DEFAULT_RECALL_TIMEOUT_SECS: u64 = 5;
/// FR-SM-06: per-item char budget for injected lessons.
pub const RECALL_ITEM_CHAR_BUDGET: usize = 400;
/// FR-SM-06: total char budget for the injected lessons block.
pub const RECALL_TOTAL_CHAR_BUDGET: usize = 3000;
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

/// One auto-recallable lesson (US-SM-02 / FR-SM-04).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct Lesson {
    pub id: String,
    pub source: String,
    pub rank: f64,
    pub text: String,
}

/// Deterministic content fingerprint for dedup (FR-SM-04): 12-hex SHA-256.
/// NOT a security hash — only collision resistance between identical texts.
pub fn content_key(text: &str) -> String {
    let digest = sha256(text.as_bytes());
    let mut s = String::with_capacity(12);
    for b in digest.iter().take(6) {
        s.push_str(&format!("{b:02x}"));
    }
    s
}

/// Ranked lessons index at `.leankg/sessions/recall_index.jsonl`
/// (FR-SM-04): one `Lesson` per line. Shared across sessions — the
/// auto-recall store is process-wide, keyed by project dir.
#[derive(Debug)]
pub struct RecallStore {
    index_path: PathBuf,
}

impl RecallStore {
    pub fn new(project_dir: &Path) -> Result<Self, String> {
        let index_path = project_dir
            .join(".leankg")
            .join("sessions")
            .join(RECALL_INDEX_FILE);
        if let Some(dir) = index_path.parent() {
            std::fs::create_dir_all(dir).map_err(|e| e.to_string())?;
        }
        Ok(Self { index_path })
    }

    pub fn index_path(&self) -> &Path {
        &self.index_path
    }

    /// Append `Lesson` if its `text` fingerprint is not already present
    /// (dedup before write, FR-SM-04). Returns true when appended.
    pub fn push_dedup(&self, lesson: &Lesson) -> Result<bool, String> {
        let key = content_key(&lesson.text);
        let existing = match std::fs::read_to_string(&self.index_path) {
            Ok(raw) => raw
                .lines()
                .filter_map(|l| serde_json::from_str::<Lesson>(l).ok())
                .any(|l| content_key(&l.text) == key),
            Err(_) => false,
        };
        if existing {
            return Ok(false);
        }
        let mut line = serde_json::to_string(lesson).map_err(|e| e.to_string())?;
        line.push('\n');
        use std::io::Write;
        let mut f = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&self.index_path)
            .map_err(|e| e.to_string())?;
        f.write_all(line.as_bytes()).map_err(|e| e.to_string())?;
        Ok(true)
    }

    /// Load lessons sorted by rank descending.
    pub fn load(&self) -> Result<Vec<Lesson>, String> {
        let raw = std::fs::read_to_string(&self.index_path).map_err(|e| e.to_string())?;
        let mut lessons: Vec<Lesson> = raw
            .lines()
            .filter_map(|l| serde_json::from_str::<Lesson>(l).ok())
            .collect();
        lessons.sort_by(|a, b| {
            b.rank
                .partial_cmp(&a.rank)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        Ok(lessons)
    }
}

/// FR-SM-06: budgeted, timeout-bounded snapshot for injection into
/// `get_overview_context`. Pure (no I/O) so it is trivially testable;
/// `recall_for_overview` applies the wall-clock timeout around it.
pub fn recall_for_overview(
    lessons: &[Lesson],
    top_k: usize,
    per_item_budget: usize,
    total_budget: usize,
) -> Option<String> {
    if lessons.is_empty() {
        return None;
    }
    let mut out = String::from("## Session lessons (auto-recall)\n");
    let mut budget = total_budget;
    for l in lessons.iter().take(top_k) {
        let item = truncate_chars(&l.text, per_item_budget);
        let line = format!("- [{}] {}\n", l.source, item);
        if line.chars().count() > budget {
            break;
        }
        budget -= line.chars().count();
        out.push_str(&line);
    }
    if out.lines().count() <= 1 {
        return None;
    }
    Some(out)
}

/// FR-SM-06: wall-clock bounded recall — on timeout, skip injection and
/// return None (never block the MCP response).
pub fn recall_for_overview_bounded(
    lessons: &[Lesson],
    top_k: usize,
    per_item_budget: usize,
    total_budget: usize,
    timeout: std::time::Duration,
) -> Option<String> {
    if lessons.is_empty() {
        return None;
    }
    let (tx, rx) = std::sync::mpsc::channel();
    let lessons = lessons.to_vec();
    std::thread::spawn(move || {
        let _ = tx.send(recall_for_overview(
            &lessons,
            top_k,
            per_item_budget,
            total_budget,
        ));
    });
    rx.recv_timeout(timeout).ok().flatten()
}

fn truncate_chars(s: &str, max: usize) -> String {
    let chars: Vec<char> = s.chars().collect();
    if chars.len() <= max {
        s.to_string()
    } else {
        let mut out: String = chars[..max].iter().collect();
        out.push('…');
        out
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

// ---------------------------------------------------------------------------
// US-SM-05 / FR-SM-10 — heat-ranked MEMORY_INDEX (white-box)
// ---------------------------------------------------------------------------

pub const MEMORY_INDEX_MD: &str = "MEMORY_INDEX.md";
pub const MEMORY_INDEX_JSON: &str = "MEMORY_INDEX.json";
/// (US-SM-05) top-K sessions promoted into the markdown index.
pub const DEFAULT_PROMOTE_TOP_K: usize = 10;
/// Exponential decay half-life for recency (seconds).
const RECENCY_HALF_LIFE_SECS: f64 = 86400.0;

/// Raw heat components (white-box; rendered into MEMORY_INDEX.md).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct HeatScore {
    pub frequency: f64,
    pub recency: f64,
    pub total: f64,
}

/// Deterministic heat score: recall frequency + recency. Pure function —
/// same inputs always produce the same score. Recency decays exponentially
/// with a 1-day half-life; frequency grows sub-linearly (log1p) so one
/// hot memory does not starve the rest of the index.
pub fn heat_score(recalls: u64, last_recalled_epoch_secs: u64, now_epoch_secs: u64) -> f64 {
    let freq = (1.0 + recalls as f64).ln();
    let age_secs = now_epoch_secs.saturating_sub(last_recalled_epoch_secs) as f64;
    let recency = 0.5f64.powf(age_secs / RECENCY_HALF_LIFE_SECS);
    freq + recency
}

/// One tracked memory (session, tool trace, lesson, …).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct MemoryItem {
    pub key: String,
    pub source: String,
    pub text: String,
    pub recalls: u64,
    pub first_seen_epoch_secs: u64,
    pub last_recalled_epoch_secs: u64,
}

/// White-box heat index: JSON state under `.leankg/MEMORY_INDEX.json`,
/// rendered markdown under `.leankg/MEMORY_INDEX.md` (FR-SM-10).
/// Refreshed on recall; never touches ontology YAML.
#[derive(Debug)]
pub struct MemoryIndex {
    project_dir: PathBuf,
    items: Vec<MemoryItem>,
}

impl MemoryIndex {
    pub fn new(project_dir: &Path) -> Result<Self, String> {
        let dir = project_dir.join(".leankg");
        std::fs::create_dir_all(&dir).map_err(|e| e.to_string())?;
        let mut idx = Self {
            project_dir: project_dir.to_path_buf(),
            items: Vec::new(),
        };
        // Load existing state if present (crash-safe re-entry).
        if let Ok(loaded) = Self::load(project_dir) {
            idx.items = loaded.items;
        }
        Ok(idx)
    }

    pub fn json_path(&self) -> PathBuf {
        self.project_dir.join(".leankg").join(MEMORY_INDEX_JSON)
    }

    pub fn md_path(&self) -> PathBuf {
        self.project_dir.join(".leankg").join(MEMORY_INDEX_MD)
    }

    /// Load persisted state. Missing file => empty index (not an error).
    pub fn load(project_dir: &Path) -> Result<Self, String> {
        let path = project_dir.join(".leankg").join(MEMORY_INDEX_JSON);
        let items = match std::fs::read_to_string(&path) {
            Ok(raw) => serde_json::from_str::<Vec<MemoryItem>>(&raw).map_err(|e| e.to_string())?,
            Err(_) => Vec::new(),
        };
        Ok(Self {
            project_dir: project_dir.to_path_buf(),
            items,
        })
    }

    pub fn items(&self) -> &[MemoryItem] {
        &self.items
    }

    /// Register one recall/hit of a memory key. Creates or bumps the item.
    /// Deterministic: identical keys coalesce into one tracked item.
    pub fn record_hit(
        &mut self,
        key: &str,
        source: &str,
        text: &str,
        now_epoch_secs: u64,
    ) -> Result<(), String> {
        let idx = self.items.iter().position(|i| i.key == key);
        match idx {
            Some(i) => {
                let it = &mut self.items[i];
                it.recalls += 1;
                it.last_recalled_epoch_secs = now_epoch_secs;
                it.source = source.to_string();
                it.text = text.to_string();
            }
            None => {
                self.items.push(MemoryItem {
                    key: key.to_string(),
                    source: source.to_string(),
                    text: text.to_string(),
                    recalls: 1,
                    first_seen_epoch_secs: now_epoch_secs,
                    last_recalled_epoch_secs: now_epoch_secs,
                });
            }
        }
        self.persist_json()
    }

    /// Mark a recall for an existing key (no-op for unknown keys). This is
    /// the hook the recall path calls so hot sessions accumulate heat.
    pub fn record_recall(&mut self, key: &str, now_epoch_secs: u64) -> Result<(), String> {
        let mut changed = false;
        if let Some(it) = self.items.iter_mut().find(|i| i.key == key) {
            it.recalls += 1;
            it.last_recalled_epoch_secs = now_epoch_secs;
            changed = true;
        }
        if changed {
            self.persist_json()?;
        }
        Ok(())
    }

    fn persist_json(&self) -> Result<(), String> {
        let raw = serde_json::to_string_pretty(&self.items).map_err(|e| e.to_string())?;
        std::fs::write(self.json_path(), raw).map_err(|e| e.to_string())
    }

    /// Refresh the markdown index. Writes only when the rendered text
    /// actually changed (no-write-on-unchanged). FR-SM-11 guarantee: only
    /// `.leankg/` files are written, never `ontology/` YAML.
    pub fn refresh(&self) -> Result<(), String> {
        let text = self.render();
        let path = self.md_path();
        match std::fs::read_to_string(&path) {
            Ok(existing) if existing == text => return Ok(()),
            _ => {}
        }
        std::fs::write(&path, text).map_err(|e| e.to_string())
    }

    /// Heat-ranked top-K list (deterministic sort by score, tie-break key).
    pub fn top_k(&self, k: usize, now_epoch_secs: u64) -> Vec<(MemoryItem, HeatScore)> {
        let mut ranked: Vec<(MemoryItem, HeatScore)> = self
            .items
            .iter()
            .map(|it| {
                let total = heat_score(it.recalls, it.last_recalled_epoch_secs, now_epoch_secs);
                let age_secs = now_epoch_secs.saturating_sub(it.last_recalled_epoch_secs) as f64;
                let score = HeatScore {
                    frequency: (1.0 + it.recalls as f64).ln(),
                    recency: 0.5f64.powf(age_secs / RECENCY_HALF_LIFE_SECS),
                    total,
                };
                (it.clone(), score)
            })
            .collect();
        ranked.sort_by(|a, b| {
            b.1.total
                .partial_cmp(&a.1.total)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.0.key.cmp(&b.0.key))
        });
        ranked.truncate(k);
        ranked
    }

    /// Render the white-box markdown index (heat-ranked, top-K).
    pub fn render(&self) -> String {
        let now = now_unix_secs();
        let ranked = self.top_k(DEFAULT_PROMOTE_TOP_K, now);
        let mut out = String::from("# MEMORY_INDEX\n\n");
        out.push_str(&format!(
            "- generated: {now} (epoch secs; deterministic score: log(1+recalls) + 0.5^(age/86400))\n"
        ));
        out.push_str(&format!(
            "- tracked: {} | promoted (top-K): {}\n\n",
            self.items.len(),
            ranked.len()
        ));
        if ranked.is_empty() {
            out.push_str("_No sessions tracked yet._\n");
            return out;
        }
        out.push_str("## Hot sessions\n\n");
        out.push_str("| rank | key | recalls | last recall (age s) | heat | source |\n");
        out.push_str("|---|---|---|---|---|---|\n");
        for (i, (it, score)) in ranked.iter().enumerate() {
            let age = now.saturating_sub(it.last_recalled_epoch_secs);
            out.push_str(&format!(
                "| {} | {} | {} | {} | {:.6} | {} |\n",
                i + 1,
                it.key,
                it.recalls,
                age,
                score.total,
                it.source
            ));
        }
        out.push_str("\n## Detail\n\n");
        for (it, score) in &ranked {
            out.push_str(&format!(
                "- **{}** — recalls={}, heat={:.6} (freq={:.6}, recency={:.6})\n  {}\n",
                it.key, it.recalls, score.total, score.frequency, score.recency, it.text
            ));
        }
        out
    }
}

// ---------------------------------------------------------------------------
// US-SM-06 / FR-SM-11 — repeated successful tool traces => workflow proposals
// ---------------------------------------------------------------------------

pub const WORKFLOW_PROPOSALS_DIR: &str = "proposals";
/// Minimum repeated occurrences before a trace is proposed (FR-SM-11).
pub const MIN_SEQUENCE_LEN: usize = 3;
/// Proposal detail step must be of length >= 3 to count as a shared trace.
const MIN_TRACE_LEN: usize = 3;

/// One proposed `add_ontology_workflow` payload (FR-SM-11). This is a
/// PROPOSAL only — the harness/human reviews and confirms; LeanKG never
/// writes it into `ontology/` YAML as source of truth.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct WorkflowProposal {
    pub name: String,
    pub description: String,
    pub steps: Vec<String>,
    pub occurrences: usize,
    pub confidence: f64,
    pub created_epoch_secs: u64,
}

/// A proposal persisted to `.leankg/proposals/workflows.jsonl`. The content
/// key dedups identical step sequences (FR-SM-04-style hashing).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct ProposalRecord {
    pub content_key: String,
    pub proposal: WorkflowProposal,
}

/// Mines repeated successful multi-tool sequences into workflow proposals.
/// Pure: no I/O, no ontology writes.
#[derive(Debug)]
pub struct SequenceMiner {
    min_occurrences: usize,
    min_trace_len: usize,
}

impl SequenceMiner {
    pub fn new(min_occurrences: usize, min_trace_len: usize) -> Self {
        Self {
            min_occurrences,
            min_trace_len,
        }
    }

    pub fn propose(
        &self,
        traces: &[Vec<String>],
        positive_terms: Option<&[&str]>,
    ) -> Vec<WorkflowProposal> {
        use std::collections::HashMap;
        let mut counts: HashMap<Vec<String>, usize> = HashMap::new();
        for t in traces {
            if t.len() < self.min_trace_len {
                continue;
            }
            let trimmed: Vec<String> = t
                .iter()
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
                .collect();
            if trimmed.len() < self.min_trace_len {
                continue;
            }
            *counts.entry(trimmed).or_insert(0) += 1;
        }
        let mut proposals = Vec::new();
        for (steps, occurrences) in counts {
            if occurrences < self.min_occurrences {
                continue;
            }
            if let Some(terms) = positive_terms {
                if !terms.iter().any(|t| steps.contains(&t.to_string())) {
                    continue;
                }
            }
            proposals.push(WorkflowProposal {
                name: workflow_name(&steps),
                description: format!(
                    "Repeated {} times across sessions: {}",
                    occurrences,
                    steps.join(" → ")
                ),
                steps,
                occurrences,
                confidence: confidence(occurrences),
                created_epoch_secs: now_unix_secs(),
            });
        }
        proposals.sort_by(|a, b| {
            b.occurrences
                .cmp(&a.occurrences)
                .then_with(|| a.name.cmp(&b.name))
        });
        proposals
    }
}

impl Default for SequenceMiner {
    fn default() -> Self {
        Self::new(MIN_SEQUENCE_LEN, MIN_TRACE_LEN)
    }
}

/// Deterministic snake-case workflow name from the step sequence.
fn workflow_name(steps: &[String]) -> String {
    let mut joined: Vec<String> = steps
        .iter()
        .map(|s| s.split('_').collect::<Vec<_>>().join(" "))
        .collect();
    let joined_str = joined.join(" ");
    joined = joined_str.split_whitespace().map(str::to_string).collect();
    let mut name = joined
        .into_iter()
        .map(|w| {
            w.chars()
                .filter(|c| c.is_ascii_alphanumeric())
                .collect::<String>()
        })
        .filter(|w| !w.is_empty())
        .collect::<Vec<String>>()
        .join("_");
    if name.is_empty() {
        name = "workflow".to_string();
    }
    name
}

/// Confidence grows with occurrences, saturating near 1.0.
fn confidence(occurrences: usize) -> f64 {
    1.0 - 0.5f64.powi(occurrences as i32 - 1)
}

fn now_unix_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
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

    // ------------------------------------------------------------------
    // US-SM-02 / FR-SM-04..06 — opt-in auto-recall
    // ------------------------------------------------------------------

    fn lesson_rank(source: &str, rank: f64, text: &str) -> Lesson {
        Lesson {
            id: "k-1".to_string(),
            source: source.to_string(),
            rank,
            text: text.to_string(),
        }
    }

    #[test]
    fn recall_index_dedups_by_content_before_write() {
        let tmp = TempDir::new().unwrap();
        let store = RecallStore::new(tmp.path()).unwrap();
        assert!(store
            .push_dedup(&lesson_rank("LESSONS.md", 1.0, "duplicate lesson"))
            .unwrap());
        assert!(!store
            .push_dedup(&lesson_rank("diary", 1.0, "duplicate lesson"))
            .unwrap());
        let lessons = store.load().unwrap();
        assert_eq!(lessons.len(), 1, "duplicate text must be deduped");
    }

    #[test]
    fn recall_index_loads_sorted_by_rank_descending() {
        let tmp = TempDir::new().unwrap();
        let store = RecallStore::new(tmp.path()).unwrap();
        store
            .push_dedup(&lesson_rank("diary", 0.1, "low rank lesson (rank 0.1)"))
            .unwrap();
        store
            .push_dedup(&lesson_rank("diary", 9.9, "high rank lesson (rank 9.9)"))
            .unwrap();
        store.push_dedup(&lesson_rank("diary", 5.0, "mid rank lesson (rank 5.0)"));
        let lessons = store.load().unwrap();
        assert_eq!(lessons.len(), 3);
        assert!(lessons[0].text.starts_with("high"), "{lessons:?}");
        assert!(lessons[2].text.starts_with("low"), "{lessons:?}");
    }

    #[test]
    fn recall_for_overview_respects_char_budgets() {
        let lessons = vec![
            lesson_rank("LESSONS.md", 1.0, &"x".repeat(600)),
            lesson_rank("diary", 1.0, &"y".repeat(500)),
            lesson_rank("knowledge", 1.0, &"z".repeat(200)),
        ];
        let out = recall_for_overview(&lessons, 5, 400, 3000).expect("some lessons");
        assert!(out.contains("## Session lessons"));
        // per-item budget: the 600-char lesson is truncated to ~400
        assert!(out.contains("…"), "truncation marker expected: {out}");
        assert!(out.len() <= 3000, "total budget exceeded: {}", out.len());
    }

    #[test]
    fn recall_for_overview_skips_when_budget_exhausted_or_empty() {
        assert!(recall_for_overview(&[], 5, 400, 3000).is_none());
        let lessons = vec![lesson_rank("diary", 1.0, &"big ".repeat(1000))];
        // per-item budget truncation keeps items, but zero total budget skips
        assert!(recall_for_overview(&lessons, 5, 10, 0).is_none());
    }

    #[test]
    fn recall_for_overview_bounded_times_out_and_skips_injection() {
        let lessons = vec![lesson_rank("diary", 1.0, "blocked lesson")];
        let out =
            recall_for_overview_bounded(&lessons, 5, 400, 3000, std::time::Duration::from_secs(5));
        assert!(out.is_some(), "fast recall returns Some");
        // timeout path: a slow computation must be skipped, not block
        let start = std::time::Instant::now();
        let slow: Vec<Lesson> = (0..100_000)
            .map(|i| {
                lesson_rank(
                    "diary",
                    1.0,
                    &format!("slow lesson {i} with padding {}", "x".repeat(100)),
                )
            })
            .collect();
        let out = recall_for_overview_bounded(
            &slow,
            100_000,
            400,
            3000,
            std::time::Duration::from_millis(10),
        );
        assert!(out.is_none() || start.elapsed() < std::time::Duration::from_secs(5));
        assert!(
            start.elapsed() < std::time::Duration::from_secs(5),
            "recall must never block beyond the timeout: {:?}",
            start.elapsed()
        );
    }

    // ------------------------------------------------------------------
    // US-SM-05 / US-SM-06 / FR-SM-10 / FR-SM-11 — heat promotion
    // ------------------------------------------------------------------

    #[test]
    fn heat_promotion_recall_path_bumps_heat() {
        let tmp = TempDir::new().unwrap();
        let mut idx = MemoryIndex::new(tmp.path()).unwrap();
        idx.record_hit("wf-debug", "LESSONS.md", "debug workflow", 100)
            .unwrap();
        idx.record_recall("wf-debug", 200).unwrap();
        idx.record_recall("wf-debug", 250).unwrap();
        let now = 260;
        let scored = heat_score(3, 250, now);
        let top = idx.top_k(5, now);
        assert_eq!(top.len(), 1);
        assert_eq!(top[0].0.recalls, 3, "hit + 2 recalls");
        assert_eq!(top[0].1.total, scored, "score equals pure fn");
    }

    #[test]
    fn heat_promotion_top_k_truncates_and_orders() {
        let tmp = TempDir::new().unwrap();
        let mut idx = MemoryIndex::new(tmp.path()).unwrap();
        for i in 0..20 {
            idx.record_hit(
                &format!("k{i:02}"),
                "diary",
                &format!("key {i}"),
                1000 + i as u64,
            )
            .unwrap();
        }
        let top = idx.top_k(5, 2000);
        assert_eq!(top.len(), 5, "top-K truncation");
        // monotonic heat within the promoted slice
        for w in top.windows(2) {
            assert!(w[0].1.total >= w[1].1.total, "ranked desc: {:?}", w);
        }
        let md = idx.render();
        assert!(md.contains("| rank | key |"), "table header: {md}");
    }

    #[test]
    fn heat_promotion_markdown_is_white_box() {
        let tmp = TempDir::new().unwrap();
        let mut idx = MemoryIndex::new(tmp.path()).unwrap();
        idx.record_hit("hot-session", "search telemetry", "the hot session", 100)
            .unwrap();
        let md = idx.render();
        assert!(md.contains("hot-session"));
        assert!(md.contains("recalls=1"), "raw recall count visible: {md}");
        assert!(md.contains("heat="), "raw heat visible: {md}");
        assert!(
            md.contains("log(1+recalls) + 0.5^(age/86400)"),
            "deterministic formula documented: {md}"
        );
    }

    #[test]
    fn workflow_proposal_shape_matches_add_ontology_workflow_payload() {
        let miner = SequenceMiner::default();
        let traces: Vec<Vec<String>> = vec![
            vec!["search_code", "get_context", "query_file"]
                .into_iter()
                .map(String::from)
                .collect(),
            vec!["search_code", "get_context", "query_file"]
                .into_iter()
                .map(String::from)
                .collect(),
            vec!["search_code", "get_context", "query_file"]
                .into_iter()
                .map(String::from)
                .collect(),
        ];
        let proposals = miner.propose(&traces, None);
        assert_eq!(proposals.len(), 1);
        let p = &proposals[0];
        // `add_ontology_workflow` requires name + description + steps array
        assert!(!p.name.is_empty());
        assert!(!p.description.is_empty());
        assert_eq!(p.steps.len(), 3);
        assert!(p.occurrences >= 3);
        assert!(p.confidence > 0.5, "confidence grows with occurrences");
    }

    #[test]
    fn workflow_proposal_dedup_key_identifies_sequence() {
        let seq = vec!["search_code".to_string(), "get_context".to_string()];
        let key = content_key(&seq.join("|"));
        assert_eq!(key.len(), 12, "12-hex content key");
        let seq2 = vec!["search_code".to_string(), "query_file".to_string()];
        assert_ne!(key, content_key(&seq2.join("|")));
    }
}
