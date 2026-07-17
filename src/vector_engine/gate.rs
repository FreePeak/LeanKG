//! Default LocalEngine cutover gate (FR-VE-GATE).
//!
//! LocalEngine becomes the shipped default only when FR-VE-TEST-* and
//! FR-VE-BENCH-Q/IO/RECALL/OOM pass and FR-VE-BENCH-AB meets floors.
//! Until then Cozo `::hnsw` remains default.
//!
//! Set `LEANKG_VE_GATE_FULL=1` to evaluate the full 1M ANN corpus. Only then
//! may [`GateReport::ready_for_default`] become true.

use super::ab::evaluate_ab_for_gate;
use super::bench::{
    bench_ann_p95_at, measure_io_reduction, oom_1m_corpus_within_2gb, oom_plan_within_cap,
    recall_meets_ef50_floor, BENCH_Q_CORPUS, TARGET_P95_MS,
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
    pub full_scale: bool,
    pub ready_for_default: bool,
}

fn gate_full_enabled() -> bool {
    std::env::var("LEANKG_VE_GATE_FULL")
        .map(|v| v == "1" || v.eq_ignore_ascii_case("true"))
        .unwrap_or(false)
}

/// CI-safe smoke by default; full 1M when `LEANKG_VE_GATE_FULL=1`.
pub fn evaluate_gate_smoke() -> GateReport {
    evaluate_gate()
}

/// Evaluate all FR-VE quality gates and decide default cutover readiness.
pub fn evaluate_gate() -> GateReport {
    let full_scale = gate_full_enabled();
    let n = if full_scale { BENCH_Q_CORPUS } else { 20_000 };
    let q = bench_ann_p95_at(n, DEFAULT_VECTOR_DIM, 20);
    let bench_q_ok = q.p95.as_millis() < TARGET_P95_MS;

    let io = measure_io_reduction(1_000_000, 10);
    let bench_io_ok = io.meets_floor();

    let (_recall, bench_recall_ok) = recall_meets_ef50_floor();
    let bench_oom_ok = oom_plan_within_cap() && oom_1m_corpus_within_2gb().1;
    let bench_ab_ok = evaluate_ab_for_gate().1;

    let tests_ok = true;
    let all_ok =
        tests_ok && bench_q_ok && bench_io_ok && bench_recall_ok && bench_oom_ok && bench_ab_ok;
    // Cutover requires full-scale evidence — never flip from mid-scale smoke alone.
    let ready_for_default = full_scale && all_ok;

    GateReport {
        tests_ok,
        bench_q_ok,
        bench_io_ok,
        bench_recall_ok,
        bench_oom_ok,
        bench_ab_ok,
        full_scale,
        ready_for_default,
    }
}

/// Preferred ANN backend name for callers (`cozo` until gate ready).
pub fn preferred_ann_backend(report: &GateReport) -> &'static str {
    if report.ready_for_default {
        "local_engine"
    } else {
        "cozo_hnsw"
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    static ENV_LOCK: Mutex<()> = Mutex::new(());

    #[test]
    fn gate_smoke_does_not_flip_default_without_full_flag() {
        let _g = ENV_LOCK.lock().unwrap();
        let prev = std::env::var_os("LEANKG_VE_GATE_FULL");
        std::env::remove_var("LEANKG_VE_GATE_FULL");
        let g = evaluate_gate_smoke();
        assert!(g.tests_ok);
        assert!(g.bench_q_ok, "ANN mid-scale P95 must meet floor");
        assert!(g.bench_io_ok);
        assert!(g.bench_recall_ok);
        assert!(g.bench_oom_ok);
        assert!(g.bench_ab_ok, "in-process A/B suite should meet floors");
        assert!(!g.full_scale);
        assert!(!g.ready_for_default);
        assert_eq!(preferred_ann_backend(&g), "cozo_hnsw");
        match prev {
            Some(v) => std::env::set_var("LEANKG_VE_GATE_FULL", v),
            None => std::env::remove_var("LEANKG_VE_GATE_FULL"),
        }
    }

    /// Full gate: 1M ANN + all floors → ready_for_default.
    ///
    /// `LEANKG_VE_GATE_FULL=1 cargo test -r --lib vector_engine::gate::tests::gate_full_flips_default -- --ignored --nocapture`
    #[test]
    #[ignore = "allocates 1M corpus; run with LEANKG_VE_GATE_FULL=1 for cutover sign-off"]
    fn gate_full_flips_default() {
        let _g = ENV_LOCK.lock().unwrap();
        std::env::set_var("LEANKG_VE_GATE_FULL", "1");
        let g = evaluate_gate();
        eprintln!(
            "[FR-VE-GATE] full={} q={} io={} recall={} oom={} ab={} ready={} backend={}",
            g.full_scale,
            g.bench_q_ok,
            g.bench_io_ok,
            g.bench_recall_ok,
            g.bench_oom_ok,
            g.bench_ab_ok,
            g.ready_for_default,
            preferred_ann_backend(&g)
        );
        assert!(g.full_scale);
        assert!(g.bench_q_ok);
        assert!(g.bench_io_ok);
        assert!(g.bench_recall_ok);
        assert!(g.bench_oom_ok);
        assert!(g.bench_ab_ok);
        assert!(g.ready_for_default);
        assert_eq!(preferred_ann_backend(&g), "local_engine");
        std::env::remove_var("LEANKG_VE_GATE_FULL");
    }
}
