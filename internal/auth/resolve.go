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
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// projectScopePrefix marks a scope that names a project the token may reach.
const projectScopePrefix = "project:"

// errUnknownToken is the one rejection for a bearer token that does not
// authenticate: an unknown token, and a token whose existing account is not active. The
// same error on both paths keeps the 401 from revealing the account state.
var errUnknownToken = errors.New("auth: unknown token")

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
	// ViaLocal means no caller reached the engine at all (stdio, the CLI): the
	// answer names no request identity, so it is an unrestricted local admin.
	ViaLocal = "local"
	// ViaAnonymous means an HTTP caller sent no Authorization header while the
	// gate is on. It holds no grant, so it reaches no project.
	ViaAnonymous = "anonymous"
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

// ProjectRestricted reports whether the token names projects at all. A token
// with no project: scope is unrestricted for projects, whatever its other
// scopes are.
func (c AuthContext) ProjectRestricted() bool {
	return c.Via == ViaAnonymous || len(c.ProjectScopes()) > 0
}

// ProjectScopes returns the caller's project: scopes, the part of its grant a
// token minted by this caller may carry. Nil for an unrestricted caller.
func (c AuthContext) ProjectScopes() []string {
	var out []string
	for _, s := range c.Scopes {
		if strings.HasPrefix(s, projectScopePrefix) {
			out = append(out, s)
		}
	}
	return out
}

// ProjectAllowed reports whether the token may reach the registered project
// called name (the basename of its directory, the name the router serves it
// by). shared is true when another registered project has the same basename:
// a grant by name cannot tell those directories apart, so the name matches no
// grant. An unrestricted token reaches every project; a restricted one reaches
// only the names its project: scopes grant, by the same exact match as
// ScopeAllowed. An anonymous caller has no grant, so it reaches none (its empty
// scope list must not read as unrestricted).
func (c AuthContext) ProjectAllowed(name string, shared bool) bool {
	if !c.ProjectRestricted() {
		return true
	}
	return c.Via != ViaAnonymous && !shared && c.ScopeAllowed(projectScopePrefix+name)
}

// ProjectDenied is the refusal for a project the caller's grant does not cover.
// It names the selector the caller sent and nothing else: a registered project
// that is withheld and a name that matches nothing get the same answer, so the
// refusal cannot be used to probe which projects exist.
func ProjectDenied(selector string) error {
	cause := fmt.Sprintf("project %q is not available to this token", selector)
	if selector == "" {
		cause = "the project this endpoint serves is not available to this token"
	}
	return errs.NewError(errs.PermissionDenied, cause, "use a token whose project: scopes grant this project")
}

// Resolve resolves the caller of r with no specific resource target.
func Resolve(st Store, r *http.Request) (AuthContext, error) {
	return fromEnv().resolveFor(st, r, Target{})
}

// ResolveFor resolves the caller of r, consulting resource ownership for
// target first. Revoked or expired DB tokens are errors and never fall through
// to the env fallback (revocation must win over a static copy).
func ResolveFor(st Store, r *http.Request, target Target) (AuthContext, error) {
	return fromEnv().resolveFor(st, r, target)
}

// resolveFor is the resolution over one registry snapshot: the middleware
// already holds a registry, so it must not rebuild the env map per request.
func (reg *registry) resolveFor(st Store, r *http.Request, target Target) (AuthContext, error) {
	bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	bearer = strings.TrimSpace(bearer)
	if hasBearer && bearer != "" {
		// 1. DB-backed tokens win: they can be minted and revoked at runtime.
		if st != nil {
			grant, found, err := Verify(r.Context(), st, bearer)
			if err != nil {
				return AuthContext{}, err
			}
			if found {
				return resolveGrant(r.Context(), st, grant, target)
			}
		}
		// 2. Env fallback (static tokens from the process environment).
		if role, ok := reg.byToken[bearer]; ok {
			return AuthContext{Role: role, Via: ViaEnv}, nil
		}
	}
	configured, err := reg.tokensConfigured(r.Context(), st)
	if err != nil {
		return AuthContext{}, err
	}
	if !configured {
		return AuthContext{Role: Admin, Via: ViaOpen}, nil
	}
	if !hasBearer || bearer == "" {
		return AuthContext{}, fmt.Errorf("auth: missing bearer token")
	}
	return AuthContext{}, errUnknownToken
}

// EffectiveRole is Resolve reduced to the role that gates actions.
func EffectiveRole(st Store, r *http.Request) (Role, error) {
	ctx, err := Resolve(st, r)
	if err != nil {
		return Viewer, err
	}
	return ctx.Role, nil
}

// tokensConfigured reports whether the gate is on: any LIVE token source (env
// or the DB store) or a site-admin account. Soft-revoked and expired DB rows do
// not count: they can never authenticate, and treating them as "configured"
// locked local REST behind 401 forever after a smoke test left a revoked row
// behind. With no live token and no site admin the gate is disabled (local
// default = Admin).
//
// A site admin keeps the gate on because it is the account that logs in through
// the authenticate tool, so the endpoints it protects must demand that login.
// An error from either store probe is returned to the caller, which fails closed.
//
// Ceiling: a soft-revoked row no longer keeps the gate on. Operators who must
// never run open should keep at least one env token set (LEANKG_TOKEN_*), which
// still enables the gate regardless of DB state.
func (reg *registry) tokensConfigured(ctx context.Context, st Store) (bool, error) {
	if reg.enabled() {
		return true, nil
	}
	if st == nil {
		return false, nil
	}
	toks, err := st.TokenList(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: token store: %w", store.Fail("token list", err))
	}
	now := time.Now().Unix()
	for _, t := range toks {
		if t.RevokedAt != 0 {
			continue
		}
		if t.ExpiresAt != 0 && t.ExpiresAt <= now {
			continue
		}
		return true, nil
	}
	admin, err := st.AdminExists(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: admin store: %w", store.Fail("admin exists", err))
	}
	return admin, nil
}

// resolveGrant applies the owner > org role > token role ladder to a verified
// token. A token with no account label has nothing to resolve and keeps its
// minted role.
func resolveGrant(ctx context.Context, st Store, g Grant, target Target) (AuthContext, error) {
	ac := AuthContext{
		AccountID: g.AccountID,
		OrgID:     g.OrgID,
		Role:      g.Role,
		Scopes:    g.Scopes,
		TokenID:   g.TokenID,
		Via:       ViaToken,
	}
	if st == nil || g.AccountID == "" {
		return ac, nil
	}
	// A token minted for an existing account works only while that account is
	// active. An account id with no row is a legacy free-form label and keeps
	// its minted role.
	acct, found, err := st.AccountByID(ctx, g.AccountID)
	if err != nil {
		return AuthContext{}, store.Fail("account by id", err)
	}
	if found && acct.Status != "active" {
		return AuthContext{}, errUnknownToken
	}
	// Ownership of the targeted resource outranks every role source: an owner
	// controls their own resource whatever the org or token says.
	if target.Type != "" && target.ID != "" {
		owns, err := st.IsResourceOwner(ctx, target.Type, target.ID, g.AccountID)
		if err != nil {
			return AuthContext{}, store.Fail("resource owner", err)
		}
		if owns {
			ac.Role, ac.Via = Admin, ViaOwner
			return ac, nil
		}
	}
	if g.OrgID == "" {
		return ac, nil
	}
	// Org ownership (orgs.owner_account_id) outranks a membership row.
	org, found, err := st.OrgByID(ctx, g.OrgID)
	if err != nil {
		return AuthContext{}, store.Fail("org by id", err)
	}
	if found && org.OwnerAccountID == g.AccountID {
		ac.OrgRole, ac.Role, ac.Via = OrgRoleOwner, Admin, ViaOwner
		return ac, nil
	}
	// A membership role outranks the token's own role.
	member, found, err := st.OrgMemberOf(ctx, g.OrgID, g.AccountID)
	if err != nil {
		return AuthContext{}, store.Fail("org member", err)
	}
	if found && ValidOrgRole(member.Role) {
		ac.OrgRole, ac.Role, ac.Via = member.Role, OrgRoleToRole(member.Role), ViaOrg
		return ac, nil
	}
	return ac, nil
}
