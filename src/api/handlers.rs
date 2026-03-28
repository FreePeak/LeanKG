use axum::{
    extract::{Query, State},
    Json,
};
use serde::{Deserialize, Serialize};

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
