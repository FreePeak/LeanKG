#![allow(dead_code)]
use crate::graph::persistent_cache::PersistentCache;
use std::collections::HashMap;
use std::hash::Hash;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::RwLock;

#[derive(Debug, Clone)]
pub struct CacheEntry<T> {
    #[allow(dead_code)]
    pub value: T,
    pub created_at: Instant,
}

pub struct TimedCache<K, V> {
    data: HashMap<K, CacheEntry<V>>,
    ttl: Duration,
    max_entries: usize,
}

impl<K: Eq + Hash + Clone, V: Clone> TimedCache<K, V> {
    pub fn new(ttl_secs: u64, max_entries: usize) -> Self {
        Self {
            data: HashMap::new(),
            ttl: Duration::from_secs(ttl_secs),
            max_entries,
        }
    }

    #[allow(dead_code)]
    pub fn get(&self, key: &K) -> Option<V> {
        self.data.get(key).and_then(|entry| {
            if entry.created_at.elapsed() < self.ttl {
                Some(entry.value.clone())
            } else {
                None
            }
        })
    }

    pub fn insert(&mut self, key: K, value: V) {
        if self.data.len() >= self.max_entries {
            self.evict_expired();
            if self.data.len() >= self.max_entries {
                if let Some(oldest) = self
                    .data
                    .iter()
                    .min_by_key(|(_, entry)| entry.created_at)
                    .map(|(k, _)| k.clone())
                {
                    self.data.remove(&oldest);
                }
            }
        }
        self.data.insert(
            key,
            CacheEntry {
                value,
                created_at: Instant::now(),
            },
        );
    }

    #[allow(dead_code)]
    pub fn invalidate(&mut self, key: &K) {
        self.data.remove(key);
    }

    pub fn invalidate_prefix(&mut self, prefix: &str)
    where
        K: AsRef<str>,
    {
        self.data.retain(|k, _| !k.as_ref().starts_with(prefix));
    }

    fn evict_expired(&mut self) {
        self.data
            .retain(|_, entry| entry.created_at.elapsed() < self.ttl);
    }

    #[allow(dead_code)]
    pub fn clear(&mut self) {
        self.data.clear();
    }

    #[allow(dead_code)]
    pub fn len(&self) -> usize {
        self.data.len()
    }

    #[allow(dead_code)]
    pub fn is_empty(&self) -> bool {
        self.data.is_empty()
    }
}

#[derive(Clone)]
pub struct QueryCache {
    dependencies: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    dependents: Arc<RwLock<TimedCache<String, Vec<String>>>>,
    persistent: Option<Arc<PersistentCache>>,
}

impl QueryCache {
    pub fn new(ttl_secs: u64, max_entries: usize) -> Self {
        Self {
            dependencies: Arc::new(RwLock::new(TimedCache::new(ttl_secs, max_entries))),
            dependents: Arc::new(RwLock::new(TimedCache::new(ttl_secs, max_entries))),
            persistent: None,
        }
    }

    pub fn with_persistence(db: Arc<crate::db::schema::CozoDb>, ttl_secs: u64, max_entries: usize) -> Self {
        Self {
            dependencies: Arc::new(RwLock::new(TimedCache::new(ttl_secs, max_entries))),
            dependents: Arc::new(RwLock::new(TimedCache::new(ttl_secs, max_entries))),
            persistent: Some(Arc::new(PersistentCache::new(db, ttl_secs))),
        }
    }

    #[allow(dead_code)]
    pub async fn get_dependencies(&self, key: &str) -> Option<Vec<String>> {
        if let Some(v) = self.dependencies.read().await.get(&key.to_string()) {
            return Some(v);
        }
        if let Some(ref p) = self.persistent {
            let key_full = format!("deps:{}", key);
            if let Some(v) = p.get::<Vec<String>>(&key_full).await {
                self.dependencies.write().await.insert(key.to_string(), v.clone());
                return Some(v);
            }
        }
        None
    }

    pub async fn set_dependencies(&self, key: String, value: Vec<String>) {
        self.dependencies.write().await.insert(key.clone(), value.clone());
        if let Some(ref p) = self.persistent {
            let key_full = format!("deps:{}", key);
            p.insert::<String, Vec<String>>(key_full, value).await;
        }
    }

    #[allow(dead_code)]
    pub async fn get_dependents(&self, key: &str) -> Option<Vec<String>> {
        if let Some(v) = self.dependents.read().await.get(&key.to_string()) {
            return Some(v);
        }
        if let Some(ref p) = self.persistent {
            let key_full = format!("deps:{}", key);
            if let Some(v) = p.get::<Vec<String>>(&key_full).await {
                self.dependents.write().await.insert(key.to_string(), v.clone());
                return Some(v);
            }
        }
        None
    }

    pub async fn set_dependents(&self, key: String, value: Vec<String>) {
        self.dependents.write().await.insert(key.clone(), value.clone());
        if let Some(ref p) = self.persistent {
            let key_full = format!("deps:{}", key);
            p.insert::<String, Vec<String>>(key_full, value).await;
        }
    }

    pub async fn invalidate_file(&self, file_path: &str) {
        self.dependencies.write().await.invalidate_prefix(file_path);
        self.dependents.write().await.invalidate_prefix(file_path);
        if let Some(ref p) = self.persistent {
            p.invalidate_prefix(&format!("deps:{}", file_path)).await;
        }
    }

    #[allow(dead_code)]
    pub async fn clear(&self) {
        self.dependencies.write().await.clear();
        self.dependents.write().await.clear();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_timed_cache_basic() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 10);
        cache.insert("key1", "value1");
        assert_eq!(cache.get(&"key1"), Some("value1"));
    }

    #[test]
    fn test_timed_cache_expiry() {
        let mut cache = TimedCache::new(0, 10);
        cache.insert("key1", "value1");
        std::thread::sleep(Duration::from_millis(10));
        assert_eq!(cache.get(&"key1"), None);
    }

    #[test]
    fn test_timed_cache_max_entries() {
        let mut cache = TimedCache::new(60, 2);
        cache.insert("key1", "value1");
        cache.insert("key2", "value2");
        cache.insert("key3", "value3");
        assert!(cache.get(&"key1").is_none());
        assert!(cache.get(&"key2").is_some());
        assert!(cache.get(&"key3").is_some());
    }

    #[tokio::test]
    async fn test_query_cache_dependencies() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("file1.rs".to_string(), vec!["file2.rs".to_string()])
            .await;
        let result = cache.get_dependencies("file1.rs").await;
        assert_eq!(result, Some(vec!["file2.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_query_cache_invalidate() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("src/file1.rs".to_string(), vec!["file2.rs".to_string()])
            .await;
        cache.invalidate_file("src/file1.rs").await;
        let result = cache.get_dependencies("src/file1.rs").await;
        assert_eq!(result, None);
    }
}
