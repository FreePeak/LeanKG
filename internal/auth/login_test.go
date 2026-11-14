package auth

import (
	"errors"
	"reflect"
	"testing"
)

// A wrong password and an unknown account must be indistinguishable to the
// caller: same error value and text, and on the REST route the same status and
// body.
func TestVerifyLoginRejectionsAreIndistinguishable(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "uniform@example.com", adminTestPassword, false)

	_, wrongErr := VerifyLogin(st, "uniform@example.com", "wrong password!")
	_, unknownErr := VerifyLogin(st, "nobody-uniform@example.com", adminTestPassword)
	if !errors.Is(wrongErr, ErrInvalidAdminLogin) || !errors.Is(unknownErr, ErrInvalidAdminLogin) {
		t.Fatalf("wrong = %v, unknown = %v; both must be the generic login error", wrongErr, unknownErr)
	}
	if wrongErr.Error() != unknownErr.Error() {
		t.Fatalf("error text differs: wrong %q, unknown %q", wrongErr, unknownErr)
	}
}

func TestLoginRouteAnswersWrongPasswordAndUnknownAccountAlike(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "route-user@example.com", adminTestPassword, false)
	h := newAuthServer(t, st)

	wrongCode, wrongBody := doJSON(t, h, "POST", "/api/v1/auth/login", "", map[string]string{
		"email": "route-user@example.com", "password": "wrong password!"})
	unknownCode, unknownBody := doJSON(t, h, "POST", "/api/v1/auth/login", "", map[string]string{
		"email": "route-ghost@example.com", "password": adminTestPassword})
	if wrongCode != unknownCode {
		t.Fatalf("status: wrong %d, unknown %d", wrongCode, unknownCode)
	}
	if !reflect.DeepEqual(wrongBody, unknownBody) {
		t.Fatalf("body differs: wrong %v, unknown %v", wrongBody, unknownBody)
	}
}

// The REST login is throttled with the same per-username backoff as the admin
// login. A lockout counts failures for unknown usernames too, so it never tells
// a caller which usernames exist.
func TestVerifyLoginLocksAfterFiveFailuresEvenForUnknownUsername(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "backoff@example.com", adminTestPassword, false)

	throttle, _ := newTestThrottle()
	for _, user := range []string{"backoff@example.com", "ghost-backoff@example.com"} {
		for i := 0; i < loginMaxFailures; i++ {
			if _, err := verifyLogin(throttle, st, user, "wrong password!", false); err == nil {
				t.Fatalf("%s: wrong password must fail", user)
			}
		}
		_, err := verifyLogin(throttle, st, user, adminTestPassword, false)
		if !errors.Is(err, ErrAdminLoginRateLimited) {
			t.Fatalf("%s: locked login err = %v, want the rate-limit marker", user, err)
		}
	}
}

func TestVerifyLoginSuccessResetsStreakAndLockoutExpires(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "streak@example.com", adminTestPassword, false)

	throttle, clock := newTestThrottle()
	for i := 0; i < loginMaxFailures-1; i++ {
		_, _ = verifyLogin(throttle, st, "streak@example.com", "wrong password!", false)
	}
	if _, err := verifyLogin(throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("login before the threshold: %v", err)
	}
	for i := 0; i < loginMaxFailures-1; i++ {
		_, _ = verifyLogin(throttle, st, "streak@example.com", "wrong password!", false)
	}
	if _, err := verifyLogin(throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("a success must reset the streak: %v", err)
	}

	for i := 0; i < loginMaxFailures; i++ {
		_, _ = verifyLogin(throttle, st, "streak@example.com", "wrong password!", false)
	}
	clock.Advance(loginLockout)
	if _, err := verifyLogin(throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("login after the lockout expires on the injected clock: %v", err)
	}
}
