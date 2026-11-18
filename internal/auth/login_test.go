package auth

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// A wrong password and an unknown account must be indistinguishable to the
// caller: same error value and text, and on the REST route the same status and
// body.
func TestVerifyLoginRejectionsAreIndistinguishable(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "uniform@example.com", adminTestPassword, false)

	_, wrongErr := VerifyLogin(context.Background(), st, "uniform@example.com", "wrong password!")
	_, unknownErr := VerifyLogin(context.Background(), st, "nobody-uniform@example.com", adminTestPassword)
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
			if _, err := verifyLogin(context.Background(), throttle, st, user, "wrong password!", false); err == nil {
				t.Fatalf("%s: wrong password must fail", user)
			}
		}
		_, err := verifyLogin(context.Background(), throttle, st, user, adminTestPassword, false)
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
		_, _ = verifyLogin(context.Background(), throttle, st, "streak@example.com", "wrong password!", false)
	}
	if _, err := verifyLogin(context.Background(), throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("login before the threshold: %v", err)
	}
	for i := 0; i < loginMaxFailures-1; i++ {
		_, _ = verifyLogin(context.Background(), throttle, st, "streak@example.com", "wrong password!", false)
	}
	if _, err := verifyLogin(context.Background(), throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("a success must reset the streak: %v", err)
	}

	for i := 0; i < loginMaxFailures; i++ {
		_, _ = verifyLogin(context.Background(), throttle, st, "streak@example.com", "wrong password!", false)
	}
	clock.Advance(loginLockout)
	if _, err := verifyLogin(context.Background(), throttle, st, "streak@example.com", adminTestPassword, false); err != nil {
		t.Fatalf("login after the lockout expires on the injected clock: %v", err)
	}
}

// setAccountStatus flips an existing account's status in place, the way a
// disable (or re-enable) would.
func setAccountStatus(t *testing.T, st store.Backend, email, status string) {
	t.Helper()
	a, found, err := st.AccountByEmail(context.Background(), email)
	if err != nil || !found {
		t.Fatalf("account %s: found=%v err=%v", email, found, err)
	}
	a.Status = status
	if err := st.AccountUpsert(context.Background(), a); err != nil {
		t.Fatalf("set status %s: %v", status, err)
	}
}

// A disabled account is refused on the REST login even with the right
// password, with the same generic error a wrong password gets.
func TestVerifyLoginRejectsDisabledAccountWithCorrectPassword(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "disabled-rest@example.com", adminTestPassword, false)
	setAccountStatus(t, st, "disabled-rest@example.com", "disabled")

	throttle, _ := newTestThrottle()
	account, err := verifyLogin(context.Background(), throttle, st, "disabled-rest@example.com", adminTestPassword, false)
	if !errors.Is(err, ErrInvalidAdminLogin) || errors.Is(err, ErrAdminLoginRateLimited) {
		t.Fatalf("disabled account err = %v, want the generic ErrInvalidAdminLogin", err)
	}
	if account.ID != "" {
		t.Fatalf("disabled account returned an account: %+v", account)
	}
}

func TestVerifyLoginDisabledRejectionMatchesWrongPassword(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "disabled-same@example.com", adminTestPassword, false)
	seedAccount(t, st, "active-same@example.com", adminTestPassword, false)
	setAccountStatus(t, st, "disabled-same@example.com", "disabled")

	_, disabledErr := VerifyLogin(context.Background(), st, "disabled-same@example.com", adminTestPassword)
	_, wrongErr := VerifyLogin(context.Background(), st, "active-same@example.com", "wrong password!")
	if disabledErr == nil || wrongErr == nil {
		t.Fatalf("both must fail: disabled = %v, wrong = %v", disabledErr, wrongErr)
	}
	if disabledErr.Error() != wrongErr.Error() {
		t.Fatalf("disabled %q differs from wrong password %q", disabledErr, wrongErr)
	}
}

// Correct-password attempts against a disabled account count as failures, so
// they feed the same lockout as wrong passwords.
func TestVerifyLoginDisabledAttemptsCountTowardThrottle(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "disabled-throttle@example.com", adminTestPassword, false)
	setAccountStatus(t, st, "disabled-throttle@example.com", "disabled")

	throttle, _ := newTestThrottle()
	for i := 0; i < loginMaxFailures; i++ {
		if _, err := verifyLogin(context.Background(), throttle, st, "disabled-throttle@example.com", adminTestPassword, false); err == nil {
			t.Fatal("disabled account must not log in")
		}
	}
	_, err := verifyLogin(context.Background(), throttle, st, "disabled-throttle@example.com", adminTestPassword, false)
	if !errors.Is(err, ErrAdminLoginRateLimited) {
		t.Fatalf("after %d disabled attempts err = %v, want the rate-limit marker", loginMaxFailures, err)
	}
}

// Re-enabling the account restores the REST login.
func TestVerifyLoginReenabledAccountLogsInAgain(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "reenabled@example.com", adminTestPassword, false)
	setAccountStatus(t, st, "reenabled@example.com", "disabled")
	if _, err := VerifyLogin(context.Background(), st, "reenabled@example.com", adminTestPassword); err == nil {
		t.Fatal("disabled account must not log in")
	}
	setAccountStatus(t, st, "reenabled@example.com", "active")
	if _, err := VerifyLogin(context.Background(), st, "reenabled@example.com", adminTestPassword); err != nil {
		t.Fatalf("re-enabled account login: %v", err)
	}
}

func TestLoginRouteAnswersDisabledAccountLikeWrongPassword(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "disabled-route@example.com", adminTestPassword, false)
	seedAccount(t, st, "wrong-route@example.com", adminTestPassword, false)
	setAccountStatus(t, st, "disabled-route@example.com", "disabled")
	h := newAuthServer(t, st)

	disabledCode, disabledBody := doJSON(t, h, "POST", "/api/v1/auth/login", "", map[string]string{
		"email": "disabled-route@example.com", "password": adminTestPassword})
	wrongCode, wrongBody := doJSON(t, h, "POST", "/api/v1/auth/login", "", map[string]string{
		"email": "wrong-route@example.com", "password": "wrong password!"})
	if disabledCode != wrongCode {
		t.Fatalf("status: disabled %d, wrong password %d", disabledCode, wrongCode)
	}
	if !reflect.DeepEqual(disabledBody, wrongBody) {
		t.Fatalf("body differs: disabled %v, wrong password %v", disabledBody, wrongBody)
	}
}

// countingStore counts AccountByEmail calls. A login reaches its password check
// only after that lookup, so the count is the number of attempts that got to
// PBKDF2.
type countingStore struct {
	Store
	lookups atomic.Int64
}

func (c *countingStore) AccountByEmail(ctx context.Context, email string) (store.Account, bool, error) {
	c.lookups.Add(1)
	return c.Store.AccountByEmail(ctx, email)
}

// Concurrent wrong-password logins for one account must not all reach the
// password check. The throttle reserves each attempt before PBKDF2 runs, so at
// most loginMaxFailures of them do.
func TestVerifyLoginConcurrentAttemptsReachVerifierAtMostLimit(t *testing.T) {
	st := openAuthStore(t)
	seedAccount(t, st, "burst@example.com", adminTestPassword, false)
	counted := &countingStore{Store: st}

	throttle, _ := newTestThrottle()
	const attempts = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = verifyLogin(context.Background(), throttle, counted, "burst@example.com", "wrong password!", false)
		}()
	}
	close(start)
	wg.Wait()

	if got := counted.lookups.Load(); got > loginMaxFailures {
		t.Fatalf("%d of %d concurrent attempts reached the password check, want at most %d", got, attempts, loginMaxFailures)
	}
	for i, err := range results {
		if !errors.Is(err, ErrInvalidAdminLogin) {
			t.Fatalf("attempt %d err = %v, want ErrInvalidAdminLogin", i, err)
		}
	}
}
