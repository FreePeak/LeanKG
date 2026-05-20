//! Procedural Ontology Module
//!
//! Procedural ontology nodes describe workflows, execution steps, decision points,
//! failure modes, and playbooks.

use super::{normalize_alias, OntologyGid};
use serde::{Deserialize, Serialize};

/// Procedural element types for the procedural layer
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ProceduralElementType {
    Workflow,
    WorkflowStep,
    DecisionPoint,
    FailureMode,
    PlaybookStep,
}

impl ProceduralElementType {
    pub fn as_str(&self) -> &'static str {
        match self {
            ProceduralElementType::Workflow => "workflow",
            ProceduralElementType::WorkflowStep => "workflow_step",
            ProceduralElementType::DecisionPoint => "decision_point",
            ProceduralElementType::FailureMode => "failure_mode",
            ProceduralElementType::PlaybookStep => "playbook_step",
        }
    }

    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "workflow" => Some(Self::Workflow),
            "workflow_step" => Some(Self::WorkflowStep),
            "decision_point" => Some(Self::DecisionPoint),
            "failure_mode" => Some(Self::FailureMode),
            "playbook_step" => Some(Self::PlaybookStep),
            _ => None,
        }
    }
}

/// Procedural relationship types
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum ProceduralRelationshipType {
    HasStep,
    NextStep,
    BranchesTo,
    ImplementedBy,
    HasFailureMode,
    HandledByPlaybook,
}

impl ProceduralRelationshipType {
    pub fn as_str(&self) -> &'static str {
        match self {
            ProceduralRelationshipType::HasStep => "has_step",
            ProceduralRelationshipType::NextStep => "next_step",
            ProceduralRelationshipType::BranchesTo => "branches_to",
            ProceduralRelationshipType::ImplementedBy => "implemented_by",
            ProceduralRelationshipType::HasFailureMode => "has_failure_mode",
            ProceduralRelationshipType::HandledByPlaybook => "handled_by_playbook",
        }
    }

    #[allow(clippy::should_implement_trait)]
    pub fn from_str(s: &str) -> Option<Self> {
        match s {
            "has_step" => Some(Self::HasStep),
            "next_step" => Some(Self::NextStep),
            "branches_to" => Some(Self::BranchesTo),
            "implemented_by" => Some(Self::ImplementedBy),
            "has_failure_mode" => Some(Self::HasFailureMode),
            "handled_by_playbook" => Some(Self::HandledByPlaybook),
            _ => None,
        }
    }
}

/// Metadata contract for workflow nodes
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WorkflowMetadata {
    pub gid: String,
    pub ontology: String,
    pub ontology_layer: String,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub entry_points: Vec<String>,
    #[serde(default)]
    pub step_count: Option<usize>,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub valid_from: Option<String>,
    #[serde(default)]
    pub valid_until: Option<String>,
    #[serde(default)]
    pub stale: bool,
    #[serde(default)]
    pub stale_reason: Option<String>,
    #[serde(default)]
    pub last_seen_at: Option<String>,
}

impl WorkflowMetadata {
    pub fn new(env: &str, scope: &str, workflow_id: &str, name: &str, description: &str) -> Self {
        let gid = OntologyGid {
            env: env.to_string(),
            scope: scope.to_string(),
            ontology_type: "workflow".to_string(),
            id: workflow_id.to_string(),
            version: "v1".to_string(),
        }
        .to_string();

        Self {
            gid,
            ontology: "procedural".to_string(),
            ontology_layer: "procedural".to_string(),
            aliases: vec![normalize_alias(name)],
            description: description.to_string(),
            entry_points: Vec::new(),
            step_count: None,
            source: None,
            valid_from: None,
            valid_until: None,
            stale: false,
            stale_reason: None,
            last_seen_at: None,
        }
    }

    pub fn with_entry_points(mut self, entry_points: Vec<String>) -> Self {
        self.entry_points = entry_points;
        self
    }

    pub fn with_step_count(mut self, count: usize) -> Self {
        self.step_count = Some(count);
        self
    }

    pub fn with_source(mut self, source: &str) -> Self {
        self.source = Some(source.to_string());
        self
    }
}

/// Metadata contract for workflow step nodes
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct WorkflowStepMetadata {
    pub gid: String,
    pub ontology: String,
    pub ontology_layer: String,
    pub workflow_gid: String,
    pub order: usize,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub code_refs: Vec<String>,
    #[serde(default)]
    pub failure_modes: Vec<String>,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub stale: bool,
    #[serde(default)]
    pub stale_reason: Option<String>,
    #[serde(default)]
    pub last_seen_at: Option<String>,
}

impl WorkflowStepMetadata {
    pub fn new(
        env: &str,
        scope: &str,
        workflow_id: &str,
        step_id: &str,
        order: usize,
        description: &str,
    ) -> Self {
        let gid = OntologyGid {
            env: env.to_string(),
            scope: scope.to_string(),
            ontology_type: "workflow_step".to_string(),
            id: step_id.to_string(),
            version: "v1".to_string(),
        }
        .to_string();

        Self {
            gid,
            ontology: "procedural".to_string(),
            ontology_layer: "procedural".to_string(),
            workflow_gid: format!("{}:{}:{}:{}:{}", env, scope, "workflow", workflow_id, "v1"),
            order,
            aliases: Vec::new(),
            description: description.to_string(),
            code_refs: Vec::new(),
            failure_modes: Vec::new(),
            source: None,
            stale: false,
            stale_reason: None,
            last_seen_at: None,
        }
    }

    pub fn with_code_refs(mut self, refs: Vec<String>) -> Self {
        self.code_refs = refs;
        self
    }

    pub fn with_failure_modes(mut self, modes: Vec<String>) -> Self {
        self.failure_modes = modes;
        self
    }
}

/// Workflow node structure for graph insertion
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowNode {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub aliases: Vec<String>,
    pub description: String,
    pub env: String,
    pub metadata: WorkflowMetadata,
}

impl WorkflowNode {
    pub fn new(env: &str, scope: &str, workflow_id: &str, name: &str, description: &str) -> Self {
        let metadata = WorkflowMetadata::new(env, scope, workflow_id, name, description);

        Self {
            gid: metadata.gid.clone(),
            name: name.to_string(),
            element_type: "workflow".to_string(),
            aliases: metadata.aliases.clone(),
            description: description.to_string(),
            env: env.to_string(),
            metadata,
        }
    }

    pub fn with_entry_points(mut self, entry_points: Vec<String>) -> Self {
        self.metadata = self.metadata.with_entry_points(entry_points);
        self
    }

    pub fn with_step_count(mut self, count: usize) -> Self {
        self.metadata = self.metadata.with_step_count(count);
        self
    }

    pub fn with_source(mut self, source: &str) -> Self {
        self.metadata = self.metadata.with_source(source);
        self
    }
}

/// Workflow step node structure
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowStepNode {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub workflow_gid: String,
    pub order: usize,
    pub description: String,
    pub env: String,
    pub metadata: WorkflowStepMetadata,
}

impl WorkflowStepNode {
    pub fn new(
        env: &str,
        scope: &str,
        workflow_id: &str,
        step_id: &str,
        name: &str,
        order: usize,
        description: &str,
    ) -> Self {
        let metadata =
            WorkflowStepMetadata::new(env, scope, workflow_id, step_id, order, description);

        Self {
            gid: metadata.gid.clone(),
            name: name.to_string(),
            element_type: "workflow_step".to_string(),
            workflow_gid: metadata.workflow_gid.clone(),
            order,
            description: description.to_string(),
            env: env.to_string(),
            metadata,
        }
    }

    pub fn with_code_refs(mut self, refs: Vec<String>) -> Self {
        self.metadata = self.metadata.with_code_refs(refs);
        self
    }

    pub fn with_failure_modes(mut self, modes: Vec<String>) -> Self {
        self.metadata = self.metadata.with_failure_modes(modes);
        self
    }
}

/// Failure mode node
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct FailureModeNode {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub description: String,
    pub env: String,
    pub metadata: FailureModeMetadata,
}

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct FailureModeMetadata {
    pub gid: String,
    pub ontology: String,
    pub ontology_layer: String,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub handled_by: Vec<String>,
    #[serde(default)]
    pub source: Option<String>,
    #[serde(default)]
    pub stale: bool,
    #[serde(default)]
    pub stale_reason: Option<String>,
    #[serde(default)]
    pub last_seen_at: Option<String>,
}

impl FailureModeNode {
    pub fn new(env: &str, scope: &str, failure_id: &str, name: &str, description: &str) -> Self {
        let gid = OntologyGid {
            env: env.to_string(),
            scope: scope.to_string(),
            ontology_type: "failure_mode".to_string(),
            id: failure_id.to_string(),
            version: "v1".to_string(),
        }
        .to_string();

        let metadata = FailureModeMetadata {
            gid: gid.clone(),
            ontology: "procedural".to_string(),
            ontology_layer: "procedural".to_string(),
            aliases: vec![normalize_alias(name)],
            description: description.to_string(),
            handled_by: Vec::new(),
            source: None,
            stale: false,
            stale_reason: None,
            last_seen_at: None,
        };

        Self {
            gid,
            name: name.to_string(),
            element_type: "failure_mode".to_string(),
            description: description.to_string(),
            env: env.to_string(),
            metadata,
        }
    }

    pub fn with_handled_by(mut self, playbooks: Vec<String>) -> Self {
        self.metadata.handled_by = playbooks;
        self
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_procedural_element_type_as_str() {
        assert_eq!(ProceduralElementType::Workflow.as_str(), "workflow");
        assert_eq!(
            ProceduralElementType::WorkflowStep.as_str(),
            "workflow_step"
        );
        assert_eq!(ProceduralElementType::FailureMode.as_str(), "failure_mode");
    }

    #[test]
    fn test_procedural_element_type_from_str() {
        assert_eq!(
            ProceduralElementType::from_str("workflow"),
            Some(ProceduralElementType::Workflow)
        );
        assert_eq!(
            ProceduralElementType::from_str("workflow_step"),
            Some(ProceduralElementType::WorkflowStep)
        );
        assert_eq!(ProceduralElementType::from_str("unknown"), None);
    }

    #[test]
    fn test_procedural_relationship_type_roundtrip() {
        assert_eq!(ProceduralRelationshipType::HasStep.as_str(), "has_step");
        assert_eq!(
            ProceduralRelationshipType::from_str("has_step"),
            Some(ProceduralRelationshipType::HasStep)
        );
        assert_eq!(
            ProceduralRelationshipType::from_str("next_step"),
            Some(ProceduralRelationshipType::NextStep)
        );
    }

    #[test]
    fn test_workflow_metadata_new() {
        let meta = WorkflowMetadata::new(
            "local",
            "checkout-service",
            "checkout",
            "Checkout",
            "End-to-end customer checkout workflow",
        );
        assert_eq!(meta.gid, "local:checkout-service:workflow:checkout:v1");
        assert_eq!(meta.ontology, "procedural");
        assert_eq!(meta.ontology_layer, "procedural");
    }

    #[test]
    fn test_workflow_step_metadata_workflow_gid() {
        let meta = WorkflowStepMetadata::new(
            "local",
            "checkout-service",
            "checkout",
            "authorize_payment",
            2,
            "Authorize payment step",
        );
        assert_eq!(
            meta.workflow_gid,
            "local:checkout-service:workflow:checkout:v1"
        );
        assert_eq!(meta.order, 2);
    }

    #[test]
    fn test_workflow_node_creation() {
        let workflow = WorkflowNode::new(
            "local",
            "checkout-service",
            "checkout",
            "Checkout",
            "End-to-end customer checkout workflow",
        )
        .with_entry_points(vec!["src/checkout/handler.rs::create_order".to_string()])
        .with_step_count(5)
        .with_source("detected_from_call_graph");

        assert_eq!(workflow.gid, "local:checkout-service:workflow:checkout:v1");
        assert_eq!(workflow.name, "Checkout");
        assert_eq!(workflow.metadata.entry_points.len(), 1);
        assert_eq!(workflow.metadata.step_count, Some(5));
    }

    #[test]
    fn test_workflow_step_node_creation() {
        let step = WorkflowStepNode::new(
            "local",
            "checkout-service",
            "checkout",
            "authorize_payment",
            "Authorize Payment",
            2,
            "Authorize payment for the order",
        )
        .with_code_refs(vec!["src/payment/client.rs::authorize".to_string()])
        .with_failure_modes(vec![
            "payment_timeout".to_string(),
            "insufficient_funds".to_string(),
        ]);

        assert_eq!(
            step.gid,
            "local:checkout-service:workflow_step:authorize_payment:v1"
        );
        assert_eq!(step.name, "Authorize Payment");
        assert_eq!(step.order, 2);
        assert_eq!(step.metadata.code_refs.len(), 1);
        assert_eq!(step.metadata.failure_modes.len(), 2);
    }

    #[test]
    fn test_failure_mode_node_creation() {
        let failure = FailureModeNode::new(
            "local",
            "checkout-service",
            "payment_timeout",
            "Payment Timeout",
            "Payment authorization timed out",
        )
        .with_handled_by(vec![
            "local:checkout-service:playbook:payment_reconciliation:v1".to_string(),
        ]);

        assert_eq!(
            failure.gid,
            "local:checkout-service:failure_mode:payment_timeout:v1"
        );
        assert_eq!(failure.name, "Payment Timeout");
        assert_eq!(failure.metadata.handled_by.len(), 1);
    }
}
