package portfolioreg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestAutoSweepForgetsRowsOfVanishedDirectories is the E.1 regression: the
// registry had no automatic prune at all (Prune and Forget had exactly one
// caller each — the manual CLI verbs), so dead rows accumulated forever.
//
// On origin/main there is no AutoSweep, so this test does not even compile —
// that is the fail-before proof. Here it must forget the row whose directory
// is gone, keep every row whose directory is live (including one with no
// store at all), and touch no file.
func TestAutoSweepForgetsRowsOfVanishedDirectories(t *testing.T) {
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
	// The registry stores the CANONICAL dir (Register resolves symlinks),
	// and on macOS /tmp is a symlink to /private/tmp — so the expectation
	// must be the canonical form too, computed while the directory exists.
	vanishedCanon, err := Canonical(vanished)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(vanished); err != nil {
		t.Fatal(err)
	}

	// A real store under a live project must survive the sweep: the sweep
	// removes REGISTRY ROWS, never files.
	newStoreWithRoot(t, live, live)
	storeFile := filepath.Join(live, ".leankg", "leankg.db")

	rep, err := AutoSweep(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Removed) != 1 || rep.Removed[0] != vanishedCanon {
		t.Fatalf("sweep removed %v, want exactly the vanished dir %q", rep.Removed, vanishedCanon)
	}
	if rep.Kept != 2 {
		t.Errorf("sweep kept %d rows, want the 2 live ones", rep.Kept)
	}
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("after sweep: %d rows, want the two live ones", len(rows))
	}
	for _, r := range rows {
		if r.Dir == vanishedCanon {
			t.Errorf("the vanished dir survived the sweep")
		}
	}
	if _, err := os.Stat(storeFile); err != nil {
		t.Errorf("the sweep deleted a store file: %v", err)
	}

	// A second sweep is a no-op, not an error: a healthy registry must be
	// left exactly as it was.
	again, err := AutoSweep(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Removed) != 0 {
		t.Errorf("second sweep removed %v, want nothing", again.Removed)
	}
	if again.Kept != 2 {
		t.Errorf("second sweep kept %d rows, want 2", again.Kept)
	}
}

// TestAutoSweepOnMissingRegistryIsEmpty pins that a deployment with no
// registry has nothing to prune: an empty report, not an error. A caller
// that runs the sweep on a timer must not have to special-case "no
// registry yet".
func TestAutoSweepOnMissingRegistryIsEmpty(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	rep, err := AutoSweep(context.Background(), o)
	if err != nil {
		t.Fatalf("sweep on a missing registry: %v", err)
	}
	if len(rep.Removed) != 0 || rep.Kept != 0 {
		t.Errorf("sweep on a missing registry = %+v, want an empty report", rep)
	}
}
