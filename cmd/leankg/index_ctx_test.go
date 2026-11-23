package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// writeSourceTree writes n small Go files spread over 20 packages under a
// fresh directory and returns it.
func writeSourceTree(t *testing.T, n int) string {
	t.Helper()
	dir := t.TempDir()
	for i := range n {
		pkg := filepath.Join(dir, fmt.Sprintf("pkg%02d", i%20))
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		src := fmt.Sprintf("package p%d\n\nfunc F%d() int { return %d }\n", i%20, i, i)
		if err := os.WriteFile(filepath.Join(pkg, fmt.Sprintf("f%04d.go", i)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A run whose context is already done returns that context's error at its
// first unit of work, writes no elements, and leaves a store that still opens.
func TestRunIndexStopsOnDoneContext(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	cases := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"cancelled", cancelled, context.Canceled},
		{"deadline", expired, context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeSourceTree(t, 200)
			start := time.Now()
			err := runIndex(tc.ctx, "", dir, "", "", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("runIndex error = %v, want %v", err, tc.want)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("runIndex took %s after its context was done", d)
			}
			// A run cancelled before the store opens leaves no file (the open is
			// ctx-aware, CX-27): nothing was written, so there is nothing to open.
			if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); os.IsNotExist(statErr) {
				return
			}
			st, err := openStore(context.Background(), dir, "", store.RO)
			if err != nil {
				t.Fatalf("store does not open after a stopped run: %v", err)
			}
			defer st.Close()
			// A run cancelled after the open but before its first store call
			// stops at the migration (CX-19 ctx-aware Migrate), so the store may
			// have no schema yet. Nothing was indexed either way.
			n, err := st.ElementCount(context.Background())
			if err != nil && !strings.Contains(err.Error(), "no such table") {
				t.Fatalf("element count after a stopped run: %v", err)
			}
			if err == nil && n != 0 {
				t.Fatalf("elements after a run stopped before its first file = %d, want 0", n)
			}
		})
	}
}

// The hook budget is the flag, else LEANKG_HOOK_BUDGET, else 10 s (plan D5). A
// missing, zero or unparsable environment value falls back to the default, as
// the hook's other limits do.
func TestHookBudgetResolution(t *testing.T) {
	cases := []struct {
		name string
		env  string
		flag time.Duration
		want time.Duration
	}{
		{"default", "", 0, 10 * time.Second},
		{"environment", "3s", 0, 3 * time.Second},
		{"flag beats environment", "3s", 4 * time.Second, 4 * time.Second},
		{"unparsable environment falls back", "soon", 0, 10 * time.Second},
		{"zero environment falls back", "0s", 0, 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LEANKG_HOOK_BUDGET", tc.env)
			if got := hookBudget(tc.flag); got != tc.want {
				t.Fatalf("hookBudget(%s) = %s, want %s", tc.flag, got, tc.want)
			}
		})
	}
}

// index --hook past its budget returns (exit 0) promptly: the budget expires
// before the first directory ends, the run pauses after it, and the call does
// not block.
func TestIndexHookStopsWhenBudgetExpires(t *testing.T) {
	dir := writeSourceTree(t, 2000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cmdIndex(ctx, []string{"--hook", "--hook-budget", "1ns", "--project", dir, dir})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		// Join the command before failing: a cmdIndex still running when the
		// test returns prints into the next test's captured stdout.
		cancel()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
		}
		t.Fatal("index --hook did not return after its budget expired")
	}
}

// storedFileCount is the number of file records in dir's store (0 before the
// first run has created it).
func storedFileCount(t *testing.T, dir string) int {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); os.IsNotExist(err) {
		return 0
	}
	st, err := openStore(context.Background(), dir, "", store.RO)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n, err := st.FileCount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// index --hook under a budget one directory cannot outlast: each run keeps the
// directories it finished, prints the paused line and exits 0, and the store
// reaches the full tree one directory per run. A run after that has nothing to do.
func TestIndexHookBudgetConvergesAcrossRuns(t *testing.T) {
	const dirs, perDir = 20, 10
	dir := writeSourceTree(t, dirs*perDir) // 20 packages of 10 files each
	for run := 1; run <= dirs+1; run++ {
		var err error
		out := captureStdout(t, func() {
			err = cmdIndex(context.Background(), []string{"--hook", "--hook-budget", "1ns", "--no-embed", dir})
		})
		if err != nil {
			t.Fatalf("run %d: %v\n%s", run, err, out)
		}
		if got, want := storedFileCount(t, dir), perDir*min(run, dirs); got != want {
			t.Fatalf("run %d stored %d files, want %d\n%s", run, got, want, out)
		}
		if paused := strings.Contains(out, "paused"); paused != (run < dirs) {
			t.Fatalf("run %d paused = %v, want %v\n%s", run, paused, run < dirs, out)
		}
	}
}
