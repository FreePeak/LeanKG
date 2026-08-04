//! PostgreSQL backend for the CozoDB → PostgreSQL migration (plan Phase 2).
//!
//! Phase 2 scope: schema DDL + versioned migrations only. The SQL translator
//! (Phase 3) picks the client (sqlx per plan D1, or postgres — the crate this
//! module already uses, keeping dependencies minimal).
pub mod migrations;
