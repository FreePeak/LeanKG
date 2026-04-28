use crate::db::schema::CozoDb;
use serde::{de::DeserializeOwned, Serialize};
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};
use tokio::sync::RwLock;

#[derive(Clone)]
struct CacheEntry {
    value_json: String,
    created_at: i64,
    ttl_seconds: i64,
}

impl CacheEntry {
    fn is_expired(&self) -> bool {
        if self.ttl_seconds == 0 {
            return true;
        }
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0);
        (now - self.created_at) > self.ttl_seconds
    }
}

#[derive(Clone)]
pub struct PersistentCache {
    db: Arc<CozoDb>,
    memory: Arc<RwLock<HashMap<String, CacheEntry>>>,
    default_ttl: u64,
    /// If true, only use memory storage (no DB persistence)
    /// Useful for reducing memory footprint when persistence isn't needed
    memory_only: bool,
}

impl PersistentCache {
    pub fn new(db: Arc<CozoDb>, default_ttl: u64) -> Self {
        Self {
            db,
            memory: Arc::new(RwLock::new(HashMap::new())),
            default_ttl,
            memory_only: false,
        }
    }

    /// Create a memory-only cache that doesn't duplicate storage in SQLite
    /// Reduces memory footprint by ~50% for transient caches
    pub fn memory_only(db: Arc<CozoDb>, default_ttl: u64) -> Self {
        Self {
            db,
            memory: Arc::new(RwLock::new(HashMap::new())),
            default_ttl,
            memory_only: true,
        }
    }

    pub fn with_ttl(db: Arc<CozoDb>, ttl_secs: u64) -> Self {
        Self::new(db, ttl_secs)
    }

    pub async fn get<V: DeserializeOwned>(&self, key: &str) -> Option<V> {
        if let Some(entry) = self.memory.read().await.get(key) {
            if !entry.is_expired() {
                return serde_json::from_str(&entry.value_json).ok();
            }
        }

        if let Some(value_json) = self.load_from_db(key).await {
            if let Ok(v) = serde_json::from_str::<V>(&value_json) {
                let now = SystemTime::now()
                    .duration_since(UNIX_EPOCH)
                    .map(|d| d.as_secs() as i64)
                    .unwrap_or(0);
                self.memory.write().await.insert(
                    key.to_string(),
                    CacheEntry {
                        value_json: value_json.clone(),
                        created_at: now,
                        ttl_seconds: self.default_ttl as i64,
                    },
                );
                return Some(v);
            }
        }
        None
    }

    #[allow(clippy::extra_unused_type_parameters)]
    pub async fn insert<K: Serialize, V: Serialize>(&self, key: String, value: V) {
        let value_json = serde_json::to_string(&value).unwrap_or_default();
        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_secs() as i64)
            .unwrap_or(0);

        self.memory.write().await.insert(
            key.clone(),
            CacheEntry {
                value_json: value_json.clone(),
                created_at: now,
                ttl_seconds: self.default_ttl as i64,
            },
        );

        // Skip DB persistence if memory_only mode is enabled
        if !self.memory_only {
            self.save_to_db(&key, &value_json, now).await.ok();
        }
    }

    pub async fn invalidate(&self, key: &str) {
        self.memory.write().await.remove(key);
        self.delete_from_db(key).await.ok();
    }

    pub async fn invalidate_prefix(&self, prefix: &str) {
        let prefix_owned = prefix.to_string();
        let keys: Vec<String> = self
            .memory
            .read()
            .await
            .keys()
            .filter(|k| k.starts_with(&prefix_owned))
            .cloned()
            .collect();

        for key in keys {
            self.invalidate(&key).await;
        }
    }

    async fn load_from_db(&self, key: &str) -> Option<String> {
        let query = r#"
            ?[value_json, created_at, ttl_seconds] := 
                *query_cache[cache_key = $key, value_json, created_at, ttl_seconds]
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "key".to_string(),
            serde_json::Value::String(key.to_string()),
        );

        let result = self.db.run_script(query, params).ok()?;

        let row = result.rows.first()?;
        let created_at = row.get(1)?.as_i64()?;
        let ttl_seconds = row.get(2)?.as_i64()?;

        if ttl_seconds > 0 {
            let now = SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .map(|d| d.as_secs() as i64)
                .unwrap_or(0);
            if (now - created_at) > ttl_seconds {
                self.delete_from_db(key).await.ok();
                return None;
            }
        }

        row.first()?.as_str().map(String::from)
    }

    async fn save_to_db(
        &self,
        key: &str,
        value_json: &str,
        created_at: i64,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let query = r#"
            ?[cache_key, value_json, created_at, ttl_seconds, tool_name, project_path, metadata] 
            <- [[ $key, $value_json, $created_at, $ttl_seconds, "unknown", "default", "{}" ]]
            :put query_cache { cache_key, value_json, created_at, ttl_seconds, tool_name, project_path, metadata }
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "key".to_string(),
            serde_json::Value::String(key.to_string()),
        );
        params.insert(
            "value_json".to_string(),
            serde_json::Value::String(value_json.to_string()),
        );
        params.insert(
            "created_at".to_string(),
            serde_json::Value::Number(created_at.into()),
        );
        params.insert(
            "ttl_seconds".to_string(),
            serde_json::Value::Number((self.default_ttl as i64).into()),
        );

        self.db.run_script(query, params)?;
        Ok(())
    }

    async fn delete_from_db(
        &self,
        key: &str,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let query = r#":delete query_cache where cache_key = $key"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "key".to_string(),
            serde_json::Value::String(key.to_string()),
        );
        self.db.run_script(query, params)?;
        Ok(())
    }

    pub async fn len(&self) -> usize {
        self.memory.read().await.len()
    }

    pub async fn is_empty(&self) -> bool {
        self.memory.read().await.is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::atomic::{AtomicU64, Ordering};
    use std::time::Duration;

    static TEST_DB_COUNTER: AtomicU64 = AtomicU64::new(0);

    fn create_test_db() -> CozoDb {
        let counter = TEST_DB_COUNTER.fetch_add(1, Ordering::SeqCst);
        let temp_dir = std::env::temp_dir();
        let db_path = temp_dir.join(format!("leankg_test_persistent_cache_{}.db", counter));
        let db = crate::db::schema::init_db(&db_path).unwrap();
        drop(db);
        std::fs::remove_file(&db_path).ok();
        crate::db::schema::init_db(&db_path).unwrap()
    }

    #[tokio::test]
    async fn test_persistent_cache_basic() {
        let db = Arc::new(create_test_db());
        let cache = PersistentCache::new(db, 300);

        cache
            .insert::<String, Vec<String>>(
                "test_key".to_string(),
                vec!["value1".to_string(), "value2".to_string()],
            )
            .await;

        let result: Option<Vec<String>> = cache.get("test_key").await;
        assert!(result.is_some());
        let values = result.unwrap();
        assert_eq!(values.len(), 2);
        assert_eq!(values[0], "value1");
    }

    #[tokio::test]
    async fn test_persistent_cache_expired() {
        let db = Arc::new(create_test_db());
        let cache = PersistentCache::new(db, 0);

        cache
            .insert::<String, Vec<String>>("expired_key".to_string(), vec!["value".to_string()])
            .await;

        tokio::time::sleep(Duration::from_millis(10)).await;

        let result: Option<Vec<String>> = cache.get("expired_key").await;
        assert!(result.is_none());
    }

    #[tokio::test]
    async fn test_persistent_cache_invalidate_prefix() {
        let db = Arc::new(create_test_db());
        let cache = PersistentCache::new(db, 300);

        cache
            .insert::<String, Vec<String>>(
                "deps:src/main.rs".to_string(),
                vec!["lib.rs".to_string()],
            )
            .await;
        cache
            .insert::<String, Vec<String>>(
                "deps:src/lib.rs".to_string(),
                vec!["mod.rs".to_string()],
            )
            .await;
        cache
            .insert::<String, String>(
                "orch:context:src/main.rs".to_string(),
                "content".to_string(),
            )
            .await;

        cache.invalidate_prefix("deps:src/").await;

        let result1: Option<Vec<String>> = cache.get("deps:src/main.rs").await;
        assert!(result1.is_none());

        let result2: Option<Vec<String>> = cache.get("deps:src/lib.rs").await;
        assert!(result2.is_none());

        let result3: Option<String> = cache.get("orch:context:src/main.rs").await;
        assert!(result3.is_some());
    }

    #[tokio::test]
    async fn test_persistent_cache_invalidate() {
        let db = Arc::new(create_test_db());
        let cache = PersistentCache::new(db, 300);

        cache
            .insert::<String, Vec<String>>("key1".to_string(), vec!["value1".to_string()])
            .await;

        cache.invalidate("key1").await;

        let result: Option<Vec<String>> = cache.get("key1").await;
        assert!(result.is_none());
    }
}
