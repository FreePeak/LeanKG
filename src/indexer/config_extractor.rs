use crate::db::models::{CodeElement, Relationship};
use regex::Regex;
use std::path::Path;

pub struct ConfigExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
    file_type: &'a str,
}

impl<'a> ConfigExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str, file_type: &'a str) -> Self {
        Self {
            source,
            file_path,
            file_type,
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        match self.file_type {
            "package_json" => self.extract_package_json(),
            "tsconfig_json" => self.extract_tsconfig_json(),
            "cargo_toml" => self.extract_cargo_toml(),
            "go_mod" => self.extract_go_mod(),
            _ => (vec![], vec![]),
        }
    }

    fn extract_package_json(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let content = String::from_utf8_lossy(self.source);
        let parsed: Result<serde_json::Value, _> = serde_json::from_str(&content);

        let config_name = Path::new(self.file_path)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .to_string();
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "config_file".to_string(),
            name: config_name,
            file_path: self.file_path.to_string(),
            language: "json".to_string(),
            ..Default::default()
        });

        if let Ok(json) = parsed {
            let mut process_deps = |key: &str| {
                if let Some(deps) = json.get(key).and_then(|v| v.as_object()) {
                    for (dep_name, dep_version) in deps {
                        let version_str = dep_version.as_str().unwrap_or("").to_string();
                        let dep_qualified = format!("__pkg__{}", dep_name);

                        relationships.push(Relationship {
                            id: None,
                            source_qualified: self.file_path.to_string(),
                            target_qualified: dep_qualified,
                            rel_type: "has_dependency".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({
                                "version": version_str,
                                "type": key,
                            }),
                        });
                    }
                }
            };

            process_deps("dependencies");
            process_deps("devDependencies");
            process_deps("peerDependencies");
        }

        (elements, relationships)
    }

    fn extract_tsconfig_json(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();

        let config_name = Path::new(self.file_path)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .to_string();
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "config_file".to_string(),
            name: config_name,
            file_path: self.file_path.to_string(),
            language: "json".to_string(),
            ..Default::default()
        });

        // We only extract target/module settings if valid JSON (ignoring comments which break strict JSON)
        let content = String::from_utf8_lossy(self.source);

        // Strip out single-line `//` comments using a basic regex so serde_json has a chance
        let re_comments = Regex::new(r"(?m)^\s*//.*$").unwrap();
        let cleaned = re_comments.replace_all(&content, "");

        let parsed: Result<serde_json::Value, _> = serde_json::from_str(&cleaned);
        if let Ok(json) = parsed {
            if let Some(compiler_options) = json.get("compilerOptions") {
                if let Some(metadata) = elements.first_mut().map(|e| &mut e.metadata) {
                    *metadata = serde_json::json!({
                        "compilerOptions": compiler_options
                    });
                }
            }
        }

        (elements, vec![])
    }

    fn extract_cargo_toml(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let config_name = Path::new(self.file_path)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .to_string();
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "config_file".to_string(),
            name: config_name,
            file_path: self.file_path.to_string(),
            language: "toml".to_string(),
            ..Default::default()
        });

        let content = String::from_utf8_lossy(self.source);

        // Match standard single-line `[dependencies]` blocks including dev and build.
        // E.g., `serde = "1.0"` or `serde = { version = "1.0" }`
        let mut in_deps_block = false;
        let block_header_re = Regex::new(r"^\[(.*dependencies.*)\]").unwrap();
        let dep_re = Regex::new(r#"^([a-zA-Z0-9_\-]+)\s*=\s*(.*)"#).unwrap();

        let mut current_dep_type = "dependencies".to_string();

        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with('#') {
                continue;
            }

            if let Some(caps) = block_header_re.captures(trimmed) {
                in_deps_block = true;
                current_dep_type = caps
                    .get(1)
                    .map_or("dependencies".to_string(), |m| m.as_str().to_string());
                continue;
            } else if trimmed.starts_with('[') {
                in_deps_block = false;
                continue;
            }

            if in_deps_block {
                if let Some(caps) = dep_re.captures(trimmed) {
                    let dep_name = caps.get(1).map_or("", |m| m.as_str());
                    let dep_val = caps.get(2).map_or("", |m| m.as_str());

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: format!("__pkg__{}", dep_name),
                        rel_type: "has_dependency".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({
                            "type": current_dep_type,
                            "value": dep_val,
                        }),
                    });
                }
            }
        }

        (elements, relationships)
    }

    fn extract_go_mod(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        let config_name = Path::new(self.file_path)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
            .to_string();
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "config_file".to_string(),
            name: config_name,
            file_path: self.file_path.to_string(),
            language: "mod".to_string(),
            ..Default::default()
        });

        let content = String::from_utf8_lossy(self.source);
        let mut in_require_block = false;

        let req_single = Regex::new(r"^\s*require\s+([^\s]+)\s+(v[^\s]+)").unwrap();
        let req_block_start = Regex::new(r"^\s*require\s*\(\s*$").unwrap();
        let req_block_end = Regex::new(r"^\s*\)\s*$").unwrap();
        let dep_line = Regex::new(r"^\s*([^\s]+)\s+(v[^\s]+)").unwrap();

        for line in content.lines() {
            let trimmed = line.trim();
            if trimmed.is_empty() || trimmed.starts_with("//") {
                continue;
            }

            if let Some(caps) = req_single.captures(line) {
                let dep_name = caps.get(1).map_or("", |m| m.as_str());
                let version = caps.get(2).map_or("", |m| m.as_str());

                relationships.push(Relationship {
                    id: None,
                    source_qualified: self.file_path.to_string(),
                    target_qualified: format!("__pkg__{}", dep_name),
                    rel_type: "has_dependency".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({
                        "version": version,
                    }),
                });
            } else if req_block_start.is_match(line) {
                in_require_block = true;
            } else if req_block_end.is_match(line) {
                in_require_block = false;
            } else if in_require_block {
                if let Some(caps) = dep_line.captures(line) {
                    let dep_name = caps.get(1).map_or("", |m| m.as_str());
                    let version = caps.get(2).map_or("", |m| m.as_str());

                    relationships.push(Relationship {
                        id: None,
                        source_qualified: self.file_path.to_string(),
                        target_qualified: format!("__pkg__{}", dep_name),
                        rel_type: "has_dependency".to_string(),
                        confidence: 1.0,
                        metadata: serde_json::json!({
                            "version": version,
                        }),
                    });
                }
            }
        }

        (elements, relationships)
    }
}
