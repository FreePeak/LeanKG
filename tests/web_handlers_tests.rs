use leankg::web::handlers::{
    AnnotationRequest, GraphData, GraphEdge, GraphNode, QueryRequest, QueryResponse, SearchParams,
};

#[test]
fn test_search_params_deserialize() {
    let json = r#"{"q": "test", "element_type": "function"}"#;
    let params: SearchParams = serde_json::from_str(json).unwrap();
    assert_eq!(params.q, Some("test".to_string()));
    assert_eq!(params.element_type, Some("function".to_string()));
    assert_eq!(params.file_path, None);
}

#[test]
fn test_search_params_all_optional() {
    let json = r#"{}"#;
    let params: SearchParams = serde_json::from_str(json).unwrap();
    assert_eq!(params.q, None);
    assert_eq!(params.element_type, None);
    assert_eq!(params.file_path, None);
}

#[test]
fn test_search_params_partial_deserialize() {
    let json = r#"{"file_path": "src/main.rs"}"#;
    let params: SearchParams = serde_json::from_str(json).unwrap();
    assert_eq!(params.q, None);
    assert_eq!(params.element_type, None);
    assert_eq!(params.file_path, Some("src/main.rs".to_string()));
}

#[test]
fn test_annotation_request_serialize() {
    let req = AnnotationRequest {
        element_qualified: "my_function".to_string(),
        description: "Test description".to_string(),
        user_story_id: Some("US-123".to_string()),
        feature_id: Some("FEAT-AUTH".to_string()),
    };
    let json = serde_json::to_string(&req).unwrap();
    assert!(json.contains("my_function"));
    assert!(json.contains("Test description"));
    assert!(json.contains("US-123"));
    assert!(json.contains("FEAT-AUTH"));
}

#[test]
fn test_annotation_request_deserialize() {
    let json = r#"{"element_qualified": "func", "description": "desc", "user_story_id": "US-1", "feature_id": "F1"}"#;
    let req: AnnotationRequest = serde_json::from_str(json).unwrap();
    assert_eq!(req.element_qualified, "func");
    assert_eq!(req.description, "desc");
    assert_eq!(req.user_story_id, Some("US-1".to_string()));
    assert_eq!(req.feature_id, Some("F1".to_string()));
}

#[test]
fn test_annotation_request_without_optionals() {
    let json = r#"{"element_qualified": "func", "description": "desc"}"#;
    let req: AnnotationRequest = serde_json::from_str(json).unwrap();
    assert_eq!(req.element_qualified, "func");
    assert_eq!(req.description, "desc");
    assert_eq!(req.user_story_id, None);
    assert_eq!(req.feature_id, None);
}

#[test]
fn test_annotation_request_roundtrip() {
    let req = AnnotationRequest {
        element_qualified: "my_func".to_string(),
        description: "A test function".to_string(),
        user_story_id: None,
        feature_id: None,
    };
    let json = serde_json::to_string(&req).unwrap();
    let req2: AnnotationRequest = serde_json::from_str(&json).unwrap();
    assert_eq!(req.element_qualified, req2.element_qualified);
    assert_eq!(req.description, req2.description);
}

#[test]
fn test_graph_node_serialize() {
    let node = GraphNode {
        id: "node1".to_string(),
        label: "MyFunction".to_string(),
        element_type: "function".to_string(),
        file_path: "src/my_func.rs".to_string(),
    };
    let json = serde_json::to_string(&node).unwrap();
    assert!(json.contains("node1"));
    assert!(json.contains("MyFunction"));
    assert!(json.contains("function"));
    assert!(json.contains("src/my_func.rs"));
}

#[test]
fn test_graph_node_has_expected_fields() {
    let node = GraphNode {
        id: "id".to_string(),
        label: "label".to_string(),
        element_type: "type".to_string(),
        file_path: "path".to_string(),
    };
    assert_eq!(node.id, "id");
    assert_eq!(node.label, "label");
    assert_eq!(node.element_type, "type");
    assert_eq!(node.file_path, "path");
}

#[test]
fn test_graph_edge_serialize() {
    let edge = GraphEdge {
        source: "source_node".to_string(),
        target: "target_node".to_string(),
        rel_type: "calls".to_string(),
    };
    let json = serde_json::to_string(&edge).unwrap();
    assert!(json.contains("source_node"));
    assert!(json.contains("target_node"));
    assert!(json.contains("calls"));
}

#[test]
fn test_graph_edge_has_expected_fields() {
    let edge = GraphEdge {
        source: "src".to_string(),
        target: "tgt".to_string(),
        rel_type: "imports".to_string(),
    };
    assert_eq!(edge.source, "src");
    assert_eq!(edge.target, "tgt");
    assert_eq!(edge.rel_type, "imports");
}

#[test]
fn test_graph_data_contains_nodes_and_edges() {
    let nodes = vec![
        GraphNode {
            id: "n1".to_string(),
            label: "Node1".to_string(),
            element_type: "function".to_string(),
            file_path: "f1.rs".to_string(),
        },
        GraphNode {
            id: "n2".to_string(),
            label: "Node2".to_string(),
            element_type: "class".to_string(),
            file_path: "f2.rs".to_string(),
        },
    ];
    let edges = vec![GraphEdge {
        source: "n1".to_string(),
        target: "n2".to_string(),
        rel_type: "calls".to_string(),
    }];
    let graph = GraphData { nodes, edges };
    assert_eq!(graph.nodes.len(), 2);
    assert_eq!(graph.edges.len(), 1);
    assert_eq!(graph.nodes[0].id, "n1");
    assert_eq!(graph.edges[0].source, "n1");
}

#[test]
fn test_graph_data_serialize() {
    let graph = GraphData {
        nodes: vec![GraphNode {
            id: "a".to_string(),
            label: "A".to_string(),
            element_type: "file".to_string(),
            file_path: "a.rs".to_string(),
        }],
        edges: vec![GraphEdge {
            source: "a".to_string(),
            target: "b".to_string(),
            rel_type: "imports".to_string(),
        }],
    };
    let json = serde_json::to_string(&graph).unwrap();
    assert!(json.contains("nodes"));
    assert!(json.contains("edges"));
    assert!(json.contains("a"));
    assert!(json.contains("b"));
}

#[test]
fn test_graph_data_empty() {
    let graph = GraphData {
        nodes: vec![],
        edges: vec![],
    };
    assert!(graph.nodes.is_empty());
    assert!(graph.edges.is_empty());
    let json = serde_json::to_string(&graph).unwrap();
    assert!(json.contains("\"nodes\":[]"));
    assert!(json.contains("\"edges\":[]"));
}

#[test]
fn test_query_request_deserialize() {
    let json = r#"{"query": "find_functions"}"#;
    let req: QueryRequest = serde_json::from_str(json).unwrap();
    assert_eq!(req.query, "find_functions");
}

#[test]
fn test_query_request_roundtrip() {
    let req = QueryRequest {
        query: "test_query".to_string(),
    };
    let json = serde_json::to_string(&req).unwrap();
    let req2: QueryRequest = serde_json::from_str(&json).unwrap();
    assert_eq!(req.query, req2.query);
}

#[test]
fn test_query_response_serialize() {
    let resp = QueryResponse {
        result: vec![serde_json::json!({"name": "test", "type": "function"})],
    };
    let json = serde_json::to_string(&resp).unwrap();
    assert!(json.contains("result"));
    assert!(json.contains("test"));
}

#[test]
fn test_search_params_with_all_fields() {
    let json = r#"{"q": "search", "element_type": "class", "file_path": "mod.rs"}"#;
    let params: SearchParams = serde_json::from_str(json).unwrap();
    assert_eq!(params.q, Some("search".to_string()));
    assert_eq!(params.element_type, Some("class".to_string()));
    assert_eq!(params.file_path, Some("mod.rs".to_string()));
}

#[test]
fn test_graph_node_clone() {
    let node = GraphNode {
        id: "id".to_string(),
        label: "label".to_string(),
        element_type: "type".to_string(),
        file_path: "path".to_string(),
    };
    let cloned = node.clone();
    assert_eq!(cloned.id, node.id);
    assert_eq!(cloned.label, node.label);
    assert_eq!(cloned.element_type, node.element_type);
    assert_eq!(cloned.file_path, node.file_path);
}

#[test]
fn test_graph_edge_clone() {
    let edge = GraphEdge {
        source: "src".to_string(),
        target: "tgt".to_string(),
        rel_type: "type".to_string(),
    };
    let cloned = edge.clone();
    assert_eq!(cloned.source, edge.source);
    assert_eq!(cloned.target, edge.target);
    assert_eq!(cloned.rel_type, edge.rel_type);
}

#[test]
fn test_graph_data_clone() {
    let graph = GraphData {
        nodes: vec![GraphNode {
            id: "n".to_string(),
            label: "l".to_string(),
            element_type: "t".to_string(),
            file_path: "p".to_string(),
        }],
        edges: vec![GraphEdge {
            source: "s".to_string(),
            target: "t".to_string(),
            rel_type: "r".to_string(),
        }],
    };
    let cloned = graph.clone();
    assert_eq!(cloned.nodes.len(), 1);
    assert_eq!(cloned.edges.len(), 1);
}
