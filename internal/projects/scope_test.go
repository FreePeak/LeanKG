package projects

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// hiddenFrom returns a visibility predicate that admits only the given dirs.
func hiddenFrom(visible ...string) func(string) bool {
	set := map[string]bool{}
	for _, d := range visible {
		set[canonical(d)] = true
	}
	return func(dir string) bool { return set[dir] }
}

// errBody is the error text with the caller's own selector blanked out, so
// two refusals for different selectors can be compared.
func errBody(err error, selector string) string {
	return strings.ReplaceAll(err.Error(), selector, "<selector>")
}

// TestHiddenProjectIsRefusedLikeAnUnknownOne: a project the caller may not see
// is refused with the same error as a project that does not exist, whether it
// is named by path, by file inside it, or by name. The error names only the
// projects the caller can see.
func TestHiddenProjectIsRefusedLikeAnUnknownOne(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	alpha := filepath.Join(base, "alpha")
	hidden := filepath.Join(base, "secret", "beta")
	r, _ := newFakeRouter(def, Config{ExtraDirs: []string{alpha, hidden}})
	visible := hiddenFrom(alpha)

	unknownSel := filepath.Join(base, "nowhere", "nope")
	_, unknownErr := r.resolveVisible(unknownSel, visible)
	if unknownErr == nil {
		t.Fatal("an unknown selector must be refused")
	}
	for _, sel := range []string{hidden, filepath.Join(hidden, "src", "x.go"), "beta"} {
		got, err := r.resolveVisible(sel, visible)
		if err == nil {
			t.Fatalf("hidden selector %q resolved to %s", sel, got)
		}
		if errBody(err, sel) != errBody(unknownErr, unknownSel) {
			t.Fatalf("hidden %q answered %q, want the unknown-project body %q",
				sel, errBody(err, sel), errBody(unknownErr, unknownSel))
		}
		if body := errBody(err, sel); strings.Contains(body, hidden) || strings.Contains(body, "beta (") {
			t.Fatalf("refusal names the hidden project: %v", err)
		}
	}
	// The visible project still routes by path and by name.
	if got, err := r.resolveVisible(alpha, visible); err != nil || got != canonical(alpha) {
		t.Fatalf("visible path = %s,%v", got, err)
	}
	if got, err := r.resolveVisible("alpha", visible); err != nil || got != canonical(alpha) {
		t.Fatalf("visible name = %s,%v", got, err)
	}
	// The serving process's own project is hidden too.
	if _, err := r.resolveVisible("", visible); err == nil {
		t.Fatal("the default project is hidden from this caller")
	}
}

// TestHiddenProjectDoesNotMakeAVisibleNameAmbiguous: two projects share a name,
// one visible to the caller. The visible one answers by name.
func TestHiddenProjectDoesNotMakeAVisibleNameAmbiguous(t *testing.T) {
	base := t.TempDir()
	mine := filepath.Join(base, "x", "api")
	theirs := filepath.Join(base, "y", "api")
	r, _ := newFakeRouter(t.TempDir(), Config{ExtraDirs: []string{mine, theirs}})
	visible := hiddenFrom(mine)
	if got, err := r.resolveVisible("api", visible); err != nil || got != canonical(mine) {
		t.Fatalf("name api = %s,%v; want the visible project without an ambiguity error", got, err)
	}
	if _, err := r.resolveVisible("api", nil); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("unscoped api must stay ambiguous, got %v", err)
	}
}

// TestOpenAsRefusesAHiddenProjectWithoutOpeningIt: a hidden project is never
// opened for the caller, so its store is not touched.
func TestOpenAsRefusesAHiddenProjectWithoutOpeningIt(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	hidden := filepath.Join(base, "beta")
	r, fo := newFakeRouter(def, Config{ExtraDirs: []string{hidden}})
	if _, _, err := r.OpenAs(context.Background(), hidden, hiddenFrom()); err == nil {
		t.Fatal("hidden project opened")
	}
	if n := fo.openCount(canonical(hidden)); n != 0 {
		t.Fatalf("hidden project opened %d times, want 0", n)
	}
}
