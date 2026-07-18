//! Per-node-type and path candidate filtering for the retrieval pipeline.
//!
//! Generalizes the test-name filter: certain ontology node types are
//! structural / grouping abstractions whose value comes from traversal
//! rather than from being seeds themselves. Policy has three tiers for
//! **types**, plus path-based drops for indexer / UI noise (FR-SEM-06):
//!
//! 1. **ALWAYS_INCLUDE** — kept unconditionally (high-signal types).
//! 2. **QUERY_GATED_DROPS** — dropped unless the query mentions a trigger
//!    word that signals intent for that type.
//! 3. **ALWAYS_DROP** — never useful as seeds (pure metadata or indexer
//!    noise).
//! 4. **ALWAYS_DROP_PATH_MARKERS** — file paths that are never useful seeds
//!    (minified UI bundles under `embed/assets/`).
//! 5. **QUERY_GATED_PATHS** — path prefixes dropped unless the query
//!    mentions a trigger (e.g. `src/benchmark/` unless query says
//!    "benchmark").
//!
//! Any type not in the three lists is kept by default (permissive), so
//! adding a new ontology type in the indexer doesn't silently disappear
//! from retrieval until a deliberate policy decision is made.

use std::collections::HashSet;

/// Code types + first-class domain concepts + top-level narrative docs.
/// Kept unconditionally — these are the things users typically ask about.
pub const ALWAYS_INCLUDE_TYPES: &[&str] = &[
    // Code
    "file",
    "function",
    "class",
    "module",
    "method",
    "trait",
    "interface",
    // First-class domain concepts
    "domain_entity",
    "service",
    "api_endpoint",
    "data_store",
    // Top-level docs / knowledge
    "workflow",
    "playbook",
    "team_knowledge",
    "known_issue",
];

/// Dropped unless the query contains one of the trigger words for that type.
/// Trigger match is case-insensitive substring.
pub const QUERY_GATED_DROPS: &[(&str, &[&str])] = &[
    ("workflow_step", &["step", "workflow step"]),
    ("playbook_step", &["step", "playbook step"]),
    ("decision_point", &["decision", "decision point"]),
    ("failure_mode", &["failure", "fail", "error", "issue"]),
];

/// Dropped unconditionally. `environment` is pure metadata; `unknown` is
/// indexer noise from chain-call extraction (e.g. `iter`, `Ok`, `unwrap_or`
/// — see analysis of Q1 traversal output, 2026-07-04).
pub const ALWAYS_DROP_TYPES: &[&str] = &["environment", "unknown"];

/// Path substrings (forward-slash normalized) that are never useful as
/// semantic seeds. Evidence: Probe G in
/// `docs/semantic-search-mcp-verification-2026-07-18.md` — HNSW queries
/// surfaced minified single-letter symbols from the embedded UI bundle.
pub const ALWAYS_DROP_PATH_MARKERS: &[&str] = &["embed/assets/"];

/// Path prefixes dropped unless the query contains a trigger word.
/// Evidence: Probe H — `src/benchmark/**::verdict` crowded out real
/// vector-scoring / retrieval symbols for "vector similarity scoring".
pub const QUERY_GATED_PATHS: &[(&str, &[&str])] = &[("src/benchmark/", &["benchmark"])];

pub struct FilterPolicy {
    include: HashSet<&'static str>,
    drop: HashSet<&'static str>,
    gated: Vec<(&'static str, &'static [&'static str])>,
}

impl FilterPolicy {
    pub fn new() -> Self {
        Self {
            include: ALWAYS_INCLUDE_TYPES.iter().copied().collect(),
            drop: ALWAYS_DROP_TYPES.iter().copied().collect(),
            gated: QUERY_GATED_DROPS.iter().copied().collect(),
        }
    }

    /// Returns true if the element should be filtered out (type-only).
    ///
    /// Prefer [`Self::should_drop_candidate`] so path noise is also removed.
    ///
    /// `query_lower` is the lowercased query — callers pre-compute once
    /// per retrieve call rather than per candidate.
    pub fn should_drop(&self, element_type: &str, query_lower: &str) -> bool {
        if self.drop.contains(element_type) {
            return true;
        }
        if self.include.contains(element_type) {
            return false;
        }
        for (t, triggers) in &self.gated {
            if *t == element_type {
                return !triggers.iter().any(|trigger| query_lower.contains(trigger));
            }
        }
        // Unknown type — be permissive, keep it. Adding a new ontology
        // type shouldn't silently break retrieval.
        false
    }

    /// Type + path filtering (FR-SEM-06).
    pub fn should_drop_candidate(
        &self,
        element_type: &str,
        file_path: &str,
        query_lower: &str,
    ) -> bool {
        if self.should_drop_path(file_path, query_lower) {
            return true;
        }
        self.should_drop(element_type, query_lower)
    }

    /// Path-only rules (always-drop markers + query-gated prefixes).
    pub fn should_drop_path(&self, file_path: &str, query_lower: &str) -> bool {
        let path = file_path.replace('\\', "/");
        for marker in ALWAYS_DROP_PATH_MARKERS {
            if path.contains(marker) {
                return true;
            }
        }
        for (prefix, triggers) in QUERY_GATED_PATHS {
            if path.contains(prefix) {
                return !triggers.iter().any(|trigger| query_lower.contains(trigger));
            }
        }
        false
    }
}

impl Default for FilterPolicy {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn always_include_types_kept() {
        let p = FilterPolicy::new();
        for t in ALWAYS_INCLUDE_TYPES {
            assert!(!p.should_drop(t, "anything"), "{t} should be kept");
        }
    }

    #[test]
    fn always_drop_types_filtered() {
        let p = FilterPolicy::new();
        for t in ALWAYS_DROP_TYPES {
            assert!(p.should_drop(t, "anything"), "{t} should be dropped");
        }
    }

    #[test]
    fn workflow_step_gated_by_step_keyword() {
        let p = FilterPolicy::new();
        assert!(!p.should_drop("workflow_step", "list all steps for checkout"));
        assert!(!p.should_drop("workflow_step", "show me the workflow step"));
        assert!(p.should_drop("workflow_step", "show me the workflow"));
    }

    #[test]
    fn failure_mode_gated_by_failure_or_error_keywords() {
        let p = FilterPolicy::new();
        assert!(!p.should_drop("failure_mode", "what failures can occur"));
        assert!(!p.should_drop("failure_mode", "common errors in payment"));
        assert!(p.should_drop("failure_mode", "show me the service"));
    }

    #[test]
    fn decision_point_dropped_by_default() {
        let p = FilterPolicy::new();
        assert!(p.should_drop("decision_point", "show me things"));
        assert!(!p.should_drop("decision_point", "what decisions are made"));
    }

    #[test]
    fn unknown_type_kept_permissive() {
        let p = FilterPolicy::new();
        assert!(!p.should_drop("weird_new_type", "anything"));
    }

    #[test]
    fn trigger_match_uses_caller_lowercased_query() {
        // Contract: caller passes an already-lowercased query. The policy
        // itself does no case-folding. pipeline.rs::retrieve does the
        // lowercase once per call.
        let p = FilterPolicy::new();
        assert!(!p.should_drop("workflow_step", "show me the workflow step now"));
        // Same query in mixed case wouldn't match — that's intentional,
        // callers must lowercase.
        assert!(p.should_drop("workflow_step", "show me the workflow STEP now"));
    }

    #[test]
    fn embed_assets_js_always_dropped() {
        let p = FilterPolicy::new();
        assert!(p.should_drop_path(
            "src/embed/assets/index-COca5qD2.js",
            "hnsw approximate nearest neighbor"
        ));
        assert!(p.should_drop_candidate(
            "function",
            "src/embed/assets/index-COca5qD2.js",
            "hnsw approximate nearest neighbor"
        ));
    }

    #[test]
    fn benchmark_path_dropped_unless_query_mentions_benchmark() {
        let p = FilterPolicy::new();
        assert!(p.should_drop_path("src/benchmark/summary.rs", "vector similarity scoring"));
        assert!(!p.should_drop_path("src/benchmark/summary.rs", "run the benchmark suite"));
        assert!(!p.should_drop_candidate(
            "function",
            "src/retrieval/pipeline.rs",
            "vector similarity scoring"
        ));
    }

    #[test]
    fn normal_source_paths_kept() {
        let p = FilterPolicy::new();
        assert!(!p.should_drop_path("src/embeddings/build.rs", "anything"));
        assert!(!p.should_drop_path("src/mcp/handler.rs", "mcp json-rpc"));
    }
}
