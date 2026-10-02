package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheFreshnessWarnNamesTheFilesAndTheRoot pins the last diagnosis surface
// the loop's convergence applies to: `doctor --deep`'s index-freshness finding.
//
// Wave 20 ended on a rule — **a diagnosis must name the thing it looked at, or
// it cannot be acted on** — and this is the largest diagnosis in the product
// still not honouring it. The check compares the files it recorded against the
// files on disk and, when they disagree, reports:
//
//	2 missing file(s) not indexed, 0 stale (0%)
//	Run `leankg index` (or watch mode) to refresh; a large delta usually means
//	the index was built from a different root or env.
//
// Three things are missing, and all three are things the check already knows.
// It walked the tree: it can print the two paths. It ran against
// `env.ProjectRoot`: it never says which root. And the hint's own escape hatch —
// "a large delta usually means the index was built from a different root" — is
// exactly the case it cannot let the reader rule out, because the root is not
// in the message. The STALE branch above it does the right thing (it samples
// three paths and names them); this branch throws away the same information it
// had one case earlier, and the WARN is the one users see most often.
//
// This is wave 1's defect at the reporting layer rather than the opening one:
// the store resolved correctly, and the report still could not tell the reader
// whether it was looking at the right thing.
func TestTheFreshnessWarnNamesTheFilesAndTheRoot(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"a.go", "b.go", "extra1.go", "extra2.go", "notes.md"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The index recorded only a.go and b.go: three files are on disk unrecorded.
	probes := &stubProbes{
		recorded: []string{"a.go", "b.go"},
		indexed:  []string{"a.go::A", "b.go::B"},
	}
	disk := []string{"a.go", "b.go", "extra1.go", "extra2.go", "notes.md"}

	f := checkIndexFreshness(probes, Env{ProjectRoot: root, DiskFiles: disk})
	if f.Status != StatusWarn {
		t.Fatalf("fixture must WARN, got %s (%s)", f.Status, f.Detail)
	}
	// It must name the root it walked — otherwise "the index was built from a
	// different root" is untestable by the reader.
	if !strings.Contains(f.Detail, root) && !strings.Contains(f.Hint, root) {
		t.Errorf("the finding must name the project root it compared against:\n  detail: %s\n  hint: %s", f.Detail, f.Hint)
	}
	// And it must name the files, the way the sibling STALE branch does.
	if !strings.Contains(f.Detail, "extra1.go") || !strings.Contains(f.Detail, "extra2.go") {
		t.Errorf("the finding must name the unindexed files:\n  detail: %s", f.Detail)
	}
	// With many, the sample is capped but still non-empty — a count alone is
	// what this branch already had and is not enough to act on.
	if !strings.Contains(f.Detail, "notes.md") {
		t.Errorf("the finding must sample the unindexed files:\n  detail: %s", f.Detail)
	}
}
