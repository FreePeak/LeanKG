// Package watch implements the fsnotify-based recursive watcher with
// debounced re-index (W2 writer role).
//
// The watcher is a trigger, not an indexer: every debounced flush runs
// index.IndexDir(ctx, st, root) once — IndexDir already 3-signal-skips
// unchanged files and handles deletions.
//
// Event delivery is best-effort (fsnotify drops events under load, and a
// missed directory-create event leaves that subtree unwatched), so the loop
// also runs a periodic full-tree reconciliation pass: re-add any directories
// the notifier lost, then run the same incremental index pass. Unchanged
// files cost only a stat there, so the pass is cheap insurance (issue #279
// "watcher-miss insurance").
//
// Integration: `leankg writer` (wired by cmd) = index once + Start; pairs
// with `leankg serve --read-only`.
package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// ErrAlreadyWatching is returned by Start when the single-flight lock for
// the project root is held (a second watcher on the same project).
var ErrAlreadyWatching = errors.New("watcher already active for this project")

// skipDirs mirrors internal/index.skipDirs; any dot-prefixed directory is
// skipped as well.
var skipDirs = map[string]bool{
	".git": true, "target": true, "node_modules": true, "vendor": true,
	".leankg": true, "dist": true, "build": true, ".worktrees": true,
	".worktree": true,
}

const defaultDebounce = 500 * time.Millisecond

// DefaultReconcileInterval is the periodic full-tree reconciliation cadence
// when neither Options.Reconcile nor LEANKG_WATCH_RECONCILE_SECS says
// otherwise, and minReconcileInterval floors the resolved value so a tiny
// configuration cannot turn the loop into a busy re-walk.
const (
	DefaultReconcileInterval = 5 * time.Minute
	minReconcileInterval     = time.Second
)

// kindReconcile is the synthetic event kind reported for a reconciliation
// pass (the reference probes use the same reason split: "watch" | "reconcile").
const kindReconcile = "reconcile"

// ReconcileIntervalFromEnv resolves LEANKG_WATCH_RECONCILE_SECS: unset means
// DefaultReconcileInterval; "0" (or "off"/"disabled") disables the pass; any
// other value must parse as a positive number of seconds. An unparsable value
// is an error rather than a silent default — a typo must not disable
// watcher-miss insurance without saying so.
func ReconcileIntervalFromEnv() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv("LEANKG_WATCH_RECONCILE_SECS"))
	switch strings.ToLower(raw) {
	case "":
		return DefaultReconcileInterval, nil
	case "0", "off", "disabled", "none":
		return 0, nil
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs < 0 {
		return 0, fmt.Errorf("watch: invalid LEANKG_WATCH_RECONCILE_SECS %q (want seconds, 0 to disable)", raw)
	}
	if secs == 0 {
		return 0, nil
	}
	return clampReconcile(time.Duration(secs) * time.Second), nil
}

// resolveReconcile applies the documented precedence: a negative Options
// value disables the pass, a positive one wins over the environment, and zero
// defers to LEANKG_WATCH_RECONCILE_SECS / DefaultReconcileInterval.
func resolveReconcile(opt time.Duration) (time.Duration, error) {
	if opt < 0 {
		return 0, nil
	}
	if opt > 0 {
		return clampReconcile(opt), nil
	}
	return ReconcileIntervalFromEnv()
}

// clampReconcile floors a positive interval at minReconcileInterval.
func clampReconcile(d time.Duration) time.Duration {
	if d < minReconcileInterval {
		return minReconcileInterval
	}
	return d
}

// Options configures Start.
type Options struct {
	// Registry, when non-nil, scopes re-indexed languages (lazy activation).
	Registry *langs.Registry
	// Debounce coalesces event paths into one IndexDir run per flush.
	// Defaults to 500ms when zero or negative.
	Debounce time.Duration
	// Reconcile is the periodic full-tree reconciliation interval. Precedence:
	// this field when positive; otherwise LEANKG_WATCH_RECONCILE_SECS;
	// otherwise DefaultReconcileInterval. A negative value disables the pass
	// outright (the watcher then relies purely on fsnotify events).
	Reconcile time.Duration
	// OnEvent is called per raw event before coalescing, and once per
	// reconciliation pass with kind "reconcile". Errors are ignored.
	OnEvent func(path, kind string) // kind: create|write|remove|rename|reconcile
}

// WatchEvent is one raw fsnotify event surfaced on the Events channel.
type WatchEvent struct {
	Path string
	Kind string
}

// Watcher is the running watcher returned by Start; Cancel via its Stop or
// the Start ctx.
type Watcher struct {
	events       chan WatchEvent
	stopOnce     sync.Once
	stopInternal func()
	done         chan struct{}
}

// Events returns the introspection channel (buffered 64, drops on overflow —
// never blocks the event loop). For tests and introspection only.
func (w *Watcher) Events() <-chan WatchEvent { return w.events }

// Done is closed when the watcher loop has fully stopped (lock released).
func (w *Watcher) Done() <-chan struct{} { return w.done }

// Stop stops the watcher cleanly (idempotent) and waits for the loop to exit.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		w.stopInternal()
	})
	<-w.done
}

func kindOf(op fsnotify.Op) string {
	switch op {
	case fsnotify.Create:
		return "create"
	case fsnotify.Write:
		return "write"
	case fsnotify.Remove:
		return "remove"
	case fsnotify.Rename:
		return "rename"
	default:
		return "other"
	}
}

// Start walks root adding every directory to an fsnotify watcher, acquires
// the single-flight lock <root>/.leankg/watch.lock (LOCK_EX|LOCK_NB — a
// second Start on the same project returns ErrAlreadyWatching), and spawns
// the event loop. The loop flushes coalesced paths after Debounce by calling
// index.IndexDir once per flush; ctx.Done stops cleanly.
func Start(ctx context.Context, st store.Backend, root string, opts Options) (*Watcher, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("watch: resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("watch: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("watch: %s is not a directory", abs)
	}

	// Single-flight lock: .leankg/watch.lock, exclusive, non-blocking.
	leankgDir := filepath.Join(abs, ".leankg")
	if err := os.MkdirAll(leankgDir, 0o755); err != nil {
		return nil, fmt.Errorf("watch: create .leankg: %w", err)
	}
	lockPath := filepath.Join(leankgDir, "watch.lock")
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("watch: open lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		return nil, fmt.Errorf("watch: %w (%s)", ErrAlreadyWatching, abs)
	}
	// Stamp the owning PID as bookkeeping for `doctor --deep` (see embed.lock).
	_ = lockFile.Truncate(0)
	_, _ = fmt.Fprintf(lockFile, "%d\n", os.Getpid())

	notifier, err := fsnotify.NewWatcher()
	if err != nil {
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, fmt.Errorf("watch: new notifier: %w", err)
	}
	if err := addDirs(notifier, abs); err != nil {
		notifier.Close()
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, err
	}

	if opts.Debounce <= 0 {
		opts.Debounce = defaultDebounce
	}
	reconcileInterval, err := resolveReconcile(opts.Reconcile)
	if err != nil {
		notifier.Close()
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
		return nil, err
	}

	w := &Watcher{
		events: make(chan WatchEvent, 64),
		done:   make(chan struct{}),
	}
	w.stopInternal = func() {
		notifier.Close()
		syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		lockFile.Close()
	}

	go func() {
		defer close(w.done)
		defer w.stopOnce.Do(w.stopInternal)
		pending := make(map[string]bool)
		ticker := time.NewTicker(opts.Debounce)
		defer ticker.Stop()
		var reconcileC <-chan time.Time
		if reconcileInterval > 0 {
			rt := time.NewTicker(reconcileInterval)
			defer rt.Stop()
			reconcileC = rt.C
		}
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-notifier.Events:
				if !ok {
					return
				}
				kind := kindOf(ev.Op)
				if opts.OnEvent != nil {
					opts.OnEvent(ev.Name, kind)
				}
				select {
				case w.events <- WatchEvent{Path: ev.Name, Kind: kind}:
				default: // drop on overflow; never block the loop
				}
				if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0 {
					pending[ev.Name] = true
				}
				// New directories join the watch set.
				if ev.Op&fsnotify.Create != 0 {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() &&
						!skipDirs[filepath.Base(ev.Name)] && !dotPrefixed(filepath.Base(ev.Name)) {
						notifier.Add(ev.Name)
					}
				}
			case err, ok := <-notifier.Errors:
				if !ok {
					return
				}
				_ = err // fsnotify error (e.g. removed watched dir); keep watching
			case <-ticker.C:
				if len(pending) == 0 {
					continue
				}
				pending = make(map[string]bool)
				flushIndex(ctx, st, abs, opts)
			case <-reconcileC:
				// The pass covers every queued path, so pending is dropped
				// rather than flushed first. Only one pass runs at a time —
				// this goroutine owns both triggers.
				pending = make(map[string]bool)
				if err := addDirs(notifier, abs); err != nil {
					_ = err // a lost subtree is retried next pass
				}
				if err := flushIndex(ctx, st, abs, opts); err != nil {
					_ = err // transient (e.g. mid-rename file); next pass retries
					continue
				}
				select {
				case w.events <- WatchEvent{Path: abs, Kind: kindReconcile}:
				default:
				}
				if opts.OnEvent != nil {
					opts.OnEvent(abs, kindReconcile)
				}
			}
		}
	}()
	return w, nil
}

// flushIndex runs one debounced/reconcile incremental index pass and then
// refreshes the inventory snapshot — the same contract every other writer
// path holds (cmd runIndex, leankg-embed Run, federation pull, gc). Without
// it the watermark races ahead of the snapshot on every flush, so a project
// under active watching reads possibly_stale forever: found on THIS repo's
// fleet leg, where the live writer daemon made the WARN permanent, not
// transient. Refresh errors are not surfaced — a snapshot hiccup must not
// drop an index pass, and doctor reports the lag honestly meanwhile.
func flushIndex(ctx context.Context, st store.Backend, abs string, opts Options) error {
	if _, err := index.IndexDirWith(ctx, st, abs, opts.Registry); err != nil {
		return err
	}
	_, _ = store.RefreshInventory(st)
	return nil
}

// addDirs walks root adding every (non-skipped) directory to the notifier.
func addDirs(n *fsnotify.Watcher, root string) error {
	if err := n.Add(root); err != nil {
		return fmt.Errorf("watch: add root: %w", err)
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: skip, don't fail Start
		}
		if !d.IsDir() {
			return nil
		}
		if p != root && (skipDirs[d.Name()] || dotPrefixed(d.Name())) {
			return filepath.SkipDir
		}
		return n.Add(p)
	})
}

func dotPrefixed(name string) bool {
	return len(name) > 1 && name[0] == '.' && name != "." && name != ".."
}
