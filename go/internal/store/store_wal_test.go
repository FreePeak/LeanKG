package store

import (
	_ "modernc.org/sqlite"
	"path/filepath"
	"strings"
	"testing"
)

// TestWALModePinned pins the load-bearing claim of the writer/reader design:
// the RW handle actually runs in WAL journal mode (§6.6 of the rewrite doc).
func TestWALModePinned(t *testing.T) {
	s := openTestStore(t)
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// TestReadOnlyRejectsWrite pins the reader half: an RO handle cannot write
// even if it wanted to (query_only pragma is defense in depth).
func TestReadOnlyRejectsWrite(t *testing.T) {
	dir := t.TempDir()
	rw, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RO)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.db.Exec(`CREATE TABLE sneaky(x)`); err == nil {
		t.Fatal("RO handle accepted a write")
	}
}

// TestSpaceInPath pins DSN safety: project paths with spaces (common on
// macOS checkouts) must open correctly.
func TestSpaceInPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my project")
	s, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open with space in path: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.UpsertElements([]Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	var n int
	if err := sqlScanCount(s, &n); err != nil || n != 1 {
		t.Fatalf("count = %d err %v", n, err)
	}
}

func sqlScanCount(s *Store, n *int) error {
	return s.db.QueryRow(`SELECT COUNT(*) FROM code_elements`).Scan(n)
}
