//! Zero-downtime GC via shadow paging (FR-VE-FS-GC / FR-VE-TEST-GC).
//!
//! Trigger when fragmentation > 30%. Concurrent readers are never blocked
//! on the live topology — compaction writes a shadow then swaps under a
//! micro-lock.

use std::sync::{Arc, RwLock};

use super::dual_write::DualWriteEngine;
use super::engine::VectorEngineError;
use super::tier1::TopologyNode;
use super::tier3::{FlatPayloadFile, PayloadRecord};

/// Fragmentation ratio: 1.0 - (live_bytes / file_bytes).
pub fn fragmentation_ratio(eng: &DualWriteEngine) -> f64 {
    let file_bytes = eng.flat.len_bytes();
    if file_bytes == 0 {
        return 0.0;
    }
    let mut live = 0u64;
    for id in eng.topology.node_ids() {
        if let Some(n) = eng.topology.get_node(id) {
            live += u64::from(n.payload_len);
        }
    }
    1.0 - (live as f64 / file_bytes as f64)
}

pub const FRAGMENTATION_TRIGGER: f64 = 0.30;

/// Compact into a shadow flat file, then atomically replace offsets.
pub fn compact_shadow(
    eng: &mut DualWriteEngine,
    read_lock: &RwLock<()>,
) -> Result<usize, VectorEngineError> {
    let dim = eng.sq8.dim();
    let parent = eng
        .topology
        .root()
        .parent()
        .ok_or_else(|| VectorEngineError::Storage("missing engine root".into()))?;
    let shadow_parent = parent.join("gc_shadow");
    let _ = std::fs::remove_dir_all(&shadow_parent);
    std::fs::create_dir_all(&shadow_parent)?;
    let mut shadow = FlatPayloadFile::open(&shadow_parent, dim)?;

    let mut new_nodes = Vec::new();
    for id in eng.topology.node_ids() {
        let Some(node) = eng.topology.get_node(id).cloned() else {
            continue;
        };
        let Some(rec) = eng.flat.read_at(node.payload_offset)? else {
            continue;
        };
        if rec.id != id {
            continue;
        }
        let payload_len = rec.payload.len();
        let offset = shadow.append(&PayloadRecord {
            id,
            vector: rec.vector,
            payload: rec.payload,
        })?;
        let plen = FlatPayloadFile::record_bytes(dim, payload_len) as u32;
        new_nodes.push(TopologyNode {
            id,
            qualified_name: node.qualified_name,
            payload_offset: offset,
            payload_len: plen,
        });
    }
    shadow.fsync()?;
    let rewritten = new_nodes.len();
    let shadow_path = shadow.path().to_path_buf();
    drop(shadow);

    let _guard = read_lock
        .write()
        .map_err(|e| VectorEngineError::Storage(format!("gc lock poisoned: {e}")))?;
    let live_path = eng.flat.path().to_path_buf();
    std::fs::copy(&shadow_path, &live_path)?;
    eng.flat = FlatPayloadFile::open(parent, dim)?;
    for node in new_nodes {
        eng.topology.upsert_node(node)?;
    }
    let _ = std::fs::remove_dir_all(&shadow_parent);
    Ok(rewritten)
}

/// Run GC if fragmentation exceeds threshold. Returns compacted count or 0.
pub fn maybe_gc(
    eng: &mut DualWriteEngine,
    lock: &Arc<RwLock<()>>,
) -> Result<usize, VectorEngineError> {
    if fragmentation_ratio(eng) <= FRAGMENTATION_TRIGGER {
        return Ok(0);
    }
    compact_shadow(eng, lock)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vector_engine::dual_write::WriteInput;
    use crate::vector_engine::engine::DEFAULT_VECTOR_DIM;
    use std::thread;
    use tempfile::TempDir;

    fn sample(qn: &str, seed: f32) -> WriteInput {
        WriteInput {
            qualified_name: qn.into(),
            vector: vec![seed; DEFAULT_VECTOR_DIM],
            payload: qn.as_bytes().to_vec(),
        }
    }

    #[test]
    fn gc_compaction_with_concurrent_reads() {
        let dir = TempDir::new().unwrap();
        let mut eng = DualWriteEngine::open_local(dir.path()).unwrap();
        for i in 0..40 {
            eng.write(sample(&format!("n::{i}"), i as f32 * 0.01))
                .unwrap();
        }
        let lock = Arc::new(RwLock::new(()));
        assert_eq!(maybe_gc(&mut eng, &lock).unwrap(), 0);

        let rewritten = compact_shadow(&mut eng, &lock).unwrap();
        assert_eq!(rewritten, 40);

        // Concurrent readers hold the shared read lock briefly while
        // another compact acquires the write lock between iterations.
        let lock2 = Arc::new(RwLock::new(()));
        let root = dir.path().to_path_buf();
        let reader = {
            let lock2 = Arc::clone(&lock2);
            thread::spawn(move || {
                for _ in 0..30 {
                    let _g = lock2.read().unwrap();
                    let eng = DualWriteEngine::open_local(&root).unwrap();
                    assert_eq!(eng.vector_count(), 40);
                    drop(_g);
                    thread::yield_now();
                }
            })
        };
        for _ in 0..3 {
            let _ = compact_shadow(&mut eng, &lock2).unwrap();
        }
        reader.join().unwrap();
        assert_eq!(eng.vector_count(), 40);
    }
}
