//go:build !tstree

package index

import (
	"strings"
	"testing"
)

// TestExtractedContentCarriesTheDocComment pins the one piece of prose an
// author writes to explain a symbol into the stored element.
//
// The defect, found by measuring retrieval on this repository's own store (wave
// 9 of the dogfood loop). `boundContent` slices an element's text from its
// first line to its last, and for Go that first line is the `func`/`type`
// line — so the `//` block an author wrote ABOVE it is not in the store. Not in
// FTS, not in the vector, not in the name arm: 2,596 of 4,175 Go elements
// (62%) have a doc comment the engine never sees.
//
// That is the prose a question is a paraphrase of. Of the 17 labelled questions
// that no arm could reach, 14 share three or more words with their answer's doc
// comment and nine are the doc comment verbatim minus the symbol name
// ("purges orphaned relationship edges" -> `cmdGC`, "returns a hash-seeded
// unit-vector provider" -> `Deterministic`). The text that would have answered
// them was in the repository the whole time, one line above the `func`.
//
// Scored through the engine's own cap and shrink ladder (a 1000-RUNE budget is
// not a token budget, so the sidecar rejects some texts and the engine halves
// them), on the 84 labelled questions, pure vector arm, depth unbounded:
//
//	qn + body (shipped)            top-1 10   top-3 23   top-10 36
//	doc + qn + body                top-1 45   top-3 58   top-10 71
//	qn + doc, no body              top-1 48   top-3 59   top-10 69
//
// The doc comment LEADS because the local sidecar truncates to a 1000-rune
// budget and a leading explanation survives every budget, while a body that
// overruns it is the first thing cut. The body is kept because a label the body
// carried must not stop working: the chosen shape regresses 0 of the 36 the
// shipped shape had in its top 10, and the body-only alternative (qn + doc, no
// body) loses one.
func TestExtractedContentCarriesTheDocComment(t *testing.T) {
	// Tested against the pure function rather than through the Go extractor:
	// the extractor's end-line detection is a separate concern with its own
	// quirks, and a fixture that trips over it tests the wrong thing.
	lines := []string{
		"package p",
		"",
		"// StandaloneDBPath resolves the configured SQLite store path for one",
		"// project directory: LEANKG_DB_PATH, else the nearest leankg.yaml.",
		"func StandaloneDBPath(dir string) string {",
		"\treturn dir",
		"}",
		"",
		"// Bare has only a one-line comment.",
		"func Bare() int { return 1 }",
		"",
		"func Uncommented() int { return 2 }",
		"",
		"// Orphaned is separated from the declaration it must not join.",
		"",
		"func Detached() int { return 3 }",
		"",
		"/* block form",
		"   more block",
		"   end block */",
		"func Blocked() int { return 3 }",
		"",
		"# hashComment is how python/ruby/shell document too.",
		"def hashed(): pass",
	}
	// Indices are DERIVED from the fixture (1-based, as start is), so a case
	// can never quietly point at the wrong line.
	at := func(decl string) int {
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), decl) {
				return i + 1
			}
		}
		t.Fatalf("fixture has no line starting %q", decl)
		return 0
	}
	cases := []struct {
		name string
		decl string
		lead string
	}{
		{"a leading block leads, body intact", "func StandaloneDBPath", "// StandaloneDBPath resolves"},
		{"a one-line comment leads", "func Bare", "// Bare has only a one-line comment."},
		{"no comment: unchanged", "func Uncommented", "func Uncommented() int { return 2 }"},
		{"a blank line ends the block", "func Detached", "func Detached() int { return 3 }"},
		{"a /* … */ block leads", "func Blocked", "/* block form"},
		{"a # comment leads (python/ruby/shell)", "def hashed", "# hashComment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := at(tc.decl)
			els := []indexedElem{{start: start, end: start + 1, parent: -1}}
			boundContent(els, lines)
			got := els[0].content
			if !strings.HasPrefix(got, tc.lead) {
				t.Fatalf("content must start with %q, got:\n%s", tc.lead, got)
			}
			if !strings.Contains(got, tc.decl) {
				t.Fatalf("content must still contain the declaration, got:\n%s", got)
			}
		})
	}
}
