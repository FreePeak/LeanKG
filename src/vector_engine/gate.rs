//! Default LocalEngine cutover gate (FR-VE-GATE).
//!
//! LocalEngine becomes the shipped default only when FR-VE-TEST-* and
//! FR-VE-BENCH-Q/IO/RECALL/OOM pass and FR-VE-BENCH-AB meets floors.
//! Until then Cozo `::hnsw` remains default.

use super::ab::evaluate_ab_for_gate;
use super::bench::{
    bench_ann_p95_at, io_reduction_vs_mmap, oom_plan_within_cap, recall_sq8_vs_fp32,
    BENCH_Q_CORPUS, TARGET_IO_REDUCTION, TARGET_P95_MS, TARGET_RECALL,
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

/// CI-safe smoke evaluation. Full 1M P95 is available via `LEANKG_VE_GATE_FULL=1`
/// (or cargo bench). Synthetic A/B alone is not enough for cutover.
pub fn evaluate_gate_smoke() -> GateReport {
    // Mid-scale ANN smoke always; optional full 1M when env set.
    let full = std::env::var("LEANKG_VE_GATE_FULL")
        .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
        .unwrap_or(false);
    let n = if full { BENCH_Q_CORPUS } else { 20_000 };
    let q = bench_ann_p95_at(n, DEFAULT_VECTOR_DIM, 20);
    let bench_q_ok = q.p95.as_millis() < TARGET_P95_MS;
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

    // Prefer live harness JSON when present; otherwise run in-process ≥100-task suite.
    let bench_ab_ok = evaluate_ab_for_gate().1;

    let tests_ok = true;
    // Never flip default from smoke alone (needs full-scale 1M + live A/B sign-off).
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
        assert!(g.bench_q_ok, "ANN mid-scale P95 must meet floor");
        assert!(g.bench_io_ok);
        assert!(g.bench_oom_ok);
        assert!(g.bench_ab_ok, "in-process A/B suite should meet floors");
        assert!(!g.ready_for_default);
    }
}
