package projects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkIndexedDir makes dir a directory with a store file, the shape `leankg index`
// leaves behind.
func mkIndexedDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".leankg", "leankg.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// logRecorder collects the router's log lines.
type logRecorder struct{ lines []string }

func (l *logRecorder) logf(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

// TestRestoreRoutesOnlyClaimedDirsThatAreStillIndexed: an indexed claim is
// routable again, a directory that is gone and a directory with no store are
// skipped, and the skip log names the project but not its path.
func TestRestoreRoutesOnlyClaimedDirsThatAreStillIndexed(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	indexed := filepath.Join(base, "indexed")
	unindexed := filepath.Join(base, "unindexed")
	gone := filepath.Join(base, "gone")
	mkIndexedDir(t, indexed)
	if err := os.MkdirAll(unindexed, 0o755); err != nil {
		t.Fatal(err)
	}
	var logs logRecorder
	r, _ := newFakeRouter(def, Config{Hosted: true, Logf: logs.logf})

	if n := r.Restore(context.Background(), []string{indexed, unindexed, gone}); n != 1 {
		t.Fatalf("Restore = %d, want 1 (only the indexed claim)", n)
	}
	if got, err := r.resolve(indexed); err != nil || got != canonical(indexed) {
		t.Fatalf("restored project must route: %s, %v", got, err)
	}
	if _, err := r.resolve(unindexed); err == nil {
		t.Fatal("an unindexed claim must not be routable")
	}
	if _, err := r.resolve(gone); err == nil {
		t.Fatal("a claim whose directory is gone must not be routable")
	}
	joined := strings.Join(logs.lines, "\n")
	for _, name := range []string{"unindexed", "gone"} {
		if !strings.Contains(joined, name) {
			t.Fatalf("skip log must name %q, got:\n%s", name, joined)
		}
	}
	if strings.Contains(joined, base) {
		t.Fatalf("skip log must not print a project path, got:\n%s", joined)
	}
	if _, release, err := r.Open(context.Background(), indexed); err != nil {
		t.Fatalf("restored project must open: %v", err)
	} else {
		release()
	}
}

// TestRestoreCountsEachProjectOnce: the same directory claimed under several
// spellings (trailing slash, symlink) registers once.
func TestRestoreCountsEachProjectOnce(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	indexed := filepath.Join(base, "indexed")
	link := filepath.Join(base, "link")
	mkIndexedDir(t, indexed)
	if err := os.Symlink(indexed, link); err != nil {
		t.Fatal(err)
	}
	r, _ := newFakeRouter(def, Config{Hosted: true})

	if n := r.Restore(context.Background(), []string{indexed, indexed + string(filepath.Separator), link, indexed}); n != 1 {
		t.Fatalf("Restore = %d, want 1 for one project under four spellings", n)
	}
	if got := r.List(); len(got) != 2 || got[1] != canonical(indexed) {
		t.Fatalf("List = %v, want [default indexed] once", got)
	}
}

// TestRestoreIsANoOpOutsideHostedMode: a non-hosted router keeps its registry
// as it is; restore never adds a claim there.
func TestRestoreIsANoOpOutsideHostedMode(t *testing.T) {
	base := t.TempDir()
	def := filepath.Join(base, "primary")
	indexed := filepath.Join(base, "indexed")
	mkIndexedDir(t, indexed)
	r, _ := newFakeRouter(def, Config{})

	if n := r.Restore(context.Background(), []string{indexed}); n != 0 {
		t.Fatalf("non-hosted Restore = %d, want 0", n)
	}
	if got := r.List(); len(got) != 1 {
		t.Fatalf("non-hosted List = %v, want the default only", got)
	}
}
