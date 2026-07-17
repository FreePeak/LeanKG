//! End-to-end coverage for the Local-First Vector Engine (PRD §5.14 / P0 gate).
//!
//! Exercises factory → dual-write/recovery → SQ8 NSW ANN → KPI floors → A/B → gate
//! in one integration path (no embeddings feature required).
//!
//! ```bash
//! cargo test --release --test vector_engine_e2e -- --nocapture
//! ```

use leankg::vector_engine::{
    ab_result_to_json, assert_no_dangling_pointers, auto_tune_threads, bench_params,
    evaluate_ab_for_gate, evaluate_gate, evaluate_gate_smoke, measure_idle_rss_after_warm,
    measure_io_reduction, measure_time_to_context_p95, preferred_ann_backend, quantize_sq8,
    recall_meets_ef50_floor, recover_and_list, select_neighbors_heuristic, synth_query,
    write_ab_result_file, DualWriteEngine, EngineKind, Sq8Nsw, VectorEngineFactory, VectorStorage,
    WriteInput, DEFAULT_EF_SEARCH, DEFAULT_VECTOR_DIM, MIN_AB_TASKS, TARGET_IDLE_RSS_BYTES,
    TARGET_IO_REDUCTION, TARGET_P95_MS, TARGET_RECALL, TARGET_TTC_P95_MS,
};
use tempfile::TempDir;

fn sample_write(qn: &str, seed: u64) -> WriteInput {
    let mut vector = vec![0.0f32; DEFAULT_VECTOR_DIM];
    for (i, slot) in vector.iter_mut().enumerate() {
        *slot = (((seed as usize + i) % 11) as f32) * 0.05 - 0.2;
    }
    WriteInput {
        qualified_name: qn.into(),
        vector,
        payload: format!("payload:{qn}").into_bytes(),
    }
}

#[test]
fn e2e_factory_local_and_cloud_construct() {
    let dir = TempDir::new().unwrap();
    let local = VectorEngineFactory::open(
        EngineKind::Local,
        dir.path().join("local"),
        DEFAULT_VECTOR_DIM,
    )
    .expect("local");
    assert_eq!(local.kind(), EngineKind::Local);
    assert_eq!(local.dim(), DEFAULT_VECTOR_DIM);

    let cloud = VectorEngineFactory::open(
        EngineKind::Cloud,
        dir.path().join("cloud"),
        DEFAULT_VECTOR_DIM,
    )
    .expect("cloud");
    assert_eq!(cloud.kind(), EngineKind::Cloud);
}

#[test]
fn e2e_dual_write_recover_and_ann_search() {
    let dir = TempDir::new().unwrap();
    let mut eng = DualWriteEngine::open(dir.path(), EngineKind::Local, DEFAULT_VECTOR_DIM)
        .expect("open dual-write");

    for i in 0..32u64 {
        eng.write(sample_write(&format!("fn::{i}"), i))
            .expect("write");
    }
    assert_no_dangling_pointers(&eng).expect("no dangling");
    let nodes = recover_and_list(&mut eng).expect("recover");
    assert_eq!(nodes.len(), 32);

    // Build NSW from quantized payloads already in RAM cache.
    let graph = Sq8Nsw::synth_for_bench(2_048, DEFAULT_VECTOR_DIM, bench_params());
    let q = synth_query(DEFAULT_VECTOR_DIM, 3);
    let hits = graph.search(&q, 10, DEFAULT_EF_SEARCH);
    assert_eq!(hits.len(), 10);
    let ids = graph.search_ids(&q, 5, DEFAULT_EF_SEARCH);
    assert_eq!(ids.len(), 5);
}

#[test]
fn e2e_kpi_floors_and_io_recall() {
    let rss = measure_idle_rss_after_warm(50_000);
    assert!(
        rss.ok,
        "idle RSS {} exceeds {}",
        rss.rss_bytes, TARGET_IDLE_RSS_BYTES
    );

    let ttc = measure_time_to_context_p95(20_000, 30);
    assert!(
        ttc.ok,
        "TTC P95 {}ms exceeds {}ms",
        ttc.p95.as_millis(),
        TARGET_TTC_P95_MS
    );
    assert!(ttc.payload_bytes > 0);

    let io = measure_io_reduction(1_000_000, 10);
    assert!(io.meets_floor());
    assert!(io.reduction >= TARGET_IO_REDUCTION);

    let (recall, ok) = recall_meets_ef50_floor();
    assert!(ok, "recall {recall} < {TARGET_RECALL}");
}

#[test]
fn e2e_ab_suite_meets_floors_and_writes_artifact() {
    let (result, ok, source) = evaluate_ab_for_gate();
    assert_eq!(source, "in_process_suite");
    assert!(ok, "A/B floors missed: {result:?}");
    assert!(result.token_reduction >= 0.60);
    assert!(result.tool_call_reduction >= 0.80);
    assert!(result.speedup >= 2.0);
    assert!(result.success_ge_baseline);

    let dir = TempDir::new().unwrap();
    let path = dir.path().join("ab.json");
    write_ab_result_file(&path, &result).expect("write");
    let raw = std::fs::read_to_string(&path).unwrap();
    assert!(raw.contains("token_reduction"));
    let roundtrip = ab_result_to_json(&result);
    assert!(roundtrip.contains("\"min_tasks\":"));
    assert!(roundtrip.contains(&MIN_AB_TASKS.to_string()));
}

#[test]
fn e2e_gate_smoke_keeps_cozo_default() {
    // Ensure FULL flag is off for this assertion.
    let prev = std::env::var_os("LEANKG_VE_GATE_FULL");
    std::env::remove_var("LEANKG_VE_GATE_FULL");
    let g = evaluate_gate_smoke();
    assert!(g.bench_q_ok);
    assert!(g.bench_io_ok);
    assert!(g.bench_recall_ok);
    assert!(g.bench_oom_ok);
    assert!(g.bench_ab_ok);
    assert!(!g.ready_for_default);
    assert_eq!(preferred_ann_backend(&g), "cozo_hnsw");
    match prev {
        Some(v) => std::env::set_var("LEANKG_VE_GATE_FULL", v),
        None => std::env::remove_var("LEANKG_VE_GATE_FULL"),
    }
}

#[test]
fn e2e_hnsw_select_and_quantize_smoke() {
    let (sq8, scale) = quantize_sq8(&[0.0, 0.5, -1.0, 0.25]);
    assert_eq!(sq8.len(), 4);
    assert!(scale > 0.0);

    let cands: Vec<(u64, f32)> = (0..40).map(|i| (i, i as f32)).collect();
    let dist = |_a: u64, _b: u64| 0.0f32;
    let picked = select_neighbors_heuristic(&cands, 16, &dist);
    assert!(!picked.is_empty());
    assert!(picked.len() <= 16);

    let plan = auto_tune_threads(EngineKind::Local);
    assert!(plan.worker_threads >= 1);
}

/// Full cutover path (1M). Run explicitly:
/// `LEANKG_VE_GATE_FULL=1 cargo test --release --test vector_engine_e2e gate_full -- --ignored --nocapture`
#[test]
#[ignore = "full 1M gate; set LEANKG_VE_GATE_FULL=1"]
fn e2e_gate_full_ready_for_default() {
    std::env::set_var("LEANKG_VE_GATE_FULL", "1");
    let g = evaluate_gate();
    eprintln!(
        "[e2e FR-VE-GATE] ready={} backend={} q_ok={} p95_target_ms={}",
        g.ready_for_default,
        preferred_ann_backend(&g),
        g.bench_q_ok,
        TARGET_P95_MS
    );
    assert!(g.ready_for_default);
    assert_eq!(preferred_ann_backend(&g), "local_engine");
    std::env::remove_var("LEANKG_VE_GATE_FULL");
}
