//! Conversation mining (US-MP-03 / FR-MP-09..13).
//!
//! Parses Claude / ChatGPT / Slack export JSON into typed `MinedItem`s
//! (decision / preference / milestone / problem) and persists them into the
//! graph as `decision` / `preference` / `milestone` / `problem` element
//! types with a `decided_about` edge from decision nodes to code elements.
//! Raw verbatim text is stored — no summarization.
//!
//! Design (ponytail): parsers stay format-local and produce a flat list of
//! `RawMessage { participant, timestamp, text }`. A single shared
//! `classify()` + keyword extractor turns messages into `MinedItem`s, so
//! all three formats get identical mining semantics for free. Persistence
//! reuses `GraphEngine::insert_elements` / `insert_relationships` — no new
//! schema tables.

mod parsers;
mod types;

use crate::graph::GraphEngine;
use parsers::{parse_chatgpt_export, parse_claude_export, parse_slack_export, RawMessage};
#[allow(unused_imports)] // re-exported for external callers (tests / CLI)
pub use types::{MinedItem, MinedItemKind, MiningResult};

use std::path::{Path, PathBuf};

/// Export format selector for `leankg mine-conversations --format`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConversationFormat {
    Claude,
    ChatGpt,
    Slack,
    Unknown,
}

impl ConversationFormat {
    /// Parse a `--format` CLI value ("claude" | "chatgpt" | "slack").
    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Self {
        match s.to_ascii_lowercase().as_str() {
            "claude" => Self::Claude,
            "chatgpt" => Self::ChatGpt,
            "slack" => Self::Slack,
            _ => Self::Unknown,
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Claude => "claude",
            Self::ChatGpt => "chatgpt",
            Self::Slack => "slack",
            Self::Unknown => "unknown",
        }
    }
}

/// Extract raw messages from a single export file in the given format.
fn parse_file(path: &Path, format: ConversationFormat) -> Result<Vec<RawMessage>, String> {
    let content = std::fs::read_to_string(path)
        .map_err(|e| format!("cannot read {}: {}", path.display(), e))?;
    let raw = match format {
        ConversationFormat::Claude => parse_claude_export(&content),
        ConversationFormat::ChatGpt => parse_chatgpt_export(&content),
        ConversationFormat::Slack => parse_slack_export(&content),
        ConversationFormat::Unknown => {
            return Err("unknown format; use --format claude|chatgpt|slack".to_string());
        }
    }
    .map_err(|e| format!("{}: {}", path.display(), e))?;
    Ok(raw)
}

fn is_export_file(path: &Path) -> bool {
    path.extension().and_then(|e| e.to_str()) == Some("json")
}

/// Mine a single file (fixture / CLI --input file).
pub fn mine_file(path: &Path, format: ConversationFormat) -> Result<Vec<MinedItem>, String> {
    let messages = parse_file(path, format)?;
    Ok(messages
        .into_iter()
        .filter_map(MinedItem::from_message)
        .collect())
}

/// Mine a directory of exports (CLI --input dir). Only files whose shape
/// matches the format's root keys are parsed; files of other formats are
/// skipped with a warning. `sources` counts parsed files.
pub fn mine_directory(dir: &Path, format: ConversationFormat) -> Result<MiningResult, String> {
    let mut all_items = Vec::new();
    let mut sources = 0usize;

    if dir.is_file() {
        let items = mine_file(dir, format)?;
        sources = 1;
        all_items.extend(items);
        return Ok(MiningResult {
            items: all_items,
            sources,
            elements_indexed: 0,
            relationships_created: 0,
        });
    }

    if !dir.exists() {
        return Err(format!("input path not found: {}", dir.display()));
    }

    let mut entries: Vec<PathBuf> = std::fs::read_dir(dir)
        .map_err(|e| format!("cannot read {}: {}", dir.display(), e))?
        .filter_map(|e| e.ok().map(|e| e.path()))
        .filter(|p| p.is_file() && is_export_file(p))
        .collect();
    entries.sort();

    for path in entries {
        match parse_file(&path, format) {
            Ok(messages) => {
                sources += 1;
                all_items.extend(messages.into_iter().filter_map(MinedItem::from_message));
            }
            Err(e) => {
                eprintln!("[mine-conversations] skipping {}: {}", path.display(), e);
            }
        }
    }

    Ok(MiningResult {
        items: all_items,
        sources,
        elements_indexed: 0,
        relationships_created: 0,
    })
}

/// Persist mined items into the project's graph (CLI entry point).
pub fn mine_into_project(
    project: &Path,
    input: &Path,
    format: ConversationFormat,
) -> Result<MiningResult, Box<dyn std::error::Error>> {
    let mut result = mine_directory(input, format)?;
    if result.items.is_empty() {
        return Ok(result);
    }
    let persisted = index_items_into(project, result.items.clone())?;
    result.elements_indexed = persisted.elements_indexed;
    result.relationships_created = persisted.relationships_created;
    Ok(result)
}

fn index_items_into(
    project: &Path,
    items: Vec<MinedItem>,
) -> Result<MiningResult, Box<dyn std::error::Error>> {
    let db_path = project.join(".leankg");
    let db = crate::db::schema::init_db(&db_path)?;
    let graph = GraphEngine::new(db);
    index_items(&graph, project, items)
}

/// Public seam (tested directly): insert mined items into an open graph.
pub fn index_items(
    graph: &GraphEngine,
    project: &Path,
    items: Vec<MinedItem>,
) -> Result<MiningResult, Box<dyn std::error::Error>> {
    let mut elements = Vec::new();
    let mut relationships = Vec::new();

    for item in &items {
        let (element, rels) = item.to_graph_elements(project);
        elements.push(element);
        relationships.extend(rels);
    }

    if !elements.is_empty() {
        graph.insert_elements(&elements)?;
    }
    if !relationships.is_empty() {
        graph.insert_relationships(&relationships)?;
    }

    Ok(MiningResult {
        items,
        sources: 0,
        elements_indexed: elements.len(),
        relationships_created: relationships.len(),
    })
}
