//! DB-backed opaque access tokens (OAuth2 client-credentials spirit).
//!
//! The server generates an opaque token, stores only its SHA-256 hash, and
//! validation resolves a Bearer token to an [`AuthContext`] (account + role).
//! Tokens are revocable and may carry an expiry.

use crate::db::backend::SharedDb;
use crate::db::models::{AuthContext, Role};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AccessToken {
    pub id: String,
    pub account_id: String,
    pub org_id: Option<String>,
    pub token_hash: String,
    pub name: String,
    pub role: String,
    pub expires_at: Option<i64>,
    pub created_at: i64,
    pub revoked_at: Option<i64>,
    pub last_used_at: Option<i64>,
}

/// Token store bound to a shared DB handle (reuses the Postgres pool).
#[derive(Clone)]
pub struct AccessTokenStore {
    db: SharedDb,
}

impl AccessTokenStore {
    pub fn new(db: SharedDb) -> Self {
        Self { db }
    }

    /// Issue a new opaque token for an account. Returns the plaintext token
    /// (shown once) and the stored row (hash only).
    pub fn create_access_token(
        &self,
        account_id: &str,
        org_id: Option<&str>,
        role: Role,
        name: &str,
        ttl_secs: Option<i64>,
    ) -> Result<(String, AccessToken), Box<dyn std::error::Error>> {
        let token = generate_token();
        let token_hash = hash_token(&token);
        let now = now_epoch();
        let expires_at = ttl_secs.map(|t| now + t);
        let row = AccessToken {
            id: Uuid::new_v4().to_string(),
            account_id: account_id.to_string(),
            org_id: org_id.map(String::from),
            token_hash: token_hash.clone(),
            name: name.to_string(),
            role: role.as_str().to_string(),
            expires_at,
            created_at: now,
            revoked_at: None,
            last_used_at: None,
        };
        let mut params = BTreeMap::new();
        params.insert("id".into(), serde_json::json!(row.id));
        params.insert("account_id".into(), serde_json::json!(row.account_id));
        params.insert(
            "org_id".into(),
            serde_json::json!(row.org_id.clone().unwrap_or_default()),
        );
        params.insert("token_hash".into(), serde_json::json!(token_hash));
        params.insert("name".into(), serde_json::json!(row.name));
        params.insert("role".into(), serde_json::json!(row.role));
        params.insert("scopes".into(), serde_json::json!(Vec::<String>::new()));
        params.insert("expires_at".into(), serde_json::json!(row.expires_at));
        params.insert("created_at".into(), serde_json::json!(row.created_at));
        params.insert("revoked_at".into(), serde_json::json!(row.revoked_at));
        params.insert("last_used_at".into(), serde_json::json!(row.last_used_at));

        let query = r#"?[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at]
            <- [[$id, $account_id, $org_id, $token_hash, $name, $role, $scopes, $expires_at, $created_at, $revoked_at, $last_used_at]]
            :put access_tokens { id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at }"#;
        self.db.run_script(query, params)?;
        Ok((token, row))
    }

    /// Validate an opaque token against the store. Returns the resolved
    /// [`AuthContext`] or an error (unknown/revoked/expired token).
    pub fn validate_token(&self, token: &str) -> Result<AuthContext, Box<dyn std::error::Error>> {
        let token_hash = hash_token(token);
        let mut params = BTreeMap::new();
        params.insert("token_hash".into(), serde_json::json!(token_hash));
        let result = self.db.run_script(
            r#"?[id, account_id, org_id, role, revoked_at, expires_at] := *access_tokens[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at], token_hash = $token_hash"#,
            params,
        )?;
        let row = result
            .rows
            .first()
            .ok_or_else(|| "invalid access token".to_string())?;
        let account_id = row[1].get_str().unwrap_or_default().to_string();
        let role_str = row[3].get_str().unwrap_or_default().to_string();
        let revoked_at = row[4].get_int();
        let expires_at = row[5].get_int();
        if revoked_at.is_some() {
            return Err("access token revoked".into());
        }
        if let Some(exp) = expires_at {
            if exp < now_epoch() {
                return Err("access token expired".into());
            }
        }
        let role = Role::from_str(&role_str).ok_or_else(|| format!("unknown role {role_str:?}"))?;
        // Touch last_used_at (best-effort).
        let _ = self.touch_last_used(token_hash.as_str());
        Ok(AuthContext {
            client_id: account_id,
            role,
        })
    }

    pub fn revoke_token(&self, id: &str) -> Result<bool, Box<dyn std::error::Error>> {
        // Load the row, stamp revoked_at, re-put (upsert by PK). Mirrors the
        // ApiKeyStore revoke pattern (read → modify → :put), which both the
        // fake backend and the PG translator support.
        let Some(row) = self.token_by_id(id)? else {
            return Ok(false);
        };
        if row.revoked_at.is_some() {
            return Ok(false);
        }
        let mut params = BTreeMap::new();
        params.insert("id".into(), serde_json::json!(row.id));
        params.insert("account_id".into(), serde_json::json!(row.account_id));
        params.insert(
            "org_id".into(),
            serde_json::json!(row.org_id.clone().unwrap_or_default()),
        );
        params.insert("token_hash".into(), serde_json::json!(row.token_hash));
        params.insert("name".into(), serde_json::json!(row.name));
        params.insert("role".into(), serde_json::json!(row.role));
        params.insert("scopes".into(), serde_json::json!(Vec::<String>::new()));
        params.insert("expires_at".into(), serde_json::json!(row.expires_at));
        params.insert("created_at".into(), serde_json::json!(row.created_at));
        params.insert("revoked_at".into(), serde_json::json!(Some(now_epoch())));
        params.insert("last_used_at".into(), serde_json::json!(row.last_used_at));
        let query = r#"?[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at] <- [[$id, $account_id, $org_id, $token_hash, $name, $role, $scopes, $expires_at, $created_at, $revoked_at, $last_used_at]]
            :put access_tokens { id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at }"#;
        self.db.run_script(query, params)?;
        Ok(true)
    }

    fn token_by_id(&self, id: &str) -> Result<Option<AccessToken>, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("id".into(), serde_json::json!(id));
        let result = self.db.run_script(
            r#"?[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at] := *access_tokens[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at], id = $id"#,
            params,
        )?;
        Ok(result.rows.first().and_then(|r| row_to_token(r)))
    }

    pub fn list_tokens(
        &self,
        account_id: &str,
    ) -> Result<Vec<AccessToken>, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("account_id".into(), serde_json::json!(account_id));
        let result = self.db.run_script(
            r#"?[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at] := *access_tokens[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at], account_id = $account_id"#,
            params,
        )?;
        Ok(result.rows.iter().filter_map(|r| row_to_token(r)).collect())
    }

    fn touch_last_used(&self, token_hash: &str) -> Result<(), Box<dyn std::error::Error>> {
        let now = now_epoch();
        let mut params = BTreeMap::new();
        params.insert("token_hash".into(), serde_json::json!(token_hash));
        params.insert("last_used_at".into(), serde_json::json!(now));
        let query = r#"?[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at] := *access_tokens[id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at], token_hash = $token_hash
            :update access_tokens { id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at }
            :put access_tokens { id, account_id, org_id, token_hash, name, role, scopes, expires_at, created_at, revoked_at, last_used_at }"#;
        let _ = self.db.run_script(query, params)?;
        Ok(())
    }
}

fn row_to_token(row: &[crate::db::backend::DataValue]) -> Option<AccessToken> {
    Some(AccessToken {
        id: row[0].get_str()?.to_string(),
        account_id: row[1].get_str()?.to_string(),
        org_id: row[2].get_str().map(String::from),
        token_hash: row[3].get_str()?.to_string(),
        name: row[4].get_str()?.to_string(),
        role: row[5].get_str()?.to_string(),
        expires_at: row[7].get_int(),
        created_at: row[8].get_int()?,
        revoked_at: row[9].get_int(),
        last_used_at: row[10].get_int(),
    })
}

/// Generate an opaque token `lkg_{uuid}_{salt8}` (mirrors `ApiKeyStore`).
pub fn generate_token() -> String {
    let key_part = Uuid::new_v4().to_string().replace("-", "");
    let salt = Uuid::new_v4().to_string().replace("-", "");
    format!("lkg_{}_{}", key_part, &salt[..8])
}

/// SHA-256 hex of an opaque token — the only form persisted.
pub fn hash_token(token: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(token.as_bytes());
    format!("{:x}", hasher.finalize())
}

pub fn now_epoch() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn store() -> AccessTokenStore {
        let db = crate::db::fake::FakeBackend::new();
        AccessTokenStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb)
    }

    #[test]
    fn hash_token_is_sha256_hex() {
        let h = hash_token("secret");
        assert_eq!(h.len(), 64);
        assert_eq!(h, hash_token("secret"));
        assert_ne!(h, hash_token("other"));
    }

    #[test]
    fn generate_token_is_ophstrue_prefix_unique() {
        let a = generate_token();
        let b = generate_token();
        assert!(a.starts_with("lkg_"));
        assert_ne!(a, b);
    }

    #[test]
    fn create_and_validate_roundtrip() {
        let s = store();
        let (token, row) = s
            .create_access_token("acct-1", None, Role::Admin, "ci", None)
            .expect("issue");
        assert_eq!(row.account_id, "acct-1");
        assert_eq!(row.role, "admin");
        let ctx = s.validate_token(&token).expect("validate");
        assert_eq!(ctx.client_id, "acct-1");
        assert_eq!(ctx.role, Role::Admin);
    }

    #[test]
    fn validate_rejects_unknown_and_revoked() {
        let s = store();
        let (token, row) = s
            .create_access_token("acct-2", None, Role::Viewer, "tmp", None)
            .expect("issue");
        assert!(s.validate_token("nope").is_err(), "unknown token fails");
        s.revoke_token(&row.id).expect("revoke");
        assert!(s.validate_token(&token).is_err(), "revoked token fails");
    }

    #[test]
    fn validate_rejects_expired_token() {
        let s = store();
        // Negative TTL → already expired.
        let (token, _) = s
            .create_access_token("acct-3", None, Role::Viewer, "exp", Some(-60))
            .expect("issue");
        assert!(s.validate_token(&token).is_err(), "expired token fails");
    }

    #[test]
    fn list_tokens_filters_by_account() {
        let s = store();
        let (_, _) = s
            .create_access_token("acct-a", None, Role::Admin, "a1", None)
            .expect("issue");
        let (_, _) = s
            .create_access_token("acct-a", None, Role::Viewer, "a2", None)
            .expect("issue");
        let (_, _) = s
            .create_access_token("acct-b", None, Role::Viewer, "b1", None)
            .expect("issue");
        let for_a = s.list_tokens("acct-a").expect("list");
        assert_eq!(for_a.len(), 2);
        let for_b = s.list_tokens("acct-b").expect("list");
        assert_eq!(for_b.len(), 1);
    }
}
