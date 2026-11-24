// Package auth is the RBAC middleware for the Go engine's HTTP surfaces
// (REST + ConnectRPC share it). Roles mirror the Rust line (FR parity with
// src/mcp/auth.rs): Admin (everything), Contributor (writes+reads), Viewer
// (reads only).
// Bearer tokens resolve DB-first: the internal/store `tokens` table (minted
// and revocable at runtime) wins over the env vars
// LEANKG_TOKEN_{ADMIN,CONTRIBUTOR,VIEWER}, which stay as the static fallback;
// absence of any configured token and of any site-admin account disables the gate (local default = no
// auth, same posture as the Rust local mode).
package auth

import (
	"context"
	"net/http"
	"os"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/httpx"
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
func (reg *registry) classify(st Store, r *http.Request) (Role, error) {
	ctx, err := reg.resolveFor(st, r, Target{})
	if err != nil {
		return Viewer, err
	}
	return ctx.Role, nil
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
func RoleForRequestWithStore(st Store, r *http.Request) (Role, error) {
	return fromEnv().classify(st, r)
}

// GateEnabled reports whether HTTP callers must authenticate: a live token (env
// or DB-backed) is configured, or a site-admin account exists. serve uses it to
// refuse a non-loopback bind with the gate open.
func GateEnabled(ctx context.Context, st Store) (bool, error) {
	return fromEnv().tokensConfigured(ctx, st)
}

// RoleOrAnonymous resolves an HTTP caller for the MCP gate. A request with no
// Authorization header is anonymous while the gate is on, and Admin while it is
// off (the local default). A lookup error that leaves the gate state unknown
// counts as on: the caller is anonymous, never Admin. A request that carries an
// Authorization header resolves exactly like RoleForRequestWithStore, so a bad
// or expired token is still an error.
func RoleOrAnonymous(st Store, r *http.Request) (role Role, anonymous bool, err error) {
	ac, anonymous, err := AuthOrAnonymous(st, r)
	return ac.Role, anonymous, err
}

// AuthOrAnonymous is RoleOrAnonymous with the caller's whole identity: the role
// and the token's project grant. The anonymous and open cases carry no grant.
func AuthOrAnonymous(st Store, r *http.Request) (ac AuthContext, anonymous bool, err error) {
	if r.Header.Get("Authorization") != "" {
		resolved, err := Resolve(st, r)
		return resolved, false, err
	}
	gated, gateErr := GateEnabled(r.Context(), st)
	if gateErr != nil || gated {
		return AuthContext{Role: Viewer}, true, nil
	}
	return AuthContext{Role: Admin, Via: ViaOpen}, false, nil
}

// AllowedTool reports whether role may invoke the MCP tool named tool.
// Reads (query/status) are open to any valid role; writes need Contributor+.
func AllowedTool(role Role, tool string) bool {
	if WriteTools[tool] {
		return role >= Contributor
	}
	return true
}

// Middleware resolves the caller once per request and stores its role in the
// request context (see WithRole). It answers 401 on a missing or unknown token.
// Each route enforces its own role with Require (CX-49).
//
// Token precedence: DB-backed tokens (st, the internal/store `tokens` table —
// minted and revocable at runtime) are consulted first; the env vars
// LEANKG_TOKEN_{ADMIN,CONTRIBUTOR,VIEWER} are the fallback. With no token
// configured in either source and no site-admin account exists, the gate is disabled (local default = Admin).
//
// Ceiling: gate enablement counts LIVE tokens only — soft-revoked and expired
// DB rows do not keep the gate on (they can never authenticate). Operators who
// must never run open should keep at least one env token set.
//
// NOTE: this cannot gate MCP — its capability lives in the JSON-RPC body.
// MCP servers must call RoleForRequest/AllowedTool inside the tool handler
// (see internal/mcp, which does).
func Middleware(next http.Handler) http.Handler {
	return MiddlewareWithStore(nil, next)
}

// MiddlewareWithStore is Middleware with a DB-backed token store wired in
// ahead of the env fallback (see Middleware for the precedence contract).
func MiddlewareWithStore(st Store, next http.Handler) http.Handler {
	reg := fromEnv()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, err := reg.resolveFor(st, r, Target{})
		if err != nil {
			httpx.WriteError(w, errs.NewError(errs.Unauthorized, errs.Unauthorized.Cause, ""))
			return
		}
		ctx := WithAuth(r.Context(), ac)
		next.ServeHTTP(w, r.WithContext(WithRole(ctx, ac.Role)))
	})
}
