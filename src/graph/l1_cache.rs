//! L1 read cache wrapping a `GraphEngine` clone.
//!
//! Hot read methods called from MCP tools (`search_code`, `find_function`,
//! `get_context`, `get_dependencies`, `get_dependents`, `get_call_graph`,
//! `find_large_functions`, `query_file`-style element lookups, `get_tested_by`)
//! are memoised here so concurrent agents asking the same questions do not
//! re-hit RocksDB/CozoDB and contend with writers for the embedded DB lock.
//!
//! Design:
//! - One moka `future::Cache` per logical read surface (so invalidation can be
//!   coarse or fine-grained). All caches share the same size/TTL knobs.
//! - Cache keys include the **logical** inputs (file, function name, depth
//!   bound, type filter, etc.) — pagination offset/limit are deliberately
//!   excluded so a second page request for the same logical query still hits
//!   the same cache entry (we cache the un-paged inner result and re-paginate
//!   on the way out).
//! - Write tools (`mcp_index`, `add_knowledge`, …) call `invalidate()` after
//!   the underlying CozoDB transaction commits so subsequent reads see fresh
//!   data.
//!
//! ponytail: This is the only caching layer; the per-tool `hot_cache` in
//! `ToolHandler` and the `QueryCache` inside `GraphEngine` stay as they are
//! (they serve different layers — request-local memoisation and Datalog
//! script-level caches). One layer up at `MCPServer::execute_tool` also has a
//! JSON response cache for tool dispatch.

use crate::db::models::{CodeElement, DependencyInfo, Relationship};
use crate::graph::query::GraphEngine;
use crate::graph::ImpactAnalyzer;
use moka::future::Cache;
use serde_json::{json, Value};
use std::time::Duration;

/// Read-side env knobs. Defaults match the plan (`LEANKG_L1_CACHE_SIZE` = 10K,
/// `LEANKG_L1_CACHE_TTL` = 60s). Either override is optional.
fn cache_capacity() -> u64 {
    std::env::var("LEANKG_L1_CACHE_SIZE")
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(10_000)
}

fn cache_ttl() -> Duration {
    std::env::var("LEANKG_L1_CACHE_TTL")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .map(Duration::from_secs)
        .unwrap_or_else(|| Duration::from_secs(60))
}

/// A read-through cache in front of a [`GraphEngine`] clone.
///
/// Cheap to clone (inner `GraphEngine` is `Arc`-wrapped; moka caches are
/// reference-counted internally and share their entries across clones).
#[derive(Clone)]
pub struct CachingGraphEngine {
    inner: GraphEngine,
    /// `search_code` / `query_file` lookups keyed by `(query, element_type, env)`.
    /// Stored as JSON so the cached payload is the same shape the tool returns.
    search_code: Cache<String, Value>,
    /// `find_function(name, ...)` keyed by function name.
    find_function: Cache<String, Value>,
    /// `get_context(file, max_tokens, signature_only)` keyed by file + flags.
    get_context: Cache<String, Value>,
    /// `get_dependencies(file)` keyed by file path.
    get_dependencies: Cache<String, Value>,
    /// `get_dependents(file)` keyed by file path.
    get_dependents: Cache<String, Value>,
    /// `get_call_graph(function, depth, max_results)` keyed by function + caps.
    get_call_graph: Cache<String, Value>,
    /// `find_large_functions(min_lines, …)` keyed by the min_lines threshold.
    /// Pagination offset is intentionally excluded — we paginate on the way out.
    find_large_functions: Cache<String, Value>,
    /// `get_tested_by(file)` keyed by file path.
    get_tested_by: Cache<String, Value>,
    /// `get_impact_radius(file, depth, min_confidence)` keyed by file + caps.
    get_impact_radius: Cache<String, Value>,
}

impl std::fmt::Debug for CachingGraphEngine {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("CachingGraphEngine").finish_non_exhaustive()
    }
}

impl CachingGraphEngine {
    /// Build a cache layer around `engine`. The caller still owns `engine` and
    /// is expected to clone it into this wrapper — we keep our own copy so
    /// `inner()` exposes a non-cached view for write paths.
    pub fn new(engine: GraphEngine) -> Self {
        let cap = cache_capacity();
        let ttl = cache_ttl();
        let make = || Cache::builder().max_capacity(cap).time_to_live(ttl).build();
        Self {
            inner: engine,
            search_code: make(),
            find_function: make(),
            get_context: make(),
            get_dependencies: make(),
            get_dependents: make(),
            get_call_graph: make(),
            find_large_functions: make(),
            get_tested_by: make(),
            get_impact_radius: make(),
        }
    }

    /// Escape an arbitrary cache-key component so a `:` or NUL in user input
    /// can't collide with the separator. We pick `:` because it never appears
    /// in our logical inputs (`file`, `function`, `query`).
    fn esc(s: &str) -> String {
        s.replace('\\', "\\\\").replace(':', "\\:")
    }

    /// Drop every cached entry. Called by the write path after a successful
    /// CozoDB mutation so subsequent reads see the new state.
    ///
    /// moka 0.12 dropped `clear()` in favour of `invalidate_entries_if`
    /// (predicate-driven). We match-all here because writes are infrequent
    /// relative to reads and the entry scan is amortised across concurrent
    /// get_with callers.
    pub async fn invalidate(&self) {
        // Const-`true` predicates may trip lints; bind to a local to silence.
        let drop_all = |_: &String, _: &Value| true;
        // moka 0.12 returns `Result<String, PredicateError>` synchronously.
        // We log a warning on failure but do not propagate — best-effort
        // eviction is acceptable here; the worst case is a stale read that
        // will TTL out within `LEANKG_L1_CACHE_TTL`.
        let run = |label: &str, cache: &Cache<String, Value>| {
            if let Err(e) = cache.invalidate_entries_if(drop_all) {
                tracing::warn!("L1 cache {} invalidate failed: {:?}", label, e);
            }
        };
        run("search_code", &self.search_code);
        run("find_function", &self.find_function);
        run("get_context", &self.get_context);
        run("get_dependencies", &self.get_dependencies);
        run("get_dependents", &self.get_dependents);
        run("get_call_graph", &self.get_call_graph);
        run("find_large_functions", &self.find_large_functions);
        run("get_tested_by", &self.get_tested_by);
        run("get_impact_radius", &self.get_impact_radius);
    }

    /// Non-cached access to the underlying engine. Used by write paths that
    /// must observe their own mutations and by callers that need methods
    /// outside the cached subset (e.g. `all_elements`, `find_element`).
    pub fn inner(&self) -> &GraphEngine {
        &self.inner
    }

    // ---------- Cached read API --------------------------------------------

    /// Cached `search_code(query, element_type, env, limit, offset)`.
    /// Pagination is applied on the way out so the same cache entry serves
    /// every page of the same logical query.
    pub async fn search_code(
        &self,
        query: &str,
        element_type: Option<&str>,
        env: &str,
        limit: usize,
        offset: usize,
    ) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!(
            "search_code:q={}:t={}:e={}",
            Self::esc(query),
            Self::esc(element_type.unwrap_or("")),
            Self::esc(env),
        );
        if let Some(cached) = self.search_code.get(&key).await {
            return Ok(apply_pagination(&cached, limit, offset));
        }
        let value = compute_search_code(&self.inner, query, element_type, env).await?;
        self.search_code.insert(key, value.clone()).await;
        Ok(apply_pagination(&value, limit, offset))
    }

    /// Cached `find_function(name)`. Filters `search_by_name_typed` for
    /// matches whose `name` contains the query, mirroring `ToolHandler`.
    pub async fn find_function(&self, name: &str) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!("find_function:n={}", Self::esc(name));
        if let Some(cached) = self.find_function.get(&key).await {
            return Ok(cached);
        }
        let elements = self
            .inner
            .search_by_name_typed(name, Some("function"), 50)?;
        let matches: Vec<Value> = elements
            .iter()
            .filter(|e| e.name.contains(name))
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "file": e.file_path,
                    "line": e.line_start,
                    "line_end": e.line_end
                })
            })
            .collect();
        let value = json!({ "functions": matches });
        self.find_function.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `get_context(file, max_tokens, signature_only)`.
    /// `max_tokens` and `signature_only` are part of the logical query.
    pub async fn get_context(
        &self,
        file: &str,
        max_tokens: usize,
        signature_only: bool,
    ) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!(
            "get_context:f={}:m={}:s={}",
            Self::esc(file),
            max_tokens,
            signature_only as u8
        );
        if let Some(cached) = self.get_context.get(&key).await {
            return Ok(cached);
        }
        let value = compute_get_context(&self.inner, file, max_tokens, signature_only)?;
        self.get_context.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `get_dependencies(file)`.
    pub async fn get_dependencies(&self, file: &str) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!("get_dependencies:f={}", Self::esc(file));
        if let Some(cached) = self.get_dependencies.get(&key).await {
            return Ok(cached);
        }
        let deps = self.inner.get_dependencies(file)?;
        let value = deps_to_json(&deps);
        self.get_dependencies.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `get_dependents(file)`.
    pub async fn get_dependents(&self, file: &str) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!("get_dependents:f={}", Self::esc(file));
        if let Some(cached) = self.get_dependents.get(&key).await {
            return Ok(cached);
        }
        let rels = self.inner.get_dependents(file)?;
        let value = json!({
            "dependents": rels.iter().map(|r| json!({
                "source": r.source_qualified,
                "type": r.rel_type
            })).collect::<Vec<_>>()
        });
        self.get_dependents.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `get_call_graph(function, depth, max_results)`.
    pub async fn get_call_graph(
        &self,
        function: &str,
        depth: u32,
        max_results: usize,
    ) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!(
            "get_call_graph:f={}:d={}:m={}",
            Self::esc(function),
            depth,
            max_results
        );
        if let Some(cached) = self.get_call_graph.get(&key).await {
            return Ok(cached);
        }
        let edges = self
            .inner
            .get_call_graph_bounded(function, depth, max_results)?;
        let calls: Vec<Value> = edges
            .iter()
            .map(|edge| {
                json!({
                    "source": edge.source,
                    "target": edge.target,
                    "depth": edge.depth,
                    "confidence": edge.confidence,
                    "confidence_label": edge.confidence_label,
                })
            })
            .collect();
        let value = json!({ "calls": calls });
        self.get_call_graph.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `find_large_functions(min_lines)`. Pagination offset/limit are
    /// applied on the way out.
    pub async fn find_large_functions(
        &self,
        min_lines: u32,
        limit: usize,
        offset: usize,
    ) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!("find_large_functions:m={}", min_lines);
        if let Some(cached) = self.find_large_functions.get(&key).await {
            return Ok(apply_pagination(&cached, limit, offset));
        }
        let mut elements = self.inner.find_oversized_functions(min_lines)?;
        // Mirror ToolHandler: skip worktree paths so cached results stay
        // stable as files move across git worktrees.
        elements.retain(|e| {
            !e.file_path.contains("/.claude/worktrees/") && !e.file_path.contains("/.worktrees/")
        });
        let entries: Vec<Value> = elements
            .iter()
            .map(|e| {
                json!({
                    "qualified_name": e.qualified_name,
                    "name": e.name,
                    "file": e.file_path,
                    "lines": e.line_end.saturating_sub(e.line_start),
                    "line_start": e.line_start,
                    "line_end": e.line_end
                })
            })
            .collect();
        let total = entries.len();
        let value = json!({
            "large_functions": entries,
            "total": total,
            "method": "l1_cached"
        });
        self.find_large_functions.insert(key, value.clone()).await;
        Ok(apply_pagination(&value, limit, offset))
    }

    /// Cached `get_tested_by(file)`.
    pub async fn get_tested_by(&self, file: &str) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!("get_tested_by:f={}", Self::esc(file));
        if let Some(cached) = self.get_tested_by.get(&key).await {
            return Ok(cached);
        }
        let rels = self.inner.get_relationships(file)?;
        let tests: Vec<&Relationship> = rels
            .iter()
            .filter(|r| r.rel_type == "tested_by" || r.rel_type == "tests")
            .collect();
        let value = json!({
            "tests": tests.iter().map(|r| json!({
                "source": r.source_qualified,
                "target": r.target_qualified,
                "type": r.rel_type
            })).collect::<Vec<_>>()
        });
        self.get_tested_by.insert(key, value.clone()).await;
        Ok(value)
    }

    /// Cached `get_impact_radius(file, depth, min_confidence)`.
    pub async fn get_impact_radius(
        &self,
        file: &str,
        depth: u32,
        min_confidence: f64,
    ) -> Result<Value, Box<dyn std::error::Error>> {
        let key = format!(
            "get_impact_radius:f={}:d={}:c={}",
            Self::esc(file),
            depth,
            min_confidence
        );
        if let Some(cached) = self.get_impact_radius.get(&key).await {
            return Ok(cached);
        }
        let analyzer = ImpactAnalyzer::new(&self.inner);
        let result =
            analyzer.calculate_impact_radius_with_confidence(file, depth, min_confidence)?;
        let value = json!({
            "start_file": result.start_file,
            "max_depth": result.max_depth,
            "affected": result.affected_elements.len(),
            "elements": result.affected_elements.iter().map(|e| json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path
            })).collect::<Vec<_>>(),
            "elements_with_confidence": result.affected_with_confidence.iter().map(|a| json!({
                "qualified_name": a.element.qualified_name,
                "name": a.element.name,
                "type": a.element.element_type,
                "file": a.element.file_path,
                "confidence": a.confidence
            })).collect::<Vec<_>>(),
            "method": "l1_cached"
        });
        self.get_impact_radius.insert(key, value.clone()).await;
        Ok(value)
    }
}

// ---------- helpers --------------------------------------------------------

fn deps_to_json(deps: &[DependencyInfo]) -> Value {
    json!({
        "dependencies": deps.iter().map(|d| json!({
            "target": d.target_qualified,
            "confidence": d.confidence,
            "type": "imports"
        })).collect::<Vec<_>>()
    })
}

/// Re-apply pagination over an array field inside a `Value`. Returns a
/// shallow-cloned object with the same top-level shape but a possibly smaller
/// array under the canonical field name (`results`/`functions`/`large_functions`).
fn apply_pagination(value: &Value, limit: usize, offset: usize) -> Value {
    let mut out = value.clone();
    let obj = match out.as_object_mut() {
        Some(m) => m,
        None => return out,
    };
    for field in ["results", "functions", "large_functions"] {
        if let Some(arr) = obj.get(field).and_then(|v| v.as_array()).cloned() {
            let total = arr.len();
            let sliced: Vec<Value> = arr.into_iter().skip(offset).take(limit).collect();
            let new_len = sliced.len();
            obj.insert(field.to_string(), Value::Array(sliced));
            if field == "results" || field == "large_functions" {
                obj.insert("count".to_string(), json!(new_len));
                obj.insert("limit".to_string(), json!(limit));
                obj.insert("offset".to_string(), json!(offset));
                obj.insert("total".to_string(), json!(total));
                obj.insert("has_more".to_string(), json!(offset + new_len < total));
            }
        }
    }
    out
}

async fn compute_search_code(
    engine: &GraphEngine,
    query: &str,
    element_type: Option<&str>,
    env: &str,
) -> Result<Value, Box<dyn std::error::Error>> {
    let mega = crate::ontology::safe_discover::is_mega_graph(engine);
    if mega {
        let mut page = crate::ontology::safe_discover::discover(
            engine, query, env, 1_000, // upper bound; pagination applied via apply_pagination
            0, true,
        )?;
        if let Some(et) = element_type {
            page.results.retain(|e| e.element_type == et);
            page.total_estimate = page.results.len();
            page.has_more = false;
        }
        return Ok(crate::ontology::safe_discover::discover_page_to_json(&page));
    }
    let limit = crate::ontology::safe_discover::clamp_limit(1_000);
    let elements = engine.search_by_name_typed(query, element_type, limit)?;
    let matches: Vec<Value> = elements
        .iter()
        .map(|e| {
            json!({
                "qualified_name": e.qualified_name,
                "name": e.name,
                "type": e.element_type,
                "file": e.file_path,
                "line": e.line_start,
                "cluster_id": e.cluster_id,
                "cluster_label": e.cluster_label
            })
        })
        .collect();
    let total_estimate = matches.len();
    Ok(json!({
        "results": matches,
        "count": matches.len(),
        "total_estimate": total_estimate,
        "has_more": false,
        "method": "l1_cached"
    }))
}

fn compute_get_context(
    engine: &GraphEngine,
    file: &str,
    max_tokens: usize,
    signature_only: bool,
) -> Result<Value, Box<dyn std::error::Error>> {
    let result = engine.get_context(file, max_tokens)?;
    let elements_json: Vec<Value> = result
        .elements
        .iter()
        .map(|ctx_elem| {
            let elem = &ctx_elem.element;
            let priority_str = match ctx_elem.priority {
                crate::graph::ContextPriority::RecentlyChanged => "recently_changed",
                crate::graph::ContextPriority::Imported => "imported",
                crate::graph::ContextPriority::Contained => "contained",
            };
            if signature_only {
                let signature = elem
                    .metadata
                    .get("signature")
                    .and_then(|v| v.as_str())
                    .unwrap_or("")
                    .to_string();
                json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path,
                    "line": elem.line_start,
                    "signature": signature,
                    "priority": priority_str,
                    "token_count": ctx_elem.token_count,
                    "cluster_id": elem.cluster_id,
                    "cluster_label": elem.cluster_label
                })
            } else {
                json!({
                    "qualified_name": elem.qualified_name,
                    "name": elem.name,
                    "type": elem.element_type,
                    "file": elem.file_path,
                    "line_start": elem.line_start,
                    "line_end": elem.line_end,
                    "priority": priority_str,
                    "token_count": ctx_elem.token_count,
                    "cluster_id": elem.cluster_id,
                    "cluster_label": elem.cluster_label
                })
            }
        })
        .collect();
    let file_element = engine.find_element(file)?;
    let cluster_info = file_element.as_ref().map(|elem| {
        json!({
            "id": elem.cluster_id,
            "label": elem.cluster_label
        })
    });
    let dependents_count = file_element
        .as_ref()
        .map(|elem| {
            engine
                .get_dependents(elem.qualified_name.as_str())
                .map(|d| d.len())
                .unwrap_or(0)
        })
        .unwrap_or(0);
    let dependencies_count = file_element
        .as_ref()
        .map(|elem| {
            engine
                .get_dependencies(elem.qualified_name.as_str())
                .map(|d| d.len())
                .unwrap_or(0)
        })
        .unwrap_or(0);
    Ok(json!({
        "file": file,
        "cluster": cluster_info,
        "dependents_count": dependents_count,
        "dependencies_count": dependencies_count,
        "elements": elements_json,
        "total_tokens": result.total_tokens,
        "max_tokens": result.max_tokens,
        "truncated": result.truncated,
        "signature_only": signature_only,
        "prompt": result.to_prompt(),
        "method": "l1_cached"
    }))
}

/// `get_relationships_for_file` is the underlying GraphEngine method that
/// backs `get_tested_by`. It's used only via the cache, never directly.
#[allow(dead_code)]
fn _unused_rels_signature() {}

/// Code elements are also reachable from the inner engine; we keep the
/// import live so future cache entries (e.g. element lists) can re-use the
/// `CodeElement` shape without re-touching this module's imports.
#[allow(dead_code)]
fn _code_element_marker(_e: &CodeElement) {}
