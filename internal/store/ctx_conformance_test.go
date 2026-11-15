package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// ctxRole is one role's probe for TestCancelledContextHonoured: read and write
// are one representative call each. A role joins by appending an entry; the
// probe runs against both engines (PostgreSQL only when LEANKG_TEST_PG_URL is
// set).
type ctxRole struct {
	name  string
	read  func(ctx context.Context, b Backend) error
	write func(ctx context.Context, b Backend) error
}

// ctxConformanceRoles lists the roles whose methods take ctx first (CX-19
// and the roles that follow it in plan section 6, P2).
var ctxConformanceRoles = []ctxRole{
	{
		name: "Migrate",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.AppliedMigrations(ctx)
			return err
		},
		write: func(ctx context.Context, b Backend) error { return b.Migrate(ctx) },
	},
	{
		name: "KV",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.KVGet(ctx, "conformance", "key")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.KVSet(ctx, "conformance", "key", "value")
		},
	},
	{
		name: "Inventories",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.Watermark(ctx)
			return err
		},
		write: func(ctx context.Context, b Backend) error { return b.BumpWatermark(ctx) },
	},
	{
		name: "Projects",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.ProjectGet(ctx, "/conformance")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.ProjectUpsert(ctx, ProjectRecord{Dir: "/conformance", Name: "conformance", RegisteredAt: "2026-01-01T00:00:00Z"})
		},
	},
	{
		name: "Summaries",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.SummaryGet(ctx, "a.go")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.SummaryUpsert(ctx, FileSummary{Path: "a.go", ContentHash: "h", Model: "m", Summary: "s"})
		},
	},
}

// openSQLiteConformance returns a migrated RW SQLite store in a temp project.
func openSQLiteConformance(t *testing.T) Backend {
	t.Helper()
	s, err := openSQLite(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

// TestCancelledContextHonoured is the dual-engine conformance check for the
// ctx-first roles: with an already-cancelled context, one read and one write
// per role return context.Canceled instead of touching the store.
func TestCancelledContextHonoured(t *testing.T) {
	engines := []struct {
		name string
		open func(t *testing.T) Backend
	}{
		{EngineSQLite, openSQLiteConformance},
		{EnginePostgres, func(t *testing.T) Backend { return openPGAt(t, t.TempDir()) }},
	}
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			b := e.open(t)
			for _, r := range ctxConformanceRoles {
				t.Run(r.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					if err := r.read(ctx, b); !errors.Is(err, context.Canceled) {
						t.Errorf("read: err = %v, want context.Canceled", err)
					}
					if err := r.write(ctx, b); !errors.Is(err, context.Canceled) {
						t.Errorf("write: err = %v, want context.Canceled", err)
					}
				})
			}
		})
	}
}
