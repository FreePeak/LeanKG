package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// TestHashToken pins the at-rest hashing: SHA-256 hex, deterministic,
// collision-free across distinct secrets (Rust auth::tokens::hash_token).
func TestHashToken(t *testing.T) {
	h := HashToken("secret")
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(h))
	}
	sum := sha256.Sum256([]byte("secret"))
	if h != hex.EncodeToString(sum[:]) {
		t.Fatalf("HashToken is not plain SHA-256 hex: %s", h)
	}
	if HashToken("secret") != h || HashToken("other") == h {
		t.Fatal("hash must be deterministic and secret-dependent")
	}
}

// TestTokenRoundTrip covers the mint -> authenticate -> revoke lifecycle over
// the Backend contract: plaintext in, only the SHA-256 hash at rest, find by
// plaintext, the lifecycle metadata (scopes, ownership labels, expiry),
// upsert-in-place, and idempotent deletes.
func TestTokenRoundTrip(t *testing.T) {
	s := openTestStore(t)
	plain := "lkg_0123456789abcdef0123456789abcdef_beef"
	expires := int64(4102444800) // 2100-01-01

	if err := s.TokenUpsert(Token{
		ID: "t1", Name: "ci", Secret: plain, Role: "admin", CreatedAt: 100,
		AccountID: "acct-a", OrgID: "org-1", Scopes: []string{"read", "write"}, ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := s.TokenList()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 token, got %d", len(list))
	}
	if list[0].ID != "t1" || list[0].Name != "ci" || list[0].Role != "admin" || list[0].CreatedAt != 100 {
		t.Fatalf("row fields mangled: %+v", list[0])
	}
	if list[0].Secret != HashToken(plain) {
		t.Fatalf("secret stored is not the SHA-256 hash: %s", list[0].Secret)
	}
	if list[0].Secret == plain {
		t.Fatal("plaintext must never reach the table")
	}
	if list[0].AccountID != "acct-a" || list[0].OrgID != "org-1" || list[0].ExpiresAt != expires {
		t.Fatalf("ownership/expiry metadata mangled: %+v", list[0])
	}
	if len(list[0].Scopes) != 2 || list[0].Scopes[0] != "read" || list[0].Scopes[1] != "write" {
		t.Fatalf("scopes mangled: %+v", list[0].Scopes)
	}

	got, found, err := s.TokenFind(plain)
	if err != nil || !found {
		t.Fatalf("find by plaintext: found=%v err=%v", found, err)
	}
	if got.ID != "t1" || got.Role != "admin" {
		t.Fatalf("found wrong row: %+v", got)
	}
	// Unset lifecycle timestamps read back as 0, and a lookup never stamps
	// last_used_at (only TokenTouch does).
	if got.RevokedAt != 0 || got.LastUsedAt != 0 {
		t.Fatalf("unset lifecycle timestamps must read as 0: %+v", got)
	}

	if _, found, _ := s.TokenFind("lkg_wrong"); found {
		t.Fatal("unknown plaintext must not match")
	}

	// Upsert with the same ID replaces in place (never duplicates). The write
	// replaces every column, so omitted lifecycle metadata clears.
	if err := s.TokenUpsert(Token{ID: "t1", Name: "ci", Secret: plain, Role: "viewer", CreatedAt: 100}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	list, _ = s.TokenList()
	if len(list) != 1 || list[0].Role != "viewer" {
		t.Fatalf("re-upsert: want 1 row with role viewer, got %+v", list)
	}
	if list[0].Scopes != nil || list[0].ExpiresAt != 0 || list[0].AccountID != "" || list[0].OrgID != "" {
		t.Fatalf("re-upsert must replace lifecycle metadata: %+v", list[0])
	}

	// Hard revoke = delete, then the plaintext stops resolving.
	if err := s.TokenDelete("t1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, found, _ := s.TokenFind(plain); found {
		t.Fatal("revoked token must not authenticate")
	}
	list, _ = s.TokenList()
	if len(list) != 0 {
		t.Fatalf("want empty list after delete, got %d", len(list))
	}
	// Idempotent: deleting an unknown/already-deleted id is not an error.
	if err := s.TokenDelete("t1"); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

// TestTokenFindRejectsRevokedAndExpired pins the rejection rules (Rust
// validate_token): a revoked or expired row never authenticates — it reports
// the reason — while an unknown secret stays a soft miss (found=false, no
// error) so the caller can fall back to another token source.
func TestTokenFindRejectsRevokedAndExpired(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().Unix()

	upsert := func(id, plain string, expires, revoked int64) {
		t.Helper()
		if err := s.TokenUpsert(Token{
			ID: id, Name: id, Secret: plain, Role: "viewer", CreatedAt: 1,
			ExpiresAt: expires, RevokedAt: revoked,
		}); err != nil {
			t.Fatalf("upsert %s: %v", id, err)
		}
	}
	upsert("expired", "lkg_expired", now-1, 0)
	upsert("revoked", "lkg_revoked", 0, now-1)
	upsert("both", "lkg_both", now-1, now-1)
	upsert("forever", "lkg_forever", 0, 0)
	upsert("future", "lkg_future", now+3600, 0)

	for _, tc := range []struct {
		plain string
		want  error
	}{
		{"lkg_expired", ErrTokenExpired},
		{"lkg_revoked", ErrTokenRevoked},
		// Revocation is checked before expiry (Rust validate_token's order).
		{"lkg_both", ErrTokenRevoked},
	} {
		if _, found, err := s.TokenFind(tc.plain); found || !errors.Is(err, tc.want) {
			t.Fatalf("TokenFind(%s): found=%v err=%v; want found=false and %v", tc.plain, found, err, tc.want)
		}
	}
	for _, plain := range []string{"lkg_forever", "lkg_future"} {
		if _, found, err := s.TokenFind(plain); err != nil || !found {
			t.Fatalf("TokenFind(%s): found=%v err=%v; want usable", plain, found, err)
		}
	}
	if _, found, err := s.TokenFind("lkg_nope"); found || err != nil {
		t.Fatalf("unknown secret: found=%v err=%v; want a soft miss", found, err)
	}
}

// TestTokenRevokeIsSoftAndIdempotent pins the difference between the two
// revocation paths: TokenRevoke stamps revoked_at once and keeps the row
// (listed, rejected, auditable), TokenDelete removes it. Repeating either is a
// no-op, including for an unknown id.
func TestTokenRevokeIsSoftAndIdempotent(t *testing.T) {
	s := openTestStore(t)
	plain := "lkg_soft_revoke"
	if err := s.TokenUpsert(Token{ID: "r1", Name: "r1", Secret: plain, Role: "viewer", CreatedAt: 1}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := s.TokenRevoke("r1", 500); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Re-revoking keeps the FIRST stamp rather than moving it.
	if err := s.TokenRevoke("r1", 900); err != nil {
		t.Fatalf("re-revoke: %v", err)
	}
	if err := s.TokenRevoke("unknown", 900); err != nil {
		t.Fatalf("revoke unknown id: %v", err)
	}

	list, err := s.TokenList()
	if err != nil || len(list) != 1 {
		t.Fatalf("soft revoke must keep the row: %d rows err=%v", len(list), err)
	}
	if list[0].RevokedAt != 500 {
		t.Fatalf("revoked_at = %d, want the first stamp 500", list[0].RevokedAt)
	}
	if _, found, err := s.TokenFind(plain); found || !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("revoked token: found=%v err=%v; want found=false and ErrTokenRevoked", found, err)
	}

	// Hard delete is the other path: the row disappears entirely.
	if err := s.TokenDelete("r1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if list, _ = s.TokenList(); len(list) != 0 {
		t.Fatalf("hard delete must drop the row, got %d", len(list))
	}
	if _, found, err := s.TokenFind(plain); found || err != nil {
		t.Fatalf("deleted token: found=%v err=%v; want a soft miss", found, err)
	}
}

// TestTokenTouchCoalesces pins the bounded last_used_at write: the first touch
// records the time, a touch inside the window is skipped, the next touch at or
// past the window lands, and a revoked row is never stamped.
func TestTokenTouchCoalesces(t *testing.T) {
	s := openTestStore(t)
	plain := "lkg_touch"
	if err := s.TokenUpsert(Token{ID: "touch", Name: "touch", Secret: plain, Role: "viewer", CreatedAt: 1}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	lastUsed := func() int64 {
		t.Helper()
		list, err := s.TokenList()
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %d rows err=%v", len(list), err)
		}
		return list[0].LastUsedAt
	}

	if err := s.TokenTouch("touch", 1000); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if got := lastUsed(); got != 1000 {
		t.Fatalf("first touch = %d, want 1000", got)
	}
	// Inside the window: no write, the stored value stays put.
	if err := s.TokenTouch("touch", 1000+TokenTouchWindowSecs-1); err != nil {
		t.Fatalf("in-window touch: %v", err)
	}
	if got := lastUsed(); got != 1000 {
		t.Fatalf("in-window touch wrote %d, want the coalesced 1000", got)
	}
	// At the window boundary the next write lands.
	next := int64(1000 + TokenTouchWindowSecs)
	if err := s.TokenTouch("touch", next); err != nil {
		t.Fatalf("boundary touch: %v", err)
	}
	if got := lastUsed(); got != next {
		t.Fatalf("boundary touch = %d, want %d", got, next)
	}

	// Revoked rows are never stamped (a revoke racing an in-flight request).
	if err := s.TokenRevoke("touch", 2000); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := s.TokenTouch("touch", 3000); err != nil {
		t.Fatalf("touch after revoke: %v", err)
	}
	if got := lastUsed(); got != next {
		t.Fatalf("revoked token was stamped: %d -> %d", next, got)
	}
}

// TestTokenMigrationUpgradesExistingStore pins that migration 008 is additive
// on a store created by the previous layout: a row written before the upgrade
// survives with unset lifecycle values, the new columns are usable right away,
// and re-running Migrate changes nothing (the ledger is the idempotency guard —
// sqlite has no ADD COLUMN IF NOT EXISTS).
func TestTokenMigrationUpgradesExistingStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	// A store as migration 007 left it: its ledger, plus the 007 tokens table.
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("legacy ledger: %v", err)
	}
	for _, m := range migrations {
		if m.version > 7 {
			continue
		}
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, '')`,
			m.version, m.name); err != nil {
			t.Fatalf("record legacy migration %d: %v", m.version, err)
		}
		if m.version == 7 {
			if _, err := s.db.Exec(m.ddl); err != nil {
				t.Fatalf("legacy 007 ddl: %v", err)
			}
		}
	}
	legacy := "lkg_legacy_00000000"
	if _, err := s.db.Exec(
		`INSERT INTO tokens (id, name, secret, role, created_at) VALUES ('legacy', 'pre-upgrade', ?, 'viewer', 42)`,
		HashToken(legacy)); err != nil {
		t.Fatalf("legacy row: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate 008: %v", err)
	}

	tok, found, err := s.TokenFind(legacy)
	if err != nil || !found {
		t.Fatalf("pre-upgrade token after migrate: found=%v err=%v", found, err)
	}
	if tok.ID != "legacy" || tok.Role != "viewer" || tok.CreatedAt != 42 {
		t.Fatalf("pre-upgrade row mangled: %+v", tok)
	}
	if tok.AccountID != "" || tok.OrgID != "" || tok.Scopes != nil ||
		tok.ExpiresAt != 0 || tok.RevokedAt != 0 || tok.LastUsedAt != 0 {
		t.Fatalf("pre-upgrade row must read as unset metadata: %+v", tok)
	}

	// The new columns are writable on the upgraded layout.
	if err := s.TokenTouch("legacy", 777); err != nil {
		t.Fatalf("touch after upgrade: %v", err)
	}
	if err := s.TokenRevoke("legacy", 778); err != nil {
		t.Fatalf("revoke after upgrade: %v", err)
	}
	if _, found, err := s.TokenFind(legacy); found || !errors.Is(err, ErrTokenRevoked) {
		t.Fatalf("soft-revoked upgraded row: found=%v err=%v", found, err)
	}

	// Re-running the migration is a no-op: the ledger skips applied steps.
	if err := s.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if list, err := s.TokenList(); err != nil || len(list) != 1 {
		t.Fatalf("after second migrate: %d rows err=%v", len(list), err)
	}
}

// TestTokenUpsertValidation pins the guard rails: no empty id/secret/role.
func TestTokenUpsertValidation(t *testing.T) {
	s := openTestStore(t)
	for _, tok := range []Token{
		{ID: "", Secret: "x", Role: "viewer"},
		{ID: "t", Secret: "", Role: "viewer"},
		{ID: "t", Secret: "x", Role: ""},
	} {
		if err := s.TokenUpsert(tok); err == nil {
			t.Fatalf("upsert %+v must be rejected", tok)
		}
	}
}

// TestAppliedMigrations reads the ledger through the backend-agnostic helper
// (the migrate verbs' applied/pending split): every embedded version, ascending.
func TestAppliedMigrations(t *testing.T) {
	s := openTestStore(t)
	got, err := AppliedMigrations(s)
	if err != nil {
		t.Fatalf("applied: %v", err)
	}
	steps := Migrations()
	for i, m := range steps {
		if got[i] != m.Version {
			t.Fatalf("applied[%d] = %d, want %d", i, got[i], m.Version)
		}
	}
}
