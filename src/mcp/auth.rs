#![allow(dead_code)]
use crate::db::models::{AuthContext, Role};
use sha2::{Digest, Sha256};
use std::collections::HashMap;

// ========================================================================
// AuthManager - Main authentication entry point
// ========================================================================

#[derive(Debug, Clone)]
pub struct AuthManager {
    config: AuthProviderConfig,
}

#[derive(Debug, Clone)]
pub enum AuthProviderConfig {
    Disabled,
    Static(StaticAuthConfig),
    // Future: Oidc(OidcAuthConfig)
}

#[derive(Debug, Clone)]
pub struct StaticAuthConfig {
    pub tokens: HashMap<String, StaticTokenEntry>,
}

#[derive(Debug, Clone)]
pub struct StaticTokenEntry {
    pub client_id: String,
    pub role: Role,
}

impl AuthManager {
    pub fn new(config: AuthProviderConfig) -> Self {
        Self { config }
    }

    pub fn disabled() -> Self {
        Self {
            config: AuthProviderConfig::Disabled,
        }
    }

    pub fn with_default_token() -> Self {
        let token = generate_token("leankg");
        let mut tokens = HashMap::new();
        tokens.insert(
            token,
            StaticTokenEntry {
                client_id: "default".to_string(),
                role: Role::Admin,
            },
        );
        Self {
            config: AuthProviderConfig::Static(StaticAuthConfig { tokens }),
        }
    }

    pub fn from_config(settings: &crate::config::AuthSettings) -> Self {
        if !settings.enabled {
            return Self::disabled();
        }

        let mut tokens = HashMap::new();
        for entry in &settings.tokens {
            if let Some(role) = Role::from_str(&entry.role) {
                tokens.insert(
                    entry.token.clone(),
                    StaticTokenEntry {
                        client_id: entry.client_id.clone(),
                        role,
                    },
                );
            }
        }

        if tokens.is_empty() {
            // If no tokens configured, create default admin token
            return Self::with_default_token();
        }

        Self {
            config: AuthProviderConfig::Static(StaticAuthConfig { tokens }),
        }
    }

    pub fn validate_token(&self, token: &str) -> Result<AuthContext, AuthError> {
        match &self.config {
            AuthProviderConfig::Disabled => Ok(AuthContext {
                client_id: "anonymous".to_string(),
                role: Role::Admin,
            }),
            AuthProviderConfig::Static(static_config) => {
                let entry = static_config
                    .tokens
                    .get(token)
                    .ok_or(AuthError::InvalidToken)?;
                Ok(AuthContext {
                    client_id: entry.client_id.clone(),
                    role: entry.role.clone(),
                })
            }
        }
    }

    pub fn check_permission(
        &self,
        context: &AuthContext,
        tool_name: &str,
    ) -> Result<(), AuthError> {
        let required = required_role(tool_name);
        if role_sufficient(&context.role, &required) {
            Ok(())
        } else {
            Err(AuthError::InsufficientPermission {
                required,
                actual: context.role.clone(),
                tool: tool_name.to_string(),
            })
        }
    }

    pub fn is_enabled(&self) -> bool {
        !matches!(self.config, AuthProviderConfig::Disabled)
    }
}

// ========================================================================
// Role-based access control
// ========================================================================

pub fn required_role(tool_name: &str) -> Role {
    match tool_name {
        // Admin-only: structural changes
        "mcp_init"
        | "mcp_index"
        | "mcp_install"
        | "promote_environment"
        | "embed_control"
        | "ontology_control" => Role::Admin,
        // Contributor: knowledge writing
        "add_knowledge" | "update_knowledge" | "delete_knowledge" | "add_annotation"
        | "link_element" | "add_documentation" => Role::Contributor,
        // Everything else: read-only
        _ => Role::Viewer,
    }
}

fn role_sufficient(actual: &Role, required: &Role) -> bool {
    let level = |r: &Role| match r {
        Role::Admin => 3,
        Role::Contributor => 2,
        Role::Viewer => 1,
    };
    level(actual) >= level(required)
}

// ========================================================================
// Auth errors
// ========================================================================

#[derive(Debug, Clone)]
pub enum AuthError {
    InvalidToken,
    InsufficientPermission {
        required: Role,
        actual: Role,
        tool: String,
    },
}

impl std::fmt::Display for AuthError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            AuthError::InvalidToken => write!(f, "Invalid or missing authentication token"),
            AuthError::InsufficientPermission {
                required,
                actual,
                tool,
            } => {
                write!(
                    f,
                    "Insufficient permission for '{}': requires '{}' but has '{}'",
                    tool, required, actual
                )
            }
        }
    }
}

// ========================================================================
// Token utilities
// ========================================================================

fn generate_token(secret: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(secret.as_bytes());
    hasher.update(
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos()
            .to_le_bytes(),
    );
    format!("{:x}", hasher.finalize())
}

#[allow(dead_code)]
pub fn hash_token(token: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(token.as_bytes());
    format!("{:x}", hasher.finalize())
}

// ========================================================================
// Legacy compatibility - Old AuthConfig struct for CLI/backward compat
// ========================================================================

#[derive(Debug, Clone)]
pub struct LegacyAuthConfig {
    pub tokens: HashMap<String, String>,
}

impl LegacyAuthConfig {
    pub fn new() -> Self {
        Self {
            tokens: HashMap::new(),
        }
    }

    pub fn with_default_token(mut self) -> Self {
        let token = generate_token("leankg");
        self.tokens.insert(token, "default".to_string());
        self
    }

    #[allow(dead_code)]
    pub fn add_token(&mut self, token: String, client_id: String) {
        self.tokens.insert(token, client_id);
    }

    #[allow(dead_code)]
    pub fn validate_token(&self, token: &str) -> Option<&String> {
        self.tokens.get(token)
    }
}

impl Default for LegacyAuthConfig {
    fn default() -> Self {
        Self::new().with_default_token()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_legacy_auth_config_default() {
        let config = LegacyAuthConfig::default();
        assert!(!config.tokens.is_empty());
    }

    #[test]
    fn test_legacy_validate_token() {
        let mut config = LegacyAuthConfig::new();
        config.add_token("test-token".to_string(), "client1".to_string());
        assert_eq!(
            config.validate_token("test-token"),
            Some(&"client1".to_string())
        );
        assert_eq!(config.validate_token("invalid"), None);
    }

    #[test]
    fn test_hash_token() {
        let hash = hash_token("my-secret-token");
        assert_eq!(hash.len(), 64);
    }

    #[test]
    fn test_auth_manager_disabled() {
        let mgr = AuthManager::disabled();
        let ctx = mgr.validate_token("anything").unwrap();
        assert_eq!(ctx.role, Role::Admin);
    }

    #[test]
    fn test_auth_manager_static() {
        let mgr = AuthManager::with_default_token();
        assert!(mgr.is_enabled());
    }

    #[test]
    fn test_required_role_mapping() {
        assert_eq!(required_role("mcp_index"), Role::Admin);
        assert_eq!(required_role("embed_control"), Role::Admin);
        assert_eq!(required_role("ontology_control"), Role::Admin);
        assert_eq!(required_role("add_knowledge"), Role::Contributor);
        assert_eq!(required_role("search_code"), Role::Viewer);
        assert_eq!(required_role("get_impact_radius"), Role::Viewer);
    }

    #[test]
    fn test_role_sufficient() {
        assert!(role_sufficient(&Role::Admin, &Role::Viewer));
        assert!(role_sufficient(&Role::Admin, &Role::Contributor));
        assert!(role_sufficient(&Role::Admin, &Role::Admin));
        assert!(role_sufficient(&Role::Contributor, &Role::Viewer));
        assert!(role_sufficient(&Role::Contributor, &Role::Contributor));
        assert!(!role_sufficient(&Role::Contributor, &Role::Admin));
        assert!(!role_sufficient(&Role::Viewer, &Role::Contributor));
        assert!(!role_sufficient(&Role::Viewer, &Role::Admin));
    }

    #[test]
    fn test_check_permission() {
        let mgr = AuthManager::with_default_token();
        let admin_ctx = AuthContext {
            client_id: "test".to_string(),
            role: Role::Admin,
        };
        let viewer_ctx = AuthContext {
            client_id: "test".to_string(),
            role: Role::Viewer,
        };

        assert!(mgr.check_permission(&admin_ctx, "mcp_index").is_ok());
        assert!(mgr.check_permission(&admin_ctx, "search_code").is_ok());
        assert!(mgr.check_permission(&viewer_ctx, "search_code").is_ok());
        assert!(mgr.check_permission(&viewer_ctx, "mcp_index").is_err());
        assert!(mgr.check_permission(&viewer_ctx, "add_knowledge").is_err());
    }
}
