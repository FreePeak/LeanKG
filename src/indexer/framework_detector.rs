use crate::db::models::{CodeElement, Relationship};
use std::collections::{HashMap, HashSet};
use std::path::Path;

pub struct FrameworkDetector;

impl FrameworkDetector {
    pub fn detect_frameworks(
        _elements: &[CodeElement],
        relationships: &[Relationship],
    ) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut new_elements = Vec::new();
        let mut new_relationships = Vec::new();
        let mut detected = HashSet::new(); // HashSet of (root_dir, framework_name)

        // Maps common package names or imports to high-level framework names
        let mut framework_map: HashMap<&str, &str> = HashMap::new();

        // JS/TS
        framework_map.insert("react", "React");
        framework_map.insert("react-dom", "React");
        framework_map.insert("next", "Next.js");
        framework_map.insert("express", "Express");
        framework_map.insert("@nestjs/core", "NestJS");
        framework_map.insert("@angular/core", "Angular");
        framework_map.insert("vue", "Vue");
        framework_map.insert("svelte", "Svelte");
        framework_map.insert("nuxt", "Nuxt.js");

        // Rust
        framework_map.insert("axum", "Axum");
        framework_map.insert("actix-web", "Actix");
        framework_map.insert("rocket", "Rocket");
        framework_map.insert("yew", "Yew");
        framework_map.insert("leptos", "Leptos");

        // Python
        framework_map.insert("django", "Django");
        framework_map.insert("flask", "Flask");
        framework_map.insert("fastapi", "FastAPI");

        // Go
        framework_map.insert("github.com/gin-gong/gin", "Gin");
        framework_map.insert("github.com/labstack/echo/v4", "Echo");
        framework_map.insert("github.com/gofiber/fiber", "Fiber");

        // Java / Kotlin
        framework_map.insert("org.springframework.boot", "Spring Boot");

        let get_lib_name = |target: &str| -> String {
            if target.starts_with("__pkg__") {
                target.trim_start_matches("__pkg__").to_string()
            } else {
                target.to_string()
            }
        };

        for rel in relationships {
            if rel.rel_type == "has_dependency" || rel.rel_type == "imports" {
                let lib_name = get_lib_name(&rel.target_qualified);

                // Fallback for sub-packages
                let matched_fw = framework_map.get(lib_name.as_str()).copied().or_else(|| {
                    // try matching prefixes for imports e.g. express/router
                    framework_map
                        .iter()
                        .find(|(k, _)| lib_name.starts_with(&format!("{}/", k)))
                        .map(|(_, v)| *v)
                });

                if let Some(fw_name) = matched_fw {
                    let source_path = Path::new(&rel.source_qualified);
                    let project_dir = source_path
                        .parent()
                        .unwrap_or(Path::new(""))
                        .to_string_lossy()
                        .to_string();
                    let project_dir_key = if project_dir.is_empty() {
                        "."
                    } else {
                        &project_dir
                    };

                    let detect_key = format!("{}::{}", project_dir_key, fw_name);

                    if !detected.contains(&detect_key) {
                        detected.insert(detect_key.clone());

                        let fw_id = format!("__fw__{}", fw_name);

                        // Only add the framework node if it doesn't already exist globally?
                        // Wait, we add it, DB insert will handle deduplication of nodes usually.
                        new_elements.push(CodeElement {
                            qualified_name: fw_id.clone(),
                            element_type: "framework".to_string(),
                            name: fw_name.to_string(),
                            file_path: "domain".to_string(), // Virtual
                            language: "domain".to_string(),
                            ..Default::default()
                        });

                        new_relationships.push(Relationship {
                            id: None,
                            source_qualified: project_dir_key.to_string(),
                            target_qualified: fw_id,
                            rel_type: "uses_framework".to_string(),
                            confidence: 1.0,
                            metadata: serde_json::json!({
                                "detected_from": lib_name,
                                "relation": rel.rel_type
                            }),
                        });
                    }
                }
            }
        }

        (new_elements, new_relationships)
    }
}
