package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// ErrInvalidAdminLogin is the only error a rejected login returns, on every
// surface (REST, the MCP authenticate tool, the CLI): an unknown username, a
// wrong password and, for admin logins, a non-admin or disabled account look the
// same. The name predates the REST login and is kept because internal/mcp
// matches on it.
var ErrInvalidAdminLogin = errors.New("invalid credentials")

// ErrAdminLoginRateLimited marks a rejection by the failure throttle. It is
// always joined with ErrInvalidAdminLogin, so a caller that checks only the
// generic error still fails closed, and a caller that wants a rate-limit
// message checks this value too.
var ErrAdminLoginRateLimited = errors.New("too many failed attempts; retry in a minute")

// loginThrottle is the process-wide failure throttle shared by every login
// surface, so alternating surfaces does not multiply the attempts allowed.
var loginThrottle = NewLoginThrottle(time.Now)

// dummyVerifier is a well-formed verifier at the live iteration count. An
// unknown username is checked against it, so a miss costs the same PBKDF2 work
// as a hit and the response time does not reveal whether the account exists.
// Its salt and key are zeros; no password is expected to match it.
var dummyVerifier = fmt.Sprintf("%s$%d$%s$%s", passwordScheme, passwordIterations,
	strings.Repeat("00", passwordSaltLen), strings.Repeat("00", passwordKeyLen))

// verifyLogin is the one credential check behind every login surface. It
// returns the account only when email names an account, password matches it
// and the account is active; with adminOnly, it must also be a site admin.
// Every rejection returns ErrInvalidAdminLogin. While the username is inside a
// lockout, the rejection also wraps ErrAdminLoginRateLimited. Failures are
// counted for unknown usernames too, so a lockout never reveals whether an
// account exists. The email is matched case-insensitively.
func verifyLogin(ctx context.Context, throttle *LoginThrottle, st Store, email, password string, adminOnly bool) (store.Account, error) {
	key := NormalizeEmail(email)
	// The attempt is reserved before any lookup or PBKDF2 work, so concurrent
	// attempts for one username cannot all pass the lockout check. A failure
	// keeps the reservation; only a success releases it.
	if !throttle.Reserve(key) {
		return store.Account{}, fmt.Errorf("%w: %w", ErrInvalidAdminLogin, ErrAdminLoginRateLimited)
	}
	account, found, err := st.AccountByEmail(ctx, key)
	if err != nil {
		return store.Account{}, fmt.Errorf("auth: login: %w", err)
	}
	if !found {
		_ = VerifyPassword(password, dummyVerifier)
		return store.Account{}, ErrInvalidAdminLogin
	}
	// The status and admin checks run after the same PBKDF2 work as a password
	// miss, so a disabled account is not distinguishable by timing.
	allowed := account.Status == "active" && (!adminOnly || account.SiteAdmin)
	if !VerifyPassword(password, account.PasswordHash) || !allowed {
		return store.Account{}, ErrInvalidAdminLogin
	}
	throttle.Success(key)
	return account, nil
}
