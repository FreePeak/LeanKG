//! Vector Engine A/B + ANN quality benches (FR-VE-BENCH-AB / FR-VE-BENCH-Q).
//!
//! Run:
//! ```text
//! cargo bench --bench vector_engine_ab
//! ```
//!
//! Writes:
//! - `target/vector_engine_ab_result.json` (gate injection via `LEANKG_VE_AB_FILE`)
//! - `docs/benchmarks/vector_engine_gate_results.json` (tracked A/B + quality report)

use std::time::Instant;

use leankg::vector_engine::{
    ann_p95_meets_1m_floor, bench_ann_p95_at, bench_query_p95, evaluate_ab_for_gate,
    evaluate_default_suite, measure_idle_rss_after_warm, measure_io_reduction,
    measure_time_to_context_p95, oom_1m_corpus_within_2gb, oom_plan_within_cap,
    recall_meets_ef50_floor, run_ab_suite, synth_sq8_cache, write_ab_result_file, AbFloors,
    BENCH_Q_CORPUS, DEFAULT_VECTOR_DIM, MIN_AB_TASKS, TARGET_IDLE_RSS_BYTES, TARGET_IO_REDUCTION,
    TARGET_P95_MS, TARGET_RECALL, TARGET_TTC_P95_MS,
};
use serde_json::json;

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

    // Persist artifact for FR-VE-GATE / live injection (`LEANKG_VE_AB_FILE`).
    let out = std::env::var("LEANKG_VE_AB_OUT")
        .unwrap_or_else(|_| "target/vector_engine_ab_result.json".into());
    if let Some(parent) = std::path::Path::new(&out).parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    write_ab_result_file(&out, &result).expect("write A/B JSON artifact");
    println!("[FR-VE-BENCH-AB] wrote artifact {out}");
    let (gate_result, gate_ok, source) = evaluate_ab_for_gate();
    println!(
        "[FR-VE-BENCH-AB] gate_source={source} meets_floors={gate_ok} token={:.1}% tool={:.1}% speedup={:.2}×",
        gate_result.token_reduction * 100.0,
        gate_result.tool_call_reduction * 100.0,
        gate_result.speedup
    );
    assert!(gate_ok, "evaluate_ab_for_gate failed via {source}");

    // Throughput: time to run 1000 simulated tasks
    let t1 = Instant::now();
    let big = run_ab_suite(1_000);
    let big_ms = t1.elapsed().as_secs_f64() * 1000.0;
    println!(
        "[FR-VE-BENCH-AB] 1000-task suite wall={:.2}ms  token_reduction={:.1}%",
        big_ms,
        big.token_reduction() * 100.0
    );

    // --- US-VE-01 / US-VE-02 KPI samples (before 1M ANN so RSS is not polluted) ---
    let rss = measure_idle_rss_after_warm(100_000);
    println!(
        "[US-VE-01] rss_after_warm={} bytes (floor <{}) ok={}",
        rss.rss_bytes, TARGET_IDLE_RSS_BYTES, rss.ok
    );
    assert!(
        rss.ok,
        "idle RSS {} exceeds floor {} (run KPI before 1M ANN)",
        rss.rss_bytes, TARGET_IDLE_RSS_BYTES
    );
    let ttc = measure_time_to_context_p95(50_000, 40);
    println!(
        "[US-VE-02] ttc_p95={:.3}ms payload={}B (floor <{}ms) ok={}",
        ttc.p95.as_secs_f64() * 1000.0,
        ttc.payload_bytes,
        TARGET_TTC_P95_MS,
        ttc.ok
    );
    assert!(ttc.ok);

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

    // --- FR-VE-BENCH-Q: ANN over SQ8 last (default 1M; override with LEANKG_VE_BENCH_N) ---
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

    // Tracked report for PRD / tracker evidence.
    let report_path = std::env::var("LEANKG_VE_RESULTS_OUT")
        .unwrap_or_else(|_| "docs/benchmarks/vector_engine_gate_results.json".into());
    if let Some(parent) = std::path::Path::new(&report_path).parent() {
        let _ = std::fs::create_dir_all(parent);
    }
    let full = json!({
        "generated_unix_secs": unix_secs(),
        "crate_version": env!("CARGO_PKG_VERSION"),
        "ab": {
            "tasks": report.tasks,
            "token_reduction": result.token_reduction,
            "tool_call_reduction": result.tool_call_reduction,
            "speedup": result.speedup,
            "success_ge_baseline": result.success_ge_baseline,
            "baseline_tokens": report.baseline_tokens,
            "engine_tokens": report.engine_tokens,
            "baseline_tool_calls": report.baseline_tool_calls,
            "engine_tool_calls": report.engine_tool_calls,
            "baseline_ms": report.baseline_ms,
            "engine_ms": report.engine_ms,
            "wall_ms": ab_wall.as_millis() as u64,
            "suite_1000_wall_ms": big_ms,
            "suite_1000_token_reduction": big.token_reduction(),
            "meets_floors": ok,
            "artifact": out,
        },
        "ann_query": {
            "n": q.n_vectors,
            "p95_ms": q.p95.as_secs_f64() * 1000.0,
            "simd": format!("{:?}", q.simd),
            "target_p95_ms": TARGET_P95_MS,
            "ok": q.p95.as_millis() < TARGET_P95_MS,
        },
        "io": {
            "legacy_disk_touches": io.legacy_disk_touches,
            "hot_disk_touches": io.hot_disk_touches,
            "reduction": io.reduction,
            "target": TARGET_IO_REDUCTION,
            "ok": io.meets_floor(),
        },
        "recall": {
            "recall_at_ef50": recall,
            "target": TARGET_RECALL,
            "ok": recall_ok,
        },
        "oom": {
            "estimated_1m_heap_bytes": heap,
            "within_2gb": oom_ok,
        },
        "kpi": {
            "idle_rss_bytes": rss.rss_bytes,
            "idle_rss_ok": rss.ok,
            "ttc_p95_ms": ttc.p95.as_secs_f64() * 1000.0,
            "ttc_ok": ttc.ok,
        }
    });
    std::fs::write(
        &report_path,
        serde_json::to_string_pretty(&full).expect("serialize results"),
    )
    .expect("write results report");
    println!("[report] wrote {report_path}");

    println!();
    println!("All vector-engine A/B + quality benches passed.");
}

fn unix_secs() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs())
        .unwrap_or(0)
}
