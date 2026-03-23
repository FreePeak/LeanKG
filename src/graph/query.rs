use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use crate::graph::cache::QueryCache;
use std::sync::Arc;
use surrealdb::engine::local::Db;
use surrealdb::Surreal;
use tokio::sync::RwLock;

#[derive(Clone)]
pub struct GraphEngine {
    db: Surreal<Db>,
    cache: Arc<RwLock<QueryCache>>,
}

impl GraphEngine {
    pub fn new(db: Surreal<Db>) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(QueryCache::new(300, 1000))),
        }
    }

    pub fn with_cache(db: Surreal<Db>, cache: QueryCache) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(cache)),
        }
    }

    pub async fn find_element(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let name = qualified_name.to_string();
        let result: Option<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE qualified_name = $name")
            .bind(("name", name))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_dependencies(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let path = file_path.to_string();
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE file_path = $path")
            .bind(("path", path))
            .await?
            .take(0)?;

        if !result.is_empty() {
            let qns: Vec<String> = result.iter().map(|e| e.qualified_name.clone()).collect();
            self.cache
                .read()
                .await
                .set_dependencies(file_path.to_string(), qns)
                .await;
        }

        Ok(result)
    }

    pub async fn get_relationships(
        &self,
        source: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let src = source.to_string();
        let result: Vec<Relationship> = self
            .db
            .query("SELECT * FROM relationships WHERE source_qualified = $source")
            .bind(("source", src))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_dependents(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let tgt = target.to_string();
        let result: Vec<Relationship> = self
            .db
            .query("SELECT * FROM relationships WHERE target_qualified = $target")
            .bind(("target", tgt))
            .await?
            .take(0)?;

        if !result.is_empty() {
            let qns: Vec<String> = result.iter().map(|r| r.target_qualified.clone()).collect();
            self.cache
                .read()
                .await
                .set_dependents(target.to_string(), qns)
                .await;
        }

        Ok(result)
    }

    pub async fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements")
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn all_relationships(&self) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let result: Vec<Relationship> = self
            .db
            .query("SELECT * FROM relationships")
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_children(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let parent = parent_qualified.to_string();
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE parent_qualified = $parent")
            .bind(("parent", parent))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_annotation(
        &self,
        element_qualified: &str,
    ) -> Result<Option<BusinessLogic>, Box<dyn std::error::Error>> {
        let name = element_qualified.to_string();
        let result: Option<BusinessLogic> = self
            .db
            .query("SELECT * FROM business_logic WHERE element_qualified = $name")
            .bind(("name", name))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn search_annotations(
        &self,
        query: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let q = format!("%{}%", query.to_lowercase());
        let result: Vec<BusinessLogic> = self
            .db
            .query("SELECT * FROM business_logic WHERE string::lowercase(description) CONTAINS $q")
            .bind(("q", q))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn all_annotations(&self) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let result: Vec<BusinessLogic> = self
            .db
            .query("SELECT * FROM business_logic")
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn insert_elements(
        &self,
        elements: &[CodeElement],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if elements.is_empty() {
            return Ok(());
        }

        let elements_json: Vec<_> = elements
            .iter()
            .map(serde_json::to_value)
            .collect::<Result<Vec<_>, _>>()?;

        self.db
            .query("INSERT INTO code_elements $elements_json")
            .bind(("elements_json", elements_json))
            .await?;

        if let Some(first) = elements.first() {
            self.cache
                .read()
                .await
                .invalidate_file(&first.file_path)
                .await;
        }

        Ok(())
    }

    pub async fn insert_relationships(
        &self,
        relationships: &[Relationship],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if relationships.is_empty() {
            return Ok(());
        }

        let rels_json: Vec<_> = relationships
            .iter()
            .map(serde_json::to_value)
            .collect::<Result<Vec<_>, _>>()?;

        self.db
            .query("INSERT INTO relationships $rels_json")
            .bind(("rels_json", rels_json))
            .await?;

        if let Some(first) = relationships.first() {
            self.cache
                .read()
                .await
                .invalidate_file(&first.source_qualified)
                .await;
        }

        Ok(())
    }

    pub async fn remove_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let path = file_path.to_string();
        self.db
            .query("DELETE FROM code_elements WHERE file_path = $path")
            .bind(("path", path))
            .await?;

        self.cache.read().await.invalidate_file(file_path).await;
        Ok(())
    }

    pub async fn remove_relationships_by_source(
        &self,
        source: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let src = source.to_string();
        self.db
            .query("DELETE FROM relationships WHERE source_qualified = $source")
            .bind(("source", src))
            .await?;

        self.cache.read().await.invalidate_file(source).await;
        Ok(())
    }

    pub async fn get_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let path = file_path.to_string();
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE file_path = $path")
            .bind(("path", path))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn search_by_name(
        &self,
        name: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let pattern = format!("%{}%", name.to_lowercase());
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE string::lowercase(name) CONTAINS $pattern")
            .bind(("pattern", pattern))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn search_by_type(
        &self,
        element_type: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let etype = element_type.to_string();
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE element_type = $etype")
            .bind(("etype", etype))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn search_by_pattern(
        &self,
        pattern: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let pat = format!("%{}%", pattern);
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE qualified_name CONTAINS $pat")
            .bind(("pat", pat))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn search_by_relation_type(
        &self,
        rel_type: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let rtype = rel_type.to_string();
        let result: Vec<Relationship> = self
            .db
            .query("SELECT * FROM relationships WHERE rel_type = $rtype")
            .bind(("rtype", rtype))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn find_oversized_functions(
        &self,
        min_lines: u32,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE element_type = 'function' AND (line_end - line_start + 1) >= $min ORDER BY (line_end - line_start + 1) DESC")
            .bind(("min", min_lines))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn find_oversized_functions_by_lang(
        &self,
        min_lines: u32,
        language: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let min = min_lines;
        let lang = language.to_string();
        let result: Vec<CodeElement> = self
            .db
            .query("SELECT * FROM code_elements WHERE element_type = 'function' AND language = $lang AND (line_end - line_start + 1) >= $min ORDER BY (line_end - line_start + 1) DESC")
            .bind(("min", min))
            .bind(("lang", lang))
            .await?
            .take(0)?;
        Ok(result)
    }
}
