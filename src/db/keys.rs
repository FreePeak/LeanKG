#![allow(dead_code)]

use argon2::{
    password_hash::{rand_core::OsRng, PasswordHash, PasswordHasher, PasswordVerifier, SaltString},
    Argon2,
};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub type KeysDb = crate::db::backend::SharedDb;

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
    /// Optional injected backend (tests pin a scratch-schema PG backend so
    /// the shared `public` layout is never touched). `None` = open a fresh
    /// `init_db_pg()` per operation, exactly as before W8.
    db: Option<KeysDb>,
}

impl ApiKeyStore {
    pub fn new() -> Result<Self, Box<dyn std::error::Error>> {
        Ok(Self { db: None })
    }

    /// Inject a shared backend (W8 wave-1 test seam; production callers keep
    /// using [`Self::new`]).
    pub fn with_db(db: KeysDb) -> Self {
        Self { db: Some(db) }
    }

    pub fn init_db(&self) -> Result<KeysDb, Box<dyn std::error::Error>> {
        // Post-migration (Phase 8): api_keys lives in Postgres (schema.sql).
        // The legacy separate `keys.db` sqlite file and its embedded-backend
        // shim are gone; the table is created by the schema migrations.
        crate::db::backend::init_db_pg()
    }

    /// The backend for one store operation: injected handle when present,
    /// else a fresh per-call `init_db_pg()` (legacy behavior).
    fn db(&self) -> Result<KeysDb, Box<dyn std::error::Error>> {
        match &self.db {
            Some(db) => Ok(db.clone()),
            None => self.init_db(),
        }
    }

    pub fn create_key(&self, name: &str) -> Result<(String, ApiKey), Box<dyn std::error::Error>> {
        let db = self.db()?;

        let key = generate_api_key();
        let key_id = Uuid::new_v4().to_string();
        let key_hash = hash_api_key(&key)?;
        let created_at = chrono_timestamp();

        let api_key = ApiKey {
            id: key_id,
            name: name.to_string(),
            key_hash,
            created_at,
            last_used_at: None,
            revoked_at: None,
        };
        db.insert_api_key(&api_key)?;

        Ok((key, api_key))
    }

    pub fn list_keys(&self) -> Result<Vec<ApiKey>, Box<dyn std::error::Error>> {
        let db = self.db()?;

        let result = db.list_api_keys()?;

        let mut keys: std::collections::HashMap<String, ApiKey> = std::collections::HashMap::new();
        for row in result {
            let id = row.id;
            let name = row.name;
            let created_at = row.created_at;
            let last_used_at = row.last_used_at;
            let revoked_at: Option<String> = row.revoked_at;

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
                        // Never surface the argon2 hash through listings —
                        // same display contract as the legacy path.
                        key_hash: String::new(),
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
        let db = self.db()?;
        db.mark_api_key_revoked(id, &chrono_timestamp())
    }

    pub fn validate_key(&self, key: &str) -> Result<Option<String>, Box<dyn std::error::Error>> {
        let db = self.db()?;

        for (key_id, stored_hash) in db.list_active_api_key_hashes()? {
            if verify_api_key(key, &stored_hash) {
                // W8 deviation (documented in plan §6): the legacy Datalog
                // flow DELETEd + re-inserted the row with name="" and
                // created_at="" here, wiping the key's identity on every
                // validation. The SQL-first path updates only last_used_at.
                let _ = db.touch_api_key_last_used(&key_id, &chrono_timestamp());
                return Ok(Some(key_id));
            }
        }

        Ok(None)
    }
}

fn generate_api_key() -> String {
    let salt = SaltString::generate(&mut OsRng);
    let key_part = Uuid::new_v4().to_string().replace("-", "");
    format!("lkkg_{}_{}", key_part, &salt.as_str()[..8])
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hash_then_verify_roundtrips() {
        let key = generate_api_key();
        let hash = hash_api_key(&key).expect("hash");
        assert!(verify_api_key(&key, &hash), "correct key verifies");
        assert!(!verify_api_key("wrong", &hash), "wrong key rejected");
    }

    #[test]
    fn verify_rejects_malformed_hash() {
        assert!(!verify_api_key("k", "not-a-argon2-hash"));
    }

    #[test]
    fn generated_keys_have_lkkg_prefix() {
        let k = generate_api_key();
        assert!(k.starts_with("lkkg_"), "{k}");
        assert!(k.len() > 20);
    }
}

impl Default for ApiKeyStore {
    fn default() -> Self {
        Self::new().expect("Failed to create API key store")
    }
}
