use std::path::Path;
use surrealdb::engine::local::{Db, RocksDb};
use surrealdb::Surreal;

pub async fn init_db(db_path: &Path) -> Result<Surreal<Db>, Box<dyn std::error::Error>> {
    let db = Surreal::new::<RocksDb>(db_path).await?;
    db.use_ns("leankg").use_db("codebase").await?;

    db.query(
        "
        DEFINE TABLE code_elements SCHEMAFULL;
        DEFINE FIELD qualified_name ON code_elements TYPE string;
        DEFINE FIELD element_type ON code_elements TYPE string;
        DEFINE FIELD name ON code_elements TYPE string;
        DEFINE FIELD file_path ON code_elements TYPE string;
        DEFINE FIELD line_start ON code_elements TYPE int;
        DEFINE FIELD line_end ON code_elements TYPE int;
        DEFINE FIELD language ON code_elements TYPE string;
        DEFINE FIELD parent_qualified ON code_elements TYPE option<string>;
        DEFINE FIELD metadata ON code_elements TYPE object;
        DEFINE INDEX qualified_name ON code_elements COLUMNS qualified_name UNIQUE;
    ",
    )
    .await?;

    db.query(
        "
        DEFINE TABLE relationships SCHEMAFULL;
        DEFINE FIELD source_qualified ON relationships TYPE string;
        DEFINE FIELD target_qualified ON relationships TYPE string;
        DEFINE FIELD rel_type ON relationships TYPE string;
        DEFINE FIELD metadata ON relationships TYPE object;
        DEFINE INDEX source ON relationships COLUMNS source_qualified;
        DEFINE INDEX target ON relationships COLUMNS target_qualified;
    ",
    )
    .await?;

    db.query(
        "
        DEFINE TABLE business_logic SCHEMAFULL;
        DEFINE FIELD element_qualified ON business_logic TYPE string;
        DEFINE FIELD description ON business_logic TYPE string;
        DEFINE FIELD user_story_id ON business_logic TYPE option<string>;
        DEFINE FIELD feature_id ON business_logic TYPE option<string>;
        DEFINE INDEX element ON business_logic COLUMNS element_qualified;
        DEFINE INDEX user_story ON business_logic COLUMNS user_story_id;
        DEFINE INDEX feature ON business_logic COLUMNS feature_id;
    ",
    )
    .await?;

    Ok(db)
}
