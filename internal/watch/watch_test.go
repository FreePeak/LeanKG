package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

func openStore(t *testing.T) store.Backend {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func await(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

type eventLog struct {
	mu  sync.Mutex
	ks  map[string]bool
	ch  <-chan WatchEvent
	end chan struct{}
}

func newEventLog(w *Watcher) *eventLog {
	el := &eventLog{ks: map[string]bool{}, ch: w.Events(), end: make(chan struct{})}
	go func() {
		defer close(el.end)
		for ev := range el.ch {
			el.mu.Lock()
			el.ks[ev.Kind] = true
			el.mu.Unlock()
		}
	}()
	return el
}

func (el *eventLog) saw(kind string) bool {
	el.mu.Lock()
	defer el.mu.Unlock()
	return el.ks[kind]
}

// TestWatchIndexOnWriteAndRemove covers the end-to-end trigger flow: write a
// .go file -> element queryable; delete it -> gone; Events channel saw
// create+remove.
func TestWatchIndexOnWriteAndRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	name := "WatchedFunc"

	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	el := newEventLog(w)

	file := filepath.Join(proj, "sample.go")
	if err := os.WriteFile(file, []byte("package sample\n\nfunc "+name+"() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact(name)
		return err == nil && len(els) > 0
	})

	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact(name)
		return err == nil && len(els) == 0
	})

	await(t, time.Second, func() bool { return el.saw("create") && el.saw("remove") })
}

// TestSecondStartErrAlreadyWatching pins the single-flight lock: a second
// Start on the same root fails with ErrAlreadyWatching; after ctx cancel
// (loop exit releases the lock), a new Start on the same root succeeds.
func TestSecondStartErrAlreadyWatching(t *testing.T) {
	st := openStore(t)
	proj := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	w, err := Start(ctx, st, proj, Options{Debounce: time.Second})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer w.Stop()

	_, err = Start(context.Background(), st, proj, Options{Debounce: time.Second})
	if !errors.Is(err, ErrAlreadyWatching) {
		t.Fatalf("second Start: want ErrAlreadyWatching, got %v", err)
	}

	cancel()
	<-w.Done() // loop exit => stopOnce => flock released

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	w2, err := Start(ctx2, st, proj, Options{Debounce: time.Second})
	if err != nil {
		t.Fatalf("Start after cancel: %v", err)
	}
	w2.Stop()
}

// TestSkipDirsNotWatched pins the index-identical skip list at the WATCHER
// level: node_modules contents never produce events (IndexDir would also
// skip them — here the whole subtree is outside the notifier), while
// normal nested dirs are watched.
func TestSkipDirsNotWatched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	for _, d := range []string{"node_modules/pkg", "src/deep"} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	// Inside node_modules: no event must ever surface, so no re-index runs.
	nm := filepath.Join(proj, "node_modules/pkg/skip.go")
	if err := os.WriteFile(nm, []byte("package pkg\n\nfunc SkipMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inside a watched dir: the write triggers the flow.
	if err := os.WriteFile(filepath.Join(proj, "src/deep/keep.go"), []byte("package deep\n\nfunc KeepMe() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact("KeepMe")
		return err == nil && len(els) > 0
	})
	// Give any (wrong) node_modules-triggered flush a chance to land, then
	// assert the skip actually held.
	time.Sleep(300 * time.Millisecond)
	if els, _ := st.FindExact("SkipMe"); len(els) > 0 {
		t.Error("node_modules file was indexed: skip list failed at watcher level")
	}
}

// TestEventsChannelDropOnOverflow pins the never-blocks contract: with no
// reader, 64+ events must not stall the loop.
func TestEventsChannelDropOnOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	for i := 0; i < 128; i++ {
		if err := os.WriteFile(filepath.Join(proj, "many.go"),
			[]byte("package p\n\nfunc Many() int { return "+string(rune('0'+i%10))+" }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// If the loop blocked on a full channel, this re-index would never run.
	await(t, 3*time.Second, func() bool {
		els, err := st.FindExact("Many")
		return err == nil && len(els) > 0
	})
}

// --- periodic reconciliation (issue #279 part 5) -----------------------------

func TestReconcileIntervalFromEnv(t *testing.T) {
	t.Run("unset means default", func(t *testing.T) {
		got, err := ReconcileIntervalFromEnv()
		if err != nil || got != DefaultReconcileInterval {
			t.Fatalf("ReconcileIntervalFromEnv() = %v, %v; want %v", got, err, DefaultReconcileInterval)
		}
	})
	t.Run("seconds honored", func(t *testing.T) {
		t.Setenv("LEANKG_WATCH_RECONCILE_SECS", "90")
		got, err := ReconcileIntervalFromEnv()
		if err != nil || got != 90*time.Second {
			t.Fatalf("ReconcileIntervalFromEnv() = %v, %v; want 90s", got, err)
		}
	})
	t.Run("zero and words disable", func(t *testing.T) {
		for _, v := range []string{"0", "off", "disabled"} {
			t.Setenv("LEANKG_WATCH_RECONCILE_SECS", v)
			got, err := ReconcileIntervalFromEnv()
			if err != nil || got != 0 {
				t.Fatalf("ReconcileIntervalFromEnv(%q) = %v, %v; want disabled", v, got, err)
			}
		}
	})
	t.Run("floor", func(t *testing.T) {
		t.Setenv("LEANKG_WATCH_RECONCILE_SECS", "1")
		got, err := ReconcileIntervalFromEnv()
		if err != nil || got != time.Second {
			t.Fatalf("ReconcileIntervalFromEnv() = %v, %v; want 1s", got, err)
		}
	})
	t.Run("negative seconds are an error", func(t *testing.T) {
		// Disablement is spelled 0/off/disabled; a negative value is a typo and
		// must be reported rather than silently turned into "off".
		t.Setenv("LEANKG_WATCH_RECONCILE_SECS", "-5")
		if _, err := ReconcileIntervalFromEnv(); err == nil {
			t.Fatal("negative interval must be refused")
		}
	})
	t.Run("garbage is an error", func(t *testing.T) {
		t.Setenv("LEANKG_WATCH_RECONCILE_SECS", "soon")
		if _, err := ReconcileIntervalFromEnv(); err == nil {
			t.Fatal("unparsable interval must error, not silently default")
		}
	})
}

func TestResolveReconcilePrecedence(t *testing.T) {
	// Options value positive wins over the environment.
	t.Setenv("LEANKG_WATCH_RECONCILE_SECS", "600")
	if got := mustResolve(t, 30*time.Second); got != 30*time.Second {
		t.Fatalf("option wins = %v, want 30s", got)
	}
	// Negative option disables outright.
	if got := mustResolve(t, -1); got != 0 {
		t.Fatalf("negative option = %v, want disabled", got)
	}
	// Zero defers to the env / default.
	if got := mustResolve(t, 0); got != 600*time.Second {
		t.Fatalf("env fallback = %v, want 600s", got)
	}
}

func mustResolve(t *testing.T, opt time.Duration) time.Duration {
	t.Helper()
	d, err := resolveReconcile(opt)
	if err != nil {
		t.Fatalf("resolveReconcile(%v): %v", opt, err)
	}
	return d
}

// TestReconcilePassRecoversMissedEvent is the watcher-miss insurance contract:
// a file written WITHOUT producing a (delivered) fsnotify event — simulated by
// writing while the watcher is stopped from delivering, i.e. outside the
// notifier's watch set via an unwatched directory created after Start... — is
// simpler here to pin via the pass itself: with Reconcile set to 1s, a file
// created in a NEW subdirectory (whose directory-add event may be missed) still
// becomes queryable within one reconcile interval, and the Events channel
// reports one kindReconcile pass.
func TestReconcilePassRecoversMissedEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()

	var reconciles int
	w, err := Start(ctx, st, proj, Options{
		Debounce:  100 * time.Millisecond,
		Reconcile: time.Second,
		OnEvent: func(path, kind string) {
			if kind == "reconcile" {
				reconciles++
			}
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	el := newEventLog(w)

	// A nested directory created after Start: even if the notifier missed the
	// directory-create event (the miss this pass insures), the reconciler
	// re-adds directories and re-runs the incremental index.
	sub := filepath.Join(proj, "nested")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sub, "reconciled.go")
	if err := os.WriteFile(file, []byte("package nested\n\nfunc MissedFunc() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	await(t, 8*time.Second, func() bool {
		els, err := st.FindExact("MissedFunc")
		return err == nil && len(els) > 0
	})
	await(t, 8*time.Second, func() bool { return el.saw("reconcile") && reconciles >= 1 })
}

// The writer flush bumps the watermark (index.IndexDirWith) and MUST
// refresh the inventory snapshot — the contract every other writer path
// holds. Without it, a project under active watching reads possibly_stale
// forever (found on this repo's own fleet leg with a live writer daemon).
func TestFlushRefreshesInventorySnapshot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond, Reconcile: -1})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package a\n\nfunc Snap() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 8*time.Second, func() bool {
		n, err := st.ElementCount()
		if err != nil || n == 0 {
			return false
		}
		return store.Freshness(st, n) == store.FreshnessFresh
	})
}

// TestReconcileDisabledByOption pins that a negative Options.Reconcile really
// turns the pass off: the loop runs long enough for several debounce ticks
// with no reconcile event emitted.
func TestReconcileDisabledByOption(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)
	proj := t.TempDir()
	w, err := Start(ctx, st, proj, Options{Debounce: 100 * time.Millisecond, Reconcile: -1})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()
	el := newEventLog(w)

	// Generate ordinary activity so the loop demonstrably runs.
	if err := os.WriteFile(filepath.Join(proj, "activity.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	await(t, 3*time.Second, func() bool { return el.saw("create") })

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !el.saw("reconcile") {
		time.Sleep(50 * time.Millisecond)
	}
	if el.saw("reconcile") {
		t.Fatal("reconcile pass ran though Options.Reconcile < 0 disables it")
	}
}
