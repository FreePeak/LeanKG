package ontology

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

func seedElements(t *testing.T, st *store.Store, els ...store.Element) {
	t.Helper()
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
}

const catalogJSON = `{
	"concepts": [
		{
			"id": "auth",
			"label": "Authentication",
			"aliases": ["login", "auth"],
			"description": "How access is granted"
		},
		{
			"id": "db",
			"label": "Database",
			"aliases": ["store"],
			"description": "Persistence layer"
		}
	]
}`

func writeCatalog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCatalog(t *testing.T) {
	cat, err := LoadCatalog(writeCatalog(t, catalogJSON))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if len(cat.Concepts) != 2 {
		t.Fatalf("concepts: %d, want 2", len(cat.Concepts))
	}
	c := cat.Concepts[0]
	if c.ID != "auth" || c.Label != "Authentication" ||
		!reflect.DeepEqual(c.Aliases, []string{"login", "auth"}) ||
		c.Description != "How access is granted" {
		t.Fatalf("concept[0]: %+v", c)
	}
}

func TestLoadCatalogDuplicateIDs(t *testing.T) {
	path := writeCatalog(t, `{"concepts":[
		{"id": "dup", "label": "One"},
		{"id": "dup", "label": "Two"}
	]}`)
	_, err := LoadCatalog(path)
	if err == nil || !strings.Contains(err.Error(), `duplicate concept id "dup"`) {
		t.Fatalf("err: %v, want duplicate concept id", err)
	}
}

func TestLoadCatalogEmptyID(t *testing.T) {
	path := writeCatalog(t, `{"concepts":[{"id": "", "label": "X"}]}`)
	if _, err := LoadCatalog(path); err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Fatalf("err: %v, want empty id rejection", err)
	}
}

func TestMatchElements(t *testing.T) {
	cat, err := LoadCatalog(writeCatalog(t, catalogJSON))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	st := setupStore(t)
	seedElements(t, st,
		// alias "login" equals the element name -> alias provenance.
		store.Element{QualifiedName: "docs/security.md#login", Name: "Login", FilePath: "docs/security.md"},
		// alias "auth" appears inside the qn ("auth-entication") -> alias
		// outranks the label substring of the same qn.
		store.Element{QualifiedName: "docs/authentication.md#overview", Name: "Overview", FilePath: "docs/authentication.md"},
		// label equals the element name -> name provenance.
		store.Element{QualifiedName: "src/middleware.go#guard", Name: "Authentication", FilePath: "src/middleware.go"},
		// alias "login" (in qn) AND name==label: dedupe keeps alias > name.
		store.Element{QualifiedName: "docs/login-auth.md#api", Name: "Authentication", FilePath: "docs/login-auth.md"},
		// db concept via alias "store" in the qualified name.
		store.Element{QualifiedName: "internal/store/backend.go#Backend", Name: "Backend", FilePath: "internal/store/backend.go"},
		// label "database" appears in the qn, no alias present -> label.
		store.Element{QualifiedName: "docs/relational-database.md#intro", Name: "Intro", FilePath: "docs/relational-database.md"},
		// no match.
		store.Element{QualifiedName: "src/util.go#counter", Name: "Counter", FilePath: "src/util.go"},
	)

	got, err := cat.MatchElements(st)
	if err != nil {
		t.Fatalf("MatchElements: %v", err)
	}
	want := []Match{
		{ConceptID: "auth", QN: "docs/authentication.md#overview", Via: "alias"},
		{ConceptID: "auth", QN: "docs/login-auth.md#api", Via: "alias"},
		{ConceptID: "auth", QN: "docs/security.md#login", Via: "alias"},
		{ConceptID: "auth", QN: "src/middleware.go#guard", Via: "name"},
		{ConceptID: "db", QN: "docs/relational-database.md#intro", Via: "label"},
		{ConceptID: "db", QN: "internal/store/backend.go#Backend", Via: "alias"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestMatchElementsCaseInsensitive(t *testing.T) {
	cat, err := LoadCatalog(writeCatalog(t, `{"concepts":[
		{"id": "c", "label": "cache", "aliases": ["LRU"]}
	]}`))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	st := setupStore(t)
	seedElements(t, st,
		// Uppercase name vs lowercase label -> name.
		store.Element{QualifiedName: "src/a.go#x", Name: "CACHE"},
		// Mixed-case qn vs lowercase alias -> alias.
		store.Element{QualifiedName: "src/Lru-Evict.go#y", Name: "Evict"},
	)
	got, err := cat.MatchElements(st)
	if err != nil {
		t.Fatalf("MatchElements: %v", err)
	}
	want := []Match{
		{ConceptID: "c", QN: "src/Lru-Evict.go#y", Via: "alias"},
		{ConceptID: "c", QN: "src/a.go#x", Via: "name"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("matches:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestMatchElementsEmptyStore(t *testing.T) {
	cat, err := LoadCatalog(writeCatalog(t, catalogJSON))
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	got, err := cat.MatchElements(setupStore(t))
	if err != nil {
		t.Fatalf("MatchElements: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("matches on empty store: %+v, want none", got)
	}
}

func TestMatchRoundTrip(t *testing.T) {
	st := setupStore(t)
	matches := []Match{
		{ConceptID: "auth", QN: "docs/security.md#login", Via: "alias"},
		{ConceptID: "db", QN: "internal/store/backend.go#Backend", Via: "alias"},
	}
	if err := SaveMatches(st, matches); err != nil {
		t.Fatalf("SaveMatches: %v", err)
	}
	got, err := LoadMatches(st)
	if err != nil {
		t.Fatalf("LoadMatches: %v", err)
	}
	if !reflect.DeepEqual(got, matches) {
		t.Fatalf("round-trip:\ngot  %+v\nwant %+v", got, matches)
	}

	// A fresh store has no key: (nil, nil), not an error.
	fresh := setupStore(t)
	if got, err := LoadMatches(fresh); err != nil || got != nil {
		t.Fatalf("LoadMatches on fresh store: (%+v, %v), want (nil, nil)", got, err)
	}
}
