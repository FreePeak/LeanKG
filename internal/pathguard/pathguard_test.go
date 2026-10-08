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
