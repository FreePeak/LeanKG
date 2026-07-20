//! Ontology Query Engine
//!
//! Provides methods for querying ontology nodes and expanding context.

use crate::db::models::{CodeElement, Relationship};
use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
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

/// A concept matched by `concept_search`, with its code references attached.
///
/// This is the "loaded concept" in the workflow:
///   grep extract user raw input -> scan concept ontology -> **load concept** -> query db
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MatchedConcept {
    pub gid: String,
    pub name: String,
    pub element_type: String,
    pub description: String,
    pub aliases: Vec<String>,
    pub match_score: f64,
    pub match_reason: String,
    /// File / directory / file::symbol references declared in the concept YAML.
    pub code_refs: Vec<String>,
    /// Documentation references declared in the concept YAML.
    pub docs: Vec<String>,
    /// Owners declared in the concept YAML.
    pub owned_by: Vec<String>,
}

/// Result of the concept-gated search workflow:
///   extract keywords -> scan concept ontology -> load concept -> query leankg db
///
/// `linked_code` holds the actual indexed code elements resolved from the matched
/// concepts' `code_refs`. If no concept matched, `fallback_used` is true and
/// `fallback_results` contains a name-based code search so callers still get output.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConceptSearchResult {
    pub query: String,
    pub extracted_keywords: Vec<String>,
    pub matched_concepts: Vec<MatchedConcept>,
    pub linked_code: Vec<CodeElement>,
    pub concept_match_count: usize,
    pub code_ref_count: usize,
    pub linked_code_count: usize,
    pub fallback_used: bool,
    pub fallback_results: Vec<CodeElement>,
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
            r#"?[qualified_name, element_type, name, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], element_type in [{}], regex_matches(file_path, "ontology://")"#,
            types_str
        );

        let result =
            crate::db::schema::run_script(&self.db, &query_str, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut matches: Vec<OntologyNodeInfo> = Vec::new();

        for row in rows {
            let qualified_name = row[0].get_str().unwrap_or("");
            let element_type = row[1].get_str().unwrap_or("");
            let name = row[2].get_str().unwrap_or("");
            let metadata_str = row[3].get_str().unwrap_or("{}");

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

        let result = crate::db::schema::run_script(&self.db, query_str, params)?;
        let rows = result.rows;

        for row in rows {
            let target = row[0].get_str().unwrap_or("");
            let rel_type = row[1].get_str().unwrap_or("");
            let confidence = row[2].get_float().unwrap_or(1.0);
            let metadata_str = row[3].get_str().unwrap_or("{}");

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

            // For concept-layer nodes (domain_entity, service, etc.), resolve
            // the concept's code_refs metadata into actual indexed code elements.
            // This is the same logic as concept_search: read code_refs from the
            // matched node's metadata, then query the DB for the real code.
            if !is_procedural_type(&node.element_type) {
                if let Ok(Some(full_element)) = self.find_element_by_qualified(&node.gid) {
                    let code_refs = json_str_array(&full_element.metadata, "code_refs");
                    if !code_refs.is_empty() {
                        let resolved = self.resolve_code_refs(&code_refs, (depth as usize) * 20)?;
                        all_elements.extend(resolved);
                    }
                }
            }

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

    /// Concept-gated search implementing the workflow:
    ///
    ///   1. **extract keywords** from the raw user input (tokenize, lowercase,
    ///      drop stop words)
    ///   2. **scan the concept ontology** for matching concepts by probing with
    ///      the full query and each extracted keyword against name / aliases /
    ///      description
    ///   3. **load each matched concept**, reading its `code_refs` from metadata
    ///   4. **query the leankg db** to resolve those `code_refs` into actual
    ///      indexed code elements
    ///
    /// If no concept matches, `fallback_used` is set and a name-based code search
    /// is returned in `fallback_results` so callers still get useful output.
    pub fn concept_search(
        &self,
        raw_input: &str,
        env: &str,
        limit: usize,
    ) -> Result<ConceptSearchResult, Box<dyn std::error::Error>> {
        let keywords = extract_keywords(raw_input);
        let limit = if limit == 0 { 20 } else { limit };

        // Build probe strings: the full query first (best for multi-word concept
        // names/aliases like "feature flag"), then each extracted keyword.
        let mut probes: Vec<String> = Vec::new();
        let full = raw_input.trim().to_lowercase();
        if !full.is_empty() {
            probes.push(full.clone());
        }
        for kw in &keywords {
            if !probes.contains(kw) {
                probes.push(kw.clone());
            }
        }

        // Scan the concept ontology with each probe; keep the best score per gid.
        let mut best_by_gid: std::collections::HashMap<String, OntologyNodeInfo> =
            std::collections::HashMap::new();
        for probe in &probes {
            let nodes = self.search_ontology_nodes(probe, env, 1)?;
            for node in nodes {
                best_by_gid
                    .entry(node.gid.clone())
                    .and_modify(|existing| {
                        if node.match_score > existing.match_score {
                            *existing = node.clone();
                        }
                    })
                    .or_insert_with(|| node.clone());
            }
        }

        // Keep only concept-layer (domain) nodes, sorted by score descending.
        let mut matched: Vec<OntologyNodeInfo> = best_by_gid.into_values().collect();
        matched.retain(|n| !is_procedural_type(&n.element_type));
        matched.sort_by(|a, b| {
            b.match_score
                .partial_cmp(&a.match_score)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        let matched: Vec<OntologyNodeInfo> = matched.into_iter().take(limit).collect();

        // Load full concept metadata (code_refs, docs, owned_by) for each match.
        let mut matched_concepts: Vec<MatchedConcept> = Vec::new();
        let mut all_code_refs: Vec<String> = Vec::new();
        for node in &matched {
            let (code_refs, docs, owned_by) = match self.find_element_by_qualified(&node.gid)? {
                Some(e) => {
                    let code_refs = json_str_array(&e.metadata, "code_refs");
                    let docs = json_str_array(&e.metadata, "docs");
                    let owned_by = json_str_array(&e.metadata, "owned_by");
                    (code_refs, docs, owned_by)
                }
                None => (vec![], vec![], vec![]),
            };
            for r in &code_refs {
                if !all_code_refs.contains(r) {
                    all_code_refs.push(r.clone());
                }
            }
            matched_concepts.push(MatchedConcept {
                gid: node.gid.clone(),
                name: node.name.clone(),
                element_type: node.element_type.clone(),
                description: node.description.clone(),
                aliases: node.aliases.clone(),
                match_score: node.match_score,
                match_reason: node.match_reason.clone(),
                code_refs: code_refs.clone(),
                docs,
                owned_by,
            });
        }

        // Resolve code_refs against indexed code elements (the "query db" step).
        let linked_code = if all_code_refs.is_empty() {
            Vec::new()
        } else {
            self.resolve_code_refs(&all_code_refs, limit * 4)?
        };
        let linked_code_count = linked_code.len();

        // Fallback: if no concept matched, do a name-based code search.
        let mut fallback_used = false;
        let mut fallback_results: Vec<CodeElement> = Vec::new();
        if matched_concepts.is_empty() {
            fallback_used = true;
            for kw in &keywords {
                let hits = self.search_code_elements_by_name(kw, limit)?;
                for h in hits {
                    if !fallback_results
                        .iter()
                        .any(|e| e.qualified_name == h.qualified_name)
                    {
                        fallback_results.push(h);
                    }
                }
                if fallback_results.len() >= limit {
                    break;
                }
            }
        }

        Ok(ConceptSearchResult {
            query: raw_input.to_string(),
            extracted_keywords: keywords,
            matched_concepts,
            linked_code,
            concept_match_count: matched.len(),
            code_ref_count: all_code_refs.len(),
            linked_code_count,
            fallback_used,
            fallback_results,
        })
    }

    /// Resolve a list of `code_refs` (file paths, directory paths, or
    /// `file::symbol` references) against the indexed code elements in the db.
    ///
    /// FR-ONT-MEGA-01: keyed / path-prefixed GraphEngine queries only — never
    /// `load_indexed_code_elements()` (full ~641k-row dump).
    fn resolve_code_refs(
        &self,
        code_refs: &[String],
        limit: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        if code_refs.is_empty() || limit == 0 {
            return Ok(Vec::new());
        }
        let engine = GraphEngine::new(self.db.clone());
        let mut matched: Vec<CodeElement> = Vec::new();
        let mut seen: std::collections::HashSet<String> = std::collections::HashSet::new();

        for raw_ref in code_refs {
            if matched.len() >= limit {
                break;
            }
            let remaining = limit - matched.len();
            let r = normalize_path(raw_ref);
            if r.is_empty() {
                continue;
            }

            // Exact qualified_name hit (file::symbol already stored as QN).
            if let Ok(Some(el)) = engine.find_element(raw_ref.trim()) {
                if seen.insert(el.qualified_name.clone()) {
                    matched.push(el);
                }
                continue;
            }
            if let Ok(Some(el)) = engine.find_element(&r) {
                if seen.insert(el.qualified_name.clone()) {
                    matched.push(el);
                }
                continue;
            }

            // file::symbol form
            if let Some((file_part, sym_part)) = r.split_once("::") {
                let file_norm = normalize_path(file_part);
                let sym_lower = sym_part.to_lowercase();
                let qn_guess = format!("{}::{}", file_norm, sym_part);
                if let Ok(Some(el)) = engine.find_element(&qn_guess) {
                    if seen.insert(el.qualified_name.clone()) {
                        matched.push(el);
                    }
                    continue;
                }
                let per_ref_cap = remaining.min(80);
                let candidates =
                    engine.find_elements_by_file_path_prefix(&file_norm, per_ref_cap)?;
                for e in candidates {
                    let efile = normalize_path(&e.file_path);
                    let matches_file = efile == file_norm
                        || efile.ends_with(&file_norm)
                        || file_norm.ends_with(&efile);
                    let matches_sym = e.name.to_lowercase() == sym_lower
                        || e.qualified_name
                            .to_lowercase()
                            .ends_with(&format!("::{}", sym_lower))
                        || e.name.to_lowercase().contains(&sym_lower);
                    if matches_file && matches_sym && seen.insert(e.qualified_name.clone()) {
                        matched.push(e);
                        if matched.len() >= limit {
                            break;
                        }
                    }
                }
                if matched.len() < limit {
                    for e in engine.search_by_name_typed(sym_part, None, 20)? {
                        let efile = normalize_path(&e.file_path);
                        if (efile == file_norm
                            || efile.ends_with(&file_norm)
                            || file_norm.ends_with(&efile))
                            && seen.insert(e.qualified_name.clone())
                        {
                            matched.push(e);
                            if matched.len() >= limit {
                                break;
                            }
                        }
                    }
                }
                continue;
            }

            // file or directory form — bounded path prefix query
            let r_norm = normalize_path(&r);
            let per_ref_cap = remaining.min(200);
            for e in engine.find_elements_by_file_path_prefix(&r_norm, per_ref_cap)? {
                if seen.insert(e.qualified_name.clone()) {
                    matched.push(e);
                    if matched.len() >= limit {
                        break;
                    }
                }
            }
        }

        tracing::debug!(
            target: "leankg::ontology",
            refs = code_refs.len(),
            matched = matched.len(),
            "TRACE concept_search: resolve_code_refs method=keyed"
        );
        Ok(matched)
    }

    /// Name-based code search over indexed (non-ontology) elements, used as the
    /// fallback when no concept ontology node matches.
    ///
    /// FR-ONT-MEGA-01: uses `search_by_name_typed` (`:limit`) — never full-table load.
    fn search_code_elements_by_name(
        &self,
        name: &str,
        limit: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let engine = GraphEngine::new(self.db.clone());
        engine.search_by_name_typed(name, None, limit.max(1))
    }

    /// Deprecated full-table loader — kept for rare offline/admin callers only.
    /// Hot paths must use keyed GraphEngine queries (FR-ONT-MEGA-01).
    #[allow(dead_code)]
    fn load_indexed_code_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        tracing::warn!(
            target: "leankg::mem",
            "load_indexed_code_elements() is deprecated on mega-graphs — use keyed GraphEngine lookups"
        );
        let tail = if crate::db::schema::run_script(
            &self.db,
            "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0",
            Default::default(),
        )
        .is_ok()
        {
            ", env, ontology_layer"
        } else {
            ", env"
        };
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env]
            := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
            !regex_matches(file_path, "^ontology://")
            :limit 1"#
        );
        let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;
        Ok(result
            .rows
            .iter()
            .map(|row| row_to_code_element(row))
            .collect())
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
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], qualified_name = $qn"#;

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(qualified_name.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].get_str().map(String::from);
        let cluster_id = row[8].get_str().map(String::from);
        let cluster_label = row[9].get_str().map(String::from);
        let metadata_str = row[10].get_str().unwrap_or("{}");
        let env = row[11].get_str().unwrap_or("local").to_string();

        Ok(Some(CodeElement {
            qualified_name: row[0].get_str().unwrap_or("").to_string(),
            element_type: row[1].get_str().unwrap_or("").to_string(),
            name: row[2].get_str().unwrap_or("").to_string(),
            file_path: row[3].get_str().unwrap_or("").to_string(),
            line_start: row[4].get_int().unwrap_or(0) as u32,
            line_end: row[5].get_int().unwrap_or(0) as u32,
            language: row[6].get_str().unwrap_or("").to_string(),
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
        // First find the workflow by name/alias/GID
        let workflows = self.search_workflows(workflow_query, env)?;

        let workflow_gid = if let Some(w) = workflows.first() {
            w.gid.clone()
        } else {
            // Fallback: if no workflow node matched, search for a workflow_step
            // whose name/alias matches the query. If found, trace its parent
            // workflow. This lets users search by step name (e.g. "checkout"
            // finds the "order" workflow that contains a "Checkout" step).
            let step_nodes = self.search_ontology_nodes(workflow_query, env, 1)?;
            let step_match = step_nodes
                .iter()
                .find(|n| n.element_type == "workflow_step");
            if let Some(step) = step_match {
                // Get the full element to read parent_qualified (the workflow GID)
                if let Some(full_elem) = self.find_element_by_qualified(&step.gid)? {
                    if let Some(parent) = &full_elem.parent_qualified {
                        parent.clone()
                    } else {
                        // Try metadata.workflow_gid as fallback
                        let meta_wgid = full_elem
                            .metadata
                            .get("workflow_gid")
                            .and_then(|v| v.as_str())
                            .unwrap_or("")
                            .to_string();
                        if meta_wgid.is_empty() {
                            return Ok(vec![]);
                        }
                        meta_wgid
                    }
                } else {
                    return Ok(vec![]);
                }
            } else {
                return Ok(vec![]);
            }
        };

        // Get all steps for this workflow
        let query_str = r#"?[qualified_name, element_type, name, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], element_type = "workflow_step", parent_qualified = $wgid"#;

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "wgid".to_string(),
            serde_json::Value::String(workflow_gid.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, query_str, params)?;
        let rows = result.rows;

        let mut steps: Vec<WorkflowStepNode> = rows
            .iter()
            .filter_map(|row| {
                let metadata_str = row[3].get_str().unwrap_or("{}");
                let metadata: WorkflowStepMetadata = serde_json::from_str(metadata_str).ok()?;
                Some(WorkflowStepNode {
                    gid: row[0].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    workflow_gid: metadata.workflow_gid.clone(),
                    order: metadata.order,
                    description: metadata.description.clone(),
                    env: row[4].get_str().unwrap_or("local").to_string(),
                    metadata,
                })
            })
            .collect();

        // Sort by order
        steps.sort_by_key(|a| a.order);

        Ok(steps)
    }

    /// Search for workflows by name/alias/GID
    pub fn search_workflows(
        &self,
        query: &str,
        _env: &str,
    ) -> Result<Vec<WorkflowNode>, Box<dyn std::error::Error>> {
        let normalized_query = query.to_lowercase();

        let query_str = r#"?[qualified_name, element_type, name, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], element_type = "workflow", regex_matches(file_path, "ontology://")"#;

        let result =
            crate::db::schema::run_script(&self.db, query_str, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut matches: Vec<WorkflowNode> = Vec::new();

        for row in rows {
            let qualified_name = row[0].get_str().unwrap_or("");
            let name = row[2].get_str().unwrap_or("");
            let metadata_str = row[3].get_str().unwrap_or("{}");
            let env = row[4].get_str().unwrap_or("local").to_string();

            let metadata: WorkflowMetadata = serde_json::from_str(metadata_str).unwrap_or_default();

            // Check if name, aliases, or GID ID match query
            let name_match = name.to_lowercase().contains(&normalized_query);
            let alias_match = metadata
                .aliases
                .iter()
                .any(|a| a.to_lowercase().contains(&normalized_query));
            let gid_match = qualified_name.to_lowercase().contains(&normalized_query);

            if name_match || alias_match || gid_match {
                matches.push(WorkflowNode {
                    gid: qualified_name.to_string(),
                    name: name.to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
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
        let _ontology_types = [
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
        let mut total_aliases: usize = 0;
        let mut nodes_missing_aliases: usize = 0;
        let mut workflow_gids: Vec<String> = Vec::new();
        let mut workflows_with_failure_modes: std::collections::HashSet<String> =
            std::collections::HashSet::new();

        // Get all ontology nodes with metadata
        let all_query = r#"?[qualified_name, element_type, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer], regex_matches(file_path, "ontology://")"#;

        if let Ok(result) =
            crate::db::schema::run_script(&self.db, all_query, std::collections::BTreeMap::new())
        {
            for row in &result.rows {
                let qualified_name = row[0].get_str().unwrap_or("");
                let element_type = row[1].get_str().unwrap_or("");
                let metadata_str = row[2].get_str().unwrap_or("{}");

                let metadata: serde_json::Value =
                    serde_json::from_str(metadata_str).unwrap_or_default();

                // Count aliases
                let aliases: Vec<String> = metadata
                    .get("aliases")
                    .and_then(|v| v.as_array())
                    .map(|arr| {
                        arr.iter()
                            .filter_map(|s| s.as_str().map(String::from))
                            .collect()
                    })
                    .unwrap_or_default();
                total_aliases += aliases.len();
                if aliases.is_empty() {
                    nodes_missing_aliases += 1;
                }

                // Count by type
                if is_procedural_type(element_type) {
                    *procedural_counts
                        .entry(element_type.to_string())
                        .or_insert(0) += 1;
                } else {
                    *concept_counts.entry(element_type.to_string()).or_insert(0) += 1;
                }

                if element_type == "workflow" {
                    workflow_gids.push(qualified_name.to_string());
                } else if element_type == "workflow_step" {
                    let failure_count = metadata
                        .get("failure_modes")
                        .and_then(|v| v.as_array())
                        .map(|a| a.len())
                        .unwrap_or(0);
                    if failure_count == 0 {
                        continue;
                    }
                    if let Some(workflow_gid) =
                        metadata.get("workflow_gid").and_then(|v| v.as_str())
                    {
                        workflows_with_failure_modes.insert(workflow_gid.to_string());
                    }
                }
            }
        }

        let workflows_without_failure_modes = workflow_gids
            .iter()
            .filter(|gid| !workflows_with_failure_modes.contains(*gid))
            .count();

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
    let desc_lower = description.to_lowercase();

    // Exact name match is highest
    if name_lower == query_lower {
        return (1.0, format!("exact name match: {}", name));
    }

    // Name contains full query
    if name_lower.contains(&query_lower) {
        return (0.8, format!("name contains '{}': {}", query, name));
    }

    // Exact alias match
    for alias in aliases {
        let alias_lower = alias.to_lowercase();
        if alias_lower == query_lower {
            return (0.9, format!("exact alias match: {}", alias));
        }
        if alias_lower.contains(&query_lower) {
            return (0.7, format!("alias contains '{}': {}", query, alias));
        }
    }

    // Description contains full query
    if desc_lower.contains(&query_lower) {
        return (0.5, format!("description contains '{}'", query));
    }

    // Multi-word query: score based on what fraction of query words match
    let query_words: Vec<&str> = query_lower.split_whitespace().collect();
    if query_words.len() > 1 {
        let mut matched_words: usize = 0;
        let mut match_sources: Vec<String> = Vec::new();
        for word in &query_words {
            if word.len() < 3 {
                continue; // skip very short words
            }
            if name_lower.contains(word) {
                matched_words += 1;
                match_sources.push(format!("{} (name)", word));
            } else if aliases.iter().any(|a| a.to_lowercase().contains(word)) {
                matched_words += 1;
                match_sources.push(format!("{} (alias)", word));
            } else if desc_lower.contains(word) {
                matched_words += 1;
                match_sources.push(format!("{} (desc)", word));
            }
        }
        let meaningful_words = query_words.iter().filter(|w| w.len() >= 3).count();
        if meaningful_words > 0 && matched_words > 0 {
            let ratio = matched_words as f64 / meaningful_words as f64;
            if ratio >= 0.5 {
                return (
                    ratio * 0.7,
                    format!(
                        "{} of {} meaningful query words matched: {}",
                        matched_words,
                        meaningful_words,
                        match_sources.join(", ")
                    ),
                );
            }
            if ratio > 0.0 {
                return (
                    ratio * 0.3,
                    format!(
                        "partial match: {} of {} words: {}",
                        matched_words,
                        meaningful_words,
                        match_sources.join(", ")
                    ),
                );
            }
        }
    }

    // Single-word query: check if any word from the query is in name or aliases
    if query_lower.len() > 2 {
        if name_lower.contains(&query_lower) {
            return (0.8, format!("name contains '{}': {}", query, name));
        }
        for alias in aliases {
            if alias.to_lowercase().contains(&query_lower) {
                return (0.7, format!("alias contains '{}': {}", query, alias));
            }
        }
        if desc_lower.contains(&query_lower) {
            return (0.4, format!("description contains '{}'", query));
        }
    }

    (0.0, String::new())
}

/// Normalize a path-like reference: trim whitespace, strip a leading `./`, and
/// trim surrounding slashes. Used so `code_refs` and stored `file_path` values
/// can be compared regardless of how each was rooted.
pub fn normalize_path(p: &str) -> String {
    p.trim()
        .trim_start_matches("./")
        .trim_matches('/')
        .to_string()
}

/// Read a `Vec<String>` field out of a JSON metadata object, tolerating a
/// missing or non-array field.
fn json_str_array(meta: &serde_json::Value, key: &str) -> Vec<String> {
    meta.get(key)
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|s| s.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default()
}

/// Map a CozoDB result row into a `CodeElement`. Mirrors the column order used
/// by `load_indexed_code_elements` / `find_element_by_qualified`.
fn row_to_code_element(row: &[cozo::DataValue]) -> CodeElement {
    CodeElement {
        qualified_name: row[0].get_str().unwrap_or("").to_string(),
        element_type: row[1].get_str().unwrap_or("").to_string(),
        name: row[2].get_str().unwrap_or("").to_string(),
        file_path: row[3].get_str().unwrap_or("").to_string(),
        line_start: row[4].get_int().unwrap_or(0) as u32,
        line_end: row[5].get_int().unwrap_or(0) as u32,
        language: row[6].get_str().unwrap_or("").to_string(),
        parent_qualified: row[7].get_str().map(String::from),
        cluster_id: row[8].get_str().map(String::from),
        cluster_label: row[9].get_str().map(String::from),
        metadata: serde_json::from_str(row[10].get_str().unwrap_or("{}")).unwrap_or_default(),
        env: row[11].get_str().unwrap_or("local").to_string(),
    }
}

/// Extract meaningful keywords from raw user input.
///
/// This is the first step of the concept ontology workflow ("grep extract user
/// raw input"): tokenize the raw natural-language input, lowercase it, strip
/// punctuation, and drop common stop words and very short tokens so the
/// remaining keywords can be scanned against the concept ontology.
pub fn extract_keywords(raw: &str) -> Vec<String> {
    const STOPWORDS: &[&str] = &[
        "the", "a", "an", "and", "or", "but", "of", "to", "in", "on", "for", "with", "is", "are",
        "was", "were", "be", "been", "being", "this", "that", "these", "those", "it", "its", "as",
        "at", "by", "from", "how", "what", "where", "why", "when", "which", "who", "whom", "whose",
        "do", "does", "did", "can", "could", "should", "would", "will", "shall", "may", "might",
        "must", "have", "has", "had", "i", "we", "you", "they", "he", "she", "my", "our", "your",
        "their", "me", "us", "them", "about", "into", "than", "then", "so", "if", "no", "not",
        "any", "all", "find", "show", "get", "tell", "explain", "describe", "see", "look", "want",
        "need", "please", "help", "use", "using", "used", "like", "also",
    ];
    let stopset: std::collections::HashSet<&str> = STOPWORDS.iter().copied().collect();

    raw.split_whitespace()
        .map(|w| {
            w.trim_matches(|c: char| !c.is_alphanumeric() && c != '-' && c != '_')
                .to_lowercase()
        })
        .filter(|w| w.len() >= 2 && !stopset.contains(w.as_str()))
        .fold(Vec::new(), |mut acc, w| {
            if !acc.contains(&w) {
                acc.push(w);
            }
            acc
        })
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OntologyStatus {
    pub concept_counts: HashMap<String, usize>,
    pub procedural_counts: HashMap<String, usize>,
    pub total_aliases: usize,
    pub nodes_missing_aliases: usize,
    pub workflows_without_failure_modes: usize,
}

/// Per-tool smoke-test result. `ok=true` means the call completed without
/// returning an error to the caller. `error` carries the message if not.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KgSelfTestEntry {
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// Full self-test report returned by `OntologyQueryEngine::self_test()`.
/// Covers the four kg_* tools that exercise Datalog rule bodies plus the
/// live schema snapshot for both core relations.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct KgSelfTestReport {
    pub code_elements: crate::db::schema::RelationSchema,
    pub relationships: crate::db::schema::RelationSchema,
    pub kg_context: KgSelfTestEntry,
    pub kg_concept_map: KgSelfTestEntry,
    pub kg_trace_workflow: KgSelfTestEntry,
    pub kg_ontology_status: KgSelfTestEntry,
    pub all_ok: bool,
}

impl OntologyQueryEngine {
    /// Run a non-mutating smoke test against every kg_* query path and
    /// return per-tool status alongside the live CozoDB schema snapshot.
    ///
    /// The probe queries use a synthetic `__selftest__` query string that
    /// does not match any ontology node in a real codebase, so the call
    /// returns an empty result set without erroring out. If a query path
    /// has a stale arity binding (the bug fixed in commit 030610a) the
    /// Datalog engine raises "Arity mismatch for rule application" and
    /// the corresponding entry surfaces the error message here.
    pub fn self_test(&self) -> KgSelfTestReport {
        let ce_schema = crate::db::schema::code_elements_schema(&self.db);
        let rel_schema = crate::db::schema::relationships_schema(&self.db);

        let probe = "__selftest__";
        let env = "local";

        let kg_context = match self.get_ontology_context(probe, env, 1) {
            Ok(_) => KgSelfTestEntry {
                ok: true,
                error: None,
            },
            Err(e) => KgSelfTestEntry {
                ok: false,
                error: Some(format!("{}", e)),
            },
        };

        let kg_concept_map = match self.search_ontology_nodes(probe, env, 1) {
            Ok(_) => KgSelfTestEntry {
                ok: true,
                error: None,
            },
            Err(e) => KgSelfTestEntry {
                ok: false,
                error: Some(format!("{}", e)),
            },
        };

        let kg_trace_workflow = match self.trace_workflow(probe, env) {
            Ok(_) => KgSelfTestEntry {
                ok: true,
                error: None,
            },
            Err(e) => KgSelfTestEntry {
                ok: false,
                error: Some(format!("{}", e)),
            },
        };

        let kg_ontology_status = match self.get_ontology_status() {
            Ok(_) => KgSelfTestEntry {
                ok: true,
                error: None,
            },
            Err(e) => KgSelfTestEntry {
                ok: false,
                error: Some(format!("{}", e)),
            },
        };

        let all_ok = kg_context.ok
            && kg_concept_map.ok
            && kg_trace_workflow.ok
            && kg_ontology_status.ok
            && ce_schema.canonical
            && rel_schema.canonical;

        KgSelfTestReport {
            code_elements: ce_schema,
            relationships: rel_schema,
            kg_context,
            kg_concept_map,
            kg_trace_workflow,
            kg_ontology_status,
            all_ok,
        }
    }
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
