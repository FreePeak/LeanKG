use sha2::{Digest, Sha256};
use std::collections::HashMap;

#[derive(Debug, Clone)]
pub struct AuthConfig {
    pub tokens: HashMap<String, String>,
}

impl AuthConfig {
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

    pub fn add_token(&mut self, token: String, client_id: String) {
        self.tokens.insert(token, client_id);
    }

    pub fn validate_token(&self, token: &str) -> Option<&String> {
        self.tokens.get(token)
    }
}

impl Default for AuthConfig {
    fn default() -> Self {
        Self::new().with_default_token()
    }
}

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

pub fn hash_token(token: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(token.as_bytes());
    format!("{:x}", hasher.finalize())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_auth_config_default() {
        let config = AuthConfig::default();
        assert!(!config.tokens.is_empty());
    }

    #[test]
    fn test_validate_token() {
        let mut config = AuthConfig::new();
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
}
