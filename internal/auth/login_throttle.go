package auth

import (
	"sync"
	"time"
)

// Admin login throttle. Five consecutive failures for one username lock that
// username for 60 seconds; a success clears the streak. Failures are counted
// per username whether or not the account exists, so the lockout itself never
// reveals which usernames are real. State is in memory and per process.
const (
	loginMaxFailures = 5
	loginLockout     = 60 * time.Second
	// loginFailWindow bounds a failure streak: a failure more than this long
	// after the previous one starts a new streak.
	loginFailWindow = 10 * time.Minute
)

// LoginThrottle tracks failed login attempts per username. It is safe for
// concurrent use; the clock is injected so tests can move time.
type LoginThrottle struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*loginState
	swept   time.Time
}

type loginState struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

// NewLoginThrottle returns an empty throttle reading time from now.
func NewLoginThrottle(now func() time.Time) *LoginThrottle {
	return &LoginThrottle{now: now, entries: map[string]*loginState{}}
}

// Reserve counts one attempt for username before its password check runs. It
// returns false, without counting, while username is locked. Otherwise the
// attempt is recorded as a failure, so concurrent attempts cannot all pass the
// lockout check before any of them is counted. A failed attempt keeps its
// reservation; a successful one releases it through Success.
func (t *LoginThrottle) Reserve(username string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if t.lockedAt(username, now) {
		return false
	}
	t.recordFailure(username, now)
	return true
}

// lockedAt reports whether username is inside a lockout at now. Caller holds mu.
func (t *LoginThrottle) lockedAt(username string, now time.Time) bool {
	st := t.live(username, now)
	return st != nil && !st.lockedUntil.IsZero()
}

// recordFailure adds one failure for username at now. Caller holds mu.
func (t *LoginThrottle) recordFailure(username string, now time.Time) {
	t.sweep(now)
	st := t.live(username, now)
	if st == nil {
		st = &loginState{}
		t.entries[username] = st
	}
	st.failures++
	st.lastFailure = now
	if st.failures >= loginMaxFailures {
		st.lockedUntil = now.Add(loginLockout)
	}
}

// Release gives back the failure Reserve counted for an attempt that never
// reached a credential check, such as a store error. The count drops by one,
// and a lockout that attempt started ends with it. Unlike Success it keeps the
// rest of the streak.
func (t *LoginThrottle) Release(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.live(username, t.now())
	if st == nil {
		return
	}
	st.failures--
	if st.failures < loginMaxFailures {
		st.lockedUntil = time.Time{}
	}
	if st.failures <= 0 {
		delete(t.entries, username)
	}
}

// Success clears the failure streak for username.
func (t *LoginThrottle) Success(username string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, username)
}

// live returns username's state while it still counts at now. A lockout that
// has run out, or a streak whose last failure is older than loginFailWindow,
// is dropped here so an expired lockout restarts the count from zero.
func (t *LoginThrottle) live(username string, now time.Time) *loginState {
	st, ok := t.entries[username]
	if !ok {
		return nil
	}
	lockExpired := !st.lockedUntil.IsZero() && !now.Before(st.lockedUntil)
	streakStale := st.lockedUntil.IsZero() && now.Sub(st.lastFailure) > loginFailWindow
	if lockExpired || streakStale {
		delete(t.entries, username)
		return nil
	}
	return st
}

// sweep drops every dead entry at most once per window, so an attacker
// spraying distinct usernames cannot grow the map without bound.
func (t *LoginThrottle) sweep(now time.Time) {
	if now.Sub(t.swept) < loginFailWindow {
		return
	}
	t.swept = now
	for username := range t.entries {
		t.live(username, now)
	}
}
