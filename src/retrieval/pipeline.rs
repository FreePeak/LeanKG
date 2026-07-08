//! Retrieval pipeline orchestration: query → embed → HNSW ANN → worktree/env
//! filter → cross-encoder rerank. Returns a `RetrievalResult` ready for the
//! MCP handler to hand off to the traversal stage.

use crate::db::models::CodeElement;
use crate::db::schema::{run_script, CozoDb};
use crate::embeddings::models::{Embedder, RerankerStatus};
use crate::retrieval::rerank::RerankStage;
use cozo::DataValue;
use std::collections::{HashMap, HashSet};

pub struct SemanticRetrievalPipeline {
    embedder: Embedder,
    rerank_stage: RerankStage,
    db: CozoDb,
}

#[derive(Debug, Clone)]
pub struct Seed {
    pub qualified_name: String,
    /// Legacy field preserved for API compat — HNSW keys on qualified_name
    /// directly so there is no separate numeric key.
    pub usearch_key: u64,
    /// Raw HNSW cosine distance from `~embedding_vectors:vec_idx`.
    pub ann_distance: f32,
    /// Set by the cross-encoder. None when the pipeline ran in ANN-only
    /// fallback mode.
    pub rerank_score: Option<f32>,
    pub element_type: String,
    pub file_path: String,
    pub env: String,
    /// Short text-blob excerpt used for rerank; included in diagnostics so
    /// agents can see *why* a seed matched.
    pub blob_excerpt: String,
}

#[derive(Debug, Clone)]
pub struct RetrievalResult {
    pub seeds: Vec<Seed>,
    pub reranker_status: RerankerStatus,
    pub ann_candidate_count: usize,
    pub worktree_filtered_count: usize,
    pub env_filtered_count: usize,
    pub test_filtered_count: usize,
    pub node_type_filtered_count: usize,
    /// Number of embedded vectors in the index at query time.
    pub index_size: usize,
    /// Effective k used for this query (may differ from opts.ann_top_k when
    /// adaptive mode is on).
    pub ann_top_k_used: usize,
    pub embeddings_stale: bool,
}

#[derive(Debug, Clone)]
pub struct RetrieveOptions {
    /// Restrict results to a single env ("local" / "staging" / "production").
    /// None disables env filtering.
    pub env: Option<String>,
    /// ANN depth. None = adaptive based on index size (default). Some(n) =
    /// explicit override.
    pub ann_top_k: Option<usize>,
    /// Final seed count after rerank. Default 10.
    pub rerank_top_n: usize,
    /// Q2 default-on worktree filter. Set true to include worktree copies.
    pub include_worktrees: bool,
    /// Per-node-type candidate filter bypass. Set true to keep
    /// workflow_step / playbook_step / decision_point / failure_mode
    /// even when the query doesn't mention them.
    pub include_ontology_steps: bool,
    /// Surface a stale-embeddings warning in diagnostics.
    pub embeddings_stale: bool,
}

impl Default for RetrieveOptions {
    fn default() -> Self {
        Self {
            env: Some("local".to_string()),
            ann_top_k: None,
            rerank_top_n: 10,
            include_worktrees: false,
            include_ontology_steps: false,
            embeddings_stale: false,
        }
    }
}

/// Adaptive ANN depth based on embedded-vector count. Keeps the candidate
/// sample ratio reasonable as the index grows without paying unbounded
/// rerank cost. Conservative — rerank is O(k) on CPU.
///
///   index_size ≤ 10k    → 50   (small repos, current default)
///   index_size ≤ 100k   → 100
///   index_size ≤ 500k   → 150
///   index_size ≤ 1M     → 200
///   index_size > 1M     → 300  (cap; bounds rerank to ~900ms)
pub fn adaptive_k(index_size: usize) -> usize {
    match index_size {
        0..=10_000 => 50,
        10_001..=100_000 => 100,
        100_001..=500_000 => 150,
        500_001..=1_000_000 => 200,
        _ => 300,
    }
}

#[cfg(test)]
mod adaptive_k_tests {
    use super::*;

    #[test]
    fn small_index_uses_baseline_50() {
        assert_eq!(adaptive_k(0), 50);
        assert_eq!(adaptive_k(2_780), 50);
        assert_eq!(adaptive_k(10_000), 50);
    }

    #[test]
    fn mid_index_uses_100() {
        assert_eq!(adaptive_k(10_001), 100);
        assert_eq!(adaptive_k(50_000), 100);
        assert_eq!(adaptive_k(100_000), 100);
    }

    #[test]
    fn large_index_uses_150() {
        assert_eq!(adaptive_k(100_001), 150);
        assert_eq!(adaptive_k(500_000), 150);
    }

    #[test]
    fn very_large_index_uses_200() {
        assert_eq!(adaptive_k(500_001), 200);
        assert_eq!(adaptive_k(1_000_000), 200);
    }

    #[test]
    fn million_plus_capped_at_300() {
        assert_eq!(adaptive_k(1_000_001), 300);
        assert_eq!(adaptive_k(10_000_000), 300);
    }
}

impl SemanticRetrievalPipeline {
    pub fn new(db: CozoDb) -> Result<Self, Box<dyn std::error::Error>> {
        let embedder = Embedder::new()?;
        let rerank_stage = RerankStage::try_new();
        Ok(Self {
            embedder,
            rerank_stage,
            db,
        })
    }

    pub fn reranker_active(&self) -> bool {
        self.rerank_stage.is_active()
    }

    pub fn retrieve(
        &self,
        query: &str,
        opts: &RetrieveOptions,
    ) -> Result<RetrievalResult, Box<dyn std::error::Error>> {
        // Resolve effective k: explicit override or adaptive on index size.
        let index_size = self.index_size()?;
        let effective_k = opts.ann_top_k.unwrap_or_else(|| adaptive_k(index_size));

        // Stage 2: embed query, run CozoDB HNSW search.
        let qvec = self.embedder.embed(&[query.to_string()])?;
        let raw = self.hnsw_retrieve(&qvec[0], effective_k)?;
        let ann_candidate_count = raw.len();

        // HNSW returns qualified_name directly — no key→QN map needed.
        let desired_qns: Vec<String> = raw.iter().map(|(qn, _)| qn.clone()).collect();
        let element_map = self.fetch_elements_batch(&desired_qns)?;

        // Build seeds, applying worktree + env + test + node-type filters.
        let query_lower = query.to_lowercase();
        let query_is_about_tests = query_lower.contains("test");
        let policy = if opts.include_ontology_steps {
            None
        } else {
            Some(crate::retrieval::FilterPolicy::new())
        };
        let mut seeds: Vec<Seed> = Vec::with_capacity(raw.len());
        let mut worktree_filtered = 0usize;
        let mut env_filtered = 0usize;
        let mut test_filtered = 0usize;
        let mut node_type_filtered = 0usize;
        for (qn, dist) in &raw {
            let Some(el) = element_map.get(qn) else {
                continue;
            };

            if !opts.include_worktrees && is_worktree_path(&el.file_path) {
                worktree_filtered += 1;
                continue;
            }
            if let Some(wanted_env) = &opts.env {
                if &el.env != wanted_env {
                    env_filtered += 1;
                    continue;
                }
            }
            if !query_is_about_tests
                && (el.name.starts_with("test_") || el.qualified_name.contains("::test_"))
            {
                test_filtered += 1;
                continue;
            }
            if let Some(p) = &policy {
                if p.should_drop(&el.element_type, &query_lower) {
                    node_type_filtered += 1;
                    continue;
                }
            }

            let blob = crate::embeddings::build_blob(el).unwrap_or_default();
            seeds.push(Seed {
                qualified_name: qn.clone(),
                usearch_key: 0,
                ann_distance: *dist,
                rerank_score: None,
                element_type: el.element_type.clone(),
                file_path: el.file_path.clone(),
                env: el.env.clone(),
                blob_excerpt: blob.clone(),
            });
        }

        // Stage 3: cross-encoder rerank.
        let docs: Vec<String> = seeds.iter().map(|s| s.blob_excerpt.clone()).collect();
        let (scores, status) = self.rerank_stage.rerank(query, docs);
        let mut ranked_seeds: Vec<Seed> = Vec::with_capacity(scores.len());
        for s in &scores {
            if let Some(mut seed) = seeds.get(s.document_idx).cloned() {
                seed.rerank_score = Some(s.score);
                ranked_seeds.push(seed);
            }
        }
        ranked_seeds.truncate(opts.rerank_top_n);

        Ok(RetrievalResult {
            seeds: ranked_seeds,
            reranker_status: status,
            ann_candidate_count,
            worktree_filtered_count: worktree_filtered,
            env_filtered_count: env_filtered,
            test_filtered_count: test_filtered,
            node_type_filtered_count: node_type_filtered,
            index_size,
            ann_top_k_used: effective_k,
            embeddings_stale: opts.embeddings_stale,
        })
    }

    /// Count rows in `embedding_state`. Cheap; called once per retrieve to
    /// size adaptive k. Reuses the existing count_by_state aggregator
    /// because CozoDB 0.7.x is picky about inline count() placement.
    fn index_size(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let counts = crate::embeddings::state::count_by_state(&self.db)?;
        Ok(counts.fresh + counts.stale + counts.other)
    }

    /// Run the HNSW search via `~embedding_vectors:vec_idx`. Returns
    /// `(qualified_name, cosine_distance)` pairs.
    fn hnsw_retrieve(
        &self,
        qvec: &[f32],
        k: usize,
    ) -> Result<Vec<(String, f32)>, Box<dyn std::error::Error>> {
        let vec_literal = qvec
            .iter()
            .map(|f| format!("{:.6}", f))
            .collect::<Vec<_>>()
            .join(", ");
        let query = format!(
            r#"?[dist, qualified_name] := ~embedding_vectors:vec_idx {{
                    qualified_name |
                    query: vec([{vec_literal}]),
                    k: {k},
                    ef: {ef},
                    bind_distance: dist
                }}"#,
            // ef (search effort) — bump with k so the index has headroom.
            ef = (k * 2).max(50)
        );
        let result = run_script(&self.db, &query, Default::default())?;
        let mut out = Vec::with_capacity(result.rows.len());
        for row in &result.rows {
            let dist = row
                .first()
                .and_then(|v: &DataValue| v.get_float())
                .unwrap_or(1.0) as f32;
            let qn = row
                .get(1)
                .and_then(|v: &DataValue| v.get_str())
                .unwrap_or("")
                .to_string();
            if !qn.is_empty() {
                out.push((qn, dist));
            }
        }
        Ok(out)
    }

    fn fetch_elements_batch(
        &self,
        qns: &[String],
    ) -> Result<HashMap<String, CodeElement>, Box<dyn std::error::Error>> {
        if qns.is_empty() {
            return Ok(HashMap::new());
        }
        let engine = crate::graph::query::GraphEngine::new(self.db.clone());
        let all = engine.all_elements()?;
        let qn_set: HashSet<&str> = qns.iter().map(|s| s.as_str()).collect();
        Ok(all
            .into_iter()
            .filter(|e| qn_set.contains(e.qualified_name.as_str()))
            .map(|e| (e.qualified_name.clone(), e))
            .collect())
    }
}

/// Match the patterns from Q2: `.worktrees/`, `.claude/worktrees/`,
/// `.opencode/worktrees/`. Path-separator aware so `.worktrees-x/` doesn't
/// false-positive.
fn is_worktree_path(path: &str) -> bool {
    const PATTERNS: &[&str] = &[
        "/.worktrees/",
        "/.claude/worktrees/",
        "/.opencode/worktrees/",
    ];
    if path.starts_with(".worktrees/")
        || path.starts_with(".claude/worktrees/")
        || path.starts_with(".opencode/worktrees/")
    {
        return true;
    }
    PATTERNS.iter().any(|p| path.contains(p))
}

fn truncate(s: &str, max_chars: usize) -> String {
    if s.len() <= max_chars {
        return s.to_string();
    }
    let mut end = max_chars;
    while end > 0 && !s.is_char_boundary(end) {
        end -= 1;
    }
    s[..end].to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn worktree_filter_matches_q2_patterns() {
        assert!(is_worktree_path("src/.worktrees/foo/bar.rs"));
        assert!(is_worktree_path(".worktrees/foo.rs"));
        assert!(is_worktree_path("repo/.claude/worktrees/abc/main.rs"));
        assert!(is_worktree_path("repo/.opencode/worktrees/x/y.rs"));
    }

    #[test]
    fn worktree_filter_does_not_match_unrelated_dirs() {
        assert!(!is_worktree_path("src/main.rs"));
        assert!(!is_worktree_path(".worktrees-extra/foo.rs"));
        assert!(!is_worktree_path("src/.worktrees_other/x.rs"));
    }

    #[test]
    fn truncate_respects_char_boundaries() {
        let s = "hello".repeat(100);
        let t = truncate(&s, 200);
        assert!(t.len() <= 200);
    }

    // =========================================================================
    // RetrieveOptions default tests
    // =========================================================================

    #[test]
    fn retrieve_options_default_env_is_local() {
        let opts = RetrieveOptions::default();
        assert_eq!(opts.env, Some("local".to_string()));
    }

    #[test]
    fn retrieve_options_default_ann_top_k_is_none() {
        let opts = RetrieveOptions::default();
        assert!(
            opts.ann_top_k.is_none(),
            "default should be adaptive (None)"
        );
    }

    #[test]
    fn retrieve_options_default_rerank_top_n_is_10() {
        let opts = RetrieveOptions::default();
        assert_eq!(opts.rerank_top_n, 10);
    }

    #[test]
    fn retrieve_options_default_include_worktrees_false() {
        let opts = RetrieveOptions::default();
        assert!(!opts.include_worktrees, "Q2 default-on worktree filter");
    }

    #[test]
    fn retrieve_options_default_include_ontology_steps_false() {
        let opts = RetrieveOptions::default();
        assert!(
            !opts.include_ontology_steps,
            "node-type filter on by default"
        );
    }

    #[test]
    fn retrieve_options_default_embeddings_stale_false() {
        let opts = RetrieveOptions::default();
        assert!(!opts.embeddings_stale);
    }
}
