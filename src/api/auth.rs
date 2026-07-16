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
    let token = if let Some(token) = auth_header.strip_prefix("Bearer ") {
        token.to_string()
    } else {
        return (StatusCode::UNAUTHORIZED, "Invalid Authorization format").into_response();
    };

    let store = match ApiKeyStore::new() {
        Ok(store) => store,
        Err(e) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                format!("Auth error: {}", e),
            )
                .into_response();
        }
    };
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

#[derive(Clone, Debug)]
pub struct TeamAuthContext {
    pub token: String,
    pub engineer: String,
    pub env: String,
}

pub async fn team_token_middleware(
    State(_state): State<ApiState>,
    mut request: Request,
    next: Next,
) -> Response {
    let get_header = |name: &str| -> Option<String> {
        request
            .headers()
            .get(name)
            .and_then(|v| v.to_str().ok())
            .map(|s| s.to_string())
    };

    let token = get_header("X-LeanKG-Token").unwrap_or_default();
    let engineer = get_header("X-LeanKG-Engineer").unwrap_or_else(|| "unknown".to_string());
    let env = get_header("X-LeanKG-Env").unwrap_or_else(|| "production".to_string());

    if token.is_empty() {
        return (StatusCode::UNAUTHORIZED, "Missing X-LeanKG-Token header").into_response();
    }

    let store = match ApiKeyStore::new() {
        Ok(store) => store,
        Err(e) => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                format!("Auth error: {}", e),
            )
                .into_response();
        }
    };
    match store.validate_key(&token) {
        Ok(Some(key_id)) => {
            request.extensions_mut().insert(TeamAuthContext {
                token: key_id,
                engineer,
                env,
            });
            next.run(request).await
        }
        Ok(None) => (StatusCode::UNAUTHORIZED, "Invalid team token").into_response(),
        Err(e) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            format!("Auth error: {}", e),
        )
            .into_response(),
    }
}

pub fn get_team_ctx(request: &Request) -> Option<TeamAuthContext> {
    request.extensions().get::<TeamAuthContext>().cloned()
}
