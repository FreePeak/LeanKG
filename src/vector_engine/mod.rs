//! Optimized Local-First Vector Graph Engine (PRD §5.14 / FR-VE-*).
//!
//! Three-tier storage behind a static enum dispatch factory:
//! - Tier 1: graph topology (RocksDB Local / TiKV Cloud)
//! - Tier 2: SQ8/INT8 vectors fully in RAM
//! - Tier 3: flat binary FP32 + source payload
//!
//! Cozo `::hnsw` remains the shipped default until FR-VE-GATE. Select this
//! engine with `LEANKG_VECTOR_ENGINE=local|cloud`.

pub mod ab;
pub mod ann;
pub mod bench;
pub mod dual_write;
pub mod engine;
pub mod gate;
pub mod gc;
pub mod hnsw;
pub mod kpi;
pub mod memory;
pub mod recovery;
pub mod simd;
pub mod threads;
pub mod tier1;
pub mod tier2;
pub mod tier3;

pub use ab::{
    ab_result_to_json, evaluate_ab_for_gate, evaluate_default_suite, load_ab_result_from_env,
    load_ab_result_from_file, run_ab_suite, simulate_task, write_ab_result_file, AbFloors,
    AbResult, AbSuiteReport, AbTaskOutcome, MIN_AB_TASKS,
};
pub use ann::{bench_params, synth_query, Sq8Nsw};
pub use bench::{
    ann_p95_meets_1m_floor, bench_ann_p95_at, bench_ann_query_p95, bench_query_p95,
    estimate_local_engine_heap_bytes, io_reduction_vs_mmap, measure_io_reduction,
    oom_1m_corpus_within_2gb, oom_plan_within_cap, recall_meets_ef50_floor,
    recall_sq8_at_ef_search, recall_sq8_vs_fp32, synth_sq8_cache, IoReductionReport,
    QueryBenchResult, BENCH_Q_CORPUS, TARGET_IO_REDUCTION, TARGET_P95_MS, TARGET_RECALL,
};
pub use dual_write::{DualWriteEngine, WriteInput};
pub use engine::{
    CloudEngine, EngineKind, LocalEngine, VectorEngine, VectorEngineError, VectorEngineFactory,
    VectorStorage, DEFAULT_VECTOR_DIM, ENV_VECTOR_ENGINE,
};
pub use gate::{evaluate_gate_smoke, GateReport};
pub use gc::{compact_shadow, fragmentation_ratio, maybe_gc, FRAGMENTATION_TRIGGER};
pub use hnsw::{
    brute_force_topk, recall_at_k, select_neighbors_heuristic, HnswParams, DEFAULT_EF_CONSTRUCTION,
    DEFAULT_EF_SEARCH, DEFAULT_M, M_MAX, M_MIN,
};
pub use kpi::{
    build_context_payload, current_process_rss_bytes, measure_idle_rss_after_warm,
    measure_time_to_context_p95, IdleRssReport, TimeToContextReport, TARGET_IDLE_RSS_BYTES,
    TARGET_TTC_P95_MS,
};
pub use memory::{
    auto_tune_block_cache, available_memory_bytes, plan_block_cache, plan_under_2gb_cgroup,
    MemoryPlan, LOCAL_SURVIVAL_CAP_BYTES,
};
pub use recovery::{assert_no_dangling_pointers, recover_and_list};
pub use simd::{detect_simd, dot_i8, dot_i8_auto, SimdKind};
pub use threads::{auto_tune_threads, build_rayon_pool, plan_threads, ThreadPlan};
pub use tier1::{
    BlockTableFactory, HnswAdjacency, RocksCompression, RocksDbLocalOptions, TopologyNode,
    TopologyStore,
};
pub use tier2::{dot_i8_scalar, quantize_sq8, Sq8Cache};
pub use tier3::{FlatPayloadFile, PayloadRecord, RECORD_HEADER_SIZE};

mod dw_test;
mod factory_test;
mod simd_test;
