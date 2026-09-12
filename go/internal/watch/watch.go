// Package watch implements the fsnotify-based recursive watcher with
// debounced re-index (W2 writer role).
//
// The watcher is a trigger, not an indexer: every debounced flush runs
// index.IndexDir(ctx, st, root) once — IndexDir already 3-signal-skips
// unchanged files and handles deletions.
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

// Options configures Start.
type Options struct {
	// Registry, when non-nil, scopes re-indexed languages (lazy activation).
	Registry *langs.Registry
	// Debounce coalesces event paths into one IndexDir run per flush.
	// Defaults to 500ms when zero or negative.
	Debounce time.Duration
	// OnEvent is called per raw event before coalescing. Errors are ignored.
	OnEvent func(path, kind string) // kind: create|write|remove|rename
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
				_, _ = index.IndexDirWith(ctx, st, abs, opts.Registry)
			}
		}
	}()
	return w, nil
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
