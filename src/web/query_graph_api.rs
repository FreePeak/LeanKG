//! FR-UI2-08 / US-UI2-06 — REST surface for NL `query_graph`.
//!
//! ui-v2 Query FAB default mode posts here; Advanced keeps `POST /api/query`.

use serde::{Deserialize, Serialize};

use crate::graph::nl_query::QueryGraphResult;
use crate::graph::query::GraphEngine;

/// Body for `POST /api/query-graph`.
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct QueryGraphRequest {
    pub question: String,
    #[serde(default)]
    pub token_budget: Option<usize>,
    #[serde(default)]
    pub max_depth: Option<usize>,
}

/// Validate and normalize the NL question (trim; reject blank).
pub fn validate_query_graph_question(question: &str) -> Result<&str, String> {
    let q = question.trim();
    if q.is_empty() {
        return Err("question must not be empty".into());
    }
    Ok(q)
}

/// Run NL `query_graph` for the REST handler.
pub fn execute_query_graph(
    engine: &GraphEngine,
    req: &QueryGraphRequest,
) -> Result<QueryGraphResult, String> {
    let question = validate_query_graph_question(&req.question)?;
    engine
        .query_graph(question, req.token_budget, req.max_depth)
        .map_err(|e| e.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::backend::init_db;
    use crate::db::models::{CodeElement, Relationship};
    use crate::graph::query::GraphEngine;
    use tempfile::TempDir;

    #[test]
    fn fr_ui2_08_rejects_blank_question() {
        assert!(validate_query_graph_question("").is_err());
        assert!(validate_query_graph_question("   \n\t  ").is_err());
    }

    #[test]
    fn fr_ui2_08_trims_question() {
        assert_eq!(
            validate_query_graph_question("  what connects auth to db?  ").unwrap(),
            "what connects auth to db?"
        );
    }

    #[test]
    fn fr_ui2_08_request_deserializes_question_only() {
        let req: QueryGraphRequest =
            serde_json::from_str(r#"{"question":"what connects auth to db?"}"#).unwrap();
        assert_eq!(req.question, "what connects auth to db?");
        assert!(req.token_budget.is_none());
        assert!(req.max_depth.is_none());
    }

    #[test]
    fn fr_ui2_08_request_deserializes_optional_budgets() {
        let req: QueryGraphRequest =
            serde_json::from_str(r#"{"question":"auth","token_budget":2000,"max_depth":2}"#)
                .unwrap();
        assert_eq!(req.token_budget, Some(2000));
        assert_eq!(req.max_depth, Some(2));
    }

    fn make_engine() -> (GraphEngine, TempDir) {
        let tmp = TempDir::new().unwrap();
        let db = init_db(&tmp.path().join("test.db")).unwrap();
        (GraphEngine::new(db), tmp)
    }

    #[test]
    fn fr_ui2_08_execute_query_graph_returns_seeds() {
        let (engine, _tmp) = make_engine();
        let elem = CodeElement {
            qualified_name: "src/auth.rs::authenticate".into(),
            element_type: "function".into(),
            name: "authenticate".into(),
            file_path: "src/auth.rs".into(),
            line_start: 1,
            line_end: 10,
            language: "rust".into(),
            ..Default::default()
        };
        engine.insert_element(&elem).unwrap();
        let rel = Relationship {
            source_qualified: "src/auth.rs::authenticate".into(),
            target_qualified: "src/auth.rs::authenticate".into(),
            rel_type: "calls".into(),
            confidence: 0.9,
            metadata: serde_json::json!({"resolution_method": "name"}),
            ..Default::default()
        };
        let _ = engine.insert_relationship(&rel);

        let req = QueryGraphRequest {
            question: "authenticate".into(),
            token_budget: Some(2000),
            max_depth: Some(1),
        };
        let result = execute_query_graph(&engine, &req).expect("execute");
        assert_eq!(result.question, "authenticate");
        assert!(
            !result.seeds.is_empty() || !result.nodes.is_empty(),
            "expected seeds or nodes: {result:?}"
        );
    }

    #[test]
    fn fr_ui2_08_execute_rejects_blank() {
        let (engine, _tmp) = make_engine();
        let req = QueryGraphRequest {
            question: "   ".into(),
            token_budget: None,
            max_depth: None,
        };
        assert!(execute_query_graph(&engine, &req).is_err());
    }
}
