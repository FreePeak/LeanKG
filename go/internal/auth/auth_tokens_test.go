package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// openAuthStore opens a migrated RW sqlite store under a temp project dir.
func openAuthStore(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// clearTokenEnv empties the env fallback so tests exercise the DB path only.
func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LEANKG_TOKEN_ADMIN", "LEANKG_TOKEN_CONTRIBUTOR", "LEANKG_TOKEN_VIEWER"} {
		t.Setenv(k, "")
	}
}

// TestDBTokenMintAuthenticateRevoke pins the full lifecycle: mint a DB token,
// authenticate with its plaintext through the middleware, revoke it, and
// confirm the plaintext stops resolving.
func TestDBTokenMintAuthenticateRevoke(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	plain, tok, err := Mint(st, MintRequest{Name: "ci", Role: "contributor"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if plain == "" || tok.ID == "" {
		t.Fatal("mint returned empty secret or id")
	}

	role, err := RoleForRequestWithStore(st, reqWith(plain))
	if err != nil || role != Contributor {
		t.Fatalf("authenticate: role=%v err=%v, want Contributor", role, err)
	}

	// Middleware end-to-end: contributor may POST the import (write) route.
	var writes int
	h := MiddlewareWithStore(st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writes++ }))
	r := httptest.NewRequest("POST", "/api/v1/import", nil)
	r.Header.Set("Authorization", "Bearer "+plain)
	h.ServeHTTP(httptest.NewRecorder(), r)
	if writes != 1 {
		t.Fatalf("contributor write through middleware: writes=%d, want 1", writes)
	}

	// Revoke: the plaintext must stop resolving. Here the DB held the ONLY
	// token, so revoking it empties every source and the gate returns to the
	// local default (Admin) — the same posture as unsetting all env vars
	// (documented ceiling: revoking every DB token with no env fallback
	// re-disables the gate).
	if err := st.TokenDelete(tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	role, err = RoleForRequestWithStore(st, reqWith("nope"))
	if err != nil || role != Admin {
		t.Fatalf("after revoke-all: role=%v err=%v, want local-default Admin", role, err)
	}
}

// TestRolePrecedenceDBOverEnv pins the precedence contract: the same secret
// present in BOTH the DB store and the env resolves to the DB role; env-only
// secrets still work as fallback.
func TestRolePrecedenceDBOverEnv(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	dual, _, err := Mint(st, MintRequest{Name: "dual", Role: "viewer"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	t.Setenv("LEANKG_TOKEN_ADMIN", dual) // same secret, higher env role
	t.Setenv("LEANKG_TOKEN_CONTRIBUTOR", "env-only-tok")

	// DB first: DB viewer wins over env admin for the dual-registered secret.
	role, err := RoleForRequestWithStore(st, reqWith(dual))
	if err != nil || role != Viewer {
		t.Fatalf("dual secret: role=%v err=%v, want DB Viewer", role, err)
	}
	// Env fallback: a secret only in env still resolves.
	role, err = RoleForRequestWithStore(st, reqWith("env-only-tok"))
	if err != nil || role != Contributor {
		t.Fatalf("env-only secret: role=%v err=%v, want Contributor", role, err)
	}
}

// TestLocalDefaultWithStoreWired pins the local default with a DB store wired
// but zero tokens anywhere: everything is Admin (no accidental lockout).
func TestLocalDefaultWithStoreWired(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	role, err := RoleForRequestWithStore(st, reqWith(""))
	if err != nil || role != Admin {
		t.Fatalf("empty store: role=%v err=%v, want Admin", role, err)
	}
}

// TestDBTokensEnableGate pins the security direction of precedence: once a
// single DB token exists (and env is empty), unknown/missing tokens are
// rejected — the DB presence alone enables the gate.
func TestDBTokensEnableGate(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	if _, _, err := Mint(st, MintRequest{Name: "ci", Role: "viewer"}); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if _, err := RoleForRequestWithStore(st, reqWith("")); err == nil {
		t.Fatal("missing token must be rejected once DB tokens exist")
	}
	if _, err := RoleForRequestWithStore(st, reqWith("nope")); err == nil {
		t.Fatal("unknown token must be rejected once DB tokens exist")
	}
}

// TestMintHashesAtRest verifies the mint helper stores only the hash.
func TestMintHashesAtRest(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)
	plain, _, err := Mint(st, MintRequest{Name: "ci", Role: "admin"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	list, err := st.TokenList()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d rows err=%v", len(list), err)
	}
	if list[0].Secret != store.HashToken(plain) || list[0].Secret == plain {
		t.Fatal("minted row must hold the SHA-256 hash, not the plaintext")
	}
	if _, ok := RoleFromString("nope"); ok {
		t.Fatal("unknown role string must not map")
	}
}

// TestMintPersistsLifecycleMetadata pins the mint contract for the request
// fields Rust's create_access_token carried: role, account/org labels, scopes,
// and TTL -> expires_at. Unknown roles are rejected before anything is stored.
func TestMintPersistsLifecycleMetadata(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	started := time.Now().Unix()
	plain, tok, err := Mint(st, MintRequest{
		Name:      "ci",
		Role:      "viewer",
		AccountID: "acct-7",
		OrgID:     "org-9",
		Scopes:    []string{"graphs:read", "memory:write"},
		TTL:       90 * time.Minute,
	})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if tok.AccountID != "acct-7" || tok.OrgID != "org-9" {
		t.Fatalf("ownership labels mangled: %+v", tok)
	}
	if len(tok.Scopes) != 2 || tok.Scopes[0] != "graphs:read" || tok.Scopes[1] != "memory:write" {
		t.Fatalf("scopes mangled: %+v", tok.Scopes)
	}
	if tok.ExpiresAt-tok.CreatedAt != 5400 || tok.CreatedAt < started {
		t.Fatalf("expires_at = %d, created_at = %d; want created+5400 (minted at %d)",
			tok.ExpiresAt, tok.CreatedAt, started)
	}
	grant, found, err := Verify(st, plain)
	if err != nil || !found {
		t.Fatalf("verify: found=%v err=%v", found, err)
	}
	if grant.TokenID != tok.ID {
		t.Fatalf("grant token id = %s, want %s", grant.TokenID, tok.ID)
	}
	if grant.AccountID != "acct-7" || grant.OrgID != "org-9" {
		t.Fatalf("grant ownership labels mangled: %+v", grant)
	}
	if len(grant.Scopes) != 2 || grant.Scopes[0] != "graphs:read" {
		t.Fatalf("grant scopes mangled: %+v", grant.Scopes)
	}
	if grant.ExpiresAt != tok.ExpiresAt {
		t.Fatalf("grant expiry = %d, want %d", grant.ExpiresAt, tok.ExpiresAt)
	}

	// Unknown role: rejected, nothing stored.
	if _, _, err := Mint(st, MintRequest{Name: "x", Role: "root"}); err == nil {
		t.Fatal("unknown role must be rejected")
	}
	if list, _ := st.TokenList(); len(list) != 1 {
		t.Fatalf("rejected mint must not store; %d rows", len(list))
	}

	// Zero TTL = never expires.
	_, forever, err := Mint(st, MintRequest{Name: "f", Role: "admin"})
	if err != nil {
		t.Fatalf("mint forever: %v", err)
	}
	if forever.ExpiresAt != 0 {
		t.Fatalf("TTL 0 must never expire; got %d", forever.ExpiresAt)
	}
}

// TestVerifyRejectsRevokedAndExpired pins that Verify stops a revoked or
// expired DB token outright — no fall-through to the env fallback, so
// revocation beats a stale static copy of the same secret (Rust
// validate_token: revoked before expired, expired when exp < now).
func TestVerifyRejectsRevokedAndExpired(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	revoked, revokedTok, err := Mint(st, MintRequest{Name: "r", Role: "admin"})
	if err != nil {
		t.Fatalf("mint revoked: %v", err)
	}
	expired, _, err := Mint(st, MintRequest{Name: "e", Role: "viewer", TTL: -time.Minute})
	if err != nil {
		t.Fatalf("mint expired: %v", err)
	}
	// Same secret statically configured as env ADMIN: revocation must still win.
	t.Setenv("LEANKG_TOKEN_ADMIN", revoked)
	t.Setenv("LEANKG_TOKEN_VIEWER", expired)

	if err := st.TokenRevoke(revokedTok.ID, time.Now().Unix()); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	revokedGrant, found, err := Verify(st, revoked)
	if found || err == nil || !errors.Is(err, store.ErrTokenRevoked) {
		t.Fatalf("revoked: grant=%v found=%v err=%v; want found=false, ErrTokenRevoked", revokedGrant, found, err)
	}
	expiredGrant, found, err := Verify(st, expired)
	if found || err == nil || !errors.Is(err, store.ErrTokenExpired) {
		t.Fatalf("expired: grant=%v found=%v err=%v; want found=false, ErrTokenExpired", expiredGrant, found, err)
	}

	// And through the gate: both tokens are rejected (401), not demoted or
	// escalated through the env fallback that shares their secrets.
	for _, secret := range []string{revoked, expired} {
		if _, err := RoleForRequestWithStore(st, reqWith(secret)); err == nil {
			t.Fatalf("revoked/expired token %q must be rejected by the gate", secret)
		}
	}
}

// TestVerifyStampsLastUsed pins that a successful authentication stamps
// last_used_at and that the stamp is coalesced (a burst of requests writes at
// most once per TokenTouchWindowSecs). Unknown secrets never stamp.
func TestVerifyStampsLastUsed(t *testing.T) {
	clearTokenEnv(t)
	st := openAuthStore(t)

	plain, tok, err := Mint(st, MintRequest{Name: "u", Role: "viewer"})
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	// A fresh row reports no use, and an unknown secret is a soft miss that
	// stamps nothing.
	if list, err := st.TokenList(); err != nil || len(list) != 1 || list[0].LastUsedAt != 0 {
		t.Fatalf("fresh mint must read last_used_at = 0: %v (err=%v)", list, err)
	}
	if _, found, err := Verify(st, "lkg_unknown"); err != nil || found {
		t.Fatalf("unknown secret: found=%v err=%v; want a soft miss", found, err)
	}

	for i := 0; i < 20; i++ {
		if _, found, err := Verify(st, plain); err != nil || !found {
			t.Fatalf("verify %d: found=%v err=%v", i, found, err)
		}
	}
	list, err := st.TokenList()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d rows err=%v", len(list), err)
	}
	if list[0].LastUsedAt == 0 {
		t.Fatal("successful verification must stamp last_used_at")
	}
	if want := time.Now().Unix() - store.TokenTouchWindowSecs; list[0].LastUsedAt < want {
		t.Fatalf("burst of verifies wrote a stale stamp %d (< %d); coalescing must keep it current",
			list[0].LastUsedAt, want)
	}
	if list[0].ID != tok.ID {
		t.Fatalf("stamped the wrong token: %s", list[0].ID)
	}
}
