// Package flock takes exclusive advisory locks on lock files: the single-flight
// guard for commands that must not run twice against one store. The lock is an
// flock(2) on an open file, so the kernel releases it when the holder exits,
// however it exits. Like the watch and embed locks it uses syscall.Flock, which
// builds on the linux and darwin release targets only.
package flock

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ErrHeld is returned by TryLock when another holder has the lock.
var ErrHeld = errors.New("flock: lock is held")

// TryLock takes an exclusive lock on path without waiting, creating the file if
// it is missing. It returns ErrHeld when another holder has the lock. The
// returned func releases the lock.
func TryLock(path string) (func(), error) {
	return lock(path, syscall.LOCK_EX|syscall.LOCK_NB)
}

// Lock takes an exclusive lock on path, waiting for any holder to release it.
// The returned func releases the lock.
func Lock(path string) (func(), error) {
	return lock(path, syscall.LOCK_EX)
}

func lock(path string, how int) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("flock: open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if how&syscall.LOCK_NB != 0 && errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, fmt.Errorf("flock: %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
