package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

func TestLoadParsesAssignments(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	writeFile(t, path, "# comment\n\nFOO=bar\nexport BAZ=\"qux quux\"\n  SPACED =  'single'  \nEMPTY=\n", 0o600)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]string{"FOO": "bar", "BAZ": "qux quux", "SPACED": "single", "EMPTY": ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, want %v", got, want)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("Load of missing file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Load of missing file = %v, want empty", got)
	}
}

func TestAppendMissingPreservesExistingBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "# keep me\nOTHER=1\n"
	writeFile(t, path, orig, 0o644)

	err := AppendMissing(path, []Pair{
		{Key: "LEANKG_ADMIN_USERNAME", Value: "admin"},
		{Key: "LEANKG_ADMIN_PASSWORD", Value: "s3cret"},
	})
	if err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	want := orig + "LEANKG_ADMIN_USERNAME=admin\nLEANKG_ADMIN_PASSWORD=s3cret\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}

func TestAppendMissingAddsNewlineWhenMissingAtEOF(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	writeFile(t, path, "OTHER=1", 0o600)

	if err := AppendMissing(path, []Pair{{Key: "NEW", Value: "v"}}); err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	if got, want := readFile(t, path), "OTHER=1\nNEW=v\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestAppendMissingSkipsKeysAlreadyPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "LEANKG_ADMIN_PASSWORD='kept'\nOTHER=1\n"
	writeFile(t, path, orig, 0o644)

	if err := AppendMissing(path, []Pair{
		{Key: "LEANKG_ADMIN_PASSWORD", Value: "replacement"},
		{Key: "LEANKG_ADMIN_USERNAME", Value: "admin"},
	}); err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	want := orig + "LEANKG_ADMIN_USERNAME=admin\n"
	if got := readFile(t, path); got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestAppendMissingNoopWhenNothingMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "A=1\nB=2\n"
	writeFile(t, path, orig, 0o644)

	if err := AppendMissing(path, []Pair{{Key: "A", Value: "x"}}); err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("content changed to %q", got)
	}
	if mode := modeOf(t, path); mode != 0o644 {
		t.Fatalf("mode = %o, want untouched 644 when nothing was written", mode)
	}
}

func TestAppendMissingCreatesAbsentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	if err := AppendMissing(path, []Pair{{Key: "K", Value: "v"}}); err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	if got := readFile(t, path); got != "K=v\n" {
		t.Fatalf("content = %q", got)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %d entries in dir", len(entries))
	}
}

func TestAppendMissingQuotesUnsafeValuesAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	value := "a b#c"
	if err := AppendMissing(path, []Pair{{Key: "K", Value: value}}); err != nil {
		t.Fatalf("AppendMissing: %v", err)
	}
	if got := readFile(t, path); got != "K='a b#c'\n" {
		t.Fatalf("content = %q", got)
	}
	back, err := Load(path)
	if err != nil || back["K"] != value {
		t.Fatalf("round trip = %q, %v; want %q", back["K"], err, value)
	}
}

func TestAppendMissingRejectsUnwritableValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	orig := "OTHER=1\n"
	writeFile(t, path, orig, 0o600)

	for _, bad := range []string{"it's", "two\nlines"} {
		if err := AppendMissing(path, []Pair{{Key: "K", Value: bad}}); err == nil {
			t.Fatalf("value %q must be rejected", bad)
		}
	}
	if got := readFile(t, path); got != orig {
		t.Fatalf("rejected write changed the file to %q", got)
	}
}
