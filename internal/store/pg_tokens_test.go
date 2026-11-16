package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestPGTokenRoundTrip mirrors the sqlite lifecycle test against the PG
// backend (gated behind LEANKG_TEST_PG_URL).
func TestPGTokenRoundTrip(t *testing.T) {
	s := openPGTest(t)
	plain := "lkg_0123456789abcdef0123456789abcdef_dead"

	if err := s.TokenUpsert(context.Background(), Token{ID: "t1", Name: "ci", Secret: plain, Role: "contributor", CreatedAt: 100}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := s.TokenList(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Secret != HashToken(plain) || list[0].Role != "contributor" {
		t.Fatalf("want 1 hashed row, got %+v (err=%v)", list, err)
	}

	got, found, err := s.TokenFind(context.Background(), plain)
	if err != nil || !found || got.ID != "t1" {
		t.Fatalf("find: found=%v id=%q err=%v", found, got.ID, err)
	}
	if _, found, _ := s.TokenFind(context.Background(), "lkg_wrong"); found {
		t.Fatal("unknown plaintext must not match")
	}

	if err := s.TokenUpsert(context.Background(), Token{ID: "t1", Name: "ci", Secret: plain, Role: "viewer", CreatedAt: 100}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	list, _ = s.TokenList(context.Background())
	if len(list) != 1 || list[0].Role != "viewer" {
		t.Fatalf("re-upsert: want 1 row with role viewer, got %+v", list)
	}

	if err := s.TokenDelete(context.Background(), "t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := s.TokenFind(context.Background(), plain); found {
		t.Fatal("revoked token must not authenticate")
	}
	if err := s.TokenDelete(context.Background(), "t1"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

// TestPGTokenLifecycle mirrors the lifecycle coverage the sqlite backend gets:
// metadata round-trip, the coalesced last_used_at write, rejection of revoked
// and expired rows, the soft revoke (row kept, rejected) against the hard
// delete (row gone), and an idempotent re-run of the migration ledger.
func TestPGTokenLifecycle(t *testing.T) {
	s := openPGTest(t)
	now := time.Now().Unix()
	plain := "lkg_pg_lifecycle_11111111"
	expires := now + 3600

	if err := s.TokenUpsert(context.Background(), Token{
		ID: "lc", Name: "lc", Secret: plain, Role: "contributor", CreatedAt: 7,
		AccountID: "acct-pg", OrgID: "org-pg", Scopes: []string{"graphs:read", "memory:write"},
		ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, found, err := s.TokenFind(context.Background(), plain)
	if err != nil || !found {
		t.Fatalf("find: found=%v err=%v", found, err)
	}
	if got.AccountID != "acct-pg" || got.OrgID != "org-pg" || got.CreatedAt != 7 || got.ExpiresAt != expires {
		t.Fatalf("metadata mangled: %+v", got)
	}
	if len(got.Scopes) != 2 || got.Scopes[0] != "graphs:read" || got.Scopes[1] != "memory:write" {
		t.Fatalf("scopes mangled: %+v", got.Scopes)
	}
	if got.RevokedAt != 0 || got.LastUsedAt != 0 {
		t.Fatalf("unset lifecycle timestamps must read as 0: %+v", got)
	}

	// last_used_at: written once, then coalesced inside the window.
	if err := s.TokenTouch(context.Background(), "lc", 1000); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := s.TokenTouch(context.Background(), "lc", 1000+TokenTouchWindowSecs-1); err != nil {
		t.Fatalf("in-window touch: %v", err)
	}
	if got, _, err = s.TokenFind(context.Background(), plain); err != nil || got.LastUsedAt != 1000 {
		t.Fatalf("coalesced last_used_at = %d (err=%v), want 1000", got.LastUsedAt, err)
	}
	next := int64(1000 + TokenTouchWindowSecs)
	if err := s.TokenTouch(context.Background(), "lc", next); err != nil {
		t.Fatalf("boundary touch: %v", err)
	}
	if got, _, err = s.TokenFind(context.Background(), plain); err != nil || got.LastUsedAt != next {
		t.Fatalf("boundary last_used_at = %d (err=%v), want %d", got.LastUsedAt, err, next)
	}

	// Soft revoke: the row stays (auditable) and stops authenticating.
	if err := s.TokenRevoke(context.Background(), "lc", 2000); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.TokenRevoke(context.Background(), "lc", 3000); err != nil {
		t.Fatalf("re-revoke: %v", err)
	}
	list, err := s.TokenList(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("soft revoke must keep the row: %d rows err=%v", len(list), err)
	}
	if list[0].RevokedAt != 2000 {
		t.Fatalf("revoked_at = %d, want the first stamp 2000", list[0].RevokedAt)
	}
	if _, found, err := s.TokenFind(context.Background(), plain); found || !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked token: found=%v err=%v; want found=false and ErrTokenRevoked", found, err)
	}

	// Expiry is rejection too.
	past := "lkg_pg_expired_22222222"
	if err := s.TokenUpsert(context.Background(), Token{
		ID: "exp", Name: "exp", Secret: past, Role: "viewer", CreatedAt: 8, ExpiresAt: now - 1,
	}); err != nil {
		t.Fatalf("upsert expired: %v", err)
	}
	if _, found, err := s.TokenFind(context.Background(), past); found || !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token: found=%v err=%v; want found=false and ErrTokenExpired", found, err)
	}

	// Hard delete is the other revocation path: the row disappears entirely.
	if err := s.TokenDelete(context.Background(), "lc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, err = s.TokenList(context.Background()); err != nil || len(list) != 1 || list[0].ID != "exp" {
		t.Fatalf("hard delete must drop the row: %+v (err=%v)", list, err)
	}

	// Re-running the migration ledger is a no-op on an already-migrated schema.
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// TestPGAppliedMigrations reads the ledger through the shared helper.
func TestPGAppliedMigrations(t *testing.T) {
	s := openPGTest(t)
	got, err := s.AppliedMigrations(context.Background())
	if err != nil {
		t.Fatalf("applied: %v", err)
	}
	if len(got) != len(pgMigrations) {
		t.Fatalf("applied = %v, want %d versions", got, len(pgMigrations))
	}
	for i, m := range pgMigrations {
		if got[i] != m.version {
			t.Fatalf("applied[%d] = %d, want %d", i, got[i], m.version)
		}
	}
}
