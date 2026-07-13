pub mod concept;
pub mod loader;
pub mod procedural;
pub mod query;
pub mod safe_discover;

// Re-export for convenience
#[allow(unused_imports)]
pub use concept::{ConceptElementType, ConceptMetadata, ConceptNode};
#[allow(unused_imports)]
pub use loader::{
    concept_nodes_to_elements, failure_mode_nodes_to_elements, load_aliases_yaml,
    load_concepts_yaml, load_workflows_yaml, workflow_nodes_to_elements,
    workflow_step_nodes_to_elements,
};
#[allow(unused_imports)]
pub use procedural::{
    FailureModeMetadata, FailureModeNode, ProceduralElementType, WorkflowMetadata, WorkflowNode,
    WorkflowStepMetadata, WorkflowStepNode,
};
#[allow(unused_imports)]
pub use query::{
    calculate_match_score, extract_keywords, normalize_path, ConceptSearchResult, KgSelfTestEntry,
    KgSelfTestReport, MatchedConcept, OntologyContextResult, OntologyNodeInfo, OntologyQueryEngine,
    OntologyStatus,
};
#[allow(unused_imports)]
pub use safe_discover::{
    clamp_limit, discover, discover_page_to_json, is_mega_graph, mega_graph_refusal,
    mega_graph_threshold, refuse_full_scan_if_mega, skip_incremental_dependents, DiscoverPage,
    DEFAULT_PAGE_LIMIT, MAX_PAGE_LIMIT,
};

use serde::{Deserialize, Serialize};

/// Ontology layer types
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum OntologyLayer {
    Domain,
    Procedural,
}

impl OntologyLayer {
    pub fn as_str(&self) -> &'static str {
        match self {
            OntologyLayer::Domain => "domain",
            OntologyLayer::Procedural => "procedural",
        }
    }
}

/// Stable Global ID for ontology nodes
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OntologyGid {
    pub env: String,
    pub scope: String,
    pub ontology_type: String,
    pub id: String,
    pub version: String,
}

impl OntologyGid {
    /// Parse a GID string like "local:checkout-service:domain_entity:refund:v1"
    pub fn parse(s: &str) -> Option<Self> {
        let parts: Vec<&str> = s.split(':').collect();
        if parts.len() != 5 {
            return None;
        }
        Some(Self {
            env: parts[0].to_string(),
            scope: parts[1].to_string(),
            ontology_type: parts[2].to_string(),
            id: parts[3].to_string(),
            version: parts[4].to_string(),
        })
    }

    /// Format GID string
    pub fn to_gid_string(&self) -> String {
        format!(
            "{}:{}:{}:{}:{}",
            self.env, self.scope, self.ontology_type, self.id, self.version
        )
    }
}

impl std::fmt::Display for OntologyGid {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "{}:{}:{}:{}:{}",
            self.env, self.scope, self.ontology_type, self.id, self.version
        )
    }
}

/// Alias normalization helper
pub fn normalize_alias(alias: &str) -> String {
    alias
        .to_lowercase()
        .trim()
        .chars()
        .filter(|c| c.is_alphanumeric() || *c == ' ' || *c == '-' || *c == '_')
        .collect::<String>()
}

/// Normalize multiple aliases
pub fn normalize_aliases(aliases: &[String]) -> Vec<String> {
    aliases.iter().map(|a| normalize_alias(a)).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_ontology_gid_parse() {
        let gid = OntologyGid::parse("local:checkout-service:domain_entity:refund:v1").unwrap();
        assert_eq!(gid.env, "local");
        assert_eq!(gid.scope, "checkout-service");
        assert_eq!(gid.ontology_type, "domain_entity");
        assert_eq!(gid.id, "refund");
        assert_eq!(gid.version, "v1");
    }

    #[test]
    fn test_ontology_gid_roundtrip() {
        let gid = OntologyGid::parse("local:checkout-service:domain_entity:refund:v1").unwrap();
        assert_eq!(
            gid.to_string(),
            "local:checkout-service:domain_entity:refund:v1"
        );
    }

    #[test]
    fn test_normalize_alias() {
        assert_eq!(normalize_alias("  Refund  "), "refund");
        assert_eq!(normalize_alias("Money Back"), "money back");
        assert_eq!(normalize_alias("charge-back"), "charge-back");
    }

    #[test]
    fn test_calculate_match_score_exact_name() {
        let (score, reason) = calculate_match_score("refund", "Refund", &[], "Money back");
        assert_eq!(score, 1.0);
        assert!(reason.contains("exact name match"));
    }

    #[test]
    fn test_calculate_match_score_name_contains() {
        let (score, reason) = calculate_match_score("refund", "RefundPolicy", &[], "");
        assert_eq!(score, 0.8);
        assert!(reason.contains("name contains"));
    }

    #[test]
    fn test_calculate_match_score_alias_match() {
        let (score, reason) = calculate_match_score(
            "reversal",
            "Refund",
            &["reversal".to_string(), "chargeback".to_string()],
            "",
        );
        assert_eq!(score, 0.9);
        assert!(reason.contains("exact alias match"));
    }

    #[test]
    fn test_calculate_match_score_description() {
        let (score, reason) = calculate_match_score("payment", "Refund", &[], "Money payment back");
        assert_eq!(score, 0.5);
        assert!(reason.contains("description contains"));
    }

    #[test]
    fn test_calculate_match_score_no_match() {
        let (score, reason) = calculate_match_score("xyz", "Refund", &[], "");
        assert_eq!(score, 0.0);
        assert!(reason.is_empty());
    }

    #[test]
    fn test_extract_keywords_drops_stopwords() {
        let kws = extract_keywords("how does the feature flag work");
        assert!(kws.contains(&"feature".to_string()));
        assert!(kws.contains(&"flag".to_string()));
        assert!(kws.contains(&"work".to_string()));
        // stop words must be dropped
        assert!(!kws.contains(&"how".to_string()));
        assert!(!kws.contains(&"the".to_string()));
        assert!(!kws.contains(&"does".to_string()));
    }

    #[test]
    fn test_extract_keywords_dedup_and_case() {
        let kws = extract_keywords("Refund refund REFUND");
        assert_eq!(kws, vec!["refund".to_string()]);
    }

    #[test]
    fn test_extract_keywords_filters_short_and_stopwords() {
        let kws = extract_keywords("a be refund");
        assert!(kws.contains(&"refund".to_string()));
        assert!(!kws.contains(&"a".to_string()));
        assert!(!kws.contains(&"be".to_string()));
    }

    #[test]
    fn test_normalize_path_strips_prefix() {
        assert_eq!(normalize_path("./src/main.rs"), "src/main.rs");
        assert_eq!(normalize_path("/src/main.rs"), "src/main.rs");
        assert_eq!(normalize_path("src/main.rs"), "src/main.rs");
        assert_eq!(normalize_path("  ./a/b/  "), "a/b");
    }
}
