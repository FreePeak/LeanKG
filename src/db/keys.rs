#![allow(dead_code)]
use argon2::{
    password_hash::{rand_core::OsRng, PasswordHash, PasswordHasher, PasswordVerifier, SaltString},
    Argon2,
};
use cozo::{Db, SqliteStorage};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use uuid::Uuid;

pub type KeysDb = Db<SqliteStorage>;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ApiKey {
    pub id: String,
    pub name: String,
    pub key_hash: String,
    pub created_at: String,
    pub last_used_at: Option<String>,
    pub revoked_at: Option<String>,
}

pub struct ApiKeyStore {
    db_path: std::path::PathBuf,
}

impl ApiKeyStore {
    pub fn new() -> Result<Self, Box<dyn std::error::Error>> {
        let home = dirs::home_dir().ok_or("Cannot find home directory")?;
        let keys_dir = home.join(".leankg");
        std::fs::create_dir_all(&keys_dir)?;
        let db_path = keys_dir.join("keys.db");
        Ok(Self { db_path })
    }

    pub fn init_db(&self) -> Result<KeysDb, Box<dyn std::error::Error>> {
        let path_str = self.db_path.to_string_lossy().to_string();
        let db = cozo::new_cozo_sqlite(path_str)?;

        let check_relations = r#"::relations"#;
        let relations_result = db.run_script(check_relations, Default::default())?;
        let existing_relations: std::collections::HashSet<String> = relations_result
            .rows
            .iter()
            .filter_map(|row| row.get(0).and_then(|v| v.as_str().map(String::from)))
            .collect();

        if !existing_relations.contains("api_keys") {
            let create_table = r#":create api_keys {id: String, name: String, key_hash: String, created_at: String, last_used_at: String?, revoked_at: String?}"#;
            db.run_script(create_table, Default::default())?;
        }

        Ok(db)
    }

    pub fn create_key(&self, name: &str) -> Result<(String, ApiKey), Box<dyn std::error::Error>> {
        let db = self.init_db()?;

        let key = generate_api_key();
        let key_id = Uuid::new_v4().to_string();
        let key_hash = hash_api_key(&key)?;
        let created_at = chrono_timestamp();

        let mut params = BTreeMap::new();
        params.insert("id".to_string(), serde_json::json!(key_id));
        params.insert("name".to_string(), serde_json::json!(name));
        params.insert("key_hash".to_string(), serde_json::json!(key_hash));
        params.insert("created_at".to_string(), serde_json::json!(created_at));
        params.insert(
            "last_used_at".to_string(),
            serde_json::json!(serde_json::Value::Null),
        );
        params.insert(
            "revoked_at".to_string(),
            serde_json::json!(serde_json::Value::Null),
        );

        let insert = r#"
        ?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]]
        :put api_keys { id, name, key_hash, created_at, last_used_at, revoked_at }
        "#;

        db.run_script(insert, params)?;

        let api_key = ApiKey {
            id: key_id,
            name: name.to_string(),
            key_hash,
            created_at,
            last_used_at: None,
            revoked_at: None,
        };

        Ok((key, api_key))
    }

    pub fn list_keys(&self) -> Result<Vec<ApiKey>, Box<dyn std::error::Error>> {
        let db = self.init_db()?;

        let query = r#"
        ?[id, name, key_hash, created_at, last_used_at, revoked_at] := *api_keys[id, name, key_hash, created_at, last_used_at, revoked_at]
        "#;

        let result = db.run_script(query, BTreeMap::new())?;

        let mut keys: std::collections::HashMap<String, ApiKey> = std::collections::HashMap::new();
        for row in result.rows {
            let id = row[0].as_str().unwrap_or("").to_string();
            let name = row[1].as_str().unwrap_or("").to_string();
            let key_hash = String::new();
            let created_at = row[3].as_str().unwrap_or("").to_string();
            let last_used_at = row[4].as_str().map(String::from);
            let revoked_at: Option<String> = row[5].as_str().map(String::from);

            if revoked_at.is_some() {
                keys.remove(&id);
                continue;
            }

            if !keys.contains_key(&id) {
                keys.insert(
                    id.clone(),
                    ApiKey {
                        id,
                        name,
                        key_hash,
                        created_at,
                        last_used_at,
                        revoked_at,
                    },
                );
            }
        }

        Ok(keys.into_values().collect())
    }

    pub fn revoke_key(&self, id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        let db = self.init_db()?;

        let query = r#"
        ?[id, name, key_hash, created_at, last_used_at, revoked_at] := *api_keys[id, name, key_hash, created_at, last_used_at, revoked_at], id = $id
        "#;

        let mut params = BTreeMap::new();
        params.insert("id".to_string(), serde_json::json!(id));

        let result = db.run_script(query, params)?;

        if result.rows.is_empty() {
            return Ok(false);
        }

        let row = &result.rows[0];

        let revoked_at_val = row[5].as_str();
        if revoked_at_val.is_some() {
            return Ok(false);
        }

        let revoked_at = chrono_timestamp();

        let update = r#"
        ?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]]
        :put api_keys { id, name, key_hash, created_at, last_used_at, revoked_at }
        "#;

        let mut params = BTreeMap::new();
        params.insert(
            "id".to_string(),
            serde_json::json!(row[0].as_str().unwrap_or("")),
        );
        params.insert(
            "name".to_string(),
            serde_json::json!(row[1].as_str().unwrap_or("")),
        );
        params.insert(
            "key_hash".to_string(),
            serde_json::json!(row[2].as_str().unwrap_or("")),
        );
        params.insert(
            "created_at".to_string(),
            serde_json::json!(row[3].as_str().unwrap_or("")),
        );
        params.insert(
            "last_used_at".to_string(),
            serde_json::json!(row[4].as_str().map(String::from)),
        );
        params.insert("revoked_at".to_string(), serde_json::json!(revoked_at));

        db.run_script(update, params)?;

        Ok(true)
    }

    pub fn validate_key(&self, key: &str) -> Result<Option<String>, Box<dyn std::error::Error>> {
        let db = self.init_db()?;

        let query = r#"
        ?[id, key_hash] := *api_keys[id, key_hash], revoked_at = null
        "#;

        let result = db.run_script(query, BTreeMap::new())?;

        for row in result.rows {
            let key_id = row[0].as_str().unwrap_or("").to_string();
            let stored_hash = row[1].as_str().unwrap_or("");
            if verify_api_key(key, stored_hash) {
                let last_used = chrono_timestamp();

                let delete = format!(r#":delete api_keys where id = "{}""#, key_id);
                let _ = db.run_script(&delete, BTreeMap::new());

                let insert = r#"
                ?[id, name, key_hash, created_at, last_used_at, revoked_at] <- [[$id, $name, $key_hash, $created_at, $last_used_at, $revoked_at]]
                :put api_keys { id, name, key_hash, created_at, last_used_at, revoked_at }
                "#;

                let mut params = BTreeMap::new();
                params.insert("id".to_string(), serde_json::json!(key_id.clone()));
                params.insert("name".to_string(), serde_json::json!(""));
                params.insert("key_hash".to_string(), serde_json::json!(stored_hash));
                params.insert("created_at".to_string(), serde_json::json!(""));
                params.insert("last_used_at".to_string(), serde_json::json!(last_used));
                params.insert(
                    "revoked_at".to_string(),
                    serde_json::json!(serde_json::Value::Null),
                );
                let _ = db.run_script(insert, params);

                return Ok(Some(key_id));
            }
        }

        Ok(None)
    }
}

fn generate_api_key() -> String {
    let salt = SaltString::generate(&mut OsRng);
    let key_part = Uuid::new_v4().to_string().replace("-", "");
    format!("lkkg_{}_{}", key_part, salt.as_str()[..8].to_string())
}

fn hash_api_key(key: &str) -> Result<String, String> {
    let salt = SaltString::generate(&mut OsRng);
    let argon2 = Argon2::default();
    let hash = argon2
        .hash_password(key.as_bytes(), &salt)
        .map_err(|e| e.to_string())?
        .to_string();
    Ok(hash)
}

fn verify_api_key(key: &str, hash: &str) -> bool {
    let parsed_hash = match PasswordHash::new(hash) {
        Ok(h) => h,
        Err(_) => return false,
    };
    Argon2::default()
        .verify_password(key.as_bytes(), &parsed_hash)
        .is_ok()
}

fn chrono_timestamp() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let duration = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    format!("{}", duration.as_secs())
}

impl Default for ApiKeyStore {
    fn default() -> Self {
        Self::new().expect("Failed to create API key store")
    }
}
