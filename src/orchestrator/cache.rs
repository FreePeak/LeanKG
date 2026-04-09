use crate::graph::persistent_cache::PersistentCache;
use std::collections::HashMap;
use std::sync::Arc;
use std::time::{Duration, Instant};

#[derive(Debug, Clone)]
pub struct OrchestratorCacheEntry {
    pub value: CachedContent,
    pub created_at: Instant,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct CachedContent {
    pub content: String,
    pub mode: String,
    pub tokens: usize,
    pub total_tokens: usize,
    pub savings_percent: f64,
    pub elements_count: usize,
}

pub struct OrchestratorCache {
    data: HashMap<String, OrchestratorCacheEntry>,
    ttl: Duration,
    max_entries: usize,
    persistent: Option<Arc<PersistentCache>>,
}

impl OrchestratorCache {
    pub fn new(ttl_secs: u64, max_entries: usize) -> Self {
        Self {
            data: HashMap::new(),
            ttl: Duration::from_secs(ttl_secs),
            max_entries,
            persistent: None,
        }
    }

    pub fn with_persistence(
        ttl_secs: u64,
        max_entries: usize,
        persistent: Arc<PersistentCache>,
    ) -> Self {
        Self {
            data: HashMap::new(),
            ttl: Duration::from_secs(ttl_secs),
            max_entries,
            persistent: Some(persistent),
        }
    }

    pub fn get(&self, key: &str) -> Option<CachedContent> {
        if let Some(entry) = self.data.get(key) {
            if entry.created_at.elapsed() < self.ttl {
                return Some(entry.value.clone());
            }
        }
        if let Some(ref p) = self.persistent {
            let key_full = format!("orch:{}", key);
            let rt = tokio::runtime::Runtime::new().unwrap();
            if let Some(v) = rt.block_on(p.get::<CachedContent>(&key_full)) {
                return Some(v);
            }
        }
        None
    }

    pub fn insert(&mut self, key: String, value: CachedContent) {
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
        let value_clone = value.clone();
        self.data.insert(
            key.clone(),
            OrchestratorCacheEntry {
                value,
                created_at: Instant::now(),
            },
        );
        if let Some(ref p) = self.persistent {
            let key_full = format!("orch:{}", key);
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(p.insert::<String, CachedContent>(key_full, value_clone));
        }
    }

    pub fn invalidate(&mut self, key: &str) {
        self.data.remove(key);
        if let Some(ref p) = self.persistent {
            let key_full = format!("orch:{}", key);
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(p.invalidate(&key_full));
        }
    }

    pub fn invalidate_prefix(&mut self, prefix: &str) {
        self.data.retain(|k, _| !k.starts_with(prefix));
        if let Some(ref p) = self.persistent {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(p.invalidate_prefix(&format!("orch:{}", prefix)));
        }
    }

    fn evict_expired(&mut self) {
        self.data
            .retain(|_, entry| entry.created_at.elapsed() < self.ttl);
    }

    pub fn clear(&mut self) {
        self.data.clear();
    }

    pub fn len(&self) -> usize {
        self.data.len()
    }

    pub fn is_empty(&self) -> bool {
        self.data.is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_cache_basic() {
        let mut cache = OrchestratorCache::new(60, 10);
        cache.insert(
            "key1".to_string(),
            CachedContent {
                content: "value1".to_string(),
                mode: "test".to_string(),
                tokens: 10,
                total_tokens: 100,
                savings_percent: 90.0,
                elements_count: 5,
            },
        );
        assert!(cache.get("key1").is_some());
    }

    #[test]
    fn test_cache_expiry() {
        let mut cache = OrchestratorCache::new(0, 10);
        cache.insert(
            "key1".to_string(),
            CachedContent {
                content: "value1".to_string(),
                mode: "test".to_string(),
                tokens: 10,
                total_tokens: 100,
                savings_percent: 90.0,
                elements_count: 5,
            },
        );
        std::thread::sleep(Duration::from_millis(10));
        assert!(cache.get("key1").is_none());
    }

    #[test]
    fn test_cache_max_entries() {
        let mut cache = OrchestratorCache::new(60, 2);
        for i in 0..3 {
            cache.insert(
                format!("key{}", i),
                CachedContent {
                    content: format!("value{}", i),
                    mode: "test".to_string(),
                    tokens: 10,
                    total_tokens: 100,
                    savings_percent: 90.0,
                    elements_count: 5,
                },
            );
        }
        assert!(cache.get("key0").is_none());
        assert!(cache.get("key1").is_some());
        assert!(cache.get("key2").is_some());
    }

    #[test]
    fn test_cache_invalidate() {
        let mut cache = OrchestratorCache::new(60, 10);
        cache.insert(
            "key1".to_string(),
            CachedContent {
                content: "value1".to_string(),
                mode: "test".to_string(),
                tokens: 10,
                total_tokens: 100,
                savings_percent: 90.0,
                elements_count: 5,
            },
        );
        cache.invalidate("key1");
        assert!(cache.get("key1").is_none());
    }
}
