//! US-CBM-B1 / FR-B03..04 / FR-LSP-A..D: hybrid typed resolve + LSP bridge.
//!
//! Two tiers:
//! 1. **Hybrid (in-process)** — [`hybrid`] + [`type_registry`] resolve
//!    CALLS edges during indexing with no spawn (Go/TS MVP).
//! 2. **External LSP bridge** — JSON-RPC to gopls / tsserver / … via
//!    [`LspBridge`] for on-demand `resolve_with_lsp` / `leankg lsp-resolve`.
//!
//! Configuration lives in the project's `leankg.yaml` under the
//! `lsp:` block. See `config.rs` for the schema; `leankg init --with-lsp`
//! writes a prefab catalog block (FR-LSP-B / REL-039).
pub mod bridge;
pub mod client;
pub mod config;
pub mod hybrid;
pub mod registry;
pub mod type_registry;

pub use bridge::LspBridge;
pub use client::LspRequest;
pub use hybrid::apply_typed_resolve;
pub use registry::{detect_language, LspServerSpec};
pub use type_registry::TypeRegistry;
