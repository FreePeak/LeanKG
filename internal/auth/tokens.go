// DB-backed bearer-token support for the auth gate (Rust src/auth/tokens.rs
// parity): minting, verification, and the DB-first classification path.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// NewSecret generates an opaque bearer secret `lkg_<32hex>_<8hex>` (Rust
// auth::tokens::generate_token). The plaintext is shown once, at mint time.
func NewSecret() (string, error) {
	key := make([]byte, 16)
	salt := make([]byte, 4)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	return "lkg_" + hex.EncodeToString(key) + "_" + hex.EncodeToString(salt), nil
}

// RoleFromString maps a stored role name to a Role (Rust Role::from_str).
func RoleFromString(s string) (Role, bool) {
	switch s {
	case "admin":
		return Admin, true
	case "contributor":
		return Contributor, true
	case "viewer":
		return Viewer, true
	}
	return Viewer, false
}

// MintRequest describes the token to issue.
type MintRequest struct {
	Name string
	Role string
	// AccountID / OrgID are opaque labels (Rust AccessToken account_id/org_id).
	// Ceiling: the Go engine has no accounts/orgs subsystem, so nothing
	// resolves or enforces them — they round-trip for callers that track
	// ownership themselves.
	AccountID string
	OrgID     string
	// Scopes are persisted and surfaced on the Grant. A project:<name> scope
	// restricts the token to the named projects (AuthContext.ProjectAllowed);
	// any other scope is opaque and authorizes nothing.
	Scopes []string
	// TTL is the token lifetime. 0 means "never expires"; a negative value
	// mints an already-expired token (the Rust test's negative-TTL path).
	// Sub-second TTLs truncate to whole seconds.
	TTL time.Duration
}

// Mint issues a new DB-backed token: it generates an opaque secret, persists
// only the SHA-256 hash through the backend's tokens table, and returns the
// plaintext (shown once) plus the stored row (Secret = at-rest hash).
func Mint(ctx context.Context, st Store, req MintRequest) (string, store.Token, error) {
	if _, ok := RoleFromString(req.Role); !ok {
		return "", store.Token{}, fmt.Errorf("auth: unknown role %q", req.Role)
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", store.Token{}, fmt.Errorf("auth: generate token id: %w", err)
	}
	secret, err := NewSecret()
	if err != nil {
		return "", store.Token{}, err
	}
	now := time.Now().Unix()
	tok := store.Token{
		ID:        hex.EncodeToString(id),
		Name:      req.Name,
		Secret:    secret, // TokenUpsert hashes at rest
		Role:      req.Role,
		CreatedAt: now,
		AccountID: req.AccountID,
		OrgID:     req.OrgID,
		Scopes:    req.Scopes,
	}
	if req.TTL != 0 {
		tok.ExpiresAt = now + int64(req.TTL/time.Second)
	}
	if err := st.TokenUpsert(ctx, tok); err != nil {
		return "", store.Token{}, fmt.Errorf("auth: mint token: %w", store.Fail("token upsert", err))
	}
	tok.Secret = store.HashToken(secret)
	return secret, tok, nil
}

// Grant is the authorization resolved from a valid DB token: the role that
// gates actions, plus the token's ownership labels and scopes (Rust
// db::models::AuthContext carried client_id + role only; its scopes column was
// never surfaced).
type Grant struct {
	TokenID   string
	AccountID string
	OrgID     string
	Role      Role
	Scopes    []string
	ExpiresAt int64 // 0 = never expires
}

// Verify resolves a plaintext bearer secret against the DB token store.
//
// An unknown secret is (Grant{}, false, nil), so the caller may fall back to
// another token source. A revoked or expired token is (Grant{}, false, err)
// wrapping store.ErrTokenRevoked / store.ErrTokenExpired: those must never
// fall through to a fallback source. A successful verification stamps
// last_used_at (coalesced by store.TokenTouch).
func Verify(ctx context.Context, st Store, secret string) (Grant, bool, error) {
	tok, found, err := st.TokenFind(ctx, secret)
	if err != nil {
		// A revoked or expired row is a rejection the client gets as a 400, not a
		// store fault, so only other failures are classified as store errors.
		if !errors.Is(err, store.ErrTokenRevoked) && !errors.Is(err, store.ErrTokenExpired) {
			err = store.Fail("token find", err)
		}
		return Grant{}, false, fmt.Errorf("auth: token store: %w", err)
	}
	if !found {
		return Grant{}, false, nil
	}
	role, ok := RoleFromString(tok.Role)
	if !ok {
		return Grant{}, false, fmt.Errorf("auth: token %s has unknown role %q", tok.ID, tok.Role)
	}
	// Best-effort: last_used_at is observability, never authorization, so a
	// failed touch (read-only handle, row deleted mid-flight) must not reject
	// a valid token. Rust validate_token ignores the touch result too.
	_ = st.TokenTouch(ctx, tok.ID, time.Now().Unix())
	return Grant{
		TokenID:   tok.ID,
		AccountID: tok.AccountID,
		OrgID:     tok.OrgID,
		Role:      role,
		Scopes:    tok.Scopes,
		ExpiresAt: tok.ExpiresAt,
	}, true, nil
}
