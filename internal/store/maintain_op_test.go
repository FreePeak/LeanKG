package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// allMaintainKinds lists every MaintainKind; the conformance tests range over
// it so a kind added later is covered without a new test.
var allMaintainKinds = []MaintainKind{
	MaintainUpgrade,
	MaintainCheckpoint,
	MaintainReclaim,
	MaintainReclaimFull,
	MaintainCompactKeywordIndex,
	MaintainAnalyze,
}

// maintainerEngines returns one read-only handle per engine. The PostgreSQL
// handle has no pool: the read-only refusal happens before any engine work,
// so these checks need no server. The live PostgreSQL run belongs to the
// integration gate.
func maintainerEngines(t *testing.T) map[string]Maintainer {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leankg.db")
	rw, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close rw: %v", err)
	}
	ro, err := Open(path, RO)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })
	return map[string]Maintainer{
		EngineSQLite:   ro,
		EnginePostgres: &PGStore{mode: RO},
	}
}

// TestMaintainRefusesReadOnlyOnEveryEngine: every MaintainOp on a read-only
// handle returns the one ErrReadOnly sentinel, on both engines.
func TestMaintainRefusesReadOnlyOnEveryEngine(t *testing.T) {
	for engine, m := range maintainerEngines(t) {
		for _, kind := range allMaintainKinds {
			_, err := m.Maintain(MaintainOp{Kind: kind})
			if !errors.Is(err, ErrReadOnly) {
				t.Errorf("%s %s on a read-only handle: err = %v, want ErrReadOnly", engine, kind, err)
			}
		}
	}
}

// TestMaintainerSweepsRefuseReadOnlyOnEveryEngine: the Maintainer role's
// remaining write methods share the same refusal.
func TestMaintainerSweepsRefuseReadOnlyOnEveryEngine(t *testing.T) {
	for engine, m := range maintainerEngines(t) {
		checks := map[string]func() error{
			"PruneEmbedRuns": func() error {
				_, err := m.PruneEmbedRuns(time.Now())
				return err
			},
			"DeleteOrphanRelationships": func() error {
				_, err := m.DeleteOrphanRelationships()
				return err
			},
			"DeleteOrphanVectors": func() error {
				_, err := m.DeleteOrphanVectors()
				return err
			},
			"DeleteStaleStamps": func() error {
				_, err := m.DeleteStaleStamps([]string{"keep-model"})
				return err
			},
		}
		for name, call := range checks {
			if err := call(); !errors.Is(err, ErrReadOnly) {
				t.Errorf("%s %s on a read-only handle: err = %v, want ErrReadOnly", engine, name, err)
			}
		}
	}
}

// TestPGMaintainReportsSkippedOps: the kinds PostgreSQL does not run come
// back with a reason, never a silent nil. No server is needed because these
// return before the pool is touched.
func TestPGMaintainReportsSkippedOps(t *testing.T) {
	pg := &PGStore{mode: RW}
	want := map[MaintainKind]string{
		MaintainUpgrade:             skipAutovacuum,
		MaintainCheckpoint:          skipServerWAL,
		MaintainReclaim:             skipAutovacuum,
		MaintainReclaimFull:         skipAutovacuum,
		MaintainCompactKeywordIndex: skipNoKeywordSegments,
	}
	for kind, reason := range want {
		res, err := pg.Maintain(MaintainOp{Kind: kind})
		if err != nil {
			t.Errorf("postgres %s: err = %v, want nil", kind, err)
		}
		if res.Skipped != reason {
			t.Errorf("postgres %s: Skipped = %q, want %q", kind, res.Skipped, reason)
		}
	}
}

// TestSQLiteMaintainSkipsAnalyze: SQLite does not run ANALYZE as a
// maintenance kind; the engine says so instead of returning nil.
func TestSQLiteMaintainSkipsAnalyze(t *testing.T) {
	st := openTestStore(t)
	res, err := st.Maintain(MaintainOp{Kind: MaintainAnalyze})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if res.Skipped == "" {
		t.Error("sqlite analyze ran or reported nothing; want a Skipped reason")
	}
}

// TestMaintainRejectsUnknownKind: an unrecognised kind is an error on both
// engines, not a silent no-op.
func TestMaintainRejectsUnknownKind(t *testing.T) {
	engines := map[string]Maintainer{
		EngineSQLite:   openTestStore(t),
		EnginePostgres: &PGStore{mode: RW},
	}
	for engine, m := range engines {
		if _, err := m.Maintain(MaintainOp{Kind: "bogus"}); err == nil {
			t.Errorf("%s accepted an unknown kind", engine)
		}
	}
}
