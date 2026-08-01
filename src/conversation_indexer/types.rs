//! Mined item model + classification (FR-MP-12).

use crate::db::models::{CodeElement, Relationship};
use std::path::Path;

/// Mined conversation item kind (element type in the graph).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MinedItemKind {
    Decision,
    Preference,
    Milestone,
    Problem,
    General,
}

impl MinedItemKind {
    /// Deterministic keyword classifier over a raw message. Prefix labels
    /// ("decision:", "preference:", ...) win; otherwise problem phrases
    /// first (most specific), then decision / milestone / preference
    /// keyword heuristics. Falls back to `General`.
    pub fn classify(text: &str) -> Self {
        let t = text.trim();
        let lower = t.to_ascii_lowercase();

        // Explicit labels
        for (label, kind) in [
            ("decision:", Self::Decision),
            ("preference:", Self::Preference),
            ("milestone:", Self::Milestone),
            ("problem:", Self::Problem),
        ] {
            if lower.starts_with(label) {
                return kind;
            }
        }

        // Problem signals (verb-first, passive failure phrasing)
        if [
            "fails",
            "failure",
            "broken",
            "bug",
            "timeout",
            "crashes",
            "error",
            "timing out",
            "stuck",
            "keeps",
        ]
        .iter()
        .any(|k| lower.contains(k))
        {
            return Self::Problem;
        }

        // Decision signals
        if [
            "we decided",
            "decision",
            "we will",
            "we should",
            "we are going to",
            "let's use",
        ]
        .iter()
        .any(|k| lower.contains(k))
        {
            return Self::Decision;
        }

        // Milestone signals
        if [
            "milestone",
            "goal",
            "deadline",
            "by end of",
            "target date",
            "ship by",
            "by q",
        ]
        .iter()
        .any(|k| lower.contains(k))
        {
            return Self::Milestone;
        }

        // Preference signals
        if [
            "prefer",
            "preference",
            "preferred",
            "rather use",
            "would like",
        ]
        .iter()
        .any(|k| lower.contains(k))
        {
            return Self::Preference;
        }

        Self::General
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Decision => "decision",
            Self::Preference => "preference",
            Self::Milestone => "milestone",
            Self::Problem => "problem",
            Self::General => "general",
        }
    }
}

/// A mined conversation item. Raw verbatim is stored — no summarization.
#[derive(Debug, Clone)]
pub struct MinedItem {
    pub kind: MinedItemKind,
    pub verbatim: String,
    pub source: String,
    pub participants: Vec<String>,
    pub timestamp: String,
    pub topic: String,
    pub code_targets: Vec<String>,
}

/// Deterministic topic extractor: first identifier-ish token set (e.g.
/// "RS256", "JWT", "gRPC", "PostgreSQL"). Used for the node `name`.
fn extract_topic(text: &str) -> String {
    let stopwords = [
        "decision",
        "preference",
        "milestone",
        "problem",
        "adopt",
        "use",
        "switch",
        "migrate",
        "prefer",
        "go",
        "pick",
        "choose",
        "we",
        "the",
        "our",
        "new",
        "for",
        "with",
        "and",
    ];
    for token in text.split(|c: char| !c.is_alphanumeric() && c != '_') {
        let t = token.trim();
        let lower = t.to_ascii_lowercase();
        if t.len() >= 3 && !stopwords.contains(&lower.as_str()) {
            return t.to_string();
        }
    }
    text.chars().take(40).collect()
}

/// Deterministic code-target extraction: backtick-quoted paths, inline
/// `file::symbol` patterns, and bare `src/...` paths inside the text.
fn extract_code_targets(text: &str) -> Vec<String> {
    use regex::Regex;

    let mut targets = Vec::new();

    let backtick = Regex::new(r"`([^`]+)`").unwrap();
    for cap in backtick.captures_iter(text) {
        if let Some(m) = cap.get(1) {
            targets.push(m.as_str().to_string());
        }
    }

    let file_symbol =
        Regex::new(r"\b([A-Za-z0-9_\-./]+\.(?:rs|go|ts|js|py|java|kt))::([A-Za-z0-9_]+)\b")
            .unwrap();
    for cap in file_symbol.captures_iter(text) {
        let full = format!("{}::{}", &cap[1], &cap[2]);
        if !targets.contains(&full) {
            targets.push(full);
        }
    }

    let src_path = Regex::new(r"\bsrc/[A-Za-z0-9_\-./]+\.(?:rs|go|ts|js|py|java|kt)\b").unwrap();
    for m in src_path.find_iter(text) {
        let s = m.as_str().to_string();
        if !targets.contains(&s) {
            targets.push(s);
        }
    }

    targets
}

impl MinedItem {
    /// Build a mined item from one raw message; `None` when the message is
    /// not classifiable.
    pub fn from_message(message: crate::conversation_indexer::parsers::RawMessage) -> Option<Self> {
        let text = message.text.trim().to_string();
        if text.is_empty() {
            return None;
        }
        let kind = MinedItemKind::classify(&text);
        if kind == MinedItemKind::General {
            return None;
        }
        let topic = extract_topic(&text);
        let code_targets = extract_code_targets(&text);
        Some(Self {
            kind,
            verbatim: text,
            source: message.source,
            participants: vec![message.participant],
            timestamp: message.timestamp,
            topic,
            code_targets,
        })
    }

    /// Qualified name of this item's graph node.
    pub fn qualified_name(&self, project: &Path) -> String {
        let project_name = project
            .file_name()
            .and_then(|n| n.to_str())
            .unwrap_or("project");
        let slug: String = self
            .topic
            .chars()
            .map(|c| {
                if c.is_alphanumeric() {
                    c.to_ascii_lowercase()
                } else {
                    '_'
                }
            })
            .collect();
        format!(
            "conversations/{project_name}/{}/{}",
            self.kind.as_str(),
            slug
        )
    }

    /// Element + relationships for the graph. Decision nodes link to code
    /// targets via `decided_about`; other kinds point back at the decision
    /// context only when a decision exists (handled by the caller).
    pub fn to_graph_elements(&self, project: &Path) -> (CodeElement, Vec<Relationship>) {
        let qn = self.qualified_name(project);
        let element = CodeElement {
            qualified_name: qn.clone(),
            element_type: self.kind.as_str().to_string(),
            name: self.topic.clone(),
            file_path: format!("conversations/{}/{}", self.source, self.kind.as_str()),
            line_start: 1,
            line_end: 1,
            language: "conversation".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({
                "kind": self.kind.as_str(),
                "verbatim": self.verbatim,
                "source": self.source,
                "participants": self.participants,
                "timestamp": self.timestamp,
                "topic": self.topic,
            }),
            ..Default::default()
        };

        let mut relationships = Vec::new();
        if self.kind == MinedItemKind::Decision {
            for target in &self.code_targets {
                relationships.push(Relationship {
                    id: None,
                    source_qualified: qn.clone(),
                    target_qualified: target.clone(),
                    rel_type: "decided_about".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "verbatim": self.verbatim,
                        "confidence_label": "EXTRACTED",
                    }),
                    ..Default::default()
                });
            }
        }

        (element, relationships)
    }
}

/// Aggregated result of a mining run.
#[derive(Debug, Clone)]
pub struct MiningResult {
    pub items: Vec<MinedItem>,
    pub sources: usize,
    pub elements_indexed: usize,
    pub relationships_created: usize,
}

impl MiningResult {
    pub fn summary(&self) -> String {
        let mut kinds: Vec<String> = self
            .items
            .iter()
            .map(|i| i.kind.as_str().to_string())
            .collect();
        kinds.sort();
        kinds.dedup();
        let item_word = if self.items.len() == 1 {
            "item"
        } else {
            "items"
        };
        format!(
            "Mined {} {} from {} source(s) [{}]: {} elements, {} relationships",
            self.items.len(),
            item_word,
            self.sources,
            kinds.join(", "),
            self.elements_indexed,
            self.relationships_created
        )
    }
}
