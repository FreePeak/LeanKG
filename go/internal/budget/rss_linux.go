//go:build linux

package budget

import "syscall"

// CurrentRSSMb returns the process's peak resident set size in MiB.
//
// Mirror of rss_darwin.go: getrusage(RUSAGE_SELF).Maxrss reports peak RSS in
// kilobytes on Linux (the Rust reference reads /proc/self/statm instead; the
// budget only needs a conservative over-approximation, and peak >= current).
func CurrentRSSMb() (uint64, error) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, err
	}
	if ru.Maxrss <= 0 {
		return 0, nil
	}
	return uint64(ru.Maxrss) * 1024 / (1024 * 1024), nil
}
