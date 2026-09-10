//! Memory store: file-backed JSONL per bank under
//! `<project>/.leankg/memory/<bank>.jsonl` — same discipline as the agent
//! diary (append-only, one JSON object per line).
//!
//! Retain contract (mnemopi): incremental transcript text in
//! `[role: user]\n…\n[user:end]` framing, one row per batch with
//! `source="coding-agent-transcript"`, importance 0.65, metadata
//! `{session_id, source_id, message_count, retained_through_user_turn, cwd}`.
//! The `retained_through_user_turn` integer cursor is persisted and honored:
//! re-retain calls skip entries at or below the cursor (resume-safety).

use serde::{Deserialize, Serialize};
use std::io::Write;
use std::path::{Path, PathBuf};

pub const CODING_AGENT_TRANSCRIPT_SOURCE: &str = "coding-agent-transcript";
pub const TRANSCRIPT_IMPORTANCE: f64 = 0.65;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct MemoryEntry {
    /// Stable id — `source_id` when retained (`<sessionId>-<ms>`), or a
    /// generated id for direct writes.
    pub id: String,
    pub content: String,
    pub source: String,
    /// Unix seconds.
    pub timestamp: i64,
    pub importance: f64,
    /// cwd is load-bearing for OMP's legacy-bank rescue scan.
    pub cwd: String,
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone)]
pub struct MemoryStore {
    root: PathBuf,
}

impl MemoryStore {
    /// `project_root` is the checkout directory (sqlite-default: the same
    /// cwd `leankg serve` runs in); banks live under `.leankg/memory/`.
    pub fn new(project_root: &Path) -> Self {
        Self {
            root: project_root.join(".leankg").join("memory"),
        }
    }

    /// FR-ZCP-07: production anchor — `db_path` IS the `.leankg` directory,
    /// so a mis-shaped db_path can never escape into a parent directory
    /// (the /tmp/.leankg pollution class). Banks live at
    /// `<db_path>/memory/<bank>.jsonl`.
    pub fn in_leankg_dir(db_path: &Path) -> Self {
        Self {
            root: db_path.join("memory"),
        }
    }

    fn bank_path(&self, bank: &str) -> PathBuf {
        self.root
            .join(format!("{}.jsonl", crate::memory::sanitize_bank(bank)))
    }

    /// Cursor for a session: the highest `retained_through_user_turn` seen.
    pub fn retained_through_user_turn(&self, session_id: &str) -> Option<usize> {
        for bank_dir_entries in std::fs::read_dir(&self.root).ok()? {
            let entry = bank_dir_entries.ok()?;
            let path = entry.path();
            let Ok(raw) = std::fs::read_to_string(&path) else {
                continue;
            };
            for line in raw.lines() {
                let Ok(v) = serde_json::from_str::<serde_json::Value>(line) else {
                    continue;
                };
                let meta = v.get("metadata")?;
                if meta.get("session_id").and_then(|s| s.as_str()) == Some(session_id) {
                    if let Some(n) = meta
                        .get("retained_through_user_turn")
                        .and_then(|c| c.as_u64())
                    {
                        return Some(n as usize);
                    }
                }
            }
        }
        None
    }

    /// Retain one transcript batch. Returns rows written. Idempotent per
    /// (session_id, user-turn range): a `retained_through` cursor at or
    /// beyond `through_user_turn` skips the write entirely.
    pub fn retain_transcript(
        &self,
        bank: &str,
        session_id: &str,
        turns: &[String],
        through_user_turn: usize,
        cwd: &str,
    ) -> Result<RetainResult, Box<dyn std::error::Error>> {
        if let Some(seen) = self.retained_through_user_turn(session_id) {
            if through_user_turn <= seen {
                return Ok(RetainResult {
                    written: 0,
                    skipped: turns.len(),
                    retained_through_user_turn: seen,
                });
            }
        }
        let ms = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_millis() as u64)
            .unwrap_or(0);
        let source_id = format!("{session_id}-{ms}");
        std::fs::create_dir_all(&self.root)?;
        let path = self.bank_path(bank);
        let mut written = 0usize;
        let mut f = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&path)?;
        for (i, turn) in turns.iter().enumerate() {
            let entry = MemoryEntry {
                id: format!("{source_id}-{i}"),
                content: turn.clone(),
                source: CODING_AGENT_TRANSCRIPT_SOURCE.into(),
                timestamp: (ms / 1000) as i64,
                importance: TRANSCRIPT_IMPORTANCE,
                cwd: cwd.into(),
                metadata: serde_json::json!({
                    "session_id": session_id,
                    "source_id": source_id,
                    "message_count": turns.len(),
                    "retained_through_user_turn": through_user_turn,
                    "cwd": cwd,
                }),
            };
            writeln!(f, "{}", serde_json::to_string(&entry)?)?;
            written += 1;
        }
        Ok(RetainResult {
            written,
            skipped: 0,
            retained_through_user_turn: through_user_turn,
        })
    }

    /// Ranked recall across the read banks (merge order per the scoping
    /// matrix). Naive relevance: substring/keyword overlap score, newest
    /// first on ties — deterministic, no index required.
    pub fn recall(&self, read_banks: &[String], query: &str, limit: usize) -> Vec<MemoryEntry> {
        let mut seen_ids: std::collections::HashSet<String> = Default::default();
        let mut scored: Vec<(i64, MemoryEntry)> = Vec::new();
        let ql: Vec<String> = query
            .to_lowercase()
            .split_whitespace()
            .map(String::from)
            .collect();
        for bank in read_banks {
            let Ok(raw) = std::fs::read_to_string(self.bank_path(bank)) else {
                continue;
            };
            for line in raw.lines() {
                let Ok(entry) = serde_json::from_str::<MemoryEntry>(line) else {
                    continue;
                };
                if !seen_ids.insert(entry.id.clone()) {
                    continue;
                }
                let content_lower = entry.content.to_lowercase();
                let matched = ql
                    .iter()
                    .filter(|q| content_lower.contains(q.as_str()))
                    .count() as i64;
                // FR-ZCP-07 recall contract: ranked {id, content, source,
                // timestamp, score}. Only entries matching at least one
                // query token are returned — an empty match must never
                // surface (that's what made a forgotten/empty entry
                // recallable).
                if matched > 0 {
                    let score = matched * 100 + (entry.timestamp % 100_000);
                    scored.push((score, entry));
                }
            }
        }
        scored.sort_by_key(|(score, _)| std::cmp::Reverse(*score));
        scored.truncate(limit);
        scored.into_iter().map(|(_, e)| e).collect()
    }

    pub fn get(&self, id: &str) -> Option<MemoryEntry> {
        for entry in std::fs::read_dir(&self.root).ok()? {
            let path = entry.ok()?.path();
            let Ok(raw) = std::fs::read_to_string(&path) else {
                continue;
            };
            for line in raw.lines() {
                if let Ok(e) = serde_json::from_str::<MemoryEntry>(line) {
                    if e.id == id {
                        return Some(e);
                    }
                }
            }
        }
        None
    }

    pub fn update(&self, id: &str, new_content: &str) -> Result<bool, Box<dyn std::error::Error>> {
        self.mutate_entry(id, |e| e.content = new_content.into())
    }

    pub fn forget(&self, id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        self.mutate_entry(id, |e| e.content = String::new())
    }

    /// Remove every entry in every bank for one session (deleting the
    /// project bank never leaves the harness cursor claiming rows that no
    /// longer exist — cursor + rows agree).
    pub fn invalidate_session(
        &self,
        session_id: &str,
    ) -> Result<usize, Box<dyn std::error::Error>> {
        let mut removed = 0usize;
        let Some(entries) = std::fs::read_dir(&self.root).ok() else {
            return Ok(0);
        };
        for entry in entries.flatten() {
            let path = entry.path();
            let Ok(raw) = std::fs::read_to_string(&path) else {
                continue;
            };
            let kept: Vec<&str> = raw
                .lines()
                .filter(|l| match serde_json::from_str::<serde_json::Value>(l) {
                    Ok(v) => {
                        let keep = v
                            .get("metadata")
                            .and_then(|m| m.get("session_id"))
                            .and_then(|s| s.as_str())
                            != Some(session_id);
                        if !keep {
                            removed += 1;
                        }
                        keep
                    }
                    Err(_) => true,
                })
                .collect();
            std::fs::write(&path, kept.join("\n") + "\n")?;
        }
        Ok(removed)
    }

    fn mutate_entry(
        &self,
        id: &str,
        f: impl Fn(&mut MemoryEntry),
    ) -> Result<bool, Box<dyn std::error::Error>> {
        let Some(entries) = std::fs::read_dir(&self.root).ok() else {
            return Ok(false);
        };
        for entry in entries.flatten() {
            let path = entry.path();
            let Ok(raw) = std::fs::read_to_string(&path) else {
                continue;
            };
            let mut rows: Vec<MemoryEntry> = raw
                .lines()
                .filter_map(|l| serde_json::from_str(l).ok())
                .collect();
            let mut hit = false;
            for e in rows.iter_mut() {
                if e.id == id {
                    f(e);
                    hit = true;
                }
            }
            if hit {
                let body: String = rows
                    .iter()
                    .map(serde_json::to_string)
                    .collect::<Result<Vec<_>, _>>()?
                    .join("\n");
                std::fs::write(&path, body + "\n")?;
                return Ok(true);
            }
        }
        Ok(false)
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RetainResult {
    pub written: usize,
    pub skipped: usize,
    pub retained_through_user_turn: usize,
}
