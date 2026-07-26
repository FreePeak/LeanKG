use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use std::collections::{HashMap, HashSet};

const DEFAULT_MAX_NODES: usize = 5000;

#[derive(Debug, Clone)]
pub struct ExportMeta {
    pub selected_nodes: usize,
    pub selected_edges: usize,
    pub max_nodes: usize,
    pub truncated: bool,
    pub scope_desc: String,
}

impl ExportMeta {
    pub fn banner_html(&self) -> String {
        if self.truncated {
            format!(
                r#"<div id="export-banner" style="background:#e94560;color:#fff;padding:8px 16px;text-align:center;font-size:0.85rem;">
                    TRUNCATED: showing {} of {} nodes (max {}) — scope: {}
                </div>"#,
                self.selected_nodes, self.selected_nodes, self.max_nodes, self.scope_desc
            )
        } else {
            format!(
                r#"<div id="export-banner" style="background:#0f3460;color:#eee;padding:8px 16px;text-align:center;font-size:0.85rem;">
                    {} nodes, {} edges — scope: {}
                </div>"#,
                self.selected_nodes, self.selected_edges, self.scope_desc
            )
        }
    }
}

/// Pure dedupe + edge filter, extracted for testability.
/// RC1: remove duplicate qualified_name; RC2: keep only edges with both endpoints in set.
/// Then apply truncation by degree if `budget < elements.len()`.
pub fn dedupe_and_filter(
    elements: &mut Vec<CodeElement>,
    relationships: &mut Vec<Relationship>,
    budget: usize,
) -> bool {
    let mut seen = HashSet::new();
    elements.retain(|e| seen.insert(e.qualified_name.clone()));

    let kept: HashSet<&str> = elements.iter().map(|e| e.qualified_name.as_str()).collect();
    relationships.retain(|r| {
        kept.contains(r.source_qualified.as_str()) && kept.contains(r.target_qualified.as_str())
    });

    let original = elements.len();
    let truncated = original > budget;

    if truncated {
        let degree: HashMap<String, usize> = {
            let mut d = HashMap::new();
            for rel in &*relationships {
                *d.entry(rel.source_qualified.clone()).or_insert(0) += 1;
                *d.entry(rel.target_qualified.clone()).or_insert(0) += 1;
            }
            d
        };

        elements.sort_by(|a, b| {
            let da = degree.get(&a.qualified_name).unwrap_or(&0);
            let db = degree.get(&b.qualified_name).unwrap_or(&0);
            db.cmp(da)
        });
        elements.truncate(budget);

        let kept: HashSet<&str> = elements.iter().map(|e| e.qualified_name.as_str()).collect();
        relationships.retain(|r| {
            kept.contains(r.source_qualified.as_str()) && kept.contains(r.target_qualified.as_str())
        });
    }

    truncated
}

#[allow(clippy::type_complexity)]
pub fn select_elements_and_relationships(
    engine: &GraphEngine,
    path_prefix: Option<&str>,
    community_id: Option<&str>,
    file_scope: Option<&str>,
    depth: u32,
    max_nodes: Option<usize>,
) -> Result<(Vec<CodeElement>, Vec<Relationship>, ExportMeta), Box<dyn std::error::Error>> {
    let budget = max_nodes.unwrap_or(DEFAULT_MAX_NODES);

    let (mut elements, mut relationships) = if let Some(file) = file_scope {
        select_file_scoped(engine, file, depth)?
    } else if let Some(prefix) = path_prefix {
        select_path_prefix(engine, prefix)?
    } else if let Some(cid) = community_id {
        select_community(engine, cid)?
    } else {
        let all_el = engine.all_elements()?;
        let all_rel = engine.all_relationships()?;
        (all_el, all_rel)
    };

    let truncated = dedupe_and_filter(&mut elements, &mut relationships, budget);

    let scope_desc = if let Some(file) = file_scope {
        format!("file:{} (depth {})", file, depth)
    } else if let Some(prefix) = path_prefix {
        format!("path:{}", prefix)
    } else if let Some(cid) = community_id {
        format!("community:{}", cid)
    } else {
        "full graph".to_string()
    };

    let meta = ExportMeta {
        selected_nodes: elements.len(),
        selected_edges: relationships.len(),
        max_nodes: budget,
        truncated,
        scope_desc,
    };

    Ok((elements, relationships, meta))
}

fn select_file_scoped(
    engine: &GraphEngine,
    file: &str,
    depth: u32,
) -> Result<(Vec<CodeElement>, Vec<Relationship>), Box<dyn std::error::Error>> {
    let mut visited_files = HashSet::new();
    let mut queue = vec![(file.to_string(), 0u32)];
    let mut scoped_rels = Vec::new();

    while let Some((current, d)) = queue.pop() {
        if d >= depth || !visited_files.insert(current.clone()) {
            continue;
        }
        if let Ok(rels) = engine.get_relationships(&current) {
            for rel in &rels {
                queue.push((rel.target_qualified.clone(), d + 1));
            }
            scoped_rels.extend(rels);
        }
    }

    let scoped_elements = engine
        .all_elements()?
        .into_iter()
        .filter(|e| visited_files.contains(&e.file_path))
        .collect();

    Ok((scoped_elements, scoped_rels))
}

fn select_path_prefix(
    engine: &GraphEngine,
    prefix: &str,
) -> Result<(Vec<CodeElement>, Vec<Relationship>), Box<dyn std::error::Error>> {
    let all = engine.all_elements()?;
    let normalized_prefix = prefix.strip_prefix("./").unwrap_or(prefix);
    let elements: Vec<CodeElement> = all
        .into_iter()
        .filter(|e| {
            let fp = e.file_path.strip_prefix("./").unwrap_or(&e.file_path);
            fp.starts_with(normalized_prefix)
        })
        .collect();
    let ids: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
    let relationships = engine.get_relationships_for_elements(&ids, None)?;
    Ok((elements, relationships))
}

fn select_community(
    engine: &GraphEngine,
    cid: &str,
) -> Result<(Vec<CodeElement>, Vec<Relationship>), Box<dyn std::error::Error>> {
    let all = engine.all_elements()?;
    let elements: Vec<CodeElement> = all
        .into_iter()
        .filter(|e| e.cluster_id.as_deref() == Some(cid) || e.cluster_label.as_deref() == Some(cid))
        .collect();
    let ids: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
    let relationships = engine.get_relationships_for_elements(&ids, None)?;
    Ok((elements, relationships))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn el(qn: &str, name: &str, el_type: &str, file: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            name: name.to_string(),
            element_type: el_type.to_string(),
            file_path: file.to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
            env: "local".into(),
        }
    }

    #[test]
    fn test_dedupe_qualified_name() {
        let mut els = vec![
            el("a.rs::f", "f", "function", "a.rs"),
            el("a.rs::f", "f", "function", "a.rs"),
            el("b.rs::g", "g", "function", "b.rs"),
        ];
        let mut rels = vec![];
        dedupe_and_filter(&mut els, &mut rels, 100);
        assert_eq!(els.len(), 2, "duplicate qualified_name must be removed");
        assert_eq!(els[0].qualified_name, "a.rs::f");
        assert_eq!(els[1].qualified_name, "b.rs::g");
    }

    #[test]
    fn test_edge_filter_always_retains_connected() {
        let mut els = vec![
            el("a.rs::f", "f", "function", "a.rs"),
            el("b.rs::g", "g", "function", "b.rs"),
        ];
        let mut rels = vec![
            Relationship {
                source_qualified: "a.rs::f".into(),
                target_qualified: "b.rs::g".into(),
                rel_type: "calls".into(),
                confidence: 1.0,
                ..Default::default()
            },
            Relationship {
                source_qualified: "a.rs::f".into(),
                target_qualified: "c.rs::h".into(),
                rel_type: "calls".into(),
                confidence: 1.0,
                ..Default::default()
            },
        ];
        dedupe_and_filter(&mut els, &mut rels, 100);
        assert_eq!(rels.len(), 1, "orphan edge must be stripped");
        assert_eq!(rels[0].target_qualified, "b.rs::g");
    }

    #[test]
    fn test_truncation_budget() {
        let mut els = (0..10)
            .map(|i| el(&format!("n{i}.rs::f"), "f", "function", &format!("n{i}.rs")))
            .collect::<Vec<_>>();
        let mut rels = vec![Relationship {
            source_qualified: "n0.rs::f".into(),
            target_qualified: "n1.rs::f".into(),
            rel_type: "calls".into(),
            confidence: 1.0,
            ..Default::default()
        }];
        dedupe_and_filter(&mut els, &mut rels, 3);
        assert_eq!(els.len(), 3, "must truncate to budget");
    }
}
