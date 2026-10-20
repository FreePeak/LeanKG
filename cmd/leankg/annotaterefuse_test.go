package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/annot"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestAnnotateRefusesAnElementTheIndexDoesNotHave pins the silent-write class
// at its worst instance.
//
// Measured live on the 298-project self-host 2026-10-11, against a store that
// HAS an index:
//
//	leankg annotate "pkg/does-not-exist.go::Nope" --description ghost
//	  -> Created annotation for 'pkg/does-not-exist.go::Nope'   (exit 0)
//
// and the row really landed — the annotation kv document held it, on an
// element `code_elements` does not contain. An annotation is the one place a
// human writes what the code does NOT say, so one attached to a typo'd or
// renamed identifier is invisible forever: it never surfaces in a query (the
// element is not in the graph), never appears in search-annotations unless
// someone guesses to grep for it, and is never migrated when the element is
// renamed. The knowledge is not merely wrong; it is unreachable.
func TestAnnotateRefusesAnElementTheIndexDoesNotHave(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), dir, store.EngineSQLite, "", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	// A real element in the index.
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "pkg/a.go::Do", ElementType: "function", Name: "Do",
		FilePath: "pkg/a.go", Language: "go", Content: "func Do() {}",
	}}); err != nil {
		t.Fatal(err)
	}

	// 1. A qualified_name the index has is accepted (no false refusal).
	if err := requireIndexedElement(st, "pkg/a.go::Do", "a note about Do"); err != nil {
		t.Fatalf("a real element was refused: %v", err)
	}
	// 2. One it does not have is refused, naming the index as the reason.
	err = requireIndexedElement(st, "pkg/does-not-exist.go::Nope", "a note about Nope")
	if err == nil {
		t.Fatal("an element the index does not have was accepted — the annotation would be unreachable")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does-not-exist.go::Nope") {
		t.Errorf("refusal does not name the element: %v", err)
	}
	if !strings.Contains(msg, "index") {
		t.Errorf("refusal does not name the index as the reason (a typo and a stale index want opposite next steps): %v", err)
	}
	// 3. An empty name is refused too, with the way to find one.
	if err := requireIndexedElement(st, "   ", "a note"); err == nil {
		t.Error("an empty element was accepted")
	}
	// 4. And the refusal happens BEFORE the write: the store stays clean.
	recs, err := annot.All(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Errorf("a refusal wrote something: %+v", recs)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	_ = filepath.Join
}
