package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// hnsw.iterative_scan exists only from pgvector 0.8.0; the setting is applied
// only where the installed extension defines it.
func TestPGVectorHasIterativeScan(t *testing.T) {
	cases := map[string]bool{
		"0.7.4":   false,
		"0.7.10":  false,
		"0.8.0":   true,
		"0.8.7":   true,
		"0.10.0":  true,
		"1.0.0":   true,
		"":        false,
		"garbage": false,
	}
	for v, want := range cases {
		if got := pgvectorHasIterativeScan(v); got != want {
			t.Errorf("pgvectorHasIterativeScan(%q) = %v; want %v", v, got, want)
		}
	}
}

// The session setting matches the installed pgvector: strict_order where the
// extension defines iterative scan, and no setting where it does not.
func TestPGIterativeScanFollowsInstalledVector(t *testing.T) {
	s := openPGTest(t)
	ctx := context.Background()
	cc, err := pgx.ParseConfig(os.Getenv(pgTestURLEnv))
	if err != nil {
		t.Fatalf("parse %s: %v", pgTestURLEnv, err)
	}
	ver, err := pgVectorVersion(ctx, cc)
	if err != nil {
		t.Fatalf("read pgvector version: %v", err)
	}
	var got string
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(current_setting('hnsw.iterative_scan', true), '')`).Scan(&got); err != nil {
		t.Fatalf("read hnsw.iterative_scan: %v", err)
	}
	want := ""
	if pgvectorHasIterativeScan(ver) {
		want = "strict_order"
	}
	if got != want {
		t.Fatalf("hnsw.iterative_scan with pgvector %q = %q; want %q", ver, got, want)
	}
}
