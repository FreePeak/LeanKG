//! Tier 2 — SQ8/INT8 vectors fully in RAM (FR-VE-T2).
//!
//! Hot ANN distance runs against this cache only — no disk I/O on the
//! inner loop. FP32 originals live in Tier-3 and are read once at post-filter.

use super::engine::VectorEngineError;

/// In-memory INT8 (SQ8) vector array.
#[derive(Debug, Clone)]
pub struct Sq8Cache {
    dim: usize,
    /// Contiguous row-major: `data[i * dim .. (i+1) * dim]`.
    data: Vec<i8>,
    /// Parallel id list (same order as rows).
    ids: Vec<u64>,
    /// Per-vector scale factors for dequantization (optional; 1.0 default).
    scales: Vec<f32>,
}

impl Sq8Cache {
    pub fn new(dim: usize) -> Self {
        Self {
            dim,
            data: Vec::new(),
            ids: Vec::new(),
            scales: Vec::new(),
        }
    }

    pub fn dim(&self) -> usize {
        self.dim
    }

    pub fn len(&self) -> usize {
        self.ids.len()
    }

    pub fn is_empty(&self) -> bool {
        self.ids.is_empty()
    }

    /// Quantize FP32 → INT8 (symmetric abs-max scale) and append.
    pub fn push_fp32(&mut self, id: u64, fp32: &[f32]) -> Result<(), VectorEngineError> {
        if fp32.len() != self.dim {
            return Err(VectorEngineError::Storage(format!(
                "dim mismatch: got {} want {}",
                fp32.len(),
                self.dim
            )));
        }
        let (sq8, scale) = quantize_sq8(fp32);
        self.ids.push(id);
        self.scales.push(scale);
        self.data.extend_from_slice(&sq8);
        Ok(())
    }

    /// Insert pre-quantized INT8 row (scale defaults to 1.0).
    pub fn push_sq8(&mut self, id: u64, sq8: &[i8]) -> Result<(), VectorEngineError> {
        self.push_sq8_scaled(id, sq8, 1.0)
    }

    pub fn push_sq8_scaled(
        &mut self,
        id: u64,
        sq8: &[i8],
        scale: f32,
    ) -> Result<(), VectorEngineError> {
        if sq8.len() != self.dim {
            return Err(VectorEngineError::Storage(format!(
                "dim mismatch: got {} want {}",
                sq8.len(),
                self.dim
            )));
        }
        self.ids.push(id);
        self.scales.push(scale);
        self.data.extend_from_slice(sq8);
        Ok(())
    }

    pub fn id_at(&self, index: usize) -> Option<u64> {
        self.ids.get(index).copied()
    }

    pub fn row(&self, index: usize) -> Option<&[i8]> {
        if index >= self.len() {
            return None;
        }
        let start = index * self.dim;
        Some(&self.data[start..start + self.dim])
    }

    pub fn scale_at(&self, index: usize) -> Option<f32> {
        self.scales.get(index).copied()
    }

    /// Find row index by id (linear scan — fine until HNSW indexes ids).
    pub fn index_of(&self, id: u64) -> Option<usize> {
        self.ids.iter().position(|&x| x == id)
    }

    /// Dot product of query (INT8) against row `index` (scalar path).
    pub fn dot_i8_scalar(&self, index: usize, query: &[i8]) -> Option<i32> {
        let row = self.row(index)?;
        if row.len() != query.len() {
            return None;
        }
        Some(dot_i8_scalar(row, query))
    }

    /// Raw backing buffer (for SIMD kernels).
    pub fn as_bytes(&self) -> &[i8] {
        &self.data
    }

    pub fn ids(&self) -> &[u64] {
        &self.ids
    }

    /// Remove by id; returns true if found. Compacts the row array.
    pub fn remove(&mut self, id: u64) -> bool {
        let Some(idx) = self.index_of(id) else {
            return false;
        };
        self.ids.remove(idx);
        self.scales.remove(idx);
        let start = idx * self.dim;
        self.data.drain(start..start + self.dim);
        true
    }

    pub fn clear(&mut self) {
        self.data.clear();
        self.ids.clear();
        self.scales.clear();
    }
}

/// Symmetric abs-max SQ8 quantization.
pub fn quantize_sq8(fp32: &[f32]) -> (Vec<i8>, f32) {
    let max_abs = fp32.iter().map(|v| v.abs()).fold(0.0f32, f32::max);
    let scale = if max_abs == 0.0 { 1.0 } else { max_abs / 127.0 };
    let sq8: Vec<i8> = fp32
        .iter()
        .map(|v| {
            let q = (v / scale).round();
            q.clamp(-127.0, 127.0) as i8
        })
        .collect();
    (sq8, scale)
}

pub fn dot_i8_scalar(a: &[i8], b: &[i8]) -> i32 {
    a.iter()
        .zip(b.iter())
        .map(|(x, y)| i32::from(*x) * i32::from(*y))
        .sum()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn push_fp32_quantizes_and_stores_in_ram() {
        let mut cache = Sq8Cache::new(4);
        cache.push_fp32(1, &[0.0, 0.5, -0.5, 1.0]).unwrap();
        assert_eq!(cache.len(), 1);
        assert_eq!(cache.id_at(0), Some(1));
        let row = cache.row(0).unwrap();
        assert_eq!(row.len(), 4);
        // max abs = 1.0 → scale = 1/127; last component → 127
        assert_eq!(row[3], 127);
    }

    #[test]
    fn dim_mismatch_rejected() {
        let mut cache = Sq8Cache::new(3);
        assert!(cache.push_fp32(1, &[1.0, 2.0]).is_err());
    }

    #[test]
    fn remove_compacts_cache() {
        let mut cache = Sq8Cache::new(2);
        cache.push_sq8(10, &[1, 2]).unwrap();
        cache.push_sq8(20, &[3, 4]).unwrap();
        assert!(cache.remove(10));
        assert_eq!(cache.len(), 1);
        assert_eq!(cache.id_at(0), Some(20));
        assert_eq!(cache.row(0).unwrap(), &[3, 4]);
    }

    #[test]
    fn dot_i8_scalar_matches_manual() {
        let mut cache = Sq8Cache::new(3);
        cache.push_sq8(1, &[1, -2, 3]).unwrap();
        let q = [2i8, 1, -1];
        assert_eq!(cache.dot_i8_scalar(0, &q), Some(2 - 2 - 3));
    }
}
