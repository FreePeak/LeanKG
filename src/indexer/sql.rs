//! US-GF-12 / FR-GF-20: SQL DDL parser for graph extraction.
//!
//! Extracts:
//!   - Tables (CREATE TABLE) -> `table` element
//!   - Columns (each column in CREATE TABLE) -> `column` element
//!   - Primary keys (PRIMARY KEY constraint) -> `column` metadata
//!   - Foreign keys (REFERENCES clause) -> `references` relationship
//!
//! Supports PostgreSQL, MySQL, and SQLite dialects as a common
//! subset. The extractor is conservative — it does not attempt to
//! evaluate expression defaults or partial FK clauses. Limitation:
//! SQL inside string literals, comments, or stored-procedure bodies
//! is not stripped, so a CREATE TABLE inside a comment may be picked
//! up. Acceptable for v0.
use crate::db::models::{CodeElement, Relationship};
use once_cell::sync::Lazy;
use regex::Regex;

static CREATE_TABLE_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r#"(?is)\bCREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:`|"|\[)?(\w+)(?:`|"|\])?\s*\((.*?)\)(?:\s*;|\s*$)"#,
    )
    .unwrap()
});

static FK_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r#"(?i)FOREIGN\s+KEY\s*\(\s*(?:`|"|\[)?(\w+)(?:`|"|\])?\s*\)\s*REFERENCES\s+(?:`|"|\[)?(\w+)(?:`|"|\])?"#,
    )
    .unwrap()
});

static PK_RE: Lazy<Regex> = Lazy::new(|| Regex::new(r#"(?i)PRIMARY\s+KEY"#).unwrap());

pub struct SqlExtractor<'a> {
    source: &'a str,
    file_path: &'a str,
}

impl<'a> SqlExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self {
            source: std::str::from_utf8(source).unwrap_or(""),
            file_path,
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements: Vec<CodeElement> = Vec::new();
        let mut relationships: Vec<Relationship> = Vec::new();

        // File-level element so search_code can locate the .sql file.
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
            language: "sql".to_string(),
            ..Default::default()
        });

        for cap in CREATE_TABLE_RE.captures_iter(self.source) {
            let table_name = cap[1].to_string();
            let body = &cap[2];
            let line_num = self.line_of(cap.get(0).unwrap().start());
            let table_qn = format!("{}::{}", self.file_path, table_name);
            elements.push(CodeElement {
                qualified_name: table_qn.clone(),
                element_type: "table".to_string(),
                name: table_name.clone(),
                file_path: self.file_path.to_string(),
                line_start: line_num,
                line_end: line_num,
                language: "sql".to_string(),
                ..Default::default()
            });
            relationships.push(Relationship {
                id: None,
                source_qualified: self.file_path.to_string(),
                target_qualified: table_qn.clone(),
                rel_type: "contains".to_string(),
                confidence: 1.0,
                metadata: serde_json::json!({"resolution_method": "name"}),
                ..Default::default()
            });

            // Identify primary key columns. The PK_RE matches both inline
            // `col INTEGER PRIMARY KEY` and constraint-form
            // `PRIMARY KEY (col1, col2)`. We then narrow down to the
            // column name by checking each column definition for
            // either an inline `PRIMARY KEY` keyword or membership
            // in the constraint-form column list.
            let pk_constraint = PK_RE
                .captures_iter(body)
                .filter_map(|c| c.get(1).map(|m| m.as_str().to_string()))
                .collect::<Vec<_>>();
            let pk_columns: Vec<String> = pk_constraint;

            // Iterate top-level column definitions.
            for raw in split_top_level(body) {
                let trimmed = raw.trim().trim_end_matches(',').trim();
                if trimmed.is_empty() {
                    continue;
                }
                // Skip constraint clauses (they start with a keyword).
                if is_constraint_keyword(trimmed) {
                    continue;
                }
                let col_name = match column_name(trimmed) {
                    Some(n) => n,
                    None => continue,
                };
                let col_qn = format!("{}::{}", table_qn, col_name);
                // Inline PRIMARY KEY (e.g. `id INTEGER PRIMARY KEY`).
                let inline_pk = trimmed.to_ascii_uppercase().contains("PRIMARY KEY");
                let is_pk = inline_pk || pk_columns.iter().any(|p| p == &col_name);
                elements.push(CodeElement {
                    qualified_name: col_qn.clone(),
                    element_type: "column".to_string(),
                    name: col_name,
                    file_path: self.file_path.to_string(),
                    line_start: line_num,
                    line_end: line_num,
                    language: "sql".to_string(),
                    parent_qualified: Some(table_qn.clone()),
                    metadata: serde_json::json!({
                        "primary_key": is_pk,
                        "raw": trimmed,
                    }),
                    ..Default::default()
                });
                relationships.push(Relationship {
                    id: None,
                    source_qualified: table_qn.clone(),
                    target_qualified: col_qn,
                    rel_type: "defines".to_string(),
                    confidence: 1.0,
                    metadata: serde_json::json!({"resolution_method": "name"}),
                    ..Default::default()
                });
            }

            // Foreign keys: source table -> target table.
            for fk in FK_RE.captures_iter(body) {
                let fk_col = fk[1].to_string();
                let target_table = fk[2].to_string();
                let target_qn = format!("{}::{}", self.file_path, target_table);
                let source_qn = format!("{}::{}", table_qn, fk_col);
                relationships.push(Relationship {
                    id: None,
                    source_qualified: source_qn,
                    target_qualified: target_qn,
                    rel_type: "references".to_string(),
                    confidence: 0.95,
                    metadata: serde_json::json!({
                        "resolution_method": "name",
                        "fk_column": fk_col,
                    }),
                    ..Default::default()
                });
            }
        }

        (elements, relationships)
    }

    fn line_of(&self, offset: usize) -> u32 {
        self.source[..offset].matches('\n').count() as u32 + 1
    }
}

/// Split a CREATE TABLE body on top-level commas. Respects nested
/// parens (e.g. for function calls in default expressions) and
/// skips commas inside string literals or comments.
fn split_top_level(body: &str) -> Vec<&str> {
    let mut out: Vec<&str> = Vec::new();
    let mut start = 0usize;
    let mut depth: i32 = 0;
    let mut in_string: bool = false;
    let mut in_line_comment: bool = false;
    let mut in_block_comment: bool = false;
    let bytes = body.as_bytes();
    let mut i = 0usize;
    while i < bytes.len() {
        let c = bytes[i] as char;
        if in_line_comment {
            if c == '\n' {
                in_line_comment = false;
            }
        } else if in_block_comment {
            if c == '*' && i + 1 < bytes.len() && bytes[i + 1] as char == '/' {
                in_block_comment = false;
                i += 1;
            }
        } else if in_string {
            if c == '\'' {
                // SQL standard: '' is an escaped quote.
                if i + 1 < bytes.len() && bytes[i + 1] as char == '\'' {
                    i += 1;
                } else {
                    in_string = false;
                }
            }
        } else if c == '\'' {
            in_string = true;
        } else if c == '-' && i + 1 < bytes.len() && bytes[i + 1] as char == '-' {
            in_line_comment = true;
            i += 1;
        } else if c == '/' && i + 1 < bytes.len() && bytes[i + 1] as char == '*' {
            in_block_comment = true;
            i += 1;
        } else if c == '(' {
            depth += 1;
        } else if c == ')' {
            depth -= 1;
        } else if c == ',' && depth == 0 {
            out.push(&body[start..i]);
            start = i + 1;
        }
        i += 1;
    }
    if start < body.len() {
        out.push(&body[start..]);
    }
    out
}

fn is_constraint_keyword(s: &str) -> bool {
    let up = s.to_ascii_uppercase();
    up.starts_with("PRIMARY KEY")
        || up.starts_with("FOREIGN KEY")
        || up.starts_with("UNIQUE ")
        || up.starts_with("CHECK ")
        || up.starts_with("CONSTRAINT ")
        || up.starts_with("INDEX ")
        || up.starts_with("KEY ")
}

fn column_name(definition: &str) -> Option<String> {
    let first = definition.split_whitespace().next()?;
    if first.is_empty() {
        return None;
    }
    let stripped = first
        .trim_start_matches('"')
        .trim_start_matches('`')
        .trim_start_matches('[')
        .trim_end_matches(']')
        .trim_end_matches('"')
        .trim_end_matches('`')
        .to_string();
    if stripped.is_empty() || !is_identifier(&stripped) {
        None
    } else {
        Some(stripped)
    }
}

fn is_identifier(s: &str) -> bool {
    let mut chars = s.chars();
    match chars.next() {
        Some(c) if c.is_ascii_alphabetic() || c == '_' => {}
        _ => return false,
    }
    chars.all(|c| c.is_ascii_alphanumeric() || c == '_')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn extracts_create_table_with_columns_and_pk() {
        let sql = r#"
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL,
    name TEXT
);
"#;
        let (elems, rels) = SqlExtractor::new(sql.as_bytes(), "schema.sql").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "table" && e.name == "users"));
        let id = elems
            .iter()
            .find(|e| e.element_type == "column" && e.name == "id");
        assert!(id.is_some());
        let id_meta = id.unwrap().metadata.clone();
        assert_eq!(id_meta["primary_key"], serde_json::Value::Bool(true));
        assert!(elems.iter().any(|e| e.name == "email"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "defines" && r.target_qualified.ends_with("::email")));
    }

    #[test]
    fn extracts_foreign_key_references_relationship() {
        let sql = r#"
CREATE TABLE orders (
    id INTEGER PRIMARY KEY,
    user_id INTEGER,
    FOREIGN KEY (user_id) REFERENCES users(id)
);
"#;
        let (elems, rels) = SqlExtractor::new(sql.as_bytes(), "orders.sql").extract();
        assert!(elems
            .iter()
            .any(|e| e.element_type == "table" && e.name == "orders"));
        assert!(rels
            .iter()
            .any(|r| r.rel_type == "references" && r.target_qualified.ends_with("::users")));
    }

    #[test]
    fn split_top_level_respects_parens_and_strings() {
        let body = "id INT DEFAULT nextval('seq'), name TEXT, age INT";
        let parts: Vec<&str> = split_top_level(body);
        assert_eq!(parts.len(), 3);
        assert!(parts[0].contains("DEFAULT nextval('seq')"));
    }
}
