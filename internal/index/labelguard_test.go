package index

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// labelRowRe matches one row of a label table in docs/retrieval-label-set.md:
// "| 1 | question | `path::Symbol` | where |".
var labelRowRe = regexp.MustCompile("(?m)^\\| (\\d+) \\| (.+?) \\| `([^`]+)` \\| (.+?) \\|$")

// TestRetrievalLabelSetPointsAtExtractableSymbols is the guard on the
// retrieval label set: every ground truth in docs/retrieval-label-set.md must
// still be a symbol the indexer actually extracts from the file it names.
//
// The label set is the ground truth of every retrieval measurement the project
// has made. A label that names a symbol the extractor no longer emits is not a
// retrieval miss — it is a broken measurement, and the bench reports it as
// STALE (excluded from every denominator). Measured live on 2026-10-20, after
// #544 deleted the store's `FuseRRF` wrapper: label 1 still named
// `internal/store/pg_fts.go::FuseRRF`, so the bench scored 83 of 84 labels and
// reported the 84th as STALE. The measurement was wrong, not the engine.
//
// The check runs the REAL extractor over the real file, so it tracks whatever
// the indexer does — including the tree-sitter tier, which emits a different
// element set for Go. It is therefore not a "does this file contain the
// string" check: a symbol that survives as text but is no longer emitted (a
// renamed type, a symbol that moved to another file, a method that lost its
// receiver) fails here.
//
// The extractor is exercised through the same seam the indexer uses
// (extractFileAs → toStore), so a label pointing at a symbol the indexer
// cannot produce fails this test rather than silently skewing a measurement.
func TestRetrievalLabelSetPointsAtExtractableSymbols(t *testing.T) {
	root := filepath.Join("..", "..")
	path := filepath.Join(root, "docs", "retrieval-label-set.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read label set: %v", err)
	}
	rows := labelRowRe.FindAllStringSubmatch(string(raw), -1)
	if len(rows) == 0 {
		t.Fatalf("no label rows parsed from %s — the table shape changed and this guard would pass on an empty set", path)
	}
	for _, m := range rows {
		num, question, qn := m[1], m[2], m[3]
		file, _, ok := strings.Cut(qn, "::")
		if !ok {
			t.Errorf("label %s (%q): qualified name %q has no \"::\" separator", num, question, qn)
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Errorf("label %s (%q): file %s named by %q does not exist: %v", num, question, file, qn, err)
			continue
		}
		fe := extractFileAs(file, src, "go")
		found := false
		for _, e := range fe.toStore() {
			if e.QualifiedName == qn {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("label %s (%q): %q is not extracted from %s — the label is STALE, so every measurement that uses it is wrong until the label is repointed at a symbol the indexer still emits", num, question, qn, file)
		}
	}
}
