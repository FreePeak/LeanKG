#![allow(dead_code)]
use axum::{
    extract::{Request, State},
    http::StatusCode,
    middleware::Next,
    response::{IntoResponse, Response},
};
use std::sync::Arc;

use crate::api::ApiState;
use crate::db::keys::ApiKeyStore;

pub async fn auth_middleware(
    State(_state): State<ApiState>,
    mut request: Request,
    next: Next,
) -> Response {
    let auth_header = request
        .headers()
        .get("authorization")
        .and_then(|v| v.to_str().ok())
        .map(|s| s.to_string());

    if auth_header.is_none() {
        return (StatusCode::UNAUTHORIZED, "Missing Authorization header").into_response();
    }

    let auth_header = auth_header.unwrap();
    let token = if auth_header.starts_with("Bearer ") {
        auth_header[7..].to_string()
    } else {
        return (StatusCode::UNAUTHORIZED, "Invalid Authorization format").into_response();
    };

    let store = ApiKeyStore::new().map_err(|e| e.to_string()).unwrap();
    match store.validate_key(&token) {
        Ok(Some(_key_id)) => {
            request
                .extensions_mut()
                .insert(AuthContext { key_id: _key_id });
            next.run(request).await
        }
        Ok(None) => (StatusCode::UNAUTHORIZED, "Invalid API key").into_response(),
        Err(e) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            format!("Auth error: {}", e),
        )
            .into_response(),
    }
}

pub async fn require_auth_middleware(
    State(_state): State<Arc<ApiState>>,
    request: Request,
    next: Next,
) -> Response {
    if request.extensions().get::<AuthContext>().is_none() {
        return (StatusCode::UNAUTHORIZED, "Authentication required").into_response();
    }
    next.run(request).await
}

#[derive(Clone)]
pub struct AuthContext {
    pub key_id: String,
}
