use crate::db::models::{CodeElement, Relationship};
use surrealdb::engine::local::Db;
use surrealdb::Surreal;

pub struct GraphEngine {
    db: Surreal<Db>,
}

impl GraphEngine {
    pub fn new(db: Surreal<Db>) -> Self {
        Self { db }
    }

    pub async fn find_element(&self, qualified_name: &str) -> Result<Option<CodeElement>, Box<dyn std::error::Error>> {
        let name = qualified_name.to_string();
        let result: Option<CodeElement> = self.db
            .query("SELECT * FROM code_elements WHERE qualified_name = $name")
            .bind(("name", name))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_dependencies(&self, file_path: &str) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let path = file_path.to_string();
        let result: Vec<CodeElement> = self.db
            .query("SELECT * FROM code_elements WHERE qualified_name = $path")
            .bind(("path", path))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_relationships(&self, source: &str) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let src = source.to_string();
        let result: Vec<Relationship> = self.db
            .query("SELECT * FROM relationships WHERE source_qualified = $source")
            .bind(("source", src))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn get_dependents(&self, target: &str) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let tgt = target.to_string();
        let result: Vec<Relationship> = self.db
            .query("SELECT * FROM relationships WHERE target_qualified = $target")
            .bind(("target", tgt))
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn all_elements(&self) -> Result<Vec<CodeElement>, Box<dyn std::error::Error>> {
        let result: Vec<CodeElement> = self.db
            .query("SELECT * FROM code_elements")
            .await?
            .take(0)?;
        Ok(result)
    }

    pub async fn all_relationships(&self) -> Result<Vec<Relationship>, Box<dyn std::error::Error>> {
        let result: Vec<Relationship> = self.db
            .query("SELECT * FROM relationships")
            .await?
            .take(0)?;
        Ok(result)
    }
}
