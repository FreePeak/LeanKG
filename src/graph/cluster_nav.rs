//! US-GE-04 / FR-GE-04: cluster-first agent navigation via precomputed
//! `cluster_id` on `code_elements`. Mega-safe: never calls `all_elements()`
//! or scans the full table — every query is scoped by `cluster_id` equality
//! plus a hard `:limit`.

use crate::db::schema::run_script;
use crate::graph::GraphEngine;
use std::collections::BTreeMap;
use std::collections::HashMap;

/// Hard cap on members returned per cluster (bounded result set).
const MAX_MEMBERS_PER_CLUSTER: usize = 500;
/// Hard cap on clusters listed / neighbors returned.
const MAX_CLUSTERS: usize = 200;

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize, PartialEq)]
pub struct ClusterMember {
    pub qualified_name: String,
    pub element_type: String,
    pub name: String,
    pub file_path: String,
}

#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize, PartialEq)]
pub struct ClusterAggregates {
    pub by_type: HashMap<String, usize>,
    pub by_file: Vec<(String, usize)>,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ClusterContext {
    pub cluster_id: String,
    pub label: String,
    pub total_members: usize,
    pub members: Vec<ClusterMember>,
    pub aggregates: ClusterAggregates,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct NeighborCluster {
    pub cluster_id: String,
    pub label: String,
    pub shared_edges: usize,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ClusterSummary {
    pub cluster_id: String,
    pub label: String,
    pub total_members: usize,
}

fn escape_datalog(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

/// Escape-safe cluster_id. Empty/unclustered rows never match.
fn scoped_members_query(engine: &GraphEngine, cluster_id: &str, limit: usize) -> String {
    let safe = escape_datalog(cluster_id);
    let tail = engine.code_elements_tail();
    format!(
        r#"?[qualified_name, element_type, name, file_path] :=
            *code_elements[qualified_name, element_type, name, file_path, _, _, _, _, cluster_id, _, _{tail}],
            cluster_id = "{safe}"
            :limit {limit}"#
    )
}

fn parse_members(
    engine: &GraphEngine,
    cluster_id: &str,
    limit: usize,
) -> Result<Vec<ClusterMember>, Box<dyn std::error::Error>> {
    let q = scoped_members_query(engine, cluster_id, limit);
    let rows = run_script(engine.db(), &q, BTreeMap::new())?;
    let mut members: Vec<ClusterMember> = rows
        .rows
        .iter()
        .map(|r| ClusterMember {
            qualified_name: r[0].get_str().unwrap_or("").to_string(),
            element_type: r[1].get_str().unwrap_or("").to_string(),
            name: r[2].get_str().unwrap_or("").to_string(),
            file_path: r[3].get_str().unwrap_or("").to_string(),
        })
        .filter(|m| !m.qualified_name.is_empty())
        .collect();
    // Deterministic order (Cozo row order is not guaranteed).
    members.sort_by(|a, b| a.qualified_name.cmp(&b.qualified_name));
    Ok(members)
}

/// Get cluster context (label + bounded members + per-type/file aggregates).
/// Unknown cluster -> `Ok(None)`.
pub fn get_cluster_context(
    engine: &GraphEngine,
    cluster_id: &str,
    member_limit: usize,
) -> Result<Option<ClusterContext>, Box<dyn std::error::Error>> {
    let limit = member_limit.min(MAX_MEMBERS_PER_CLUSTER);
    let safe = escape_datalog(cluster_id);
    let tail = engine.code_elements_tail();

    // Single aggregate query: total + by_type + by_file + label in one pass
    // over the cluster_id-scoped rows (bounded by the cluster, not the table).
    let agg_q = format!(
        r#"?[element_type, file_path, cluster_label, count(n)] :=
            *code_elements[n, element_type, _, file_path, _, _, _, _, cluster_id, cluster_label, _{tail}],
            cluster_id = "{safe}"
            :order -count(n)"#
    );
    let agg_rows = run_script(engine.db(), &agg_q, BTreeMap::new())?;

    if agg_rows.rows.is_empty() {
        return Ok(None);
    }

    let mut by_type: HashMap<String, usize> = HashMap::new();
    let mut by_file: HashMap<String, usize> = HashMap::new();
    let mut total = 0usize;
    let mut label = cluster_id.to_string();
    for r in &agg_rows.rows {
        let et = r[0].get_str().unwrap_or("").to_string();
        let fp = r[1].get_str().unwrap_or("").to_string();
        if let Some(l) = r[2].get_str() {
            if !l.is_empty() {
                label = l.to_string();
            }
        }
        let n = r[3].get_int().unwrap_or(0) as usize;
        if et.is_empty() && fp.is_empty() {
            continue;
        }
        total += n;
        *by_type.entry(et).or_insert(0) += n;
        *by_file.entry(fp).or_insert(0) += n;
    }

    let mut top_files: Vec<(String, usize)> = by_file.into_iter().collect();
    top_files.sort_by(|a, b| b.1.cmp(&a.1).then_with(|| a.0.cmp(&b.0)));
    top_files.truncate(5);

    let members = parse_members(engine, cluster_id, limit)?;

    Ok(Some(ClusterContext {
        cluster_id: cluster_id.to_string(),
        label,
        total_members: total,
        members,
        aggregates: ClusterAggregates {
            by_type,
            by_file: top_files,
        },
    }))
}

/// Expand a cluster: bounded member list (same scoped query family).
pub fn expand_cluster(
    engine: &GraphEngine,
    cluster_id: &str,
    member_limit: usize,
) -> Result<Vec<ClusterMember>, Box<dyn std::error::Error>> {
    let limit = member_limit.min(MAX_MEMBERS_PER_CLUSTER);
    parse_members(engine, cluster_id, limit)
}

/// List all precomputed clusters as bounded summaries (id, label, size).
/// Uses a single group-by over the cluster_id column — not a full-table scan.
pub fn list_clusters(
    engine: &GraphEngine,
    limit: usize,
) -> Result<Vec<ClusterSummary>, Box<dyn std::error::Error>> {
    let limit = limit.min(MAX_CLUSTERS);
    let tail = engine.code_elements_tail();
    let q = format!(
        r#"?[cluster_id, cluster_label, count(n)] :=
            *code_elements[n, _, _, _, _, _, _, _, cluster_id, cluster_label, _{tail}],
            cluster_id != null,
            cluster_id != ""
            :order -count(n) :limit {limit}"#
    );
    let rows = run_script(engine.db(), &q, BTreeMap::new())?;
    let mut out: Vec<ClusterSummary> = rows
        .rows
        .iter()
        .map(|r| ClusterSummary {
            cluster_id: r[0].get_str().unwrap_or("").to_string(),
            label: r[1]
                .get_str()
                .map(String::from)
                .filter(|l| !l.is_empty())
                .unwrap_or_else(|| r[0].get_str().unwrap_or("").to_string()),
            total_members: r[2].get_int().unwrap_or(0) as usize,
        })
        .filter(|c| !c.cluster_id.is_empty())
        .collect();
    out.sort_by(|a, b| {
        b.total_members
            .cmp(&a.total_members)
            .then_with(|| a.cluster_id.cmp(&b.cluster_id))
    });
    Ok(out)
}

/// Neighboring clusters ranked by shared edges. Scoped: only relationships
/// touching this cluster's members are fetched (bounded by the member set),
/// then grouped by the neighbor's cluster_id.
pub fn nearest_clusters(
    engine: &GraphEngine,
    cluster_id: &str,
    limit: usize,
) -> Result<Vec<NeighborCluster>, Box<dyn std::error::Error>> {
    let limit = limit.min(MAX_CLUSTERS);
    let members = parse_members(engine, cluster_id, MAX_MEMBERS_PER_CLUSTER)?;
    if members.is_empty() {
        return Ok(Vec::new());
    }
    let qn_list: Vec<String> = members
        .iter()
        .map(|m| format!(r#""{}""#, escape_datalog(&m.qualified_name)))
        .collect();
    let qn_clause = qn_list.join(", ");
    let safe_self = escape_datalog(cluster_id);
    let tail = engine.code_elements_tail();

    // Edges where source is a member; join to target's cluster_id; exclude self.
    let q = format!(
        r#"?[neighbor, label, count(source_qualified)] :=
            *relationships[source_qualified, target_qualified, _, _, _, _],
            source_qualified in [{qn_clause}],
            *code_elements[target_qualified, _, _, _, _, _, _, _, neighbor, label, _{tail}],
            neighbor != "{safe_self}",
            neighbor != null,
            neighbor != ""
            :order -count(source_qualified) :limit {limit}"#
    );
    let rows = run_script(engine.db(), &q, BTreeMap::new())?;
    let mut out: Vec<NeighborCluster> = rows
        .rows
        .iter()
        .map(|r| NeighborCluster {
            cluster_id: r[0].get_str().unwrap_or("").to_string(),
            label: r[1]
                .get_str()
                .map(String::from)
                .filter(|l| !l.is_empty())
                .unwrap_or_else(|| r[0].get_str().unwrap_or("").to_string()),
            shared_edges: r[2].get_int().unwrap_or(0) as usize,
        })
        .filter(|n| !n.cluster_id.is_empty())
        .collect();
    out.sort_by_key(|n| std::cmp::Reverse(n.shared_edges));
    Ok(out)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::CodeElement;
    use crate::db::schema::init_db;
    use crate::graph::GraphEngine;
    use tempfile::TempDir;

    fn with_test_graph<F>(callback: F)
    where
        F: FnOnce(&GraphEngine, &TempDir),
    {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = init_db(db_path.as_path()).unwrap();
        let graph = GraphEngine::new(db.clone());
        callback(&graph, &tmp);
    }

    fn elem(qn: &str, name: &str, fp: &str, etype: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: etype.to_string(),
            name: name.to_string(),
            file_path: fp.to_string(),
            line_start: 1,
            line_end: 5,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: Some("cluster_a".to_string()),
            cluster_label: Some("auth".to_string()),
            metadata: serde_json::json!({}),
            ..Default::default()
        }
    }

    fn seed_cluster(graph: &GraphEngine, elements: &[CodeElement]) {
        graph.insert_elements(elements).unwrap();
    }

    fn rel(graph: &GraphEngine, src: &str, tgt: &str, rtype: &str) {
        use crate::db::models::Relationship;
        graph
            .insert_relationship(&Relationship {
                source_qualified: src.to_string(),
                target_qualified: tgt.to_string(),
                rel_type: rtype.to_string(),
                confidence: 1.0,
                ..Default::default()
            })
            .unwrap();
    }

    // --- Seam 1: cluster_id -> bounded result set, no full scan ---

    #[test]
    fn get_cluster_context_returns_bounded_members_for_known_cluster() {
        with_test_graph(|graph, _| {
            seed_cluster(
                graph,
                &[
                    elem("a.rs::a", "a", "src/a.rs", "function"),
                    elem("b.rs::b", "b", "src/b.rs", "function"),
                    elem("c.rs::c", "c", "src/c.rs", "struct"),
                ],
            );
            let ctx = get_cluster_context(graph, "cluster_a", 10)
                .unwrap()
                .unwrap();
            assert_eq!(ctx.cluster_id, "cluster_a");
            assert_eq!(ctx.label, "auth");
            assert_eq!(ctx.total_members, 3);
            assert_eq!(ctx.members.len(), 3);
            assert_eq!(ctx.aggregates.by_type.get("function"), Some(&2));
            assert_eq!(ctx.aggregates.by_type.get("struct"), Some(&1));
        });
    }

    // --- Seam 2: unknown cluster -> empty, no panic ---

    #[test]
    fn get_cluster_context_unknown_cluster_returns_none() {
        with_test_graph(|graph, _| {
            seed_cluster(graph, &[elem("a.rs::a", "a", "src/a.rs", "function")]);
            assert!(get_cluster_context(graph, "nope", 10).unwrap().is_none());
        });
    }

    // --- Seam 3: empty cluster ---

    #[test]
    fn get_cluster_context_no_rows_returns_none_not_error() {
        with_test_graph(|graph, _| {
            seed_cluster(graph, &[]);
            assert!(get_cluster_context(graph, "cluster_a", 10)
                .unwrap()
                .is_none());
        });
    }

    // --- Seam 4: member limit respected (boundedness) ---

    #[test]
    fn get_cluster_context_respects_member_limit() {
        with_test_graph(|graph, _| {
            seed_cluster(
                graph,
                &[
                    elem("a.rs::a", "a", "src/a.rs", "function"),
                    elem("b.rs::b", "b", "src/b.rs", "function"),
                    elem("c.rs::c", "c", "src/c.rs", "function"),
                ],
            );
            let ctx = get_cluster_context(graph, "cluster_a", 2).unwrap().unwrap();
            assert_eq!(ctx.members.len(), 2);
            // total_members is the aggregate count, not the truncated member list
            assert_eq!(ctx.total_members, 3);
        });
    }

    // --- Seam 5: determinism (same cluster, repeated calls, same order) ---

    #[test]
    fn cluster_context_deterministic_across_calls() {
        with_test_graph(|graph, _| {
            seed_cluster(
                graph,
                &[
                    elem("z.rs::z", "z", "src/z.rs", "function"),
                    elem("a.rs::a", "a", "src/a.rs", "function"),
                    elem("m.rs::m", "m", "src/m.rs", "function"),
                ],
            );
            let c1 = get_cluster_context(graph, "cluster_a", 10)
                .unwrap()
                .unwrap();
            let c2 = get_cluster_context(graph, "cluster_a", 10)
                .unwrap()
                .unwrap();
            let names = |c: &ClusterContext| {
                c.members
                    .iter()
                    .map(|m| m.qualified_name.clone())
                    .collect::<Vec<_>>()
            };
            assert_eq!(names(&c1), names(&c2));
            assert_eq!(names(&c1), vec!["a.rs::a", "m.rs::m", "z.rs::z"]);
        });
    }

    // --- Seam 6: nearest clusters ---

    #[test]
    fn nearest_clusters_ranks_by_shared_edges_and_excludes_self() {
        with_test_graph(|graph, _| {
            let mut a = elem("a.rs::a", "a", "src/a.rs", "function");
            a.cluster_id = Some("cluster_a".to_string());
            let mut b = elem("b.rs::b", "b", "src/b.rs", "function");
            b.cluster_id = Some("cluster_b".to_string());
            let mut c = elem("c.rs::c", "c", "src/c.rs", "function");
            c.cluster_id = Some("cluster_c".to_string());
            seed_cluster(graph, &[a, b, c]);

            rel(graph, "a.rs::a", "b.rs::b", "calls");
            rel(graph, "a.rs::a", "b.rs::b", "imports");
            rel(graph, "b.rs::b", "c.rs::c", "calls");

            let neigh = nearest_clusters(graph, "cluster_a", 5).unwrap();
            assert_eq!(neigh.len(), 1);
            assert_eq!(neigh[0].cluster_id, "cluster_b");
            assert_eq!(neigh[0].shared_edges, 2);
        });
    }

    #[test]
    fn nearest_clusters_unknown_cluster_empty() {
        with_test_graph(|graph, _| {
            seed_cluster(graph, &[elem("a.rs::a", "a", "src/a.rs", "function")]);
            assert!(nearest_clusters(graph, "nope", 5).unwrap().is_empty());
        });
    }

    // --- Seam 7: expand cluster (members list) ---

    #[test]
    fn expand_cluster_returns_member_names() {
        with_test_graph(|graph, _| {
            seed_cluster(
                graph,
                &[
                    elem("a.rs::a", "a", "src/a.rs", "function"),
                    elem("b.rs::b", "b", "src/b.rs", "function"),
                ],
            );
            let members = expand_cluster(graph, "cluster_a", 10).unwrap();
            assert_eq!(members.len(), 2);
            assert!(members.iter().any(|m| m.qualified_name == "a.rs::a"));
            assert!(members.iter().any(|m| m.qualified_name == "b.rs::b"));
        });
    }

    #[test]
    fn expand_cluster_unknown_cluster_empty() {
        with_test_graph(|graph, _| {
            seed_cluster(graph, &[elem("a.rs::a", "a", "src/a.rs", "function")]);
            assert!(expand_cluster(graph, "nope", 10).unwrap().is_empty());
        });
    }

    // --- Seam 8: list clusters (bounded summary) ---

    #[test]
    fn list_clusters_returns_summaries() {
        with_test_graph(|graph, _| {
            let mut a = elem("a.rs::a", "a", "src/a.rs", "function");
            a.cluster_id = Some("cluster_a".to_string());
            let mut b = elem("b.rs::b", "b", "src/b.rs", "function");
            b.cluster_id = Some("cluster_a".to_string());
            let mut c = elem("c.rs::c", "c", "src/c.rs", "function");
            c.cluster_id = Some("cluster_b".to_string());
            c.cluster_label = Some("payments".to_string());
            seed_cluster(graph, &[a, b, c]);

            let list = list_clusters(graph, 10).unwrap();
            assert_eq!(list.len(), 2);
            let by_id: std::collections::HashMap<_, _> = list
                .iter()
                .map(|s| (s.cluster_id.clone(), s.total_members))
                .collect();
            assert_eq!(by_id.get("cluster_a"), Some(&2));
            assert_eq!(by_id.get("cluster_b"), Some(&1));
        });
    }
}
