use std::collections::HashMap;
use std::hash::{DefaultHasher, Hash, Hasher};
use std::time::Instant;

use super::estimate_tokens;

fn normalize_key(path: &str) -> String {
    path.to_string()
}

fn max_cache_tokens() -> usize {
    std::env::var("LEANKG_CACHE_MAX_TOKENS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(500_000)
}

fn compute_hash(content: &str) -> String {
    let mut hasher = DefaultHasher::new();
    content.hash(&mut hasher);
    format!("{:x}", hasher.finish())
}

#[derive(Clone, Debug)]
pub struct CacheEntry {
    pub content: String,
    pub hash: String,
    pub line_count: usize,
    pub original_tokens: usize,
    pub read_count: u32,
    pub path: String,
    pub last_access: Instant,
}

impl CacheEntry {
    /// Boltzmann-inspired eviction score. Higher = more valuable = keep longer.
    pub fn eviction_score(&self, now: Instant) -> f64 {
        let elapsed = now.duration_since(self.last_access).as_secs_f64();
        let recency = 1.0 / (1.0 + elapsed.sqrt());
        let frequency = (self.read_count as f64 + 1.0).ln();
        let size_value = (self.original_tokens as f64 + 1.0).ln();
        recency * 0.4 + frequency * 0.3 + size_value * 0.3
    }
}

pub struct SessionCache {
    entries: HashMap<String, CacheEntry>,
    file_refs: HashMap<String, String>,
    next_ref: usize,
}

impl Default for SessionCache {
    fn default() -> Self {
        Self::new()
    }
}

impl SessionCache {
    pub fn new() -> Self {
        Self {
            entries: HashMap::new(),
            file_refs: HashMap::new(),
            next_ref: 1,
        }
    }

    pub fn get_file_ref(&mut self, path: &str) -> String {
        let key = normalize_key(path);
        if let Some(r) = self.file_refs.get(&key) {
            return r.clone();
        }
        let r = format!("_F{}_", self.next_ref);
        self.next_ref += 1;
        self.file_refs.insert(key, r.clone());
        r
    }

    pub fn get(&self, path: &str) -> Option<&CacheEntry> {
        self.entries.get(&normalize_key(path))
    }

    pub fn invalidate(&mut self, path: &str) {
        let key = normalize_key(path);
        self.entries.remove(&key);
        self.file_refs.remove(&key);
    }

    pub fn record_cache_hit(&mut self, path: &str) -> Option<&CacheEntry> {
        let key = normalize_key(path);
        if let Some(entry) = self.entries.get_mut(&key) {
            entry.read_count += 1;
            entry.last_access = Instant::now();
            Some(entry)
        } else {
            None
        }
    }

    pub fn store(&mut self, path: &str, content: String) -> (CacheEntry, bool, Option<String>) {
        let key = normalize_key(path);
        let hash = compute_hash(&content);
        let line_count = content.lines().count();
        let original_tokens = estimate_tokens(&content);
        let now = Instant::now();

        if let Some(existing) = self.entries.get_mut(&key) {
            existing.last_access = now;
            if existing.hash == hash {
                existing.read_count += 1;
                return (existing.clone(), true, None);
            }
            let old_content = existing.content.clone();
            existing.content = content;
            existing.hash = hash.clone();
            existing.line_count = line_count;
            existing.original_tokens = original_tokens;
            existing.read_count += 1;
            return (existing.clone(), false, Some(old_content));
        }

        self.evict_if_needed(original_tokens);
        self.get_file_ref(&key);

        let entry = CacheEntry {
            content,
            hash,
            line_count,
            original_tokens,
            read_count: 1,
            path: key.clone(),
            last_access: now,
        };

        self.entries.insert(key.clone(), entry.clone());
        (entry, false, None)
    }

    pub fn total_cached_tokens(&self) -> usize {
        self.entries.values().map(|e| e.original_tokens).sum()
    }

    pub fn evict_if_needed(&mut self, incoming_tokens: usize) {
        let max_tokens = max_cache_tokens();
        let current = self.total_cached_tokens();
        if current + incoming_tokens <= max_tokens {
            return;
        }

        let now = Instant::now();
        let mut scored: Vec<(String, f64)> = self
            .entries
            .iter()
            .map(|(path, entry)| (path.clone(), entry.eviction_score(now)))
            .collect();
        scored.sort_by(|a, b| a.1.partial_cmp(&b.1).unwrap_or(std::cmp::Ordering::Equal));

        let mut freed = 0usize;
        let target = (current + incoming_tokens).saturating_sub(max_tokens);
        for (path, _score) in &scored {
            if freed >= target {
                break;
            }
            if let Some(entry) = self.entries.remove(path) {
                freed += entry.original_tokens;
                self.file_refs.remove(path);
            }
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_store_and_hit() {
        let mut cache = SessionCache::new();
        let (entry1, hit1, old1) = cache.store("dummy.rs", "pub fn main() {}".to_string());
        assert!(!hit1);
        assert!(old1.is_none());
        assert_eq!(entry1.read_count, 1);

        let (entry2, hit2, old2) = cache.store("dummy.rs", "pub fn main() {}".to_string());
        assert!(hit2);
        assert!(old2.is_none());
        assert_eq!(entry2.read_count, 2);

        let (_entry3, hit3, old3) = cache.store("dummy.rs", "pub fn diff() {}".to_string());
        assert!(!hit3);
        assert!(old3.is_some());
        assert_eq!(old3.unwrap(), "pub fn main() {}");
    }

    #[test]
    fn test_invalidation() {
        let mut cache = SessionCache::new();
        cache.store("target.rs", "data".to_string());
        assert!(cache.get("target.rs").is_some());

        cache.invalidate("target.rs");
        assert!(cache.get("target.rs").is_none());
    }

    #[test]
    fn test_eviction() {
        let mut cache = SessionCache::new();
        // Since LEANKG_CACHE_MAX_TOKENS = 500,000 natively, we'll force small eviction limit in env later or just test manual sizes.
        // Actually we can just unit test eviction logic directly.
        cache.store(
            "file1",
            "a b c d e f g h i j k l m n o p q r s t u v w x y z".to_string(),
        );
        cache.store("file2", "hello world".to_string());

        // Let's force an eviction constraint manually by calling evict_if_needed with a huge incoming token requirement
        cache.evict_if_needed(500_001); // Requires max size eviction!
        assert!(
            cache.entries.is_empty(),
            "Eviction should drain entries to make room"
        );
    }
}
