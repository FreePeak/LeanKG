use leankg::db::models::{CodeElement, Relationship};
use leankg::db::schema::init_db;
use leankg::graph::GraphEngine;
use tempfile::TempDir;

fn with_test_graph<F>(callback: F)
where
    F: FnOnce(&GraphEngine, &TempDir),
{
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    callback(&graph, &tmp);
}

fn create_code_element(name: &str, file_path: &str, element_type: &str, lines: u32) -> CodeElement {
    CodeElement {
        qualified_name: format!("{}::{}", file_path, name),
        element_type: element_type.to_string(),
        name: name.to_string(),
        file_path: file_path.to_string(),
        line_start: 1,
        line_end: 1 + lines,
        language: "rust".to_string(),
        parent_qualified: None,
        cluster_id: None,
        cluster_label: None,
        metadata: serde_json::json!({}),
    }
}

#[test]
fn test_search_by_pattern() {
    with_test_graph(|graph, _| {
        graph.insert_elements(&[
            create_code_element("handle_user_auth", "src/auth.rs", "function", 10),
            create_code_element("process_payment_auth", "src/pay.rs", "function", 20),
            create_code_element("unrelated_func", "src/main.rs", "function", 5),
        ]).unwrap();

        // Standard substring search uses `escape_datalog` or `(?i)` under the hood
        let results = graph.search_by_pattern("auth").unwrap();
        assert_eq!(results.len(), 2);
        
        let found_names: Vec<String> = results.into_iter().map(|e| e.name).collect();
        assert!(found_names.contains(&"handle_user_auth".to_string()));
        assert!(found_names.contains(&"process_payment_auth".to_string()));
    });
}

#[test]
fn test_search_by_pattern_malicious_injection() {
    with_test_graph(|graph, _| {
        graph.insert_elements(&[
            create_code_element("regular_func", "src/main.rs", "function", 10),
        ]).unwrap();

        // Testing SQL injection boundaries in parameter formats natively bounded by escape functions
        let malicious_pattern = "auth\"; DROP TABLE code_elements; --";
        let results = graph.search_by_pattern(malicious_pattern).unwrap();
        assert!(results.is_empty(), "Injection should not cause failure but securely match zero elements");
    });
}

#[test]
fn test_search_by_type() {
    with_test_graph(|graph, _| {
        graph.insert_elements(&[
            create_code_element("MyStruct", "src/mod.rs", "struct", 10),
            create_code_element("my_func", "src/mod.rs", "function", 20),
            create_code_element("MyEnum", "src/mod.rs", "enum", 5),
        ]).unwrap();

        let structs = graph.search_by_type("struct").unwrap();
        assert_eq!(structs.len(), 1);
        assert_eq!(structs[0].name, "MyStruct");

        let enums = graph.search_by_type("enum").unwrap();
        assert_eq!(enums.len(), 1);
        assert_eq!(enums[0].name, "MyEnum");
    });
}

#[test]
fn test_search_by_relation_type() {
    with_test_graph(|graph, _| {
        let rel1 = Relationship {
            id: None,
            source_qualified: "src/main.rs::a".to_string(),
            target_qualified: "src/main.rs::b".to_string(),
            rel_type: "calls".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        };
        let rel2 = Relationship {
            id: None,
            source_qualified: "src/main.rs::b".to_string(),
            target_qualified: "src/main.rs::c".to_string(),
            rel_type: "implements".to_string(),
            confidence: 1.0,
            metadata: serde_json::json!({}),
        };
        
        graph.insert_relationships(&[rel1, rel2]).unwrap();

        let calls = graph.search_by_relation_type("calls").unwrap();
        assert_eq!(calls.len(), 1);
        assert_eq!(calls[0].source_qualified, "src/main.rs::a");

        let implements = graph.search_by_relation_type("implements").unwrap();
        assert_eq!(implements.len(), 1);
        assert_eq!(implements[0].source_qualified, "src/main.rs::b");
    });
}

#[test]
fn test_find_oversized_functions() {
    with_test_graph(|graph, _| {
        graph.insert_elements(&[
            // 99 lines (start = 1, end = 100)
            create_code_element("small_func", "src/a.rs", "function", 99),
            // 200 lines
            create_code_element("big_func", "src/a.rs", "function", 200),
            // 50 lines, but it's a struct! Output shouldn't grab structural nodes.
            create_code_element("big_struct", "src/b.rs", "struct", 300),
        ]).unwrap();

        // Fetch functions with lines > 150
        let big = graph.find_oversized_functions(150).unwrap();
        assert_eq!(big.len(), 1);
        assert_eq!(big[0].name, "big_func");
        
        // Fetch > 50
        let medium = graph.find_oversized_functions(50).unwrap();
        assert_eq!(medium.len(), 2, "Should grab both functions but IGNORE struct");
    });
}

#[test]
fn test_update_element_cluster() {
    with_test_graph(|graph, _| {
        graph.insert_elements(&[
            create_code_element("func_a", "src/a.rs", "function", 10),
        ]).unwrap();
        
        let initial = graph.find_element("src/a.rs::func_a").unwrap().unwrap();
        assert!(initial.cluster_id.is_none());

        graph.update_element_cluster("src/a.rs::func_a", Some("cluster_99".to_string()), Some("Auth Services".to_string())).unwrap();

        let updated = graph.find_element("src/a.rs::func_a").unwrap().unwrap();
        assert_eq!(updated.cluster_id.unwrap(), "cluster_99");
        assert_eq!(updated.cluster_label.unwrap(), "Auth Services");
    });
}
