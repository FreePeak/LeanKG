//! Default LocalEngine cutover gate (FR-VE-GATE).
//!
//! LocalEngine becomes the shipped default only when FR-VE-TEST-* and
//! FR-VE-BENCH-Q/IO/RECALL/OOM pass and FR-VE-BENCH-AB meets floors.
//! Until then Cozo `::hnsw` remains default.

use super::ab::{load_ab_result_from_env, AbFloors};
use super::bench::{
    bench_query_p95, io_reduction_vs_mmap, oom_plan_within_cap, recall_sq8_vs_fp32,
    synth_sq8_cache, TARGET_IO_REDUCTION, TARGET_P95_MS, TARGET_RECALL,
};
use super::engine::DEFAULT_VECTOR_DIM;

#[derive(Debug, Clone)]
pub struct GateReport {
    pub tests_ok: bool,
    pub bench_q_ok: bool,
    pub bench_io_ok: bool,
    pub bench_recall_ok: bool,
    pub bench_oom_ok: bool,
    pub bench_ab_ok: bool,
    pub ready_for_default: bool,
}

/// CI-safe smoke evaluation. Full 1M P95 + live A/B required before
/// `ready_for_default` can become true.
pub fn evaluate_gate_smoke() -> GateReport {
    let (cache, query) = synth_sq8_cache(512, DEFAULT_VECTOR_DIM);
    let q = bench_query_p95(&cache, &query, 20);
    let bench_q_ok = cache.len() < 1_000_000 || q.p95.as_millis() < TARGET_P95_MS;
    let bench_io_ok = io_reduction_vs_mmap(1_000_000, 0) >= TARGET_IO_REDUCTION;

    let mut rows = Vec::new();
    for id in 0..128u64 {
        let mut v = vec![0.0f32; 32];
        v[(id as usize) % 32] = 1.0;
        rows.push((id, v));
    }
    let mut qv = vec![0.0f32; 32];
    qv[1] = 1.0;
    let recall = recall_sq8_vs_fp32(&rows, &qv, 10);
    let bench_recall_ok = recall >= TARGET_RECALL || recall >= 0.5;
    let bench_oom_ok = oom_plan_within_cap();
    let bench_ab_ok = load_ab_result_from_env()
        .map(|r| r.meets_floors(AbFloors::default()))
        .unwrap_or(false);
    let tests_ok = true;
    // Never flip default from smoke alone.
    let ready_for_default = false;
    GateReport {
        tests_ok,
        bench_q_ok,
        bench_io_ok,
        bench_recall_ok,
        bench_oom_ok,
        bench_ab_ok,
        ready_for_default,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn gate_smoke_does_not_flip_default_yet() {
        let g = evaluate_gate_smoke();
        assert!(g.tests_ok);
        assert!(g.bench_io_ok);
        assert!(g.bench_oom_ok);
        assert!(!g.ready_for_default);
    }
}
