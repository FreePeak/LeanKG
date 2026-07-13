//! Ontology-first, paginated discovery for mega-graphs.
//!
//! Large nested-git workspaces (e.g. BE with 600k+ elements) must never
//! materialize the full graph for discovery. Callers should go through:
//!   1. concept ontology scan
//!   2. semantic / name search with hard pagination
//!   3. targeted file/QN lookups
//!
//! Full-table helpers (`all_elements` / `all_relationships`) are refused when
//! the graph exceeds `LEANKG_MAX_CACHE_ELEMENTS` (default 50_000).

use crate::db::models::CodeElement;
use crate::graph::GraphEngine;
use crate::ontology::{ConceptSearchResult, OntologyQueryEngine};
use serde_json::{json, Value};

/// Default page size for discovery tools.
pub const DEFAULT_PAGE_LIMIT: usize = 20;
/// Hard ceiling for any single discovery page.
pub const MAX_PAGE_LIMIT: usize = 50;

/// Mega-graph threshold (same default as in-memory cache gate).
pub fn mega_graph_threshold() -> usize {
    std::env::var("LEANKG_MAX_CACHE_ELEMENTS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(50_000)
}

pub fn clamp_limit(limit: usize) -> usize {
    if limit == 0 {
        DEFAULT_PAGE_LIMIT
    } else {
        limit.min(MAX_PAGE_LIMIT)
    }
}

/// True when element count exceeds the mega-graph threshold.
pub fn is_mega_graph(engine: &GraphEngine) -> bool {
    engine
        .count_elements()
        .map(|n| n > mega_graph_threshold())
        .unwrap_or(true)
}

/// Standard refusal payload when a tool would full-scan a mega-graph.
pub fn mega_graph_refusal(tool: &str, element_count: usize) -> Value {
    let max = mega_graph_threshold();
    json!({
        "error": format!(
            "{tool} refused: graph has {element_count} elements (max {max} for full-scan tools)"
        ),
        "element_count": element_count,
        "max_full_scan": max,
        "hint": "Use concept_search, semantic_search, or search_code (ontology-first, paginated with limit/offset). Avoid get_clusters / full-tree scans on mega-graphs.",
        "recommended_tools": [
            "concept_search",
            "semantic_search",
            "search_code",
            "kg_context",
            "find_function",
            "query_file"
        ]
    })
}

/// If mega-graph, return refusal; otherwise None (caller may proceed).
pub fn refuse_full_scan_if_mega(engine: &GraphEngine, tool: &str) -> Option<Value> {
    match engine.count_elements() {
        Ok(n) if n > mega_graph_threshold() => Some(mega_graph_refusal(tool, n)),
        Ok(_) => None,
        Err(e) => Some(json!({
            "error": format!("{tool} refused: failed to count elements: {e}"),
            "hint": "Use concept_search / semantic_search with pagination."
        })),
    }
}

#[derive(Debug)]
pub struct DiscoverPage {
    pub query: String,
    pub env: String,
    pub limit: usize,
    pub offset: usize,
    pub method: String,
    pub concept: Option<ConceptSearchResult>,
    pub results: Vec<CodeElement>,
    pub total_estimate: usize,
    pub has_more: bool,
}

/// Ontology-first discovery with pagination.
///
/// Order:
/// 1. `OntologyQueryEngine::concept_search` (concept ontology → code_refs)
/// 2. If empty: paginated name search over keywords (no full-table load)
pub fn discover(
    engine: &GraphEngine,
    query: &str,
    env: &str,
    limit: usize,
    offset: usize,
    prefer_ontology: bool,
) -> Result<DiscoverPage, Box<dyn std::error::Error>> {
    let limit = clamp_limit(limit);
    let env = if env.is_empty() { "local" } else { env };

    if prefer_ontology {
        let oq = OntologyQueryEngine::new(engine.db().clone());
        // Fetch a slightly larger concept page then slice for offset.
        let fetch = limit
            .saturating_add(offset)
            .min(MAX_PAGE_LIMIT * 4)
            .max(limit);
        let concept = oq.concept_search(query, env, fetch)?;
        if concept.concept_match_count > 0 || !concept.linked_code.is_empty() {
            let mut merged = concept.linked_code.clone();
            if merged.is_empty() {
                merged = concept.fallback_results.clone();
            }
            let total_estimate = merged.len();
            let page: Vec<CodeElement> = merged.into_iter().skip(offset).take(limit).collect();
            let has_more = offset + page.len() < total_estimate;
            return Ok(DiscoverPage {
                query: query.to_string(),
                env: env.to_string(),
                limit,
                offset,
                method: "ontology+concept".to_string(),
                concept: Some(concept),
                results: page,
                total_estimate,
                has_more,
            });
        }
    }

    // Semantic / name fallback: keyword probes with DB-level typed search only.
    let query_lower = query.to_lowercase();
    let keywords: Vec<&str> = query_lower.split_whitespace().collect();
    let probes: Vec<&str> = if keywords.is_empty() {
        vec![query]
    } else {
        keywords
    };

    let mut seen = std::collections::HashSet::new();
    let mut scored: Vec<(i32, CodeElement)> = Vec::new();

    for probe in probes.iter().take(8) {
        let hits =
            engine.search_by_name_typed(probe, None, limit.saturating_add(offset).max(limit))?;
        for elem in hits {
            if !seen.insert(elem.qualified_name.clone()) {
                continue;
            }
            let name_l = elem.name.to_lowercase();
            let qn_l = elem.qualified_name.to_lowercase();
            let mut score = 0;
            if name_l == query_lower {
                score += 100;
            }
            if name_l.contains(&query_lower) {
                score += 40;
            }
            for kw in &probes {
                if name_l.contains(kw) {
                    score += 10;
                }
                if qn_l.contains(kw) {
                    score += 3;
                }
            }
            if score > 0 {
                scored.push((score, elem));
            }
        }
    }

    scored.sort_by_key(|b| std::cmp::Reverse(b.0));
    let total_estimate = scored.len();
    let page: Vec<CodeElement> = scored
        .into_iter()
        .skip(offset)
        .take(limit)
        .map(|(_, e)| e)
        .collect();
    let has_more = offset + page.len() < total_estimate;

    Ok(DiscoverPage {
        query: query.to_string(),
        env: env.to_string(),
        limit,
        offset,
        method: if prefer_ontology {
            "semantic+name_fallback".to_string()
        } else {
            "semantic+name".to_string()
        },
        concept: None,
        results: page,
        total_estimate,
        has_more,
    })
}

pub fn discover_page_to_json(page: &DiscoverPage) -> Value {
    let results: Vec<Value> = page
        .results
        .iter()
        .map(|e| {
            json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "element_type": e.element_type,
                "file": e.file_path,
                "file_path": e.file_path,
                "line": e.line_start,
                "line_start": e.line_start,
                "language": e.language,
                "env": e.env,
            })
        })
        .collect();

    let mut body = json!({
        "query": page.query,
        "env": page.env,
        "method": page.method,
        "results": results,
        "count": results.len(),
        "limit": page.limit,
        "offset": page.offset,
        "total_estimate": page.total_estimate,
        "has_more": page.has_more,
        "pagination": {
            "limit": page.limit,
            "offset": page.offset,
            "has_more": page.has_more,
            "next_offset": if page.has_more { Some(page.offset + page.limit) } else { None::<usize> }
        }
    });

    if let Some(concept) = &page.concept {
        body["matched_concepts"] = json!(concept
            .matched_concepts
            .iter()
            .map(|c| json!({
                "gid": c.gid,
                "name": c.name,
                "element_type": c.element_type,
                "match_score": c.match_score,
                "match_reason": c.match_reason,
                "code_refs": c.code_refs,
            }))
            .collect::<Vec<_>>());
        body["concept_match_count"] = json!(concept.concept_match_count);
        body["fallback_used"] = json!(concept.fallback_used);
    }

    body
}

/// Whether incremental indexing should skip full-graph dependent expansion.
pub fn skip_incremental_dependents(engine: &GraphEngine) -> bool {
    if std::env::var("LEANKG_INCREMENTAL_SKIP_DEPENDENTS")
        .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
        .unwrap_or(false)
    {
        return true;
    }
    is_mega_graph(engine)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn clamp_limit_caps_at_max_page() {
        assert_eq!(clamp_limit(0), DEFAULT_PAGE_LIMIT);
        assert_eq!(clamp_limit(10), 10);
        assert_eq!(clamp_limit(10_000), MAX_PAGE_LIMIT);
    }

    #[test]
    fn mega_graph_refusal_mentions_ontology_tools() {
        let v = mega_graph_refusal("get_clusters", 600_000);
        let s = v.to_string();
        assert!(s.contains("concept_search"));
        assert!(s.contains("600000"));
    }
}
