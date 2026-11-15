package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// ctxRole is one role's probe for TestCancelledContextHonoured: read and write
// are one representative call each. A read-only role leaves write nil. A role
// joins by appending an entry; the probe runs against both engines (PostgreSQL
// only when LEANKG_TEST_PG_URL is set).
type ctxRole struct {
	name  string
	read  func(ctx context.Context, b Backend) error
	write func(ctx context.Context, b Backend) error
}

// ctxConformanceRoles lists the roles whose methods take ctx first (CX-19,
// CX-20 and the roles that follow them in plan section 6, P2).
var ctxConformanceRoles = []ctxRole{
	{
		name: "IndexWriter",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.Files(ctx)
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.UpsertElements(ctx, []Element{{QualifiedName: "conformance", ElementType: "function", Name: "conformance", FilePath: "a.go"}})
		},
	},
	{
		name: "ElementReader",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.FindExact(ctx, "conformance")
			return err
		},
	},
	{
		name: "Searcher.FindFuzzy",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.FindFuzzy(ctx, "conformance", 5)
			return err
		},
	},
	{
		name: "Searcher.SearchVectors",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.SearchVectors(ctx, VectorQuery{ModelID: "conformance", Vec: []float32{1, 0}, K: 5})
			return err
		},
	},
	{
		// The keyword-only path: a text query with no vector must still
		// honour the ctx instead of swallowing the arm error.
		name: "Searcher.HybridText",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.Hybrid(ctx, HybridQuery{ModelID: "conformance", Text: "conformance", Limit: 5})
			return err
		},
	},
	{
		name: "Searcher.HybridVector",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.Hybrid(ctx, HybridQuery{ModelID: "conformance", Text: "conformance", Vec: []float32{1, 0}, Limit: 5})
			return err
		},
	},
	{
		name: "GraphReader",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.Outgoing(ctx, "conformance")
			return err
		},
	},
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
	{
		name: "Vectors",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.VectorCount(ctx, "conformance")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.UpsertVectors(ctx, "conformance", []VectorRow{{QualifiedName: "conformance", Vec: []float32{1, 0}}})
		},
	},
	{
		name: "EmbedRuns",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.LastEmbedRunAny(ctx)
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			_, err := b.StartEmbedRun(ctx, "conformance", "full", 0)
			return err
		},
	},
	{
		name: "Identity",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.AccountByID(ctx, "conformance")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.AccountUpsert(ctx, Account{ID: "conformance", Email: "conformance@example.com", Name: "conformance", Status: "active"})
		},
	},
	{
		name: "Tokens",
		read: func(ctx context.Context, b Backend) error {
			_, _, err := b.TokenFind(ctx, "conformance-secret")
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.TokenUpsert(ctx, Token{ID: "conformance", Name: "conformance", Secret: "conformance-secret", Role: "viewer", CreatedAt: 1})
		},
	},
	{
		name: "Audit",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.AuditTail(ctx, 1)
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			return b.AppendAudit(ctx, &AuditEntry{Actor: "conformance", Action: "conformance", Target: "conformance"})
		},
	},
	{
		name: "Maintainer",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.Space(ctx)
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			_, err := b.Maintain(ctx, MaintainOp{Kind: MaintainCheckpoint})
			return err
		},
	},
	{
		name: "Maintainer.Sweeps",
		read: func(ctx context.Context, b Backend) error {
			_, err := b.DeleteStaleStamps(ctx, []string{"conformance"})
			return err
		},
		write: func(ctx context.Context, b Backend) error {
			_, err := b.DeleteOrphanRelationships(ctx)
			return err
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
					if r.write == nil {
						return
					}
					if err := r.write(ctx, b); !errors.Is(err, context.Canceled) {
						t.Errorf("write: err = %v, want context.Canceled", err)
					}
				})
			}
		})
	}
}
