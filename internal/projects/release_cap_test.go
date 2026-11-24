package projects

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// openCount is the number of routed projects the router holds open.
func openCount(r *Router) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openProjectsLocked()
}

// TestCapHoldsWhenLeasesAreReleased: six projects are opened at once under live
// leases, so the cap cannot evict any of them while they are in use. When the
// leases are released the open count must converge to the cap, not stay at six.
func TestCapHoldsWhenLeasesAreReleased(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	var dirs []string
	for i := 0; i < 6; i++ {
		dirs = append(dirs, filepath.Join(base, string(rune('a'+i))))
	}
	r, _ := newFakeRouter(def, Config{ExtraDirs: dirs, MaxOpen: 2})

	ctx := context.Background()
	var opened sync.WaitGroup
	gate := make(chan struct{})
	var released sync.WaitGroup
	for _, d := range dirs {
		opened.Add(1)
		released.Add(1)
		go func(d string) {
			defer released.Done()
			_, release, err := r.Open(ctx, d)
			opened.Done()
			if err != nil {
				t.Errorf("open %s: %v", d, err)
				return
			}
			<-gate
			release()
		}(d)
	}
	opened.Wait()
	if n := openCount(r); n != 6 {
		t.Fatalf("with all six leased the router holds %d open, want 6 (no lease may be evicted)", n)
	}
	close(gate)
	released.Wait()

	if n := openCount(r); n > 2 {
		t.Fatalf("after every lease is released %d projects stay open, want at most the cap of 2", n)
	}
}

// TestCapEvictsIdleLeastRecentlyUsedOnRelease: the release that brings the count
// over the cap closes the idle project used longest ago, and the two most
// recently released stay open.
func TestCapEvictsIdleLeastRecentlyUsedOnRelease(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	var dirs []string
	for _, n := range []string{"r1", "r2", "r3", "r4", "r5", "r6"} {
		dirs = append(dirs, filepath.Join(base, n))
	}
	r, fo := newFakeRouter(def, Config{ExtraDirs: dirs, MaxOpen: 2})
	ctx := context.Background()
	releases := make([]func(), len(dirs))
	for i, d := range dirs {
		_, rel, err := r.Open(ctx, d)
		if err != nil {
			t.Fatal(err)
		}
		releases[i] = rel
	}
	for _, rel := range releases {
		rel()
	}
	if n := openCount(r); n != 2 {
		t.Fatalf("open after releasing all six = %d, want 2", n)
	}
	for _, d := range dirs[:4] {
		if n := closedCount(t, fo, d); n != 1 {
			t.Fatalf("%s closed %d times, want 1 (idle, released before the last two)", filepath.Base(d), n)
		}
	}
	for _, d := range dirs[4:] {
		if n := closedCount(t, fo, d); n != 0 {
			t.Fatalf("%s closed %d times, want 0 (most recently released)", filepath.Base(d), n)
		}
	}
}

// TestReleaseNeverEvictsALiveLease: with cap 1, a project still leased survives
// the release of another project that pushes the count over the cap.
func TestReleaseNeverEvictsALiveLease(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{a, b}, MaxOpen: 1})
	ctx := context.Background()

	_, releaseA, err := r.Open(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	_, releaseB, err := r.Open(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	releaseB()
	if n := closedCount(t, fo, a); n != 0 {
		t.Fatalf("leased project a closed %d times on release of b", n)
	}
	if n := closedCount(t, fo, b); n != 1 {
		t.Fatalf("idle project b closed %d times, want 1 (over the cap)", n)
	}
	releaseA()
	if n := closedCount(t, fo, a); n != 0 {
		t.Fatalf("a closed %d times after its own release, want 0 (within the cap)", n)
	}
}
