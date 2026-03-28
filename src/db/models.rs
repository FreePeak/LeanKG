#![allow(dead_code)]
use serde::{Deserialize, Serialize};

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub enum RelationshipType {
    Imports,
    Calls,
    References,
    DocumentedBy,
    TestedBy,
    Tests,
    Contains,
    Defines,
    Implements,
    Implementations,
}

impl RelationshipType {
    pub fn as_str(&self) -> &'static str {
        match self {
            RelationshipType::Imports => "imports",
            RelationshipType::Calls => "calls",
            RelationshipType::References => "references",
            RelationshipType::DocumentedBy => "documented_by",
            RelationshipType::TestedBy => "tested_by",
            RelationshipType::Tests => "tests",
            RelationshipType::Contains => "contains",
            RelationshipType::Defines => "defines",
            RelationshipType::Implements => "implements",
            RelationshipType::Implementations => "implementations",
        }
    }

    #[allow(dead_code)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "imports" => Some(RelationshipType::Imports),
            "calls" => Some(RelationshipType::Calls),
            "references" => Some(RelationshipType::References),
            "documented_by" => Some(RelationshipType::DocumentedBy),
            "tested_by" => Some(RelationshipType::TestedBy),
            "tests" => Some(RelationshipType::Tests),
            "contains" => Some(RelationshipType::Contains),
            "defines" => Some(RelationshipType::Defines),
            "implements" => Some(RelationshipType::Implements),
            "implementations" => Some(RelationshipType::Implementations),
            _ => None,
        }
    }
}

impl std::fmt::Display for RelationshipType {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.as_str())
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CodeElement {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub language: String,
    pub parent_qualified: Option<String>,
    #[serde(default)]
    pub cluster_id: Option<String>,
    #[serde(default)]
    pub cluster_label: Option<String>,
    #[serde(default)]
    pub metadata: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Relationship {
    #[serde(skip)]
    #[allow(dead_code)]
    pub id: Option<String>,
    pub source_qualified: String,
    pub target_qualified: String,
    pub rel_type: String,
    pub confidence: f64,
    pub metadata: serde_json::Value,
}

impl Relationship {
    pub fn severity(&self, depth: u32) -> &'static str {
        if depth == 1 && self.confidence >= 0.85 {
            "WILL BREAK"
        } else if depth == 1 && self.confidence >= 0.60 {
            "LIKELY AFFECTED"
        } else {
            "MAY BE AFFECTED"
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BusinessLogic {
    #[serde(skip)]
    #[allow(dead_code)]
    pub id: Option<String>,
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct BusinessLogicWithDoc {
    pub business_logic: BusinessLogic,
    pub doc_links: Vec<DocLink>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DocLink {
    pub doc_qualified: String,
    pub doc_title: String,
    pub context: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraceabilityEntry {
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
    pub feature_id: Option<String>,
    pub doc_links: Vec<DocLink>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TraceabilityReport {
    pub element_qualified: String,
    pub entries: Vec<TraceabilityEntry>,
    pub count: usize,
}

#[allow(dead_code)]
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
            ..Default::default()
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
            confidence: 1.0,
            metadata: serde_json::json!({}),
        };
        assert_eq!(rel.rel_type, "imports");
        assert_eq!(rel.confidence, 1.0);
    }

    #[test]
    fn test_relationship_type_display() {
        assert_eq!(RelationshipType::Imports.as_str(), "imports");
        assert_eq!(
            RelationshipType::Implementations.as_str(),
            "implementations"
        );
        assert_eq!(format!("{}", RelationshipType::Calls), "calls");
    }

    #[test]
    fn test_relationship_type_from_str() {
        assert_eq!(
            RelationshipType::from_str("imports"),
            Some(RelationshipType::Imports)
        );
        assert_eq!(
            RelationshipType::from_str("implementations"),
            Some(RelationshipType::Implementations)
        );
        assert_eq!(RelationshipType::from_str("unknown"), None);
    }
}
