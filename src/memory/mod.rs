//! FR-ZCP-07: harness memory-backend adjacency.
//!
//! LeanKG as a harness memory via MCP — mnemopi-compatible bank naming,
//! scoping matrix, and the retain/recall contract with the
//! `retained_through_user_turn` cursor. Storage is file-backed JSONL per
//! bank under `<project>/.leankg/memory/<bank>.jsonl` (same discipline as
//! the agent diary), so the sqlite/PG split does not apply.

pub mod bank;
pub mod store;

pub use bank::{mnemopi_bank_name, sanitize_bank, BankScope};
pub use store::MemoryStore;

#[cfg(test)]
mod tests;
