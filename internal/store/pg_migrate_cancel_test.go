package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// A migration cancelled while its DDL runs must roll its transaction back even
// though the caller's ctx is already done: the rollback is detached with
// context.WithoutCancel, so no session is left "idle in transaction".
//
// The DDL is made to block: migration 17 re-runs once its ledger row is gone,
// and its ALTER needs a lock that a second session holds. The ctx is cancelled
// while the ALTER waits on that lock, which is the only point where the
// rollback runs with a dead ctx.
func TestPGMigrateRollsBackWhenCtxCancelled(t *testing.T) {
	s := openPGTest(t)
	ctx := context.Background()
	url := os.Getenv(pgTestURLEnv)

	if _, err := s.pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = 17`); err != nil {
		t.Fatalf("delete ledger row 17: %v", err)
	}
	locker, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatalf("connect locker: %v", err)
	}
	defer locker.Close(ctx)
	holder, err := locker.Begin(ctx)
	if err != nil {
		t.Fatalf("begin locker tx: %v", err)
	}
	if _, err := holder.Exec(ctx, `LOCK TABLE `+s.schema+`.emb_stamp IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock emb_stamp: %v", err)
	}

	mctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Migrate(mctx) }()

	deadline := time.Now().Add(20 * time.Second)
	for {
		var waiting int
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query LIKE '%ALTER TABLE emb_stamp%'`).Scan(&waiting); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration 17 never blocked on the emb_stamp lock")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Migrate returned nil after its ctx was cancelled mid-DDL")
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release locker: %v", err)
	}

	// The server ends a session whose client went away only when it next
	// writes to it, which happens after the lock is released, so the check
	// polls: a transaction that stays open past the deadline is the leak.
	deadline = time.Now().Add(20 * time.Second)
	for {
		var leaked int
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_stat_activity
			WHERE datname = current_database() AND state LIKE 'idle in transaction%'
			AND query LIKE '%ALTER TABLE emb_stamp%'`).Scan(&leaked); err != nil {
			t.Fatalf("count leaked transactions: %v", err)
		}
		if leaked == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d session(s) still idle in transaction after a cancelled migration; want 0", leaked)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
