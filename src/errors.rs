//! FR-ZCP-12 T1 — stable error-code catalog.
//!
//! Contract: every user-facing CLI + MCP error renders as
//! `<CODE>: <cause>. Fix: <fix> (docs: <doc_anchor>)` — Stripe's `code` +
//! `doc_url` model, clig.dev's "what went wrong + how do I fix it".
//!
//! This module is deliberately dependency-free (no `crate::` imports): other
//! modules use the catalog, the catalog never depends on other modules.
//!
//! CI enforcement (tests below):
//! * every catalog entry has non-empty code / cause / fix / doc_anchor;
//! * every `LEANKG_ERROR_*` literal appearing in `src/` has a catalog entry
//!   (100% coverage — a new code without a catalog row fails the build);
//! * every catalog entry is referenced by at least one site outside this
//!   file (no dead entries).

/// One stable error code with its human cause clause, runnable fix, and doc anchor.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ErrorCode {
    /// Stable machine token, e.g. `LEANKG_ERROR_UNKNOWN_PROJECT`.
    pub code: &'static str,
    /// Human "what went wrong" clause.
    pub cause: &'static str,
    /// Runnable fix naming the concrete command / flag / env var.
    pub fix: &'static str,
    /// Doc anchor the code resolves to (README heading or in-repo doc path).
    pub doc_anchor: &'static str,
}

impl ErrorCode {
    /// Canonical static rendering: `<CODE>: <cause>. Fix: <fix> (docs: <anchor>)`.
    pub fn message(&self) -> String {
        format!(
            "{}: {}. Fix: {} (docs: {})",
            self.code, self.cause, self.fix, self.doc_anchor
        )
    }
}

pub const PG_UNREACHABLE: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_PG_UNREACHABLE",
    cause: "Postgres is not reachable at the configured URL (connection refused or timed out)",
    fix: "start Postgres (`docker compose up -d postgres`) or point LEANKG_PG_URL at your instance (`export LEANKG_PG_URL=postgresql://user:pass@host:5432/db`), then check `leankg doctor`",
    doc_anchor: "README.md#get-started",
};

pub const PG_URL_MALFORMED: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_PG_URL_MALFORMED",
    cause: "the resolved Postgres URL is malformed",
    fix: "set a full postgres:// URL including host and database (`export LEANKG_PG_URL=postgresql://user:pass@host:5432/db`) or the `db.url` key in .leankg/leankg.yaml",
    doc_anchor: "README.md#get-started",
};

pub const PROJECT_NOT_INITIALIZED: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_PROJECT_NOT_INITIALIZED",
    cause: "the target directory has no .leankg project directory",
    fix: "run `leankg add <path>` (registers and starts the background index) or `leankg init <path>` to create .leankg first",
    doc_anchor: "README.md#get-started",
};

pub const UNKNOWN_PROJECT: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_UNKNOWN_PROJECT",
    cause: "no .leankg project is registered for the requested path",
    fix: "run `leankg add <path>` (or call mcp_init with that path) so the project resolves to its own schema; queries never fall back to another project's data",
    doc_anchor: "README.md#get-started",
};

pub const AUTO_ATTACH_FAILED: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_AUTO_ATTACH_FAILED",
    cause: "auto-attach could not initialize the project in place",
    fix: "check write permission on the project directory and run `leankg add <path>` once, then retry",
    doc_anchor: "README.md#get-started",
};

pub const UNAUTHORIZED: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_UNAUTHORIZED",
    cause: "the request carried no Authorization header or the Bearer token does not match",
    fix: "start the server with `leankg mcp-http --auth <token>` (or MCP_HTTP_AUTH=<token>) and send header `Authorization: Bearer <token>`; omit --auth/MCP_HTTP_AUTH on the server to disable auth entirely",
    doc_anchor: "README.md#troubleshooting",
};

pub const UNKNOWN_TOOL: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_UNKNOWN_TOOL",
    cause: "the requested tool name is not in this server's registry",
    fix: "call `leankg_context` (the default router that serves every intent) or re-read tools/list for the complete catalog and retry with the corrected name",
    doc_anchor: "docs/archive/mcp-tools.md",
};

pub const NO_VECTORS: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_NO_VECTORS",
    cause: "no embedding vectors exist for this project, so semantic_search cannot match anything",
    fix: "use leankg_context (keyword rung) or search_code instead, and run `leankg embed` (requires the embeddings cargo feature) to build the vectors",
    doc_anchor: "src/embeddings/EMBEDDINGS.md",
};

pub const TRGM_UNAVAILABLE: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_TRGM_UNAVAILABLE",
    cause: "the pg_trgm extension is unavailable on this database, so trigram fuzzy ranking degrades to ILIKE substring recall",
    fix: "install the postgresql-contrib package and run `CREATE EXTENSION IF NOT EXISTS pg_trgm;` on the LEANKG_PG_URL database, or continue — availability is unchanged, only ranking quality drops",
    doc_anchor: "src/db/pg/migrations/007_trgm_fuzzy.sql",
};

pub const METHOD_NOT_FOUND: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_METHOD_NOT_FOUND",
    cause: "the JSON-RPC method is not implemented by this server",
    fix: "use initialize, tools/list, tools/call, resources/list, or ping; re-initialize the session after upgrading the server",
    doc_anchor: "docs/archive/mcp-tools.md",
};

pub const READ_ONLY: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_READ_ONLY",
    cause: "the server runs in read-only mode and the requested tool mutates state",
    fix: "restart the server without --read-only (`leankg mcp-http ...`) or route writes through a writable instance",
    doc_anchor: "README.md#troubleshooting",
};

pub const UNKNOWN_ACTION: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_UNKNOWN_ACTION",
    cause: "the tool was called with an action value it does not implement",
    fix: "use one of the tool's documented action values (embed_control: on|off|status; ontology_control: sync|status) — see its inputSchema in tools/list",
    doc_anchor: "docs/archive/mcp-tools.md",
};

pub const MISSING_PARAM: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_MISSING_PARAM",
    cause: "a required tool parameter was omitted or null",
    fix: "re-read tools/list for the tool's inputSchema.required and supply the named parameter",
    doc_anchor: "docs/archive/mcp-tools.md",
};

pub const PERMISSION_DENIED: ErrorCode = ErrorCode {
    code: "LEANKG_ERROR_PERMISSION_DENIED",
    cause: "the authenticated account's role is not allowed to call this tool",
    fix: "retry with an access token whose role covers this tool (DB-backed access-token store), or ask an admin to grant the role",
    doc_anchor: "docs/archive/mcp-tools.md",
};

/// The registry. Order is presentation order, not significance.
static CATALOG: &[ErrorCode] = &[
    PG_UNREACHABLE,
    PG_URL_MALFORMED,
    PROJECT_NOT_INITIALIZED,
    UNKNOWN_PROJECT,
    AUTO_ATTACH_FAILED,
    UNAUTHORIZED,
    UNKNOWN_TOOL,
    NO_VECTORS,
    TRGM_UNAVAILABLE,
    METHOD_NOT_FOUND,
    READ_ONLY,
    UNKNOWN_ACTION,
    MISSING_PARAM,
    PERMISSION_DENIED,
];

/// Every registered error code, in presentation order.
pub fn all() -> &'static [ErrorCode] {
    CATALOG
}

/// Resolve a `LEANKG_ERROR_*` code to its catalog entry.
pub fn lookup(code: &str) -> Option<&'static ErrorCode> {
    CATALOG.iter().find(|e| e.code == code)
}

/// Render a dynamic error against the catalog: `<CODE>: <cause>. Fix: <fix>
/// (docs: <anchor>)`. The anchor comes from the catalog entry for `code`;
/// an unregistered code still renders (cause + fix) so callers never dead-end.
pub fn render(code: &str, cause: &str, fix: &str) -> String {
    match lookup(code) {
        Some(entry) => format!(
            "{}: {}. Fix: {} (docs: {})",
            code, cause, fix, entry.doc_anchor
        ),
        None => format!("{code}: {cause}. Fix: {fix}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::{Path, PathBuf};

    #[test]
    fn catalog_entries_are_complete() {
        assert!(!CATALOG.is_empty(), "catalog must not be empty");
        for e in CATALOG {
            assert!(
                e.code.starts_with("LEANKG_ERROR_"),
                "{}: code must use the LEANKG_ERROR_ prefix",
                e.code
            );
            assert!(
                !e.cause.trim().is_empty(),
                "{}: cause clause must be non-empty",
                e.code
            );
            assert!(
                !e.fix.trim().is_empty(),
                "{}: runnable fix must be non-empty",
                e.code
            );
            assert!(
                !e.doc_anchor.trim().is_empty(),
                "{}: doc anchor must be non-empty",
                e.code
            );
        }
        let mut codes: Vec<&str> = CATALOG.iter().map(|e| e.code).collect();
        codes.sort_unstable();
        let before = codes.len();
        codes.dedup();
        assert_eq!(codes.len(), before, "duplicate codes in catalog");
    }

    #[test]
    fn lookup_resolves_registered_codes_only() {
        assert!(lookup("LEANKG_ERROR_UNKNOWN_PROJECT").is_some());
        assert!(lookup("LEANKG_ERROR_NO_SUCH_CODE").is_none());
        // render never dead-ends, registered or not.
        let rendered = render("LEANKG_ERROR_UNKNOWN_PROJECT", "cause", "run this");
        assert!(rendered.starts_with("LEANKG_ERROR_UNKNOWN_PROJECT: cause. Fix: run this"));
        assert!(rendered.contains("(docs: README.md#get-started)"));
        assert_eq!(
            render("LEANKG_ERROR_NO_SUCH_CODE", "cause", "run this"),
            "LEANKG_ERROR_NO_SUCH_CODE: cause. Fix: run this"
        );
    }

    /// Recursively collect `*.rs` files under `dir`.
    fn walk_rs_files(dir: &Path, out: &mut Vec<PathBuf>) {
        let Ok(entries) = std::fs::read_dir(dir) else {
            return;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                walk_rs_files(&path, out);
            } else if path.extension().is_some_and(|e| e == "rs") {
                out.push(path);
            }
        }
    }

    fn src_rs_files() -> Vec<PathBuf> {
        let manifest = Path::new(env!("CARGO_MANIFEST_DIR"));
        let mut files = Vec::new();
        walk_rs_files(&manifest.join("src"), &mut files);
        files
    }

    /// Extract every `LEANKG_ERROR_<NAME>` token from `text`.
    fn scan_error_codes(text: &str) -> Vec<String> {
        const MARKER: &str = "LEANKG_ERROR_";
        let bytes = text.as_bytes();
        let mut out = Vec::new();
        let mut cursor = 0;
        while let Some(pos) = text[cursor..].find(MARKER) {
            let start = cursor + pos;
            let mut end = start + MARKER.len();
            while end < bytes.len()
                && (bytes[end].is_ascii_uppercase()
                    || bytes[end] == b'_'
                    || bytes[end].is_ascii_digit())
            {
                end += 1;
            }
            // Require at least one name char after the marker (skips prose
            // like `LEANKG_ERROR_*`), and trim a dangling trailing underscore.
            let raw = &text[start + MARKER.len()..end];
            let name = raw.trim_end_matches('_');
            if !name.is_empty() {
                out.push(format!("{MARKER}{name}"));
            }
            cursor = end.max(start + MARKER.len());
        }
        out
    }

    /// FR-ZCP-12 T1 AC: every `LEANKG_ERROR_*` literal used in src/ has a
    /// catalog entry — 100% coverage, enforced.
    #[test]
    fn every_error_literal_in_src_has_a_catalog_entry() {
        let files = src_rs_files();
        assert!(
            files.len() > 50,
            "src walk found only {} rs files; walker is broken",
            files.len()
        );
        let mut offenders: Vec<String> = Vec::new();
        for file in files.iter().filter(|f| !f.ends_with("src/errors.rs")) {
            let Ok(text) = std::fs::read_to_string(file) else {
                continue;
            };
            for code in scan_error_codes(&text) {
                if lookup(&code).is_none() {
                    offenders.push(format!("{}: {code}", file.display()));
                }
            }
        }
        assert!(
            offenders.is_empty(),
            "LEANKG_ERROR_ literals without a catalog entry in src/errors.rs:\n{}",
            offenders.join("\n")
        );
    }

    /// FR-ZCP-12 T1 AC: no dead entries — every catalog constant is
    /// referenced by at least one site outside src/errors.rs.
    #[test]
    fn every_catalog_entry_is_referenced_by_a_site() {
        const CONSTS: &[(&str, &str)] = &[
            ("PG_UNREACHABLE", "LEANKG_ERROR_PG_UNREACHABLE"),
            ("PG_URL_MALFORMED", "LEANKG_ERROR_PG_URL_MALFORMED"),
            (
                "PROJECT_NOT_INITIALIZED",
                "LEANKG_ERROR_PROJECT_NOT_INITIALIZED",
            ),
            ("UNKNOWN_PROJECT", "LEANKG_ERROR_UNKNOWN_PROJECT"),
            ("AUTO_ATTACH_FAILED", "LEANKG_ERROR_AUTO_ATTACH_FAILED"),
            ("UNAUTHORIZED", "LEANKG_ERROR_UNAUTHORIZED"),
            ("UNKNOWN_TOOL", "LEANKG_ERROR_UNKNOWN_TOOL"),
            ("NO_VECTORS", "LEANKG_ERROR_NO_VECTORS"),
            ("TRGM_UNAVAILABLE", "LEANKG_ERROR_TRGM_UNAVAILABLE"),
            ("METHOD_NOT_FOUND", "LEANKG_ERROR_METHOD_NOT_FOUND"),
            ("READ_ONLY", "LEANKG_ERROR_READ_ONLY"),
            ("UNKNOWN_ACTION", "LEANKG_ERROR_UNKNOWN_ACTION"),
            ("MISSING_PARAM", "LEANKG_ERROR_MISSING_PARAM"),
            ("PERMISSION_DENIED", "LEANKG_ERROR_PERMISSION_DENIED"),
        ];
        assert_eq!(
            CONSTS.len(),
            CATALOG.len(),
            "const table out of sync with catalog"
        );
        let files: Vec<PathBuf> = src_rs_files()
            .into_iter()
            .filter(|f| !f.ends_with("src/errors.rs"))
            .collect();
        let texts: Vec<String> = files
            .iter()
            .filter_map(|f| std::fs::read_to_string(f).ok())
            .collect();
        for (name, code) in CONSTS {
            let needle = format!("errors::{name}");
            assert!(
                texts.iter().any(|t| t.contains(&needle)),
                "catalog entry {code} has no use site (expected `errors::{name}` outside src/errors.rs)"
            );
        }
    }
}
