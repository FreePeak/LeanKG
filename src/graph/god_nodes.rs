//! God-node / importance scoring (FR-GF-10) + persisted `node_scores`.
//!
//! FR-GF-10: index-time node importance = degree (in + out relationship
//! count) plus an optional cheap PageRank-like iterative rank. Scores are
//! persisted in a `node_scores` relation at index time so `get_architecture`
//! hotspots (FR-GF-12) and `get_god_nodes` can read them without recomputing
//! over every relationship on each call.

use crate::db::models::{CodeElement, Relationship};
use crate::db::schema::{run_script, CozoDb};
use crate::graph::GraphEngine;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// One persisted importance score row.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct NodeScore {
    pub qualified_name: String,
    /// Total relationship count (in + out).
    pub degree: u64,
    /// Cheap iterative rank (normalized 0.0..=1.0, degree fallback when
    /// no relationships exist). Optional — always computed because it is
    /// nearly free on top of the degree pass.
    pub rank_score: f64,
    /// PageRank-like score (normalized 0.0..=1.0) when the caller opts in.
    pub pagerank_score: Option<f64>,
}

/// Builds a degree + iterative-rank score table for every element in the
/// graph. Pure function over the two model lists — the unit-test seam for
/// FR-GF-10.
///
/// Scoring (importance = degree first, PageRank refines ties):
/// - `degree` = in + out relationship count (the classic god-node metric).
/// - `pagerank_score` = cheap power-iteration rank, normalized to the
///   maximum after the last iteration. Nodes with no outgoing edges are
///   handled via the standard dangling-mass redistribution term.
/// - `rank_score` = blended importance: `0.7 * (degree / max_degree) +
///   0.3 * pagerank_normalized`. Pure PageRank favors in-target nodes over
///   outward hubs; blending keeps hubs (the intended god nodes) on top while
///   still differentiating equal-degree nodes.
///
/// Ordering is rank desc, then degree desc, then name asc (deterministic).
pub fn score_graph(
    elements: &[CodeElement],
    relationships: &[Relationship],
    iterations: usize,
    damping: f64,
) -> Vec<NodeScore> {
    let iterations = iterations.max(1);
    let damping = damping.clamp(0.0, 1.0);

    // Adjacency: for each target, the sources that point at it.
    let mut in_neighbors: HashMap<String, Vec<String>> = HashMap::new();
    let mut out_degree: HashMap<String, usize> = HashMap::new();
    for rel in relationships {
        in_neighbors
            .entry(rel.target_qualified.clone())
            .or_default()
            .push(rel.source_qualified.clone());
        *out_degree.entry(rel.source_qualified.clone()).or_default() += 1;
    }

    let mut degree: HashMap<String, u64> = HashMap::new();
    for rel in relationships {
        *degree.entry(rel.source_qualified.clone()).or_default() += 1;
        *degree.entry(rel.target_qualified.clone()).or_default() += 1;
    }
    // Nodes with no relationships still get a row (degree 0).
    for e in elements {
        degree.entry(e.qualified_name.clone()).or_insert(0);
        in_neighbors.entry(e.qualified_name.clone()).or_default();
        out_degree.entry(e.qualified_name.clone()).or_insert(0);
    }

    let n = degree.len();
    let mut rank: HashMap<String, f64> = degree
        .keys()
        .map(|k| (k.clone(), 1.0 / n.max(1) as f64))
        .collect();

    for _ in 0..iterations {
        let mut next: HashMap<String, f64> = HashMap::with_capacity(n);
        // Dangling mass: nodes with zero out-degree leak their rank back.
        let dangling_sum: f64 = rank
            .iter()
            .filter(|(k, _)| out_degree.get(*k).copied().unwrap_or(0) == 0)
            .map(|(_, v)| v)
            .sum();
        let base = damping / n.max(1) as f64;
        for node in rank.keys() {
            let incoming: f64 = in_neighbors
                .get(node)
                .map(|srcs| {
                    srcs.iter()
                        .map(|s| {
                            let od = out_degree.get(s).copied().unwrap_or(1).max(1);
                            rank.get(s).copied().unwrap_or(0.0) / od as f64
                        })
                        .sum()
                })
                .unwrap_or(0.0);
            next.insert(
                node.clone(),
                base + (1.0 - damping) * (incoming + dangling_sum / n.max(1) as f64),
            );
        }
        rank = next;
    }

    let max_rank = rank.values().cloned().fold(0.0_f64, f64::max);
    let pagerank_max = max_rank.max(1.0);

    let max_degree = degree.values().cloned().max().unwrap_or(0).max(1);
    let has_edges = !relationships.is_empty();

    let mut scores: Vec<NodeScore> = degree
        .into_iter()
        .map(|(qn, deg)| {
            let deg_norm = deg as f64 / max_degree as f64;
            if has_edges {
                let pr_norm = rank.get(&qn).copied().unwrap_or(0.0) / pagerank_max;
                NodeScore {
                    qualified_name: qn,
                    degree: deg,
                    rank_score: 0.7 * deg_norm + 0.3 * pr_norm,
                    pagerank_score: Some(pr_norm),
                }
            } else {
                NodeScore {
                    qualified_name: qn,
                    degree: deg,
                    rank_score: deg_norm,
                    pagerank_score: None,
                }
            }
        })
        .collect();
    // Degree tiebreak keeps god-node ordering deterministic and matches the
    // existing `get_god_nodes` (degree descending).
    scores.sort_by(|a, b| {
        b.rank_score
            .partial_cmp(&a.rank_score)
            .unwrap_or(std::cmp::Ordering::Equal)
            .then(b.degree.cmp(&a.degree))
            .then(a.qualified_name.cmp(&b.qualified_name))
    });
    scores
}

const CREATE_NODE_SCORES: &str = r#":create node_scores {
    qualified_name: String =>
    degree: Int,
    rank_score: Float,
    pagerank_score: Float?
}"#;

pub fn ensure_node_scores_table(db: &CozoDb) -> Result<(), Box<dyn std::error::Error>> {
    let existing: std::collections::HashSet<String> =
        run_script(db, "::relations", Default::default())?
            .rows
            .iter()
            .filter_map(|r| r.first().and_then(|v| v.get_str()).map(String::from))
            .collect();
    if !existing.contains("node_scores") {
        run_script(db, CREATE_NODE_SCORES, Default::default())?;
    }
    Ok(())
}

/// Persist a fresh score table. Full replace (`:replace` requires an exact
/// full-relation rewrite; we instead delete all rows then put — the table is
/// small relative to the graph and this keeps the relation non-canonical).
pub fn persist_scores(db: &CozoDb, scores: &[NodeScore]) -> Result<(), Box<dyn std::error::Error>> {
    ensure_node_scores_table(db)?;
    run_script(
        db,
        r#"?[qualified_name] := *node_scores[qualified_name, _, _, _] :rm node_scores { qualified_name }"#,
        Default::default(),
    )?;
    for chunk in scores.chunks(500) {
        let batch: Vec<serde_json::Value> = chunk
            .iter()
            .map(|s| {
                serde_json::json!([
                    s.qualified_name,
                    s.degree as i64,
                    s.rank_score,
                    s.pagerank_score,
                ])
            })
            .collect();
        let query = r#"?[qualified_name, degree, rank_score, pagerank_score] <- $batch :put node_scores { qualified_name, degree, rank_score, pagerank_score }"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert("batch".to_string(), serde_json::Value::Array(batch));
        run_script(db, query, params)?;
    }
    Ok(())
}

/// Load persisted scores, highest rank first.
pub fn load_scores(db: &CozoDb) -> Result<Vec<NodeScore>, Box<dyn std::error::Error>> {
    ensure_node_scores_table(db)?;
    let result = run_script(
        db,
        r#"?[qualified_name, degree, rank_score, pagerank_score] := *node_scores[qualified_name, degree, rank_score, pagerank_score] :order -rank_score"#,
        Default::default(),
    )?;
    Ok(result
        .rows
        .iter()
        .map(|row| NodeScore {
            qualified_name: row[0].get_str().unwrap_or("").to_string(),
            degree: row[1].get_int().unwrap_or(0) as u64,
            rank_score: row[2].get_float().unwrap_or(0.0),
            pagerank_score: row[3].get_float(),
        })
        .collect())
}

/// Index-time entry point: recompute + persist scores for the whole graph.
/// Called from `refresh_index_inventory` so both bulk and incremental index
/// runs refresh scores once per batch (FR-GF-10). Never fails the index:
/// callers log and continue.
pub fn refresh_node_scores(graph: &GraphEngine) -> Result<usize, Box<dyn std::error::Error>> {
    let elements = graph.all_elements()?;
    let relationships = graph.all_relationships()?;
    let scores = score_graph(&elements, &relationships, 25, 0.85);
    let n = scores.len();
    persist_scores(graph.db(), &scores)?;
    Ok(n)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::schema::init_db;
    use tempfile::TempDir;

    fn element(qn: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: "function".to_string(),
            name: qn.rsplit("::").next().unwrap_or(qn).to_string(),
            file_path: qn.split("::").next().unwrap_or("").to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        }
    }

    fn rel(src: &str, dst: &str) -> Relationship {
        Relationship {
            source_qualified: src.to_string(),
            target_qualified: dst.to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.9,
            metadata: serde_json::json!({}),
            ..Default::default()
        }
    }

    #[test]
    fn score_graph_degree_counts_in_and_out() {
        // a -> b, a -> c, c -> b : degree a=2 (out), b=2 (in), c=2 (in+out)
        let elements = vec![element("x::a"), element("x::b"), element("x::c")];
        let rels = vec![
            rel("x::a", "x::b"),
            rel("x::a", "x::c"),
            rel("x::c", "x::b"),
        ];
        let scores = score_graph(&elements, &rels, 5, 0.85);
        let by_qn: HashMap<&str, &NodeScore> = scores
            .iter()
            .map(|s| (s.qualified_name.as_str(), s))
            .collect();
        assert_eq!(by_qn["x::a"].degree, 2);
        assert_eq!(by_qn["x::b"].degree, 2);
        assert_eq!(by_qn["x::c"].degree, 2);
        assert_eq!(scores.len(), 3);
    }

    #[test]
    fn score_graph_ranked_highest_first() {
        // Hub node a connects to many leaves; a must outrank a leaf.
        let mut elements = vec![element("x::a"), element("x::leaf1"), element("x::leaf2")];
        elements.push(element("x::leaf3"));
        let rels = vec![
            rel("x::a", "x::leaf1"),
            rel("x::a", "x::leaf2"),
            rel("x::a", "x::leaf3"),
        ];
        let scores = score_graph(&elements, &rels, 25, 0.85);
        assert_eq!(scores[0].qualified_name, "x::a");
        assert_eq!(scores[0].degree, 3);
        assert_eq!(scores[1].degree, 1);
        assert!(scores[0].rank_score > scores[1].rank_score);
        // Optional PageRank-like score is present and normalized.
        assert!(scores[0].pagerank_score.unwrap() <= 1.0);
        assert!(scores[0].pagerank_score.unwrap() > 0.0);
    }

    #[test]
    fn score_graph_dangling_node_handled() {
        // b has no out-edges (dangling); must not produce NaN/inf and the
        // sink must not overflow: both nodes keep finite, in-range scores.
        let elements = vec![element("x::a"), element("x::b")];
        let rels = vec![rel("x::a", "x::b")];
        let scores = score_graph(&elements, &rels, 25, 0.85);
        assert_eq!(scores.len(), 2);
        assert!(scores.iter().all(|s| s.rank_score.is_finite()));
        assert!(scores.iter().all(|s| s
            .pagerank_score
            .is_some_and(|p| p.is_finite() && (0.0..=1.0).contains(&p))));
        // Equal degree (1/1) → the sink b ranks marginally higher via
        // PageRank; the key guarantee is a stable total order, no ties.
        assert_eq!(scores[0].qualified_name, "x::b");
        assert_eq!(scores[1].qualified_name, "x::a");
        assert!(scores[0].pagerank_score.unwrap() > scores[1].pagerank_score.unwrap());
    }

    #[test]
    fn score_graph_empty_graph_returns_zero_scores() {
        let scores = score_graph(&[], &[], 5, 0.85);
        assert!(scores.is_empty());
    }

    #[test]
    fn persist_and_load_scores_roundtrip() {
        let tmp = TempDir::new().unwrap();
        let db = init_db(tmp.path()).unwrap();
        let scores = vec![
            NodeScore {
                qualified_name: "x::hub".to_string(),
                degree: 4,
                rank_score: 1.0,
                pagerank_score: Some(0.9),
            },
            NodeScore {
                qualified_name: "x::leaf".to_string(),
                degree: 1,
                rank_score: 0.1,
                pagerank_score: Some(0.05),
            },
        ];
        persist_scores(&db, &scores).unwrap();
        let loaded = load_scores(&db).unwrap();
        assert_eq!(loaded.len(), 2);
        assert_eq!(loaded[0].qualified_name, "x::hub");
        assert_eq!(loaded[0].degree, 4);
        assert_eq!(loaded[0].pagerank_score, Some(0.9));
        assert_eq!(loaded[1].qualified_name, "x::leaf");
    }

    #[test]
    fn persist_scores_replaces_previous_run() {
        let tmp = TempDir::new().unwrap();
        let db = init_db(tmp.path()).unwrap();
        persist_scores(
            &db,
            &[NodeScore {
                qualified_name: "x::old".to_string(),
                degree: 2,
                rank_score: 0.5,
                pagerank_score: None,
            }],
        )
        .unwrap();
        persist_scores(
            &db,
            &[NodeScore {
                qualified_name: "x::new".to_string(),
                degree: 3,
                rank_score: 1.0,
                pagerank_score: Some(1.0),
            }],
        )
        .unwrap();
        let loaded = load_scores(&db).unwrap();
        assert_eq!(loaded.len(), 1);
        assert_eq!(loaded[0].qualified_name, "x::new");
    }
}
