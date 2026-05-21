//! Concept Ontology Module
//!
//! Concept ontology nodes describe domain entities, services, APIs, data stores,
//! environments, known issues, and knowledge artifacts.

use super::{normalize_alias, OntologyGid};
use serde::{Deserialize, Serialize};

/// Concept element types for the domain layer
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ConceptElementType {
    DomainEntity,
    Service,
    ApiEndpoint,
    DataStore,
    Environment,
    KnownIssue,
    Playbook,
    TeamKnowledge,
}

impl ConceptElementType {
    pub fn as_str(&self) -> &'static str {
        match self {
            ConceptElementType::DomainEntity => "domain_entity",
            ConceptElementType::Service => "service",
            ConceptElementType::ApiEndpoint => "api_endpoint",
            ConceptElementType::DataStore => "data_store",
            ConceptElementType::Environment => "environment",
            ConceptElementType::KnownIssue => "known_issue",
            ConceptElementType::Playbook => "playbook",
            ConceptElementType::TeamKnowledge => "team_knowledge",
        }
    }

    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "domain_entity" => Some(Self::DomainEntity),
            "service" => Some(Self::Service),
            "api_endpoint" => Some(Self::ApiEndpoint),
            "data_store" => Some(Self::DataStore),
            "environment" => Some(Self::Environment),
            "known_issue" => Some(Self::KnownIssue),
            "playbook" => Some(Self::Playbook),
            "team_knowledge" => Some(Self::TeamKnowledge),
            _ => None,
        }
    }
}

/// Concept relationship types
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ConceptRelationshipType {
    OwnsConcept,
    ImplementsConcept,
    ExposesEndpoint,
    ReadsFrom,
    WritesTo,
    DocumentsConcept,
    HasKnownIssue,
    ResolvedByPlaybook,
}

impl ConceptRelationshipType {
    pub fn as_str(&self) -> &'static str {
        match self {
            ConceptRelationshipType::OwnsConcept => "owns_concept",
            ConceptRelationshipType::ImplementsConcept => "implements_concept",
            ConceptRelationshipType::ExposesEndpoint => "exposes_endpoint",
            ConceptRelationshipType::ReadsFrom => "reads_from",
            ConceptRelationshipType::WritesTo => "writes_to",
            ConceptRelationshipType::DocumentsConcept => "documents_concept",
            ConceptRelationshipType::HasKnownIssue => "has_known_issue",
            ConceptRelationshipType::ResolvedByPlaybook => "resolved_by_playbook",
        }
    }

    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "owns_concept" => Some(Self::OwnsConcept),
            "implements_concept" => Some(Self::ImplementsConcept),
            "exposes_endpoint" => Some(Self::ExposesEndpoint),
            "reads_from" => Some(Self::ReadsFrom),
            "writes_to" => Some(Self::WritesTo),
            "documents_concept" => Some(Self::DocumentsConcept),
            "has_known_issue" => Some(Self::HasKnownIssue),
            "resolved_by_playbook" => Some(Self::ResolvedByPlaybook),
            _ => None,
        }
    }
}

/// Metadata contract for concept ontology nodes
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConceptMetadata {
    pub gid: String,
    pub ontology: String,
    pub ontology_layer: String,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub valid_from: Option<String>,
    #[serde(default)]
    pub valid_until: Option<String>,
    #[serde(default)]
    pub owned_by: Vec<String>,
    #[serde(default)]
    pub code_refs: Vec<String>,
    #[serde(default)]
    pub docs: Vec<String>,
    #[serde(default)]
    pub stale: bool,
    #[serde(default)]
    pub stale_reason: Option<String>,
    #[serde(default)]
    pub last_seen_at: Option<String>,
}

impl ConceptMetadata {
    pub fn new(
        env: &str,
        scope: &str,
        element_type: &str,
        id: &str,
        name: &str,
        description: &str,
    ) -> Self {
        let gid = OntologyGid {
            env: env.to_string(),
            scope: scope.to_string(),
            ontology_type: element_type.to_string(),
            id: id.to_string(),
            version: "v1".to_string(),
        }
        .to_string();

        Self {
            gid,
            ontology: "concept".to_string(),
            ontology_layer: "domain".to_string(),
            aliases: vec![normalize_alias(name)],
            description: description.to_string(),
            source: None,
            valid_from: None,
            valid_until: None,
            owned_by: Vec::new(),
            code_refs: Vec::new(),
            docs: Vec::new(),
            stale: false,
            stale_reason: None,
            last_seen_at: None,
        }
    }

    pub fn with_aliases(mut self, aliases: Vec<String>) -> Self {
        let normalized: Vec<String> = aliases.iter().map(|a| normalize_alias(a)).collect();
        self.aliases.extend(normalized);
        self
    }

    pub fn with_source(mut self, source: &str) -> Self {
        self.source = Some(source.to_string());
        self
    }

    pub fn with_owned_by(mut self, owners: Vec<String>) -> Self {
        self.owned_by = owners;
        self
    }

    pub fn with_code_refs(mut self, refs: Vec<String>) -> Self {
        self.code_refs = refs;
        self
    }

    pub fn with_docs(mut self, docs: Vec<String>) -> Self {
        self.docs = docs;
        self
    }
}

/// Concept node structure for graph insertion
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConceptNode {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub aliases: Vec<String>,
    pub description: String,
    pub env: String,
    pub metadata: ConceptMetadata,
}

impl ConceptNode {
    pub fn new(
        env: &str,
        scope: &str,
        element_type: ConceptElementType,
        id: &str,
        name: &str,
        description: &str,
    ) -> Self {
        let element_type_str = element_type.as_str();
        let metadata = ConceptMetadata::new(env, scope, element_type_str, id, name, description);

        Self {
            gid: metadata.gid.clone(),
            name: name.to_string(),
            element_type: element_type_str.to_string(),
            aliases: metadata.aliases.clone(),
            description: description.to_string(),
            env: env.to_string(),
            metadata,
        }
    }

    pub fn domain_entity(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::DomainEntity,
            id,
            name,
            description,
        )
    }

    pub fn service(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::Service,
            id,
            name,
            description,
        )
    }

    pub fn api_endpoint(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::ApiEndpoint,
            id,
            name,
            description,
        )
    }

    pub fn data_store(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::DataStore,
            id,
            name,
            description,
        )
    }

    pub fn known_issue(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::KnownIssue,
            id,
            name,
            description,
        )
    }

    pub fn playbook(env: &str, scope: &str, id: &str, name: &str, description: &str) -> Self {
        Self::new(
            env,
            scope,
            ConceptElementType::Playbook,
            id,
            name,
            description,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_concept_element_type_as_str() {
        assert_eq!(ConceptElementType::DomainEntity.as_str(), "domain_entity");
        assert_eq!(ConceptElementType::Service.as_str(), "service");
        assert_eq!(ConceptElementType::ApiEndpoint.as_str(), "api_endpoint");
        assert_eq!(ConceptElementType::DataStore.as_str(), "data_store");
        assert_eq!(ConceptElementType::KnownIssue.as_str(), "known_issue");
        assert_eq!(ConceptElementType::Playbook.as_str(), "playbook");
    }

    #[test]
    fn test_concept_element_type_from_str() {
        assert_eq!(
            ConceptElementType::from_str("domain_entity"),
            Some(ConceptElementType::DomainEntity)
        );
        assert_eq!(
            ConceptElementType::from_str("service"),
            Some(ConceptElementType::Service)
        );
        assert_eq!(ConceptElementType::from_str("unknown"), None);
    }

    #[test]
    fn test_concept_relationship_type_roundtrip() {
        assert_eq!(
            ConceptRelationshipType::OwnsConcept.as_str(),
            "owns_concept"
        );
        assert_eq!(
            ConceptRelationshipType::from_str("owns_concept"),
            Some(ConceptRelationshipType::OwnsConcept)
        );
        assert_eq!(
            ConceptRelationshipType::from_str("implements_concept"),
            Some(ConceptRelationshipType::ImplementsConcept)
        );
    }

    #[test]
    fn test_concept_metadata_new() {
        let meta = ConceptMetadata::new(
            "local",
            "checkout-service",
            "domain_entity",
            "refund",
            "Refund",
            "Money returned to customer",
        );
        assert_eq!(meta.gid, "local:checkout-service:domain_entity:refund:v1");
        assert_eq!(meta.ontology, "concept");
        assert_eq!(meta.ontology_layer, "domain");
        assert!(meta.aliases.contains(&"refund".to_string()));
    }

    #[test]
    fn test_concept_metadata_with_aliases() {
        let meta = ConceptMetadata::new(
            "local",
            "checkout-service",
            "domain_entity",
            "refund",
            "Refund",
            "Money returned to customer",
        )
        .with_aliases(vec!["reversal".to_string(), "chargeback".to_string()]);

        assert!(meta.aliases.contains(&"reversal".to_string()));
        assert!(meta.aliases.contains(&"chargeback".to_string()));
    }

    #[test]
    fn test_concept_node_creation() {
        let node = ConceptNode::domain_entity(
            "local",
            "checkout-service",
            "refund",
            "Refund",
            "Money returned to customer after payment capture",
        );
        assert_eq!(node.gid, "local:checkout-service:domain_entity:refund:v1");
        assert_eq!(node.element_type, "domain_entity");
        assert_eq!(node.name, "Refund");
    }
}
