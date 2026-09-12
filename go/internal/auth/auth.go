// Package auth is the RBAC middleware for the Go engine's HTTP surfaces
// (REST + ConnectRPC share it). Roles mirror the Rust line (FR parity with
// src/mcp/auth.rs): Admin (everything), Contributor (writes+reads), Viewer
// (reads only).
// Bearer tokens resolve DB-first: the internal/store `tokens` table (minted
// and revocable at runtime) wins over the env vars
// LEANKG_TOKEN_{ADMIN,CONTRIBUTOR,VIEWER}, which stay as the static fallback;
// absence of any configured token disables the gate (local default = no
// auth, same posture as the Rust local mode).
package auth

import (
	"net/http"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Role is a capability class.
type Role int

const (
	// Viewer reads only (query, status, memory reads).
	Viewer Role = iota
	// Contributor adds write access (import, memory writes).
	Contributor
	// Admin adds management actions.
	Admin
)

func (r Role) String() string {
	switch r {
	case Admin:
		return "admin"
	case Contributor:
		return "contributor"
	default:
		return "viewer"
	}
}

// token -> role, built from env:
//
//	LEANKG_TOKEN_ADMIN=<token>
//	LEANKG_TOKEN_CONTRIBUTOR=<token>
//	LEANKG_TOKEN_VIEWER=<token>
type registry struct{ byToken map[string]Role }

func fromEnv() *registry {
	reg := &registry{byToken: map[string]Role{}}
	for _, pair := range []struct {
		env  string
		role Role
	}{
		{"LEANKG_TOKEN_ADMIN", Admin},
		{"LEANKG_TOKEN_CONTRIBUTOR", Contributor},
		{"LEANKG_TOKEN_VIEWER", Viewer},
	} {
		if tok := os.Getenv(pair.env); tok != "" {
			reg.byToken[tok] = pair.role
		}
	}
	return reg
}

// enabled reports whether any token is configured.
func (reg *registry) enabled() bool { return len(reg.byToken) > 0 }

// Classify maps a request to a role, consulting the DB token store FIRST
// (minted/revocable at runtime) and the env fallback second. With no tokens
// configured in either source everything is Admin (local default); with
// tokens configured, a missing/unknown token is a rejection — never a
// silent escalation.
//
// A DB token that is revoked or expired is rejected outright, without falling
// through to the env fallback: revocation is an operator action and must win
// over a static copy of the same secret.
//
// Classify is the enterprise resolution (ResolveFor) reduced to the capability
// role: it resolves the same way, then keeps only the Role. The gate is
// process-scoped, so there is no resource target to check — the org rungs
// still refine a token that carries account/org labels.
func (reg *registry) classify(st store.Backend, r *http.Request) (Role, error) {
	ctx, err := reg.resolveFor(st, r, Target{})
	if err != nil {
		return Viewer, err
	}
	return ctx.Role, nil
}

// writePrefixes are path PREFIXES whose routes mutate state. Prefix matching
// (not exact equality) is required: parameterized routes such as
// /api/v1/memory/banks/{bank}/memories can never match a literal-path map,
// which silently left hindsight memory retain open to Viewers.
var writePrefixes = []string{
	"/api/v1/import",
	"/leankg.v1.LeanKG/Import",
}

// isWritePath reports whether a request path targets a mutating route.
// Hindsight memory retain is the parameterized exception: its route ends in
// /memories (recall, a read, ends in /recall).
func isWritePath(path string) bool {
	for _, p := range writePrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return strings.HasPrefix(path, "/api/v1/memory/banks/") && strings.HasSuffix(path, "/memories")
}

// WriteTools are the MCP tool names that mutate state (all import actions).
// Capability MUST be classified from the tool name in the JSON-RPC body —
// `writeActions` paths cannot see it.
var WriteTools = map[string]bool{"import": true}

// RoleForRequest classifies the caller role from the Authorization header
// against env-configured tokens only (MCP uses this; it has no store handle).
// No configured tokens = Admin (local default); otherwise a missing/unknown
// token is an error.
func RoleForRequest(r *http.Request) (Role, error) {
	return fromEnv().classify(nil, r)
}

// RoleForRequestWithStore classifies like RoleForRequest but consults the
// DB-backed token store FIRST (revocable at runtime), then the env vars.
func RoleForRequestWithStore(st store.Backend, r *http.Request) (Role, error) {
	return fromEnv().classify(st, r)
}

// AllowedTool reports whether role may invoke the MCP tool named tool.
// Reads (query/status) are open to any valid role; writes need Contributor+.
func AllowedTool(role Role, tool string) bool {
	if WriteTools[tool] {
		return role >= Contributor
	}
	return true
}

// Middleware gates a handler tree by request PATH. Reads are always allowed
// for any valid role; path-level writes require Contributor or Admin. 401 on
// missing token, 403 on insufficient role.
//
// Token precedence: DB-backed tokens (st, the internal/store `tokens` table —
// minted and revocable at runtime) are consulted first; the env vars
// LEANKG_TOKEN_{ADMIN,CONTRIBUTOR,VIEWER} are the fallback. With no token
// configured in either source the gate is disabled (local default = Admin).
//
// Ceiling: gate enablement is presence-based — it counts token ROWS, so a
// soft-revoked token (TokenRevoke) keeps the gate on while refusing every
// request, and only deleting the last row (TokenDelete) re-disables it and
// returns the process to the local default (Admin). Operators who must never
// run open should keep at least one env token set.
//
// NOTE: this cannot gate MCP — its capability lives in the JSON-RPC body.
// MCP servers must call RoleForRequest/AllowedTool inside the tool handler
// (see internal/mcp, which does).
func Middleware(next http.Handler) http.Handler {
	return MiddlewareWithStore(nil, next)
}

// MiddlewareWithStore is Middleware with a DB-backed token store wired in
// ahead of the env fallback (see Middleware for the precedence contract).
func MiddlewareWithStore(st store.Backend, next http.Handler) http.Handler {
	reg := fromEnv()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, err := reg.classify(st, r)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if isWritePath(r.URL.Path) && role < Contributor {
			http.Error(w, `{"error":"forbidden: writes require contributor or admin"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
