package store

import (
	"path/filepath"
	"testing"
)

// TestNameTokensAreRareInTheCorpus pins the gate that keeps the name arm from
// being a noise machine. It is a RARITY test, not a shape test, and that
// distinction is the whole finding: agents ask in PROSE ("take the single-flight
// lock for embedding"), so the informative token is a lowercase English word
// (`lock`, `stamp`, `runes`) that a capital/underscore shape test rejects — a
// shape gate let the arm fire on 2 of the 30 labelled questions, a rarity gate
// lets it fire on the ones that matter.
//
// The corollary is that a stop-list of English words can never be right: every
// word it omits is a real symbol name somewhere. Rarity is measured from the
// store, so it adapts to the project.
func TestNameTokensAreRareInTheCorpus(t *testing.T) {
	// A 40-element corpus: the bound is elements/500 with a floor of 2, so a
	// token must sit on at most 2 of them to count. `lock` and `stamp` are
	// unique; `read` and `text` are everywhere; the rest of the question's
	// words are unseen, which counts as rare (that is how "runes" reaches an arm
	// on a corpus that happens not to contain it) and is why the function-word
	// stop list is applied FIRST.
	freq := map[string]int{"lock": 1, "stamp": 1, "read": 12, "text": 9}
	const elements = 40
	cases := []struct {
		query string
		want  []string
	}{
		{"take the single-flight lock for embedding", []string{"take", "single", "flight", "lock", "embedding"}},
		{"compose the model stamp for a provider", []string{"compose", "model", "stamp", "provider"}},
		{"cut a string to n runes", []string{"cut", "string", "runes"}},
		{"truncateRunes", []string{"truncateRunes"}},
		{"Store.Space", []string{"Store.Space"}},
		// Common across the corpus: they discriminate nothing, and a wall of
		// near-identical names is worse than a miss.
		{"where is the text read", nil},
		{"how do i read the text", nil},
		// Too short to be a name, and function words are out before rarity is even
		// consulted.
		{"a b go", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := nameTokens(tc.query, freq, elements)
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
	// The LIKE metacharacters are data, not wildcards: the field splitter drops
	// them, so neither reaches the SQL at all.
	if toks := nameTokens("%", map[string]int{}, 10); len(toks) != 0 {
		t.Errorf("a bare wildcard must not become a name token: %v", toks)
	}
	if toks := nameTokens("a%b", map[string]int{}, 10); len(toks) != 0 {
		t.Errorf("a token carrying a LIKE wildcard must not reach the query: %v", toks)
	}
}
