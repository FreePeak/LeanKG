package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/maintain"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
)

// projectWithMemory builds a project directory that has BOTH a store and a
// memory index. The bank file holds a duplicated body, the way a bank
// written before the content identity existed looks, so a compaction that
// reaches the project collapses it.
func projectWithMemory(t *testing.T, dir string) {
	t.Helper()
	m, err := memory.Open(dir, false)
	if err != nil {
		t.Fatalf("open memory %s: %v", dir, err)
	}
	t.Cleanup(func() { m.Close() })
	body := "a retained row for " + dir
	rows := []memory.Entry{{ID: "r1", Content: body}, {ID: "r2", Content: body}}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatalf("encode fixture row: %v", err)
		}
	}
	banks := filepath.Join(dir, ".leankg", "memory", "banks")
	if err := os.MkdirAll(banks, 0o755); err != nil {
		t.Fatalf("create banks dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(banks, "bank.jsonl"), []byte(buf.String()), 0o644); err != nil {
		t.Fatalf("write fixture bank: %v", err)
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store %s: %v", dir, err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate store %s: %v", dir, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// bankRowCount counts the rows in a project's memory bank file.
func bankRowCount(t *testing.T, dir string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, ".leankg", "memory", "banks", "bank.jsonl"))
	if err != nil {
		t.Fatalf("read bank %s: %v", dir, err)
	}
	n := 0
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// TestFleetTargetsCompactEveryProjectMemory pins C.4: the fleet set Mem
// only for the default project, so the routed projects' memory indexes were
// never compacted — measured: xdev/.leankg/memory/index.db sat at 4 KB plus
// a 124.8 KB WAL, untouched for a week.
//
// One fleet pass now compacts the memory index of EVERY project that has
// one.
func TestFleetTargetsCompactEveryProjectMemory(t *testing.T) {
	ctx := context.Background()
	defaultDir := t.TempDir()
	projectWithMemory(t, defaultDir)
	routed1, routed2 := t.TempDir(), t.TempDir()
	projectWithMemory(t, routed1)
	projectWithMemory(t, routed2)

	// The routed projects start with a duplicated bank row the pass has to
	// collapse.
	for _, d := range []string{routed1, routed2} {
		if n := bankRowCount(t, d); n != 2 {
			t.Fatalf("precondition: %s holds %d bank rows, want 2", d, n)
		}
	}

	st, err := store.Open(filepath.Join(defaultDir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open default store: %v", err)
	}
	defer st.Close()
	mem, err := memory.Open(defaultDir, false)
	if err != nil {
		t.Fatalf("open default memory: %v", err)
	}
	defer mem.Close()

	targets := fleetTargets(ctx, defaultDir, st, mem, []string{routed1, routed2}, store.EngineSQLite, nil)
	if len(targets) != 3 {
		t.Fatalf("fleet has %d targets, want 3 (default + 2 routed)", len(targets))
	}
	for _, tg := range targets[1:] {
		if tg.Mem == nil {
			t.Fatalf("routed target %q has no memory layer: its index is never compacted", tg.Label)
		}
	}

	// One pass over the fleet.
	opts := maintain.Options{Logf: t.Logf}
	for _, tg := range targets {
		if _, err := maintain.Pass(tg.Store, opts); err != nil {
			t.Fatalf("pass %s: %v", tg.Label, err)
		}
		maintain.CompactMemory(tg.Mem, opts)
		if tg.Close != nil {
			tg.Close()
		}
	}

	for _, d := range []string{routed1, routed2} {
		if n := bankRowCount(t, d); n != 1 {
			t.Fatalf("%s holds %d bank rows after the pass, want 1 (the duplicate collapsed)", d, n)
		}
	}
}

// TestFleetTargetsSkipProjectsWithoutAMemoryIndex: a project that never
// used memory must not grow a memory tree because a maintenance pass ran.
func TestFleetTargetsSkipProjectsWithoutAMemoryIndex(t *testing.T) {
	ctx := context.Background()
	defaultDir := t.TempDir()
	projectWithMemory(t, defaultDir)
	plain := t.TempDir()
	st, err := store.Open(filepath.Join(plain, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st.Close()

	mem, err := memory.Open(defaultDir, false)
	if err != nil {
		t.Fatalf("open default memory: %v", err)
	}
	defer mem.Close()
	defStore, err := store.Open(filepath.Join(defaultDir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open default store: %v", err)
	}
	defer defStore.Close()

	targets := fleetTargets(ctx, defaultDir, defStore, mem, []string{plain}, store.EngineSQLite, nil)
	for _, tg := range targets[1:] {
		if tg.Mem != nil {
			t.Fatalf("target %q was given a memory layer for a project with no memory index", tg.Label)
		}
		if tg.Close != nil {
			tg.Close()
		}
	}
	if _, err := os.Stat(filepath.Join(plain, ".leankg", "memory")); !os.IsNotExist(err) {
		t.Fatalf("the fleet created a memory tree for a project that has none: %v", err)
	}
}
