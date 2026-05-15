use leankg::db::{self, models::Incident, schema::init_db};
use leankg::graph::GraphEngine;

fn test_db() -> (tempfile::TempDir, leankg::db::schema::CozoDb) {
    let tmp = tempfile::tempdir().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(&db_path).unwrap();
    (tmp, db)
}

fn insert_service(
    db: &leankg::db::schema::CozoDb,
    qualified_name: &str,
    name: &str,
    env: &str,
    version: &str,
) {
    let query = r#"
    ?[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] <-
    [[$qualified_name, "service", $name, "service.yaml", 1, 1, "yaml", null, null, null, $metadata, $env]]
    :put code_elements {qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env}
    "#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "qualified_name".to_string(),
        serde_json::Value::String(qualified_name.to_string()),
    );
    params.insert(
        "name".to_string(),
        serde_json::Value::String(name.to_string()),
    );
    params.insert(
        "metadata".to_string(),
        serde_json::Value::String(serde_json::json!({ "version": version }).to_string()),
    );
    params.insert(
        "env".to_string(),
        serde_json::Value::String(env.to_string()),
    );
    db.run_script(query, params).unwrap();
}

fn insert_call(db: &leankg::db::schema::CozoDb, source: &str, target: &str, env: &str) {
    let query = r#"
    ?[source_qualified, target_qualified, rel_type, confidence, metadata, env] <-
    [[$source, $target, "calls", 1.0, "{}", $env]]
    :put relationships {source_qualified, target_qualified, rel_type, confidence, metadata, env}
    "#;
    let mut params = std::collections::BTreeMap::new();
    params.insert(
        "source".to_string(),
        serde_json::Value::String(source.to_string()),
    );
    params.insert(
        "target".to_string(),
        serde_json::Value::String(target.to_string()),
    );
    params.insert(
        "env".to_string(),
        serde_json::Value::String(env.to_string()),
    );
    db.run_script(query, params).unwrap();
}

#[test]
fn v2_schema_uses_canonical_env_arity() {
    let (_tmp, db) = test_db();

    db.run_script(
        "?[qualified_name] := *code_elements[qualified_name, element_type, name, file_path, line_start, line_end, language, parent_qualified, cluster_id, cluster_label, metadata, env] :limit 0",
        Default::default(),
    )
    .unwrap();
    db.run_script(
        "?[source_qualified] := *relationships[source_qualified, target_qualified, rel_type, confidence, metadata, env] :limit 0",
        Default::default(),
    )
    .unwrap();
}

#[test]
fn incidents_filter_by_service_pattern_and_env() {
    let (_tmp, db) = test_db();
    let incident = Incident {
        id: "inc-1".to_string(),
        env: "production".to_string(),
        title: "Database connection pool exhausted".to_string(),
        severity: "P1".to_string(),
        occurred_at: 1000,
        resolved_at: None,
        root_cause: "api leaked database connections".to_string(),
        resolution: "restart api workers".to_string(),
        affected_services: vec!["api".to_string(), "database".to_string()],
        trigger_pattern: Some("connection timeout".to_string()),
        prevention: None,
        tags: vec!["db".to_string()],
        author: "oncall".to_string(),
        linked_ticket: None,
    };
    db::create_incident(&db, &incident).unwrap();

    let by_service =
        db::query_incidents(&db, Some("api"), Some("connection"), Some("production"), 10).unwrap();
    let wrong_env = db::query_incidents(&db, Some("api"), None, Some("staging"), 10).unwrap();

    assert_eq!(by_service.len(), 1);
    assert_eq!(by_service[0].id, "inc-1");
    assert!(wrong_env.is_empty());
}

#[test]
fn graph_service_context_reads_env_scoped_data() {
    let (_tmp, db) = test_db();
    insert_service(&db, "api", "api", "production", "abc123");
    insert_service(&db, "database", "database", "production", "def456");
    insert_call(&db, "api", "database", "production");

    db::create_incident(
        &db,
        &Incident {
            id: "inc-2".to_string(),
            env: "production".to_string(),
            title: "API timeout".to_string(),
            severity: "P2".to_string(),
            occurred_at: 2000,
            resolved_at: None,
            root_cause: "api called database too slowly".to_string(),
            resolution: "add index".to_string(),
            affected_services: vec!["api".to_string()],
            trigger_pattern: None,
            prevention: None,
            tags: vec![],
            author: "oncall".to_string(),
            linked_ticket: None,
        },
    )
    .unwrap();

    let graph = GraphEngine::new(db);
    let context = graph.get_service_context("api", "production").unwrap();

    assert_eq!(context.version.as_deref(), Some("abc123"));
    assert_eq!(context.calls, vec!["database".to_string()]);
    assert_eq!(context.open_incidents, 1);
    assert_eq!(context.recent_incidents, vec!["API timeout".to_string()]);
}
