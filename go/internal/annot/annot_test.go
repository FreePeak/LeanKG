package annot

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func newStore(t *testing.T) store.Backend {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func getRecord(t *testing.T, st store.Backend, element string) *Record {
	t.Helper()
	r, err := Get(st, element)
	if err != nil {
		t.Fatalf("get %s: %v", element, err)
	}
	return r
}

func TestAnnotateCreateAndUpdate(t *testing.T) {
	st := newStore(t)

	if r := getRecord(t, st, "pkg.Parse"); r != nil {
		t.Fatalf("Get on unannotated element = %+v; want nil", r)
	}

	story, feature := "US-1", "FR-9"
	created, err := Annotate(st, "pkg.Parse", "parses input", &story, nil)
	if err != nil {
		t.Fatalf("annotate create: %v", err)
	}
	if !created {
		t.Fatal("first Annotate reported created=false; want true")
	}
	want := Record{Element: "pkg.Parse", Description: "parses input", UserStoryID: "US-1"}
	if got := getRecord(t, st, "pkg.Parse"); got == nil || *got != want {
		t.Fatalf("Get after create = %+v; want %+v", got, want)
	}

	created, err = Annotate(st, "pkg.Parse", "parses input strictly", nil, &feature)
	if err != nil {
		t.Fatalf("annotate update: %v", err)
	}
	if created {
		t.Fatal("second Annotate reported created=true; want false")
	}
	// Update replaces the whole row: the story link is cleared, not merged.
	want = Record{Element: "pkg.Parse", Description: "parses input strictly", FeatureID: "FR-9"}
	if got := getRecord(t, st, "pkg.Parse"); got == nil || *got != want {
		t.Fatalf("Get after update = %+v; want %+v", got, want)
	}
}

func TestDelete(t *testing.T) {
	st := newStore(t)

	if err := Put(st, Record{Element: "pkg.A", Description: "a"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := Delete(st, "pkg.A"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if r := getRecord(t, st, "pkg.A"); r != nil {
		t.Fatalf("Get after Delete = %+v; want nil", r)
	}
	if recs, err := All(st); err != nil || len(recs) != 0 {
		t.Fatalf("All after Delete = %v, %v; want empty", recs, err)
	}
	// Deleting an element that never had an annotation is a no-op.
	if err := Delete(st, "pkg.Missing"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestAllSortedAndEmptyStore(t *testing.T) {
	st := newStore(t)

	recs, err := All(st)
	if err != nil {
		t.Fatalf("All on empty store: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("All on empty store = %v; want empty", recs)
	}

	for _, qn := range []string{"pkg.C", "pkg.A", "pkg.B"} {
		if err := Put(st, Record{Element: qn, Description: qn}); err != nil {
			t.Fatalf("put %s: %v", qn, err)
		}
	}
	recs, err = All(st)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var got []string
	for _, r := range recs {
		got = append(got, r.Element)
	}
	if want := []string{"pkg.A", "pkg.B", "pkg.C"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("All order = %v; want %v", got, want)
	}
}

func TestSearch(t *testing.T) {
	st := newStore(t)

	for _, r := range []Record{
		{Element: "auth.Login", Description: "Validates the bearer token"},
		{Element: "auth.Session", Description: "Refreshes a session"},
	} {
		if err := Put(st, r); err != nil {
			t.Fatalf("put %s: %v", r.Element, err)
		}
	}

	check := func(query string, want []string) {
		t.Helper()
		recs, err := Search(st, query)
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		var got []string
		for _, r := range recs {
			got = append(got, r.Element)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Search(%q) = %v; want %v", query, got, want)
		}
	}

	// Case-insensitive, and a substring anywhere in the description.
	check("validates", []string{"auth.Login"})
	check("VALIDATES", []string{"auth.Login"})
	check("bearer", []string{"auth.Login"})
	check("sess", []string{"auth.Session"})
	// Element names are never searched.
	check("auth", nil)
	check("Login", nil)

	if _, err := Search(st, "["); err == nil {
		t.Fatal("Search with an invalid regex returned no error")
	}
}

func TestByUserStoryAndByFeature(t *testing.T) {
	st := newStore(t)

	for _, r := range []Record{
		{Element: "pkg.A", Description: "a", UserStoryID: "US-1"},
		{Element: "pkg.B", Description: "b", UserStoryID: "US-1", FeatureID: "FR-2"},
		{Element: "pkg.C", Description: "c", FeatureID: "FR-2"},
	} {
		if err := Put(st, r); err != nil {
			t.Fatalf("put %s: %v", r.Element, err)
		}
	}

	got, err := ByUserStory(st, "US-1")
	if err != nil {
		t.Fatalf("ByUserStory: %v", err)
	}
	if want := []string{"pkg.A", "pkg.B"}; !reflect.DeepEqual(elements(got), want) {
		t.Fatalf("ByUserStory(US-1) = %v; want %v", elements(got), want)
	}

	got, err = ByFeature(st, "FR-2")
	if err != nil {
		t.Fatalf("ByFeature: %v", err)
	}
	if want := []string{"pkg.B", "pkg.C"}; !reflect.DeepEqual(elements(got), want) {
		t.Fatalf("ByFeature(FR-2) = %v; want %v", elements(got), want)
	}

	got, err = ByUserStory(st, "US-9")
	if err != nil {
		t.Fatalf("ByUserStory miss: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ByUserStory(US-9) = %v; want empty", got)
	}
}

func elements(recs []Record) []string {
	var out []string
	for _, r := range recs {
		out = append(out, r.Element)
	}
	return out
}

func TestLinkCreatesAnnotation(t *testing.T) {
	st := newStore(t)

	if err := Link(st, "pkg.Story", "US-7", "story"); err != nil {
		t.Fatalf("link story: %v", err)
	}
	want := Record{Element: "pkg.Story", Description: "Linked to story US-7", UserStoryID: "US-7"}
	if got := getRecord(t, st, "pkg.Story"); got == nil || *got != want {
		t.Fatalf("linked story = %+v; want %+v", got, want)
	}

	if err := Link(st, "pkg.Feature", "FR-3", "feature"); err != nil {
		t.Fatalf("link feature: %v", err)
	}
	want = Record{Element: "pkg.Feature", Description: "Linked to feature FR-3", FeatureID: "FR-3"}
	if got := getRecord(t, st, "pkg.Feature"); got == nil || *got != want {
		t.Fatalf("linked feature = %+v; want %+v", got, want)
	}
}

func TestLinkAppendsSuffixOnceAndKeepsOtherLink(t *testing.T) {
	st := newStore(t)

	if err := Put(st, Record{Element: "pkg.A", Description: "handles retries", FeatureID: "FR-1"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := Link(st, "pkg.A", "US-2", "story"); err != nil {
		t.Fatalf("link: %v", err)
	}
	want := Record{
		Element:     "pkg.A",
		Description: "handles retries | Linked to story US-2",
		UserStoryID: "US-2",
		FeatureID:   "FR-1", // the unrelated link survives
	}
	if got := getRecord(t, st, "pkg.A"); got == nil || *got != want {
		t.Fatalf("after link = %+v; want %+v", got, want)
	}

	// A description that already starts with "Linked to" is not appended to
	// again — this is the create-then-relink path.
	if err := Link(st, "pkg.Linked", "US-1", "story"); err != nil {
		t.Fatalf("link create: %v", err)
	}
	if err := Link(st, "pkg.Linked", "US-1", "story"); err != nil {
		t.Fatalf("relink: %v", err)
	}
	want = Record{Element: "pkg.Linked", Description: "Linked to story US-1", UserStoryID: "US-1"}
	if got := getRecord(t, st, "pkg.Linked"); got == nil || *got != want {
		t.Fatalf("after relink = %+v; want %+v", got, want)
	}
}

func TestLinkFeatureKindSetsFeatureID(t *testing.T) {
	st := newStore(t)

	if err := Put(st, Record{Element: "pkg.A", Description: "does things", UserStoryID: "US-1"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := Link(st, "pkg.A", "FR-4", "feature"); err != nil {
		t.Fatalf("link: %v", err)
	}
	want := Record{
		Element:     "pkg.A",
		Description: "does things | Linked to feature FR-4",
		UserStoryID: "US-1",
		FeatureID:   "FR-4",
	}
	if got := getRecord(t, st, "pkg.A"); got == nil || *got != want {
		t.Fatalf("after feature link = %+v; want %+v", got, want)
	}

	// Any non-"story" kind is a feature link.
	if err := Link(st, "pkg.B", "EP-5", "epic"); err != nil {
		t.Fatalf("link epic: %v", err)
	}
	got, err := ByFeature(st, "EP-5")
	if err != nil {
		t.Fatalf("ByFeature: %v", err)
	}
	if want := []string{"pkg.B"}; !reflect.DeepEqual(elements(got), want) {
		t.Fatalf("ByFeature(EP-5) = %v; want %v", elements(got), want)
	}
}
