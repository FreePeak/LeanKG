//! Ontology YAML Loader
//!
//! Loads concept and workflow definitions from YAML files.

use crate::db::models::{CodeElement, Relationship};
use crate::ontology::concept::{ConceptMetadata, ConceptNode};
use crate::ontology::procedural::{
    FailureModeNode, WorkflowMetadata, WorkflowNode, WorkflowStepNode,
};
use serde::{Deserialize, Serialize};
use std::path::Path;
use tracing::warn;

/// Root structure for concepts.yaml
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConceptsYaml {
    pub concepts: Vec<ConceptDef>,
}

/// A concept definition from YAML
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConceptDef {
    pub id: String,
    #[serde(default)]
    pub type_: String,
    pub name: String,
    #[serde(default = "default_env")]
    pub env: String,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub owned_by: Vec<String>,
    #[serde(default)]
    pub code_refs: Vec<String>,
    #[serde(default)]
    pub docs: Vec<String>,
}

/// Root structure for workflows.yaml
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowsYaml {
    pub workflows: Vec<WorkflowDef>,
}

/// A workflow definition from YAML
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowDef {
    pub id: String,
    pub name: String,
    #[serde(default = "default_env")]
    pub env: String,
    #[serde(default)]
    pub aliases: Vec<String>,
    pub description: String,
    #[serde(default)]
    pub entry_points: Vec<String>,
    #[serde(default)]
    pub steps: Vec<WorkflowStepDef>,
}

/// A workflow step definition
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkflowStepDef {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub code_refs: Vec<String>,
    #[serde(default)]
    pub failure_modes: Vec<String>,
    #[serde(default)]
    pub aliases: Vec<String>,
    #[serde(default)]
    pub feature_ids: Vec<String>,
    #[serde(default)]
    pub user_story_ids: Vec<String>,
}

/// Root structure for aliases.yaml
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AliasesYaml {
    pub aliases: Vec<AliasDef>,
}

/// An alias definition
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AliasDef {
    pub gid: String,
    pub alias: String,
}

fn default_env() -> String {
    "local".to_string()
}

/// Result of loading ontology from YAML
#[derive(Debug, Clone, Default)]
pub struct OntologyLoadResult {
    pub concept_nodes: Vec<ConceptNode>,
    pub workflow_nodes: Vec<WorkflowNode>,
    pub workflow_step_nodes: Vec<WorkflowStepNode>,
    pub failure_mode_nodes: Vec<FailureModeNode>,
    pub relationships: Vec<Relationship>,
    pub code_refs_resolved: usize,
    pub code_refs_total: usize,
    pub stale_nodes: Vec<String>,
}

impl OntologyLoadResult {
    pub fn summary(&self) -> String {
        format!(
            "Ontology load complete\nConcept nodes: {}\nWorkflow nodes: {}\nWorkflow steps: {}\nFailure modes: {}\nAliases resolved: {}/{}\nStale nodes: {}",
            self.concept_nodes.len(),
            self.workflow_nodes.len(),
            self.workflow_step_nodes.len(),
            self.failure_mode_nodes.len(),
            self.code_refs_resolved,
            self.code_refs_total,
            self.stale_nodes.len()
        )
    }
}

/// Load concept ontology from YAML file
pub fn load_concepts_yaml(
    path: &Path,
) -> Result<Vec<ConceptNode>, Box<dyn std::error::Error + Send + Sync>> {
    let content = std::fs::read_to_string(path)?;
    let yaml: ConceptsYaml = serde_yaml::from_str(&content)?;

    let mut nodes = Vec::new();
    for concept_def in yaml.concepts {
        let element_type = match concept_def.type_.as_str() {
            "domain_entity" => "domain_entity",
            "service" => "service",
            "api_endpoint" => "api_endpoint",
            "data_store" => "data_store",
            "environment" => "environment",
            "known_issue" => "known_issue",
            "playbook" => "playbook",
            "team_knowledge" => "team_knowledge",
            other => {
                warn!(
                    "Unknown concept type '{}', defaulting to domain_entity",
                    other
                );
                "domain_entity"
            }
        };

        let scope = concept_def
            .owned_by
            .first()
            .cloned()
            .unwrap_or_else(|| "default".to_string());

        let mut metadata = ConceptMetadata::new(
            &concept_def.env,
            &scope,
            element_type,
            &concept_def.id,
            &concept_def.name,
            &concept_def.description,
        );
        metadata = metadata.with_aliases(concept_def.aliases);
        metadata = metadata.with_source(path.to_str().unwrap_or("unknown"));
        metadata = metadata.with_owned_by(concept_def.owned_by);
        metadata = metadata.with_code_refs(concept_def.code_refs.clone());
        metadata = metadata.with_docs(concept_def.docs);

        let node = ConceptNode {
            gid: metadata.gid.clone(),
            name: concept_def.name,
            element_type: element_type.to_string(),
            aliases: metadata.aliases.clone(),
            description: concept_def.description,
            env: concept_def.env,
            metadata,
        };

        nodes.push(node);
    }

    Ok(nodes)
}

/// Load workflow ontology from YAML file
#[allow(clippy::type_complexity)]
pub fn load_workflows_yaml(
    path: &Path,
) -> Result<
    (
        Vec<WorkflowNode>,
        Vec<WorkflowStepNode>,
        Vec<FailureModeNode>,
        Vec<Relationship>,
    ),
    Box<dyn std::error::Error + Send + Sync>,
> {
    let content = std::fs::read_to_string(path)?;
    let yaml: WorkflowsYaml = serde_yaml::from_str(&content)?;

    let mut workflow_nodes = Vec::new();
    let mut step_nodes = Vec::new();
    let mut failure_nodes = Vec::new();
    let mut relationships = Vec::new();

    for workflow_def in yaml.workflows {
        let scope = "default".to_string();

        // Create workflow node
        let mut metadata = WorkflowMetadata::new(
            &workflow_def.env,
            &scope,
            &workflow_def.id,
            &workflow_def.name,
            &workflow_def.description,
        );
        metadata = metadata.with_aliases(workflow_def.aliases.clone());
        metadata = metadata.with_entry_points(workflow_def.entry_points.clone());
        metadata = metadata.with_step_count(workflow_def.steps.len());
        metadata = metadata.with_source(path.to_str().unwrap_or("unknown"));

        let workflow_node = WorkflowNode {
            gid: metadata.gid.clone(),
            name: workflow_def.name.clone(),
            element_type: "workflow".to_string(),
            aliases: metadata.aliases.clone(),
            description: workflow_def.description,
            env: workflow_def.env.clone(),
            metadata,
        };
        workflow_nodes.push(workflow_node.clone());

        // Create step nodes and relationships
        for (order, step_def) in workflow_def.steps.iter().enumerate() {
            let step_node = WorkflowStepNode::new(
                &workflow_def.env,
                &scope,
                &workflow_def.id,
                &step_def.id,
                &step_def.name,
                order + 1,
                "", // steps don't have separate descriptions in current format
            )
            .with_code_refs(step_def.code_refs.clone())
            .with_failure_modes(step_def.failure_modes.clone())
            .with_feature_ids(step_def.feature_ids.clone())
            .with_user_story_ids(step_def.user_story_ids.clone());

            step_nodes.push(step_node.clone());

            // has_step relationship: workflow -> workflow_step
            relationships.push(Relationship {
                id: None,
                source_qualified: workflow_node.gid.clone(),
                target_qualified: step_node.gid.clone(),
                rel_type: "has_step".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
                env: workflow_def.env.clone(),
            });

            // next_step relationship: step -> step (if not last)
            if order > 0 {
                let prev_step = &step_nodes[step_nodes.len() - 2];
                relationships.push(Relationship {
                    id: None,
                    source_qualified: prev_step.gid.clone(),
                    target_qualified: step_node.gid.clone(),
                    rel_type: "next_step".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                    env: workflow_def.env.clone(),
                });
            }

            // Create failure mode nodes
            for failure_id in &step_def.failure_modes {
                let failure_node = FailureModeNode::new(
                    &workflow_def.env,
                    &scope,
                    failure_id,
                    failure_id,
                    &format!("Failure mode: {}", failure_id),
                );
                failure_nodes.push(failure_node.clone());

                // has_failure_mode: step -> failure_mode
                relationships.push(Relationship {
                    id: None,
                    source_qualified: step_node.gid.clone(),
                    target_qualified: failure_node.gid.clone(),
                    rel_type: "has_failure_mode".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({}),
                    env: workflow_def.env.clone(),
                });
            }
        }
    }

    Ok((workflow_nodes, step_nodes, failure_nodes, relationships))
}

/// Load aliases from YAML file
pub fn load_aliases_yaml(
    path: &Path,
) -> Result<Vec<(String, String)>, Box<dyn std::error::Error + Send + Sync>> {
    let content = std::fs::read_to_string(path)?;
    let yaml: AliasesYaml = serde_yaml::from_str(&content)?;

    Ok(yaml.aliases.into_iter().map(|a| (a.gid, a.alias)).collect())
}

/// Convert ontology nodes to CodeElements for graph storage
pub fn concept_nodes_to_elements(nodes: &[ConceptNode]) -> Vec<CodeElement> {
    nodes
        .iter()
        .map(|node| CodeElement {
            qualified_name: node.gid.clone(),
            element_type: node.element_type.clone(),
            name: node.name.clone(),
            file_path: format!("ontology://{}", node.gid),
            line_start: 0,
            line_end: 0,
            language: "ontology".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::to_value(&node.metadata).unwrap_or_default(),
            env: node.env.clone(),
        })
        .collect()
}

/// Convert workflow nodes to CodeElements
pub fn workflow_nodes_to_elements(nodes: &[WorkflowNode]) -> Vec<CodeElement> {
    nodes
        .iter()
        .map(|node| CodeElement {
            qualified_name: node.gid.clone(),
            element_type: node.element_type.clone(),
            name: node.name.clone(),
            file_path: format!("ontology://{}", node.gid),
            line_start: 0,
            line_end: 0,
            language: "ontology".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::to_value(&node.metadata).unwrap_or_default(),
            env: node.env.clone(),
        })
        .collect()
}

/// Convert workflow step nodes to CodeElements
pub fn workflow_step_nodes_to_elements(nodes: &[WorkflowStepNode]) -> Vec<CodeElement> {
    nodes
        .iter()
        .map(|node| CodeElement {
            qualified_name: node.gid.clone(),
            element_type: node.element_type.clone(),
            name: node.name.clone(),
            file_path: format!("ontology://{}", node.gid),
            line_start: 0,
            line_end: 0,
            language: "ontology".to_string(),
            parent_qualified: Some(node.workflow_gid.clone()),
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::to_value(&node.metadata).unwrap_or_default(),
            env: node.env.clone(),
        })
        .collect()
}

/// Convert failure mode nodes to CodeElements
pub fn failure_mode_nodes_to_elements(nodes: &[FailureModeNode]) -> Vec<CodeElement> {
    nodes
        .iter()
        .map(|node| CodeElement {
            qualified_name: node.gid.clone(),
            element_type: node.element_type.clone(),
            name: node.name.clone(),
            file_path: format!("ontology://{}", node.gid),
            line_start: 0,
            line_end: 0,
            language: "ontology".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::to_value(&node.metadata).unwrap_or_default(),
            env: node.env.clone(),
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use tempfile::NamedTempFile;

    #[test]
    fn test_load_concepts_yaml() {
        let yaml_content = r#"
concepts:
  - id: refund
    type: domain_entity
    name: Refund
    env: local
    aliases:
      - reversal
      - chargeback
    description: Money returned to customer
    owned_by:
      - checkout-service
    code_refs:
      - src/refund/handler.rs
    docs:
      - docs/refund.md
"#;

        let mut temp_file = NamedTempFile::with_suffix(".yaml").unwrap();
        temp_file.write_all(yaml_content.as_bytes()).unwrap();

        let nodes = load_concepts_yaml(temp_file.path()).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(
            nodes[0].gid,
            "local:checkout-service:domain_entity:refund:v1"
        );
        assert_eq!(nodes[0].name, "Refund");
    }

    #[test]
    fn test_load_workflows_yaml() {
        let yaml_content = r#"
workflows:
  - id: checkout
    name: Checkout
    env: local
    aliases:
      - place order
    description: End-to-end checkout
    entry_points:
      - src/checkout/handler.rs::create_order
    steps:
      - id: create_order
        name: Create Order
        code_refs:
          - src/order/service.rs::create
      - id: authorize_payment
        name: Authorize Payment
        code_refs:
          - src/payment/client.rs::authorize
        failure_modes:
          - payment_timeout
"#;

        let mut temp_file = NamedTempFile::with_suffix(".yaml").unwrap();
        temp_file.write_all(yaml_content.as_bytes()).unwrap();

        let (workflows, steps, failures, relationships) =
            load_workflows_yaml(temp_file.path()).unwrap();
        assert_eq!(workflows.len(), 1);
        assert_eq!(workflows[0].name, "Checkout");
        assert!(workflows[0].aliases.contains(&"place order".to_string()));
        assert_eq!(steps.len(), 2);
        assert_eq!(failures.len(), 1);
        assert!(relationships.len() >= 3); // has_step + next_step + has_failure_mode
    }

    #[test]
    fn test_concept_nodes_to_elements() {
        let node = ConceptNode::domain_entity(
            "local",
            "checkout-service",
            "refund",
            "Refund",
            "Money returned to customer",
        );

        let elements = concept_nodes_to_elements(&[node]);
        assert_eq!(elements.len(), 1);
        assert_eq!(
            elements[0].qualified_name,
            "local:checkout-service:domain_entity:refund:v1"
        );
        assert_eq!(elements[0].element_type, "domain_entity");
    }
}
