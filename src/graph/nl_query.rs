//! US-GF-03 / FR-GF-05: natural-language scoped subgraph query.
//!
//! Pipeline: keyword seed retrieval → bounded BFS expand (or shortest-path
//! when the question asks what connects A to B) → trim to token budget.
//! Every returned edge carries `confidence_label` (EXTRACTED / INFERRED /
//! AMBIGUOUS). Distinct from `orchestrate` (routing) and embed pipelines.

use std::collections::{HashMap, HashSet, VecDeque};

use serde::{Deserialize, Serialize};

use crate::db::models::{CodeElement, Relationship};

use super::query::{GraphEngine, PathHop, ShortestPathResult};

const DEFAULT_TOKEN_BUDGET: usize = 2000;
const DEFAULT_MAX_DEPTH: usize = 2;
const MAX_SEEDS: usize = 8;
const MAX_SEED_HITS_PER_TERM: usize = 3;
const MAX_FRONTIER_VISITS: usize = 500;

const STOP_WORDS: &[&str] = &[
    "a",
    "an",
    "the",
    "is",
    "are",
    "was",
    "were",
    "be",
    "been",
    "being",
    "what",
    "which",
    "who",
    "whom",
    "whose",
    "where",
    "when",
    "why",
    "how",
    "do",
    "does",
    "did",
    "can",
    "could",
    "should",
    "would",
    "will",
    "may",
    "might",
    "must",
    "of",
    "in",
    "on",
    "at",
    "to",
    "for",
    "from",
    "by",
    "with",
    "about",
    "into",
    "through",
    "during",
    "before",
    "after",
    "above",
    "below",
    "between",
    "under",
    "again",
    "further",
    "then",
    "once",
    "here",
    "there",
    "all",
    "both",
    "each",
    "few",
    "more",
    "most",
    "other",
    "some",
    "such",
    "no",
    "nor",
    "not",
    "only",
    "own",
    "same",
    "so",
    "than",
    "too",
    "very",
    "just",
    "also",
    "connects",
    "connect",
    "connected",
    "link",
    "links",
    "linked",
    "related",
    "relation",
    "relations",
    "show",
    "me",
    "find",
    "give",
    "tell",
    "please",
    "and",
    "or",
    "vs",
    "versus",
];

/// Lightweight synonym expansion for common connection-question nouns.
fn expand_term(term: &str) -> Vec<String> {
    let lower = term.to_lowercase();
    let mut out = vec![lower.clone()];
    match lower.as_str() {
        "db" | "database" => {
            out.extend(["db", "database", "repo", "repository", "store"].map(str::to_string));
        }
        "auth" | "authentication" => {
            out.extend(
                ["auth", "authentication", "login", "session", "authorize"].map(str::to_string),
            );
        }
        "api" => out.extend(["api", "handler", "controller", "route"].map(str::to_string)),
        _ => {}
    }
    out.sort();
    out.dedup();
    out
}

/// US-GF-03: Node in a budgeted subgraph response.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QueryGraphNode {
    pub qualified_name: String,
    pub name: String,
    pub element_type: String,
    pub file_path: String,
    pub is_seed: bool,
}

/// US-GF-03: Edge with provenance label for agents.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QueryGraphEdge {
    pub from: String,
    pub to: String,
    pub rel_type: String,
    pub confidence: f64,
    pub confidence_label: String,
}

/// US-GF-03 / FR-GF-05: Budgeted subgraph answering a connection question.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct QueryGraphResult {
    pub question: String,
    pub seeds: Vec<String>,
    pub nodes: Vec<QueryGraphNode>,
    pub edges: Vec<QueryGraphEdge>,
    pub hops: usize,
    pub truncated: bool,
    pub token_budget: usize,
    pub tokens_estimate: usize,
    /// When the question resolved to an A→B path, include that path summary.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<ShortestPathResult>,
}

impl GraphEngine {
    /// US-GF-03 / FR-GF-05: NL scoped subgraph query.
    pub fn query_graph(
        &self,
        question: &str,
        token_budget: Option<usize>,
        max_depth: Option<usize>,
    ) -> Result<QueryGraphResult, Box<dyn std::error::Error>> {
        let budget = token_budget
            .unwrap_or(DEFAULT_TOKEN_BUDGET)
            .clamp(200, 20_000);
        let depth = max_depth.unwrap_or(DEFAULT_MAX_DEPTH).clamp(1, 5);
        let question = question.trim();
        if question.is_empty() {
            return Err("question must not be empty".into());
        }

        tracing::debug!(
            "TRACE query_graph: question={:?} budget={} depth={}",
            question,
            budget,
            depth
        );

        let connect_pair = extract_connect_pair(question);
        let keywords = extract_keywords(question);

        let mut seed_qns: Vec<String> = Vec::new();
        let mut seed_set: HashSet<String> = HashSet::new();

        // Prefer explicit A/B ends when present.
        if let Some((a, b)) = &connect_pair {
            for term in [a.as_str(), b.as_str()] {
                for qn in self.resolve_seed_terms(term)? {
                    if seed_set.insert(qn.clone()) {
                        seed_qns.push(qn);
                    }
                }
            }
        }
        for kw in &keywords {
            if seed_qns.len() >= MAX_SEEDS {
                break;
            }
            for qn in self.resolve_seed_terms(kw)? {
                if seed_qns.len() >= MAX_SEEDS {
                    break;
                }
                if seed_set.insert(qn.clone()) {
                    seed_qns.push(qn);
                }
            }
        }

        tracing::debug!("TRACE query_graph: seeds={:?}", seed_qns);

        // Connection questions with two resolvable ends → shortest path first.
        let mut path_summary: Option<ShortestPathResult> = None;
        let mut nodes: HashMap<String, QueryGraphNode> = HashMap::new();
        let mut edges: Vec<QueryGraphEdge> = Vec::new();
        let mut hops_used = 0usize;

        if let Some((a, b)) = &connect_pair {
            if let (Some(src), Some(tgt)) = (
                seed_qns
                    .iter()
                    .find(|qn| qn_matches_term(qn, a))
                    .cloned()
                    .or_else(|| self.resolve_to_qualified(a)),
                seed_qns
                    .iter()
                    .find(|qn| qn_matches_term(qn, b))
                    .cloned()
                    .or_else(|| self.resolve_to_qualified(b)),
            ) {
                if let Some(path) = self.shortest_path(&src, &tgt, depth.max(6))? {
                    hops_used = path.hops;
                    for hop in &path.path {
                        push_edge_from_hop(&mut edges, hop);
                        ensure_node_stub(&mut nodes, &hop.from, seed_set.contains(&hop.from));
                        ensure_node_stub(&mut nodes, &hop.to, seed_set.contains(&hop.to));
                    }
                    if path.hops == 0 {
                        ensure_node_stub(&mut nodes, &path.source, true);
                    }
                    path_summary = Some(path);
                }
            }
        }

        // Always expand around seeds (fills empty path case + adds context).
        if !seed_qns.is_empty() {
            let (exp_nodes, exp_edges, exp_hops) =
                self.expand_from_seeds(&seed_qns, &seed_set, depth)?;
            hops_used = hops_used.max(exp_hops);
            for (qn, node) in exp_nodes {
                nodes.entry(qn).or_insert(node);
            }
            merge_edges(&mut edges, exp_edges);
        }

        // Enrich stubs with element metadata when available.
        self.enrich_nodes(&mut nodes)?;

        let mut result = QueryGraphResult {
            question: question.to_string(),
            seeds: seed_qns,
            nodes: nodes.into_values().collect(),
            edges,
            hops: hops_used,
            truncated: false,
            token_budget: budget,
            tokens_estimate: 0,
            path: path_summary,
        };
        result.nodes.sort_by(|a, b| {
            b.is_seed
                .cmp(&a.is_seed)
                .then_with(|| a.qualified_name.cmp(&b.qualified_name))
        });
        result
            .edges
            .sort_by(|a, b| a.from.cmp(&b.from).then_with(|| a.to.cmp(&b.to)));

        trim_to_budget(&mut result, budget);
        result.tokens_estimate = estimate_tokens(&result);
        tracing::debug!(
            "TRACE query_graph: nodes={} edges={} truncated={} tokens~{}",
            result.nodes.len(),
            result.edges.len(),
            result.truncated,
            result.tokens_estimate
        );
        Ok(result)
    }

    fn resolve_seed_terms(&self, term: &str) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let mut found: Vec<CodeElement> = Vec::new();
        let mut seen: HashSet<String> = HashSet::new();
        for variant in expand_term(term) {
            if let Some(qn) = self.resolve_to_qualified(&variant) {
                if seen.insert(qn.clone()) {
                    if let Some(el) = self.find_element(&qn)? {
                        found.push(el);
                    }
                }
            }
            for el in self
                .search_by_name_typed(&variant, None, MAX_SEED_HITS_PER_TERM * 3)?
                .into_iter()
                .take(MAX_SEED_HITS_PER_TERM)
            {
                if seen.insert(el.qualified_name.clone()) {
                    found.push(el);
                }
            }
        }
        found.sort_by(|a, b| {
            rank_seed_type(&a.element_type)
                .cmp(&rank_seed_type(&b.element_type))
                .then_with(|| a.qualified_name.cmp(&b.qualified_name))
        });
        Ok(found
            .into_iter()
            .take(MAX_SEED_HITS_PER_TERM)
            .map(|e| e.qualified_name)
            .collect())
    }

    #[allow(clippy::type_complexity)]
    fn expand_from_seeds(
        &self,
        seeds: &[String],
        seed_set: &HashSet<String>,
        max_depth: usize,
    ) -> Result<
        (HashMap<String, QueryGraphNode>, Vec<QueryGraphEdge>, usize),
        Box<dyn std::error::Error>,
    > {
        let mut nodes: HashMap<String, QueryGraphNode> = HashMap::new();
        let mut edges: Vec<QueryGraphEdge> = Vec::new();
        let mut edge_keys: HashSet<(String, String, String)> = HashSet::new();
        let mut visited: HashMap<String, usize> = HashMap::new();
        let mut queue: VecDeque<(String, usize)> = VecDeque::new();

        for seed in seeds {
            visited.insert(seed.clone(), 0);
            queue.push_back((seed.clone(), 0));
            ensure_node_stub(&mut nodes, seed, seed_set.contains(seed));
        }

        let mut max_hop = 0usize;
        while let Some((current, dist)) = queue.pop_front() {
            if visited.len() > MAX_FRONTIER_VISITS {
                break;
            }
            max_hop = max_hop.max(dist);
            if dist >= max_depth {
                continue;
            }
            let rels = self
                .get_relationships_involving_elements_fast(std::slice::from_ref(&current), None)?;
            let mut neighbors: Vec<(String, Relationship)> = Vec::new();
            for rel in rels {
                let next = if rel.source_qualified == current {
                    rel.target_qualified.clone()
                } else if rel.target_qualified == current {
                    rel.source_qualified.clone()
                } else {
                    continue;
                };
                neighbors.push((next, rel));
            }
            neighbors.sort_by(|a, b| {
                label_rank(a.1.confidence_label()).cmp(&label_rank(b.1.confidence_label()))
            });
            for (next, rel) in neighbors {
                let key = (
                    rel.source_qualified.clone(),
                    rel.target_qualified.clone(),
                    rel.rel_type.clone(),
                );
                if edge_keys.insert(key) {
                    edges.push(QueryGraphEdge {
                        from: rel.source_qualified.clone(),
                        to: rel.target_qualified.clone(),
                        rel_type: rel.rel_type.clone(),
                        confidence: rel.confidence,
                        confidence_label: rel.confidence_label().to_string(),
                    });
                }
                let next_dist = dist + 1;
                let should_visit = match visited.get(&next) {
                    Some(&prev) => next_dist < prev,
                    None => true,
                };
                if should_visit {
                    visited.insert(next.clone(), next_dist);
                    ensure_node_stub(&mut nodes, &next, seed_set.contains(&next));
                    queue.push_back((next, next_dist));
                }
            }
        }

        Ok((nodes, edges, max_hop))
    }

    fn enrich_nodes(
        &self,
        nodes: &mut HashMap<String, QueryGraphNode>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        for (qn, node) in nodes.iter_mut() {
            if let Some(el) = self.find_element(qn)? {
                node.name = el.name;
                node.element_type = el.element_type;
                node.file_path = el.file_path;
            }
        }
        Ok(())
    }
}

fn rank_seed_type(element_type: &str) -> u8 {
    match element_type {
        "function" | "method" => 0,
        "class" | "struct" | "interface" | "type" => 1,
        "file" => 2,
        "module" | "package" => 3,
        _ => 4,
    }
}

fn label_rank(label: &str) -> u8 {
    match label {
        "EXTRACTED" => 0,
        "INFERRED" => 1,
        _ => 2,
    }
}

fn qn_matches_term(qn: &str, term: &str) -> bool {
    let t = term.to_lowercase();
    let q = qn.to_lowercase();
    q.contains(&t)
        || q.rsplit("::")
            .next()
            .map(|n| n.contains(&t))
            .unwrap_or(false)
}

fn extract_keywords(question: &str) -> Vec<String> {
    let mut out = Vec::new();
    for raw in question.split(|c: char| !c.is_alphanumeric() && c != '_' && c != '-') {
        let lower = raw.to_lowercase();
        if lower.len() < 2 {
            continue;
        }
        if STOP_WORDS.contains(&lower.as_str()) {
            continue;
        }
        if !out.contains(&lower) {
            out.push(lower);
        }
    }
    out
}

/// Detect "connects X to Y" / "from X to Y" / "between X and Y" style pairs.
fn extract_connect_pair(question: &str) -> Option<(String, String)> {
    let lower = question.to_lowercase();
    let patterns: &[&str] = &[
        r"(?i)connects?\s+(\w[\w-]*)\s+to\s+(?:the\s+)?(\w[\w-]*)",
        r"(?i)from\s+(\w[\w-]*)\s+to\s+(?:the\s+)?(\w[\w-]*)",
        r"(?i)between\s+(\w[\w-]*)\s+and\s+(?:the\s+)?(\w[\w-]*)",
        r"(?i)(\w[\w-]*)\s+(?:->|→|↔)\s+(\w[\w-]*)",
    ];
    for pat in patterns {
        if let Ok(re) = regex::Regex::new(pat) {
            if let Some(caps) = re.captures(&lower) {
                let a = caps.get(1)?.as_str().to_string();
                let b = caps.get(2)?.as_str().to_string();
                if !STOP_WORDS.contains(&a.as_str()) && !STOP_WORDS.contains(&b.as_str()) {
                    return Some((a, b));
                }
            }
        }
    }
    // Fallback: first two non-stop keywords as soft pair.
    let kws = extract_keywords(question);
    if kws.len() >= 2 {
        return Some((kws[0].clone(), kws[1].clone()));
    }
    None
}

fn ensure_node_stub(nodes: &mut HashMap<String, QueryGraphNode>, qn: &str, is_seed: bool) {
    nodes.entry(qn.to_string()).or_insert_with(|| {
        let name = qn.rsplit("::").next().unwrap_or(qn).to_string();
        QueryGraphNode {
            qualified_name: qn.to_string(),
            name,
            element_type: String::new(),
            file_path: String::new(),
            is_seed,
        }
    });
    if is_seed {
        if let Some(n) = nodes.get_mut(qn) {
            n.is_seed = true;
        }
    }
}

fn push_edge_from_hop(edges: &mut Vec<QueryGraphEdge>, hop: &PathHop) {
    let exists = edges
        .iter()
        .any(|e| e.from == hop.from && e.to == hop.to && e.rel_type == hop.rel_type);
    if !exists {
        edges.push(QueryGraphEdge {
            from: hop.from.clone(),
            to: hop.to.clone(),
            rel_type: hop.rel_type.clone(),
            confidence: hop.confidence,
            confidence_label: hop.confidence_label.clone(),
        });
    }
}

fn merge_edges(into: &mut Vec<QueryGraphEdge>, extra: Vec<QueryGraphEdge>) {
    for e in extra {
        let exists = into
            .iter()
            .any(|x| x.from == e.from && x.to == e.to && x.rel_type == e.rel_type);
        if !exists {
            into.push(e);
        }
    }
}

fn estimate_tokens(result: &QueryGraphResult) -> usize {
    serde_json::to_string(result)
        .map(|s| s.len() / 4)
        .unwrap_or(0)
}

/// Keep `path` hops aligned with surviving edges after provenance-based trimming.
fn sync_path_with_edges(result: &mut QueryGraphResult) {
    let Some(path) = result.path.as_mut() else {
        return;
    };
    let edge_keys: HashSet<(String, String, String)> = result
        .edges
        .iter()
        .map(|e| (e.from.clone(), e.to.clone(), e.rel_type.clone()))
        .collect();
    path.path.retain(|hop| {
        edge_keys.contains(&(hop.from.clone(), hop.to.clone(), hop.rel_type.clone()))
    });
    path.hops = path.path.len();
    if path.path.is_empty() {
        result.path = None;
        return;
    }
    path.source = path
        .path
        .first()
        .map(|h| h.from.clone())
        .unwrap_or_default();
    path.target = path.path.last().map(|h| h.to.clone()).unwrap_or_default();
}

/// Drop AMBIGUOUS then INFERRED edges, then non-seed leaf nodes, until under budget.
fn trim_to_budget(result: &mut QueryGraphResult, budget: usize) {
    if estimate_tokens(result) <= budget {
        return;
    }
    result.truncated = true;

    // 1) Drop AMBIGUOUS edges
    result.edges.retain(|e| e.confidence_label != "AMBIGUOUS");
    sync_path_with_edges(result);
    prune_orphan_nodes(result);
    if estimate_tokens(result) <= budget {
        return;
    }

    // 2) Drop INFERRED edges
    result.edges.retain(|e| e.confidence_label != "INFERRED");
    sync_path_with_edges(result);
    prune_orphan_nodes(result);
    if estimate_tokens(result) <= budget {
        return;
    }

    // 3) Drop non-seed nodes that are least connected
    loop {
        if estimate_tokens(result) <= budget {
            break;
        }
        let mut degrees: HashMap<String, usize> = HashMap::new();
        for e in &result.edges {
            *degrees.entry(e.from.clone()).or_default() += 1;
            *degrees.entry(e.to.clone()).or_default() += 1;
        }
        let victim = result
            .nodes
            .iter()
            .filter(|n| !n.is_seed)
            .min_by_key(|n| degrees.get(&n.qualified_name).copied().unwrap_or(0))
            .map(|n| n.qualified_name.clone());
        let Some(qn) = victim else {
            // Path summary can dominate the JSON budget even when edges are gone.
            if result.path.is_some() {
                result.path = None;
                continue;
            }
            // Last resort: truncate edges from the end.
            if result.edges.is_empty() {
                break;
            }
            result.edges.pop();
            sync_path_with_edges(result);
            prune_orphan_nodes(result);
            continue;
        };
        result.nodes.retain(|n| n.qualified_name != qn);
        result.edges.retain(|e| e.from != qn && e.to != qn);
        sync_path_with_edges(result);
    }
}

fn prune_orphan_nodes(result: &mut QueryGraphResult) {
    let mut keep: HashSet<String> = result.seeds.iter().cloned().collect();
    for e in &result.edges {
        keep.insert(e.from.clone());
        keep.insert(e.to.clone());
    }
    for n in &result.nodes {
        if n.is_seed {
            keep.insert(n.qualified_name.clone());
        }
    }
    result.nodes.retain(|n| keep.contains(&n.qualified_name));
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::{CodeElement, Relationship};
    use crate::db::schema::init_db;
    use tempfile::TempDir;

    fn make_engine() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("test.db")).unwrap();
        (GraphEngine::new(db), tmp)
    }

    fn insert_fn(engine: &GraphEngine, file: &str, name: &str) {
        let elem = CodeElement {
            qualified_name: format!("{}::{}", file, name),
            element_type: "function".into(),
            name: name.into(),
            file_path: file.into(),
            line_start: 1,
            line_end: 10,
            language: "rust".into(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    fn insert_rel(engine: &GraphEngine, src: &str, tgt: &str, method: &str, confidence: f64) {
        let rel = Relationship {
            source_qualified: src.into(),
            target_qualified: tgt.into(),
            rel_type: "calls".into(),
            confidence,
            metadata: serde_json::json!({"resolution_method": method}),
            ..Default::default()
        };
        engine.insert_relationship(&rel).unwrap();
    }

    #[test]
    fn extract_keywords_drops_stop_words() {
        let kws = extract_keywords("what connects auth to the database?");
        assert!(kws.contains(&"auth".to_string()));
        assert!(kws.contains(&"database".to_string()));
        assert!(!kws.contains(&"what".to_string()));
        assert!(!kws.contains(&"the".to_string()));
    }

    #[test]
    fn extract_connect_pair_from_connects_phrase() {
        let pair = extract_connect_pair("what connects auth to the database?");
        assert_eq!(pair, Some(("auth".into(), "database".into())));
    }

    #[test]
    fn query_graph_returns_path_with_provenance() {
        let (engine, _tmp) = make_engine();
        insert_fn(&engine, "src/auth.rs", "authenticate");
        insert_fn(&engine, "src/auth.rs", "login");
        insert_fn(&engine, "src/db.rs", "query_db");
        insert_rel(
            &engine,
            "src/auth.rs::authenticate",
            "src/auth.rs::login",
            "name",
            0.9,
        );
        insert_rel(
            &engine,
            "src/auth.rs::login",
            "src/db.rs::query_db",
            "typed",
            0.95,
        );

        let result = engine
            .query_graph("what connects auth to the database?", Some(4000), Some(3))
            .unwrap();

        assert!(!result.seeds.is_empty(), "expected seeds for auth/db");
        assert!(
            !result.edges.is_empty(),
            "expected connecting edges: {result:?}"
        );
        assert!(
            result
                .edges
                .iter()
                .any(|e| e.confidence_label == "EXTRACTED"),
            "typed/name high-conf edges should be EXTRACTED: {:?}",
            result.edges
        );
        assert!(result.tokens_estimate > 0);
        assert!(result.tokens_estimate <= result.token_budget || result.truncated);
    }

    #[test]
    fn query_graph_respects_token_budget() {
        let (engine, _tmp) = make_engine();
        insert_fn(&engine, "src/a.rs", "alpha");
        insert_fn(&engine, "src/b.rs", "beta");
        insert_fn(&engine, "src/c.rs", "gamma");
        insert_fn(&engine, "src/d.rs", "delta");
        insert_rel(&engine, "src/a.rs::alpha", "src/b.rs::beta", "name", 0.9);
        insert_rel(
            &engine,
            "src/b.rs::beta",
            "src/c.rs::gamma",
            "name_file_hint",
            0.55,
        );
        insert_rel(
            &engine,
            "src/c.rs::gamma",
            "src/d.rs::delta",
            "unresolved",
            0.2,
        );

        let result = engine
            .query_graph("what connects alpha to delta?", Some(200), Some(4))
            .unwrap();
        assert!(
            result.tokens_estimate <= result.token_budget,
            "tokens {} > budget {}",
            result.tokens_estimate,
            result.token_budget
        );
    }

    #[test]
    fn query_graph_empty_question_errors() {
        let (engine, _tmp) = make_engine();
        let err = engine.query_graph("   ", None, None).unwrap_err();
        assert!(err.to_string().contains("empty"));
    }

    #[test]
    fn query_graph_seeds_via_typed_name_search() {
        let (engine, _tmp) = make_engine();
        insert_fn(&engine, "src/auth.rs", "authenticate");
        insert_fn(&engine, "src/other.rs", "auth_helper");

        let result = engine
            .query_graph("authenticate", Some(4000), Some(1))
            .unwrap();
        assert!(
            result
                .seeds
                .contains(&"src/auth.rs::authenticate".to_string()),
            "expected typed seed for authenticate, got {:?}",
            result.seeds
        );
    }
}
