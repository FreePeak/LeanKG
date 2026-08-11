//! Accounts / orgs / memberships / resource-ownership stores.
//!
//! Registration creates an account + a bootstrap org owned by that account.
//! Permission checks are role-based (`owner` > `admin` > `member` > `viewer`),
//! mirroring the MCP `Role` hierarchy.

use crate::auth::tokens::now_epoch;
use crate::db::backend::SharedDb;
use argon2::{
    password_hash::{rand_core::OsRng, PasswordHash, PasswordHasher, PasswordVerifier, SaltString},
    Argon2,
};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Account {
    pub id: String,
    pub email: String,
    pub name: String,
    pub password_hash: String,
    pub status: String,
    pub created_at: i64,
    pub updated_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Org {
    pub id: String,
    pub name: String,
    pub owner_account_id: String,
    pub created_at: i64,
    pub updated_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct OrgMember {
    pub org_id: String,
    pub account_id: String,
    pub role: String,
    pub joined_at: i64,
}

/// Account + org store bound to a shared DB handle.
#[derive(Clone)]
pub struct AccountStore {
    db: SharedDb,
}

impl AccountStore {
    pub fn new(db: SharedDb) -> Self {
        Self { db }
    }

    /// Register an account, creating a bootstrap org owned by it. Idempotent
    /// on duplicate email (returns the existing account error).
    pub fn register(
        &self,
        email: &str,
        password: &str,
        name: &str,
    ) -> Result<Account, Box<dyn std::error::Error>> {
        let email = email.trim().to_ascii_lowercase();
        if !email.contains('@') {
            return Err("invalid email".into());
        }
        if password.len() < 8 {
            return Err("password must be at least 8 characters".into());
        }
        if self.account_by_email(&email)?.is_some() {
            return Err(format!("account already exists: {email}").into());
        }
        let now = now_epoch();
        let account = Account {
            id: Uuid::new_v4().to_string(),
            email,
            name: name.to_string(),
            password_hash: hash_password(password)?,
            status: "active".to_string(),
            created_at: now,
            updated_at: now,
        };
        self.upsert_account(&account)?;
        // Bootstrap org owned by the account.
        let org = Org {
            id: Uuid::new_v4().to_string(),
            name: format!("{}'s org", name),
            owner_account_id: account.id.clone(),
            created_at: now,
            updated_at: now,
        };
        self.upsert_org(&org)?;
        self.upsert_member(&OrgMember {
            org_id: org.id,
            account_id: account.id.clone(),
            role: "owner".into(),
            joined_at: now,
        })?;
        Ok(account)
    }

    pub fn verify_login(
        &self,
        email: &str,
        password: &str,
    ) -> Result<Account, Box<dyn std::error::Error>> {
        let account = self
            .account_by_email(&email.trim().to_ascii_lowercase())?
            .ok_or_else(|| format!("no account for {email}"))?;
        if !verify_password(password, &account.password_hash) {
            return Err("invalid password".into());
        }
        Ok(account)
    }

    pub fn create_org(
        &self,
        name: &str,
        owner_account_id: &str,
    ) -> Result<Org, Box<dyn std::error::Error>> {
        let now = now_epoch();
        let org = Org {
            id: Uuid::new_v4().to_string(),
            name: name.to_string(),
            owner_account_id: owner_account_id.to_string(),
            created_at: now,
            updated_at: now,
        };
        self.upsert_org(&org)?;
        self.upsert_member(&OrgMember {
            org_id: org.id.clone(),
            account_id: owner_account_id.to_string(),
            role: "owner".into(),
            joined_at: now,
        })?;
        Ok(org)
    }

    pub fn add_member(
        &self,
        org_id: &str,
        account_id: &str,
        role: &str,
    ) -> Result<OrgMember, Box<dyn std::error::Error>> {
        let valid = matches!(role, "owner" | "admin" | "member" | "viewer");
        if !valid {
            return Err(format!("invalid role {role:?}").into());
        }
        let member = OrgMember {
            org_id: org_id.to_string(),
            account_id: account_id.to_string(),
            role: role.to_string(),
            joined_at: now_epoch(),
        };
        self.upsert_member(&member)?;
        Ok(member)
    }

    /// True when `account_id` holds the given (or higher) org role.
    pub fn org_role_sufficient(
        &self,
        org_id: &str,
        account_id: &str,
        required: &str,
    ) -> Result<bool, Box<dyn std::error::Error>> {
        let Some(member) = self.member_in_org(org_id, account_id)? else {
            return Ok(false);
        };
        Ok(role_sufficient(&member.role, required))
    }

    pub fn member_in_org(
        &self,
        org_id: &str,
        account_id: &str,
    ) -> Result<Option<OrgMember>, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("org_id".into(), serde_json::json!(org_id));
        params.insert("account_id".into(), serde_json::json!(account_id));
        let result = self.db.run_script(
            r#"?[org_id, account_id, role, joined_at] := *org_memberships[org_id, account_id, role, joined_at], org_id = $org_id, account_id = $account_id"#,
            params,
        )?;
        Ok(result.rows.first().and_then(|r| {
            Some(OrgMember {
                org_id: r[0].get_str()?.to_string(),
                account_id: r[1].get_str()?.to_string(),
                role: r[2].get_str()?.to_string(),
                joined_at: r[3].get_int()?,
            })
        }))
    }

    pub fn list_org_members(
        &self,
        org_id: &str,
    ) -> Result<Vec<OrgMember>, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("org_id".into(), serde_json::json!(org_id));
        let result = self.db.run_script(
            r#"?[org_id, account_id, role, joined_at] := *org_memberships[org_id, account_id, role, joined_at], org_id = $org_id"#,
            params,
        )?;
        Ok(result
            .rows
            .iter()
            .filter_map(|r| {
                Some(OrgMember {
                    org_id: r[0].get_str()?.to_string(),
                    account_id: r[1].get_str()?.to_string(),
                    role: r[2].get_str()?.to_string(),
                    joined_at: r[3].get_int()?,
                })
            })
            .collect())
    }

    /// Record resource ownership (owner the resources).
    pub fn claim_resource(
        &self,
        resource_type: &str,
        resource_id: &str,
        owner_account_id: &str,
        org_id: Option<&str>,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("resource_type".into(), serde_json::json!(resource_type));
        params.insert("resource_id".into(), serde_json::json!(resource_id));
        params.insert(
            "owner_account_id".into(),
            serde_json::json!(owner_account_id),
        );
        params.insert("org_id".into(), serde_json::json!(org_id.unwrap_or("")));
        params.insert("created_at".into(), serde_json::json!(now_epoch()));
        let query = r#"?[resource_type, resource_id, owner_account_id, org_id, created_at] <- [[$resource_type, $resource_id, $owner_account_id, $org_id, $created_at]]
            :put resource_ownership { resource_type, resource_id, owner_account_id, org_id, created_at }"#;
        self.db.run_script(query, params)?;
        Ok(())
    }

    pub fn is_resource_owner(
        &self,
        resource_type: &str,
        resource_id: &str,
        account_id: &str,
    ) -> Result<bool, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("resource_type".into(), serde_json::json!(resource_type));
        params.insert("resource_id".into(), serde_json::json!(resource_id));
        params.insert("owner_account_id".into(), serde_json::json!(account_id));
        let result = self.db.run_script(
            r#"?[resource_type, resource_id] := *resource_ownership[resource_type, resource_id, owner_account_id, org_id, created_at], resource_type = $resource_type, resource_id = $resource_id, owner_account_id = $owner_account_id"#,
            params,
        )?;
        Ok(!result.rows.is_empty())
    }

    fn account_by_email(&self, email: &str) -> Result<Option<Account>, Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("email".into(), serde_json::json!(email));
        let result = self.db.run_script(
            r#"?[id, email, name, password_hash, status, created_at, updated_at] := *accounts[id, email, name, password_hash, status, created_at, updated_at], email = $email"#,
            params,
        )?;
        Ok(result.rows.first().and_then(|r| {
            Some(Account {
                id: r[0].get_str()?.to_string(),
                email: r[1].get_str()?.to_string(),
                name: r[2].get_str()?.to_string(),
                password_hash: r[3].get_str()?.to_string(),
                status: r[4].get_str()?.to_string(),
                created_at: r[5].get_int()?,
                updated_at: r[6].get_int()?,
            })
        }))
    }

    fn upsert_account(&self, a: &Account) -> Result<(), Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("id".into(), serde_json::json!(a.id));
        params.insert("email".into(), serde_json::json!(a.email));
        params.insert("name".into(), serde_json::json!(a.name));
        params.insert("password_hash".into(), serde_json::json!(a.password_hash));
        params.insert("status".into(), serde_json::json!(a.status));
        params.insert("created_at".into(), serde_json::json!(a.created_at));
        params.insert("updated_at".into(), serde_json::json!(a.updated_at));
        let query = r#"?[id, email, name, password_hash, status, created_at, updated_at] <- [[$id, $email, $name, $password_hash, $status, $created_at, $updated_at]]
            :put accounts { id, email, name, password_hash, status, created_at, updated_at }"#;
        self.db.run_script(query, params)?;
        Ok(())
    }

    fn upsert_org(&self, o: &Org) -> Result<(), Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("id".into(), serde_json::json!(o.id));
        params.insert("name".into(), serde_json::json!(o.name));
        params.insert(
            "owner_account_id".into(),
            serde_json::json!(o.owner_account_id),
        );
        params.insert("created_at".into(), serde_json::json!(o.created_at));
        params.insert("updated_at".into(), serde_json::json!(o.updated_at));
        let query = r#"?[id, name, owner_account_id, created_at, updated_at] <- [[$id, $name, $owner_account_id, $created_at, $updated_at]]
            :put orgs { id, name, owner_account_id, created_at, updated_at }"#;
        self.db.run_script(query, params)?;
        Ok(())
    }

    fn upsert_member(&self, m: &OrgMember) -> Result<(), Box<dyn std::error::Error>> {
        let mut params = BTreeMap::new();
        params.insert("org_id".into(), serde_json::json!(m.org_id));
        params.insert("account_id".into(), serde_json::json!(m.account_id));
        params.insert("role".into(), serde_json::json!(m.role));
        params.insert("joined_at".into(), serde_json::json!(m.joined_at));
        let query = r#"?[org_id, account_id, role, joined_at] <- [[$org_id, $account_id, $role, $joined_at]]
            :put org_memberships { org_id, account_id, role, joined_at }"#;
        self.db.run_script(query, params)?;
        Ok(())
    }
}

pub fn hash_password(password: &str) -> Result<String, String> {
    let salt = SaltString::generate(&mut OsRng);
    let argon2 = Argon2::default();
    argon2
        .hash_password(password.as_bytes(), &salt)
        .map(|h| h.to_string())
        .map_err(|e| e.to_string())
}

pub fn verify_password(password: &str, hash: &str) -> bool {
    let Ok(parsed) = PasswordHash::new(hash) else {
        return false;
    };
    Argon2::default()
        .verify_password(password.as_bytes(), &parsed)
        .is_ok()
}

fn role_sufficient(actual: &str, required: &str) -> bool {
    let level = |r: &str| match r {
        "owner" => 4,
        "admin" => 3,
        "member" => 2,
        "viewer" => 1,
        _ => 0,
    };
    level(actual) >= level(required)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn role_sufficient_hierarchy() {
        assert!(role_sufficient("owner", "viewer"));
        assert!(role_sufficient("owner", "owner"));
        assert!(role_sufficient("admin", "member"));
        assert!(role_sufficient("member", "viewer"));
        assert!(!role_sufficient("viewer", "member"));
        assert!(!role_sufficient("member", "admin"));
        assert!(!role_sufficient("bogus", "viewer"));
    }

    #[test]
    fn password_hash_roundtrip() {
        let hash = hash_password("correct horse").expect("hash");
        assert!(verify_password("correct horse", &hash));
        assert!(!verify_password("wrong", &hash));
    }

    #[test]
    fn register_creates_account_and_bootstrap_org() {
        let db = crate::db::fake::FakeBackend::new();
        let store = AccountStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb);
        let account = store
            .register("a@example.com", "password123", "Alice")
            .expect("register");
        assert_eq!(account.email, "a@example.com");
        assert_eq!(account.status, "active");
        assert!(verify_password("password123", &account.password_hash));

        // Bootstrap org owned by the account, owner membership present.
        let members = store
            .list_org_members(&org_id_of(&store, &account.id))
            .unwrap();
        assert_eq!(members.len(), 1);
        assert_eq!(members[0].account_id, account.id);
        assert_eq!(members[0].role, "owner");
    }

    #[test]
    fn register_rejects_duplicate_email_and_short_password() {
        let db = crate::db::fake::FakeBackend::new();
        let store = AccountStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb);
        store
            .register("dup@example.com", "password123", "Dup")
            .expect("first register");
        assert!(
            store
                .register("dup@example.com", "password123", "Dup2")
                .is_err(),
            "duplicate email must fail"
        );
        assert!(
            store.register("new@example.com", "short", "New").is_err(),
            "short password must fail"
        );
    }

    #[test]
    fn verify_login_roundtrip() {
        let db = crate::db::fake::FakeBackend::new();
        let store = AccountStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb);
        store
            .register("login@example.com", "password123", "Log")
            .expect("register");
        let account = store
            .verify_login("LOGIN@example.com", "password123")
            .expect("login (case-insensitive email)");
        assert_eq!(account.email, "login@example.com");
        assert!(
            store.verify_login("login@example.com", "wrong").is_err(),
            "wrong password must fail"
        );
    }

    #[test]
    fn org_role_sufficient_checks_hierarchy() {
        let db = crate::db::fake::FakeBackend::new();
        let store = AccountStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb);
        let owner = store
            .register("owner@example.com", "password123", "Owner")
            .expect("register");
        let org_id = org_id_of(&store, &owner.id);

        let member = store
            .register("member@example.com", "password123", "Member")
            .expect("register");
        store
            .add_member(&org_id, &member.id, "member")
            .expect("add member");

        assert!(store
            .org_role_sufficient(&org_id, &owner.id, "admin")
            .unwrap());
        assert!(!store
            .org_role_sufficient(&org_id, &member.id, "admin")
            .unwrap());
        assert!(store
            .org_role_sufficient(&org_id, &member.id, "viewer")
            .unwrap());
        assert!(!store
            .org_role_sufficient(&org_id, "nobody", "viewer")
            .unwrap());
    }

    #[test]
    fn resource_ownership_claim_and_check() {
        let db = crate::db::fake::FakeBackend::new();
        let store = AccountStore::new(std::sync::Arc::new(db) as crate::db::backend::SharedDb);
        let account = store
            .register("owner2@example.com", "password123", "Owner2")
            .expect("register");
        store
            .claim_resource("knowledge", "entry-1", &account.id, None)
            .expect("claim");
        assert!(store
            .is_resource_owner("knowledge", "entry-1", &account.id)
            .unwrap());
        assert!(!store
            .is_resource_owner("knowledge", "entry-2", &account.id)
            .unwrap());
    }

    /// Find the bootstrap org id for an account (first membership row).
    fn org_id_of(store: &AccountStore, account_id: &str) -> String {
        // Reuse list_org_members over every org is awkward on the fake; the
        // register flow creates exactly one org. Query orgs by owner.
        let result = store
            .db
            .run_script(
                "?[id] := *orgs[id, name, owner_account_id, created_at, updated_at], owner_account_id = $oid",
                std::collections::BTreeMap::from([(
                    "oid".to_string(),
                    serde_json::json!(account_id),
                )]),
            )
            .expect("find org");
        result
            .rows
            .first()
            .and_then(|r| r.first().and_then(|v| v.get_str().map(String::from)))
            .expect("bootstrap org")
    }
}
