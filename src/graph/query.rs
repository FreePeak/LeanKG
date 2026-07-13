#![allow(clippy::needless_borrow)]
use crate::db::models::{
    BusinessLogic, CodeElement, DependencyInfo, DocLink, Incident, Relationship, TraceabilityEntry,
    TraceabilityReport,
};
use crate::db::schema::CozoDb;
use crate::graph::cache::QueryCache;
use serde::{Deserialize, Serialize};
use std::sync::Arc;
use tracing::debug;

fn escape_datalog(s: &str) -> String {
    s.replace('\\', "\\\\").replace('"', "\\\"")
}

const CODE_ELEMENTS_12_TAIL: &str = ", env";
const CODE_ELEMENTS_13_TAIL: &str = ", env, ontology_layer";

fn normalize_path(path: &str) -> String {
    let p = if path == "." || path.is_empty() {
        String::new()
    } else {
        path.strip_prefix("./").unwrap_or(path).to_string()
    };
    if p.is_empty() {
        String::new()
    } else {
        p
    }
}

/// Maximum number of elements/relationships to cache in memory.
/// Mega-graphs above this threshold skip the permanent cache to avoid
/// unbounded RSS growth (see root_cause_docker_memory_2026-07-13.md RC1).
/// Override via LEANKG_MAX_CACHE_ELEMENTS env var.
fn max_cache_elements() -> usize {
    std::env::var("LEANKG_MAX_CACHE_ELEMENTS")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(50_000)
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ChildrenResult {
    pub elements: Vec<CodeElement>,
    pub relationships: Vec<Relationship>,
    pub total_count: usize,
    pub has_more: bool,
}

#[derive(Clone)]
#[allow(clippy::type_complexity)]
pub struct GraphEngine {
    db: CozoDb,
    cache: QueryCache,
    elements_cache: std::sync::Arc<parking_lot::RwLock<Option<Vec<CodeElement>>>>,
    relationships_cache: std::sync::Arc<parking_lot::RwLock<Option<Vec<Relationship>>>>,
    // Secondary index: element_id -> indices of relationships involving that element
    relationships_by_element:
        std::sync::Arc<parking_lot::RwLock<Option<std::collections::HashMap<String, Vec<usize>>>>>,
}

impl GraphEngine {
    pub fn new(db: CozoDb) -> Self {
        Self {
            db,
            cache: QueryCache::new(300, 1000),
            elements_cache: std::sync::Arc::new(parking_lot::RwLock::new(None::<Vec<CodeElement>>)),
            relationships_cache: std::sync::Arc::new(parking_lot::RwLock::new(
                None::<Vec<Relationship>>,
            )),
            relationships_by_element: std::sync::Arc::new(parking_lot::RwLock::new(None)),
        }
    }

    #[allow(dead_code)]
    pub fn with_cache(db: CozoDb, cache: QueryCache) -> Self {
        Self {
            db,
            cache,
            elements_cache: std::sync::Arc::new(parking_lot::RwLock::new(None::<Vec<CodeElement>>)),
            relationships_cache: std::sync::Arc::new(parking_lot::RwLock::new(
                None::<Vec<Relationship>>,
            )),
            relationships_by_element: std::sync::Arc::new(parking_lot::RwLock::new(None)),
        }
    }

    pub fn with_persistence(db: CozoDb) -> Self {
        let db_arc = Arc::new(db);
        let cache = QueryCache::with_persistence(db_arc.clone(), 300, 1000);
        Self {
            db: (*db_arc).clone(),
            cache,
            elements_cache: std::sync::Arc::new(parking_lot::RwLock::new(None::<Vec<CodeElement>>)),
            relationships_cache: std::sync::Arc::new(parking_lot::RwLock::new(
                None::<Vec<Relationship>>,
            )),
            relationships_by_element: std::sync::Arc::new(parking_lot::RwLock::new(None)),
        }
    }

    pub fn db(&self) -> &CozoDb {
        &self.db
    }

    /// Run SQLite `VACUUM` against the underlying CozoDB SQLite store to
    /// reclaim disk space after large deletes. No-op for RocksDB backends.
    /// The operation can be expensive (rewrites the entire DB file), so
    /// callers should gate it on a size check first.
    pub fn vacuum(&self) -> Result<(), Box<dyn std::error::Error>> {
        if let Err(e) =
            crate::db::schema::run_script(&self.db, "VACUUM", std::collections::BTreeMap::new())
        {
            return Err(format!("VACUUM failed: {:?}", e).into());
        }
        self.invalidate_cache();
        Ok(())
    }

    fn code_elements_tail(&self) -> &'static str {
        let arity_13_probe = r#"?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env, ontology_layer] :limit 0"#;
        if crate::db::schema::run_script(
            &self.db,
            arity_13_probe,
            std::collections::BTreeMap::new(),
        )
        .is_ok()
        {
            CODE_ELEMENTS_13_TAIL
        } else {
            CODE_ELEMENTS_12_TAIL
        }
    }

    /// Invalidate all caches - call this when data changes (e.g., after indexing)
    pub fn invalidate_cache(&self) {
        *self.elements_cache.write() = None;
        *self.relationships_cache.write() = None;
        *self.relationships_by_element.write() = None;
    }

    /// Check if cache is valid (has data loaded)
    pub fn is_cache_valid(&self) -> bool {
        self.elements_cache.read().is_some()
            && self.relationships_cache.read().is_some()
            && self.relationships_by_element.read().is_some()
    }

    pub fn find_element(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], qualified_name = $qn"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(qualified_name.to_string()),
        );
        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].get_str().map(String::from);
        let cluster_id = row[8].get_str().map(String::from);
        let cluster_label = row[9].get_str().map(String::from);
        let metadata_str = row[10].get_str().unwrap_or("{}");

        Ok(Some(CodeElement {
            qualified_name: row[0].get_str().unwrap_or("").to_string(),
            element_type: row[1].get_str().unwrap_or("").to_string(),
            name: row[2].get_str().unwrap_or("").to_string(),
            file_path: row[3].get_str().unwrap_or("").to_string(),
            line_start: row[4].get_int().unwrap_or(0) as u32,
            line_end: row[5].get_int().unwrap_or(0) as u32,
            language: row[6].get_str().unwrap_or("").to_string(),
            parent_qualified,
            cluster_id,
            cluster_label,
            metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
            ..Default::default()
        }))
    }

    #[allow(dead_code)]
    pub fn find_element_by_name(
        &self,
        name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], name = $nm"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "nm".to_string(),
            serde_json::Value::String(name.to_string()),
        );
        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].get_str().map(String::from);
        let cluster_id = row[8].get_str().map(String::from);
        let cluster_label = row[9].get_str().map(String::from);
        let metadata_str = row[10].get_str().unwrap_or("{}");

        Ok(Some(CodeElement {
            qualified_name: row[0].get_str().unwrap_or("").to_string(),
            element_type: row[1].get_str().unwrap_or("").to_string(),
            name: row[2].get_str().unwrap_or("").to_string(),
            file_path: row[3].get_str().unwrap_or("").to_string(),
            line_start: row[4].get_int().unwrap_or(0) as u32,
            line_end: row[5].get_int().unwrap_or(0) as u32,
            language: row[6].get_str().unwrap_or("").to_string(),
            parent_qualified,
            cluster_id,
            cluster_label,
            metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
            ..Default::default()
        }))
    }

    pub fn get_dependencies(
        &self,
        file_path: &str,
    ) -> Result<Vec<DependencyInfo>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(file_path);

        // Check cache first
        let cache = self.cache.clone();
        let cache_key = normalized.clone();

        let cached_qns =
            crate::runtime::run_blocking(async { cache.get_dependencies(&cache_key).await });

        if let Some(cached_qns) = cached_qns {
            if !cached_qns.is_empty() {
                tracing::debug!("get_dependencies cache hit for {}", file_path);
                // Convert cached names back to DependencyInfo (without confidence)
                return Ok(cached_qns
                    .into_iter()
                    .map(|qn| DependencyInfo {
                        target_qualified: qn,
                        confidence: 1.0,
                    })
                    .collect());
            }
        }

        let query = r#"?[target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "imports""#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq1".to_string(),
            serde_json::Value::String(normalized.clone()),
        );
        params.insert(
            "sq2".to_string(),
            serde_json::Value::String(format!("./{}", normalized)),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let deps: Vec<DependencyInfo> = rows
            .iter()
            .filter_map(|row| {
                let target = row[0].get_str()?;
                if target.is_empty() {
                    return None;
                }
                Some(DependencyInfo {
                    target_qualified: target.to_string(),
                    confidence: row[2].get_float().unwrap_or(1.0),
                })
            })
            .collect();

        // Cache the qualified names
        if !deps.is_empty() {
            let qns: Vec<String> = deps.iter().map(|d| d.target_qualified.clone()).collect();
            let db_path = normalize_path(file_path);
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.set_dependencies(db_path, qns).await;
            });
        }

        Ok(deps)
    }

    pub fn get_relationships(
        &self,
        source: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(source);
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], (source_qualified = $sq1 or source_qualified = $sq2)"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq1".to_string(),
            serde_json::Value::String(normalized.clone()),
        );
        params.insert(
            "sq2".to_string(),
            serde_json::Value::String(format!("./{}", normalized)),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    env: row[5].get_str().unwrap_or("local").to_string(),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn get_relationships_for_target(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(target);
        let _escaped_normalized = escape_datalog(&normalized);

        let cache = self.cache.clone();
        let cache_key = normalized.clone();

        let cached_source_qns =
            crate::runtime::run_blocking(async { cache.get_dependents(&cache_key).await });

        if let Some(cached_source_qns) = cached_source_qns {
            if !cached_source_qns.is_empty() {
                tracing::debug!("get_relationships_for_target cache hit for {}", target);
                let relationships: Vec<Relationship> = cached_source_qns
                    .iter()
                    .map(|source_qn| Relationship {
                        id: None,
                        source_qualified: source_qn.clone(),
                        target_qualified: target.to_string(),
                        rel_type: "imports".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({}),
                        // TODO: cache doesn't track env
                        env: "local".to_string(),
                    })
                    .collect();
                return Ok(relationships);
            }
        }

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata, env] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], (target_qualified = $tq1 or target_qualified = $tq2)"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "tq1".to_string(),
            serde_json::Value::String(normalized.clone()),
        );
        params.insert(
            "tq2".to_string(),
            serde_json::Value::String(format!("./{}", normalized)),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    env: row[5].get_str().unwrap_or("local").to_string(),
                }
            })
            .collect();

        if !relationships.is_empty() {
            let qns: Vec<String> = relationships
                .iter()
                .map(|r| r.target_qualified.clone())
                .collect();
            let cache = self.cache.clone();
            let t = target.to_string();
            crate::runtime::get_runtime().spawn(async move {
                cache.set_dependents(t, qns).await;
            });
        }

        Ok(relationships)
    }

    pub fn get_dependents(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        self.get_relationships_for_target(target)
    }

    pub fn run_raw_query(
        &self,
        query: &str,
        params: std::collections::BTreeMap<String, serde_json::Value>,
    ) -> Result<cozo::NamedRows, Box<dyn std::error::Error + Send + Sync>> {
        crate::db::schema::run_script(&self.db, &query, params).map_err(|e| {
            let msg = e.to_string();
            Box::new(std::io::Error::other(msg)) as Box<dyn std::error::Error + Send + Sync>
        })
    }

    /// Get elements with pagination - memory efficient alternative to all_elements()
    /// Avoids loading entire database into memory at once
    pub fn get_elements_paginated(
        &self,
        limit: usize,
        offset: usize,
    ) -> Result<(Vec<CodeElement>, usize), Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let limit = limit.min(1000); // Cap to prevent excessive memory
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}] :limit {} :offset {}"#,
            limit, offset
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;

        let elements: Vec<CodeElement> = result
            .rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        // Total count for pagination (approximate, actual would need count query)
        let total_count = elements.len() + offset;
        Ok((elements, total_count))
    }

    /// Memory-efficient code element query for get_code_tree.
    /// Filters by element_type at the database level (avoids loading all elements)
    /// and caps results to prevent excessive memory on mega-graphs.
    /// See root_cause_docker_memory_2026-07-13.md RC1.
    pub fn get_code_elements_for_tree(
        &self,
        cap: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let cap = cap.min(50_000); // hard cap to protect memory
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end] :=
                *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
                element_type in ["function", "struct", "class", "module", "interface", "enum", "trait"]
                :limit {}"#,
            cap
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let elements: Vec<CodeElement> = result
            .rows
            .iter()
            .map(|row| CodeElement {
                qualified_name: row[0].get_str().unwrap_or("").to_string(),
                element_type: row[1].get_str().unwrap_or("").to_string(),
                name: row[2].get_str().unwrap_or("").to_string(),
                file_path: row[3].get_str().unwrap_or("").to_string(),
                line_start: row[4].get_int().unwrap_or(0) as u32,
                line_end: row[5].get_int().unwrap_or(0) as u32,
                ..Default::default()
            })
            .collect();
        Ok(elements)
    }

    /// Count code elements (function/struct/class/module/interface/enum/trait)
    /// for accurate pagination metadata in get_code_tree.
    pub fn count_code_elements(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[count(n)] :=
                *code_elements[n, et, a, b, c, d, e, f, g, h, i, j{tail}],
                et in ["function", "struct", "class", "module", "interface", "enum", "trait"]"#
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    /// Get relationships with pagination - memory efficient alternative to all_relationships()
    /// Avoids loading entire relationship set + building secondary index in memory
    pub fn get_relationships_paginated(
        &self,
        limit: usize,
        offset: usize,
    ) -> Result<(Vec<Relationship>, usize), Box<dyn std::error::Error>> {
        let limit = limit.min(1000); // Cap to prevent excessive memory
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _] :limit {} :offset {}"#,
            limit, offset
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;

        let relationships: Vec<Relationship> = result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        let total_count = relationships.len() + offset;
        Ok((relationships, total_count))
    }

    /// Get relationships for specific elements without loading all relationships into memory
    /// Uses database-level filtering instead of secondary index
    pub fn get_relationships_for_elements_paginated(
        &self,
        element_ids: &[String],
        rel_types: Option<&[&str]>,
        limit: usize,
        offset: usize,
    ) -> Result<(Vec<Relationship>, usize), Box<dyn std::error::Error>> {
        if element_ids.is_empty() {
            return Ok((vec![], 0));
        }

        let limit = limit.min(1000);
        let rel_types_filter: std::collections::HashSet<&str> = rel_types
            .map(|types| types.iter().copied().collect())
            .unwrap_or_default();

        // Build query with element filter at database level
        let element_patterns: Vec<String> = element_ids
            .iter()
            .map(|id| format!(r#"source_qualified = "{}""#, escape_datalog(id)))
            .collect();
        let source_filter = element_patterns.join(" or ");

        let (query, params) = if rel_types_filter.is_empty() {
            let q = format!(
                r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                    ({})
                    :limit {} :offset {}"#,
                source_filter, limit, offset
            );
            (q, std::collections::BTreeMap::new())
        } else {
            let types_str = rel_types_filter
                .iter()
                .map(|t| format!(r#""{}""#, t))
                .collect::<Vec<_>>()
                .join(", ");
            let q = format!(
                r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                    ({}),
                    rel_type in [{}]
                    :limit {} :offset {}"#,
                source_filter, types_str, limit, offset
            );
            (q, std::collections::BTreeMap::new())
        };

        let result = crate::db::schema::run_script(&self.db, &query, params)?;

        let relationships: Vec<Relationship> = result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        let total_count = relationships.len() + offset;
        Ok((relationships, total_count))
    }

    pub fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        // Check cache first
        if let Some(cached) = self.elements_cache.read().as_ref() {
            return Ok(cached.clone());
        }

        // DEPRECATED: Use get_elements_paginated() instead for memory efficiency
        // This method loads ALL elements into memory - problematic for large codebases
        tracing::warn!("all_elements() is deprecated - use get_elements_paginated() instead");

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}]"#
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                let env = row[11].get_str().unwrap_or("local").to_string();
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    env,
                }
            })
            .collect();

        // Store in cache ONLY when we have data AND graph is not too large.
        // Mega-graphs (> LEANKG_MAX_CACHE_ELEMENTS) are never cached to avoid
        // unbounded RSS growth (see root_cause_docker_memory_2026-07-13.md RC1).
        let max_cache = max_cache_elements();
        if !elements.is_empty() && elements.len() <= max_cache {
            *self.elements_cache.write() = Some(elements.clone());
        } else if elements.len() > max_cache {
            tracing::warn!(
                target: "leankg::mem",
                elements = elements.len(),
                max_cache,
                "skipping elements_cache for large graph (LEANKG_MAX_CACHE_ELEMENTS)"
            );
        }

        Ok(elements)
    }

    pub fn get_elements_in_folder(
        &self,
        folder_path: &str,
        limit: Option<usize>,
        offset: Option<usize>,
        all_content: bool,
    ) -> Result<ChildrenResult, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let limit = limit.unwrap_or(500).min(500);
        let offset = offset.unwrap_or(0);

        // When path is empty or ".", return root-level elements
        // If all_content=true, return all elements under root (for single-repo expansion)
        if folder_path.is_empty() || folder_path == "." {
            if all_content {
                // Load ALL elements under root (for single-repo when user wants full content)
                let query_str = format!(
                    "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}] :limit {} :offset {}",
                    limit,
                    offset
                );
                let result = crate::db::schema::run_script(
                    &self.db,
                    &query_str,
                    std::collections::BTreeMap::new(),
                )?;
                let total_count = result.rows.len();

                let elements: Vec<CodeElement> = result
                    .rows
                    .iter()
                    .map(|row| {
                        let parent_qualified = row[7].get_str().map(String::from);
                        let cluster_id = row[8].get_str().map(String::from);
                        let cluster_label = row[9].get_str().map(String::from);
                        let metadata_str = row[10].get_str().unwrap_or("{}");
                        CodeElement {
                            qualified_name: row[0].get_str().unwrap_or("").to_string(),
                            element_type: row[1].get_str().unwrap_or("").to_string(),
                            name: row[2].get_str().unwrap_or("").to_string(),
                            file_path: row[3].get_str().unwrap_or("").to_string(),
                            line_start: row[4].get_int().unwrap_or(0) as u32,
                            line_end: row[5].get_int().unwrap_or(0) as u32,
                            language: row[6].get_str().unwrap_or("").to_string(),
                            parent_qualified,
                            cluster_id,
                            cluster_label,
                            metadata: serde_json::from_str(metadata_str)
                                .unwrap_or(serde_json::json!({})),
                            ..Default::default()
                        }
                    })
                    .collect();

                let element_qns: std::collections::HashSet<String> =
                    elements.iter().map(|e| e.qualified_name.clone()).collect();

                let rel_query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                    source_qualified in $qns"#;
                let mut rel_params = std::collections::BTreeMap::new();
                rel_params.insert(
                    "qns".to_string(),
                    serde_json::Value::Array(
                        element_qns
                            .iter()
                            .map(|s| serde_json::Value::String(s.clone()))
                            .collect(),
                    ),
                );
                let rel_result = crate::db::schema::run_script(&self.db, rel_query, rel_params)?;
                let relationships: Vec<Relationship> = rel_result
                    .rows
                    .iter()
                    .map(|row| {
                        let metadata_str = row[4].get_str().unwrap_or("{}");
                        Relationship {
                            id: None,
                            source_qualified: row[0].get_str().unwrap_or("").to_string(),
                            target_qualified: row[1].get_str().unwrap_or("").to_string(),
                            rel_type: row[2].get_str().unwrap_or("").to_string(),
                            confidence: row[3].get_float().unwrap_or(1.0),
                            metadata: serde_json::from_str(metadata_str)
                                .unwrap_or(serde_json::json!({})),
                            ..Default::default()
                        }
                    })
                    .collect();

                let has_more = offset + limit < total_count;
                return Ok(ChildrenResult {
                    elements,
                    relationships,
                    total_count,
                    has_more,
                });
            }

            // For root without all_content, return direct children only
            // Query a reasonable number of rows (direct children are typically few)
            let query_str = format!(
                "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}] :limit {} :offset {}",
                5000,  // Large enough to get all root elements
                0
            );

            let result = crate::db::schema::run_script(
                &self.db,
                &query_str,
                std::collections::BTreeMap::new(),
            )?;
            let _total_count = result.rows.len();

            // Filter to direct children only (paths starting with "./" and containing exactly one "/")
            let all_direct: Vec<CodeElement> = result
                .rows
                .iter()
                .filter_map(|row| {
                    let file_path = row[3].get_str().unwrap_or("");
                    // Direct child: starts with "./", has at most one more "/" (for the child name)
                    let is_direct = file_path.starts_with("./") && !file_path[2..].contains('/');

                    if !is_direct {
                        return None;
                    }

                    let parent_qualified = row[7].get_str().map(String::from);
                    let cluster_id = row[8].get_str().map(String::from);
                    let cluster_label = row[9].get_str().map(String::from);
                    let metadata_str = row[10].get_str().unwrap_or("{}");
                    Some(CodeElement {
                        qualified_name: row[0].get_str().unwrap_or("").to_string(),
                        element_type: row[1].get_str().unwrap_or("").to_string(),
                        name: row[2].get_str().unwrap_or("").to_string(),
                        file_path: row[3].get_str().unwrap_or("").to_string(),
                        line_start: row[4].get_int().unwrap_or(0) as u32,
                        line_end: row[5].get_int().unwrap_or(0) as u32,
                        language: row[6].get_str().unwrap_or("").to_string(),
                        parent_qualified,
                        cluster_id,
                        cluster_label,
                        metadata: serde_json::from_str(metadata_str)
                            .unwrap_or(serde_json::json!({})),
                        ..Default::default()
                    })
                })
                .collect();

            // Apply offset/limit to direct children
            let total_direct_count = all_direct.len();
            let has_more = offset + limit < total_direct_count;
            let elements: Vec<CodeElement> =
                all_direct.into_iter().skip(offset).take(limit).collect();

            // Get relationships for these root elements
            let element_qns: std::collections::HashSet<String> =
                elements.iter().map(|e| e.qualified_name.clone()).collect();

            let rel_query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                source_qualified in $qns"#;
            let mut rel_params = std::collections::BTreeMap::new();
            rel_params.insert(
                "qns".to_string(),
                serde_json::Value::Array(
                    element_qns
                        .iter()
                        .map(|s| serde_json::Value::String(s.clone()))
                        .collect(),
                ),
            );
            let rel_result = crate::db::schema::run_script(&self.db, rel_query, rel_params)?;
            let relationships: Vec<Relationship> = rel_result
                .rows
                .iter()
                .map(|row| {
                    let metadata_str = row[4].get_str().unwrap_or("{}");
                    Relationship {
                        id: None,
                        source_qualified: row[0].get_str().unwrap_or("").to_string(),
                        target_qualified: row[1].get_str().unwrap_or("").to_string(),
                        rel_type: row[2].get_str().unwrap_or("").to_string(),
                        confidence: row[3].get_float().unwrap_or(1.0),
                        metadata: serde_json::from_str(metadata_str)
                            .unwrap_or(serde_json::json!({})),
                        ..Default::default()
                    }
                })
                .collect();

            return Ok(ChildrenResult {
                elements,
                relationships,
                total_count: total_direct_count,
                has_more,
            });
        }

        // For non-empty path, get elements in the folder (same as before but with limit/offset)
        let pattern = format!(
            ".*{}/.*",
            folder_path.replace('.', "\\.").replace('/', "\\/")
        );
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], regex_matches(file_path, $pat) :limit {} :offset {}"#,
            limit, offset
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert("pat".to_string(), serde_json::Value::String(pattern));

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let total_count = result.rows.len();

        // Filter to direct children only (same logic as search_elements)
        // When all_content=true, skip this filter to return all nested content
        let prefix_for_filter = if folder_path.is_empty() || folder_path == "." {
            "./".to_string()
        } else {
            format!("./{}/", folder_path.trim_start_matches("./"))
        };

        let elements: Vec<CodeElement> = result
            .rows
            .iter()
            .filter_map(|row| {
                let file_path = row[3].get_str().unwrap_or("");
                let _element_type = row[1].get_str().unwrap_or("");

                let remainder = file_path
                    .strip_prefix(&prefix_for_filter)
                    .unwrap_or(file_path);
                // Direct child = no additional path separator after the prefix
                // When all_content=true, we want all nested content (files, functions, etc.)
                let is_direct_child = if all_content {
                    true // No filtering when loading all content
                } else {
                    !remainder.contains('/')
                };

                if !is_direct_child {
                    return None;
                }

                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                Some(CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                })
            })
            .collect();

        // Get relationships for these elements
        let element_qns: std::collections::HashSet<String> =
            elements.iter().map(|e| e.qualified_name.clone()).collect();

        let rel_query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
            *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
            source_qualified in $qns"#;
        let mut rel_params = std::collections::BTreeMap::new();
        rel_params.insert(
            "qns".to_string(),
            serde_json::Value::Array(
                element_qns
                    .iter()
                    .map(|s| serde_json::Value::String(s.clone()))
                    .collect(),
            ),
        );
        let rel_result = crate::db::schema::run_script(&self.db, rel_query, rel_params)?;
        let relationships: Vec<Relationship> = rel_result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        let has_more = elements.len() == limit;

        Ok(ChildrenResult {
            elements,
            relationships,
            total_count,
            has_more,
        })
    }

    pub fn get_relationships_for_elements(
        &self,
        element_ids: &[String],
        rel_types: Option<&[&str]>,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        if element_ids.is_empty() {
            return Ok(vec![]);
        }

        // Ensure relationships are loaded (and index is built)
        let all_rels = self.all_relationships()?;
        let index = self.relationships_by_element.read();

        // Use the index to find relationship indices for our elements
        let mut relevant_indices: std::collections::HashSet<usize> =
            std::collections::HashSet::new();
        for element_id in element_ids {
            if let Some(indices) = index.as_ref().and_then(|i| i.get(element_id)) {
                for &idx in indices {
                    relevant_indices.insert(idx);
                }
            }
        }

        let rel_types_filter: std::collections::HashSet<&str> = rel_types
            .map(|types| types.iter().copied().collect())
            .unwrap_or_default();

        // Filter by relationship types if specified
        let relationships: Vec<Relationship> = relevant_indices
            .iter()
            .filter_map(|&idx| all_rels.get(idx).cloned())
            .filter(|r| {
                rel_types_filter.is_empty() || rel_types_filter.contains(r.rel_type.as_str())
            })
            .collect();

        Ok(relationships)
    }

    /// Memory-efficient version of get_relationships_for_elements
    /// Filters at database level instead of loading all relationships + building index
    pub fn get_relationships_for_elements_fast(
        &self,
        element_ids: &[String],
        rel_types: Option<&[&str]>,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        if element_ids.is_empty() {
            return Ok(vec![]);
        }

        // Build query with element filter at database level
        let element_patterns: Vec<String> = element_ids
            .iter()
            .map(|id| format!(r#"source_qualified = "{}""#, escape_datalog(id)))
            .collect();
        let source_filter = element_patterns.join(" or ");

        let rel_types_filter: std::collections::HashSet<&str> = rel_types
            .map(|types| types.iter().copied().collect())
            .unwrap_or_default();

        let (query, params) = if rel_types_filter.is_empty() {
            let q = format!(
                r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                    ({})
                    :limit 5000"#,
                source_filter
            );
            (q, std::collections::BTreeMap::new())
        } else {
            let types_str = rel_types_filter
                .iter()
                .map(|t| format!(r#""{}""#, t))
                .collect::<Vec<_>>()
                .join(", ");
            let q = format!(
                r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
                    ({}),
                    rel_type in [{}]
                    :limit 5000"#,
                source_filter, types_str
            );
            (q, std::collections::BTreeMap::new())
        };

        let result = crate::db::schema::run_script(&self.db, &query, params)?;

        let relationships: Vec<Relationship> = result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn all_relationships(&self) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        // Check cache first
        if let Some(cached) = self.relationships_cache.read().as_ref() {
            return Ok(cached.clone());
        }

        // DEPRECATED: Use get_relationships_paginated() or get_relationships_for_elements_paginated() instead
        // This method loads ALL relationships into memory AND builds a secondary index HashMap
        // For large codebases (52K+ relationships), this causes significant memory pressure
        tracing::warn!("all_relationships() is deprecated - use get_relationships_paginated() or get_relationships_for_elements_paginated() instead");

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _]"#;

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        // Build secondary index: element_id -> relationship indices
        // This HashMap alone can consume significant memory for large relationship sets
        let mut index: std::collections::HashMap<String, Vec<usize>> =
            std::collections::HashMap::new();
        for (idx, rel) in relationships.iter().enumerate() {
            index
                .entry(rel.source_qualified.clone())
                .or_default()
                .push(idx);
            index
                .entry(rel.target_qualified.clone())
                .or_default()
                .push(idx);
        }

        // Store in cache ONLY when graph is not too large.
        // Mega-graphs (> LEANKG_MAX_CACHE_ELEMENTS) skip cache to avoid
        // unbounded RSS growth (see root_cause_docker_memory_2026-07-13.md RC1).
        let max_cache = max_cache_elements();
        if relationships.len() <= max_cache {
            *self.relationships_cache.write() = Some(relationships.clone());
            *self.relationships_by_element.write() = Some(index);
        } else {
            tracing::warn!(
                target: "leankg::mem",
                relationships = relationships.len(),
                max_cache,
                "skipping relationships_cache for large graph (LEANKG_MAX_CACHE_ELEMENTS)"
            );
        }

        Ok(relationships)
    }

    #[allow(dead_code)]
    pub fn get_children(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], parent_qualified = $pq"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "pq".to_string(),
            serde_json::Value::String(parent_qualified.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn get_children_filtered(
        &self,
        parent_path: &str,
        element_types: Option<&[String]>,
        limit: Option<usize>,
        offset: Option<usize>,
    ) -> Result<ChildrenResult, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let normalized_prefix = normalize_path(parent_path);
        let prefix_with_slash = if normalized_prefix.is_empty() {
            String::new()
        } else {
            format!("{}/", normalized_prefix)
        };

        let limit = limit.unwrap_or(200).min(500);
        let offset = offset.unwrap_or(0);

        // Build type filter clause using CozoDB's = syntax
        // For multiple types, we use multiple = clauses separated by comma (AND logic)
        let type_clause = match element_types {
            Some(types) if !types.is_empty() => {
                // For simplicity, use only the first type if multiple specified
                // TODO: support multiple types with proper OR logic
                let t = &types[0];
                format!(", element_type = \"{}\"", t)
            }
            _ => String::new(),
        };

        let (query, params) = if prefix_with_slash.is_empty() {
            // Empty parent - return root-level direct children (no type filtering in this branch)
            // Note: Type filtering for empty parent would need a different approach
            let query = format!(
                "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}] :limit {} :offset {}",
                limit,
                offset
            );
            (query, std::collections::BTreeMap::new())
        } else {
            let stripped = prefix_with_slash
                .strip_prefix("./")
                .unwrap_or(prefix_with_slash.as_str())
                .trim_end_matches('/')
                .to_string();
            let literal_pattern = format!(".*{}/.*", stripped);
            let query = if type_clause.is_empty() {
                format!(
                    "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], regex_matches(file_path, $pat) :limit {} :offset {}",
                    limit, offset
                )
            } else {
                format!(
                    "?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], regex_matches(file_path, $pat), {} :limit {} :offset {}",
                    type_clause, limit, offset
                )
            };
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "pat".to_string(),
                serde_json::Value::String(literal_pattern),
            );
            (query, params)
        };

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let total_count = result.rows.len();
        let rows = result.rows;

        let prefix_for_filter = if prefix_with_slash.is_empty() {
            "./".to_string()
        } else {
            format!("./{}", prefix_with_slash)
        };

        // At root level (empty parent), exclude nested content types (function, class, method, etc.)
        // These are not "structural" children like files and folders
        let nested_types = [
            "function",
            "class",
            "method",
            "interface",
            "property",
            "struct",
            "enum",
        ];

        let elements: Vec<CodeElement> = rows
            .iter()
            .filter_map(|row| {
                let file_path = row[3].get_str().unwrap_or("");
                let element_type = row[1].get_str().unwrap_or("");
                let _qualified_name = row[0].get_str().unwrap_or("");

                let remainder = file_path
                    .strip_prefix(&prefix_for_filter)
                    .unwrap_or(file_path);
                // Direct child = no additional path separator after the prefix
                // At root level, also filter out nested content types
                let is_direct_child = !remainder.contains('/');
                let is_nested_content =
                    prefix_with_slash.is_empty() && nested_types.contains(&element_type);

                if !is_direct_child || is_nested_content {
                    return None;
                }

                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                Some(CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                })
            })
            .collect();

        let element_qns: std::collections::HashSet<String> =
            elements.iter().map(|e| e.qualified_name.clone()).collect();

        let rel_query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
    *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _],
    source_qualified in $qns"#;
        let mut rel_params = std::collections::BTreeMap::new();
        rel_params.insert(
            "qns".to_string(),
            serde_json::Value::Array(
                element_qns
                    .iter()
                    .map(|s| serde_json::Value::String(s.clone()))
                    .collect(),
            ),
        );
        let rel_result = crate::db::schema::run_script(&self.db, rel_query, rel_params)?;
        let relationships: Vec<Relationship> = rel_result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        let has_more = elements.len() == limit;

        Ok(ChildrenResult {
            elements,
            relationships,
            total_count,
            has_more,
        })
    }

    pub fn get_top_level_directories(
        &self,
        prefix: &str,
    ) -> Result<Vec<String>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let normalized_prefix = normalize_path(prefix);
        let prefix_with_slash = if normalized_prefix.is_empty() {
            String::new()
        } else {
            format!("{}/", normalized_prefix)
        };

        let lo = if prefix_with_slash.is_empty() {
            "./".to_string()
        } else {
            prefix_with_slash.clone()
        };
        let hi = if prefix_with_slash.is_empty() {
            "./\x7f".to_string()
        } else {
            format!("{}\x7f", prefix_with_slash)
        };
        let query = format!(
            r#"?[fp] := *code_elements[qualified_name, element_type, name, fp, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], fp >= $lo and fp < $hi"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert("lo".to_string(), serde_json::Value::String(lo));
        params.insert("hi".to_string(), serde_json::Value::String(hi));

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let mut directories: std::collections::HashSet<String> = std::collections::HashSet::new();

        for row in result.rows.iter() {
            if let Some(fp) = row[0].get_str() {
                let remainder = if prefix_with_slash.is_empty() {
                    fp.to_string()
                } else if let Some(idx) = fp.strip_prefix(&prefix_with_slash) {
                    idx.to_string()
                } else {
                    continue;
                };

                if let Some(first_slash) = remainder.find('/') {
                    let top_dir = remainder[..first_slash].to_string();
                    if !top_dir.is_empty() && !top_dir.starts_with('.') {
                        directories.insert(top_dir);
                    }
                }
            }
        }

        let mut dirs: Vec<String> = directories.into_iter().collect();
        dirs.sort();
        Ok(dirs)
    }

    pub fn get_annotation(
        &self,
        element_qualified: &str,
    ) -> Result<Option<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], element_qualified = $eq"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "eq".to_string(),
            serde_json::Value::String(element_qualified.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        Ok(Some(BusinessLogic {
            id: None,
            element_qualified: row[0].get_str().unwrap_or("").to_string(),
            description: row[1].get_str().unwrap_or("").to_string(),
            user_story_id: row[2].get_str().map(String::from),
            feature_id: row[3].get_str().map(String::from),
        }))
    }

    #[allow(dead_code)]
    pub fn search_annotations(
        &self,
        query_str: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let safe_pattern = escape_datalog(&query_str.to_lowercase());
        let query = format!(
            r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], regex_matches(lowercase(description), ".*{safe_pattern}.*")"#,
            safe_pattern = safe_pattern
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
                user_story_id: row[2].get_str().map(String::from),
                feature_id: row[3].get_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn all_annotations(&self) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id]"#;

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
                user_story_id: row[2].get_str().map(String::from),
                feature_id: row[3].get_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn get_documented_by(
        &self,
        element_qualified: &str,
    ) -> Result<Vec<DocLink>, Box<dyn std::error::Error>> {
        let normalized = normalize_path(element_qualified);
        let query = r#"?[source_qualified, target_qualified, rel_type, metadata, confidence] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], (source_qualified = $sq1 or source_qualified = $sq2), rel_type = "documented_by""#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq1".to_string(),
            serde_json::Value::String(normalized.clone()),
        );
        params.insert(
            "sq2".to_string(),
            serde_json::Value::String(format!("./{}", normalized)),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let doc_links: Vec<DocLink> = rows
            .iter()
            .filter_map(|row| {
                let doc_qualified = row[1].get_str().unwrap_or("").to_string();
                let _rel_type = row[2].get_str().unwrap_or("");
                let metadata_str = row.get(3).and_then(|v| v.get_str()).unwrap_or("{}");
                let metadata: serde_json::Value = serde_json::from_str(metadata_str).ok()?;

                let doc_title = metadata
                    .get("title")
                    .and_then(|v| v.as_str())
                    .unwrap_or("Untitled")
                    .to_string();
                let context = metadata
                    .get("context")
                    .and_then(|v| v.as_str())
                    .map(String::from);

                Some(DocLink {
                    doc_qualified,
                    doc_title,
                    context,
                })
            })
            .collect();

        Ok(doc_links)
    }

    pub fn get_traceability_report(
        &self,
        element_qualified: &str,
    ) -> Result<TraceabilityReport, Box<dyn std::error::Error>> {
        let bl = self.get_annotation(element_qualified)?;
        let doc_links = self.get_documented_by(element_qualified)?;

        let entry = TraceabilityEntry {
            element_qualified: element_qualified.to_string(),
            description: bl
                .as_ref()
                .map(|b| b.description.clone())
                .unwrap_or_default(),
            user_story_id: bl.as_ref().and_then(|b| b.user_story_id.clone()),
            feature_id: bl.as_ref().and_then(|b| b.feature_id.clone()),
            doc_links,
        };

        Ok(TraceabilityReport {
            element_qualified: element_qualified.to_string(),
            entries: vec![entry],
            count: 1,
        })
    }

    pub fn get_code_for_requirement(
        &self,
        requirement_id: &str,
    ) -> Result<Vec<TraceabilityEntry>, Box<dyn std::error::Error>> {
        let bl_entries = self.get_business_logic_by_user_story(requirement_id)?;

        let mut entries = Vec::new();
        for bl in bl_entries {
            let doc_links = self.get_documented_by(&bl.element_qualified)?;

            entries.push(TraceabilityEntry {
                element_qualified: bl.element_qualified,
                description: bl.description,
                user_story_id: bl.user_story_id,
                feature_id: bl.feature_id,
                doc_links,
            });
        }

        Ok(entries)
    }

    pub fn get_business_logic_by_user_story(
        &self,
        user_story_id: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] := *business_logic[element_qualified, description, user_story_id, feature_id], user_story_id = $uid"#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "uid".to_string(),
            serde_json::Value::String(user_story_id.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let business_logic: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].get_str().unwrap_or("").to_string(),
                description: row[1].get_str().unwrap_or("").to_string(),
                user_story_id: row[2].get_str().map(String::from),
                feature_id: row[3].get_str().map(String::from),
            })
            .collect();

        Ok(business_logic)
    }

    pub fn insert_elements(
        &self,
        elements: &[CodeElement],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if elements.is_empty() {
            return Ok(());
        }

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- $batch_data :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata }"#;

        let batch_data: Vec<serde_json::Value> = elements
            .iter()
            .map(|element| {
                let metadata_str =
                    serde_json::to_string(&element.metadata).unwrap_or_else(|_| "{}".to_string());
                serde_json::json!([
                    element.qualified_name.clone(),
                    element.element_type.clone(),
                    element.name.clone(),
                    element.file_path.clone(),
                    element.line_start as i64,
                    element.line_end as i64,
                    element.language.clone(),
                    element.parent_qualified.clone(),
                    element.cluster_id.clone(),
                    element.cluster_label.clone(),
                    metadata_str
                ])
            })
            .collect();

        for chunk in batch_data.chunks(1000) {
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "batch_data".to_string(),
                serde_json::Value::Array(chunk.to_vec()),
            );
            crate::db::schema::run_script(&self.db, &query, params)?;
        }

        let mut unique_files = std::collections::HashSet::new();
        for element in elements {
            unique_files.insert(element.file_path.clone());
        }

        for fp in unique_files {
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&fp).await;
            });
        }

        Ok(())
    }

    pub fn insert_element(&self, element: &CodeElement) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&element.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "qn".to_string(),
            serde_json::Value::String(element.qualified_name.clone()),
        );
        params.insert(
            "et".to_string(),
            serde_json::Value::String(element.element_type.clone()),
        );
        params.insert(
            "nm".to_string(),
            serde_json::Value::String(element.name.clone()),
        );
        params.insert(
            "fp".to_string(),
            serde_json::Value::String(element.file_path.clone()),
        );
        params.insert(
            "ls".to_string(),
            serde_json::Value::Number(element.line_start.into()),
        );
        params.insert(
            "le".to_string(),
            serde_json::Value::Number(element.line_end.into()),
        );
        params.insert(
            "lg".to_string(),
            serde_json::Value::String(element.language.clone()),
        );
        match &element.parent_qualified {
            Some(pq) => params.insert("pq".to_string(), serde_json::Value::String(pq.clone())),
            None => params.insert("pq".to_string(), serde_json::Value::Null),
        };
        match &element.cluster_id {
            Some(cid) => params.insert("cid".to_string(), serde_json::Value::String(cid.clone())),
            None => params.insert("cid".to_string(), serde_json::Value::Null),
        };
        match &element.cluster_label {
            Some(cl) => params.insert("cl".to_string(), serde_json::Value::String(cl.clone())),
            None => params.insert("cl".to_string(), serde_json::Value::Null),
        };
        params.insert("md".to_string(), serde_json::Value::String(metadata_str));

        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] <- [[ $qn, $et, $nm, $fp, $ls, $le, $lg, $pq, $cid, $cl, $md ]] :put code_elements { qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata }"#;

        crate::db::schema::run_script(&self.db, &query, params)?;

        let cache = self.cache.clone();
        let fp = element.file_path.clone();
        crate::runtime::get_runtime().spawn(async move {
            cache.invalidate_file(&fp).await;
        });

        Ok(())
    }

    pub fn update_element_cluster(
        &self,
        qualified_name: &str,
        cluster_id: Option<String>,
        cluster_label: Option<String>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        if let Some(mut element) = self.find_element(qualified_name)? {
            // Remove the specific original element securely
            let query = format!(
                r#"
                ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
                    *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], qualified_name = $qn
                :rm code_elements {{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}}
            "#
            );
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "qn".to_string(),
                serde_json::Value::String(qualified_name.to_string()),
            );
            crate::db::schema::run_script(&self.db, &query, params)?;

            // Apply new cluster attributes and natively reinsert mapped into caches and DB
            element.cluster_id = cluster_id;
            element.cluster_label = cluster_label;
            self.insert_elements(&[element])?;
        }
        Ok(())
    }

    pub fn insert_relationship(
        &self,
        relationship: &Relationship,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let metadata_str = serde_json::to_string(&relationship.metadata)?;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq".to_string(),
            serde_json::Value::String(relationship.source_qualified.clone()),
        );
        params.insert(
            "tq".to_string(),
            serde_json::Value::String(relationship.target_qualified.clone()),
        );
        params.insert(
            "rt".to_string(),
            serde_json::Value::String(relationship.rel_type.clone()),
        );
        params.insert("cn".to_string(), serde_json::json!(relationship.confidence));
        params.insert("md".to_string(), serde_json::Value::String(metadata_str));

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] <- [[ $sq, $tq, $rt, $cn, $md ]] :put relationships { source_qualified, target_qualified, rel_type, confidence, metadata }"#;

        crate::db::schema::run_script(&self.db, &query, params)?;

        Ok(())
    }

    pub fn insert_relationships(
        &self,
        relationships: &[Relationship],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if relationships.is_empty() {
            return Ok(());
        }

        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] <- $batch_data :put relationships { source_qualified, target_qualified, rel_type, confidence, metadata }"#;

        let batch_data: Vec<serde_json::Value> = relationships
            .iter()
            .map(|rel| {
                let metadata_str =
                    serde_json::to_string(&rel.metadata).unwrap_or_else(|_| "{}".to_string());
                serde_json::json!([
                    rel.source_qualified.clone(),
                    rel.target_qualified.clone(),
                    rel.rel_type.clone(),
                    rel.confidence,
                    metadata_str
                ])
            })
            .collect();

        for chunk in batch_data.chunks(1000) {
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "batch_data".to_string(),
                serde_json::Value::Array(chunk.to_vec()),
            );
            crate::db::schema::run_script(&self.db, &query, params)?;
        }

        let mut unique_sources = std::collections::HashSet::new();
        for rel in relationships {
            unique_sources.insert(rel.source_qualified.clone());
        }

        for source in unique_sources {
            let cache = self.cache.clone();
            crate::runtime::get_runtime().spawn(async move {
                cache.invalidate_file(&source).await;
            });
        }

        Ok(())
    }

    pub fn remove_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
                *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], file_path = $fp
            :rm code_elements {{qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata}}
        "#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "fp".to_string(),
            serde_json::Value::String(file_path.to_string()),
        );

        crate::db::schema::run_script(&self.db, &query, params)?;

        let cache = self.cache.clone();
        let fp = file_path.to_string();
        crate::runtime::get_runtime().spawn(async move {
            cache.invalidate_file(&fp).await;
        });

        Ok(())
    }

    pub fn remove_relationships_by_source(
        &self,
        source: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = r#"
            ?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $sq
            :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq".to_string(),
            serde_json::Value::String(source.to_string()),
        );

        crate::db::schema::run_script(&self.db, &query, params)?;

        let cache = self.cache.clone();
        let s = source.to_string();
        crate::runtime::get_runtime().spawn(async move {
            cache.invalidate_file(&s).await;
        });

        Ok(())
    }

    pub fn get_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], file_path = $fp"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "fp".to_string(),
            serde_json::Value::String(file_path.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_name(
        &self,
        name: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let lower_name = name.to_lowercase();
        let safe_name = escape_datalog(&regex::escape(&lower_name));
        let cache_key = format!("search:name:{}", lower_name);

        // Check cache first
        if let Some(cached) = self.cache.get_search(&cache_key) {
            return Ok(cached);
        }

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], regex_matches(lowercase(name), ".*{safe_name}.*")"#,
            safe_name = safe_name
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        // Cache results
        self.cache.set_search(cache_key, elements.clone());

        Ok(elements)
    }

    pub fn search_by_type(
        &self,
        element_type: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = "{}""#,
            element_type
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_pattern(
        &self,
        pattern: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := 
            *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
            str_includes(lowercase(qualified_name), lowercase($pattern))"#
        );

        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "pattern".to_string(),
            serde_json::Value::String(pattern.to_string()),
        );

        let result = crate::db::schema::run_script(&self.db, &query, params)?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_content(
        &self,
        pattern: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let lower_pattern = pattern.to_lowercase();
        let safe_pattern = escape_datalog(&lower_pattern);
        let cache_key = format!("search:content:{}", lower_pattern);

        if let Some(cached) = self.cache.get_search(&cache_key) {
            return Ok(cached);
        }

        // Substring match (case-insensitive) across name, qualified_name, and file_path.
        // This is intentionally broader than search_by_pattern (qualified_name only) and
        // search_by_name (name only) so users can find symbols whose name is split across
        // naming conventions (e.g. snake_case query matching camelCase symbols).
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
               str_includes(lowercase(name), "{pattern}")
               or str_includes(lowercase(qualified_name), "{pattern}")
               or str_includes(lowercase(file_path), "{pattern}")
               :limit 200"#,
            tail = tail,
            pattern = safe_pattern,
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        self.cache.set_search(cache_key, elements.clone());
        Ok(elements)
    }

    pub fn search_by_relation_type(
        &self,
        rel_type: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let escaped = escape_datalog(rel_type);
        let query = format!(
            r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], rel_type = "{}""#,
            escaped
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[4].get_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].get_str().unwrap_or("").to_string(),
                    target_qualified: row[1].get_str().unwrap_or("").to_string(),
                    rel_type: row[2].get_str().unwrap_or("").to_string(),
                    confidence: row[3].get_float().unwrap_or(1.0),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn find_oversized_functions(
        &self,
        min_lines: u32,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = "function", (line_end - line_start + 1) >= {}"#,
            min_lines
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        elements.sort_by(|a, b| {
            let a_lines = a.line_end - a.line_start + 1;
            let b_lines = b.line_end - b.line_start + 1;
            b_lines.cmp(&a_lines)
        });

        Ok(elements)
    }

    pub fn find_oversized_functions_by_lang(
        &self,
        min_lines: u32,
        language: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = "function", language = "{}", (line_end - line_start + 1) >= {}"#,
            language, min_lines
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect();

        elements.sort_by(|a, b| {
            let a_lines = a.line_end - a.line_start + 1;
            let b_lines = b.line_end - b.line_start + 1;
            b_lines.cmp(&a_lines)
        });

        Ok(elements)
    }

    fn run_element_query(
        &self,
        query: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;
        Ok(result
            .rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    ..Default::default()
                }
            })
            .collect())
    }

    pub fn search_by_name_typed(
        &self,
        name: &str,
        element_type: Option<&str>,
        limit: usize,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let lower_name = name.to_lowercase();
        let safe_name = escape_datalog(&regex::escape(&lower_name));
        let (filter_clause, has_type_filter) = match element_type {
            Some(t) => (format!(r#", element_type = "{}""#, escape_datalog(t)), true),
            None => (String::new(), false),
        };
        let query = if has_type_filter {
            format!(
                r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
                   := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}]{filter_clause},
                  regex_matches(lowercase(name), "{pattern}")
               :limit {limit}"#,
                filter_clause = filter_clause,
                pattern = safe_name,
                limit = limit,
            )
        } else {
            format!(
                r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
                   := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
                  regex_matches(lowercase(name), "{pattern}")
               :limit {limit}"#,
                pattern = safe_name,
                limit = limit,
            )
        };
        self.run_element_query(&query)
    }

    #[allow(dead_code)]
    pub fn find_elements_by_name_exact(
        &self,
        name: &str,
        element_type: Option<&str>,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let safe_name = escape_datalog(name);
        let type_clause = match element_type {
            Some(t) => format!(r#", element_type = "{}""#, escape_datalog(t)),
            None => String::new(),
        };
        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata]
               := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}]{type_clause},
              name = "{name}"
           :limit 20"#,
            type_clause = type_clause,
            name = safe_name,
        );
        self.run_element_query(&query)
    }

    pub fn get_callers(
        &self,
        function_name: &str,
        file_scope: Option<&str>,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let safe_name = escape_datalog(function_name);

        let file_filter = match file_scope {
            Some(f) => format!(r#", regex_matches(file_path, ".*{}.*")"#, escape_datalog(f)),
            None => String::new(),
        };

        // Query callers: find source_qualified values that call the target function
        let query = format!(
            r#"?[src, tgt, rel_type, conf, meta] :=
               *relationships[src, tgt, rel_type, conf, meta, _],
               rel_type = "calls",
               regex_matches(tgt, ".*{function_name}.*")
               :limit 50"#,
            function_name = safe_name
        );

        let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;

        if result.rows.is_empty() {
            return Ok(vec![]);
        }

        // Now get code elements for the caller sources
        let caller_sources: Vec<String> = result
            .rows
            .iter()
            .filter_map(|r| r[0].get_str().map(|s| s.to_string()))
            .collect();

        if caller_sources.is_empty() {
            return Ok(vec![]);
        }

        // Build query to get code elements for these sources
        let sources_pattern = caller_sources
            .iter()
            .map(|s| format!(r#"qualified_name = "{}""#, escape_datalog(s)))
            .collect::<Vec<_>>()
            .join(" or ");

        let element_query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] :=
               *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}],
               ({sources}){file_filter}
               :limit 50"#,
            sources = sources_pattern,
            file_filter = file_filter
        );

        self.run_element_query(&element_query)
    }

    #[allow(clippy::type_complexity)]
    pub fn get_call_graph_bounded(
        &self,
        source_qualified: &str,
        max_depth: u32,
        max_results: usize,
    ) -> Result<Vec<(String, String, u32)>, Box<dyn std::error::Error>> {
        // Resolve short function name to full qualified name
        let resolved = if source_qualified.contains("::") {
            normalize_path(source_qualified)
        } else {
            self.find_element_by_name(source_qualified)?
                .map(|e| e.qualified_name)
                .unwrap_or_else(|| source_qualified.to_string())
        };

        let mut all_calls: Vec<(String, String, u32)> = Vec::new();
        let mut frontier: Vec<String> = vec![resolved.clone()];
        let mut visited: std::collections::HashSet<String> = std::collections::HashSet::new();

        for depth in 0..max_depth {
            if frontier.is_empty() {
                break;
            }
            let mut next_frontier: Vec<String> = Vec::new();
            for src in &frontier {
                if visited.contains(src) {
                    continue;
                }
                visited.insert(src.clone());

                let filter = if src.contains("::") {
                    format!(r#"src = "{}""#, escape_datalog(src))
                } else {
                    format!(
                        r#"(src = "{}" or src = "./{}")"#,
                        escape_datalog(src),
                        escape_datalog(src)
                    )
                };

                let query = format!(
                    r#"?[src, tgt] :=
                       *relationships[src, tgt, rel_type, conf, meta, _],
                       rel_type = "calls",
                       {}
                       :limit {}"#,
                    filter, max_results,
                );

                let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;
                for row in &result.rows {
                    let tgt = row[1].get_str().unwrap_or("").to_string();
                    if !visited.contains(&tgt) && !tgt.starts_with("__unresolved__") {
                        next_frontier.push(tgt.clone());
                    }
                    all_calls.push((src.clone(), tgt, depth + 1));
                }
            }
            frontier = next_frontier;
            if all_calls.len() >= max_results {
                all_calls.truncate(max_results);
                break;
            }
        }

        Ok(all_calls)
    }

    pub fn resolve_call_edges(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], rel_type = "calls""#;
        debug!("Running resolve_call_edges query (filtered at DB level)");
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;

        let unresolved_rows: Vec<_> = result
            .rows
            .iter()
            .filter(|row| {
                let target = row[1].get_str().unwrap_or("");
                target.starts_with("__unresolved__")
            })
            .collect();

        let total_unresolved = unresolved_rows.len();
        debug!(
            "Found {} unresolved call edges to resolve",
            total_unresolved
        );

        if total_unresolved == 0 {
            return Ok(0);
        }

        debug!("Loading all functions into memory for fast lookup...");
        let functions_query = format!(
            r#"?[qualified_name, name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = "function""#
        );
        let func_result = crate::db::schema::run_script(
            &self.db,
            &functions_query,
            std::collections::BTreeMap::new(),
        )?;

        let mut by_name_and_file: std::collections::HashMap<(String, String), (String, f64)> =
            std::collections::HashMap::new();
        let mut by_name: std::collections::HashMap<String, (String, f64)> =
            std::collections::HashMap::new();

        for row in &func_result.rows {
            let qn = row[0].get_str().unwrap_or("").to_string();
            let name = row[1].get_str().unwrap_or("").to_string();
            let file_path = row[2].get_str().unwrap_or("").to_string();
            if !qn.is_empty() && !name.is_empty() {
                by_name_and_file.insert((name.clone(), file_path.clone()), (qn.clone(), 1.0));
                if !by_name.contains_key(&name) {
                    by_name.insert(name.clone(), (qn.clone(), 0.7));
                }
            }
        }
        debug!("Loaded {} functions into memory", by_name.len());

        let mut to_insert: Vec<Relationship> = Vec::new();
        let mut to_delete_keys: Vec<[serde_json::Value; 5]> = Vec::new();

        for row in unresolved_rows.iter() {
            let source = row[0].get_str().unwrap_or("").to_string();
            let target_qualified = row[1].get_str().unwrap_or("");
            let meta_str = row[4].get_str().unwrap_or("{}");

            let bare_name = target_qualified
                .trim_start_matches("__unresolved__")
                .to_string();

            let callee_file_hint: Option<String> =
                serde_json::from_str::<serde_json::Value>(meta_str)
                    .ok()
                    .and_then(|m| m.get("callee_file_hint").cloned())
                    .and_then(|v| v.as_str().map(String::from));

            let target_qn = if let Some(hint) = &callee_file_hint {
                by_name_and_file
                    .get(&(bare_name.clone(), hint.clone()))
                    .map(|(qn, _)| qn.clone())
                    .or_else(|| by_name.get(&bare_name).map(|(qn, _)| qn.clone()))
                    .unwrap_or_else(|| bare_name.clone())
            } else {
                by_name
                    .get(&bare_name)
                    .map(|(qn, _)| qn.clone())
                    .unwrap_or_else(|| bare_name.clone())
            };

            to_delete_keys.push([
                serde_json::Value::String(source.clone()),
                serde_json::Value::String(target_qualified.to_string()),
                serde_json::Value::String("calls".to_string()),
                serde_json::Value::from(row[3].get_float().unwrap_or(1.0)),
                serde_json::Value::String(row[4].get_str().unwrap_or("{}").to_string()),
            ]);
            to_insert.push(Relationship {
                id: None,
                source_qualified: source,
                target_qualified: target_qn,
                rel_type: "calls".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({}),
                ..Default::default()
            });
        }

        self._batch_delete_unresolved_calls(&to_delete_keys)?;
        self.insert_relationships(&to_insert)?;

        debug!("Resolved {} call edges", to_insert.len());

        Ok(to_insert.len())
    }

    fn _batch_delete_unresolved_calls(
        &self,
        keys: &[[serde_json::Value; 5]],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if keys.is_empty() {
            return Ok(());
        }
        for chunk in keys.chunks(1000) {
            let batch_data: Vec<serde_json::Value> = chunk
                .iter()
                .map(|row| serde_json::Value::Array(row.to_vec()))
                .collect();

            let query = r#"
                ?[source_qualified, target_qualified, rel_type, confidence, metadata] <-
                    $batch_data
                :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
            "#;
            let mut params = std::collections::BTreeMap::new();
            params.insert(
                "batch_data".to_string(),
                serde_json::Value::Array(batch_data),
            );
            crate::db::schema::run_script(&self.db, &query, params)?;
        }
        Ok(())
    }

    #[allow(dead_code)]
    fn find_function_by_name_with_confidence(
        &self,
        name: &str,
        file_hint: Option<&str>,
    ) -> Result<(Option<String>, f64), Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let safe_name = escape_datalog(name);

        if let Some(hint) = file_hint {
            let safe_hint = escape_datalog(hint);
            let query = format!("?[qualified_name, file_path] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = \"function\", name = \"{}\", file_path = \"{}\" :limit 1", safe_name, safe_hint);
            let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;
            if let Some(row) = result.rows.first() {
                let qn = row[0].get_str().map(String::from);
                let found_file = row[1].get_str().unwrap_or("");
                let confidence = if found_file == hint { 1.0 } else { 0.9 };
                return Ok((qn, confidence));
            }
        }

        let query = format!("?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], element_type = \"function\", name = \"{}\" :limit 1", safe_name);
        let result = crate::db::schema::run_script(&self.db, &query, Default::default())?;
        Ok((
            result
                .rows
                .first()
                .and_then(|row| row[0].get_str().map(String::from)),
            0.7,
        ))
    }

    fn _delete_relationship(
        &self,
        source: &str,
        target: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = r#"
            ?[source_qualified, target_qualified, rel_type, confidence, metadata] :=
                *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], source_qualified = $sq, target_qualified = $tq, rel_type = "calls"
            :rm relationships {source_qualified, target_qualified, rel_type, confidence, metadata}
        "#;
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "sq".to_string(),
            serde_json::Value::String(source.to_string()),
        );
        params.insert(
            "tq".to_string(),
            serde_json::Value::String(target.to_string()),
        );

        crate::db::schema::run_script(&self.db, &query, params)?;
        Ok(())
    }

    pub fn get_service_graph(
        &self,
        current_service: &str,
    ) -> Result<ServiceGraph, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, rel_type, confidence, metadata] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, _], rel_type = "service_calls""#;
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;

        let mut service_connections: std::collections::HashMap<
            (String, String),
            Vec<serde_json::Value>,
        > = std::collections::HashMap::new();
        let mut all_services: std::collections::HashSet<String> = std::collections::HashSet::new();

        all_services.insert(current_service.to_string());

        for row in &result.rows {
            let source = row[0].get_str().unwrap_or("").to_string();
            let target = row[1].get_str().unwrap_or("").to_string();
            let metadata_str = row[4].get_str().unwrap_or("{}");
            let metadata: serde_json::Value =
                serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({}));

            all_services.insert(source.clone());
            all_services.insert(target.clone());

            let key = (source.clone(), target.clone());
            service_connections.entry(key).or_default().push(metadata);
        }

        let mut nodes: Vec<ServiceNode> = Vec::new();
        let mut edges: Vec<ServiceEdge> = Vec::new();

        let current_lc = current_service.to_lowercase();
        for service in &all_services {
            let connection_count = service_connections
                .keys()
                .filter(|(s, t)| s == service || t == service)
                .count();

            let is_current = service.to_lowercase() == current_lc;
            let weight = if is_current {
                10.0
            } else {
                1.0 + (connection_count as f64 * 0.5).min(5.0)
            };

            nodes.push(ServiceNode {
                id: service.clone(),
                label: service.clone(),
                is_current_service: is_current,
                weight,
                connection_count,
            });
        }

        nodes.sort_by(|a, b| {
            b.weight
                .partial_cmp(&a.weight)
                .unwrap_or(std::cmp::Ordering::Equal)
        });

        let mut seen_edges: std::collections::HashSet<(String, String)> =
            std::collections::HashSet::new();
        for ((source, target), metas) in &service_connections {
            if seen_edges.contains(&(source.clone(), target.clone())) {
                continue;
            }
            seen_edges.insert((source.clone(), target.clone()));

            let protocols: std::collections::HashSet<String> = metas
                .iter()
                .filter_map(|m| m.get("protocol").and_then(|p| p.as_str()).map(String::from))
                .collect();

            edges.push(ServiceEdge {
                id: format!("{}_{}", source, target),
                source_id: source.clone(),
                target_id: target.clone(),
                call_count: metas.len(),
                protocols: protocols.into_iter().collect(),
                rel_type: "service_calls".to_string(),
            });
        }

        let total_connections = edges.len();

        Ok(ServiceGraph {
            nodes,
            edges,
            current_service: current_service.to_string(),
            total_services: all_services.len(),
            total_connections,
        })
    }

    pub fn count_elements(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query =
            format!(r#"?[count(n)] := *code_elements[n, a, b, c, d, e, f, g, h, i, j{tail}]"#);
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    pub fn has_elements(&self) -> Result<bool, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}] :limit 1"#
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(!result.rows.is_empty())
    }

    pub fn count_relationships(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let query = r#"?[count(n)] := *relationships[n, a, b, c, d, _]"#;
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    pub fn count_business_logic(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let query = r#"?[count(n)] := *business_logic[n, a, b, c]"#;
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    pub fn count_files(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"files[f] := *code_elements[n, a, b, f, c, d, e, g, h, i, j{tail}]
?[count(f)] := files[f]"#
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    pub fn count_by_element_type(
        &self,
        element_type: &str,
    ) -> Result<usize, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let query = format!(
            r#"?[count(n)] := *code_elements[n, t, a, b, c, d, e, f, g, h, i{tail}], t = "{}""#,
            element_type
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|r| r[0].get_int())
            .unwrap_or(0) as usize)
    }

    pub fn query_incidents(
        &self,
        service: Option<&str>,
        pattern: Option<&str>,
        env: &str,
        limit: usize,
    ) -> Result<Vec<Incident>, String> {
        let mut conditions = vec![format!("env = \"{}\"", escape_datalog(env))];

        if let Some(svc) = service {
            let safe_svc = escape_datalog(&format!(".*{}.*", regex::escape(&svc.to_lowercase())));
            conditions.push(format!(
                "regex_matches(lowercase(affected_services), \"{}\")",
                safe_svc
            ));
        }

        if let Some(pat) = pattern {
            let safe_pat = escape_datalog(&format!(".*{}.*", regex::escape(&pat.to_lowercase())));
            conditions.push(format!(
                "(regex_matches(lowercase(title), \"{}\") or regex_matches(lowercase(root_cause), \"{}\"))",
                safe_pat, safe_pat
            ));
        }

        let where_clause = conditions.join(", ");
        let query = format!(
            r#"?[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket] := *incidents[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket], {} :limit {}"#,
            where_clause, limit
        );

        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())
                .map_err(|e| e.to_string())?;

        let mut incidents: Vec<Incident> = result
            .rows
            .iter()
            .map(|r| {
                let affected_services: Vec<String> =
                    serde_json::from_str(r[8].get_str().unwrap_or("[]")).unwrap_or_default();
                let tags: Vec<String> =
                    serde_json::from_str(r[11].get_str().unwrap_or("[]")).unwrap_or_default();

                Incident {
                    id: r[0].get_str().unwrap_or("").to_string(),
                    env: r[1].get_str().unwrap_or("local").to_string(),
                    title: r[2].get_str().unwrap_or("").to_string(),
                    severity: r[3].get_str().unwrap_or("").to_string(),
                    occurred_at: r[4].get_int().unwrap_or(0),
                    resolved_at: r[5].get_int(),
                    root_cause: r[6].get_str().unwrap_or("").to_string(),
                    resolution: r[7].get_str().unwrap_or("").to_string(),
                    affected_services,
                    trigger_pattern: r[9].get_str().map(String::from),
                    prevention: r[10].get_str().map(String::from),
                    tags,
                    author: r[12].get_str().unwrap_or("").to_string(),
                    linked_ticket: r[13].get_str().map(String::from),
                }
            })
            .collect();

        incidents.sort_by_key(|b| std::cmp::Reverse(b.occurred_at));
        incidents.truncate(limit);
        Ok(incidents)
    }

    pub fn get_service_context(&self, service: &str, env: &str) -> Result<ServiceContext, String> {
        let tail = self.code_elements_tail();
        let safe_service = escape_datalog(service);
        let safe_env = escape_datalog(env);

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], qualified_name = "{}", env = "{}""#,
            safe_service, safe_env
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())
                .map_err(|e| e.to_string())?;

        let version = if let Some(row) = result.rows.first() {
            let metadata_str = row[10].get_str().unwrap_or("{}");
            let metadata: serde_json::Value =
                serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({}));
            metadata
                .get("version")
                .and_then(|v| v.as_str())
                .map(String::from)
        } else {
            None
        };

        let (team, on_call, repo_url, language) = self.get_service_metadata_fields(service, env);

        let outgoing_query = format!(
            r#"?[target_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], source_qualified = "{}", env = "{}", (rel_type = "calls" or rel_type = "service_calls")"#,
            safe_service, safe_env
        );
        let outgoing_result = crate::db::schema::run_script(
            &self.db,
            &outgoing_query,
            std::collections::BTreeMap::new(),
        )
        .map_err(|e| e.to_string())?;
        let calls: Vec<String> = outgoing_result
            .rows
            .iter()
            .filter_map(|r| r[0].get_str().map(String::from))
            .collect();

        let incoming_query = format!(
            r#"?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env], target_qualified = "{}", env = "{}", (rel_type = "calls" or rel_type = "service_calls")"#,
            safe_service, safe_env
        );
        let incoming_result = crate::db::schema::run_script(
            &self.db,
            &incoming_query,
            std::collections::BTreeMap::new(),
        )
        .map_err(|e| e.to_string())?;
        let called_by: Vec<String> = incoming_result
            .rows
            .iter()
            .filter_map(|r| r[0].get_str().map(String::from))
            .collect();

        let service_prefix = format!("./{}", service);
        let schemas_query = format!(
            r#"?[name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], starts_with(file_path, "{}"), regex_matches(element_type, "(schema|protobuf|proto|openapi|json_schema|avro|sql_table|event|topic|config)")"#,
            escape_datalog(&service_prefix)
        );
        let schemas: Vec<String> = crate::db::schema::run_script(
            &self.db,
            &schemas_query,
            std::collections::BTreeMap::new(),
        )
        .map(|r| {
            r.rows
                .iter()
                .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
                .collect()
        })
        .unwrap_or_default();

        let incidents_query = format!(
            r#"?[id, resolved_at, title, occurred_at, prevention, root_cause] := *incidents[id, env, title, severity, occurred_at, resolved_at, root_cause, resolution, affected_services, trigger_pattern, prevention, tags, author, linked_ticket], regex_matches(lowercase(affected_services), "{}"), env = "{}""#,
            escape_datalog(&format!(".*{}.*", regex::escape(&service.to_lowercase()))),
            safe_env
        );
        let incidents_result = crate::db::schema::run_script(
            &self.db,
            &incidents_query,
            std::collections::BTreeMap::new(),
        )
        .map_err(|e| e.to_string())?;

        let open_incidents = incidents_result
            .rows
            .iter()
            .filter(|r| matches!(r[1], cozo::DataValue::Bot | cozo::DataValue::Null))
            .count() as i64;

        let mut recent: Vec<(i64, String)> = incidents_result
            .rows
            .iter()
            .filter_map(|r| {
                let title = r[2].get_str().map(String::from)?;
                let occurred = r[3].get_int()?;
                Some((occurred, title))
            })
            .collect();
        recent.sort_by_key(|b| std::cmp::Reverse(b.0));
        let recent_incidents: Vec<String> = recent.iter().take(3).map(|(_, t)| t.clone()).collect();

        let last_incident = recent.first().map(|(ts, t)| format!("{}: {}", ts, t));

        let known_risks: Vec<String> = incidents_result
            .rows
            .iter()
            .filter_map(|r| {
                let prevention = r[4].get_str()?;
                if prevention.is_empty() {
                    None
                } else {
                    let root = r[5].get_str().unwrap_or("");
                    Some(format!("{} (root: {})", prevention, root))
                }
            })
            .take(5)
            .collect();

        Ok(ServiceContext {
            service: service.to_string(),
            env: env.to_string(),
            version,
            team,
            on_call,
            repo_url,
            language,
            calls,
            called_by,
            schemas,
            open_incidents,
            recent_incidents,
            last_incident,
            known_risks,
        })
    }

    fn get_service_metadata_fields(
        &self,
        service: &str,
        env: &str,
    ) -> (
        Option<String>,
        Option<String>,
        Option<String>,
        Option<String>,
    ) {
        let query = format!(
            r#"?[team, on_call, repo_url, language] := *service_metadata[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at], service_name = "{}", env = "{}""#,
            escape_datalog(service),
            escape_datalog(env)
        );
        let result =
            crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new()).ok();
        result
            .and_then(|r| {
                r.rows.first().map(|row| {
                    (
                        row[0].get_str().filter(|s| !s.is_empty()).map(String::from),
                        row[1].get_str().filter(|s| !s.is_empty()).map(String::from),
                        row[2].get_str().filter(|s| !s.is_empty()).map(String::from),
                        row[3].get_str().filter(|s| !s.is_empty()).map(String::from),
                    )
                })
            })
            .unwrap_or((None, None, None, None))
    }

    pub fn find_env_conflicts(&self, service: &str) -> Result<Vec<EnvConflict>, String> {
        let tail = self.code_elements_tail();
        let envs = vec!["local", "staging", "production"];
        let mut env_elements: std::collections::HashMap<String, Option<CodeElement>> =
            std::collections::HashMap::new();

        for env in &envs {
            let query = format!(
                r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], qualified_name = "{}", env = "{}""#,
                escape_datalog(service),
                escape_datalog(env)
            );
            let result =
                crate::db::schema::run_script(&self.db, &query, std::collections::BTreeMap::new())
                    .map_err(|e| e.to_string())?;

            let element = result.rows.first().map(|row| {
                let parent_qualified = row[7].get_str().map(String::from);
                let cluster_id = row[8].get_str().map(String::from);
                let cluster_label = row[9].get_str().map(String::from);
                let metadata_str = row[10].get_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].get_str().unwrap_or("").to_string(),
                    element_type: row[1].get_str().unwrap_or("").to_string(),
                    name: row[2].get_str().unwrap_or("").to_string(),
                    file_path: row[3].get_str().unwrap_or("").to_string(),
                    line_start: row[4].get_int().unwrap_or(0) as u32,
                    line_end: row[5].get_int().unwrap_or(0) as u32,
                    language: row[6].get_str().unwrap_or("").to_string(),
                    parent_qualified,
                    cluster_id,
                    cluster_label,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                    env: row[11].get_str().unwrap_or("local").to_string(),
                }
            });
            env_elements.insert(env.to_string(), element);
        }

        let mut conflicts = Vec::new();

        for env in &envs {
            if env_elements.get(*env).unwrap_or(&None).is_none() {
                conflicts.push(EnvConflict {
                    conflict_type: "missing_in_env".to_string(),
                    detail: format!("Service '{}' is missing in {} environment", service, env),
                    risk: if *env == "production" {
                        "HIGH".to_string()
                    } else {
                        "MEDIUM".to_string()
                    },
                });
            }
        }

        let present_envs: Vec<(String, &serde_json::Value)> = env_elements
            .iter()
            .filter_map(|(env, elem)| elem.as_ref().map(|e| (env.clone(), &e.metadata)))
            .collect();

        if present_envs.len() >= 2 {
            let base = &present_envs[0];
            for (env, metadata) in &present_envs[1..] {
                if base.1 != *metadata {
                    let base_version = base
                        .1
                        .get("version")
                        .and_then(|v| v.as_str())
                        .unwrap_or("unknown");
                    let env_version = metadata
                        .get("version")
                        .and_then(|v| v.as_str())
                        .unwrap_or("unknown");

                    if base_version != env_version {
                        conflicts.push(EnvConflict {
                            conflict_type: "schema_version".to_string(),
                            detail: format!(
                                "Version mismatch: {} has '{}', {} has '{}'",
                                base.0, base_version, env, env_version
                            ),
                            risk: "HIGH".to_string(),
                        });
                    } else {
                        conflicts.push(EnvConflict {
                            conflict_type: "config_drift".to_string(),
                            detail: format!("Metadata differs between {} and {}", base.0, env),
                            risk: "MEDIUM".to_string(),
                        });
                    }
                }
            }
        }

        Ok(conflicts)
    }

    /// FR-B22: Truncate a JSON array section to max_items entries, recording
    /// truncation metadata if any items were dropped.
    fn truncate_section(
        section_name: &str,
        items: Vec<serde_json::Value>,
        max_items: Option<usize>,
        truncated_sections: &mut Vec<serde_json::Value>,
    ) -> Vec<serde_json::Value> {
        match max_items {
            Some(n) if items.len() > n => {
                let original_count = items.len();
                let truncated: Vec<serde_json::Value> = items.into_iter().take(n).collect();
                truncated_sections.push(serde_json::json!({
                    "section": section_name,
                    "original_count": original_count,
                    "returned_count": n,
                }));
                truncated
            }
            _ => items,
        }
    }

    /// FR-B20: Get architecture overview - languages, packages, entry points,
    /// routes, hotspots, clusters, knowledge counts.
    /// FR-B22: Honors token budgets via per-section max_items truncation.
    /// When max_items is Some(n), each array section is capped at n entries and
    /// truncated_sections reports which sections were trimmed and their original counts.
    pub fn get_architecture(
        &self,
        max_items: Option<usize>,
    ) -> Result<serde_json::Value, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let mut truncated_sections: Vec<serde_json::Value> = Vec::new();

        let lang_query = format!(
            r#"?[language, count(language)] := *code_elements[_, _, _, _, _, _, language, _, _, _, _{tail}]
:order -count(language)"#
        );
        let lang_result = crate::db::schema::run_script(
            &self.db,
            &lang_query,
            std::collections::BTreeMap::new(),
        )?;
        let languages: Vec<serde_json::Value> = lang_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "language": row[0].get_str().unwrap_or("unknown"),
                    "element_count": row[1].get_int().unwrap_or(0),
                })
            })
            .collect();

        let entry_query = format!(
            r#"?[qualified_name, file_path, language] := *code_elements[
                qualified_name, "function", name, file_path, _, _, language, _, _, _, _{tail}
            ], (name = "main" or name = "Main" or name = "start" or name = "serve" or name = "Start")"#
        );
        let entry_result = crate::db::schema::run_script(
            &self.db,
            &entry_query,
            std::collections::BTreeMap::new(),
        )?;
        let entry_points: Vec<serde_json::Value> = entry_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "qualified_name": row[0].get_str().unwrap_or(""),
                    "file_path": row[1].get_str().unwrap_or(""),
                    "language": row[2].get_str().unwrap_or(""),
                })
            })
            .collect();

        let cluster_query = format!(
            r#"?[cluster_label, cluster_id, count(qn)] := *code_elements[
                qn, _, _, _, _, _, _, _, cluster_id, cluster_label, _{tail}
            ], cluster_id != null, cluster_id != """#,
        );
        let cluster_result = crate::db::schema::run_script(
            &self.db,
            &cluster_query,
            std::collections::BTreeMap::new(),
        )?;
        let clusters: Vec<serde_json::Value> = cluster_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "label": row[0].get_str().unwrap_or(""),
                    "cluster_id": row[1].get_str().unwrap_or(""),
                    "element_count": row[2].get_int().unwrap_or(0),
                })
            })
            .collect();

        let rel_query =
            r#"?[rel_type, count(rel_type)] := *relationships[_, _, rel_type, _, _, _]"#;
        let rel_result =
            crate::db::schema::run_script(&self.db, rel_query, std::collections::BTreeMap::new())?;
        let relationship_counts: Vec<serde_json::Value> = rel_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "rel_type": row[0].get_str().unwrap_or(""),
                    "count": row[1].get_int().unwrap_or(0),
                })
            })
            .collect();

        let hotspot_query = format!(
            r#"?[file_path, count(qualified_name)] := *code_elements[
                qualified_name, "function", _, file_path, _, _, _, _, _, _, _{tail}
            ], file_path != ""
:order -count(qualified_name)
:limit 10"#
        );
        let hotspot_result = crate::db::schema::run_script(
            &self.db,
            &hotspot_query,
            std::collections::BTreeMap::new(),
        )?;
        let hotspots: Vec<serde_json::Value> = hotspot_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "file_path": row[0].get_str().unwrap_or(""),
                    "function_count": row[1].get_int().unwrap_or(0),
                })
            })
            .collect();

        // Find routes
        let route_query = format!(
            r#"?[qualified_name, file_path, metadata] := *code_elements[
                qualified_name, "route", name, file_path, _, _, language, _, _, _, metadata{tail}
            ]"#
        );
        let route_result = crate::db::schema::run_script(
            &self.db,
            &route_query,
            std::collections::BTreeMap::new(),
        )?;
        let routes: Vec<serde_json::Value> = route_result
            .rows
            .iter()
            .map(|row| {
                let metadata_str = row[2].get_str().unwrap_or("{}");
                let metadata: serde_json::Value =
                    serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({}));
                serde_json::json!({
                    "qualified_name": row[0].get_str().unwrap_or(""),
                    "file_path": row[1].get_str().unwrap_or(""),
                    "method": metadata.get("method").and_then(|v| v.as_str()).unwrap_or(""),
                    "path": metadata.get("path").and_then(|v| v.as_str()).unwrap_or(""),
                    "framework": metadata.get("framework").and_then(|v| v.as_str()).unwrap_or(""),
                })
            })
            .collect();

        let languages =
            Self::truncate_section("languages", languages, max_items, &mut truncated_sections);
        let entry_points = Self::truncate_section(
            "entry_points",
            entry_points,
            max_items,
            &mut truncated_sections,
        );
        let routes = Self::truncate_section("routes", routes, max_items, &mut truncated_sections);
        let clusters =
            Self::truncate_section("clusters", clusters, max_items, &mut truncated_sections);
        let hotspots =
            Self::truncate_section("hotspots", hotspots, max_items, &mut truncated_sections);
        let relationship_counts = Self::truncate_section(
            "relationship_summary",
            relationship_counts,
            max_items,
            &mut truncated_sections,
        );

        Ok(serde_json::json!({
            "languages": languages,
            "entry_points": entry_points,
            "routes": routes,
            "clusters": clusters,
            "hotspots": hotspots,
            "relationship_summary": relationship_counts,
            "knowledge_count": self.count_knowledge().unwrap_or(0),
            "total_elements": self.count_elements().unwrap_or(0),
            "total_files": self.count_files().unwrap_or(0),
            "max_items": max_items,
            "truncated_sections": truncated_sections,
        }))
    }

    fn count_knowledge(&self) -> Result<usize, Box<dyn std::error::Error>> {
        let query = r#"?[count(id)] := *knowledge_entries[id, _, _, _, _, _, _, _, _, _, _, _, _]"#;
        let result =
            crate::db::schema::run_script(&self.db, query, std::collections::BTreeMap::new())?;
        Ok(result
            .rows
            .first()
            .and_then(|row| row.first()?.get_int())
            .unwrap_or(0) as usize)
    }

    /// FR-B21: Get graph schema - element type counts, relationship type counts.
    /// FR-B22: Honors token budgets via per-section max_items truncation.
    pub fn get_graph_schema(
        &self,
        max_items: Option<usize>,
    ) -> Result<serde_json::Value, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let mut truncated_sections: Vec<serde_json::Value> = Vec::new();
        let type_query = format!(
            r#"?[element_type, count(element_type)] := *code_elements[
                _, element_type, _, _, _, _, _, _, _, _, _{tail}
            ]
:order -count(element_type)"#
        );
        let type_result = crate::db::schema::run_script(
            &self.db,
            &type_query,
            std::collections::BTreeMap::new(),
        )?;
        let element_types: Vec<serde_json::Value> = type_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "element_type": row[0].get_str().unwrap_or(""),
                    "count": row[1].get_int().unwrap_or(0),
                })
            })
            .collect();

        let rel_query = r#"?[rel_type, count(rel_type)] := *relationships[_, _, rel_type, _, _, _]
:order -count(rel_type)"#;
        let rel_result =
            crate::db::schema::run_script(&self.db, rel_query, std::collections::BTreeMap::new())?;
        let relationship_types: Vec<serde_json::Value> = rel_result
            .rows
            .iter()
            .map(|row| {
                serde_json::json!({
                    "rel_type": row[0].get_str().unwrap_or(""),
                    "count": row[1].get_int().unwrap_or(0),
                })
            })
            .collect();

        let element_types = Self::truncate_section(
            "element_types",
            element_types,
            max_items,
            &mut truncated_sections,
        );
        let relationship_types = Self::truncate_section(
            "relationship_types",
            relationship_types,
            max_items,
            &mut truncated_sections,
        );

        Ok(serde_json::json!({
            "element_types": element_types,
            "relationship_types": relationship_types,
            "total_elements": self.count_elements().unwrap_or(0),
            "total_relationships": self.count_relationships().unwrap_or(0),
            "max_items": max_items,
            "truncated_sections": truncated_sections,
        }))
    }

    /// FR-B23: Find dead code - functions with zero callers, excluding entry points.
    /// Implemented as a candidate fetch followed by an in-Rust set difference because
    /// Cozo's negated rule application does not allow projecting the negated symbol in
    /// the rule head. The set of "called or tested" qualified names is small relative
    /// to the candidate set, so the materialised HashSet is cheap to build.
    pub fn find_dead_code(
        &self,
        min_lines: u32,
    ) -> Result<Vec<serde_json::Value>, Box<dyn std::error::Error>> {
        let tail = self.code_elements_tail();
        let candidate_query = format!(
            r#"?[qualified_name, file_path, line_end, line_start, language, name, span] := *code_elements[qualified_name, "function", name, file_path, line_start, line_end, language, _, _, _, _{tail}], line_end >= 0, line_start >= 0, (line_end - line_start) >= {min_lines}, name != "main", name != "Main", name != "start", name != "serve", name != "Start", span = line_end - line_start:order -span"#,
            min_lines = min_lines
        );
        let candidates = crate::db::schema::run_script(
            &self.db,
            &candidate_query,
            std::collections::BTreeMap::new(),
        )?;
        let referenced_targets = self.referenced_qualified_names()?;
        let mut dead: Vec<serde_json::Value> = candidates
            .rows
            .iter()
            .filter(|row| {
                let qn = row[0].get_str().unwrap_or("");
                !referenced_targets.contains(qn)
            })
            .map(|row| {
                serde_json::json!({
                    "qualified_name": row[0].get_str().unwrap_or(""),
                    "file_path": row[1].get_str().unwrap_or(""),
                    "line_end": row[2].get_int().unwrap_or(0),
                    "line_start": row[3].get_int().unwrap_or(0),
                    "language": row[4].get_str().unwrap_or(""),
                    "name": row[5].get_str().unwrap_or(""),
                    "line_count": row[2].get_int().unwrap_or(0) - row[3].get_int().unwrap_or(0) + 1,
                })
            })
            .collect();
        dead.sort_by(|a, b| {
            let al = a["line_count"].as_i64().unwrap_or(0);
            let bl = b["line_count"].as_i64().unwrap_or(0);
            bl.cmp(&al)
        });
        Ok(dead)
    }

    /// Returns the set of qualified names that appear as the `target_qualified` of
    /// any `calls` or `tested_by` relationship. Used by `find_dead_code` to compute
    /// the live-function complement in memory.
    fn referenced_qualified_names(
        &self,
    ) -> Result<std::collections::HashSet<String>, Box<dyn std::error::Error>> {
        // Bind column 2 (target_qualified) to `tgt` and column 3 (rel_type) to `r`
        // to avoid shadowing/keyword issues. We project `tgt` (target_qualified) of
        // every `calls` or `tested_by` relationship.
        let query =
            r#"?[tgt] := *relationships[_, tgt, r, _, _, _], (r = "calls" or r = "tested_by")"#;
        let result =
            crate::db::schema::run_script(&self.db, query, std::collections::BTreeMap::new())?;
        let set: std::collections::HashSet<String> = result
            .rows
            .iter()
            .filter_map(|row| row.first().and_then(|v| v.get_str().map(String::from)))
            .collect();
        tracing::debug!(
            target: "leankg::dead_code",
            rows = result.rows.len(),
            set_size = set.len(),
            sample = ?set.iter().take(5).collect::<Vec<_>>(),
            "referenced_qualified_names"
        );
        Ok(set)
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceContext {
    pub service: String,
    pub env: String,
    pub version: Option<String>,
    pub team: Option<String>,
    pub on_call: Option<String>,
    pub repo_url: Option<String>,
    pub language: Option<String>,
    pub calls: Vec<String>,
    pub called_by: Vec<String>,
    pub schemas: Vec<String>,
    pub open_incidents: i64,
    pub recent_incidents: Vec<String>,
    pub last_incident: Option<String>,
    pub known_risks: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct EnvConflict {
    pub conflict_type: String,
    pub detail: String,
    pub risk: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceNode {
    pub id: String,
    pub label: String,
    pub is_current_service: bool,
    pub weight: f64,
    pub connection_count: usize,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceEdge {
    pub id: String,
    pub source_id: String,
    pub target_id: String,
    pub call_count: usize,
    pub protocols: Vec<String>,
    pub rel_type: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ServiceGraph {
    pub nodes: Vec<ServiceNode>,
    pub edges: Vec<ServiceEdge>,
    pub current_service: String,
    pub total_services: usize,
    pub total_connections: usize,
}

/// US-GF-01: A single hop in a shortest_path result.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PathHop {
    pub from: String,
    pub to: String,
    pub rel_type: String,
    pub confidence: f64,
    pub confidence_label: String,
    pub source_file: String,
}

/// US-GF-01: Result of a shortest-path query.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ShortestPathResult {
    pub source: String,
    pub target: String,
    pub hops: usize,
    pub path: Vec<PathHop>,
}

/// US-GF-02: Compact view of a neighbor relation type and its edge count.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NeighborHint {
    pub rel_type: String,
    pub count: usize,
}

/// US-GF-02: Aggregated single-node dossier returned by `explain_node`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeExplanation {
    pub qualified_name: String,
    pub name: String,
    pub element_type: String,
    pub file_path: String,
    pub line_start: u32,
    pub line_end: u32,
    pub cluster_id: Option<String>,
    pub cluster_label: Option<String>,
    pub in_degree: usize,
    pub out_degree: usize,
    pub top_neighbors: Vec<NeighborHint>,
}

/// US-GF-05: Top-degree node entry for god-node ranking.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GodNode {
    pub qualified_name: String,
    pub name: String,
    pub element_type: String,
    pub degree: usize,
}

/// US-GF-06: Per-label count + percentage for confidence distribution.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LabelCount {
    pub label: String,
    pub count: usize,
    pub pct: f64,
}

/// US-MP-05: Single finding from consistency check.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConsistencyFinding {
    pub severity: String, // BROKEN | STALE | CURRENT
    pub source: String,
    pub target: String,
    pub rel_type: String,
    pub message: String,
}

/// US-MP-05: Aggregated consistency report.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ConsistencyReport {
    pub total_relationships: usize,
    pub broken: usize,
    pub stale: usize,
    pub findings: Vec<ConsistencyFinding>,
}

/// US-MP-01: Single event in a code element's relationship timeline.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TimelineEvent {
    pub timestamp: i64,
    pub action: String, // "added" | "invalidated"
    pub edge: Relationship,
}

/// US-MP-08: Parsed directory metadata from a directory element.
#[derive(Debug, Clone)]
pub struct DirectoryMetadata {
    pub child_count: usize,
    pub total_lines: usize,
    pub language_distribution: std::collections::HashMap<String, usize>,
}

/// US-MP-04: Agent persona config stored in `.leankg/agents/<name>.json`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentPersona {
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub focus_areas: Vec<String>,
    #[serde(default)]
    pub path_filters: Vec<String>,
    #[serde(default)]
    pub cluster_id: Option<String>,
    #[serde(default)]
    pub element_types: Vec<String>,
}

/// US-MP-04: Diary entry appended to `.leankg/agents/<name>.diary.jsonl`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DiaryEntry {
    pub timestamp: i64,
    pub agent: String,
    pub note: String,
    #[serde(default)]
    pub tags: Vec<String>,
}

/// US-MP-04: Focused subgraph returned for a persona.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentFocus {
    pub agent: String,
    pub elements: Vec<CodeElement>,
    pub relationships: Vec<Relationship>,
}

/// US-V2-12: One entry in the team map (team name, on-call, services owned).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TeamMapEntry {
    pub team: String,
    pub on_call: String,
    pub services: Vec<String>,
}

/// US-GF-08: per-file impact (cluster_id, label) for changed files.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrFileImpact {
    pub file: String,
    pub cluster_id: Option<String>,
    pub cluster_label: Option<String>,
}

/// US-GF-08: aggregated PR impact report (severity + touched clusters).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrImpactReport {
    pub env: String,
    pub changed_file_count: usize,
    pub touched_clusters: Vec<String>,
    pub severity: String,
    pub files: Vec<PrFileImpact>,
}

/// US-MP-06: A cross-cluster tunnel edge (relationship between two
/// elements belonging to different Leiden clusters).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Tunnel {
    pub source: String,
    pub target: String,
    pub rel_type: String,
    pub confidence: f64,
    pub source_cluster: String,
    pub target_cluster: String,
}

/// US-MP-08 / FR-MP-21..23: helpers for folder-as-graph conventions.
pub mod folder_gn {
    use super::CodeElement;
    use super::DirectoryMetadata;

    /// Canonical directory qualified_name with trailing slash.
    /// E.g. `"src/graph"` -> `"src/graph/"`. The trailing slash
    /// distinguishes directories from files whose qualified_name is
    /// their full path.
    pub fn qualified_name(path: &str) -> String {
        let trimmed = path.trim_end_matches('/');
        format!("{}/", trimmed)
    }

    /// Strip the trailing slash for filesystem comparisons.
    pub fn strip(qn: &str) -> &str {
        qn.trim_end_matches('/')
    }

    /// True when the qualified_name follows the directory convention
    /// (trailing slash). Used by `search_code` / `query_file` folder
    /// scoping and impact analysis at directory level.
    pub fn is_directory(qn: &str) -> bool {
        qn.ends_with('/')
    }

    /// Read child_count / language_distribution / total_lines from a
    /// directory element's metadata. Returns None for non-directory
    /// nodes or nodes missing the metadata block.
    pub fn metadata(elem: &CodeElement) -> Option<DirectoryMetadata> {
        if elem.element_type != "directory" {
            return None;
        }
        let child_count = elem
            .metadata
            .get("child_count")
            .and_then(|v| v.as_u64())
            .unwrap_or(0) as usize;
        let total_lines = elem
            .metadata
            .get("total_lines")
            .and_then(|v| v.as_u64())
            .unwrap_or(0) as usize;
        let language_distribution: std::collections::HashMap<String, usize> = elem
            .metadata
            .get("language_distribution")
            .and_then(|v| v.as_object())
            .map(|m| {
                m.iter()
                    .filter_map(|(k, v)| v.as_u64().map(|n| (k.clone(), n as usize)))
                    .collect()
            })
            .unwrap_or_default();
        Some(DirectoryMetadata {
            child_count,
            total_lines,
            language_distribution,
        })
    }
}

/// US-GF-06: Aggregated graph report. Renders to `GRAPH_REPORT.md`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct GraphReport {
    pub project: String,
    pub total_elements: usize,
    pub total_relationships: usize,
    pub file_count: usize,
    pub function_count: usize,
    pub class_count: usize,
    pub god_nodes: Vec<GodNode>,
    pub confidence_distribution: Vec<LabelCount>,
    pub suggested_questions: Vec<String>,
}

impl GraphReport {
    /// Render the report to markdown for `.leankg/GRAPH_REPORT.md`.
    pub fn to_markdown(&self) -> String {
        let mut out = String::new();
        out.push_str(&format!("# Graph Report: {}\n\n", self.project));
        out.push_str("## Overview\n\n");
        out.push_str(&format!("- Total elements: {}\n", self.total_elements));
        out.push_str(&format!(
            "- Total relationships: {}\n",
            self.total_relationships
        ));
        out.push_str(&format!("- Files: {}\n", self.file_count));
        out.push_str(&format!("- Functions: {}\n", self.function_count));
        out.push_str(&format!("- Classes/Structs: {}\n\n", self.class_count));

        out.push_str("## Confidence Distribution\n\n");
        out.push_str("| Label | Count | % |\n|---|---|---|\n");
        for c in &self.confidence_distribution {
            out.push_str(&format!("| {} | {} | {:.1}% |\n", c.label, c.count, c.pct));
        }
        out.push('\n');

        out.push_str("## Top God Nodes\n\n");
        if self.god_nodes.is_empty() {
            out.push_str("_No relationships indexed yet._\n\n");
        } else {
            out.push_str("| Qualified Name | Type | Degree |\n|---|---|---|\n");
            for n in &self.god_nodes {
                out.push_str(&format!(
                    "| {} | {} | {} |\n",
                    n.qualified_name, n.element_type, n.degree
                ));
            }
            out.push('\n');
        }

        out.push_str("## Suggested Questions\n\n");
        for (i, q) in self.suggested_questions.iter().enumerate() {
            out.push_str(&format!("{}. {}\n", i + 1, q));
        }
        out
    }
}

/// Element-type ranking for resolve_to_qualified: more specific types win
/// over files / directories when names collide.
fn rank_element_type(t: &str) -> u8 {
    match t {
        "function" | "method" | "constructor" => 0,
        "class" | "struct" | "interface" | "enum" | "trait" => 1,
        "route" | "module" | "property" | "field" => 2,
        "file" => 3,
        "directory" | "folder" => 4,
        _ => 5,
    }
}

impl GraphEngine {
    /// US-GF-01 / FR-GF-01: BFS shortest path between two symbols.
    ///
    /// Returns an ordered list of hops from `source` to `target`. Each hop
    /// carries the relation, confidence and provenance label so agents can
    /// see how A connects to B and whether each edge is explicit (EXTRACTED)
    /// or resolver-derived (INFERRED / AMBIGUOUS).
    ///
    /// Inputs are resolved by qualified_name, exact element name, or
    /// fuzzy suffix match. Returns `Ok(None)` when no path exists within
    /// `max_hops`.
    pub fn shortest_path(
        &self,
        source: &str,
        target: &str,
        max_hops: usize,
    ) -> Result<Option<ShortestPathResult>, Box<dyn std::error::Error>> {
        let max_hops = max_hops.clamp(1, 10);

        let source_qn = self
            .resolve_to_qualified(source)
            .ok_or_else(|| format!("source '{}' not found", source))?;
        let target_qn = self
            .resolve_to_qualified(target)
            .ok_or_else(|| format!("target '{}' not found", target))?;

        if source_qn == target_qn {
            return Ok(Some(ShortestPathResult {
                source: source_qn,
                target: target_qn,
                hops: 0,
                path: Vec::new(),
            }));
        }

        // BFS over (qualified_name) using all relationships as edges.
        let all_rels = self.all_relationships()?;
        let mut adjacency: std::collections::HashMap<String, Vec<(String, Relationship)>> =
            std::collections::HashMap::new();
        for rel in &all_rels {
            adjacency
                .entry(rel.source_qualified.clone())
                .or_default()
                .push((rel.target_qualified.clone(), rel.clone()));
            // Treat graph as undirected for path-finding so callers can
            // express either "X calls Y" or "Y is called by X" intent.
            adjacency
                .entry(rel.target_qualified.clone())
                .or_default()
                .push((rel.source_qualified.clone(), rel.clone()));
        }

        let mut visited: std::collections::HashSet<String> = std::collections::HashSet::new();
        let mut queue: std::collections::VecDeque<(String, Vec<PathHop>)> =
            std::collections::VecDeque::new();
        queue.push_back((source_qn.clone(), Vec::new()));
        visited.insert(source_qn.clone());

        while let Some((current, path)) = queue.pop_front() {
            if path.len() >= max_hops {
                continue;
            }
            if let Some(neighbors) = adjacency.get(&current) {
                for (next, rel) in neighbors {
                    if !visited.insert(next.clone()) {
                        continue;
                    }
                    let mut new_path = path.clone();
                    new_path.push(PathHop {
                        from: current.clone(),
                        to: next.clone(),
                        rel_type: rel.rel_type.clone(),
                        confidence: rel.confidence,
                        confidence_label: rel.confidence_label().to_string(),
                        source_file: rel
                            .metadata
                            .get("source_file")
                            .and_then(|v| v.as_str())
                            .unwrap_or("")
                            .to_string(),
                    });
                    if next == &target_qn {
                        return Ok(Some(ShortestPathResult {
                            source: source_qn,
                            target: target_qn,
                            hops: new_path.len(),
                            path: new_path,
                        }));
                    }
                    queue.push_back((next.clone(), new_path));
                }
            }
        }

        Ok(None)
    }

    /// Resolve a free-form input (qualified_name, exact name, or fuzzy
    /// suffix match) to a single qualified_name. Returns the first match
    /// by priority: exact qualified_name > exact element name > suffix.
    fn resolve_to_qualified(&self, input: &str) -> Option<String> {
        let elements = self.all_elements().ok()?;
        // 1. Exact qualified_name
        if elements.iter().any(|e| e.qualified_name == input) {
            return Some(input.to_string());
        }
        // 2. Exact element name
        let mut by_name: Vec<&CodeElement> = elements.iter().filter(|e| e.name == input).collect();
        if !by_name.is_empty() {
            // Prefer functions / classes over files / directories when names collide.
            by_name.sort_by(|a, b| {
                rank_element_type(&a.element_type).cmp(&rank_element_type(&b.element_type))
            });
            return Some(by_name[0].qualified_name.clone());
        }
        // 3. Suffix match
        let suffix_matches: Vec<&CodeElement> = elements
            .iter()
            .filter(|e| e.qualified_name.ends_with(input) || e.qualified_name.contains(input))
            .collect();
        if !suffix_matches.is_empty() {
            return Some(suffix_matches[0].qualified_name.clone());
        }
        None
    }

    pub fn wake_up_summary(&self) -> Result<String, String> {
        let elements = self.all_elements().map_err(|e| e.to_string())?;

        let total = elements.len();
        let file_count = elements.iter().filter(|e| e.element_type == "File").count();
        let func_count = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();
        let class_count = elements
            .iter()
            .filter(|e| e.element_type == "class" || e.element_type == "struct")
            .count();

        let mut languages: std::collections::HashMap<String, usize> =
            std::collections::HashMap::new();
        for e in &elements {
            if !e.language.is_empty() {
                *languages.entry(e.language.clone()).or_insert(0) += 1;
            }
        }
        let mut lang_list: Vec<(String, usize)> = languages.into_iter().collect();
        lang_list.sort_by_key(|(_, c)| std::cmp::Reverse(*c));
        let primary_langs: Vec<String> = lang_list.iter().take(5).map(|(l, _)| l.clone()).collect();

        let mut top_dirs: std::collections::HashMap<String, usize> =
            std::collections::HashMap::new();
        for e in &elements {
            if e.element_type == "directory" && !e.file_path.is_empty() {
                let depth = e.file_path.chars().filter(|&c| c == '/').count();
                if depth == 1 {
                    let dir_name = e.file_path.strip_prefix("./").unwrap_or(&e.file_path);
                    if let Some(child_count) =
                        e.metadata.get("child_count").and_then(|v| v.as_u64())
                    {
                        top_dirs.insert(dir_name.to_string(), child_count as usize);
                    }
                }
            }
        }
        let mut dir_list: Vec<(String, usize)> = top_dirs.into_iter().collect();
        dir_list.sort_by_key(|(_, c)| std::cmp::Reverse(*c));
        let dirs: Vec<String> = dir_list.iter().take(8).map(|(d, _)| d.clone()).collect();

        let mut lines = Vec::new();
        lines.push(format!("Project: {}", primary_langs.join(", ")));
        lines.push(format!(
            "Files: {} | Functions: {} | Classes: {} | Total elements: {}",
            file_count, func_count, class_count, total
        ));
        if !dirs.is_empty() {
            lines.push(format!("Top directories: {}", dirs.join(", ")));
        }

        let rel_count = self.all_relationships().map(|r| r.len()).unwrap_or(0);
        let import_count = elements
            .iter()
            .filter(|e| e.element_type == "import")
            .count();
        lines.push(format!(
            "Relationships: {} | Imports: {}",
            rel_count, import_count
        ));

        Ok(lines.join("\n"))
    }

    /// US-MP-02 / FR-MP-05: Generate L0 context (~50 tokens).
    /// Project identity: name, languages, top-level directories,
    /// architecture pattern. Stored at `.leankg/identity.md`.
    pub fn identity_context(&self, project_name: &str) -> Result<String, String> {
        let elements = self.all_elements().map_err(|e| e.to_string())?;
        let langs: std::collections::BTreeSet<String> = elements
            .iter()
            .map(|e| e.language.clone())
            .filter(|l| !l.is_empty())
            .collect();

        let top_dirs: std::collections::BTreeSet<String> = elements
            .iter()
            .filter_map(|e| {
                let p = e.file_path.trim_start_matches("./").trim_start_matches('/');
                p.split('/').next().map(String::from)
            })
            .filter(|d| !d.is_empty())
            .collect();

        let mut out = String::new();
        out.push_str(&format!("# {}\n\n", project_name));
        if !langs.is_empty() {
            out.push_str(&format!(
                "Languages: {}\n",
                langs.into_iter().collect::<Vec<_>>().join(", ")
            ));
        }
        if !top_dirs.is_empty() {
            let dirs = top_dirs.into_iter().take(8).collect::<Vec<_>>();
            out.push_str(&format!("Top-level: {}\n", dirs.join(", ")));
        }
        Ok(out)
    }

    /// US-MP-02 / FR-MP-06: Generate L1 context (~120 tokens).
    /// Critical facts: hot modules (top god nodes), element counts,
    /// relationship counts. Stored at `.leankg/critical_facts.md`.
    pub fn critical_facts_context(&self) -> Result<String, String> {
        let elements = self.all_elements().map_err(|e| e.to_string())?;
        let rels = self.all_relationships().map_err(|e| e.to_string())?;
        let gods = self.get_god_nodes(5, Some(90)).map_err(|e| e.to_string())?;

        let total = elements.len();
        let rel_count = rels.len();
        let func_count = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();

        let mut out = String::new();
        out.push_str("## Critical facts\n\n");
        out.push_str(&format!(
            "Elements: {} (functions: {}). Relationships: {}.\n",
            total, func_count, rel_count
        ));
        if !gods.is_empty() {
            let names: Vec<String> = gods
                .iter()
                .map(|g| format!("`{}` (degree {})", g.qualified_name, g.degree))
                .collect();
            out.push_str(&format!("Hot modules: {}.\n", names.join(", ")));
        }
        Ok(out)
    }

    /// US-GF-02 / FR-GF-03: Aggregate a single-node dossier.
    ///
    /// Returns the element's definition site, cluster membership, in/out
    /// degree, top neighbors by relation type, and recent incident / annotation
    /// context if any. Designed for a single MCP response that lets an agent
    /// "explain" a symbol without juggling multiple round-trips.
    pub fn explain_node(
        &self,
        input: &str,
    ) -> Result<Option<NodeExplanation>, Box<dyn std::error::Error>> {
        let qn = match self.resolve_to_qualified(input) {
            Some(q) => q,
            None => return Ok(None),
        };

        let elements = self.all_elements()?;
        let element = match elements.iter().find(|e| e.qualified_name == qn) {
            Some(e) => e.clone(),
            None => return Ok(None),
        };

        let all_rels = self.all_relationships()?;
        let in_degree = all_rels.iter().filter(|r| r.target_qualified == qn).count();
        let out_degree = all_rels.iter().filter(|r| r.source_qualified == qn).count();

        // Top neighbors grouped by relation type
        let mut by_type: std::collections::HashMap<String, Vec<String>> =
            std::collections::HashMap::new();
        for r in &all_rels {
            if r.source_qualified == qn {
                by_type
                    .entry(r.rel_type.clone())
                    .or_default()
                    .push(r.target_qualified.clone());
            } else if r.target_qualified == qn {
                by_type
                    .entry(format!("<-{}", r.rel_type))
                    .or_default()
                    .push(r.source_qualified.clone());
            }
        }
        let mut neighbors: Vec<(String, usize)> =
            by_type.into_iter().map(|(t, ns)| (t, ns.len())).collect();
        neighbors.sort_by_key(|n| std::cmp::Reverse(n.1));

        // Sample top 5 neighbor qualified_names by relation type for hint list
        let neighbor_hints: Vec<NeighborHint> = neighbors
            .iter()
            .take(8)
            .map(|(rel_type, count)| NeighborHint {
                rel_type: rel_type.clone(),
                count: *count,
            })
            .collect();

        Ok(Some(NodeExplanation {
            qualified_name: qn,
            name: element.name,
            element_type: element.element_type,
            file_path: element.file_path,
            line_start: element.line_start,
            line_end: element.line_end,
            cluster_id: element.cluster_id,
            cluster_label: element.cluster_label,
            in_degree,
            out_degree,
            top_neighbors: neighbor_hints,
        }))
    }

    /// US-GF-05 / FR-GF-10..11: Top-degree god nodes.
    ///
    /// Returns the most-connected elements (sum of in + out degree) sorted
    /// descending. Optionally excludes utility super-hubs whose degree exceeds
    /// `exclude_hubs_percentile` (0-100).
    pub fn get_god_nodes(
        &self,
        limit: usize,
        exclude_hubs_percentile: Option<u8>,
    ) -> Result<Vec<GodNode>, Box<dyn std::error::Error>> {
        let limit = limit.clamp(1, 200);
        let all_rels = self.all_relationships()?;
        let elements = self.all_elements()?;

        let mut degree: std::collections::HashMap<String, usize> = std::collections::HashMap::new();
        for r in &all_rels {
            *degree.entry(r.source_qualified.clone()).or_default() += 1;
            *degree.entry(r.target_qualified.clone()).or_default() += 1;
        }

        let mut nodes: Vec<(String, usize)> = degree.into_iter().collect();
        nodes.sort_by_key(|n| std::cmp::Reverse(n.1));

        if let Some(pctl) = exclude_hubs_percentile {
            if !nodes.is_empty() {
                let cutoff_idx =
                    ((nodes.len() as f64 * (100.0 - pctl as f64) / 100.0) as usize).max(1);
                nodes.truncate(cutoff_idx);
            }
        }

        let qn_set: std::collections::HashSet<String> =
            nodes.iter().map(|(qn, _)| qn.clone()).collect();
        let by_qn: std::collections::HashMap<String, CodeElement> = elements
            .into_iter()
            .filter(|e| qn_set.contains(&e.qualified_name))
            .map(|e| (e.qualified_name.clone(), e))
            .collect();

        Ok(nodes
            .into_iter()
            .take(limit)
            .map(|(qn, deg)| {
                let element_type = by_qn
                    .get(&qn)
                    .map(|e| e.element_type.clone())
                    .unwrap_or_default();
                let name = by_qn
                    .get(&qn)
                    .map(|e| e.name.clone())
                    .unwrap_or_else(|| qn.clone());
                GodNode {
                    qualified_name: qn,
                    name,
                    element_type,
                    degree: deg,
                }
            })
            .collect())
    }

    /// US-GF-06 / FR-GF-13: Build a `GRAPH_REPORT.md` summary of the
    /// indexed codebase: top god-nodes, confidence label distribution,
    /// and 4-5 suggested agent questions. Returns a `GraphReport`
    /// struct that can be serialized to JSON or markdown.
    pub fn generate_graph_report(
        &self,
        project_name: &str,
    ) -> Result<GraphReport, Box<dyn std::error::Error>> {
        let elements = self.all_elements()?;
        let rels = self.all_relationships()?;
        let god_nodes = self.get_god_nodes(10, Some(90))?;

        // Confidence label distribution
        let mut by_label: std::collections::HashMap<&'static str, usize> =
            std::collections::HashMap::new();
        for r in &rels {
            let label = r.confidence_label();
            *by_label.entry(label).or_default() += 1;
        }

        let total = rels.len();
        let confidence_dist: Vec<LabelCount> = ["EXTRACTED", "INFERRED", "AMBIGUOUS"]
            .into_iter()
            .map(|l| LabelCount {
                label: l.to_string(),
                count: *by_label.get(l).unwrap_or(&0),
                pct: if total == 0 {
                    0.0
                } else {
                    (*by_label.get(l).unwrap_or(&0) as f64 * 100.0) / total as f64
                },
            })
            .collect();

        let total_elements = elements.len();
        let file_count = elements.iter().filter(|e| e.element_type == "file").count();
        let func_count = elements
            .iter()
            .filter(|e| e.element_type == "function")
            .count();
        let class_count = elements
            .iter()
            .filter(|e| e.element_type == "class" || e.element_type == "struct")
            .count();

        let suggested_questions = vec![
            format!(
                "Which functions in {} are most central to the call graph? (use explain_node on top god nodes)",
                project_name
            ),
            "Find the shortest path from a hot entry point to a low-level helper (use shortest_path)".to_string(),
            format!(
                "How many of the {} relationships are AMBIGUOUS vs EXTRACTED? Where are the AMBIGUOUS edges clustered?",
                total
            ),
            "Which directories hold the most cross-cluster traffic (use query_graph or shortest_path across cluster boundaries)?".to_string(),
            format!(
                "What is the impact radius of the highest-degree {} (use get_impact_radius)?",
                god_nodes.first().map(|g| g.name.clone()).unwrap_or_default()
            ),
        ];

        Ok(GraphReport {
            project: project_name.to_string(),
            total_elements,
            total_relationships: total,
            file_count,
            function_count: func_count,
            class_count,
            god_nodes,
            confidence_distribution: confidence_dist,
            suggested_questions,
        })
    }

    /// US-MP-01 / FR-MP-01..02: read the optional valid_from timestamp
    /// from relationship metadata (epoch seconds). Returns None for
    /// edges that pre-date the temporal feature.
    pub fn valid_from(rel: &Relationship) -> Option<i64> {
        rel.metadata.get("valid_from").and_then(|v| v.as_i64())
    }

    /// US-MP-01: read the valid_to timestamp; non-null means the edge
    /// has been invalidated but is retained for historical queries.
    pub fn valid_to(rel: &Relationship) -> Option<i64> {
        rel.metadata.get("valid_to").and_then(|v| v.as_i64())
    }

    /// US-MP-01 / FR-MP-03: temporal_query — return the graph state
    /// as of a given epoch (seconds). An edge is "live" at that
    /// moment if `valid_from <= now <= valid_to` (or valid_to is
    /// unset and valid_from <= now).
    pub fn temporal_query(
        &self,
        at_epoch: i64,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let rels = self.all_relationships()?;
        Ok(rels
            .into_iter()
            .filter(|r| {
                let valid_from = Self::valid_from(r).unwrap_or(0);
                let valid_to = Self::valid_to(r).unwrap_or(i64::MAX);
                valid_from <= at_epoch && at_epoch <= valid_to
            })
            .collect())
    }

    /// US-MP-01 / FR-MP-04: timeline — chronological evolution of a
    /// single code element's relationships. Returns a sorted list of
    /// events (added / invalidated) with timestamps.
    pub fn timeline(
        &self,
        qualified_name: &str,
    ) -> Result<Vec<TimelineEvent>, Box<dyn std::error::Error>> {
        let rels = self.all_relationships()?;
        let mut events: Vec<TimelineEvent> = Vec::new();
        for r in &rels {
            if r.source_qualified != qualified_name && r.target_qualified != qualified_name {
                continue;
            }
            if let Some(vf) = Self::valid_from(r) {
                events.push(TimelineEvent {
                    timestamp: vf,
                    action: "added".into(),
                    edge: r.clone(),
                });
            }
            if let Some(vt) = Self::valid_to(r) {
                events.push(TimelineEvent {
                    timestamp: vt,
                    action: "invalidated".into(),
                    edge: r.clone(),
                });
            }
        }
        events.sort_by_key(|e| e.timestamp);
        Ok(events)
    }

    /// US-MP-05 / FR-MP-14..15: Check the graph for stale or broken
    /// links. Returns findings with severity (BROKEN / STALE / CURRENT)
    /// so agents and `leankg check-consistency` can prioritize fixes.
    ///   BROKEN  — element missing or referenced file_path absent
    ///   STALE   — invalidation timestamp set without replacement
    ///   CURRENT — edge present and target still exists
    pub fn check_consistency(&self) -> Result<ConsistencyReport, Box<dyn std::error::Error>> {
        let elements = self.all_elements()?;
        let rels = self.all_relationships()?;
        let qn_set: std::collections::HashSet<String> =
            elements.iter().map(|e| e.qualified_name.clone()).collect();

        let mut findings: Vec<ConsistencyFinding> = Vec::new();
        for r in &rels {
            // Invalidated edges that are still referenced from elsewhere.
            if Self::valid_to(r).is_some() {
                findings.push(ConsistencyFinding {
                    severity: "STALE".into(),
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                    message: "edge has valid_to set but row still present".into(),
                });
                continue;
            }
            // Target missing
            if !qn_set.contains(&r.target_qualified) {
                findings.push(ConsistencyFinding {
                    severity: "BROKEN".into(),
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                    message: "target element missing from code_elements".into(),
                });
                continue;
            }
            // Source missing
            if !qn_set.contains(&r.source_qualified) {
                findings.push(ConsistencyFinding {
                    severity: "BROKEN".into(),
                    source: r.source_qualified.clone(),
                    target: r.target_qualified.clone(),
                    rel_type: r.rel_type.clone(),
                    message: "source element missing from code_elements".into(),
                });
                continue;
            }
        }
        // Stale annotations are out of scope here — handled by separate
        // doc consistency check — but we record one CURRENT marker so
        // the report isn't empty when the graph is healthy.
        if findings.is_empty() {
            findings.push(ConsistencyFinding {
                severity: "CURRENT".into(),
                source: "-".into(),
                target: "-".into(),
                rel_type: "-".into(),
                message: "graph is consistent".into(),
            });
        }
        let broken = findings.iter().filter(|f| f.severity == "BROKEN").count();
        let stale = findings.iter().filter(|f| f.severity == "STALE").count();
        Ok(ConsistencyReport {
            total_relationships: rels.len(),
            broken,
            stale,
            findings,
        })
    }

    /// US-MP-08 / FR-MP-25: list all directory nodes that are direct
    /// children of the given folder qualified_name (with trailing
    /// slash). Returns them sorted by name. Used by folder-scoped
    /// search and impact analysis at directory level.
    pub fn subdirectories(
        &self,
        folder_qn: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let prefix = folder_gn::strip(folder_qn);
        let depth = prefix.matches('/').count() + 1; // children are one level deeper
        let elements = self.all_elements()?;
        let mut children: Vec<CodeElement> = elements
            .into_iter()
            .filter(|e| e.element_type == "directory")
            .filter(|e| {
                let p = folder_gn::strip(&e.qualified_name);
                p.starts_with(prefix)
                    && p.len() > prefix.len()
                    && p[prefix.len()..]
                        .trim_start_matches('/')
                        .matches('/')
                        .count()
                        == 0
            })
            .collect();
        // Sanity: limit depth
        children.retain(|e| folder_gn::strip(&e.qualified_name).matches('/').count() == depth);
        children.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(children)
    }

    /// US-MP-06 / FR-MP-16..17: detect cross-domain tunnels between
    /// clusters. A tunnel is a `Relationship` whose source and target
    /// belong to different Leiden clusters — i.e. a cross-cluster
    /// dependency that an agent should know about.
    pub fn find_tunnels(&self) -> Result<Vec<Tunnel>, Box<dyn std::error::Error>> {
        let rels = self.all_relationships()?;
        let elements = self.all_elements()?;
        let cluster_of: std::collections::HashMap<String, Option<String>> = elements
            .iter()
            .map(|e| (e.qualified_name.clone(), e.cluster_id.clone()))
            .collect();

        let mut tunnels: Vec<Tunnel> = Vec::new();
        for r in &rels {
            let src_cluster = cluster_of.get(&r.source_qualified).and_then(|c| c.clone());
            let tgt_cluster = cluster_of.get(&r.target_qualified).and_then(|c| c.clone());
            // Only count when both ends are clustered and clusters differ.
            if let (Some(sc), Some(tc)) = (src_cluster.as_ref(), tgt_cluster.as_ref()) {
                if sc != tc {
                    tunnels.push(Tunnel {
                        source: r.source_qualified.clone(),
                        target: r.target_qualified.clone(),
                        rel_type: r.rel_type.clone(),
                        confidence: r.confidence,
                        source_cluster: sc.clone(),
                        target_cluster: tc.clone(),
                    });
                }
            }
        }
        tunnels.sort_by(|a, b| {
            b.confidence
                .partial_cmp(&a.confidence)
                .unwrap_or(std::cmp::Ordering::Equal)
        });
        Ok(tunnels)
    }

    /// US-MP-04 / FR-MP-18: List agent personas defined in
    /// `.leankg/agents/*.json`.
    pub fn list_agents(
        project_path: &std::path::Path,
    ) -> Result<Vec<AgentPersona>, Box<dyn std::error::Error>> {
        let dir = project_path.join(".leankg").join("agents");
        if !dir.exists() {
            return Ok(Vec::new());
        }
        let mut agents = Vec::new();
        for entry in std::fs::read_dir(&dir)? {
            let entry = entry?;
            let path = entry.path();
            if path.extension().and_then(|e| e.to_str()) != Some("json") {
                continue;
            }
            let raw = std::fs::read_to_string(&path)?;
            if let Ok(p) = serde_json::from_str::<AgentPersona>(&raw) {
                agents.push(p);
            }
        }
        agents.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(agents)
    }

    /// US-MP-04 / FR-MP-19: focus the graph for a specialist agent
    /// persona, returning only elements matching path/cluster/type
    /// filters and relationships entirely within that subset.
    pub fn agent_focus(
        &self,
        persona: &AgentPersona,
    ) -> Result<AgentFocus, Box<dyn std::error::Error>> {
        let elements = self.all_elements()?;
        let rels = self.all_relationships()?;
        let qn_keep: std::collections::HashSet<String> = elements
            .iter()
            .filter(|e| {
                if !persona.element_types.is_empty()
                    && !persona.element_types.contains(&e.element_type)
                {
                    return false;
                }
                if let Some(ref cid) = persona.cluster_id {
                    if e.cluster_id.as_deref() != Some(cid.as_str()) {
                        return false;
                    }
                }
                if !persona.path_filters.is_empty()
                    && !persona
                        .path_filters
                        .iter()
                        .any(|p| e.file_path.starts_with(p))
                {
                    return false;
                }
                true
            })
            .map(|e| e.qualified_name.clone())
            .collect();
        let focused_rels: Vec<Relationship> = rels
            .into_iter()
            .filter(|r| {
                qn_keep.contains(&r.source_qualified) && qn_keep.contains(&r.target_qualified)
            })
            .collect();
        Ok(AgentFocus {
            agent: persona.name.clone(),
            elements: elements
                .into_iter()
                .filter(|e| qn_keep.contains(&e.qualified_name))
                .collect(),
            relationships: focused_rels,
        })
    }

    /// US-GF-09 / FR-GF-19: Record a query outcome (useful /
    /// dead_end / corrected) for a graph answer and append to the
    /// reflections journal at `.leankg/reflections/LESSONS.md`.
    pub fn report_query_outcome(
        project_path: &std::path::Path,
        question: &str,
        nodes: &[String],
        outcome: &str,
        note: Option<&str>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let dir = project_path.join(".leankg").join("reflections");
        std::fs::create_dir_all(&dir)?;
        let lessons = dir.join("LESSONS.md");
        let entry = format!(
            "\n## {} — {}\n\n- Question: {}\n- Nodes: {}\n- Outcome: {}\n{}\n",
            chrono_unix(),
            outcome,
            question,
            if nodes.is_empty() {
                "(none)".to_string()
            } else {
                nodes.join(", ")
            },
            outcome,
            note.map(|n| format!("- Note: {}", n)).unwrap_or_default(),
        );
        use std::io::Write;
        let mut f = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&lessons)?;
        f.write_all(entry.as_bytes())?;
        Ok(())
    }

    /// US-V2-12 / FR-V2-12: aggregated team / ownership map across
    /// all services in a given environment. Returns one entry per
    /// team with the on-call rotation and a list of services the
    /// team owns.
    pub fn get_team_map(&self, env: &str) -> Result<Vec<TeamMapEntry>, Box<dyn std::error::Error>> {
        let services = self.get_all_service_metadata(env)?;
        let mut by_team: std::collections::HashMap<String, TeamMapEntry> =
            std::collections::HashMap::new();
        for svc in services {
            let team = svc.team.unwrap_or_else(|| "(unassigned)".into());
            let on_call = svc.on_call.unwrap_or_else(|| "(none)".into());
            let entry = by_team.entry(team.clone()).or_insert_with(|| TeamMapEntry {
                team: team.clone(),
                on_call: on_call.clone(),
                services: Vec::new(),
            });
            if entry.on_call == "(none)" && on_call != "(none)" {
                entry.on_call = on_call;
            }
            entry.services.push(svc.service_name.clone());
        }
        let mut result: Vec<TeamMapEntry> = by_team.into_values().collect();
        result.sort_by(|a, b| a.team.cmp(&b.team));
        Ok(result)
    }

    /// US-V2-12 helper: fetch all service metadata rows for an env.
    fn get_all_service_metadata(
        &self,
        env: &str,
    ) -> Result<Vec<crate::db::models::ServiceMetadata>, Box<dyn std::error::Error>> {
        let query = "?[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at] := *service_metadata[service_name, env, team, on_call, repo_url, language, health_endpoint, slo_p99_ms, incident_count, last_incident, tags, version, deploy_envs, created_at, updated_at], env = $env";
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "env".to_string(),
            serde_json::Value::String(env.to_string()),
        );
        let result = crate::db::schema::run_script(&self.db, query, params)?;
        let mut out = Vec::new();
        for row in &result.rows {
            out.push(crate::db::models::ServiceMetadata {
                service_name: row
                    .first()
                    .and_then(|v| v.get_str())
                    .unwrap_or("")
                    .to_string(),
                env: env.to_string(),
                team: row.get(2).and_then(|v| v.get_str()).map(String::from),
                on_call: row.get(3).and_then(|v| v.get_str()).map(String::from),
                repo_url: row.get(4).and_then(|v| v.get_str()).map(String::from),
                language: row.get(5).and_then(|v| v.get_str()).map(String::from),
                health_endpoint: row.get(6).and_then(|v| v.get_str()).map(String::from),
                slo_p99_ms: row.get(7).and_then(|v| v.get_int()).map(|n| n as i32),
                incident_count: row.get(8).and_then(|v| v.get_int()).unwrap_or(0) as i32,
                last_incident: row.get(9).and_then(|v| v.get_int()),
                tags: row
                    .get(10)
                    .and_then(|v| v.get_str())
                    .unwrap_or("")
                    .to_string(),
                version: row.get(11).and_then(|v| v.get_str()).map(String::from),
                deploy_envs: row
                    .get(12)
                    .and_then(|v| v.get_str())
                    .unwrap_or("")
                    .to_string(),
                created_at: row.get(13).and_then(|v| v.get_int()).unwrap_or(0),
                updated_at: row.get(14).and_then(|v| v.get_int()).unwrap_or(0),
            });
        }
        Ok(out)
    }

    /// US-GF-08 / FR-GF-17..18: PR impact dashboard. Given a list of
    /// changed files (typically from `git diff --name-only`), return
    /// each file's cluster membership and a severity rating based on
    /// the number of distinct clusters touched. Useful for
    /// merge-order risk assessment and conflict triage.
    pub fn pr_impact(
        &self,
        changed_files: &[String],
        env: &str,
    ) -> Result<PrImpactReport, Box<dyn std::error::Error>> {
        let elements = self.all_elements()?;
        let mut touched_clusters: std::collections::HashSet<String> =
            std::collections::HashSet::new();
        let mut per_file: Vec<PrFileImpact> = Vec::new();
        for f in changed_files {
            let cluster = elements
                .iter()
                .find(|e| e.file_path == *f && e.cluster_id.is_some())
                .and_then(|e| e.cluster_id.clone());
            let label = elements
                .iter()
                .find(|e| e.file_path == *f)
                .and_then(|e| e.cluster_label.clone());
            if let Some(ref c) = cluster {
                touched_clusters.insert(c.clone());
            }
            per_file.push(PrFileImpact {
                file: f.clone(),
                cluster_id: cluster,
                cluster_label: label,
            });
        }
        let severity = if touched_clusters.len() >= 5 {
            "HIGH"
        } else if touched_clusters.len() >= 2 {
            "MEDIUM"
        } else {
            "LOW"
        };
        Ok(PrImpactReport {
            env: env.to_string(),
            changed_file_count: changed_files.len(),
            touched_clusters: touched_clusters.into_iter().collect(),
            severity: severity.to_string(),
            files: per_file,
        })
    }
}

fn chrono_unix() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::models::CodeElement;
    use crate::db::schema::init_db;
    use tempfile::TempDir;

    fn make_test_engine() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = init_db(&db_path).unwrap();
        let engine = GraphEngine::new(db);
        (engine, tmp)
    }

    #[test]
    fn test_vacuum_is_callable_on_initialised_db() {
        // Regression guard: `vacuum()` is invoked from the watcher when the
        // database file exceeds the configured size cap. We just assert that
        // the call returns a `Result` (it may be `Err` on a completely empty
        // database depending on the CozoDB backend, which is acceptable
        // because the caller logs and continues) and that it does not panic.
        let (engine, _tmp) = make_test_engine();
        let _ = engine.vacuum();
    }

    fn insert_test_element(engine: &GraphEngine, name: &str, element_type: &str) {
        let elem = CodeElement {
            qualified_name: format!("src/test.rs::{}", name),
            element_type: element_type.to_string(),
            name: name.to_string(),
            file_path: "src/test.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    #[test]
    fn test_search_by_name_finds_exact_match() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "my_function", "function");

        let results = engine.search_by_name("my_function").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_name should find elements by exact name"
        );
        assert_eq!(results[0].name, "my_function");
    }

    #[test]
    fn test_search_by_name_case_insensitive() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "MyFunction", "function");

        let results = engine.search_by_name("myfunction").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_name should be case-insensitive"
        );
    }

    #[test]
    fn test_search_by_name_partial_match() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "calculate_total", "function");

        let results = engine.search_by_name("calculate").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_name should find partial matches"
        );
    }

    #[test]
    fn test_search_by_name_no_match_returns_empty() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "existing_function", "function");

        let results = engine.search_by_name("nonexistent_xyz_abc").unwrap();
        assert!(
            results.is_empty(),
            "search_by_name should return empty for no match"
        );
    }

    #[test]
    fn test_run_raw_query_with_empty_params() {
        let (engine, _tmp) = make_test_engine();
        let tail = engine.code_elements_tail();
        insert_test_element(&engine, "main", "function");

        let query = format!(
            r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}]"#
        );
        let result = engine.run_raw_query(&query, Default::default());
        assert!(
            result.is_ok(),
            "run_raw_query should succeed with valid query"
        );
        let rows = result.unwrap().rows;
        assert!(
            !rows.is_empty(),
            "run_raw_query should return inserted elements"
        );
    }

    #[test]
    fn test_run_raw_query_with_params() {
        let (engine, _tmp) = make_test_engine();
        let tail = engine.code_elements_tail();
        insert_test_element(&engine, "main", "function");

        let query = format!(
            r#"?[qualified_name, name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata{tail}], name = $nm"#
        );
        let mut params = std::collections::BTreeMap::new();
        params.insert(
            "nm".to_string(),
            serde_json::Value::String("main".to_string()),
        );
        let result = engine.run_raw_query(&query, params);
        assert!(
            result.is_ok(),
            "run_raw_query should succeed with parameterized query"
        );
        let rows = result.unwrap().rows;
        assert!(
            !rows.is_empty(),
            "run_raw_query with params should find element named 'main'"
        );
    }

    #[test]
    fn test_search_by_content_matches_name_field() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "get_vendor", "function");

        let results = engine.search_by_content("get_vendor").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_content should find an element by exact name"
        );
        assert_eq!(results[0].name, "get_vendor");
    }

    #[test]
    fn test_search_by_content_matches_qualified_name_only() {
        // Element where the user-supplied substring only appears in qualified_name
        // (not in the bare `name` field). search_by_content should still find it.
        let (engine, _tmp) = make_test_engine();
        let elem = CodeElement {
            qualified_name: "src/special/get_vendor_helper.rs::Helper".to_string(),
            element_type: "function".to_string(),
            name: "Helper".to_string(),
            file_path: "src/special/get_vendor_helper.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();

        let results = engine.search_by_content("get_vendor").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_content should find elements whose qualified_name contains the substring"
        );
    }

    #[test]
    fn test_search_by_content_matches_file_path() {
        let (engine, _tmp) = make_test_engine();
        let elem = CodeElement {
            qualified_name: "src/foo.rs::Handler".to_string(),
            element_type: "function".to_string(),
            name: "Handler".to_string(),
            file_path: "src/get_vendor_module/foo.rs".to_string(),
            line_start: 1,
            line_end: 10,
            language: "rust".to_string(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();

        let results = engine.search_by_content("get_vendor").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_content should find elements whose file_path contains the substring"
        );
    }

    #[test]
    fn test_search_by_content_case_insensitive() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "GetVendorById", "function");

        let results = engine.search_by_content("getvendor").unwrap();
        assert!(
            !results.is_empty(),
            "search_by_content should be case-insensitive"
        );
    }

    #[test]
    fn test_search_by_content_no_match_returns_empty() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "calculate_total", "function");

        let results = engine.search_by_content("zzz_nonexistent_xyz").unwrap();
        assert!(
            results.is_empty(),
            "search_by_content should return empty when nothing matches"
        );
    }

    // Phase 1 structural parity tests

    fn insert_test_element_full(
        engine: &GraphEngine,
        name: &str,
        element_type: &str,
        file_path: &str,
        language: &str,
        line_start: u32,
        line_end: u32,
    ) {
        let elem = CodeElement {
            qualified_name: format!("{}::{}", file_path, name),
            element_type: element_type.to_string(),
            name: name.to_string(),
            file_path: file_path.to_string(),
            line_start,
            line_end,
            language: language.to_string(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
    }

    fn insert_test_rel(
        engine: &GraphEngine,
        source: &str,
        target: &str,
        rel_type: &str,
        confidence: f64,
    ) {
        let rel = Relationship {
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: rel_type.to_string(),
            confidence,
            metadata: serde_json::json!({"resolution_method": "name", "line": 1}),
            ..Default::default()
        };
        engine.insert_relationship(&rel).unwrap();
    }

    #[test]
    fn test_graph_schema_counts() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "func_a", "function");
        insert_test_element(&engine, "func_b", "function");
        insert_test_element(&engine, "MyClass", "class");

        let schema = engine.get_graph_schema(None).unwrap();
        let obj = schema.as_object().unwrap();
        assert!(obj.contains_key("element_types"));
        assert!(obj.contains_key("relationship_types"));
        assert_eq!(obj["total_elements"].as_u64().unwrap(), 3);
    }

    #[test]
    fn test_architecture_returns_languages() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "go_handler", "function");
        insert_test_element_full(
            &engine,
            "ts_comp",
            "function",
            "src/app.ts",
            "typescript",
            1,
            10,
        );
        let arch = engine.get_architecture(None).unwrap();
        let obj = arch.as_object().unwrap();
        assert!(obj.contains_key("languages"));
        assert!(obj.contains_key("entry_points"));
        assert!(obj.contains_key("hotspots"));
        assert_eq!(obj["total_elements"].as_u64().unwrap(), 2);
    }

    #[test]
    fn test_architecture_finds_entry_points() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "helper", "function");
        insert_test_element(&engine, "main", "function");
        insert_test_element(&engine, "serve", "function");
        insert_test_element(&engine, "Start", "function");
        let arch = engine.get_architecture(None).unwrap();
        let obj = arch.as_object().unwrap();
        let eps = obj["entry_points"].as_array().unwrap();
        assert_eq!(eps.len(), 3, "Should find main, serve, Start");
    }

    #[test]
    fn test_architecture_finds_hotspots() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "hot1", "function", "src/hot.rs", "rust", 1, 5);
        insert_test_element_full(&engine, "hot2", "function", "src/hot.rs", "rust", 6, 10);
        insert_test_element_full(&engine, "hot3", "function", "src/hot.rs", "rust", 11, 15);
        insert_test_element_full(&engine, "cold", "function", "src/cold.rs", "rust", 1, 5);
        let arch = engine.get_architecture(None).unwrap();
        let obj = arch.as_object().unwrap();
        let hs = obj["hotspots"].as_array().unwrap();
        assert!(!hs.is_empty(), "Should find hotspots");
        let top = hs[0].as_object().unwrap();
        assert_eq!(top["file_path"].as_str().unwrap(), "src/hot.rs");
    }

    #[test]
    fn test_find_dead_code_finds_unused() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "used", "function", "src/lib.rs", "rust", 1, 15);
        insert_test_element_full(&engine, "caller", "function", "src/lib.rs", "rust", 20, 25);
        insert_test_rel(
            &engine,
            "src/lib.rs::caller",
            "src/lib.rs::used",
            "calls",
            0.95,
        );
        insert_test_element_full(&engine, "unused", "function", "src/lib.rs", "rust", 30, 55);
        let dead = engine.find_dead_code(10).unwrap();
        let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
        assert!(names.contains(&"unused"), "unused should be dead");
        assert!(!names.contains(&"used"), "used should not be dead");
    }

    #[test]
    fn test_find_dead_code_excludes_entry_points() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "main", "function", "src/main.rs", "rust", 1, 50);
        insert_test_element_full(&engine, "dead", "function", "src/lib.rs", "rust", 1, 20);
        let dead = engine.find_dead_code(10).unwrap();
        let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
        assert!(!names.contains(&"main"), "main should be excluded");
        assert!(names.contains(&"dead"), "dead should be listed");
    }

    #[test]
    fn test_find_dead_code_excludes_tested() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "tested", "function", "src/lib.rs", "rust", 1, 20);
        insert_test_rel(
            &engine,
            "src/test.rs::t",
            "src/lib.rs::tested",
            "tested_by",
            0.90,
        );
        insert_test_element_full(
            &engine,
            "truly_dead",
            "function",
            "src/lib.rs",
            "rust",
            25,
            45,
        );
        let dead = engine.find_dead_code(10).unwrap();
        let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
        assert!(
            !names.contains(&"tested"),
            "tested function should not be dead"
        );
        assert!(names.contains(&"truly_dead"), "truly_dead should be listed");
    }

    #[test]
    fn test_find_dead_code_respects_min_lines() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "short", "function", "src/lib.rs", "rust", 1, 3);
        insert_test_element_full(
            &engine,
            "long_dead",
            "function",
            "src/lib.rs",
            "rust",
            10,
            30,
        );
        let dead = engine.find_dead_code(10).unwrap();
        let names: Vec<&str> = dead.iter().map(|d| d["name"].as_str().unwrap()).collect();
        assert!(
            !names.contains(&"short"),
            "short should be excluded by min_lines"
        );
        assert!(names.contains(&"long_dead"), "long_dead should be included");
    }

    #[test]
    fn test_graph_schema_empty_db() {
        let (engine, _tmp) = make_test_engine();
        let schema = engine.get_graph_schema(None).unwrap();
        let obj = schema.as_object().unwrap();
        assert_eq!(obj["total_elements"].as_u64().unwrap(), 0);
        assert_eq!(obj["total_relationships"].as_u64().unwrap(), 0);
    }

    // ── FR-B22: Token budget / max_items truncation tests ──

    #[test]
    fn test_arch_max_items_truncates_languages() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "f1", "function", "src/a.rs", "rust", 1, 10);
        insert_test_element_full(&engine, "f2", "function", "src/b.go", "go", 1, 10);
        insert_test_element_full(&engine, "f3", "function", "src/c.ts", "typescript", 1, 10);
        insert_test_element_full(&engine, "f4", "function", "src/d.py", "python", 1, 10);
        insert_test_element_full(&engine, "f5", "function", "src/e.java", "java", 1, 10);

        let arch = engine.get_architecture(Some(2)).unwrap();
        let obj = arch.as_object().unwrap();
        assert_eq!(obj["languages"].as_array().unwrap().len(), 2);
        let trunc = obj["truncated_sections"].as_array().unwrap();
        let lt = trunc
            .iter()
            .find(|t| t["section"].as_str() == Some("languages"));
        assert!(lt.is_some());
        assert_eq!(lt.unwrap()["original_count"].as_u64(), Some(5));
        assert_eq!(lt.unwrap()["returned_count"].as_u64(), Some(2));
    }

    #[test]
    fn test_arch_max_items_truncates_hotspots() {
        let (engine, _tmp) = make_test_engine();
        for i in 0..5 {
            insert_test_element_full(
                &engine,
                &format!("f{}", i),
                "function",
                &format!("src/file_{}.rs", i),
                "rust",
                1,
                10,
            );
        }
        let arch = engine.get_architecture(Some(2)).unwrap();
        let obj = arch.as_object().unwrap();
        assert_eq!(obj["hotspots"].as_array().unwrap().len(), 2);
        let trunc = obj["truncated_sections"].as_array().unwrap();
        assert!(trunc
            .iter()
            .any(|t| t["section"].as_str() == Some("hotspots")));
    }

    #[test]
    fn test_arch_max_items_truncates_entry_points() {
        let (engine, _tmp) = make_test_engine();
        for ep in &["main", "Main", "start", "serve", "Start"] {
            insert_test_element_full(
                &engine,
                ep,
                "function",
                &format!("src/{}.rs", ep),
                "rust",
                1,
                10,
            );
        }
        let arch = engine.get_architecture(Some(2)).unwrap();
        let obj = arch.as_object().unwrap();
        assert_eq!(obj["entry_points"].as_array().unwrap().len(), 2);
        let trunc = obj["truncated_sections"].as_array().unwrap();
        let et = trunc
            .iter()
            .find(|t| t["section"].as_str() == Some("entry_points"));
        assert!(et.is_some());
        assert_eq!(et.unwrap()["original_count"].as_u64(), Some(5));
    }

    #[test]
    fn test_arch_none_max_items_no_truncation() {
        let (engine, _tmp) = make_test_engine();
        for i in 0..10 {
            insert_test_element_full(
                &engine,
                &format!("f{}", i),
                "function",
                &format!("src/f{}.rs", i),
                "rust",
                1,
                10,
            );
        }
        let arch = engine.get_architecture(None).unwrap();
        let obj = arch.as_object().unwrap();
        assert!(obj["truncated_sections"].as_array().unwrap().is_empty());
        assert!(obj["max_items"].is_null());
    }

    #[test]
    fn test_arch_max_items_larger_than_data() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "f1", "function", "src/a.rs", "rust", 1, 10);
        let arch = engine.get_architecture(Some(100)).unwrap();
        let obj = arch.as_object().unwrap();
        assert!(obj["truncated_sections"].as_array().unwrap().is_empty());
    }

    #[test]
    fn test_arch_max_items_includes_field() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "func1", "function");
        let arch = engine.get_architecture(Some(5)).unwrap();
        let obj = arch.as_object().unwrap();
        assert_eq!(obj["max_items"].as_u64(), Some(5));
    }

    #[test]
    fn test_arch_max_items_one_minimal() {
        let (engine, _tmp) = make_test_engine();
        for i in 0..5 {
            insert_test_element_full(
                &engine,
                &format!("f{}", i),
                "function",
                &format!("src/f{}.rs", i),
                "rust",
                1,
                10,
            );
        }
        let arch = engine.get_architecture(Some(1)).unwrap();
        let obj = arch.as_object().unwrap();
        for key in &[
            "languages",
            "entry_points",
            "hotspots",
            "clusters",
            "relationship_summary",
        ] {
            assert!(
                obj[*key].as_array().unwrap().len() <= 1,
                "Section {} has >1 item",
                key
            );
        }
    }

    #[test]
    fn test_arch_max_items_preserves_scalars() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        let arch = engine.get_architecture(Some(1)).unwrap();
        let obj = arch.as_object().unwrap();
        assert!(obj.contains_key("knowledge_count"));
        assert!(obj.contains_key("total_elements"));
        assert!(obj.contains_key("total_files"));
    }

    #[test]
    fn test_arch_truncation_preserves_all_keys() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        let arch = engine.get_architecture(Some(1)).unwrap();
        let obj = arch.as_object().unwrap();
        for key in &[
            "languages",
            "entry_points",
            "routes",
            "clusters",
            "hotspots",
            "relationship_summary",
            "knowledge_count",
            "total_elements",
            "total_files",
            "max_items",
            "truncated_sections",
        ] {
            assert!(
                obj.contains_key(*key),
                "Missing key after truncation: {}",
                key
            );
        }
    }

    #[test]
    fn test_schema_max_items_truncates_element_types() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        insert_test_element(&engine, "s1", "struct");
        insert_test_element(&engine, "e1", "enum");
        insert_test_element(&engine, "c1", "class");
        insert_test_element(&engine, "i1", "interface");
        let schema = engine.get_graph_schema(Some(2)).unwrap();
        let obj = schema.as_object().unwrap();
        assert_eq!(obj["element_types"].as_array().unwrap().len(), 2);
        let trunc = obj["truncated_sections"].as_array().unwrap();
        let et = trunc
            .iter()
            .find(|t| t["section"].as_str() == Some("element_types"));
        assert!(et.is_some());
        assert_eq!(et.unwrap()["original_count"].as_u64(), Some(5));
    }

    #[test]
    fn test_schema_max_items_truncates_rel_types() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element_full(&engine, "a", "function", "src/a.rs", "rust", 1, 10);
        insert_test_element_full(&engine, "b", "function", "src/b.rs", "rust", 1, 10);
        insert_test_element_full(&engine, "c", "function", "src/c.rs", "rust", 1, 10);
        insert_test_element_full(&engine, "d", "function", "src/d.rs", "rust", 1, 10);
        insert_test_rel(&engine, "src/a.rs::a", "src/b.rs::b", "calls", 0.9);
        insert_test_rel(&engine, "src/c.rs::c", "src/d.rs::d", "imports", 0.8);
        insert_test_rel(&engine, "src/a.rs::a", "src/c.rs::c", "references", 0.7);
        let schema = engine.get_graph_schema(Some(1)).unwrap();
        let obj = schema.as_object().unwrap();
        assert_eq!(obj["relationship_types"].as_array().unwrap().len(), 1);
        let trunc = obj["truncated_sections"].as_array().unwrap();
        let rt = trunc
            .iter()
            .find(|t| t["section"].as_str() == Some("relationship_types"));
        assert!(rt.is_some());
        assert_eq!(rt.unwrap()["original_count"].as_u64(), Some(3));
    }

    #[test]
    fn test_schema_none_max_items_no_truncation() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        let schema = engine.get_graph_schema(None).unwrap();
        let obj = schema.as_object().unwrap();
        assert!(obj["truncated_sections"].as_array().unwrap().is_empty());
        assert!(obj["max_items"].is_null());
    }

    #[test]
    fn test_schema_max_items_includes_field() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        let schema = engine.get_graph_schema(Some(5)).unwrap();
        let obj = schema.as_object().unwrap();
        assert_eq!(obj["max_items"].as_u64(), Some(5));
    }

    #[test]
    fn test_schema_max_items_preserves_scalars() {
        let (engine, _tmp) = make_test_engine();
        insert_test_element(&engine, "f1", "function");
        let schema = engine.get_graph_schema(Some(1)).unwrap();
        let obj = schema.as_object().unwrap();
        assert!(obj.contains_key("total_elements"));
        assert!(obj.contains_key("total_relationships"));
    }

    // US-GF-01: rank_element_type + PathHop serialization
    #[test]
    fn rank_function_wins_over_file() {
        assert!(rank_element_type("function") < rank_element_type("file"));
        assert!(rank_element_type("class") < rank_element_type("file"));
        assert!(rank_element_type("method") < rank_element_type("directory"));
    }

    #[test]
    fn path_hop_serializes_with_provenance_label() {
        let hop = PathHop {
            from: "a".into(),
            to: "b".into(),
            rel_type: "calls".into(),
            confidence: 0.9,
            confidence_label: "EXTRACTED".into(),
            source_file: "src/main.rs".into(),
        };
        let v = serde_json::to_value(&hop).unwrap();
        assert_eq!(v["from"], "a");
        assert_eq!(v["to"], "b");
        assert_eq!(v["confidence_label"], "EXTRACTED");
    }

    // US-GF-02: NodeExplanation serialization shape
    #[test]
    fn node_explanation_serializes_required_fields() {
        let expl = NodeExplanation {
            qualified_name: "src/main.rs::main".into(),
            name: "main".into(),
            element_type: "function".into(),
            file_path: "src/main.rs".into(),
            line_start: 1,
            line_end: 10,
            cluster_id: Some("c1".into()),
            cluster_label: Some("entry".into()),
            in_degree: 2,
            out_degree: 3,
            top_neighbors: vec![NeighborHint {
                rel_type: "calls".into(),
                count: 3,
            }],
        };
        let v = serde_json::to_value(&expl).unwrap();
        assert_eq!(v["in_degree"], 2);
        assert_eq!(v["out_degree"], 3);
        assert_eq!(v["top_neighbors"][0]["rel_type"], "calls");
    }

    // US-GF-05: GodNode degree ordering
    #[test]
    fn god_node_orders_by_degree_descending() {
        let a = GodNode {
            qualified_name: "a".into(),
            name: "a".into(),
            element_type: "file".into(),
            degree: 5,
        };
        let b = GodNode {
            qualified_name: "b".into(),
            name: "b".into(),
            element_type: "file".into(),
            degree: 10,
        };
        let mut v = vec![a.clone(), b.clone()];
        v.sort_by(|x, y| y.degree.cmp(&x.degree));
        assert_eq!(v[0].degree, 10);
        assert_eq!(v[1].degree, 5);
    }

    // US-GF-06: GraphReport markdown rendering
    #[test]
    fn graph_report_markdown_contains_required_sections() {
        let report = GraphReport {
            project: "demo".into(),
            total_elements: 10,
            total_relationships: 5,
            file_count: 4,
            function_count: 6,
            class_count: 1,
            god_nodes: vec![GodNode {
                qualified_name: "a".into(),
                name: "a".into(),
                element_type: "file".into(),
                degree: 4,
            }],
            confidence_distribution: vec![
                LabelCount {
                    label: "EXTRACTED".into(),
                    count: 3,
                    pct: 60.0,
                },
                LabelCount {
                    label: "INFERRED".into(),
                    count: 2,
                    pct: 40.0,
                },
            ],
            suggested_questions: vec!["Question 1?".into()],
        };
        let md = report.to_markdown();
        assert!(md.contains("# Graph Report: demo"));
        assert!(md.contains("Total elements: 10"));
        assert!(md.contains("EXTRACTED"));
        assert!(md.contains("Suggested Questions"));
        assert!(md.contains("Question 1?"));
    }

    // US-MP-08: folder_gn helpers
    #[test]
    fn folder_qualified_name_adds_trailing_slash() {
        assert_eq!(folder_gn::qualified_name("src"), "src/");
        assert_eq!(folder_gn::qualified_name("src/graph"), "src/graph/");
        assert_eq!(folder_gn::qualified_name("src/graph/"), "src/graph/");
    }

    #[test]
    fn folder_is_directory_detects_trailing_slash() {
        assert!(folder_gn::is_directory("src/"));
        assert!(!folder_gn::is_directory("src/main.rs"));
        assert!(!folder_gn::is_directory(""));
    }

    #[test]
    fn folder_metadata_reads_known_fields() {
        let elem = CodeElement {
            qualified_name: "src/".into(),
            element_type: "directory".into(),
            name: "src".into(),
            file_path: "src/".into(),
            metadata: serde_json::json!({
                "child_count": 5,
                "total_lines": 1234,
                "language_distribution": {"rust": 3, "toml": 2}
            }),
            ..Default::default()
        };
        let m = folder_gn::metadata(&elem).expect("directory metadata");
        assert_eq!(m.child_count, 5);
        assert_eq!(m.total_lines, 1234);
        assert_eq!(m.language_distribution.get("rust"), Some(&3));
    }

    #[test]
    fn folder_metadata_returns_none_for_non_directory() {
        let elem = CodeElement {
            element_type: "function".into(),
            ..Default::default()
        };
        assert!(folder_gn::metadata(&elem).is_none());
    }

    // US-MP-01: temporal helpers
    #[test]
    fn temporal_helpers_read_metadata_fields() {
        let mut rel = Relationship {
            source_qualified: "a".into(),
            target_qualified: "b".into(),
            rel_type: "calls".into(),
            confidence: 1.0,
            ..Default::default()
        };
        assert!(GraphEngine::valid_from(&rel).is_none());
        assert!(GraphEngine::valid_to(&rel).is_none());

        rel.metadata = serde_json::json!({"valid_from": 100, "valid_to": 200});
        assert_eq!(GraphEngine::valid_from(&rel), Some(100));
        assert_eq!(GraphEngine::valid_to(&rel), Some(200));
    }

    #[test]
    fn timeline_event_serializes_action_and_edge() {
        let ev = TimelineEvent {
            timestamp: 1234,
            action: "added".into(),
            edge: Relationship {
                source_qualified: "a".into(),
                target_qualified: "b".into(),
                rel_type: "calls".into(),
                ..Default::default()
            },
        };
        let v = serde_json::to_value(&ev).unwrap();
        assert_eq!(v["action"], "added");
        assert_eq!(v["timestamp"], 1234);
        assert_eq!(v["edge"]["rel_type"], "calls");
    }

    // US-MP-05: ConsistencyReport serialization
    #[test]
    fn consistency_report_serializes_counts_and_findings() {
        let report = ConsistencyReport {
            total_relationships: 10,
            broken: 1,
            stale: 2,
            findings: vec![ConsistencyFinding {
                severity: "BROKEN".into(),
                source: "a".into(),
                target: "missing".into(),
                rel_type: "calls".into(),
                message: "target missing".into(),
            }],
        };
        let v = serde_json::to_value(&report).unwrap();
        assert_eq!(v["broken"], 1);
        assert_eq!(v["stale"], 2);
        assert_eq!(v["findings"][0]["severity"], "BROKEN");
    }

    // US-MP-06: Tunnel serialization
    #[test]
    fn tunnel_serializes_cluster_pair() {
        let t = Tunnel {
            source: "a".into(),
            target: "b".into(),
            rel_type: "calls".into(),
            confidence: 0.9,
            source_cluster: "c1".into(),
            target_cluster: "c2".into(),
        };
        let v = serde_json::to_value(&t).unwrap();
        assert_eq!(v["source_cluster"], "c1");
        assert_eq!(v["target_cluster"], "c2");
        assert_eq!(v["confidence"], 0.9);
    }

    // US-MP-04: AgentPersona roundtrip + DiaryEntry
    #[test]
    fn agent_persona_roundtrip_and_filters() {
        let persona = AgentPersona {
            name: "reviewer".into(),
            description: "code review specialist".into(),
            focus_areas: vec!["correctness".into(), "style".into()],
            path_filters: vec!["src/".into()],
            cluster_id: Some("c1".into()),
            element_types: vec!["function".into(), "class".into()],
        };
        let json = serde_json::to_string(&persona).unwrap();
        let back: AgentPersona = serde_json::from_str(&json).unwrap();
        assert_eq!(back.name, "reviewer");
        assert_eq!(back.path_filters, vec!["src/".to_string()]);
        assert_eq!(back.cluster_id.as_deref(), Some("c1"));
    }

    #[test]
    fn diary_entry_serializes_with_tags() {
        let entry = DiaryEntry {
            timestamp: 1700000000,
            agent: "reviewer".into(),
            note: "looked at foo.rs".into(),
            tags: vec!["review".into(), "blocking".into()],
        };
        let v = serde_json::to_value(&entry).unwrap();
        assert_eq!(v["agent"], "reviewer");
        assert_eq!(v["tags"][0], "review");
    }

    // US-V2-12: TeamMapEntry serialization
    #[test]
    fn team_map_entry_serializes_services() {
        let e = TeamMapEntry {
            team: "platform".into(),
            on_call: "alice".into(),
            services: vec!["svc-a".into(), "svc-b".into()],
        };
        let v = serde_json::to_value(&e).unwrap();
        assert_eq!(v["team"], "platform");
        assert_eq!(v["on_call"], "alice");
        assert_eq!(v["services"].as_array().unwrap().len(), 2);
    }

    // US-GF-08: PrImpactReport serialization
    #[test]
    fn pr_impact_report_severity_serialization() {
        let report = PrImpactReport {
            env: "local".into(),
            changed_file_count: 3,
            touched_clusters: vec!["c1".into(), "c2".into()],
            severity: "MEDIUM".into(),
            files: vec![PrFileImpact {
                file: "src/main.rs".into(),
                cluster_id: Some("c1".into()),
                cluster_label: Some("entry".into()),
            }],
        };
        let v = serde_json::to_value(&report).unwrap();
        assert_eq!(v["severity"], "MEDIUM");
        assert_eq!(v["changed_file_count"], 3);
        assert_eq!(v["files"].as_array().unwrap().len(), 1);
    }
}
