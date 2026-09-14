package obsidian

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// openStore mirrors the store-opening helper used across the engine's package
// tests: one temp project directory, migrated SQLite store.
func openStore(t *testing.T) store.Backend {
	t.Helper()
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", store.RW)
	if err != nil {
		t.Fatalf("OpenBackend: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// writeNote writes one file under dir, creating parent directories.
func writeNote(t *testing.T, dir, rel, content string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", full, err)
	}
	return full
}

// vaultFiles lists every file under the vault, relative and sorted.
func vaultFiles(t *testing.T, vault string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(vault, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(vault, p)
		if rerr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk vault: %v", err)
	}
	return out
}

func TestVaultPath(t *testing.T) {
	got := VaultPath("/proj/.leankg", "")
	if want := filepath.Join("/proj/.leankg", "obsidian", "vault"); got != want {
		t.Fatalf("default vault path = %q, want %q", got, want)
	}
	if got, want := VaultPath("/proj/.leankg", "/custom/vault"), "/custom/vault"; got != want {
		t.Fatalf("custom vault path = %q, want %q", got, want)
	}
}

func TestInitAndStatus(t *testing.T) {
	st := openStore(t)
	vault := filepath.Join(t.TempDir(), "vault")
	e := New(vault, st)

	if s, err := e.Status(); err != nil || s.Initialized || s.NoteCount != 0 {
		t.Fatalf("status before init = %+v (err %v), want uninitialized", s, err)
	}

	if err := e.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	readme := filepath.Join(vault, readmeName)
	b, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, want := range []string{
		"# LeanKG Obsidian Vault",
		"`leankg obsidian push`",
		"`leankg obsidian pull`",
		"`leankg obsidian watch`",
		"- `leankg_annotation` - Editable annotation description",
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("README missing %q:\n%s", want, b)
		}
	}

	// Init is idempotent and the README counts as a note, like the Rust status.
	if err := e.Init(); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	s, err := e.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !s.Initialized || s.NoteCount != 1 {
		t.Fatalf("status after init = %+v, want initialized with 1 markdown file", s)
	}

	writeNote(t, vault, "notes/a.md", "# A\n")
	s, err = e.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if s.NoteCount != 2 {
		t.Fatalf("NoteCount = %d, want 2", s.NoteCount)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	content := "---\r\nleankg_id: ./src/a.rs::A\r\nleankg_annotation: \"quoted: value\"\r\nleankg_relationships:\r\n---\r\n\r\n# Body\r\n"
	content = strings.ReplaceAll(content, "\r\n", "\n")
	meta, body := splitFrontmatter(content)
	if meta["leankg_id"] != "./src/a.rs::A" {
		t.Fatalf("leankg_id = %q", meta["leankg_id"])
	}
	if meta["leankg_annotation"] != `"quoted: value"` {
		t.Fatalf("annotation value must keep its quotes, got %q", meta["leankg_annotation"])
	}
	if body != "# Body" {
		t.Fatalf("body = %q, want %q", body, "# Body")
	}

	// No frontmatter at all: everything is body, no keys.
	meta, body = splitFrontmatter("# Plain\n\ntext\n")
	if len(meta) != 0 || body != "# Plain\n\ntext" {
		t.Fatalf("plain note parsed as meta=%v body=%q", meta, body)
	}
}

func TestReadAnnotationLine(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
		ok      bool
	}{
		{"quoted", "---\nleankg_annotation: \"hello\"\n---\n", "hello", true},
		{"bare", "leankg_annotation: plain\n", "plain", true},
		{"empty", "leankg_annotation: \"\"\n", "", true},
		{"absent", "---\nleankg_type: note\n---\n", "", false},
		{"indented is not the marker", "  leankg_annotation: nope\n", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := readAnnotationLine(tc.content)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("readAnnotationLine = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}
