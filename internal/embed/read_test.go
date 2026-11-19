package embed

import (
	"context"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// elementsStore serves a fixed element list; the rest of Store stays unset.
type elementsStore struct {
	Store
	els []store.Element
}

func (s elementsStore) Elements(context.Context) ([]store.Element, error) { return s.els, nil }

// TestReadElementsSkipsPackageElements: a Go package element (file_path is a
// directory) carries no embeddable text, so no embedding writer or backlog count
// reads it, and the file elements are read as before.
func TestReadElementsSkipsPackageElements(t *testing.T) {
	st := elementsStore{els: []store.Element{
		{QualifiedName: "a.go::Handle", ElementType: "function", Name: "Handle", FilePath: "a.go", Content: "func Handle() {}"},
		{QualifiedName: "a", ElementType: "package", Name: "a", FilePath: "a"},
	}}
	got, err := readElements(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].QN != "a.go::Handle" {
		t.Fatalf("readElements = %+v, want only a.go::Handle", got)
	}
}
