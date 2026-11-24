package store

import (
	"context"
	"reflect"
	"testing"
)

// TestResourceClaimsByOrg pins the org-keyed reads that scope a project to the
// org that claimed it: ResourceIDs lists an org's resources, ResourceOrgs lists
// a resource's claiming orgs, and ResourceRelease drops every claim on one.
func TestResourceClaimsByOrg(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	claims := []struct{ id, owner, org string }{
		{"/work/a", "org:org-1", "org-1"},
		{"/work/b", "org:org-1", "org-1"},
		{"/work/c", "org:org-2", "org-2"},
	}
	for _, c := range claims {
		if err := s.ResourceClaim(ctx, "project", c.id, c.owner, c.org); err != nil {
			t.Fatalf("claim %s: %v", c.id, err)
		}
	}

	if got, _ := s.ResourceIDs(ctx, "project", "org-1"); !reflect.DeepEqual(got, []string{"/work/a", "/work/b"}) {
		t.Fatalf("ResourceIDs(org-1) = %v, want [/work/a /work/b]", got)
	}
	if got, _ := s.ResourceIDs(ctx, "project", "org-2"); !reflect.DeepEqual(got, []string{"/work/c"}) {
		t.Fatalf("ResourceIDs(org-2) = %v, want [/work/c]", got)
	}
	if got, err := s.ResourceIDs(ctx, "project", "org-3"); err != nil || len(got) != 0 {
		t.Fatalf("ResourceIDs(org-3) = %v, %v; want none", got, err)
	}
	if got, _ := s.ResourceOrgs(ctx, "project", "/work/c"); !reflect.DeepEqual(got, []string{"org-2"}) {
		t.Fatalf("ResourceOrgs(/work/c) = %v, want [org-2]", got)
	}
	if got, err := s.ResourceOrgs(ctx, "project", "/work/none"); err != nil || len(got) != 0 {
		t.Fatalf("ResourceOrgs(unclaimed) = %v, %v; want none", got, err)
	}

	if err := s.ResourceRelease(ctx, "project", "/work/a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got, _ := s.ResourceIDs(ctx, "project", "org-1"); !reflect.DeepEqual(got, []string{"/work/b"}) {
		t.Fatalf("after release ResourceIDs(org-1) = %v, want [/work/b]", got)
	}
	if got, _ := s.ResourceOrgs(ctx, "project", "/work/a"); len(got) != 0 {
		t.Fatalf("after release ResourceOrgs(/work/a) = %v, want none", got)
	}
}

// TestClaimedResourceIDsSpansEveryOrg pins the read a hosted restart needs:
// ClaimedResourceIDs lists each claimed resource of a type once, across every
// org, and skips other types and released claims.
func TestClaimedResourceIDsSpansEveryOrg(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	claims := []struct{ typ, id, owner, org string }{
		{"project", "/work/b", "org:org-1", "org-1"},
		{"project", "/work/a", "org:org-2", "org-2"},
		{"project", "/work/a", "org:org-3", "org-3"},
		{"other", "/work/z", "org:org-1", "org-1"},
	}
	for _, c := range claims {
		if err := s.ResourceClaim(ctx, c.typ, c.id, c.owner, c.org); err != nil {
			t.Fatalf("claim %s/%s: %v", c.typ, c.id, err)
		}
	}
	if got, err := s.ClaimedResourceIDs(ctx, "project"); err != nil || !reflect.DeepEqual(got, []string{"/work/a", "/work/b"}) {
		t.Fatalf("ClaimedResourceIDs(project) = %v, %v; want [/work/a /work/b]", got, err)
	}
	if err := s.ResourceRelease(ctx, "project", "/work/b"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got, err := s.ClaimedResourceIDs(ctx, "project"); err != nil || !reflect.DeepEqual(got, []string{"/work/a"}) {
		t.Fatalf("after release ClaimedResourceIDs = %v, %v; want [/work/a]", got, err)
	}
	if got, err := s.ClaimedResourceIDs(ctx, "missing"); err != nil || len(got) != 0 {
		t.Fatalf("ClaimedResourceIDs(missing) = %v, %v; want none", got, err)
	}
}
