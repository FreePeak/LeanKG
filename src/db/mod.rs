pub mod models;
pub mod schema;

pub use models::*;
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
        .query("SELECT * FROM business_logic WHERE string::lowercase(description) CONTAINS $q")
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
