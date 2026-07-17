//! Vector Engine A/B + ANN quality benches (FR-VE-BENCH-AB / FR-VE-BENCH-Q).
//!
//! Run:
//! ```text
//! cargo bench --bench vector_engine_ab --release
//! ```

use std::time::Instant;

use leankg::vector_engine::{
    ann_p95_meets_1m_floor, bench_ann_p95_at, bench_query_p95, evaluate_default_suite,
    measure_io_reduction, oom_1m_corpus_within_2gb, oom_plan_within_cap, recall_meets_ef50_floor,
    run_ab_suite, synth_sq8_cache, AbFloors, BENCH_Q_CORPUS, DEFAULT_VECTOR_DIM, MIN_AB_TASKS,
    TARGET_IO_REDUCTION, TARGET_P95_MS, TARGET_RECALL,
};

fn main() {
    println!("============================================================");
    println!(" LeanKG Vector Engine — A/B + Quality Benches");
    println!("============================================================");
    println!();

    // --- FR-VE-BENCH-AB: ≥100 synthetic agent tasks ---
    let t0 = Instant::now();
    let (report, result, ok) = evaluate_default_suite();
    let ab_wall = t0.elapsed();
    println!("[FR-VE-BENCH-AB] suite tasks={}", report.tasks);
    println!(
        "  tokens:     baseline={} engine={}  reduction={:.1}% (floor ≥60%)",
        report.baseline_tokens,
        report.engine_tokens,
        result.token_reduction * 100.0
    );
    println!(
        "  tool calls: baseline={} engine={}  reduction={:.1}% (floor ≥80%)",
        report.baseline_tool_calls,
        report.engine_tool_calls,
        result.tool_call_reduction * 100.0
    );
    println!(
        "  latency:    baseline={}ms engine={}ms  speedup={:.2}× (floor ≥2×)",
        report.baseline_ms, report.engine_ms, result.speedup
    );
    println!(
        "  success:    baseline={} engine={}  ge_baseline={}",
        report.baseline_successes, report.engine_successes, result.success_ge_baseline
    );
    println!(
        "  wall: {:.2}ms  meets_floors={}",
        ab_wall.as_secs_f64() * 1000.0,
        ok
    );
    assert!(
        report.tasks >= MIN_AB_TASKS,
        "A/B suite must run ≥{MIN_AB_TASKS} tasks"
    );
    assert!(ok, "A/B suite failed PRD floors: {:?}", AbFloors::default());

    // Throughput: time to run 1000 simulated tasks
    let t1 = Instant::now();
    let big = run_ab_suite(1_000);
    let big_ms = t1.elapsed().as_secs_f64() * 1000.0;
    println!(
        "[FR-VE-BENCH-AB] 1000-task suite wall={:.2}ms  token_reduction={:.1}%",
        big_ms,
        big.token_reduction() * 100.0
    );

    // --- FR-VE-BENCH-Q: ANN over SQ8 (default 1M; override with LEANKG_VE_BENCH_N) ---
    let n = std::env::var("LEANKG_VE_BENCH_N")
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(BENCH_Q_CORPUS);
    let q = if n >= BENCH_Q_CORPUS {
        let (res, ok) = ann_p95_meets_1m_floor(40);
        println!(
            "[FR-VE-BENCH-Q] ANN n={} P95={:.3}ms simd={:?} (target <{}ms) ok={}",
            res.n_vectors,
            res.p95.as_secs_f64() * 1000.0,
            res.simd,
            TARGET_P95_MS,
            ok
        );
        assert!(
            ok,
            "P95 {}ms exceeds {}ms at 1M",
            res.p95.as_millis(),
            TARGET_P95_MS
        );
        res
    } else {
        let res = bench_ann_p95_at(n, DEFAULT_VECTOR_DIM, 40);
        println!(
            "[FR-VE-BENCH-Q] ANN n={} P95={:.3}ms simd={:?} (1M target <{}ms)",
            res.n_vectors,
            res.p95.as_secs_f64() * 1000.0,
            res.simd,
            TARGET_P95_MS
        );
        assert!(
            res.p95.as_millis() < TARGET_P95_MS,
            "P95 {}ms exceeds {}ms at n={}",
            res.p95.as_millis(),
            TARGET_P95_MS,
            n
        );
        res
    };
    // Keep a small flat-scan microbench for regression visibility.
    let (cache, query) = synth_sq8_cache(2_000.min(n), DEFAULT_VECTOR_DIM);
    let flat = bench_query_p95(&cache, &query, 20);
    println!(
        "[FR-VE-BENCH-Q] flat-scan micro n={} P95={:.3}ms (informational)",
        flat.n_vectors,
        flat.p95.as_secs_f64() * 1000.0
    );
    let _ = q;

    // --- FR-VE-BENCH-IO / RECALL / OOM ---
    let io = measure_io_reduction(1_000_000, 10);
    println!(
        "[FR-VE-BENCH-IO] legacy={} hot={} reduction={:.1}% (floor ≥{:.0}%)",
        io.legacy_disk_touches,
        io.hot_disk_touches,
        io.reduction * 100.0,
        TARGET_IO_REDUCTION * 100.0
    );
    assert!(io.meets_floor());

    let (recall, recall_ok) = recall_meets_ef50_floor();
    println!(
        "[FR-VE-BENCH-RECALL] recall@efSearch=50={:.3} (target >{:.2}) ok={}",
        recall, TARGET_RECALL, recall_ok
    );
    assert!(recall_ok, "SQ8 recall {recall:.3} below floor");

    assert!(oom_plan_within_cap());
    let (heap, oom_ok) = oom_1m_corpus_within_2gb();
    println!("[FR-VE-BENCH-OOM] estimated_1M_heap={heap} bytes within_2gb={oom_ok}");
    assert!(oom_ok, "1M LocalEngine heap estimate exceeds 2GB");

    println!();
    println!("All vector-engine A/B + quality benches passed.");
}
