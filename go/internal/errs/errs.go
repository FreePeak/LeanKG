// Package errs is the FR-ZCP-12 T1 stable error-code catalog.
//
// Contract (ported from the Rust reference, src/errors.rs): every user-facing
// CLI + MCP error renders as
//
//	<CODE>: <cause>. Fix: <fix> (docs: <doc_anchor>)
//
// i.e. Stripe's `code` + `doc_url` model with clig.dev's "what went wrong +
// how do I fix it". The package is deliberately dependency-free: other
// packages use the catalog, the catalog never depends on them.
//
// The test suite enforces three properties:
//
//   - every catalog entry has non-empty code / cause / fix / doc anchor, and
//     no code appears twice;
//   - every `LEANKG_ERROR_*` literal appearing anywhere in the Go module has a
//     catalog entry (a new code without a catalog row fails the build);
//   - every catalog entry is referenced by at least one site outside this
//     package (no dead entries).
package errs

import (
	"fmt"
	"strings"
)

// ErrorCode is one stable error code with its human cause clause, runnable fix,
// and doc anchor.
type ErrorCode struct {
	// Code is the stable machine token, e.g. `LEANKG_ERROR_UNKNOWN_PROJECT`.
	Code string
	// Cause is the human "what went wrong" clause.
	Cause string
	// Fix names the concrete command / flag / env var that resolves it.
	Fix string
	// DocAnchor is the doc the code resolves to (README heading or in-repo doc).
	DocAnchor string
}

// Message is the canonical static rendering of the catalog entry:
// `<CODE>: <cause>. Fix: <fix> (docs: <anchor>)`.
func (e ErrorCode) Message() string {
	return fmt.Sprintf("%s: %s. Fix: %s (docs: %s)", e.Code, e.Cause, e.Fix, e.DocAnchor)
}

// Render is the canonical rendering of a dynamic cause+fix against this code's
// doc anchor: `<CODE>: <cause>. Fix: <fix> (docs: <anchor>)`.
func (e ErrorCode) Render(cause, fix string) string {
	return fmt.Sprintf("%s: %s. Fix: %s (docs: %s)", e.Code, cause, fix, e.DocAnchor)
}

// PGUnreachable: Postgres was explicitly requested but is not answering.
var PGUnreachable = ErrorCode{
	Code:      "LEANKG_ERROR_PG_UNREACHABLE",
	Cause:     "Postgres is not reachable at the configured URL (connection refused or timed out)",
	Fix:       "Postgres is only used when explicitly requested (LEANKG_DB_ENGINE=postgres + LEANKG_PG_URL); ensure the instance is reachable, or drop back to the sqlite default by unsetting LEANKG_DB_ENGINE/LEANKG_PG_URL, then check `leankg doctor`",
	DocAnchor: "README.md#get-started",
}

// PGURLMalformed: the resolved Postgres URL cannot be parsed.
var PGURLMalformed = ErrorCode{
	Code:      "LEANKG_ERROR_PG_URL_MALFORMED",
	Cause:     "the resolved Postgres URL is malformed",
	Fix:       "set a full postgres:// URL including host and database (`export LEANKG_PG_URL=postgresql://user:pass@host:5432/db`) or the `db.url` key in .leankg/leankg.yaml",
	DocAnchor: "README.md#get-started",
}

// ProjectNotInitialized: the target directory has no .leankg project.
var ProjectNotInitialized = ErrorCode{
	Code:      "LEANKG_ERROR_PROJECT_NOT_INITIALIZED",
	Cause:     "the target directory has no .leankg project directory",
	Fix:       "run `leankg index <path>` (registers the project and builds its index) or open it through `leankg serve` to create .leankg first",
	DocAnchor: "README.md#get-started",
}

// UnknownProject: no project is registered for the requested path.
var UnknownProject = ErrorCode{
	Code:      "LEANKG_ERROR_UNKNOWN_PROJECT",
	Cause:     "no .leankg project is registered for the requested path",
	Fix:       "run `leankg index <path>` for that path (or select it via LEANKG_PROJECT_DIRS) so it resolves to its own store; queries never fall back to another project's data",
	DocAnchor: "README.md#get-started",
}

// AutoAttachFailed: nearest-project auto-attach could not initialize in place.
var AutoAttachFailed = ErrorCode{
	Code:      "LEANKG_ERROR_AUTO_ATTACH_FAILED",
	Cause:     "auto-attach could not initialize the project in place",
	Fix:       "check write permission on the project directory and run `leankg index <path>` once, then retry",
	DocAnchor: "README.md#get-started",
}

// Unauthorized: no Authorization header, or the Bearer token does not match.
var Unauthorized = ErrorCode{
	Code:      "LEANKG_ERROR_UNAUTHORIZED",
	Cause:     "the request carried no Authorization header or the Bearer token does not match",
	Fix:       "start the server with `leankg serve --http <addr> --auth <token>` (or LEANKG_AUTH_TOKEN=<token>) and send header `Authorization: Bearer <token>`; omit the flag/env on the server to disable auth entirely",
	DocAnchor: "README.md#troubleshooting",
}

// UnknownTool: the requested tool name is not in this server's registry.
var UnknownTool = ErrorCode{
	Code:      "LEANKG_ERROR_UNKNOWN_TOOL",
	Cause:     "the requested tool name is not in this server's registry",
	Fix:       "call the `query` tool (the default router that serves every intent) or re-read tools/list for the complete catalog and retry with the corrected name",
	DocAnchor: "docs/archive/mcp-tools.md",
}

// NoVectors: no embeddings exist, so the semantic rung cannot match.
var NoVectors = ErrorCode{
	Code:      "LEANKG_ERROR_NO_VECTORS",
	Cause:     "no embedding vectors exist for this project, so the semantic rung cannot match anything",
	Fix:       "use a keyword rung (query action exact/fuzzy or search) instead, and run `leankg-embed` to build the vectors",
	DocAnchor: "src/embeddings/EMBEDDINGS.md",
}

// TrgmUnavailable: pg_trgm is missing, so fuzzy ranking degrades to ILIKE.
var TrgmUnavailable = ErrorCode{
	Code:      "LEANKG_ERROR_TRGM_UNAVAILABLE",
	Cause:     "the pg_trgm extension is unavailable on this database, so trigram fuzzy ranking degrades to ILIKE substring recall",
	Fix:       "install the postgresql-contrib package and run `CREATE EXTENSION IF NOT EXISTS pg_trgm;` on the LEANKG_PG_URL database, or continue — availability is unchanged, only ranking quality drops",
	DocAnchor: "src/db/pg/migrations/007_trgm_fuzzy.sql",
}

// MethodNotFound: the JSON-RPC method is not implemented by this server.
var MethodNotFound = ErrorCode{
	Code:      "LEANKG_ERROR_METHOD_NOT_FOUND",
	Cause:     "the JSON-RPC method is not implemented by this server",
	Fix:       "use initialize, tools/list, tools/call, resources/list, or ping; re-initialize the session after upgrading the server",
	DocAnchor: "docs/archive/mcp-tools.md",
}

// ReadOnly: a mutating tool was called against a read-only server.
var ReadOnly = ErrorCode{
	Code:      "LEANKG_ERROR_READ_ONLY",
	Cause:     "the server runs in read-only mode and the requested tool mutates state",
	Fix:       "restart the server without `--read-only` (`leankg serve ...`) or route writes through a writable instance",
	DocAnchor: "README.md#troubleshooting",
}

// UnknownAction: the tool was called with an action value it does not implement.
var UnknownAction = ErrorCode{
	Code:      "LEANKG_ERROR_UNKNOWN_ACTION",
	Cause:     "the tool was called with an action value it does not implement",
	Fix:       "use one of the tool's documented action values (see the tool's inputSchema `action` enum in tools/list for the exact set)",
	DocAnchor: "docs/archive/mcp-tools.md",
}

// MissingParam: a required tool parameter was omitted or null.
var MissingParam = ErrorCode{
	Code:      "LEANKG_ERROR_MISSING_PARAM",
	Cause:     "a required tool parameter was omitted or null",
	Fix:       "re-read tools/list for the tool's inputSchema.required and supply the named parameter",
	DocAnchor: "docs/archive/mcp-tools.md",
}

// PermissionDenied: the caller's role does not cover the requested tool.
var PermissionDenied = ErrorCode{
	Code:      "LEANKG_ERROR_PERMISSION_DENIED",
	Cause:     "the authenticated account's role is not allowed to call this tool",
	Fix:       "retry with a token whose role covers this tool, or ask an admin to grant the role (roles: reader, contributor, admin)",
	DocAnchor: "docs/archive/mcp-tools.md",
}

// Catalog is the registry, in presentation order (not significance).
var Catalog = []ErrorCode{
	PGUnreachable,
	PGURLMalformed,
	ProjectNotInitialized,
	UnknownProject,
	AutoAttachFailed,
	Unauthorized,
	UnknownTool,
	NoVectors,
	TrgmUnavailable,
	MethodNotFound,
	ReadOnly,
	UnknownAction,
	MissingParam,
	PermissionDenied,
}

// All returns every registered error code, in presentation order.
func All() []ErrorCode { return Catalog }

// Codes returns every registered code string, in presentation order.
func Codes() []string {
	out := make([]string, 0, len(Catalog))
	for _, e := range Catalog {
		out = append(out, e.Code)
	}
	return out
}

// Lookup resolves a `LEANKG_ERROR_*` code to its catalog entry.
func Lookup(code string) (ErrorCode, bool) {
	for _, e := range Catalog {
		if e.Code == code {
			return e, true
		}
	}
	return ErrorCode{}, false
}

// Render renders a dynamic error against the catalog: `<CODE>: <cause>. Fix:
// <fix> (docs: <anchor>)`. The anchor comes from the catalog entry for `code`;
// an unregistered code still renders (cause + fix) so callers never dead-end.
func Render(code, cause, fix string) string {
	entry, ok := Lookup(code)
	if ok {
		return entry.Render(cause, fix)
	}
	return fmt.Sprintf("%s: %s. Fix: %s", code, cause, fix)
}

// Error is a rendered catalog error that also satisfies the error interface, so
// a call site can return it directly (MCP tool results, REST bodies) and keep
// the code/cause/fix/anchor fields for structured output.
type Error struct {
	Entry ErrorCode
	Cause string
	Fix   string
}

// NewError builds a catalog error carrying a dynamic cause and fix (the fix
// falls back to the catalog's own when empty).
func NewError(entry ErrorCode, cause, fix string) *Error {
	if strings.TrimSpace(fix) == "" {
		fix = entry.Fix
	}
	return &Error{Entry: entry, Cause: cause, Fix: fix}
}

// NewErrorFor builds a catalog error for a registered code. Unknown codes yield
// a generic entry carrying the code itself, so nothing dead-ends.
func NewErrorFor(code, cause, fix string) *Error {
	if entry, ok := Lookup(code); ok {
		return NewError(entry, cause, fix)
	}
	return &Error{Entry: ErrorCode{Code: code, DocAnchor: ""}, Cause: cause, Fix: fix}
}

// Code is the stable machine token.
func (e *Error) Code() string { return e.Entry.Code }

// Error renders the full contract string.
func (e *Error) Error() string {
	if e.Entry.DocAnchor == "" {
		return fmt.Sprintf("%s: %s. Fix: %s", e.Entry.Code, e.Cause, e.Fix)
	}
	return e.Entry.Render(e.Cause, e.Fix)
}

// Unwrap keeps *Error comparable with errors.Is/As over identical codes.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && other.Entry.Code == e.Entry.Code
}
