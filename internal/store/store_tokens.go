// DB-backed bearer tokens (Rust src/auth/tokens.rs + src/db/pg/migrations/
// 004_auth.sql access_tokens parity). Only the SHA-256 hex of each opaque
// secret is ever persisted: TokenUpsert and TokenFind take the PLAINTEXT
// secret and hash it internally (Rust auth::tokens::hash_token), so no
// plaintext can reach the table. All returned Tokens carry the at-rest hash.
//
// Lifecycle columns (migration 008 "auth-token-lifecycle" — additive on top
// of 007):
//   - expires_at: 0 means "never expires"; TokenFind rejects a token whose
//     expiry has passed (Rust validate_token: expired when exp < now).
//   - revoked_at: soft revocation. TokenFind rejects a revoked token before
//     it looks at expiry (same order as Rust). TokenDelete stays the hard
//     delete.
//   - last_used_at: stamped by TokenTouch on a successful authentication,
//     coalesced so a busy bearer cannot turn every request into a write.
//   - scopes: comma-separated TEXT (see encodeScopes).
//   - account_id / org_id: opaque LABELS. Ceiling: the Go engine has no
//     accounts/orgs subsystem (Rust auth/accounts.rs is not ported), so
//     nothing resolves or enforces them; they round-trip for clients that
//     carry ownership metadata.
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TokenTouchWindowSecs bounds how often one token's last_used_at is written:
// a touch whose stored value is newer than this is a no-op. Bounded write —
// a token authenticating 1000 req/s still costs at most one UPDATE per token
// per window. Rust's validate_token wrote on every call.
//
// ponytail: fixed 5s window, not per-token backoff. Ceiling: the stored value
// can lag real activity by up to the window. Upgrade path: make it
// configurable or write through an in-process ring buffer if last_used
// precision ever matters.
const TokenTouchWindowSecs int64 = 5

// ErrTokenExpired / ErrTokenRevoked are the rejection reasons TokenFind
// reports for a token that exists but must not authenticate (Rust
// validate_token's "access token expired" / "access token revoked").
var (
	ErrTokenExpired = errors.New("access token expired")
	ErrTokenRevoked = errors.New("access token revoked")
)

// Token is one DB-backed bearer token. On input to TokenUpsert, Secret is the
// plaintext; every Token returned by the store carries the at-rest form (the
// SHA-256 hex digest of that plaintext, never the plaintext itself).
//
// The three lifecycle timestamps use 0 for "not set" (never expires, not
// revoked, never used) — the stored columns are NULL in that case, and Rust's
// Option<i64> maps onto the same meaning.
//
// AccountID / OrgID are opaque labels: the Go engine has no accounts
// subsystem, so they are never resolved to an account or membership.
type Token struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Secret     string   `json:"secret"`
	Role       string   `json:"role"`
	CreatedAt  int64    `json:"created_at"`
	AccountID  string   `json:"account_id,omitempty"`
	OrgID      string   `json:"org_id,omitempty"`
	Scopes     []string `json:"scopes,omitempty"`
	ExpiresAt  int64    `json:"expires_at,omitempty"`
	RevokedAt  int64    `json:"revoked_at,omitempty"`
	LastUsedAt int64    `json:"last_used_at,omitempty"`
}

// HashToken returns the SHA-256 hex digest of a token plaintext
// (Rust auth::tokens::hash_token).
func HashToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// encodeScopes joins scopes into the at-rest comma-separated form.
//
// ponytail: comma-separated TEXT, not JSON. Rust's 004_auth.sql declared
// scopes JSONB but its store always wrote an empty Vec and never read the
// column, so there is no Rust behavior to preserve here. Ceiling: a scope
// name cannot contain a comma (RFC 6749 scope tokens cannot either).
func encodeScopes(scopes []string) string { return strings.Join(scopes, ",") }

// decodeScopes splits the at-rest form back into scope names; empty means no
// scopes and yields nil.
func decodeScopes(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nullIfZero writes NULL for an unset timestamp; 0 and NULL are the same
// value to every reader (the SELECTs COALESCE back to 0).
func nullIfZero(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// tokenInsertCols is the shared INSERT/UPDATE column list of both backends;
// tokenSelectCols adds the NULL -> unset normalization every read applies.
// The two are separate because COALESCE is legal in a SELECT list and not in
// an INSERT column list.
const tokenInsertCols = `id, name, secret, role, created_at, account_id, org_id, scopes,
	expires_at, revoked_at, last_used_at`

const tokenSelectCols = `id, name, secret, role, created_at, account_id, org_id, scopes,
	COALESCE(expires_at, 0), COALESCE(revoked_at, 0), COALESCE(last_used_at, 0)`

// TokenUpsert inserts or replaces a token keyed by ID. tok.Secret is the
// plaintext; only HashToken(tok.Secret) is written. The write replaces every
// column, so an upsert with zero lifecycle values clears them (Rust's
// AccessTokenStore :put behaves the same) — TokenRevoke is the operation that
// only ever sets a timestamp.
func (s *Store) TokenUpsert(tok Token) error {
	if tok.ID == "" {
		return fmt.Errorf("store: token upsert: empty id")
	}
	if tok.Secret == "" {
		return fmt.Errorf("store: token upsert: empty secret")
	}
	if tok.Role == "" {
		return fmt.Errorf("store: token upsert: empty role")
	}
	_, err := s.db.Exec(`INSERT INTO tokens (`+tokenInsertCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, secret = excluded.secret,
			role = excluded.role, created_at = excluded.created_at,
			account_id = excluded.account_id, org_id = excluded.org_id,
			scopes = excluded.scopes, expires_at = excluded.expires_at,
			revoked_at = excluded.revoked_at, last_used_at = excluded.last_used_at`,
		tok.ID, tok.Name, HashToken(tok.Secret), tok.Role, tok.CreatedAt,
		tok.AccountID, tok.OrgID, encodeScopes(tok.Scopes),
		nullIfZero(tok.ExpiresAt), nullIfZero(tok.RevokedAt), nullIfZero(tok.LastUsedAt))
	if err != nil {
		return fmt.Errorf("store: token upsert %s: %w", tok.ID, err)
	}
	return nil
}

// TokenList returns every token (at-rest hash form) ordered by creation,
// revoked rows included — revocation is state, not deletion.
func (s *Store) TokenList() ([]Token, error) {
	rows, err := s.db.Query(`SELECT ` + tokenSelectCols + ` FROM tokens ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("store: token list: %w", err)
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		t, err := scanToken(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: token list scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TokenDelete removes a token (hard revocation). Deleting an unknown id is a
// no-op, so revoke flows stay idempotent.
func (s *Store) TokenDelete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tokens WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("store: token delete %s: %w", id, err)
	}
	return nil
}

// TokenRevoke soft-revokes a token: it stamps revoked_at (once) and leaves the
// row in place. Revoking an unknown id or an already-revoked token is a no-op,
// matching TokenDelete's idempotence (Rust's revoke_token reports false for
// both cases instead of erroring).
func (s *Store) TokenRevoke(id string, at int64) error {
	_, err := s.db.Exec(
		`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, at, id)
	if err != nil {
		return fmt.Errorf("store: token revoke %s: %w", id, err)
	}
	return nil
}

// TokenTouch stamps last_used_at for a successful authentication, coalesced to
// at most one write per TokenTouchWindowSecs (see the constant). It skips
// revoked tokens, so a revoke racing an in-flight request cannot resurrect
// "last used" state. Callers treat failures as best-effort (Rust's
// validate_token ignores the touch result too).
func (s *Store) TokenTouch(id string, at int64) error {
	_, err := s.db.Exec(
		`UPDATE tokens SET last_used_at = ? WHERE id = ? AND revoked_at IS NULL
			AND (last_used_at IS NULL OR last_used_at <= ?)`,
		at, id, at-TokenTouchWindowSecs)
	if err != nil {
		return fmt.Errorf("store: token touch %s: %w", id, err)
	}
	return nil
}

// TokenFind resolves a plaintext bearer secret to its stored row by matching
// HashToken(secret) against the at-rest column.
//
// An unknown secret is (Token{}, false, nil) so callers can fall back to other
// token sources. A revoked or expired row is (Token{}, false, err) wrapping
// ErrTokenRevoked / ErrTokenExpired: revocation is checked first, then expiry
// (Rust validate_token's order), and neither may authenticate.
func (s *Store) TokenFind(secret string) (Token, bool, error) {
	row := s.db.QueryRow(`SELECT `+tokenSelectCols+` FROM tokens WHERE secret = ?`, HashToken(secret))
	t, err := scanToken(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, false, nil
		}
		return Token{}, false, fmt.Errorf("store: token find: %w", err)
	}
	if err := t.reject(time.Now().Unix()); err != nil {
		return Token{}, false, err
	}
	return t, true, nil
}

// reject reports why a stored token must not authenticate, or nil when it is
// usable. now is epoch seconds; expiry is strict (a token is valid during its
// expiry second) — Rust validate_token: `if exp < now_epoch()`.
func (t Token) reject(now int64) error {
	if t.RevokedAt != 0 {
		return fmt.Errorf("store: token %s: %w", t.ID, ErrTokenRevoked)
	}
	if t.ExpiresAt != 0 && t.ExpiresAt < now {
		return fmt.Errorf("store: token %s: %w", t.ID, ErrTokenExpired)
	}
	return nil
}

// scanToken scans the shared token column list through either backend's Scan.
func scanToken(scan func(dest ...any) error) (Token, error) {
	var t Token
	var scopes string
	err := scan(&t.ID, &t.Name, &t.Secret, &t.Role, &t.CreatedAt,
		&t.AccountID, &t.OrgID, &scopes, &t.ExpiresAt, &t.RevokedAt, &t.LastUsedAt)
	if err != nil {
		return Token{}, err
	}
	t.Scopes = decodeScopes(scopes)
	return t, nil
}
