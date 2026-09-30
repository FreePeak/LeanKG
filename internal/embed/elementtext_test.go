package embed

import (
	"strings"
	"testing"
)

// TestEmbeddedTextCarriesTheQualifiedName pins the one thing the embedding
// pipeline must put in a vector for code search to work at all: the element's
// NAME.
//
// Measured, not assumed. Against this repository's own collection with the
// pinned bge-small-en-v1.5 sidecar, the cosine between an intent question and
// `internal/store/pg_fts.go::FuseRRF` was:
//
//	body only                     0.618
//	name + body                   0.672
//	qualified_name + body          0.696   <- highest, on 3 of 3 questions
//	qualified_name + signature     0.702   <- highest on 1 of 3
//
// and a full retrieval bench over 30 labelled questions (docs/
// retrieval-label-set.md) put 16 of them in NEITHER arm's top 30 — a vector
// built from a body alone simply does not know what the symbol is called. The
// keyword arm still finds those by name, which is why the fusion helps, but the
// vector arm contributes nothing for a code symbol, and a question phrased in
// prose ("adopt incremental vacuum in the header") is exactly the case where
// the name is the only bridge.
//
// The qualified_name beats the bare name because it carries the file, which
// disambiguates `FuseRRF` from any prose that happens to use the words, and
// because `file.go::Type.Method` is the identifier an agent actually searches
// for. The name goes FIRST: the local sidecar truncates to a 1000-rune budget,
// and a truncated tail is a truncated body, whereas a leading name survives every
// budget.
//
// The stamp guard does the rest — ChunkerVersion is bumped in the same commit,
// so an existing collection is a rebuild directive rather than a silent mix of
// vectors built from differently-cut text.
func TestEmbeddedTextCarriesTheQualifiedName(t *testing.T) {
	cases := []struct {
		name  string
		qn    string
		body  string
		want  string // must be a prefix: the name survives the truncation budget
		never string // must NOT lead: the body's own text is not the identity
	}{
		{
			name:  "a function",
			qn:    "internal/store/pg_fts.go::FuseRRF",
			body:  "func FuseRRF(lists []RankList) []FusedHit {\n\treturn nil\n}",
			want:  "internal/store/pg_fts.go::FuseRRF",
			never: "",
		},
		{
			name:  "a method",
			qn:    "internal/store/store.go::Store.Open",
			body:  "func (s *Store) Open(path string) (*Store, error) {",
			want:  "internal/store/store.go::Store.Open",
			never: "",
		},
		{
			name:  "an empty body still carries the name",
			qn:    "docs/prd.md::Some Heading",
			body:  "",
			want:  "docs/prd.md::Some Heading",
			never: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := embedText(tc.qn, tc.body)
			if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("embedded text must LEAD with the qualified name (%q), got:\n%s", tc.want, got)
			}
			if tc.body != "" && !strings.Contains(got, strings.TrimSpace(tc.body)) {
				t.Errorf("embedded text must still contain the body, got:\n%s", got)
			}
		})
	}
}

// TestEmbeddedTextNameLeadsAnOverlongBody pins the budget interaction the
// measurement depends on. The cap itself is applied by the caller (Run, after
// this builder) — what belongs to the builder is that the name is at the FRONT,
// so the truncation that follows can only ever cost body text.
func TestEmbeddedTextNameLeadsAnOverlongBody(t *testing.T) {
	qn := "internal/store/pg_fts.go::FuseRRF"
	body := strings.Repeat("x", maxLocalTextChars*2)
	got := embedText(qn, body)
	if !strings.HasPrefix(got, qn) {
		t.Fatalf("an overlong body must never push the name out of the lead: %q", got[:40])
	}
	// And truncating the result to the local cap — what Run does — keeps it.
	if cut := truncateRunes(got, maxLocalTextChars); !strings.HasPrefix(cut, qn) {
		t.Errorf("after the caller's truncation the name must still lead, got %q", cut[:40])
	}
}
