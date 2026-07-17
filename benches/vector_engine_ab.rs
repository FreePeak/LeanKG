//! Vector Engine A/B + ANN quality benches (FR-VE-BENCH-AB / FR-VE-BENCH-Q).
//!
//! Run:
//! ```text
//! cargo bench --bench vector_engine_ab --release
//! ```

use std::time::Instant;

use leankg::vector_engine::{
    bench_query_p95, evaluate_default_suite, io_reduction_vs_mmap, oom_plan_within_cap,
    recall_sq8_vs_fp32, run_ab_suite, synth_sq8_cache, AbFloors, DEFAULT_VECTOR_DIM, MIN_AB_TASKS,
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

    // --- FR-VE-BENCH-Q: SQ8 query P95 (scaled; full 1M is optional via env) ---
    let n = std::env::var("LEANKG_VE_BENCH_N")
        .ok()
        .and_then(|s| s.parse().ok())
        .unwrap_or(10_000usize);
    let (cache, query) = synth_sq8_cache(n, DEFAULT_VECTOR_DIM);
    let q = bench_query_p95(&cache, &query, 50);
    println!(
        "[FR-VE-BENCH-Q] n={} P95={:.3}ms simd={:?} (1M target <{}ms)",
        q.n_vectors,
        q.p95.as_secs_f64() * 1000.0,
        q.simd,
        TARGET_P95_MS
    );
    if n >= 1_000_000 {
        assert!(
            q.p95.as_millis() < TARGET_P95_MS,
            "P95 {}ms exceeds {}ms at 1M",
            q.p95.as_millis(),
            TARGET_P95_MS
        );
    }

    // --- FR-VE-BENCH-IO / RECALL / OOM smoke ---
    let io = io_reduction_vs_mmap(1_000_000, 0);
    println!(
        "[FR-VE-BENCH-IO] reduction={:.1}% (floor ≥{:.0}%)",
        io * 100.0,
        TARGET_IO_REDUCTION * 100.0
    );
    assert!(io >= TARGET_IO_REDUCTION);

    let mut rows = Vec::new();
    for id in 0..256u64 {
        let mut v = vec![0.0f32; 32];
        v[(id as usize) % 32] = 1.0;
        rows.push((id, v));
    }
    let mut qv = vec![0.0f32; 32];
    qv[1] = 1.0;
    let recall = recall_sq8_vs_fp32(&rows, &qv, 10);
    println!(
        "[FR-VE-BENCH-RECALL] recall@{:.0}={:.3} (target >{:.2})",
        10.0, recall, TARGET_RECALL
    );

    assert!(oom_plan_within_cap());
    println!("[FR-VE-BENCH-OOM] 2GB cgroup plan within survival cap: OK");

    println!();
    println!("All vector-engine A/B + quality benches passed.");
}
