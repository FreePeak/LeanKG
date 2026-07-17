//! Optimized Local-First Vector Graph Engine (PRD §5.14 / FR-VE-*).
//!
//! Three-tier storage behind a static enum dispatch factory:
//! - Tier 1: graph topology (RocksDB Local / TiKV Cloud)
//! - Tier 2: SQ8/INT8 vectors fully in RAM
//! - Tier 3: flat binary FP32 + source payload
//!
//! Cozo `::hnsw` remains the shipped default until FR-VE-GATE. Select this
//! engine with `LEANKG_VECTOR_ENGINE=local|cloud`.

pub mod engine;
pub mod tier1;
pub mod tier2;
pub mod tier3;

pub use engine::{
    CloudEngine, EngineKind, LocalEngine, VectorEngine, VectorEngineError, VectorEngineFactory,
    VectorStorage, DEFAULT_VECTOR_DIM, ENV_VECTOR_ENGINE,
};
pub use tier1::{
    BlockTableFactory, HnswAdjacency, RocksCompression, RocksDbLocalOptions, TopologyNode,
    TopologyStore,
};
pub use tier2::{dot_i8_scalar, quantize_sq8, Sq8Cache};
pub use tier3::{FlatPayloadFile, PayloadRecord, RECORD_HEADER_SIZE};
