package auth

import (
	"strings"
	"testing"
)

// TestRoleSufficientOrgHierarchy ports the Rust role_sufficient table: owner >
// admin > member > viewer, unknown roles below every real role.
func TestRoleSufficientOrgHierarchy(t *testing.T) {
	cases := []struct {
		actual, required string
		want             bool
	}{
		{"owner", "viewer", true},
		{"owner", "owner", true},
		{"admin", "member", true},
		{"member", "viewer", true},
		{"viewer", "member", false},
		{"member", "admin", false},
		{"bogus", "viewer", false},
		{"viewer", "bogus", true}, // a viewer outranks an unknown requirement
	}
	for _, c := range cases {
		if got := RoleSufficient(c.actual, c.required); got != c.want {
			t.Errorf("RoleSufficient(%q, %q) = %v; want %v", c.actual, c.required, got, c.want)
		}
	}
}

func TestOrgRoleToRoleMapping(t *testing.T) {
	cases := map[string]Role{
		OrgRoleOwner: Admin, OrgRoleAdmin: Admin, OrgRoleMember: Contributor, OrgRoleViewer: Viewer,
		"bogus": Viewer,
	}
	for orgRole, want := range cases {
		if got := OrgRoleToRole(orgRole); got != want {
			t.Errorf("OrgRoleToRole(%q) = %v; want %v", orgRole, got, want)
		}
	}
}

// TestPasswordHashRoundTrip ports the Rust password_hash_roundtrip test and
// pins the encoded verifier's shape.
func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, passwordScheme+"$") {
		t.Fatalf("verifier %q lacks the scheme prefix", hash)
	}
	if !VerifyPassword("correct horse battery", hash) {
		t.Fatal("correct password must verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("wrong password must not verify")
	}
	other, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if other == hash {
		t.Fatal("equal passwords must not share a verifier (per-hash salt)")
	}
	for _, malformed := range []string{"", "pbkdf2-sha256$600000$zz$zz", "argon2id$x$y$z", "pbkdf2-sha256$notint$aabb$ccdd"} {
		if VerifyPassword("correct horse battery", malformed) {
			t.Fatalf("malformed verifier %q must not authenticate", malformed)
		}
	}
}

func TestRegisterCreatesAccountAndBootstrapOrg(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	account, err := Register(st, "  Alice@Example.COM ", "password123", "Alice")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if account.Email != "alice@example.com" {
		t.Fatalf("email = %q; want normalized lowercase", account.Email)
	}
	if account.Status != "active" || account.ID == "" {
		t.Fatalf("account = %+v; want active with an id", account)
	}
	if _, err := VerifyLogin(st, "alice@example.com", "password123"); err != nil {
		t.Fatalf("stored verifier does not match the password: %v", err)
	}

	orgs, err := st.OrgsByOwner(account.ID)
	if err != nil {
		t.Fatalf("orgs by owner: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("bootstrap orgs = %d; want 1", len(orgs))
	}
	if orgs[0].Name != "Alice's org" || orgs[0].OwnerAccountID != account.ID {
		t.Fatalf("bootstrap org = %+v", orgs[0])
	}
	members, err := st.OrgMembers(orgs[0].ID)
	if err != nil {
		t.Fatalf("org members: %v", err)
	}
	if len(members) != 1 || members[0].AccountID != account.ID || members[0].Role != OrgRoleOwner {
		t.Fatalf("bootstrap members = %+v; want one owner membership", members)
	}
}

func TestRegisterRejectsDuplicateEmailAndShortPassword(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	if _, err := Register(st, "dup@example.com", "password123", "Dup"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := Register(st, "DUP@example.com", "password123", "Dup2"); err == nil {
		t.Fatal("duplicate email (different case) must be rejected")
	}
	if _, err := Register(st, "new@example.com", "short", "New"); err == nil {
		t.Fatal("short password must be rejected")
	}
	if _, err := Register(st, "not-an-email", "password123", "NoAt"); err == nil {
		t.Fatal("email without @ must be rejected")
	}
}

func TestVerifyLoginRoundTrip(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	if _, err := Register(st, "login@example.com", "password123", "Log"); err != nil {
		t.Fatalf("register: %v", err)
	}
	account, err := VerifyLogin(st, "LOGIN@example.com", "password123")
	if err != nil {
		t.Fatalf("login with mixed-case email: %v", err)
	}
	if account.Email != "login@example.com" {
		t.Fatalf("login account = %+v", account)
	}
	if _, err := VerifyLogin(st, "login@example.com", "wrong"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if _, err := VerifyLogin(st, "nobody@example.com", "password123"); err == nil {
		t.Fatal("unknown account must fail")
	}
}

// TestOrgRoleSufficientChecksHierarchy ports the Rust test of the same name:
// org admin+ required for member management, any membership for reads.
func TestOrgRoleSufficientChecksHierarchy(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	owner, err := Register(st, "owner@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	org, err := st.OrgsByOwner(owner.ID)
	if err != nil || len(org) != 1 {
		t.Fatalf("orgs by owner = %+v, %v", org, err)
	}
	orgID := org[0].ID

	member, err := Register(st, "member@example.com", "password123", "Member")
	if err != nil {
		t.Fatalf("register member: %v", err)
	}
	if _, err := AddOrgMember(st, orgID, member.ID, OrgRoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}

	for _, c := range []struct {
		account, required string
		want              bool
	}{
		{owner.ID, OrgRoleAdmin, true},
		{member.ID, OrgRoleAdmin, false},
		{member.ID, OrgRoleViewer, true},
		{"nobody", OrgRoleViewer, false},
	} {
		got, err := OrgRoleSufficient(st, orgID, c.account, c.required)
		if err != nil {
			t.Fatalf("OrgRoleSufficient(%q, %q): %v", c.account, c.required, err)
		}
		if got != c.want {
			t.Errorf("OrgRoleSufficient(%q, %q) = %v; want %v", c.account, c.required, got, c.want)
		}
	}

	if _, err := AddOrgMember(st, orgID, member.ID, "superuser"); err == nil {
		t.Fatal("an unknown org role must be rejected")
	}
}

func TestCreateOrgOwnsAndClaimsResource(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	account, err := Register(st, "builder@example.com", "password123", "Builder")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	org, err := CreateOrg(st, "Second org", account.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if sufficient, err := OrgRoleSufficient(st, org.ID, account.ID, OrgRoleAdmin); err != nil || !sufficient {
		t.Fatalf("creator is not org admin+: %v, %v", sufficient, err)
	}
	if orgs, _ := st.OrgsByOwner(account.ID); len(orgs) != 2 {
		t.Fatalf("orgs by owner = %d; want 2 (bootstrap + created)", len(orgs))
	}

	if err := ClaimResource(st, "knowledge", "entry-1", account.ID, org.ID); err != nil {
		t.Fatalf("claim resource: %v", err)
	}
	owned, err := IsResourceOwner(st, "knowledge", "entry-1", account.ID)
	if err != nil || !owned {
		t.Fatalf("is resource owner = %v, %v; want true", owned, err)
	}
}
