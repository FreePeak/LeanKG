package projects

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUnknownProjectErrorIsBounded pins the size of the unknown-project
// error.
//
// The self-host registers every repository under the scan root — 286 of them
// on the machine this was measured on — and the error used to name ALL of
// them, as absolute paths. That error travels into an agent's context on
// every retry, so the wall of paths was paid for in tokens each time.
//
// The list is now bounded, and the remainder is counted rather than dropped:
// "which projects exist?" is a real question, and the answer must not be a
// 30 KB string.
func TestUnknownProjectErrorIsBounded(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "primary")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	extra := make([]string, 0, maxKnownProjectsInError+5)
	for i := 0; i < maxKnownProjectsInError+5; i++ {
		d := filepath.Join(base, fmt.Sprintf("repo-%02d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		extra = append(extra, d)
	}
	r := NewRouter(defaultDir, Config{ExtraDirs: extra})

	_, err := r.resolve("does-not-exist")
	if err == nil {
		t.Fatal("an unknown selector must error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown project") {
		t.Fatalf("error must name the unknown selector: %q", msg)
	}
	// The registry holds the default plus the extras; the error lists
	// maxKnownProjectsInError of them and counts the rest.
	want := fmt.Sprintf("(+%d more)", len(extra)+1-maxKnownProjectsInError)
	if !strings.Contains(msg, want) {
		t.Fatalf("error must count the projects it did not list (%s): %q", want, msg)
	}
	// 10 listed + the 5 counted + the default = 16 paths at most. Before the
	// bound the same error carried all 16 absolute paths.
	listed := strings.Count(msg, base)
	if listed > maxKnownProjectsInError+1 {
		t.Fatalf("error lists %d paths, want at most %d: %q", listed, maxKnownProjectsInError+1, msg)
	}
	// The rule that matters is unchanged: an unknown selector never falls
	// back to another project's data.
	if !strings.Contains(msg, "never fall back") {
		t.Fatalf("error must keep the no-fallback rule: %q", msg)
	}
}

// TestUnknownProjectErrorListsASmallRegistry keeps the useful case intact:
// with a handful of projects the error names all of them, paths included.
func TestUnknownProjectErrorListsASmallRegistry(t *testing.T) {
	base := t.TempDir()
	defaultDir := filepath.Join(base, "primary")
	dirA := filepath.Join(base, "alpha")
	for _, d := range []string{defaultDir, dirA} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRouter(defaultDir, Config{ExtraDirs: []string{dirA}})

	_, err := r.resolve("nope")
	if err == nil {
		t.Fatal("an unknown selector must error")
	}
	if !strings.Contains(err.Error(), canonical(dirA)) {
		t.Fatalf("a small registry must still be named in full: %q", err.Error())
	}
}
