package auth

import (
	"context"
	"testing"
)

// TestOrgOnlyTokenScopesProjectsByOrgID: a control-plane token carries org_id
// and no account_id. Its OrgID must reach the AuthContext, and the project
// scope must follow the org claim alone.
func TestOrgOnlyTokenScopesProjectsByOrgID(t *testing.T) {
	clearTokenEnv(t)
	ctx := context.Background()
	st := openAuthStore(t)
	if err := ClaimResource(ctx, st, ResourceProject, "/work/a", "org:org-1", "org-1"); err != nil {
		t.Fatal(err)
	}
	if err := ClaimResource(ctx, st, ResourceProject, "/work/b", "org:org-2", "org-2"); err != nil {
		t.Fatal(err)
	}
	secret, _, err := Mint(ctx, st, MintRequest{Name: "cp", Role: "contributor", OrgID: "org-1"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	ac, err := ResolveFor(st, reqWith(secret), Target{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ac.OrgID != "org-1" || ac.AccountID != "" {
		t.Fatalf("ctx = %+v; want OrgID org-1 with no account", ac)
	}
	visible, err := ProjectScope(ctx, st, ac)
	if err != nil || visible == nil {
		t.Fatalf("ProjectScope = %v, %v; an org-scoped caller must get a scope", visible != nil, err)
	}
	if !visible("/work/a") || visible("/work/b") || visible("/work/none") {
		t.Fatal("org-1 must see /work/a only")
	}
	// The same token through the request helper gives the same scope.
	fromReq, err := RequestScope(ctx, st, reqWith(secret))
	if err != nil || fromReq == nil || fromReq("/work/b") {
		t.Fatalf("RequestScope = %v, %v; want org-1's scope", fromReq != nil, err)
	}
}

// TestTokenWithoutOrgIsUnscoped: env and legacy tokens carry no org, so every
// project stays visible (single-user installs are unchanged).
func TestTokenWithoutOrgIsUnscoped(t *testing.T) {
	clearTokenEnv(t)
	ctx := context.Background()
	st := openAuthStore(t)
	secret, _, err := Mint(ctx, st, MintRequest{Name: "plain", Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := RequestScope(ctx, st, reqWith(secret))
	if err != nil || scope != nil {
		t.Fatalf("RequestScope(no org) = %v, %v; want nil (unscoped)", scope != nil, err)
	}
	// A request with no bearer is never org-scoped.
	if scope, err := RequestScope(ctx, st, reqWith("")); err != nil || scope != nil {
		t.Fatalf("RequestScope(no header) = %v, %v; want nil", scope != nil, err)
	}
	// An org-keyed caller over a store that cannot list claims fails closed.
	if visible, err := ProjectScope(ctx, nil, AuthContext{OrgID: "org-9"}); err != nil || visible == nil || visible("/anything") {
		t.Fatal("a nil store must give an org caller no project")
	}
}
