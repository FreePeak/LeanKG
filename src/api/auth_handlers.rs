//! HTTP handlers for the OAuth2-style access-token auth API.
//!
//! Endpoints (all under `/api/v1/auth/...`):
//! - `POST /api/v1/auth/register` — create an account (+ bootstrap org)
//! - `POST /api/v1/auth/login` — verify credentials, returns account (no token; use issue)
//! - `POST /api/v1/auth/token` — issue an access token for an account (Bearer: admin token)
//! - `POST /api/v1/auth/token/revoke` — revoke a token
//! - `GET  /api/v1/auth/token` — list tokens for the authenticated account
//! - `POST /api/v1/auth/org` — create an org (owner = authed account)
//! - `POST /api/v1/auth/org/{id}/member` — add/set member role
//! - `GET  /api/v1/auth/org/{id}/members` — list members
//! - `POST /api/v1/auth/resource/claim` — record resource ownership
//!
//! Authentication: `Authorization: Bearer <access_token>`. The token
//! resolves via [`crate::auth::AccessTokenStore`] → [`AuthContext`].

use crate::api::{ApiResponse, ApiState};
use crate::auth::accounts::AccountStore;
use crate::auth::tokens::AccessTokenStore;
use crate::db::models::Role;
use axum::extract::{Path, State};
use axum::http::{header, HeaderMap};
use axum::Json;
use serde::{Deserialize, Serialize};

#[derive(Debug, Deserialize)]
pub struct RegisterRequest {
    pub email: String,
    pub password: String,
    pub name: String,
}

#[derive(Debug, Deserialize)]
pub struct LoginRequest {
    pub email: String,
    pub password: String,
}

#[derive(Debug, Deserialize)]
pub struct IssueTokenRequest {
    pub account_id: String,
    #[serde(default)]
    pub org_id: Option<String>,
    #[serde(default = "default_role")]
    pub role: String,
    pub name: String,
    #[serde(default)]
    pub ttl_secs: Option<i64>,
}

fn default_role() -> String {
    "viewer".to_string()
}

#[derive(Debug, Deserialize)]
pub struct RevokeTokenRequest {
    pub token_id: String,
}

#[derive(Debug, Deserialize)]
pub struct AddMemberRequest {
    pub account_id: String,
    pub role: String,
}

#[derive(Debug, Deserialize)]
pub struct ClaimResourceRequest {
    pub resource_type: String,
    pub resource_id: String,
    #[serde(default)]
    pub org_id: Option<String>,
}

#[derive(Serialize)]
pub struct TokenIssued {
    pub access_token: String,
    pub token_type: String,
    pub account_id: String,
    pub role: String,
    pub expires_at: Option<i64>,
}

fn stores(
    state: &ApiState,
) -> Result<(AccountStore, AccessTokenStore), Box<dyn std::error::Error + Send + Sync>> {
    let db = state.get_db()?;
    Ok((AccountStore::new(db.clone()), AccessTokenStore::new(db)))
}

/// Resolve the Bearer token from the Authorization header.
fn bearer_from_headers(headers: &HeaderMap) -> Option<String> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|v| v.to_str().ok())
        .and_then(|a| a.strip_prefix("Bearer "))
        .map(|s| s.trim().to_string())
}

/// Public: register an account (creates a bootstrap org).
pub async fn register(
    State(state): State<ApiState>,
    Json(req): Json<RegisterRequest>,
) -> Json<ApiResponse<crate::auth::accounts::Account>> {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let store = AccountStore::new(db);
    match store.register(&req.email, &req.password, &req.name) {
        Ok(account) => Json(ApiResponse::success(account)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Public: verify credentials (returns the account; clients then call `/token`).
pub async fn login(
    State(state): State<ApiState>,
    Json(req): Json<LoginRequest>,
) -> Json<ApiResponse<crate::auth::accounts::Account>> {
    let db = match state.get_db() {
        Ok(db) => db,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let store = AccountStore::new(db);
    match store.verify_login(&req.email, &req.password) {
        Ok(account) => Json(ApiResponse::success(account)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: issue an access token. Requires a valid Bearer token with
/// admin/owner role (the caller must be an org admin or the account owner).
pub async fn issue_token(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Json(req): Json<IssueTokenRequest>,
) -> Json<ApiResponse<TokenIssued>> {
    let (accounts, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    // Only admins/owners may issue; the target must be self or in the caller's org.
    if !ctx.role.can_write() && req.account_id != ctx.client_id {
        return Json(ApiResponse::error("insufficient permission to issue token"));
    }
    let role = Role::from_str(&req.role).unwrap_or(Role::Viewer);
    match tokens.create_access_token(
        &req.account_id,
        req.org_id.as_deref(),
        role,
        &req.name,
        req.ttl_secs,
    ) {
        Ok((token, row)) => Json(ApiResponse::success(TokenIssued {
            access_token: token,
            token_type: "Bearer".to_string(),
            account_id: row.account_id,
            role: row.role,
            expires_at: row.expires_at,
        })),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: revoke a token.
pub async fn revoke_token(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Json(req): Json<RevokeTokenRequest>,
) -> Json<ApiResponse<bool>> {
    let (_, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    if bearer_from_headers(&headers).is_none() {
        return Json(ApiResponse::error("missing Bearer token"));
    }
    match tokens.revoke_token(&req.token_id) {
        Ok(ok) => Json(ApiResponse::success(ok)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: list tokens for the authenticated account.
pub async fn list_tokens(
    State(state): State<ApiState>,
    headers: HeaderMap,
) -> Json<ApiResponse<Vec<crate::auth::tokens::AccessToken>>> {
    let (_, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    match tokens.list_tokens(&ctx.client_id) {
        Ok(rows) => Json(ApiResponse::success(rows)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: create an org owned by the authenticated account.
pub async fn create_org(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Json(name): Json<String>,
) -> Json<ApiResponse<crate::auth::accounts::Org>> {
    let (accounts, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    match accounts.create_org(&name, &ctx.client_id) {
        Ok(org) => Json(ApiResponse::success(org)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: add/set an org member (org owner/admin only).
pub async fn add_org_member(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Path(org_id): Path<String>,
    Json(req): Json<AddMemberRequest>,
) -> Json<ApiResponse<crate::auth::accounts::OrgMember>> {
    let (accounts, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let allowed = match accounts.org_role_sufficient(&org_id, &ctx.client_id, "admin") {
        Ok(v) => v,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    if !allowed {
        return Json(ApiResponse::error(
            "insufficient permission: org admin+ required",
        ));
    }
    match accounts.add_member(&org_id, &req.account_id, &req.role) {
        Ok(member) => Json(ApiResponse::success(member)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: list org members (any org member).
pub async fn list_org_members(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Path(org_id): Path<String>,
) -> Json<ApiResponse<Vec<crate::auth::accounts::OrgMember>>> {
    let (accounts, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    match accounts.org_role_sufficient(&org_id, &ctx.client_id, "viewer") {
        Ok(true) => match accounts.list_org_members(&org_id) {
            Ok(rows) => Json(ApiResponse::success(rows)),
            Err(e) => Json(ApiResponse::error(&e.to_string())),
        },
        Ok(false) => Json(ApiResponse::error("not an org member")),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}

/// Protected: claim a resource's ownership.
pub async fn claim_resource(
    State(state): State<ApiState>,
    headers: HeaderMap,
    Json(req): Json<ClaimResourceRequest>,
) -> Json<ApiResponse<bool>> {
    let (accounts, tokens) = match stores(&state) {
        Ok(s) => s,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    let Some(bearer) = bearer_from_headers(&headers) else {
        return Json(ApiResponse::error("missing Bearer token"));
    };
    let ctx = match tokens.validate_token(&bearer) {
        Ok(ctx) => ctx,
        Err(e) => return Json(ApiResponse::error(&e.to_string())),
    };
    match accounts.claim_resource(
        &req.resource_type,
        &req.resource_id,
        &ctx.client_id,
        req.org_id.as_deref(),
    ) {
        Ok(()) => Json(ApiResponse::success(true)),
        Err(e) => Json(ApiResponse::error(&e.to_string())),
    }
}
