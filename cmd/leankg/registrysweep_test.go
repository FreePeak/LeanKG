package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/portfolioreg"
)

// TestSweepRegistryForgetsVanishedProjects is the CALLER regression: the
// registry sweep must actually FIRE on the serve path's maintenance tick.
//
// The registry is the server-side list of what the deployment serves, and
// nothing pruned it automatically: a row for a project that left the fleet
// outlived the project forever. The sweep exists (internal/portfolioreg) and
// the serve path calls it (cmd/leankg/main.go, sweepRegistry) — this test
// pins the second half, so a serve path that stops sweeping fails here
// rather than in production, where nothing would notice for a month.
func TestSweepRegistryForgetsVanishedProjects(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv("LEANKG_PORTFOLIO_DB", registry)
	ctx := context.Background()

	// A project directory that is alive, and one that is gone.
	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{"live": live, "gone": gone} {
		if _, err := portfolioreg.Register(ctx, portfolioreg.Options{DBPath: registry}, dir, name, 0, 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	goneCanon, err := portfolioreg.Canonical(gone)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	// The call the serve path makes on every maintenance tick.
	sweepRegistry(ctx, "sqlite")

	rows, err := portfolioreg.Projects(ctx, portfolioreg.Options{DBPath: registry})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after the sweep: %d registry rows, want 1 (the live project)", len(rows))
	}
	for _, r := range rows {
		if r.Dir == goneCanon {
			t.Errorf("the vanished project survived the sweep")
		}
	}
}

// TestSweepRegistryKeepsLiveProjects pins the other half of the rule on the
// CALLER: a project that is still on disk keeps its row, whether or not it
// has a store. A sweep that dropped every storeless row would unregister a
// live project the fleet may index later.
func TestSweepRegistryKeepsLiveProjects(t *testing.T) {
	registry := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv("LEANKG_PORTFOLIO_DB", registry)
	ctx := context.Background()

	// A live project with no store: registered, never indexed.
	live := t.TempDir()
	if _, err := portfolioreg.Register(ctx, portfolioreg.Options{DBPath: registry}, live, "live", 0, 0, nil); err != nil {
		t.Fatal(err)
	}

	sweepRegistry(ctx, "sqlite")

	rows, err := portfolioreg.Projects(ctx, portfolioreg.Options{DBPath: registry})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after the sweep: %d registry rows, want 1 (the live project)", len(rows))
	}
}

// TestServePathSweepsTheRegistry pins that the serve path CALLS the sweep.
//
// The sweep function working is not the same as the sweep FIRING: a serve
// path that never calls it leaves the registry exactly as dead as before,
// and nothing would notice for a month. The serve path is not runnable in
// isolation (it binds listeners and blocks), so this reads the source —
// the same guard shape internal/index's label guard uses.
//
// The call must sit inside the maintenance tick, so the registry is swept
// as often as the fleet is maintained.
func TestServePathSweepsTheRegistry(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)
	if !strings.Contains(body, "sweepRegistry(ctx, eng)") {
		t.Error("the serve path never calls sweepRegistry — the registry sweep would never fire")
	}
	// The call must be on the maintenance tick, not a one-off at start-up:
	// a start-up-only sweep would prune the rows that appear later.
	if !strings.Contains(body, "maintain.RunFleet(ctx, func() []maintain.Target {") {
		t.Error("the maintenance tick no longer has the fleet target callback the sweep rides")
	}
	// The sweep must be inside that callback, not somewhere else in the
	// function: the call site is what decides whether it runs per tick.
	idx := strings.Index(body, "maintain.RunFleet(ctx, func() []maintain.Target {")
	if idx < 0 {
		t.Fatal("no maintain.RunFleet call in main.go")
	}
	tail := body[idx:]
	if end := strings.Index(tail, "}, maintain.Options{"); end > 0 {
		tail = tail[:end]
	}
	if !strings.Contains(tail, "sweepRegistry(ctx, eng)") {
		t.Error("sweepRegistry is not called inside the maintenance tick — it would not run once per tick")
	}
}
