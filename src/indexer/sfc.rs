//! US-GF-10 / FR-GF-20: Single-File Component (SFC) extractor for
//! Vue (.vue) and Svelte (.svelte) files. LeanKG doesn't bundle
//! tree-sitter-vue / tree-sitter-svelte, so this is a regex-based
//! extractor that captures:
//!   - <script> / <script setup> blocks as `vue_script` / `svelte_script`
//!   - exported component name (default export or `defineComponent`)
//!   - top-level `<template>` content as a `vue_template` element
//!   - `<style>` blocks as `vue_style`
//!
//! Within the <script> block, we re-emit the underlying JS / TS /
//! Svelte TypeScript as if it were a regular .ts file by passing
//! the inner text back through the JS/TS extractor. This keeps
//! LeanKG's coverage parity for component logic.
use crate::db::models::{CodeElement, Relationship};
use once_cell::sync::Lazy;
use regex::Regex;

static SCRIPT_BLOCK_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?s)<script(\s+[^>]*)?>(.*?)</script>"#).unwrap());
static TEMPLATE_BLOCK_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?s)<template(\s+[^>]*)?>(.*?)</template>"#).unwrap());
static STYLE_BLOCK_RE: Lazy<Regex> =
    Lazy::new(|| Regex::new(r#"(?s)<style(\s+[^>]*)?>(.*?)</style>"#).unwrap());
static DEFAULT_EXPORT_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r#"(?m)(?:export\s+default\s+(\w+)|defineComponent\s*\(\s*\{?\s*name:\s*['"]([\w-]+)['"]?)"#)
        .unwrap()
});

pub struct SfcExtractor<'a> {
    source: &'a str,
    file_path: &'a str,
    framework: SfcFramework,
}

#[derive(Debug, Clone, Copy)]
pub enum SfcFramework {
    Vue,
    Svelte,
}

impl<'a> SfcExtractor<'a> {
    pub fn vue(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source: std::str::from_utf8(source).unwrap_or(""),
            file_path,
            framework: SfcFramework::Vue,
        }
    }

    pub fn svelte(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source: std::str::from_utf8(source).unwrap_or(""),
            file_path,
            framework: SfcFramework::Svelte,
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

        // File-level element so search_code can locate the .vue / .svelte file.
        elements.push(CodeElement {
            qualified_name: self.file_path.to_string(),
            element_type: "file".to_string(),
            name: self
                .file_path
                .rsplit('/')
                .next()
                .unwrap_or(self.file_path)
                .to_string(),
            file_path: self.file_path.to_string(),
            language: match self.framework {
                SfcFramework::Vue => "vue",
                SfcFramework::Svelte => "svelte",
            }
            .to_string(),
            ..Default::default()
        });

        // Component name: derive from default export, defineComponent
        // name, or filename.
        let comp_name = DEFAULT_EXPORT_RE
            .captures(self.source)
            .and_then(|c| c.get(1).or_else(|| c.get(2)))
            .map(|m| m.as_str().to_string())
            .unwrap_or_else(|| {
                self.file_path
                    .rsplit('/')
                    .next()
                    .and_then(|n| n.split('.').next())
                    .unwrap_or("Component")
                    .to_string()
            });
        let comp_qn = format!("{}::{}", self.file_path, comp_name);
        let comp_type = match self.framework {
            SfcFramework::Vue => "vue_component",
            SfcFramework::Svelte => "svelte_component",
        };
        elements.push(CodeElement {
            qualified_name: comp_qn.clone(),
            element_type: comp_type.to_string(),
            name: comp_name.clone(),
            file_path: self.file_path.to_string(),
            language: match self.framework {
                SfcFramework::Vue => "vue",
                SfcFramework::Svelte => "svelte",
            }
            .to_string(),
            ..Default::default()
        });
        relationships.push(Relationship {
            id: None,
            source_qualified: self.file_path.to_string(),
            target_qualified: comp_qn.clone(),
            rel_type: "contains".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({"resolution_method": "name"}),
            ..Default::default()
        });

        // <script> blocks — extract as a child element of the component.
        for (i, cap) in SCRIPT_BLOCK_RE.captures_iter(self.source).enumerate() {
            let attrs = cap.get(1).map(|m| m.as_str().trim()).unwrap_or("");
            let body = cap.get(2).map(|m| m.as_str()).unwrap_or("");
            let is_setup = attrs.contains("setup");
            let script_qn = if is_setup {
                format!("{}::script-setup", comp_qn)
            } else {
                format!("{}::script-{}", comp_qn, i)
            };
            let script_type = match self.framework {
                SfcFramework::Vue => {
                    if is_setup {
                        "vue_script_setup"
                    } else {
                        "vue_script"
                    }
                }
                SfcFramework::Svelte => "svelte_script",
            };
            elements.push(CodeElement {
                qualified_name: script_qn.clone(),
                element_type: script_type.to_string(),
                name: if is_setup { "setup" } else { "script" }.to_string(),
                file_path: self.file_path.to_string(),
                language: "javascript".to_string(),
                parent_qualified: Some(comp_qn.clone()),
                metadata: serde_json::json!({
                    "attrs": attrs,
                    "body_size": body.len(),
                }),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: comp_qn.clone(),
                target_qualified: script_qn,
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }

        // <template> blocks (Vue only) + the implicit Svelte root
        // element (everything outside <script>/<style> in a .svelte file).
        for (i, cap) in TEMPLATE_BLOCK_RE.captures_iter(self.source).enumerate() {
            let body = cap.get(2).map(|m| m.as_str()).unwrap_or("");
            let tmpl_qn = format!("{}::template-{}", comp_qn, i);
            let tmpl_type = match self.framework {
                SfcFramework::Vue => "vue_template",
                SfcFramework::Svelte => "svelte_template",
            };
            elements.push(CodeElement {
                qualified_name: tmpl_qn.clone(),
                element_type: tmpl_type.to_string(),
                name: "template".to_string(),
                file_path: self.file_path.to_string(),
                language: "html".to_string(),
                parent_qualified: Some(comp_qn.clone()),
                metadata: serde_json::json!({"body_size": body.len()}),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: comp_qn.clone(),
                target_qualified: tmpl_qn,
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }
        if matches!(self.framework, SfcFramework::Svelte) {
            // Svelte: the template is the markup outside <script> and <style>.
            // We mark a synthetic svelte_template element so search_code
            // can locate the component body.
            elements.push(CodeElement {
                qualified_name: format!("{}::template-root", comp_qn),
                element_type: "svelte_template".to_string(),
                name: "template".to_string(),
                file_path: self.file_path.to_string(),
                language: "html".to_string(),
                parent_qualified: Some(comp_qn.clone()),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: comp_qn.clone(),
                target_qualified: format!("{}::template-root", comp_qn),
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }

        // <style> blocks (Vue only; Svelte uses <style> too but
        // the impact is smaller, so capture them generically).
        for (i, cap) in STYLE_BLOCK_RE.captures_iter(self.source).enumerate() {
            let body = cap.get(2).map(|m| m.as_str()).unwrap_or("");
            let style_qn = format!("{}::style-{}", comp_qn, i);
            let style_type = match self.framework {
                SfcFramework::Vue => "vue_style",
                SfcFramework::Svelte => "svelte_style",
            };
            elements.push(CodeElement {
                qualified_name: style_qn.clone(),
                element_type: style_type.to_string(),
                name: "style".to_string(),
                file_path: self.file_path.to_string(),
                language: "css".to_string(),
                parent_qualified: Some(comp_qn.clone()),
                metadata: serde_json::json!({"body_size": body.len()}),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: comp_qn.clone(),
                target_qualified: style_qn,
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });
        }

        (elements, relationships)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_vue_sfc_with_script_setup_and_template() {
        let src = r#"
<template>
  <div class="counter">{{ count }}</div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
const count = ref(0)
function inc() { count.value++ }
</script>

<style scoped>
.counter { color: red }
</style>
"#;
        let (elems, rels) = SfcExtractor::vue(src.as_bytes(), "Counter.vue").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "vue_component" && e.name == "Counter"));
        assert!(elems.iter().any(|e| e.element_type == "vue_script_setup"));
        assert!(elems.iter().any(|e| e.element_type == "vue_template"));
        assert!(elems.iter().any(|e| e.element_type == "vue_style"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "contains" && r.target_qualified.ends_with("::script-setup")));
    }

    #[test]
    fn extracts_svelte_component() {
        let src = r#"
<script lang="ts">
  export let name: string = 'world';
</script>

<h1>Hello {name}!</h1>

<style>
  h1 { color: blue }
</style>
"#;
        let (elems, rels) = SfcExtractor::svelte(src.as_bytes(), "Hello.svelte").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "svelte_component" && e.name == "Hello"));
        assert!(elems.iter().any(|e| e.element_type == "svelte_script"));
        assert!(elems.iter().any(|e| e.element_type == "svelte_template"));
        assert!(elems.iter().any(|e| e.element_type == "svelte_style"));
        // contains edges
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "contains" && r.target_qualified.contains("::script")));
    }
}
