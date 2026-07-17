//! Tier 3 — flat binary FP32 + source/chunk payload (FR-VE-T3).
//!
//! Append-only file. Records are read **once** during post-filter after ANN.
//! Dual-write (FR-VE-FS-DW) appends here before committing Tier-1 offsets.

use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom, Write};
use std::path::{Path, PathBuf};

use super::engine::VectorEngineError;

/// On-disk record layout (little-endian):
/// `[u64 id][u32 payload_len][u32 reserved][f32 * dim][payload bytes]`
pub const RECORD_HEADER_SIZE: usize = 8 + 4 + 4;

#[derive(Debug, Clone, PartialEq)]
pub struct PayloadRecord {
    pub id: u64,
    pub vector: Vec<f32>,
    pub payload: Vec<u8>,
}

#[derive(Debug)]
pub struct FlatPayloadFile {
    path: PathBuf,
    dim: usize,
    file: File,
    /// Logical end offset (may exceed committed if crash mid-write).
    len: u64,
}

impl FlatPayloadFile {
    pub fn open(root: impl AsRef<Path>, dim: usize) -> Result<Self, VectorEngineError> {
        let dir = root.as_ref().join("tier3");
        std::fs::create_dir_all(&dir)?;
        let path = dir.join("payload.bin");
        let file = OpenOptions::new()
            .create(true)
            .read(true)
            .write(true)
            .truncate(false)
            .open(&path)?;
        let len = file.metadata()?.len();
        Ok(Self {
            path,
            dim,
            file,
            len,
        })
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn dim(&self) -> usize {
        self.dim
    }

    pub fn len_bytes(&self) -> u64 {
        self.len
    }

    /// Append a record. Returns the starting byte offset.
    /// Caller must `fsync` (FR-VE-FS-DW) before committing Tier-1 offsets.
    pub fn append(&mut self, record: &PayloadRecord) -> Result<u64, VectorEngineError> {
        if record.vector.len() != self.dim {
            return Err(VectorEngineError::Storage(format!(
                "tier3 dim mismatch: got {} want {}",
                record.vector.len(),
                self.dim
            )));
        }
        if record.payload.len() > u32::MAX as usize {
            return Err(VectorEngineError::Storage("payload too large".into()));
        }
        let offset = self.len;
        self.file.seek(SeekFrom::Start(offset))?;
        self.file.write_all(&record.id.to_le_bytes())?;
        let plen = record.payload.len() as u32;
        self.file.write_all(&plen.to_le_bytes())?;
        self.file.write_all(&0u32.to_le_bytes())?; // reserved
        for v in &record.vector {
            self.file.write_all(&v.to_le_bytes())?;
        }
        self.file.write_all(&record.payload)?;
        self.len = offset + Self::record_bytes(self.dim, record.payload.len()) as u64;
        Ok(offset)
    }

    pub fn fsync(&mut self) -> Result<(), VectorEngineError> {
        self.file.sync_all()?;
        Ok(())
    }

    /// Read one record at `offset`. Returns None if truncated / incomplete.
    pub fn read_at(&mut self, offset: u64) -> Result<Option<PayloadRecord>, VectorEngineError> {
        if offset + RECORD_HEADER_SIZE as u64 > self.len {
            return Ok(None);
        }
        self.file.seek(SeekFrom::Start(offset))?;
        let mut hdr = [0u8; RECORD_HEADER_SIZE];
        if self.file.read_exact(&mut hdr).is_err() {
            return Ok(None);
        }
        let id = u64::from_le_bytes(hdr[0..8].try_into().unwrap());
        let payload_len = u32::from_le_bytes(hdr[8..12].try_into().unwrap()) as usize;
        let need = RECORD_HEADER_SIZE + self.dim * 4 + payload_len;
        if offset + need as u64 > self.len {
            return Ok(None);
        }
        let mut vector = vec![0f32; self.dim];
        for slot in &mut vector {
            let mut buf = [0u8; 4];
            self.file.read_exact(&mut buf)?;
            *slot = f32::from_le_bytes(buf);
        }
        let mut payload = vec![0u8; payload_len];
        if payload_len > 0 {
            self.file.read_exact(&mut payload)?;
        }
        Ok(Some(PayloadRecord {
            id,
            vector,
            payload,
        }))
    }

    pub fn record_bytes(dim: usize, payload_len: usize) -> usize {
        RECORD_HEADER_SIZE + dim * 4 + payload_len
    }

    /// Truncate file to `new_len` (used by crash recovery / GC).
    pub fn truncate_to(&mut self, new_len: u64) -> Result<(), VectorEngineError> {
        self.file.set_len(new_len)?;
        self.len = new_len;
        self.fsync()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn append_fsync_and_read_roundtrip() {
        let dir = TempDir::new().unwrap();
        let mut store = FlatPayloadFile::open(dir.path(), 2).unwrap();
        let rec = PayloadRecord {
            id: 42,
            vector: vec![1.5, -2.0],
            payload: b"hello".to_vec(),
        };
        let off = store.append(&rec).unwrap();
        store.fsync().unwrap();
        let got = store.read_at(off).unwrap().unwrap();
        assert_eq!(got, rec);
    }

    #[test]
    fn incomplete_tail_returns_none() {
        let dir = TempDir::new().unwrap();
        let mut store = FlatPayloadFile::open(dir.path(), 2).unwrap();
        let off = store
            .append(&PayloadRecord {
                id: 1,
                vector: vec![0.0, 1.0],
                payload: b"x".to_vec(),
            })
            .unwrap();
        store.fsync().unwrap();
        // Simulate truncated read beyond EOF
        assert!(store.read_at(off + 10_000).unwrap().is_none());
    }
}
