//! Dual-write + crash recovery (FR-VE-FS-DW, FR-VE-FS-REC).
//!
//! Safe order: **Append Flat File → fsync → Commit offsets to Tier-1 →
//! Update RAM SQ8 cache**. Crash after append but before Tier-1 commit
//! leaves no dangling pointers — recovery truncates incomplete tails and
//! skips uncommitted records.

use std::path::Path;

use super::engine::{EngineKind, VectorEngineError, DEFAULT_VECTOR_DIM};
use super::tier1::{RocksDbLocalOptions, TopologyNode, TopologyStore};
use super::tier2::Sq8Cache;
use super::tier3::{FlatPayloadFile, PayloadRecord};

/// Composed Local/Cloud writable store for dual-write.
#[derive(Debug)]
pub struct DualWriteEngine {
    pub kind: EngineKind,
    pub topology: TopologyStore,
    pub sq8: Sq8Cache,
    pub flat: FlatPayloadFile,
    next_id: u64,
}

#[derive(Debug, Clone)]
pub struct WriteInput {
    pub qualified_name: String,
    pub vector: Vec<f32>,
    pub payload: Vec<u8>,
}

impl DualWriteEngine {
    pub fn open(
        root: impl AsRef<Path>,
        kind: EngineKind,
        dim: usize,
    ) -> Result<Self, VectorEngineError> {
        let root = root.as_ref();
        let opts = RocksDbLocalOptions::for_local();
        let topology = TopologyStore::open(root, kind, opts)?;
        let flat = FlatPayloadFile::open(root, dim)?;
        let sq8 = Sq8Cache::new(dim);
        let mut engine = Self {
            kind,
            topology,
            sq8,
            flat,
            next_id: 1,
        };
        engine.recover()?;
        Ok(engine)
    }

    pub fn open_local(root: impl AsRef<Path>) -> Result<Self, VectorEngineError> {
        Self::open(root, EngineKind::Local, DEFAULT_VECTOR_DIM)
    }

    /// Dual-write one vector. Returns assigned id.
    pub fn write(&mut self, input: WriteInput) -> Result<u64, VectorEngineError> {
        if input.vector.len() != self.sq8.dim() {
            return Err(VectorEngineError::Storage(format!(
                "vector dim {} != {}",
                input.vector.len(),
                self.sq8.dim()
            )));
        }
        let id = self.next_id;
        self.next_id += 1;

        // 1) Append flat file
        let record = PayloadRecord {
            id,
            vector: input.vector.clone(),
            payload: input.payload,
        };
        let offset = self.flat.append(&record)?;
        // 2) fsync
        self.flat.fsync()?;
        // 3) Commit offsets to Tier-1
        let payload_len =
            FlatPayloadFile::record_bytes(self.sq8.dim(), record.payload.len()) as u32;
        self.topology.upsert_node(TopologyNode {
            id,
            qualified_name: input.qualified_name,
            payload_offset: offset,
            payload_len,
        })?;
        // 4) Update RAM SQ8
        self.sq8.push_fp32(id, &input.vector)?;
        Ok(id)
    }

    /// Crash recovery: drop Tier-3 bytes beyond any committed offset+len,
    /// and never load SQ8 rows without a Tier-1 node (no dangling pointers).
    pub fn recover(&mut self) -> Result<(), VectorEngineError> {
        let mut max_end = 0u64;
        // Rebuild next_id and SQ8 from committed Tier-1 nodes only.
        self.sq8.clear();
        let mut max_id = 0u64;
        for id in self.topology.node_ids() {
            max_id = max_id.max(id);
            if let Some(node) = self.topology.get_node(id) {
                let end = node.payload_offset + u64::from(node.payload_len);
                max_end = max_end.max(end);
                if let Some(rec) = self.flat.read_at(node.payload_offset)? {
                    if rec.id == id {
                        self.sq8.push_fp32(id, &rec.vector)?;
                    }
                    // else: incomplete / mismatched — skip (no dangling)
                }
            }
        }
        if self.flat.len_bytes() > max_end {
            // Truncate uncommitted tail (crash after append, before commit).
            self.flat.truncate_to(max_end)?;
        }
        self.next_id = max_id + 1;
        Ok(())
    }

    pub fn vector_count(&self) -> usize {
        self.sq8.len()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    fn sample(qn: &str) -> WriteInput {
        WriteInput {
            qualified_name: qn.into(),
            vector: vec![0.1; DEFAULT_VECTOR_DIM],
            payload: qn.as_bytes().to_vec(),
        }
    }

    #[test]
    fn dual_write_order_roundtrip() {
        let dir = TempDir::new().unwrap();
        let mut eng = DualWriteEngine::open_local(dir.path()).unwrap();
        let id = eng.write(sample("a::f")).unwrap();
        assert_eq!(id, 1);
        assert_eq!(eng.vector_count(), 1);
        assert!(eng.topology.get_node(1).is_some());
    }
}
