fn main() { let _db = cozo::new_cozo_sqlite("mem").unwrap(); let mut params = std::collections::BTreeMap::new(); params.insert("k".to_string(), "v".to_string()); let _ = _db.run_script("", params); }
