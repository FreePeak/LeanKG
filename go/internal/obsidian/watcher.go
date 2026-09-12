package obsidian

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatchOptions configures Watch.
type WatchOptions struct {
	// Debounce is the minimum gap between two applied syncs: events arriving
	// inside the window are coalesced away (Rust ObsidianWatcher semantics).
	// Zero or negative means DefaultDebounce.
	Debounce time.Duration
	// OnEvent is called with the path of every event that starts a sync.
	OnEvent func(path string)
	// OnSync is called with the result of each debounced Pull.
	OnSync func(res PullResult, err error)
}

// rawEvent is one filesystem event offered to the debounce loop.
type rawEvent struct {
	path string
	op   fsnotify.Op
}

// Watch watches the vault recursively and pulls on every debounced
// create/write/remove/rename event until ctx is done (Rust
// ObsidianWatcher::watch). Directories created later join the watch set.
func (e *Engine) Watch(ctx context.Context, opts WatchOptions) error {
	debounce := opts.Debounce
	if debounce <= 0 {
		debounce = DefaultDebounce
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("obsidian: watcher: %w", err)
	}
	defer w.Close()
	if err := addWatchDirs(w, e.vault); err != nil {
		return err
	}

	// Bounded handoff: on overflow events are dropped, never blocked on.
	events := make(chan rawEvent, 100)
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Op&fsnotify.Create != 0 {
					if fi, serr := os.Stat(ev.Name); serr == nil && fi.IsDir() {
						_ = addWatchDirs(w, ev.Name)
					}
				}
				select {
				case events <- rawEvent{path: ev.Name, op: ev.Op}:
				default:
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
				// fsnotify error (for example a watched directory removed):
				// keep watching, the next event re-syncs.
			}
		}
	}()

	e.debounceLoop(ctx, events, debounce, time.Now, opts)
	return nil
}

// debounceLoop applies the Rust throttle: the first event that arrives at least
// debounce after the loop starts syncs immediately, every event inside the
// window is dropped, and the pull runs inline (events arriving meanwhile wait
// in the bounded channel).
func (e *Engine) debounceLoop(ctx context.Context, events <-chan rawEvent, debounce time.Duration, now func() time.Time, opts WatchOptions) {
	last := now()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if !syncableEvent(ev) {
				continue
			}
			t := now()
			if t.Sub(last) < debounce {
				continue // inside the debounce window: coalesce away
			}
			last = t
			if opts.OnEvent != nil {
				opts.OnEvent(ev.path)
			}
			res, err := e.Pull()
			if opts.OnSync != nil {
				opts.OnSync(res, err)
			}
		}
	}
}

// syncableEvent ports the Rust should_sync_event filter: create, modify,
// remove. fsnotify reports renames as their own op, and notify surfaced them
// as Modify, so renames sync too.
func syncableEvent(ev rawEvent) bool {
	return ev.op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) != 0
}

// addWatchDirs adds root and every directory below it to the watcher.
// Unreadable subtrees are skipped rather than failing the watch.
func addWatchDirs(w *fsnotify.Watcher, root string) error {
	if err := w.Add(root); err != nil {
		return fmt.Errorf("obsidian: watch %s: %w", root, err)
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == root {
			return nil
		}
		_ = w.Add(p)
		return nil
	})
}
