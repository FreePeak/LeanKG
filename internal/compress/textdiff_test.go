package compress

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func lineNums(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s%d\n", prefix, i)
	}
	return b.String()
}

func TestUnifiedDiffIdentical(t *testing.T) {
	if got := UnifiedDiff("a\nb\n", "a\nb\n", 3); got != "" {
		t.Errorf("identical inputs should emit no diff, got:\n%s", got)
	}
	if got := UnifiedDiff("", "", 3); got != "" {
		t.Errorf("empty inputs should emit no diff, got:\n%s", got)
	}
}

func TestUnifiedDiffMiddleChangeFormat(t *testing.T) {
	old := lineNums("line", 20)
	newText := strings.Replace(old, "line10\n", "LINE10\n", 1)
	diff := UnifiedDiff(old, newText, 3)
	want := "@@ -7,7 +7,7 @@\n line7\n line8\n line9\n-line10\n+LINE10\n line11\n line12\n line13\n"
	if diff != want {
		t.Errorf("diff =\n%q\nwant\n%q", diff, want)
	}
}

func TestUnifiedDiffInsertionHunkHeader(t *testing.T) {
	diff := UnifiedDiff("a\n", "x\na\n", 3)
	want := "@@ -1 +1,2 @@\n+x\n a\n"
	if diff != want {
		t.Errorf("diff =\n%q\nwant\n%q", diff, want)
	}

	diff = UnifiedDiff("", "a\nb\n", 3)
	want = "@@ -0,0 +1,2 @@\n+a\n+b\n"
	if diff != want {
		t.Errorf("empty-old diff =\n%q\nwant\n%q", diff, want)
	}
}

func TestUnifiedDiffDeletionHunkHeader(t *testing.T) {
	diff := UnifiedDiff("a\nb\nc\n", "a\nc\n", 3)
	want := "@@ -1,3 +1,2 @@\n a\n-b\n c\n"
	if diff != want {
		t.Errorf("diff =\n%q\nwant\n%q", diff, want)
	}
}

func TestUnifiedDiffSplitsDistantHunks(t *testing.T) {
	old := lineNums("line", 40)
	newText := strings.Replace(old, "line5\n", "LINE5\n", 1)
	newText = strings.Replace(newText, "line35\n", "LINE35\n", 1)
	diff := UnifiedDiff(old, newText, 3)
	if got := strings.Count(diff, "@@ -"); got != 2 {
		t.Errorf("expected 2 hunks, got %d:\n%s", got, diff)
	}
	if !strings.Contains(diff, "@@ -2,7 +2,7 @@") || !strings.Contains(diff, "@@ -32,7 +32,7 @@") {
		t.Errorf("hunk headers wrong:\n%s", diff)
	}
}

func TestUnifiedDiffMergesCloseHunks(t *testing.T) {
	old := lineNums("line", 20)
	newText := strings.Replace(old, "line5\n", "LINE5\n", 1)
	newText = strings.Replace(newText, "line8\n", "LINE8\n", 1)
	diff := UnifiedDiff(old, newText, 3)
	if got := strings.Count(diff, "@@ -"); got != 1 {
		t.Errorf("changes 3 lines apart must merge into one hunk, got %d:\n%s", got, diff)
	}
}

func TestUnifiedDiffNoTrailingNewlineHint(t *testing.T) {
	diff := UnifiedDiff("a\nb", "a\nc", 3)
	if !strings.Contains(diff, "\\ No newline at end of file") {
		t.Errorf("missing newline hint expected:\n%s", diff)
	}
}

// TestUnifiedDiffPatchRoundTrip verifies the diff is a correct edit script by
// re-applying it to the old text and checking the result equals the new text.
func TestUnifiedDiffPatchRoundTrip(t *testing.T) {
	long := lineNums("line", 60)
	tests := []struct{ name, old, new string }{
		{"middle change", "a\nb\nc\nd\ne\n", "a\nb\nX\nd\ne\n"},
		{"insert at start", "a\nb\n", "x\na\nb\n"},
		{"insert at end", "a\nb\n", "a\nb\nx\n"},
		{"delete middle", "a\nb\nc\nd\n", "a\nd\n"},
		{"delete all", "a\nb\nc\n", ""},
		{"all new", "", "a\nb\nc\n"},
		{"full replace", "a\nb\nc\n", "x\ny\nz\n"},
		{"multi hunk", "1\n2\n3\n" + long + "99\n", "1\nX\n3\n" + long + "Y\n"},
		{"blank lines", "a\n\nb\n\nc\n", "a\n\nB\n\nc\n"},
		{"duplicate lines", "x\ny\nx\ny\n", "y\nx\ny\nx\n"},
		{"no trailing newline", "a\nb", "a\nc"},
		{"prefix and suffix only", "a\nb\nc\n", "a\nB\nc\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff := UnifiedDiff(tt.old, tt.new, 3)
			if tt.old == tt.new {
				if diff != "" {
					t.Fatalf("identical content must produce no diff, got:\n%s", diff)
				}
				return
			}
			if diff == "" {
				t.Fatalf("diff unexpectedly empty for %q -> %q", tt.old, tt.new)
			}
			applyUnifiedDiff(t, diff, tt.old, tt.new)
		})
	}
}

// applyUnifiedDiff re-applies a unified diff and fails the test unless the
// reconstruction reproduces both sides exactly.
func applyUnifiedDiff(t *testing.T, diff, oldText, newText string) {
	t.Helper()
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	var gotOld, gotNew []string
	oldCursor, newCursor := 0, 0

	lines := splitLines(diff)
	i := 0
	for i < len(lines) {
		header := lines[i]
		if !strings.HasPrefix(header, "@@ -") {
			t.Fatalf("expected hunk header, got %q in:\n%s", header, diff)
		}
		oldStart, oldCount, newStart, newCount := parseHunkHeader(t, header)

		consumedOld := oldStart - 1
		if oldCount == 0 {
			consumedOld = oldStart
		}
		consumedNew := newStart - 1
		if newCount == 0 {
			consumedNew = newStart
		}
		for oldCursor < consumedOld {
			gotOld = append(gotOld, oldLines[oldCursor])
			oldCursor++
		}
		for newCursor < consumedNew {
			gotNew = append(gotNew, newLines[newCursor])
			newCursor++
		}

		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ -") {
			l := lines[i]
			switch {
			case strings.HasPrefix(l, `\`):
				// "\ No newline at end of file" marker.
			case l == "":
				t.Fatalf("unexpected empty diff line in:\n%s", diff)
			case l[0] == ' ':
				gotOld = append(gotOld, l[1:])
				gotNew = append(gotNew, l[1:])
				oldCursor++
				newCursor++
			case l[0] == '-':
				gotOld = append(gotOld, l[1:])
				oldCursor++
			case l[0] == '+':
				gotNew = append(gotNew, l[1:])
				newCursor++
			default:
				t.Fatalf("unrecognized diff line %q in:\n%s", l, diff)
			}
			i++
		}
	}

	for oldCursor < len(oldLines) {
		gotOld = append(gotOld, oldLines[oldCursor])
		oldCursor++
	}
	for newCursor < len(newLines) {
		gotNew = append(gotNew, newLines[newCursor])
		newCursor++
	}

	if strings.Join(gotOld, "\n") != strings.Join(oldLines, "\n") {
		t.Fatalf("reconstructed old text mismatch:\ngot  %q\nwant %q\ndiff:\n%s",
			strings.Join(gotOld, "\n"), strings.Join(oldLines, "\n"), diff)
	}
	if strings.Join(gotNew, "\n") != strings.Join(newLines, "\n") {
		t.Fatalf("reconstructed new text mismatch:\ngot  %q\nwant %q\ndiff:\n%s",
			strings.Join(gotNew, "\n"), strings.Join(newLines, "\n"), diff)
	}
}

func parseHunkHeader(t *testing.T, header string) (oldStart, oldCount, newStart, newCount int) {
	t.Helper()
	parts := strings.Split(header, " ")
	if len(parts) != 4 || parts[0] != "@@" || parts[3] != "@@" {
		t.Fatalf("malformed hunk header %q", header)
	}
	oldStart, oldCount = parseHunkRange(t, strings.TrimPrefix(parts[1], "-"))
	newStart, newCount = parseHunkRange(t, strings.TrimPrefix(parts[2], "+"))
	return oldStart, oldCount, newStart, newCount
}

func parseHunkRange(t *testing.T, spec string) (start, count int) {
	t.Helper()
	if i := strings.IndexByte(spec, ','); i >= 0 {
		start, _ = strconv.Atoi(spec[:i])
		count, _ = strconv.Atoi(spec[i+1:])
		return start, count
	}
	start, _ = strconv.Atoi(spec)
	return start, 1
}

// TestUnifiedDiffDepthFallbackIsValidPatch exercises the documented depth
// ceiling: a fully rewritten file beyond diffDepthLimit degrades to a single
// full-replace hunk, which must still be a valid patch.
func TestUnifiedDiffDepthFallbackIsValidPatch(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&oldB, "old line %d\n", i)
		fmt.Fprintf(&newB, "new line %d\n", i)
	}
	oldText, newText := oldB.String(), newB.String()

	diff := UnifiedDiff(oldText, newText, 3)
	if got := strings.Count(diff, "@@ -"); got != 1 {
		t.Errorf("fallback should emit one hunk, got %d", got)
	}
	applyUnifiedDiff(t, diff, oldText, newText)
}
