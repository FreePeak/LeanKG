//! Crash recovery for dual-write (FR-VE-FS-REC).
//!
//! Invariant: after recover, every Tier-3 byte range referenced by Tier-1
//! is complete, and no Tier-1 offset points past the flat-file end.
//! Uncommitted flat tails are truncated — **no dangling pointers**.

use super::dual_write::DualWriteEngine;
use super::engine::VectorEngineError;
use super::tier1::TopologyNode;

/// Verify Tier-1 offsets are consistent with the flat file length.
pub fn assert_no_dangling_pointers(eng: &DualWriteEngine) -> Result<(), VectorEngineError> {
    let flat_len = eng.flat.len_bytes();
    for id in eng.topology.node_ids() {
        let Some(node) = eng.topology.get_node(id) else {
            continue;
        };
        let end = node.payload_offset + u64::from(node.payload_len);
        if end > flat_len {
            return Err(VectorEngineError::Storage(format!(
                "dangling pointer: node {id} end {end} > flat_len {flat_len}"
            )));
        }
    }
    Ok(())
}

/// Re-run recovery and return committed node snapshots.
pub fn recover_and_list(eng: &mut DualWriteEngine) -> Result<Vec<TopologyNode>, VectorEngineError> {
    eng.recover()?;
    assert_no_dangling_pointers(eng)?;
    let mut nodes = Vec::new();
    for id in eng.topology.node_ids() {
        if let Some(n) = eng.topology.get_node(id) {
            nodes.push(n.clone());
        }
    }
    nodes.sort_by_key(|n| n.id);
    Ok(nodes)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::vector_engine::dual_write::{DualWriteEngine, WriteInput};
    use crate::vector_engine::engine::DEFAULT_VECTOR_DIM;
    use crate::vector_engine::tier3::PayloadRecord;
    use tempfile::TempDir;

    fn sample(qn: &str) -> WriteInput {
        WriteInput {
            qualified_name: qn.into(),
            vector: vec![0.1; DEFAULT_VECTOR_DIM],
            payload: qn.as_bytes().to_vec(),
        }
    }

    #[test]
    fn crash_after_append_before_commit_recovers_clean() {
        let dir = TempDir::new().unwrap();
        {
            let mut eng = DualWriteEngine::open_local(dir.path()).unwrap();
            eng.write(sample("ok::one")).unwrap();
            let orphan = PayloadRecord {
                id: 99,
                vector: vec![0.2; DEFAULT_VECTOR_DIM],
                payload: b"orphan".to_vec(),
            };
            eng.flat.append(&orphan).unwrap();
            eng.flat.fsync().unwrap();
        }
        let mut eng = DualWriteEngine::open_local(dir.path()).unwrap();
        let nodes = recover_and_list(&mut eng).unwrap();
        assert_eq!(nodes.len(), 1);
        assert_eq!(nodes[0].id, 1);
        assert!(eng.topology.get_node(99).is_none());
        assert_eq!(eng.vector_count(), 1);
        let node = eng.topology.get_node(1).unwrap();
        assert_eq!(
            eng.flat.len_bytes(),
            node.payload_offset + u64::from(node.payload_len)
        );
    }
}
