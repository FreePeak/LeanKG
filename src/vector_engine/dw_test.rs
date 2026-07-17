//! Dual-write crash simulation (FR-VE-TEST-DW).

#[cfg(test)]
mod tests {
    use crate::vector_engine::dual_write::{DualWriteEngine, WriteInput};
    use crate::vector_engine::recovery::assert_no_dangling_pointers;
    use crate::vector_engine::tier3::PayloadRecord;
    use crate::vector_engine::DEFAULT_VECTOR_DIM;
    use tempfile::TempDir;

    #[test]
    fn dual_write_crash_simulation_recovers() {
        let dir = TempDir::new().unwrap();
        {
            let mut eng = DualWriteEngine::open_local(dir.path()).unwrap();
            eng.write(WriteInput {
                qualified_name: "ok::fn".into(),
                vector: vec![0.05; DEFAULT_VECTOR_DIM],
                payload: b"ok".to_vec(),
            })
            .unwrap();
            eng.flat
                .append(&PayloadRecord {
                    id: 777,
                    vector: vec![0.9; DEFAULT_VECTOR_DIM],
                    payload: b"orphan".to_vec(),
                })
                .unwrap();
            eng.flat.fsync().unwrap();
        }
        let eng = DualWriteEngine::open_local(dir.path()).unwrap();
        assert_no_dangling_pointers(&eng).unwrap();
        assert_eq!(eng.vector_count(), 1);
        assert!(eng.topology.get_node(777).is_none());
    }
}
