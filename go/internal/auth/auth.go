// Package auth is the RBAC middleware for the Go engine's HTTP surfaces
// (REST + ConnectRPC share it). Roles mirror the Rust line (FR parity with
// src/mcp/auth.rs): Admin (everything), Contributor (writes+reads),
// Viewer (reads only). Bearer tokens come from the environment; absence of
// any configured token disables the gate (local default = no auth, same
// posture as the Rust local mode).
package auth

import (
	"fmt"
	"net/http"
	"os"
	"strings"
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

// Classify maps a request to a role. With no tokens configured everything is
// Admin (local default). With tokens configured, a missing/unknown token is
// a rejection — never a silent escalation.
func (reg *registry) classify(r *http.Request) (Role, error) {
	if !reg.enabled() {
		return Admin, nil
	}
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return Viewer, fmt.Errorf("auth: missing bearer token")
	}
	role, ok := reg.byToken[strings.TrimPrefix(h, "Bearer ")]
	if !ok {
		return Viewer, fmt.Errorf("auth: unknown token")
	}
	return role, nil
}

// writeActions are the REST/RPC paths that mutate state.
var writeActions = map[string]bool{
	"/api/v1/import":                true,
	"/leankg.v1.LeanKG/Import":      true, // ConnectRPC unary path
	"/leankg.v1.LeanKG/MemoryWrite": true, // reserved (not yet a service method)
}

// Middleware gates a handler tree. Reads are always allowed for any valid
// role; writes require Contributor or Admin. 401 on missing token, 403 on
// insufficient role.
func Middleware(next http.Handler) http.Handler {
	reg := fromEnv()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, err := reg.classify(r)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		if writeActions[r.URL.Path] && role < Contributor {
			http.Error(w, `{"error":"forbidden: writes require contributor or admin"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
