use leankg::graph::cache::{CacheEntry, QueryCache, TimedCache};
use std::time::{Duration, Instant};

#[cfg(test)]
mod timed_cache_tests {

    use super::*;

    #[test]
    fn test_ttl_expiry_immediate_with_zero_ttl() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(0, 100);
        cache.insert("key1", "value1");
        assert_eq!(cache.get(&"key1"), None);
    }

    #[test]
    fn test_ttl_expiry_after_some_time() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(0, 100);
        cache.insert("key1", "value1");
        std::thread::sleep(Duration::from_millis(10));
        assert_eq!(cache.get(&"key1"), None);
    }

    #[test]
    fn test_ttl_not_expired_within_ttl() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        assert_eq!(cache.get(&"key1"), Some("value1"));
    }

    #[test]
    fn test_ttl_not_expired_concurrent_access() {
        let mut cache: TimedCache<String, Vec<u32>> = TimedCache::new(60, 100);
        cache.insert("nums".to_string(), vec![1, 2, 3]);
        let result = cache.get(&"nums".to_string());
        assert_eq!(result, Some(vec![1, 2, 3]));
    }

    #[test]
    fn test_max_entries_eviction_removes_oldest() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 2);
        cache.insert("key1", "value1");
        std::thread::sleep(Duration::from_millis(5));
        cache.insert("key2", "value2");
        std::thread::sleep(Duration::from_millis(5));
        cache.insert("key3", "value3");

        assert!(cache.get(&"key1").is_none());
        assert_eq!(cache.get(&"key2"), Some("value2"));
        assert_eq!(cache.get(&"key3"), Some("value3"));
    }

    #[test]
    fn test_max_entries_eviction_when_exactly_full() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 3);
        cache.insert("key1", "value1");
        cache.insert("key2", "value2");
        cache.insert("key3", "value3");
        assert_eq!(cache.len(), 3);
        assert!(cache.get(&"key1").is_some());
        assert!(cache.get(&"key2").is_some());
        assert!(cache.get(&"key3").is_some());
    }

    #[test]
    fn test_max_entries_eviction_on_insert() {
        let mut cache: TimedCache<i32, &str> = TimedCache::new(60, 2);
        cache.insert(1, "one");
        cache.insert(2, "two");
        cache.insert(3, "three");
        assert!(cache.get(&1).is_none());
        assert_eq!(cache.get(&2), Some("two"));
        assert_eq!(cache.get(&3), Some("three"));
    }

    #[test]
    fn test_invalidate_prefix_removes_matching_keys() {
        let mut cache: TimedCache<String, String> = TimedCache::new(60, 100);
        cache.insert("src/main.rs".to_string(), "content1".to_string());
        cache.insert("src/lib.rs".to_string(), "content2".to_string());
        cache.insert("tests/main.rs".to_string(), "content3".to_string());

        cache.invalidate_prefix("src/");

        assert!(cache.get(&"src/main.rs".to_string()).is_none());
        assert!(cache.get(&"src/lib.rs".to_string()).is_none());
        assert_eq!(
            cache.get(&"tests/main.rs".to_string()),
            Some("content3".to_string())
        );
    }

    #[test]
    fn test_invalidate_prefix_removes_all_prefixed() {
        let mut cache: TimedCache<String, i32> = TimedCache::new(60, 100);
        cache.insert("prefix_a".to_string(), 1);
        cache.insert("prefix_b".to_string(), 2);
        cache.insert("other".to_string(), 3);

        cache.invalidate_prefix("prefix_");

        assert!(cache.get(&"prefix_a".to_string()).is_none());
        assert!(cache.get(&"prefix_b".to_string()).is_none());
        assert_eq!(cache.get(&"other".to_string()), Some(3));
    }

    #[test]
    fn test_invalidate_prefix_no_match() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        cache.insert("key2", "value2");

        cache.invalidate_prefix("nonexistent");

        assert_eq!(cache.get(&"key1"), Some("value1"));
        assert_eq!(cache.get(&"key2"), Some("value2"));
    }

    #[test]
    fn test_invalidate_prefix_empty_cache() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.invalidate_prefix("any");
        assert!(cache.is_empty());
    }

    #[test]
    fn test_invalidate_removes_specific_key() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        cache.insert("key2", "value2");

        cache.invalidate(&"key1");

        assert!(cache.get(&"key1").is_none());
        assert_eq!(cache.get(&"key2"), Some("value2"));
    }

    #[test]
    fn test_invalidate_nonexistent_key() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        cache.invalidate(&"nonexistent");
        assert_eq!(cache.len(), 1);
    }

    #[test]
    fn test_clear_removes_all_entries() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        cache.insert("key2", "value2");
        cache.insert("key3", "value3");

        cache.clear();

        assert!(cache.is_empty());
        assert_eq!(cache.len(), 0);
    }

    #[test]
    fn test_len_returns_correct_count() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        assert_eq!(cache.len(), 0);
        cache.insert("key1", "value1");
        assert_eq!(cache.len(), 1);
        cache.insert("key2", "value2");
        assert_eq!(cache.len(), 2);
    }

    #[test]
    fn test_is_empty_returns_true_when_empty() {
        let cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        assert!(cache.is_empty());
    }

    #[test]
    fn test_is_empty_returns_false_when_not_empty() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 100);
        cache.insert("key1", "value1");
        assert!(!cache.is_empty());
    }

    #[test]
    fn test_cache_entry_clone() {
        let entry = CacheEntry {
            value: "test".to_string(),
            created_at: Instant::now(),
        };
        let cloned = entry.clone();
        assert_eq!(cloned.value, entry.value);
    }

    #[test]
    fn test_multiple_inserts_same_key_updates_value() {
        let mut cache: TimedCache<&str, &str> = TimedCache::new(60, 10);
        cache.insert("key1", "value1");
        cache.insert("key1", "value2");
        assert_eq!(cache.get(&"key1"), Some("value2"));
        assert_eq!(cache.len(), 1);
    }
}

#[cfg(test)]
mod query_cache_tests {

    use super::*;

    #[tokio::test]
    async fn test_set_and_get_dependencies() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies(
                "file1.rs".to_string(),
                vec!["file2.rs".to_string(), "file3.rs".to_string()],
            )
            .await;

        let result = cache.get_dependencies("file1.rs").await;
        assert_eq!(
            result,
            Some(vec!["file2.rs".to_string(), "file3.rs".to_string()])
        );
    }

    #[tokio::test]
    async fn test_set_and_get_dependents() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("file2.rs".to_string(), vec!["file1.rs".to_string()])
            .await;

        let result = cache.get_dependents("file2.rs").await;
        assert_eq!(result, Some(vec!["file1.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_get_dependencies_nonexistent() {
        let cache = QueryCache::new(60, 100);
        let result = cache.get_dependencies("nonexistent.rs").await;
        assert_eq!(result, None);
    }

    #[tokio::test]
    async fn test_get_dependents_nonexistent() {
        let cache = QueryCache::new(60, 100);
        let result = cache.get_dependents("nonexistent.rs").await;
        assert_eq!(result, None);
    }

    #[tokio::test]
    async fn test_invalidate_file_clears_dependencies() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("src/main.rs".to_string(), vec!["lib.rs".to_string()])
            .await;

        cache.invalidate_file("src/main.rs").await;

        let result = cache.get_dependencies("src/main.rs").await;
        assert_eq!(result, None);
    }

    #[tokio::test]
    async fn test_invalidate_file_clears_dependents() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("src/lib.rs".to_string(), vec!["main.rs".to_string()])
            .await;

        cache.invalidate_file("src/lib.rs").await;

        let result = cache.get_dependents("src/lib.rs").await;
        assert_eq!(result, None);
    }

    #[tokio::test]
    async fn test_invalidate_file_clears_both_caches() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("src/main.rs".to_string(), vec!["lib.rs".to_string()])
            .await;
        cache
            .set_dependents("src/main.rs".to_string(), vec!["test.rs".to_string()])
            .await;

        cache.invalidate_file("src/main.rs").await;

        let deps = cache.get_dependencies("src/main.rs").await;
        let depts = cache.get_dependents("src/main.rs").await;
        assert_eq!(deps, None);
        assert_eq!(depts, None);
    }

    #[tokio::test]
    async fn test_invalidate_file_with_nested_path() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies(
                "src/handlers/mod.rs".to_string(),
                vec!["lib.rs".to_string()],
            )
            .await;
        cache
            .set_dependents(
                "src/handlers/mod.rs".to_string(),
                vec!["main.rs".to_string()],
            )
            .await;

        cache.invalidate_file("src/handlers/mod.rs").await;

        let deps = cache.get_dependencies("src/handlers/mod.rs").await;
        let depts = cache.get_dependents("src/handlers/mod.rs").await;
        assert_eq!(deps, None);
        assert_eq!(depts, None);
    }

    #[tokio::test]
    async fn test_invalidate_file_preserves_other_entries() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("src/file1.rs".to_string(), vec!["lib.rs".to_string()])
            .await;
        cache
            .set_dependencies("src/file2.rs".to_string(), vec!["lib.rs".to_string()])
            .await;

        cache.invalidate_file("src/file1.rs").await;

        let file1_deps = cache.get_dependencies("src/file1.rs").await;
        let file2_deps = cache.get_dependencies("src/file2.rs").await;
        assert_eq!(file1_deps, None);
        assert_eq!(file2_deps, Some(vec!["lib.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_clear_clears_both_dependencies_and_dependents() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("file1.rs".to_string(), vec!["file2.rs".to_string()])
            .await;
        cache
            .set_dependents("file2.rs".to_string(), vec!["file1.rs".to_string()])
            .await;

        cache.clear().await;

        let deps = cache.get_dependencies("file1.rs").await;
        let depts = cache.get_dependents("file2.rs").await;
        assert_eq!(deps, None);
        assert_eq!(depts, None);
    }

    #[tokio::test]
    async fn test_clear_on_empty_cache() {
        let cache = QueryCache::new(60, 100);
        cache.clear().await;
        let deps = cache.get_dependencies("any.rs").await;
        let depts = cache.get_dependents("any.rs").await;
        assert_eq!(deps, None);
        assert_eq!(depts, None);
    }

    #[tokio::test]
    async fn test_multiple_dependencies_entries() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("a.rs".to_string(), vec!["b.rs".to_string()])
            .await;
        cache
            .set_dependencies("c.rs".to_string(), vec!["d.rs".to_string()])
            .await;

        assert_eq!(
            cache.get_dependencies("a.rs").await,
            Some(vec!["b.rs".to_string()])
        );
        assert_eq!(
            cache.get_dependencies("c.rs").await,
            Some(vec!["d.rs".to_string()])
        );
    }

    #[tokio::test]
    async fn test_multiple_dependents_entries() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("b.rs".to_string(), vec!["a.rs".to_string()])
            .await;
        cache
            .set_dependents("d.rs".to_string(), vec!["c.rs".to_string()])
            .await;

        assert_eq!(
            cache.get_dependents("b.rs").await,
            Some(vec!["a.rs".to_string()])
        );
        assert_eq!(
            cache.get_dependents("d.rs").await,
            Some(vec!["c.rs".to_string()])
        );
    }

    #[tokio::test]
    async fn test_update_existing_dependencies() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("file.rs".to_string(), vec!["old.rs".to_string()])
            .await;
        cache
            .set_dependencies("file.rs".to_string(), vec!["new.rs".to_string()])
            .await;

        let result = cache.get_dependencies("file.rs").await;
        assert_eq!(result, Some(vec!["new.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_update_existing_dependents() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("file.rs".to_string(), vec!["old.rs".to_string()])
            .await;
        cache
            .set_dependents("file.rs".to_string(), vec!["new.rs".to_string()])
            .await;

        let result = cache.get_dependents("file.rs").await;
        assert_eq!(result, Some(vec!["new.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_empty_vec_dependencies() {
        let cache = QueryCache::new(60, 100);
        cache.set_dependencies("file.rs".to_string(), vec![]).await;

        let result = cache.get_dependencies("file.rs").await;
        assert_eq!(result, Some(vec![]));
    }

    #[tokio::test]
    async fn test_empty_vec_dependents() {
        let cache = QueryCache::new(60, 100);
        cache.set_dependents("file.rs".to_string(), vec![]).await;

        let result = cache.get_dependents("file.rs").await;
        assert_eq!(result, Some(vec![]));
    }

    #[tokio::test]
    async fn test_concurrent_dependencies_access() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("file.rs".to_string(), vec!["dep.rs".to_string()])
            .await;

        let cache_clone = cache.clone();
        let handle = tokio::spawn(async move { cache_clone.get_dependencies("file.rs").await });

        let result = handle.await.unwrap();
        assert_eq!(result, Some(vec!["dep.rs".to_string()]));
    }

    #[tokio::test]
    async fn test_invalidate_nonexistent_file() {
        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("real.rs".to_string(), vec!["dep.rs".to_string()])
            .await;

        cache.invalidate_file("nonexistent.rs").await;

        let result = cache.get_dependencies("real.rs").await;
        assert_eq!(result, Some(vec!["dep.rs".to_string()]));
    }
}
