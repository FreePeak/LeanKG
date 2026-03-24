use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use crate::db::schema::CozoDb;
use crate::graph::cache::QueryCache;
use std::sync::Arc;
use tokio::sync::RwLock;

#[derive(Clone)]
pub struct GraphEngine {
    db: CozoDb,
    cache: Arc<RwLock<QueryCache>>,
}

impl GraphEngine {
    pub fn new(db: CozoDb) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(QueryCache::new(300, 1000))),
        }
    }

    pub fn with_cache(db: CozoDb, cache: QueryCache) -> Self {
        Self {
            db,
            cache: Arc::new(RwLock::new(cache)),
        }
    }

    pub fn find_element(
        &self,
        qualified_name: &str,
    ) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where qualified_name = "{}"
            "#,
            qualified_name
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        let parent_qualified = row[7].as_str().map(String::from);
        let metadata_str = row[8].as_str().unwrap_or("{}");
        
        Ok(Some(CodeElement {
            qualified_name: row[0].as_str().unwrap_or("").to_string(),
            element_type: row[1].as_str().unwrap_or("").to_string(),
            name: row[2].as_str().unwrap_or("").to_string(),
            file_path: row[3].as_str().unwrap_or("").to_string(),
            line_start: row[4].as_i64().unwrap_or(0) as u32,
            line_end: row[5].as_i64().unwrap_or(0) as u32,
            language: row[6].as_str().unwrap_or("").to_string(),
            parent_qualified,
            metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
        }))
    }

    pub fn get_dependencies(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where file_path = "{}"
            "#,
            file_path
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        if !elements.is_empty() {
            let qns: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
            let db_path = file_path.to_string();
            let cache = self.cache.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.set_dependencies(db_path, qns).await;
                });
            });
        }

        Ok(elements)
    }

    pub fn get_relationships(
        &self,
        source: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[source_qualified, target_qualified, rel_type, metadata] <- relationships[source_qualified, target_qualified, rel_type, metadata]
            where source_qualified = "{}"
            "#,
            source
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[3].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn get_dependents(
        &self,
        target: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[source_qualified, target_qualified, rel_type, metadata] <- relationships[source_qualified, target_qualified, rel_type, metadata]
            where target_qualified = "{}"
            "#,
            target
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[3].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        if !relationships.is_empty() {
            let qns: Vec<String> = relationships.iter().map(|r| r.target_qualified.clone()).collect();
            let db_target = target.to_string();
            let cache = self.cache.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.set_dependents(db_target, qns).await;
                });
            });
        }

        Ok(relationships)
    }

    pub fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = r#"?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn all_relationships(&self) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let query = r#"?[source_qualified, target_qualified, rel_type, metadata] <- relationships[source_qualified, target_qualified, rel_type, metadata]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[3].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn get_children(
        &self,
        parent_qualified: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where parent_qualified = "{}"
            "#,
            parent_qualified
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn get_annotation(
        &self,
        element_qualified: &str,
    ) -> Result<Option<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[element_qualified, description, user_story_id, feature_id] <- business_logic[element_qualified, description, user_story_id, feature_id]
            where element_qualified = "{}"
            "#,
            element_qualified
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        if rows.is_empty() {
            return Ok(None);
        }

        let row = &rows[0];
        Ok(Some(BusinessLogic {
            id: None,
            element_qualified: row[0].as_str().unwrap_or("").to_string(),
            description: row[1].as_str().unwrap_or("").to_string(),
            user_story_id: row[2].as_str().map(String::from),
            feature_id: row[3].as_str().map(String::from),
        }))
    }

    pub fn search_annotations(
        &self,
        query_str: &str,
    ) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let like_pattern = format!("%{}%", query_str.to_lowercase());
        
        let query = format!(
            r#"
            ?[element_qualified, description, user_story_id, feature_id] <- business_logic[element_qualified, description, user_story_id, feature_id]
            where description =~ "{}"
            "#,
            like_pattern
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id: row[2].as_str().map(String::from),
                feature_id: row[3].as_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn all_annotations(&self) -> Result<Vec<BusinessLogic>, Box<dyn std::error::Error>> {
        let query = r#"?[element_qualified, description, user_story_id, feature_id] <- business_logic[element_qualified, description, user_story_id, feature_id]"#;

        let result = self.db.run_script(query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let annotations: Vec<BusinessLogic> = rows
            .iter()
            .map(|row| BusinessLogic {
                id: None,
                element_qualified: row[0].as_str().unwrap_or("").to_string(),
                description: row[1].as_str().unwrap_or("").to_string(),
                user_story_id: row[2].as_str().map(String::from),
                feature_id: row[3].as_str().map(String::from),
            })
            .collect();

        Ok(annotations)
    }

    pub fn insert_elements(
        &self,
        elements: &[CodeElement],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if elements.is_empty() {
            return Ok(());
        }

        for element in elements {
            let metadata_str = serde_json::to_string(&element.metadata)?;
            let parent_val = element.parent_qualified.as_ref()
                .map(|s| format!("\"{}\"", s))
                .unwrap_or_else(|| "null".to_string());
            
            let query = format!(
                r#"
                ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- [
                    ["{}": String, "{}": String, "{}": String, "{}": String, {}: Int, {}: Int, "{}": String, {}: String, "{}": String]
                ] :put code_elements {{ qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata }}
                "#,
                element.qualified_name,
                element.element_type,
                element.name,
                element.file_path,
                element.line_start,
                element.line_end,
                element.language,
                parent_val,
                metadata_str,
            );
            
            self.db.run_script(&query, std::collections::BTreeMap::new())?;
        }

        if let Some(first) = elements.first() {
            let cache = self.cache.clone();
            let file_path = first.file_path.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.invalidate_file(&file_path).await;
                });
            });
        }

        Ok(())
    }

    pub fn insert_relationships(
        &self,
        relationships: &[Relationship],
    ) -> Result<(), Box<dyn std::error::Error>> {
        if relationships.is_empty() {
            return Ok(());
        }

        for rel in relationships {
            let metadata_str = serde_json::to_string(&rel.metadata)?;
            
            let query = format!(
                r#"
                ?[source_qualified, target_qualified, rel_type, metadata] <- [
                    ["{}": String, "{}": String, "{}": String, "{}": String]
                ] :put relationships {{ source_qualified, target_qualified, rel_type, metadata }}
                "#,
                rel.source_qualified,
                rel.target_qualified,
                rel.rel_type,
                metadata_str,
            );
            
            self.db.run_script(&query, std::collections::BTreeMap::new())?;
        }

        if let Some(first) = relationships.first() {
            let cache = self.cache.clone();
            let file_path = first.source_qualified.clone();
            std::thread::spawn(move || {
                let rt = tokio::runtime::Runtime::new().unwrap();
                rt.block_on(async {
                    cache.read().await.invalidate_file(&file_path).await;
                });
            });
        }

        Ok(())
    }

    pub fn remove_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            :delete code_elements where file_path = "{}"
            "#,
            file_path
        );
        
        self.db.run_script(&query, std::collections::BTreeMap::new())?;

        let cache = self.cache.clone();
        let file_path_str = file_path.to_string();
        std::thread::spawn(move || {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(async {
                cache.read().await.invalidate_file(&file_path_str).await;
            });
        });
        
        Ok(())
    }

    pub fn remove_relationships_by_source(
        &self,
        source: &str,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            :delete relationships where source_qualified = "{}"
            "#,
            source
        );
        
        self.db.run_script(&query, std::collections::BTreeMap::new())?;

        let cache = self.cache.clone();
        let source_str = source.to_string();
        std::thread::spawn(move || {
            let rt = tokio::runtime::Runtime::new().unwrap();
            rt.block_on(async {
                cache.read().await.invalidate_file(&source_str).await;
            });
        });
        
        Ok(())
    }

    pub fn get_elements_by_file(
        &self,
        file_path: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where file_path = "{}"
            "#,
            file_path
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_name(
        &self,
        name: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let pattern = format!("%{}%", name.to_lowercase());
        
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where name =~ "{}"
            "#,
            pattern
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_type(
        &self,
        element_type: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where element_type = "{}"
            "#,
            element_type
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_pattern(
        &self,
        pattern: &str,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let like_pattern = format!("%{}%", pattern);
        
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where qualified_name =~ "{}"
            "#,
            like_pattern
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(elements)
    }

    pub fn search_by_relation_type(
        &self,
        rel_type: &str,
    ) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[source_qualified, target_qualified, rel_type, metadata] <- relationships[source_qualified, target_qualified, rel_type, metadata]
            where rel_type = "{}"
            "#,
            rel_type
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let relationships: Vec<Relationship> = rows
            .iter()
            .map(|row| {
                let metadata_str = row[3].as_str().unwrap_or("{}");
                Relationship {
                    id: None,
                    source_qualified: row[0].as_str().unwrap_or("").to_string(),
                    target_qualified: row[1].as_str().unwrap_or("").to_string(),
                    rel_type: row[2].as_str().unwrap_or("").to_string(),
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
                }
            })
            .collect();

        Ok(relationships)
    }

    pub fn find_oversized_functions(
        &self,
        min_lines: u32,
    ) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where element_type = "function" and (line_end - line_start + 1) >= {}
            "#,
            min_lines
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
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
        let query = format!(
            r#"
            ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata] <- code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, metadata]
            where element_type = "function" and language = "{}" and (line_end - line_start + 1) >= {}
            "#,
            language,
            min_lines
        );

        let result = self.db.run_script(&query, std::collections::BTreeMap::new())?;
        let rows = result.rows;

        let mut elements: Vec<CodeElement> = rows
            .iter()
            .map(|row| {
                let parent_qualified = row[7].as_str().map(String::from);
                let metadata_str = row[8].as_str().unwrap_or("{}");
                CodeElement {
                    qualified_name: row[0].as_str().unwrap_or("").to_string(),
                    element_type: row[1].as_str().unwrap_or("").to_string(),
                    name: row[2].as_str().unwrap_or("").to_string(),
                    file_path: row[3].as_str().unwrap_or("").to_string(),
                    line_start: row[4].as_i64().unwrap_or(0) as u32,
                    line_end: row[5].as_i64().unwrap_or(0) as u32,
                    language: row[6].as_str().unwrap_or("").to_string(),
                    parent_qualified,
                    metadata: serde_json::from_str(metadata_str).unwrap_or(serde_json::json!({})),
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
}
