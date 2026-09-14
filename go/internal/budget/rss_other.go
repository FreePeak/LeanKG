//go:build !darwin && !linux

package budget

import "errors"

// CurrentRSSMb is unavailable on this platform. Returning an error makes the
// RSS half of BudgetGuard inert (Rust: `Ok(0)` on unsupported targets, which
// the caller reads as "no RSS data, can't enforce").
func CurrentRSSMb() (uint64, error) {
	return 0, errors.New("budget: RSS probe unsupported on this platform")
}
