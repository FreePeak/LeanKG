#![allow(dead_code)]
use serde::{Deserialize, Serialize};
use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Registry {
    pub version: u32,
    pub repos: HashMap<String, RepoEntry>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RepoEntry {
    pub path: String,
    pub last_indexed: Option<String>,
    pub element_count: Option<usize>,
}

impl Default for Registry {
    fn default() -> Self {
        Self {
            version: 1,
            repos: HashMap::new(),
        }
    }
}

impl Registry {
    pub fn load() -> Result<Self, Box<dyn std::error::Error>> {
        let path = Self::registry_path()?;
        if !path.exists() {
            return Ok(Registry::default());
        }
        let content = fs::read_to_string(&path)?;
        let registry: Registry = serde_json::from_str(&content)?;
        Ok(registry)
    }

    pub fn save(&self) -> Result<(), Box<dyn std::error::Error>> {
        let path = Self::registry_path()?;
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        let content = serde_json::to_string_pretty(self)?;
        fs::write(&path, content)?;
        Ok(())
    }

    pub fn register(
        &mut self,
        name: String,
        path: String,
    ) -> Result<(), Box<dyn std::error::Error>> {
        let abs_path = if PathBuf::from(&path).is_absolute() {
            path.clone()
        } else {
            std::env::current_dir()
                .map(|p| p.join(&path).to_string_lossy().to_string())
                .unwrap_or(path)
        };

        let entry = RepoEntry {
            path: abs_path,
            last_indexed: None,
            element_count: None,
        };
        self.repos.insert(name, entry);
        self.save()?;
        Ok(())
    }

    pub fn unregister(&mut self, name: &str) -> Result<(), Box<dyn std::error::Error>> {
        self.repos.remove(name);
        self.save()?;
        Ok(())
    }

    pub fn get_repo(&self, name: &str) -> Option<&RepoEntry> {
        self.repos.get(name)
    }

    pub fn list_repos(&self) -> Vec<(&String, &RepoEntry)> {
        self.repos.iter().collect()
    }

    #[allow(dead_code)]
    pub fn update_last_indexed(
        &mut self,
        name: &str,
        timestamp: String,
        element_count: usize,
    ) -> Result<(), Box<dyn std::error::Error>> {
        if let Some(entry) = self.repos.get_mut(name) {
            entry.last_indexed = Some(timestamp);
            entry.element_count = Some(element_count);
            self.save()?;
        }
        Ok(())
    }

    fn registry_path() -> Result<PathBuf, Box<dyn std::error::Error>> {
        let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
        Ok(PathBuf::from(home).join(".leankg").join("registry.json"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::TempDir;

    #[test]
    fn test_registry_default() {
        let registry = Registry::default();
        assert_eq!(registry.version, 1);
        assert!(registry.repos.is_empty());
    }

    #[test]
    fn test_registry_register_and_unregister() {
        let temp_dir = TempDir::new().unwrap();
        let _registry_path = temp_dir.path().join("registry.json");

        std::env::set_var("HOME", temp_dir.path());

        let mut registry = Registry::default();
        registry
            .register("test-repo".to_string(), "/path/to/test".to_string())
            .unwrap();

        assert!(registry.repos.contains_key("test-repo"));
        assert_eq!(
            registry.get_repo("test-repo").unwrap().path,
            "/path/to/test"
        );

        registry.unregister("test-repo").unwrap();
        assert!(!registry.repos.contains_key("test-repo"));

        std::env::remove_var("HOME");
    }
}
