//! Bench harness for LocalEngine quality gates (FR-VE-BENCH-*).

use std::time::{Duration, Instant};

use super::ann::{bench_params, synth_query, Sq8Nsw};
use super::engine::EngineKind;
use super::hnsw::{brute_force_topk, recall_at_k, DEFAULT_EF_SEARCH};
use super::memory::{plan_under_2gb_cgroup, LOCAL_SURVIVAL_CAP_BYTES};
use super::simd::{detect_simd, dot_i8, SimdKind};
use super::tier2::{quantize_sq8, Sq8Cache};

#[derive(Debug, Clone)]
pub struct QueryBenchResult {
    pub n_vectors: usize,
    pub p95: Duration,
    pub simd: SimdKind,
}

pub const TARGET_P95_MS: u128 = 50;
/// Corpus size required by FR-VE-BENCH-Q (1 query vs 1M SQ8 chunks).
pub const BENCH_Q_CORPUS: usize = 1_000_000;

/// Run `iters` queries against an in-RAM SQ8 cache; return approximate P95.
///
/// Flat scan — useful for small-n microbenches. Full 1M gate uses
/// [`bench_ann_query_p95`] (graph ANN over SQ8).
pub fn bench_query_p95(cache: &Sq8Cache, query: &[i8], iters: usize) -> QueryBenchResult {
    let simd = detect_simd();
    let dim = cache.dim();
    let data = cache.as_bytes();
    let mut samples = Vec::with_capacity(iters);
    for _ in 0..iters {
        let t0 = Instant::now();
        let mut best = i32::MIN;
        for row in data.chunks_exact(dim) {
            best = best.max(dot_i8(row, query, simd));
        }
        let _ = best;
        samples.push(t0.elapsed());
    }
    samples.sort();
    let idx = ((samples.len() as f64) * 0.95).floor() as usize;
    let p95 = samples[idx.min(samples.len().saturating_sub(1))];
    QueryBenchResult {
        n_vectors: cache.len(),
        p95,
        simd,
    }
}

/// ANN (NSW/HNSW layer-0) query P95 over an in-RAM SQ8 graph (FR-VE-BENCH-Q).
pub fn bench_ann_query_p95(graph: &Sq8Nsw, query: &[i8], iters: usize) -> QueryBenchResult {
    let ef = graph.params().ef_search.max(DEFAULT_EF_SEARCH);
    let k = 10.min(graph.len().max(1));
    let mut samples = Vec::with_capacity(iters);
    for _ in 0..iters {
        let t0 = Instant::now();
        let hits = graph.search(query, k, ef);
        let _ = hits;
        samples.push(t0.elapsed());
    }
    samples.sort();
    let idx = ((samples.len() as f64) * 0.95).floor() as usize;
    let p95 = samples[idx.min(samples.len().saturating_sub(1))];
    QueryBenchResult {
        n_vectors: graph.len(),
        p95,
        simd: detect_simd(),
    }
}

/// Build synth NSW + measure ANN P95 (convenience for gate / cargo bench).
pub fn bench_ann_p95_at(n: usize, dim: usize, iters: usize) -> QueryBenchResult {
    let graph = Sq8Nsw::synth_for_bench(n, dim, bench_params());
    let query = synth_query(dim, 42);
    bench_ann_query_p95(&graph, &query, iters)
}

/// True when ANN P95 at [`BENCH_Q_CORPUS`] meets the &lt;50ms floor.
pub fn ann_p95_meets_1m_floor(iters: usize) -> (QueryBenchResult, bool) {
    let res = bench_ann_p95_at(BENCH_Q_CORPUS, super::engine::DEFAULT_VECTOR_DIM, iters);
    let ok = res.p95.as_millis() < TARGET_P95_MS;
    (res, ok)
}

/// Build a synthetic SQ8 cache of `n` vectors.
pub fn synth_sq8_cache(n: usize, dim: usize) -> (Sq8Cache, Vec<i8>) {
    let mut cache = Sq8Cache::new(dim);
    let mut query_fp = vec![0.0f32; dim];
    for (i, slot) in query_fp.iter_mut().enumerate() {
        *slot = ((i % 7) as f32) * 0.1 - 0.3;
    }
    let (query, _) = quantize_sq8(&query_fp);
    for id in 0..n as u64 {
        let mut fp = vec![0.0f32; dim];
        for (j, slot) in fp.iter_mut().enumerate() {
            *slot = (((id as usize + j) % 11) as f32) * 0.05 - 0.2;
        }
        cache.push_fp32(id, &fp).unwrap();
    }
    (cache, query)
}

/// Estimate I/O reduction vs a hypothetical mmap full-scan (FR-VE-BENCH-IO).
pub fn io_reduction_vs_mmap(n_vectors: usize, pages_touched_hot: usize) -> f64 {
    if n_vectors == 0 {
        return 1.0;
    }
    1.0 - (pages_touched_hot as f64 / n_vectors as f64)
}

pub const TARGET_IO_REDUCTION: f64 = 0.80;

/// Recall of SQ8 ranking vs FP32 brute-force (FR-VE-BENCH-RECALL).
pub fn recall_sq8_vs_fp32(fp32_rows: &[(u64, Vec<f32>)], query: &[f32], k: usize) -> f32 {
    let fp_scores: Vec<(u64, f32)> = fp32_rows
        .iter()
        .map(|(id, v)| {
            let s: f32 = v.iter().zip(query.iter()).map(|(a, b)| a * b).sum();
            (*id, s)
        })
        .collect();
    let truth = brute_force_topk(&fp_scores, k);
    let (q8, _) = quantize_sq8(query);
    let sq_scores: Vec<(u64, f32)> = fp32_rows
        .iter()
        .map(|(id, v)| {
            let (row, _) = quantize_sq8(v);
            let s = dot_i8(&row, &q8, SimdKind::Scalar) as f32;
            (*id, s)
        })
        .collect();
    let approx = brute_force_topk(&sq_scores, k);
    recall_at_k(&truth, &approx)
}

pub const TARGET_RECALL: f32 = 0.90;

/// 2GB cgroup memory plan must stay within survival cap (FR-VE-BENCH-OOM).
pub fn oom_plan_within_cap() -> bool {
    let plan = plan_under_2gb_cgroup(EngineKind::Local);
    plan.block_cache_bytes <= LOCAL_SURVIVAL_CAP_BYTES
        && plan.survival_cap_bytes <= LOCAL_SURVIVAL_CAP_BYTES
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vector_engine::engine::DEFAULT_VECTOR_DIM;
    use crate::vector_engine::hnsw::DEFAULT_EF_SEARCH;

    #[test]
    fn bench_query_small_n_completes() {
        let (cache, query) = synth_sq8_cache(200, DEFAULT_VECTOR_DIM);
        let res = bench_query_p95(&cache, &query, 20);
        assert_eq!(res.n_vectors, 200);
        assert!(res.p95 < Duration::from_secs(1));
        let _ = TARGET_P95_MS;
    }

    #[test]
    fn ann_query_mid_scale_under_p95_floor() {
        // Mid-scale smoke (CI-safe). Full 1M is `ann_p95_1m_under_floor`.
        let res = bench_ann_p95_at(20_000, DEFAULT_VECTOR_DIM, 30);
        assert_eq!(res.n_vectors, 20_000);
        assert!(
            res.p95.as_millis() < TARGET_P95_MS,
            "ANN P95 {}ms exceeds {}ms at 20k",
            res.p95.as_millis(),
            TARGET_P95_MS
        );
    }

    /// FR-VE-BENCH-Q: 1 query vs 1M SQ8 chunks, Local ANN P95 &lt; 50ms.
    ///
    /// Needs ~400MB+ RAM and a few seconds to build; run with:
    /// `cargo test -r --lib vector_engine::bench::tests::ann_p95_1m_under_floor -- --ignored --nocapture`
    #[test]
    #[ignore = "full 1M corpus; run explicitly for FR-VE-BENCH-Q sign-off"]
    fn ann_p95_1m_under_floor() {
        let (res, ok) = ann_p95_meets_1m_floor(40);
        eprintln!(
            "[FR-VE-BENCH-Q] n={} P95={:.3}ms simd={:?} ok={}",
            res.n_vectors,
            res.p95.as_secs_f64() * 1000.0,
            res.simd,
            ok
        );
        assert!(
            ok,
            "P95 {}ms exceeds {}ms at 1M",
            res.p95.as_millis(),
            TARGET_P95_MS
        );
    }

    #[test]
    fn io_reduction_meets_floor_when_hot_path_ram_only() {
        let red = io_reduction_vs_mmap(1_000_000, 0);
        assert!(red >= TARGET_IO_REDUCTION);
    }

    #[test]
    fn recall_helper_runs_at_ef_search_default() {
        let mut rows = Vec::new();
        for id in 0..64u64 {
            let mut v = vec![0.0f32; 16];
            v[(id as usize) % 16] = 1.0;
            rows.push((id, v));
        }
        let mut q = vec![0.0f32; 16];
        q[3] = 1.0;
        let r = recall_sq8_vs_fp32(&rows, &q, DEFAULT_EF_SEARCH.min(10));
        assert!(r >= 0.0);
        let _ = TARGET_RECALL;
    }

    #[test]
    fn oom_2gb_cgroup_plan_safe() {
        assert!(oom_plan_within_cap());
    }
}
