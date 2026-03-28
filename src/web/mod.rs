#![allow(dead_code)]
pub mod handlers;

use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post, put},
    Json, Router,
};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::sync::RwLock;

use crate::db::schema::{init_db, CozoDb};
use crate::graph::GraphEngine;

#[derive(Clone)]
pub struct AppState {
    pub db_path: Arc<RwLock<std::path::PathBuf>>,
    pub current_project_path: Arc<RwLock<std::path::PathBuf>>,
    db: Arc<RwLock<Option<CozoDb>>>,
    pub indexing_state: Arc<RwLock<IndexingState>>,
}

#[derive(Clone, Default)]
pub struct IndexingState {
    pub is_indexing: bool,
    pub progress_percent: usize,
    pub current_file: String,
    pub total_files: usize,
    pub indexed_files: usize,
    pub error: Option<String>,
}

impl Default for AppState {
    fn default() -> Self {
        Self {
            db_path: Arc::new(RwLock::new(std::path::PathBuf::new())),
            current_project_path: Arc::new(RwLock::new(std::path::PathBuf::new())),
            db: Arc::new(RwLock::new(None)),
            indexing_state: Arc::new(RwLock::new(IndexingState::default())),
        }
    }
}

impl AppState {
    pub async fn new(
        db_path: std::path::PathBuf,
        current_project_path: std::path::PathBuf,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        Ok(Self {
            db_path: Arc::new(RwLock::new(db_path)),
            current_project_path: Arc::new(RwLock::new(current_project_path)),
            db: Arc::new(RwLock::new(None)),
            indexing_state: Arc::new(RwLock::new(IndexingState::default())),
        })
    }

    pub async fn reset_indexing_state(&self) {
        let mut state = self.indexing_state.write().await;
        state.is_indexing = false;
        state.progress_percent = 0;
        state.current_file = String::new();
        state.total_files = 0;
        state.indexed_files = 0;
        state.error = None;
    }

    pub async fn set_indexing_started(&self, total_files: usize) {
        let mut state = self.indexing_state.write().await;
        state.is_indexing = true;
        state.progress_percent = 0;
        state.total_files = total_files;
        state.indexed_files = 0;
        state.error = None;
    }

    pub async fn update_indexing_progress(&self, indexed_files: usize, current_file: &str) {
        let mut state = self.indexing_state.write().await;
        state.indexed_files = indexed_files;
        state.current_file = current_file.to_string();
        if state.total_files > 0 {
            state.progress_percent = (indexed_files * 100) / state.total_files;
        }
    }

    pub async fn set_indexing_error(&self, error: String) {
        let mut state = self.indexing_state.write().await;
        state.is_indexing = false;
        state.error = Some(error);
    }

    pub async fn set_indexing_complete(&self) {
        let mut state = self.indexing_state.write().await;
        state.is_indexing = false;
        state.progress_percent = 100;
        state.current_file = String::new();
    }

    pub async fn switch_project(
        &self,
        project_path: std::path::PathBuf,
    ) -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
        let db_path = project_path.join(".leankg");
        
        {
            let mut path_guard = self.db_path.write().await;
            *path_guard = db_path.clone();
        }
        {
            let mut proj_guard = self.current_project_path.write().await;
            *proj_guard = project_path.clone();
        }
        
        let db = init_db(&db_path).map_err(|e| {
            let msg = e.to_string();
            Box::new(std::io::Error::new(std::io::ErrorKind::Other, msg)) as Box<dyn std::error::Error + Send + Sync>
        })?;
        {
            let mut lock = self.db.write().await;
            *lock = Some(db);
        }
        
        self.reset_indexing_state().await;
        
        Ok(())
    }

    pub async fn init_db(&self) -> Result<(), Box<dyn std::error::Error>> {
        let db_path = self.db_path.read().await.clone();
        let db = init_db(&db_path)?;
        let mut lock = self.db.write().await;
        *lock = Some(db);
        Ok(())
    }

    pub fn get_db(&self) -> Result<CozoDb, Box<dyn std::error::Error + Send + Sync>> {
        let rt = tokio::runtime::Runtime::new().unwrap();
        rt.block_on(async {
            let lock = self.db.read().await;
            lock.clone()
                .ok_or_else(|| "Database not initialized".into())
        })
    }

    pub async fn get_graph_engine(
        &self,
    ) -> Result<GraphEngine, Box<dyn std::error::Error + Send + Sync>> {
        let lock = self.db.read().await;
        let db = lock
            .clone()
            .ok_or_else(|| -> Box<dyn std::error::Error + Send + Sync> {
                "Database not initialized".into()
            })?;
        Ok(GraphEngine::new(db))
    }
}

#[derive(serde::Serialize)]
pub struct ApiResponse<T> {
    pub success: bool,
    pub data: Option<T>,
    pub error: Option<String>,
}

impl<T: serde::Serialize> IntoResponse for ApiResponse<T> {
    fn into_response(self) -> Response {
        let status = if self.success {
            StatusCode::OK
        } else {
            StatusCode::BAD_REQUEST
        };
        (status, Json(self)).into_response()
    }
}

pub async fn start_server(
    port: u16,
    db_path: std::path::PathBuf,
) -> Result<(), Box<dyn std::error::Error>> {
    let state = AppState::new(db_path.clone(), db_path).await?;
    state.init_db().await?;

    let app = Router::new()
        .route("/", get(handlers::index))
        .route("/health", get(handlers::index))
        .route("/project", get(handlers::project_selector))
        .route("/graph", get(handlers::graph))
        .route("/browse", get(handlers::browse))
        .route("/docs", get(handlers::docs))
        .route("/annotate", get(handlers::annotate))
        .route("/quality", get(handlers::quality))
        .route("/export", get(handlers::export_page))
        .route("/settings", get(handlers::settings))
        .route("/api/elements", get(handlers::api_elements))
        .route("/api/relationships", get(handlers::api_relationships))
        .route("/api/annotations", get(handlers::api_annotations))
        .route("/api/annotations", post(handlers::api_create_annotation))
        .route(
            "/api/annotations/:element",
            get(handlers::api_get_annotation),
        )
        .route(
            "/api/annotations/:element",
            put(handlers::api_update_annotation),
        )
        .route("/api/search", get(handlers::api_search))
        .route("/api/graph/data", get(handlers::api_graph_data))
        .route("/api/export/graph", get(handlers::api_export_graph))
        .route("/api/query", post(handlers::api_query))
        .route("/api/project/switch", post(handlers::api_switch_path))
        .route("/api/index/status", get(handlers::api_index_status))
        .route("/api/github/clone", post(handlers::api_github_clone))
        .with_state(state);

    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    println!("LeanKG Web UI listening on http://localhost:{}", port);
    println!("Press Ctrl+C to stop");

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;

    Ok(())
}
