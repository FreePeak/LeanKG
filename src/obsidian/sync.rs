use crate::db;
use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
use crate::obsidian::note_generator::{NoteGenerator, ObsidianError};
use std::collections::HashMap;
use std::path::Path;
use std::sync::Arc;
use tokio::sync::RwLock;

pub struct SyncEngine {
    generator: NoteGenerator,
    db: CozoDb,
    changes: Arc<RwLock<SyncChanges>>,
}

#[derive(Debug, Default)]
pub struct SyncChanges {
    pub pushed: Vec<String>,
    pub pulled: Vec<String>,
    pub conflicts: Vec<ConflictInfo>,
}

#[derive(Debug)]
pub struct ConflictInfo {
    pub element_id: String,
    pub local_annotation: String,
    pub remote_annotation: String,
}

impl SyncEngine {
    pub fn new(vault_path: &str, db_path: std::path::PathBuf) -> Self {
        let db = db::init_db(&db_path).expect("Failed to init db");
        Self {
            generator: NoteGenerator::new(vault_path),
            db,
            changes: Arc::new(RwLock::new(SyncChanges::default())),
        }
    }

    pub async fn push(&self) -> Result<SyncResult, ObsidianError> {
        let graph_engine = GraphEngine::new(self.db.clone());

        let elements = graph_engine.all_elements()
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        let relationships = graph_engine.all_relationships()
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        let annotations = db::all_business_logic(&self.db)
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        let annotation_map: HashMap<_, _> = annotations
            .iter()
            .map(|a| (a.element_qualified.clone(), a))
            .collect();

        let rel_map: HashMap<String, Vec<crate::db::models::Relationship>> = relationships
            .into_iter()
            .fold(HashMap::new(), |mut acc, r| {
                acc.entry(r.source_qualified.clone()).or_default().push(r);
                acc
            });

        let mut pushed = Vec::new();
        for element in elements {
            // Skip build artifacts and generated files
            if element.file_path.contains("/target/")
                || element.file_path.contains("/.next/")
                || element.file_path.contains("/node_modules/")
                || element.file_path.contains("/dist/")
                || element.file_path.contains(".test.")
                || element.file_path.ends_with(".generated.rs")
            {
                continue;
            }
            
            let annotation = annotation_map.get(&element.qualified_name).copied();
            let element_rels: Vec<_> = rel_map.get(&element.qualified_name)
                .cloned()
                .unwrap_or_default();
            
            match self.generator.generate_note(&element, &element_rels, annotation) {
                Ok(note) => pushed.push(note.element_id),
                Err(e) => eprintln!("Failed to generate note for {}: {}", element.qualified_name, e),
            }
        }

        let mut changes = self.changes.write().await;
        changes.pushed = pushed;

        Ok(SyncResult {
            pushed: changes.pushed.len(),
            pulled: 0,
            conflicts: 0,
        })
    }

    pub async fn pull(&self) -> Result<SyncResult, ObsidianError> {
        let mut pulled = Vec::new();
        let mut conflicts = Vec::new();

        let vault_path = Path::new(&self.generator.vault_path);
        let entries = walkdir(vault_path)
            .into_iter()
            .filter(|e| e.path().extension().map(|ext| ext == "md").unwrap_or(false));

        for entry in entries {
            if let Ok(Some(annotation)) = self.generator.read_existing_annotation(
                entry.path().strip_prefix(vault_path).unwrap().to_str().unwrap_or("")
            ) {
                let frontmatter = self.parse_frontmatter(&entry.path())?;
                let element_id = frontmatter.get("leankg_id").cloned().unwrap_or_default();
                
                if element_id.is_empty() {
                    continue;
                }

                if let Some(existing) = db::get_business_logic(&self.db, &element_id)
                    .map_err(|e| ObsidianError::IoError(e.to_string()))? {
                    
                    if existing.description != annotation && !annotation.is_empty() {
                        conflicts.push(ConflictInfo {
                            element_id: element_id.clone(),
                            local_annotation: existing.description.clone(),
                            remote_annotation: annotation.clone(),
                        });
                    }
                } else {
                    db::create_business_logic(&self.db, &element_id, &annotation, None, None)
                        .map_err(|e| ObsidianError::IoError(e.to_string()))?;
                    pulled.push(element_id);
                }
            }
        }

        let pulled_count = pulled.len();
        let conflicts_count = conflicts.len();

        let mut changes = self.changes.write().await;
        changes.pulled = pulled;
        changes.conflicts = conflicts;

        Ok(SyncResult {
            pushed: 0,
            pulled: pulled_count,
            conflicts: conflicts_count,
        })
    }

    fn parse_frontmatter(&self, path: &Path) -> Result<HashMap<String, String>, ObsidianError> {
        let content = std::fs::read_to_string(path)
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        let mut result = HashMap::new();
        let mut in_frontmatter = false;

        for line in content.lines() {
            if line.trim() == "---" {
                if in_frontmatter {
                    break;
                }
                in_frontmatter = true;
                continue;
            }
            if in_frontmatter {
                if let Some((key, value)) = line.split_once(':') {
                    result.insert(key.trim().to_string(), value.trim().to_string());
                }
            }
        }

        Ok(result)
    }

    pub async fn status(&self) -> Result<VaultStatus, ObsidianError> {
        let vault_path = Path::new(&self.generator.vault_path);
        
        if !vault_path.exists() {
            return Ok(VaultStatus {
                initialized: false,
                note_count: 0,
                last_sync: None,
            });
        }

        let count = walkdir(vault_path)
            .into_iter()
            .filter(|e| e.path().extension().map(|ext| ext == "md").unwrap_or(false))
            .count();

        Ok(VaultStatus {
            initialized: true,
            note_count: count,
            last_sync: None,
        })
    }

    pub fn init(&self) -> Result<(), ObsidianError> {
        let vault_path = Path::new(&self.generator.vault_path);
        std::fs::create_dir_all(vault_path)
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        let readme_content = r#"# LeanKG Obsidian Vault

This vault is managed by LeanKG. Notes in `.leankg/obsidian/vault/` are auto-generated from LeanKG's knowledge graph.

## Sync Commands

- `leankg obsidian push` - Generate notes from LeanKG database
- `leankg obsidian pull` - Import annotation edits back to LeanKG
- `leankg obsidian watch` - Watch for changes and auto-sync

## Frontmatter Fields

- `leankg_id` - Unique identifier for the code element
- `leankg_type` - Element type (function, file, class, etc.)
- `leankg_file` - Source file path
- `leankg_line` - Line range in source file
- `leankg_relationships` - List of related elements
- `leankg_annotation` - Editable annotation description

## Notes

- LeanKG is the source of truth
- `push` overwrites `leankg_*` frontmatter fields
- `pull` imports only `leankg_annotation` back to LeanKG
- Your custom notes in note bodies are never overwritten
"#;

        let readme_path = vault_path.join("README.md");
        std::fs::write(&readme_path, readme_content)
            .map_err(|e| ObsidianError::IoError(e.to_string()))?;

        Ok(())
    }
}

fn walkdir(path: &Path) -> Vec<std::fs::DirEntry> {
    let mut results = Vec::new();
    if let Ok(entries) = std::fs::read_dir(path) {
        for entry in entries.flatten() {
            if entry.file_type().map(|ft| ft.is_dir()).unwrap_or(false) {
                results.extend(walkdir(&entry.path()));
            } else {
                results.push(entry);
            }
        }
    }
    results
}

#[derive(Debug)]
pub struct SyncResult {
    pub pushed: usize,
    pub pulled: usize,
    pub conflicts: usize,
}

#[derive(Debug)]
pub struct VaultStatus {
    pub initialized: bool,
    pub note_count: usize,
    pub last_sync: Option<String>,
}
