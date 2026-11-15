package telemetry

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// The rollback runs on a WithoutCancel of the caller's ctx: a write that fails
// after its ctx was cancelled still rolls back, and the connection is left
// with no open transaction.
func TestWithWriteTxRollbackRunsAfterCancel(t *testing.T) {
	st := openTestLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := st.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	boom := errors.New("boom")
	err = withWriteTx(ctx, conn, func(q querier) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES ('ctx_tail', '1')`); err != nil {
			t.Fatal(err)
		}
		cancel()
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("withWriteTx err = %v, want boom", err)
	}
	var n int
	if err := conn.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM meta WHERE key = 'ctx_tail'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("failed write left %d row(s): the rollback did not run", n)
	}
	if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatalf("connection still holds a transaction after rollback: %v", err)
	}
	_, _ = conn.ExecContext(context.Background(), `ROLLBACK`)
}

// Entry point: opening a ledger honours a cancelled ctx.
func TestOpenSQLiteHonoursCancelledCtx(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "telemetry.db"), false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenSQLite err = %v, want context.Canceled", err)
	}
}

// Entry point: the vacuum conversion honours a cancelled ctx before it starts.
func TestEnableIncrementalVacuumHonoursCancelledCtx(t *testing.T) {
	path := filepath.Join(t.TempDir(), "telemetry.db")
	seed, err := OpenSQLite(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	_ = seed.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EnableIncrementalVacuum(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnableIncrementalVacuum err = %v, want context.Canceled", err)
	}
}

// The flush on Close is a detached tail: events queued before a shutdown
// cancel still reach the ledger.
func TestRecorderFlushOnCloseRunsAfterCancel(t *testing.T) {
	st := openTestLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	rec := NewRecorder(ctx, st, Metadata, 1024)
	cancel()
	rec.RecordCall(CallEvent{Tool: "query"})
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}
	stats, err := reopenRO(t, st).Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Calls != 1 {
		t.Fatalf("calls = %d after close with a cancelled ctx, want 1 (flush tail did not run)", stats.Calls)
	}
}

// The retention sweep records its failure through a WithoutCancel of the
// sweep ctx, so a sweep that was cancelled still leaves its error in the ledger.
func TestRetentionSweepRecordsErrorAfterCancel(t *testing.T) {
	st := openTestLedger(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sweepLedger(ctx, st, 7)
	var v string
	if err := st.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, metaLastError).Scan(&v); err != nil {
		t.Fatalf("last_error not recorded: %v", err)
	}
	if !strings.Contains(v, "context canceled") {
		t.Fatalf("last_error = %q, want the cancellation", v)
	}
}
