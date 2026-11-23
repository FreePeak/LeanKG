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
		for _, n := range findConflictMarkers(strings.Split(string(data), "\n")) {
			found = append(found, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), n))
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

// findConflictMarkers returns the 1-based line numbers of git conflict markers
// in lines: lines that begin with "<<<<<<<" or ">>>>>>>", and the "======="
// separator, but only between an open "<<<<<<<" and its ">>>>>>>".
func findConflictMarkers(lines []string) []int {
	var found []int
	open := false
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "<<<<<<<"):
			open = true
			found = append(found, i+1)
		case strings.HasPrefix(line, ">>>>>>>"):
			open = false
			found = append(found, i+1)
		case line == "=======" && open:
			found = append(found, i+1)
		}
	}
	return found
}

// TestConflictMarkerDetection pins what counts as a conflict marker. A bare
// "=======" line is also a Markdown setext H1 underline, so it is a marker only
// inside an open "<<<<<<<" block, the way git writes a conflict.
func TestConflictMarkerDetection(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []int
	}{
		{
			name:  "setext H1 underline is not a marker",
			lines: []string{"Title", "=======", "", "body"},
			want:  nil,
		},
		{
			name:  "full git conflict block is marked",
			lines: []string{"<<<<<<< HEAD", "ours", "=======", "theirs", ">>>>>>> branch"},
			want:  []int{1, 3, 5},
		},
		{
			name:  "separator after a closed block is not a marker",
			lines: []string{"<<<<<<< HEAD", "ours", ">>>>>>> branch", "Title", "======="},
			want:  []int{1, 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findConflictMarkers(tc.lines)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("findConflictMarkers = %v, want %v", got, tc.want)
			}
		})
	}
}
