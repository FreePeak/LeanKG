use axum::{
    extract::{Query, State},
    Json,
};
use serde::{Deserialize, Serialize};
use serde_json::{json, Value};

use crate::api::{ApiResponse, ApiState};

#[derive(Serialize)]
pub struct StatusData {
    pub elements: usize,
    pub relationships: usize,
    pub annotations: usize,
    pub files: usize,
    pub functions: usize,
    pub classes: usize,
    pub database: String,
}

pub async fn health() -> Json<ApiResponse<crate::api::HealthResponse>> {
    Json(ApiResponse::success(crate::api::HealthResponse {
        status: "ok".to_string(),
        version: env!("CARGO_PKG_VERSION").to_string(),
    }))
}

pub async fn api_status(
    State(state): State<ApiState>,
) -> Result<Json<ApiResponse<StatusData>>, &'static str> {
    let mut element_count = 0usize;
    let mut relationship_count = 0usize;
    let mut annotation_count = 0usize;
    let mut files_count = 0usize;
    let mut functions_count = 0usize;
    let mut classes_count = 0usize;

    if let Ok(graph) = state.get_graph_engine().await {
        if let Ok(elements) = graph.all_elements() {
            element_count = elements.len();
            let unique_files: std::collections::HashSet<_> =
                elements.iter().map(|e| e.file_path.clone()).collect();
            files_count = unique_files.len();
            functions_count = elements
                .iter()
                .filter(|x| x.element_type == "function")
                .count();
            classes_count = elements
                .iter()
                .filter(|x| x.element_type == "class" || x.element_type == "struct")
                .count();
        }
        if let Ok(relns) = graph.all_relationships() {
            relationship_count = relns.len();
        }
        if let Ok(anns) = graph.all_annotations() {
            annotation_count = anns.len();
        }
    }

    Ok(Json(ApiResponse::success(StatusData {
        elements: element_count,
        relationships: relationship_count,
        annotations: annotation_count,
        files: files_count,
        functions: functions_count,
        classes: classes_count,
        database: state.db_path.to_string_lossy().to_string(),
    })))
}

#[derive(Deserialize)]
pub struct SearchQuery {
    pub q: String,
    #[serde(default = "default_limit")]
    pub limit: usize,
}

fn default_limit() -> usize {
    20
}

#[derive(Serialize)]
pub struct SearchResult {
    pub elements: Vec<SearchElement>,
}

#[derive(Serialize)]
pub struct SearchElement {
    pub qualified_name: String,
    pub name: String,
    pub element_type: String,
    pub file_path: String,
    pub line_start: usize,
    pub line_end: usize,
}

pub async fn api_search(
    State(state): State<ApiState>,
    Query(query): Query<SearchQuery>,
) -> Result<Json<ApiResponse<SearchResult>>, &'static str> {
    if query.q.is_empty() {
        return Err("Query parameter 'q' is required");
    }

    let graph = match state.get_graph_engine().await {
        Ok(g) => g,
        Err(_) => return Err("Failed to get graph engine"),
    };

    let search_results = graph
        .search_by_name(&query.q)
        .map_err(|_| "Search failed")?;

    let elements: Vec<SearchElement> = search_results
        .into_iter()
        .take(query.limit)
        .map(|e| SearchElement {
            qualified_name: e.qualified_name,
            name: e.name,
            element_type: e.element_type,
            file_path: e.file_path,
            line_start: e.line_start as usize,
            line_end: e.line_end as usize,
        })
        .collect();

    Ok(Json(ApiResponse::success(SearchResult { elements })))
}

pub async fn api_v2_status(State(state): State<ApiState>) -> Json<ApiResponse<Value>> {
    let graph = match state.get_graph_engine().await {
        Ok(g) => g,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let elements = graph.all_elements().unwrap_or_default();
    let rels = graph.all_relationships().unwrap_or_default();
    Json(ApiResponse::success(json!({
        "total_elements": elements.len(),
        "total_relationships": rels.len(),
        "service": state.db_path.file_name().and_then(|n| n.to_str()).unwrap_or("unknown"),
        "version": env!("CARGO_PKG_VERSION"),
    })))
}

pub async fn api_v2_service_context(
    State(state): State<ApiState>,
    Query(params): Query<std::collections::HashMap<String, String>>,
) -> Json<ApiResponse<Value>> {
    let graph = match state.get_graph_engine().await {
        Ok(g) => g,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let service = params.get("service").map(|s| s.as_str()).unwrap_or("");
    let env = params
        .get("env")
        .map(|s| s.as_str())
        .unwrap_or("production");
    match graph.get_service_context(service, env) {
        Ok(ctx) => Json(ApiResponse::success(
            serde_json::to_value(&ctx).unwrap_or_default(),
        )),
        Err(e) => Json(ApiResponse::error(&e)),
    }
}

pub async fn api_v2_incidents(
    State(state): State<ApiState>,
    Query(params): Query<std::collections::HashMap<String, String>>,
) -> Json<ApiResponse<Value>> {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let service = params.get("service").map(|s| s.as_str());
    let pattern = params.get("pattern").map(|s| s.as_str());
    let env = params
        .get("env")
        .map(|s| s.as_str())
        .unwrap_or("production");
    let limit = params
        .get("limit")
        .and_then(|v| v.parse::<usize>().ok())
        .unwrap_or(10);
    match crate::db::query_incidents(&db, service, pattern, Some(env), limit) {
        Ok(incidents) => Json(ApiResponse::success(
            serde_json::to_value(&incidents).unwrap_or_default(),
        )),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

pub async fn api_v2_env_diff(
    State(state): State<ApiState>,
    Query(params): Query<std::collections::HashMap<String, String>>,
) -> Json<ApiResponse<Value>> {
    let graph = match state.get_graph_engine().await {
        Ok(g) => g,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let service = params.get("service").map(|s| s.as_str()).unwrap_or("");
    match graph.find_env_conflicts(service) {
        Ok(conflicts) => Json(ApiResponse::success(
            serde_json::to_value(&conflicts).unwrap_or_default(),
        )),
        Err(e) => Json(ApiResponse::error(&e)),
    }
}

pub async fn api_v2_health(State(_state): State<ApiState>) -> Json<ApiResponse<Value>> {
    Json(ApiResponse::success(json!({
        "status": "healthy",
        "version": env!("CARGO_PKG_VERSION"),
    })))
}

#[derive(Deserialize)]
pub struct HistoryQuery {
    pub element: Option<String>,
    pub from: Option<i64>,
    pub to: Option<i64>,
    pub env: Option<String>,
    pub limit: Option<usize>,
}

pub async fn api_v2_history(
    State(state): State<ApiState>,
    Query(params): Query<HistoryQuery>,
) -> Json<ApiResponse<Value>> {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let limit = params.limit.unwrap_or(100);
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64;
    let from_time = params.from.unwrap_or(0);
    let to_time = params.to.unwrap_or(now);
    let env = params.env.clone();

    match crate::db::query_change_history(&db, params.element, from_time, to_time, env, limit) {
        Ok(changes) => {
            let entries: Vec<_> = changes
                .iter()
                .map(|c| {
                    json!({
                        "element_qualified": c.element_qualified,
                        "change_type": c.change_type,
                        "description": c.description,
                        "valid_from": c.valid_from,
                        "valid_to": c.valid_to,
                        "created_at": c.created_at,
                        "env": c.env,
                        "file_path": c.file_path,
                    })
                })
                .collect();
            Json(ApiResponse::success(json!({
                "entries": entries,
                "total_count": entries.len(),
            })))
        }
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

#[derive(Deserialize)]
pub struct SnapshotQuery {
    pub element: Option<String>,
    pub as_of: Option<i64>,
    pub env: Option<String>,
}

pub async fn api_v2_snapshot(
    State(state): State<ApiState>,
    Query(params): Query<SnapshotQuery>,
) -> Json<ApiResponse<Value>> {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let now = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64;
    let as_of_time = params.as_of.unwrap_or(now);
    let env = params.env.unwrap_or_else(|| "local".to_string());

    if let Some(elem) = params.element {
        match crate::db::query_element_snapshot(&db, &elem, as_of_time, &env) {
            Ok(elements) => Json(ApiResponse::success(json!({
                "elements": elements,
                "as_of_time": as_of_time,
                "env": env,
            }))),
            Err(e) => Json(ApiResponse::error(&e.to_string())),
        }
    } else {
        match crate::db::query_all_snapshots(&db, as_of_time, &env) {
            Ok(elements) => Json(ApiResponse::success(json!({
                "elements": elements,
                "as_of_time": as_of_time,
                "env": env,
            }))),
            Err(e) => Json(ApiResponse::error(&e.to_string())),
        }
    }
}
