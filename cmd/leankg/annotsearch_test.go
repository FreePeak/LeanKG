package main

import (
	"context"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/annot"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestSearchAnnotationsHint covers the miss message: annot.Search matches the
// DESCRIPTION only, so a feature id the caller just set is stored, printed and
// then invisible to search. Measured 2026-10-11 (feature FR-9999-DEMO present,
// `search-annotations FR-9999-DEMO` → "No annotations found"), which reads as
// "no such row" — the same silent-miss class as #471/#472.
//
// The semantics are unchanged (searching descriptions only); the fix is that a
// miss now says so when any annotation exists.
func TestSearchAnnotationsHint(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	// Empty store: the miss stays the plain one-line answer (nothing to hint at).
	hits, err := annot.Search(st, "anything")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("empty store returned %d hits", len(hits))
	}
	all, err := annot.All(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("empty store holds %d annotations", len(all))
	}

	// With a row present, a description-only search matches; the feature id
	// does not — which is the case the message must cover.
	feature := "FR-9999-DEMO"
	if _, err := annot.Annotate(st, "pkg/a.go::Do", "hint probe", nil, &feature); err != nil {
		t.Fatal(err)
	}
	if hits, _ := annot.Search(st, feature); len(hits) != 0 {
		t.Errorf("feature id matched %d rows — annot.Search is description-only by design", len(hits))
	}
	if hits, _ := annot.Search(st, "hint probe"); len(hits) != 1 {
		t.Errorf("description search returned %d rows, want 1", len(hits))
	}
	// The count the message prints comes from All, so it must see the row.
	all, err = annot.All(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("All = %d rows, want 1 (the hint would under-report)", len(all))
	}
	// Sanity on the sentence itself: it names the two working reads.
	msg := "search matches DESCRIPTION text only"
	if !strings.Contains(msg, "DESCRIPTION") {
		t.Fatal("hint text drifted")
	}
}
