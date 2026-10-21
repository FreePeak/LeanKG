package telemetry

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// D.1 — the retention sweeper must run for a ledger that exists, whether or
// not capture is on. With capture off there is no ledger yet, but a ledger
// that is already there keeps growing until the next consent reload, and
// nothing ever reclaims it.
//
// The sweep must be observable without waiting for the daily tick: the first
// sweep runs when the sweeper starts.
func TestOpenWithCaptureOffStillSweepsAnExistingLedger(t *testing.T) {
	home := homeDir(t)
	ctx := context.Background()

	// A ledger from an earlier session, with a row past the retention
	// window and one inside it.
	path := DBPath(home)
	st, err := OpenSQLite(path, false)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	now := time.Now()
	if err := st.InsertCalls(ctx, []CallEvent{
		{ID: "stale", TS: now.Add(-45 * 24 * time.Hour), Identity: Identity{ClientName: "pi", Cwd: "/w"}},
		{ID: "fresh", TS: now.Add(-time.Hour), Identity: Identity{ClientName: "pi", Cwd: "/w"}},
	}); err != nil {
		t.Fatalf("InsertCalls: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Capture is off (no consent), but the ledger exists: it must be swept.
	rec, err := Open(home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rec.Close()
	if _, isNop := rec.(Nop); isNop {
		t.Fatal("Open returned Nop for an existing ledger: no retention sweeper is installed")
	}
	// The first sweep runs in the background; wait for it rather than
	// guessing. The interface is declared here so this test also compiles
	// against the unfixed code, where the assertion simply fails.
	sw, ok := rec.(interface{ sweptSignal() <-chan struct{} })
	if !ok {
		t.Fatalf("Open returned %T for an existing ledger: it owns no retention sweeper", rec)
	}
	select {
	case <-sw.sweptSignal():
	case <-time.After(30 * time.Second):
		t.Fatal("the first retention sweep did not finish")
	}

	after, err := OpenStore(home, true)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer after.Close()
	if _, ok, err := after.Call(ctx, "stale"); err != nil {
		t.Fatalf("Call: %v", err)
	} else if ok {
		t.Fatal("the expired call survived: the retention sweeper did not run")
	}
	if _, ok, err := after.Call(ctx, "fresh"); err != nil {
		t.Fatalf("Call: %v", err)
	} else if !ok {
		t.Fatal("the fresh call was swept: the retention window is wrong")
	}
}

// D.2 — a failed sweep must not vanish. It is logged and recorded in the
// ledger's meta table, so an operator can see it after the fact.
func TestRetentionSweepErrorIsRecorded(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()

	// Break the purge: the table it deletes from is gone.
	if _, err := st.db.ExecContext(ctx, `DROP TABLE calls`); err != nil {
		t.Fatalf("drop calls: %v", err)
	}

	if _, err := retentionSweepAt(ctx, st, 30, time.Now()); err == nil {
		t.Fatal("the sweep was expected to fail")
	}

	// The key is spelled out rather than referenced through a constant, so
	// this test compiles against the unfixed code too and fails there for
	// the right reason: the failure was dropped.
	var recorded string
	err := st.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, "last_error").Scan(&recorded)
	if err != nil {
		t.Fatalf("the sweep failure was dropped: no %q row in meta (%v)", "last_error", err)
	}
	if recorded == "" {
		t.Fatalf("the sweep failure was recorded as an empty %q row", "last_error")
	}
}

// D.3 — the purge must reclaim the A/B runs too: they are rows in the same
// ledger and grow at the same rate.
func TestPurgeRemovesExpiredABRuns(t *testing.T) {
	st := openTestLedger(t)
	ctx := context.Background()
	now := time.Now()

	if err := st.InsertABRuns(ctx, []ABRun{
		{ID: "old", Source: "bench", Task: "t1", Arm: "a", Repo: "/r", ImportedAt: now.Add(-45 * 24 * time.Hour)},
		{ID: "new", Source: "bench", Task: "t2", Arm: "a", Repo: "/r", ImportedAt: now.Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("InsertABRuns: %v", err)
	}

	if _, err := st.Purge(ctx, now.AddDate(0, 0, -30)); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	runs, err := st.ABRuns(ctx)
	if err != nil {
		t.Fatalf("ABRuns: %v", err)
	}
	for _, r := range runs {
		if r.ID == "old" {
			t.Fatal("the expired A/B run survived the purge")
		}
	}
	if len(runs) != 1 || runs[0].ID != "new" {
		t.Fatalf("A/B runs after the purge = %d, want only the fresh one", len(runs))
	}
}

// D.4 — a ledger must actually shrink when it is swept. A new ledger is
// created with incremental auto-vacuum, the purge hands the freed pages
// back, and the write-ahead log is truncated.
func TestPurgeReclaimsPagesAndTruncatesTheWAL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.db")
	st, err := OpenSQLite(path, false)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer st.Close()
	ctx := context.Background()

	// A new ledger must be in incremental auto-vacuum mode, or a purge can
	// never give a freed page back.
	var mode int
	if err := st.db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum = %d, want 2 (INCREMENTAL): a purge cannot reclaim the pages it frees", mode)
	}

	// Fill the ledger so there are pages to free.
	calls := make([]CallEvent, 0, 2000)
	for i := range 2000 {
		calls = append(calls, CallEvent{
			ID: "c" + itoa(i), TS: time.Now(), Transport: TransportStdio, Method: "tools/call",
			Tool: "query", BodyRedacted: string(make([]byte, 1024)),
		})
	}
	if err := st.InsertCalls(ctx, calls); err != nil {
		t.Fatalf("InsertCalls: %v", err)
	}
	before := pageCount(t, st)
	if before < 100 {
		t.Fatalf("the test ledger has only %d pages; it cannot show a shrink", before)
	}

	if _, err := st.Purge(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	after := pageCount(t, st)
	if after >= before {
		t.Fatalf("the ledger did not shrink: %d pages before the purge, %d after", before, after)
	}
	var free int
	if err := st.db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatalf("PRAGMA freelist_count: %v", err)
	}
	if free != 0 {
		t.Fatalf("freelist_count = %d after the purge, want 0: the freed pages were not returned", free)
	}
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() > 0 {
		t.Fatalf("the write-ahead log is %d bytes after the purge, want 0", fi.Size())
	}
}

// D.4 — an existing ledger (created before new ledgers got the mode at
// creation time) can be converted to incremental auto-vacuum, on request.
// The conversion is opt-in: it rewrites the whole file.
func TestEnableIncrementalVacuumConvertsAnExistingLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.db")

	// A ledger in the old mode: opened without the auto_vacuum setting, the
	// way a ledger created before this fix was. The table is named so it
	// does not collide with the schema migration.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE legacy_rows (id TEXT PRIMARY KEY, ts INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create legacy_rows: %v", err)
	}
	var mode int
	if err := db.QueryRowContext(context.Background(), `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 0 {
		t.Fatalf("the fixture ledger is in auto_vacuum mode %d, want 0 (the old mode)", mode)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := EnableIncrementalVacuum(path); err != nil {
		t.Fatalf("EnableIncrementalVacuum: %v", err)
	}

	after, err := OpenSQLite(path, false)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer after.Close()
	if err := after.db.QueryRowContext(context.Background(), `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum = %d after the conversion, want 2 (INCREMENTAL)", mode)
	}
}

// pageCount reads the ledger's page count.
func pageCount(t *testing.T, st *SQLiteStore) int {
	t.Helper()
	var n int
	if err := st.db.QueryRowContext(context.Background(), `PRAGMA page_count`).Scan(&n); err != nil {
		t.Fatalf("PRAGMA page_count: %v", err)
	}
	return n
}

// itoa is a small integer formatter for row ids.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
