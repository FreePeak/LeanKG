package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostedErrorRegistry registers a default and two projects under one base
// directory. mine is the caller's (visible) project, theirs belongs to another
// org and must never be named to the caller.
func hostedErrorRegistry(t *testing.T, hosted bool) (r *Router, base, mine string) {
	t.Helper()
	base = t.TempDir()
	def := filepath.Join(base, "primary")
	mine = filepath.Join(base, "org-a", "mine")
	theirs := filepath.Join(base, "org-b", "theirs")
	for _, d := range []string{def, mine, theirs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r = NewRouter(def, Config{ExtraDirs: []string{mine, theirs}, Hosted: hosted})
	return r, base, mine
}

// TestHostedUnknownProjectErrorNamesOnlyTheCallersProjects: a hosted server
// answers an unknown selector with the NAMES of the caller's own projects and
// no absolute path, so the data-root layout never reaches the caller.
func TestHostedUnknownProjectErrorNamesOnlyTheCallersProjects(t *testing.T) {
	r, base, _ := hostedErrorRegistry(t, true)
	_, err := r.resolveVisible("does-not-exist", hiddenFrom(filepath.Join(base, "org-a", "mine")))
	if err == nil {
		t.Fatal("an unknown selector must error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown project") || !strings.Contains(msg, "never fall back") {
		t.Fatalf("error lost its shape: %q", msg)
	}
	if !strings.Contains(msg, "mine") {
		t.Fatalf("error must name the caller's own project: %q", msg)
	}
	if strings.Contains(msg, base) || strings.Contains(msg, "/") {
		t.Fatalf("hosted error reveals a path: %q", msg)
	}
	if strings.Contains(msg, "theirs") || strings.Contains(msg, "primary") {
		t.Fatalf("hosted error names a project the caller cannot see: %q", msg)
	}
}

// TestHostedUnknownProjectErrorWithNoVisibleProjects: a caller whose org sees
// nothing gets no list at all, and no path.
func TestHostedUnknownProjectErrorWithNoVisibleProjects(t *testing.T) {
	r, base, _ := hostedErrorRegistry(t, true)
	_, err := r.resolveVisible("does-not-exist", hiddenFrom())
	if err == nil {
		t.Fatal("an unknown selector must error")
	}
	msg := err.Error()
	if strings.Contains(msg, base) || strings.Contains(msg, "/") ||
		strings.Contains(msg, "mine") || strings.Contains(msg, "theirs") || strings.Contains(msg, "primary") {
		t.Fatalf("hosted error leaks projects or paths to a caller with none visible: %q", msg)
	}
}

// TestHostedAmbiguousNameListsNoPaths: the ambiguity error names the
// candidates as paths on a standalone server (kept, see the next test); on a
// hosted server it names no path, because the caller would learn the layout.
func TestHostedAmbiguousNameListsNoPaths(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "x", "same")
	b := filepath.Join(base, "y", "same")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRouter(filepath.Join(base, "primary"), Config{ExtraDirs: []string{a, b}, Hosted: true})
	_, err := r.resolveVisible("same", nil)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("a shared name must be ambiguous, got %v", err)
	}
	if msg := err.Error(); strings.Contains(msg, base) || strings.Contains(msg, "/") {
		t.Fatalf("hosted ambiguity error reveals a path: %q", msg)
	}
}

// TestNonHostedUnknownProjectErrorStillListsPaths: self-hosters rely on the
// path hints, so a standalone registry keeps naming projects with their dirs.
func TestNonHostedUnknownProjectErrorStillListsPaths(t *testing.T) {
	r, _, mine := hostedErrorRegistry(t, false)
	_, err := r.resolve("does-not-exist")
	if err == nil {
		t.Fatal("an unknown selector must error")
	}
	if msg := err.Error(); !strings.Contains(msg, canonical(mine)) {
		t.Fatalf("standalone error must keep its path hints: %q", msg)
	}
}
