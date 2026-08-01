//! Format-specific export JSON parsers (FR-MP-09 / FR-MP-10 / FR-MP-11).
//!
//! Each parser normalizes its format's shape into `RawMessage` records;
//! classification lives in `types.rs` so mining semantics are shared.

use serde::Deserialize;
use serde_json::Value;

/// Normalized message record shared by all three parsers.
#[derive(Debug, Clone)]
pub struct RawMessage {
    pub participant: String,
    pub timestamp: String,
    pub text: String,
    pub source: String,
}

fn extract_text_parts(value: &Value) -> String {
    // Claude: content is either a string, or an array of blocks with "text".
    // ChatGPT: content is an object { content_type, parts: [String] }.
    match value {
        Value::String(s) => s.clone(),
        Value::Array(blocks) => {
            let mut out = Vec::new();
            for block in blocks {
                if let Some(text) = block.get("text").and_then(|t| t.as_str()) {
                    out.push(text.to_string());
                }
            }
            out.join("\n")
        }
        Value::Object(map) => {
            if let Some(parts) = map.get("parts").and_then(|p| p.as_array()) {
                let mut out = Vec::new();
                for part in parts {
                    if let Some(s) = part.as_str() {
                        out.push(s.to_string());
                    }
                }
                return out.join("\n");
            }
            map.get("text")
                .and_then(|t| t.as_str())
                .unwrap_or("")
                .to_string()
        }
        _ => String::new(),
    }
}

// ---------------------------------------------------------------- Claude

#[derive(Deserialize)]
struct ClaudeExport {
    conversations: Vec<ClaudeConversation>,
}

#[derive(Deserialize)]
struct ClaudeConversation {
    messages: Vec<ClaudeMessage>,
}

#[derive(Deserialize)]
struct ClaudeMessage {
    role: Option<String>,
    text: Option<String>,
    content: Option<Value>,
}

/// Claude export: `{ "conversations": [ { "messages": [ { role, content } ] } ] }`.
pub fn parse_claude_export(content: &str) -> Result<Vec<RawMessage>, String> {
    let export: ClaudeExport =
        serde_json::from_str(content).map_err(|e| format!("not a Claude export: {e}"))?;
    let mut out = Vec::new();
    for conv in export.conversations {
        for msg in conv.messages {
            let text = match (&msg.text, &msg.content) {
                (Some(t), _) => t.clone(),
                (None, Some(c)) => extract_text_parts(c),
                (None, None) => String::new(),
            };
            out.push(RawMessage {
                participant: msg.role.unwrap_or_else(|| "user".to_string()),
                timestamp: String::new(),
                text,
                source: "claude".to_string(),
            });
        }
    }
    Ok(out)
}

// ---------------------------------------------------------------- ChatGPT

#[derive(Deserialize)]
struct ChatGptExport {
    mapping: std::collections::BTreeMap<String, ChatGptNode>,
}

#[derive(Deserialize)]
struct ChatGptNode {
    #[serde(default)]
    message: Option<ChatGptMessage>,
    #[serde(default)]
    mapping: Option<std::collections::BTreeMap<String, ChatGptNode>>,
}

#[derive(Deserialize)]
struct ChatGptMessage {
    author: Option<ChatGptAuthor>,
    #[serde(default)]
    content: Option<Value>,
}

#[derive(Deserialize)]
struct ChatGptAuthor {
    #[serde(default)]
    role: Option<String>,
}

/// ChatGPT export: `{ "mapping": { "<id>": { "message": { author.role,
/// content.parts[] } } } }`. Nested `mapping` subtrees (conversation nodes
/// that wrap child nodes) are walked recursively.
pub fn parse_chatgpt_export(content: &str) -> Result<Vec<RawMessage>, String> {
    let export: ChatGptExport =
        serde_json::from_str(content).map_err(|e| format!("not a ChatGPT export: {e}"))?;
    let mut out = Vec::new();
    collect_chatgpt_nodes(&export.mapping, &mut out);
    Ok(out)
}

fn collect_chatgpt_nodes(
    nodes: &std::collections::BTreeMap<String, ChatGptNode>,
    out: &mut Vec<RawMessage>,
) {
    for node in nodes.values() {
        if let Some(msg) = &node.message {
            if let Some(content) = msg.content.as_ref() {
                let text = extract_text_parts(content);
                if !text.trim().is_empty() {
                    out.push(RawMessage {
                        participant: msg
                            .author
                            .as_ref()
                            .and_then(|a| a.role.clone())
                            .unwrap_or_else(|| "user".to_string()),
                        timestamp: String::new(),
                        text,
                        source: "chatgpt".to_string(),
                    });
                }
            }
        }
        if let Some(nested) = &node.mapping {
            collect_chatgpt_nodes(nested, out);
        }
    }
}

// ---------------------------------------------------------------- Slack

#[derive(Deserialize)]
struct SlackExport {
    #[serde(default)]
    #[allow(dead_code)] // shape marker: distinguishes Slack channel exports
    channel: Option<Value>,
    messages: Vec<SlackMessage>,
}

#[derive(Deserialize)]
struct SlackMessage {
    #[serde(default)]
    r#type: Option<String>,
    #[serde(default)]
    user: Option<String>,
    #[serde(default)]
    text: Option<String>,
}

/// Slack channel export: `{ "channel": {...}, "messages": [ { user, ts,
/// text } ] }`. Works for both full channel files and files.json-style
/// single-message objects that wrap a `message` field.
pub fn parse_slack_export(content: &str) -> Result<Vec<RawMessage>, String> {
    let export: SlackExport =
        serde_json::from_str(content).map_err(|e| format!("not a Slack export: {e}"))?;
    let mut out = Vec::new();
    for msg in export.messages {
        if msg.r#type.as_deref() == Some("message") || msg.text.is_some() {
            out.push(RawMessage {
                participant: msg.user.unwrap_or_else(|| "unknown".to_string()),
                timestamp: String::new(),
                text: msg.text.unwrap_or_default(),
                source: "slack".to_string(),
            });
        }
    }
    Ok(out)
}
