use std::collections::BTreeMap;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use cozo::{Db, SqliteStorage};
use parking_lot::RwLock;
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CacheValue {
    pub value_json: String,
    pub created_at: u64,
    pub ttl_seconds: u64,
    pub tool_name: String,
    pub project_path: String,
}

pub struct PersistentCache {
    db: Db<SqliteStorage>,
    default_ttl: u64,
    l1: Arc<RwLock<HashMap<String, CacheValue>>>,
}

impl PersistentCache {
    pub fn new(db: Db<SqliteStorage>, default_ttl: u64) -> Self {
        Self {
            db,
            default_ttl,
            l1: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    pub fn get(&self, key: &str) -> Option<String> {
        let l1_expired = {
            let l1 = self.l1.read();
            if let Some(l1_value) = l1.get(key) {
                if !self.is_expired(l1_value) {
                    return Some(l1_value.value_json.clone());
                }
                true
            } else {
                false
            }
        };

        if l1_expired {
            self.l1.write().remove(key);
        }

        let key_clone = key.to_string();
        let result = self.get_from_db(&key_clone);
        if let Some(ref cached) = result {
            if !self.is_expired(cached) {
                self.l1.write().insert(key_clone, cached.clone());
            }
        }
        result.map(|c| c.value_json)
    }

    pub fn insert(
        &self,
        key: String,
        value_json: String,
        tool_name: String,
        project_path: String,
        ttl_seconds: Option<u64>,
    ) {
        let ttl = ttl_seconds.unwrap_or(self.default_ttl);
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();

        let cache_value = CacheValue {
            value_json,
            created_at: now,
            ttl_seconds: ttl,
            tool_name,
            project_path,
        };

        self.l1.write().insert(key.clone(), cache_value.clone());
        self.insert_to_db(&key, &cache_value);
    }

    pub fn invalidate(&self, key: &str) {
        self.l1.write().remove(key);
        self.delete_from_db(key);
    }

    pub fn invalidate_prefix(&self, prefix: &str) {
        let keys_to_delete: Vec<String> = self
            .l1
            .read()
            .keys()
            .filter(|k| k.starts_with(prefix))
            .cloned()
            .collect();

        for key in keys_to_delete {
            self.invalidate(&key);
        }
    }

    fn is_expired(&self, value: &CacheValue) -> bool {
        if value.ttl_seconds == 0 {
            return true;
        }
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_secs();
        value.created_at + value.ttl_seconds < now
    }

    fn get_from_db(&self, key: &str) -> Option<CacheValue> {
        let query = r#"
        SELECT cache_key, value_json, created_at, ttl_seconds, tool_name, project_path
        FROM query_cache
        WHERE cache_key = $key
       "#;

        let mut params = BTreeMap::new();
        params.insert("key".to_string(), Value::String(key.to_string()));

        match self.db.run_script(query, params) {
            Ok(result) => {
                if result.rows.is_empty() {
                    return None;
                }
                let row = &result.rows[0];
                Some(CacheValue {
                    value_json: row[1].as_str().unwrap_or("").to_string(),
                    created_at: row[2].as_str().and_then(|s| s.parse().ok()).unwrap_or(0),
                    ttl_seconds: row[3].as_str().and_then(|s| s.parse().ok()).unwrap_or(0),
                    tool_name: row[4].as_str().unwrap_or("").to_string(),
                    project_path: row[5].as_str().unwrap_or("").to_string(),
                })
            }
            Err(_) => None,
        }
    }

    fn insert_to_db(&self, key: &str, value: &CacheValue) {
        let query = r#"
        INSERT INTO query_cache (cache_key, value_json, created_at, ttl_seconds, tool_name, project_path, metadata)
        VALUES ($key, $value_json, $created_at, $ttl_seconds, $tool_name, $project_path, '{}')
       "#;

        let mut params = BTreeMap::new();
        params.insert("key".to_string(), Value::String(key.to_string()));
        params.insert(
            "value_json".to_string(),
            Value::String(value.value_json.clone()),
        );
        params.insert(
            "created_at".to_string(),
            Value::Number(serde_json::Number::from(value.created_at)),
        );
        params.insert(
            "ttl_seconds".to_string(),
            Value::Number(serde_json::Number::from(value.ttl_seconds)),
        );
        params.insert(
            "tool_name".to_string(),
            Value::String(value.tool_name.clone()),
        );
        params.insert(
            "project_path".to_string(),
            Value::String(value.project_path.clone()),
        );

        let _ = self.db.run_script(query, params);
    }

    fn delete_from_db(&self, key: &str) {
        let query = r#"DELETE FROM query_cache WHERE cache_key = $key"#;
        let mut params = BTreeMap::new();
        params.insert("key".to_string(), Value::String(key.to_string()));
        let _ = self.db.run_script(query, params);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;
    use tempfile::tempdir;

    fn create_test_db() -> Db<SqliteStorage> {
        let temp_dir = tempdir().unwrap();
        let db_path = temp_dir.path().join("test_cache.db");
        let path_str = db_path.to_string_lossy().to_string();
        cozo::new_cozo_sqlite(path_str).unwrap()
    }

    fn init_test_schema(db: &Db<SqliteStorage>) {
        let create_query_cache = r#":create query_cache {
            cache_key: String,
            value_json: String,
            created_at: Int,
            ttl_seconds: Int,
            tool_name: String,
            project_path: String,
            metadata: String
        }"#;
        let _ = db.run_script(create_query_cache, Default::default());
    }

    #[test]
    fn test_persistent_cache_get_returns_cached_value() {
        let db = create_test_db();
        init_test_schema(&db);

        let cache = PersistentCache::new(db, 3600);

        cache.insert(
            "deps:src/main.rs".to_string(),
            r#"["file1.rs", "file2.rs"]"#.to_string(),
            "get_dependencies".to_string(),
            "/project".to_string(),
            None,
        );

        let result = cache.get("deps:src/main.rs");
        assert_eq!(result, Some(r#"["file1.rs", "file2.rs"]"#.to_string()));
    }

    #[test]
    fn test_persistent_cache_ttl_expiration() {
        let db = create_test_db();
        init_test_schema(&db);

        let cache = PersistentCache::new(db, 0);

        cache.insert(
            "deps:src/main.rs".to_string(),
            r#"["file1.rs"]"#.to_string(),
            "get_dependencies".to_string(),
            "/project".to_string(),
            Some(0),
        );

        std::thread::sleep(Duration::from_millis(10));

        let result = cache.get("deps:src/main.rs");
        assert_eq!(result, None);
    }

    #[test]
    fn test_persistent_cache_invalidate_prefix() {
        let db = create_test_db();
        init_test_schema(&db);

        let cache = PersistentCache::new(db, 3600);

        cache.insert(
            "deps:src/file1.rs".to_string(),
            r#"["a.rs"]"#.to_string(),
            "get_dependencies".to_string(),
            "/project".to_string(),
            None,
        );
        cache.insert(
            "deps:src/file2.rs".to_string(),
            r#"["b.rs"]"#.to_string(),
            "get_dependencies".to_string(),
            "/project".to_string(),
            None,
        );
        cache.insert(
            "orch:query1".to_string(),
            r#"{"result": "ok"}"#.to_string(),
            "orchestrate".to_string(),
            "/project".to_string(),
            None,
        );

        cache.invalidate_prefix("deps:");

        assert_eq!(cache.get("deps:src/file1.rs"), None);
        assert_eq!(cache.get("deps:src/file2.rs"), None);
        assert_eq!(
            cache.get("orch:query1"),
            Some(r#"{"result": "ok"}"#.to_string())
        );
    }
}
