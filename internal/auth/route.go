package auth

import (
	"context"
	"net/http"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/httpx"
)

// roleKey is the context key the gate stores the caller's role under.
type roleKey struct{}

// WithRole returns ctx carrying the caller's resolved role. The gate sets it
// once per request; Require and handlers read it back with RoleFrom.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// authKey is the context key the gate stores the caller's full identity under.
type authKey struct{}

// WithAuth returns ctx carrying the caller's resolved identity, so the layers
// that choose a project (the router, the portfolio fan-out) can apply its grant.
func WithAuth(ctx context.Context, ac AuthContext) context.Context {
	return context.WithValue(ctx, authKey{}, ac)
}

// AuthFrom returns the caller's identity from ctx. ok is false when no gate ran
// (stdio, a local listener): such a caller carries no project grant.
func AuthFrom(ctx context.Context) (AuthContext, bool) {
	ac, ok := ctx.Value(authKey{}).(AuthContext)
	return ac, ok
}

// RoleFrom returns the caller's role from ctx. ok is false when no gate ran
// for the request.
func RoleFrom(ctx context.Context) (Role, bool) {
	role, ok := ctx.Value(roleKey{}).(Role)
	return role, ok
}

// Require serves h only to a caller that holds at least role. Every route on a
// gated listener declares its role here at registration (CX-49); a route
// without a declaration is caught by the route guard test. A request with no
// role in its context, because no gate ran, is denied.
func Require(role Role, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := RoleFrom(r.Context())
		switch {
		case !ok:
			httpx.WriteError(w, errs.NewError(errs.Unauthorized, errs.Unauthorized.Cause, ""))
		case got < role:
			httpx.WriteError(w, errs.NewError(errs.PermissionDenied, errs.PermissionDenied.Cause, ""))
		default:
			h.ServeHTTP(w, r)
		}
	})
}

// Public declares a route with no role floor: any caller the listener admits.
// The listener gate, when it has one, has already authenticated the caller. It
// also marks a route whose handler decides access itself (the auth bootstrap)
// and a catch-all that delegates to a mux which declares its own roles.
func Public(h http.Handler) http.Handler { return h }
