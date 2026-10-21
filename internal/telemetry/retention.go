package telemetry

import (
	"context"
	"log"
	"os"
	"sync"
	"time"
)

// retentionEvery is how often a running recorder sweeps old rows (DS-02).
const retentionEvery = 24 * time.Hour

// sweepTimeout bounds one retention sweep.
const sweepTimeout = time.Minute

// metaLastError is the meta key the last ledger-level failure is recorded
// under, so a failure is visible in the ledger and not only in a log line
// that scrolled away.
const metaLastError = "last_error"

// metaRecorder is the optional Store capability of recording a key/value
// pair in the ledger's meta table. It is optional so a Store without a meta
// table (a test double, a future backend) still works.
type metaRecorder interface {
	SetMeta(ctx context.Context, key, value string) error
}

// retentionSweepAt deletes rows older than days before now. A non-positive
// days value means the default window.
//
// A failure is recorded in the ledger's meta table before the error is
// returned: a sweep that fails silently lets the ledger grow without
// bound, which is exactly what the retention window exists to prevent.
func retentionSweepAt(ctx context.Context, st Store, days int, now time.Time) (int64, error) {
	if days <= 0 {
		days = DefaultRetentionDays
	}
	n, err := st.Purge(ctx, now.AddDate(0, 0, -days))
	if err != nil {
		recordLedgerError(ctx, st, err)
	}
	return n, err
}

// recordLedgerError writes a ledger-level failure to the ledger's meta
// table, so an operator can read it back later.
//
// The meta write gets its own context: the sweep's context may already be
// expired, and a failure must still be recorded.
func recordLedgerError(ctx context.Context, st Store, err error) {
	m, ok := st.(metaRecorder)
	if !ok {
		return
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sweepTimeout)
	defer cancel()
	if werr := m.SetMeta(wctx, metaLastError, err.Error()); werr != nil {
		log.Printf("telemetry: ledger error not recorded: %v", werr)
	}
}

// sweeper runs the retention sweep for one ledger path on a daily tick.
// Sweepers are keyed by path: a server that reaches the same ledger twice
// (the normal open path and a later consent reload) must not purge it
// twice, and must not run two tickers over one file.
type sweeper struct {
	refs int
	stop chan struct{}
	done chan struct{}
	// swept is closed once the first sweep has finished, so a caller (and
	// a test) can wait for it instead of guessing.
	swept chan struct{}
}

// retentionHandle is what an open path holds: the stop function of the
// sweeper and the signal that the first sweep has finished.
type retentionHandle struct {
	stop  func()
	swept <-chan struct{}
}

var sweepers = struct {
	mu sync.Mutex
	m  map[string]*sweeper
}{m: map[string]*sweeper{}}

// StartRetention sweeps the ledger once now and then daily until the
// returned stop function is called. Errors are reported, not dropped:
// retention must never affect a call.
//
// StartRetention is the exported form of startRetention: a caller that
// builds a recorder lazily (cmd/leankg's consent reload, when capture was
// off at startup) needs the same retention sweep the normal open path
// installs, or a first-after-reload ledger would never be swept.
func StartRetention(st Store, days int) (func(), error) {
	if st == nil {
		return func() {}, nil
	}
	return startRetention(st, days).stop, nil
}

// startRetention sweeps st now and then daily until the returned handle's
// stop function is called. A store that knows its own path joins the
// sweeper for that path, so the two open paths (Open and a consent reload)
// share one ticker over one file.
func startRetention(st Store, days int) *retentionHandle {
	if p, ok := st.(interface{ Path() string }); ok && p.Path() != "" {
		return startPathRetention(p.Path(), days)
	}
	return startStoreRetention(st, days)
}

// startPathRetention starts (or joins) the retention sweeper for the ledger
// at path and returns a handle that releases this holder.
//
// The sweeper owns its own handle to the ledger: it keeps working whichever
// handle opened it, and it never interferes with the recorder's writes. The
// first sweep runs in the background, so opening a ledger never blocks on
// the purge (which waits for any other process reading the file).
func startPathRetention(path string, days int) *retentionHandle {
	sweepers.mu.Lock()
	if s, ok := sweepers.m[path]; ok {
		s.refs++
		sweepers.mu.Unlock()
		return &retentionHandle{stop: releaser(path, s), swept: s.swept}
	}
	s := &sweeper{refs: 1, stop: make(chan struct{}), done: make(chan struct{}), swept: make(chan struct{})}
	sweepers.m[path] = s
	sweepers.mu.Unlock()

	st, err := openLedger(path)
	if err != nil {
		log.Printf("telemetry: retention: open %s: %v", path, err)
		sweepers.mu.Lock()
		delete(sweepers.m, path)
		sweepers.mu.Unlock()
		close(s.stop)
		close(s.done)
		close(s.swept)
		return &retentionHandle{stop: func() {}, swept: s.swept}
	}

	go func() {
		defer close(s.done)
		defer st.Close()
		sweepLedger(st, days)
		close(s.swept)
		t := time.NewTicker(retentionEvery)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-t.C:
				sweepLedger(st, days)
			}
		}
	}()
	return &retentionHandle{stop: releaser(path, s), swept: s.swept}
}

// startStoreRetention sweeps st now and then daily until the returned stop
// function is called. It is the fallback for a store that does not know its
// own path.
func startStoreRetention(st Store, days int) *retentionHandle {
	swept := make(chan struct{})
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer func() { _ = recover() }()
		sweepLedger(st, days)
		close(swept)
		t := time.NewTicker(retentionEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				sweepLedger(st, days)
			}
		}
	}()
	var once sync.Once
	return &retentionHandle{
		swept: swept,
		stop: func() {
			once.Do(func() {
				close(done)
				<-finished
			})
		},
	}
}

// sweepLedger runs one retention sweep. A failure is logged (the ledger
// meta row records it too) so it cannot pass unnoticed.
func sweepLedger(st Store, days int) {
	ctx, cancel := context.WithTimeout(context.Background(), sweepTimeout)
	defer cancel()
	if _, err := retentionSweepAt(ctx, st, days, time.Now()); err != nil {
		log.Printf("telemetry: retention sweep failed: %v", err)
	}
}

// releaser returns the stop function of the sweeper for path. The sweeper
// stops when its last holder releases it.
func releaser(path string, s *sweeper) (stop func()) {
	var once sync.Once
	return func() {
		once.Do(func() { releaseSweeper(path, s) })
	}
}

// releaseSweeper drops one holder of the sweeper for path and stops the
// sweeper when the last holder is gone.
func releaseSweeper(path string, s *sweeper) {
	sweepers.mu.Lock()
	s.refs--
	last := s.refs <= 0
	if last {
		delete(sweepers.m, path)
	}
	sweepers.mu.Unlock()
	if last {
		close(s.stop)
		<-s.done
	}
}

// openLedger opens the ledger at path for writing without ever creating it:
// the sweeper must never bring a ledger into existence.
func openLedger(path string) (*SQLiteStore, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return OpenSQLite(path, false)
}
