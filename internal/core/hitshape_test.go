package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/FreePeak/LeanKG/internal/store"
)

// A search hit carries a content snippet and the full locating shape at every
// window: language and line_end stay in the shape whatever the limit, because
// the shape is contractual. The 200-byte snippet cut is what keeps hits small.
func TestShapeElementKeepsFullShapeAtEveryWindow(t *testing.T) {
	el := store.Element{
		QualifiedName: "internal/x/y.go::Y", ElementType: "function", Name: "Y",
		FilePath: "internal/x/y.go", LineStart: 3, LineEnd: 40, Language: "go",
		Content: strings.Repeat("a", 300),
	}

	shape := shapeElement(el)
	for _, k := range []string{"language", "line_end", "file_path", "line_start", "qualified_name", "element_type", "name"} {
		if _, ok := shape[k]; !ok {
			t.Errorf("%q must stay in the shape", k)
		}
	}
	if shape["language"] != "go" || shape["line_end"] != 40 {
		t.Errorf("language=%v line_end=%v, want go and 40", shape["language"], shape["line_end"])
	}
	if got := shape["content"].(string); got != strings.Repeat("a", 200)+"…" {
		t.Errorf("content = %d bytes, want the 200-byte snippet plus an ellipsis", len(got))
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
			got := shapeElement(el)["content"].(string)
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

// Every search hit carries language and line_end at any window size. The
// ladder widens its window for test demotion (req.Limit = limit*2), so a limit
// of 3 used to be shaped at 6 and lose those fields; the shape must not depend
// on the window at all.
func TestQueryHitsKeepLanguageAndLineEndAtAnyWindow(t *testing.T) {
	e, _ := newEngine(t)
	var els []store.Element
	for i := 0; i < 12; i++ {
		els = append(els, store.Element{QualifiedName: fmt.Sprintf("pkg/w%02d.go::Widget", i), ElementType: "function",
			Name: "Widget", FilePath: fmt.Sprintf("pkg/w%02d.go", i), LineStart: 3, LineEnd: 9, Language: "go"})
	}
	if err := e.st.UpsertElements(context.Background(), els); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{3, 10} {
		out, err := e.Query(context.Background(), QueryRequest{Query: "Widget", Limit: limit})
		if err != nil {
			t.Fatal(err)
		}
		hits := hitsOf(out)
		if len(hits) != limit {
			t.Fatalf("limit %d: %d hits, want %d", limit, len(hits), limit)
		}
		for _, h := range hits {
			for _, k := range []string{"language", "line_end"} {
				if _, ok := h[k]; !ok {
					t.Errorf("limit %d: hit %v lacks %q", limit, h["qualified_name"], k)
				}
			}
		}
	}
}
