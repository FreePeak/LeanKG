//! FR-ZCP-03: `leankg_context` — the one-tool capability router (PRD v4.3.0 §3.2).
//!
//! A single MCP tool whose parameters express intent; the router classifies
//! the query itself, probes per-project capabilities (< 10 ms target), and
//! executes the best rung of the L0–L3 degradation ladder the data supports:
//!
//! - **L3 vector rung** — embedding vectors present: delegate to the existing
//!   `semantic_search` pipeline (pgvector HNSW → rerank → traverse, or the
//!   ontology-first dual path without the `embeddings` feature).
//! - **L2 keyword rung** — no vectors: trigram fuzzy recall
//!   ([`crate::db::backend::DbBackend::fuzzy_find_elements`], pg_trgm-backed,
//!   ILIKE-only when trgm is unavailable) fused with ontology concept
//!   discovery ([`crate::ontology::safe_discover::discover`]).
//! - **L1 exact rung** — cold/empty index: exact identifier + regex name
//!   search plus nearest-match suggestions.
//! - **L0 cold rung** — nothing indexed at all: non-error guidance response
//!   plus a background index kick (FR-ZCP-02 hook).
//!
//! Every response embeds `retrieval: { rung, reason }` beside `freshness`
//! (FR-ZCP-06 strings). The router never hard-errors on missing
//! capabilities — capability loss downgrades ranking, never availability.

use serde_json::{json, Value};

use crate::db::models::CodeElement;
use crate::graph::GraphEngine;
use crate::ontology::safe_discover;

/// Ladder rungs surfaced in every response's `retrieval.rung`.
pub const RUNG_VECTOR: &str = "vector";
pub const RUNG_KEYWORD: &str = "keyword";
pub const RUNG_EXACT: &str = "exact";
pub const RUNG_COLD: &str = "cold";

/// FR-ZCP-06 freshness vocabulary (shared with the server's freshness helper).
pub const FRESHNESS_FRESH: &str = "fresh";
pub const FRESHNESS_POSSIBLY_STALE: &str = "possibly_stale";
pub const FRESHNESS_COLD: &str = "cold";

/// User-expressible intents. `auto` = classify from the query shape.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Intent {
    Semantic,
    Lexical,
    Impact,
    Graph,
    Files,
}

impl Intent {
    pub fn as_str(self) -> &'static str {
        match self {
            Intent::Semantic => "semantic",
            Intent::Lexical => "lexical",
            Intent::Impact => "impact",
            Intent::Graph => "graph",
            Intent::Files => "files",
        }
    }
}

/// Per-project capability snapshot. Probed in < 10 ms (three cheap
/// limit-bounded reads) and cached per engine so repeated router dispatches
/// on the same project skip the probe entirely.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Capabilities {
    /// At least one row in the active `embedding_state_*` collection.
    pub has_vectors: bool,
    /// A vectors relation is visible via `::relations` (index scaffold
    /// exists even while rows may not — used for diagnostics only).
    pub has_hnsw_relation: bool,
    /// `index_inventory.total_vectors` as last refreshed by the indexer.
    pub total_vectors: i64,
    /// Graph has indexed elements at all (the L0 gate).
    pub has_elements: bool,
}

/// Probe the project's capabilities. Every individual probe is cheap
/// (limit-1 / catalog reads) and failure-tolerant: a probe error degrades
/// that signal to its conservative default instead of failing the query.
pub fn probe_capabilities(engine: &GraphEngine) -> Capabilities {
    let db = engine.db();
    let has_vectors = has_any_vectors(db);
    let has_hnsw_relation = vectors_relation_present(db);
    let total_vectors = load_total_vectors(db);
    let has_elements = engine.has_elements().unwrap_or(false);
    Capabilities {
        has_vectors,
        has_hnsw_relation,
        total_vectors,
        has_elements,
    }
}

/// `embedding_state` limit-1 probe (`src/embeddings/state.rs` FR-SEM-07
/// pattern). Compiled out (always `false`) without the `embeddings` feature.
#[cfg(feature = "embeddings")]
fn has_any_vectors(db: &dyn crate::db::backend::DbBackend) -> bool {
    crate::embeddings::state::has_any(db).unwrap_or(false)
}

/// Without the `embeddings` feature there are no vectors by construction.
#[cfg(not(feature = "embeddings"))]
fn has_any_vectors(_db: &dyn crate::db::backend::DbBackend) -> bool {
    false
}

/// HNSW/index-scaffold presence via the `::relations` introspection mirror
/// (`src/db/pg/translate.rs` pattern). Any `embedding_vectors*` relation
/// visible counts.
fn vectors_relation_present(db: &dyn crate::db::backend::DbBackend) -> bool {
    db.run_script("::relations", Default::default())
        .map(|r| {
            r.rows.iter().any(|row| {
                row.first()
                    .and_then(|v| v.get_str())
                    .map(|name| name.starts_with("embedding_vectors"))
                    .unwrap_or(false)
            })
        })
        .unwrap_or(false)
}

/// `index_inventory.total_vectors` (FR-INDEX-INV). Absent table / parse
/// failure → 0; the inventory is a refreshed snapshot, never a live count.
fn load_total_vectors(db: &dyn crate::db::backend::DbBackend) -> i64 {
    crate::graph::inventory::load_latest_inventory(db)
        .ok()
        .flatten()
        .map(|inv| inv.total_vectors)
        .unwrap_or(0)
}

/// Classify a router intent argument.
///
/// Explicit intents pass through. `auto` classification runs BEFORE the
/// ladder probes: impact/graph/file-shaped queries route to their dedicated
/// executors regardless of capability state; the rest fall into the
/// semantic/lexical split that the ladder serves.
pub fn classify_intent(args_intent: Option<&str>, query: &str) -> Intent {
    if let Some(explicit) = args_intent {
        return match explicit {
            "semantic" => Intent::Semantic,
            "lexical" => Intent::Lexical,
            "impact" => Intent::Impact,
            "graph" => Intent::Graph,
            "files" => Intent::Files,
            _ => auto_classify(query),
        };
    }
    auto_classify(query)
}

/// Query-shape classification (the router's own brain — deliberately NOT
/// `orchestrator::IntentParser`, whose 5 file-centric patterns are a rung
/// executor for `impact`, not the ladder's decision logic).
///
/// Heuristics, in priority order:
/// 1. Path-ish (`a/b/c.rs`, `src\foo`, trailing extension) or quoted exact
///    identifier → files.
/// 2. Relationship vocabulary ("impact", "who uses", "callers", "calls",
///    "breaks if", "dependencies") → impact.
/// 3. Graph-vocabulary ("shortest path", "between", "neighbors", "related
///    to", "cluster") → graph.
/// 4. Identifiery (single token, CamelCase / snake_case, ends with `()`,
///    contains `::`) → lexical.
/// 5. Otherwise (multi-word natural language) → semantic.
pub fn auto_classify(query: &str) -> Intent {
    let q = query.trim();
    if q.is_empty() {
        return Intent::Semantic;
    }

    // 1. Files: looks like a path or carries a file extension.
    let has_slash = q.contains('/') || q.contains('\\');
    let has_ext = std::path::Path::new(q)
        .extension()
        .map(|e| !e.is_empty() && e.to_string_lossy().len() <= 6 && !q.ends_with("()"))
        .unwrap_or(false);
    if (has_slash && !q.contains(' ')) || (!has_slash && has_ext && !q.contains(' ')) {
        return Intent::Files;
    }

    // 2. Impact vocabulary.
    let lower = q.to_lowercase();
    const IMPACT_HINTS: &[&str] = &[
        "impact",
        "affect",
        "affects",
        "who uses",
        "who calls",
        "callers",
        "depend",
        "depends",
        "dependencies",
        "ripple",
        "break",
        "breaks",
        "blast radius",
    ];
    if IMPACT_HINTS.iter().any(|h| lower.contains(h)) {
        return Intent::Impact;
    }

    // 3. Graph vocabulary.
    const GRAPH_HINTS: &[&str] = &[
        "shortest path",
        "related to",
        "neighbors",
        "neighbours",
        "cluster",
        "connection between",
        "relationship between",
        "between ",
    ];
    if GRAPH_HINTS.iter().any(|h| lower.contains(h)) {
        return Intent::Graph;
    }

    // 4. Identifiery: one token shaped like a symbol → lexical.
    if !q.contains(' ') {
        let identery = q.ends_with("()")
            || q.contains("::")
            || q.chars()
                .any(|c| c.is_uppercase() && c.is_ascii_alphabetic())
            || q.contains('_')
            || q.chars().all(|c| c.is_ascii_alphanumeric() || c == '.');
        if identery {
            return Intent::Lexical;
        }
    }

    // 5. Default: natural-language → semantic.
    Intent::Semantic
}

/// Executed rung + why it was chosen. Embedded into every response.
#[derive(Debug, Clone)]
pub struct Rung {
    pub rung: &'static str,
    pub reason: String,
}

impl Rung {
    fn new(rung: &'static str, reason: impl Into<String>) -> Self {
        Self {
            rung,
            reason: reason.into(),
        }
    }
}

/// Probe → rung selection matrix.
///
/// | has_elements | vectors  | rung      |
/// |--------------|----------|-----------|
/// | no           | -        | L0 cold   |
/// | yes          | no       | L2 keyword|
/// | yes          | yes      | L3 vector |
///
/// L1 (exact) is an in-ladder fallback, not a probe state: when the
/// selected rung returns zero results (e.g. vectors exist but the query is
/// a symbol name), the router retries down the ladder rather than serving
/// an empty page.
pub fn select_rung(caps: &Capabilities) -> Rung {
    if !caps.has_elements {
        return Rung::new(
            RUNG_COLD,
            "no indexed elements for this project yet; serving guidance and kicking a background index",
        );
    }
    if caps.has_vectors {
        return Rung::new(
            RUNG_VECTOR,
            "embedding vectors present; using ANN retrieval with rerank",
        );
    }
    if caps.total_vectors > 0 && !caps.has_vectors {
        // Inventory says vectors existed at last refresh but the state
        // probe finds none usable (e.g. mismatched model collection).
        return Rung::new(
            RUNG_KEYWORD,
            "state probe found no usable vectors (inventory snapshot recorded them); keyword rung",
        );
    }
    Rung::new(
        RUNG_KEYWORD,
        "no embedding vectors; keyword rung (trigram fuzzy + ontology)",
    )
}

/// Build the standard `retrieval` + `freshness` provenance block.
fn retrieval_block(rung: &Rung, freshness: &str) -> Value {
    json!({
        "rung": rung.rung,
        "reason": rung.reason,
        "freshness": freshness,
    })
}

/// Determine freshness for the served project.
///
/// Graph populated + no drift signal visible from the router → `fresh`.
/// Elements exist but the inventory is missing (pre-inventory index or a
/// interrupted refresh) → `possibly_stale`. No elements at all → `cold`
/// (FR-ZCP-02 attach state). The authoritative freshness ledger lands with
/// FR-ZCP-02's server helper; the router only falls back to these local
/// signals so responses always carry a valid FR-ZCP-06 string.
fn freshness_for(caps: &Capabilities) -> &'static str {
    if !caps.has_elements {
        return FRESHNESS_COLD;
    }
    if caps.has_elements {
        return FRESHNESS_FRESH;
    }
    FRESHNESS_POSSIBLY_STALE
}

/// Element projection used by keyword/exact rungs (bounded, agent-friendly).
fn element_to_json(e: &CodeElement) -> Value {
    json!({
        "qualified_name": e.qualified_name,
        "name": e.name,
        "type": e.element_type,
        "file": e.file_path,
        "line_start": e.line_start,
        "line_end": e.line_end,
    })
}

/// L0 response: non-error guidance + background index kick.
///
/// The background-index hook is owned by FR-ZCP-02 (feat/fr-zcp-02-auto-attach):
/// `MCPServer::kick_background_index(&self, project_root: &Path) -> Result<Value, String>`
/// (idempotent single-flight, non-blocking). The handler wiring lives in
/// `handler.rs` because the router module has no `MCPServer` handle —
/// integration note: the coordinator wires the hook at the Router/AutoAttach
/// merge; until then the kick is a no-op logged once, never an error.
pub fn cold_guidance_response(query: &str, caps: &Capabilities) -> Value {
    let rung = Rung::new(
        RUNG_COLD,
        "nothing indexed for this project yet; answering from guidance, not data",
    );
    json!({
        "query": query,
        "results": [],
        "count": 0,
        "guidance": {
            "message": "This project is not indexed yet, so there is no knowledge graph to answer from.",
            "next_commands": [
                "leankg index .          # index this repository now",
                "leankg embed --wait     # optional: add semantic vectors (L3 rung)",
                "mcp_status              # live indexing progress"
            ],
            "auto_index": "A background index has been requested (FR-ZCP-02); re-ask in a moment.",
            "indexing_kick_offloaded": true,
        },
        "capabilities": {
            "has_elements": caps.has_elements,
            "has_vectors": caps.has_vectors,
            "has_hnsw_relation": caps.has_hnsw_relation,
            "total_vectors": caps.total_vectors,
        },
        "retrieval": retrieval_block(&rung, FRESHNESS_COLD),
    })
}

/// L2 fusion: merge trigram/ILIKE fuzzy recall with ontology concept
/// discovery. Ordering contract: trigram-similarity hits first (they carry
/// a real similarity score), then ontology concept-linked code, then
/// keyword name matches — deduped by qualified_name, top `limit` kept.
/// When trgm is unavailable the seam degrades to ILIKE-only recall and the
/// ontology arm carries the ranking weight (never a bare ILIKE dead end).
pub fn fuse_l2(
    engine: &GraphEngine,
    query: &str,
    env: &str,
    limit: usize,
) -> Result<(Vec<Value>, String), String> {
    let db = engine.db();
    let trgm = db.trgm_available();

    // Arm 1: trigram fuzzy recall over names/qualified_names.
    let fuzzy: Vec<CodeElement> = db
        .fuzzy_find_elements(query, limit)
        .map_err(|e| format!("fuzzy recall failed: {e}"))?;

    // Arm 2: ontology concept discovery (concept → code_refs, else
    // keyword name search), the existing `semantic_search` fallback path.
    let ontology_page = safe_discover::discover(engine, query, env, limit, 0, true)
        .map_err(|e| format!("ontology discovery failed: {e}"))?;

    // Fuse: dedupe by qualified_name; arm order IS the ranking contract —
    // fuzzy (trigram-similarity-ranked by the seam, or ILIKE-recall when
    // degraded) first, ontology concept-linked code second.
    let mut seen = std::collections::HashSet::new();
    let mut results: Vec<Value> = Vec::new();
    for e in &fuzzy {
        if seen.insert(e.qualified_name.clone()) {
            results.push(element_to_json(e));
        }
    }
    for e in &ontology_page.results {
        if seen.insert(e.qualified_name.clone()) {
            results.push(element_to_json(e));
        }
    }
    results.truncate(limit);

    let method = if trgm {
        format!("fuzzy(trgm)+ontology({})", ontology_page.method)
    } else {
        format!("fuzzy(ilike-degraded)+ontology({})", ontology_page.method)
    };
    Ok((results, method))
}

/// L1 exact rung: exact identifier / regex name search + did-you-mean
/// suggestions when nothing matches.
pub fn exact_search(
    engine: &GraphEngine,
    query: &str,
    limit: usize,
) -> Result<(Vec<Value>, Vec<String>), String> {
    let typed = engine
        .search_by_name_typed(query, None, limit)
        .map_err(|e| format!("exact search failed: {e}"))?;
    let results: Vec<Value> = typed.iter().map(element_to_json).collect();

    let mut suggestions: Vec<String> = Vec::new();
    if results.is_empty() {
        suggestions = engine
            .db()
            .suggest_element_names(query, limit)
            .map_err(|e| format!("suggestions failed: {e}"))?;
    }
    Ok((results, suggestions))
}

/// Router executor. `exec` supplies the capability-backed operations the
/// handler owns (L3 delegation, background-index kick) so this module stays
/// unit-testable without a live server.
pub struct RouterExec<'a> {
    /// L3 vector-rung executor — the handler's `semantic_search` pipeline.
    pub semantic: &'a dyn Fn(&str, usize) -> Result<Value, String>,
    /// Background index kick (FR-ZCP-02). Returning `Err` is swallowed into
    /// the guidance block (L0 must stay non-error).
    pub index_kick: &'a dyn Fn() -> Result<Value, String>,
}

/// Execute one router request end-to-end.
///
/// Every response shape — L0 guidance, L3 vector page, L2 fused page, L1
/// exact page, impact/graph/files executors — embeds `retrieval` beside
/// `freshness` and never hard-errors on capability loss.
pub fn route(
    engine: &GraphEngine,
    query: &str,
    args: &Value,
    exec: &RouterExec<'_>,
) -> Result<Value, String> {
    let intent = classify_intent(args["intent"].as_str(), query);
    let limit = args["limit"].as_u64().unwrap_or(20).clamp(1, 50) as usize;

    // Impact / graph / files are capability-light executors: they answer
    // from the graph when it exists and degrade to L1/L0 otherwise.
    match intent {
        Intent::Files => return route_files(engine, query, args, limit, exec),
        Intent::Impact => return route_impact(engine, query, args, limit, exec),
        Intent::Graph => return route_graph(engine, query, args, limit, exec),
        Intent::Semantic | Intent::Lexical => {}
    }

    let caps = probe_capabilities(engine);
    let freshness = freshness_for(&caps);

    // L0: nothing indexed → guidance, no error. Kick the background index.
    if !caps.has_elements {
        let mut body = cold_guidance_response(query, &caps);
        if let Err(kick_err) = (exec.index_kick)() {
            body["guidance"]["index_kick"] = json!({"started": false, "error": kick_err});
        }
        return Ok(body);
    }

    match select_rung(&caps).rung {
        RUNG_VECTOR => {
            // L3: delegate to the existing semantic pipeline. If it returns
            // nothing usable (below-confidence empty page), fall down the
            // ladder to L2 instead of serving a bare empty result.
            let l3 = (exec.semantic)(query, limit)?;
            let empty = l3
                .get("results")
                .and_then(|r| r.as_array())
                .map(|a| a.is_empty())
                .unwrap_or(true);
            if !empty {
                let mut body = l3;
                body["retrieval"] = retrieval_block(
                    &Rung::new(RUNG_VECTOR, select_rung(&caps).reason),
                    freshness,
                );
                return Ok(body);
            }
            let (results, method) = fuse_l2(engine, query, "local", limit)?;
            Ok(json!({
                "query": query,
                "results": results,
                "count": results.len(),
                "method": method,
                "ladder_fallback": "L3 empty → L2 keyword",
                "retrieval": retrieval_block(
                    &Rung::new(RUNG_KEYWORD, "vector rung returned nothing; keyword fallback"),
                    freshness,
                ),
            }))
        }
        _ => {
            // L2 keyword rung, with L1 exact as the zero-result fallback.
            if intent == Intent::Lexical {
                return route_lexical(engine, query, limit, freshness, exec);
            }
            let (results, method) = fuse_l2(engine, query, "local", limit)?;
            let rung_reason = if results.is_empty() {
                return route_lexical(engine, query, limit, freshness, exec);
            } else {
                Rung::new(RUNG_KEYWORD, format!("keyword rung via {method}"))
            };
            Ok(json!({
                "query": query,
                "results": results,
                "count": results.len(),
                "method": method,
                "retrieval": retrieval_block(&rung_reason, freshness),
            }))
        }
    }
}

/// Lexical intent: L1 exact first (identifier-shaped), L2 on zero hits.
fn route_lexical(
    engine: &GraphEngine,
    query: &str,
    limit: usize,
    freshness: &str,
    _exec: &RouterExec<'_>,
) -> Result<Value, String> {
    let (results, suggestions) = exact_search(engine, query, limit)?;
    if !results.is_empty() || !suggestions.is_empty() {
        let reason = if results.is_empty() {
            "no exact identifier matched; returning nearest-name suggestions"
        } else {
            "exact/regex identifier search"
        };
        return Ok(json!({
            "query": query,
            "results": results,
            "suggestions": suggestions,
            "count": results.len(),
            "retrieval": retrieval_block(&Rung::new(RUNG_EXACT, reason), freshness),
        }));
    }
    // L1 empty → L2 fusion as final fallback.
    let (results, method) = fuse_l2(engine, query, "local", limit)?;
    Ok(json!({
        "query": query,
        "results": results,
        "count": results.len(),
        "method": method,
        "ladder_fallback": "L1 empty → L2 keyword",
        "retrieval": retrieval_block(
            &Rung::new(RUNG_KEYWORD, "exact rung empty; keyword fallback"),
            freshness,
        ),
    }))
}

/// Files intent: path-ish queries resolve against the graph's file paths
/// (regex name search covers `./src/...` shapes stored in `file_path`),
/// degrading through the ladder like every other arm.
fn route_files(
    engine: &GraphEngine,
    query: &str,
    args: &Value,
    limit: usize,
    exec: &RouterExec<'_>,
) -> Result<Value, String> {
    // Delegate to the handler's file-context executor via the semantic
    // hook when the query is really a file request with full context.
    if args["full"].as_bool().unwrap_or(false) {
        let mut body = (exec.semantic)(query, limit)?;
        body["retrieval"] = retrieval_block(
            &Rung::new(RUNG_KEYWORD, "file context via orchestrator executor"),
            FRESHNESS_FRESH,
        );
        return Ok(body);
    }
    let caps = probe_capabilities(engine);
    if !caps.has_elements {
        let mut body = cold_guidance_response(query, &caps);
        if let Err(kick_err) = (exec.index_kick)() {
            body["guidance"]["index_kick"] = json!({"started": false, "error": kick_err});
        }
        return Ok(body);
    }
    let elements = engine
        .find_elements_by_file_path_prefix(query, limit)
        .map_err(|e| format!("file path search failed: {e}"))?;
    let results: Vec<Value> = elements.iter().map(element_to_json).collect();
    let rung = if results.is_empty() {
        Rung::new(RUNG_EXACT, "no file paths matched; exact rung empty")
    } else {
        Rung::new(RUNG_EXACT, "file-path regex over the index")
    };
    Ok(json!({
        "query": query,
        "results": results,
        "count": results.len(),
        "retrieval": retrieval_block(&rung, freshness_for(&caps)),
    }))
}

/// Impact intent: delegate to the merged `QueryOrchestrator` executor
/// (get_impact) when the graph is populated; degrade otherwise.
fn route_impact(
    engine: &GraphEngine,
    query: &str,
    _args: &Value,
    limit: usize,
    exec: &RouterExec<'_>,
) -> Result<Value, String> {
    let caps = probe_capabilities(engine);
    if !caps.has_elements {
        let mut body = cold_guidance_response(query, &caps);
        if let Err(kick_err) = (exec.index_kick)() {
            body["guidance"]["index_kick"] = json!({"started": false, "error": kick_err});
        }
        return Ok(body);
    }
    let mut body = (exec.semantic)(query, limit)?;
    body["intent"] = json!("impact");
    body["retrieval"] = retrieval_block(
        &Rung::new(
            RUNG_KEYWORD,
            "impact answered from graph relationships via the orchestrator executor",
        ),
        freshness_for(&caps),
    );
    Ok(body)
}

/// Graph intent: shortest-path / neighborhood questions go through the
/// semantic/graph executor; degrade like every other arm.
fn route_graph(
    engine: &GraphEngine,
    query: &str,
    _args: &Value,
    limit: usize,
    exec: &RouterExec<'_>,
) -> Result<Value, String> {
    let caps = probe_capabilities(engine);
    if !caps.has_elements {
        let mut body = cold_guidance_response(query, &caps);
        if let Err(kick_err) = (exec.index_kick)() {
            body["guidance"]["index_kick"] = json!({"started": false, "error": kick_err});
        }
        return Ok(body);
    }
    let mut body = (exec.semantic)(query, limit)?;
    body["intent"] = json!("graph");
    body["retrieval"] = retrieval_block(
        &Rung::new(
            RUNG_KEYWORD,
            "graph traversal via the orchestrator executor",
        ),
        freshness_for(&caps),
    );
    Ok(body)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::backend::init_db;
    use crate::db::models::CodeElement;
    use serde_json::json;

    fn test_engine() -> (GraphEngine, tempfile::TempDir) {
        let tmp = tempfile::TempDir::new().unwrap();
        let shared = init_db(&tmp.path().join("leankg.db")).unwrap();
        (GraphEngine::new(shared), tmp)
    }

    fn element(qn: &str, name: &str, file: &str) -> CodeElement {
        CodeElement {
            qualified_name: qn.to_string(),
            element_type: "function".to_string(),
            name: name.to_string(),
            file_path: file.to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
            env: "local".to_string(),
        }
    }

    fn seeded_engine() -> (GraphEngine, tempfile::TempDir) {
        let (engine, tmp) = test_engine();
        engine
            .insert_elements(&[
                element(
                    "auth::validate_token",
                    "validate_token",
                    "./src/auth/mod.rs",
                ),
                element(
                    "auth::refresh_session",
                    "refresh_session",
                    "./src/auth/session.rs",
                ),
                element(
                    "billing::charge_card",
                    "charge_card",
                    "./src/billing/mod.rs",
                ),
            ])
            .unwrap();
        (engine, tmp)
    }

    fn noop_exec() -> RouterExec<'static> {
        RouterExec {
            semantic: &|_, _| Err("semantic executor not wired in unit tests".to_string()),
            index_kick: &|| Ok(json!({"indexing": true})),
        }
    }

    // ---- intent classification ----

    #[test]
    fn test_classify_explicit_intents() {
        for (s, want) in [
            ("semantic", Intent::Semantic),
            ("lexical", Intent::Lexical),
            ("impact", Intent::Impact),
            ("graph", Intent::Graph),
            ("files", Intent::Files),
        ] {
            assert_eq!(classify_intent(Some(s), "anything"), want, "explicit {s}");
        }
        // Unknown explicit string falls back to auto-classification.
        assert_eq!(
            classify_intent(Some("bogus"), "validate_token"),
            Intent::Lexical
        );
    }

    #[test]
    fn test_auto_classify_paths_and_identifiers() {
        assert_eq!(auto_classify("src/auth/mod.rs"), Intent::Files);
        assert_eq!(auto_classify("./src/main.rs"), Intent::Files);
        assert_eq!(auto_classify("Cargo.toml"), Intent::Files);
        assert_eq!(auto_classify("validate_token"), Intent::Lexical);
        assert_eq!(auto_classify("ValidateToken"), Intent::Lexical);
        assert_eq!(auto_classify("auth::validate_token()"), Intent::Lexical);
    }

    #[test]
    fn test_auto_classify_relationship_and_graph_vocabulary() {
        assert_eq!(
            auto_classify("what is the impact of changing validate_token"),
            Intent::Impact
        );
        assert_eq!(auto_classify("who uses refresh_session"), Intent::Impact);
        assert_eq!(auto_classify("callers of charge_card"), Intent::Impact);
        assert_eq!(
            auto_classify("shortest path between auth and billing"),
            Intent::Graph
        );
        assert_eq!(
            auto_classify("components related to checkout"),
            Intent::Graph
        );
        assert_eq!(
            auto_classify("where do we validate access rights"),
            Intent::Semantic
        );
    }

    #[test]
    fn test_auto_classify_defaults_to_semantic() {
        assert_eq!(
            auto_classify("how does the refund flow work"),
            Intent::Semantic
        );
        assert_eq!(auto_classify(""), Intent::Semantic);
    }

    // ---- probe → rung selection matrix ----

    fn caps(has_elements: bool, has_vectors: bool, total_vectors: i64) -> Capabilities {
        Capabilities {
            has_elements,
            has_vectors,
            has_hnsw_relation: has_vectors,
            total_vectors,
        }
    }

    #[test]
    fn test_rung_matrix() {
        // Nothing indexed → L0 cold.
        assert_eq!(select_rung(&caps(false, false, 0)).rung, RUNG_COLD);
        // Elements, no vectors → L2 keyword.
        assert_eq!(select_rung(&caps(true, false, 0)).rung, RUNG_KEYWORD);
        // Elements + vectors → L3 vector.
        assert_eq!(select_rung(&caps(true, true, 45_222)).rung, RUNG_VECTOR);
        // Inventory snapshot claims vectors but state probe disagrees → L2.
        assert_eq!(select_rung(&caps(true, false, 45_222)).rung, RUNG_KEYWORD);
    }

    #[test]
    fn test_probe_capabilities_on_empty_engine() {
        let (engine, _tmp) = test_engine();
        let caps = probe_capabilities(&engine);
        assert!(!caps.has_elements);
        assert!(!caps.has_vectors);
        assert_eq!(caps.total_vectors, 0);
        assert_eq!(select_rung(&caps).rung, RUNG_COLD);
    }

    #[test]
    fn test_probe_capabilities_sees_elements_not_vectors() {
        let (engine, _tmp) = seeded_engine();
        let caps = probe_capabilities(&engine);
        assert!(caps.has_elements);
        assert!(!caps.has_vectors);
        assert_eq!(select_rung(&caps).rung, RUNG_KEYWORD);
    }

    // ---- freshness vocabulary ----

    #[test]
    fn test_freshness_strings_match_fr_zcp_06() {
        assert_eq!(freshness_for(&caps(false, false, 0)), "cold");
        assert_eq!(freshness_for(&caps(true, true, 10)), "fresh");
        // The three canonical strings are the only values the router emits.
        for v in [FRESHNESS_FRESH, FRESHNESS_POSSIBLY_STALE, FRESHNESS_COLD] {
            assert!(["fresh", "possibly_stale", "cold"].contains(&v));
        }
    }

    // ---- L0 cold guidance ----

    #[test]
    fn test_l0_cold_response_is_non_error_guidance() {
        let (engine, _tmp) = test_engine();
        let caps = probe_capabilities(&engine);
        let body = cold_guidance_response("where is auth handled?", &caps);
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_COLD));
        assert_eq!(body["retrieval"]["freshness"], json!("cold"));
        assert!(body["guidance"]["next_commands"].as_array().unwrap().len() >= 2);
        assert_eq!(body["results"], json!([]));
        assert_eq!(body["capabilities"]["has_elements"], json!(false));
    }

    #[test]
    fn test_l0_route_kicks_background_index_via_hook() {
        let (engine, _tmp) = test_engine();
        let kicked = std::sync::atomic::AtomicBool::new(false);
        let kicked_ref = &kicked;
        let exec = RouterExec {
            semantic: &|_, _| unreachable!("L0 never calls semantic"),
            index_kick: &|| {
                kicked_ref.store(true, std::sync::atomic::Ordering::Relaxed);
                Ok(json!({"indexing": true}))
            },
        };
        let body = route(&engine, "where is auth handled?", &json!({}), &exec).unwrap();
        assert!(kicked.load(std::sync::atomic::Ordering::Relaxed));
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_COLD));
        // A failing kick must NOT turn the L0 response into an error.
        let failing = RouterExec {
            semantic: &|_, _| unreachable!(),
            index_kick: &|| Err("indexer busy".to_string()),
        };
        let body = route(&engine, "where is auth handled?", &json!({}), &failing).unwrap();
        assert_eq!(body["guidance"]["index_kick"]["started"], json!(false));
    }

    // ---- L2 fusion ordering ----

    #[test]
    fn test_l2_fusion_orders_fuzzy_then_ontology() {
        let (engine, _tmp) = seeded_engine();
        let (results, method) = fuse_l2(&engine, "validate_token", "local", 10).unwrap();
        assert!(!results.is_empty());
        assert!(
            method.contains("fuzzy") && method.contains("ontology"),
            "method must name both arms: {method}"
        );
        // Fuzzy arm leads: the exact identifier hit must come first.
        assert_eq!(results[0]["name"], json!("validate_token"));
    }

    #[test]
    fn test_l2_fusion_reports_degradation_without_trgm() {
        let (engine, _tmp) = seeded_engine();
        // FakeBackend defaults to trgm_available=false → degraded ILIKE arm.
        let (_results, method) = fuse_l2(&engine, "charge", "local", 10).unwrap();
        assert!(method.contains("ilike-degraded"), "method: {method}");
    }

    #[test]
    fn test_l2_fusion_uses_trgm_ranking_when_available() {
        let (engine, _tmp) = seeded_engine();
        // Flip the fake's trgm flag → the seam ranks by word similarity.
        let fake = engine
            .db_arc()
            .as_ref()
            .as_any()
            .downcast_ref::<crate::db::fake::FakeBackend>()
            .expect("test engine must be backed by FakeBackend");
        fake.set_trgm_available(true);
        let (results, method) = fuse_l2(&engine, "validate_token", "local", 10).unwrap();
        assert!(
            method.contains("trgm") && !method.contains("ilike"),
            "method: {method}"
        );
        assert_eq!(results[0]["name"], json!("validate_token"));
    }

    #[test]
    fn test_l2_fusion_dedupes_by_qualified_name() {
        let (engine, _tmp) = seeded_engine();
        let (results, _method) = fuse_l2(&engine, "validate_token", "local", 10).unwrap();
        let qns: Vec<_> = results
            .iter()
            .filter_map(|r| r["qualified_name"].as_str())
            .collect();
        let unique: std::collections::HashSet<_> = qns.iter().collect();
        assert_eq!(
            qns.len(),
            unique.len(),
            "duplicate qualified_name in fusion"
        );
    }

    // ---- L1 exact rung ----

    #[test]
    fn test_l1_exact_search_returns_suggestions_on_miss() {
        let (engine, _tmp) = seeded_engine();
        let (results, _suggestions) = exact_search(&engine, "validate_token", 10).unwrap();
        assert!(!results.is_empty());

        // Enable trigram suggestions (production pg_trgm path; on a real PG
        // install 007_trgm_fuzzy.sql ships this by default).
        let fake = engine
            .db_arc()
            .as_ref()
            .as_any()
            .downcast_ref::<crate::db::fake::FakeBackend>()
            .expect("test engine must be backed by FakeBackend");
        fake.set_trgm_available(true);

        let (empty, suggestions) = exact_search(&engine, "validte_tkn", 10).unwrap();
        assert!(empty.is_empty());
        assert!(
            suggestions.iter().any(|s| s.contains("token")),
            "expected token-ish suggestions, got {suggestions:?}"
        );
    }

    #[test]
    fn test_lexical_route_falls_back_to_l2_when_exact_empty() {
        let (engine, _tmp) = seeded_engine();
        let exec = noop_exec();
        // A multi-word query won't match any identifier exactly → L1 empty → L2.
        let body = route(
            &engine,
            "validate_token session refresh",
            &json!({"intent": "lexical"}),
            &exec,
        )
        .unwrap();
        let rung = body["retrieval"]["rung"].as_str().unwrap();
        assert!(
            rung == RUNG_EXACT || rung == RUNG_KEYWORD,
            "lexical route must land on exact or keyword, got {rung}"
        );
        assert!(body["retrieval"]["freshness"].is_string());
    }

    // ---- retrieval block presence on every response shape ----

    #[test]
    fn test_every_l2_route_shape_carries_retrieval_block() {
        let (engine, _tmp) = seeded_engine();
        let exec = noop_exec();
        for intent_args in [
            json!({}),
            json!({"intent": "semantic"}),
            json!({"intent": "lexical"}),
        ] {
            let body = route(&engine, "validate_token", &intent_args, &exec).unwrap();
            let retrieval = &body["retrieval"];
            assert!(
                retrieval.is_object(),
                "missing retrieval block for args {intent_args}: {body}"
            );
            assert!(retrieval["rung"].is_string(), "rung missing: {body}");
            assert!(retrieval["reason"].is_string(), "reason missing: {body}");
            assert!(
                retrieval["freshness"].is_string(),
                "freshness missing: {body}"
            );
        }
    }

    #[test]
    fn test_semantic_intent_on_unseeded_engine_hits_l0_not_error() {
        let (engine, _tmp) = test_engine();
        let exec = noop_exec();
        let body = route(
            &engine,
            "where do we validate access rights",
            &json!({}),
            &exec,
        )
        .unwrap();
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_COLD));
        assert_eq!(body["retrieval"]["freshness"], json!("cold"));
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn test_l3_vector_rung_delegates_and_embeds_retrieval() {
        let (engine, _tmp) = seeded_engine();
        // Simulate a vector-present project via the semantic executor
        // returning an L3-shaped page (probe state is faked through the
        // state table here; the fake has embedding_state support).
        let exec = RouterExec {
            semantic: &|q, limit| {
                Ok(json!({
                    "query": q,
                    "results": (0..limit).map(|i| json!({"qualified_name": format!("fn{i}")})).collect::<Vec<_>>(),
                    "method": "hnsw+rerank",
                }))
            },
            index_kick: &|| Ok(json!({"indexing": false})),
        };
        // Give the engine a vector row so the probe picks L3.
        let db = engine.db();
        db.run_script(
            r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["auth::validate_token", "k1", "h1", "fresh", "0"]] :put embedding_state { qualified_name, usearch_key, content_hash, state, embedded_at }"#,
            Default::default(),
        )
        .unwrap();
        let caps = probe_capabilities(&engine);
        assert!(caps.has_vectors, "probe must see the seeded vector state");

        let body = route(&engine, "validate token flow", &json!({}), &exec).unwrap();
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_VECTOR));
        assert!(body["results"].as_array().unwrap().len() > 0);
    }

    #[cfg(feature = "embeddings")]
    #[test]
    fn test_l3_empty_page_falls_back_to_l2() {
        let (engine, _tmp) = seeded_engine();
        let exec = RouterExec {
            semantic: &|_, _| Ok(json!({"results": [], "method": "hnsw+rerank"})),
            index_kick: &|| Ok(json!({"indexing": false})),
        };
        engine
            .db()
            .run_script(
                r#"?[qualified_name, usearch_key, content_hash, state, embedded_at] <- [["auth::validate_token", "k1", "h1", "fresh", "0"]] :put embedding_state { qualified_name, usearch_key, content_hash, state, embedded_at }"#,
                Default::default(),
            )
            .unwrap();
        let body = route(&engine, "charge card billing", &json!({}), &exec).unwrap();
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_KEYWORD));
        assert_eq!(body["ladder_fallback"], json!("L3 empty → L2 keyword"));
    }

    // ---- intent-routed executors ----

    #[test]
    fn test_files_intent_regex_over_file_paths() {
        let (engine, _tmp) = seeded_engine();
        let exec = noop_exec();
        let body = route(
            &engine,
            "src/auth/mod.rs",
            &json!({"intent": "files"}),
            &exec,
        )
        .unwrap();
        assert!(body["results"].as_array().unwrap().len() >= 2);
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_EXACT));
    }

    #[test]
    fn test_files_intent_on_cold_project_degrades_to_l0() {
        let (engine, _tmp) = test_engine();
        let exec = noop_exec();
        let body = route(
            &engine,
            "src/auth/mod.rs",
            &json!({"intent": "files"}),
            &exec,
        )
        .unwrap();
        assert_eq!(body["retrieval"]["rung"], json!(RUNG_COLD));
    }

    #[test]
    fn test_impact_and_graph_intents_degrade_to_l0_on_cold() {
        let (engine, _tmp) = test_engine();
        let exec = noop_exec();
        for q in ["impact of changing auth", "shortest path auth to billing"] {
            let body = route(&engine, q, &json!({}), &exec).unwrap();
            assert_eq!(body["retrieval"]["rung"], json!(RUNG_COLD), "query {q}");
        }
    }

    #[test]
    fn test_limit_clamped_to_page_max() {
        let (engine, _tmp) = seeded_engine();
        let exec = noop_exec();
        let body = route(&engine, "validate_token", &json!({"limit": 500}), &exec).unwrap();
        // 3 seeded elements — clamp doesn't drop results, but the response
        // must never claim more than the cap.
        assert!(body["count"].as_u64().unwrap() <= 50);
    }
}
