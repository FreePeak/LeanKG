package auth

import (
	"context"
	"errors"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// VerifyAdminLogin returns the account only when username names a site admin
// and password matches it. Every rejection returns ErrInvalidAdminLogin. While
// the username is inside a lockout, the rejection also wraps
// ErrAdminLoginRateLimited. The username is matched case-insensitively, as an
// account email.
func VerifyAdminLogin(ctx context.Context, st store.Backend, username, password string) (store.Account, error) {
	return verifyLogin(ctx, loginThrottle, st, username, password, true)
}

// CreateSiteAdmin stores the site-admin account for username, keeping only a
// PBKDF2 verifier of password. The username is normalized and used as the
// account's email key. Callers decide whether an admin may be created at all.
func CreateSiteAdmin(ctx context.Context, st store.Backend, username, password string) (store.Account, error) {
	key := NormalizeEmail(username)
	if key == "" {
		return store.Account{}, errors.New("admin username is required")
	}
	if err := ValidatePassword(password); err != nil {
		return store.Account{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return store.Account{}, err
	}
	id, err := newID()
	if err != nil {
		return store.Account{}, err
	}
	now := time.Now().Unix()
	account := store.Account{
		ID:           id,
		Email:        key,
		Name:         key,
		PasswordHash: hash,
		Status:       "active",
		SiteAdmin:    true,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := st.AccountUpsert(ctx, account); err != nil {
		return store.Account{}, err
	}
	return account, nil
}
