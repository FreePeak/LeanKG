use crate::db::models::CodeElement;
use crate::graph::query::folder_gn;
use crate::graph::GraphEngine;
use std::collections::{HashSet, VecDeque};

pub struct ImpactAnalyzer<'a> {
    graph: &'a GraphEngine,
}

impl<'a> ImpactAnalyzer<'a> {
    pub fn new(graph: &'a GraphEngine) -> Self {
        Self { graph }
    }

    /// Expand a directory qualified_name (trailing slash, e.g. `src/indexer/`)
    /// into its direct children at index time: the directory node itself,
    /// plus every `directory` / `File` child one level below (US-MP-08 /
    /// FR-MP-24 — "get_impact_radius accepts directory qualified names").
    /// The BFS treats each child as a seed; edges discovered at depth 1
    /// carry depth 1 severity because the requested scope is the folder.
    ///
    /// The indexer stores directory nodes without a trailing slash
    /// (`src/indexer`), so the canonical `folder_gn::strip` convention is
    /// applied before relationship lookups. `contains` edges connect a
    /// folder to its File children; `subdirectories` returns one-level-deep
    /// directory children. Function/class elements inside the folder are
    /// also seeded (their CALLS edges carry the folder's real dependencies).
    fn expand_directory_seeds(
        &self,
        folder_qn: &str,
    ) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let folder_path = folder_gn::strip(folder_qn);
        let mut seeds: Vec<String> = vec![folder_path.to_string()];
        let mut files_in_scope: Vec<String> = Vec::new();

        // File children of the folder itself (contains edges).
        for rel in self.graph.get_relationships(folder_path)? {
            if rel.rel_type == "contains" {
                seeds.push(rel.target_qualified.clone());
                files_in_scope.push(rel.target_qualified.clone());
            }
        }

        // One level of directory children; expand each to its File children.
        for child in self.graph.subdirectories(folder_qn)? {
            let child_path = folder_gn::strip(&child.qualified_name);
            seeds.push(child_path.to_string());
            for rel in self.graph.get_relationships(child_path)? {
                if rel.rel_type == "contains" {
                    seeds.push(rel.target_qualified.clone());
                    files_in_scope.push(rel.target_qualified.clone());
                }
            }
        }

        // Seed function/class elements whose file lives under the folder so
        // their CALLS edges drive impact without an explicit file seed.
        for file in files_in_scope {
            for element in self.graph.get_elements_by_file(&file)? {
                if matches!(
                    element.element_type.as_str(),
                    "function" | "class" | "method"
                ) {
                    seeds.push(element.qualified_name.clone());
                }
            }
        }
        Ok(seeds)
    }

    pub fn calculate_impact_radius(
        &self,
        start_file: &str,
        depth: u32,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        self.calculate_impact_radius_with_confidence(start_file, depth, 0.0)
    }

    pub fn calculate_impact_radius_with_confidence(
        &self,
        start_file: &str,
        depth: u32,
        min_confidence: f64,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        self.calculate_impact_radius_with_options(
            start_file,
            depth,
            min_confidence,
            &ImpactScanOptions::default(),
        )
    }

    /// Like [`Self::calculate_impact_radius_with_confidence`] but with
    /// explicit scan options. See [`ImpactScanOptions`] for the knobs.
    pub fn calculate_impact_radius_with_options(
        &self,
        start_file: &str,
        depth: u32,
        min_confidence: f64,
        opts: &ImpactScanOptions,
    ) -> Result<ImpactResult, Box<dyn std::error::Error>> {
        let mut visited = HashSet::new();
        let mut queue = VecDeque::new();
        let mut affected_with_confidence: Vec<AffectedElementWithConfidence> = Vec::new();
        let mut seen_qualified: HashSet<String> = HashSet::new();
        let mut guard = crate::budget::BudgetGuard::for_tool("calculate_impact_radius");

        // US-MP-08 / FR-MP-24: a directory qualified_name (`src/indexer/`)
        // seeds the BFS with the folder node plus its direct children, so
        // impact analysis at directory level works like a file-level scan.
        // Depth semantics stay natural: folder=0, child file=1, callee=2.
        let start_is_directory = folder_gn::is_directory(start_file);
        let seeds: Vec<String> = if start_is_directory {
            self.expand_directory_seeds(start_file)?
        } else {
            vec![start_file.to_string()]
        };

        for seed in seeds {
            queue.push_back((seed.clone(), 0));
            visited.insert(seed);
        }

        let mut truncated = false;
        while let Some((current, current_depth)) = queue.pop_front() {
            if current_depth >= depth {
                continue;
            }
            if affected_with_confidence.len() >= opts.max_affected {
                truncated = true;
                break;
            }

            let relationships = self.graph.get_relationships(&current)?;

            for rel in relationships {
                if rel.confidence < min_confidence {
                    continue;
                }
                let target = &rel.target_qualified;
                if !visited.contains(target) {
                    visited.insert(target.clone());
                    queue.push_back((target.clone(), current_depth + 1));
                }
                if seen_qualified.insert(target.clone()) {
                    if let Ok(Some(element)) = self.graph.find_element(target) {
                        let severity = rel.severity(current_depth + 1).to_string();
                        affected_with_confidence.push(AffectedElementWithConfidence {
                            element,
                            confidence: rel.confidence,
                            confidence_label: rel.confidence_label().to_string(),
                            severity,
                            depth: current_depth + 1,
                        });
                    }
                }
                guard.tick();
                if guard.check().is_err() {
                    truncated = true;
                    break;
                }
                if affected_with_confidence.len() >= opts.max_affected {
                    truncated = true;
                    break;
                }
            }
            if truncated {
                break;
            }

            let dependents = self.graph.get_dependents(&current)?;
            for rel in dependents {
                if rel.confidence < min_confidence {
                    continue;
                }
                let source = &rel.source_qualified;
                if !visited.contains(source) {
                    visited.insert(source.clone());
                    queue.push_back((source.clone(), current_depth + 1));
                }
                if seen_qualified.insert(source.clone()) {
                    if let Ok(Some(element)) = self.graph.find_element(source) {
                        let severity = rel.severity(current_depth + 1).to_string();
                        affected_with_confidence.push(AffectedElementWithConfidence {
                            element,
                            confidence: rel.confidence,
                            confidence_label: rel.confidence_label().to_string(),
                            severity,
                            depth: current_depth + 1,
                        });
                    }
                }
                guard.tick();
                if guard.check().is_err() {
                    truncated = true;
                    break;
                }
                if affected_with_confidence.len() >= opts.max_affected {
                    truncated = true;
                    break;
                }
            }
            if truncated {
                break;
            }
        }

        let affected_elements: Vec<CodeElement> = affected_with_confidence
            .iter()
            .map(|a| a.element.clone())
            .collect();

        Ok(ImpactResult {
            start_file: start_file.to_string(),
            max_depth: depth,
            affected_elements,
            affected_with_confidence,
            truncated,
        })
    }
}

/// Tunable knobs for [`ImpactAnalyzer::calculate_impact_radius_with_options`].
#[derive(Debug, Clone)]
pub struct ImpactScanOptions {
    /// Hard cap on the number of affected elements returned. Defaults to
    /// 10_000 to keep memory bounded on big monorepos.
    pub max_affected: usize,
}

impl Default for ImpactScanOptions {
    fn default() -> Self {
        let max_affected = std::env::var("LEANKG_IMPACT_MAX_AFFECTED")
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(10_000);
        Self { max_affected }
    }
}

#[derive(Debug, Clone)]
pub struct AffectedElementWithConfidence {
    pub element: CodeElement,
    pub confidence: f64,
    pub confidence_label: String,
    pub severity: String,
    pub depth: u32,
}

#[derive(Debug)]
pub struct ImpactResult {
    pub start_file: String,
    pub max_depth: u32,
    pub affected_elements: Vec<CodeElement>,
    pub affected_with_confidence: Vec<AffectedElementWithConfidence>,
    /// True when the scan was aborted early because the budget /
    /// max-affected cap was reached. Callers should re-scope (smaller
    /// depth, fewer seeds, larger max_affected) and re-run.
    pub truncated: bool,
}

// ===========================================================================
// Semantic-retrieval traversal (Stage 4)
//
// Adaptive N-hop BFS from each seed node. Hops + allowed edge types + fanout
// cap depend on the seed's element_type — see `traverse_rule_for`. The
// function is feature-independent: callers pass plain `(qualified_name,
// element_type)` tuples so this module doesn't depend on the gated retrieval
// types. The retrieval pipeline + MCP handler adapt their seed list to this
// shape.
//
// See docs/plans/2026-06-30-embedding-retrieve-rerank-traverse.md §"Adaptive
// Traversal Rules".
// ====================================================================================

/// Hard ceiling on total traversed neighbors across all seeds, regardless of
/// per-seed fanout. Keeps MCP response size bounded for agents.
const GLOBAL_NEIGHBOR_CAP: usize = 60;

pub struct TraverseRule {
    pub hops: u32,
    pub edge_types: &'static [&'static str],
    pub fanout_cap: usize,
}

const WORKFLOW_EDGES: &[&str] = &[
    "has_step",
    "next_step",
    "branches_to",
    "implemented_by",
    "entry_point_of",
    "step_in_process",
    "has_failure_mode",
];

const STEP_EDGES: &[&str] = &[
    "next_step",
    "branches_to",
    "implemented_by",
    "handled_by_playbook",
    "has_failure_mode",
    "resolved_by_playbook",
];

const CONCEPT_EDGES: &[&str] = &[
    "owns_concept",
    "implements_concept",
    "exposes_endpoint",
    "reads_from",
    "writes_to",
    "documents_concept",
    "has_known_issue",
];

const ISSUE_EDGES: &[&str] = &[
    "has_known_issue",
    "resolved_by_playbook",
    "documents_concept",
];

const CODE_EDGES: &[&str] = &[
    "calls",
    "imports",
    "references",
    "tested_by",
    "documented_by",
    "implements_concept",
];

const FILE_EDGES: &[&str] = &[
    "imports",
    "references",
    "tested_by",
    "documented_by",
    "contains",
    "defines",
];

const DOC_EDGES: &[&str] = &["documented_by", "documents_concept"];

/// Element types that should never appear as traversal neighbors.
/// `unknown` is indexer noise — chain-call artifacts (iter, Ok,
/// unwrap_or, etc.) that tree-sitter extraction wrongly promotes to
/// first-class elements. `environment` is pure metadata. Both crowd
/// out real signal in `traverse_seeds` output.
const INDEXER_NOISE_TYPES: &[&str] = &["unknown", "environment"];

pub fn is_indexer_noise(element_type: &str) -> bool {
    INDEXER_NOISE_TYPES.contains(&element_type)
}

pub fn traverse_rule_for(element_type: &str) -> TraverseRule {
    match element_type {
        "workflow" => TraverseRule {
            hops: 2,
            edge_types: WORKFLOW_EDGES,
            fanout_cap: 20,
        },
        "workflow_step" | "decision_point" | "failure_mode" => TraverseRule {
            hops: 2,
            edge_types: STEP_EDGES,
            fanout_cap: 15,
        },
        "domain_entity" | "service" | "api_endpoint" | "data_store" => TraverseRule {
            hops: 1,
            edge_types: CONCEPT_EDGES,
            fanout_cap: 15,
        },
        "known_issue" | "playbook" | "team_knowledge" => TraverseRule {
            hops: 1,
            edge_types: ISSUE_EDGES,
            fanout_cap: 10,
        },
        "function" | "class" => TraverseRule {
            hops: 1,
            edge_types: CODE_EDGES,
            fanout_cap: 10,
        },
        "file" | "module" => TraverseRule {
            hops: 1,
            edge_types: FILE_EDGES,
            fanout_cap: 10,
        },
        _ => TraverseRule {
            hops: 1,
            edge_types: DOC_EDGES,
            fanout_cap: 5,
        },
    }
}

#[derive(Debug, Clone)]
pub struct TraversedNode {
    pub qualified_name: String,
    pub element_type: String,
    pub from_seed: String,
    pub via_edge: String,
    pub hop: u32,
}

#[derive(Debug, Clone)]
pub struct TraverseResult {
    pub nodes: Vec<TraversedNode>,
    pub edges: Vec<TraversedEdge>,
    pub total_neighbors: usize,
    pub capped: bool,
}

#[derive(Debug, Clone)]
pub struct TraversedEdge {
    pub source: String,
    pub target: String,
    pub rel_type: String,
}

/// Adaptive multi-hop BFS from a set of seed nodes. Returns traversed
/// neighbors (excluding the seeds themselves) plus the edges that connect
/// them. Honors per-seed-type fanout caps and the global
/// `GLOBAL_NEIGHBOR_CAP`.
pub fn traverse_seeds<I>(
    graph: &GraphEngine,
    seeds: I,
    env: Option<&str>,
) -> Result<TraverseResult, Box<dyn std::error::Error>>
where
    I: IntoIterator<Item = (String, String)>,
{
    use std::collections::{HashSet, VecDeque};

    let mut visited: HashSet<String> = HashSet::new();
    let mut nodes: Vec<TraversedNode> = Vec::new();
    let mut edges: Vec<TraversedEdge> = Vec::new();
    let mut total = 0usize;

    for (seed_qn, seed_type) in seeds {
        // Seed itself is always "visited" — we don't return it in the
        // traversed set even if a cycle would bring us back to it.
        visited.insert(seed_qn.clone());

        let rule = traverse_rule_for(&seed_type);
        let mut frontier: VecDeque<(String, u32, String, String)> = VecDeque::new();
        // (current_qn, current_hop, from_seed_qn, via_edge_into_current)
        frontier.push_back((seed_qn.clone(), 0, seed_qn.clone(), "seed".to_string()));

        let mut seed_count = 0usize;
        while let Some((current, hop, from, _via)) = frontier.pop_front() {
            if hop >= rule.hops {
                continue;
            }
            if seed_count >= rule.fanout_cap || total >= GLOBAL_NEIGHBOR_CAP {
                break;
            }

            let outgoing = graph.get_relationships(&current).unwrap_or_default();
            let incoming = graph
                .get_relationships_for_target(&current)
                .unwrap_or_default();

            for rel in outgoing.iter().chain(incoming.iter()) {
                if !rule.edge_types.contains(&rel.rel_type.as_str()) {
                    continue;
                }
                if let Some(wanted) = env {
                    if rel.env != wanted {
                        continue;
                    }
                }

                let neighbor = if rel.source_qualified == current {
                    rel.target_qualified.clone()
                } else {
                    rel.source_qualified.clone()
                };

                if visited.contains(&neighbor) {
                    continue;
                }

                let element_type = graph
                    .find_element(&neighbor)
                    .ok()
                    .flatten()
                    .map(|e| e.element_type)
                    .unwrap_or_else(|| "unknown".to_string());

                // Skip indexer noise: chain-call artifacts (iter, Ok,
                // unwrap_or, etc.) that tree-sitter extraction wrongly
                // promotes to first-class elements, plus pure-metadata
                // environment nodes. These crowd out real neighbors in
                // traversal output. Mark visited so we don't re-check.
                if is_indexer_noise(&element_type) {
                    visited.insert(neighbor);
                    continue;
                }

                visited.insert(neighbor.clone());

                edges.push(TraversedEdge {
                    source: rel.source_qualified.clone(),
                    target: rel.target_qualified.clone(),
                    rel_type: rel.rel_type.clone(),
                });

                nodes.push(TraversedNode {
                    qualified_name: neighbor.clone(),
                    element_type: element_type.clone(),
                    from_seed: from.clone(),
                    via_edge: rel.rel_type.clone(),
                    hop: hop + 1,
                });

                frontier.push_back((neighbor, hop + 1, from.clone(), rel.rel_type.clone()));

                seed_count += 1;
                total += 1;
                if total >= GLOBAL_NEIGHBOR_CAP || seed_count >= rule.fanout_cap {
                    break;
                }
            }
        }
    }

    Ok(TraverseResult {
        nodes,
        edges,
        total_neighbors: total,
        capped: total >= GLOBAL_NEIGHBOR_CAP,
    })
}

#[cfg(test)]
mod traverse_tests {
    use super::*;
    use crate::db::schema::init_db;
    use tempfile::TempDir;

    fn make_graph() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("impact.db")).unwrap();
        (GraphEngine::new(db), tmp)
    }

    fn elem(qn: &str, etype: &str, file: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.into(),
            element_type: etype.into(),
            name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
            file_path: file.into(),
            line_start: 1,
            line_end: 10,
            language: "rust".into(),
            ..Default::default()
        }
    }

    fn rel(src: &str, tgt: &str, rtype: &str) -> crate::db::models::Relationship {
        crate::db::models::Relationship {
            source_qualified: src.into(),
            target_qualified: tgt.into(),
            rel_type: rtype.into(),
            confidence: 0.95,
            metadata: serde_json::json!({"resolution_method": "name"}),
            ..Default::default()
        }
    }

    #[test]
    fn impact_radius_accepts_directory_qualified_name() {
        // US-MP-08 / FR-MP-24: get_impact_radius("src/indexer/") reaches
        // file-level edges inside the folder without a file-path seed.
        let (graph, _tmp) = make_graph();
        graph
            .insert_element(&elem("src", "directory", "src"))
            .unwrap();
        graph
            .insert_element(&elem("src/indexer", "directory", "src/indexer"))
            .unwrap();
        graph
            .insert_element(&elem("src/indexer/mod.rs", "File", "src/indexer/mod.rs"))
            .unwrap();
        graph
            .insert_element(&elem("src/graph", "directory", "src/graph"))
            .unwrap();
        graph
            .insert_element(&elem("src/graph/query.rs", "File", "src/graph/query.rs"))
            .unwrap();
        graph
            .insert_element(&elem(
                "src/indexer/mod.rs::extract",
                "function",
                "src/indexer/mod.rs",
            ))
            .unwrap();
        graph
            .insert_element(&elem(
                "src/graph/query.rs::run",
                "function",
                "src/graph/query.rs",
            ))
            .unwrap();
        // indexer/mod.rs::extract -> graph/query.rs::run (calls edge)
        graph
            .insert_relationship(&rel(
                "src/indexer/mod.rs::extract",
                "src/graph/query.rs::run",
                "calls",
            ))
            .unwrap();
        // src/indexer -> src/indexer/mod.rs contains
        graph
            .insert_relationship(&rel("src/indexer", "src/indexer/mod.rs", "contains"))
            .unwrap();

        let analyzer = ImpactAnalyzer::new(&graph);
        let result = analyzer
            .calculate_impact_radius("src/indexer/", 2)
            .expect("directory impact radius");
        let qns: HashSet<String> = result
            .affected_elements
            .iter()
            .map(|e| e.qualified_name.clone())
            .collect();
        assert!(
            qns.contains("src/indexer/mod.rs::extract"),
            "directory impact must include functions under the folder: {qns:?}"
        );
        assert!(
            qns.contains("src/graph/query.rs::run"),
            "directory impact must cross folder boundaries via edges: {qns:?}"
        );
        // Folder-scoped hits report natural BFS depths (folder=0 seed, child
        // file=1, callee=2) so severity labels stay meaningful.
        let run_hit = result
            .affected_with_confidence
            .iter()
            .find(|a| a.element.qualified_name == "src/graph/query.rs::run")
            .expect("cross-folder callee hit");
        // extract and run are both folder-internal seeds; the cross-folder
        // calls edge lands at depth 1 (folder scope = 0).
        assert_eq!(run_hit.depth, 1);
        let file_hit = result
            .affected_with_confidence
            .iter()
            .find(|a| a.element.qualified_name == "src/indexer/mod.rs")
            .expect("folder child file hit");
        assert_eq!(file_hit.depth, 1);
    }

    #[test]
    fn impact_radius_non_directory_preserves_depth_semantics() {
        let (graph, _tmp) = make_graph();
        graph
            .insert_element(&elem("src/a.rs::f", "function", "src/a.rs"))
            .unwrap();
        graph
            .insert_element(&elem("src/b.rs::g", "function", "src/b.rs"))
            .unwrap();
        graph
            .insert_relationship(&rel("src/a.rs::f", "src/b.rs::g", "calls"))
            .unwrap();
        let analyzer = ImpactAnalyzer::new(&graph);
        let result = analyzer
            .calculate_impact_radius("src/a.rs::f", 1)
            .expect("file impact");
        let hit = result
            .affected_with_confidence
            .iter()
            .find(|a| a.element.qualified_name == "src/b.rs::g")
            .expect("callee hit");
        assert_eq!(hit.depth, 1);
        assert_eq!(hit.severity, "WILL BREAK");
    }

    #[test]
    fn impact_scan_options_default_caps_at_10k() {
        let opts = ImpactScanOptions::default();
        assert_eq!(opts.max_affected, 10_000);
    }

    #[test]
    fn impact_scan_options_respects_env_override() {
        std::env::set_var("LEANKG_IMPACT_MAX_AFFECTED", "5");
        let opts = ImpactScanOptions::default();
        assert_eq!(opts.max_affected, 5);
        std::env::remove_var("LEANKG_IMPACT_MAX_AFFECTED");
    }

    #[test]
    fn rule_for_workflow_is_two_hops() {
        let r = traverse_rule_for("workflow");
        assert_eq!(r.hops, 2);
        assert_eq!(r.fanout_cap, 20);
        assert!(r.edge_types.contains(&"has_step"));
    }

    #[test]
    fn rule_for_function_is_one_hop() {
        let r = traverse_rule_for("function");
        assert_eq!(r.hops, 1);
        assert_eq!(r.fanout_cap, 10);
        assert!(r.edge_types.contains(&"calls"));
    }

    #[test]
    fn rule_for_unknown_type_falls_back_to_docs() {
        let r = traverse_rule_for("some-random-type");
        assert_eq!(r.hops, 1);
        assert_eq!(r.fanout_cap, 5);
        assert!(r.edge_types.contains(&"documented_by"));
    }

    // =========================================================================
    // Indexer-noise filtering tests
    //
    // `is_indexer_noise` prevents chain-call artifacts (iter, Ok, unwrap_or,
    // etc. — promoted to `unknown` element type by tree-sitter extraction)
    // and pure-metadata `environment` nodes from crowding out real signal
    // in `traverse_seeds` output.
    // =========================================================================

    #[test]
    fn is_indexer_noise_filters_unknown_type() {
        assert!(is_indexer_noise("unknown"));
    }

    #[test]
    fn is_indexer_noise_filters_environment_type() {
        assert!(is_indexer_noise("environment"));
    }

    #[test]
    fn is_indexer_noise_passes_real_code_types() {
        assert!(!is_indexer_noise("function"));
        assert!(!is_indexer_noise("class"));
        assert!(!is_indexer_noise("file"));
        assert!(!is_indexer_noise("module"));
        assert!(!is_indexer_noise("method"));
    }

    #[test]
    fn is_indexer_noise_passes_ontology_types() {
        assert!(!is_indexer_noise("workflow"));
        assert!(!is_indexer_noise("playbook"));
        assert!(!is_indexer_noise("domain_entity"));
        assert!(!is_indexer_noise("service"));
        assert!(!is_indexer_noise("known_issue"));
    }

    #[test]
    fn is_indexer_noise_passes_empty_and_arbitrary_strings() {
        // Non-noise strings (including empty) are not in the noise set.
        assert!(!is_indexer_noise(""));
        assert!(!is_indexer_noise("random_type"));
        assert!(!is_indexer_noise("cluster"));
    }

    #[test]
    fn indexer_noise_types_has_exactly_two_entries() {
        assert_eq!(INDEXER_NOISE_TYPES.len(), 2);
        assert!(INDEXER_NOISE_TYPES.contains(&"unknown"));
        assert!(INDEXER_NOISE_TYPES.contains(&"environment"));
    }

    #[test]
    fn global_neighbor_cap_is_60() {
        // Bounded MCP response size — documented contract.
        assert_eq!(GLOBAL_NEIGHBOR_CAP, 60);
    }

    // =========================================================================
    // Traverse rule edge-type coverage tests
    // =========================================================================

    #[test]
    fn rule_for_workflow_step_is_two_hops() {
        let r = traverse_rule_for("workflow_step");
        assert_eq!(r.hops, 2);
        assert!(r.fanout_cap > 0);
        assert!(r.edge_types.contains(&"next_step"));
        assert!(r.edge_types.contains(&"branches_to"));
    }

    #[test]
    fn rule_for_class_is_one_hop() {
        let r = traverse_rule_for("class");
        assert_eq!(r.hops, 1);
        assert!(r.edge_types.contains(&"calls") || r.edge_types.contains(&"imports"));
    }

    #[test]
    fn rule_for_file_is_one_hop() {
        let r = traverse_rule_for("file");
        assert_eq!(r.hops, 1);
        assert!(r.edge_types.contains(&"imports"));
        assert!(r.edge_types.contains(&"contains"));
    }

    #[test]
    fn rule_for_domain_entity_uses_concept_edges() {
        let r = traverse_rule_for("domain_entity");
        assert!(r.hops >= 1);
        assert!(
            r.edge_types.contains(&"owns_concept") || r.edge_types.contains(&"implements_concept")
        );
    }

    #[test]
    fn rule_for_known_issue_uses_issue_edges() {
        let r = traverse_rule_for("known_issue");
        assert!(r.hops >= 1);
        assert!(
            r.edge_types.contains(&"has_known_issue")
                || r.edge_types.contains(&"resolved_by_playbook")
        );
    }

    #[test]
    fn all_traverse_rules_have_positive_fanout_cap() {
        for et in &[
            "workflow",
            "workflow_step",
            "playbook",
            "playbook_step",
            "domain_entity",
            "service",
            "api_endpoint",
            "data_store",
            "known_issue",
            "function",
            "class",
            "file",
            "module",
            "method",
            "trait",
            "interface",
            "team_knowledge",
        ] {
            let r = traverse_rule_for(et);
            assert!(
                r.fanout_cap > 0,
                "fanout_cap for '{et}' should be positive, got {}",
                r.fanout_cap
            );
            assert!(
                !r.edge_types.is_empty(),
                "edge_types for '{et}' should not be empty"
            );
        }
    }
}
