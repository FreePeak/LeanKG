package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/langs"
)

// hangingSpec runs the shared fake in hang mode: it records its pid in a file
// and then never answers the handshake. A background child of the server is
// recorded too, so a test can check that no process of the spawn survives.
func hangingSpec(t *testing.T) (spec *langs.LSPSpec, pidFile, childFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "server.pid")
	childFile = filepath.Join(dir, "child.pid")
	t.Setenv("HANG_PID_FILE", pidFile)
	t.Setenv("HANG_CHILD_FILE", childFile)
	return fakeSpec(t, "hang"), pidFile, childFile
}

// readPID polls a pid file the hanging server writes, so a test knows the
// spawn is in flight before it cancels.
func readPID(path string) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, errors.New("pid file " + path + " never written")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertReaped fails unless pid is gone. kill(pid, 0) reports ESRCH once the
// process has been waited for; an orphan reparented to init may linger as a
// zombie for a moment, so the check polls briefly.
func assertReaped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive after the spawn was cancelled", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestStartCommandCancelReapsServer: a cancelled ctx stops a spawn that is
// waiting for its handshake, returns promptly with the cancellation, and leaves
// neither the server nor its child running.
func TestStartCommandCancelReapsServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake LSP server is a POSIX sh script")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "server.pid")
	childFile := filepath.Join(dir, "child.pid")
	args := []string{"-c", "echo $$ > " + pidFile + "; sleep 60 & echo $! > " + childFile + "; wait"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type spawned struct{ server, child int }
	ready := make(chan spawned, 1)
	go func() {
		defer cancel()
		server, err1 := readPID(pidFile)
		child, err2 := readPID(childFile)
		if err1 != nil || err2 != nil {
			ready <- spawned{}
			return
		}
		ready <- spawned{server, child}
	}()

	start := time.Now()
	_, err := StartCommand(ctx, langs.Go, "/bin/sh", args, dir, nil)
	elapsed := time.Since(start)

	got := <-ready
	if got.server == 0 {
		t.Fatal("the server never started, so the cancellation was not exercised")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartCommand error = %v, want context.Canceled", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("StartCommand took %v after cancel; the spawn ignored ctx (handshake timeout is %v)", elapsed, handshakeTimeout)
	}
	assertReaped(t, got.server)
	assertReaped(t, got.child)
}

// TestManagerSpawnHoldsNoLock: a server that is still in its handshake must not
// block the manager for other callers, and cancelling that Get returns the
// cancellation.
func TestManagerSpawnHoldsNoLock(t *testing.T) {
	spec, pidFile, _ := hangingSpec(t)
	m := NewManager(0)
	defer m.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.Get(ctx, langs.Go, spec, t.TempDir())
		done <- err
	}()
	if _, err := readPID(pidFile); err != nil {
		cancel()
		select {
		case spawnErr := <-done:
			t.Fatalf("spawn never started: %v (Get returned %v)", err, spawnErr)
		case <-time.After(2 * time.Second):
			t.Fatalf("spawn never started: %v", err)
		}
	}

	lenDone := make(chan int, 1)
	go func() { lenDone <- m.Len() }()
	select {
	case <-lenDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Len blocked while a spawn was in flight: the manager lock is held across the spawn")
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
}
