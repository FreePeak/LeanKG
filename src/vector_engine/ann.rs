//! Layer-0 HNSW / NSW search over in-RAM SQ8 (FR-VE-BENCH-Q).
//!
//! Full-corpus flat scans of 1M×384 INT8 cannot meet the &lt;50ms P95 gate on
//! typical laptops. The LocalEngine hot path is graph ANN with SQ8 distance
//! in RAM — this module provides a buildable NSW for benches + gate evidence.

use std::cmp::Ordering;
use std::collections::{BinaryHeap, HashSet};

use super::hnsw::{HnswParams, DEFAULT_EF_SEARCH, DEFAULT_M};
use super::simd::{detect_simd, dot_i8, SimdKind};
use super::tier2::Sq8Cache;

#[derive(Debug, Clone, Copy)]
struct Cand {
    idx: usize,
    /// Higher is better (INT8 dot).
    score: i32,
}

impl PartialEq for Cand {
    fn eq(&self, other: &Self) -> bool {
        self.idx == other.idx
    }
}
impl Eq for Cand {}
impl PartialOrd for Cand {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for Cand {
    fn cmp(&self, other: &Self) -> Ordering {
        self.score
            .cmp(&other.score)
            .then_with(|| self.idx.cmp(&other.idx))
    }
}

/// Min-heap by score (worst at top) for ef-bounded candidate sets.
#[derive(Debug, Clone, Copy)]
struct MinCand(Cand);
impl PartialEq for MinCand {
    fn eq(&self, other: &Self) -> bool {
        self.0.idx == other.0.idx
    }
}
impl Eq for MinCand {}
impl PartialOrd for MinCand {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}
impl Ord for MinCand {
    fn cmp(&self, other: &Self) -> Ordering {
        other.0.cmp(&self.0)
    }
}

/// Navigable small-world graph over an [`Sq8Cache`] (layer 0).
#[derive(Debug)]
pub struct Sq8Nsw {
    cache: Sq8Cache,
    /// Undirected neighbors by row index.
    neighbors: Vec<Vec<usize>>,
    entry: usize,
    params: HnswParams,
    simd: SimdKind,
}

impl Sq8Nsw {
    pub fn cache(&self) -> &Sq8Cache {
        &self.cache
    }

    pub fn len(&self) -> usize {
        self.cache.len()
    }

    pub fn is_empty(&self) -> bool {
        self.cache.is_empty()
    }

    pub fn params(&self) -> HnswParams {
        self.params
    }

    /// Fast synthetic NSW for benches: patterned SQ8 rows + ring+shortcut edges.
    ///
    /// Build is O(n·M) and avoids FP32 quantization. Suitable for latency gates
    /// (FR-VE-BENCH-Q); recall vs FP32 is covered by FR-VE-BENCH-RECALL separately.
    pub fn synth_for_bench(n: usize, dim: usize, params: HnswParams) -> Self {
        let mut cache = Sq8Cache::new(dim);
        for id in 0..n as u64 {
            let mut row = vec![0i8; dim];
            for (j, slot) in row.iter_mut().enumerate() {
                // Compute in i32 first: `% 254 as i8` truncates >127 and then
                // `- 127` overflows in debug builds (CI).
                let v = ((id as usize).wrapping_mul(31) + j * 17) % 254;
                *slot = (v as i32 - 127) as i8;
            }
            cache.push_sq8(id, &row).expect("dim matches");
        }
        let m = params.m.clamp(super::hnsw::M_MIN, super::hnsw::M_MAX);
        let mut neighbors = vec![Vec::with_capacity(m); n];
        if n == 0 {
            return Self {
                cache,
                neighbors,
                entry: 0,
                params,
                simd: detect_simd(),
            };
        }
        for (i, slot) in neighbors.iter_mut().enumerate() {
            let mut nbrs = HashSet::new();
            // Ring neighbors
            for d in 1..=(m / 2).max(1) {
                nbrs.insert((i + d) % n);
                nbrs.insert((i + n - d) % n);
            }
            // Deterministic long-range shortcuts
            for k in 0..(m.saturating_sub(nbrs.len())) {
                let jump =
                    1 + ((i.wrapping_mul(2654435761) + k * 97) % (n.saturating_sub(1).max(1)));
                nbrs.insert((i + jump) % n);
            }
            nbrs.remove(&i);
            let mut list: Vec<usize> = nbrs.into_iter().take(m).collect();
            list.sort_unstable();
            *slot = list;
        }
        Self {
            cache,
            neighbors,
            entry: 0,
            params,
            simd: detect_simd(),
        }
    }

    fn score_at(&self, idx: usize, query: &[i8]) -> i32 {
        let row = self.cache.row(idx).expect("idx in range");
        dot_i8(row, query, self.simd)
    }

    /// Beam search (ef) returning up to `k` neighbor row indices (highest score).
    pub fn search(&self, query: &[i8], k: usize, ef: usize) -> Vec<usize> {
        if self.is_empty() || k == 0 {
            return Vec::new();
        }
        let ef = ef.max(k).max(1);
        let entry = self.entry.min(self.len() - 1);
        let mut visited = HashSet::new();
        visited.insert(entry);

        let mut candidates = BinaryHeap::new(); // max-heap by score
        let mut w = BinaryHeap::new(); // min-heap (worst of ef)
        let entry_score = self.score_at(entry, query);
        candidates.push(Cand {
            idx: entry,
            score: entry_score,
        });
        w.push(MinCand(Cand {
            idx: entry,
            score: entry_score,
        }));

        while let Some(c) = candidates.pop() {
            let worst = w.peek().map(|m| m.0.score).unwrap_or(i32::MIN);
            if c.score < worst && w.len() >= ef {
                break;
            }
            for &nb in &self.neighbors[c.idx] {
                if !visited.insert(nb) {
                    continue;
                }
                let s = self.score_at(nb, query);
                let worst = w.peek().map(|m| m.0.score).unwrap_or(i32::MIN);
                if s > worst || w.len() < ef {
                    candidates.push(Cand { idx: nb, score: s });
                    w.push(MinCand(Cand { idx: nb, score: s }));
                    if w.len() > ef {
                        w.pop();
                    }
                }
            }
        }

        let mut out: Vec<Cand> = w.into_iter().map(|m| m.0).collect();
        out.sort_by(|a, b| b.cmp(a));
        out.into_iter().map(|c| c.idx).take(k).collect()
    }

    /// Search returning element ids (not row indices).
    pub fn search_ids(&self, query: &[i8], k: usize, ef: usize) -> Vec<u64> {
        self.search(query, k, ef)
            .into_iter()
            .filter_map(|i| self.cache.id_at(i))
            .collect()
    }
}

/// Build a patterned query matching [`Sq8Nsw::synth_for_bench`] style.
pub fn synth_query(dim: usize, seed: u64) -> Vec<i8> {
    let mut q = vec![0i8; dim];
    for (j, slot) in q.iter_mut().enumerate() {
        let v = ((seed as usize).wrapping_mul(13) + j * 11) % 254;
        *slot = (v as i32 - 127) as i8;
    }
    q
}

/// Default bench params (PRD M∈[12,16], efSearch=50).
pub fn bench_params() -> HnswParams {
    HnswParams {
        m: DEFAULT_M,
        ef_search: DEFAULT_EF_SEARCH,
        ..HnswParams::default()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vector_engine::engine::DEFAULT_VECTOR_DIM;

    #[test]
    fn synth_nsw_search_returns_k() {
        let graph = Sq8Nsw::synth_for_bench(256, DEFAULT_VECTOR_DIM, bench_params());
        let q = synth_query(DEFAULT_VECTOR_DIM, 7);
        let hits = graph.search(&q, 10, DEFAULT_EF_SEARCH);
        assert_eq!(hits.len(), 10);
        assert!(hits.iter().all(|&i| i < graph.len()));
    }

    #[test]
    fn synth_nsw_search_ids_match_rows() {
        let graph = Sq8Nsw::synth_for_bench(128, DEFAULT_VECTOR_DIM, bench_params());
        let q = synth_query(DEFAULT_VECTOR_DIM, 11);
        let idxs = graph.search(&q, 8, DEFAULT_EF_SEARCH);
        let ids = graph.search_ids(&q, 8, DEFAULT_EF_SEARCH);
        assert_eq!(ids.len(), idxs.len());
        for (idx, id) in idxs.iter().zip(ids.iter()) {
            assert_eq!(graph.cache().id_at(*idx), Some(*id));
        }
    }

    #[test]
    fn empty_graph_search_ok() {
        let graph = Sq8Nsw::synth_for_bench(0, 8, bench_params());
        assert!(graph.search(&[0i8; 8], 5, 10).is_empty());
        assert!(graph.search_ids(&[0i8; 8], 5, 10).is_empty());
    }
}
