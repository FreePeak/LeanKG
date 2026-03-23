use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;

#[derive(Debug, thiserror::Error)]
pub enum TemplateError {
    #[error("Template not found: {0}")]
    NotFound(String),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("Render error: {0}")]
    Render(String),
}

pub struct TemplateEngine {
    templates_dir: PathBuf,
}

impl TemplateEngine {
    pub fn new(templates_dir: PathBuf) -> Self {
        Self { templates_dir }
    }

    pub fn load_template(&self, name: &str) -> Result<String, TemplateError> {
        let template_path = self.templates_dir.join(format!("{}.tmpl", name));
        if template_path.exists() {
            Ok(fs::read_to_string(&template_path)?)
        } else {
            Err(TemplateError::NotFound(format!(
                "Template '{}' not found at {}",
                name,
                template_path.display()
            )))
        }
    }

    pub fn render_template(template: &str, variables: &HashMap<String, String>) -> String {
        let mut output = template.to_string();
        for (key, value) in variables {
            let placeholder = format!("{{{{{}}}}}", key);
            output = output.replace(&placeholder, value);
        }
        output
    }

    pub fn render_element_template(
        &self,
        template_name: &str,
        qualified_name: &str,
        element_type: &str,
        relationships: &[String],
    ) -> Result<String, TemplateError> {
        let template = self.load_template(template_name)?;

        let rel_str = if relationships.is_empty() {
            String::new()
        } else {
            relationships.iter().map(|r| format!("- {}\n", r)).collect()
        };

        let mut variables = HashMap::new();
        variables.insert("qualified_name".to_string(), qualified_name.to_string());
        variables.insert("element_type".to_string(), element_type.to_string());
        variables.insert("relationships".to_string(), rel_str);

        Ok(Self::render_template(&template, &variables))
    }

    pub fn render_agents_template(elements: &[String]) -> String {
        let mut output = String::from("# Agent Guidelines for LeanKG\n\n");
        output.push_str("## Codebase Structure\n\n");
        output.push_str("```\n");

        for element in elements {
            output.push_str(&format!("- {}\n", element));
        }

        output.push_str("```\n");
        output
    }

    pub fn render_claude_template(context: &str) -> String {
        let mut output = String::from("# CLAUDE.md\n\n");
        output.push_str("## Project Context\n\n");
        output.push_str(context);
        output.push('\n');
        output
    }

    pub fn render_file_summary(
        file_path: &str,
        elements: &[String],
        relationships: &[String],
    ) -> String {
        let mut output = String::new();
        output.push_str(&format!("# {}\n\n", file_path));
        output.push_str("## Elements\n\n");
        for elem in elements {
            output.push_str(&format!("- {}\n", elem));
        }
        output.push_str("\n## Relationships\n\n");
        for rel in relationships {
            output.push_str(&format!("- {}\n", rel));
        }
        output
    }

    pub fn save_template(&self, name: &str, content: &str) -> Result<(), TemplateError> {
        let template_path = self.templates_dir.join(format!("{}.tmpl", name));
        fs::create_dir_all(&self.templates_dir)?;
        fs::write(&template_path, content)?;
        Ok(())
    }

    pub fn list_templates(&self) -> Result<Vec<String>, TemplateError> {
        let mut templates = Vec::new();
        if self.templates_dir.exists() {
            for entry in fs::read_dir(&self.templates_dir)? {
                let entry = entry?;
                let path = entry.path();
                if path.extension().is_some_and(|ext| ext == "tmpl") {
                    if let Some(stem) = path.file_stem() {
                        templates.push(stem.to_string_lossy().to_string());
                    }
                }
            }
        }
        Ok(templates)
    }

    pub fn render_custom_template(
        &self,
        template_name: &str,
        variables: &HashMap<String, String>,
    ) -> Result<String, TemplateError> {
        let template = self.load_template(template_name)?;
        Ok(Self::render_template(&template, variables))
    }

    pub fn get_default_agents_template() -> &'static str {
        r#"# Agent Guidelines for {{project_name}}

## Project Overview

{{project_description}}

## Build Commands

### Standard Build
```bash
cargo build                    # Debug build
cargo build --release          # Release build
```

### Testing
```bash
cargo test                     # Run all tests
cargo test <test_name>         # Run specific test
cargo test --package <pkg>     # Test specific package
cargo test -- --nocapture      # Show println output during tests
```

### Code Quality
```bash
cargo fmt                      # Format code
cargo fmt -- --check           # Check formatting without changes
cargo clippy                   # Run linter
cargo clippy -- -D warnings    # Treat warnings as errors
```

## Code Structure Overview

This codebase contains {{element_count}} elements and {{relationship_count}} relationships.

### Key Modules

{{modules}}

### Files

{{files}}

### Functions

{{functions}}

## Context Guidelines

{{context_guidelines}}
"#
    }

    pub fn get_default_claude_template() -> &'static str {
        r#"# CLAUDE.md

## Project Overview

{{project_description}}

## Architecture Decisions

{{architecture_decisions}}

## Context Statistics

- **Total elements**: {{element_count}}
- **Total relationships**: {{relationship_count}}
- **Business logic annotations**: {{annotation_count}}

## Key Files

{{key_files}}

## Context Guidelines for AI

{{context_guidelines}}

## Business Logic Annotations

{{annotations}}
"#
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_render_agents_template_empty() {
        let out = TemplateEngine::render_agents_template(&[]);
        assert!(out.contains("# Agent Guidelines"));
        assert!(out.contains("## Codebase Structure"));
    }

    #[test]
    fn test_render_agents_template_with_elements() {
        let elements = vec!["foo".to_string(), "bar".to_string()];
        let out = TemplateEngine::render_agents_template(&elements);
        assert!(out.contains("- foo"));
        assert!(out.contains("- bar"));
    }

    #[test]
    fn test_render_claude_template() {
        let out = TemplateEngine::render_claude_template("test context");
        assert!(out.contains("# CLAUDE.md"));
        assert!(out.contains("test context"));
    }

    #[test]
    fn test_render_file_summary() {
        let elements = vec!["func1".to_string(), "func2".to_string()];
        let relationships = vec!["imports: mod".to_string()];
        let out = TemplateEngine::render_file_summary("src/main.rs", &elements, &relationships);
        assert!(out.contains("# src/main.rs"));
        assert!(out.contains("- func1"));
        assert!(out.contains("- imports: mod"));
    }

    #[test]
    fn test_render_template_with_variables() {
        let template = "Hello {{name}}, you have {{count}} messages.";
        let mut vars = HashMap::new();
        vars.insert("name".to_string(), "Alice".to_string());
        vars.insert("count".to_string(), "5".to_string());
        let out = TemplateEngine::render_template(template, &vars);
        assert_eq!(out, "Hello Alice, you have 5 messages.");
    }

    #[test]
    fn test_get_default_agents_template() {
        let t = TemplateEngine::get_default_agents_template();
        assert!(t.contains("# Agent Guidelines for {{project_name}}"));
        assert!(t.contains("## Build Commands"));
        assert!(t.contains("cargo build"));
    }

    #[test]
    fn test_get_default_claude_template() {
        let t = TemplateEngine::get_default_claude_template();
        assert!(t.contains("# CLAUDE.md"));
        assert!(t.contains("## Architecture Decisions"));
        assert!(t.contains("{{element_count}}"));
    }
}
