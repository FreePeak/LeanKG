package core

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/internal/store"
)

// A search hit carries a content snippet and, for a window of more than
// compactHitLimit results, drops language and line_end. The fields a caller
// needs to locate the element (file_path, line_start, qualified_name,
// element_type, name) stay in every shape.
func TestShapeElementCompactsWideWindows(t *testing.T) {
	el := store.Element{
		QualifiedName: "internal/x/y.go::Y", ElementType: "function", Name: "Y",
		FilePath: "internal/x/y.go", LineStart: 3, LineEnd: 40, Language: "go",
		Content: strings.Repeat("a", 300),
	}

	wide := shapeElement(el, 10)
	for _, k := range []string{"language", "line_end"} {
		if _, ok := wide[k]; ok {
			t.Errorf("limit 10: %q must be omitted, got %v", k, wide[k])
		}
	}
	for _, k := range []string{"file_path", "line_start", "qualified_name", "element_type", "name"} {
		if _, ok := wide[k]; !ok {
			t.Errorf("limit 10: %q must stay in the shape", k)
		}
	}
	if got := wide["content"].(string); got != strings.Repeat("a", 200)+"…" {
		t.Errorf("content = %d bytes, want the 200-byte snippet plus an ellipsis", len(got))
	}

	narrow := shapeElement(el, 5)
	if narrow["language"] != "go" || narrow["line_end"] != 40 {
		t.Errorf("limit 5 must keep language and line_end, got language=%v line_end=%v", narrow["language"], narrow["line_end"])
	}
}

// The snippet is cut on a rune boundary: a multi-byte rune that straddles the
// 200-byte cut is dropped whole, so the JSON carries valid UTF-8 and the
// snippet body (without the ellipsis) stays within hitSnippetBytes.
func TestShapeElementSnippetCutsOnARuneBoundary(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"two-byte rune straddles the cut", strings.Repeat("a", 199) + "é" + "tail", strings.Repeat("a", 199) + "…"},
		{"three-byte rune straddles the cut", strings.Repeat("a", 198) + "€" + "tail", strings.Repeat("a", 198) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			el := store.Element{QualifiedName: "internal/x/y.go::Y", ElementType: "function", Name: "Y",
				FilePath: "internal/x/y.go", LineStart: 1, LineEnd: 2, Language: "go", Content: tc.content}
			got := shapeElement(el, 10)["content"].(string)
			if !utf8.ValidString(got) {
				t.Fatalf("content is not valid UTF-8: %q", got)
			}
			if body := strings.TrimSuffix(got, "…"); len(body) > hitSnippetBytes {
				t.Errorf("snippet body = %d bytes, want <= %d", len(body), hitSnippetBytes)
			}
			if got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}
