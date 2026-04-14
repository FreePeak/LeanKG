use crate::db::models::{Relationship, RelationshipType};
use regex::Regex;
use serde_yaml::Value;
use std::collections::HashMap;
use std::path::Path;
use walkdir::WalkDir;

pub struct MicroserviceExtractor {
    grpc_pattern: Regex,
    http_pattern: Regex,
    client_dirs: Vec<String>,
}

impl MicroserviceExtractor {
    pub fn new() -> Self {
        Self {
            // Matches: dns:///service-name.default.svc.cluster.local.:10000
            grpc_pattern: Regex::new(r"dns:///([a-z0-9-]+)\.default\.svc\.cluster\.local\.:\d+")
                .unwrap(),
            // Matches: http://service-name.default.svc.cluster.local./
            http_pattern: Regex::new(r"https?://([a-z0-9-]+)\.default\.svc\.cluster\.local\.")
                .unwrap(),
            client_dirs: vec!["internal/external".to_string()],
        }
    }

    pub fn with_config(
        client_dirs: Vec<String>,
        grpc_pattern: String,
        http_pattern: String,
    ) -> Self {
        Self {
            grpc_pattern: Regex::new(&grpc_pattern).unwrap_or_else(|_| {
                Regex::new(r"dns:///[a-z0-9-]+\.default\.svc\.cluster\.local\.\:\d+").unwrap()
            }),
            http_pattern: Regex::new(&http_pattern).unwrap_or_else(|_| {
                Regex::new(r"https?://[a-z0-9-]+\.default\.svc\.cluster\.local\.").unwrap()
            }),
            client_dirs,
        }
    }

    /// Extract microservice relationships from a project directory
    pub fn extract(&self, project_path: &str) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let service_names = self.discover_services(project_path);
        let project_service = service_names.values().next().cloned();

        // Scan client files for gRPC calls
        for client_dir in &self.client_dirs {
            let full_path = Path::new(project_path).join(client_dir);
            if full_path.exists() {
                let file_relationships = self.scan_client_files(&full_path, &project_service);
                relationships.extend(file_relationships);
            }
        }

        // Scan config files for service addresses
        let config_relationships = self.scan_config_files(project_path, &project_service);
        relationships.extend(config_relationships);

        relationships
    }

    /// Discover service names from go.mod or service discovery
    fn discover_services(&self, project_path: &str) -> HashMap<String, String> {
        let mut services = HashMap::new();

        // Read go.mod to get module name as service prefix
        let go_mod_path = Path::new(project_path).join("go.mod");
        if let Ok(content) = std::fs::read_to_string(&go_mod_path) {
            for line in content.lines() {
                if line.starts_with("module ") {
                    let module = line.trim_start_matches("module ");
                    // Extract service name from module path
                    if let Some(last_segment) = module.rsplit('/').next() {
                        services.insert(last_segment.to_string(), last_segment.to_string());
                    }
                    break;
                }
            }
        }

        services
    }

    /// Scan client files (internal/external/) for gRPC client instantiations
    fn scan_client_files(&self, dir: &Path, project_service: &Option<String>) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        for entry in WalkDir::new(dir)
            .into_iter()
            .filter_map(|e| e.ok())
            .filter(|e| e.path().extension().map(|ext| ext == "go").unwrap_or(false))
        {
            let file_path = entry.path();
            if let Ok(content) = std::fs::read_to_string(file_path) {
                let file_rels = self.extract_grpc_calls(
                    &content,
                    file_path.to_str().unwrap_or(""),
                    project_service,
                );
                relationships.extend(file_rels);
            }
        }

        relationships
    }

    /// Extract gRPC calls from file content
    fn extract_grpc_calls(
        &self,
        content: &str,
        file_path: &str,
        project_service: &Option<String>,
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Pattern: grpc.NewClient("dns:///service-name.default.svc.cluster.local.:10000", ...)
        let grpc_client_re = Regex::new(r#"(?m)grpc\.NewClient\s*\(\s*"([^"]+)"[,\s]"#).unwrap();

        for cap in grpc_client_re.captures_iter(content) {
            let address = &cap[1];
            if let Some(service_name) = self.extract_service_name(address, "grpc") {
                let line_number = content[..cap.get(0).map(|m| m.end()).unwrap_or(0)]
                    .lines()
                    .count() as u32;

                relationships.push(self.create_relationship(
                    service_name,
                    "grpc".to_string(),
                    address.to_string(),
                    "unknown".to_string(),
                    file_path.to_string(),
                    line_number,
                    project_service,
                ));
            }
        }

        relationships
    }

    /// Extract service name from DNS address
    fn extract_service_name(&self, address: &str, protocol: &str) -> Option<String> {
        if protocol == "grpc" {
            // dns:///service-name.default.svc.cluster.local.:10000
            if let Some(caps) = self.grpc_pattern.captures(address) {
                return Some(
                    caps.get(1)
                        .map(|m| m.as_str().to_string())
                        .unwrap_or_default(),
                );
            }
        }
        None
    }

    /// Scan config files for service address configurations
    fn scan_config_files(
        &self,
        project_path: &str,
        project_service: &Option<String>,
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Scan config/config.go for YAML configs
        let config_go = Path::new(project_path).join("config/config.go");
        if config_go.exists() {
            if let Ok(content) = std::fs::read_to_string(&config_go) {
                // Look for YAML content in config files
                let yaml_re = Regex::new(r#"be_(\w+)_address\s*[=:]\s*["']([^"']+)["']"#).unwrap();
                for cap in yaml_re.captures_iter(&content) {
                    let _service_key = &cap[1];
                    let address = &cap[2];

                    if address.starts_with("dns:///") {
                        if let Some(service_name) = self.extract_service_name(address, "grpc") {
                            relationships.push(self.create_relationship(
                                service_name,
                                "grpc".to_string(),
                                address.to_string(),
                                format!("config:{}", &cap[1]),
                                config_go.to_str().unwrap_or("").to_string(),
                                0,
                                project_service,
                            ));
                        }
                    }
                }
            }
        }

        // Scan YAML config files
        for entry in WalkDir::new(Path::new(project_path).join("config"))
            .into_iter()
            .filter_map(|e| e.ok())
            .filter(|e| {
                let path = e.path();
                path.extension()
                    .map(|ext| ext == "yaml" || ext == "yml")
                    .unwrap_or(false)
            })
        {
            if let Ok(content) = std::fs::read_to_string(entry.path()) {
                if let Ok(yaml) = serde_yaml::from_str::<Value>(&content) {
                    let file_rels = self.extract_from_yaml(
                        &yaml,
                        entry.path().to_str().unwrap_or(""),
                        project_service,
                    );
                    relationships.extend(file_rels);
                }
            }
        }

        relationships
    }

    /// Extract service addresses from YAML content
    fn extract_from_yaml(
        &self,
        yaml: &Value,
        file_path: &str,
        project_service: &Option<String>,
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        if let Some(obj) = yaml.as_mapping() {
            for (key, val) in obj {
                let key_str = key.as_str().unwrap_or("");
                if key_str.ends_with("_address")
                    && val
                        .as_str()
                        .map(|s| s.starts_with("dns:///"))
                        .unwrap_or(false)
                {
                    let address = val.as_str().unwrap_or("");
                    if let Some(service_name) = self.extract_service_name(address, "grpc") {
                        relationships.push(self.create_relationship(
                            service_name,
                            "grpc".to_string(),
                            address.to_string(),
                            key_str.to_string(),
                            file_path.to_string(),
                            0,
                            project_service,
                        ));
                    }
                }
                // Recurse into nested mappings
                if let Some(nested) = val.as_mapping() {
                    let nested_rels =
                        self.extract_from_yaml_internal(nested, file_path, project_service);
                    relationships.extend(nested_rels);
                }
            }
        }

        relationships
    }

    fn extract_from_yaml_internal(
        &self,
        yaml: &serde_yaml::Mapping,
        file_path: &str,
        project_service: &Option<String>,
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        for (key, val) in yaml {
            let key_str = key.as_str().unwrap_or("");
            if key_str.ends_with("_address")
                && val
                    .as_str()
                    .map(|s| s.starts_with("dns:///"))
                    .unwrap_or(false)
            {
                let address = val.as_str().unwrap_or("");
                if let Some(service_name) = self.extract_service_name(address, "grpc") {
                    relationships.push(self.create_relationship(
                        service_name,
                        "grpc".to_string(),
                        address.to_string(),
                        key_str.to_string(),
                        file_path.to_string(),
                        0,
                        project_service,
                    ));
                }
            }
            if let Some(nested) = val.as_mapping() {
                let nested_rels =
                    self.extract_from_yaml_internal(nested, file_path, project_service);
                relationships.extend(nested_rels);
            }
        }

        relationships
    }

    /// Create a Relationship with service_calls type
    fn create_relationship(
        &self,
        target_service: String,
        protocol: String,
        address: String,
        api_path: String,
        source_file: String,
        line_number: u32,
        project_service: &Option<String>,
    ) -> Relationship {
        Relationship {
            id: None,
            source_qualified: project_service
                .clone()
                .unwrap_or_else(|| self.infer_source_service(&source_file)),
            target_qualified: target_service,
            rel_type: RelationshipType::ServiceCalls.as_str().to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({
                "protocol": protocol,
                "address": address,
                "api_path": api_path,
                "source_file": source_file,
                "line_number": line_number,
            }),
        }
    }

    /// Infer the source service name from the file path
    /// Pattern: .../service-name/internal/external/...
    fn infer_source_service(&self, file_path: &str) -> String {
        let path = Path::new(file_path);
        let components: Vec<_> = path.components().collect();

        // Look for the service name in the path
        // Pattern: .../service-name/internal/external/...
        // The service name is the parent of "internal"
        for i in 0..components.len() {
            if components[i].as_os_str() == "internal" {
                if i > 0 {
                    return components[i - 1].as_os_str().to_string_lossy().to_string();
                }
            }
        }

        // Fallback: use the directory containing internal/external
        for i in 0..components.len() {
            if components[i].as_os_str() == "external" {
                if i >= 2 {
                    // Return the grandparent (parent of "external"'s parent)
                    return components[i - 2].as_os_str().to_string_lossy().to_string();
                }
            }
        }

        "unknown-service".to_string()
    }
}

impl Default for MicroserviceExtractor {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_service_name_from_grpc_address() {
        let extractor = MicroserviceExtractor::new();
        let address = "dns:///service-a.default.svc.cluster.local.:10000";
        let service = extractor.extract_service_name(address, "grpc");
        assert_eq!(service, Some("service-a".to_string()));
    }

    #[test]
    fn test_grpc_pattern_matching() {
        let content = r#"
grpc.NewClient("dns:///service-a.default.svc.cluster.local.:10000",
    grpc.WithTransportCredentials(insecure.NewCredentials()),
)
"#;
        let extractor = MicroserviceExtractor::new();
        let relationships = extractor.extract_grpc_calls(content, "test.go", &None);
        assert!(!relationships.is_empty());
        assert_eq!(relationships[0].target_qualified, "service-a");
    }

    #[test]
    fn test_infer_source_service() {
        let extractor = MicroserviceExtractor::new();
        // Path: .../service-gateway/internal/external/client.go
        // Service is "service-gateway" (parent of "internal")
        let path = "/path/to/service-gateway/internal/external/client.go";
        let service = extractor.infer_source_service(path);
        assert_eq!(service, "service-gateway");
    }

    #[test]
    fn test_infer_source_service_nested() {
        let extractor = MicroserviceExtractor::new();
        // Path: .../my-service/internal/external/client.go
        let path = "/workspace/my-service/internal/external/client.go";
        let service = extractor.infer_source_service(path);
        assert_eq!(service, "my-service");
    }
}
