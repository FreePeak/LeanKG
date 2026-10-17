package portfolioreg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestManifestMarksGoneDirectories pins dir_exists: 84 of 108 rows (2026-10-11) had a deleted directory.
func TestManifestMarksGoneDirectories(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "vanished")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 0)
	if _, err := Register(ctx, o, live, "live", 12, 3, &stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, gone, "gone", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	// The row outlives its directory: this is how CI runs left 84 dead rows behind.
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	entries, err := Manifest(ctx, o, nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ManifestEntry{}
	for _, e := range entries {
		byName[e.Project] = e
	}
	if !byName["live"].DirExists {
		t.Errorf("live row reports dir_exists=false")
	}
	if byName["gone"].DirExists {
		t.Errorf("row for a deleted directory reports dir_exists=true — the manifest cannot say which rows are real")
	}
}

// TestPruneForgetsOnlyVanishedDirectories pins what a destructive sweep must
// and must not take. A never-indexed project (dir present, no store) is a
// legitimate registry row and stays; only a directory that is GONE goes.
func TestPruneForgetsOnlyVanishedDirectories(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	live := t.TempDir()
	never := t.TempDir() // on disk, no .leankg: registered but never indexed
	vanished := filepath.Join(t.TempDir(), "vanished")
	if err := os.MkdirAll(vanished, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"live": live, "never": never, "vanished": vanished} {
		if _, err := Register(ctx, o, dir, name, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The registry stores the CANONICAL dir (Register resolves symlinks), and
	// on macOS /tmp is a symlink to /private/tmp — so the expectation must be
	// the canonical form too, computed while the directory still exists.
	vanishedCanon, err := Canonical(vanished)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(vanished); err != nil {
		t.Fatal(err)
	}

	// Dry run first: it must name the casualty and change nothing.
	would, err := Prune(ctx, o, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(would) != 1 || would[0] != vanishedCanon {
		t.Fatalf("dry run = %v, want exactly the vanished dir", would)
	}
	if rows, _ := Projects(ctx, o); len(rows) != 3 {
		t.Fatalf("dry run changed the registry: %d rows", len(rows))
	}

	gone, err := Prune(ctx, o, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(gone) != 1 || gone[0] != vanishedCanon {
		t.Fatalf("prune = %v, want exactly the vanished dir", gone)
	}
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("after prune: %d rows, want the two live ones", len(rows))
	}
	for _, r := range rows {
		if r.Dir == vanished {
			t.Errorf("the vanished dir survived the prune")
		}
	}
	// A second prune is a no-op, not an error.
	again, err := Prune(ctx, o, false)
	if err != nil || len(again) != 0 {
		t.Fatalf("second prune = %v, %v; want nothing and no error", again, err)
	}
}

// TestPruneKeepsRowsWhoseDirectoryCannotBeStatted pins that only a directory
// proven absent is gone. A stat that fails for another reason (here EACCES, from
// a locked parent) says nothing about the directory, and the sweep is
// destructive, so the row stays.
func TestPruneKeepsRowsWhoseDirectoryCannotBeStatted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so the stat cannot fail")
	}
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	parent := t.TempDir()
	dir := filepath.Join(parent, "locked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, dir, "locked", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	if would, err := Prune(ctx, o, true); err != nil || len(would) != 0 {
		t.Errorf("dry run = %v, %v; want nothing: the directory exists, it is only unreadable", would, err)
	}
	if gone, err := Prune(ctx, o, false); err != nil || len(gone) != 0 {
		t.Errorf("apply = %v, %v; want nothing forgotten", gone, err)
	}
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("registry has %d rows after apply; want the unreadable project kept", len(rows))
	}
	if entries, err := Manifest(ctx, o, nil); err != nil || len(entries) != 1 || !entries[0].DirExists {
		t.Errorf("manifest = %+v, %v; want the unreadable directory reported as present", entries, err)
	}
}
