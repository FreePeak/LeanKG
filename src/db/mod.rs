pub mod models;
pub mod schema;

#[allow(unused_imports)]
pub use models::*;
#[allow(unused_imports)]
pub use schema::*;

use surrealdb::engine::local::Db;
use surrealdb::Surreal;

pub async fn create_business_logic(
    db: &Surreal<Db>,
    element_qualified: &str,
    description: &str,
    user_story_id: Option<&str>,
    feature_id: Option<&str>,
) -> Result<models::BusinessLogic, Box<dyn std::error::Error>> {
    let bl = models::BusinessLogic {
        id: None,
        element_qualified: element_qualified.to_string(),
        description: description.to_string(),
        user_story_id: user_story_id.map(String::from),
        feature_id: feature_id.map(String::from),
    };

    let result: Option<models::BusinessLogic> = db
        .query("CREATE business_logic CONTENT $bl RETURN *")
        .bind(("bl", bl))
        .await?
        .take(0)?;

    result.ok_or_else(|| "Failed to create business logic".into())
}

pub async fn get_business_logic(
    db: &Surreal<Db>,
    element_qualified: &str,
) -> Result<Option<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let name = element_qualified.to_string();
    let result: Option<models::BusinessLogic> = db
        .query("SELECT * FROM business_logic WHERE element_qualified = $name")
        .bind(("name", name))
        .await?
        .take(0)?;
    Ok(result)
}

pub async fn update_business_logic(
    db: &Surreal<Db>,
    element_qualified: &str,
    description: &str,
    user_story_id: Option<&str>,
    feature_id: Option<&str>,
) -> Result<Option<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let name = element_qualified.to_string();
    let desc = description.to_string();
    let story = user_story_id.map(String::from);
    let feature = feature_id.map(String::from);
    let result: Option<models::BusinessLogic> = db
        .query("UPDATE business_logic SET description = $desc, user_story_id = $story, feature_id = $feature WHERE element_qualified = $name RETURN *")
        .bind(("name", name))
        .bind(("desc", desc))
        .bind(("story", story))
        .bind(("feature", feature))
        .await?
        .take(0)?;
    Ok(result)
}

pub async fn delete_business_logic(
    db: &Surreal<Db>,
    element_qualified: &str,
) -> Result<(), Box<dyn std::error::Error>> {
    let name = element_qualified.to_string();
    db.query("DELETE FROM business_logic WHERE element_qualified = $name")
        .bind(("name", name))
        .await?;
    Ok(())
}

pub async fn get_by_user_story(
    db: &Surreal<Db>,
    user_story_id: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let story = user_story_id.to_string();
    let result: Vec<models::BusinessLogic> = db
        .query("SELECT * FROM business_logic WHERE user_story_id = $story")
        .bind(("story", story))
        .await?
        .take(0)?;
    Ok(result)
}

pub async fn get_by_feature(
    db: &Surreal<Db>,
    feature_id: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let feature = feature_id.to_string();
    let result: Vec<models::BusinessLogic> = db
        .query("SELECT * FROM business_logic WHERE feature_id = $feature")
        .bind(("feature", feature))
        .await?
        .take(0)?;
    Ok(result)
}

pub async fn search_business_logic(
    db: &Surreal<Db>,
    query: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let q = format!("%{}%", query.to_lowercase());
    let result: Vec<models::BusinessLogic> = db
        .query("SELECT * FROM business_logic WHERE string::lowercase(description) LIKE $q")
        .bind(("q", q))
        .await?
        .take(0)?;
    Ok(result)
}

pub async fn all_business_logic(
    db: &Surreal<Db>,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    let result: Vec<models::BusinessLogic> =
        db.query("SELECT * FROM business_logic").await?.take(0)?;
    Ok(result)
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FeatureTraceEntry {
    pub element_qualified: String,
    pub description: String,
    pub user_story_id: Option<String>,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct FeatureTraceability {
    pub feature_id: String,
    pub code_elements: Vec<FeatureTraceEntry>,
    pub count: usize,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UserStoryTraceEntry {
    pub element_qualified: String,
    pub description: String,
    pub feature_id: Option<String>,
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct UserStoryTraceability {
    pub user_story_id: String,
    pub code_elements: Vec<UserStoryTraceEntry>,
    pub count: usize,
}

pub async fn get_feature_traceability(
    db: &Surreal<Db>,
    feature_id: &str,
) -> Result<FeatureTraceability, Box<dyn std::error::Error>> {
    let elements = get_by_feature(db, feature_id).await?;
    let code_elements: Vec<FeatureTraceEntry> = elements
        .into_iter()
        .map(|bl| FeatureTraceEntry {
            element_qualified: bl.element_qualified,
            description: bl.description,
            user_story_id: bl.user_story_id,
        })
        .collect();
    let count = code_elements.len();
    Ok(FeatureTraceability {
        feature_id: feature_id.to_string(),
        code_elements,
        count,
    })
}

pub async fn get_user_story_traceability(
    db: &Surreal<Db>,
    user_story_id: &str,
) -> Result<UserStoryTraceability, Box<dyn std::error::Error>> {
    let elements = get_by_user_story(db, user_story_id).await?;
    let code_elements: Vec<UserStoryTraceEntry> = elements
        .into_iter()
        .map(|bl| UserStoryTraceEntry {
            element_qualified: bl.element_qualified,
            description: bl.description,
            feature_id: bl.feature_id,
        })
        .collect();
    let count = code_elements.len();
    Ok(UserStoryTraceability {
        user_story_id: user_story_id.to_string(),
        code_elements,
        count,
    })
}

pub async fn all_feature_traceability(
    db: &Surreal<Db>,
) -> Result<Vec<FeatureTraceability>, Box<dyn std::error::Error>> {
    let all = all_business_logic(db).await?;
    let mut feature_map: std::collections::HashMap<String, Vec<FeatureTraceEntry>> =
        std::collections::HashMap::new();

    for bl in all {
        if let Some(ref fid) = bl.feature_id {
            let entry = FeatureTraceEntry {
                element_qualified: bl.element_qualified.clone(),
                description: bl.description.clone(),
                user_story_id: bl.user_story_id.clone(),
            };
            feature_map.entry(fid.clone()).or_default().push(entry);
        }
    }

    let traces: Vec<FeatureTraceability> = feature_map
        .into_iter()
        .map(|(feature_id, code_elements)| {
            let count = code_elements.len();
            FeatureTraceability {
                feature_id,
                code_elements,
                count,
            }
        })
        .collect();
    Ok(traces)
}

pub async fn all_user_story_traceability(
    db: &Surreal<Db>,
) -> Result<Vec<UserStoryTraceability>, Box<dyn std::error::Error>> {
    let all = all_business_logic(db).await?;
    let mut story_map: std::collections::HashMap<String, Vec<UserStoryTraceEntry>> =
        std::collections::HashMap::new();

    for bl in all {
        if let Some(ref sid) = bl.user_story_id {
            let entry = UserStoryTraceEntry {
                element_qualified: bl.element_qualified.clone(),
                description: bl.description.clone(),
                feature_id: bl.feature_id.clone(),
            };
            story_map.entry(sid.clone()).or_default().push(entry);
        }
    }

    let traces: Vec<UserStoryTraceability> = story_map
        .into_iter()
        .map(|(user_story_id, code_elements)| {
            let count = code_elements.len();
            UserStoryTraceability {
                user_story_id,
                code_elements,
                count,
            }
        })
        .collect();
    Ok(traces)
}

pub async fn find_by_business_domain(
    db: &Surreal<Db>,
    domain: &str,
) -> Result<Vec<models::BusinessLogic>, Box<dyn std::error::Error>> {
    search_business_logic(db, domain).await
}
