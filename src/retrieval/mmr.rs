//! US-SEM-04 / FR-SEM-05: file-diversity / MMR post-filter.
//!
//! Pure greedy Maximal Marginal Relevance over search results that carry a
//! `file_path`. When diversity mode is on, the top-k can no longer collapse
//! to ≥70% one file (the MCP-dispatch probe failure mode: 8/10 hits from
//! `src/embed/assets/bundle.js`). Off = exact pass-through (no regression).
//!
//! Pure functions — no I/O, no embeddings, no LLM. The MCP `semantic_search`
//! seam applies `apply_mmr_diversity` to its merged `results` after
//! HNSW+rerank, before pagination.

use std::collections::HashSet;

/// Relevance-diversity trade-off (FR-SEM-05). 0.5 = balanced; 0.0 = pure
/// relevance (off); 1.0 = pure diversity.
pub const DEFAULT_DIVERSITY_LAMBDA: f64 = 0.5;
/// FR-SEM-05: at least N distinct `file_path`s in top-k (default ≥3 for k=10).
pub const DEFAULT_MIN_DISTINCT_FILES: usize = 3;

/// One ranked search hit with the file it lives in.
#[derive(Debug, Clone, PartialEq)]
pub struct GreedyMmrItem {
    pub id: String,
    /// Relevance score (higher = better; any scale).
    pub score: f64,
    /// File path used for the diversity penalty.
    pub file_path: String,
}

/// Deterministic greedy MMR:
///   1. pick the argmax-relevance item first;
///   2. each next pick maximizes
///      λ·score − (1−λ)·max_similarity_to_picked
///      where similarity = 1.0 for same file, 0.0 for a new file.
///
/// Ties break by (id, original index) so same inputs always yield the same
/// output. `k = 0` returns everything.
pub fn greedy_mmr(items: &[GreedyMmrItem], k: usize, lambda: f64) -> Vec<GreedyMmrItem> {
    if items.is_empty() || k == 0 {
        return Vec::new();
    }
    let k = k.min(items.len());
    let lambda = lambda.clamp(0.0, 1.0);
    let mut picked: Vec<usize> = Vec::with_capacity(k);
    let mut picked_files: HashSet<&str> = HashSet::new();

    // Pick 1: argmax relevance; tie-break by id then original index.
    let first = items
        .iter()
        .enumerate()
        .max_by(|(ia, a), (ib, b)| {
            a.score
                .partial_cmp(&b.score)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.id.cmp(&b.id))
                .then_with(|| ib.cmp(ia)) // original index ascending
        })
        .map(|(i, _)| i)
        .expect("non-empty items");
    picked.push(first);
    picked_files.insert(items[first].file_path.as_str());

    while picked.len() < k {
        let mut best: Option<(usize, f64)> = None;
        for (i, it) in items.iter().enumerate() {
            if picked.contains(&i) {
                continue;
            }
            let sim = if picked_files.contains(it.file_path.as_str()) {
                1.0
            } else {
                0.0
            };
            let marginal = lambda * it.score - (1.0 - lambda) * sim;
            let better = match best {
                None => true,
                Some((_, best_m)) => {
                    marginal > best_m
                        || (marginal == best_m
                            && (it.id < items[best.unwrap().0].id
                                || (it.id == items[best.unwrap().0].id && i < best.unwrap().0)))
                }
            };
            if better {
                best = Some((i, marginal));
            }
        }
        let (idx, _) = best.expect("remaining items exist while loop runs");
        picked_files.insert(items[idx].file_path.as_str());
        picked.push(idx);
    }

    picked.into_iter().map(|i| items[i].clone()).collect()
}

/// Post-filter seam: apply MMR file-diversity to ranked results.
///
/// * `lambda = 0.0` → exact pass-through (ranking matches HNSW+rerank).
/// * `lambda > 0.0` → greedy MMR reorder; when `min_distinct_files` cannot
///   be met (corpus has fewer files), best-effort diversity is kept and the
///   result never shrinks below `k`.
pub fn apply_mmr_diversity(
    items: &[GreedyMmrItem],
    top_k: usize,
    lambda: f64,
    min_distinct_files: usize,
) -> Vec<GreedyMmrItem> {
    if items.is_empty() {
        return Vec::new();
    }
    let k = if top_k == 0 {
        items.len()
    } else {
        top_k.min(items.len())
    };
    if lambda <= 0.0 {
        return items.iter().take(k).cloned().collect();
    }
    let picked = greedy_mmr(items, k, lambda);
    // Best-effort: if the corpus physically cannot reach min_distinct_files,
    // keep whatever greedy produced (never error, never shrink).
    let distinct: HashSet<&str> = picked.iter().map(|i| i.file_path.as_str()).collect();
    if distinct.len() >= min_distinct_files {
        picked
    } else {
        // Degenerate single-file corpus: fall back to plain top-k by
        // relevance (diversity impossible) — still deterministic.
        let mut sorted = items.iter().take(k).cloned().collect::<Vec<_>>();
        sorted.sort_by(|a, b| {
            b.score
                .partial_cmp(&a.score)
                .unwrap_or(std::cmp::Ordering::Equal)
                .then_with(|| a.id.cmp(&b.id))
        });
        sorted
    }
}
