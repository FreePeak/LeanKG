package auth

import (
	"sync"
	"testing"
	"time"
)

// fakeClock is the injectable clock the throttle reads; tests move it forward
// instead of sleeping.
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestThrottle() (*LoginThrottle, *fakeClock) {
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	return NewLoginThrottle(clock.Now), clock
}

func failN(th *LoginThrottle, user string, n int) {
	for i := 0; i < n; i++ {
		th.Failure(user)
	}
}

func TestLoginThrottleLocksAfterFiveFailures(t *testing.T) {
	th, _ := newTestThrottle()
	failN(th, "admin", 4)
	if th.Blocked("admin") {
		t.Fatal("4 failures must not lock the username")
	}
	th.Failure("admin")
	if !th.Blocked("admin") {
		t.Fatal("5th consecutive failure must lock the username")
	}
	if th.Blocked("someone-else") {
		t.Fatal("a lockout must be per username")
	}
}

func TestLoginThrottleLockoutLastsSixtySeconds(t *testing.T) {
	th, clock := newTestThrottle()
	failN(th, "admin", 5)

	clock.Advance(59 * time.Second)
	if !th.Blocked("admin") {
		t.Fatal("lockout must still hold at 59s")
	}
	clock.Advance(time.Second)
	if th.Blocked("admin") {
		t.Fatal("lockout must end at 60s")
	}
}

func TestLoginThrottleOneFailureAfterLockoutDoesNotRelock(t *testing.T) {
	th, clock := newTestThrottle()
	failN(th, "admin", 5)
	clock.Advance(60 * time.Second)

	th.Failure("admin")
	if th.Blocked("admin") {
		t.Fatal("after a lockout expires the streak restarts: one failure must not relock")
	}
}

func TestLoginThrottleSuccessResetsStreak(t *testing.T) {
	th, _ := newTestThrottle()
	failN(th, "admin", 4)
	th.Success("admin")
	failN(th, "admin", 4)
	if th.Blocked("admin") {
		t.Fatal("a success must reset the consecutive-failure count")
	}
}

func TestLoginThrottleStreakExpiresOutsideWindow(t *testing.T) {
	th, clock := newTestThrottle()
	failN(th, "admin", 4)
	clock.Advance(loginFailWindow + time.Second)
	failN(th, "admin", 4)
	if th.Blocked("admin") {
		t.Fatal("failures spread beyond the window must not add up to a lockout")
	}
}

func TestLoginThrottleSweepsStaleEntries(t *testing.T) {
	th, clock := newTestThrottle()
	for i := 0; i < 100; i++ {
		th.Failure(string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	clock.Advance(loginFailWindow + time.Second)
	th.Failure("fresh")
	if n := len(th.entries); n != 1 {
		t.Fatalf("stale entries must be swept: %d left, want 1", n)
	}
}

func TestLoginThrottleConcurrentUse(t *testing.T) {
	th, _ := newTestThrottle()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			th.Failure("admin")
			_ = th.Blocked("admin")
		}()
	}
	wg.Wait()
	if !th.Blocked("admin") {
		t.Fatal("50 concurrent failures must lock the username")
	}
}
