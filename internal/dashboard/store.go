package dashboard

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// ledger hands out the telemetry store lazily. The file is opened only when
// it already exists, so opening the dashboard never creates telemetry.db; a
// missing file yields emptyStore and every report is empty.
type ledger struct {
	home string
	open func(home string, readOnly bool) (telemetry.Store, error)

	mu sync.Mutex
	st telemetry.Store

	lmu  sync.Mutex
	link *linker
}

type linker struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *ledger) store() (telemetry.Store, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.st != nil {
		return l.st, nil
	}
	path := telemetry.DBPath(l.home)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyStore{path: path}, nil
		}
		return nil, err
	}
	st, err := l.open(l.home, false)
	if err != nil {
		return nil, err
	}
	l.st = st
	return st, nil
}

// startLinker runs fn under a cancellable context unless one is running.
func (l *ledger) startLinker(parent context.Context, fn func(context.Context)) {
	l.lmu.Lock()
	defer l.lmu.Unlock()
	if l.link != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	l.link = &linker{cancel: cancel, done: done}
	go func() {
		defer close(done)
		fn(ctx)
	}()
}

func (l *ledger) linking() bool {
	l.lmu.Lock()
	defer l.lmu.Unlock()
	return l.link != nil
}

// stopLinker cancels the linker and waits for it, so no link pass can use a
// store that is about to be closed or purged.
func (l *ledger) stopLinker() {
	l.lmu.Lock()
	lk := l.link
	l.link = nil
	l.lmu.Unlock()
	if lk != nil {
		lk.cancel()
		<-lk.done
	}
}

func (l *ledger) close() {
	l.stopLinker()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.st != nil {
		_ = l.st.Close()
		l.st = nil
	}
}

// purge stops linking, closes the store and deletes the ledger files.
func (l *ledger) purge() error {
	l.stopLinker()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.st != nil {
		_ = l.st.Close()
		l.st = nil
	}
	db := telemetry.DBPath(l.home)
	var firstErr error
	for _, p := range []string{db, db + "-wal", db + "-shm", db + "-journal"} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

var errReadOnly = errors.New("dashboard: the ledger is read-only here")

// emptyStore stands in for a ledger that does not exist yet.
type emptyStore struct{ path string }

func (emptyStore) InsertCalls(context.Context, []telemetry.CallEvent) error { return errReadOnly }
func (emptyStore) InsertMemoryEvents(context.Context, []telemetry.MemoryEvent) error {
	return errReadOnly
}
func (emptyStore) UpsertSessions(context.Context, []telemetry.Session) error  { return errReadOnly }
func (emptyStore) UpsertLinks(context.Context, []telemetry.SessionLink) error { return errReadOnly }
func (emptyStore) InsertABRuns(context.Context, []telemetry.ABRun) error      { return errReadOnly }
func (emptyStore) AddDropped(context.Context, int64) error                    { return errReadOnly }

func (emptyStore) Calls(context.Context, telemetry.CallFilter) ([]telemetry.CallEvent, error) {
	return []telemetry.CallEvent{}, nil
}
func (emptyStore) Call(context.Context, string) (telemetry.CallEvent, bool, error) {
	return telemetry.CallEvent{}, false, nil
}
func (emptyStore) MemoryEvents(context.Context, telemetry.CallFilter) ([]telemetry.MemoryEvent, error) {
	return []telemetry.MemoryEvent{}, nil
}
func (emptyStore) Sessions(context.Context, telemetry.CallFilter) ([]telemetry.Session, error) {
	return []telemetry.Session{}, nil
}
func (emptyStore) Session(context.Context, string) (telemetry.Session, bool, error) {
	return telemetry.Session{}, false, nil
}
func (emptyStore) Links(context.Context, string) ([]telemetry.SessionLink, error) {
	return []telemetry.SessionLink{}, nil
}
func (emptyStore) UnlinkedCalls(context.Context, time.Time, int) ([]telemetry.CallEvent, error) {
	return []telemetry.CallEvent{}, nil
}
func (emptyStore) ABRuns(context.Context) ([]telemetry.ABRun, error) {
	return []telemetry.ABRun{}, nil
}
func (emptyStore) Purge(context.Context, time.Time) (int64, error) { return 0, nil }
func (e emptyStore) Stats(context.Context) (telemetry.Stats, error) {
	return telemetry.Stats{Path: e.path}, nil
}
func (emptyStore) Close() error { return nil }
