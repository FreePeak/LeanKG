//! HNSW neighbor selection with low M (FR-VE-HNSW).
//!
//! `selectNeighborsHeuristic` keeps M ∈ [12, 16]; raise `ef_construction`
//! to protect recall (>90% at efSearch=50 vs FP32 brute-force).

use std::cmp::Ordering;
use std::collections::{BinaryHeap, HashSet};

/// Inclusive M range from PRD §5.14.2.
pub const M_MIN: usize = 12;
pub const M_MAX: usize = 16;
pub const DEFAULT_M: usize = 16;
pub const DEFAULT_EF_CONSTRUCTION: usize = 200;
pub const DEFAULT_EF_SEARCH: usize = 50;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct HnswParams {
    pub m: usize,
    pub ef_construction: usize,
    pub ef_search: usize,
}

impl Default for HnswParams {
    fn default() -> Self {
        Self {
            m: DEFAULT_M,
            ef_construction: DEFAULT_EF_CONSTRUCTION,
            ef_search: DEFAULT_EF_SEARCH,
        }
    }
}

impl HnswParams {
    pub fn validate(&self) -> Result<(), String> {
        if !(M_MIN..=M_MAX).contains(&self.m) {
            return Err(format!("M must be in [{M_MIN}, {M_MAX}], got {}", self.m));
        }
        if self.ef_construction < self.m {
            return Err("ef_construction must be >= M".into());
        }
        if self.ef_search == 0 {
            return Err("ef_search must be > 0".into());
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy)]
struct Scored {
    id: u64,
    /// Higher is better (dot product / similarity).
    score: f32,
}

impl PartialEq for Scored {
    fn eq(&self, other: &Self) -> bool {
        self.id == other.id
    }
}
impl Eq for Scored {}
impl PartialOrd for Scored {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for Scored {
    fn cmp(&self, other: &Self) -> Ordering {
        // Max-heap by score; BinaryHeap is max-heap.
        self.score
            .partial_cmp(&other.score)
            .unwrap_or(Ordering::Equal)
            .then_with(|| self.id.cmp(&other.id))
    }
}

/// Min-heap wrapper for keeping worst candidates at top when capping size.
#[derive(Debug, Clone, Copy)]
struct MinScored(Scored);
impl PartialEq for MinScored {
    fn eq(&self, other: &Self) -> bool {
        self.0.id == other.0.id
    }
}
impl Eq for MinScored {}
impl PartialOrd for MinScored {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for MinScored {
    fn cmp(&self, other: &Self) -> Ordering {
        other.0.cmp(&self.0)
    }
}

/// Heuristic neighbor selection (HNSW paper Algorithm 4 style).
///
/// `candidates` are (id, similarity score). Returns up to `m` neighbor ids,
/// preferring diverse high-score nodes.
pub fn select_neighbors_heuristic(
    candidates: &[(u64, f32)],
    m: usize,
    dist_fn: &dyn Fn(u64, u64) -> f32,
) -> Vec<u64> {
    let m = m.clamp(M_MIN, M_MAX);
    let mut sorted: Vec<Scored> = candidates
        .iter()
        .map(|&(id, score)| Scored { id, score })
        .collect();
    sorted.sort_by(|a, b| b.cmp(a));

    let mut selected: Vec<Scored> = Vec::with_capacity(m);
    for cand in sorted {
        if selected.len() >= m {
            break;
        }
        let mut ok = true;
        for s in &selected {
            // If cand is closer to an already-selected neighbor than to the
            // query (approx via score), skip for diversity.
            let d_cs = dist_fn(cand.id, s.id);
            if d_cs > cand.score {
                ok = false;
                break;
            }
        }
        if ok {
            selected.push(cand);
        }
    }
    // Fill remaining slots by score if heuristic was too strict.
    if selected.len() < m {
        let have: HashSet<u64> = selected.iter().map(|s| s.id).collect();
        let mut rest: BinaryHeap<MinScored> = BinaryHeap::new();
        for &(id, score) in candidates {
            if have.contains(&id) {
                continue;
            }
            rest.push(MinScored(Scored { id, score }));
            if rest.len() > m - selected.len() {
                rest.pop();
            }
        }
        while let Some(MinScored(s)) = rest.pop() {
            selected.push(s);
        }
    }
    selected.into_iter().map(|s| s.id).take(m).collect()
}

/// Brute-force top-k by score (recall baseline helper).
pub fn brute_force_topk(scores: &[(u64, f32)], k: usize) -> Vec<u64> {
    let mut v = scores.to_vec();
    v.sort_by(|a, b| b.1.partial_cmp(&a.1).unwrap_or(Ordering::Equal));
    v.into_iter().map(|(id, _)| id).take(k).collect()
}

/// Recall@k of `approx` vs `truth` (fraction of truth ids present).
pub fn recall_at_k(truth: &[u64], approx: &[u64]) -> f32 {
    if truth.is_empty() {
        return 1.0;
    }
    let set: HashSet<u64> = approx.iter().copied().collect();
    let hit = truth.iter().filter(|id| set.contains(id)).count();
    hit as f32 / truth.len() as f32
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn params_m_in_range() {
        let p = HnswParams::default();
        p.validate().unwrap();
        assert!((M_MIN..=M_MAX).contains(&p.m));
    }

    #[test]
    fn reject_m_out_of_range() {
        let p = HnswParams {
            m: 8,
            ..Default::default()
        };
        assert!(p.validate().is_err());
    }

    #[test]
    fn select_neighbors_respects_m() {
        let cands: Vec<(u64, f32)> = (0..40).map(|i| (i, i as f32)).collect();
        let dist = |_a: u64, _b: u64| 0.0f32;
        let picked = select_neighbors_heuristic(&cands, 16, &dist);
        assert!(picked.len() <= 16);
        assert!(!picked.is_empty());
    }

    #[test]
    fn recall_helper_perfect_match() {
        let truth = vec![1, 2, 3];
        assert!((recall_at_k(&truth, &[3, 2, 1, 9]) - 1.0).abs() < 1e-6);
    }
}
