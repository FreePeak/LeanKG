//! Optimized Local-First Vector Graph Engine (PRD §5.14 / FR-VE-*).
//!
//! Three-tier storage behind a static enum dispatch factory:
//! - Tier 1: graph topology (RocksDB Local / TiKV Cloud)
//! - Tier 2: SQ8/INT8 vectors fully in RAM
//! - Tier 3: flat binary FP32 + source payload
//!
//! Cozo `::hnsw` remains the shipped default until FR-VE-GATE. Select this
//! engine with `LEANKG_VECTOR_ENGINE=local|cloud`.

pub mod dual_write;
pub mod engine;
pub mod gc;
pub mod hnsw;
pub mod memory;
pub mod recovery;
pub mod simd;
pub mod threads;
pub mod tier1;
pub mod tier2;
pub mod tier3;

pub use dual_write::{DualWriteEngine, WriteInput};
pub use engine::{
    CloudEngine, EngineKind, LocalEngine, VectorEngine, VectorEngineError, VectorEngineFactory,
    VectorStorage, DEFAULT_VECTOR_DIM, ENV_VECTOR_ENGINE,
};
pub use gc::{compact_shadow, fragmentation_ratio, maybe_gc, FRAGMENTATION_TRIGGER};
pub use hnsw::{
    brute_force_topk, recall_at_k, select_neighbors_heuristic, HnswParams, DEFAULT_EF_CONSTRUCTION,
    DEFAULT_EF_SEARCH, DEFAULT_M, M_MAX, M_MIN,
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
