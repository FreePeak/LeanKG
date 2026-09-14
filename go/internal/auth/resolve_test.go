package auth

import (
	"errors"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestResolvePrecedence pins the enterprise ladder, highest rung first:
// resource/org ownership > org membership role > token role > env token, with
// the no-token-configured local default last.
func TestResolvePrecedence(t *testing.T) {
	t.Run("open local default", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		ctx, err := Resolve(st, reqWith(""))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Role != Admin || ctx.Via != ViaOpen || ctx.AccountID != "" {
			t.Fatalf("ctx = %+v; want Admin/open with no account", ctx)
		}
	})

	t.Run("env token", func(t *testing.T) {
		st := openAuthStore(t)
		t.Setenv("LEANKG_TOKEN_VIEWER", "env-viewer")
		t.Setenv("LEANKG_TOKEN_ADMIN", "")
		t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
		ctx, err := Resolve(st, reqWith("env-viewer"))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Role != Viewer || ctx.Via != ViaEnv {
			t.Fatalf("ctx = %+v; want Viewer/env", ctx)
		}
	})

	t.Run("token role", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		secret, _, err := Mint(st, MintRequest{Name: "plain", Role: "viewer"})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Role != Viewer || ctx.Via != ViaToken || ctx.AccountID != "" {
			t.Fatalf("ctx = %+v; want Viewer/token", ctx)
		}
	})

	t.Run("token role outranks env", func(t *testing.T) {
		st := openAuthStore(t)
		secret, tok, err := Mint(st, MintRequest{Name: "dual", Role: "admin"})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		// Same secret present in the env with a LOWER role: the DB row wins.
		t.Setenv("LEANKG_TOKEN_VIEWER", secret)
		t.Setenv("LEANKG_TOKEN_ADMIN", "")
		t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "")
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Role != Admin || ctx.Via != ViaToken || ctx.TokenID != tok.ID {
			t.Fatalf("ctx = %+v; want Admin/token (DB beats env)", ctx)
		}
	})

	t.Run("org role outranks token role", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		orgID, _, member := orgWithMember(t, st)

		// The token claims admin; the org membership says member.
		secret, _, err := Mint(st, MintRequest{Name: "member-token", Role: "admin", AccountID: member.ID, OrgID: orgID})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Via != ViaOrg || ctx.Role != Contributor || ctx.OrgRole != OrgRoleMember || ctx.AccountID != member.ID {
			t.Fatalf("ctx = %+v; want Contributor/org (member beats admin token)", ctx)
		}
	})

	t.Run("org ownership outranks org role", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		account, err := Register(st, "boss@example.com", "password123", "Boss")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		orgs, err := st.OrgsByOwner(account.ID)
		if err != nil || len(orgs) != 1 {
			t.Fatalf("orgs by owner = %+v, %v", orgs, err)
		}
		// Downgrade the membership row: ownership still outranks it.
		if _, err := AddOrgMember(st, orgs[0].ID, account.ID, OrgRoleViewer); err != nil {
			t.Fatalf("downgrade membership: %v", err)
		}
		secret, _, err := Mint(st, MintRequest{Name: "boss-token", Role: "viewer", AccountID: account.ID, OrgID: orgs[0].ID})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Via != ViaOwner || ctx.Role != Admin || ctx.OrgRole != OrgRoleOwner {
			t.Fatalf("ctx = %+v; want Admin/owner (ownership beats viewer membership)", ctx)
		}
	})

	t.Run("resource ownership outranks everything", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		account, err := Register(st, "owner2@example.com", "password123", "Owner2")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := ClaimResource(st, "knowledge", "entry-1", account.ID, ""); err != nil {
			t.Fatalf("claim: %v", err)
		}
		secret, _, err := Mint(st, MintRequest{Name: "viewer-token", Role: "viewer", AccountID: account.ID})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := ResolveFor(st, reqWith(secret), Target{Type: "knowledge", ID: "entry-1"})
		if err != nil {
			t.Fatalf("resolve owned resource: %v", err)
		}
		if ctx.Via != ViaOwner || ctx.Role != Admin {
			t.Fatalf("ctx = %+v; want Admin/owner for the owned resource", ctx)
		}
		// A resource the account does not own falls back to the token role.
		ctx, err = ResolveFor(st, reqWith(secret), Target{Type: "knowledge", ID: "entry-2"})
		if err != nil {
			t.Fatalf("resolve foreign resource: %v", err)
		}
		if ctx.Via != ViaToken || ctx.Role != Viewer {
			t.Fatalf("ctx = %+v; want Viewer/token for a foreign resource", ctx)
		}
	})

	t.Run("unknown org label falls back to token role", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		secret, _, err := Mint(st, MintRequest{Name: "stale-org", Role: "contributor", AccountID: "ghost", OrgID: "no-such-org"})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if ctx.Via != ViaToken || ctx.Role != Contributor {
			t.Fatalf("ctx = %+v; want Contributor/token", ctx)
		}
	})

	t.Run("revoked token never falls through to env", func(t *testing.T) {
		st := openAuthStore(t)
		secret, tok, err := Mint(st, MintRequest{Name: "revoked", Role: "admin"})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		t.Setenv("LEANKG_TOKEN_ADMIN", secret) // static copy of the same secret
		if err := st.TokenRevoke(tok.ID, 1); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := Resolve(st, reqWith(secret)); !errors.Is(err, store.ErrTokenRevoked) {
			t.Fatalf("resolve revoked = %v; want ErrTokenRevoked (no env fallback)", err)
		}
	})

	t.Run("missing and unknown tokens with tokens configured", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		if _, _, err := Mint(st, MintRequest{Name: "configured", Role: "viewer"}); err != nil {
			t.Fatalf("mint: %v", err)
		}
		if _, err := Resolve(st, reqWith("")); err == nil {
			t.Fatal("missing bearer must be rejected once tokens exist")
		}
		if _, err := Resolve(st, reqWith("nope")); err == nil {
			t.Fatal("unknown bearer must be rejected once tokens exist")
		}
	})

	t.Run("scopes round-trip and gate by membership", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		secret, _, err := Mint(st, MintRequest{Name: "scoped", Role: "contributor", Scopes: []string{"org:read", "org:write"}})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if !ctx.ScopeAllowed("org:read") || !ctx.ScopeAllowed("org:write") {
			t.Fatalf("ctx = %+v; want both scopes allowed", ctx)
		}
		if ctx.ScopeAllowed("admin:all") {
			t.Fatalf("ctx = %+v; an unlisted scope must be denied", ctx)
		}
		if !ctx.CanWrite() || ctx.CanAdmin() {
			t.Fatalf("ctx = %+v; a contributor writes but does not administer", ctx)
		}
	})

	t.Run("unscoped token allows any scope", func(t *testing.T) {
		clearTokenEnv(t)
		st := openAuthStore(t)
		secret, _, err := Mint(st, MintRequest{Name: "unscoped", Role: "viewer"})
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		ctx, err := Resolve(st, reqWith(secret))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if !ctx.ScopeAllowed("anything") {
			t.Fatalf("ctx = %+v; want unrestricted (Rust never enforced scopes)", ctx)
		}
	})
}

// TestClassifyUsesEnterpriseResolution pins that the gate's classify sees the
// same org refinement (one resolution path, no second convention).
func TestClassifyUsesEnterpriseResolution(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	orgID, _, member := orgWithMember(t, st)

	secret, _, err := Mint(st, MintRequest{Name: "member-token", Role: "admin", AccountID: member.ID, OrgID: orgID})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	role, err := RoleForRequestWithStore(st, reqWith(secret))
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if role != Contributor {
		t.Fatalf("classify role = %v; want Contributor (org member) not the admin token role", role)
	}
}

// orgWithMember registers an owner plus a second account, adds the second as an
// org member, and returns the org id, the owner and the member.
func orgWithMember(t *testing.T, st store.Backend) (orgID string, owner, member store.Account) {
	t.Helper()
	owner, err := Register(st, "org-owner@example.com", "password123", "OrgOwner")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	orgs, err := st.OrgsByOwner(owner.ID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("orgs by owner = %+v, %v", orgs, err)
	}
	member, err = Register(st, "org-member@example.com", "password123", "OrgMember")
	if err != nil {
		t.Fatalf("register member: %v", err)
	}
	if _, err := AddOrgMember(st, orgs[0].ID, member.ID, OrgRoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return orgs[0].ID, owner, member
}
