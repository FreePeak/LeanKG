use crate::db::models::{BusinessLogic, CodeElement, Relationship};
use std::fs;
use std::io::Write;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};

pub struct NoteGenerator {
    pub vault_path: String,
}

#[derive(Debug)]
pub struct GeneratedNote {
    pub path: String,
    pub element_id: String,
}

#[derive(Debug)]
pub struct NoteMetadata {
    pub leankg_id: String,
    pub leankg_type: String,
    pub leankg_file: String,
    pub leankg_line: String,
    pub leankg_annotation: String,
    pub leankg_relationships: Vec<String>,
    pub leankg_relationships_wikilinks: Vec<String>,
    pub created: String,
    pub updated: String,
}

impl NoteGenerator {
    pub fn new(vault_path: &str) -> Self {
        Self {
            vault_path: vault_path.to_string(),
        }
    }

    pub fn generate_note(
        &self,
        element: &CodeElement,
        relationships: &[Relationship],
        annotation: Option<&BusinessLogic>,
    ) -> Result<GeneratedNote, ObsidianError> {
        let note_path = self.element_to_note_path(element);
        let metadata = self.build_metadata(element, relationships, annotation);
        let content = self.build_note_content(element, &metadata);

        let full_path = Path::new(&self.vault_path).join(&note_path);
        if let Some(parent) = full_path.parent() {
            fs::create_dir_all(parent).map_err(|e| ObsidianError::IoError(e.to_string()))?;
        }

        let mut file =
            fs::File::create(&full_path).map_err(|e| ObsidianError::IoError(e.to_string()))?;
        file.write_all(content.as_bytes())
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        Ok(GeneratedNote {
            path: note_path,
            element_id: element.qualified_name.clone(),
        })
    }

    pub fn element_to_note_path(&self, element: &CodeElement) -> String {
        let safe_name = element
            .qualified_name
            .replace("::", "/")
            .replace(":", "_")
            .replace(" ", "_")
            .replace("(", "_")
            .replace(")", "_");
        // Add suffix to folder notes to avoid conflict with actual folder paths
        if element.element_type == "Folder" {
            format!("{}.folder.md", safe_name)
        } else {
            format!("{}.md", safe_name)
        }
    }

    fn build_metadata(
        &self,
        element: &CodeElement,
        relationships: &[Relationship],
        annotation: Option<&BusinessLogic>,
    ) -> NoteMetadata {
        let rel_strings: Vec<String> = relationships
            .iter()
            .map(|r| format!("{} ({})", r.target_qualified, r.rel_type))
            .collect();

        let annotation_text = annotation
            .map(|a| a.description.clone())
            .unwrap_or_default();

        let now = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| chrono_like_format(d.as_secs()))
            .unwrap_or_else(|_| "1970-01-01T00:00:00Z".to_string());

        let rel_wikilinks: Vec<String> = relationships
            .iter()
            .filter(|r| !r.target_qualified.starts_with("__unresolved__"))
            .filter(|r| {
                !r.target_qualified.starts_with("std::")
                    && !r.target_qualified.starts_with("core::")
            })
            .filter(|r| {
                let target = &r.target_qualified;
                if target.contains("::") {
                    let parts: Vec<&str> = target.split("::").collect();
                    if parts.len() >= 2 {
                        let file = parts[0].replace("./", "");
                        file.contains('/')
                    } else {
                        true
                    }
                } else {
                    target.contains('/')
                }
            })
            .map(|r| {
                let target = &r.target_qualified;
                let rel_type = &r.rel_type;

                // ./src/main.rs::main -> [[src/main.rs/main]]
                // ./src/api -> [[src/api.folder]] (if it's a folder)
                let wiki_link = if target.contains("::") {
                    let parts: Vec<&str> = target.split("::").collect();
                    if parts.len() >= 2 {
                        let file = parts[0].replace("./", "");
                        let func = parts[1];
                        format!("[[{}/{}]]", file, func)
                    } else {
                        let cleaned = target.replace("./", "");
                        // Check if it's likely a folder (no file extension)
                        if !cleaned.contains('.') || cleaned.ends_with('/') {
                            format!("[[{}.folder]]", cleaned.trim_end_matches('/'))
                        } else {
                            format!("[[{}]]", cleaned)
                        }
                    }
                } else {
                    let cleaned = target.replace("./", "");
                    // Check if it's likely a folder (no file extension)
                    if !cleaned.contains('.') || cleaned.ends_with('/') {
                        format!("[[{}.folder]]", cleaned.trim_end_matches('/'))
                    } else {
                        format!("[[{}]]", cleaned)
                    }
                };
                format!("- {} ({})", wiki_link, rel_type)
            })
            .collect();

        // Add parent folder link for file elements to create folder->file edges in graph
        let mut all_wikilinks = rel_wikilinks;
        if element.element_type == "File" {
            let path = std::path::Path::new(&element.file_path);
            if let Some(parent) = path.parent() {
                let parent_str = parent.to_string_lossy().replace("./", "");
                if !parent_str.is_empty() && parent_str != "." {
                    all_wikilinks.push(format!("- [[{}.folder]] (contained_by)", parent_str));
                }
            }
        }

        NoteMetadata {
            leankg_id: element.qualified_name.clone(),
            leankg_type: element.element_type.clone(),
            leankg_file: element.file_path.clone(),
            leankg_line: format!("{}-{}", element.line_start, element.line_end),
            leankg_annotation: annotation_text,
            leankg_relationships: rel_strings,
            leankg_relationships_wikilinks: all_wikilinks,
            created: now.clone(),
            updated: now,
        }
    }

    fn build_note_content(&self, element: &CodeElement, metadata: &NoteMetadata) -> String {
        let relationships_str = if metadata.leankg_relationships.is_empty() {
            String::new()
        } else {
            metadata
                .leankg_relationships
                .iter()
                .map(|r| format!("  - {}", r))
                .collect::<Vec<_>>()
                .join("\n")
        };

        let annotation_block = if metadata.leankg_annotation.is_empty() {
            String::new()
        } else {
            format!("\n> **Annotation**: {}\n", metadata.leankg_annotation)
        };

        let rels_text = if metadata.leankg_relationships.is_empty() {
            "  (none)".to_string()
        } else {
            relationships_str
        };

        let wikilinks_text = if metadata.leankg_relationships_wikilinks.is_empty() {
            String::new()
        } else {
            metadata.leankg_relationships_wikilinks.join("\n")
        };

        format!(
            r#"---
leankg_id: {leankg_id}
leankg_type: {leankg_type}
leankg_file: {leankg_file}
leankg_line: {leankg_line}
leankg_relationships:
{relationships}
leankg_annotation: "{leankg_annotation}"
created: {created}
updated: {updated}
---

# {name}

**Type**: {element_type}
**File**: `{file_path}`
**Lines**: {line_start}-{line_end}
{annotation}
**Relationships**:
{relationships_text}

{wikipedia_links}
"#,
            leankg_id = metadata.leankg_id,
            leankg_type = metadata.leankg_type,
            leankg_file = metadata.leankg_file,
            leankg_line = metadata.leankg_line,
            leankg_annotation = metadata.leankg_annotation.replace('"', "'"),
            created = metadata.created,
            updated = metadata.updated,
            name = element.name,
            element_type = element.element_type,
            file_path = element.file_path,
            line_start = element.line_start,
            line_end = element.line_end,
            annotation = annotation_block,
            relationships = rels_text,
            relationships_text = rels_text,
            wikipedia_links = wikilinks_text
        )
    }

    pub fn read_existing_annotation(
        &self,
        note_path: &str,
    ) -> Result<Option<String>, ObsidianError> {
        let full_path = Path::new(&self.vault_path).join(note_path);
        if !full_path.exists() {
            return Ok(None);
        }

        let content =
            fs::read_to_string(&full_path).map_err(|e| ObsidianError::IoError(e.to_string()))?;

        for line in content.lines() {
            if line.starts_with("leankg_annotation:") {
                let value = line
                    .trim()
                    .strip_prefix("leankg_annotation:")
                    .map(|s| s.trim().trim_matches('"'))
                    .unwrap_or("");
                return Ok(Some(value.to_string()));
            }
        }

        Ok(None)
    }

    pub fn note_exists(&self, element: &CodeElement) -> bool {
        let path = self.element_to_note_path(element);
        Path::new(&self.vault_path).join(path).exists()
    }
}

fn chrono_like_format(secs: u64) -> String {
    let days = secs / 86400;
    let remaining = secs % 86400;
    let hours = remaining / 3600;
    let minutes = (remaining % 3600) / 60;
    let seconds = remaining % 60;

    let base_days: i64 = days as i64 - 11017;
    let year = 1970 + base_days / 365;
    let remaining_days = base_days % 365;
    let month = (remaining_days / 29) + 1;
    let day = (remaining_days % 29) + 1;

    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}Z",
        year, month, day, hours, minutes, seconds
    )
}

#[derive(Debug, thiserror::Error)]
pub enum ObsidianError {
    #[error("IO error: {0}")]
    IoError(String),
    #[error("Parse error: {0}")]
    ParseError(String),
}

pub fn notes_directory(leankg_path: &Path) -> std::path::PathBuf {
    leankg_path.join("obsidian").join("vault")
}
