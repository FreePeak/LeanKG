package auth

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ErrInvalidAdminLogin is the only error a rejected admin login returns: an
// unknown username, a wrong password and a non-admin account look the same.
var ErrInvalidAdminLogin = errors.New("invalid admin credentials")

// ErrAdminLoginRateLimited marks a rejection by the failure throttle. It is
// always joined with ErrInvalidAdminLogin, so a caller that checks only the
// generic error still fails closed, and a caller that wants a rate-limit
// message checks this value too.
var ErrAdminLoginRateLimited = errors.New("too many failed attempts; retry in a minute")

// adminLoginThrottle is the process-wide failure throttle for admin logins.
var adminLoginThrottle = NewLoginThrottle(time.Now)

// dummyVerifier is a well-formed verifier at the live iteration count. An
// unknown username is checked against it, so a miss costs the same PBKDF2 work
// as a hit and the response time does not reveal whether the account exists.
// Its salt and key are zeros; no password is expected to match it.
var dummyVerifier = fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations,
	strings.Repeat("00", passwordSaltLen), strings.Repeat("00", passwordKeyLen))

// VerifyAdminLogin returns the account only when username names a site admin
// and password matches it. Every rejection returns ErrInvalidAdminLogin. While
// the username is inside a lockout, the rejection also wraps
// ErrAdminLoginRateLimited. The username is matched case-insensitively, as an
// account email.
func VerifyAdminLogin(st store.Backend, username, password string) (store.Account, error) {
	return verifyAdminLogin(adminLoginThrottle, st, username, password)
}

func verifyAdminLogin(throttle *LoginThrottle, st store.Backend, username, password string) (store.Account, error) {
	key := NormalizeEmail(username)
	if throttle.Blocked(key) {
		return store.Account{}, fmt.Errorf("%w: %w", ErrInvalidAdminLogin, ErrAdminLoginRateLimited)
	}
	account, found, err := st.AccountByEmail(key)
	if err != nil {
		return store.Account{}, fmt.Errorf("auth: admin login: %w", err)
	}
	if !found {
		_ = VerifyPassword(password, dummyVerifier)
		throttle.Failure(key)
		return store.Account{}, ErrInvalidAdminLogin
	}
	// The status check runs after the same PBKDF2 work as a password miss, so a
	// disabled account is not distinguishable by timing.
	if !VerifyPassword(password, account.PasswordHash) || !account.SiteAdmin || account.Status != "active" {
		throttle.Failure(key)
		return store.Account{}, ErrInvalidAdminLogin
	}
	throttle.Success(key)
	return account, nil
}

// CreateSiteAdmin stores the site-admin account for username, keeping only a
// PBKDF2 verifier of password. The username is normalized and used as the
// account's email key. Callers decide whether an admin may be created at all.
func CreateSiteAdmin(st store.Backend, username, password string) (store.Account, error) {
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
	if err := st.AccountUpsert(account); err != nil {
		return store.Account{}, err
	}
	return account, nil
}
