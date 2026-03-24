pub mod handlers;

use axum::{
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
    Json, Router,
};
use std::net::SocketAddr;
use std::sync::Arc;
use tokio::sync::RwLock;

use crate::db::schema::{init_db, CozoDb};
use crate::graph::GraphEngine;

#[derive(Clone)]
pub struct AppState {
    pub db_path: std::path::PathBuf,
    db: Arc<RwLock<Option<CozoDb>>>,
}

impl AppState {
    #[allow(dead_code)]
    pub async fn new(db_path: std::path::PathBuf) -> Result<Self, Box<dyn std::error::Error>> {
        Ok(Self {
            db_path,
            db: Arc::new(RwLock::new(None)),
        })
    }

    #[allow(dead_code)]
    pub async fn init_db(&self) -> Result<(), Box<dyn std::error::Error>> {
        let db = init_db(&self.db_path)?;
        let mut lock = self.db.write().await;
        *lock = Some(db);
        Ok(())
    }

    #[allow(dead_code)]
    pub fn get_db(&self) -> Result<CozoDb, Box<dyn std::error::Error + Send + Sync>> {
        let rt = tokio::runtime::Runtime::new().unwrap();
        rt.block_on(async {
            let lock = self.db.read().await;
            lock.clone()
                .ok_or_else(|| "Database not initialized".into())
        })
    }

    pub async fn get_graph_engine(&self) -> Result<GraphEngine, Box<dyn std::error::Error + Send + Sync>> {
        let lock = self.db.read().await;
        let db = lock.clone()
            .ok_or_else(|| -> Box<dyn std::error::Error + Send + Sync> { "Database not initialized".into() })?;
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

#[allow(dead_code)]
pub async fn start_server(
    port: u16,
    db_path: std::path::PathBuf,
) -> Result<(), Box<dyn std::error::Error>> {
    let state = AppState::new(db_path).await?;
    state.init_db().await?;

    async fn handler() -> &'static str {
        "LeanKG Web UI - Use CLI commands for full functionality"
    }

    let app = Router::new()
        .route("/", get(handler))
        .route("/health", get(handler))
        .with_state(state);

    let addr = SocketAddr::from(([127, 0, 0, 1], port));
    println!("Web UI listening on http://{}", addr);

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;

    Ok(())
}
