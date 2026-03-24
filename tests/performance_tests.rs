#[cfg(test)]
mod performance_tests {
    use leankg::db::models::{CodeElement, Relationship};
    use leankg::db::schema;
    use leankg::graph::query::GraphEngine;
    use leankg::indexer::ParserManager;
    use std::time::{Duration, Instant};
    use tempfile::TempDir;

    fn create_test_elements(count: usize, file_path: &str) -> Vec<CodeElement> {
        (0..count)
            .map(|i| CodeElement {
                qualified_name: format!("{}::func_{}", file_path, i),
                element_type: "function".to_string(),
                name: format!("func_{}", i),
                file_path: file_path.to_string(),
                line_start: i as u32 * 10,
                line_end: i as u32 * 10 + 5,
                language: "go".to_string(),
                parent_qualified: None,
                metadata: serde_json::json!({}),
            })
            .collect()
    }

    fn create_test_relationships(count: usize, source: &str) -> Vec<Relationship> {
        (0..count)
            .map(|i| Relationship {
                id: None,
                source_qualified: source.to_string(),
                target_qualified: format!("dep_{}.go", i),
                rel_type: "imports".to_string(),
                metadata: serde_json::json!({}),
            })
            .collect()
    }

    #[tokio::test]
    async fn test_batch_insert_performance() {
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join("test_batch.db");
        let db = schema::init_db(&db_path).await.unwrap();
        let graph = GraphEngine::new(db);

        let element_counts = [10, 50, 100, 500];

        for count in element_counts {
            let elements = create_test_elements(count, "test.go");

            let start = Instant::now();
            graph.insert_elements(&elements).await.unwrap();
            let elapsed = start.elapsed();

            let rate = count as f64 / elapsed.as_secs_f64();
            println!(
                "Batch insert {} elements: {:?} ({:.2} elements/sec)",
                count, elapsed, rate
            );

            assert!(
                elapsed < Duration::from_secs(5),
                "Batch insert took too long"
            );
        }
    }

    #[tokio::test]
    async fn test_batch_relationship_insert_performance() {
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join("test_rel_batch.db");
        let db = schema::init_db(&db_path).await.unwrap();
        let graph = GraphEngine::new(db);

        let rel_counts = [10, 50, 100, 500];

        for count in rel_counts {
            let relationships = create_test_relationships(count, "source.go");

            let start = Instant::now();
            graph.insert_relationships(&relationships).await.unwrap();
            let elapsed = start.elapsed();

            let rate = count as f64 / elapsed.as_secs_f64();
            println!(
                "Batch insert {} relationships: {:?} ({:.2} rels/sec)",
                count, elapsed, rate
            );

            assert!(
                elapsed < Duration::from_secs(5),
                "Batch relationship insert took too long"
            );
        }
    }

    #[tokio::test]
    async fn test_query_cache_hit_rate() {
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join("test_cache.db");
        let db = schema::init_db(&db_path).await.unwrap();
        let graph = GraphEngine::new(db);

        let elements = create_test_elements(10, "cache_test.go");
        graph.insert_elements(&elements).await.unwrap();

        let start = Instant::now();
        for _ in 0..100 {
            let _ = graph.get_dependencies("cache_test.go").await;
        }
        let elapsed = start.elapsed();

        let rate = 100f64 / elapsed.as_secs_f64();
        println!(
            "100 cached queries: {:?} ({:.2} queries/sec)",
            elapsed, rate
        );

        assert!(
            elapsed < Duration::from_millis(500),
            "Cached queries too slow"
        );
    }

    #[tokio::test]
    async fn test_parser_reuse_performance() {
        let mut manager = ParserManager::new();
        manager.init_parsers().unwrap();

        let source = b"
            package main
            
            func foo() {}
            func bar() {}
            func baz() {}
        ";

        let iterations = 1000;

        let start = Instant::now();
        for _ in 0..iterations {
            let parser = manager.get_parser_for_language("go").unwrap();
            let _ = parser.parse(source, None);
        }
        let elapsed = start.elapsed();

        let rate = iterations as f64 / elapsed.as_secs_f64();
        println!(
            "{} parser reuses: {:?} ({:.2} parses/sec)",
            iterations, elapsed, rate
        );

        assert!(elapsed < Duration::from_secs(30), "Parser reuse too slow");
    }

    #[tokio::test]
    async fn test_concurrent_cache_access() {
        use tokio::task;

        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join("test_concurrent.db");
        let db = schema::init_db(&db_path).await.unwrap();
        let graph = GraphEngine::new(db);

        let elements = create_test_elements(10, "concurrent.go");
        graph.insert_elements(&elements).await.unwrap();

        let mut handles = vec![];
        for i in 0..10 {
            let graph_clone = graph.clone();
            handles.push(task::spawn(async move {
                for j in 0..100 {
                    let path = format!("concurrent_{}.go", (i * 100 + j) % 10);
                    let _ = graph_clone.get_dependencies(&path).await;
                }
            }));
        }

        let start = Instant::now();
        for h in handles {
            let _ = h.await;
        }
        let elapsed = start.elapsed();

        println!("1000 concurrent cache accesses: {:?}", elapsed);
        assert!(
            elapsed < Duration::from_secs(10),
            "Concurrent access too slow"
        );
    }

    #[tokio::test]
    async fn test_memory_usage_batch_operations() {
        let temp_dir = TempDir::new().unwrap();
        let db_path = temp_dir.path().join("test_memory.db");
        let db = schema::init_db(&db_path).await.unwrap();
        let graph = GraphEngine::new(db);

        for batch in 0..10 {
            let elements: Vec<CodeElement> = (0..100)
                .map(|i| CodeElement {
                    qualified_name: format!("batch{}_func_{}", batch, i),
                    element_type: "function".to_string(),
                    name: format!("func_{}", i),
                    file_path: format!("batch{}.go", batch),
                    line_start: i as u32,
                    line_end: i as u32 + 5,
                    language: "go".to_string(),
                    parent_qualified: None,
                    metadata: serde_json::json!({}),
                })
                .collect();

            graph.insert_elements(&elements).await.unwrap();
        }

        let all_elements = graph.all_elements().await.unwrap();
        assert_eq!(all_elements.len(), 1000);

        let relationships: Vec<Relationship> = (0..1000)
            .map(|i| Relationship {
                id: None,
                source_qualified: format!("batch{}.go", i % 10),
                target_qualified: format!("dep_{}.go", i),
                rel_type: "imports".to_string(),
                metadata: serde_json::json!({}),
            })
            .collect();

        graph.insert_relationships(&relationships).await.unwrap();

        let all_rels = graph.all_relationships().await.unwrap();
        assert_eq!(all_rels.len(), 1000);
    }
}
