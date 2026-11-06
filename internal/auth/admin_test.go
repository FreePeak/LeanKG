package auth

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

const adminTestPassword = "correct horse battery"

// seedAccount stores an account directly, bypassing Register, so a test can
// create a plain (non-admin) account with a real verifier.
func seedAccount(t *testing.T, st store.Backend, email, password string, siteAdmin bool) store.Account {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	a := store.Account{ID: "acct-" + email, Email: email, Name: email, PasswordHash: hash,
		Status: "active", SiteAdmin: siteAdmin, CreatedAt: 1, UpdatedAt: 1}
	if err := st.AccountUpsert(a); err != nil {
		t.Fatalf("upsert %s: %v", email, err)
	}
	return a
}

func TestVerifyAdminLoginAcceptsSiteAdmin(t *testing.T) {
	st := openAuthStore(t)
	seeded := seedAccount(t, st, "admin", adminTestPassword, true)

	throttle, _ := newTestThrottle()
	got, err := verifyAdminLogin(throttle, st, "ADMIN", adminTestPassword)
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	if got.ID != seeded.ID || !got.SiteAdmin {
		t.Fatalf("returned account = %+v, want the site admin", got)
	}
}

// The three rejections must be indistinguishable to the caller: same error
// value, same text.
func TestVerifyAdminLoginGenericErrorForEveryRejection(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "admin", adminTestPassword, true)
	seedAccount(t, st, "plain", adminTestPassword, false)

	throttle, _ := newTestThrottle()
	cases := map[string]struct{ user, password string }{
		"wrong password": {"admin", "wrong password!"},
		"unknown user":   {"nobody", adminTestPassword},
		"non-admin":      {"plain", adminTestPassword},
	}
	for name, c := range cases {
		_, err := verifyAdminLogin(throttle, st, c.user, c.password)
		if !errors.Is(err, ErrInvalidAdminLogin) {
			t.Fatalf("%s: err = %v, want ErrInvalidAdminLogin", name, err)
		}
		if errors.Is(err, ErrAdminLoginRateLimited) {
			t.Fatalf("%s: a single failure must not report rate limiting", name)
		}
		if err.Error() != ErrInvalidAdminLogin.Error() {
			t.Fatalf("%s: err text %q differs from the generic error", name, err.Error())
		}
	}
}

// A disabled site admin is rejected with the same generic error even when the
// password is correct.
func TestVerifyAdminLoginRejectsDisabledAdmin(t *testing.T) {
	st := openAuthStore(t)
	a := seedAccount(t, st, "admin", adminTestPassword, true)
	a.Status = "disabled"
	if err := st.AccountUpsert(a); err != nil {
		t.Fatalf("disable admin: %v", err)
	}

	throttle, _ := newTestThrottle()
	_, err := verifyAdminLogin(throttle, st, "admin", adminTestPassword)
	if !errors.Is(err, ErrInvalidAdminLogin) || errors.Is(err, ErrAdminLoginRateLimited) {
		t.Fatalf("disabled admin err = %v, want the generic ErrInvalidAdminLogin", err)
	}
}

func TestVerifyAdminLoginThrottlesAfterFiveFailures(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "admin", adminTestPassword, true)

	throttle, clock := newTestThrottle()
	for i := 0; i < loginMaxFailures; i++ {
		if _, err := verifyAdminLogin(throttle, st, "admin", "wrong password!"); err == nil {
			t.Fatal("wrong password must fail")
		}
	}

	// The correct password is rejected too while the username is locked, and
	// the rejection carries both the generic error and the rate-limit marker.
	_, err := verifyAdminLogin(throttle, st, "admin", adminTestPassword)
	if !errors.Is(err, ErrInvalidAdminLogin) || !errors.Is(err, ErrAdminLoginRateLimited) {
		t.Fatalf("locked login err = %v, want generic + rate-limited", err)
	}

	clock.Advance(loginLockout)
	if _, err := verifyAdminLogin(throttle, st, "admin", adminTestPassword); err != nil {
		t.Fatalf("login after the 60s lockout: %v", err)
	}
}

func TestDummyVerifierMatchesLiveCost(t *testing.T) {
	parts := strings.Split(dummyVerifier, "$")
	if len(parts) != 4 || parts[0] != passwordScheme {
		t.Fatalf("dummy verifier %q is not in the live scheme", dummyVerifier)
	}
	if n, err := strconv.Atoi(parts[1]); err != nil || n != passwordIterations {
		t.Fatalf("dummy iterations = %q, want %d", parts[1], passwordIterations)
	}
}

func TestCreateSiteAdminStoresOnlyAVerifier(t *testing.T) {
	st := openAuthStore(t)
	a, err := CreateSiteAdmin(st, " Admin ", adminTestPassword)
	if err != nil {
		t.Fatalf("CreateSiteAdmin: %v", err)
	}
	if a.Email != "admin" || !a.SiteAdmin || a.Status != "active" {
		t.Fatalf("account = %+v, want normalized site admin", a)
	}
	if a.PasswordHash == adminTestPassword || !VerifyPassword(adminTestPassword, a.PasswordHash) {
		t.Fatal("stored credential must be a verifier of the password, never the password")
	}
	if ok, err := st.AdminExists(); err != nil || !ok {
		t.Fatalf("AdminExists after create = %v, %v", ok, err)
	}
}
