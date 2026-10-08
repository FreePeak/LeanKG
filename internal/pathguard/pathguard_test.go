package pathguard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	outsideDir := t.TempDir()
	secret := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "escape.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "pkg", "a.go"), filepath.Join(root, "inside.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "outdir")); err != nil {
		t.Fatal(err)
	}

	ok := []struct{ in, rel string }{
		{"pkg/a.go", "pkg/a.go"},
		{"./pkg/a.go", "pkg/a.go"},
		{filepath.Join(root, "pkg", "a.go"), "pkg/a.go"},
		{"pkg/../pkg/a.go", "pkg/a.go"},
		{"inside.go", "inside.go"}, // symlink that stays inside
	}
	for _, c := range ok {
		abs, rel, err := Resolve(root, c.in)
		if err != nil {
			t.Errorf("Resolve(%q) = %v, want ok", c.in, err)
			continue
		}
		if rel != c.rel || !filepath.IsAbs(abs) {
			t.Errorf("Resolve(%q) = (%q, %q), want rel %q", c.in, abs, rel, c.rel)
		}
	}

	refused := []string{
		secret,       // absolute outside
		"/etc/hosts", // absolute outside
		"../" + filepath.Base(outsideDir) + "/secret.txt",
		"pkg/../../x",       // climb
		"escape.txt",        // symlink leaving the root
		"outdir/secret.txt", // directory symlink leaving the root
	}
	for _, in := range refused {
		if _, _, err := Resolve(root, in); !IsOutside(err) {
			t.Errorf("Resolve(%q) = %v, want PATH_OUTSIDE_PROJECT", in, err)
		}
	}

	if _, _, err := Resolve(root, "pkg/missing.go"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing file = %v, want ErrNotFound", err)
	}
	if _, _, err := Resolve(root, ""); err == nil {
		t.Error("empty path accepted")
	}
}

// TestResolveAliasedRoot pins the macOS /tmp -> /private/tmp split: a server
// rooted at the real path must accept a client path spelled through a symlink
// alias of the root, and still refuse an alias that leaves it.
func TestResolveAliasedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if _, rel, err := Resolve(root, filepath.Join(alias, "a.go")); err != nil || rel != "a.go" {
		t.Fatalf("Resolve(<alias>/a.go) = (%q, %v), want a.go", rel, err)
	}
	if _, _, err := Resolve(filepath.Join(root, "missing-subdir"), filepath.Join(alias, "a.go")); err == nil {
		t.Fatal("alias resolving outside a narrower root was accepted")
	}
}
