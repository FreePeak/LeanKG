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
            ".rs", ".md", ".go", ".ts", ".js", ".py", ".java", ".cpp", ".c", ".h", ".tsx", ".jsx",
            ".cs", ".swift", ".kt",
        ];
        let path_indicators = [
            "src/",
            "lib/",
            "bin/",
            "test/",
            "tests/",
            "benches/",
            "examples/",
            "docs/",
            "scripts/",
        ];
        // Common action words that often appear between marker and actual target
        let skip_words = [
            "the",
            "a",
            "an",
            "changing",
            "change",
            "changing",
            "update",
            "updating",
            "modifying",
            "modify",
            "editing",
            "edit",
            "removing",
            "remove",
            "adding",
            "add",
            "creating",
            "create",
            "deleting",
            "delete",
            "impact",
            "affect",
            "affected",
            "affecting",
            "file",
            "files",
        ];

        for marker in &markers {
            if let Some(pos) = text.find(marker) {
                let start = pos + marker.len();
                let rest = &text[start..];

                // Find all tokens (words) in the rest of the string
                let words: Vec<&str> = rest.split_whitespace().collect();

                // First, scan for file extensions or paths in all words after the marker
                for word in &words {
                    let cleaned = word.trim_matches(|c: char| c.is_ascii_punctuation());
                    // Check for file with extension
                    if file_extensions.iter().any(|ext| cleaned.ends_with(ext)) {
                        return Some(cleaned.to_string());
                    }
                    // Check for path-like structures (e.g., src/main.rs)
                    if path_indicators.iter().any(|p| cleaned.starts_with(p))
                        && cleaned.contains('/')
                    {
                        return Some(cleaned.to_string());
                    }
                }

                // If no file/path found, try the first word if it's not a skip word
                if let Some(first_word) = words.first() {
                    let cleaned = first_word.trim_matches(|c: char| c.is_ascii_punctuation());
                    if !cleaned.is_empty() && cleaned.len() > 1 && !skip_words.contains(&cleaned) {
                        // Check for module-like names (lowercase with underscores)
                        if cleaned.contains('_')
                            && cleaned
                                .chars()
                                .next()
                                .map(|c| c.is_lowercase())
                                .unwrap_or(false)
                        {
                            return Some(cleaned.to_string());
                        }

                        // Check for file extension in first token
                        if file_extensions.iter().any(|ext| cleaned.ends_with(ext)) {
                            return Some(cleaned.to_string());
                        }
                    }
                }

                // Try to find file extension anywhere in the remaining text
                for (i, _) in rest.char_indices() {
                    if file_extensions.iter().any(|ext| rest[i..].starts_with(ext)) {
                        let remaining = &rest[i..];
                        let word_end = remaining
                            .find(|c: char| {
                                c.is_whitespace() || c == ',' || c == '\n' || c == '"' || c == '\''
                            })
                            .map(|p| i + p)
                            .unwrap_or(rest.len());
                        let word_start = rest[..i]
                            .rfind(|c: char| {
                                c.is_whitespace() || c == ',' || c == '\n' || c == '"' || c == '\''
                            })
                            .map(|p| p + 1)
                            .unwrap_or(0);
                        let candidate = &rest[word_start..word_end];
                        if !candidate.is_empty() && candidate.len() > 1 {
                            return Some(candidate.to_string());
                        }
                    }
                }
            }
        }

        // Try to find a module-like name (lowercase with underscores, e.g., "orchestrator", "cache_module")
        // This helps when user says "show me the orchestrator module" without a marker
        let words: Vec<&str> = text.split_whitespace().collect();

        // First priority: any word with a file extension
        for word in &words {
            let cleaned = word.trim_matches(|c: char| c.is_ascii_punctuation());
            if file_extensions.iter().any(|ext| cleaned.ends_with(ext)) {
                return Some(cleaned.to_string());
            }
        }

        // Second priority: module names with underscores (e.g., "cache_module")
        for word in &words {
            let cleaned = word.trim_matches(|c: char| c.is_ascii_punctuation());
            if cleaned.len() >= 3
                && cleaned
                    .chars()
                    .next()
                    .map(|c| c.is_lowercase())
                    .unwrap_or(false)
                && cleaned.contains('_')
            {
                return Some(cleaned.to_string());
            }
        }

        // Third priority: single lowercase words that might be module names (e.g., "orchestrator")
        for word in &words {
            let cleaned = word.trim_matches(|c: char| c.is_ascii_punctuation());
            // Skip common words, look for likely module names
            if cleaned.len() >= 4
                && cleaned
                    .chars()
                    .next()
                    .map(|c| c.is_lowercase())
                    .unwrap_or(false)
                && ![
                    "the", "for", "with", "from", "this", "that", "file", "module", "show",
                ]
                .contains(&cleaned)
            {
                // Check if it looks like a module/class name (camelCase or snake_case)
                if cleaned.contains('_')
                    || (cleaned.chars().all(|c| c.is_lowercase()) && cleaned.len() > 6)
                {
                    return Some(cleaned.to_string());
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

    #[test]
    fn test_parse_impact_intent_with_path() {
        let parser = IntentParser::new();

        // This is the specific failing case: action word "changing" between marker and path
        let intent = parser.parse("show me impact of changing src/mcp/handler.rs");
        assert_eq!(intent.query_type, "impact");
        assert_eq!(intent.target, Some("src/mcp/handler.rs".to_string()));
    }
}
