//! Token tracking utilities for Kilo session analysis

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct SessionInfo {
    pub session_id: String,
    pub title: String,
    pub directory: String,
    pub total_messages: usize,
    pub tool_calls: usize,
    pub leankg_calls: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TokenStats {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub cache_read_tokens: u64,
}

pub fn parse_session(data: &[u8]) -> Result<SessionInfo, String> {
    let json: serde_json::Value =
        serde_json::from_slice(data).map_err(|e| format!("Failed to parse JSON: {}", e))?;

    let info = json.get("info").ok_or("Missing 'info' field")?;
    let messages = json.get("messages").ok_or("Missing 'messages' field")?;

    let session_id = info
        .get("id")
        .and_then(|v| v.as_str())
        .unwrap_or("unknown")
        .to_string();

    let title = info
        .get("title")
        .and_then(|v| v.as_str())
        .unwrap_or("unknown")
        .to_string();

    let directory = info
        .get("directory")
        .and_then(|v| v.as_str())
        .unwrap_or("")
        .to_string();

    let total_messages = messages.as_array().map(|arr| arr.len()).unwrap_or(0);

    Ok(SessionInfo {
        session_id,
        title,
        directory,
        total_messages,
        tool_calls: 0,
        leankg_calls: Vec::new(),
    })
}

pub fn extract_tool_calls(messages: &[serde_json::Value]) -> Vec<String> {
    let mut tools = Vec::new();
    for msg in messages {
        if let Some(tool_calls) = msg.get("tool_calls").and_then(|v| v.as_array()) {
            for tc in tool_calls {
                if let Some(name) = tc.get("name").and_then(|v| v.as_str()) {
                    tools.push(name.to_string());
                }
            }
        }
    }
    tools
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_session_structure() {
        let mock_session = r#"{
            "info": {
                "id": "test_123",
                "title": "Test Session",
                "directory": "/test"
            },
            "messages": []
        }"#;

        let result = parse_session(mock_session.as_bytes());
        assert!(result.is_ok());
        let parsed = result.unwrap();
        assert_eq!(parsed.session_id, "test_123");
        assert_eq!(parsed.title, "Test Session");
        assert_eq!(parsed.directory, "/test");
        assert_eq!(parsed.total_messages, 0);
    }

    #[test]
    fn test_extract_tool_calls() {
        let mock_messages = vec![serde_json::json!({
            "tool_calls": [
                {"name": "leankg_mcp_status"},
                {"name": "search_code"}
            ]
        })];

        let tools = extract_tool_calls(&mock_messages);
        assert_eq!(tools.len(), 2);
        assert_eq!(tools[0], "leankg_mcp_status");
        assert_eq!(tools[1], "search_code");
    }
}
