package archtest

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDocsHaveNoConflictMarkers fails when any file under docs/ carries a git
// merge conflict marker at the start of a line. A marker that reaches main is
// invisible to the Go gates, so this is the only check that catches it.
func TestDocsHaveNoConflictMarkers(t *testing.T) {
	root := repoRoot(t)
	docs := filepath.Join(root, "docs")
	var found []string
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if isConflictMarker(line) {
				found = append(found, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan docs: %v", err)
	}
	for _, loc := range found {
		t.Errorf("conflict marker at %s: resolve the merge and remove the marker line", loc)
	}
}

// isConflictMarker reports whether line is a git conflict marker: it begins
// with "<<<<<<<" or ">>>>>>>", or it is the "=======" separator.
func isConflictMarker(line string) bool {
	line = strings.TrimSuffix(line, "\r")
	return strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, ">>>>>>>") || line == "======="
}
