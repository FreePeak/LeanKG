package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestNameTokensAreIdentifierShaped pins the gate that keeps the name arm from
// being a noise machine: a token must look like code, not English.
//
// The arm matches a query token inside a SYMBOL NAME, so a stop-list of common
// words can never be complete — "lock", "stamp", "list" and "run" are all real
// symbols in this repository's own code, and dropping them is exactly the
// failure the arm exists to prevent. The shape test is the filter: a capital, an
// underscore, a dot (Type.Method), a digit, or a later capital inside the token.
// Prose is excluded by shape alone, and `how`/`the`/`where` are excluded by a
// short stop set on top of it.
func TestNameTokensAreIdentifierShaped(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"truncateRunes", []string{"truncateRunes"}},
		{"where is truncateRunes used", []string{"truncateRunes"}},
		{"Store.Space", []string{"Store.Space"}},
		{"fts5 vs trigram", []string{"fts5"}},      // "trigram" is prose: shape rejects it
		{"parseURL and run", []string{"parseURL"}}, // "run" is prose: shape rejects it
		// A bare lowercase English word is rejected even when it IS a real
		// symbol ("lock", "stamp"): firing on it would pull every element with
		// that word in its name into every result, which is worse than missing
		// one. Capitalised and compound forms are the ones that reach an arm.
		{"lock", nil},
		{"stamp", nil},
		{"Stamp", []string{"Stamp"}}, // capitalised single word IS a symbol
		{"RuneCountInString", []string{"RuneCountInString"}},
		// Prose must produce nothing at all: an arm that fires here would put
		// every element whose name contains a word of the sentence in the result.
		{"how do i make the thing go faster please", nil},
		{"where is the store opened for a project", nil},
		{"list the files the indexer would take", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := nameTokens(tc.query)
		if len(got) != len(tc.want) {
			t.Errorf("nameTokens(%q) = %v, want %v", tc.query, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("nameTokens(%q) = %v, want %v", tc.query, got, tc.want)
				break
			}
		}
	}
}

// TestFindByNameTokenRanksBySpecificity pins the arm's own ordering, which is
// NOT relevance: the most specific name first, so `TruncateRunes` outranks a
// `TruncateRunesForTheFormatter` that merely contains it.
func TestFindByNameTokenRanksBySpecificity(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "names.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Element{
		{QualifiedName: "a/very/long/path/truncaterunesfortheformatter.go::TruncateRunesForTheFormatter", Name: "TruncateRunesForTheFormatter", FilePath: "a/very/long/path/x.go", Language: "go"},
		{QualifiedName: "b.go::TruncateRunes", Name: "TruncateRunes", FilePath: "b.go", Language: "go"},
		{QualifiedName: "c.go::unrelated", Name: "unrelated", FilePath: "c.go", Language: "go"},
	} {
		if err := st.UpsertElements([]Element{e}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := st.FindByNameToken("TruncateRunes", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("arm returned %d hits, want 2 (both names contain the token): %+v", len(hits), hits)
	}
	if hits[0].Element.QualifiedName != "b.go::TruncateRunes" {
		t.Errorf("most specific name must rank first, got %q", hits[0].Element.QualifiedName)
	}

	// A prose query is a no-op, not a scan.
	if hits, err := st.FindByNameToken("where is the store opened", 10); err != nil || len(hits) != 0 {
		t.Errorf("prose query must return nothing (got %d hits, err %v)", len(hits), err)
	}
}

// TestFindByNameTokenIsInjectionSafe pins that a query cannot reach SQL: the
// tokens become bound LIKE patterns, so quotes and wildcards are data.
func TestFindByNameTokenIsInjectionSafe(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "inject.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]Element{
		{QualifiedName: "b.go::Widget", Name: "Widget", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`Widget'; DROP TABLE code_elements; --`,
		`%`,
		`_`,
		`Widget" OR 1=1`,
	} {
		hits, err := st.FindByNameToken(q, 10)
		if err != nil {
			t.Errorf("query %q must not error: %v", q, err)
		}
		// A bare wildcard would match everything; a real one matches at most the
		// rows whose name contains it. Either way the table must still be there.
		if _, err := st.ElementCount(); err != nil {
			t.Fatalf("query %q damaged the store: %v", q, err)
		}
		for _, h := range hits {
			if h.Element.QualifiedName != "b.go::Widget" {
				t.Errorf("query %q matched an unexpected element %q", q, h.Element.QualifiedName)
			}
		}
	}
	// The LIKE metacharacters must be escaped, not treated as wildcards: a bare
	// `%` token is not identifier-shaped, so it never even reaches the SQL.
	if toks := nameTokens("%"); len(toks) != 0 {
		t.Errorf("a bare wildcard must not become a name token: %v", toks)
	}
	if toks := nameTokens("a%b"); len(toks) != 0 && strings.Contains(toks[0], "%") {
		t.Errorf("a token carrying a LIKE wildcard must not reach the query: %v", toks)
	}
}
