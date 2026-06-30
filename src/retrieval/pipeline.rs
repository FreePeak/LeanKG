//! Retrieval pipeline orchestration: query → embed → ANN → worktree/env
//! filter → cross-encoder rerank. Returns a `RetrievalResult` ready for the
//! MCP handler to hand off to the traversal stage.

use crate::db::models::CodeElement;
use crate::db::schema::CozoDb;
use crate::embeddings::{
    index::AnnIndex,
    models::{Embedder, RerankerStatus},
};
use crate::retrieval::{ann::AnnRetrieve, rerank::RerankStage};
use std::collections::{HashMap, HashSet};
use std::path::Path;

pub struct SemanticRetrievalPipeline {
    embedder: Embedder,
    index: AnnIndex,
    rerank_stage: RerankStage,
    db: CozoDb,
}

#[derive(Debug, Clone)]
pub struct Seed {
    pub qualified_name: String,
    pub usearch_key: u64,
    /// Raw usearch cosine distance/similarity (semantics depend on usearch
    /// version; we surface the value as-is for diagnostics).
    pub ann_distance: f32,
    /// Set by the cross-encoder. None when the pipeline ran in ANN-only
    /// fallback mode (Q4 option A).
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
    pub embeddings_stale: bool,
}

#[derive(Debug, Clone)]
pub struct RetrieveOptions {
    /// Restrict results to a single env ("local" / "staging" / "production").
    /// None disables env filtering.
    pub env: Option<String>,
    /// ANN depth. The reranker then narrows to `rerank_top_n`. Default 50.
    pub ann_top_k: usize,
    /// Final seed count after rerank. Default 10.
    pub rerank_top_n: usize,
    /// Q2 default-on worktree filter. Set true to include worktree copies.
    pub include_worktrees: bool,
    /// Surface a stale-embeddings warning in diagnostics. Set by the caller
    /// based on comparing embeddings.meta.json.built_at vs last index run.
    pub embeddings_stale: bool,
}

impl Default for RetrieveOptions {
    fn default() -> Self {
        Self {
            env: Some("local".to_string()),
            ann_top_k: 50,
            rerank_top_n: 10,
            include_worktrees: false,
            embeddings_stale: false,
        }
    }
}

impl SemanticRetrievalPipeline {
    pub fn new(db: CozoDb, index_path: &Path) -> Result<Self, Box<dyn std::error::Error>> {
        let embedder = Embedder::new()?;
        let index = AnnIndex::load(index_path)?;
        let rerank_stage = RerankStage::try_new();
        Ok(Self {
            embedder,
            index,
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
        // Stage 2: ANN retrieve.
        let ann = AnnRetrieve::new(&self.embedder, &self.index);
        let raw = ann.retrieve(query, opts.ann_top_k)?;
        let ann_candidate_count = raw.len();

        // Map keys → qualified_names (single batched query).
        let qn_map = self.build_key_to_qn_map()?;

        // Resolve desired qualified_names for the batch CodeElements fetch.
        let desired_qns: Vec<String> = raw
            .iter()
            .filter_map(|r| qn_map.get(&r.key).cloned())
            .collect();

        // Fetch CodeElements for those qualified_names.
        let element_map = self.fetch_elements_batch(&desired_qns)?;

        // Build seeds, applying worktree + env filters.
        let mut seeds: Vec<Seed> = Vec::with_capacity(raw.len());
        let mut worktree_filtered = 0usize;
        let mut env_filtered = 0usize;
        for r in &raw {
            let Some(qn) = qn_map.get(&r.key) else {
                continue;
            };
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

            let blob = crate::embeddings::build_blob(el).unwrap_or_default();
            seeds.push(Seed {
                qualified_name: qn.clone(),
                usearch_key: r.key,
                ann_distance: r.distance,
                rerank_score: None,
                element_type: el.element_type.clone(),
                file_path: el.file_path.clone(),
                env: el.env.clone(),
                blob_excerpt: truncate(&blob, 200),
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
            embeddings_stale: opts.embeddings_stale,
        })
    }

    fn build_key_to_qn_map(&self) -> Result<HashMap<u64, String>, Box<dyn std::error::Error>> {
        let rows = crate::embeddings::state::list_all(&self.db)?;
        Ok(rows
            .into_iter()
            .map(|r| (r.usearch_key as u64, r.qualified_name))
            .collect())
    }

    fn fetch_elements_batch(
        &self,
        qns: &[String],
    ) -> Result<HashMap<String, CodeElement>, Box<dyn std::error::Error>> {
        if qns.is_empty() {
            return Ok(HashMap::new());
        }
        // Phase 2 simplicity: pull all elements and filter in Rust. This is
        // O(n) per query which is fine for repos up to ~50k elements; larger
        // deployments should swap in a real batched Datalog lookup.
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
}
