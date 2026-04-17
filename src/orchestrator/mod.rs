pub mod cache;
pub mod intent;

use crate::compress::{FileReader, ReadMode};
use crate::graph::GraphEngine;
use cache::{CachedContent, OrchestratorCache};
use intent::{Intent, IntentParser};
use parking_lot::Mutex;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrchestratedResult {
    pub intent: String,
    pub query_type: String,
    pub content: String,
    pub mode: String,
    pub tokens: usize,
    pub total_tokens: usize,
    pub savings_percent: f64,
    pub is_cached: bool,
    pub cache_key: String,
    pub elements_count: usize,
}

pub struct QueryOrchestrator {
    graph_engine: GraphEngine,
    cache: Arc<Mutex<OrchestratorCache>>,
    intent_parser: IntentParser,
}

impl QueryOrchestrator {
    pub fn new(graph_engine: GraphEngine) -> Self {
        Self {
            graph_engine,
            cache: Arc::new(Mutex::new(OrchestratorCache::new(300, 1000))),
            intent_parser: IntentParser::new(),
        }
    }

    pub fn with_persistence(graph_engine: GraphEngine) -> Self {
        let db_arc = Arc::new(graph_engine.db().clone());
        let persistent_cache = Arc::new(crate::graph::PersistentCache::new(db_arc.clone(), 300));
        Self {
            graph_engine,
            cache: Arc::new(Mutex::new(OrchestratorCache::with_persistence(
                300,
                1000,
                persistent_cache,
            ))),
            intent_parser: IntentParser::new(),
        }
    }

    pub fn orchestrate(
        &self,
        intent_str: &str,
        file: Option<&str>,
        mode: Option<&str>,
        fresh: bool,
    ) -> Result<OrchestratedResult, String> {
        let intent = self.intent_parser.parse(intent_str);
        let cache_key = self.compute_cache_key(&intent, file, mode);

        if !fresh {
            if let Some(cached) = self.cache.lock().get(&cache_key) {
                return Ok(OrchestratedResult {
                    intent: intent_str.to_string(),
                    query_type: intent.query_type.clone(),
                    content: cached.content.clone(),
                    mode: cached.mode.clone(),
                    tokens: cached.tokens,
                    total_tokens: cached.total_tokens,
                    savings_percent: cached.savings_percent,
                    is_cached: true,
                    cache_key,
                    elements_count: cached.elements_count,
                });
            }
        }

        let result = self.execute_intent(&intent, file, mode)?;
        self.cache.lock().insert(cache_key.clone(), result.clone());

        Ok(OrchestratedResult {
            intent: intent_str.to_string(),
            query_type: intent.query_type,
            content: result.content,
            mode: result.mode,
            tokens: result.tokens,
            total_tokens: result.total_tokens,
            savings_percent: result.savings_percent,
            is_cached: false,
            cache_key,
            elements_count: result.elements_count,
        })
    }

    fn execute_intent(
        &self,
        intent: &Intent,
        file: Option<&str>,
        mode: Option<&str>,
    ) -> Result<CachedContent, String> {
        match intent.query_type.as_str() {
            "context" => self.get_context_internal(file, mode),
            "impact" => self.get_impact_internal(file, mode),
            "dependencies" => self.get_dependencies_internal(file),
            "search" => self.search_internal(file.unwrap_or("*")),
            "doc" => self.get_doc_internal(file),
            _ => self.get_context_internal(file, mode),
        }
    }

    fn read_file(&self, path: &str, mode: ReadMode) -> Result<CachedContent, String> {
        let mut reader = FileReader::default();
        let result = reader
            .read(path, mode, None, false)
            .map_err(|e| e.to_string())?;
        Ok(CachedContent {
            content: result.content,
            mode: format!("{:?}", result.mode),
            tokens: result.tokens,
            total_tokens: result.total_tokens,
            savings_percent: result.savings_percent,
            elements_count: 0,
        })
    }

    fn get_context_internal(
        &self,
        file: Option<&str>,
        mode: Option<&str>,
    ) -> Result<CachedContent, String> {
        let target_file = file.ok_or("File required for context query")?;
        let read_mode = self.resolve_mode(mode, target_file);

        let file_elements = self
            .graph_engine
            .get_elements_by_file(target_file)
            .map_err(|e| e.to_string())?;

        let result = self.read_file(target_file, read_mode)?;

        Ok(CachedContent {
            content: result.content,
            mode: result.mode,
            tokens: result.tokens,
            total_tokens: result.total_tokens,
            savings_percent: result.savings_percent,
            elements_count: file_elements.len(),
        })
    }

    fn get_impact_internal(
        &self,
        file: Option<&str>,
        mode: Option<&str>,
    ) -> Result<CachedContent, String> {
        let target_file = file.ok_or("File required for impact analysis")?;
        let affected = self
            .graph_engine
            .get_elements_by_file(target_file)
            .map_err(|e| e.to_string())?;

        let mut content = format!("# Impact Analysis for {}\n\n", target_file);
        content += &format!("Affected elements: {}\n\n", affected.len());

        for elem in affected.iter().take(20) {
            content += &format!("- {} ({})\n", elem.qualified_name, elem.element_type);
        }

        let read_mode = self.resolve_mode(mode, target_file);
        let result = self.read_file(target_file, read_mode)?;

        content += &format!("\n## File Content ({} mode)\n\n", read_mode);
        content += &result.content;

        Ok(CachedContent {
            content,
            mode: format!("{:?}", read_mode),
            tokens: result.tokens,
            total_tokens: result.total_tokens,
            savings_percent: result.savings_percent,
            elements_count: affected.len(),
        })
    }

    fn get_dependencies_internal(&self, file: Option<&str>) -> Result<CachedContent, String> {
        let target_file = file.ok_or("File required for dependencies query")?;

        let relationships = self
            .graph_engine
            .get_relationships(target_file)
            .map_err(|e| e.to_string())?;

        let deps: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "imports" || r.rel_type == "calls")
            .collect();

        let mut content = format!("# Dependencies for {}\n\n", target_file);
        content += &format!("Total dependencies: {}\n\n", deps.len());

        for dep in deps.iter().take(50) {
            content += &format!("- {} ({})\n", dep.target_qualified, dep.rel_type);
        }

        let tokens = content.len() / 4;

        Ok(CachedContent {
            content,
            mode: "dependencies".to_string(),
            tokens,
            total_tokens: tokens,
            savings_percent: 0.0,
            elements_count: deps.len(),
        })
    }

    fn search_internal(&self, pattern: &str) -> Result<CachedContent, String> {
        let elements = self
            .graph_engine
            .search_by_pattern(pattern)
            .map_err(|e| e.to_string())?;

        let mut content = format!("# Search Results for '{}'\n\n", pattern);
        content += &format!("Total matches: {}\n\n", elements.len());

        for elem in elements.iter().take(30) {
            content += &format!(
                "- {} ({}): {} [L{}-{}]\n",
                elem.qualified_name,
                elem.element_type,
                elem.file_path,
                elem.line_start,
                elem.line_end
            );
        }

        let tokens = content.len() / 4;
        let savings = if elements.len() > 10 { 75.0 } else { 0.0 };

        Ok(CachedContent {
            content,
            mode: "search".to_string(),
            tokens,
            total_tokens: tokens,
            savings_percent: savings,
            elements_count: elements.len(),
        })
    }

    fn get_doc_internal(&self, file: Option<&str>) -> Result<CachedContent, String> {
        let target_file = file.ok_or("File required for doc query")?;

        let file_elements = self
            .graph_engine
            .get_elements_by_file(target_file)
            .map_err(|e| e.to_string())?;

        let relationships = self
            .graph_engine
            .get_relationships(target_file)
            .map_err(|e| e.to_string())?;

        let docs: Vec<_> = relationships
            .iter()
            .filter(|r| r.rel_type == "documented_by")
            .collect();

        let mut content = format!("# Documentation for {}\n\n", target_file);
        content += &format!(
            "Code elements: {}, Related docs: {}\n\n",
            file_elements.len(),
            docs.len()
        );

        if !docs.is_empty() {
            content += "## Documentation Links\n\n";
            for doc in docs.iter().take(10) {
                content += &format!("- {} ({})\n", doc.target_qualified, doc.rel_type);
            }
        }

        let tokens = content.len() / 4;

        Ok(CachedContent {
            content,
            mode: "documentation".to_string(),
            tokens,
            total_tokens: tokens,
            savings_percent: 0.0,
            elements_count: file_elements.len(),
        })
    }

    fn resolve_mode(&self, mode: Option<&str>, file: &str) -> ReadMode {
        if let Some(m) = mode {
            let parsed = ReadMode::from_str(m);
            match parsed {
                Some(ReadMode::Adaptive) => ReadMode::select_adaptive(file, 1000, 100),
                Some(m) => m,
                None => ReadMode::select_adaptive(file, 1000, 100),
            }
        } else {
            ReadMode::select_adaptive(file, 1000, 100)
        }
    }

    fn compute_cache_key(&self, intent: &Intent, file: Option<&str>, mode: Option<&str>) -> String {
        format!(
            "{}:{}:{}",
            intent.query_type,
            file.unwrap_or("*"),
            mode.unwrap_or("auto")
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_intent_parser_context() {
        let parser = IntentParser::new();
        let intent = parser.parse("give me context for main.rs");
        assert_eq!(intent.query_type, "context");
    }

    #[test]
    fn test_intent_parser_impact() {
        let parser = IntentParser::new();
        let intent = parser.parse("what's the impact of changing lib.rs");
        assert_eq!(intent.query_type, "impact");
    }

    #[test]
    fn test_intent_parser_dependencies() {
        let parser = IntentParser::new();
        let intent = parser.parse("show me dependencies for handler.rs");
        assert_eq!(intent.query_type, "dependencies");
    }

    #[test]
    fn test_intent_parser_search() {
        let parser = IntentParser::new();
        let intent = parser.parse("find function named parse_config");
        assert_eq!(intent.query_type, "search");
    }

    #[test]
    fn test_intent_parser_doc() {
        let parser = IntentParser::new();
        let intent = parser.parse("get documentation for mod.rs");
        assert_eq!(intent.query_type, "doc");
    }

    #[test]
    fn test_resolve_mode() {
        let temp_dir = std::env::temp_dir();
        let db_path = temp_dir.join("leankg_testResolveMode.db");
        let db = crate::db::schema::init_db(&db_path).unwrap();
        let graph = GraphEngine::new(db);
        let orchestrator = QueryOrchestrator::new(graph);

        let mode = orchestrator.resolve_mode(Some("signatures"), "test.rs");
        assert_eq!(mode, ReadMode::Signatures);

        let mode = orchestrator.resolve_mode(None, "test.rs");
        assert_eq!(mode, ReadMode::Map);

        let mode = orchestrator.resolve_mode(None, "README.md");
        assert_eq!(mode, ReadMode::Full);

        std::fs::remove_file(db_path).ok();
    }

    #[test]
    fn test_compute_cache_key() {
        let temp_dir = std::env::temp_dir();
        let db_path = temp_dir.join("leankg_testComputeCacheKey.db");
        let db = crate::db::schema::init_db(&db_path).unwrap();
        let graph = GraphEngine::new(db);
        let orchestrator = QueryOrchestrator::new(graph);
        let intent = IntentParser::new().parse("context query");

        let key = orchestrator.compute_cache_key(&intent, Some("main.rs"), Some("adaptive"));
        assert_eq!(key, "context:main.rs:adaptive");

        let key = orchestrator.compute_cache_key(&intent, None, None);
        assert_eq!(key, "context:*:auto");

        std::fs::remove_file(db_path).ok();
    }
}
