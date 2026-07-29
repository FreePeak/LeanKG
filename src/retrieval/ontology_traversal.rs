//! Ontology-guided **top-down** graph traversal for semantic search.
//!
//! [`crate::graph::traversal::traverse_seeds`] is *additive* and
//! type-agnostic: starting from any seed it returns every typed neighbor
//! within a small hop budget, without preferring any target type or
//! scoring the result. That is the right tool for "show me the
//! neighborhood of these seeds" (used by `kg_semantic_context` to enrich
//! the response with a `traversed` array).
//!
//! This module answers a different question: given a set of **upper**
//! nodes (class / struct / file / document / workflow / domain concept /
//! known issue / playbook / …) that already matched an NL intent, walk
//! *down* the graph from each one and discover the **function** nodes
//! that implement that intent. The discovered functions are then ranked
//! against the original intent with a *composite* embedding —
//! `"{upper_name}\n{function_blob}"` — so a function that lives under a
//! strongly-matching class beats the same function under an unrelated
//! one.
//!
//! Design notes:
//! - Reuses the same edge-type whitelists and indexer-noise filter as
//!   `traverse_seeds`; only the *direction of intent* is new (we head
//!   toward `function | method | constructor` and stop there).
//! - For concept / workflow / workflow_step nodes whose code links live
//!   in `metadata.code_refs` (and may not be materialized as DB edges),
//!   we fall back to the same keyed path-prefix resolution that
//!   `OntologyQueryEngine::resolve_code_refs` uses. No full-table scan
//!   (FR-ONT-MEGA-01).
//! - Pure data in / data out — no MCP / JSON concerns — so the engine is
//!   unit-testable in isolation.
//!
//! See `docs/plans/2026-07-27-semantic-search-ontology-traversal.md`.

#![cfg(feature = "embeddings")]

use crate::db::models::CodeElement;
use crate::embeddings::{build_blob, Embedder};
use crate::graph::traversal::is_indexer_noise;
use crate::graph::GraphEngine;
use std::collections::{HashMap, HashSet, VecDeque};

/// Element types we treat as the **deliverable** target of a downward
/// walk. Traversal stops as soon as it reaches one of these — we don't
/// expand *through* a function to its callees (that would explode the
/// frontier and dilute the signal).
pub const FUNCTION_TARGET_TYPES: &[&str] = &["function", "method", "constructor"];

/// Element types we treat as **upper** seeds (anything that is *not* a
/// function but can plausibly own / document / implement one). Mirrors
/// the `FilterPolicy::ALWAYS_INCLUDE_TYPES` set used by the retrieval
/// pipeline minus the function-like types.
pub const UPPER_TYPES: &[&str] = &[
    "class",
    "struct",
    "interface",
    "trait",
    "module",
    "file",
    "document",
    "doc_section",
    "workflow",
    "workflow_step",
    "decision_point",
    "failure_mode",
    "domain_entity",
    "service",
    "api_endpoint",
    "data_store",
    "known_issue",
    "playbook",
    "playbook_step",
    "team_knowledge",
];

/// Returns true if `element_type` is a function-like target of a
/// downward walk.
pub fn is_function_target(element_type: &str) -> bool {
    FUNCTION_TARGET_TYPES.contains(&element_type)
}

/// Returns true if `element_type` is a valid upper seed.
pub fn is_upper_type(element_type: &str) -> bool {
    UPPER_TYPES.contains(&element_type)
}

/// Per-type downward traversal policy — the "ontology distance from
/// this node type to a function node". Reuses the same shape as
/// [`crate::graph::traversal::TraverseRule`] but lives separately so we
/// never disturb the additive `traverse_seeds` callers.
pub struct DownwardRule {
    pub hops: u32,
    pub edge_types: &'static [&'static str],
    pub fanout_cap: usize,
}

// Reused edge whitelists (kept in sync with `src/graph/traversal.rs`).
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

// Downward-specific edge sets. `contains` / `defines` / `has_method` are
// the structural class→method and file→function bridges that the
// additive traversal deliberately leaves out of CODE_EDGES.
const CLASS_DOWN_EDGES: &[&str] = &["contains", "defines", "has_method", "has_property"];
const FILE_DOWN_EDGES: &[&str] = &[
    "contains",
    "defines",
    "imports",
    "references",
    "tested_by",
    "documented_by",
];
const DOC_DOWN_EDGES: &[&str] = &["references", "documented_by"];
const DOC_FALLBACK_EDGES: &[&str] = &["documented_by", "documents_concept"];

/// Returns the downward traversal rule for an upper-seed element type.
/// This is the "ontology distance from `element_type` to a function
/// node", expressed as a hop budget + allowed edge types + fanout cap.
///
/// Tuning rationale (mirrors the additive `traverse_rule_for`):
/// - `class` / `struct` / `interface` / `trait` / `module`: 1 hop via
///   containment edges — methods hang directly off their enclosing type.
/// - `file`: 1 hop — functions are direct children of their file.
/// - `document` / `doc_section`: 1 hop via `references` / `documented_by`
///   — docs link directly to the code they describe.
/// - `workflow`: 2 hops — workflow → step (`has_step`) → function
///   (`implemented_by`).
/// - `workflow_step` / `decision_point` / `failure_mode`: 1 hop via
///   `implemented_by`.
/// - `domain_entity` / `service` / `api_endpoint` / `data_store`: 2 hops
///   via `implements_concept` etc.; if DB edges yield nothing we also try
///   the `metadata.code_refs` fallback (see [`resolve_code_refs_fallback`]).
/// - `known_issue` / `playbook` / `playbook_step` / `team_knowledge`:
///   1 hop — these document specific code.
/// - unknown / other: 1 hop via doc edges, small fanout.
pub fn downward_rule_for(element_type: &str) -> DownwardRule {
    match element_type {
        "class" | "struct" | "interface" | "trait" | "module" => DownwardRule {
            hops: 1,
            edge_types: CLASS_DOWN_EDGES,
            fanout_cap: 12,
        },
        "file" => DownwardRule {
            hops: 1,
            edge_types: FILE_DOWN_EDGES,
            fanout_cap: 12,
        },
        "document" | "doc_section" => DownwardRule {
            hops: 1,
            edge_types: DOC_DOWN_EDGES,
            fanout_cap: 10,
        },
        "workflow" => DownwardRule {
            hops: 2,
            edge_types: WORKFLOW_EDGES,
            fanout_cap: 15,
        },
        "workflow_step" | "decision_point" | "failure_mode" => DownwardRule {
            hops: 1,
            edge_types: STEP_EDGES,
            fanout_cap: 12,
        },
        "domain_entity" | "service" | "api_endpoint" | "data_store" => DownwardRule {
            hops: 2,
            edge_types: CONCEPT_EDGES,
            fanout_cap: 12,
        },
        "known_issue" | "playbook" | "playbook_step" | "team_knowledge" => DownwardRule {
            hops: 1,
            edge_types: ISSUE_EDGES,
            fanout_cap: 8,
        },
        _ => DownwardRule {
            hops: 1,
            edge_types: DOC_FALLBACK_EDGES,
            fanout_cap: 5,
        },
    }
}

/// Hard ceiling on the total number of discovered functions across all
/// upper seeds. Keeps MCP response size bounded.
pub const GLOBAL_FUNCTION_CAP: usize = 80;

/// An upper node to traverse *down* from. `name` is used downstream to
/// build the composite embed text (`"{upper_name}\n{function_blob}"`).
#[derive(Debug, Clone)]
pub struct UpperSeed {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
}

impl UpperSeed {
    pub fn new(qualified_name: impl Into<String>, element_type: impl Into<String>) -> Self {
        Self::with_name(qualified_name, element_type, "")
    }

    pub fn with_name(
        qualified_name: impl Into<String>,
        element_type: impl Into<String>,
        name: impl Into<String>,
    ) -> Self {
        let qualified_name = qualified_name.into();
        let name = name.into();
        // Derive the display name before moving `qualified_name` into
        // the struct field.
        let display_name = if name.is_empty() {
            derive_display_name(&qualified_name)
        } else {
            name
        };
        Self {
            qualified_name,
            element_type: element_type.into(),
            name: display_name,
        }
    }
}

/// Derive a human-readable display name from a qualified_name when the
/// caller didn't supply one. Handles three QN shapes:
/// - ontology GID `ontology://env:scope:type:id:version` → `id` (e.g.
///   `...:domain_entity:refund:v1` → `refund`; version `v1` is noise).
/// - code `path/to/file.rs::Symbol` → `Symbol`.
/// - anything else → trailing `/`/`:` segment, else the whole string.
fn derive_display_name(qualified_name: &str) -> String {
    // Strip the `ontology://` scheme, then for a 5-part GID
    // `env:scope:type:id:version` return the `id` (4th part).
    if let Some(gid) = qualified_name.strip_prefix("ontology://") {
        let parts: Vec<&str> = gid.split(':').collect();
        if parts.len() >= 5 {
            let id = parts[parts.len() - 2];
            if !id.is_empty() {
                return id.to_string();
            }
        }
        // Fall through to segment logic for malformed GIDs.
    }
    qualified_name
        .rsplit(['/', ':'])
        .next()
        .filter(|s| !s.is_empty())
        .unwrap_or(qualified_name)
        .to_string()
}

/// A function discovered by downward traversal. `via_upper` is the QN of
/// the upper node it was reached from; `via_upper_type` / `via_edge` /
/// `hop` describe the path.
#[derive(Debug, Clone)]
pub struct DiscoveredFunction {
    pub qualified_name: String,
    pub element_type: String,
    pub file_path: String,
    pub env: String,
    pub via_upper: String,
    pub via_upper_type: String,
    pub via_upper_name: String,
    pub via_edge: String,
    pub hop: u32,
    /// Set by [`score_functions`] — cosine of the composite embedding
    /// (`"{upper_name}\n{function_blob}"`) against the intent vector.
    /// `None` until scored.
    pub composite_score: Option<f32>,
}

/// Walk *down* from each upper seed, collecting function nodes via the
/// per-type [`downward_rule_for`] policy. Branches terminate at function
/// targets (we don't expand through them). Deduplicates by function QN,
/// keeping the shortest-hop / first-seen upper node. Honors
/// [`GLOBAL_FUNCTION_CAP`].
///
/// For `domain_entity` / `workflow` / `workflow_step` upper seeds whose
/// code links live in `metadata.code_refs` rather than DB edges, falls
/// back to keyed path-prefix resolution (same path as
/// `OntologyQueryEngine::resolve_code_refs`) when BFS yields nothing.
pub fn traverse_to_functions(
    graph: &GraphEngine,
    upper_seeds: &[UpperSeed],
    env: Option<&str>,
) -> Result<Vec<DiscoveredFunction>, Box<dyn std::error::Error>> {
    let mut discovered: Vec<DiscoveredFunction> = Vec::new();
    // function QN -> index into `discovered`, so we can dedup and keep
    // the shortest-hop entry.
    let mut seen: HashMap<String, usize> = HashMap::new();
    // Global guard against runaway expansion across all upper seeds.
    let mut total = 0usize;

    for upper in upper_seeds {
        if total >= GLOBAL_FUNCTION_CAP {
            break;
        }

        let rule = downward_rule_for(&upper.element_type);
        let seed_fanout_cap = rule
            .fanout_cap
            .min(GLOBAL_FUNCTION_CAP.saturating_sub(total));

        let mut found_for_this_seed = 0usize;
        let mut visited: HashSet<String> = HashSet::new();
        visited.insert(upper.qualified_name.clone());

        // (current_qn, current_hop, via_edge_into_current)
        let mut frontier: VecDeque<(String, u32, String)> = VecDeque::new();
        frontier.push_back((upper.qualified_name.clone(), 0, "seed".to_string()));

        while let Some((current, hop, _via_edge_into_current)) = frontier.pop_front() {
            if found_for_this_seed >= seed_fanout_cap || total >= GLOBAL_FUNCTION_CAP {
                break;
            }
            if hop >= rule.hops {
                continue;
            }

            let outgoing = graph.get_relationships(&current).unwrap_or_default();
            let incoming = graph
                .get_relationships_for_target(&current)
                .unwrap_or_default();

            for rel in outgoing.iter().chain(incoming.iter()) {
                if found_for_this_seed >= seed_fanout_cap || total >= GLOBAL_FUNCTION_CAP {
                    break;
                }
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

                if !visited.insert(neighbor.clone()) {
                    continue;
                }

                let Some(element) = graph.find_element(&neighbor).ok().flatten() else {
                    // Element row missing — skip but keep as visited so we
                    // don't re-fetch on every hop.
                    continue;
                };
                if is_indexer_noise(&element.element_type) {
                    continue;
                }

                let next_hop = hop + 1;
                let via_edge = rel.rel_type.clone();

                if is_function_target(&element.element_type) {
                    // Target reached — record and do NOT expand further
                    // through it.
                    record_function(
                        &mut discovered,
                        &mut seen,
                        &mut total,
                        &element,
                        &upper.qualified_name,
                        &upper.element_type,
                        &upper.name,
                        &via_edge,
                        next_hop,
                    );
                    found_for_this_seed += 1;
                    continue;
                }

                // Non-target intermediate node: pass-through. Only push
                // if we still have hop budget.
                if next_hop < rule.hops {
                    frontier.push_back((neighbor, next_hop, via_edge));
                }
            }
        }

        // code_refs fallback for ontology nodes whose code links are
        // stored in metadata rather than as DB edges. Cheap and bounded
        // — only runs when BFS produced nothing for this seed.
        if found_for_this_seed == 0 && needs_code_refs_fallback(&upper.element_type) {
            resolve_code_refs_fallback(
                graph,
                upper,
                env,
                seed_fanout_cap,
                &mut discovered,
                &mut seen,
                &mut total,
            )?;
        }
    }

    Ok(discovered)
}

/// `true` for upper-seed types whose code links typically live in
/// `metadata.code_refs` rather than as DB edges. Triggers the fallback
/// resolver when BFS yields nothing.
fn needs_code_refs_fallback(element_type: &str) -> bool {
    matches!(
        element_type,
        "domain_entity"
            | "service"
            | "api_endpoint"
            | "data_store"
            | "workflow"
            | "workflow_step"
            | "known_issue"
            | "playbook"
            | "team_knowledge"
    )
}

/// Resolve `metadata.code_refs` of an upper-seed element into concrete
/// function nodes, using the same keyed path-prefix lookups as
/// `OntologyQueryEngine::resolve_code_refs`. No full-table scan
/// (FR-ONT-MEGA-01). Caps at `fanout_cap` matched functions.
fn resolve_code_refs_fallback(
    graph: &GraphEngine,
    upper: &UpperSeed,
    env: Option<&str>,
    fanout_cap: usize,
    discovered: &mut Vec<DiscoveredFunction>,
    seen: &mut HashMap<String, usize>,
    total: &mut usize,
) -> Result<(), Box<dyn std::error::Error>> {
    let Some(element) = graph.find_element(&upper.qualified_name)? else {
        return Ok(());
    };
    let code_refs = element
        .metadata
        .get("code_refs")
        .and_then(|v| v.as_array())
        .map(|arr| {
            arr.iter()
                .filter_map(|v| v.as_str().map(String::from))
                .collect::<Vec<_>>()
        });
    let Some(code_refs) = code_refs else {
        return Ok(());
    };
    if code_refs.is_empty() {
        return Ok(());
    }

    let mut matched = 0usize;
    for raw_ref in &code_refs {
        if matched >= fanout_cap || *total >= GLOBAL_FUNCTION_CAP {
            break;
        }
        // Try exact QN first.
        let candidates: Vec<CodeElement> = if let Ok(Some(el)) = graph.find_element(raw_ref.trim())
        {
            vec![el]
        } else if let Some((file_part, sym_part)) = raw_ref.trim().split_once("::") {
            // file::symbol form — bounded prefix query filtered by symbol.
            let per_ref_cap = (fanout_cap - matched).min(80);
            graph
                .find_elements_by_file_path_prefix(file_part, per_ref_cap)?
                .into_iter()
                .filter(|e| {
                    let sym_lower = sym_part.to_lowercase();
                    e.name.to_lowercase() == sym_lower
                        || e.qualified_name
                            .to_lowercase()
                            .ends_with(&format!("::{}", sym_lower))
                })
                .collect()
        } else {
            // file or directory form — bounded prefix query.
            let per_ref_cap = (fanout_cap - matched).min(80);
            graph.find_elements_by_file_path_prefix(raw_ref.trim(), per_ref_cap)?
        };

        for el in candidates {
            if matched >= fanout_cap || *total >= GLOBAL_FUNCTION_CAP {
                break;
            }
            if let Some(wanted) = env {
                if el.env != wanted {
                    continue;
                }
            }
            if !is_function_target(&el.element_type) {
                continue;
            }
            record_function(
                discovered,
                seen,
                total,
                &el,
                &upper.qualified_name,
                &upper.element_type,
                &upper.name,
                "code_ref",
                1,
            );
            matched += 1;
        }
    }
    Ok(())
}

/// Insert a discovered function, deduplicating by QN. When the function
/// was already discovered via a strictly shorter hop, the existing entry
/// is overwritten with the shorter-path provenance. Same-hop duplicates
/// keep the first-seen upper seed's provenance (deterministic for callers).
#[allow(clippy::too_many_arguments)]
fn record_function(
    discovered: &mut Vec<DiscoveredFunction>,
    seen: &mut HashMap<String, usize>,
    total: &mut usize,
    element: &CodeElement,
    via_upper: &str,
    via_upper_type: &str,
    via_upper_name: &str,
    via_edge: &str,
    hop: u32,
) {
    if let Some(&idx) = seen.get(&element.qualified_name) {
        // Keep the shortest-hop provenance.
        if hop < discovered[idx].hop {
            discovered[idx] = DiscoveredFunction {
                qualified_name: element.qualified_name.clone(),
                element_type: element.element_type.clone(),
                file_path: element.file_path.clone(),
                env: element.env.clone(),
                via_upper: via_upper.to_string(),
                via_upper_type: via_upper_type.to_string(),
                via_upper_name: via_upper_name.to_string(),
                via_edge: via_edge.to_string(),
                hop,
                composite_score: None,
            };
        }
        return;
    }
    seen.insert(element.qualified_name.clone(), discovered.len());
    discovered.push(DiscoveredFunction {
        qualified_name: element.qualified_name.clone(),
        element_type: element.element_type.clone(),
        file_path: element.file_path.clone(),
        env: element.env.clone(),
        via_upper: via_upper.to_string(),
        via_upper_type: via_upper_type.to_string(),
        via_upper_name: via_upper_name.to_string(),
        via_edge: via_edge.to_string(),
        hop,
        composite_score: None,
    });
    *total += 1;
}

/// Cosine similarity between two (assumed unit-norm, equal-length)
/// embedding vectors. The BGE embedder L2-normalizes its outputs, so
/// cosine = dot product. Length equality is asserted in debug builds;
/// unit-norm is assumed (no normalization is performed).
pub fn cosine(a: &[f32], b: &[f32]) -> f32 {
    debug_assert_eq!(a.len(), b.len(), "cosine: length mismatch");
    a.iter().zip(b.iter()).map(|(x, y)| x * y).sum()
}

/// Composite text used to re-embed a discovered function for scoring
/// against the original intent. Embedding `"{upper_name}\n{func_blob}"`
/// lets a function rank higher when its enclosing class / workflow /
/// doc matches the intent, even if the function's own blob is generic.
pub fn composite_text(upper_name: &str, func_blob: &str) -> String {
    if func_blob.is_empty() {
        upper_name.to_string()
    } else {
        format!("{upper_name}\n{func_blob}")
    }
}

/// Score every discovered function against `query_vec` using a composite
/// embedding of `"{upper_name}\n{function_blob}"`. Batches all embed
/// calls into one [`Embedder::embed`] for throughput. Sets
/// [`DiscoveredFunction::composite_score`] in place; returns the same
/// vector sorted by score descending.
///
/// `query_vec` must be the L2-normalized embedding of the original NL
/// intent. Passing it in avoids a second embed call per query — the
/// pipeline already computed it during HNSW retrieval.
pub fn score_functions(
    graph: &GraphEngine,
    query_vec: &[f32],
    functions: Vec<DiscoveredFunction>,
    embedder: &Embedder,
) -> Result<Vec<DiscoveredFunction>, Box<dyn std::error::Error>> {
    if functions.is_empty() {
        return Ok(functions);
    }

    // Fetch element blobs in one batch so we don't do N keyed lookups
    // inside the embed loop. Cache by QN in case of dupes.
    let mut blob_cache: HashMap<String, String> = HashMap::new();
    let qns: Vec<String> = functions.iter().map(|f| f.qualified_name.clone()).collect();
    for qn in &qns {
        if blob_cache.contains_key(qn) {
            continue;
        }
        let blob = graph
            .find_element(qn)
            .ok()
            .flatten()
            .and_then(|el| build_blob(&el))
            .unwrap_or_default();
        blob_cache.insert(qn.clone(), blob);
    }

    // Build composite texts in the same order as `functions`.
    let composite_texts: Vec<String> = functions
        .iter()
        .map(|f| {
            composite_text(
                &f.via_upper_name,
                blob_cache
                    .get(&f.qualified_name)
                    .map(|s| s.as_str())
                    .unwrap_or(""),
            )
        })
        .collect();

    let borrowed: Vec<String> = composite_texts; // already owned
    let vectors = embedder.embed(&borrowed)?;

    let mut scored = functions;
    for (func, vec) in scored.iter_mut().zip(vectors.iter()) {
        let raw = cosine(query_vec, vec);
        // Clamp into [-1, 1] defensively — embedder outputs are unit-norm
        // up to float error, but downstream consumers (score blending,
        // JSON serialization) expect a bounded similarity.
        let clamped = raw.clamp(-1.0, 1.0);
        func.composite_score = Some(clamped);
    }
    scored.sort_by(|a, b| {
        b.composite_score
            .unwrap_or(-1.0)
            .partial_cmp(&a.composite_score.unwrap_or(-1.0))
            .unwrap_or(std::cmp::Ordering::Equal)
    });
    Ok(scored)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn downward_rule_for_class_is_one_hop_via_containment() {
        for t in ["class", "struct", "interface", "trait", "module"] {
            let r = downward_rule_for(t);
            assert_eq!(r.hops, 1, "{t} should be 1 hop");
            assert!(
                r.edge_types.contains(&"contains"),
                "{t} should allow contains"
            );
            assert!(
                r.edge_types.contains(&"defines"),
                "{t} should allow defines"
            );
            assert!(
                r.edge_types.contains(&"has_method"),
                "{t} should allow has_method"
            );
        }
    }

    #[test]
    fn downward_rule_for_workflow_is_two_hops() {
        let r = downward_rule_for("workflow");
        assert_eq!(r.hops, 2);
        assert!(r.edge_types.contains(&"has_step"));
        assert!(r.edge_types.contains(&"implemented_by"));
    }

    #[test]
    fn downward_rule_for_concept_two_hops_with_code_refs_fallback() {
        for t in ["domain_entity", "service", "api_endpoint", "data_store"] {
            let r = downward_rule_for(t);
            assert_eq!(r.hops, 2, "{t} should be 2 hops");
            assert!(
                needs_code_refs_fallback(t),
                "{t} should need code_refs fallback"
            );
        }
    }

    #[test]
    fn downward_rule_for_step_one_hop_via_implemented_by() {
        for t in ["workflow_step", "decision_point", "failure_mode"] {
            let r = downward_rule_for(t);
            assert_eq!(r.hops, 1);
            assert!(
                r.edge_types.contains(&"implemented_by"),
                "{t} should allow implemented_by"
            );
        }
    }

    #[test]
    fn downward_rule_default_uses_doc_edges() {
        let r = downward_rule_for("some_unknown_type");
        assert_eq!(r.hops, 1);
        assert_eq!(r.fanout_cap, 5);
        assert!(r.edge_types.contains(&"documented_by"));
    }

    #[test]
    fn is_function_target_matches_function_types() {
        assert!(is_function_target("function"));
        assert!(is_function_target("method"));
        assert!(is_function_target("constructor"));
        assert!(!is_function_target("class"));
        assert!(!is_function_target("struct"));
        assert!(!is_function_target("file"));
    }

    #[test]
    fn is_upper_type_matches_upper_types() {
        assert!(is_upper_type("class"));
        assert!(is_upper_type("workflow"));
        assert!(is_upper_type("domain_entity"));
        assert!(is_upper_type("document"));
        assert!(!is_upper_type("function"));
        assert!(!is_upper_type("method"));
    }

    #[test]
    fn indexer_noise_types_are_filtered_by_is_indexer_noise() {
        // Re-exported from graph::traversal; ensures we honour the same
        // noise filter as the additive traversal.
        assert!(is_indexer_noise("unknown"));
        assert!(is_indexer_noise("environment"));
        assert!(!is_indexer_noise("function"));
    }

    #[test]
    fn cosine_identical_vectors_is_one() {
        let a = vec![0.6, 0.8, 0.0];
        let c = cosine(&a, &a);
        assert!(
            (c - 1.0).abs() < 1e-5,
            "identical unit vectors: cosine ~ 1, got {c}"
        );
    }

    #[test]
    fn cosine_orthogonal_vectors_is_zero() {
        let a = vec![1.0, 0.0];
        let b = vec![0.0, 1.0];
        let c = cosine(&a, &b);
        assert!(c.abs() < 1e-5, "orthogonal: cosine ~ 0, got {c}");
    }

    #[test]
    fn cosine_opposite_vectors_is_minus_one() {
        let a = vec![1.0, 0.0];
        let b = vec![-1.0, 0.0];
        let c = cosine(&a, &b);
        assert!((c + 1.0).abs() < 1e-5, "opposite: cosine ~ -1, got {c}");
    }

    #[test]
    fn composite_text_joins_upper_name_and_blob() {
        let t = composite_text(
            "SemanticRetrievalPipeline",
            "fn retrieve(query) -> Result<...>",
        );
        assert!(t.starts_with("SemanticRetrievalPipeline\n"));
        assert!(t.contains("fn retrieve"));
    }

    #[test]
    fn composite_text_empty_blob_is_just_upper_name() {
        let t = composite_text("MyClass", "");
        assert_eq!(t, "MyClass");
    }

    #[test]
    fn upper_seed_default_name_uses_trailing_segment() {
        let s = UpperSeed::new(
            "src/retrieval/pipeline.rs::SemanticRetrievalPipeline",
            "class",
        );
        assert_eq!(s.name, "SemanticRetrievalPipeline");
    }

    #[test]
    fn upper_seed_default_name_extracts_ontology_gid_id_not_version() {
        // GID format `ontology://env:scope:type:id:version` → the human
        // id is `refund`, not the `v1` version tag.
        let s = UpperSeed::new(
            "ontology://local:checkout-service:domain_entity:refund:v1",
            "domain_entity",
        );
        assert_eq!(s.name, "refund");
        let s2 = UpperSeed::new(
            "ontology://local:ws:workflow:code_indexing_flow:v2",
            "workflow",
        );
        assert_eq!(s2.name, "code_indexing_flow");
    }

    #[test]
    fn upper_seed_with_empty_name_falls_back_to_qn_segment() {
        let s = UpperSeed::with_name("src/foo.rs::Bar", "class", "");
        assert_eq!(s.name, "Bar");
        let s2 = UpperSeed::with_name("src/foo.rs::Bar", "class", "CustomName");
        assert_eq!(s2.name, "CustomName");
    }

    #[test]
    fn needs_code_refs_fallback_only_for_ontology_types() {
        assert!(needs_code_refs_fallback("domain_entity"));
        assert!(needs_code_refs_fallback("workflow"));
        assert!(needs_code_refs_fallback("workflow_step"));
        assert!(!needs_code_refs_fallback("class"));
        assert!(!needs_code_refs_fallback("file"));
        assert!(!needs_code_refs_fallback("document"));
    }

    #[test]
    fn record_function_dedups_keeping_shortest_hop() {
        let mut discovered: Vec<DiscoveredFunction> = Vec::new();
        let mut seen: HashMap<String, usize> = HashMap::new();
        let mut total = 0usize;

        let make_el = |qn: &str| CodeElement {
            qualified_name: qn.to_string(),
            element_type: "function".to_string(),
            file_path: "src/x.rs".to_string(),
            env: "local".to_string(),
            ..Default::default()
        };

        // First discovery via workflow (2 hops).
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &make_el("src/x.rs::foo"),
            "ontology://w",
            "workflow",
            "WorkflowA",
            "implemented_by",
            2,
        );
        assert_eq!(discovered.len(), 1);
        assert_eq!(total, 1);
        assert_eq!(discovered[0].hop, 2);

        // Same function via a class at 1 hop — should overwrite.
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &make_el("src/x.rs::foo"),
            "src/x.rs::Foo",
            "class",
            "Foo",
            "contains",
            1,
        );
        assert_eq!(discovered.len(), 1, "dedup must not add a second entry");
        assert_eq!(total, 1, "total must not double-count");
        assert_eq!(discovered[0].hop, 1, "shortest hop should win");
        assert_eq!(discovered[0].via_upper_type, "class");

        // A different function via the same class — should add.
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &make_el("src/x.rs::bar"),
            "src/x.rs::Foo",
            "class",
            "Foo",
            "contains",
            1,
        );
        assert_eq!(discovered.len(), 2);
        assert_eq!(total, 2);
    }

    #[test]
    fn record_function_longer_hop_does_not_overwrite_shorter() {
        let mut discovered: Vec<DiscoveredFunction> = Vec::new();
        let mut seen: HashMap<String, usize> = HashMap::new();
        let mut total = 0usize;
        let el = CodeElement {
            qualified_name: "src/x.rs::foo".to_string(),
            element_type: "function".to_string(),
            ..Default::default()
        };

        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &el,
            "C",
            "class",
            "C",
            "contains",
            1,
        );
        // Later attempt via workflow at 2 hops — must NOT overwrite.
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &el,
            "W",
            "workflow",
            "W",
            "implemented_by",
            2,
        );

        assert_eq!(discovered.len(), 1);
        assert_eq!(discovered[0].hop, 1);
        assert_eq!(discovered[0].via_upper, "C");
    }

    #[test]
    fn record_function_same_hop_keeps_first_seen() {
        // When two upper seeds reach the same function at the SAME hop,
        // the first-seen upper seed's provenance wins (deterministic for
        // callers). This is the contract tightened in the A5 fix.
        let mut discovered: Vec<DiscoveredFunction> = Vec::new();
        let mut seen: HashMap<String, usize> = HashMap::new();
        let mut total = 0usize;
        let make_el = |qn: &str| CodeElement {
            qualified_name: qn.to_string(),
            element_type: "function".to_string(),
            file_path: "src/x.rs".to_string(),
            env: "local".to_string(),
            ..Default::default()
        };

        // First: via class Foo at hop 1.
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &make_el("src/x.rs::foo"),
            "src/x.rs::Foo",
            "class",
            "Foo",
            "contains",
            1,
        );

        // Same function at the same hop 1, but via a different upper
        // (workflow WorkflowA). Must keep Foo, not overwrite.
        record_function(
            &mut discovered,
            &mut seen,
            &mut total,
            &make_el("src/x.rs::foo"),
            "ontology://w",
            "workflow",
            "WorkflowA",
            "has_step",
            1,
        );

        assert_eq!(discovered.len(), 1, "dedup at same hop is a no-op");
        assert_eq!(total, 1, "total must not double-count");
        assert_eq!(discovered[0].via_upper, "src/x.rs::Foo");
        assert_eq!(discovered[0].via_upper_type, "class");
        assert_eq!(discovered[0].via_upper_name, "Foo");
        assert_eq!(discovered[0].hop, 1);
    }

    #[test]
    fn cosine_equal_lengths_returns_standard_dot_product() {
        // Sanity test for the A6 fix: vectors of equal length produce
        // the documented unit-norm cosine. The length-mismatch branch
        // uses `debug_assert_eq!` and is verified separately by the
        // `cosine_length_mismatch_panics_in_debug` test below (which
        // is compiled only in debug mode, where `cargo test` defaults
        // run; in `cargo test --release` the assertion is a no-op).
        let a = vec![0.6, 0.8, 0.0];
        let b = vec![0.6, 0.8, 0.0];
        let c = cosine(&a, &b);
        assert!((c - 1.0).abs() < 1e-5, "identical unit vectors: {c}");
    }

    #[test]
    #[cfg(debug_assertions)]
    #[should_panic(expected = "cosine: length mismatch")]
    fn cosine_length_mismatch_panics_in_debug() {
        // A6 fix: `debug_assert_eq!(a.len(), b.len())` fires on
        // mismatched lengths. Only runs under `cargo test` (debug),
        // since `cargo test --release` strips debug_assertions.
        let a = vec![0.6, 0.8];
        let b = vec![0.6, 0.8, 0.0];
        let _ = cosine(&a, &b);
    }
}
