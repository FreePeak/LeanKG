package projects

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
)

// TestAuthorizeSharedBasename pins the fail-closed grant. Two registered
// directories with one basename both fall outside a project:alpha grant, and
// the refusal is the one a withheld project gets, with no word about the
// shared name. A unique basename keeps the grant as before, and a caller with
// no grant reaches both.
func TestAuthorizeSharedBasename(t *testing.T) {
	base := t.TempDir()
	x := filepath.Join(base, "x", "alpha")
	y := filepath.Join(base, "y", "alpha")
	gamma := filepath.Join(base, "gamma")
	for _, d := range []string{x, y, gamma} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scoped := auth.WithAuth(context.Background(), auth.AuthContext{Scopes: []string{"project:alpha"}})

	shared := NewRouter(gamma, Config{ExtraDirs: []string{x, y}})
	for _, sel := range []string{x, y, "alpha"} {
		_, err := shared.Authorize(scoped, sel)
		if err == nil || err.Error() != auth.ProjectDenied(sel).Error() {
			t.Fatalf("Authorize(%q) with a shared basename = %v, want the withheld refusal", sel, err)
		}
	}
	if _, err := shared.Authorize(context.Background(), x); err != nil {
		t.Fatalf("no-grant caller must reach a shared-basename project; got %v", err)
	}

	unique := NewRouter(gamma, Config{ExtraDirs: []string{x}})
	if _, err := unique.Authorize(scoped, x); err != nil {
		t.Fatalf("unique basename under project:alpha = %v, want allowed", err)
	}
	if _, err := unique.Authorize(scoped, ""); err == nil {
		t.Fatalf("default gamma under project:alpha must be refused")
	}
}
