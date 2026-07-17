//! Env injection selects LocalEngine vs CloudEngine (FR-VE-TEST-FACTORY).

#[cfg(test)]
mod tests {
    use crate::vector_engine::{
        EngineKind, VectorEngineFactory, VectorStorage, DEFAULT_VECTOR_DIM, ENV_VECTOR_ENGINE,
    };
    use tempfile::TempDir;

    #[test]
    fn env_injection_selects_local_and_cloud() {
        let dir = TempDir::new().unwrap();
        // Parse path used by from_env — avoid mutating process env under parallel tests.
        for (raw, want) in [("local", EngineKind::Local), ("cloud", EngineKind::Cloud)] {
            let kind = EngineKind::parse(raw).unwrap();
            let engine =
                VectorEngineFactory::open(kind, dir.path().join(raw), DEFAULT_VECTOR_DIM).unwrap();
            assert_eq!(engine.kind(), want);
            assert_eq!(engine.as_storage().kind(), want);
        }
        assert_eq!(ENV_VECTOR_ENGINE, "LEANKG_VECTOR_ENGINE");
    }
}
