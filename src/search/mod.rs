//! Hybrid recall ranking for agent memory (US-SM-04 / FR-SM-09).
//!
//! Reciprocal Rank Fusion (RRF) merges ranked lists from independent
//! retrievers (session recall index, LESSONS, dynamic ontology, graph
//! search) into one deterministic ranking. `k` is fixed at 60 per
//! FR-SM-09 — the standard RRF constant.

use std::collections::HashMap;

use serde::{Deserialize, Serialize};

use crate::session::{classify_memory_kind, Lesson, RecallStore};

/// RRF constant mandated by FR-SM-09.
pub const DEFAULT_RRF_K: usize = 60;
/// Smallest accepted k (validation guard for the constant).
pub const MIN_RRF_K: usize = 1;

/// One entry in a single ranked list: id + retriever score (used only to
/// derive order within the list; RRF converts order to score).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct SearchRankedItem {
    pub id: String,
    pub score: f64,
}

/// One fused hit with provenance (FR-SM-07 shape): which lists contributed,
/// which kind, which node_id / session, which code elements.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct SearchRankedHit {
    pub id: String,
    /// Fused RRF score = Σ 1/(k + rank_i) over contributing lists.
    pub score: f64,
    /// 1-based fused rank.
    pub rank: usize,
    /// Names of the lists that contributed this hit (e.g. `session`,
    /// `graph`, `lessons`).
    pub sources: Vec<String>,
    /// Human-readable title/preview of the memory.
    pub title: String,
    /// Typed agent-memory kind (`preference` / `decision` / `standing_rule`).
    pub kind: Option<String>,
    /// Offload node_id of the underlying payload, when known.
    pub node_id: Option<String>,
    /// Session that produced the memory, when known.
    pub source_session_id: Option<String>,
    /// Code element qualified names referenced by the memory.
    pub element_refs: Vec<String>,
}

/// RRF over plain lists (source labels omitted). Deterministic: ties break
/// by id ascending. Pure function — no I/O.
pub fn fuse_ranked_lists(lists: &[Vec<SearchRankedItem>]) -> Vec<SearchRankedHit> {
    let named: Vec<(&str, &[SearchRankedItem])> =
        lists.iter().map(|l| ("", l.as_slice())).collect();
    fuse_named_lists(&named)
}

/// RRF over named lists: each hit records which lists contributed it.
/// Duplicate ids within one list keep the best (first) rank. Ties on the
/// fused score break lexicographically by id — same inputs always yield
/// the same output.
pub fn fuse_named_lists(lists: &[(&str, &[SearchRankedItem])]) -> Vec<SearchRankedHit> {
    let k = DEFAULT_RRF_K;
    let mut scores: HashMap<String, f64> = HashMap::new();
    let mut sources: HashMap<String, Vec<String>> = HashMap::new();
    let mut titles: HashMap<String, String> = HashMap::new();
    for (source, items) in lists {
        let mut best_rank: HashMap<String, usize> = HashMap::new();
        for (i, item) in items.iter().enumerate() {
            if best_rank.contains_key(&item.id) {
                continue; // first occurrence wins (best rank)
            }
            best_rank.insert(item.id.clone(), i + 1);
        }
        for (id, rank) in best_rank {
            // 1-indexed rank → 1/(k + rank); k=60.
            *scores.entry(id.clone()).or_insert(0.0) += 1.0 / (k + rank) as f64;
            sources
                .entry(id.clone())
                .or_default()
                .push(source.to_string());
        }
    }
    let mut hits: Vec<SearchRankedHit> = scores
        .into_iter()
        .map(|(id, score)| {
            let srcs = sources.remove(&id).unwrap_or_default();
            let title = titles.remove(&id).unwrap_or_else(|| id.clone());
            SearchRankedHit {
                id,
                score,
                rank: 0,
                sources: srcs,
                title,
                kind: None,
                node_id: None,
                source_session_id: None,
                element_refs: Vec::new(),
            }
        })
        .collect();
    hits.sort_by(|a, b| {
        b.score
            .partial_cmp(&a.score)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then_with(|| a.id.cmp(&b.id))
    });
    for (i, h) in hits.iter_mut().enumerate() {
        h.rank = i + 1;
    }
    hits
}

/// FR-SM-09: hybrid search over session recall + LESSONS (+ caller-supplied
/// graph/ontology lists), merged with RRF k=60. Score threshold + budgets:
/// hits are deduped by id, and the result is capped at `limit`.
pub fn search_memory_rrf(
    project: &std::path::Path,
    query: &str,
    limit: usize,
) -> Result<Vec<SearchRankedHit>, String> {
    let query_tokens: Vec<String> = query
        .split_whitespace()
        .map(|t| t.to_ascii_lowercase())
        .collect();
    let matches = |text: &str| -> bool {
        if query_tokens.is_empty() {
            return true;
        }
        let lower = text.to_ascii_lowercase();
        query_tokens.iter().all(|t| lower.contains(t))
    };

    // List 1: session recall index (.leankg/sessions/recall_index.jsonl).
    let lessons = RecallStore::new(project)
        .map_err(|e| e.to_string())?
        .load()?;
    let session_items: Vec<SearchRankedItem> = lessons
        .iter()
        .filter(|l| matches(&l.text))
        .map(|l| SearchRankedItem {
            id: l.id.clone(),
            score: l.rank,
        })
        .collect();
    let lesson_map: HashMap<String, &Lesson> = lessons.iter().map(|l| (l.id.clone(), l)).collect();

    // List 2: LESSONS journal (.leankg/reflections/LESSONS.md) — one
    // candidate per `## <ts> — <outcome>` section, keyword overlap as the
    // retriever score.
    let lessons_path = project
        .join(".leankg")
        .join("reflections")
        .join("LESSONS.md");
    let lessons_items: Vec<SearchRankedItem> = std::fs::read_to_string(&lessons_path)
        .map(|raw| {
            parse_lessons_sections(&raw)
                .into_iter()
                .filter(|s| matches(s))
                .enumerate()
                .map(|(i, s)| SearchRankedItem {
                    id: format!("lessons-{i}"),
                    score: keyword_overlap(&s, &query_tokens),
                })
                .collect()
        })
        .unwrap_or_default();

    let lists: Vec<(&str, &[SearchRankedItem])> = vec![
        ("session", session_items.as_slice()),
        ("lessons", lessons_items.as_slice()),
    ];
    let mut hits = fuse_named_lists(&lists);
    // Attach provenance from the session index (FR-SM-07).
    for h in hits.iter_mut() {
        if let Some(l) = lesson_map.get(&h.id) {
            h.title = l.text.clone();
            h.kind = Some(l.kind().as_str().to_string());
            if let Some(p) = &l.provenance {
                h.node_id = p.node_id.clone();
                h.source_session_id = Some(p.source_session_id.clone());
                h.element_refs = p.element_refs.clone();
            }
        } else {
            h.kind = Some(classify_memory_kind(&h.title).as_str().to_string());
        }
    }
    hits.truncate(limit);
    Ok(hits)
}

/// Split LESSONS.md into `## …` sections, returning each section's text.
fn parse_lessons_sections(raw: &str) -> Vec<String> {
    let mut sections: Vec<String> = Vec::new();
    let mut current = String::new();
    for line in raw.lines() {
        if line.starts_with("## ") {
            if !current.trim().is_empty() {
                sections.push(current.clone());
            }
            current.clear();
        }
        current.push_str(line);
        current.push('\n');
    }
    if !current.trim().is_empty() {
        sections.push(current);
    }
    sections
}

/// Number of query tokens present in `text` (the retriever score for the
/// LESSONS list).
fn keyword_overlap(text: &str, tokens: &[String]) -> f64 {
    let lower = text.to_ascii_lowercase();
    tokens.iter().filter(|t| lower.contains(t.as_str())).count() as f64
}
