package store

import "testing"

func TestAccountUpsertAndLookup(t *testing.T) {
	s := openTestStore(t)
	a := Account{
		ID: "acct-1", Email: "alice@example.com", Name: "Alice",
		PasswordHash: "pbkdf2-sha256$600000$aa$bb", Status: "active",
		CreatedAt: 10, UpdatedAt: 10,
	}
	if err := s.AccountUpsert(a); err != nil {
		t.Fatalf("account upsert: %v", err)
	}
	got, found, err := s.AccountByEmail("alice@example.com")
	if err != nil || !found {
		t.Fatalf("account by email = %+v, %v, %v; want found", got, found, err)
	}
	if got.ID != a.ID || got.Name != "Alice" || got.Status != "active" || got.CreatedAt != 10 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Upsert replaces on the primary key, not duplicates.
	a.Name = "Alice II"
	if err := s.AccountUpsert(a); err != nil {
		t.Fatalf("account re-upsert: %v", err)
	}
	if got, _, _ := s.AccountByEmail("alice@example.com"); got.Name != "Alice II" {
		t.Fatalf("re-upsert did not replace: %+v", got)
	}

	// Unknown email is a miss, not an error.
	if _, found, err := s.AccountByEmail("nobody@example.com"); err != nil || found {
		t.Fatalf("unknown email = found %v, err %v; want miss", found, err)
	}

	// The email column is UNIQUE across accounts.
	if err := s.AccountUpsert(Account{ID: "acct-2", Email: "alice@example.com", Name: "Impostor"}); err == nil {
		t.Fatal("duplicate email must be rejected by the unique index")
	}
}

func TestOrgAndMembershipRoundTrip(t *testing.T) {
	s := openTestStore(t)
	org := Org{ID: "org-1", Name: "Alice's org", OwnerAccountID: "acct-1", CreatedAt: 10, UpdatedAt: 10}
	if err := s.OrgUpsert(org); err != nil {
		t.Fatalf("org upsert: %v", err)
	}
	got, found, err := s.OrgByID("org-1")
	if err != nil || !found || got.OwnerAccountID != "acct-1" {
		t.Fatalf("org by id = %+v, %v, %v", got, found, err)
	}
	if orgs, err := s.OrgsByOwner("acct-1"); err != nil || len(orgs) != 1 || orgs[0].ID != "org-1" {
		t.Fatalf("orgs by owner = %+v, %v", orgs, err)
	}
	if orgs, err := s.OrgsByOwner("acct-2"); err != nil || len(orgs) != 0 {
		t.Fatalf("orgs by other owner = %+v, %v; want empty", orgs, err)
	}

	// Adding the same pair twice re-roles; it never duplicates the row.
	if err := s.OrgMembershipUpsert(OrgMember{OrgID: "org-1", AccountID: "acct-1", Role: "owner", JoinedAt: 10}); err != nil {
		t.Fatalf("membership upsert: %v", err)
	}
	if err := s.OrgMembershipUpsert(OrgMember{OrgID: "org-1", AccountID: "acct-1", Role: "viewer", JoinedAt: 20}); err != nil {
		t.Fatalf("membership re-upsert: %v", err)
	}
	members, err := s.OrgMembers("org-1")
	if err != nil || len(members) != 1 {
		t.Fatalf("org members = %+v, %v; want exactly one", members, err)
	}
	if members[0].Role != "viewer" || members[0].JoinedAt != 20 {
		t.Fatalf("re-upsert did not re-role: %+v", members[0])
	}
	if m, found, err := s.OrgMemberOf("org-1", "acct-9"); err != nil || found {
		t.Fatalf("non-member = %+v, %v, %v; want miss", m, found, err)
	}
}

func TestTeamMemberRoundTrip(t *testing.T) {
	s := openTestStore(t)
	if err := s.TeamMemberUpsert(TeamMember{TeamID: "team-1", AccountID: "acct-1", Role: "admin", JoinedAt: 10}); err != nil {
		t.Fatalf("team member upsert: %v", err)
	}
	if err := s.TeamMemberUpsert(TeamMember{TeamID: "team-1", AccountID: "acct-1", Role: "member", JoinedAt: 20}); err != nil {
		t.Fatalf("team member re-upsert: %v", err)
	}
	m, found, err := s.TeamMemberOf("team-1", "acct-1")
	if err != nil || !found || m.Role != "member" || m.JoinedAt != 20 {
		t.Fatalf("team member = %+v, %v, %v", m, found, err)
	}
	members, err := s.TeamMembers("team-1")
	if err != nil || len(members) != 1 {
		t.Fatalf("team members = %+v, %v; want exactly one", members, err)
	}
	if _, found, err := s.TeamMemberOf("team-1", "acct-9"); err != nil || found {
		t.Fatalf("non-member found=%v err=%v; want miss", found, err)
	}
}

func TestResourceOwnershipClaim(t *testing.T) {
	s := openTestStore(t)
	if err := s.ResourceClaim("knowledge", "entry-1", "acct-1", "org-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if owned, err := s.IsResourceOwner("knowledge", "entry-1", "acct-1"); err != nil || !owned {
		t.Fatalf("owner check = %v, %v; want true", owned, err)
	}
	if owned, err := s.IsResourceOwner("knowledge", "entry-1", "acct-2"); err != nil || owned {
		t.Fatalf("other account check = %v, %v; want false", owned, err)
	}
	if owned, err := s.IsResourceOwner("knowledge", "entry-2", "acct-1"); err != nil || owned {
		t.Fatalf("other resource check = %v, %v; want false", owned, err)
	}

	// Re-claiming with a different org updates the row instead of appending one.
	if err := s.ResourceClaim("knowledge", "entry-1", "acct-1", "org-2"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM resource_ownership`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("re-claim created %d rows; want 1", rows)
	}
	if owned, _ := s.IsResourceOwner("knowledge", "entry-1", "acct-1"); !owned {
		t.Fatal("ownership lost after re-claim")
	}

	// A second account can own the same resource id; neither loses ownership.
	if err := s.ResourceClaim("knowledge", "entry-1", "acct-2", ""); err != nil {
		t.Fatalf("second owner: %v", err)
	}
	if owned, _ := s.IsResourceOwner("knowledge", "entry-1", "acct-1"); !owned {
		t.Fatal("first owner lost ownership after a second claim")
	}
	if owned, _ := s.IsResourceOwner("knowledge", "entry-1", "acct-2"); !owned {
		t.Fatal("second owner not recorded")
	}
}
