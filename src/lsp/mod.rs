//! US-CBM-B1 / FR-B03..04: generic LSP bridge.
//!
//! Exposes the building blocks for talking to any LSP server that
//! supports the standard `textDocument/definition` and
//! `textDocument/references` methods. LeanKG's typed resolve path
//! routes through `LspBridge` when the `typed_resolve` feature
//! flag is enabled and a server is configured for the language.
//!
//! Configuration lives in the project's `leankg.yaml` under the
//! `lsp:` block. See `config.rs` for the schema.
pub mod bridge;
pub mod client;
pub mod config;

pub use bridge::LspBridge;
pub use client::LspRequest;
