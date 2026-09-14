// PostgreSQL token-store methods: mirror of store_tokens.go over pgx.
// Only the SHA-256 hex of each secret is ever persisted; TokenUpsert and
// TokenFind take the PLAINTEXT secret and hash it internally. Column list,
// rejection rules (revoked before expired, expiry strict) and the coalesced
// last_used_at write are shared with the sqlite backend — see store_tokens.go
// for the contract and its ceilings.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// TokenUpsert inserts or replaces a token keyed by ID. tok.Secret is the
// plaintext; only HashToken(tok.Secret) is written.
func (s *PGStore) TokenUpsert(tok Token) error {
	if tok.ID == "" {
		return fmt.Errorf("store: token upsert: empty id")
	}
	if tok.Secret == "" {
		return fmt.Errorf("store: token upsert: empty secret")
	}
	if tok.Role == "" {
		return fmt.Errorf("store: token upsert: empty role")
	}
	_, err := s.pool.Exec(pgCtx, `INSERT INTO tokens (`+tokenInsertCols+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
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
func (s *PGStore) TokenList() ([]Token, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+tokenSelectCols+` FROM tokens ORDER BY created_at, id`)
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
func (s *PGStore) TokenDelete(id string) error {
	_, err := s.pool.Exec(pgCtx, `DELETE FROM tokens WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: token delete %s: %w", id, err)
	}
	return nil
}

// TokenRevoke soft-revokes a token: it stamps revoked_at (once) and leaves the
// row in place. Revoking an unknown id or an already-revoked token is a no-op,
// matching TokenDelete's idempotence.
func (s *PGStore) TokenRevoke(id string, at int64) error {
	_, err := s.pool.Exec(pgCtx,
		`UPDATE tokens SET revoked_at = $1 WHERE id = $2 AND revoked_at IS NULL`, at, id)
	if err != nil {
		return fmt.Errorf("store: token revoke %s: %w", id, err)
	}
	return nil
}

// TokenTouch stamps last_used_at for a successful authentication, coalesced to
// at most one write per TokenTouchWindowSecs. Revoked tokens are skipped.
func (s *PGStore) TokenTouch(id string, at int64) error {
	_, err := s.pool.Exec(pgCtx,
		`UPDATE tokens SET last_used_at = $1 WHERE id = $2 AND revoked_at IS NULL
			AND (last_used_at IS NULL OR last_used_at <= $3)`,
		at, id, at-TokenTouchWindowSecs)
	if err != nil {
		return fmt.Errorf("store: token touch %s: %w", id, err)
	}
	return nil
}

// TokenFind resolves a plaintext bearer secret to its stored row by matching
// HashToken(secret) against the at-rest column. An unknown secret is
// (Token{}, false, nil); a revoked or expired row is (Token{}, false, err)
// wrapping ErrTokenRevoked / ErrTokenExpired.
func (s *PGStore) TokenFind(secret string) (Token, bool, error) {
	row := s.pool.QueryRow(pgCtx, `SELECT `+tokenSelectCols+` FROM tokens WHERE secret = $1`,
		HashToken(secret))
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
