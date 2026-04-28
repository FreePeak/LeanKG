use leankg::db::models::{CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use tempfile::TempDir;

fn with_test_db<F>(callback: F)
where
    F: FnOnce(leankg::db::CozoDb, &GraphEngine, &TempDir),
{
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    callback(db, &graph, &tmp);
}

#[cfg(test)]
mod db_parameterized_queries_tests {
    use super::*;

    #[test]
    fn test_create_business_logic_basic() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                "Test description",
                Some("US-001"),
                Some("F-001"),
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert_eq!(bl.element_qualified, "test::func");
            assert_eq!(bl.description, "Test description");
            assert_eq!(bl.user_story_id.as_deref(), Some("US-001"));
            assert_eq!(bl.feature_id.as_deref(), Some("F-001"));
        });
    }

    #[test]
    fn test_create_business_logic_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                r#"Description with "quotes" and 'apostrophes' and \ backslash"#,
                Some(r#"User"Story::Id"#),
                Some(r#"Feature-Id::With::Colons"#),
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert_eq!(bl.element_qualified, "test::func");
            assert_eq!(
                bl.description,
                r#"Description with "quotes" and 'apostrophes' and \ backslash"#
            );
            assert_eq!(bl.user_story_id.as_deref(), Some(r#"User"Story::Id"#));
            assert_eq!(
                bl.feature_id.as_deref(),
                Some(r#"Feature-Id::With::Colons"#)
            );
        });
    }

    #[test]
    fn test_create_business_logic_with_sql_injection_attempt() {
        with_test_db(|db, _graph, _tmp| {
            let injection_attempt = r#""; DROP TABLE code_elements; --"#;
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                "Normal description",
                Some(injection_attempt),
                Some("F-001"),
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert_eq!(bl.user_story_id.as_deref(), Some(injection_attempt));
        });
    }

    #[test]
    fn test_create_business_logic_with_null_user_story() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                "Test description",
                None,
                Some("F-001"),
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert!(bl.user_story_id.is_none());
            assert_eq!(bl.feature_id.as_deref(), Some("F-001"));
        });
    }

    #[test]
    fn test_create_business_logic_with_null_feature() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                "Test description",
                Some("US-001"),
                None,
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert_eq!(bl.user_story_id.as_deref(), Some("US-001"));
            assert!(bl.feature_id.is_none());
        });
    }

    #[test]
    fn test_create_business_logic_with_both_null() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::create_business_logic(
                &db,
                "test::func",
                "Test description",
                None,
                None,
            );
            assert!(result.is_ok());
            let bl = result.unwrap();
            assert!(bl.user_story_id.is_none());
            assert!(bl.feature_id.is_none());
        });
    }

    #[test]
    fn test_get_business_logic_existing() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(
                &db,
                "test::func",
                "Test description",
                Some("US-001"),
                Some("F-001"),
            )
            .unwrap();
            let result = leankg::db::get_business_logic(&db, "test::func");
            assert!(result.is_ok());
            let bl = result.unwrap().unwrap();
            assert_eq!(bl.element_qualified, "test::func");
            assert_eq!(bl.description, "Test description");
        });
    }

    #[test]
    fn test_get_business_logic_nonexistent() {
        with_test_db(|db, _graph, _tmp| {
            let result = leankg::db::get_business_logic(&db, "nonexistent::func");
            assert!(result.is_ok());
            assert!(result.unwrap().is_none());
        });
    }

    #[test]
    fn test_get_business_logic_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            let special_name = r#"test::func::with "quotes""#;
            leankg::db::create_business_logic(
                &db,
                special_name,
                "Test description",
                Some("US-001"),
                None,
            )
            .unwrap();
            let result = leankg::db::get_business_logic(&db, special_name);
            assert!(result.is_ok());
            assert!(result.unwrap().is_some());
        });
    }

    #[test]
    fn test_update_business_logic_existing() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(
                &db,
                "test::func",
                "Original description",
                Some("US-001"),
                Some("F-001"),
            )
            .unwrap();
            let result = leankg::db::update_business_logic(
                &db,
                "test::func",
                "Updated description",
                Some("US-002"),
                Some("F-002"),
            );
            assert!(result.is_ok());
            let bl = result.unwrap().unwrap();
            assert_eq!(bl.description, "Updated description");
            assert_eq!(bl.user_story_id.as_deref(), Some("US-002"));
            assert_eq!(bl.feature_id.as_deref(), Some("F-002"));
        });
    }

    #[test]
    fn test_update_business_logic_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(&db, "test::func", "Original", Some("US-001"), None)
                .unwrap();
            let special_chars = r#"New "description" with 'quotes' and \ backslash"#;
            let result = leankg::db::update_business_logic(
                &db,
                "test::func",
                special_chars,
                Some(r#"User"Story"#),
                None,
            );
            assert!(result.is_ok());
            let bl = result.unwrap().unwrap();
            assert_eq!(bl.description, special_chars);
        });
    }

    #[test]
    fn test_get_by_user_story() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(&db, "func1", "Desc1", Some("US-001"), Some("F-001"))
                .unwrap();
            leankg::db::create_business_logic(&db, "func2", "Desc2", Some("US-001"), Some("F-001"))
                .unwrap();
            leankg::db::create_business_logic(&db, "func3", "Desc3", Some("US-002"), None).unwrap();
            let result = leankg::db::get_by_user_story(&db, "US-001");
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 2);
        });
    }

    #[test]
    fn test_get_by_user_story_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            let special_us = r#"User"Story::Id"#;
            leankg::db::create_business_logic(&db, "func1", "Desc", Some(special_us), None)
                .unwrap();
            let result = leankg::db::get_by_user_story(&db, special_us);
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 1);
        });
    }

    #[test]
    fn test_get_by_feature() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(&db, "func1", "Desc1", Some("US-001"), Some("F-001"))
                .unwrap();
            leankg::db::create_business_logic(&db, "func2", "Desc2", Some("US-002"), Some("F-001"))
                .unwrap();
            leankg::db::create_business_logic(&db, "func3", "Desc3", Some("US-003"), Some("F-002"))
                .unwrap();
            let result = leankg::db::get_by_feature(&db, "F-001");
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 2);
        });
    }

    #[test]
    fn test_get_by_feature_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            let special_feat = r#"Feature::"With"::Colons"#;
            leankg::db::create_business_logic(&db, "func1", "Desc", None, Some(special_feat))
                .unwrap();
            let result = leankg::db::get_by_feature(&db, special_feat);
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 1);
        });
    }

    #[test]
    fn test_search_business_logic_basic() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(
                &db,
                "func1",
                "Handle user authentication",
                Some("US-001"),
                None,
            )
            .unwrap();
            leankg::db::create_business_logic(
                &db,
                "func2",
                "Process payment transactions",
                Some("US-002"),
                None,
            )
            .unwrap();
            let result = leankg::db::search_business_logic(&db, "authentication");
            assert!(result.is_ok());
            let items = result.unwrap();
            assert_eq!(items.len(), 1);
            assert_eq!(items[0].description, "Handle user authentication");
        });
    }

    #[test]
    fn test_search_business_logic_case_insensitive() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(
                &db,
                "func1",
                "Handle USER Authentication",
                Some("US-001"),
                None,
            )
            .unwrap();
            let result = leankg::db::search_business_logic(&db, "authentication");
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 1);
        });
    }

    #[test]
    fn test_search_business_logic_with_special_chars() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(
                &db,
                "func1",
                r#"Process "quote" and 'apostrophe' data"#,
                Some("US-001"),
                None,
            )
            .unwrap();
            let result = leankg::db::search_business_logic(&db, "quote");
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 1);
        });
    }

    #[test]
    fn test_all_business_logic() {
        with_test_db(|db, _graph, _tmp| {
            leankg::db::create_business_logic(&db, "func1", "Desc1", Some("US-001"), None).unwrap();
            leankg::db::create_business_logic(&db, "func2", "Desc2", Some("US-002"), None).unwrap();
            let result = leankg::db::all_business_logic(&db);
            assert!(result.is_ok());
            assert_eq!(result.unwrap().len(), 2);
        });
    }
}

#[cfg(test)]
mod graph_batched_insert_tests {
    use super::*;

    fn create_code_element(name: &str, file_path: &str, line: u32) -> CodeElement {
        CodeElement {
            qualified_name: format!("{}::{}", file_path, name),
            element_type: "function".to_string(),
            name: name.to_string(),
            file_path: file_path.to_string(),
            line_start: line,
            line_end: line + 10,
            language: "rust".to_string(),
            parent_qualified: None,
            cluster_id: None,
            cluster_label: None,
            metadata: serde_json::json!({}),
        }
    }

    fn create_relationship(source: &str, target: &str) -> Relationship {
        Relationship {
            id: None,
            source_qualified: source.to_string(),
            target_qualified: target.to_string(),
            rel_type: "calls".to_string(),
            confidence: 0.9,
            metadata: serde_json::json!({}),
        }
    }

    #[test]
    fn test_insert_elements_empty() {
        with_test_db(|_db, graph, _tmp| {
            assert!(graph.insert_elements(&[]).is_ok());
        });
    }

    #[test]
    fn test_insert_elements_single() {
        with_test_db(|_db, graph, _tmp| {
            let elements = vec![create_code_element("test_func", "src/lib.rs", 1)];
            let result = graph.insert_elements(&elements);
            assert!(result.is_ok());
            let all = graph.all_elements().unwrap();
            assert_eq!(all.len(), 1);
            assert_eq!(all[0].name, "test_func");
        });
    }

    #[test]
    fn test_insert_elements_multiple() {
        with_test_db(|_db, graph, _tmp| {
            let elements = vec![
                create_code_element("func1", "src/lib.rs", 1),
                create_code_element("func2", "src/lib.rs", 10),
                create_code_element("func3", "src/lib.rs", 20),
            ];
            assert!(graph.insert_elements(&elements).is_ok());
            assert_eq!(graph.all_elements().unwrap().len(), 3);
        });
    }

    #[test]
    fn test_insert_elements_large_batch() {
        with_test_db(|_db, graph, _tmp| {
            let elements: Vec<_> = (0..1500)
                .map(|i| create_code_element(&format!("func_{}", i), "src/lib.rs", i * 10))
                .collect();
            assert!(graph.insert_elements(&elements).is_ok());
            assert_eq!(graph.all_elements().unwrap().len(), 1500);
        });
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_insert_elements_invalidates_cache_for_all_files() {
        use leankg::graph::cache::QueryCache;
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = init_db(db_path.as_path()).unwrap();

        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("file1.rs".to_string(), vec!["dep1".to_string()])
            .await;
        cache
            .set_dependencies("file2.rs".to_string(), vec!["dep2".to_string()])
            .await;
        cache
            .set_dependencies("file3.rs".to_string(), vec!["dep3".to_string()])
            .await;

        let graph = GraphEngine::with_cache(db, cache.clone());

        let elements = vec![
            create_code_element("func1", "file1.rs", 1),
            create_code_element("func2", "file2.rs", 10),
            create_code_element("func3", "file3.rs", 20),
        ];

        // When inserting batched elements for 3 distinct files...
        assert!(graph.insert_elements(&elements).is_ok());

        // We spawned invalidations asynchronously via global `OnceLock` runtime. Wait for propagation.
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        // Assert ALL 3 unique files' caches were strictly invalidated!
        assert!(
            cache.get_dependencies("file1.rs").await.is_none(),
            "file1.rs cache wasn't invalidated"
        );
        assert!(
            cache.get_dependencies("file2.rs").await.is_none(),
            "file2.rs cache wasn't invalidated"
        );
        assert!(
            cache.get_dependencies("file3.rs").await.is_none(),
            "file3.rs cache wasn't invalidated"
        );
    }

    #[test]
    fn test_insert_elements_with_metadata() {
        with_test_db(|_db, graph, _tmp| {
            let mut elem = create_code_element("test_func", "src/lib.rs", 1);
            elem.metadata = serde_json::json!({
                "complex": "metadata",
                "with": ["array", "values"],
                "nested": {"object": "structure"}
            });
            assert!(graph.insert_elements(&[elem]).is_ok());
        });
    }

    #[test]
    fn test_insert_elements_with_optional_fields() {
        with_test_db(|_db, graph, _tmp| {
            let mut elem = create_code_element("test_func", "src/lib.rs", 1);
            elem.parent_qualified = Some("src/lib.rs::Struct".to_string());
            elem.cluster_id = Some("cluster-1".to_string());
            elem.cluster_label = Some("TestCluster".to_string());
            assert!(graph.insert_elements(&[elem]).is_ok());
        });
    }

    #[test]
    fn test_insert_relationships_empty() {
        with_test_db(|_db, graph, _tmp| {
            assert!(graph.insert_relationships(&[]).is_ok());
        });
    }

    #[test]
    fn test_insert_relationships_single() {
        with_test_db(|_db, graph, _tmp| {
            let rels = vec![create_relationship(
                "src/lib.rs::func1",
                "src/lib.rs::func2",
            )];
            assert!(graph.insert_relationships(&rels).is_ok());
        });
    }

    #[test]
    fn test_insert_relationships_multiple() {
        with_test_db(|_db, graph, _tmp| {
            let rels = vec![
                create_relationship("src/lib.rs::func1", "src/lib.rs::func2"),
                create_relationship("src/lib.rs::func1", "src/lib.rs::func3"),
                create_relationship("src/lib.rs::func2", "src/lib.rs::func3"),
            ];
            assert!(graph.insert_relationships(&rels).is_ok());
        });
    }

    #[test]
    fn test_insert_relationships_large_batch() {
        with_test_db(|_db, graph, _tmp| {
            let rels: Vec<_> = (0..1500)
                .map(|i| {
                    create_relationship(&format!("src/lib.rs::caller{}", i), "src/lib.rs::callee")
                })
                .collect();
            assert!(graph.insert_relationships(&rels).is_ok());
        });
    }

    #[test]
    fn test_insert_relationships_with_metadata() {
        with_test_db(|_db, graph, _tmp| {
            let mut rel = create_relationship("src/lib.rs::func1", "src/lib.rs::func2");
            rel.metadata = serde_json::json!({
                "line_number": 42,
                "call_type": "direct",
                "tags": ["important", "core"]
            });
            assert!(graph.insert_relationships(&[rel]).is_ok());
        });
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_insert_relationships_invalidates_cache_for_all_sources() {
        use leankg::graph::cache::QueryCache;
        let tmp = TempDir::new().unwrap();
        let db_path = tmp.path().join("test.db");
        let db = init_db(db_path.as_path()).unwrap();

        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("src/caller1.rs".to_string(), vec!["dep1".to_string()])
            .await;
        cache
            .set_dependents("src/caller2.rs".to_string(), vec!["dep2".to_string()])
            .await;
        cache
            .set_dependents("src/caller3.rs".to_string(), vec!["dep3".to_string()])
            .await;

        let graph = GraphEngine::with_cache(db, cache.clone());

        let rels = vec![
            create_relationship("src/caller1.rs", "src/target.rs"),
            create_relationship("src/caller2.rs", "src/target.rs"),
            create_relationship("src/caller3.rs", "src/target.rs"),
        ];

        // Assert we successfully pushed to graph
        assert!(graph.insert_relationships(&rels).is_ok());

        // Wait for asynchronous global invalidation to hit
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        // Assert caches are cleared iteratively across all batch sources!
        assert!(
            cache.get_dependents("src/caller1.rs").await.is_none(),
            "src/caller1.rs cache wasn't invalidated"
        );
        assert!(
            cache.get_dependents("src/caller2.rs").await.is_none(),
            "src/caller2.rs cache wasn't invalidated"
        );
        assert!(
            cache.get_dependents("src/caller3.rs").await.is_none(),
            "src/caller3.rs cache wasn't invalidated"
        );
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_remove_elements_by_file_clears_cache() {
        use leankg::graph::cache::QueryCache;
        let tmp = TempDir::new().unwrap();
        let db = init_db(tmp.path().join("test.db").as_path()).unwrap();

        let cache = QueryCache::new(60, 100);
        cache
            .set_dependencies("src/victim.rs".to_string(), vec!["dep1".to_string()])
            .await;
        cache
            .set_dependencies("src/bystander.rs".to_string(), vec!["dep2".to_string()])
            .await;

        let graph = GraphEngine::with_cache(db, cache.clone());
        graph
            .insert_elements(&[create_code_element("func", "src/victim.rs", 1)])
            .unwrap();

        // insert_elements spawns async cache invalidation; wait for it then repopulate
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        cache
            .set_dependencies("src/victim.rs".to_string(), vec!["dep1".to_string()])
            .await;
        cache
            .set_dependencies("src/bystander.rs".to_string(), vec!["dep2".to_string()])
            .await;

        // Ensure cache is initially populated
        assert!(cache.get_dependencies("src/victim.rs").await.is_some());

        // Remove file
        if let Err(e) = graph.remove_elements_by_file("src/victim.rs") {
            panic!("remove_elements_by_file failed: {:?}", e);
        }

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        // Victim is wiped from DB and memory
        assert!(cache.get_dependencies("src/victim.rs").await.is_none());
        // Bystander remains
        assert!(cache.get_dependencies("src/bystander.rs").await.is_some());
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn test_remove_relationships_by_source_clears_cache() {
        use leankg::graph::cache::QueryCache;
        let tmp = TempDir::new().unwrap();
        let db = init_db(tmp.path().join("test.db").as_path()).unwrap();

        let cache = QueryCache::new(60, 100);
        cache
            .set_dependents("src/victim.rs".to_string(), vec!["dep1".to_string()])
            .await;

        let graph = GraphEngine::with_cache(db, cache.clone());
        graph
            .insert_relationships(&[create_relationship("src/victim.rs", "src/dest.rs")])
            .unwrap();

        // insert_relationships spawns async cache invalidation; wait for it then repopulate
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        cache
            .set_dependents("src/victim.rs".to_string(), vec!["dep1".to_string()])
            .await;

        assert!(cache.get_dependents("src/victim.rs").await.is_some());
        if let Err(e) = graph.remove_relationships_by_source("src/victim.rs") {
            panic!("remove_relationships_by_source failed: {:?}", e);
        }

        tokio::time::sleep(std::time::Duration::from_millis(50)).await;
        assert!(cache.get_dependents("src/victim.rs").await.is_none());
    }

    #[test]
    fn test_find_element_parameterized_injection() {
        with_test_db(|_db, graph, _tmp| {
            let malicious_name = "func_name\"; DROP TABLE code_elements; --";
            let element = create_code_element(malicious_name, "src/lib.rs", 1);
            graph.insert_elements(&[element]).unwrap();

            // Looking up the malicious string directly confirms strict parameterized binding handles the query safely
            let retrieved = graph
                .find_element(&format!("src/lib.rs::{}", malicious_name))
                .unwrap();
            assert!(retrieved.is_some());
            assert_eq!(retrieved.unwrap().name, malicious_name);
        });
    }
}
