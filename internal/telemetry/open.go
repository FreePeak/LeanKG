package telemetry

import (
	"os"
)

// openedRecorder is the recorder Open returns. Close stops the retention
// sweep before the recorder closes the store under it.
type openedRecorder struct {
	Recorder
	stopRetention func()
}

func (o *openedRecorder) Close() error {
	o.stopRetention()
	return o.Recorder.Close()
}

// Open returns the process Recorder for home: Nop (and no file) unless the
// effective level is above Off. A ledger that opens is swept for expired
// rows at once and then daily (DS-02).
func Open(home string) (Recorder, error) {
	cfg, err := LoadConfig(home)
	if err != nil {
		return Nop{}, err
	}
	lvl := EffectiveLevel(cfg, os.Getenv("LEANKG_TELEMETRY"))
	if lvl == Off {
		return Nop{}, nil
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
	return &openedRecorder{Recorder: rec, stopRetention: startRetention(st, cfg.RetentionDays)}, nil
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
