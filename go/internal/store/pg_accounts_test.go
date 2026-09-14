// PostgreSQL parity tests for the enterprise auth tables. Skipped unless
// LEANKG_TEST_PG_URL is set (see openPGTest in store_pg_test.go). Each test
// gets its own project schema, dropped on cleanup.
package store

import (
	"testing"
)

func TestPGAccountRoundTrip(t *testing.T) {
	s := openPGTest(t)
	a := Account{
		ID: "acct-1", Email: "alice@example.com", Name: "Alice",
		PasswordHash: "pbkdf2-sha256$600000$aa$bb", Status: "active",
		CreatedAt: 10, UpdatedAt: 10,
	}
	if err := s.AccountUpsert(a); err != nil {
		t.Fatalf("account upsert: %v", err)
	}
	got, found, err := s.AccountByEmail("alice@example.com")
	if err != nil || !found || got.ID != a.ID || got.Name != "Alice" || got.CreatedAt != 10 {
		t.Fatalf("account by email = %+v, %v, %v", got, found, err)
	}
	if err := s.AccountUpsert(Account{ID: "acct-2", Email: "alice@example.com", Name: "Impostor"}); err == nil {
		t.Fatal("duplicate email must violate the unique constraint")
	}
	if _, found, err := s.AccountByEmail("nobody@example.com"); err != nil || found {
		t.Fatalf("unknown email = found %v, err %v; want miss", found, err)
	}
}

func TestPGOrgMembershipRoundTrip(t *testing.T) {
	s := openPGTest(t)
	if err := s.OrgUpsert(Org{ID: "org-1", Name: "Org", OwnerAccountID: "acct-1", CreatedAt: 10, UpdatedAt: 10}); err != nil {
		t.Fatalf("org upsert: %v", err)
	}
	if got, found, err := s.OrgByID("org-1"); err != nil || !found || got.OwnerAccountID != "acct-1" {
		t.Fatalf("org by id = %+v, %v, %v", got, found, err)
	}
	if orgs, err := s.OrgsByOwner("acct-1"); err != nil || len(orgs) != 1 {
		t.Fatalf("orgs by owner = %+v, %v", orgs, err)
	}

	if err := s.OrgMembershipUpsert(OrgMember{OrgID: "org-1", AccountID: "acct-1", Role: "owner", JoinedAt: 10}); err != nil {
		t.Fatalf("membership upsert: %v", err)
	}
	if err := s.OrgMembershipUpsert(OrgMember{OrgID: "org-1", AccountID: "acct-1", Role: "viewer", JoinedAt: 20}); err != nil {
		t.Fatalf("membership re-upsert: %v", err)
	}
	members, err := s.OrgMembers("org-1")
	if err != nil || len(members) != 1 || members[0].Role != "viewer" || members[0].JoinedAt != 20 {
		t.Fatalf("org members = %+v, %v; want one re-roled viewer", members, err)
	}
	if m, found, err := s.OrgMemberOf("org-1", "acct-9"); err != nil || found {
		t.Fatalf("non-member = %+v, %v, %v; want miss", m, found, err)
	}
}

func TestPGTeamMemberRoundTrip(t *testing.T) {
	s := openPGTest(t)
	if err := s.TeamMemberUpsert(TeamMember{TeamID: "team-1", AccountID: "acct-1", Role: "member", JoinedAt: 10}); err != nil {
		t.Fatalf("team member upsert: %v", err)
	}
	if m, found, err := s.TeamMemberOf("team-1", "acct-1"); err != nil || !found || m.Role != "member" {
		t.Fatalf("team member = %+v, %v, %v", m, found, err)
	}
	if members, err := s.TeamMembers("team-1"); err != nil || len(members) != 1 {
		t.Fatalf("team members = %+v, %v", members, err)
	}
}

func TestPGResourceOwnershipClaim(t *testing.T) {
	s := openPGTest(t)
	if err := s.ResourceClaim("knowledge", "entry-1", "acct-1", "org-1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if owned, err := s.IsResourceOwner("knowledge", "entry-1", "acct-1"); err != nil || !owned {
		t.Fatalf("owner check = %v, %v; want true", owned, err)
	}
	if owned, err := s.IsResourceOwner("knowledge", "entry-1", "acct-2"); err != nil || owned {
		t.Fatalf("other-account check = %v, %v; want false", owned, err)
	}
	// Re-claim updates the single row instead of appending.
	if err := s.ResourceClaim("knowledge", "entry-1", "acct-1", "org-2"); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	var rows int
	if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM resource_ownership`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("re-claim created %d rows; want 1", rows)
	}
}
