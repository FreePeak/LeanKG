//! Storage abstraction + static enum dispatch (FR-VE-ABS).
//!
//! `VectorEngine` is a closed enum (`Local` | `Cloud`) selected from
//! `LEANKG_VECTOR_ENGINE` / config. Call sites use static dispatch — no
//! `dyn VectorStorage` on the hot path.

use std::fmt;
use std::path::{Path, PathBuf};

use thiserror::Error;

/// Env var that selects Local vs Cloud engine (PRD §3.13 / §5.14).
pub const ENV_VECTOR_ENGINE: &str = "LEANKG_VECTOR_ENGINE";

/// Default embedding dimension (matches BGE-small-en-v1.5 / Cozo HNSW).
pub const DEFAULT_VECTOR_DIM: usize = 384;

/// Errors from engine construction or storage operations.
#[derive(Debug, Error)]
pub enum VectorEngineError {
    #[error("unknown vector engine kind '{0}' (expected local|cloud)")]
    UnknownKind(String),
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("storage error: {0}")]
    Storage(String),
}

/// Backend kind selected by env/config.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EngineKind {
    Local,
    Cloud,
}

impl EngineKind {
    /// Parse `local` / `cloud` (case-insensitive). Empty → Local.
    pub fn parse(raw: &str) -> Result<Self, VectorEngineError> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "" | "local" => Ok(Self::Local),
            "cloud" => Ok(Self::Cloud),
            other => Err(VectorEngineError::UnknownKind(other.to_string())),
        }
    }

    /// Read `LEANKG_VECTOR_ENGINE`, defaulting to Local when unset.
    pub fn from_env() -> Result<Self, VectorEngineError> {
        match std::env::var(ENV_VECTOR_ENGINE) {
            Ok(v) => Self::parse(&v),
            Err(_) => Ok(Self::Local),
        }
    }

    pub fn as_str(self) -> &'static str {
        match self {
            Self::Local => "local",
            Self::Cloud => "cloud",
        }
    }
}

impl fmt::Display for EngineKind {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

/// Shared storage contract implemented by both Local and Cloud engines.
///
/// Tier methods are stubbed here and filled by subsequent FR-VE-T* commits.
pub trait VectorStorage {
    fn kind(&self) -> EngineKind;
    fn root_dir(&self) -> &Path;
    fn dim(&self) -> usize;
    /// Number of vectors currently resident in the Tier-2 SQ8 cache.
    fn vector_count(&self) -> usize;
}

/// Static enum dispatch over Local / Cloud backends (FR-VE-ABS).
#[derive(Debug)]
pub enum VectorEngine {
    Local(LocalEngine),
    Cloud(CloudEngine),
}

impl VectorEngine {
    pub fn kind(&self) -> EngineKind {
        match self {
            Self::Local(_) => EngineKind::Local,
            Self::Cloud(_) => EngineKind::Cloud,
        }
    }

    pub fn as_storage(&self) -> &dyn VectorStorage {
        match self {
            Self::Local(e) => e,
            Self::Cloud(e) => e,
        }
    }
}

impl VectorStorage for VectorEngine {
    fn kind(&self) -> EngineKind {
        VectorEngine::kind(self)
    }

    fn root_dir(&self) -> &Path {
        self.as_storage().root_dir()
    }

    fn dim(&self) -> usize {
        self.as_storage().dim()
    }

    fn vector_count(&self) -> usize {
        self.as_storage().vector_count()
    }
}

/// Local-first engine (ARM64/x86 laptop envelope).
#[derive(Debug)]
pub struct LocalEngine {
    root: PathBuf,
    dim: usize,
    vector_count: usize,
}

impl LocalEngine {
    pub fn open(root: impl Into<PathBuf>, dim: usize) -> Result<Self, VectorEngineError> {
        let root = root.into();
        std::fs::create_dir_all(&root)?;
        Ok(Self {
            root,
            dim,
            vector_count: 0,
        })
    }
}

impl VectorStorage for LocalEngine {
    fn kind(&self) -> EngineKind {
        EngineKind::Local
    }

    fn root_dir(&self) -> &Path {
        &self.root
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn vector_count(&self) -> usize {
        self.vector_count
    }
}

/// Cloud-scale twin (TiKV Tier-1; same API surface as Local).
#[derive(Debug)]
pub struct CloudEngine {
    root: PathBuf,
    dim: usize,
    vector_count: usize,
}

impl CloudEngine {
    pub fn open(root: impl Into<PathBuf>, dim: usize) -> Result<Self, VectorEngineError> {
        let root = root.into();
        std::fs::create_dir_all(&root)?;
        Ok(Self {
            root,
            dim,
            vector_count: 0,
        })
    }
}

impl VectorStorage for CloudEngine {
    fn kind(&self) -> EngineKind {
        EngineKind::Cloud
    }

    fn root_dir(&self) -> &Path {
        &self.root
    }

    fn dim(&self) -> usize {
        self.dim
    }

    fn vector_count(&self) -> usize {
        self.vector_count
    }
}

/// Factory: env/config → static `VectorEngine` enum (FR-VE-ABS / US-VE-03).
pub struct VectorEngineFactory;

impl VectorEngineFactory {
    pub fn open(
        kind: EngineKind,
        root: impl Into<PathBuf>,
        dim: usize,
    ) -> Result<VectorEngine, VectorEngineError> {
        let root = root.into();
        match kind {
            EngineKind::Local => Ok(VectorEngine::Local(LocalEngine::open(root, dim)?)),
            EngineKind::Cloud => Ok(VectorEngine::Cloud(CloudEngine::open(root, dim)?)),
        }
    }

    /// Construct from `LEANKG_VECTOR_ENGINE` (default Local).
    pub fn from_env(
        root: impl Into<PathBuf>,
        dim: usize,
    ) -> Result<VectorEngine, VectorEngineError> {
        Self::open(EngineKind::from_env()?, root, dim)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn parse_kind_local_cloud_and_reject_unknown() {
        assert_eq!(EngineKind::parse("local").unwrap(), EngineKind::Local);
        assert_eq!(EngineKind::parse("LOCAL").unwrap(), EngineKind::Local);
        assert_eq!(EngineKind::parse("cloud").unwrap(), EngineKind::Cloud);
        assert_eq!(EngineKind::parse("").unwrap(), EngineKind::Local);
        assert!(matches!(
            EngineKind::parse("redis"),
            Err(VectorEngineError::UnknownKind(_))
        ));
    }

    #[test]
    fn factory_selects_local_engine() {
        let dir = TempDir::new().unwrap();
        let engine =
            VectorEngineFactory::open(EngineKind::Local, dir.path(), DEFAULT_VECTOR_DIM).unwrap();
        assert_eq!(engine.kind(), EngineKind::Local);
        assert_eq!(engine.dim(), DEFAULT_VECTOR_DIM);
        assert!(engine.root_dir().exists());
    }

    #[test]
    fn factory_selects_cloud_engine() {
        let dir = TempDir::new().unwrap();
        let engine =
            VectorEngineFactory::open(EngineKind::Cloud, dir.path(), DEFAULT_VECTOR_DIM).unwrap();
        assert_eq!(engine.kind(), EngineKind::Cloud);
        assert_eq!(engine.as_storage().vector_count(), 0);
    }

    #[test]
    fn factory_from_env_uses_parsed_kind() {
        // Avoid mutating process env (races under cargo test --test-threads>1).
        // from_env is parse(env) + open(kind); open is covered above.
        let dir = TempDir::new().unwrap();
        for (raw, want) in [("local", EngineKind::Local), ("cloud", EngineKind::Cloud)] {
            let kind = EngineKind::parse(raw).unwrap();
            assert_eq!(kind, want);
            let engine = VectorEngineFactory::open(kind, dir.path(), DEFAULT_VECTOR_DIM).unwrap();
            assert_eq!(engine.kind(), want);
        }
    }
}
