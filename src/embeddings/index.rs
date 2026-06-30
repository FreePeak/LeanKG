//! usearch HNSW ANN wrapper with file persistence.
//!
//! Cosine similarity, f32 quantization, auto connectivity/expansion. Keys
//! are u64 — we use the deterministic SHA-256-derived key from
//! `text_blob::usearch_key_for` so the same `qualified_name` always maps to
//! the same usearch key across rebuilds.

use std::path::Path;
use usearch::{Index, IndexOptions, MetricKind, ScalarKind, new_index};

pub struct AnnIndex {
    inner: Index,
    dim: usize,
}

#[derive(Debug, Clone)]
pub struct AnnSearchResult {
    pub key: u64,
    /// Cosine distance in [-1, 1]. Lower is more similar for L2; for Cos,
    /// usearch returns similarity directly (higher is better) — semantics
    /// depend on the underlying library version. We expose the raw value
    /// and let callers decide.
    pub distance: f32,
}

impl AnnIndex {
    /// Create an empty index. Memory-only until `save` is called.
    pub fn new(dim: usize) -> Result<Self, Box<dyn std::error::Error>> {
        let opts = IndexOptions {
            dimensions: dim,
            metric: MetricKind::Cos,
            quantization: ScalarKind::F32,
            connectivity: 0, // auto
            expansion_add: 0,
            expansion_search: 0,
            multi: false,
        };
        let inner = new_index(&opts)?;
        Ok(Self { inner, dim })
    }

    /// Load an existing index from disk. The dimension is read from the
    /// file's metadata via `inner.dimensions()` after load.
    pub fn load(path: &Path) -> Result<Self, Box<dyn std::error::Error>> {
        let path_str = path.to_string_lossy().to_string();
        // We need dimensions to construct an index, but the index file
        // already encodes them. Trick: create a 1-d placeholder, then load
        // (which overrides), then read back the real dim.
        let placeholder = Self::new(1)?;
        placeholder.inner.load(&path_str)?;
        let dim = placeholder.inner.dimensions() as usize;
        Ok(Self {
            inner: placeholder.inner,
            dim,
        })
    }

    pub fn save(&self, path: &Path) -> Result<(), Box<dyn std::error::Error>> {
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        let path_str = path.to_string_lossy().to_string();
        self.inner.save(&path_str)?;
        Ok(())
    }

    pub fn add(&self, key: u64, vector: &[f32]) -> Result<(), Box<dyn std::error::Error>> {
        if vector.len() != self.dim {
            return Err(format!(
                "vector dim mismatch: expected {}, got {}",
                self.dim,
                vector.len()
            )
            .into());
        }
        self.inner.add(key, vector)?;
        Ok(())
    }

    /// Remove a vector by key. Best-effort: silently succeeds if the key
    /// isn't present. Used by the embed step to reap orphans.
    pub fn remove(&self, key: u64) -> Result<(), Box<dyn std::error::Error>> {
        self.inner.remove(key)?;
        Ok(())
    }

    pub fn search(
        &self,
        query: &[f32],
        k: usize,
    ) -> Result<Vec<AnnSearchResult>, Box<dyn std::error::Error>> {
        if query.len() != self.dim {
            return Err(format!(
                "query dim mismatch: expected {}, got {}",
                self.dim,
                query.len()
            )
            .into());
        }
        let matches = self.inner.search(query, k)?;
        Ok(matches
            .keys
            .iter()
            .zip(matches.distances.iter())
            .map(|(&k, &d)| AnnSearchResult {
                key: k,
                distance: d,
            })
            .collect())
    }

    /// Hint capacity to avoid reallocations during bulk insert. Optional.
    pub fn reserve(&self, capacity: usize) -> Result<(), Box<dyn std::error::Error>> {
        self.inner.reserve(capacity)?;
        Ok(())
    }

    pub fn size(&self) -> usize {
        self.inner.size() as usize
    }

    pub fn dim(&self) -> usize {
        self.dim
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn add_and_search_returns_nearest_first() {
        let index = AnnIndex::new(3).unwrap();
        index.add(1, &[0.1, 0.2, 0.3]).unwrap();
        index.add(2, &[0.9, 0.8, 0.7]).unwrap();
        index.add(3, &[0.15, 0.25, 0.35]).unwrap();

        let results = index.search(&[0.1, 0.2, 0.3], 2).unwrap();
        assert_eq!(results.len(), 2);
        // Closest to [0.1, 0.2, 0.3] is key 1 (exact match), then key 3.
        assert_eq!(results[0].key, 1);
        assert_eq!(results[1].key, 3);
    }

    #[test]
    fn save_load_roundtrip_preserves_size() {
        let tmp = tempfile::tempdir().unwrap();
        let path = tmp.path().join("test.usearch");

        {
            let index = AnnIndex::new(2).unwrap();
            index.add(10, &[1.0, 0.0]).unwrap();
            index.add(20, &[0.0, 1.0]).unwrap();
            index.save(&path).unwrap();
        }

        let loaded = AnnIndex::load(&path).unwrap();
        assert_eq!(loaded.dim(), 2);
        assert_eq!(loaded.size(), 2);

        let results = loaded.search(&[1.0, 0.0], 1).unwrap();
        assert_eq!(results[0].key, 10);
    }

    #[test]
    fn dim_mismatch_is_an_error() {
        let index = AnnIndex::new(3).unwrap();
        let err = index.add(1, &[0.0, 0.0]).unwrap_err();
        assert!(err.to_string().contains("dim mismatch"));
    }
}
