//! Tier 1 — graph topology store (FR-VE-T1).
//!
//! Local: RocksDB-shaped options (mmap OFF, Zstd, pin L0 filter/index,
//! BinaryAndHash). Persistence is a simple KV dir so unit tests stay light;
//! production opens apply [`RocksDbLocalOptions`] when the RocksDB backend
//! is wired. Cloud uses the same API over TiKV (stub root for now).

use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};

use serde::{Deserialize, Serialize};

use super::engine::{EngineKind, VectorEngineError};

/// RocksDB knobs required by PRD §5.14.1 for Local Tier-1.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct RocksDbLocalOptions {
    /// Always false on Local — avoid mmap page-fault storms on 256GB SSDs.
    pub mmap_enabled: bool,
    pub compression: RocksCompression,
    pub pin_l0_filter_and_index: bool,
    pub block_based_table_factory: BlockTableFactory,
    /// Filled by FR-VE-RT-MEM auto-tune; 0 means "unset / decide later".
    pub block_cache_bytes: usize,
}

impl Default for RocksDbLocalOptions {
    fn default() -> Self {
        Self {
            mmap_enabled: false,
            compression: RocksCompression::Zstd,
            pin_l0_filter_and_index: true,
            block_based_table_factory: BlockTableFactory::BinaryAndHash,
            block_cache_bytes: 0,
        }
    }
}

impl RocksDbLocalOptions {
    pub fn for_local() -> Self {
        Self::default()
    }

    pub fn validate_local(&self) -> Result<(), VectorEngineError> {
        if self.mmap_enabled {
            return Err(VectorEngineError::Storage(
                "Local Tier-1 must disable mmap (FR-VE-T1)".into(),
            ));
        }
        if self.compression != RocksCompression::Zstd {
            return Err(VectorEngineError::Storage(
                "Local Tier-1 requires Zstd compression (FR-VE-T1)".into(),
            ));
        }
        Ok(())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RocksCompression {
    Zstd,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BlockTableFactory {
    BinaryAndHash,
}

/// Node metadata stored in Tier-1 (AST refs, qualified names, offsets).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct TopologyNode {
    pub id: u64,
    pub qualified_name: String,
    /// Byte offset into Tier-3 flat file (set by dual-write).
    pub payload_offset: u64,
    pub payload_len: u32,
}

/// HNSW adjacency list for one node at one layer.
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct HnswAdjacency {
    pub id: u64,
    pub layer: u8,
    pub neighbors: Vec<u64>,
}

/// Tier-1 topology: metadata + HNSW adjacency.
#[derive(Debug)]
pub struct TopologyStore {
    root: PathBuf,
    kind: EngineKind,
    options: RocksDbLocalOptions,
    nodes: HashMap<u64, TopologyNode>,
    adjacency: HashMap<(u64, u8), Vec<u64>>,
}

impl TopologyStore {
    pub fn open(
        root: impl Into<PathBuf>,
        kind: EngineKind,
        options: RocksDbLocalOptions,
    ) -> Result<Self, VectorEngineError> {
        if kind == EngineKind::Local {
            options.validate_local()?;
        }
        let root = root.into().join("tier1");
        fs::create_dir_all(&root)?;
        let mut store = Self {
            root,
            kind,
            options,
            nodes: HashMap::new(),
            adjacency: HashMap::new(),
        };
        store.load()?;
        Ok(store)
    }

    pub fn kind(&self) -> EngineKind {
        self.kind
    }

    pub fn options(&self) -> &RocksDbLocalOptions {
        &self.options
    }

    pub fn set_block_cache_bytes(&mut self, bytes: usize) {
        self.options.block_cache_bytes = bytes;
    }

    pub fn upsert_node(&mut self, node: TopologyNode) -> Result<(), VectorEngineError> {
        self.nodes.insert(node.id, node);
        self.persist_nodes()
    }

    pub fn get_node(&self, id: u64) -> Option<&TopologyNode> {
        self.nodes.get(&id)
    }

    pub fn put_adjacency(
        &mut self,
        id: u64,
        layer: u8,
        neighbors: Vec<u64>,
    ) -> Result<(), VectorEngineError> {
        self.adjacency.insert((id, layer), neighbors);
        self.persist_adjacency()
    }

    pub fn get_adjacency(&self, id: u64, layer: u8) -> Option<&[u64]> {
        self.adjacency.get(&(id, layer)).map(|v| v.as_slice())
    }

    pub fn node_count(&self) -> usize {
        self.nodes.len()
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    fn nodes_path(&self) -> PathBuf {
        self.root.join("nodes.json")
    }

    fn adj_path(&self) -> PathBuf {
        self.root.join("adjacency.json")
    }

    fn load(&mut self) -> Result<(), VectorEngineError> {
        if self.nodes_path().exists() {
            let raw = fs::read_to_string(self.nodes_path())?;
            let list: Vec<TopologyNode> = serde_json::from_str(&raw)
                .map_err(|e| VectorEngineError::Storage(e.to_string()))?;
            self.nodes = list.into_iter().map(|n| (n.id, n)).collect();
        }
        if self.adj_path().exists() {
            let raw = fs::read_to_string(self.adj_path())?;
            let list: Vec<HnswAdjacency> = serde_json::from_str(&raw)
                .map_err(|e| VectorEngineError::Storage(e.to_string()))?;
            self.adjacency = list
                .into_iter()
                .map(|a| ((a.id, a.layer), a.neighbors))
                .collect();
        }
        Ok(())
    }

    fn persist_nodes(&self) -> Result<(), VectorEngineError> {
        let list: Vec<&TopologyNode> = self.nodes.values().collect();
        let raw =
            serde_json::to_string(&list).map_err(|e| VectorEngineError::Storage(e.to_string()))?;
        fs::write(self.nodes_path(), raw)?;
        Ok(())
    }

    fn persist_adjacency(&self) -> Result<(), VectorEngineError> {
        let list: Vec<HnswAdjacency> = self
            .adjacency
            .iter()
            .map(|(&(id, layer), neighbors)| HnswAdjacency {
                id,
                layer,
                neighbors: neighbors.clone(),
            })
            .collect();
        let raw =
            serde_json::to_string(&list).map_err(|e| VectorEngineError::Storage(e.to_string()))?;
        fs::write(self.adj_path(), raw)?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn local_options_disable_mmap_and_use_zstd() {
        let opts = RocksDbLocalOptions::for_local();
        assert!(!opts.mmap_enabled);
        assert_eq!(opts.compression, RocksCompression::Zstd);
        assert!(opts.pin_l0_filter_and_index);
        assert_eq!(
            opts.block_based_table_factory,
            BlockTableFactory::BinaryAndHash
        );
        opts.validate_local().unwrap();
    }

    #[test]
    fn reject_mmap_enabled_on_local() {
        let mut opts = RocksDbLocalOptions::for_local();
        opts.mmap_enabled = true;
        assert!(opts.validate_local().is_err());
    }

    #[test]
    fn upsert_and_reload_nodes_and_adjacency() {
        let dir = TempDir::new().unwrap();
        {
            let mut store = TopologyStore::open(
                dir.path(),
                EngineKind::Local,
                RocksDbLocalOptions::for_local(),
            )
            .unwrap();
            store
                .upsert_node(TopologyNode {
                    id: 7,
                    qualified_name: "src/main.rs::main".into(),
                    payload_offset: 0,
                    payload_len: 0,
                })
                .unwrap();
            store.put_adjacency(7, 0, vec![1, 2, 3]).unwrap();
            assert_eq!(store.node_count(), 1);
        }
        let store = TopologyStore::open(
            dir.path(),
            EngineKind::Local,
            RocksDbLocalOptions::for_local(),
        )
        .unwrap();
        let node = store.get_node(7).unwrap();
        assert_eq!(node.qualified_name, "src/main.rs::main");
        assert_eq!(store.get_adjacency(7, 0).unwrap(), &[1, 2, 3]);
    }
}
