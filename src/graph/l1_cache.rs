use crate::db::models::{CodeElement, Relationship};
use crate::graph::GraphEngine;
use moka::future::Cache;
use std::sync::Arc;
use std::time::Duration;

/// Read-through L1 cache wrapping a GraphEngine for hot query paths.
/// Uses moka (lock-free, async-native, with TTL and LRU eviction).
#[derive(Clone)]
pub struct CachingGraphEngine {
    inner: GraphEngine,
    element_cache: Cache<String, Arc<Vec<CodeElement>>>,
    relationship_cache: Cache<String, Arc<Vec<Relationship>>>,
}

impl CachingGraphEngine {
    pub fn new(inner: GraphEngine) -> Self {
        let cache_size = std::env::var("LEANKG_L1_CACHE_SIZE")
            .ok()
            .and_then(|v| v.parse().ok())
            .unwrap_or(10_000);
        let cache_ttl = Duration::from_secs(
            std::env::var("LEANKG_L1_CACHE_TTL")
                .ok()
                .and_then(|v| v.parse().ok())
                .unwrap_or(60),
        );
        Self {
            inner,
            element_cache: Cache::builder()
                .max_capacity(cache_size as u64)
                .time_to_live(cache_ttl)
                .build(),
            relationship_cache: Cache::builder()
                .max_capacity(cache_size as u64)
                .time_to_live(cache_ttl)
                .build(),
        }
    }

    pub fn inner(&self) -> &GraphEngine {
        &self.inner
    }

    pub async fn get_elements_cached(
        &self,
        page_size: usize,
        offset: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let key = format!("el:{}:{}", page_size, offset);
        if let Some(cached) = self.element_cache.get(&key).await {
            return Ok((*cached).clone());
        }
        let (rows, _) = self.inner.get_elements_paginated(page_size, offset)?;
        self.element_cache.insert(key, Arc::new(rows.clone())).await;
        Ok(rows)
    }

    pub async fn get_relationships_cached(
        &self,
        page_size: usize,
        offset: usize,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let key = format!("rel:{}:{}", page_size, offset);
        if let Some(cached) = self.relationship_cache.get(&key).await {
            return Ok((*cached).clone());
        }
        let (rows, _) = self.inner.get_relationships_paginated(page_size, offset)?;
        self.relationship_cache
            .insert(key, Arc::new(rows.clone()))
            .await;
        Ok(rows)
    }
}
