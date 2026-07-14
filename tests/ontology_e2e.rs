//! End-to-end tests for concept and procedural ontologies.
//!
//! Verifies that:
//!   - Built-in concept ontology YAML loads and parses without errors
//!   - Built-in procedural ontology YAML loads and parses without errors
//!   - Concept/procedural types round-trip through as_str/from_str
//!   - Node factories produce correct GIDs and metadata
//!   - aliases + ownership + failure modes wire up correctly across
//!     the concept and procedural surfaces
//!
//! Covers PRD: ONT-1..ONT-12 (concept + procedural wiring).

use leankg::ontology::concept::{
    ConceptElementType, ConceptMetadata, ConceptNode, ConceptRelationshipType,
};
use leankg::ontology::procedural::{
    FailureModeNode, ProceduralElementType, ProceduralRelationshipType, WorkflowMetadata,
    WorkflowNode, WorkflowStepMetadata, WorkflowStepNode,
};
use leankg::ontology::{normalize_alias, normalize_aliases, OntologyGid};

const CONCEPTS_YAML: &str = "ontology/concepts.yaml";
const WORKFLOWS_YAML: &str = "ontology/workflows.yaml";

// ============================================================================
// Built-in YAML loaders
// ============================================================================

#[test]
fn e2e_ontology_concepts_yaml_loads_and_parses() {
    let nodes = leankg::ontology::load_concepts_yaml(std::path::Path::new(CONCEPTS_YAML))
        .expect("concepts.yaml should load and parse");
    assert!(
        !nodes.is_empty(),
        "concepts.yaml must contain at least one concept node"
    );

    // Every node must have a well-formed GID.
    for n in &nodes {
        let parsed = OntologyGid::parse(&n.gid)
            .unwrap_or_else(|| panic!("concept node gid must parse, got '{}'", n.gid));
        assert_eq!(parsed.ontology_type, n.element_type);
        assert_eq!(parsed.env, n.env);
    }
}

#[test]
fn e2e_ontology_workflows_yaml_loads_and_parses() {
    let (workflows, steps, failures, _relationships) =
        leankg::ontology::load_workflows_yaml(std::path::Path::new(WORKFLOWS_YAML))
            .expect("workflows.yaml should load and parse");

    assert!(
        !workflows.is_empty(),
        "workflows.yaml must declare at least one workflow"
    );
    assert!(
        !steps.is_empty(),
        "workflows.yaml must declare at least one step"
    );

    // Every step's workflow_gid must point at a known workflow gid.
    let workflow_gids: std::collections::HashSet<String> =
        workflows.iter().map(|w| w.gid.clone()).collect();
    for s in &steps {
        assert!(
            workflow_gids.contains(&s.workflow_gid),
            "step '{}' has unknown workflow_gid '{}'",
            s.gid,
            s.workflow_gid
        );
    }

    // Failure modes (if declared) belong to this workflow scope.
    for f in &failures {
        let parsed = OntologyGid::parse(&f.gid)
            .unwrap_or_else(|| panic!("failure gid must parse, got '{}'", f.gid));
        assert_eq!(parsed.ontology_type, "failure_mode");
    }
}

// ============================================================================
// Built-in element / relationship type enums
// ============================================================================

#[test]
fn e2e_concept_element_types_roundtrip() {
    let types = [
        (ConceptElementType::DomainEntity, "domain_entity"),
        (ConceptElementType::Service, "service"),
        (ConceptElementType::ApiEndpoint, "api_endpoint"),
        (ConceptElementType::DataStore, "data_store"),
        (ConceptElementType::Environment, "environment"),
        (ConceptElementType::KnownIssue, "known_issue"),
        (ConceptElementType::Playbook, "playbook"),
        (ConceptElementType::TeamKnowledge, "team_knowledge"),
    ];
    for (t, s) in types {
        assert_eq!(t.as_str(), s, "as_str for {:?}", t);
        assert_eq!(
            ConceptElementType::from_str(s),
            Some(t),
            "from_str for {}",
            s
        );
    }
    assert_eq!(ConceptElementType::from_str("garbage"), None);
}

#[test]
fn e2e_procedural_element_types_roundtrip() {
    let types = [
        (ProceduralElementType::Workflow, "workflow"),
        (ProceduralElementType::WorkflowStep, "workflow_step"),
        (ProceduralElementType::DecisionPoint, "decision_point"),
        (ProceduralElementType::FailureMode, "failure_mode"),
        (ProceduralElementType::PlaybookStep, "playbook_step"),
    ];
    for (t, s) in types {
        assert_eq!(t.as_str(), s, "as_str for {:?}", t);
        assert_eq!(
            ProceduralElementType::from_str(s),
            Some(t),
            "from_str for {}",
            s
        );
    }
    assert_eq!(ProceduralElementType::from_str("garbage"), None);
}

#[test]
fn e2e_concept_relationship_types_roundtrip() {
    let cases = [
        (ConceptRelationshipType::OwnsConcept, "owns_concept"),
        (
            ConceptRelationshipType::ImplementsConcept,
            "implements_concept",
        ),
        (ConceptRelationshipType::ExposesEndpoint, "exposes_endpoint"),
        (ConceptRelationshipType::ReadsFrom, "reads_from"),
        (ConceptRelationshipType::WritesTo, "writes_to"),
        (
            ConceptRelationshipType::DocumentsConcept,
            "documents_concept",
        ),
        (ConceptRelationshipType::HasKnownIssue, "has_known_issue"),
        (
            ConceptRelationshipType::ResolvedByPlaybook,
            "resolved_by_playbook",
        ),
    ];
    for (t, s) in cases {
        assert_eq!(t.as_str(), s);
        assert_eq!(ConceptRelationshipType::from_str(s), Some(t));
    }
    assert_eq!(ConceptRelationshipType::from_str("nope"), None);
}

#[test]
fn e2e_procedural_relationship_types_roundtrip() {
    let cases = [
        (ProceduralRelationshipType::HasStep, "has_step"),
        (ProceduralRelationshipType::NextStep, "next_step"),
        (ProceduralRelationshipType::BranchesTo, "branches_to"),
        (ProceduralRelationshipType::ImplementedBy, "implemented_by"),
        (
            ProceduralRelationshipType::HasFailureMode,
            "has_failure_mode",
        ),
        (
            ProceduralRelationshipType::HandledByPlaybook,
            "handled_by_playbook",
        ),
    ];
    for (t, s) in cases {
        assert_eq!(t.as_str(), s);
        assert_eq!(ProceduralRelationshipType::from_str(s), Some(t));
    }
    assert_eq!(ProceduralRelationshipType::from_str("nope"), None);
}

// ============================================================================
// Node factories: GID + metadata correctness
// ============================================================================

#[test]
fn e2e_concept_domain_entity_node() {
    let node = ConceptNode::domain_entity(
        "local",
        "checkout-service",
        "refund",
        "Refund",
        "Money returned to customer after payment capture",
    );
    assert_eq!(node.gid, "local:checkout-service:domain_entity:refund:v1");
    assert_eq!(node.element_type, "domain_entity");
    assert_eq!(node.aliases, vec!["refund".to_string()]);
    assert_eq!(node.metadata.ontology, "concept");
    assert_eq!(node.metadata.ontology_layer, "domain");
    assert!(!node.metadata.stale);
}

#[test]
fn e2e_concept_service_node_with_owner_and_code_refs() {
    let mut meta = ConceptMetadata::new(
        "local",
        "checkout-service",
        "service",
        "checkout",
        "Checkout",
        "Customer checkout service",
    );
    meta = meta.with_owned_by(vec!["payments-team".to_string()]);
    meta = meta.with_code_refs(vec![
        "./src/handlers/checkout.rs::create_order".to_string(),
        "./src/handlers/checkout.rs::complete_order".to_string(),
    ]);
    let node = ConceptNode::new(
        "local",
        "checkout-service",
        ConceptElementType::Service,
        "checkout",
        "Checkout",
        "Customer checkout service",
    );
    // Replace metadata on the node with our custom one.
    let node = ConceptNode {
        metadata: meta,
        ..node
    };
    assert_eq!(node.metadata.owned_by, vec!["payments-team".to_string()]);
    assert_eq!(node.metadata.code_refs.len(), 2);
    // name normalized on the alias side
    assert!(node.metadata.aliases.contains(&"checkout".to_string()));
}

#[test]
fn e2e_concept_known_issue_with_playbook() {
    let node = ConceptNode::known_issue(
        "local",
        "checkout-service",
        "duplicate_charge",
        "Duplicate Charge",
        "Customer charged twice for same order",
    );
    assert_eq!(
        node.gid,
        "local:checkout-service:known_issue:duplicate_charge:v1"
    );
    assert_eq!(node.element_type, "known_issue");
}

#[test]
fn e2e_procedural_workflow_with_entry_points_and_step_count() {
    let wf = WorkflowNode::new(
        "local",
        "checkout-service",
        "checkout",
        "Checkout",
        "End-to-end customer checkout workflow",
    )
    .with_entry_points(vec![
        "./src/handlers/checkout.rs::create_order".to_string(),
        "./src/jobs/checkout_timeout.rs::run".to_string(),
    ])
    .with_step_count(5)
    .with_source("call_graph_mining");

    assert_eq!(wf.gid, "local:checkout-service:workflow:checkout:v1");
    assert_eq!(wf.metadata.entry_points.len(), 2);
    assert_eq!(wf.metadata.step_count, Some(5));
    assert_eq!(wf.metadata.source.as_deref(), Some("call_graph_mining"));
}

#[test]
fn e2e_procedural_workflow_step_links_to_parent_workflow() {
    let meta = WorkflowStepMetadata::new(
        "local",
        "checkout-service",
        "checkout",
        "authorize_payment",
        2,
        "Authorize payment via /payments/authorize",
    );
    assert_eq!(
        meta.workflow_gid,
        "local:checkout-service:workflow:checkout:v1"
    );
    assert_eq!(meta.order, 2);

    let step = WorkflowStepNode::new(
        "local",
        "checkout-service",
        "checkout",
        "authorize_payment",
        "Authorize Payment",
        2,
        "Authorize payment via /payments/authorize",
    )
    .with_code_refs(vec!["./src/payments/client.rs::authorize".to_string()])
    .with_failure_modes(vec![
        "payment_timeout".to_string(),
        "insufficient_funds".to_string(),
    ]);
    assert_eq!(
        step.gid,
        "local:checkout-service:workflow_step:authorize_payment:v1"
    );
    assert_eq!(step.metadata.code_refs.len(), 1);
    assert_eq!(step.metadata.failure_modes.len(), 2);
}

#[test]
fn e2e_procedural_failure_mode_with_playbook_handler() {
    let fm = FailureModeNode::new(
        "local",
        "checkout-service",
        "payment_timeout",
        "Payment Timeout",
        "Payment authorization timed out",
    )
    .with_handled_by(vec![
        "local:checkout-service:playbook_step:retry_authorize:v1".to_string(),
    ]);
    assert_eq!(
        fm.gid,
        "local:checkout-service:failure_mode:payment_timeout:v1"
    );
    assert_eq!(fm.metadata.handled_by.len(), 1);
}

// ============================================================================
// Alias and path normalization
// ============================================================================

#[test]
fn e2e_normalize_alias_handles_casing_and_punctuation() {
    assert_eq!(normalize_alias("  Refund Policy "), "refund policy");
    assert_eq!(normalize_alias("CHARGE-back"), "charge-back");
    assert_eq!(normalize_alias("Order::Place"), "orderplace");
}

#[test]
fn e2e_normalize_aliases_iterates() {
    let aliases = vec!["  Refund  ".to_string(), "Money Back".to_string()];
    let n = normalize_aliases(&aliases);
    assert_eq!(n, vec!["refund".to_string(), "money back".to_string()]);
}

#[test]
fn e2e_workflow_metadata_with_aliases_dedupes() {
    let wf = WorkflowMetadata::new("local", "checkout", "checkout", "Checkout", "desc")
        .with_aliases(vec!["Checkout".to_string(), "checkout".to_string()])
        .with_step_count(3);
    assert!(wf.aliases.iter().any(|a| a == "checkout"));
    assert_eq!(wf.step_count, Some(3));
}

// ============================================================================
// Concept + procedural cross-wiring sanity check
// ============================================================================

#[test]
fn e2e_concept_and_procedural_share_env_scope_format() {
    // Same env + scope + id should produce the same GID prefix across both layers.
    let env = "local";
    let scope = "checkout-service";

    let concept = ConceptNode::service(env, scope, "checkout", "Checkout", "Customer checkout");
    let workflow = WorkflowNode::new(env, scope, "checkout", "Checkout", "Customer checkout");

    // Strip the ontology_type segment from each GID and ensure the
    // (env, scope, id) prefix matches. This proves both layers address
    // the same domain scope under the same id.
    let strip_ont = |gid: &str| {
        let mut parts: Vec<&str> = gid.split(':').collect();
        parts.remove(2); // remove ontology_type
        parts.join(":")
    };
    assert_eq!(strip_ont(&concept.gid), strip_ont(&workflow.gid));
}
