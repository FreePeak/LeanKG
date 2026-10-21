package telemetry

import (
	"errors"
	"os"
)

// openedRecorder is the recorder Open returns. Close stops the retention
// sweep before the recorder closes the store under it.
type openedRecorder struct {
	Recorder
	stopRetention func()
	// swept is closed when the first retention sweep has finished.
	swept <-chan struct{}
}

func (o *openedRecorder) Close() error {
	o.stopRetention()
	return o.Recorder.Close()
}

// sweptSignal returns the channel that is closed when the first retention
// sweep has finished.
func (o *openedRecorder) sweptSignal() <-chan struct{} { return o.swept }

// retentionOnly is the recorder for a ledger that exists while capture is
// off. It records nothing and owns the retention sweeper: without it the
// rows the ledger already holds are never reclaimed, because the window
// only means something if something enforces it.
type retentionOnly struct {
	stop func()
	// swept is closed when the first retention sweep has finished.
	swept <-chan struct{}
}

func (r *retentionOnly) Level() Level { return Off }

// SetLevel refuses to raise the level: capture off means off. A consent
// reload builds a recorder of its own (cmd/leankg's ReloadTelemetry), so
// refusing here keeps "off means off" true for this recorder.
func (r *retentionOnly) SetLevel(level Level) error {
	if level == Off {
		return nil
	}
	return errors.New("telemetry: capture is off; a consent reload builds a new recorder")
}

func (r *retentionOnly) RecordCall(CallEvent)     {}
func (r *retentionOnly) RecordMemory(MemoryEvent) {}

// Close stops the retention sweeper.
func (r *retentionOnly) Close() error {
	r.stop()
	return nil
}

// sweptSignal returns the channel that is closed when the first retention
// sweep has finished.
func (r *retentionOnly) sweptSignal() <-chan struct{} { return r.swept }

// Open returns the process Recorder for home: Nop (and no file) unless the
// effective level is above Off. A ledger that opens is swept for expired
// rows at once and then daily (DS-02).
//
// Capture off does not create a ledger, but a ledger that already exists
// still gets its retention sweeper: the sweeper is what reclaims the rows
// the window covers, and it must not depend on whether the server captures
// anything today.
func Open(home string) (Recorder, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		return Nop{}, err
	}
	lvl := EffectiveLevel(cfg, os.Getenv("LEANKG_TELEMETRY"))
	if lvl == Off {
		return openRetentionOnly(home, cfg.RetentionDays)
	}
	maxBody := cfg.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	st, err := OpenSQLite(DBPath(home), false)
	if err != nil {
		return Nop{}, err
	}
	rec := NewRecorder(st, lvl, maxBody)
	h := startRetention(st, cfg.RetentionDays)
	return &openedRecorder{Recorder: rec, stopRetention: h.stop, swept: h.swept}, nil
}

// openRetentionOnly returns the recorder for a home whose capture is off.
// With no ledger on disk there is nothing to sweep and nothing to open, so
// it is the plain Nop. With a ledger on disk the sweeper runs: it opens the
// ledger itself, so nothing is written while capture is off.
func openRetentionOnly(home string, days int) (Recorder, error) {
	path := DBPath(home)
	if _, err := os.Stat(path); err != nil {
		return Nop{}, nil
	}
	h := startPathRetention(path, days)
	return &retentionOnly{stop: h.stop, swept: h.swept}, nil
}

// OpenStore opens DBPath(home). readOnly never creates the file and returns
// an error wrapping os.ErrNotExist when the ledger does not exist.
func OpenStore(home string, readOnly bool) (Store, error) {
	st, err := OpenSQLite(DBPath(home), readOnly)
	if err != nil {
		return nil, err
	}
	return st, nil
}
