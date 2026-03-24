use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CodeElement {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub language: String,
    pub parent_qualified: Option<String>,
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Relationship {
    #[serde(skip)]
    pub id: Option<String>,
    pub source_qualified: String,
    pub target_qualified: String,
    pub rel_type: String,
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BusinessLogic {
    #[serde(skip)]
    pub id: Option<String>,
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Document {
    #[serde(skip)]
    pub id: Option<String>,
    pub title: String,
    pub content: String,
    pub file_path: String,
    pub generated_from: Vec<String>,
    pub last_updated: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_code_element_creation() {
        let elem = CodeElement {
            qualified_name: "src/main.rs::main".to_string(),
            element_type: "function".to_string(),
            name: "main".to_string(),
            file_path: "src/main.rs".to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
        };
        assert_eq!(elem.name, "main");
    }

    #[test]
    fn test_relationship_creation() {
        let rel = Relationship {
            id: None,
            source_qualified: "a.go".to_string(),
            target_qualified: "b.go".to_string(),
            rel_type: "imports".to_string(),
            metadata: serde_json::json!({}),
        };
        assert_eq!(rel.rel_type, "imports");
    }
}
