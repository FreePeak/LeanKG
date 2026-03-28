#![allow(dead_code)]
pub mod auth;
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
use tower_http::cors::{Any, CorsLayer};

use crate::db::schema::{init_db, CozoDb};
use crate::graph::GraphEngine;

#[derive(Clone)]
pub struct ApiState {
    pub db_path: std::path::PathBuf,
    db: Arc<RwLock<Option<CozoDb>>>,
}

impl ApiState {
    pub async fn new(db_path: std::path::PathBuf) -> Result<Self, Box<dyn std::error::Error>> {
        Ok(Self {
            db_path,
            db: Arc::new(RwLock::new(None)),
        })
    }

    pub async fn init_db(&self) -> Result<(), Box<dyn std::error::Error>> {
        let db = init_db(&self.db_path)?;
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

impl<T: serde::Serialize> ApiResponse<T> {
    pub fn success(data: T) -> Self {
        Self {
            success: true,
            data: Some(data),
            error: None,
        }
    }

    pub fn error(msg: &str) -> Self {
        Self {
            success: false,
            data: None,
            error: Some(msg.to_string()),
        }
    }
}

#[derive(serde::Serialize)]
pub struct HealthResponse {
    pub status: String,
    pub version: String,
}

pub async fn start_api_server(
    port: u16,
    db_path: std::path::PathBuf,
    require_auth: bool,
) -> Result<(), Box<dyn std::error::Error>> {
    let state = ApiState::new(db_path).await?;
    state.init_db().await?;

    let cors = CorsLayer::new()
        .allow_origin(Any)
        .allow_methods(Any)
        .allow_headers(Any);

    let app = Router::new()
        .route("/health", get(handlers::health))
        .route("/api/v1/status", get(handlers::api_status))
        .route("/api/v1/search", get(handlers::api_search))
        .layer(cors)
        .with_state(state);

    if require_auth {
        println!("Warning: Auth not yet implemented, starting without auth");
    }

    let addr = SocketAddr::from(([0, 0, 0, 0], port));
    println!("LeanKG REST API listening on http://localhost:{}", port);

    let listener = tokio::net::TcpListener::bind(addr).await?;
    axum::serve(listener, app).await?;

    Ok(())
}
