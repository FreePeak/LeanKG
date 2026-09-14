// Enterprise authorization resolution (Rust auth/accounts.rs +
// api/auth_handlers.rs): the account/org/ownership chain that turns one
// request into a capability role.
//
// Precedence, highest first:
//
//	owner      the caller's account owns the targeted resource, or owns the
//	           org the token is scoped to
//	org        the caller's org membership role, mapped onto the capability
//	           Role (owner/admin -> Admin, member -> Contributor, viewer -> Viewer)
//	token      the bearer token's own minted role
//	env        a static LEANKG_TOKEN_{ADMIN,CONTRIBUTOR,VIEWER} token
//
// With no token configured in either source the resolution is Admin/open
// (local default), the same posture classify and the middleware already use.
package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Via names the source that decided the resolved role, in precedence order.
const (
	// ViaOwner means resource or org ownership decided the role.
	ViaOwner = "owner"
	// ViaOrg means an org membership role decided the role.
	ViaOrg = "org"
	// ViaToken means the bearer token's own role decided the role.
	ViaToken = "token"
	// ViaEnv means a static environment token decided the role.
	ViaEnv = "env"
	// ViaOpen means no token was configured anywhere (local default).
	ViaOpen = "open"
)

// Target identifies the resource an authorization decision is about. The zero
// value means "no specific resource", which skips the ownership rung.
type Target struct {
	Type string
	ID   string
}

// AuthContext is the resolved enterprise caller identity. It carries both the
// capability Role that gates write/admin actions and the raw OrgRole when an
// org membership decided it, plus the token's ownership labels and scopes.
type AuthContext struct {
	AccountID string
	OrgID     string
	OrgRole   string
	Role      Role
	Scopes    []string
	TokenID   string
	Via       string
}

// CanWrite reports whether the resolved role may mutate state
// (Rust Role::can_write: admin and contributor).
func (c AuthContext) CanWrite() bool { return c.Role >= Contributor }

// CanAdmin reports whether the resolved role may run management actions
// (Rust Role::can_admin).
func (c AuthContext) CanAdmin() bool { return c.Role >= Admin }

// ScopeAllowed reports whether the token allows scope. A token without scopes
// is unrestricted: Rust declared an access_tokens.scopes column but always
// wrote an empty list and never read it, so scopes are opt-in restriction
// only — a token that lists scopes allows exactly those.
func (c AuthContext) ScopeAllowed(scope string) bool {
	if len(c.Scopes) == 0 {
		return true
	}
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Resolve resolves the caller of r with no specific resource target.
func Resolve(st store.Backend, r *http.Request) (AuthContext, error) {
	return fromEnv().resolveFor(st, r, Target{})
}

// ResolveFor resolves the caller of r, consulting resource ownership for
// target first. Revoked or expired DB tokens are errors and never fall through
// to the env fallback (revocation must win over a static copy).
func ResolveFor(st store.Backend, r *http.Request, target Target) (AuthContext, error) {
	return fromEnv().resolveFor(st, r, target)
}

// resolveFor is the resolution over one registry snapshot: the middleware
// already holds a registry, so it must not rebuild the env map per request.
func (reg *registry) resolveFor(st store.Backend, r *http.Request, target Target) (AuthContext, error) {
	bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	bearer = strings.TrimSpace(bearer)
	if hasBearer && bearer != "" {
		// 1. DB-backed tokens win: they can be minted and revoked at runtime.
		if st != nil {
			grant, found, err := Verify(st, bearer)
			if err != nil {
				return AuthContext{}, err
			}
			if found {
				return resolveGrant(st, grant, target)
			}
		}
		// 2. Env fallback (static tokens from the process environment).
		if role, ok := reg.byToken[bearer]; ok {
			return AuthContext{Role: role, Via: ViaEnv}, nil
		}
	}
	configured, err := reg.tokensConfigured(st)
	if err != nil {
		return AuthContext{}, err
	}
	if !configured {
		return AuthContext{Role: Admin, Via: ViaOpen}, nil
	}
	if !hasBearer || bearer == "" {
		return AuthContext{}, fmt.Errorf("auth: missing bearer token")
	}
	return AuthContext{}, fmt.Errorf("auth: unknown token")
}

// EffectiveRole is Resolve reduced to the role that gates actions.
func EffectiveRole(st store.Backend, r *http.Request) (Role, error) {
	ctx, err := Resolve(st, r)
	if err != nil {
		return Viewer, err
	}
	return ctx.Role, nil
}

// tokensConfigured reports whether any token source is configured at all (env
// or the DB store). No configured source means the gate is disabled.
func (reg *registry) tokensConfigured(st store.Backend) (bool, error) {
	if reg.enabled() {
		return true, nil
	}
	if st == nil {
		return false, nil
	}
	toks, err := st.TokenList()
	if err != nil {
		return false, fmt.Errorf("auth: token store: %w", err)
	}
	return len(toks) > 0, nil
}

// resolveGrant applies the owner > org role > token role ladder to a verified
// token. A token with no account label has nothing to resolve and keeps its
// minted role.
func resolveGrant(st store.Backend, g Grant, target Target) (AuthContext, error) {
	ctx := AuthContext{
		AccountID: g.AccountID,
		OrgID:     g.OrgID,
		Role:      g.Role,
		Scopes:    g.Scopes,
		TokenID:   g.TokenID,
		Via:       ViaToken,
	}
	if st == nil || g.AccountID == "" {
		return ctx, nil
	}
	// Ownership of the targeted resource outranks every role source: an owner
	// controls their own resource whatever the org or token says.
	if target.Type != "" && target.ID != "" {
		owns, err := st.IsResourceOwner(target.Type, target.ID, g.AccountID)
		if err != nil {
			return AuthContext{}, err
		}
		if owns {
			ctx.Role, ctx.Via = Admin, ViaOwner
			return ctx, nil
		}
	}
	if g.OrgID == "" {
		return ctx, nil
	}
	// Org ownership (orgs.owner_account_id) outranks a membership row.
	org, found, err := st.OrgByID(g.OrgID)
	if err != nil {
		return AuthContext{}, err
	}
	if found && org.OwnerAccountID == g.AccountID {
		ctx.OrgRole, ctx.Role, ctx.Via = OrgRoleOwner, Admin, ViaOwner
		return ctx, nil
	}
	// A membership role outranks the token's own role.
	member, found, err := st.OrgMemberOf(g.OrgID, g.AccountID)
	if err != nil {
		return AuthContext{}, err
	}
	if found && ValidOrgRole(member.Role) {
		ctx.OrgRole, ctx.Role, ctx.Via = member.Role, OrgRoleToRole(member.Role), ViaOrg
		return ctx, nil
	}
	return ctx, nil
}
