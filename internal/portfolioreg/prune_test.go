package portfolioreg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestManifestMarksGoneDirectories is the measurement behind the prune verb.
//
// On this machine's own registry (measured 2026-10-11): `query action=portfolio`
// answered count=104 while 83 of those directories no longer existed — CI
// fixtures under /private/tmp that registered themselves and were deleted with
// the run. StoreOnDisk was the only presence probe, and "no store on a missing
// dir" is indistinguishable from "registered but never indexed", so an agent
// asking "what can I query?" could not tell which of the 104 rows were real.
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
	// The row outlives its directory: this is exactly how the 83 dead rows
	// were made by CI runs that cleaned their own temp dirs.
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
