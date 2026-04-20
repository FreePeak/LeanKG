use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

/// Extractor for Room database patterns from Kotlin files
pub struct AndroidRoomExtractor<'a> {
    source: &'a [u8],
    file_path: &'a str,
}

impl<'a> AndroidRoomExtractor<'a> {
    pub fn new(source: &'a [u8], file_path: &'a str) -> Self {
        Self { source, file_path }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let content = match std::str::from_utf8(self.source) {
            Ok(s) => s,
            Err(_) => {
                eprintln!("warn: non-UTF-8 content in {}, skipping", self.file_path);
                return (Vec::new(), Vec::new());
            }
        };
        let mut elements = Vec::new();
        let mut relationships = Vec::new();

        // Extract entities
        let entities = self.extract_entities(content);
        for entity in &entities {
            elements.push(entity.clone());
        }

        // Extract DAOs
        let daos = self.extract_daos(content);
        for dao in &daos {
            elements.push(dao.clone());
        }

        // Extract databases
        let databases = self.extract_databases(content);
        for db in &databases {
            elements.push(db.clone());
        }

        // Extract foreign key relationships
        let fk_rels = self.extract_foreign_keys(content, &entities);
        relationships.extend(fk_rels);

        // Extract database content relationships
        let db_rels = self.extract_database_relationships(content, &databases, &entities, &daos);
        relationships.extend(db_rels);

        (elements, relationships)
    }

    fn extract_entities(&self, content: &str) -> Vec<CodeElement> {
        let mut entities = Vec::new();
        // Match @Entity annotation followed by data class
        // Use (?s) for dotall mode, .*? for non-greedy match of annotation params
        let re = Regex::new(r"(?s)@Entity\s*(?:\(.*?\))?\s*data\s+class\s+(\w+)").unwrap();

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let entity_name = name_match.as_str();
                let qualified_name = format!("{}::RoomEntity:{}", self.file_path, entity_name);

                entities.push(CodeElement {
                    qualified_name,
                    element_type: "room_entity".to_string(),
                    name: entity_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({"class_name": entity_name}),
                    ..Default::default()
                });
            }
        }

        entities
    }

    fn extract_daos(&self, content: &str) -> Vec<CodeElement> {
        let mut daos = Vec::new();
        let re = Regex::new(r"@Dao\s*\n?\s*\n?(?:interface|class)\s+(\w+)").unwrap();

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let dao_name = name_match.as_str();
                let qualified_name = format!("{}::RoomDao:{}", self.file_path, dao_name);

                daos.push(CodeElement {
                    qualified_name,
                    element_type: "room_dao".to_string(),
                    name: dao_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({"interface_name": dao_name}),
                    ..Default::default()
                });
            }
        }

        daos
    }

    fn extract_databases(&self, content: &str) -> Vec<CodeElement> {
        let mut databases = Vec::new();
        let re = Regex::new(r"@Database\s*\([^)]*\)\s*\n?\s*abstract\s+class\s+(\w+)").unwrap();

        for cap in re.captures_iter(content) {
            if let Some(name_match) = cap.get(1) {
                let db_name = name_match.as_str();
                let qualified_name = format!("{}::RoomDatabase:{}", self.file_path, db_name);

                databases.push(CodeElement {
                    qualified_name,
                    element_type: "room_database".to_string(),
                    name: db_name.to_string(),
                    file_path: self.file_path.to_string(),
                    language: "kotlin".to_string(),
                    metadata: serde_json::json!({"class_name": db_name}),
                    ..Default::default()
                });
            }
        }

        databases
    }

    fn find_class_body_end(content: &str, class_start: usize) -> usize {
        let after = &content[class_start..];
        let mut depth = 0i32;
        let mut found_open = false;
        for (i, ch) in after.char_indices() {
            match ch {
                '{' => { depth += 1; found_open = true; }
                '}' => {
                    depth -= 1;
                    if found_open && depth == 0 {
                        return class_start + i + 1;
                    }
                }
                _ => {}
            }
        }
        content.len()
    }

    fn extract_foreign_keys(&self, content: &str, entities: &[CodeElement]) -> Vec<Relationship> {
        let mut relationships = Vec::new();
        let fk_re = Regex::new(r"ForeignKey\s*\(\s*entity\s*=\s*(\w+)::class[^)]+parentColumns\s*=\s*\[(\w+)\][^)]+childColumns\s*=\s*\[(\w+)\]").unwrap();

        for entity in entities {
            let entity_pattern = format!(r"(?:data\s+)?class\s+{}", regex::escape(&entity.name));
            if let Ok(re) = Regex::new(&entity_pattern) {
                if let Some(mat) = re.find(content) {
                    let entity_start = mat.start();
                    let entity_end = Self::find_class_body_end(content, entity_start);
                    let entity_content = &content[entity_start..entity_end];

                    for cap in fk_re.captures_iter(entity_content) {
                        if let Some(ref_entity) = cap.get(1) {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: format!("{}::RoomEntity:{}", self.file_path, entity.name),
                                target_qualified: format!("__room_entity__{}", ref_entity.as_str()),
                                rel_type: "room_entity_has_foreign_key".to_string(),
                                confidence: 0.9,
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                }
            }
        }

        relationships
    }

    fn extract_database_relationships(
        &self,
        content: &str,
        databases: &[CodeElement],
        entities: &[CodeElement],
        daos: &[CodeElement],
    ) -> Vec<Relationship> {
        let mut relationships = Vec::new();

        // Parse entities array from @Database annotation
        let entities_array_re = Regex::new(r"entities\s*=\s*\[([^\]]+)\]").unwrap();
        let entity_class_re = Regex::new(r"(\w+)::class").unwrap();

        for db in databases {
            // Find entities in database annotation
            if let Some(cap) = entities_array_re.captures(content) {
                if let Some(array_match) = cap.get(1) {
                    for entity_cap in entity_class_re.captures_iter(array_match.as_str()) {
                        if let Some(entity_name) = entity_cap.get(1) {
                            let db_qualified = &db.qualified_name;
                            let entity_qualified = format!("{}::RoomEntity:{}", self.file_path, entity_name.as_str());

                            relationships.push(Relationship {
                                id: None,
                                source_qualified: db_qualified.clone(),
                                target_qualified: entity_qualified,
                                rel_type: "room_database_contains_entity".to_string(),
                                confidence: 1.0,
                                metadata: serde_json::json!({}),
                            });
                        }
                    }
                }
            }

            // Link DAOs to database (heuristic: DAOs referenced in same file as database)
            for dao in daos {
                // Simple heuristic: if DAO is in same file as Database, assume relationship
                relationships.push(Relationship {
                    id: None,
                    source_qualified: db.qualified_name.clone(),
                    target_qualified: dao.qualified_name.clone(),
                    rel_type: "room_database_contains_dao".to_string(),
                    confidence: 0.7,
                    metadata: serde_json::json!({}),
                });
            }
        }

        // Extract DAO queries (look for @Query annotation with entity names)
        for dao in daos {
            let query_re = Regex::new(r#"@Query\s*\(\s*"([^"]+)"\s*\)"#).unwrap();
            for cap in query_re.captures_iter(content) {
                if let Some(query) = cap.get(1) {
                    let query_str = query.as_str();
                    // Look for FROM clauses referencing entities
                    let from_re = Regex::new(r"(?i)FROM\s+(\w+)").unwrap();
                    if let Some(from_cap) = from_re.captures(query_str) {
                        if let Some(table_name) = from_cap.get(1) {
                            relationships.push(Relationship {
                                id: None,
                                source_qualified: dao.qualified_name.clone(),
                                target_qualified: format!("{}::RoomEntity:{}", self.file_path, table_name.as_str()),
                                rel_type: "room_dao_queries_entity".to_string(),
                                confidence: 0.8,
                                metadata: serde_json::json!({"query": query_str}),
                            });
                        }
                    }
                }
            }
        }

        relationships
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_entity() {
        let source = r#"
            @Entity(tableName = "channels")
            data class ChannelEntity(
                @PrimaryKey val id: Long,
                val name: String
            )
        "#;
        let extractor = AndroidRoomExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _) = extractor.extract();

        let entities: Vec<_> = elements.iter().filter(|e| e.element_type == "room_entity").collect();
        assert_eq!(entities.len(), 1);
        assert_eq!(entities[0].name, "ChannelEntity");
    }

    #[test]
    fn test_extract_dao() {
        let source = r#"
            @Dao
            interface ChannelDao {
                @Query("SELECT * FROM channels")
                fun getAll(): List<ChannelEntity>
            }
        "#;
        let extractor = AndroidRoomExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _) = extractor.extract();

        let daos: Vec<_> = elements.iter().filter(|e| e.element_type == "room_dao").collect();
        assert_eq!(daos.len(), 1);
        assert_eq!(daos[0].name, "ChannelDao");
    }

    #[test]
    fn test_extract_database() {
        let source = r#"
            @Database(entities = [ChannelEntity::class, VodEntity::class], version = 1)
            abstract class TvDatabase : RoomDatabase()
        "#;
        let extractor = AndroidRoomExtractor::new(source.as_bytes(), "./test.kt");
        let (elements, _) = extractor.extract();

        let dbs: Vec<_> = elements.iter().filter(|e| e.element_type == "room_database").collect();
        assert_eq!(dbs.len(), 1);
        assert_eq!(dbs[0].name, "TvDatabase");
    }
}
