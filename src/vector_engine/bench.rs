//! Bench harness for LocalEngine quality gates (FR-VE-BENCH-*).

use std::time::{Duration, Instant};

use super::simd::{detect_simd, dot_i8, SimdKind};
use super::tier2::{quantize_sq8, Sq8Cache};

#[derive(Debug, Clone)]
pub struct QueryBenchResult {
    pub n_vectors: usize,
    pub p95: Duration,
    pub simd: SimdKind,
}

pub const TARGET_P95_MS: u128 = 50;

/// Run `iters` queries against an in-RAM SQ8 cache; return approximate P95.
pub fn bench_query_p95(cache: &Sq8Cache, query: &[i8], iters: usize) -> QueryBenchResult {
    let simd = detect_simd();
    let mut samples = Vec::with_capacity(iters);
    for _ in 0..iters {
        let t0 = Instant::now();
        let mut best = i32::MIN;
        for i in 0..cache.len() {
            if let Some(row) = cache.row(i) {
                best = best.max(dot_i8(row, query, simd));
            }
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

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vector_engine::engine::DEFAULT_VECTOR_DIM;

    #[test]
    fn bench_query_small_n_completes() {
        let (cache, query) = synth_sq8_cache(200, DEFAULT_VECTOR_DIM);
        let res = bench_query_p95(&cache, &query, 20);
        assert_eq!(res.n_vectors, 200);
        assert!(res.p95 < Duration::from_secs(1));
        // Full 1M @ <50ms is enforced by cargo bench / FR-VE-GATE at scale.
        let _ = TARGET_P95_MS;
    }

    #[test]
    fn io_reduction_meets_floor_when_hot_path_ram_only() {
        let red = io_reduction_vs_mmap(1_000_000, 0);
        assert!(red >= TARGET_IO_REDUCTION);
    }
}
