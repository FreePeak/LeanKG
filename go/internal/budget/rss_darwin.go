//go:build darwin

package budget

import "syscall"

// CurrentRSSMb returns the process's peak resident set size in MiB.
//
// The Rust reference reads *current* RSS via proc_pidinfo(PROC_PIDTASKINFO).
// Go's stdlib has no such call; getrusage(RUSAGE_SELF).Maxrss is peak RSS on
// Darwin and is reported in bytes here, so it is a faithful (if monotone)
// proxy: the budget only needs "have we blown past the cap".
func CurrentRSSMb() (uint64, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, err
	}
	if ru.Maxrss <= 0 {
		return 0, nil
	}
	return uint64(ru.Maxrss) / (1024 * 1024), nil
}
