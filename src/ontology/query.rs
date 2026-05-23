//! Ontology Query Engine
//!
//! Provides methods for querying ontology nodes and expanding context.

use crate::db::models::{CodeElement, Relationship};
use crate::db::schema::CozoDb;
use crate::ontology::procedural::{
    FailureModeMetadata, FailureModeNode, WorkflowMetadata, WorkflowNode, WorkflowStepMetadata,
    WorkflowStepNode,
};
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OntologyContextResult {
    pub matched_ontology_nodes: Vec<OntologyNodeInfo>,
    pub expanded_code_context: Vec<CodeElement>,
    pub expanded_relationships: Vec<Relationship>,
    pub workflows: Vec<WorkflowNode>,
    pub workflow_steps: Vec<WorkflowStepNode>,
    pub failure_modes: Vec<FailureModeNode>,
    pub confidence: f64,
    pub match_reasons: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OntologyNodeInfo {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub description: String,
    pub aliases: Vec<String>,
    pub ontology_layer: String,
    pub match_score: f64,
    pub match_reason: String,
}

impl OntologyContextResult {
    pub fn is_empty(&self) -> bool {
        self.matched_ontology_nodes.is_empty()
    }

    pub fn len(&self) -> usize {
        self.matched_ontology_nodes.len()
    }
}

/// Query engine for ontology nodes
pub struct OntologyQueryEngine {
    db: CozoDb,
}

impl OntologyQueryEngine {
    pub fn new(db: CozoDb) -> Self {
        Self { db }
    }

    /// Search ontology nodes by query string (matches name, aliases, description)
    pub fn search_ontology_nodes(
        &self,
        query: &str,
        _env: &str,
        _depth: u32,
    ) -> Result<Vec<OntologyNodeInfo>, Box<dyn std::error::Error>> {
        let normalized_query = query.to_lowercase();

        // Query all ontology nodes (element_type in concept or procedural types)
        let types_list = [
            "domain_entity",
            "service",
            "api_endpoint",
            "data_store",
            "environment",
            "known_issue",
            "playbook",
            "team_knowledge",
            "workflow",
            "workflow_step",
            "decision_point",
            "failure_mode",
            "playbook_step",
        ];
        let types_str = types_list
            .iter()
            .map(|s| format!("\"{}\"", s))
            .collect::<Vec<_>>()
            .join(",");
        let query_str = format!(
            r#"?[qualified_name, element_type, name, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], element_type in [{}], regex_matches(file_path, "ontology://")"#,
            types_str
        );

        let result = self
            .db
            .run_script(&query_str, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut matches: Vec<OntologyNodeInfo> = Vec::new();

        for row in rows {
            let qualified_name = row[0].as_str().unwrap_or("");
            let element_type = row[1].as_str().unwrap_or("");
            let name = row[2].as_str().unwrap_or("");
            let metadata_str = row[3].as_str().unwrap_or("{}");

            // Parse metadata to get aliases, description, ontology_layer
            let metadata: serde_json::Value =
                serde_json::from_str(metadata_str).unwrap_or_default();

            let aliases: Vec<String> = metadata
                .get("aliases")
                .and_then(|v| v.as_array())
                .map(|arr| {
                    arr.iter()
                        .filter_map(|s| s.as_str().map(String::from))
                        .collect()
                })
                .unwrap_or_default();

            let description = metadata
                .get("description")
                .and_then(|v| v.as_str())
                .unwrap_or("")
                .to_string();

            let ontology_layer = metadata
                .get("ontology_layer")
                .and_then(|v| v.as_str())
                .unwrap_or("domain")
                .to_string();

            // Calculate match score
            let (score, reason) =
                calculate_match_score(&normalized_query, name, &aliases, &description);

            if score > 0.0 {
                matches.push(OntologyNodeInfo {
                    gid: qualified_name.to_string(),
                    name: name.to_string(),
                    element_type: element_type.to_string(),
                    description,
                    aliases,
                    ontology_layer,
                    match_score: score,
                    match_reason: reason,
                });
            }
        }

        // Sort by score descending
        matches.sort_by(|a, b| {
            b.match_score
                .partial_cmp(&a.match_score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });

        Ok(matches)
    }

    /// Expand ontology node to related code context
    pub fn expand_ontology_context(
        &self,
        node_gid: &str,
        depth: u32,
    ) -> Result<(Vec<CodeElement>, Vec<Relationship>), Box<dyn std::error::Error>> {
        let mut expanded_elements = Vec::new();
        let mut expanded_relationships = Vec::new();
        let mut visited = std::collections::HashSet::new();
        visited.insert(node_gid.to_string());

        // Get direct relationships from this node
        let query_str = r#"?[target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $gid"#;

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "gid".to_string(),
            serde_json::Value::String(node_gid.to_string()),
        );

        let result = self.db.run_script(query_str, params)?;
        let rows = result.rows;

        for row in rows {
            let target = row[0].as_str().unwrap_or("");
            let rel_type = row[1].as_str().unwrap_or("");
            let confidence = row[2].as_f64().unwrap_or(1.0);
            let metadata_str = row[3].as_str().unwrap_or("{}");

            if !target.is_empty() && !visited.contains(target) {
                visited.insert(target.to_string());

                // Get the target element
                if let Ok(Some(element)) = self.find_element_by_qualified(target) {
                    expanded_elements.push(element);
                }

                expanded_relationships.push(Relationship {
                    id: None,
                    source_qualified: node_gid.to_string(),
                    target_qualified: target.to_string(),
                    rel_type: rel_type.to_string(),
                    confidence,
                    metadata: serde_json::from_str(metadata_str).unwrap_or_default(),
                    env: "local".to_string(),
                });

                // Recurse if depth allows
                if depth > 1 {
                    let (mut elements, mut rels) =
                        self.expand_ontology_context(target, depth - 1)?;
                    expanded_elements.append(&mut elements);
                    expanded_relationships.append(&mut rels);
                }
            }
        }

        Ok((expanded_elements, expanded_relationships))
    }

    /// Get full context for a semantic query
    pub fn get_ontology_context(
        &self,
        query: &str,
        env: &str,
        depth: u32,
    ) -> Result<OntologyContextResult, Box<dyn std::error::Error>> {
        // Search for matching ontology nodes
        let matched_nodes = self.search_ontology_nodes(query, env, depth)?;

        if matched_nodes.is_empty() {
            return Ok(OntologyContextResult {
                matched_ontology_nodes: vec![],
                expanded_code_context: vec![],
                expanded_relationships: vec![],
                workflows: vec![],
                workflow_steps: vec![],
                failure_modes: vec![],
                confidence: 0.0,
                match_reasons: vec![],
            });
        }

        let mut all_elements = Vec::new();
        let mut all_relationships = Vec::new();
        let mut workflows = Vec::new();
        let mut workflow_steps = Vec::new();
        let mut failure_modes = Vec::new();
        let mut match_reasons = Vec::new();

        for node in &matched_nodes {
            match_reasons.push(node.match_reason.clone());

            // Expand context for each matched node
            let (elements, relationships) = self.expand_ontology_context(&node.gid, depth)?;
            all_elements.extend(elements);
            all_relationships.extend(relationships);

            // Check if this is a workflow or workflow_step
            if node.element_type == "workflow" {
                if let Some(w) = self.get_workflow_by_gid(&node.gid)? {
                    workflows.push(w);
                }
            } else if node.element_type == "workflow_step" {
                if let Some(ws) = self.get_workflow_step_by_gid(&node.gid)? {
                    workflow_steps.push(ws);
                }
            } else if node.element_type == "failure_mode" {
                if let Some(fm) = self.get_failure_mode_by_gid(&node.gid)? {
                    failure_modes.push(fm);
                }
            }
        }

        // Calculate overall confidence
        let avg_confidence: f64 =
            matched_nodes.iter().map(|n| n.match_score).sum::<f64>() / matched_nodes.len() as f64;

        Ok(OntologyContextResult {
            matched_ontology_nodes: matched_nodes,
            expanded_code_context: all_elements,
            expanded_relationships: all_relationships,
            workflows,
            workflow_steps,
            failure_modes,
            confidence: avg_confidence,
            match_reasons,
        })
    }

    /// Get workflow node by GID
    fn get_workflow_by_gid(
        &self,
        gid: &str,
    ) -> Result<Option<WorkflowNode>, Box<dyn std::error::Error>> {
        if let Some(element) = self.find_element_by_qualified(gid)? {
            let metadata: WorkflowMetadata = serde_json::from_value(element.metadata)
                .ok()
                .unwrap_or_default();
            Ok(Some(WorkflowNode {
                gid: element.qualified_name,
                name: element.name,
                element_type: element.element_type,
                aliases: metadata.aliases.clone(),
                description: metadata.description.clone(),
                env: element.env,
                metadata,
            }))
        } else {
            Ok(None)
        }
    }

    /// Get workflow step node by GID
    fn get_workflow_step_by_gid(
        &self,
        gid: &str,
    ) -> Result<Option<WorkflowStepNode>, Box<dyn std::error::Error>> {
        if let Some(element) = self.find_element_by_qualified(gid)? {
            let metadata: WorkflowStepMetadata = serde_json::from_value(element.metadata)
                .ok()
                .unwrap_or_default();
            Ok(Some(WorkflowStepNode {
                gid: element.qualified_name,
                name: element.name,
                element_type: element.element_type,
                workflow_gid: metadata.workflow_gid.clone(),
                order: metadata.order,
                description: metadata.description.clone(),
                env: element.env,
                metadata,
            }))
        } else {
            Ok(None)
        }
    }

    /// Get failure mode node by GID
    fn get_failure_mode_by_gid(
        &self,
        gid: &str,
    ) -> Result<Option<FailureModeNode>, Box<dyn std::error::Error>> {
        if let Some(element) = self.find_element_by_qualified(gid)? {
            let metadata: FailureModeMetadata = serde_json::from_value(element.metadata)
                .ok()
                .unwrap_or_default();
            Ok(Some(FailureModeNode {
                gid: element.qualified_name,
                name: element.name,
                element_type: element.element_type,
                description: metadata.description.clone(),
                env: element.env,
                metadata,
            }))
        } else {
            Ok(None)
        }
    }

    /// Find element by qualified name
    fn find_element_by_qualified(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], qualified_name = $qn"#;

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(qualified_name.to_string()),
        );

        let result = self.db.run_script(query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].as_str().map(String::from);
        let cluster_id = row[8].as_str().map(String::from);
        let cluster_label = row[9].as_str().map(String::from);
        let metadata_str = row[10].as_str().unwrap_or("{}");
        let env = row[11].as_str().unwrap_or("local").to_string();

        Ok(Some(CodeElement {
            qualified_name: row[0].as_str().unwrap_or("").to_string(),
            element_type: row[1].as_str().unwrap_or("").to_string(),
            name: row[2].as_str().unwrap_or("").to_string(),
            file_path: row[3].as_str().unwrap_or("").to_string(),
            line_start: row[4].as_i64().unwrap_or(0) as u32,
            line_end: row[5].as_i64().unwrap_or(0) as u32,
            language: row[6].as_str().unwrap_or("").to_string(),
            parent_qualified,
            cluster_id,
            cluster_label,
            metadata: serde_json::from_str(metadata_str).unwrap_or_default(),
            env,
        }))
    }

    /// Trace a workflow (get ordered steps)
    pub fn trace_workflow(
        &self,
        workflow_query: &str,
        env: &str,
    ) -> Result<Vec<WorkflowStepNode>, Box<dyn std::error::Error>> {
        // First find the workflow
        let workflows = self.search_workflows(workflow_query, env)?;

        if workflows.is_empty() {
            return Ok(vec![]);
        }

        let workflow = &workflows[0];
        let workflow_gid = &workflow.gid;

        // Get all steps for this workflow
        let query_str = r#"?[qualified_name, element_type, name, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], element_type = "workflow_step", parent_qualified = $wgid"#;

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "wgid".to_string(),
            serde_json::Value::String(workflow_gid.to_string()),
        );

        let result = self.db.run_script(query_str, params)?;
        let rows = result.rows;

        let mut steps: Vec<WorkflowStepNode> = rows
            .iter()
            .filter_map(|row| {
                let metadata_str = row[3].as_str().unwrap_or("{}");
                let metadata: WorkflowStepMetadata = serde_json::from_str(metadata_str).ok()?;
                Some(WorkflowStepNode {
                    gid: row[0].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    workflow_gid: metadata.workflow_gid.clone(),
                    order: metadata.order,
                    description: metadata.description.clone(),
                    env: row[4].as_str().unwrap_or("local").to_string(),
                    metadata,
                })
            })
            .collect();

        // Sort by order
        steps.sort_by_key(|a| a.order);

        Ok(steps)
    }

    /// Search for workflows by name/alias
    pub fn search_workflows(
        &self,
        query: &str,
        _env: &str,
    ) -> Result<Vec<WorkflowNode>, Box<dyn std::error::Error>> {
        let normalized_query = query.to_lowercase();

        let query_str = r#"?[qualified_name, element_type, name, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], element_type = "workflow", file_path =~ "ontology://""#;

        let result = self
            .db
            .run_script(query_str, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut matches: Vec<WorkflowNode> = Vec::new();

        for row in rows {
            let qualified_name = row[0].as_str().unwrap_or("");
            let name = row[2].as_str().unwrap_or("");
            let metadata_str = row[3].as_str().unwrap_or("{}");
            let env = row[4].as_str().unwrap_or("local").to_string();

            let metadata: WorkflowMetadata = serde_json::from_str(metadata_str).unwrap_or_default();

            // Check if name or aliases match query
            let name_match = name.to_lowercase().contains(&normalized_query);
            let alias_match = metadata
                .aliases
                .iter()
                .any(|a| a.to_lowercase().contains(&normalized_query));

            if name_match || alias_match {
                matches.push(WorkflowNode {
                    gid: qualified_name.to_string(),
                    name: name.to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    aliases: metadata.aliases.clone(),
                    description: metadata.description.clone(),
                    env,
                    metadata,
                });
            }
        }

        Ok(matches)
    }

    /// Get ontology status (counts by type)
    pub fn get_ontology_status(&self) -> Result<OntologyStatus, Box<dyn std::error::Error>> {
        let ontology_types = [
            "domain_entity",
            "service",
            "api_endpoint",
            "data_store",
            "environment",
            "known_issue",
            "playbook",
            "team_knowledge",
            "workflow",
            "workflow_step",
            "decision_point",
            "failure_mode",
            "playbook_step",
        ];

        let mut concept_counts: HashMap<String, usize> = HashMap::new();
        let mut procedural_counts: HashMap<String, usize> = HashMap::new();
        let total_aliases = 0;
        let nodes_missing_aliases = 0;
        let workflows_without_failure_modes = 0;

        for ont_type in &ontology_types {
            let type_query = format!(
                r#"?[cnt] := code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env], file_path =~ "ontology://", element_type = "{}""#,
                ont_type
            );

            if let Ok(result) = self
                .db
                .run_script(&type_query, std::collections::BTreeMap::new())
            {
                let count = result.rows.first().and_then(|r| r[0].as_u64()).unwrap_or(0) as usize;
                let count = std::cmp::min(count, 1_000_000);

                if is_procedural_type(ont_type) {
                    procedural_counts.insert(ont_type.to_string(), count);
                } else {
                    concept_counts.insert(ont_type.to_string(), count);
                }
            }
        }

        Ok(OntologyStatus {
            concept_counts,
            procedural_counts,
            total_aliases,
            nodes_missing_aliases,
            workflows_without_failure_modes,
        })
    }
}

/// Check if type is procedural
fn is_procedural_type(t: &str) -> bool {
    matches!(
        t,
        "workflow" | "workflow_step" | "decision_point" | "failure_mode" | "playbook_step"
    )
}

/// Calculate match score for a query against an ontology node
pub fn calculate_match_score(
    query: &str,
    name: &str,
    aliases: &[String],
    description: &str,
) -> (f64, String) {
    let query_lower = query.to_lowercase();
    let name_lower = name.to_lowercase();

    // Exact name match is highest
    if name_lower == query_lower {
        return (1.0, format!("exact name match: {}", name));
    }

    // Name contains query
    if name_lower.contains(&query_lower) {
        return (0.8, format!("name contains '{}': {}", query, name));
    }

    // Alias match
    for alias in aliases {
        let alias_lower = alias.to_lowercase();
        if alias_lower == query_lower {
            return (0.9, format!("exact alias match: {}", alias));
        }
        if alias_lower.contains(&query_lower) {
            return (0.7, format!("alias contains '{}': {}", query, alias));
        }
    }

    // Description contains query
    if description.to_lowercase().contains(&query_lower) {
        return (0.5, format!("description contains '{}'", query));
    }

    // Check for multi-word query match
    let query_words: Vec<&str> = query_lower.split_whitespace().collect();
    if query_words.len() > 1 {
        let mut matched_words = 0;
        for word in &query_words {
            if name_lower.contains(word) || aliases.iter().any(|a| a.to_lowercase().contains(word))
            {
                matched_words += 1;
            }
        }
        if matched_words == query_words.len() {
            return (0.6, "all query words matched in name/aliases".to_string());
        }
    }

    (0.0, String::new())
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OntologyStatus {
    pub concept_counts: HashMap<String, usize>,
    pub procedural_counts: HashMap<String, usize>,
    pub total_aliases: usize,
    pub nodes_missing_aliases: usize,
    pub workflows_without_failure_modes: usize,
}

#[cfg(test)]
mod tests {
    use super::*;

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
}
