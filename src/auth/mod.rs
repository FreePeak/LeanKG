//! OAuth2-style access-token auth for protected DB resources.
//!
//! DB-backed accounts / orgs / teams / access tokens. Opaque tokens are
//! generated server-side and stored as SHA-256 hashes; validation resolves a
//! Bearer token to an [`AuthContext`] (account + role). All reads/writes go
//! through a shared [`crate::db::backend::SharedDb`] so the Postgres client
//! pool is reused — no fresh backend per call (contrast the legacy
//! `ApiKeyStore`, which opened `init_db_pg()` per call).
//!
//! See `docs/` + migration `004_auth.sql` for the schema.

pub mod accounts;
pub mod tokens;

// Re-exports serve lib consumers (auth_handlers, tests). The bin compiles the
// module tree inline and reaches into `auth::accounts::` / `auth::tokens::`
// directly, so the re-exports look unused there.
#[allow(unused_imports)]
pub use accounts::{Account, AccountStore, Org, OrgMember};
#[allow(unused_imports)]
pub use tokens::{AccessToken, AccessTokenStore};
