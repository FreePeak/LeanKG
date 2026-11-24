package auth

import (
	"context"
	"net/http/httptest"
	"testing"
)

// TestProjectAllowed pins the project grant. A token that names projects with
// project:<name> scopes reaches only those projects. A token that names none,
// including one that carries only other scopes, is unrestricted, and so is the
// zero AuthContext a stdio or no-caller context resolves to.
func TestProjectAllowed(t *testing.T) {
	cases := []struct {
		name       string
		scopes     []string
		project    string
		wantAllow  bool
		restricted bool
	}{
		{"zero context is unrestricted", nil, "alpha", true, false},
		{"granted project", []string{"project:alpha"}, "alpha", true, true},
		{"other project denied", []string{"project:alpha"}, "beta", false, true},
		{"unknown name denied", []string{"project:alpha"}, "gamma", false, true},
		{"prefix is not a grant", []string{"project:alph"}, "alpha", false, true},
		{"non-project scopes do not restrict", []string{"graphs:read"}, "beta", true, false},
		{"project grant beside other scopes", []string{"graphs:read", "project:alpha"}, "beta", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := AuthContext{Scopes: tc.scopes}
			if got := c.ProjectRestricted(); got != tc.restricted {
				t.Fatalf("ProjectRestricted() = %v, want %v", got, tc.restricted)
			}
			if got := c.ProjectAllowed(tc.project, false); got != tc.wantAllow {
				t.Fatalf("ProjectAllowed(%q) = %v, want %v", tc.project, got, tc.wantAllow)
			}
		})
	}
}

// TestProjectAllowedSharedName pins the fail-closed rule: a name two registered
// directories share matches no grant, not even one that names it. An
// unrestricted caller still reaches both.
func TestProjectAllowedSharedName(t *testing.T) {
	scoped := AuthContext{Scopes: []string{"project:alpha"}}
	if scoped.ProjectAllowed("alpha", true) {
		t.Fatal("a shared basename must not match project:alpha")
	}
	if !scoped.ProjectAllowed("alpha", false) {
		t.Fatal("a unique basename must match project:alpha")
	}
	if !(AuthContext{}).ProjectAllowed("alpha", true) {
		t.Fatal("an unrestricted caller reaches a shared basename")
	}
}

// TestAnonymousProjectDenied pins fix S2: an anonymous HTTP caller (no
// Authorization header) with the gate on reaches no project. With the gate off
// the same request is the open local admin and reaches every project, and a
// context with no caller at all stays unrestricted.
func TestAnonymousProjectDenied(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	if _, _, err := Mint(context.Background(), st, MintRequest{Name: "live", Role: "viewer"}); err != nil {
		t.Fatal(err)
	}
	ac, anonymous, err := AuthOrAnonymous(st, httptest.NewRequest("GET", "/mcp", nil))
	if err != nil || !anonymous {
		t.Fatalf("gate on, no header: anonymous=%v err=%v; want anonymous", anonymous, err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if ac.ProjectAllowed(name, false) {
			t.Fatalf("anonymous caller reaches %q; want denied", name)
		}
	}

	ac, anonymous, err = AuthOrAnonymous(openAuthStore(t), httptest.NewRequest("GET", "/mcp", nil))
	if err != nil || anonymous || !ac.ProjectAllowed("alpha", false) {
		t.Fatalf("gate off: anonymous=%v err=%v allowed=%v; want open and allowed", anonymous, err, ac.ProjectAllowed("alpha", false))
	}
	if !(AuthContext{}).ProjectAllowed("alpha", false) {
		t.Fatal("a context with no caller must reach every project")
	}
}
