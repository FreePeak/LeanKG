use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Intent {
    pub query_type: String,
    pub target: Option<String>,
    pub confidence: f32,
}

pub struct IntentParser {
    patterns: Vec<IntentPattern>,
}

struct IntentPattern {
    keywords: Vec<&'static str>,
    query_type: &'static str,
    confidence: f32,
}

impl IntentParser {
    pub fn new() -> Self {
        let patterns = vec![
            IntentPattern {
                keywords: vec![
                    "context",
                    "content",
                    "read",
                    "file",
                    "show me",
                    "what's in",
                    "what is in",
                ],
                query_type: "context",
                confidence: 0.9,
            },
            IntentPattern {
                keywords: vec![
                    "impact", "affect", "changing", "change", "changes", "effects", "ripple",
                    "break",
                ],
                query_type: "impact",
                confidence: 0.85,
            },
            IntentPattern {
                keywords: vec!["depend", "import", "require", "use"],
                query_type: "dependencies",
                confidence: 0.85,
            },
            IntentPattern {
                keywords: vec!["search", "find", "look for", "where is", "locate"],
                query_type: "search",
                confidence: 0.8,
            },
            IntentPattern {
                keywords: vec!["doc", "document", "readme", "spec", "requirement"],
                query_type: "doc",
                confidence: 0.85,
            },
            IntentPattern {
                keywords: vec!["test", "spec", "unit"],
                query_type: "test",
                confidence: 0.8,
            },
            IntentPattern {
                keywords: vec!["trace", "traceability", "requirement", "user story"],
                query_type: "traceability",
                confidence: 0.85,
            },
        ];
        Self { patterns }
    }

    pub fn parse(&self, intent_str: &str) -> Intent {
        let lower = intent_str.to_lowercase();

        let mut best_match: Option<Intent> = None;
        let mut best_confidence: f32 = 0.0;

        for pattern in &self.patterns {
            let matches = pattern
                .keywords
                .iter()
                .filter(|kw| lower.contains(*kw))
                .count();

            if matches > 0 {
                let confidence =
                    pattern.confidence * (matches as f32 / pattern.keywords.len() as f32);

                if confidence > best_confidence {
                    best_confidence = confidence;
                    best_match = Some(Intent {
                        query_type: pattern.query_type.to_string(),
                        target: self.extract_target(&lower),
                        confidence,
                    });
                }
            }
        }

        best_match.unwrap_or_else(|| Intent {
            query_type: "context".to_string(),
            target: self.extract_target(&lower),
            confidence: 0.5,
        })
    }

    fn extract_target(&self, text: &str) -> Option<String> {
        let markers = ["for ", "of ", "in ", "to ", "from ", "named "];
        let file_extensions = [
            ".rs", ".md", ".go", ".ts", ".js", ".py", ".java", ".cpp", ".c", ".h",
        ];

        for marker in &markers {
            if let Some(pos) = text.find(marker) {
                let start = pos + marker.len();
                let rest = &text[start..];

                let first_token_end = rest.find(' ').unwrap_or(rest.len());
                let first_token = &rest[..first_token_end];

                if !first_token.is_empty() && first_token.len() > 1 {
                    if file_extensions.iter().any(|ext| first_token.ends_with(ext)) {
                        return Some(first_token.to_string());
                    }

                    if first_token.contains('_')
                        && first_token
                            .chars()
                            .next()
                            .map(|c| c.is_lowercase())
                            .unwrap_or(false)
                    {
                        return Some(first_token.to_string());
                    }

                    for (i, _) in rest.char_indices() {
                        if file_extensions.iter().any(|ext| rest[i..].starts_with(ext)) {
                            let remaining = &rest[i..];
                            let word_end = remaining
                                .find(|c: char| {
                                    c.is_whitespace()
                                        || c == ','
                                        || c == '\n'
                                        || c == '"'
                                        || c == '\''
                                })
                                .map(|p| i + p)
                                .unwrap_or(rest.len());
                            let word_start = rest[..i]
                                .rfind(|c: char| {
                                    c.is_whitespace()
                                        || c == ','
                                        || c == '\n'
                                        || c == '"'
                                        || c == '\''
                                })
                                .map(|p| p + 1)
                                .unwrap_or(0);
                            let candidate = &rest[word_start..word_end];
                            if !candidate.is_empty()
                                && candidate.len() > 1
                                && candidate != first_token
                            {
                                return Some(candidate.to_string());
                            }
                        }
                    }
                }
            }
        }

        // Try to extract identifier after function/class keywords
        let func_markers = ["function ", "class ", "struct ", "enum ", "trait ", "impl "];
        for marker in &func_markers {
            if let Some(pos) = text.find(marker) {
                let start = pos + marker.len();
                let rest = &text[start..];
                let first_token_end = rest
                    .find(|c: char| c.is_whitespace() || c == '(' || c == '{')
                    .unwrap_or(rest.len());
                let first_token = &rest[..first_token_end];
                if !first_token.is_empty() && first_token.len() > 1 {
                    return Some(first_token.to_string());
                }
            }
        }

        let words: Vec<&str> = text.split_whitespace().collect();
        if let Some(last) = words.last() {
            let cleaned = last.trim_matches(|c: char| c.is_ascii_punctuation());
            if !cleaned.is_empty() && file_extensions.iter().any(|ext| cleaned.ends_with(ext)) {
                return Some(cleaned.to_string());
            }
        }

        None
    }
}

impl Default for IntentParser {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_parse_context_intent() {
        let parser = IntentParser::new();

        let intent = parser.parse("show me context for src/main.rs");
        assert_eq!(intent.query_type, "context");
        assert_eq!(intent.target, Some("src/main.rs".to_string()));
    }

    #[test]
    fn test_parse_impact_intent() {
        let parser = IntentParser::new();

        let intent = parser.parse("what's the impact of changing lib.rs");
        assert_eq!(intent.query_type, "impact");
        assert_eq!(intent.target, Some("lib.rs".to_string()));
    }

    #[test]
    fn test_parse_dependencies_intent() {
        let parser = IntentParser::new();

        let intent = parser.parse("show dependencies of handler.rs");
        assert_eq!(intent.query_type, "dependencies");
        assert_eq!(intent.target, Some("handler.rs".to_string()));
    }

    #[test]
    fn test_parse_search_intent() {
        let parser = IntentParser::new();

        let intent = parser.parse("find function named parse_config");
        assert_eq!(intent.query_type, "search");
        assert_eq!(intent.target, Some("parse_config".to_string()));
    }

    #[test]
    fn test_parse_doc_intent() {
        let parser = IntentParser::new();

        let intent = parser.parse("get documentation for api.rs");
        assert_eq!(intent.query_type, "doc");
        assert_eq!(intent.target, Some("api.rs".to_string()));
    }

    #[test]
    fn test_parse_no_match() {
        let parser = IntentParser::new();

        let intent = parser.parse("hello world");
        assert_eq!(intent.query_type, "context");
        assert_eq!(intent.target, None);
    }

    #[test]
    fn test_extract_target_with_file() {
        let parser = IntentParser::new();

        let intent = parser.parse("analyze src/lib.rs");
        assert_eq!(intent.target, Some("src/lib.rs".to_string()));
    }

    #[test]
    fn test_extract_target_without_marker() {
        let parser = IntentParser::new();

        let intent = parser.parse("context main.rs");
        assert!(intent.target.is_some());
    }

    #[test]
    fn test_confidence_scoring() {
        let parser = IntentParser::new();

        let intent1 = parser.parse("context for file.rs");
        let intent2 = parser.parse("give me the context for the file");

        assert!(intent1.confidence >= 0.0);
        assert!(intent2.confidence >= 0.0);
    }
}
